package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractPlan_MarkdownHeaders(t *testing.T) {
	text := `Some thinking text here.

## Context
Analyzed the project. Found issues.

## Plan
1. [Fix]: Add error handling in main.go:10
2. [Fix]: Fix logging in server.go:25
`
	got := extractPlan(text)
	if !contains(got, "## Context") {
		t.Error("should contain ## Context header")
	}
	if !contains(got, "1. [Fix]") {
		t.Error("should contain plan items")
	}
	if contains(got, "Some thinking") {
		t.Error("should not contain pre-context text")
	}
}

func TestExtractPlan_PlainHeaders(t *testing.T) {
	text := "Some intermediate text\n\nCONTEXT\nAnalyzed project.\n\nPLAN\n1. [Fix]: Fix bug in foo.go:5\n2. [Fix]: Fix bar in bar.go:10\n"
	got := extractPlan(text)

	if !contains(got, "PLAN") {
		t.Error("should contain PLAN header")
	}
	if !contains(got, "1. [Fix]") {
		t.Error("should contain plan items")
	}
}

func TestExtractPlan_LastMarkerWins(t *testing.T) {
	text := `
CONTEXT
First hallucinated analysis.

PLAN
1. [Fix]: Fake fix in nonexistent.go:1

More tool calls here...

CONTEXT
Second real analysis after reading files.

PLAN
1. [Fix]: Real fix in cmd/main.go:15
2. [Fix]: Real fix in internal/handler.go:30
`
	got := extractPlan(text)

	if contains(got, "Fake fix") {
		t.Error("should not contain first hallucinated plan")
	}
	if !contains(got, "Real fix in cmd/main.go:15") {
		t.Error("should contain the last (real) plan")
	}
}

func TestExtractPlan_NoMarkers_NoList(t *testing.T) {
	text := "Just some plain text without any markers."
	got := extractPlan(text)
	if got != "" {
		t.Errorf("should return empty when no markers and no numbered list: got %q", got)
	}
}

func TestExtractPlan_NoMarkers_WithList(t *testing.T) {
	text := "Some intro text.\n1. Fix error handling in main.go:10\n2. Add tests for handler.go:20"
	got := extractPlan(text)
	if got == "" {
		t.Error("should return text when it contains a numbered list")
	}
	if !contains(got, "1. Fix error") {
		t.Error("should contain numbered items")
	}
}

func TestTrimPlanGarbage_RemovesTrailingText(t *testing.T) {
	text := `PLAN
1. [Fix]: Add error handling in main.go:10
2. [Fix]: Fix logging in server.go:25

Давайте продолжим анализ проекта.

Давайте проверим другие файлы:`

	got := trimPlanGarbage(text)

	if !contains(got, "1. [Fix]") {
		t.Error("should keep plan items")
	}
	if !contains(got, "2. [Fix]") {
		t.Error("should keep all plan items")
	}
	if contains(got, "Давайте продолжим") {
		t.Error("should remove garbage text after plan")
	}
	if contains(got, "Давайте проверим") {
		t.Error("should remove all garbage text")
	}
}

func TestTrimPlanGarbage_KeepsContextAndPlan(t *testing.T) {
	text := `## Context
Analyzed the ping-service project. Found 3 issues.

## Plan
1. [Fix]: Add error handling in main.go:10
2. [Fix]: Fix logging in server.go:25
3. [Fix]: Add input validation in handler.go:42`

	got := trimPlanGarbage(text)

	if !contains(got, "## Context") {
		t.Error("should keep Context header")
	}
	if !contains(got, "Analyzed the ping-service") {
		t.Error("should keep context description")
	}
	if !contains(got, "3. [Fix]") {
		t.Error("should keep all plan items")
	}
}

func TestTrimPlanGarbage_MultiLinePlanItems(t *testing.T) {
	text := `PLAN
1. [Fix]: Add error handling in main.go:10
   This is a continuation of item 1
2. [Fix]: Fix logging in server.go:25

Some garbage text here.`

	got := trimPlanGarbage(text)

	if !contains(got, "continuation of item 1") {
		t.Error("should keep indented continuation lines")
	}
	if contains(got, "Some garbage") {
		t.Error("should remove garbage after plan")
	}
}

func TestTrimPlanGarbage_NoGarbage(t *testing.T) {
	text := `PLAN
1. [Fix]: One fix
2. [Fix]: Two fix`

	got := trimPlanGarbage(text)
	if got != text {
		t.Errorf("should return text unchanged when no garbage:\ngot:  %q\nwant: %q", got, text)
	}
}

