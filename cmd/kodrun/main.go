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

var version = "v1.0.0-beta1"

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
		os.Exit(1)
	}
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load(context.Background(), flags.config, flags.workDir)
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

func setupAgent(ctx context.Context, cfg config.Config, scope rules.Scope) (*agent.Agent, *tools.Registry, *rules.Loader, *snippets.Loader, *rag.MultiIndex, *projectlang.State, *mcp.Manager, *ollama.Client) {
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

	reg := tools.NewRegistry()
	tools.RegisterAllTools(ctx, reg, flags.workDir, cfg.Tools.ForbiddenPatterns, cfg.Tools.MaxReadLines, loader, snippetLoader, scope, cfg.Rules.UseTool, cfg.Snippets.UseTool, cfg.RAG.Enabled, currentLang)

	// RAG setup (may use a different provider)
	var ragIndex *rag.MultiIndex
	if cfg.RAG.Enabled {
		indexPath := cfg.RAG.IndexPath
		if !filepath.IsAbs(indexPath) {
			indexPath = filepath.Join(flags.workDir, indexPath)
		}
		ragProv := cfg.RAGProvider()
		ragClient := ollama.NewClient(ragProv.BaseURL, ragProv.Timeout)
		ragIndex = rag.NewMultiIndex(ragClient, ragProv.Model, indexPath)
		ragIndex.SetActiveLanguage(string(currentLang))
		if err := ragIndex.LoadCommon(); err != nil {
			slog.Warn("RAG common index load failed", "error", err)
		}
		if currentLang != projectlang.LangUnknown {
			if err := ragIndex.LoadLanguage(string(currentLang)); err != nil {
				slog.Warn("RAG language index load failed", "error", err)
			}
		}
		reg.Register(tools.NewRAGSearchTool(ragIndex, cfg.RAG.TopK))
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
	return ag, reg, loader, snippetLoader, ragIndex, langState, mcpMgr, chatClient
}

func runRoot(_ context.Context, cmd *cli.Command) error {
	// Resolve workDir to absolute path so file reads work regardless of cwd
	absWorkDir, err := filepath.Abs(flags.workDir)
	if err != nil {
		return errors.WithMessage(err, "resolve work-dir")
	}
	flags.workDir = absWorkDir

	args := cmd.Args().Slice()

	cfg, err := loadConfig()
	if err != nil {
		return errors.WithMessage(err, "load config")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ag, reg, loader, snippetLoader, ragIndex, langState, mcpMgr, client := setupAgent(ctx, cfg, rules.ScopeCoding)
	if mcpMgr != nil {
		defer mcpMgr.Close()
		ag.AddConfirmTools(mcpMgr.ConfirmTools())
		ag.AddReadOnlyTools(mcpMgr.ReadOnlyTools())
	}

	// Set default mode and think from config
	defaultMode := agent.ModePlan
	if cfg.Agent.DefaultMode == "edit" {
		defaultMode = agent.ModeEdit
	}
	ag.SetMode(defaultMode)
	ag.SetThink(cfg.Agent.Think)
	ag.SetLanguage(cfg.Agent.Language)
	ag.SetAutoCompact(cfg.Agent.AutoCompact)
	ag.SetMaxWorkers(cfg.Agent.MaxWorkers)
	ag.SetHasSnippets(cfg.Snippets.UseTool && !cfg.RAG.Enabled)
	ag.SetHasRAG(cfg.RAG.Enabled)
	ag.SetRAGIndex(ragIndex)

	// Check Ollama connectivity
	chatProv := cfg.ChatProvider()
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
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
	events := make(chan agent.Event, 100)
	// emit sends an event without blocking if context is cancelled or channel is full.
	emit := func(e agent.Event) {
		select {
		case events <- e:
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

	// Background RAG indexing
	if ragIndex != nil {
		indexing.Store(true)
		safeGo(&bgWg, events, func() {
			defer indexing.Store(false)
			defer func() {
				emit(agent.Event{Type: agent.EventRAGProgress})
			}()
			chunks, err := rag.ChunkFiles(ctx, flags.workDir, cfg.RAG.IndexDirs, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)
			if err != nil {
				emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG chunking: %s", err)})
				return
			}
			// Index snippets into RAG
			for _, s := range snippetLoader.Snippets() {
				chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
					Name:        s.Name,
					Description: s.Description,
					Tags:        s.Tags,
					Content:     s.Content,
					SourcePath:  s.SourcePath,
				}})...)
			}
			// Index rules into RAG
			for _, r := range loader.AllRules() {
				chunks = append(chunks, rag.ChunkRules([]rag.RuleInfo{{
					Name:    filepath.Base(r.Path),
					Content: r.Content,
					Path:    r.Path,
				}})...)
			}
			// Index reference docs into RAG
			for path, content := range loader.ReferenceDocPaths() {
				chunks = append(chunks, rag.ChunkRefDocs([]rag.RefDocInfo{{
					Path:    path,
					Content: content,
				}})...)
			}
			// Index embedded reference docs (Effective Go)
			progressFn := func(label string, done, total int) {
				emit(agent.Event{
					Type:          agent.EventRAGProgress,
					ProgressDone:  done,
					ProgressTotal: total,
					ProgressLabel: label,
				})
			}
			n, err := ragIndex.BuildCommonWithProgress(ctx, chunks, progressFn)
			if err != nil {
				emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG index: %s", err)})
				return
			}
			if err := ragIndex.Save(); err != nil {
				emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG save: %s", err)})
				return
			}
			if n > 0 {
				emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG indexed %d new chunks (%d total)", n, ragIndex.Size())})
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
			parts := strings.SplitN(input, " ", 2)
			cmdName := strings.TrimPrefix(parts[0], "/")

			// Built-in commands
			switch cmdName {
			case "compact":
				var instructions string
				if len(parts) > 1 {
					instructions = parts[1]
				}
				emit(agent.Event{Type: agent.EventAgent, Message: "Compacting context..."})
				if err := ag.Compact(taskCtx, instructions); err != nil {
					emit(agent.Event{Type: agent.EventError, Message: err.Error()})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "edit":
				if ag.LastPlan() != "" {
					ag.EnterEditWithPlan()
					emit(agent.Event{Type: agent.EventAgent, Message: "Loaded approved plan. Send any message to start execution."})
				} else {
					ag.SetMode(agent.ModeEdit)
					ag.SetThink(false)
					emit(agent.Event{Type: agent.EventAgent, Message: "No plan available. Switched to edit mode."})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "clear":
				ag.ClearHistory()
				ag.ClearSessionPermissions()
				emit(agent.Event{Type: agent.EventAgent, Message: "Context and permissions cleared"})
				used, total := ag.ContextUsage()
				emit(agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
				emit(agent.Event{Type: agent.EventDone})
				return
			case "diff":
				emit(agent.Event{Type: agent.EventAgent, Message: "Computing diff..."})
				var diffArgs []string
				if len(parts) > 1 {
					diffArgs = strings.Fields(parts[1])
				}
				diffOutput, err := gitDiff(flags.workDir, diffArgs)
				if err != nil {
					emit(agent.Event{Type: agent.EventError, Message: err.Error()})
				} else if diffOutput == "" {
					emit(agent.Event{Type: agent.EventAgent, Message: "No uncommitted changes."})
				} else {
					emit(agent.Event{Type: agent.EventAgent, Message: diffOutput})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "resume":
				s, err := agent.LatestSession(sessionsDir)
				if err != nil {
					emit(agent.Event{Type: agent.EventAgent, Message: "No sessions to resume."})
				} else {
					ag.LoadFromSession(s)
					emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Resumed session %s (%d messages, %s mode)", s.ID, len(s.Messages), s.Mode)})
					used, total := ag.ContextUsage()
					emit(agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "sessions":
				summaries, err := agent.ListSessions(sessionsDir)
				if err != nil || len(summaries) == 0 {
					emit(agent.Event{Type: agent.EventAgent, Message: "No saved sessions."})
				} else {
					var sb strings.Builder
					sb.WriteString("Saved sessions:\n")
					for _, s := range summaries {
						fmt.Fprintf(&sb, "  %s  %s  %s  %d msgs\n", s.ID, s.Model, s.Mode, s.MessageCount)
					}
					emit(agent.Event{Type: agent.EventAgent, Message: sb.String()})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "reindex":
				if ragIndex == nil {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
					emit(agent.Event{Type: agent.EventDone})
				} else if !indexing.CompareAndSwap(false, true) {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG indexing already in progress"})
					emit(agent.Event{Type: agent.EventDone})
				} else {
					emit(agent.Event{Type: agent.EventAgent, Message: "Reindexing..."})
					safeGo(&bgWg, events, func() {
						defer indexing.Store(false)
						defer func() {
							// Always clear the progress indicator on exit so the
							// status bar disappears even on error paths.
							emit(agent.Event{Type: agent.EventRAGProgress})
						}()
						chunks, err := rag.ChunkFiles(ctx, flags.workDir, cfg.RAG.IndexDirs, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)
						if err != nil {
							emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("chunk: %s", err)})
							emit(agent.Event{Type: agent.EventDone})
							return
						}
						// Index snippets into RAG (parity with initial indexing pipeline).
						for _, s := range snippetLoader.Snippets() {
							chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
								Name:        s.Name,
								Description: s.Description,
								Tags:        s.Tags,
								Content:     s.Content,
								SourcePath:  s.SourcePath,
							}})...)
						}
						// Index rules into RAG
						for _, r := range loader.AllRules() {
							chunks = append(chunks, rag.ChunkRules([]rag.RuleInfo{{
								Name:    filepath.Base(r.Path),
								Content: r.Content,
								Path:    r.Path,
							}})...)
						}
						// Index reference docs into RAG
						for path, content := range loader.ReferenceDocPaths() {
							chunks = append(chunks, rag.ChunkRefDocs([]rag.RefDocInfo{{
								Path:    path,
								Content: content,
							}})...)
						}
						// Index embedded reference docs (Effective Go)
						progressFn := func(label string, done, total int) {
							emit(agent.Event{
								Type:          agent.EventRAGProgress,
								ProgressDone:  done,
								ProgressTotal: total,
								ProgressLabel: label,
							})
						}
						n, err := ragIndex.BuildCommonWithProgress(ctx, chunks, progressFn)
						if err != nil {
							emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index: %s", err)})
							emit(agent.Event{Type: agent.EventDone})
							return
						}
						if err := ragIndex.Save(); err != nil {
							emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("save: %s", err)})
							emit(agent.Event{Type: agent.EventDone})
							return
						}
						emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Indexed %d new chunks (%d total)", n, ragIndex.Size())})
						emit(agent.Event{Type: agent.EventDone})
					})
				}
				return
			case "rag":
				if ragIndex == nil {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable with rag.enabled: true"})
				} else {
					emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG: %d entries, model: %s, updated: %s",
						ragIndex.Size(), cfg.RAGProvider().Model, ragIndex.Updated().Format(time.RFC3339))})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "add_doc":
				if ragIndex == nil {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
					emit(agent.Event{Type: agent.EventDone})
					return
				}
				var docPath string
				if len(parts) > 1 {
					docPath = strings.TrimSpace(parts[1])
				}
				if docPath == "" {
					emit(agent.Event{Type: agent.EventAgent, Message: "Usage: /add_doc <file_path>"})
					emit(agent.Event{Type: agent.EventDone})
					return
				}
				if !filepath.IsAbs(docPath) {
					docPath = filepath.Join(flags.workDir, docPath)
				}
				emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Adding %s to RAG index...", docPath)})
				safeGo(&bgWg, events, func() {
					chunks, err := rag.ChunkFile(docPath, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)
					if err != nil {
						emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("read: %s", err)})
						emit(agent.Event{Type: agent.EventDone})
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
						emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index: %s", err)})
						emit(agent.Event{Type: agent.EventDone})
						return
					}
					if err := ragIndex.Save(); err != nil {
						emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("save: %s", err)})
						emit(agent.Event{Type: agent.EventDone})
						return
					}
					emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Added %d chunks from %s (%d total)", n, filepath.Base(docPath), ragIndex.Size())})
					emit(agent.Event{Type: agent.EventDone})
				})
				return
			case "init":
				emit(agent.Event{Type: agent.EventAgent, Message: "Scanning project and generating AGENTS.md..."})
				res, err := kodruninit.Run(taskCtx, flags.workDir, client, chatProv.Model)
				if err != nil {
					emit(agent.Event{Type: agent.EventError, Message: err.Error()})
				} else {
					for _, path := range res.Created {
						emit(agent.Event{Type: agent.EventAgent, Message: "created " + path})
					}
					emit(agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Done: %d items created", len(res.Created))})
				}
				emit(agent.Event{Type: agent.EventDone})
				return
			case "code-review":
				var diffArgs []string
				if len(parts) > 1 {
					diffArgs = strings.Fields(parts[1])
				}
				emit(agent.Event{Type: agent.EventAgent, Message: "Collecting diff for code review..."})
				diffOut, err := gitDiff(flags.workDir, diffArgs)
				if err != nil {
					emit(agent.Event{Type: agent.EventError, Message: err.Error()})
					emit(agent.Event{Type: agent.EventDone})
					return
				}
				if strings.TrimSpace(diffOut) == "" {
					emit(agent.Event{Type: agent.EventAgent, Message: "No changes to review."})
					emit(agent.Event{Type: agent.EventDone})
					return
				}
				lang := langState.Current()
				task = buildCodeReviewPrompt(lang, diffOut, cfg)
				// Pre-fetch RAG context for the changed files. Local models
				// (qwen, etc.) often ignore "MUST call search_docs" hints in
				// the system prompt and dive straight into read_file. By
				// running the searches ourselves and injecting the results as
				// MANDATORY context, we guarantee that conventions reach the
				// reviewer regardless of model behaviour.
				if ragIndex != nil && cfg.RAG.Enabled {
					if pre := buildCodeReviewRAGPrefetch(taskCtx, ragIndex, diffOut, cfg.RAG.TopK, snippetLoader.Snippets()); pre != "" {
						task = pre + "\n" + task
					}
				}
				// fall through to the standard agent pipeline
			}

			if cmdName == "orchestrate" {
				orchTask := task
				if len(parts) > 1 {
					orchTask = parts[1]
				}
				orch := newOrchestrator(client, chatProv, reg, cfg, emit, ag.GetConfirmFunc(), planConfirmFn, ruleCatalog, ragIndex, langState, loader)
				err := orch.Run(taskCtx, orchTask)
				if err != nil && taskCtx.Err() == nil {
					emit(agent.Event{Type: agent.EventError, Message: err.Error()})
					emit(agent.Event{Type: agent.EventDone})
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
			wrappedEmit := agent.EventHandler(func(e agent.Event) {
				if e.Type == agent.EventDone {
					doneSent.Store(true)
				}
				emit(e)
			})
			orch := newOrchestrator(client, chatProv, reg, cfg, wrappedEmit, ag.GetConfirmFunc(), planConfirmFn, ruleCatalog, ragIndex, langState, loader)
			if err := orch.Run(taskCtx, task); err != nil && taskCtx.Err() == nil {
				emit(agent.Event{Type: agent.EventError, Message: err.Error()})
			}
			// Ensure EventDone is always sent so the TUI stops the timer.
			if !doneSent.Load() {
				emit(agent.Event{Type: agent.EventDone})
			}
			return
		}

		err := ag.Send(taskCtx, task)
		if err != nil {
			// Send() emits EventDone on success, but not on error.
			// Emit error + done so timer stops and running resets.
			if taskCtx.Err() != nil {
				// Context was cancelled (Esc pressed) — don't show cryptic error
				emit(agent.Event{Type: agent.EventDone})
			} else {
				emit(agent.Event{Type: agent.EventError, Message: err.Error()})
				emit(agent.Event{Type: agent.EventDone})
			}
			return
		}

		if ag.Mode() == agent.ModePlan && ag.LastPlan() != "" {
			// In standalone plan mode, show the extracted plan.
			emit(agent.Event{Type: agent.EventAgent, Message: ag.LastPlan()})
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
			taskCtx, thinkClient, thinkModel, cfg.Agent.Language, task, responseText, 60*time.Second,
		)
		if classifyErr != nil {
			slog.Debug("classifier failed", "err", classifyErr)
		}

		if verdict.SuggestedAction != agent.ClassifyActionApprovePlan {
			return
		}

		// Append CTA only if the agent did not already include one.
		if verdict.CTAText != "" && !strings.Contains(responseText, verdict.CTAText) {
			emit(agent.Event{Type: agent.EventAgent, Message: verdict.CTAText})
		}

		// Show the plan-confirm dialog.
		cr := planConfirmFn(responseText)
		switch cr.Action {
		case agent.PlanDeny:
			emit(agent.Event{Type: agent.EventAgent, Message: "Execution cancelled by user."})
			return
		case agent.PlanAugment:
			// Re-route augment text as a new task in the next turn.
			emit(agent.Event{Type: agent.EventAgent, Message: "Plan augmentation: send your refinement as a new message."})
			return
		case agent.PlanAutoAccept, agent.PlanManualApprove:
			var confirmFn agent.ConfirmFunc
			if cr.Action == agent.PlanManualApprove {
				confirmFn = ag.GetConfirmFunc()
			}
			emit(agent.Event{Type: agent.EventAgent, Message: "▸ Executing approved plan..."})
			orch := newOrchestrator(client, chatProv, reg, cfg, emit, confirmFn, planConfirmFn, ruleCatalog, ragIndex, langState, loader)
			if err := orch.RunExecutor(taskCtx, responseText, confirmFn); err != nil && taskCtx.Err() == nil {
				emit(agent.Event{Type: agent.EventError, Message: err.Error()})
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
	model := tui.NewModel(chatProv.Model, version, chatProv.ContextSize, taskFn, cancelTask, events, commands, confirmCh, planConfirmCh,
		flags.workDir, defaultMode, cfg.Agent.Think, setModeFn, contextFn, cfg.Agent.Language, cfg.TUI.MaxHistory)

	// Save terminal state before bubbletea modifies it (alt screen, mouse reporting).
	// Deferred restore acts as safety net if bubbletea cannot clean up.
	if oldState, err := term.GetState(int(os.Stdin.Fd())); err == nil {
		defer term.Restore(int(os.Stdin.Fd()), oldState)
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
	case <-time.After(3 * time.Second):
		slog.Warn("kodrun: shutdown timeout, forcing exit")
	}

	close(events)

	return nil
}

func buildCommandItems(loader *rules.Loader) []tui.CommandItem {
	cmds := loader.Commands()
	items := make([]tui.CommandItem, 0, len(cmds)+3)

	// Built-in commands
	items = append(items,
		tui.CommandItem{Name: "compact", Description: "Summarize conversation to free context"},
		tui.CommandItem{Name: "edit", Description: "Switch to edit mode (with plan if available)"},
		tui.CommandItem{Name: "init", Description: "Create .kodrun/ starter structure"},
		tui.CommandItem{Name: "clear", Description: "Clear conversation context"},
		tui.CommandItem{Name: "diff", Description: "Show git diff (uncommitted changes)"},
		tui.CommandItem{Name: "resume", Description: "Resume last saved session"},
		tui.CommandItem{Name: "sessions", Description: "List saved sessions"},
		tui.CommandItem{Name: "reindex", Description: "Rebuild RAG index"},
		tui.CommandItem{Name: "rag", Description: "Show RAG index status"},
		tui.CommandItem{Name: "add_doc", Description: "Add a document to RAG index"},
		tui.CommandItem{Name: "orchestrate", Description: "Run Plan→Execute→Review pipeline"},
		tui.CommandItem{Name: "code-review", Description: "Run a language-aware code review of uncommitted or specified changes"},
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
func gitDiff(workDir string, args []string) (string, error) {
	cmdArgs := []string{"diff", "--stat"}
	cmdArgs = append(cmdArgs, args...)

	// First get stat summary
	statCmd := exec.Command("git", cmdArgs...)
	statCmd.Dir = workDir
	var statOut bytes.Buffer
	statCmd.Stdout = &statOut
	statCmd.Stderr = &statOut
	statCmd.Run() // ignore error, stat may be empty

	// Then get full diff
	fullArgs := []string{"diff"}
	fullArgs = append(fullArgs, args...)
	fullCmd := exec.Command("git", fullArgs...)
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
func buildCodeReviewPrompt(lang projectlang.Language, diff string, cfg config.Config) string {
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
	b.WriteString("- A project architecture overview is PINNED at the top of `[PROJECT RULES — pre-fetched from RAG]`. Read it FIRST. It defines the layer map, dependency direction and component responsibilities; do not contradict it.\n")
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
	b.WriteString("- **Architecture & structure**: does the change fit its layer/package per `AGENTS.md`? Any forbidden dependency direction (e.g. repository calling server)? Premature abstractions or fat helpers?\n")
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
var pinnedOverviewTags = map[string]struct{}{
	"architecture": {},
	"overview":     {},
	"structure":    {},
}

func buildCodeReviewRAGPrefetch(ctx context.Context, ragIndex tools.RAGSearcher, diff string, topK int, allSnippets []snippets.Snippet) string {
	if ragIndex == nil {
		return ""
	}
	if topK <= 0 {
		topK = 5
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
	for q := range querySet {
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
		}
	}
	if len(results) == 0 {
		return ""
	}

	// Cap the total injected size so we don't blow the context window. ~64KB
	// of pre-fetched docs fits a typical architecture overview plus a handful
	// of per-file convention chunks.
	const maxBytes = 64 * 1024
	var b strings.Builder
	b.WriteString("[PROJECT RULES — pre-fetched from RAG, treat as MANDATORY]\n")
	b.WriteString("The following chunks are project conventions and code templates that ALREADY apply to the changes under review. ")
	b.WriteString("Read them BEFORE looking at the diff. Every violation must be reported as a finding.\n\n")

	// Pinned overview snippets first: these describe the project map and
	// must be visible to the reviewer regardless of semantic ranking.
	pinnedSeen := make(map[string]bool)
	for _, s := range allSnippets {
		if !hasAnyPinnedOverviewTag(s.Tags) {
			continue
		}
		if pinnedSeen[s.SourcePath] {
			continue
		}
		pinnedSeen[s.SourcePath] = true
		entry := fmt.Sprintf("--- PINNED OVERVIEW: %s ---\n%s\n\n", s.SourcePath, s.Content)
		if b.Len()+len(entry) > maxBytes {
			b.WriteString("[... pinned overview truncated ...]\n")
			break
		}
		b.WriteString(entry)
	}

	for _, r := range results {
		// Skip chunks that come from a snippet we've already injected in full
		// as a pinned overview — no need to repeat them.
		if pinnedSeen[r.Chunk.FilePath] {
			continue
		}
		entry := fmt.Sprintf("--- %s:%d-%d ---\n%s\n\n", r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
		if b.Len()+len(entry) > maxBytes {
			b.WriteString("[... truncated, additional results omitted ...]\n")
			break
		}
		b.WriteString(entry)
	}
	b.WriteString("[END PROJECT RULES]\n")
	return b.String()
}

// hasAnyPinnedOverviewTag reports whether any of the snippet's tags marks it
// as a project-wide overview that should always be injected into code-review.
func hasAnyPinnedOverviewTag(tags []string) bool {
	for _, t := range tags {
		if _, ok := pinnedOverviewTags[strings.ToLower(strings.TrimSpace(t))]; ok {
			return true
		}
	}
	return false
}

// changedFilesFromDiff extracts file paths from a unified-diff `git diff`
// output. It looks at "+++ b/<path>" lines and ignores /dev/null entries.
func changedFilesFromDiff(diff string) []string {
	var files []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		p := strings.TrimPrefix(line, "+++ ")
		p = strings.TrimPrefix(p, "b/")
		p = strings.TrimSpace(p)
		if p == "" || p == "/dev/null" || seen[p] {
			continue
		}
		seen[p] = true
		files = append(files, p)
	}
	return files
}

func newOrchestrator(client *ollama.Client, chatProv config.ProviderConfig, reg *tools.Registry, cfg config.Config, emit agent.EventHandler, confirmFn agent.ConfirmFunc, planConfirmFn agent.PlanConfirmFunc, ruleCatalog string, ragIndex tools.RAGSearcher, langState *projectlang.State, rulesLoader *rules.Loader) *agent.Orchestrator {
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

	return agent.NewOrchestrator(primaryClient, primaryModel, reg, flags.workDir, primaryCtx, agent.OrchestratorConfig{
		EventHandler:         emit,
		ConfirmFunc:          confirmFn,
		PlanConfirm:          planConfirmFn,
		Language:             cfg.Agent.Language,
		RuleCatalog:          ruleCatalog,
		Review:               cfg.Agent.Review,
		HasSnippets:          cfg.Snippets.UseTool && !cfg.RAG.Enabled,
		HasRAG:               cfg.RAG.Enabled,
		PrefetchCode:         cfg.Agent.PrefetchCode,
		RAGIndex:             ragIndex,
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
	})
}

func buildCmd() *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "Run go build with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool("go_build", cmd.Args().Slice())
		},
	}
}

func testCmd() *cli.Command {
	return &cli.Command{
		Name:  "test",
		Usage: "Run go test with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool("go_test", cmd.Args().Slice())
		},
	}
}

func lintCmd() *cli.Command {
	return &cli.Command{
		Name:  "lint",
		Usage: "Run golangci-lint with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool("go_lint", cmd.Args().Slice())
		},
	}
}

func fixCmd() *cli.Command {
	return &cli.Command{
		Name:  "fix",
		Usage: "Fix issues in a specific file",
		Action: func(_ context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return errors.New("fix requires exactly one file argument")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			ag, _, loader, _, _, _, mcpMgr, _ := setupAgent(ctx, cfg, rules.ScopeFix)
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
			cfg, err := loadConfig()
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

func runGoTool(toolName string, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ag, reg, _, _, _, _, mcpMgr, _ := setupAgent(ctx, cfg, rules.ScopeFix)
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

	if !result.Success && cfg.Agent.AutoFix && !flags.noFix {
		fmt.Println("\n[auto-fix] Attempting to fix errors...")
		chatProv := cfg.ChatProvider()
		client := ollama.NewClient(chatProv.BaseURL, chatProv.Timeout)
		fixer := runner.NewFixer(ctx, client, chatProv.Model, reg, 3)
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

	if !result.Success {
		os.Exit(1)
	}
	return nil
}
