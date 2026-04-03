package rag

import _ "embed"

//go:embed docs/effective_go.md
var effectiveGoContent string

// embeddedDoc describes one built-in reference document and the language
// it applies to. An empty Lang means the document is language-neutral.
type embeddedDoc struct {
	Path    string
	Lang    string
	Content string
}

// embeddedDocs is the registry of built-in reference docs. Add new entries
// here when shipping per-language documentation. Empty Lang documents are
// always included regardless of the requested filter.
var embeddedDocs = []embeddedDoc{
	{Path: "embedded://effective_go.md", Lang: "go", Content: effectiveGoContent},
	// TODO: ship Python (PEP 8) and JS/TS (TypeScript handbook) reference docs.
}

// ChunkEmbeddedDocs returns chunks for embedded reference documents whose
// Lang matches the given lang or that are language-neutral. An empty lang
// returns only the language-neutral subset.
func ChunkEmbeddedDocs(lang string, chunkSize, chunkOverlap int) []Chunk {
	var out []Chunk
	for _, d := range embeddedDocs {
		if d.Lang != "" && d.Lang != lang {
			continue
		}
		out = append(out, splitIntoChunks(d.Path, d.Content, chunkSize, chunkOverlap)...)
	}
	return out
}
