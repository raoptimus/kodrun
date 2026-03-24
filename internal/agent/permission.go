package agent

import (
	"fmt"
	"sync"
)

// ConfirmAction represents the user's decision on a tool confirmation.
type ConfirmAction int

const (
	ConfirmDeny         ConfirmAction = iota // Deny this operation
	ConfirmAllowOnce                         // Allow once
	ConfirmAllowSession                      // Allow this fingerprint for the rest of the session
	ConfirmAugment                           // User provides text constraint; agent must rebuild the tool call
)

// ConfirmResult carries the user's decision and optional augment text.
type ConfirmResult struct {
	Action  ConfirmAction
	Augment string // Non-empty only when Action == ConfirmAugment
}

// ConfirmFunc asks user to confirm a destructive operation.
// Returns ConfirmResult with the user's choice.
type ConfirmFunc func(tool string, detail string) ConfirmResult

// PlanConfirmAction represents the user's decision on a plan execution.
type PlanConfirmAction int

const (
	PlanDeny          PlanConfirmAction = iota // Cancel execution
	PlanAutoAccept                             // Execute plan, auto-accept all edits
	PlanManualApprove                          // Execute plan, ask confirmation for each edit
	PlanAugment                                // User provides feedback to revise the plan
)

// PlanConfirmResult carries the user's decision on plan execution.
type PlanConfirmResult struct {
	Action  PlanConfirmAction
	Augment string // Non-empty only when Action == PlanAugment
}

// PlanConfirmFunc asks user to confirm plan execution.
type PlanConfirmFunc func(plan string) PlanConfirmResult

// Fingerprint computes a deterministic key for a tool call.
func Fingerprint(tool string, args map[string]any) string {
	switch tool {
	case "bash":
		cmd, _ := args["command"].(string)
		if len(cmd) > 80 {
			cmd = cmd[:80]
		}
		return fmt.Sprintf("bash:%s", cmd)
	case "move_file":
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		return fmt.Sprintf("move_file:%s->%s", from, to)
	default:
		path, _ := args["path"].(string)
		return fmt.Sprintf("%s:%s", tool, path)
	}
}

// PermissionManager tracks session-level permissions for tool calls.
type PermissionManager struct {
	mu      sync.RWMutex
	allowed map[string]bool
}

// NewPermissionManager creates a new PermissionManager.
func NewPermissionManager() *PermissionManager {
	return &PermissionManager{allowed: make(map[string]bool)}
}

// IsAllowed returns true if this fingerprint was granted session permission.
func (pm *PermissionManager) IsAllowed(fingerprint string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.allowed[fingerprint]
}

// AllowSession grants session-wide permission for this fingerprint.
func (pm *PermissionManager) AllowSession(fingerprint string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.allowed[fingerprint] = true
}

// Reset clears all session permissions.
func (pm *PermissionManager) Reset() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.allowed = make(map[string]bool)
}
