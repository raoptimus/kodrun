package agent

import (
	"testing"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/stretchr/testify/require"
)

func TestLooksLikeMarkdownPlan(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "empty",
			content: "",
			want:    false,
		},
		{
			name:    "short answer",
			content: "Yes, that is correct.",
			want:    false,
		},
		{
			name:    "russian plan headings",
			content: "АНАЛИЗ ТЕКУЩЕГО СОСТОЯНИЯ ПРОЕКТА\n\nНа основе предоставленной информации я проанализировал...\n\nПЛАН ИСПРАВЛЕНИЙ\n\n1. Создать composition root\n2. Переместить интерфейсы\n3. Исправить зависимости",
			want:    true,
		},
		{
			name:    "english implementation plan heading",
			content: "Here is my implementation plan for the refactor:\n\n1. Add new field to struct\n2. Update constructor\n3. Migrate callers",
			want:    true,
		},
		{
			name:    "three numbered items, no marker",
			content: "Looking at the code, here is what I would do:\n\n1. First, add the validator method.\n2. Second, wire it into the handler.\n3. Third, write a unit test for the new path.",
			want:    true,
		},
		{
			name:    "two numbered items only",
			content: "Looking at the code, here is what I would do:\n\n1. Add validator.\n2. Wire it.",
			want:    false,
		},
		{
			name:    "two markdown headers no code fence",
			content: "## Overview\n\nThis describes the architecture of the system in some detail so that the answer is not too short.\n\n## Details\n\nMore detail here so that the content is long enough to clear the threshold.",
			want:    true,
		},
		{
			name:    "headers with code fence (real answer)",
			content: "## Example\n\n```go\nfunc Bar() {}\n```\n\n## Note\n\nThe code above shows how to define an empty function.",
			want:    false,
		},
		{
			name:    "long single-line prose without lists",
			content: "The reason for choosing this approach is that it decouples the caller from the implementation detail and keeps the API surface area small, which matters for long-term maintenance and makes testing trivial.",
			want:    false,
		},
		{
			name:    "unicode bullets are not numbered items",
			content: "Here is a description of the design:\n\n• first consideration that is relevant\n• second consideration worth noting\n• third consideration to keep in mind\n\nOverall the approach should work fine.",
			want:    false,
		},
		{
			name:    "mixed code fence with headers is a real answer",
			content: "## Usage\n\nCall the function like so:\n\n```go\nDoThing(ctx, \"foo\")\n```\n\n## Notes\n\nIt returns an error on invalid input.",
			want:    false,
		},
		{
			name:    "long prose answer to a question",
			content: "The reason Go uses interfaces declared on the consumer side is that it decouples implementations from their abstractions. This makes it easier to write tests and to evolve the codebase over time without breaking existing callers.",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := looksLikeMarkdownPlan(tc.content)
			if got != tc.want {
				t.Errorf("looksLikeMarkdownPlan() = %v, want %v\ncontent:\n%s", got, tc.want, tc.content)
			}
		})
	}
}

