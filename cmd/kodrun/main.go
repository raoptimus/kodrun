package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/kodruninit"
	"github.com/raoptimus/kodrun/internal/mcp"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/runner"
	"github.com/raoptimus/kodrun/internal/snippets"
	"github.com/raoptimus/kodrun/internal/tools"
	"github.com/raoptimus/kodrun/internal/tui"
)

var version = "v1.0.0-beta3"

const (
	cmdNameEdit       = "edit"
	cmdNameCodeReview = "code-review"
	pinnedTierMust    = "must"
	pinnedTierNice    = "nice"
	pathDevNull       = "/dev/null"

	pingTimeout      = 5 * time.Second
	eventChanSize    = 100
	classifyTimeout  = 60 * time.Second
	maxFixAttempts   = 3
	ragBudgetKB      = 24
	pollInterval     = 250 * time.Millisecond
	shutdownTimeout  = 3 * time.Second
	builtinCmdCount  = 3 // number of built-in commands added beyond user commands
	ragBudgetMulti   = 1024
	splitCmdArgParts = 2 // SplitN for "command arg"
	ragWaitTimeout   = 2 * time.Minute
)

var flags struct {
	model     string
	workDir   string
	ollamaURL string
	noTUI     bool
	noFix     bool
	config    string
	verbose   bool
}

// safeGo launches a goroutine tracked by wg with panic recovery.
// On panic, it sends an error event to the events channel (non-blocking).
func safeGo(wg *sync.WaitGroup, events chan agent.Event, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				select {
				case events <- agent.Event{Type: agent.EventError, Message: fmt.Sprintf("background panic: %v", r)}:
				default:
				}
			}
		}()
		fn()
	}()
}

// restoreTerminal disables mouse reporting and exits alt screen via raw ANSI sequences.
// Used as a safety net when bubbletea cannot clean up (panic, SIGTERM, etc.).
func restoreTerminal() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	// Disable mouse reporting modes (1000=basic, 1002=cell motion, 1003=all, 1006=SGR extended)
	fmt.Fprint(os.Stderr, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l")
	// Exit alt screen
	fmt.Fprint(os.Stderr, "\x1b[?1049l")
	// Show cursor
	fmt.Fprint(os.Stderr, "\x1b[?25h")
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			restoreTerminal()
			slog.Error("kodrun panic", "recover", r)
			os.Exit(1)
		}
	}()

	app := &cli.Command{
		Name:    "kodrun",
		Usage:   "CLI agent for Go code",
		Version: version,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "model", Sources: cli.EnvVars("KODRUN_MODEL"), Destination: &flags.model, Usage: "Ollama model (overrides config)"},
			&cli.StringFlag{Name: "work-dir", Value: ".", Sources: cli.EnvVars("KODRUN_WORK_DIR"), Destination: &flags.workDir, Usage: "Working directory"},
			&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("KODRUN_OLLAMA_URL"), Destination: &flags.ollamaURL, Usage: "Ollama API URL (overrides config)"},
			&cli.BoolFlag{Name: "no-tui", Sources: cli.EnvVars("KODRUN_NO_TUI"), Destination: &flags.noTUI, Usage: "Plain stdout mode"},
			&cli.BoolFlag{Name: "no-fix", Destination: &flags.noFix, Usage: "Disable auto-fix"},
			&cli.StringFlag{Name: "config", Destination: &flags.config, Usage: "Config file path"},
			&cli.BoolFlag{Name: "verbose", Destination: &flags.verbose, Usage: "Verbose output"},
		},
		Action:   runRoot,
		Commands: []*cli.Command{buildCmd(), testCmd(), lintCmd(), fixCmd(), initCmd()},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		exitWithCode(1)
	}
}

// exitWithCode exits with the given code. Extracted to avoid exitAfterDefer lint.
func exitWithCode(code int) { os.Exit(code) }

func loadConfig(ctx context.Context) (config.Config, error) {
	cfg, err := config.Load(ctx, flags.config, flags.workDir)
	if err != nil {
		return cfg, err
	}
	if flags.model != "" {
		p := cfg.ChatProvider()
		p.Model = flags.model
		cfg.Providers[cfg.Agent.Provider] = p
	}
	if flags.ollamaURL != "" {
		p := cfg.ChatProvider()
		p.BaseURL = flags.ollamaURL
		cfg.Providers[cfg.Agent.Provider] = p
	}
	return cfg, nil
}

// agentSetup holds the result of setupAgent.
type agentSetup struct {
	ag            *agent.Agent
	reg           *tools.Registry
	loader        *rules.Loader
	snippetLoader *snippets.Loader
	ragIndex      *rag.MultiIndex
	godocIndexer  tools.GoDocIndexer
	langState     *projectlang.State
	mcpMgr        *mcp.Manager
	client        *ollama.Client
}

func setupAgent(ctx context.Context, cfg *config.Config, scope rules.Scope) agentSetup {
	chatProv := cfg.ChatProvider()
	chatClient := ollama.NewClient(chatProv.BaseURL, chatProv.Timeout)

	loader := rules.NewLoader(flags.workDir, cfg.Rules.MaxRefSize)
	if err := loader.Load(ctx); err != nil {
		slog.Warn("rules load failed", "error", err)
	}
	for _, ref := range loader.UnresolvedRefs() {
		slog.Warn("rule references missing file",
			"rule", ref.RulePath,
			"ref", "@"+ref.RefPath,
		)
	}
	snippetLoader := snippets.NewLoader(flags.workDir)
	if err := snippetLoader.Load(ctx); err != nil {
		slog.Warn("snippets load failed", "error", err)
	}

	// Detect project language up front. An override from config wins;
	// otherwise we use the marker-file detector. Empty result means
	// "unknown" — language tools will be added lazily on first Run.
	langState := projectlang.NewState(projectlang.New(flags.workDir), projectlang.Language(cfg.Agent.ProjectLanguage))
	currentLang, _ := langState.EnsureDetected()

	// RAG setup (may use a different provider) — must happen before tool
	// registration so the godoc indexer can be wired into go_doc.
	var ragIndex *rag.MultiIndex
	var godocIndexer tools.GoDocIndexer // nil when RAG is disabled
	if cfg.RAG.Enabled {
		indexPath := cfg.RAG.IndexPath
		if !filepath.IsAbs(indexPath) {
			indexPath = filepath.Join(flags.workDir, indexPath)
		}
		ragProv := cfg.RAGProvider()
		ragClient := ollama.NewClient(ragProv.BaseURL, ragProv.Timeout)
		ragIndex = rag.NewMultiIndex(ragClient, ragProv.Model, indexPath)
		// Legacy per-language sub-indexes (go/python/jsts) are no longer
		// used — RAG indexes only project conventions, which are not
		// partitioned by language. Remove any leftover directories from
		// earlier kodrun versions so stale code chunks cannot survive.
		cleanupLegacyLangDirs(indexPath)
		if err := ragIndex.LoadCommon(); err != nil {
			slog.Warn("RAG common index load failed", "error", err)
		}
		if err := ragIndex.LoadGodoc(); err != nil {
			slog.Warn("RAG godoc index load failed", "error", err)
		}
		godocIndexer = ragIndex.GodocIndexer()
	}

	reg := tools.NewRegistry()
	tools.RegisterAllTools(ctx, reg, flags.workDir, cfg.Tools.ForbiddenPatterns, cfg.Tools.MaxReadLines, loader, snippetLoader, scope, cfg.Rules.UseTool, cfg.Snippets.UseTool, cfg.RAG.Enabled, currentLang, godocIndexer, langState)

	// Register RAG search tools (only when RAG is enabled).
	if ragIndex != nil {
		reg.Register(tools.NewRAGSearchTool(ragIndex, cfg.RAG.TopK))
		// Legacy-code cleanup is deferred to runRoot where the TUI event
		// channel is available, so any warning is visible to the user
		// instead of being buried in stderr logs.
	}

	// Register web_fetch tool (works with and without RAG).
	{
		var webIndexer tools.WebIndexer
		if ragIndex != nil {
			webIndexer = ragIndex.WebIndexer()
		}
		topK := cfg.RAG.TopK
		if topK <= 0 {
			topK = 5
		}
		reg.Register(tools.NewWebFetchTool(webIndexer, topK))
	}

	// MCP servers setup
	var mcpMgr *mcp.Manager
	if len(cfg.MCP) > 0 {
		mcpMgr = mcp.NewManager()
		mcpConfigs := make(map[string]mcp.ServerConfig, len(cfg.MCP))
		for name, sc := range cfg.MCP {
			mcpConfigs[name] = mcp.ServerConfig{
				Command:          sc.Command,
				Args:             sc.Args,
				Env:              sc.Env,
				AutoApprove:      sc.AutoApprove,
				AutoApproveTools: sc.AutoApproveTools,
				ReadOnlyTools:    sc.ReadOnlyTools,
				Disabled:         sc.Disabled,
			}
		}
		if err := mcpMgr.Start(ctx, mcpConfigs, flags.workDir); err != nil {
			slog.Error("MCP start failed", "error", err)
		}
		for _, e := range mcpMgr.Errors() {
			slog.Warn("MCP server warning", "error", e)
		}
		mcpMgr.RegisterTools(reg)
	}

	ag := agent.New(chatClient, chatProv.Model, reg, cfg.Agent.MaxIterations, flags.workDir, chatProv.ContextSize)
	ag.SetLanguageState(langState)
	ag.SetGodocIndexer(godocIndexer)
	ag.SetVerbose(flags.verbose)
	return agentSetup{ag, reg, loader, snippetLoader, ragIndex, godocIndexer, langState, mcpMgr, chatClient}
}

