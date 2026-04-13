package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
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

var version = "v1.1.0-beta"

const (
	cmdNameEdit       = "edit"
	cmdNameCodeReview = "code-review"
	pathDevNull       = "/dev/null"

	pingTimeout      = 5 * time.Second
	eventChanSize    = 100
	classifyTimeout  = 60 * time.Second
	maxFixAttempts   = 3
	pollInterval     = 250 * time.Millisecond
	shutdownTimeout  = 3 * time.Second
	builtinCmdCount  = 3 // number of built-in commands added beyond user commands
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
	langState.SetTechDetector(projectlang.NewTechDetector(flags.workDir))
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
			techStack := langState.EnsureTechDetected().Strings()
			var chunks []rag.Chunk
			// Index snippets into RAG
			for i := range snips {
				chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
					Name:        snips[i].Name,
					Description: snips[i].Description,
					Tags:        snips[i].Tags,
					Requires:    snips[i].Requires,
					Content:     snips[i].Content,
					SourcePath:  snips[i].SourcePath,
				}}, techStack)...)
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
						reloadTechStack := langState.EnsureTechDetected().Strings()
						var chunks []rag.Chunk
						// Index snippets into RAG (parity with initial indexing pipeline).
						for i := range reloadedSnips {
							chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
								Name:        reloadedSnips[i].Name,
								Description: reloadedSnips[i].Description,
								Tags:        reloadedSnips[i].Tags,
								Requires:    reloadedSnips[i].Requires,
								Content:     reloadedSnips[i].Content,
								SourcePath:  reloadedSnips[i].SourcePath,
							}}, reloadTechStack)...)
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
				runCodeReview(taskCtx, flags.workDir, &cfg, client, chatProv, reg, ag, emit, planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader, snippetLoader)
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

// findAllSourceFiles walks workDir and returns relative paths to all source
// code files in the project, skipping hidden directories, vendor, etc.
func findAllSourceFiles(workDir string) []string {
	var files []string
	err := filepath.WalkDir(workDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(workDir, p)
		if relErr != nil {
			return relErr
		}
		if isSourceCodePath(rel) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		slog.Debug("findAllSourceFiles walk failed", "error", err)
	}
	return files
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

// runCodeReview handles the orchestrator-driven code review pipeline.
func runCodeReview(ctx context.Context, workDir string, cfg *config.Config, client *ollama.Client, chatProv config.ProviderConfig, reg *tools.Registry, ag *agent.Agent, emit agent.EventHandler, planConfirmFn agent.PlanConfirmFunc, stepConfirmFn agent.StepConfirmFunc, ruleCatalog string, ragIndex tools.RAGSearcher, godocIndexer tools.GoDocIndexer, langState *projectlang.State, loader *rules.Loader, snippetLoader *snippets.Loader) {
	sourceFiles := findAllSourceFiles(workDir)
	if len(sourceFiles) == 0 {
		emit(&agent.Event{Type: agent.EventAgent, Message: "No source files found."})
		emit(&agent.Event{Type: agent.EventDone})
		return
	}
	var doneSent atomic.Bool
	wrappedEmit := agent.EventHandler(func(e *agent.Event) {
		if e.Type == agent.EventDone {
			doneSent.Store(true)
		}
		emit(e)
	})
	orch := newOrchestrator(client, chatProv, reg, cfg, wrappedEmit, ag.GetConfirmFunc(), planConfirmFn, stepConfirmFn, ruleCatalog, ragIndex, godocIndexer, langState, loader)
	if err := orch.RunCodeReview(ctx, sourceFiles, snippetLoader.Snippets()); err != nil && ctx.Err() == nil {
		emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	}
	if !doneSent.Load() {
		emit(&agent.Event{Type: agent.EventDone})
	}
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
		Think:                cfg.Agent.Think,
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
