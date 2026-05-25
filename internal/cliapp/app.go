/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tui"
)

// pingTimeout caps how long we wait for the chat backend (Ollama et al.) to
// respond to a connectivity check at startup.
const pingTimeout = 5 * time.Second

// eventChanSize buffers UI events between background goroutines and the
// bubbletea program. Sized to avoid back-pressure during indexing bursts.
const eventChanSize = 100

// Run is the entry point invoked by cmd/kodrun's CLI Action. It wires
// configuration, agent setup, RAG indexing, the bubbletea TUI, and the
// task dispatcher together. Returns the error from agent.Run (one-shot
// or stdin paths) or RunTUI (interactive path).
func Run(ctx context.Context, cmd *cli.Command, f *Flags, version string) error {
	ConfigureSlog(f.Verbose)
	absWorkDir, err := filepath.Abs(f.WorkDir)
	if err != nil {
		return errors.WithMessage(err, "resolve work-dir")
	}
	f.WorkDir = absWorkDir

	args := cmd.Args().Slice()

	cfg, err := LoadConfig(ctx, f)
	if err != nil {
		return errors.WithMessage(err, "load config")
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s := SetupAgent(ctx, &cfg, rules.ScopeCoding, f)
	ag, reg, loader, snippetLoader := s.Agent, s.Registry, s.Loader, s.SnippetLoader
	ragIndex, godocIndexer, langState := s.RAGIndex, s.GodocIndexer, s.LangState
	mcpMgr, client := s.MCPManager, s.Client
	if mcpMgr != nil {
		defer mcpMgr.Close()
		ag.AddConfirmTools(mcpMgr.ConfirmTools())
		ag.AddReadOnlyTools(mcpMgr.ReadOnlyTools())
	}

	defaultMode := agent.ModePlan
	switch cfg.Agent.DefaultMode {
	case CmdNameEdit:
		defaultMode = agent.ModeEdit
	case agent.ModeChat.String():
		defaultMode = agent.ModeChat
	}
	ag.SetMode(defaultMode)
	ag.SetThink(cfg.Agent.Think)
	ag.SetLanguage(cfg.Agent.Language)
	ag.SetAutoCompact(cfg.Agent.AutoCompact)
	ag.SetMaxToolWorkers(cfg.Agent.MaxToolWorkers)
	ag.SetHasSnippets(cfg.Snippets.UseTool && !cfg.RAG.Enabled)
	ag.SetHasRAG(cfg.RAG.Enabled)
	ag.SetRAGIndex(ragIndex)

	chatProv := cfg.ChatProvider()
	pingCtx, pingCancel := context.WithTimeout(ctx, pingTimeout)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		return errors.WithMessage(err, "cannot connect to Ollama\nMake sure 'ollama serve' is running")
	}

	ruleCatalog := loader.RuleCatalogString(ctx, rules.ScopeCoding, cfg.Rules.UseTool)

	if len(args) > 0 {
		ag.SetMode(agent.ModeEdit)
		task := strings.Join(args, " ")
		output := agent.NewPlainOutput(os.Stdout)
		ag.SetEventHandler(output.Handle)
		return ag.Run(ctx, task, ruleCatalog)
	}

	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if !isTTY || f.NoTUI {
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

	events := make(chan agent.Event, eventChanSize)
	emit := func(e *agent.Event) {
		select {
		case events <- *e:
		case <-ctx.Done():
		}
	}
	ag.SetEventHandler(emit)

	sessionsDir := filepath.Join(f.WorkDir, ".kodrun", "sessions")
	ag.SetSessionDir(sessionsDir)
	ag.Init(ruleCatalog)

	var bgWg sync.WaitGroup
	var indexing atomic.Bool

	MigrateLegacyChunks(ragIndex, emit)

	if ragIndex != nil {
		indexing.Store(true)
		SafeGo(&bgWg, events, func() {
			defer indexing.Store(false)
			defer func() {
				emit(&agent.Event{Type: agent.EventRAGProgress})
			}()
			RunInitialIndex(ctx, ragIndex, loader, snippetLoader, langState, &cfg, emit)
		})
	}

	confirmCh := make(chan tui.ConfirmRequest, 1)
	ag.SetConfirmFunc(func(payload agent.ConfirmPayload) agent.ConfirmResult {
		resultCh := make(chan agent.ConfirmResult, 1)
		confirmCh <- tui.ConfirmRequest{Payload: payload, Result: resultCh}
		return <-resultCh
	})

	planConfirmCh := make(chan tui.PlanConfirmRequest, 1)
	planConfirmFn := func(plan string) agent.PlanConfirmResult {
		resultCh := make(chan agent.PlanConfirmResult, 1)
		planConfirmCh <- tui.PlanConfirmRequest{Plan: plan, Result: resultCh}
		return <-resultCh
	}

	stepConfirmCh := make(chan tui.StepConfirmRequest, 1)
	stepConfirmFn := func(description string) agent.StepConfirmAction {
		resultCh := make(chan agent.StepConfirmAction, 1)
		stepConfirmCh <- tui.StepConfirmRequest{Description: description, Result: resultCh}
		return <-resultCh
	}

	modelPickerCh := make(chan tui.ModelPickerRequest, 1)

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

	taskDeps := &TaskDeps{
		TaskBaseCtx: TaskBaseCtx{
			Ctx:         ctx,
			Cfg:         &cfg,
			Flags:       f,
			SessionsDir: sessionsDir,
			RuleCatalog: ruleCatalog,
		},
		TaskAgentDeps: TaskAgentDeps{
			Client:         client,
			ChatProv:       &chatProv,
			Agent:          ag,
			Registry:       reg,
			RulesLoader:    loader,
			SnippetsLoader: snippetLoader,
			LangState:      langState,
			RAGIndex:       ragIndex,
			GodocIndexer:   godocIndexer,
		},
		TaskUISignals: TaskUISignals{
			Emit:          emit,
			PlanConfirm:   planConfirmFn,
			StepConfirm:   stepConfirmFn,
			ModelPickerCh: modelPickerCh,
			Events:        events,
			BgWg:          &bgWg,
			Indexing:      &indexing,
		},
	}
	taskFn := func(input string) {
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
		RunTask(taskCtx, taskDeps, input)
	}

	return RunTUI(&TUIParams{
		Agent:         ag,
		Cfg:           &cfg,
		ChatProv:      &chatProv,
		RulesLoader:   loader,
		MCPManager:    mcpMgr,
		Events:        events,
		ConfirmCh:     confirmCh,
		PlanConfirmCh: planConfirmCh,
		StepConfirmCh: stepConfirmCh,
		ModelPickerCh: modelPickerCh,
		WorkDir:       f.WorkDir,
		Version:       version,
		DefaultMode:   defaultMode,
		TaskFn:        taskFn,
		CancelTask:    cancelTask,
		Cancel:        cancel,
		BgWg:          &bgWg,
	})
}
