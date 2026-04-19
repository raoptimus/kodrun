/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	_ "embed"
	"strings"
)

//go:embed docs/effective_go.md
var effectiveGoContent string

//go:embed docs/go_code_review_comments.md
var goCodeReviewCommentsContent string

//go:embed docs/go_common_mistakes.md
var goCommonMistakesContent string

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
	{Path: "embedded://go_code_review_comments.md", Lang: "go", Content: goCodeReviewCommentsContent},
	{Path: "embedded://go_common_mistakes.md", Lang: "go", Content: goCommonMistakesContent},
	// TODO: ship Python (PEP 8) and JS/TS (TypeScript handbook) reference docs.
}

// EmbeddedDocNames returns the human-readable names of embedded reference
// documents for the given language. An empty lang returns only language-neutral
// doc names. Names are derived from the Path field
// (e.g. "embedded://effective_go.md" → "effective_go").
func EmbeddedDocNames(lang string) []string {
	var out []string
	for _, d := range embeddedDocs {
		if d.Lang != "" && d.Lang != lang {
			continue
		}
		name := strings.TrimPrefix(d.Path, "embedded://")
		name = strings.TrimSuffix(name, ".md")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
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