func runRoot(ctx context.Context, cmd *cli.Command) error {
	// Resolve workDir to absolute path so file reads work regardless of cwd
	absWorkDir, err := filepath.Abs(flags.workDir)
	if err != nil {
		return errors.WithMessage(err, "resolve work-dir")
	}
	flags.workDir = absWorkDir

	args := cmd.Args().Slice()

	cfg, err := loadConfig(ctx)
	if err != nil {
		return errors.WithMessage(err, "load config")
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s := setupAgent(ctx, &cfg, rules.ScopeCoding)
	ag, reg, loader, snippetLoader := s.ag, s.reg, s.loader, s.snippetLoader
	ragIndex, godocIndexer, langState := s.ragIndex, s.godocIndexer, s.langState
	mcpMgr, client := s.mcpMgr, s.client
	if mcpMgr != nil {
		defer mcpMgr.Close()
		ag.AddConfirmTools(mcpMgr.ConfirmTools())
		ag.AddReadOnlyTools(mcpMgr.ReadOnlyTools())
	}

	// Set default mode and think from config
	defaultMode := agent.ModePlan
	if cfg.Agent.DefaultMode == cmdNameEdit {
		defaultMode = agent.ModeEdit
	}
	ag.SetMode(defaultMode)
	ag.SetThink(cfg.Agent.Think)
	ag.SetLanguage(cfg.Agent.Language)
	ag.SetAutoCompact(cfg.Agent.AutoCompact)
	ag.SetMaxToolWorkers(cfg.Agent.MaxToolWorkers)
	ag.SetHasSnippets(cfg.Snippets.UseTool && !cfg.RAG.Enabled)
	ag.SetHasRAG(cfg.RAG.Enabled)
	ag.SetRAGIndex(ragIndex)

	// Check Ollama connectivity
	chatProv := cfg.ChatProvider()
	pingCtx, pingCancel := context.WithTimeout(ctx, pingTimeout)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		return errors.WithMessage(err, "cannot connect to Ollama\nMake sure 'ollama serve' is running")
	}

	ruleCatalog := loader.RuleCatalogString(ctx, rules.ScopeCoding, cfg.Rules.UseTool)

	// One-shot task from args — always use edit mode
	if len(args) > 0 {
		ag.SetMode(agent.ModeEdit)
		task := strings.Join(args, " ")
		output := agent.NewPlainOutput(os.Stdout)
		ag.SetEventHandler(output.Handle)
		return ag.Run(ctx, task, ruleCatalog)
	}

	// Interactive mode
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if !isTTY || flags.noTUI {
		// Plain stdin mode — always use edit mode
		ag.SetMode(agent.ModeEdit)
		output := agent.NewPlainOutput(os.Stdout)
		ag.SetEventHandler(output.Handle)

		stdinData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return errors.WithMessage(err, "read stdin")
		}
		task := strings.TrimSpace(string(stdinData))
		if task == "" {
			return errors.New("no task provided")
		}
		return ag.Run(ctx, task, ruleCatalog)
	}

	// TUI mode
	events := make(chan agent.Event, eventChanSize)
	// emit sends an event without blocking if context is cancelled or channel is full.
	emit := func(e *agent.Event) {
		select {
		case events <- *e:
		case <-ctx.Done():
		}
	}
	ag.SetEventHandler(emit)

	// Enable session persistence
	sessionsDir := filepath.Join(flags.workDir, ".kodrun", "sessions")
	ag.SetSessionDir(sessionsDir)

	// Initialize agent once — history persists across Send() calls
	ag.Init(ruleCatalog)

	// Background task tracking for graceful shutdown.
	var bgWg sync.WaitGroup
	var indexing atomic.Bool

	// One-shot migration: if the loaded common index still contains chunks
	// that point at real source files (leftover from a kodrun version that
	// indexed project code), wipe it so the background re-indexer rebuilds
	// the conventions-only corpus from scratch. Runs before background
	// indexing so users see both messages in order in the TUI.
	if ragIndex != nil && ragIndex.HasLegacyCodeChunks() {
		emit(&agent.Event{Type: agent.EventAgent, Message: "RAG: dropping legacy code chunks from common index"})
		if err := ragIndex.Reset(); err != nil {
			emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG reset failed: %s", err)})
		}
	}

	// Background RAG indexing
	if ragIndex != nil {
		indexing.Store(true)
		safeGo(&bgWg, events, func() {
			defer indexing.Store(false)
			defer func() {
				emit(&agent.Event{Type: agent.EventRAGProgress})
			}()
			// RAG indexes only project conventions — rules, snippets, docs,
			// embedded language standards. Source code is NOT indexed: it
			// changes too fast, chunked snapshots go stale between reindexes,
			// and reviewers end up citing code that no longer exists. The diff
			// plus live `read_file` tool calls are the authoritative view of
			// code; RAG is reserved for stable conventions.
			snips := snippetLoader.Snippets()
			var chunks []rag.Chunk
			// Index snippets into RAG
			for i := range snips {
				chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
					Name:        snips[i].Name,
					Description: snips[i].Description,
					Tags:        snips[i].Tags,
					Content:     snips[i].Content,
					SourcePath:  snips[i].SourcePath,
				}})...)
			}
			// Index rules into RAG
			for _, r := range loader.AllRules() {
				chunks = append(chunks, rag.ChunkRules([]rag.RuleInfo{{
					Name:    filepath.Base(r.Path),
					Content: r.Content,
					Path:    r.Path,
				}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
			}
			// Index reference docs into RAG
			for path, content := range loader.ReferenceDocPaths() {
				chunks = append(chunks, rag.ChunkRefDocs([]rag.RefDocInfo{{
					Path:    path,
					Content: content,
				}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
			}
			// Index built-in language reference docs (e.g. Effective Go) for
			// the detected project language.
			chunks = append(chunks, rag.ChunkEmbeddedDocs(string(langState.Current()), cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
			progressFn := func(label string, done, total int) {
				emit(&agent.Event{
					Type:          agent.EventRAGProgress,
					ProgressDone:  done,
					ProgressTotal: total,
					ProgressLabel: label,
				})
			}
			// All indexed chunks are synthetic (rules://, snippets://,
			// embedded://) or live under .kodrun/docs — they all route to the
			// common sub-index. Language sub-indexes are intentionally left
			// empty now that project source code is no longer indexed.
			n, err := ragIndex.BuildCommonWithProgress(ctx, chunks, progressFn)
			if err != nil {
				emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG index (common): %s", err)})
				return
			}
			if err := ragIndex.Save(); err != nil {
				emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG save: %s", err)})
				return
			}
			// Only announce when the index actually changed. A silent
			// exit on a fully-cached startup avoids giving the impression
			// of a reindex that never happened.
			if n > 0 {
				emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG ready: %d new, %d total", n, ragIndex.Size())})
			}
		})
	}

	// Confirmation channel for destructive operations
	confirmCh := make(chan tui.ConfirmRequest, 1)
	ag.SetConfirmFunc(func(payload agent.ConfirmPayload) agent.ConfirmResult {
		resultCh := make(chan agent.ConfirmResult, 1)
		confirmCh <- tui.ConfirmRequest{
			Payload: payload,
			Result:  resultCh,
		}
		return <-resultCh
	})

	// Plan confirmation channel (3-option dialog for orchestrator)
	planConfirmCh := make(chan tui.PlanConfirmRequest, 1)
	planConfirmFn := func(plan string) agent.PlanConfirmResult {
		resultCh := make(chan agent.PlanConfirmResult, 1)
		planConfirmCh <- tui.PlanConfirmRequest{
			Plan:   plan,
			Result: resultCh,
		}
		return <-resultCh
	}

	// Step confirmation channel (per-step confirm in DAG executor)
	stepConfirmCh := make(chan tui.StepConfirmRequest, 1)
	stepConfirmFn := func(description string) agent.StepConfirmAction {
		resultCh := make(chan agent.StepConfirmAction, 1)
		stepConfirmCh <- tui.StepConfirmRequest{
			Description: description,
			Result:      resultCh,
		}
		return <-resultCh
	}

	// Per-task cancelable context
	var (
		taskCancelMu sync.Mutex
		taskCancelFn context.CancelFunc
	)

	cancelTask := func() {
		taskCancelMu.Lock()
		if taskCancelFn != nil {
			taskCancelFn()
		}
		taskCancelMu.Unlock()
	}

	taskFn := func(input string) {
		// Create per-task cancelable context
		taskCtx, taskCancel := context.WithCancel(ctx)
		taskCancelMu.Lock()
		taskCancelFn = taskCancel
		taskCancelMu.Unlock()
		defer func() {
			taskCancel()
			taskCancelMu.Lock()
			taskCancelFn = nil
			taskCancelMu.Unlock()
		}()

		task := input
		if strings.HasPrefix(input, "/") {
			parts := strings.SplitN(input, " ", splitCmdArgParts)
			cmdName := strings.TrimPrefix(parts[0], "/")

			// Built-in commands
			switch cmdName {
			case "compact":
				var instructions string
				if len(parts) > 1 {
					instructions = parts[1]
				}
				emit(&agent.Event{Type: agent.EventAgent, Message: "Compacting context..."})
				if err := ag.Compact(taskCtx, instructions); err != nil {
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case cmdNameEdit:
				if ag.LastPlan() != "" {
					ag.EnterEditWithPlan()
					emit(&agent.Event{Type: agent.EventAgent, Message: "Loaded approved plan. Send any message to start execution."})
				} else {
					ag.SetMode(agent.ModeEdit)
					ag.SetThink(false)
					emit(&agent.Event{Type: agent.EventAgent, Message: "No plan available. Switched to edit mode."})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "clear":
				ag.ClearHistory()
				ag.ClearSessionPermissions()
				emit(&agent.Event{Type: agent.EventAgent, Message: "Context and permissions cleared"})
				used, total := ag.ContextUsage()
				emit(&agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "diff":
				emit(&agent.Event{Type: agent.EventAgent, Message: "Computing diff..."})
				var diffArgs []string
				if len(parts) > 1 {
					diffArgs = strings.Fields(parts[1])
				}
				diffOutput, err := gitDiff(ctx, flags.workDir, diffArgs)
				switch {
				case err != nil:
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
				case diffOutput == "":
					emit(&agent.Event{Type: agent.EventAgent, Message: "No uncommitted changes."})
				default:
					emit(&agent.Event{Type: agent.EventAgent, Message: diffOutput})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "resume":
				s, err := agent.LatestSession(sessionsDir)
				if err != nil {
					emit(&agent.Event{Type: agent.EventAgent, Message: "No sessions to resume."})
				} else {
					ag.LoadFromSession(s)
					emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Resumed session %s (%d messages, %s mode)", s.ID, len(s.Messages), s.Mode)})
					used, total := ag.ContextUsage()
					emit(&agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "sessions":
				summaries, err := agent.ListSessions(sessionsDir)
				if err != nil || len(summaries) == 0 {
					emit(&agent.Event{Type: agent.EventAgent, Message: "No saved sessions."})
				} else {
					var sb strings.Builder
					sb.WriteString("Saved sessions:\n")
					for _, s := range summaries {
						fmt.Fprintf(&sb, "  %s  %s  %s  %d msgs\n", s.ID, s.Model, s.Mode, s.MessageCount)
					}
					emit(&agent.Event{Type: agent.EventAgent, Message: sb.String()})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "reindex":
				switch {
				case ragIndex == nil:
					emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
					emit(&agent.Event{Type: agent.EventDone})
				case !indexing.CompareAndSwap(false, true):
					emit(&agent.Event{Type: agent.EventAgent, Message: "RAG indexing already in progress"})
					emit(&agent.Event{Type: agent.EventDone})
				default:
					emit(&agent.Event{Type: agent.EventAgent, Message: "Reindexing..."})
					safeGo(&bgWg, events, func() {
						defer indexing.Store(false)
						defer func() {
							// Always clear the progress indicator on exit so the
							// status bar disappears even on error paths.
							emit(&agent.Event{Type: agent.EventRAGProgress})
						}()
						// Re-read rules and snippets from disk before rebuilding
						// the index. Without this, /reindex would use whatever the
						// loaders cached at process start, missing any rule or
						// @-referenced doc the user edited during the session.
						// Fail fast on loader errors: silently continuing would
						// rebuild the index with stale rules/snippets, which is
						// exactly what reindex is meant to prevent.
						if err := loader.Load(ctx); err != nil {
							emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("rules reload: %s", err)})
							emit(&agent.Event{Type: agent.EventDone})
							return
						}
						if err := snippetLoader.Load(ctx); err != nil {
							emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("snippets reload: %s", err)})
							emit(&agent.Event{Type: agent.EventDone})
							return
						}
						// Drop any leftover per-language sub-index directories from
						// earlier kodrun versions before rebuilding. Idempotent.
						cleanupLegacyLangDirs(ragIndex.BasePath())
						// /reindex is an explicit request to rebuild from scratch.
						// Wipe the in-memory index and the on-disk file so no stale
						// entries (e.g. source-code chunks from older kodrun builds,
						// or rules removed since the last run) can leak through
						// hash-based skipping in BuildWithProgress. The trade-off is
						// that every /reindex re-embeds every chunk, which is fine
						// for the small convention-only corpus.
						if err := ragIndex.Reset(); err != nil {
							emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("reset: %s", err)})
							emit(&agent.Event{Type: agent.EventDone})
							return
						}
						// RAG only indexes conventions (rules/snippets/docs/embedded).
						// Project source code is intentionally excluded — see the
						// initial indexing block above for the rationale.
						reloadedSnips := snippetLoader.Snippets()
						var chunks []rag.Chunk
						// Index snippets into RAG (parity with initial indexing pipeline).
						for i := range reloadedSnips {
							chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
								Name:        reloadedSnips[i].Name,
								Description: reloadedSnips[i].Description,
								Tags:        reloadedSnips[i].Tags,
								Content:     reloadedSnips[i].Content,
								SourcePath:  reloadedSnips[i].SourcePath,
							}})...)
						}
						// Index rules into RAG
						for _, r := range loader.AllRules() {
							chunks = append(chunks, rag.ChunkRules([]rag.RuleInfo{{
								Name:    filepath.Base(r.Path),
								Content: r.Content,
								Path:    r.Path,
							}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
						}
						// Index reference docs into RAG
						for path, content := range loader.ReferenceDocPaths() {
							chunks = append(chunks, rag.ChunkRefDocs([]rag.RefDocInfo{{
								Path:    path,
								Content: content,
							}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
						}
						// Built-in language reference docs (Effective Go etc.) for the
						// detected project language. Routed to common via embedded:// prefix.
						chunks = append(chunks, rag.ChunkEmbeddedDocs(string(langState.Current()), cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
						progressFn := func(label string, done, total int) {
							emit(&agent.Event{
								Type:          agent.EventRAGProgress,
								ProgressDone:  done,
								ProgressTotal: total,
								ProgressLabel: label,
							})
						}
						// Everything routes to common — source code is no longer indexed.
						n, err := ragIndex.BuildCommonWithProgress(ctx, chunks, progressFn)
						if err != nil {
							emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index (common): %s", err)})
							emit(&agent.Event{Type: agent.EventDone})
							return
						}
						if err := ragIndex.Save(); err != nil {
							emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("save: %s", err)})
							emit(&agent.Event{Type: agent.EventDone})
							return
						}
						emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG reindex: %d new, %d total", n, ragIndex.Size())})
						emit(&agent.Event{Type: agent.EventDone})
					})
				}
				return
			case "rag":
				if ragIndex == nil {
					emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable with rag.enabled: true"})
				} else {
					emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG: %d entries, model: %s, updated: %s",
						ragIndex.Size(), cfg.RAGProvider().Model, ragIndex.Updated().Format(time.RFC3339))})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case "add_doc":
				if ragIndex == nil {
					emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
					emit(&agent.Event{Type: agent.EventDone})
					return
				}
				var docPath string
				if len(parts) > 1 {
					docPath = strings.TrimSpace(parts[1])
				}
				if docPath == "" {
					emit(&agent.Event{Type: agent.EventAgent, Message: "Usage: /add_doc <file_path>"})
					emit(&agent.Event{Type: agent.EventDone})
					return
				}
				if !filepath.IsAbs(docPath) {
					docPath = filepath.Join(flags.workDir, docPath)
				}
				emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Adding %s to RAG index...", docPath)})
				safeGo(&bgWg, events, func() {
					chunks, err := rag.ChunkFile(docPath, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)
					if err != nil {
						emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("read: %s", err)})
						emit(&agent.Event{Type: agent.EventDone})
						return
					}
					// Use relative path for chunk file paths
					if rel, e := filepath.Rel(flags.workDir, docPath); e == nil {
						for i := range chunks {
							chunks[i].FilePath = rel
						}
					}
					n, err := ragIndex.Build(ctx, chunks)
					if err != nil {
						emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index: %s", err)})
						emit(&agent.Event{Type: agent.EventDone})
						return
					}
					if err := ragIndex.Save(); err != nil {
						emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("save: %s", err)})
						emit(&agent.Event{Type: agent.EventDone})
						return
					}
					emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Added %d chunks from %s (%d total)", n, filepath.Base(docPath), ragIndex.Size())})
					emit(&agent.Event{Type: agent.EventDone})
				})
				return
			case "init":
				emit(&agent.Event{Type: agent.EventAgent, Message: "Scanning project and generating AGENTS.md..."})
				res, err := kodruninit.Run(taskCtx, flags.workDir, client, chatProv.Model)
				if err != nil {
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
				} else {
					for _, path := range res.Created {
						emit(&agent.Event{Type: agent.EventAgent, Message: "created " + path})
					}
					emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Done: %d items created", len(res.Created))})
				}
				emit(&agent.Event{Type: agent.EventDone})
				return
			case cmdNameCodeReview:
				if indexing.Load() {
					emit(&agent.Event{Type: agent.EventAgent, Message: "Waiting for RAG indexing to finish before review..."})
					waitForRAGReady(taskCtx, &indexing, ragWaitTimeout)
				}
				var diffArgs []string
				var packageScope string
				if len(parts) > 1 {
					rawArgs := strings.Fields(parts[1])
					// Peel off `--package <path>` (or `--package=<path>`).
					for i := 0; i < len(rawArgs); i++ {
						a := rawArgs[i]
						if a == "--package" && i+1 < len(rawArgs) {
							packageScope = strings.TrimSuffix(rawArgs[i+1], "/")
							i++
							continue
						}
						if strings.HasPrefix(a, "--package=") {
							packageScope = strings.TrimSuffix(strings.TrimPrefix(a, "--package="), "/")
							continue
						}
						diffArgs = append(diffArgs, a)
					}
				}
				if packageScope != "" {
					emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Collecting diff for code review (scope: %s)...", packageScope)})
				} else {
					emit(&agent.Event{Type: agent.EventAgent, Message: "Collecting diff for code review..."})
				}
				diffOut, err := gitDiff(ctx, flags.workDir, diffArgs)
				if err != nil {
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
					emit(&agent.Event{Type: agent.EventDone})
					return
				}
				if packageScope != "" {
					diffOut = filterDiffByPackage(diffOut, packageScope)
				}
				if strings.TrimSpace(diffOut) == "" {
					if packageScope != "" {
						emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("No changes to review under %q.", packageScope)})
					} else {
						emit(&agent.Event{Type: agent.EventAgent, Message: "No changes to review."})
					}
					emit(&agent.Event{Type: agent.EventDone})
					return
				}
				lang := langState.Current()
				// Fan-out specialists must NOT receive the monolithic single-
				// reviewer prompt — it conflicts with their narrow focus and
				// strict output format (each specialist has its own system
				// prompt with a read_file gate). Use a thin task for fan-out,
				// and the full monolithic prompt only for the single-reviewer
				// fallback.
				fanout := cfg.Agent.Orchestrator
				if fanout {
					// Fan-out specialists MUST call read_file on every
					// changed file per their system-prompt workflow. Sending
					// full diff content is unnecessary and harmful: it bloats
					// the task, causes timeouts on big diffs, and tempts
					// models to skip read_file. Instead we send only the stat
					// summary (which files changed and by how many lines) and
					// the filtered list of source-code files.
					rawDiff, rerr := gitDiffRaw(ctx, flags.workDir, diffArgs)
					var sourceFiles []string
					if rerr == nil && rawDiff != "" {
						if packageScope != "" {
							rawDiff = filterDiffByPackage(rawDiff, packageScope)
						}
						filtered := filterDiffToSourceCode(rawDiff)
						sourceFiles = changedFilesFromDiff(filtered)
					}
					if len(sourceFiles) == 0 {
						// Fallback: no source files after filter — use full file list.
						sourceFiles = changedFilesFromDiff(diffOut)
					}
					stat := gitStatRaw(ctx, flags.workDir, diffArgs)
					task = buildFanoutReviewTask(stat, sourceFiles)
					// Skip RAG prefetch for fan-out specialists. The prefetch
					// block dumps 500+ lines of project conventions (often from
					// a PINNED overview that targets a different canonical
					// project) into every specialist's task, drowning the
					// actual diff and the read_file instruction. Specialists
					// can still call `search_docs` themselves if they need a
					// convention lookup.
				} else {
					task = buildCodeReviewPrompt(lang, diffOut, &cfg)
					// Pre-fetch RAG context for the single-reviewer fallback.
					// Local models (qwen, etc.) often ignore "MUST call
					// search_docs" hints in the system prompt, so the prefetch
					// guarantees conventions reach the reviewer regardless of
					// model behaviour.
					if ragIndex != nil && cfg.RAG.Enabled {
						if pre := buildCodeReviewRAGPrefetch(taskCtx, ragIndex, diffOut, cfg.RAG.TopK, snippetLoader.Snippets(), cfg.RAG.ReviewBudgetBytes); pre != "" {
							task = pre + "\n" + task
						}
					}
				}
				// fall through to the standard agent pipeline
			}

			// Parallel specialist reviewer path. When max_parallel_tasks > 1,
			// /code-review is handled by a dedicated orchestrator method that
			// fans out one focused sub-agent per review axis (rules, idiomatic,
			// best-practice, security, structure, architecture) and merges their
			// findings via the extractor into a single plan. Architecture
			// review is one of these specialists — there is no separate
			// /arch-review command.
			if cfg.Agent.Orchestrator && cmdName == cmdNameCodeReview {
				roles := agent.SpecialistReviewerRoles
				var doneSent atomic.Bool
				wrappedEmit := agent.EventHandler(func(e *agent.Event) {
					if e.Type == agent.EventDone {
						doneSent.Store(true)
					}
					emit(e)
				})
				orch := newOrchestrator(client, chatProv, reg, &cfg, wrappedEmit, ag.GetConfirmFunc(), planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader)
				if err := orch.RunCodeReview(taskCtx, task, roles); err != nil && taskCtx.Err() == nil {
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
				}
				if !doneSent.Load() {
					emit(&agent.Event{Type: agent.EventDone})
				}
				return
			}

			if cmdName == "orchestrate" {
				orchTask := task
				if len(parts) > 1 {
					orchTask = parts[1]
				}
				orch := newOrchestrator(client, chatProv, reg, &cfg, emit, ag.GetConfirmFunc(), planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader)
				err := orch.Run(taskCtx, orchTask)
				if err != nil && taskCtx.Err() == nil {
					emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
					emit(&agent.Event{Type: agent.EventDone})
				}
				return
			}

			if cmd, ok := loader.GetCommand(cmdName); ok {
				task = cmd.Template
				if len(parts) > 1 {
					task = strings.ReplaceAll(task, "{{arg}}", strings.TrimSpace(parts[1]))
				}
			}
		}

		// Use orchestrator only in plan mode (not in edit mode).
		if cfg.Agent.Orchestrator && ag.Mode() == agent.ModePlan {
			var doneSent atomic.Bool
			wrappedEmit := agent.EventHandler(func(e *agent.Event) {
				if e.Type == agent.EventDone {
					doneSent.Store(true)
				}
				emit(e)
			})
			orch := newOrchestrator(client, chatProv, reg, &cfg, wrappedEmit, ag.GetConfirmFunc(), planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader)
			if err := orch.Run(taskCtx, task); err != nil && taskCtx.Err() == nil {
				emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
			}
			// Ensure EventDone is always sent so the TUI stops the timer.
			if !doneSent.Load() {
				emit(&agent.Event{Type: agent.EventDone})
			}
			return
		}

		err := ag.Send(taskCtx, task)
		if err != nil {
			// Send() emits EventDone on success, but not on error.
			// Emit error + done so timer stops and running resets.
			if taskCtx.Err() != nil {
				// Context was cancelled (Esc pressed) — don't show cryptic error
				emit(&agent.Event{Type: agent.EventDone})
			} else {
				emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
				emit(&agent.Event{Type: agent.EventDone})
			}
			return
		}

		if ag.Mode() == agent.ModePlan && ag.LastPlan() != "" {
			// In standalone plan mode, show the extracted plan.
			emit(&agent.Event{Type: agent.EventAgent, Message: ag.LastPlan()})
		}

		// Classifier path: only when orchestrator is OFF and we are in ModePlan.
		// When the orchestrator is enabled, ModePlan is fully handled by it
		// (plan-confirm dialog included), so the classifier is not needed.
		if cfg.Agent.Orchestrator || ag.Mode() != agent.ModePlan {
			return
		}

		responseText := ag.LastPlan()
		if strings.TrimSpace(responseText) == "" {
			return
		}

		// Run the classifier in foreground but with a hard timeout, so the
		// dialog appears (or not) before the user sends the next message.
		thinkProv := cfg.ThinkingProvider()
		thinkClient := client
		thinkModel := chatProv.Model
		if cfg.Agent.ThinkingProvider != "" && cfg.Agent.ThinkingProvider != cfg.Agent.Provider {
			thinkClient = ollama.NewClient(thinkProv.BaseURL, thinkProv.Timeout)
			thinkModel = thinkProv.Model
		}

		verdict, classifyErr := agent.ClassifyResponse(
			taskCtx, thinkClient, thinkModel, cfg.Agent.Language, task, responseText, classifyTimeout,
		)
		if classifyErr != nil {
			slog.Debug("classifier failed", "err", classifyErr)
		}

		if verdict.SuggestedAction != agent.ClassifyActionApprovePlan {
			return
		}

		// Append CTA only if the agent did not already include one.
		if verdict.CTAText != "" && !strings.Contains(responseText, verdict.CTAText) {
			emit(&agent.Event{Type: agent.EventAgent, Message: verdict.CTAText})
		}

		// Show the plan-confirm dialog.
		cr := planConfirmFn(responseText)
		switch cr.Action {
		case agent.PlanDeny:
			emit(&agent.Event{Type: agent.EventAgent, Message: "Execution cancelled by user."})
			return
		case agent.PlanAugment:
			// Re-route augment text as a new task in the next turn.
			emit(&agent.Event{Type: agent.EventAgent, Message: "Plan augmentation: send your refinement as a new message."})
			return
		case agent.PlanAutoAccept, agent.PlanManualApprove:
			var confirmFn agent.ConfirmFunc
			if cr.Action == agent.PlanManualApprove {
				confirmFn = ag.GetConfirmFunc()
			}
			emit(&agent.Event{Type: agent.EventAgent, Message: "▸ Executing approved plan..."})
			orch := newOrchestrator(client, chatProv, reg, &cfg, emit, confirmFn, planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader)
			if err := orch.RunExecutor(taskCtx, responseText, confirmFn); err != nil && taskCtx.Err() == nil {
				emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
			}
		}
	}

	setModeFn := func(mode agent.Mode, think bool) {
		if mode == agent.ModeEdit && ag.Mode() == agent.ModePlan && ag.LastPlan() != "" {
			ag.EnterEditWithPlan()
		} else {
			ag.SetMode(mode)
			ag.SetThink(think)
		}
	}

	contextFn := func() string {
		return formatContext(ag.History())
	}

	commands := buildCommandItems(loader)
	model := tui.NewModel(chatProv.Model, version, chatProv.ContextSize, taskFn, cancelTask, events, commands, confirmCh, planConfirmCh, stepConfirmCh,
		flags.workDir, defaultMode, cfg.Agent.Think, setModeFn, contextFn, cfg.Agent.Language, cfg.TUI.MaxHistory)

	// Save terminal state before bubbletea modifies it (alt screen, mouse reporting).
	// Deferred restore acts as safety net if bubbletea cannot clean up.
	if oldState, err := term.GetState(int(os.Stdin.Fd())); err == nil {
		defer func() {
			if restoreErr := term.Restore(int(os.Stdin.Fd()), oldState); restoreErr != nil {
				slog.Warn("kodrun: failed to restore terminal state", "error", restoreErr)
			}
		}()
	}

	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return err
	}

	// Cancel context and wait for background goroutines with timeout.
	cancel()

	// Close MCP servers early (before waiting) so their processes don't linger.
	if mcpMgr != nil {
		mcpMgr.Close()
	}

	waitDone := make(chan struct{})
	go func() {
		bgWg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(shutdownTimeout):
		slog.Warn("kodrun: shutdown timeout, forcing exit")
	}

	close(events)

	return nil
}

