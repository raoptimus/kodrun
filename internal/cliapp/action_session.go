/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/agent"
)

// actionResume loads the most recent saved session into the agent. Missing
// or unreadable sessions surface as a friendly chat message rather than an
// error event.
func actionResume(d *TaskDeps) {
	s, err := agent.LatestSession(d.SessionsDir)
	if err != nil {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "No sessions to resume."})
	} else {
		d.Agent.LoadFromSession(s)
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Resumed session %s (%d messages, %s mode)", s.ID, len(s.Messages), s.Mode)})
		used, total := d.Agent.ContextUsage()
		d.Emit(&agent.Event{Type: agent.EventTokens, ContextUsed: used, ContextTotal: total})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}

// actionSessions prints a one-line summary for every saved session, sorted
// by recency.
func actionSessions(d *TaskDeps) {
	summaries, err := agent.ListSessions(d.SessionsDir)
	if err != nil || len(summaries) == 0 {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "No saved sessions."})
	} else {
		var sb strings.Builder
		sb.WriteString("Saved sessions:\n")
		for _, s := range summaries {
			fmt.Fprintf(&sb, "  %s  %s  %s  %d msgs\n", s.ID, s.Model, s.Mode, s.MessageCount)
		}
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: sb.String()})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}