func TestExtractPlan_RealWorldDuplicate(t *testing.T) {
	// Simulates real model output with self-correction.
	text := `CONTEXT
Я проанализировал проект. Основные находки касаются обработки ошибок.

PLAN
1. [Fix]: Добавить обработку ошибок в main.go:18
2. [Fix]: Исправить логирование в main.go:25

Однако, после более внимательного анализа кода, я понял ошибки. Поэтому:

CONTEXT
Проанализирован проект ping-service с реальными файлами.

PLAN
1. [Fix]: Добавить обработку ошибок в cmd/ping-service/main.go:119
2. [Fix]: Исправить логирование в cmd/ping-service/main.go:93
3. [Fix]: Добавить проверку JSON в internal/server/http/handler/ping.go:38
`
	got := extractPlan(text)

	// Should get the last PLAN (after self-correction).
	if contains(got, "main.go:18") {
		t.Error("should not contain first hallucinated plan")
	}
	if !contains(got, "cmd/ping-service/main.go:119") {
		t.Error("should contain the corrected plan item 1")
	}
	if !contains(got, "internal/server/http/handler/ping.go:38") {
		t.Error("should contain the corrected plan item 3")
	}
	// Should not contain self-correction text.
	if contains(got, "Однако") {
		t.Error("should not contain self-correction text")
	}
}

func TestValidatePlanPaths(t *testing.T) {
	// Create a temp directory with some files.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "cmd", "app"), 0o755)
	os.MkdirAll(filepath.Join(dir, "internal", "handler"), 0o755)
	os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "handler", "ping.go"), []byte("package handler"), 0o644)

	orch := &Orchestrator{workDir: dir}

	plan := `## Plan
1. [Fix]: Fix error in cmd/app/main.go:10
2. [Fix]: Fix handler in internal/handler/ping.go:25
3. [Fix]: Fix nonexistent in internal/missing/file.go:1
4. [Fix]: Fix another in fake/path.go:99`

	invalid := orch.validatePlanPaths(plan)

	if len(invalid) != 2 {
		t.Errorf("expected 2 invalid paths, got %d: %v", len(invalid), invalid)
	}

	invalidSet := make(map[string]bool)
	for _, p := range invalid {
		invalidSet[p] = true
	}
	if !invalidSet["internal/missing/file.go"] {
		t.Error("should detect internal/missing/file.go as invalid")
	}
	if !invalidSet["fake/path.go"] {
		t.Error("should detect fake/path.go as invalid")
	}
}

func TestValidatePlanPaths_NoPaths(t *testing.T) {
	orch := &Orchestrator{workDir: t.TempDir()}
	invalid := orch.validatePlanPaths("No file paths mentioned here.")
	if len(invalid) != 0 {
		t.Errorf("expected 0 invalid paths, got %d", len(invalid))
	}
}

func TestValidatePlanQuality_PlaceholderLine(t *testing.T) {
	plan := `## Plan
1. [Fix]: Check error handling (file: main.go:line)
2. [Fix]: Verify security (file: handler.go:line)`

	issues := validatePlanQuality(plan)
	if len(issues) == 0 {
		t.Error("should detect ':line' placeholder")
	}
	found := false
	for _, issue := range issues {
		if contains(issue, ":line") {
			found = true
		}
	}
	if !found {
		t.Errorf("should mention ':line' in issues: %v", issues)
	}
}

func TestValidatePlanQuality_VaguePhrases(t *testing.T) {
	plan := `## Plan
1. Проверить обработку ошибок в main.go:10
2. Убедиться что валидация корректна в handler.go:20
3. Проверить безопасность в server.go:30`

	issues := validatePlanQuality(plan)
	if len(issues) == 0 {
		t.Error("should detect vague phrases")
	}
}

func TestValidatePlanQuality_GoodPlan(t *testing.T) {
	plan := `## Plan
1. [Fix]: error from json.Encode() is ignored — add error check (file: internal/handler/ping.go:38)
2. [Fix]: http.ListenAndServe error not handled — wrap with log.Fatal (file: cmd/main.go:25)
3. [Fix]: SQL query uses string concatenation — use parameterized query (file: internal/repo/user.go:42)`

	issues := validatePlanQuality(plan)
	if len(issues) != 0 {
		t.Errorf("good plan should have no issues, got: %v", issues)
	}
}
