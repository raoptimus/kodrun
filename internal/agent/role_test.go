package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskLabelForRole_KnownRoles_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role Role
		want string
	}{
		{name: "planner", role: RolePlanner, want: "Planning..."},
		{name: "executor", role: RoleExecutor, want: "Executing plan..."},
		{name: "reviewer", role: RoleReviewer, want: "Reviewing changes..."},
		{name: "extractor", role: RoleExtractor, want: "Extracting structured plan..."},
		{name: "structurer", role: RoleStructurer, want: "Converting plan to JSON..."},
		{name: "response_classifier", role: RoleResponseClassifier, want: "Classifying response..."},
		{name: "reviewer_rules", role: RoleReviewerRules, want: "Reviewing: project rules & naming"},
		{name: "reviewer_idiomatic", role: RoleReviewerIdiomatic, want: "Reviewing: language idiomaticity"},
		{name: "reviewer_best_practice", role: RoleReviewerBestPractice, want: "Reviewing: best practices"},
		{name: "reviewer_security", role: RoleReviewerSecurity, want: "Reviewing: security"},
		{name: "reviewer_structure", role: RoleReviewerStructure, want: "Reviewing: structure & layering"},
		{name: "reviewer_architecture", role: RoleReviewerArchitecture, want: "Reviewing: architecture"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := taskLabelForRole(tt.role)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskLabelForRole_UnknownRole_Successfully(t *testing.T) {
	t.Parallel()

	got := taskLabelForRole(Role("unknown_role"))

	assert.Equal(t, "Processing task...", got)
}

func TestReviewerShortLabel_ReviewerRoles_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role Role
		want string
	}{
		{name: "reviewer_rules", role: RoleReviewerRules, want: "project rules & naming"},
		{name: "reviewer_idiomatic", role: RoleReviewerIdiomatic, want: "language idiomaticity"},
		{name: "reviewer_best_practice", role: RoleReviewerBestPractice, want: "best practices"},
		{name: "reviewer_security", role: RoleReviewerSecurity, want: "security"},
		{name: "reviewer_structure", role: RoleReviewerStructure, want: "structure & layering"},
		{name: "reviewer_architecture", role: RoleReviewerArchitecture, want: "architecture"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := reviewerShortLabel(tt.role)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestReviewerShortLabel_NonReviewerRole_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role Role
		want string
	}{
		{name: "planner", role: RolePlanner, want: "planner"},
		{name: "executor", role: RoleExecutor, want: "executor"},
		{name: "unknown", role: Role("custom"), want: "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := reviewerShortLabel(tt.role)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSystemPromptForRole_ContainsAssistantIdentity_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role Role
	}{
		{name: "planner", role: RolePlanner},
		{name: "executor", role: RoleExecutor},
		{name: "reviewer", role: RoleReviewer},
		{name: "extractor", role: RoleExtractor},
		{name: "structurer", role: RoleStructurer},
		{name: "response_classifier", role: RoleResponseClassifier},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := systemPromptForRole(tt.role, "en", "", nil)
			assert.Contains(t, got, "You are KodRun")
		})
	}
}

func TestSystemPromptForRole_NonEnglishIncludesLanguageDirective_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RolePlanner, "ru", "", nil)

	assert.Contains(t, got, "Russian")
	assert.Contains(t, got, "IMPORTANT: ALL your responses MUST be in Russian")
}

func TestSystemPromptForRole_EnglishOmitsLanguageDirective_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RolePlanner, "en", "", nil)

	assert.NotContains(t, got, "IMPORTANT: ALL your responses MUST be in English")
}

func TestSystemPromptForRole_IncludesRuleCatalog_Successfully(t *testing.T) {
	t.Parallel()

	catalog := "## Rules\n- Use context.Context\n"
	got := systemPromptForRole(RoleExecutor, "en", catalog, nil)

	assert.Contains(t, got, catalog)
}

