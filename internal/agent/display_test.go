/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToolDisplayName_KnownTools_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{name: "read_file", toolName: "read_file", want: "Read"},
		{name: "write_file", toolName: "write_file", want: "Write"},
		{name: "edit_file", toolName: "edit_file", want: "Edit"},
		{name: "delete_file", toolName: "delete_file", want: "Delete"},
		{name: "list_dir", toolName: "list_dir", want: "ListDir"},
		{name: "find_files", toolName: "find_files", want: "Find"},
		{name: "create_dir", toolName: "create_dir", want: "CreateDir"},
		{name: "move_file", toolName: "move_file", want: "Move"},
		{name: "grep", toolName: "grep", want: "Grep"},
		{name: "go_build", toolName: "go_build", want: "Build"},
		{name: "go_test", toolName: "go_test", want: "Test"},
		{name: "go_lint", toolName: "go_lint", want: "Lint"},
		{name: "go_vet", toolName: "go_vet", want: "Vet"},
		{name: "go_doc", toolName: "go_doc", want: "GoDoc"},
		{name: "go_structure", toolName: "go_structure", want: "Structure"},
		{name: "search_docs", toolName: "search_docs", want: "SearchDocs"},
		{name: "bash", toolName: "bash", want: "Bash"},
		{name: "snippets", toolName: "snippets", want: "Snippets"},
		{name: "git_status", toolName: "git_status", want: "GitStatus"},
		{name: "git_diff", toolName: "git_diff", want: "GitDiff"},
		{name: "git_log", toolName: "git_log", want: "GitLog"},
		{name: "git_commit", toolName: "git_commit", want: "GitCommit"},
		{name: "get_rule", toolName: "get_rule", want: "GetRule"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ToolDisplayName(tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDisplayName_UnknownToolUsesSnakeToPascal_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		want     string
	}{
		{name: "single word", toolName: "something", want: "Something"},
		{name: "two words", toolName: "my_tool", want: "MyTool"},
		{name: "three words", toolName: "my_custom_tool", want: "MyCustomTool"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ToolDisplayName(tt.toolName)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSnakeToPascal_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty string", input: "", want: ""},
		{name: "single word", input: "hello", want: "Hello"},
		{name: "two words", input: "hello_world", want: "HelloWorld"},
		{name: "already capitalized", input: "Hello_World", want: "HelloWorld"},
		{name: "trailing underscore", input: "hello_", want: "Hello"},
		{name: "leading underscore", input: "_hello", want: "Hello"},
		{name: "consecutive underscores", input: "a__b", want: "AB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := snakeToPascal(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
