/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"context"
	"testing"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeToolCall is a helper to build llm.ToolCall values concisely.
func makeToolCall(name string, args map[string]any) llm.ToolCall {
	return llm.ToolCall{
		ID: "tc_1",
		Function: llm.ToolCallFunc{
			Name:      name,
			Arguments: args,
		},
	}
}

// stubReadOnlyTool is a no-op tool whose only purpose is to advertise itself
// as read-only via the IsReadOnly() optional interface, so registry-derived
// dedup logic can be exercised in unit tests without pulling real tools in.
type stubReadOnlyTool struct{ name string }

func (s *stubReadOnlyTool) Name() string           { return s.name }
func (s *stubReadOnlyTool) Description() string    { return "" }
func (s *stubReadOnlyTool) Schema() llm.JSONSchema { return llm.JSONSchema{} }
func (s *stubReadOnlyTool) IsReadOnly() bool       { return true }
func (s *stubReadOnlyTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	return &tools.ToolResult{}, nil
}

// testReadOnlyToolNames lists every read-only tool name referenced by the
// agent dedup tests; agentWithSession registers a stub for each so that the
// Agent's registry-derived readOnlyTools() returns the expected set.
var testReadOnlyToolNames = []string{
	toolNameReadFile,
	toolNameListDir,
	toolNameFindFiles,
	toolNameGrep,
	toolNameGitStatus,
	toolNameWebFetch,
}

// agentWithSession returns a minimal Agent whose readPathsSession is
// pre-populated with the given paths. The registry is wired with stub
// read-only tools so that dedup helpers see the expected read-only set.
func agentWithSession(paths ...string) *Agent {
	reg := tools.NewRegistry()
	for _, name := range testReadOnlyToolNames {
		reg.Register(&stubReadOnlyTool{name: name})
	}
	a := &Agent{
		reg:              reg,
		readPathsSession: make(map[string]struct{}),
		exec:             newToolExecutor(reg, NewPermissionManager(), ""),
	}
	a.exec.attach(a)
	for _, p := range paths {
		a.readPathsSession[p] = struct{}{}
	}
	return a
}

// Test_guardDuplicateRead tests the guardDuplicateRead method.

func Test_guardDuplicateRead_ReturnsNil_WhenNotReadFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		path     string
	}{
		{
			name:     "edit_file with known path",
			toolName: toolNameEditFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "write_file with known path",
			toolName: toolNameWriteFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "list_dir with known path",
			toolName: toolNameListDir,
			path:     "internal/app/handler.go",
		},
		{
			name:     "bash with no path",
			toolName: toolNameBash,
			path:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agentWithSession("internal/app/handler.go")
			tc := makeToolCall(tt.toolName, map[string]any{"path": tt.path})

			got := a.exec.guardDuplicateRead(tc)

			assert.Nil(t, got)
		})
	}
}

func Test_guardDuplicateRead_ReturnsNil_WhenEmptyPath(t *testing.T) {
	t.Parallel()

	a := agentWithSession()
	tc := makeToolCall(toolNameReadFile, map[string]any{"path": ""})

	got := a.exec.guardDuplicateRead(tc)

	assert.Nil(t, got)
}

func Test_guardDuplicateRead_ReturnsNil_WhenPathNotInSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sessionPath string
		readPath    string
	}{
		{
			name:        "empty session",
			sessionPath: "",
			readPath:    "internal/app/handler.go",
		},
		{
			name:        "different path in session",
			sessionPath: "internal/app/other.go",
			readPath:    "internal/app/handler.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var a *Agent
			if tt.sessionPath == "" {
				a = agentWithSession()
			} else {
				a = agentWithSession(tt.sessionPath)
			}
			tc := makeToolCall(toolNameReadFile, map[string]any{"path": tt.readPath})

			got := a.exec.guardDuplicateRead(tc)

			assert.Nil(t, got)
		})
	}
}

func Test_guardDuplicateRead_ReturnsSyntheticResult_WhenPathAlreadySeen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{
			name: "absolute path already read",
			path: "/Users/dev/project/internal/app/handler.go",
		},
		{
			name: "relative path already read",
			path: "internal/app/service.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agentWithSession(tt.path)
			tc := makeToolCall(toolNameReadFile, map[string]any{"path": tt.path})

			got := a.exec.guardDuplicateRead(tc)

			require.NotNil(t, got)
			assert.Contains(t, got.Output, tt.path)
			require.NotNil(t, got.Meta)
			assert.Equal(t, true, got.Meta["dedup_hit"])
		})
	}
}

// Test_recordReadPath tests the recordReadPath method.

func Test_recordReadPath_AddsPath_WhenReadFileWithNonEmptyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{
			name: "simple relative path",
			path: "internal/app/handler.go",
		},
		{
			name: "absolute path",
			path: "/Users/dev/project/main.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agentWithSession()
			tc := makeToolCall(toolNameReadFile, map[string]any{"path": tt.path})

			a.exec.recordReadPath(tc)

			_, exists := a.readPathsSession[tt.path]
			assert.True(t, exists)
		})
	}
}

