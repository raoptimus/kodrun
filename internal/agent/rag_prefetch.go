package agent

import (
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/rag"
)

// formatRAGResults formats RAG search results into a string block
// suitable for injection into a user message.
func formatRAGResults(results []rag.SearchResult) string {
	var b strings.Builder
	b.WriteString("[Project conventions and documentation (from RAG)]\n")
	for i, r := range results {
		fmt.Fprintf(&b, "--- %d (%.2f) %s:%d-%d ---\n%s\n\n",
			i+1, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
	}
	return b.String()
}
