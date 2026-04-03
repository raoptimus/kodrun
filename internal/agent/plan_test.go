package agent

import (
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
