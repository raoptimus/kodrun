package rag

import (
	"fmt"
	"strings"
)

const (
	snippetChunkMaxBytes = 512 // max bytes per snippet chunk
	snippetChunkOverlap  = 32  // overlap bytes between snippet chunks
)

// SnippetInfo holds the snippet metadata needed for chunking.
type SnippetInfo struct {
	Name        string
	Description string
	Tags        []string
	Requires    []string
	Content     string
	SourcePath  string
}

// ChunkSnippets converts snippets into RAG chunks.
// Large snippets are automatically split via splitIntoChunks to respect byte limits.
// Snippets whose Requires are not satisfied by projectTech are skipped.
func ChunkSnippets(snippets []SnippetInfo, projectTech []string) []Chunk {
	techSet := toStringSet(projectTech)
	chunks := make([]Chunk, 0, len(snippets))
	for _, s := range snippets {
		if !requiresMet(s.Requires, techSet) {
			continue
		}
		body := fmt.Sprintf("Snippet: %s\nDescription: %s\nTags: %s\n\n%s",
			s.Name, s.Description, strings.Join(s.Tags, ", "), s.Content)
		subChunks := splitIntoChunks(s.SourcePath, body, snippetChunkMaxBytes, snippetChunkOverlap)
		chunks = append(chunks, subChunks...)
	}
	return chunks
}

// requiresMet returns true if all required items are present in the available set.
// An empty requires list is always satisfied.
func requiresMet(requires []string, available map[string]bool) bool {
	for _, r := range requires {
		if !available[r] {
			return false
		}
	}
	return true
}

func toStringSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
