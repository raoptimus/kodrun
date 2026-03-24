package rag

import _ "embed"

//go:embed docs/effective_go.md
var effectiveGoContent string

// ChunkEmbeddedDocs chunks all embedded reference documents for RAG indexing.
func ChunkEmbeddedDocs(chunkSize, chunkOverlap int) []Chunk {
	return splitIntoChunks("embedded://effective_go.md", effectiveGoContent, chunkSize, chunkOverlap)
}
