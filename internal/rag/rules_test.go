/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkRules_EmptySlice_ReturnsEmpty_Successfully(t *testing.T) {
	chunks := ChunkRules(nil, 128, 0)

	assert.Empty(t, chunks)
}

func TestChunkRules_SingleRule_ReturnsChunks_Successfully(t *testing.T) {
	rules := []RuleInfo{
		{
			Name:    "style",
			Content: "Use gofmt.",
			Path:    "",
		},
	}

	chunks := ChunkRules(rules, 128, 0)

	require.NotEmpty(t, chunks)
	assert.Equal(t, "rules://style", chunks[0].FilePath)
	assert.Contains(t, chunks[0].Content, "Rule: style")
	assert.Contains(t, chunks[0].Content, "Use gofmt.")
}

func TestChunkRules_WithExplicitPath_UsesPath_Successfully(t *testing.T) {
	rules := []RuleInfo{
		{
			Name:    "style",
			Content: "Use gofmt.",
			Path:    "/project/.kodrun/rules/style.md",
		},
	}

	chunks := ChunkRules(rules, 128, 0)

	require.NotEmpty(t, chunks)
	assert.Equal(t, "/project/.kodrun/rules/style.md", chunks[0].FilePath)
}

func TestChunkRules_MultipleRules_ProducesChunksForEach_Successfully(t *testing.T) {
	rules := []RuleInfo{
		{Name: "rule-a", Content: "Content A", Path: ""},
		{Name: "rule-b", Content: "Content B", Path: ""},
	}

	chunks := ChunkRules(rules, 128, 0)

	require.GreaterOrEqual(t, len(chunks), 2)

	hasA := false
	hasB := false
	for _, c := range chunks {
		if c.FilePath == "rules://rule-a" {
			hasA = true
		}
		if c.FilePath == "rules://rule-b" {
			hasB = true
		}
	}
	assert.True(t, hasA, "expected chunk for rule-a")
	assert.True(t, hasB, "expected chunk for rule-b")
}

func TestChunkRules_ZeroChunkSize_DefaultsTo128_Successfully(t *testing.T) {
	rules := []RuleInfo{
		{Name: "r", Content: "content", Path: ""},
	}

	chunksZero := ChunkRules(rules, 0, 0)
	chunksNeg := ChunkRules(rules, -5, 0)

	// Both should produce same result as default chunkSize=128.
	require.NotEmpty(t, chunksZero)
	require.NotEmpty(t, chunksNeg)
	assert.Equal(t, len(chunksZero), len(chunksNeg))
}

func TestChunkRefDocs_EmptySlice_ReturnsEmpty_Successfully(t *testing.T) {
	chunks := ChunkRefDocs(nil, 128, 0)

	assert.Empty(t, chunks)
}

func TestChunkRefDocs_SingleDoc_ReturnsChunks_Successfully(t *testing.T) {
	docs := []RefDocInfo{
		{
			Path:    "/docs/guide.md",
			Content: "Getting started guide.",
		},
	}

	chunks := ChunkRefDocs(docs, 128, 0)

	require.NotEmpty(t, chunks)
	assert.Equal(t, "/docs/guide.md", chunks[0].FilePath)
	assert.Contains(t, chunks[0].Content, "Documentation: guide.md")
	assert.Contains(t, chunks[0].Content, "Getting started guide.")
}

func TestChunkRefDocs_ZeroChunkSize_DefaultsTo128_Successfully(t *testing.T) {
	docs := []RefDocInfo{
		{Path: "/docs/a.md", Content: "hello"},
	}

	chunks := ChunkRefDocs(docs, 0, 0)

	require.NotEmpty(t, chunks)
}

func TestChunkRefDocs_MultipleDocs_ProducesChunksForEach_Successfully(t *testing.T) {
	docs := []RefDocInfo{
		{Path: "/docs/a.md", Content: "AAA"},
		{Path: "/docs/b.md", Content: "BBB"},
	}

	chunks := ChunkRefDocs(docs, 128, 0)

	require.GreaterOrEqual(t, len(chunks), 2)

	hasA := false
	hasB := false
	for _, c := range chunks {
		if c.FilePath == "/docs/a.md" {
			hasA = true
		}
		if c.FilePath == "/docs/b.md" {
			hasB = true
		}
	}
	assert.True(t, hasA, "expected chunk for a.md")
	assert.True(t, hasB, "expected chunk for b.md")
}
