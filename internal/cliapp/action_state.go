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

	"github.com/raoptimus/kodrun/internal/agent"
)

// actionCompact runs the agent's history-compaction routine. Optional free
// text after "/compact" is forwarded as user instructions.
func actionCompact(taskCtx context.Context, d *TaskDeps, parts []string) {
	var instructions string
	if len(parts) > 1 {
		instructions = parts[1]
	}
	d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Compacting context..."})
	if err := d.Agent.Compact(taskCtx, instructions); err != nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}

// actionEdit toggles to edit mode. If a plan was previously approved the
// agent re-enters edit-with-plan; otherwise we just switch the mode.
func actionEdit(d *TaskDeps) {
	if d.Agent.LastPlan() != "" {
		d.Agent.EnterEditWithPlan()
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Loaded approved plan. Send any message to start execution."})
	} else {
		d.Agent.SetMode(agent.ModeEdit)
		d.Agent.SetThink(false)
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "No plan available. Switched to edit mode."})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}

// actionClear wipes both conversation history and any per-session permission
// grants and reports the resulting context-window usage.
func actionClear(d *TaskDeps) {
	d.Agent.ClearHistory()
	d.Agent.ClearSessionPermissions()
	d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Context and permissions cleared"})
	used, total := d.Agent.ContextUsage()
	d.Emit(&agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
	d.Emit(&agent.Event{Type: agent.EventDone})
}

// actionDiff prints `git diff` (optionally restricted by extra args) into
// the chat. Empty diff is announced explicitly so the user is not left
// staring at silence.
func actionDiff(d *TaskDeps, parts []string) {
	d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Computing diff..."})
	var diffArgs []string
	if len(parts) > 1 {
		diffArgs = strings.Fields(parts[1])
	}
	diffOutput, err := GitDiff(d.Ctx, d.Flags.WorkDir, diffArgs)
	switch {
	case err != nil:
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	case diffOutput == "":
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "No uncommitted changes."})
	default:
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: diffOutput})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}