func TestSystemPromptForRole_IncludesToolNames_Successfully(t *testing.T) {
	t.Parallel()

	tools := []string{"read_file", "write_file", "bash"}
	got := systemPromptForRole(RoleExecutor, "en", "", tools)

	assert.Contains(t, got, "Available tools: read_file, write_file, bash")
}

func TestSystemPromptForRole_PlannerWithSnippets_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RolePlanner, "en", "", nil, true)

	assert.Contains(t, got, "snippets")
	assert.Contains(t, got, "IMPORTANT")
}

func TestSystemPromptForRole_PlannerWithRAG_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RolePlanner, "en", "", nil, false, true)

	assert.Contains(t, got, "MANDATORY RULES")
	assert.Contains(t, got, "search_docs")
}

func TestSystemPromptForRole_ExecutorContainsRole_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RoleExecutor, "en", "", nil)

	assert.Contains(t, got, "You are the EXECUTOR agent")
	assert.Contains(t, got, "IMPLEMENT an approved plan")
}

func TestSystemPromptForRole_ReviewerContainsRole_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RoleReviewer, "en", "", nil)

	assert.Contains(t, got, "You are the REVIEWER agent")
}

func TestSystemPromptForRole_ExtractorContainsJSONSchema_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RoleExtractor, "en", "", nil)

	assert.Contains(t, got, "PLAN EXTRACTOR")
	assert.Contains(t, got, `"context"`)
	assert.Contains(t, got, `"plan"`)
}

func TestSystemPromptForRole_StructurerContainsJSONSchema_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RoleStructurer, "en", "", nil)

	assert.Contains(t, got, "PLAN STRUCTURER")
	assert.Contains(t, got, `"steps"`)
	assert.Contains(t, got, `"depends_on"`)
}

func TestSystemPromptForRole_ResponseClassifierContainsSchema_Successfully(t *testing.T) {
	t.Parallel()

	got := systemPromptForRole(RoleResponseClassifier, "en", "", nil)

	assert.Contains(t, got, "RESPONSE CLASSIFIER")
	assert.Contains(t, got, `"kind"`)
	assert.Contains(t, got, `"needs_user_action"`)
}

func TestSpecialistReviewerFocus_AllRoles_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		role      Role
		wantFocus string
	}{
		{name: "rules", role: RoleReviewerRules, wantFocus: "project rules and naming conventions"},
		{name: "idiomatic", role: RoleReviewerIdiomatic, wantFocus: "English idiomaticity"},
		{name: "best_practice", role: RoleReviewerBestPractice, wantFocus: "best practices"},
		{name: "security", role: RoleReviewerSecurity, wantFocus: "security"},
		{name: "structure", role: RoleReviewerStructure, wantFocus: "package/module structure"},
		{name: "architecture", role: RoleReviewerArchitecture, wantFocus: "architectural invariants"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			focus, rules := specialistReviewerFocus(tt.role, "en")
			assert.Contains(t, focus, tt.wantFocus)
			assert.NotEmpty(t, rules)
		})
	}
}

func TestSpecialistReviewerFocus_UnknownRole_Successfully(t *testing.T) {
	t.Parallel()

	focus, rules := specialistReviewerFocus(Role("unknown"), "en")

	assert.Equal(t, "code quality", focus)
	assert.Nil(t, rules)
}

func TestSpecialistReviewerFocus_RussianLanguage_Successfully(t *testing.T) {
	t.Parallel()

	focus, _ := specialistReviewerFocus(RoleReviewerIdiomatic, "ru")

	assert.Contains(t, focus, "Russian")
}

func TestSpecialistReviewerRoles_ContainsAllRoles_Successfully(t *testing.T) {
	t.Parallel()

	expected := []Role{
		RoleReviewerRules,
		RoleReviewerIdiomatic,
		RoleReviewerBestPractice,
		RoleReviewerSecurity,
		RoleReviewerStructure,
		RoleReviewerArchitecture,
	}

	assert.Equal(t, expected, SpecialistReviewerRoles)
}
