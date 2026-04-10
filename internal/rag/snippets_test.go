package rag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkSnippets_EmptySlice_ReturnsEmpty_Successfully(t *testing.T) {
	chunks := ChunkSnippets(nil)

	assert.Empty(t, chunks)
}

func TestChunkSnippets_SingleSnippet_ReturnsChunks_Successfully(t *testing.T) {
	snippets := []SnippetInfo{
		{
			Name:        "grpc-handler",
			Description: "gRPC handler template",
			Tags:        []string{"grpc", "handler"},
			Content:     "func (s *Server) Handle(ctx context.Context) {}",
			SourcePath:  "snippets://grpc-handler",
		},
	}

	chunks := ChunkSnippets(snippets)

	require.NotEmpty(t, chunks)
	assert.Equal(t, "snippets://grpc-handler", chunks[0].FilePath)
	assert.Contains(t, chunks[0].Content, "Snippet: grpc-handler")
	assert.Contains(t, chunks[0].Content, "Description: gRPC handler template")
	assert.Contains(t, chunks[0].Content, "Tags: grpc, handler")
	assert.Contains(t, chunks[0].Content, "func (s *Server) Handle")
}

func TestChunkSnippets_EmptyTags_FormattedCorrectly_Successfully(t *testing.T) {
	snippets := []SnippetInfo{
		{
			Name:        "simple",
			Description: "A simple snippet",
			Tags:        nil,
			Content:     "hello",
			SourcePath:  "snippets://simple",
		},
	}

	chunks := ChunkSnippets(snippets)

	require.NotEmpty(t, chunks)
	assert.Contains(t, chunks[0].Content, "Tags: \n")
}

func TestChunkSnippets_MultipleSnippets_ProducesChunksForEach_Successfully(t *testing.T) {
	snippets := []SnippetInfo{
		{
			Name:       "alpha",
			Content:    "aaa",
			SourcePath: "snippets://alpha",
		},
		{
			Name:       "beta",
			Content:    "bbb",
			SourcePath: "snippets://beta",
		},
	}

	chunks := ChunkSnippets(snippets)

	require.GreaterOrEqual(t, len(chunks), 2)

	hasAlpha := false
	hasBeta := false
	for _, c := range chunks {
		if c.FilePath == "snippets://alpha" {
			hasAlpha = true
		}
		if c.FilePath == "snippets://beta" {
			hasBeta = true
		}
	}
	assert.True(t, hasAlpha, "expected chunk for alpha")
	assert.True(t, hasBeta, "expected chunk for beta")
}

func TestChunkSnippets_LargeSnippet_SplitsIntoMultipleChunks_Successfully(t *testing.T) {
	// Create content larger than snippetChunkMaxBytes (512).
	largeContent := ""
	for i := range 100 {
		largeContent += "// line " + string(rune('A'+i%26)) + " of the snippet\n"
	}

	snippets := []SnippetInfo{
		{
			Name:       "big",
			Content:    largeContent,
			SourcePath: "snippets://big",
		},
	}

	chunks := ChunkSnippets(snippets)

	assert.Greater(t, len(chunks), 1)
	for _, c := range chunks {
		assert.Equal(t, "snippets://big", c.FilePath)
	}
}