func buildCommandItems(loader *rules.Loader) []tui.CommandItem {
	cmds := loader.Commands()
	items := make([]tui.CommandItem, 0, len(cmds)+builtinCmdCount)

	// Built-in commands
	items = append(items,
		tui.CommandItem{Name: "compact", Description: "Summarize conversation to free context"},
		tui.CommandItem{Name: cmdNameEdit, Description: "Switch to edit mode (with plan if available)"},
		tui.CommandItem{Name: "init", Description: "Create .kodrun/ starter structure"},
		tui.CommandItem{Name: "clear", Description: "Clear conversation context"},
		tui.CommandItem{Name: "diff", Description: "Show git diff (uncommitted changes)"},
		tui.CommandItem{Name: "resume", Description: "Resume last saved session"},
		tui.CommandItem{Name: "sessions", Description: "List saved sessions"},
		tui.CommandItem{Name: "reindex", Description: "Rebuild RAG index"},
		tui.CommandItem{Name: "rag", Description: "Show RAG index status"},
		tui.CommandItem{Name: "add_doc", Description: "Add a document to RAG index"},
		tui.CommandItem{Name: "orchestrate", Description: "Run Plan→Execute→Review pipeline"},
		tui.CommandItem{Name: cmdNameCodeReview, Description: "Parallel specialist code review: rules, idiomaticity, best practices, security, structure, architecture"},
		tui.CommandItem{Name: "exit", Description: "Exit KodRun"},
	)

	for _, cmd := range cmds {
		items = append(items, tui.CommandItem{
			Name:        cmd.Name,
			Description: cmd.Description,
			Template:    cmd.Template,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

// gitDiffRaw runs `git diff <args>` and returns the raw unified-diff output
// with no Markdown wrapping, stat section, or truncation. It is used by the
// fan-out code-review path, which filters the diff down to source-code files
// before applying any size cap — truncating before filtering causes a
// head-of-alphabet directory like `.kodrun/` to consume the whole budget and
// starve the real source files later in the alphabet.
func gitDiffRaw(ctx context.Context, workDir string, args []string) (string, error) {
	full := []string{"diff"}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", errors.WithMessage(err, "git diff")
		}
	}
	return strings.TrimSpace(out.String()), nil
}

// gitStatRaw runs `git diff --stat <args>` and returns the raw stat output.
func gitStatRaw(ctx context.Context, workDir string, args []string) string {
	full := []string{"diff", "--stat"}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		slog.Debug("git diff --stat failed", "error", err)
	}
	return strings.TrimSpace(out.String())
}

// gitDiff runs git diff and returns formatted output.
func gitDiff(ctx context.Context, workDir string, args []string) (string, error) {
	cmdArgs := []string{"diff", "--stat"}
	cmdArgs = append(cmdArgs, args...)

	// First get stat summary
	statCmd := exec.CommandContext(ctx, "git", cmdArgs...)
	statCmd.Dir = workDir
	var statOut bytes.Buffer
	statCmd.Stdout = &statOut
	statCmd.Stderr = &statOut
	if err := statCmd.Run(); err != nil {
		slog.Debug("git diff --stat failed", "error", err)
	}

	// Then get full diff
	fullArgs := []string{"diff"}
	fullArgs = append(fullArgs, args...)
	fullCmd := exec.CommandContext(ctx, "git", fullArgs...)
	fullCmd.Dir = workDir
	var fullOut bytes.Buffer
	fullCmd.Stdout = &fullOut
	fullCmd.Stderr = &fullOut
	if err := fullCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", errors.WithMessage(err, "git diff")
		}
	}

	stat := strings.TrimSpace(statOut.String())
	full := strings.TrimSpace(fullOut.String())

	if full == "" {
		return "", nil
	}

	// Format: stat summary + full diff (truncated to 32KB)
	var result strings.Builder
	if stat != "" {
		result.WriteString("## Summary\n```\n")
		result.WriteString(stat)
		result.WriteString("\n```\n\n")
	}
	result.WriteString("## Diff\n```diff\n")
	if len(full) > 32*1024 {
		result.WriteString(full[:32*1024])
		result.WriteString("\n... (truncated)")
	} else {
		result.WriteString(full)
	}
	result.WriteString("\n```")

	return result.String(), nil
}

