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
	"sort"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/tui"
)

// actionModel surfaces the model picker dialog, then applies the selection
// to the current agent and chat provider config.
func actionModel(taskCtx context.Context, d *TaskDeps) {
	models, err := d.Client.Models(taskCtx)
	if err != nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("Failed to list models: %v", err)})
		d.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	names := make([]string, len(models))
	for i, m := range models {
		names[i] = m.Name
	}
	sort.Strings(names)
	resultCh := make(chan string, 1)
	d.ModelPickerCh <- tui.ModelPickerRequest{
		Models:  names,
		Current: d.Agent.Model(),
		Result:  resultCh,
	}
	selected := <-resultCh
	if selected != "" && selected != d.Agent.Model() {
		d.Agent.SetModel(selected)
		d.ChatProv.Model = selected
		d.Emit(&agent.Event{Type: agent.EventModelChange, Message: selected})
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Model switched to: %s", selected)})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}
