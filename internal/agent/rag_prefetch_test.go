package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/raoptimus/kodrun/internal/rag"
)

func TestDetectEntityTypeFromPath_TestFile_Successfully(t *testing.T) {
	t.Parallel()

	got := detectEntityTypeFromPath("internal/service/user_test.go", []string{"service", "tests", "repository"})

	assert.Equal(t, "tests", got)
}

func TestDetectEntityTypeFromPath_MatchesSuffix_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		ruleNames []string
		want      string
	}{
		{
			name:      "service suffix",
			path:      "internal/user_service.go",
			ruleNames: []string{"service", "repository"},
			want:      "service",
		},
		{
			name:      "repository suffix",
			path:      "internal/user_repository.go",
			ruleNames: []string{"service", "repository"},
			want:      "repository",
		},
		{
			name:      "exact match",
			path:      "service.go",
			ruleNames: []string{"service"},
			want:      "service",
		},
		{
			name:      "contains match",
			path:      "my_service_impl.go",
			ruleNames: []string{"service"},
			want:      "service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectEntityTypeFromPath(tt.path, tt.ruleNames)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetectEntityTypeFromPath_NoMatch_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		ruleNames []string
	}{
		{name: "no matching rule", path: "main.go", ruleNames: []string{"service", "repository"}},
		{name: "empty path", path: "", ruleNames: []string{"service"}},
		{name: "empty rule names", path: "service.go", ruleNames: nil},
		{name: "both empty", path: "", ruleNames: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := detectEntityTypeFromPath(tt.path, tt.ruleNames)
			assert.Equal(t, "", got)
		})
	}
}

func TestDetectEntityTypeFromPath_LongerRuleMatchesFirst_Successfully(t *testing.T) {
	t.Parallel()

	// "user_service" should match "service" not just "ice" if both were present.
	// More importantly, "user_service_converter" should match "service_converter"
	// over "service" because it's sorted by length desc.
	got := detectEntityTypeFromPath("user_service_converter.go", []string{"service", "service_converter"})

	assert.Equal(t, "service_converter", got)
}

func TestDetectEntityTypeFromPath_TestFilePriorityOverSuffix_Successfully(t *testing.T) {
	t.Parallel()

	// A _test.go file should return "tests" even if "service" also matches.
	got := detectEntityTypeFromPath("user_service_test.go", []string{"service", "tests"})

	assert.Equal(t, "tests", got)
}

func TestEntityTypesFromPaths_Successfully(t *testing.T) {
	t.Parallel()

	paths := []string{
		"user_service.go",
		"order_service.go",
		"user_repository.go",
		"main.go",
	}
	ruleNames := []string{"service", "repository"}

	got := entityTypesFromPaths(paths, ruleNames)

	assert.Len(t, got, 2)
	assert.Contains(t, got, "service")
	assert.Contains(t, got, "repository")
}

func TestEntityTypesFromPaths_NoDuplicates_Successfully(t *testing.T) {
	t.Parallel()

	paths := []string{
		"user_service.go",
		"order_service.go",
	}
	ruleNames := []string{"service"}

	got := entityTypesFromPaths(paths, ruleNames)

	assert.Len(t, got, 1)
	assert.Equal(t, "service", got[0])
}

func TestEntityTypesFromPaths_EmptyPaths_Successfully(t *testing.T) {
	t.Parallel()

	got := entityTypesFromPaths(nil, []string{"service"})

	assert.Empty(t, got)
}

func TestChunkCategory_Successfully(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filePath string
		want     string
	}{
		{name: "godoc prefix", filePath: "godoc://fmt", want: "godoc"},
		{name: "rules prefix", filePath: "rules://service", want: "rule"},
		{name: "embedded prefix", filePath: "embedded://go-standards", want: "standard"},
		{name: "example file", filePath: ".kodrun/docs/example_service.go", want: "snippet"},
		{name: "snippets dir", filePath: "project/snippets/auth.go", want: "snippet"},
		{name: "snippet extension", filePath: "auth.snippet", want: "snippet"},
		{name: "docs dir", filePath: "project/docs/guide.md", want: "rule"},
		{name: "rules dir", filePath: "project/rules/naming.md", want: "rule"},
		{name: "regular code file", filePath: "internal/service/user.go", want: "code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := chunkCategory(tt.filePath)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatRAGResults_EmptyResults_Successfully(t *testing.T) {
	t.Parallel()

	got := formatRAGResults(nil)

	assert.Equal(t, "", got)
}

func TestFormatRAGResults_RulesSection_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "rules://service",
				Content:   "Always use context.Context",
				StartLine: 1,
				EndLine:   5,
			},
			Score: 0.95,
		},
	}

	got := formatRAGResults(results)

	assert.Contains(t, got, "[MANDATORY PROJECT RULES")
	assert.Contains(t, got, "RULE 1")
	assert.Contains(t, got, "0.95")
	assert.Contains(t, got, "Always use context.Context")
}

func TestFormatRAGResults_StandardsSection_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "embedded://go-idioms",
				Content:   "Use early returns",
				StartLine: 1,
				EndLine:   3,
			},
			Score: 0.85,
		},
	}

	got := formatRAGResults(results)

	assert.Contains(t, got, "[GO STANDARDS")
	assert.Contains(t, got, "STANDARD 1")
	assert.Contains(t, got, "Use early returns")
}

func TestFormatRAGResults_SnippetsSection_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "docs/example_service.go",
				Content:   "func NewService() *Service",
				StartLine: 1,
				EndLine:   10,
			},
			Score: 0.80,
		},
	}

	got := formatRAGResults(results)

	assert.Contains(t, got, "[CODE TEMPLATES")
	assert.Contains(t, got, "TEMPLATE 1")
}

func TestFormatRAGResults_GodocSection_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "godoc://context",
				Content:   "Package context defines...",
				StartLine: 1,
				EndLine:   20,
			},
			Score: 0.70,
		},
	}

	got := formatRAGResults(results)

	assert.Contains(t, got, "[GO DOCUMENTATION")
	assert.Contains(t, got, "DOC 1")
}

func TestFormatRAGResults_DropsCodeChunks_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "internal/service/user.go",
				Content:   "func GetUser() {}",
				StartLine: 1,
				EndLine:   5,
			},
			Score: 0.90,
		},
	}

	got := formatRAGResults(results)

	// "code" category chunks are dropped.
	assert.Equal(t, "", got)
}

func TestFormatRAGResults_MixedCategories_Successfully(t *testing.T) {
	t.Parallel()

	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{
				FilePath:  "rules://naming",
				Content:   "Use Owner not GetOwner",
				StartLine: 1,
				EndLine:   3,
			},
			Score: 0.95,
		},
		{
			Chunk: rag.Chunk{
				FilePath:  "embedded://go-errors",
				Content:   "Wrap errors with context",
				StartLine: 1,
				EndLine:   5,
			},
			Score: 0.88,
		},
	}

	got := formatRAGResults(results)

	assert.Contains(t, got, "[MANDATORY PROJECT RULES")
	assert.Contains(t, got, "[GO STANDARDS")
	assert.Contains(t, got, "RULE 1")
	assert.Contains(t, got, "STANDARD 2")
}
