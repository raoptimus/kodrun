/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import "github.com/raoptimus/kodrun/internal/tools"

// readOnlyToolsFor returns the set of read-only tool names registered with reg.
// Tools advertise read-only status by implementing tools.ReadOnlyMarker;
// anything else is considered a write tool.
func readOnlyToolsFor(reg *tools.Registry) map[string]bool {
	return reg.ReadOnlyTools()
}

// Mode represents the agent operating mode.
type Mode int

const (
	// ModePlan is read-only analysis mode.
	ModePlan Mode = iota
	// ModeEdit is full tool access mode.
	ModeEdit
	// ModeChat is free-form discussion mode with read-only tool access.
	ModeChat
)

const modeChatStr = "chat"

// String returns the mode name.
func (m Mode) String() string {
	switch m {
	case ModePlan:
		return string(ClassifyKindPlan)
	case ModeChat:
		return modeChatStr
	default:
		return "edit"
	}
}
