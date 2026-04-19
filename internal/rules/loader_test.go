/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoader_Load(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)

	content := `---
priority: high
scope: coding
---

# Style Rules

- Use errors.Is
`
	os.WriteFile(filepath.Join(rulesDir, "style.md"), []byte(content), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	rules := loader.Rules(context.Background(), ScopeCoding)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Priority != PriorityHigh {
		t.Errorf("priority = %d, want %d", rules[0].Priority, PriorityHigh)
	}
}

func TestLoader_ScopeFilter(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)

	os.WriteFile(filepath.Join(rulesDir, "coding.md"), []byte("---\nscope: coding\n---\ncoding rule"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "review.md"), []byte("---\nscope: review\n---\nreview rule"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "all.md"), []byte("---\nscope: all\n---\nall rule"), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	codingRules := loader.Rules(context.Background(), ScopeCoding)
	if len(codingRules) != 2 { // coding + all
		t.Errorf("expected 2 coding rules, got %d", len(codingRules))
	}

	reviewRules := loader.Rules(context.Background(), ScopeReview)
	if len(reviewRules) != 2 { // review + all
		t.Errorf("expected 2 review rules, got %d", len(reviewRules))
	}
}

func TestLoader_Commands(t *testing.T) {
	dir := t.TempDir()
	cmdDir := filepath.Join(dir, CommandsDir)
	os.MkdirAll(cmdDir, 0o755)

	content := `---
command: /review
description: "Review a file"
---

Review {{file}} for issues.
`
	os.WriteFile(filepath.Join(cmdDir, "review.md"), []byte(content), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
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

func TestLoader_ResolveReferences(t *testing.T) {
	dir := t.TempDir()

	// Create .kodrun/docs/ with an md and go file
	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "model.md"), []byte("# Model\nUse value objects."), 0o644)
	os.WriteFile(filepath.Join(docsDir, "example_model.go"), []byte("package model\n\ntype User struct{}"), 0o644)

	// Rules reference docs via @.kodrun/docs/ (the only supported root).
	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	ruleContent := "---\nscope: coding\n---\nСоглашения: @.kodrun/docs/model.md\nПример: @.kodrun/docs/example_model.go"
	os.WriteFile(filepath.Join(rulesDir, "model.md"), []byte(ruleContent), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	rules := loader.Rules(context.Background(), ScopeCoding)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	content := rules[0].Content
	// Should contain [see: X] labels instead of inline content
	if !strings.Contains(content, "[see: model.md]") {
		t.Errorf("expected [see: model.md] label, got:\n%s", content)
	}
	if !strings.Contains(content, "[see: example_model.go]") {
		t.Errorf("expected [see: example_model.go] label, got:\n%s", content)
	}
	// Should NOT contain raw @references
	if strings.Contains(content, "@.kodrun/docs/model.md") {
		t.Errorf("expected @reference to be resolved, got:\n%s", content)
	}
	// Should NOT contain inline doc content
	if strings.Contains(content, "Use value objects") {
		t.Errorf("rule should not contain inline doc content, got:\n%s", content)
	}

	// ReferenceDocs should contain the actual content
	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if !strings.Contains(refDocs, "Use value objects") {
		t.Errorf("ReferenceDocs should contain model.md content, got:\n%s", refDocs)
	}
	if !strings.Contains(refDocs, "type User struct{}") {
		t.Errorf("ReferenceDocs should contain example_model.go content, got:\n%s", refDocs)
	}
}

func TestLoader_ResolveReferences_DirectPath(t *testing.T) {
	dir := t.TempDir()

	// Create .kodrun/docs/ with a doc
	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "style.md"), []byte("# Style\nMax 120 chars."), 0o644)

	// Rule referencing via .kodrun/ path directly
	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	ruleContent := "---\nscope: coding\n---\nСтиль: @.kodrun/docs/style.md"
	os.WriteFile(filepath.Join(rulesDir, "style.md"), []byte(ruleContent), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	rules := loader.Rules(context.Background(), ScopeCoding)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if !strings.Contains(rules[0].Content, "[see: style.md]") {
		t.Errorf("expected [see: style.md] label, got:\n%s", rules[0].Content)
	}

	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if !strings.Contains(refDocs, "Max 120 chars") {
		t.Errorf("ReferenceDocs should contain style.md content, got:\n%s", refDocs)
	}
}

func TestLoader_DeduplicateReferences(t *testing.T) {
	dir := t.TempDir()

	// Create a shared doc
	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "styleguide.md"), []byte("# Styleguide\nUse gofmt."), 0o644)

	// Two rules referencing the same doc
	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "a.md"), []byte("---\nscope: coding\n---\nStyle: @.kodrun/docs/styleguide.md"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "b.md"), []byte("---\nscope: coding\n---\nAlso: @.kodrun/docs/styleguide.md"), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	// styleguide.md content should appear exactly once
	count := strings.Count(refDocs, "Use gofmt.")
	if count != 1 {
		t.Errorf("expected styleguide.md content once, found %d times in:\n%s", count, refDocs)
	}
}

