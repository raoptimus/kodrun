/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubWebIndexer is a test double for WebIndexer.
type stubWebIndexer struct {
	buildFn  func(ctx context.Context, chunks []rag.Chunk) (int, error)
	searchFn func(ctx context.Context, query string, topK int) ([]rag.SearchResult, error)
}

func (s *stubWebIndexer) Build(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return s.buildFn(ctx, chunks)
}

func (s *stubWebIndexer) Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return s.searchFn(ctx, query, topK)
}

func TestWebFetchTool_Execute_EmptyURL_Failure(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestWebFetchTool_Name_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)

	assert.Equal(t, "web_fetch", tool.Name())
}

func TestWebFetchTool_Schema_RequiresURL_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)

	schema := tool.Schema()

	assert.Contains(t, schema.Required, "url")
}

func TestNewWebFetchTool_DefaultTopK_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 0)

	assert.Equal(t, webFetchTopMatches, tool.topK)
}

func TestNewWebFetchTool_CustomTopK_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 10)

	assert.Equal(t, 10, tool.topK)
}

func TestWebFetchTool_ExecuteWithTextMatch_NoQuery_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	chunks := []rag.Chunk{
		{FilePath: "web://example.com", Content: "Hello World", StartLine: 1, EndLine: 5},
	}

	result := tool.executeWithTextMatch(chunks, "")

	assert.Contains(t, result.Output, "Hello World")
}

func TestWebFetchTool_ExecuteWithTextMatch_WithQuery_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	chunks := []rag.Chunk{
		{FilePath: "web://example.com", Content: "Go programming language", StartLine: 1, EndLine: 5},
		{FilePath: "web://example.com", Content: "Python scripting", StartLine: 6, EndLine: 10},
		{FilePath: "web://example.com", Content: "JavaScript runtime", StartLine: 11, EndLine: 15},
	}

	result := tool.executeWithTextMatch(chunks, "Go programming")

	assert.Contains(t, result.Output, "Go programming language")
	assert.NotContains(t, result.Output, "Python scripting")
}

func TestWebFetchTool_ExecuteWithTextMatch_NoMatches_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	chunks := []rag.Chunk{
		{FilePath: "web://example.com", Content: "Hello World", StartLine: 1, EndLine: 5},
	}

	result := tool.executeWithTextMatch(chunks, "zzz_nonexistent")

	assert.Contains(t, result.Output, "No matching content found")
}

func TestWebFetchTool_ExecuteWithTextMatch_LimitsResults_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	chunks := make([]rag.Chunk, 10)
	for i := range chunks {
		chunks[i] = rag.Chunk{
			FilePath:  "web://example.com",
			Content:   "match keyword here",
			StartLine: i*5 + 1,
			EndLine:   i*5 + 5,
		}
	}

	result := tool.executeWithTextMatch(chunks, "keyword")

	assert.Equal(t, webFetchTopMatches, result.Meta["matches"])
}

func TestWebFetchTool_SummarizeChunks_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	chunks := []rag.Chunk{
		{Content: "chunk one"},
		{Content: "chunk two"},
	}

	result := tool.summarizeChunks(chunks)

	assert.Contains(t, result.Output, "chunk one")
	assert.Contains(t, result.Output, "chunk two")
}

func TestWebFetchTool_FormatSearchResults_Successfully(t *testing.T) {
	tool := NewWebFetchTool(nil, 5)
	results := []rag.SearchResult{
		{
			Chunk: rag.Chunk{FilePath: "a.go", Content: "func A()", StartLine: 1, EndLine: 3},
			Score: 0.92,
		},
		{
			Chunk: rag.Chunk{FilePath: "b.go", Content: "func B()", StartLine: 10, EndLine: 12},
			Score: 0.81,
		},
	}

	result := tool.formatSearchResults(results)

	assert.Contains(t, result.Output, "Result 1")
	assert.Contains(t, result.Output, "0.92")
	assert.Contains(t, result.Output, "Result 2")
	assert.Equal(t, 2, result.Meta["results"])
}

func TestWebFetchTool_ExecuteWithRAG_NoQuery_Successfully(t *testing.T) {
	indexer := &stubWebIndexer{
		buildFn: func(_ context.Context, _ []rag.Chunk) (int, error) {
			return 1, nil
		},
	}
	tool := NewWebFetchTool(indexer, 5)
	chunks := []rag.Chunk{
		{Content: "indexed content"},
	}

	result, err := tool.executeWithRAG(context.Background(), chunks, "")

	require.NoError(t, err)
	assert.Contains(t, result.Output, "indexed content")
}

func TestWebFetchTool_ExecuteWithRAG_WithQuery_Successfully(t *testing.T) {
	indexer := &stubWebIndexer{
		buildFn: func(_ context.Context, _ []rag.Chunk) (int, error) {
			return 1, nil
		},
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return []rag.SearchResult{
				{
					Chunk: rag.Chunk{Content: "found it", StartLine: 1, EndLine: 5},
					Score: 0.9,
				},
			}, nil
		},
	}
	tool := NewWebFetchTool(indexer, 5)

	result, err := tool.executeWithRAG(context.Background(), nil, "find something")

	require.NoError(t, err)
	assert.Contains(t, result.Output, "found it")
}

func TestWebFetchTool_ExecuteWithRAG_SearchNoResults_Successfully(t *testing.T) {
	indexer := &stubWebIndexer{
		buildFn: func(_ context.Context, _ []rag.Chunk) (int, error) {
			return 1, nil
		},
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return nil, nil
		},
	}
	tool := NewWebFetchTool(indexer, 5)

	result, err := tool.executeWithRAG(context.Background(), nil, "nothing here")

	require.NoError(t, err)
	assert.Contains(t, result.Output, "No relevant results")
}

func TestWebFetchTool_ExecuteWithRAG_BuildError_Failure(t *testing.T) {
	indexer := &stubWebIndexer{
		buildFn: func(_ context.Context, _ []rag.Chunk) (int, error) {
			return 0, errors.New("build failed")
		},
	}
	tool := NewWebFetchTool(indexer, 5)

	_, err := tool.executeWithRAG(context.Background(), nil, "query")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "index")
}

func TestWebFetchTool_ExecuteWithRAG_SearchError_Failure(t *testing.T) {
	indexer := &stubWebIndexer{
		buildFn: func(_ context.Context, _ []rag.Chunk) (int, error) {
			return 1, nil
		},
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return nil, errors.New("search failed")
		},
	}
	tool := NewWebFetchTool(indexer, 5)

	_, err := tool.executeWithRAG(context.Background(), nil, "query")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "search")
}
