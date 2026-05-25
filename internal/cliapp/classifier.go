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
	"strings"
	"time"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tools"
)

// ClassifierDeps groups everything HandleClassifierFlow needs to classify
// a plan response and, on approval, build an orchestrator to execute it.
type ClassifierDeps struct {
	Agent        *agent.Agent
	Client       llm.Client
	ChatProv     *config.ProviderConfig
	Cfg          *config.Config
	Registry     *tools.Registry
	PlanConfirm  agent.PlanConfirmFunc
	StepConfirm  agent.StepConfirmFunc
	RuleCatalog  string
	RAGIndex     tools.RAGSearcher
	GodocIndexer tools.GoDocIndexer
	LangState    *projectlang.State
	RulesLoader  *rules.Loader
	Flags        *Flags
}

// HandleClassifierFlow runs the post-Send classifier path: it asks the
// thinking model whether the agent's plan-mode response is actually a plan
// the user might want to execute. On approve-plan verdict, it shows the
// plan-confirm dialog and, if accepted, kicks off the orchestrator's
// RunExecutor against the response text. Returns silently when the
// classifier is disabled by config (orchestrator on, non-plan mode) or the
// response is empty.
func HandleClassifierFlow(
	ctx context.Context,
	d *ClassifierDeps,
	emit agent.EventHandler,
	task string,
	classifyTimeout time.Duration,
) {
	if d.Cfg.Agent.Orchestrator || d.Agent.Mode() != agent.ModePlan {
		return
	}
	responseText := d.Agent.LastPlan()
	if strings.TrimSpace(responseText) == "" {
		return
	}

	thinkProv := d.Cfg.ThinkingProvider()
	thinkClient := d.Client
	thinkModel := d.ChatProv.Model
	if d.Cfg.Agent.ThinkingProvider != "" && d.Cfg.Agent.ThinkingProvider != d.Cfg.Agent.Provider {
		thinkClient = NewLLMClient(&thinkProv)
		thinkModel = thinkProv.Model
	}

	verdict, classifyErr := agent.ClassifyResponse(
		ctx, thinkClient, thinkModel, d.Cfg.Agent.Language, task, responseText, classifyTimeout,
	)
	if classifyErr != nil {
		slog.Debug("classifier failed", "err", classifyErr)
	}

	if verdict.SuggestedAction != agent.ClassifyActionApprovePlan {
		return
	}

	if verdict.CTAText != "" && !strings.Contains(responseText, verdict.CTAText) {
		emit(&agent.Event{Type: agent.EventAgent, Message: verdict.CTAText})
	}

	cr := d.PlanConfirm(responseText)
	switch cr.Action {
	case agent.PlanDeny:
		emit(&agent.Event{Type: agent.EventAgent, Message: "Execution cancelled by user."})
		return
	case agent.PlanAugment:
		emit(&agent.Event{Type: agent.EventAgent, Message: "Plan augmentation: send your refinement as a new message."})
		return
	case agent.PlanAutoAccept, agent.PlanManualApprove:
		autoAccept := cr.Action == agent.PlanAutoAccept
		var confirmFn agent.ConfirmFunc
		if !autoAccept {
			confirmFn = d.Agent.GetConfirmFunc()
		}
		emit(&agent.Event{Type: agent.EventAgent, Message: "▸ Executing approved plan..."})
		orch := NewOrchestrator(d.Client, d.ChatProv, d.Registry, d.Cfg, emit, confirmFn, d.PlanConfirm, d.StepConfirm, d.RuleCatalog, d.RAGIndex, d.GodocIndexer, d.LangState, d.RulesLoader, d.Flags)
		if err := orch.RunExecutor(ctx, responseText, confirmFn, autoAccept); err != nil && ctx.Err() == nil {
			emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
		}
	}
}