func TestLoader_ReferenceDocs_ScopeFiltered(t *testing.T) {
	dir := t.TempDir()

	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "coding_doc.md"), []byte("coding doc content"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "review_doc.md"), []byte("review doc content"), 0o644)

	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "code.md"), []byte("---\nscope: coding\n---\nRef: @.kodrun/docs/coding_doc.md"), 0o644)
	os.WriteFile(filepath.Join(rulesDir, "rev.md"), []byte("---\nscope: review\n---\nRef: @.kodrun/docs/review_doc.md"), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	codingDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if !strings.Contains(codingDocs, "coding doc content") {
		t.Error("coding scope should include coding_doc.md")
	}
	if strings.Contains(codingDocs, "review doc content") {
		t.Error("coding scope should NOT include review_doc.md")
	}

	reviewDocs := loader.ReferenceDocs(context.Background(), ScopeReview)
	if !strings.Contains(reviewDocs, "review doc content") {
		t.Error("review scope should include review_doc.md")
	}
	if strings.Contains(reviewDocs, "coding doc content") {
		t.Error("review scope should NOT include coding_doc.md")
	}
}

func TestLoader_Truncation(t *testing.T) {
	dir := t.TempDir()

	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	// Create a doc larger than maxRefSize
	bigContent := strings.Repeat("A", 200)
	os.WriteFile(filepath.Join(docsDir, "big.md"), []byte(bigContent), 0o644)

	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	os.WriteFile(filepath.Join(rulesDir, "r.md"), []byte("---\nscope: coding\n---\nDoc: @.kodrun/docs/big.md"), 0o644)

	maxRefSize := 50
	loader := NewLoader(dir, maxRefSize)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if !strings.Contains(refDocs, "[truncated") {
		t.Errorf("expected truncation marker, got:\n%s", refDocs)
	}
	if !strings.Contains(refDocs, "read_file") {
		t.Errorf("expected read_file hint in truncation marker, got:\n%s", refDocs)
	}
	// Should contain the first maxRefSize bytes
	if !strings.Contains(refDocs, strings.Repeat("A", maxRefSize)) {
		t.Error("expected first maxRefSize bytes to be preserved")
	}
	// Should NOT contain the full content
	if strings.Contains(refDocs, bigContent) {
		t.Error("expected content to be truncated")
	}
}

func TestLoader_RulesContainLabels(t *testing.T) {
	dir := t.TempDir()

	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "styleguide.md"), []byte("style content"), 0o644)
	os.WriteFile(filepath.Join(docsDir, "example.go"), []byte("package ex"), 0o644)

	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	ruleContent := "---\nscope: coding\n---\nStyle: @.kodrun/docs/styleguide.md\nExample: @.kodrun/docs/example.go"
	os.WriteFile(filepath.Join(rulesDir, "r.md"), []byte(ruleContent), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	content := loader.AllRulesContent(context.Background(), ScopeCoding)
	if !strings.Contains(content, "[see: styleguide.md]") {
		t.Errorf("expected [see: styleguide.md] label in rules, got:\n%s", content)
	}
	if !strings.Contains(content, "[see: example.go]") {
		t.Errorf("expected [see: example.go] label in rules, got:\n%s", content)
	}
	// Should NOT contain inline content
	if strings.Contains(content, "style content") {
		t.Errorf("rules should not contain inline doc content")
	}
}

func TestLoader_ResolveReferences_LeadingSlash(t *testing.T) {
	dir := t.TempDir()

	docsDir := filepath.Join(dir, DocsDir)
	os.MkdirAll(docsDir, 0o755)
	os.WriteFile(filepath.Join(docsDir, "arch.md"), []byte("arch body"), 0o644)

	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	ruleContent := "---\nscope: coding\n---\nArch: @/.kodrun/docs/arch.md"
	os.WriteFile(filepath.Join(rulesDir, "arch.md"), []byte(ruleContent), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if !strings.Contains(refDocs, "arch body") {
		t.Errorf("ReferenceDocs should resolve @/.kodrun/... as project-root path, got:\n%s", refDocs)
	}
}

func TestLoader_ResolveReferences_EscapeRejected(t *testing.T) {
	dir := t.TempDir()

	// File outside workDir that must NOT be readable via @-reference.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.md"), []byte("SECRET"), 0o644)

	rulesDir := filepath.Join(dir, RulesDirs)
	os.MkdirAll(rulesDir, 0o755)
	// Try to escape via "..".
	ruleContent := "---\nscope: coding\n---\nLeak: @../" + filepath.Base(outside) + "/secret.md"
	os.WriteFile(filepath.Join(rulesDir, "leak.md"), []byte(ruleContent), 0o644)

	loader := NewLoader(dir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}

	refDocs := loader.ReferenceDocs(context.Background(), ScopeCoding)
	if strings.Contains(refDocs, "SECRET") {
		t.Errorf("@-reference must not escape project root, but got:\n%s", refDocs)
	}
}

func TestLoader_MissingDir(t *testing.T) {
	loader := NewLoader("/nonexistent/path", 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatalf("should not error on missing dir: %v", err)
	}
}
