package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/rag"
)

// RAGSearcher is implemented by both *rag.Index and *rag.MultiIndex.
type RAGSearcher interface {
	Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error)
}

// RAGSearchTool searches the RAG index for relevant code/docs.
type RAGSearchTool struct {
	index RAGSearcher
	topK  int
}

// NewRAGSearchTool creates a new search_docs tool. The index argument
// can be either *rag.Index or *rag.MultiIndex.
func NewRAGSearchTool(index RAGSearcher, topK int) *RAGSearchTool {
	return &RAGSearchTool{index: index, topK: topK}
}

func (t *RAGSearchTool) Name() string { return "search_docs" }
func (t *RAGSearchTool) Description() string {
	return "Search project files using semantic similarity (RAG). Use for finding relevant code, docs, or examples."
}

func (t *RAGSearchTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"query": {Type: "string", Description: "Search query describing what you're looking for"},
		},
		Required: []string{"query"},
	}
}

func (t *RAGSearchTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	query := stringParam(params, "query")
	if query == "" {
		return nil, &ToolError{Msg: "query is required"}
	}

	results, err := t.index.Search(ctx, query, t.topK)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		return &ToolResult{Output: "No relevant results found. Try a different query or run /reindex."}, nil
	}

	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- Result %d (%.2f) %s:%d-%d ---\n%s\n\n",
			i+1, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
	}

	return &ToolResult{
		Output: b.String(),
		Meta: map[string]any{
			"results": len(results),
		},
	}, nil
}
