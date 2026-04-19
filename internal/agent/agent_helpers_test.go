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

func TestLangName_KnownCodes_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want string
	}{
		{name: "russian", code: "ru", want: "Russian"},
		{name: "english", code: "en", want: "English"},
		{name: "german", code: "de", want: "German"},
		{name: "french", code: "fr", want: "French"},
		{name: "spanish", code: "es", want: "Spanish"},
		{name: "chinese", code: "zh", want: "Chinese"},
		{name: "japanese", code: "ja", want: "Japanese"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := langName(tt.code)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLangName_EmptyCode_Successfully(t *testing.T) {
	t.Parallel()

	got := langName("")

	assert.Equal(t, "English", got)
}

func TestLangName_UnknownCode_Successfully(t *testing.T) {
	t.Parallel()

	got := langName("pt")

	assert.Equal(t, "pt", got)
}

func TestTruncate_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxRunes int
		want     string
	}{
		{name: "shorter than limit", input: "hello", maxRunes: 10, want: "hello"},
		{name: "equal to limit", input: "hello", maxRunes: 5, want: "hello"},
		{name: "longer than limit", input: "hello world", maxRunes: 5, want: "hello..."},
		{name: "empty string", input: "", maxRunes: 5, want: ""},
		{name: "unicode truncation", input: "Привет мир", maxRunes: 6, want: "Привет..."},
		{name: "limit 0", input: "hello", maxRunes: 0, want: "..."},
		{name: "limit 1", input: "hello", maxRunes: 1, want: "h..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncate(tt.input, tt.maxRunes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncateOneLine_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{name: "no newlines short", input: "hello", n: 10, want: "hello"},
		{name: "no newlines equal", input: "hello", n: 5, want: "hello"},
		{name: "no newlines truncated", input: "hello world", n: 5, want: "hello..."},
		{name: "newlines replaced", input: "line1\nline2", n: 20, want: "line1⏎line2"},
		{name: "newlines replaced and truncated", input: "line1\nline2\nline3", n: 10, want: "line1⏎li..."},
		{name: "empty", input: "", n: 5, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := truncateOneLine(tt.input, tt.n)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPreviewNewFile_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		content  string
		maxLines int
		want     string
	}{
		{
			name:     "fewer lines than limit",
			content:  "line1\nline2\nline3",
			maxLines: 5,
			want:     "line1\nline2\nline3",
		},
		{
			name:     "equal to limit",
			content:  "line1\nline2\nline3",
			maxLines: 3,
			want:     "line1\nline2\nline3",
		},
		{
			name:     "more lines than limit",
			content:  "line1\nline2\nline3\nline4\nline5",
			maxLines: 2,
			want:     "line1\nline2\n... (3 more lines)",
		},
		{
			name:     "single line",
			content:  "single",
			maxLines: 1,
			want:     "single",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := previewNewFile(tt.content, tt.maxLines)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDetail_FileTools_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		want     string
	}{
		{name: "read_file", toolName: "read_file", args: map[string]any{"path": "main.go"}, want: "main.go"},
		{name: "write_file", toolName: "write_file", args: map[string]any{"path": "new.go"}, want: "new.go"},
		{name: "edit_file", toolName: "edit_file", args: map[string]any{"path": "edit.go"}, want: "edit.go"},
		{name: "delete_file", toolName: "delete_file", args: map[string]any{"path": "old.go"}, want: "old.go"},
		{name: "list_dir", toolName: "list_dir", args: map[string]any{"path": "."}, want: "."},
		{name: "find_files", toolName: "find_files", args: map[string]any{"path": "internal"}, want: "internal"},
		{name: "create_dir", toolName: "create_dir", args: map[string]any{"path": "newdir"}, want: "newdir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toolDetail(tt.toolName, tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDetail_MoveFile_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("move_file", map[string]any{"from": "old.go", "to": "new.go"})

	assert.Equal(t, "old.go → new.go", got)
}

func TestToolDetail_Grep_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("grep", map[string]any{"pattern": "TODO", "path": "."})

	assert.Equal(t, `"TODO" in .`, got)
}

func TestToolDetail_GoTools_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		want     string
	}{
		{name: "go_build with packages", toolName: "go_build", args: map[string]any{"packages": "./cmd/..."}, want: "./cmd/..."},
		{name: "go_build without packages", toolName: "go_build", args: map[string]any{}, want: "./..."},
		{name: "go_test with packages", toolName: "go_test", args: map[string]any{"packages": "./internal/..."}, want: "./internal/..."},
		{name: "go_lint without packages", toolName: "go_lint", args: map[string]any{}, want: "./..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toolDetail(tt.toolName, tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDetail_Bash_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("bash", map[string]any{"command": "ls -la"})

	assert.Equal(t, "ls -la", got)
}

func TestToolDetail_BashTruncated_Successfully(t *testing.T) {
	t.Parallel()

	longCmd := "echo " + string(make([]byte, 200))
	got := toolDetail("bash", map[string]any{"command": longCmd})

	assert.Len(t, got, toolArgTruncateLen+3) // truncated + "..."
	assert.Contains(t, got, "...")
}

func TestToolDetail_Snippets_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "with action", args: map[string]any{"action": "list"}, want: "list"},
		{name: "without action", args: map[string]any{}, want: "match"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toolDetail("snippets", tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDetail_GitTools_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		args     map[string]any
		want     string
	}{
		{name: "git_status", toolName: "git_status", args: map[string]any{}, want: ""},
		{name: "git_diff with path", toolName: "git_diff", args: map[string]any{"path": "main.go"}, want: "main.go"},
		{name: "git_diff without path", toolName: "git_diff", args: map[string]any{}, want: ""},
		{name: "git_log with path", toolName: "git_log", args: map[string]any{"path": "."}, want: "."},
		{name: "git_commit with message", toolName: "git_commit", args: map[string]any{"message": "fix bug"}, want: "fix bug"},
		{name: "git_commit empty message", toolName: "git_commit", args: map[string]any{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toolDetail(tt.toolName, tt.args)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToolDetail_WebFetch_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("web_fetch", map[string]any{"url": "https://example.com"})

	assert.Equal(t, "https://example.com", got)
}

func TestToolDetail_GetRule_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("get_rule", map[string]any{"name": "service"})

	assert.Equal(t, "service", got)
}

func TestToolDetail_UnknownToolFallback_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("unknown_tool", map[string]any{"path": "/some/path"})

	assert.Equal(t, "/some/path", got)
}

func TestToolDetail_UnknownToolNoArgs_Successfully(t *testing.T) {
	t.Parallel()

	got := toolDetail("unknown_tool", map[string]any{})

	assert.Equal(t, "", got)
}

func TestStringFromMap_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{name: "key exists", m: map[string]any{"key": "value"}, key: "key", want: "value"},
		{name: "key missing", m: map[string]any{"other": "value"}, key: "key", want: ""},
		{name: "key wrong type", m: map[string]any{"key": 42}, key: "key", want: ""},
		{name: "nil map", m: nil, key: "key", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stringFromMap(tt.m, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMode_String_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode Mode
		want string
	}{
		{name: "plan mode", mode: ModePlan, want: "plan"},
		{name: "edit mode", mode: ModeEdit, want: "edit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.mode.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSessionStats_RecordFileAction_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		action         string
		path           string
		added          int
		removed        int
		wantAdded      int
		wantModified   int
		wantDeleted    int
		wantRenamed    int
		wantLinesAdded int
		wantLinesRemov int
		wantFiles      []string
	}{
		{
			name: "add action", action: "Add", path: "new.go",
			added: 50, removed: 0,
			wantAdded: 1, wantLinesAdded: 50, wantFiles: []string{"new.go"},
		},
		{
			name: "update action", action: "Update", path: "main.go",
			added: 10, removed: 5,
			wantModified: 1, wantLinesAdded: 10, wantLinesRemov: 5, wantFiles: []string{"main.go"},
		},
		{
			name: "delete action", action: "Delete", path: "old.go",
			added: 0, removed: 30,
			wantDeleted: 1, wantLinesRemov: 30, wantFiles: []string{"old.go"},
		},
		{
			name: "rename action", action: "Rename", path: "renamed.go",
			added: 0, removed: 0,
			wantRenamed: 1, wantFiles: []string{"renamed.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var s SessionStats
			s.recordFileAction(tt.action, tt.path, tt.added, tt.removed)

			assert.Equal(t, tt.wantAdded, s.FilesAdded)
			assert.Equal(t, tt.wantModified, s.FilesModified)
			assert.Equal(t, tt.wantDeleted, s.FilesDeleted)
			assert.Equal(t, tt.wantRenamed, s.FilesRenamed)
			assert.Equal(t, tt.wantLinesAdded, s.LinesAdded)
			assert.Equal(t, tt.wantLinesRemov, s.LinesRemoved)
			assert.Equal(t, tt.wantFiles, s.ChangedFiles)
		})
	}
}

func TestSessionStats_RecordFileAction_EmptyPath_Successfully(t *testing.T) {
	t.Parallel()

	var s SessionStats
	s.recordFileAction("Add", "", 10, 0)

	assert.Equal(t, 1, s.FilesAdded)
	assert.Nil(t, s.ChangedFiles)
}

func TestSessionStats_RecordTokens_Successfully(t *testing.T) {
	t.Parallel()

	var s SessionStats

	s.recordTokens(100, 50, 25.0, 30)
	s.recordTokens(200, 80, 35.0, 60)

	assert.Equal(t, 300, s.TotalPrompt)
	assert.Equal(t, 130, s.TotalEval)
	assert.Equal(t, 60, s.PeakContextPct)
}

func TestSessionStats_RecordTokens_ZeroTkPerSec_Successfully(t *testing.T) {
	t.Parallel()

	var s SessionStats

	s.recordTokens(100, 50, 0, 10)
	s.recordTokens(200, 80, 30.0, 20)

	// Only the non-zero tkPerSec should be counted.
	assert.InDelta(t, 30.0, s.avgTkPerSec(), 0.001)
}

func TestSessionStats_AvgTkPerSec_NoRecords_Successfully(t *testing.T) {
	t.Parallel()

	var s SessionStats

	assert.Equal(t, 0.0, s.avgTkPerSec())
}

func TestSessionStats_AvgTkPerSec_Successfully(t *testing.T) {
	t.Parallel()

	var s SessionStats

	s.recordTokens(0, 0, 20.0, 0)
	s.recordTokens(0, 0, 40.0, 0)

	assert.InDelta(t, 30.0, s.avgTkPerSec(), 0.001)
}

func TestSessionStats_Reset_Successfully(t *testing.T) {
	t.Parallel()

	s := SessionStats{
		FilesAdded:    5,
		FilesModified: 3,
		TotalPrompt:   1000,
		ChangedFiles:  []string{"a.go"},
	}

	s.reset()

	assert.Equal(t, SessionStats{}, s)
}
