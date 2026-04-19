/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"fmt"
	"path/filepath"
)

// RuleInfo holds the rule metadata needed for chunking.
type RuleInfo struct {
	Name    string
	Content string
	Path    string
}

// ChunkRules converts rules into RAG chunks. chunkSize/chunkOverlap come
// from cfg.RAG so rules use the same chunking parameters as project files —
// otherwise hashes built with different sizes never align across reindexes.
func ChunkRules(rules []RuleInfo, chunkSize, chunkOverlap int) []Chunk {
	if chunkSize <= 0 {
		chunkSize = 128
	}
	chunks := make([]Chunk, 0, len(rules))
	for _, r := range rules {
		body := fmt.Sprintf("Rule: %s\n\n%s", r.Name, r.Content)
		relPath := r.Path
		if relPath == "" {
			relPath = "rules://" + r.Name
		}
		subChunks := splitIntoChunks(relPath, body, chunkSize, chunkOverlap)
		chunks = append(chunks, subChunks...)
	}
	return chunks
}

// RefDocInfo holds reference documentation metadata for chunking.
type RefDocInfo struct {
	Path    string
	Content string
}

// ChunkRefDocs converts reference documents into RAG chunks.
func ChunkRefDocs(docs []RefDocInfo, chunkSize, chunkOverlap int) []Chunk {
	if chunkSize <= 0 {
		chunkSize = 128
	}
	chunks := make([]Chunk, 0, len(docs))
	for _, d := range docs {
		name := filepath.Base(d.Path)
		body := fmt.Sprintf("Documentation: %s\n\n%s", name, d.Content)
		subChunks := splitIntoChunks(d.Path, body, chunkSize, chunkOverlap)
		chunks = append(chunks, subChunks...)
	}
	return chunks
}
