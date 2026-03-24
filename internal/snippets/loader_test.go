package snippets

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoaderLoad(t *testing.T) {
	dir := t.TempDir()
	snippetsDir := filepath.Join(dir, ".kodrun", "snippets")
	if err := os.MkdirAll(snippetsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
description: table-driven test template
tags: [go, test]
paths: ["**/*_test.go"]
related: [go-subtest]
placeholders:
  Entity: "PascalCase"
lang: go
---

## Test

` + "```go\nfunc TestThing(t *testing.T) {}\n```"

	if err := os.WriteFile(filepath.Join(snippetsDir, "go-test-table.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := NewLoader(dir)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	items := loader.Snippets()
	if len(items) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(items))
	}
	if items[0].Name != "go-test-table" {
		t.Fatalf("name = %q, want go-test-table", items[0].Name)
	}
	if !reflect.DeepEqual(items[0].Tags, []string{"go", "test"}) {
		t.Fatalf("tags = %#v", items[0].Tags)
	}
	if items[0].Description != "table-driven test template" {
		t.Fatalf("description = %q, want table-driven test template", items[0].Description)
	}
}

func TestParseSnippetMissingFrontmatter(t *testing.T) {
	_, err := parseSnippet("bad.md", []byte("no frontmatter"))
	if err == nil || !strings.Contains(err.Error(), "missing YAML frontmatter") {
		t.Fatalf("unexpected error: %v", err)
	}
}
