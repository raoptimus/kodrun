package rag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkEmbeddedDocs(t *testing.T) {
	chunks := ChunkEmbeddedDocs("go", 128, 64)

	require.NotEmpty(t, chunks)

	for _, c := range chunks {
		assert.Equal(t, "embedded://effective_go.md", c.FilePath)
		assert.NotEmpty(t, c.Content)
		assert.Greater(t, c.StartLine, 0)
		assert.GreaterOrEqual(t, c.EndLine, c.StartLine)
	}
}
