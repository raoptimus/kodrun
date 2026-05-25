/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/raoptimus/kodrun/internal/cliapp"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/rules"
)

// builtinNames is the canonical sorted list of 14 built-in command names.
var builtinNames = []string{
	"add_doc",
	"clear",
	cliapp.CmdNameCodeReview,
	"compact",
	"diff",
	cliapp.CmdNameEdit,
	"exit",
	"init",
	"model",
	"orchestrate",
	"rag",
	"reindex",
	"resume",
	"sessions",
}

func init() {
	// Verify the constant assumption: builtinNames must already be sorted.
	if !sort.StringsAreSorted(builtinNames) {
		panic("builtinNames must be sorted")
	}
}

// newLoader creates a *rules.Loader rooted at the given directory and
// optionally calls Load(ctx) when load==true.
func newLoader(t *testing.T, workDir string, load bool) *rules.Loader {
	t.Helper()
	loader := rules.NewLoader(workDir, 1<<20)
	if load {
		require.NoError(t, loader.Load(context.Background()))
	}
	return loader
}

// writeCommandFile creates <workDir>/.kodrun/commands/<name>.md with the given
// frontmatter description and body.
func writeCommandFile(t *testing.T, workDir, name, description, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".kodrun", "commands")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := "---\ndescription: " + description + "\n---\n" + body
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644))
}

// TestBuildCommandItems_NoUserCommands verifies that exactly 14 built-in items
// are returned (sorted) when the loader has no user commands.
func TestBuildCommandItems_NoUserCommands(t *testing.T) {
	loader := newLoader(t, t.TempDir(), false)

	items := cliapp.BuildCommandItems(loader)

	require.Len(t, items, 14)

	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.Name
	}
	assert.Equal(t, builtinNames, names)
}

// TestBuildCommandItems_WithUserCommands verifies that user-defined commands are
// merged with built-ins and the result is sorted ascending by Name.
func TestBuildCommandItems_WithUserCommands(t *testing.T) {
	workDir := t.TempDir()
	writeCommandFile(t, workDir, "zfoo", "Z description", "zfoo template body")
	writeCommandFile(t, workDir, "abar", "A description", "abar template body")

	loader := newLoader(t, workDir, true)

	items := cliapp.BuildCommandItems(loader)

	require.Len(t, items, 16)

	names := make([]string, len(items))
	for i, item := range items {
		names[i] = item.Name
	}
	assert.True(t, sort.StringsAreSorted(names), "items must be sorted by Name: %v", names)

	// "abar" sorts before all built-ins that start with 'c' or later
	assert.Equal(t, "abar", names[0])
	// "zfoo" sorts after all built-ins
	assert.Equal(t, "zfoo", names[len(names)-1])
}

// TestBuildCommandItems_UserCommandTemplate verifies that Template field of a
// user-defined command item contains the body text (without frontmatter).
func TestBuildCommandItems_UserCommandTemplate(t *testing.T) {
	workDir := t.TempDir()
	writeCommandFile(t, workDir, "mymacro", "My macro description", "do something useful")

	loader := newLoader(t, workDir, true)

	items := cliapp.BuildCommandItems(loader)

	var found bool
	for _, item := range items {
		if item.Name == "mymacro" {
			found = true
			assert.Equal(t, "do something useful", item.Template)
			assert.Equal(t, "My macro description", item.Description)
			break
		}
	}
	require.True(t, found, "user command 'mymacro' not found in items")
}

// TestFormatContext_FormatContext tests the FormatContext function across
// multiple scenarios using table-driven tests.
func TestFormatContext_FormatContext(t *testing.T) {
	tests := []struct {
		name     string
		history  []llm.Message
		contains []string
		exact    string
	}{
		{
			name:    "empty history",
			history: []llm.Message{},
			exact:   "\nTotal messages: 0",
		},
		{
			name: "single user message",
			history: []llm.Message{
				{Role: "user", Content: "hello world"},
			},
			contains: []string{
				"[0] USER",
				"hello world",
				"Total messages: 1",
			},
		},
		{
			name: "single assistant message with tool call",
			history: []llm.Message{
				{
					Role:    "assistant",
					Content: "",
					ToolCalls: []llm.ToolCall{
						{
							ID: "tc-1",
							Function: llm.ToolCallFunc{
								Name:      "foo",
								Arguments: map[string]any{"x": 1},
							},
						},
					},
				},
			},
			contains: []string{
				"[0] ASSISTANT",
				"→ tool_call: foo(",
				"Total messages: 1",
			},
		},
		{
			name: "tool result message with ToolCallID",
			history: []llm.Message{
				{Role: "tool", Content: "result data", ToolCallID: "abc"},
			},
			contains: []string{
				"[0] TOOL",
				"result data",
				"tool_call_id: abc",
				"Total messages: 1",
			},
		},
		{
			name: "three mixed messages index check",
			history: []llm.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "second"},
				{Role: "tool", Content: "third", ToolCallID: "xyz"},
			},
			contains: []string{
				"[0] USER",
				"[1] ASSISTANT",
				"[2] TOOL",
				"Total messages: 3",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cliapp.FormatContext(tt.history)

			if tt.exact != "" {
				assert.Equal(t, tt.exact, result)
			}
			for _, want := range tt.contains {
				assert.True(t, strings.Contains(result, want),
					"expected result to contain %q, got:\n%s", want, result)
			}
		})
	}
}
