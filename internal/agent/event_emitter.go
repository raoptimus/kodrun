/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

// eventEmitter ships agent lifecycle events to a handler. It owns the optional
// TUI group id so sub-agents can stamp every event without each call site
// having to know about groups.
type eventEmitter struct {
	handler EventHandler
	groupID string
}

// SetHandler installs the handler invoked for every emitted event. Pass nil
// to silence emission.
func (e *eventEmitter) SetHandler(h EventHandler) {
	e.handler = h
}

// SetGroupID associates every event emitted by this emitter with the given
// TUI collapsible group. Call sites can still override by setting GroupID on
// the event explicitly before emit.
func (e *eventEmitter) SetGroupID(id string) {
	e.groupID = id
}

// emit dispatches the event to the handler, stamping the configured group id
// when the event does not already carry one. Safe to call when no handler is
// configured — drops the event silently.
func (e *eventEmitter) emit(ev *Event) {
	if e.handler == nil {
		return
	}
	if ev.GroupID == "" && e.groupID != "" {
		ev.GroupID = e.groupID
	}
	e.handler(ev)
}