func TestParseTextToolCall_EditFile_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCall *llm.ToolCall
	}{
		{
			name:    "standard edit_file with old_str and new_str",
			content: "edit_file\npath: internal/app/api/application.go\nold_str: type Application struct {\n    logger *logrus.Logger\n}\nnew_str: type Application struct {\n    logger *logrus.Logger\n    server *http.Server\n}",
			wantCall: &llm.ToolCall{
				ID: "synth_0",
				Function: llm.ToolCallFunc{
					Name: "edit_file",
					Arguments: map[string]any{
						"path":    "internal/app/api/application.go",
						"old_str": "type Application struct {\n    logger *logrus.Logger\n}",
						"new_str": "type Application struct {\n    logger *logrus.Logger\n    server *http.Server\n}",
					},
				},
			},
		},
		{
			name:    "edit_file with only new_str",
			content: "edit_file\npath: internal/app/handler.go\nnew_str: func Handle() error {\n    return nil\n}",
			wantCall: &llm.ToolCall{
				ID: "synth_0",
				Function: llm.ToolCallFunc{
					Name: "edit_file",
					Arguments: map[string]any{
						"path":    "internal/app/handler.go",
						"new_str": "func Handle() error {\n    return nil\n}",
					},
				},
			},
		},
		{
			name:    "edit_file with multiline indented values",
			content: "edit_file\npath: pkg/service/svc.go\nold_str: func (s *Service) Run() {\n\treturn\n}\nnew_str: func (s *Service) Run() error {\n\tif err := s.init(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}",
			wantCall: &llm.ToolCall{
				ID: "synth_0",
				Function: llm.ToolCallFunc{
					Name: "edit_file",
					Arguments: map[string]any{
						"path":    "pkg/service/svc.go",
						"old_str": "func (s *Service) Run() {\n\treturn\n}",
						"new_str": "func (s *Service) Run() error {\n\tif err := s.init(); err != nil {\n\t\treturn err\n\t}\n\treturn nil\n}",
					},
				},
			},
		},
		{
			name:    "edit_file preceded by text on earlier lines",
			content: "I will make the following change:\n\nedit_file\npath: main.go\nold_str: fmt.Println(\"hello\")\nnew_str: fmt.Println(\"world\")",
			wantCall: &llm.ToolCall{
				ID: "synth_0",
				Function: llm.ToolCallFunc{
					Name: "edit_file",
					Arguments: map[string]any{
						"path":    "main.go",
						"old_str": "fmt.Println(\"hello\")",
						"new_str": "fmt.Println(\"world\")",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTextToolCall(tt.content)
			require.NotNil(t, got)
			require.Equal(t, tt.wantCall, got)
		})
	}
}

func TestParseTextToolCall_WriteFile_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantCall *llm.ToolCall
	}{
		{
			name:    "standard write_file with content",
			content: "write_file\npath: internal/app/new_file.go\ncontent: package app\n\nfunc New() {}",
			wantCall: &llm.ToolCall{
				ID: "synth_0",
				Function: llm.ToolCallFunc{
					Name: "write_file",
					Arguments: map[string]any{
						"path":    "internal/app/new_file.go",
						"content": "package app\n\nfunc New() {}",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTextToolCall(tt.content)
			require.NotNil(t, got)
			require.Equal(t, tt.wantCall, got)
		})
	}
}

func TestParseTextToolCall_NoToolCallDetected_ReturnsNil(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "empty string",
			content: "",
		},
		{
			name:    "regular markdown text",
			content: "Here is an explanation of the code.\n\nThe function does X and Y.\n\n```go\nfmt.Println(\"example\")\n```",
		},
		{
			name:    "numbered plan without tool keywords",
			content: "1. First step\n2. Second step\n3. Third step",
		},
		{
			name:    "edit_file without path",
			content: "edit_file\nold_str: foo\nnew_str: bar",
		},
		{
			name:    "edit_file without old_str and new_str",
			content: "edit_file\npath: some/file.go",
		},
		{
			name:    "write_file without content",
			content: "write_file\npath: some/file.go",
		},
		{
			name:    "edit_file as substring in text",
			content: "You should use the edit_file tool to make changes.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTextToolCall(tt.content)
			require.Nil(t, got)
		})
	}
}

func TestExtractDiffFromText_EditFile_Successfully(t *testing.T) {
	content := "edit_file\npath: internal/app/api/app.go\nold_str: func Run() {\n}\nnew_str: func Run() error {\n    return nil\n}"

	got := ExtractDiffFromText(content)
	require.NotEmpty(t, got)
}

func TestExtractDiffFromText_NoEditFile_ReturnsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "write_file returns empty",
			content: "write_file\npath: internal/app/new_file.go\ncontent: package app",
		},
		{
			name:    "regular text returns empty",
			content: "This is just a regular response with no tool call.",
		},
		{
			name:    "empty string returns empty",
			content: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractDiffFromText(tt.content)
			require.Empty(t, got)
		})
	}
}
