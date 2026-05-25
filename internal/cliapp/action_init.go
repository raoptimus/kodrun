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
	"fmt"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/kodruninit"
)

// actionInit scaffolds the .kodrun/ tree and AGENTS.md by delegating to the
// kodruninit package.
func actionInit(taskCtx context.Context, d *TaskDeps) {
	d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Scanning project and generating AGENTS.md..."})
	res, err := kodruninit.Run(taskCtx, d.Flags.WorkDir, d.Client, d.ChatProv.Model)
	if err != nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	} else {
		for _, path := range res.Created {
			d.Emit(&agent.Event{Type: agent.EventAgent, Message: "created " + path})
		}
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Done: %d items created", len(res.Created))})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}
