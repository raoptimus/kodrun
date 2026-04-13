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
		{name: "code_reviewer", role: RoleCodeReviewer, want: "Reviewing file..."},
		{name: "arch_reviewer", role: RoleArchReviewer, want: "Reviewing architecture..."},
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

func TestReviewChecks_AllCategories_Successfully(t *testing.T) {
	t.Parallel()

	checks := reviewChecks("en")

	expectedKeys := []string{"rules", "idiomatic", "best_practice", "security", "structure", "architecture"}
	for _, key := range expectedKeys {
		assert.NotEmpty(t, checks[key], "expected non-empty checks for %q", key)
	}
}

func TestReviewChecks_RussianLanguage_Successfully(t *testing.T) {
	t.Parallel()

	checks := reviewChecks("ru")

	// Idiomatic checks should mention Russian.
	var hasRussian bool
	for _, c := range checks["idiomatic"] {
		if contains(c, "Russian") {
			hasRussian = true
			break
		}
	}
	assert.True(t, hasRussian)
}
