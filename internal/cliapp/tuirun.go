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
	"log/slog"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/mcp"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tui"
)

// shutdownTimeout caps how long we wait for background goroutines to drain
// after the TUI exits. Three seconds is enough for normal cleanup; anything
// longer holds up exit while the user has already closed the program.
const shutdownTimeout = 3 * time.Second

// TUIParams aggregates the dependencies RunTUI needs. Most are produced by
// SetupAgent or by runRoot's local channels.
type TUIParams struct {
	Agent         *agent.Agent
	Cfg           *config.Config
	ChatProv      *config.ProviderConfig
	RulesLoader   *rules.Loader
	MCPManager    *mcp.Manager
	Events        chan agent.Event
	ConfirmCh     chan tui.ConfirmRequest
	PlanConfirmCh chan tui.PlanConfirmRequest
	StepConfirmCh chan tui.StepConfirmRequest
	ModelPickerCh chan tui.ModelPickerRequest
	WorkDir       string
	Version       string
	DefaultMode   agent.Mode
	TaskFn        func(string)
	CancelTask    func()
	Cancel        context.CancelFunc
	BgWg          *sync.WaitGroup
}

// RunTUI builds the bubbletea model, saves/restores terminal state around
// p.Run, and orchestrates graceful shutdown: it cancels the root context,
// closes MCP servers, then waits up to shutdownTimeout for background
// goroutines to drain. Returns the error from tea.Program.Run.
func RunTUI(p *TUIParams) error {
	ag := p.Agent
	setModeFn := func(mode agent.Mode, think bool) {
		if mode == agent.ModeEdit && ag.Mode() == agent.ModePlan && ag.LastPlan() != "" {
			ag.EnterEditWithPlan()
		} else {
			ag.SetMode(mode)
			ag.SetThink(think)
		}
	}
	contextFn := func() string {
		return FormatContext(ag.History())
	}

	commands := BuildCommandItems(p.RulesLoader)
	model := tui.NewModel(p.ChatProv.Model, p.Version, p.ChatProv.ContextSize, p.TaskFn, p.CancelTask, p.Events, commands, p.ConfirmCh, p.PlanConfirmCh, p.StepConfirmCh, p.ModelPickerCh,
		p.WorkDir, p.DefaultMode, p.Cfg.Agent.Think, setModeFn, contextFn, p.Cfg.Agent.Language, p.Cfg.TUI.MaxHistory)

	// Save terminal state before bubbletea modifies it (alt screen, mouse reporting).
	// Deferred restore acts as safety net if bubbletea cannot clean up.
	if oldState, err := term.GetState(int(os.Stdin.Fd())); err == nil {
		defer func() {
			if restoreErr := term.Restore(int(os.Stdin.Fd()), oldState); restoreErr != nil {
				slog.Warn("kodrun: failed to restore terminal state", "error", restoreErr)
			}
		}()
	}

	prog := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := prog.Run(); err != nil {
		return err
	}

	// Cancel context and wait for background goroutines with timeout.
	p.Cancel()

	// Close MCP servers early (before waiting) so their processes don't linger.
	if p.MCPManager != nil {
		p.MCPManager.Close()
	}

	waitDone := make(chan struct{})
	go func() {
		p.BgWg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(shutdownTimeout):
		slog.Warn("kodrun: shutdown timeout, forcing exit")
	}

	// Intentionally do not close(events): on shutdown timeout, late goroutines
	// may still attempt to send and would panic. The TUI has already exited
	// (p.Run returned), so no consumer is left, and the channel will be
	// reclaimed by GC once the goroutines finish.

	return nil
}
