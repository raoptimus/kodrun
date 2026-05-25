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
	"sync/atomic"

	"github.com/raoptimus/kodrun/internal/agent"
)

// runOrchestrate executes the explicit `/orchestrate` slash command. The
// optional task argument overrides the original input.
func runOrchestrate(taskCtx context.Context, d *TaskDeps, task string, parts []string) {
	orchTask := task
	if len(parts) > 1 {
		orchTask = parts[1]
	}
	orch := NewOrchestrator(d.Client, d.ChatProv, d.Registry, d.Cfg, d.Emit, d.Agent.GetConfirmFunc(), d.PlanConfirm, d.StepConfirm, d.RuleCatalog, d.RAGIndex, d.GodocIndexer, d.LangState, d.RulesLoader, d.Flags)
	if err := orch.Run(taskCtx, orchTask); err != nil && taskCtx.Err() == nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
		d.Emit(&agent.Event{Type: agent.EventDone})
	}
}

// runOrchestratorPlan handles the implicit "plan-mode + cfg.Agent.Orchestrator"
// path. EventDone is wrapped so we only emit it once even when the
// orchestrator already did.
func runOrchestratorPlan(taskCtx context.Context, d *TaskDeps, task string) {
	var doneSent atomic.Bool
	wrappedEmit := agent.EventHandler(func(e *agent.Event) {
		if e.Type == agent.EventDone {
			doneSent.Store(true)
		}
		d.Emit(e)
	})
	orch := NewOrchestrator(d.Client, d.ChatProv, d.Registry, d.Cfg, wrappedEmit, d.Agent.GetConfirmFunc(), d.PlanConfirm, d.StepConfirm, d.RuleCatalog, d.RAGIndex, d.GodocIndexer, d.LangState, d.RulesLoader, d.Flags)
	if err := orch.Run(taskCtx, task); err != nil && taskCtx.Err() == nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	}
	if !doneSent.Load() {
		d.Emit(&agent.Event{Type: agent.EventDone})
	}
}
