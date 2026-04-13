package agent

import (
	"testing"
)

func TestSystemPromptForRole_Planner(t *testing.T) {
	prompt := systemPromptForRole(RolePlanner, "en", "", []string{"read_file", "grep"})

	if !contains(prompt, "PLANNER") {
		t.Error("planner prompt should mention PLANNER")
	}
	if !contains(prompt, "code block") {
		t.Error("planner prompt should prohibit code blocks")
	}
	if !contains(prompt, "English") {
		t.Error("planner prompt should mention English")
	}
}

func TestSystemPromptForRole_Executor(t *testing.T) {
	prompt := systemPromptForRole(RoleExecutor, "ru", "", []string{"write_file", "edit_file", "go_build"})

	if !contains(prompt, "EXECUTOR") {
		t.Error("executor prompt should mention EXECUTOR")
	}
	if !contains(prompt, "write_file") {
		t.Error("executor prompt should list available tools")
	}
	if !contains(prompt, "Russian") {
		t.Error("executor prompt should mention Russian for lang=ru")
	}
}

func TestSystemPromptForRole_Reviewer(t *testing.T) {
	prompt := systemPromptForRole(RoleReviewer, "en", "## Rules\n- rule1", []string{"read_file"})

	if !contains(prompt, "REVIEWER") {
		t.Error("reviewer prompt should mention REVIEWER")
	}
	if !contains(prompt, "## Rules") {
		t.Error("reviewer prompt should include rule catalog")
	}
	if !contains(prompt, "bugs") {
		t.Error("reviewer prompt should mention checking for bugs")
	}
}

func TestSystemPromptForRole_WithRuleCatalog(t *testing.T) {
	catalog := "## Project Rules\n- Use Go 1.25+\n- Follow conventions"
	prompt := systemPromptForRole(RolePlanner, "en", catalog, nil)

	if !contains(prompt, "Project Rules") {
		t.Error("prompt should include rule catalog")
	}
}

func TestNewOrchestrator(t *testing.T) {
	orch := NewOrchestrator(nil, "test-model", nil, "/tmp", 32768, &OrchestratorConfig{})
	if orch == nil {
		t.Fatal("NewOrchestrator returned nil")
	}
	if orch.model != "test-model" {
		t.Errorf("model: got %q", orch.model)
	}
	if orch.review {
		t.Error("review should be false by default")
	}
}

func TestOrchestrator_NewAgent_Roles(t *testing.T) {
	orch := NewOrchestrator(nil, "test-model", nil, "/tmp", 32768, &OrchestratorConfig{
		Language: "ru",
		Think:    true,
	})

	tests := []struct {
		role      Role
		wantMode  Mode
		wantThink bool
	}{
		{RolePlanner, ModePlan, true},
		{RoleExecutor, ModeEdit, false},
		{RoleReviewer, ModePlan, true},
	}

	for _, tt := range tests {
		ag := orch.newAgent(tt.role, 10)
		if ag.Mode() != tt.wantMode {
			t.Errorf("role %s: mode got %v, want %v", tt.role, ag.Mode(), tt.wantMode)
		}
		if ag.Think() != tt.wantThink {
			t.Errorf("role %s: think got %v, want %v", tt.role, ag.Think(), tt.wantThink)
		}
		if ag.language != "ru" {
			t.Errorf("role %s: language got %q, want 'ru'", tt.role, ag.language)
		}
	}
}

func TestOrchestrator_Config(t *testing.T) {
	orch := NewOrchestrator(nil, "m", nil, "/tmp", 32768, &OrchestratorConfig{
		Review: true,
	})
	if !orch.review {
		t.Error("review should be true from config")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && // avoid trivial matches
		stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