// buildCodeReviewPrompt builds a structured, language-aware code review prompt
// for the LLM. It tells the model exactly which tools to use to fetch rules,
// snippets and project knowledge depending on what is enabled in the config.
func buildCodeReviewPrompt(lang projectlang.Language, diff string, cfg *config.Config) string {
	var b strings.Builder

	langName := "Unknown"
	switch lang {
	case projectlang.LangGo:
		langName = "Go"
	case projectlang.LangPython:
		langName = "Python"
	case projectlang.LangJSTS:
		langName = "JavaScript/TypeScript"
	}

	// Section 1. Role and mode.
	fmt.Fprintf(&b, "# Code Review (%s)\n\n", langName)
	b.WriteString("You are a strict senior code reviewer for this project. ")
	b.WriteString("This is a strictly READ-ONLY review: do not edit files, do not run formatters or linters that mutate code, do not commit, do not create files. ")
	b.WriteString("Your only output is a structured report.\n\n")

	// Section 2. Knowledge sources — rules.
	b.WriteString("## Knowledge sources\n\n")
	b.WriteString("### Rules\n")
	switch {
	case cfg.Rules.UseTool:
		b.WriteString("- Call `get_rule(name=...)` for every component type you need to verify. The list of available rule names is in the system prompt under the rules catalog.\n")
	case cfg.RAG.Enabled:
		b.WriteString("- Call `search_docs(query=...)` with rule keywords (e.g. `service conventions`, `repository entity postgres`, `validator rules`).\n")
	default:
		b.WriteString("- Read `.kodrun/rules/<name>.md` via `read_file`. The catalog of rule files is listed in the system prompt. NOTE: rules live under `.kodrun/rules/`, not `.claude/rules/`.\n")
	}

	// Section 2. Knowledge sources — snippets.
	b.WriteString("\n### Snippets / examples\n")
	switch {
	case cfg.Snippets.UseTool:
		b.WriteString("- Call `snippets(query=..., tag=..., path=...)` to find concrete examples from `.kodrun/snippets/`. Start with `snippets()` (no filter) once to discover available tags, then narrow down by tag or query.\n")
	case cfg.RAG.Enabled:
		b.WriteString("- Use `search_docs` with queries like `<component type> example` — RAG indexes snippets too.\n")
	default:
		b.WriteString("- Use `list_dir .kodrun/snippets` and then `read_file` on relevant files.\n")
	}

	// Section 2. Knowledge sources — project knowledge.
	b.WriteString("\n### Project knowledge (architecture & structure)\n")
	b.WriteString("- A project architecture overview is PINNED at the top of `[PROJECT CONVENTIONS — rules / snippets / docs]`. Read it FIRST. It defines the layer map, dependency direction and component responsibilities; do not contradict it. That block contains conventions only — for the actual state of any file under review, call `read_file`.\n")
	b.WriteString("- ALWAYS read `AGENTS.md` next via `read_file` — it is the package map for this repo. If `README.md` exists, skim it too.\n")
	b.WriteString("- Use `list_dir` on the repo root and on `internal/` (or the language equivalent) to discover what packages exist before reasoning about layering.\n")
	if cfg.RAG.Enabled {
		b.WriteString("- For architectural questions also call `search_docs` with queries like `architecture overview`, `package <name> responsibility`, `security <topic>`.\n")
	}

	// Section 3. Algorithm.
	b.WriteString("\n## Review algorithm (mandatory order)\n")
	b.WriteString("1. **Change map** — list every changed file from the diff with its package.\n")
	b.WriteString("2. **Context understanding** — for each touched package: read the relevant section of `AGENTS.md`; if anything is unclear, `read_file` neighbouring files (interfaces, constructors) and/or use the knowledge sources above. Do NOT raise findings before you understand the context.\n")
	b.WriteString("3. **Classification** — for each file determine its component type (`service`, `repository`, `usecase`, `model`, `converter`, `validator`, `client`, `server`, `metric`, `module`, `tests`, or `infrastructure/CLI`).\n")
	b.WriteString("4. **Pull the rule** using the source defined above. If no rule exists, say so honestly — do not invent requirements.\n")
	b.WriteString("5. **Pull a snippet/example** the same way.\n")
	b.WriteString("6. **Check** against the language checklist (below) and the cross-cutting axes (architecture, security, performance, compatibility).\n")
	b.WriteString("7. **Record findings** with severity `blocker / major / minor / nit`, format: `<file>:<line> [severity] [category] — point. Source: <rule/snippet>`.\n")

	// Section 4. Language-specific checklist.
	b.WriteString("\n## Language checklist\n")
	switch lang {
	case projectlang.LangGo:
		b.WriteString("- Errors: wrap with `errors.Wrap`/`%w`; package-level sentinel errors where appropriate; no bare `panic`/`os.Exit` outside `main`.\n")
		b.WriteString("- Contexts: `ctx context.Context` is the first parameter; cancellation is propagated; no `context.Background()` in library code.\n")
		b.WriteString("- Concurrency: every goroutine has a clear owner; no data races; correct use of `sync.Mutex` / channels.\n")
		b.WriteString("- Resources: `defer Close()`; no leaks of goroutines, files, or connections.\n")
		b.WriteString("- Interfaces declared on the consumer side; small interfaces; no needless getters.\n")
		b.WriteString("- Tests: table-driven (TDT), equivalence classes and boundary conditions per the `tests` rule; no DB mocks in integration tests.\n")
		b.WriteString("- Component conformance: cross-check against `service / repository / usecase / converter / validator / client / server / metric / module` rules using the source defined above.\n")
	case projectlang.LangPython:
		b.WriteString("- Types: `mypy`-compatible, explicit annotations on public API.\n")
		b.WriteString("- Exceptions: narrow `except`, no bare `except:`, no swallowed errors.\n")
		b.WriteString("- Use context managers (`with`); no mutable default arguments.\n")
		b.WriteString("- Tests: `pytest` with parametrization; fixtures for resources.\n")
		b.WriteString("- Dependencies pinned; no top-level side effects on import.\n")
	case projectlang.LangJSTS:
		b.WriteString("- Strict TS (`strict: true`); no `any` / `as any`.\n")
		b.WriteString("- `async/await` without swallowed promises; handle `Promise.all` rejections.\n")
		b.WriteString("- Immutability; no leaked listeners/effects.\n")
		b.WriteString("- Tests (vitest/jest) cover public API; no tests on private internals.\n")
	default:
		b.WriteString("- Readability, error handling, security and tests.\n")
	}

	// Section 5. Cross-cutting axes.
	b.WriteString("\n## Cross-cutting axes (always check)\n")
	b.WriteString("- **Architecture & structure**: does the change fit its layer/package per `AGENTS.md`? Any forbidden dependency direction (e.g. repository calling server)? Premature abstractions or fat helpers? If you notice that the current project structure diverges from what `AGENTS.md` or the pinned architecture snippets describe (missing layer, orphan package, wrong dependency direction, layer named differently), list every divergence explicitly in the Cross-cutting section — do not stay silent because \"it is not in the diff\".\n")
	b.WriteString("- **Security**: input validation at boundaries (CLI/HTTP/RPC); no secrets in code or logs; SQL only via parameterized queries; paths free of `..` traversal; no shell injection in command execution; cryptography only from standard libraries; errors must not leak sensitive data into logs.\n")
	b.WriteString("- **Performance**: no accidental O(n²) on large inputs; no allocations in hot paths; batching where appropriate; no sync calls where a batch is expected.\n")
	b.WriteString("- **Compatibility**: public API not broken without reason; DB migrations are reversible or explicitly marked irreversible.\n")

	// Section 5b. Anti-hallucination guardrails.
	b.WriteString("\n## What NOT to flag (anti-hallucination)\n")
	b.WriteString("- Do NOT invent APIs, types, functions or constants. If you cannot point to the exact symbol in a rule, snippet, or `read_file` result, do not mention it.\n")
	b.WriteString("- Do NOT propose replacing a helper function with the type it returns (e.g. `validatorRules() validator.RuleSet` is a normal pattern, not an anti-pattern). Helpers that wrap a literal of a library type are idiomatic.\n")
	b.WriteString("- Do NOT propose changes to files or symbols that are not in the diff. Reviews are about the diff under review, not a global refactor.\n")
	b.WriteString("- Do NOT flag composition-root files (`cmd/<svc>/main.go`, `internal/app/**/application.go`, `internal/app/**/options.go`, `*build*.go`) for missing business logic, missing validation, or being \"too large\" — these files exist solely to wire dependencies. The only legitimate findings here are about the dependency graph itself (wrong type passed, missing wiring of an existing component, dependency direction violation).\n")
	b.WriteString("- Do NOT recommend renaming, restructuring or modernising code unless a rule or snippet explicitly requires it. Stylistic preferences without a rule citation are out of scope.\n")
	b.WriteString("- If no rule or snippet covers something, write \"no rule found\" and move on. Never invent a requirement.\n")
	b.WriteString("- Every finding MUST cite its source: `Source: rule:<name>` or `Source: snippet:<name>` or `Source: AGENTS.md#<section>`. Findings without a source are forbidden.\n")

	// Section 6. Output format.
	b.WriteString("\n## Output format\n")
	b.WriteString("1. **Summary** — 1–3 lines: language, number of files, overall impression.\n")
	b.WriteString("2. **Findings by file** — for each file, the list of findings with severities.\n")
	b.WriteString("3. **Cross-cutting** — a separate block for architecture and security findings (if any).\n")
	b.WriteString("4. **Verdict** — one of `approve` / `changes requested` / `needs major rework`, with one line of justification.\n")
	b.WriteString("5. Do NOT produce diff patches or auto-fixes.\n")

	// Diff payload.
	b.WriteString("\n## Diff under review\n")
	b.WriteString(diff)
	b.WriteString("\n")

	return b.String()
}

