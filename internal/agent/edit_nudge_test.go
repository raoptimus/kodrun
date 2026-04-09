package agent

import "testing"

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
