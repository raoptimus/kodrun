package rag

import (
	"fmt"
	"strings"
)

// SnippetInfo holds the snippet metadata needed for chunking.
type SnippetInfo struct {
	Name        string
	Description string
	Tags        []string
	Content     string
	SourcePath  string
}

// ChunkSnippets converts snippets into RAG chunks.
// Large snippets are automatically split via splitIntoChunks to respect byte limits.
func ChunkSnippets(snippets []SnippetInfo) []Chunk {
	chunks := make([]Chunk, 0, len(snippets))
	for _, s := range snippets {
		body := fmt.Sprintf("Snippet: %s\nDescription: %s\nTags: %s\n\n%s",
			s.Name, s.Description, strings.Join(s.Tags, ", "), s.Content)
		subChunks := splitIntoChunks(s.SourcePath, body, 512, 32)
		chunks = append(chunks, subChunks...)
	}
	return chunks
}
