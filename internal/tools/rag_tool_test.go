package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubRAGSearcher is a test double for RAGSearcher.
type stubRAGSearcher struct {
	searchFn func(ctx context.Context, query string, topK int) ([]rag.SearchResult, error)
}

func (s *stubRAGSearcher) Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return s.searchFn(ctx, query, topK)
}

func TestRAGSearchTool_Execute_ReturnsResults_Successfully(t *testing.T) {
	searcher := &stubRAGSearcher{
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return []rag.SearchResult{
				{
					Chunk: rag.Chunk{
						FilePath:  "main.go",
						Content:   "func main() {}",
						StartLine: 1,
						EndLine:   3,
					},
					Score: 0.85,
				},
				{
					Chunk: rag.Chunk{
						FilePath:  "util.go",
						Content:   "func helper() {}",
						StartLine: 10,
						EndLine:   12,
					},
					Score: 0.72,
				},
			}, nil
		},
	}
	tool := NewRAGSearchTool(searcher, 5)

	result, err := tool.Execute(context.Background(), map[string]any{"query": "main function"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "Result 1")
	assert.Contains(t, result.Output, "0.85")
	assert.Contains(t, result.Output, "main.go:1-3")
	assert.Contains(t, result.Output, "Result 2")
	assert.Contains(t, result.Output, "util.go:10-12")
	assert.Equal(t, 2, result.Meta["results"])
}

func TestRAGSearchTool_Execute_NoResults_Successfully(t *testing.T) {
	searcher := &stubRAGSearcher{
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return nil, nil
		},
	}
	tool := NewRAGSearchTool(searcher, 5)

	result, err := tool.Execute(context.Background(), map[string]any{"query": "nonexistent"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "No relevant results found")
}

func TestRAGSearchTool_Execute_EmptyQuery_Failure(t *testing.T) {
	searcher := &stubRAGSearcher{}
	tool := NewRAGSearchTool(searcher, 5)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "query is required")
}

func TestRAGSearchTool_Execute_SearchError_Failure(t *testing.T) {
	searcher := &stubRAGSearcher{
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return nil, errors.New("embedding failed")
		},
	}
	tool := NewRAGSearchTool(searcher, 5)

	_, err := tool.Execute(context.Background(), map[string]any{"query": "test"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "search")
}

func TestRAGSearchTool_Name_Successfully(t *testing.T) {
	tool := NewRAGSearchTool(nil, 5)

	assert.Equal(t, "search_docs", tool.Name())
}

func TestRAGSearchTool_Schema_RequiresQuery_Successfully(t *testing.T) {
	tool := NewRAGSearchTool(nil, 5)

	schema := tool.Schema()

	assert.Contains(t, schema.Required, "query")
}
