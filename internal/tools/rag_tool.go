package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rag"
)

// RAGSearchTool searches the RAG index for relevant code/docs.
type RAGSearchTool struct {
	index *rag.Index
	topK  int
}

// NewRAGSearchTool creates a new search_docs tool.
func NewRAGSearchTool(index *rag.Index, topK int) *RAGSearchTool {
	return &RAGSearchTool{index: index, topK: topK}
}

func (t *RAGSearchTool) Name() string { return "search_docs" }
func (t *RAGSearchTool) Description() string {
	return "Search project files using semantic similarity (RAG). Use for finding relevant code, docs, or examples."
}

func (t *RAGSearchTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"query": {Type: "string", Description: "Search query describing what you're looking for"},
		},
		Required: []string{"query"},
	}
}

func (t *RAGSearchTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	query, _ := params["query"].(string)
	if query == "" {
		return ToolResult{Error: "query is required", Success: false}, nil
	}

	results, err := t.index.Search(ctx, query, t.topK)
	if err != nil {
		return ToolResult{Error: fmt.Sprintf("search: %s", err), Success: false}, nil
	}

	if len(results) == 0 {
		return ToolResult{Output: "No relevant results found. Try a different query or run /reindex.", Success: true}, nil
	}

	var b strings.Builder
	for i, r := range results {
		fmt.Fprintf(&b, "--- Result %d (%.2f) %s:%d-%d ---\n%s\n\n",
			i+1, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
	}

	return ToolResult{
		Output:  b.String(),
		Success: true,
		Meta: map[string]any{
			"results": len(results),
		},
	}, nil
}
