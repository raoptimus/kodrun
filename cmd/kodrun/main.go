package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
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

func setupAgent(ctx context.Context, cfg config.Config, scope rules.Scope) (*agent.Agent, *tools.Registry, *rules.Loader, *snippets.Loader, *rag.Index, *mcp.Manager, *ollama.Client) {
	chatProv := cfg.ChatProvider()
	chatClient := ollama.NewClient(chatProv.BaseURL, chatProv.Timeout)

	loader := rules.NewLoader(flags.workDir, cfg.Rules.MaxRefSize)
	if err := loader.Load(ctx); err != nil {
		slog.Warn("rules load failed", "error", err)
	}
	snippetLoader := snippets.NewLoader(flags.workDir)
	if err := snippetLoader.Load(ctx); err != nil {
		slog.Warn("snippets load failed", "error", err)
	}

	reg := tools.NewRegistry()
	tools.RegisterAllTools(ctx, reg, flags.workDir, cfg.Tools.ForbiddenPatterns, cfg.Tools.MaxReadLines, loader, snippetLoader, scope, cfg.Rules.UseTool, cfg.Snippets.UseTool, cfg.RAG.Enabled)

	// RAG setup (may use a different provider)
	var ragIndex *rag.Index
	if cfg.RAG.Enabled {
		indexPath := cfg.RAG.IndexPath
		if !filepath.IsAbs(indexPath) {
			indexPath = filepath.Join(flags.workDir, indexPath)
		}
		ragProv := cfg.RAGProvider()
		ragClient := ollama.NewClient(ragProv.BaseURL, ragProv.Timeout)
		ragIndex = rag.NewIndex(ragClient, cfg.RAG.EmbeddingModel, indexPath)
		if err := ragIndex.Load(); err != nil {
			slog.Warn("RAG index load failed", "error", err)
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
	return ag, reg, loader, snippetLoader, ragIndex, mcpMgr, chatClient
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

	ag, reg, loader, snippetLoader, ragIndex, mcpMgr, client := setupAgent(ctx, cfg, rules.ScopeCoding)
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
			chunks = append(chunks, rag.ChunkEmbeddedDocs(cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
			n, err := ragIndex.Build(ctx, chunks)
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
	ag.SetConfirmFunc(func(tool string, detail string) agent.ConfirmResult {
		resultCh := make(chan agent.ConfirmResult, 1)
		danger := tool == "bash" && tools.IsDangerousCommand(detail)
		confirmCh <- tui.ConfirmRequest{
			Tool:   tool,
			Detail: detail,
			Danger: danger,
			Result: resultCh,
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
			case "reindex":
				if ragIndex == nil {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
				} else if !indexing.CompareAndSwap(false, true) {
					emit(agent.Event{Type: agent.EventAgent, Message: "RAG indexing already in progress"})
				} else {
					emit(agent.Event{Type: agent.EventAgent, Message: "Reindexing..."})
					safeGo(&bgWg, events, func() {
						defer indexing.Store(false)
						chunks, err := rag.ChunkFiles(taskCtx, flags.workDir, cfg.RAG.IndexDirs, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)
						if err != nil {
							emit(agent.Event{Type: agent.EventError, Message: fmt.Sprintf("chunk: %s", err)})
							emit(agent.Event{Type: agent.EventDone})
							return
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
						chunks = append(chunks, rag.ChunkEmbeddedDocs(cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
						n, err := ragIndex.Build(taskCtx, chunks)
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
						ragIndex.Size(), cfg.RAG.EmbeddingModel, ragIndex.Updated().Format(time.RFC3339))})
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
					n, err := ragIndex.Build(taskCtx, chunks)
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
			}

			if cmdName == "orchestrate" {
				orchTask := task
				if len(parts) > 1 {
					orchTask = parts[1]
				}
				orch := newOrchestrator(client, chatProv, reg, cfg, emit, ag.GetConfirmFunc(), planConfirmFn, ruleCatalog, ragIndex)
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
		// Skip orchestrator for simple questions — route them directly to ag.Send().
		if cfg.Agent.Orchestrator && ag.Mode() == agent.ModePlan && !looksLikeQuestion(task) {
			var doneSent atomic.Bool
			wrappedEmit := agent.EventHandler(func(e agent.Event) {
				if e.Type == agent.EventDone {
					doneSent.Store(true)
				}
				emit(e)
			})
			orch := newOrchestrator(client, chatProv, reg, cfg, wrappedEmit, ag.GetConfirmFunc(), planConfirmFn, ruleCatalog, ragIndex)
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
		} else if ag.Mode() == agent.ModePlan && ag.LastPlan() != "" {
			// In standalone plan mode, show the extracted plan.
			emit(agent.Event{Type: agent.EventAgent, Message: ag.LastPlan()})
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
		tui.CommandItem{Name: "reindex", Description: "Rebuild RAG index"},
		tui.CommandItem{Name: "rag", Description: "Show RAG index status"},
		tui.CommandItem{Name: "add_doc", Description: "Add a document to RAG index"},
		tui.CommandItem{Name: "orchestrate", Description: "Run Plan→Execute→Review pipeline"},
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

func newOrchestrator(client *ollama.Client, chatProv config.ProviderConfig, reg *tools.Registry, cfg config.Config, emit agent.EventHandler, confirmFn agent.ConfirmFunc, planConfirmFn agent.PlanConfirmFunc, ruleCatalog string, ragIndex *rag.Index) *agent.Orchestrator {
	return agent.NewOrchestrator(client, chatProv.Model, reg, flags.workDir, chatProv.ContextSize, agent.OrchestratorConfig{
		EventHandler: emit,
		ConfirmFunc:  confirmFn,
		PlanConfirm:  planConfirmFn,
		Language:     cfg.Agent.Language,
		RuleCatalog:  ruleCatalog,
		Review:       cfg.Agent.Review,
		HasSnippets:  cfg.Snippets.UseTool && !cfg.RAG.Enabled,
		HasRAG:       cfg.RAG.Enabled,
		PrefetchCode: cfg.Agent.PrefetchCode,
		RAGIndex:     ragIndex,
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
			ag, _, loader, _, _, mcpMgr, _ := setupAgent(ctx, cfg, rules.ScopeFix)
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

	ag, reg, _, _, _, mcpMgr, _ := setupAgent(ctx, cfg, rules.ScopeFix)
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

// looksLikeQuestion returns true if the input looks like a question
// rather than a task/instruction for the orchestrator.
func looksLikeQuestion(text string) bool {
	text = strings.TrimSpace(text)
	if strings.HasSuffix(text, "?") {
		return true
	}
	lower := strings.ToLower(text)
	// Check if any word in the text is a question word (handles "вы как...", "а почему..." etc.)
	questionWords := []string{
		"what ", "why ", "how ", "when ", "where ", "who ",
		"which ", "is it ", "are there ", "can you ", "could you ",
		"would you ", "should ", "does ", "explain ", "tell me",
		"что ", "почему ", "как ", "когда ", "где ", "кто ",
		"какой ", "какая ", "какое ", "какие ", "зачем ", "сколько ",
		"можно ", "нужно ", "скажи", "расскажи", "объясни",
		"подскажи", "в чём ", "в чем ", "чем ",
	}
	for _, w := range questionWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}
