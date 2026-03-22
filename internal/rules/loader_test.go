package rules

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoader_Load(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	os.MkdirAll(rulesDir, 0o755)

	content := `---
priority: high
scope: coding
---

# Style Rules

- Use errors.Is
`
	os.WriteFile(filepath.Join(rulesDir, "style.md"), []byte(content), 0o644)

	loader := NewLoader([]string{rulesDir})
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}

	rules := loader.Rules(ScopeCoding)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Priority != PriorityHigh {
		t.Errorf("priority = %d, want %d", rules[0].Priority, PriorityHigh)
	}
}

func TestLoader_ScopeFilter(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "rules")
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(rulesDir, "coding.md"), []byte("---\nscope: coding\n---\ncoding rule"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "review.md"), []byte("---\nscope: review\n---\nreview rule"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "all.md"), []byte("---\nscope: all\n---\nall rule"), 0o644)

	loader := NewLoader([]string{rulesDir})
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}

	codingRules := loader.Rules(ScopeCoding)
	if len(codingRules) != 2 { // coding + all
		t.Errorf("expected 2 coding rules, got %d", len(codingRules))
	}

	reviewRules := loader.Rules(ScopeReview)
	if len(reviewRules) != 2 { // review + all
		t.Errorf("expected 2 review rules, got %d", len(reviewRules))
	}
}

func TestLoader_Commands(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, "commands")
	os.MkdirAll(cmdDir, 0o755)

	content := `---
command: /review
description: "Review a file"
---

Review {{file}} for issues.
`
	os.WriteFile(filepath.Join(cmdDir, "review.md"), []byte(content), 0o644)

	loader := NewLoader([]string{cmdDir})
	if err := loader.Load(); err != nil {
		t.Fatal(err)
	}

	cmd, ok := loader.GetCommand("review")
	if !ok {
		t.Fatal("expected to find review command")
	}
	if cmd.Description != "Review a file" {
		t.Errorf("description = %q, want %q", cmd.Description, "Review a file")
	}
}

func TestLoader_MissingDir(t *testing.T) {
	loader := NewLoader([]string{"/nonexistent/path"})
	if err := loader.Load(); err != nil {
		t.Fatalf("should not error on missing dir: %v", err)
	}
}
