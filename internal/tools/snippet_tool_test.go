package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/raoptimus/kodrun/internal/snippets"
)

func TestSnippetToolListAndMatch(t *testing.T) {
	dir := t.TempDir()
	snippetsDir := filepath.Join(dir, ".kodrun", "snippets")
	if err := os.MkdirAll(snippetsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	content := `---
description: repo template
tags: [go, repository]
paths: ["**/repository/**/*.go"]
---

## Repository

` + "```go\npackage repository\n```"
	if err := os.WriteFile(filepath.Join(snippetsDir, "go-repository.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := snippets.NewLoader(dir)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	tool := NewSnippetTool(loader)

	listRes, err := tool.Execute(t.Context(), map[string]any{"action": "list"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listRes.Output, "go-repository") {
		t.Fatalf("unexpected list result: %#v", listRes)
	}

	matchRes, err := tool.Execute(t.Context(), map[string]any{
		"action": "match",
		"paths":  []any{"internal/repository/user.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(matchRes.Output, "go-repository") {
		t.Fatalf("unexpected match result: %#v", matchRes)
	}
}