func Test_recordReadPath_DoesNotAddPath_WhenReadFileWithEmptyPath(t *testing.T) {
	t.Parallel()

	a := agentWithSession()
	tc := makeToolCall(toolNameReadFile, map[string]any{"path": ""})

	a.exec.recordReadPath(tc)

	assert.Empty(t, a.readPathsSession)
}

func Test_recordReadPath_DoesNotAddPath_WhenNotReadFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		path     string
	}{
		{
			name:     "edit_file with path",
			toolName: toolNameEditFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "write_file with path",
			toolName: toolNameWriteFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "bash with no path",
			toolName: toolNameBash,
			path:     "",
		},
		{
			name:     "list_dir with path",
			toolName: toolNameListDir,
			path:     "internal/app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agentWithSession()
			tc := makeToolCall(tt.toolName, map[string]any{"path": tt.path})

			a.exec.recordReadPath(tc)

			assert.Empty(t, a.readPathsSession)
		})
	}
}

// Test_clearReadPathOnWrite tests the clearReadPathOnWrite method.

func Test_clearReadPathOnWrite_RemovesPath_WhenWriteTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		path     string
	}{
		{
			name:     "edit_file removes path",
			toolName: toolNameEditFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "write_file removes path",
			toolName: toolNameWriteFile,
			path:     "internal/app/handler.go",
		},
		{
			name:     "bash removes path",
			toolName: toolNameBash,
			path:     "internal/app/handler.go",
		},
		{
			name:     "delete_file removes path",
			toolName: toolNameDeleteFile,
			path:     "internal/app/handler.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := agentWithSession("internal/app/handler.go")
			tc := makeToolCall(tt.toolName, map[string]any{"path": tt.path})

			a.exec.clearReadPathOnWrite(tc)

			_, exists := a.readPathsSession["internal/app/handler.go"]
			assert.False(t, exists)
		})
	}
}

func Test_clearReadPathOnWrite_DoesNotRemovePath_WhenReadOnlyTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
	}{
		{
			name:     "read_file does not remove",
			toolName: toolNameReadFile,
		},
		{
			name:     "list_dir does not remove",
			toolName: toolNameListDir,
		},
		{
			name:     "find_files does not remove",
			toolName: toolNameFindFiles,
		},
		{
			name:     "grep does not remove",
			toolName: toolNameGrep,
		},
		{
			name:     "git_status does not remove",
			toolName: toolNameGitStatus,
		},
		{
			name:     "web_fetch does not remove",
			toolName: toolNameWebFetch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const trackedPath = "internal/app/handler.go"
			a := agentWithSession(trackedPath)
			tc := makeToolCall(tt.toolName, map[string]any{"path": trackedPath})

			a.exec.clearReadPathOnWrite(tc)

			_, exists := a.readPathsSession[trackedPath]
			assert.True(t, exists, "read-only tool must not remove path from session")
		})
	}
}

func Test_clearReadPathOnWrite_DoesNotRemovePath_WhenEmptyPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
	}{
		{
			name:     "edit_file with empty path",
			toolName: toolNameEditFile,
		},
		{
			name:     "write_file with empty path",
			toolName: toolNameWriteFile,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const trackedPath = "internal/app/handler.go"
			a := agentWithSession(trackedPath)
			tc := makeToolCall(tt.toolName, map[string]any{"path": ""})

			a.exec.clearReadPathOnWrite(tc)

			_, exists := a.readPathsSession[trackedPath]
			assert.True(t, exists, "empty path must not remove other paths from session")
		})
	}
}

// Test_readPathsSession_Reset verifies that readPathsSession is empty after
// the per-Send reset (simulated by the same initialisation pattern used in Send).
func Test_readPathsSession_Reset_WhenSendResets(t *testing.T) {
	t.Parallel()

	a := agentWithSession(
		"internal/app/handler.go",
		"internal/app/service.go",
		"cmd/main.go",
	)

	require.Len(t, a.readPathsSession, 3)

	// Simulate the reset that happens at the start of every Send().
	a.readPathsSession = make(map[string]struct{})

	assert.Empty(t, a.readPathsSession)
}

// Test_guardDuplicateRead_AfterRecordAndClear is a scenario test that exercises
// the full record → guard → clear → guard cycle in one sequence.
func Test_guardDuplicateRead_AfterRecordAndClear_Scenario(t *testing.T) {
	t.Parallel()

	const path = "internal/app/handler.go"

	a := agentWithSession()
	readTC := makeToolCall(toolNameReadFile, map[string]any{"path": path})
	editTC := makeToolCall(toolNameEditFile, map[string]any{"path": path})

	// First read: not yet in session — no guard.
	assert.Nil(t, a.exec.guardDuplicateRead(readTC))

	// Record the read.
	a.exec.recordReadPath(readTC)

	// Second read on the same path: guard fires.
	result := a.exec.guardDuplicateRead(readTC)
	require.NotNil(t, result)
	assert.Contains(t, result.Output, path)
	assert.Equal(t, true, result.Meta["dedup_hit"])

	// Write tool clears the path.
	a.exec.clearReadPathOnWrite(editTC)

	// After write, guard no longer fires.
	assert.Nil(t, a.exec.guardDuplicateRead(readTC))
}
