package snippets

import (
	"reflect"
	"testing"
)

func TestMatchWithOpts(t *testing.T) {
	items := []Snippet{
		mustSnippet(t, "go-test-table", "table-driven test", []string{"go", "test"}, []string{"**/*_test.go"}),
		mustSnippet(t, "go-repo", "repository", []string{"go", "repository"}, []string{"**/repository/**/*.go"}),
	}

	out := MatchWithOpts(items, &MatchOpts{Paths: []string{"internal/foo/user_test.go"}})
	if len(out.Snippets) != 1 {
		t.Fatalf("expected 1 match, got %d", len(out.Snippets))
	}
	if out.Snippets[0].Name != "go-test-table" {
		t.Fatalf("got %q", out.Snippets[0].Name)
	}
}

func TestGroupByTags(t *testing.T) {
	items := []Snippet{
		mustSnippet(t, "a", "A", []string{"go", "test"}, nil),
		mustSnippet(t, "b", "B", []string{"go"}, nil),
	}

	groups := GroupByTags(items)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if !reflect.DeepEqual(groups[0], TagGroup{Tag: "go", Snippets: []string{"a", "b"}}) {
		t.Fatalf("unexpected first group: %#v", groups[0])
	}
}

func mustSnippet(t *testing.T, name, desc string, tags, paths []string) Snippet {
	t.Helper()
	data := `---
description: ` + desc + `
tags: [` + joinCSV(tags) + `]
paths: [` + joinQuoted(paths) + `]
---

## Body

` + "```go\npackage test\n```"

	s, err := parseSnippet(name+".md", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func joinCSV(items []string) string {
	return joinMapped(items, func(s string) string { return s })
}

func joinQuoted(items []string) string {
	return joinMapped(items, func(s string) string { return `"` + s + `"` })
}

func joinMapped(items []string, fn func(string) string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ", "
		}
		out += fn(item)
	}
	return out
}
