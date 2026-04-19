package agent

import (
	"strings"
	"testing"
)

func TestPlanFromMarkdown_ExtractsFiles(t *testing.T) {
	md := `## Plan
1. Update internal/agent/agent.go:42 to add the field.
2. Add a new file internal/agent/subagent.go.
3. Touch internal/config/config.go and internal/agent/agent.go again.
4. Edit README.md.
`
	plan := PlanFromMarkdown(md)
	got := plan.AffectedFiles()
	want := []string{
		"README.md",
		"internal/agent/agent.go",
		"internal/agent/subagent.go",
		"internal/config/config.go",
	}
	if len(got) != len(want) {
		t.Fatalf("AffectedFiles=%v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("AffectedFiles[%d]=%q, want %q", i, got[i], w)
		}
	}
}

func TestPlanFromMarkdown_NoFiles(t *testing.T) {
	plan := PlanFromMarkdown("Some plan with no file references.")
	if len(plan.AffectedFiles()) != 0 {
		t.Errorf("expected empty file list, got %v", plan.AffectedFiles())
	}
}

func TestPathAllowed_NilWhitelistAllowsEverything(t *testing.T) {
	a := &Agent{}
	if !a.pathAllowed("anything.go") {
		t.Fatal("nil whitelist should allow all paths")
	}
}

func TestPathAllowed_RespectsWhitelist(t *testing.T) {
	a := &Agent{}
	a.SetAllowedReadPaths([]string{"internal/agent/agent.go", "config.go"})
	if !a.pathAllowed("internal/agent/agent.go") {
		t.Error("expected exact path to be allowed")
	}
	if !a.pathAllowed("config.go") {
		t.Error("expected basename match to be allowed")
	}
	if a.pathAllowed("internal/secret/keys.go") {
		t.Error("expected non-whitelisted path to be denied")
	}
}

func TestRenderExtractorOutput_WithAffectedFilesAndVerification(t *testing.T) {
	input := `{
		"context": "Fix naming issues",
		"plan": ["a.go:10 — blocker — rename func"],
		"affected_files": ["a.go", "b.go"],
		"verification": ["make build", "make lint", "make test-unit"]
	}`
	out := RenderExtractorOutput(input, "en")
	if !strings.Contains(out, "## Affected files") {
		t.Errorf("expected affected files section, got:\n%s", out)
	}
	if !strings.Contains(out, "- a.go") || !strings.Contains(out, "- b.go") {
		t.Errorf("expected file list, got:\n%s", out)
	}
	if !strings.Contains(out, "## Post-execution verification") {
		t.Errorf("expected verification section, got:\n%s", out)
	}
	if !strings.Contains(out, "- [ ] make build") {
		t.Errorf("expected verification items, got:\n%s", out)
	}
}

func TestRenderExtractorOutput_WithoutOptionalSections(t *testing.T) {
	input := `{
		"context": "Fix issues",
		"plan": ["a.go:10 — blocker — fix it"]
	}`
	out := RenderExtractorOutput(input, "en")
	if strings.Contains(out, "Affected files") {
		t.Errorf("should not have affected files section without data, got:\n%s", out)
	}
	if strings.Contains(out, "verification") {
		t.Errorf("should not have verification section without data, got:\n%s", out)
	}
}

func TestRenderExtractorOutput_StructuredPlanItems(t *testing.T) {
	input := `{
		"context": "Fix naming issues",
		"plan": [
			{
				"file": "cmd/main.go",
				"line": 67,
				"severity": "blocker",
				"what": "Опечатка в имени функции InitStandartLogger",
				"why": "Код не скомпилируется",
				"fix": "Заменить InitStandartLogger на InitStandardLogger",
				"before": "logger.InitStandartLogger()",
				"after": "logger.InitStandardLogger()",
				"rules": ["naming"]
			}
		],
		"affected_files": ["cmd/main.go"],
		"verification": ["make build"]
	}`
	out := RenderExtractorOutput(input, "ru")
	if !strings.Contains(out, "### 1. cmd/main.go:67 [blocker]") {
		t.Errorf("expected structured heading, got:\n%s", out)
	}
	if !strings.Contains(out, "- **What:** Опечатка в имени функции") {
		t.Errorf("expected What field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Why:** Код не скомпилируется") {
		t.Errorf("expected Why field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Fix:** Заменить") {
		t.Errorf("expected Fix field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Before:**") {
		t.Errorf("expected Before field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **After:**") {
		t.Errorf("expected After field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Rules:** naming") {
		t.Errorf("expected Rules field, got:\n%s", out)
	}
}

func TestSetAllowedReadPaths_NilClearsWhitelist(t *testing.T) {
	a := &Agent{}
	a.SetAllowedReadPaths([]string{"a.go"})
	if a.allowedReadPaths == nil {
		t.Fatal("expected whitelist to be set")
	}
	a.SetAllowedReadPaths(nil)
	if a.allowedReadPaths != nil {
		t.Fatal("expected whitelist to be cleared")
	}
}
