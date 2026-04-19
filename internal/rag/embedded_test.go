package rag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedDocNames(t *testing.T) {
	t.Run("go returns 3 names", func(t *testing.T) {
		names := EmbeddedDocNames("go")
		assert.Len(t, names, 3)
		assert.Contains(t, names, "effective_go")
		assert.Contains(t, names, "go_code_review_comments")
		assert.Contains(t, names, "go_common_mistakes")
	})

	t.Run("unknown lang returns empty", func(t *testing.T) {
		names := EmbeddedDocNames("ruby")
		assert.Empty(t, names)
	})

	t.Run("empty lang returns empty", func(t *testing.T) {
		names := EmbeddedDocNames("")
		assert.Empty(t, names)
	})
}

func TestChunkEmbeddedDocs(t *testing.T) {
	chunks := ChunkEmbeddedDocs("go", 128, 64)

	require.NotEmpty(t, chunks)

	allowedPaths := map[string]bool{
		"embedded://effective_go.md":            true,
		"embedded://go_code_review_comments.md": true,
		"embedded://go_common_mistakes.md":      true,
	}

	for _, c := range chunks {
		assert.True(t, allowedPaths[c.FilePath], "unexpected embedded doc path: %s", c.FilePath)
		assert.NotEmpty(t, c.Content)
		assert.Greater(t, c.StartLine, 0)
		assert.GreaterOrEqual(t, c.EndLine, c.StartLine)
	}
}