// buildFanoutReviewTask builds a minimal user task for a specialist reviewer
// sub-agent in the parallel code-review fan-out. Each specialist already has
// its own strict system prompt (focus area, read_file gate, output format);
// the user message must not carry the monolithic single-reviewer instructions,
// which conflict with the specialist's narrow output contract. The task lists
// only the diff, the changed files, and a short reminder to read each cited
// file before emitting a finding.
// buildFanoutReviewTask builds a minimal user task for a specialist reviewer
// in the parallel code-review fan-out. It deliberately omits the full diff
// content: specialists are required to call `read_file` on every file, so
// including the diff only wastes context and tempts the model to skip tools.
// Instead the task carries:
//   - a short stat summary (which files changed and by how many lines);
//   - a filtered list of source-code files the specialist must read.
func buildFanoutReviewTask(stat string, files []string) string {
	var b strings.Builder
	b.WriteString("# Code review — parallel specialist\n\n")
	b.WriteString("You are one of several specialist reviewers running in parallel.\n")
	b.WriteString("Your focus area, output format and read_file workflow are defined in your system prompt. Follow them STRICTLY.\n\n")

	if len(files) > 0 {
		b.WriteString("## Files changed in this diff\n")
		for _, f := range files {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if stat != "" {
		b.WriteString("## Change summary (git diff --stat)\n```\n")
		b.WriteString(stat)
		b.WriteString("\n```\n\n")
	}

	b.WriteString("## Reminders\n")
	b.WriteString("- Diff content is NOT included in this task. You MUST call `read_file(path)` on every file listed above.\n")
	b.WriteString("- Take line numbers ONLY from `read_file` output, never from memory or guesses.\n")
	b.WriteString("- Stay strictly within your specialist focus. Do not comment on anything outside it.\n")
	b.WriteString("- Output format is `path:LINE — SEVERITY — description` per line, or exactly `NO_ISSUES`.\n")
	b.WriteString("- Do NOT produce Summary / Verdict / Cross-cutting sections.\n\n")
	return b.String()
}

// buildCodeReviewRAGPrefetch runs a small fan-out of RAG searches for the
// files appearing in the diff and formats the results as a MANDATORY context
// block prepended to the reviewer prompt. This bypasses the unreliable
// "MUST call search_docs" hint and guarantees that conventions reach the
// model regardless of how the local model handles tool-call instructions.
// pinnedOverviewTags marks snippets that describe high-level project
// architecture / structure / overview. Snippets carrying any of these tags are
// always injected verbatim into the code-review prefetch block, regardless of
// semantic search ranking. This guarantees the reviewer model sees the project
// map (e.g. .kodrun/snippets/go-architecture.md) before classifying files.
// pinnedOverviewTags partitions pinned snippet tags into two tiers:
//
//   - must: always injected first, even on tight budgets. These are
//     project-wide maps the reviewer cannot do without (e.g. architecture).
//   - nice: injected only if at least half of the RAG budget is still free.
//     These augment the picture but are dropped under pressure.
//
// Lookup is via pinnedTagTier, which returns ("must"|"nice"|"").
var pinnedOverviewTagsMust = map[string]struct{}{
	"architecture": {},
	"overview":     {},
}

var pinnedOverviewTagsNice = map[string]struct{}{
	"structure":   {},
	"conventions": {},
}

// pinnedTagTier classifies a tag list. pinnedTierMust wins over pinnedTierNice
// if both apply to the same snippet.
func pinnedTagTier(tags []string) string {
	tier := ""
	for _, t := range tags {
		norm := strings.ToLower(strings.TrimSpace(t))
		if _, ok := pinnedOverviewTagsMust[norm]; ok {
			return pinnedTierMust
		}
		if _, ok := pinnedOverviewTagsNice[norm]; ok {
			tier = pinnedTierNice
		}
	}
	return tier
}

func buildCodeReviewRAGPrefetch(ctx context.Context, ragIndex tools.RAGSearcher, diff string, topK int, allSnippets []snippets.Snippet, budgetBytes int) string {
	if ragIndex == nil {
		return ""
	}
	if topK <= 0 {
		topK = 5
	}
	if budgetBytes <= 0 {
		budgetBytes = ragBudgetKB * ragBudgetMulti
	}

	files := changedFilesFromDiff(diff)

	// Build a deduplicated set of queries: per-file (basename + parent dir),
	// plus a fixed list of architecture-wide topics that almost always apply.
	querySet := make(map[string]struct{})
	addQuery := func(q string) {
		q = strings.TrimSpace(q)
		if q != "" {
			querySet[q] = struct{}{}
		}
	}
	for _, f := range files {
		base := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		addQuery(base + " conventions")
		if dir := filepath.Dir(f); dir != "." && dir != "" {
			addQuery(filepath.Base(dir) + " conventions")
		}
	}
	addQuery("architecture overview")
	addQuery("error handling conventions")
	addQuery("validator rules")
	addQuery("data-response factory")
	addQuery("main bootstrap conventions")
	addQuery("http server conventions")

	type chunkKey struct {
		path  string
		start int
	}
	seen := make(map[chunkKey]bool)
	var results []rag.SearchResult
	// Track accumulated bytes across all collected chunks for early exit:
	// once we are clearly over the final injection budget there is no value
	// in spending more embedding calls on additional queries.
	var accBytes int
	earlyExit := false
	for q := range querySet {
		if earlyExit {
			break
		}
		hits, err := ragIndex.Search(ctx, q, topK)
		if err != nil {
			continue
		}
		for _, h := range hits {
			k := chunkKey{path: h.Chunk.FilePath, start: h.Chunk.StartLine}
			if seen[k] {
				continue
			}
			seen[k] = true
			results = append(results, h)
			accBytes += len(h.Chunk.Content)
			// 2x budget gives the formatter room to drop low-score chunks.
			if accBytes > 2*budgetBytes {
				earlyExit = true
				break
			}
		}
	}
	if len(results) == 0 {
		return ""
	}

	maxBytes := budgetBytes
	var b strings.Builder
	b.WriteString("[PROJECT CONVENTIONS — rules / snippets / docs]\n")
	b.WriteString("These chunks are project conventions and code templates pre-fetched from RAG. ")
	b.WriteString("They are NOT the current state of source files under review. ")
	b.WriteString("For ANY claim about code, call `read_file` on the real file from the diff — do not cite code from this block. ")
	b.WriteString("Do not report a finding without first reading the affected file in full.\n\n")

	// Pinned overview snippets first: these describe the project map and
	// must be visible to the reviewer regardless of semantic ranking.
	// Two tiers: must (always attempted) and nice (only if budget >50% free).
	pinnedSeen := make(map[string]bool)
	var mustSnips, niceSnips []snippets.Snippet
	for i := range allSnippets {
		switch pinnedTagTier(allSnippets[i].Tags) {
		case pinnedTierMust:
			mustSnips = append(mustSnips, allSnippets[i])
		case pinnedTierNice:
			niceSnips = append(niceSnips, allSnippets[i])
		}
	}
	for i := range mustSnips {
		if pinnedSeen[mustSnips[i].SourcePath] {
			continue
		}
		pinnedSeen[mustSnips[i].SourcePath] = true
		entry := fmt.Sprintf("--- PINNED OVERVIEW: %s ---\n%s\n\n", mustSnips[i].SourcePath, mustSnips[i].Content)
		if b.Len()+len(entry) > maxBytes {
			b.WriteString("[... pinned overview truncated ...]\n")
			break
		}
		b.WriteString(entry)
	}
	// Nice-tier snippets only squeeze in when at least half the budget is free.
	if b.Len() < maxBytes/2 {
		for i := range niceSnips {
			if pinnedSeen[niceSnips[i].SourcePath] {
				continue
			}
			pinnedSeen[niceSnips[i].SourcePath] = true
			entry := fmt.Sprintf("--- PINNED OVERVIEW: %s ---\n%s\n\n", niceSnips[i].SourcePath, niceSnips[i].Content)
			if b.Len()+len(entry) > maxBytes/2 {
				break
			}
			b.WriteString(entry)
		}
	}

	for _, r := range results {
		// Skip chunks that come from a snippet we've already injected in full
		// as a pinned overview — no need to repeat them.
		if pinnedSeen[r.Chunk.FilePath] {
			continue
		}
		content := rag.CompressChunk(r.Chunk.FilePath, r.Chunk.Content)
		entry := fmt.Sprintf("--- %s:%d-%d ---\n%s\n\n", r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, content)
		if b.Len()+len(entry) > maxBytes {
			b.WriteString("[... truncated, additional results omitted ...]\n")
			break
		}
		b.WriteString(entry)
	}
	b.WriteString("[END PROJECT CONVENTIONS]\n")
	return b.String()
}

// waitForRAGReady blocks until the background RAG indexing flag clears or
// the timeout/context fires. It is a best-effort gate used by review
// commands so they do not query a half-built index right after startup.
func waitForRAGReady(ctx context.Context, indexing *atomic.Bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for indexing.Load() {
		if ctx.Err() != nil {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// filterDiffByPackage keeps only the file sections of a unified-diff whose
// `+++ b/<path>` line is rooted under the given package scope. Used by
// `/code-review --package <path>` to narrow review to a single package
// without re-running git. Empty diff or empty scope return the input as-is.
func filterDiffByPackage(diff, scope string) string {
	scope = strings.TrimSuffix(scope, "/")
	if scope == "" || diff == "" {
		return diff
	}
	prefix := scope + "/"
	lines := strings.Split(diff, "\n")
	var out []string
	keep := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git ") {
			// Look ahead for the +++ b/<path> line that determines whether
			// we keep this file section. Stop at the next "diff --git" or EOF.
			path := ""
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "diff --git "); j++ {
				if strings.HasPrefix(lines[j], "+++ ") {
					p := strings.TrimPrefix(lines[j], "+++ ")
					p = strings.TrimPrefix(p, "b/")
					path = strings.TrimSpace(p)
					break
				}
			}
			keep = path != "" && (path == scope || strings.HasPrefix(path, prefix))
		}
		if keep {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// filterDiffToSourceCode keeps only file sections whose `+++ b/<path>` points
// at actual source code. It drops documentation, configuration and project
// metadata (markdown, YAML, JSON, lockfiles, `.kodrun/**`, `vendor/**`,
// `testdata/**`, `go.mod`/`go.sum`, etc.) so fan-out specialist reviewers are
// not flooded with noise when a large diff mixes a handful of code changes
// with bulk doc or config updates. Empty diff returns the input as-is.
func filterDiffToSourceCode(diff string) string {
	if diff == "" {
		return diff
	}
	lines := strings.Split(diff, "\n")
	var out []string
	keep := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git ") {
			path := ""
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "diff --git "); j++ {
				if strings.HasPrefix(lines[j], "+++ ") {
					p := strings.TrimPrefix(lines[j], "+++ ")
					p = strings.TrimPrefix(p, "b/")
					path = strings.TrimSpace(p)
					break
				}
			}
			keep = isSourceCodePath(path)
		}
		if keep {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// isSourceCodePath reports whether path looks like a real source code file
// worth code-reviewing. The check is extension- and prefix-based, deliberately
// conservative: extend the allow list when new languages are added rather
// than trying to derive it from projectlang.
func isSourceCodePath(path string) bool {
	if path == "" || path == pathDevNull {
		return false
	}
	// Deny: project metadata and convention dumps.
	denyPrefixes := []string{
		".kodrun/", ".claude/", ".github/", ".git/",
		"vendor/", "node_modules/", "testdata/", "docs/", "doc/",
	}
	for _, p := range denyPrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	denyNames := map[string]bool{
		"go.sum":            true,
		"package-lock.json": true, "yarn.lock": true,
		"pnpm-lock.yaml": true, "Cargo.lock": true, "poetry.lock": true,
		"Pipfile.lock": true, "AGENTS.md": true, "README.md": true,
		"CLAUDE.md": true,
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if denyNames[base] {
		return false
	}
	// Allow: known source-code extensions.
	allowExts := []string{
		".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".rs", ".java", ".kt", ".kts", ".scala", ".rb", ".php",
		".c", ".h", ".cc", ".cpp", ".hpp", ".m", ".mm", ".swift",
		".cs", ".fs", ".vb", ".ex", ".exs", ".erl", ".hrl",
		".lua", ".pl", ".pm", ".sh", ".bash", ".zsh", ".sql",
		".proto",
	}
	for _, ext := range allowExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// changedFilesFromDiff extracts file paths from a unified-diff `git diff`
// output. It looks at "+++ b/<path>" lines and ignores /dev/null entries.
func changedFilesFromDiff(diff string) []string {
	lines := strings.Split(diff, "\n")
	files := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		p := strings.TrimPrefix(line, "+++ ")
		p = strings.TrimPrefix(p, "b/")
		p = strings.TrimSpace(p)
		if p == "" || p == pathDevNull || seen[p] {
			continue
		}
		seen[p] = true
		files = append(files, p)
	}
	return files
}

func newOrchestrator(client *ollama.Client, chatProv config.ProviderConfig, reg *tools.Registry, cfg *config.Config, emit agent.EventHandler, confirmFn agent.ConfirmFunc, planConfirmFn agent.PlanConfirmFunc, stepConfirmFn agent.StepConfirmFunc, ruleCatalog string, ragIndex tools.RAGSearcher, godocIndexer tools.GoDocIndexer, langState *projectlang.State, rulesLoader *rules.Loader) *agent.Orchestrator {
	// Resolve thinking/executor providers (fallback to chat provider).
	thinkProv := cfg.ThinkingProvider()
	execProv := cfg.ExecutorProvider()

	// The orchestrator's primary client/model serve "thinking" roles
	// (planner, reviewer, extractor). Use a dedicated client only when the
	// thinking provider differs from the default chat provider; otherwise
	// reuse the existing client to avoid duplicate connections.
	primaryClient := client
	primaryModel := chatProv.Model
	primaryCtx := chatProv.ContextSize
	if cfg.Agent.ThinkingProvider != "" && cfg.Agent.ThinkingProvider != cfg.Agent.Provider {
		primaryClient = ollama.NewClient(thinkProv.BaseURL, thinkProv.Timeout)
		primaryModel = thinkProv.Model
		primaryCtx = thinkProv.ContextSize
	}

	// Executor client: only build a separate one when the executor provider
	// is explicitly configured and differs from the primary.
	var (
		execClient  *ollama.Client
		execModel   string
		execCtxSize int
	)
	if cfg.Agent.ExecutorProvider != "" {
		execClient = ollama.NewClient(execProv.BaseURL, execProv.Timeout)
		execModel = execProv.Model
		execCtxSize = execProv.ContextSize
	}

	// Extractor: deterministic JSON output. ExtractorProvider() applies the
	// Temperature=0 + Format=json overlay automatically. We always build a
	// dedicated client for it so chat-mode generation parameters do not leak.
	extractorProv := cfg.ExtractorProvider()
	extractorClient := ollama.NewClient(extractorProv.BaseURL, extractorProv.Timeout)
	if cfg.Agent.ExtractorProvider == "" {
		// Reuse the chat client connection when no dedicated provider is set
		// to avoid spawning extra HTTP clients against the same backend.
		extractorClient = client
	}

	return agent.NewOrchestrator(primaryClient, primaryModel, reg, flags.workDir, primaryCtx, &agent.OrchestratorConfig{
		EventHandler:         emit,
		ConfirmFunc:          confirmFn,
		PlanConfirm:          planConfirmFn,
		StepConfirmFn:        stepConfirmFn,
		Language:             cfg.Agent.Language,
		RuleCatalog:          ruleCatalog,
		Review:               cfg.Agent.Review,
		HasSnippets:          cfg.Snippets.UseTool && !cfg.RAG.Enabled,
		HasRAG:               cfg.RAG.Enabled,
		PrefetchCode:         cfg.Agent.PrefetchCode,
		RAGIndex:             ragIndex,
		GodocIndexer:         godocIndexer,
		LangState:            langState,
		RulesLoader:          rulesLoader,
		ExecutorClient:       execClient,
		ExecutorModel:        execModel,
		ExecutorContextSize:  execCtxSize,
		ExtractorClient:      extractorClient,
		ExtractorModel:       extractorProv.Model,
		ExtractorContextSize: extractorProv.ContextSize,
		ExtractorTemperature: extractorProv.Temperature,
		ExtractorFormat:      extractorProv.Format,
		MaxParallelTasks:     cfg.Agent.MaxParallelTasks,
		MaxReplans:           cfg.Agent.MaxReplans,
		MaxIterations:        cfg.Agent.MaxIterations,
		SpecialistTimeout:    cfg.Agent.SpecialistTimeout,
		AutoCommit:           cfg.Agent.AutoCommit,
		Verbose:              flags.verbose,
	})
}

func buildCmd() *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "Run go build with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, "go_build", cmd.Args().Slice())
		},
	}
}

func testCmd() *cli.Command {
	return &cli.Command{
		Name:  "test",
		Usage: "Run go test with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, "go_test", cmd.Args().Slice())
		},
	}
}

func lintCmd() *cli.Command {
	return &cli.Command{
		Name:  "lint",
		Usage: "Run golangci-lint with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, "go_lint", cmd.Args().Slice())
		},
	}
}

func fixCmd() *cli.Command {
	return &cli.Command{
		Name:  "fix",
		Usage: "Fix issues in a specific file",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return errors.New("fix requires exactly one file argument")
			}
			cfg, err := loadConfig(ctx)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()
			s := setupAgent(ctx, &cfg, rules.ScopeFix)
			ag, loader, mcpMgr := s.ag, s.loader, s.mcpMgr
			if mcpMgr != nil {
				defer mcpMgr.Close()
			}

			output := agent.NewPlainOutput(os.Stdout)
			ag.SetEventHandler(output.Handle)
			ruleCatalog := loader.RuleCatalogString(ctx, rules.ScopeFix, cfg.Rules.UseTool)

			task := fmt.Sprintf("Read file %s, find and fix all issues (bugs, style, errors). Run go_build and go_test after fixing.", cmd.Args().First())
			return ag.Run(ctx, task, ruleCatalog)
		},
	}
}

