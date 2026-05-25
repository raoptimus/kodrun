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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/snippets"
	"github.com/raoptimus/kodrun/internal/tools"
	"github.com/raoptimus/kodrun/internal/tui"
)

// splitCmdArgParts is the SplitN limit for "/cmd arg" inputs.
const splitCmdArgParts = 2

// classifyTimeout caps how long the foreground classifier blocks before
// the dialog is surfaced (or skipped).
const classifyTimeout = 60 * time.Second

// ragWaitTimeout caps how long /code-review blocks waiting for background
// RAG indexing to finish.
const ragWaitTimeout = 2 * time.Minute

// TaskBaseCtx carries cross-cutting context used by every command handler:
// cancellation, configuration, CLI flags, on-disk session/rule paths.
type TaskBaseCtx struct {
	Ctx         context.Context
	Cfg         *config.Config
	Flags       *Flags
	SessionsDir string
	RuleCatalog string
}

// TaskAgentDeps groups the agent and its supporting infrastructure: LLM
// client, tool registry, rule/snippet loaders, RAG and language state.
type TaskAgentDeps struct {
	Client         llm.Client
	ChatProv       *config.ProviderConfig
	Agent          *agent.Agent
	Registry       *tools.Registry
	RulesLoader    *rules.Loader
	SnippetsLoader *snippets.Loader
	LangState      *projectlang.State
	RAGIndex       rag.Backend
	GodocIndexer   tools.GoDocIndexer
}

// TaskUISignals bundles the channels and callbacks that connect a command
// handler back to the bubbletea TUI: event stream, confirm dialogs, model
// picker, plus background-indexing synchronisation primitives.
type TaskUISignals struct {
	Emit          agent.EventHandler
	PlanConfirm   agent.PlanConfirmFunc
	StepConfirm   agent.StepConfirmFunc
	ModelPickerCh chan tui.ModelPickerRequest
	Events        chan agent.Event
	BgWg          *sync.WaitGroup
	Indexing      *atomic.Bool
}

// TaskDeps aggregates everything RunTask needs to dispatch one user input.
// Constructed once in main and reused for every turn. Composed of three
// thematic groups via embedding so call sites still write d.Cfg / d.Agent /
// d.Emit while the structure makes the responsibilities visible.
type TaskDeps struct {
	TaskBaseCtx
	TaskAgentDeps
	TaskUISignals
}

// RunTask processes one user input — either a slash command or free-form
// text. Slash commands dispatch to built-in handlers; free text either runs
// the orchestrator (plan mode + cfg.Agent.Orchestrator) or hits ag.Send and
// then runs the classifier flow.
func RunTask(taskCtx context.Context, d *TaskDeps, input string) {
	task := input
	if strings.HasPrefix(input, "/") {
		parts := strings.SplitN(input, " ", splitCmdArgParts)
		cmdName := strings.TrimPrefix(parts[0], "/")

		if handled := dispatchBuiltin(taskCtx, d, cmdName, parts); handled {
			return
		}

		if cmdName == "orchestrate" {
			runOrchestrate(taskCtx, d, task, parts)
			return
		}

		if cmd, ok := d.RulesLoader.GetCommand(cmdName); ok {
			task = cmd.Template
			if len(parts) > 1 {
				task = strings.ReplaceAll(task, "{{arg}}", strings.TrimSpace(parts[1]))
			}
		}
	}

	if d.Cfg.Agent.Orchestrator && d.Agent.Mode() == agent.ModePlan {
		runOrchestratorPlan(taskCtx, d, task)
		return
	}

	if err := d.Agent.Send(taskCtx, task); err != nil {
		if taskCtx.Err() != nil {
			d.Emit(&agent.Event{Type: agent.EventDone})
		} else {
			d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
			d.Emit(&agent.Event{Type: agent.EventDone})
		}
		return
	}

	if d.Agent.Mode() == agent.ModePlan && d.Agent.LastPlan() != "" {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: d.Agent.LastPlan()})
	}

	HandleClassifierFlow(taskCtx, &ClassifierDeps{
		Agent:        d.Agent,
		Client:       d.Client,
		ChatProv:     d.ChatProv,
		Cfg:          d.Cfg,
		Registry:     d.Registry,
		PlanConfirm:  d.PlanConfirm,
		StepConfirm:  d.StepConfirm,
		RuleCatalog:  d.RuleCatalog,
		RAGIndex:     d.RAGIndex,
		GodocIndexer: d.GodocIndexer,
		LangState:    d.LangState,
		RulesLoader:  d.RulesLoader,
		Flags:        d.Flags,
	}, d.Emit, task, classifyTimeout)
}

// dispatchBuiltin handles every built-in slash command. Returns true when
// cmdName matched and the handler emitted EventDone; false otherwise.
func dispatchBuiltin(taskCtx context.Context, d *TaskDeps, cmdName string, parts []string) bool {
	switch cmdName {
	case "model":
		actionModel(taskCtx, d)
	case "compact":
		actionCompact(taskCtx, d, parts)
	case CmdNameEdit:
		actionEdit(d)
	case "clear":
		actionClear(d)
	case "diff":
		actionDiff(d, parts)
	case "resume":
		actionResume(d)
	case "sessions":
		actionSessions(d)
	case "reindex":
		actionReindex(d)
	case "rag":
		actionRAGStatus(d)
	case "add_doc":
		actionAddDoc(d, parts)
	case "init":
		actionInit(taskCtx, d)
	case CmdNameCodeReview:
		actionCodeReview(taskCtx, d, parts)
	default:
		return false
	}
	return true
}
