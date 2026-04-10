package rag

import (
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float64
		want float64
	}{
		{"identical", []float64{1, 0, 0}, []float64{1, 0, 0}, 1.0},
		{"orthogonal", []float64{1, 0, 0}, []float64{0, 1, 0}, 0.0},
		{"opposite", []float64{1, 0}, []float64{-1, 0}, -1.0},
		{"empty", nil, nil, 0.0},
		{"mismatch", []float64{1}, []float64{1, 2}, 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("cosineSimilarity() = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestChunkHash(t *testing.T) {
	c1 := Chunk{FilePath: "a.go", Content: "hello", StartLine: 1, EndLine: 5}
	c2 := Chunk{FilePath: "a.go", Content: "hello", StartLine: 1, EndLine: 5}
	c3 := Chunk{FilePath: "b.go", Content: "hello", StartLine: 1, EndLine: 5}

	if chunkHash(c1) != chunkHash(c2) {
		t.Error("identical chunks should have same hash")
	}
	if chunkHash(c1) == chunkHash(c3) {
		t.Error("different chunks should have different hash")
	}
}

func TestTruncateInput_ShortInput_ReturnsUnchanged_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxBytes int
		expected string
	}{
		{
			name:     "Empty string",
			input:    "",
			maxBytes: 100,
			expected: "",
		},
		{
			name:     "Exactly at limit",
			input:    "abcde",
			maxBytes: 5,
			expected: "abcde",
		},
		{
			name:     "Below limit",
			input:    "abc",
			maxBytes: 10,
			expected: "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateInput(tt.input, tt.maxBytes)

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncateInput_LongInputWithNewline_TruncatesAtLastNewline_Successfully(t *testing.T) {
	// Build input: "line1\nline2\nline3..." where total > maxBytes.
	// The newline at position > maxBytes/2 should be the truncation point.
	input := "aaaa\nbbbb\ncccc\ndddd\neeee"
	maxBytes := 15

	result := truncateInput(input, maxBytes)

	// maxBytes=15 -> truncated[:15] = "aaaa\nbbbb\ncccc\n" (roughly)
	// LastIndex of "\n" in that slice should be > 15/2=7 => truncates at newline
	assert.LessOrEqual(t, len(result), maxBytes)
	assert.True(t, strings.HasSuffix(result, "bbbb") || strings.HasSuffix(result, "cccc"),
		"expected truncation at newline boundary, got: %q", result)
}

func TestTruncateInput_LongInputNoNewlineNearEnd_TruncatesAtMaxBytes_Successfully(t *testing.T) {
	input := strings.Repeat("x", 100)
	maxBytes := 50

	result := truncateInput(input, maxBytes)

	assert.Equal(t, 50, len(result))
	assert.Equal(t, strings.Repeat("x", 50), result)
}

func TestIndex_HasLegacyCodeChunks_NoEntries_ReturnsFalse_Successfully(t *testing.T) {
	idx := &Index{}

	assert.False(t, idx.hasLegacyCodeChunks())
}

func TestIndex_HasLegacyCodeChunks_OnlyConventionSources_ReturnsFalse_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
	}{
		{name: "rules prefix", filePath: "rules://style"},
		{name: "snippets prefix", filePath: "snippets://grpc"},
		{name: "embedded prefix", filePath: "embedded://effective_go.md"},
		{name: "godoc prefix", filePath: "godoc://fmt"},
		{name: "kodrun path", filePath: "/project/.kodrun/rules/style.md"},
		{name: "docs path", filePath: "/project/docs/guide.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := &Index{
				entries: []IndexEntry{
					{Chunk: Chunk{FilePath: tt.filePath}},
				},
			}

			assert.False(t, idx.hasLegacyCodeChunks())
		})
	}
}

func TestIndex_HasLegacyCodeChunks_HasCodeFile_ReturnsTrue_Successfully(t *testing.T) {
	idx := &Index{
		entries: []IndexEntry{
			{Chunk: Chunk{FilePath: "rules://style"}},
			{Chunk: Chunk{FilePath: "internal/server/handler.go"}},
		},
	}

	assert.True(t, idx.hasLegacyCodeChunks())
}

func TestIndex_HasLegacyCodeChunks_OnlyCodeFiles_ReturnsTrue_Successfully(t *testing.T) {
	idx := &Index{
		entries: []IndexEntry{
			{Chunk: Chunk{FilePath: "main.go"}},
		},
	}

	assert.True(t, idx.hasLegacyCodeChunks())
}

func TestIndex_LoadSaveRoundTrip_Successfully(t *testing.T) {
	dir := t.TempDir()

	idx := &Index{
		path: dir,
		entries: []IndexEntry{
			{
				Chunk:     Chunk{FilePath: "rules://test", Content: "hello", StartLine: 1, EndLine: 1},
				Embedding: []float64{0.1, 0.2, 0.3},
				Hash:      "abc123",
			},
		},
	}

	err := idx.Save()
	require.NoError(t, err)

	loaded := &Index{path: dir}
	err = loaded.Load()
	require.NoError(t, err)

	require.Len(t, loaded.entries, 1)
	assert.Equal(t, "rules://test", loaded.entries[0].Chunk.FilePath)
	assert.Equal(t, "hello", loaded.entries[0].Chunk.Content)
	assert.Equal(t, []float64{0.1, 0.2, 0.3}, loaded.entries[0].Embedding)
	assert.Equal(t, "abc123", loaded.entries[0].Hash)
}

func TestIndex_Load_NoFile_ReturnsNilError_Successfully(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{path: dir}

	err := idx.Load()

	assert.NoError(t, err)
	assert.Empty(t, idx.entries)
}

func TestIndex_Load_EmptyPath_NoOp_Successfully(t *testing.T) {
	idx := &Index{path: ""}

	err := idx.Load()

	assert.NoError(t, err)
}

func TestIndex_Save_EmptyPath_NoOp_Successfully(t *testing.T) {
	idx := &Index{path: ""}

	err := idx.Save()

	assert.NoError(t, err)
}

func TestIndex_Reset_ClearsEntries_Successfully(t *testing.T) {
	dir := t.TempDir()
	idx := &Index{
		path: dir,
		entries: []IndexEntry{
			{Chunk: Chunk{FilePath: "test.go"}},
		},
	}

	err := idx.Reset()

	require.NoError(t, err)
	assert.Empty(t, idx.entries)
	assert.True(t, idx.updated.IsZero())
}

func TestIndex_Size_ReturnsEntryCount_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		entries  []IndexEntry
		expected int
	}{
		{
			name:     "Empty index",
			entries:  nil,
			expected: 0,
		},
		{
			name: "Two entries",
			entries: []IndexEntry{
				{Chunk: Chunk{FilePath: "a.go"}},
				{Chunk: Chunk{FilePath: "b.go"}},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := &Index{entries: tt.entries}

			assert.Equal(t, tt.expected, idx.Size())
		})
	}
}