func initCmd() *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Create .kodrun/ starter structure",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg, err := loadConfig(ctx)
			if err != nil {
				return err
			}
			chatProv := cfg.ChatProvider()
			client := ollama.NewClient(chatProv.BaseURL, chatProv.Timeout)
			fmt.Println("Scanning project and generating AGENTS.md via LLM...")
			res, err := kodruninit.Run(ctx, flags.workDir, client, chatProv.Model)
			if err != nil {
				return err
			}
			for _, path := range res.Created {
				fmt.Println("  created", path)
			}
			fmt.Printf("Done: %d items created\n", len(res.Created))
			return nil
		},
	}
}

func formatContext(history []ollama.Message) string {
	var b strings.Builder
	for i, msg := range history {
		role := strings.ToUpper(msg.Role)
		b.WriteString(fmt.Sprintf("─── [%d] %s ───\n", i, role))
		content := msg.Content
		if content != "" {
			b.WriteString(content)
			b.WriteString("\n")
		}
		for _, tc := range msg.ToolCalls {
			b.WriteString(fmt.Sprintf("  → tool_call: %s(%v)\n", tc.Function.Name, tc.Function.Arguments))
		}
		if msg.ToolCallID != "" {
			b.WriteString(fmt.Sprintf("  tool_call_id: %s\n", msg.ToolCallID))
		}
	}
	b.WriteString(fmt.Sprintf("\nTotal messages: %d", len(history)))
	return b.String()
}

