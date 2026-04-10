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

// ConfirmPayload describes a tool call awaiting user confirmation.
// It carries everything the UI needs to render an informative confirmation card:
// the tool name, the relevant arguments (already filtered for display), an
// optional preview (unified diff for edit/write, command body for bash, etc.),
// and the danger flag for visually-flagged operations.
type ConfirmPayload struct {
	Tool    string
	Args    map[string]string // ordered key→value pairs for display
	ArgKeys []string          // preserve insertion order across map iterations
	Preview string            // unified diff or other multi-line preview
	Danger  bool
}

// Detail returns a short single-line summary of the payload, used as a
// fingerprint-friendly description and as a fallback for compact UIs.
func (p ConfirmPayload) Detail() string {
	switch p.Tool {
	case toolNameMoveFile:
		return p.Args["from"] + " → " + p.Args["to"]
	case toolNameBash:
		return p.Args["command"]
	}
	if v, ok := p.Args["path"]; ok {
		return v
	}
	for _, k := range p.ArgKeys {
		return p.Args[k]
	}
	return p.Tool
}

// ConfirmFunc asks user to confirm a destructive operation.
// Returns ConfirmResult with the user's choice.
type ConfirmFunc func(payload ConfirmPayload) ConfirmResult

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

// StepConfirmAction represents the user's decision on a single step.
type StepConfirmAction int

const (
	StepAllow   StepConfirmAction = iota // Execute this step
	StepSkip                             // Skip this step, continue with next
	StepDenyAll                          // Cancel all remaining steps
)

// StepConfirmFunc asks user to confirm execution of a single plan step.
// The description contains step title, files, action, and rationale.
type StepConfirmFunc func(description string) StepConfirmAction

// Fingerprint computes a deterministic key for a tool call.
func Fingerprint(tool string, args map[string]any) string {
	switch tool {
	case toolNameBash:
		cmd := stringFromMap(args, "command")
		if len(cmd) > maxFingerprintLen {
			cmd = cmd[:maxFingerprintLen]
		}
		return fmt.Sprintf("bash:%s", cmd)
	case toolNameMoveFile:
		from := stringFromMap(args, "from")
		to := stringFromMap(args, "to")
		return fmt.Sprintf("move_file:%s->%s", from, to)
	default:
		// Per-tool fingerprint: ConfirmAllowSession должен покрывать команду
		// целиком, а не отдельный path. Иначе каждый новый файл вызывает
		// повторный диалог подтверждения.
		_ = args
		return tool
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
