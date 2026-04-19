/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import "testing"

func TestFingerprint(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{
			name: "write_file is per-tool (path-independent)",
			tool: "write_file",
			args: map[string]any{"path": "main.go"},
			want: "write_file",
		},
		{
			name: "edit_file is per-tool (path-independent)",
			tool: "edit_file",
			args: map[string]any{"path": "internal/agent/agent.go"},
			want: "edit_file",
		},
		{
			name: "delete_file is per-tool (path-independent)",
			tool: "delete_file",
			args: map[string]any{"path": "tmp.go"},
			want: "delete_file",
		},
		{
			name: "move_file",
			tool: "move_file",
			args: map[string]any{"from": "old.go", "to": "new.go"},
			want: "move_file:old.go->new.go",
		},
		{
			name: "bash short command",
			tool: "bash",
			args: map[string]any{"command": "go build ./..."},
			want: "bash:go build ./...",
		},
		{
			name: "bash long command truncated",
			tool: "bash",
			args: map[string]any{"command": "echo " + string(make([]byte, 100))},
			want: "bash:" + ("echo " + string(make([]byte, 100)))[:80],
		},
		{
			name: "missing path",
			tool: "write_file",
			args: map[string]any{},
			want: "write_file",
		},
		{
			name: "edit_file fingerprint independent of path: a vs b",
			tool: "edit_file",
			args: map[string]any{"path": "b.go"},
			want: "edit_file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Fingerprint(tt.tool, tt.args)
			if got != tt.want {
				t.Errorf("Fingerprint(%q, %v) = %q, want %q", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestPermissionManager_AllowSession(t *testing.T) {
	pm := NewPermissionManager()

	fp := "write_file:main.go"

	if pm.IsAllowed(fp) {
		t.Fatal("expected not allowed before AllowSession")
	}

	pm.AllowSession(fp)

	if !pm.IsAllowed(fp) {
		t.Fatal("expected allowed after AllowSession")
	}

	// Different fingerprint should not be allowed
	if pm.IsAllowed("write_file:other.go") {
		t.Fatal("expected different fingerprint not allowed")
	}
}

func TestPermissionManager_Reset(t *testing.T) {
	pm := NewPermissionManager()

	fp := "bash:go test ./..."
	pm.AllowSession(fp)

	if !pm.IsAllowed(fp) {
		t.Fatal("expected allowed before reset")
	}

	pm.Reset()

	if pm.IsAllowed(fp) {
		t.Fatal("expected not allowed after reset")
	}
}