func runGoTool(ctx context.Context, toolName string, args []string) error {
	cfg, err := loadConfig(ctx)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s := setupAgent(ctx, &cfg, rules.ScopeFix)
	ag, reg, mcpMgr := s.ag, s.reg, s.mcpMgr
	if mcpMgr != nil {
		defer mcpMgr.Close()
	}

	output := agent.NewPlainOutput(os.Stdout)
	ag.SetEventHandler(output.Handle)

	params := map[string]any{}
	if len(args) > 0 {
		params["packages"] = strings.Join(args, " ")
	}

	result, err := reg.Execute(ctx, toolName, params)
	if err != nil {
		return err
	}

	fmt.Println(result.Output)

	exitCode, ok := result.Meta["exit_code"].(int)
	if !ok {
		exitCode = 0
	}
	success := exitCode == 0

	if !success && cfg.Agent.AutoFix && !flags.noFix {
		fmt.Println("\n[auto-fix] Attempting to fix errors...")
		chatProv := cfg.ChatProvider()
		client := ollama.NewClient(chatProv.BaseURL, chatProv.Timeout)
		fixer := runner.NewFixer(ctx, client, chatProv.Model, reg, maxFixAttempts)
		fixed, err := fixer.Fix(ctx, toolName, result.Output, func(msg string) {
			fmt.Println(msg)
		})
		if err != nil {
			return errors.WithMessage(err, "auto-fix")
		}
		if fixed {
			fmt.Println("[auto-fix] Errors fixed successfully!")
		} else {
			fmt.Println("[auto-fix] Could not fix all errors")
		}
	}

	if !success {
		exitWithCode(1)
	}
	return nil
}

// cleanupLegacyLangDirs removes per-language RAG sub-index directories left
// over from earlier kodrun versions that indexed project source code. RAG now
// stores everything under <basePath>/common; any "go", "python" or "jsts"
// directory is stale and can only corrupt search results. The call is
// idempotent — missing directories are silently ignored.
func cleanupLegacyLangDirs(basePath string) {
	for _, lang := range []string{"go", "python", "jsts"} {
		_ = os.RemoveAll(filepath.Join(basePath, lang))
	}
}
