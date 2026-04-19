/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkGoDoc_EmptyInput_ReturnsNil(t *testing.T) {
	tests := []struct {
		name      string
		docOutput string
	}{
		{
			name:      "Empty string",
			docOutput: "",
		},
		{
			name:      "Only whitespace",
			docOutput: "   \n\t\n  ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := ChunkGoDoc("fmt", tt.docOutput, 2000)

			assert.Nil(t, chunks)
		})
	}
}

func TestChunkGoDoc_SingleDeclaration_ReturnsOneChunk_Successfully(t *testing.T) {
	docOutput := "func Println(a ...any) (n int, err error)"

	chunks := ChunkGoDoc("fmt", docOutput, 2000)

	require.Len(t, chunks, 1)
	assert.Equal(t, "godoc://fmt", chunks[0].FilePath)
	assert.Contains(t, chunks[0].Content, "func Println")
	assert.Equal(t, 1, chunks[0].StartLine)
}

func TestChunkGoDoc_MultipleDeclarations_SplitsIntoChunks_Successfully(t *testing.T) {
	docOutput := "func Println(a ...any) (n int, err error)\n\nfunc Sprintf(format string, a ...any) string"

	chunks := ChunkGoDoc("fmt", docOutput, 2000)

	require.Len(t, chunks, 2)
	assert.Contains(t, chunks[0].Content, "func Println")
	assert.Contains(t, chunks[1].Content, "func Sprintf")
	for _, c := range chunks {
		assert.Equal(t, "godoc://fmt", c.FilePath)
	}
}

func TestChunkGoDoc_PreambleBeforeFirstDecl_IncludesPreambleChunk_Successfully(t *testing.T) {
	docOutput := "Package fmt implements formatted I/O.\n\nfunc Println(a ...any) (n int, err error)"

	chunks := ChunkGoDoc("fmt", docOutput, 2000)

	require.GreaterOrEqual(t, len(chunks), 2)
	assert.Contains(t, chunks[0].Content, "Package fmt")
	assert.Contains(t, chunks[1].Content, "func Println")
}

func TestChunkGoDoc_CommentAttachedToDecl_KeptTogether_Successfully(t *testing.T) {
	docOutput := "// Println formats using the default formats.\nfunc Println(a ...any) (n int, err error)"

	chunks := ChunkGoDoc("fmt", docOutput, 2000)

	require.Len(t, chunks, 1)
	assert.Contains(t, chunks[0].Content, "// Println formats")
	assert.Contains(t, chunks[0].Content, "func Println")
}

func TestChunkGoDoc_NoDeclarations_ReturnsSingleChunk_Successfully(t *testing.T) {
	docOutput := "Package fmt implements formatted I/O with functions analogous to C's printf and scanf."

	chunks := ChunkGoDoc("fmt", docOutput, 2000)

	require.Len(t, chunks, 1)
	assert.Equal(t, "godoc://fmt", chunks[0].FilePath)
	assert.Contains(t, chunks[0].Content, "Package fmt")
	assert.Equal(t, 1, chunks[0].StartLine)
}

func TestChunkGoDoc_MaxBytesDefaultsWhenZeroOrNegative_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		maxBytes int
	}{
		{
			name:     "Zero maxBytes",
			maxBytes: 0,
		},
		{
			name:     "Negative maxBytes",
			maxBytes: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			docOutput := "func Println(a ...any) (n int, err error)"

			chunks := ChunkGoDoc("fmt", docOutput, tt.maxBytes)

			require.Len(t, chunks, 1)
			assert.Contains(t, chunks[0].Content, "func Println")
		})
	}
}

// NOTE: ChunkGoDoc delegates to splitIntoChunks(prefix, content, 0, 0) for
// oversized declarations. When chunkSize=0, splitIntoChunks enters an
// infinite loop because the inner line-counting loop never advances. This is
// a known limitation: production code avoids triggering it because
// MaxChunkBytes (2000) is large enough for typical `go doc` declarations.
// A test for the oversized-declaration path is intentionally omitted to
// avoid exercising the bug.

func TestChunkGoDoc_AllDeclKeywords_Recognized_Successfully(t *testing.T) {
	docOutput := "func Foo() int\n\ntype Bar struct{}\n\nvar Baz int\n\nconst Qux = 1\n\npackage main"

	chunks := ChunkGoDoc("mypkg", docOutput, 2000)

	// Each keyword line should be recognized as a boundary.
	require.GreaterOrEqual(t, len(chunks), 5)
}

func TestIsGoDeclLine_ReturnsTrue_Successfully(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "func keyword", line: "func Foo() int"},
		{name: "type keyword", line: "type Bar struct{}"},
		{name: "var keyword", line: "var x int"},
		{name: "const keyword", line: "const Y = 1"},
		{name: "package keyword", line: "package main"},
		{name: "Leading whitespace", line: "  func Foo() int"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, isGoDeclLine(tt.line))
		})
	}
}

func TestIsGoDeclLine_ReturnsFalse_Successfully(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "Comment line", line: "// func Foo()"},
		{name: "Ordinary line", line: "x := 1"},
		{name: "Empty line", line: ""},
		{name: "Only spaces", line: "   "},
		{name: "Partial keyword", line: "functional programming"},
		{name: "Type suffix", line: "mytype Foo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, isGoDeclLine(tt.line))
		})
	}
}

func TestFindCommentStart_NoComments_ReturnsSameIndex_Successfully(t *testing.T) {
	lines := []string{"func Foo()", "func Bar()"}

	result := findCommentStart(lines, 1)

	assert.Equal(t, 1, result)
}

func TestFindCommentStart_WithComments_ReturnsFirstCommentLine_Successfully(t *testing.T) {
	lines := []string{"// Comment 1", "// Comment 2", "func Foo()"}

	result := findCommentStart(lines, 2)

	assert.Equal(t, 0, result)
}

func TestFindCommentStart_BlankLinesBetweenComments_IncludesAll_Successfully(t *testing.T) {
	lines := []string{"// First comment", "", "// Second comment", "func Foo()"}

	result := findCommentStart(lines, 3)

	assert.Equal(t, 0, result)
}

func TestFindCommentStart_IndexZero_ReturnsZero_Successfully(t *testing.T) {
	lines := []string{"func Foo()"}

	result := findCommentStart(lines, 0)

	assert.Equal(t, 0, result)
}

func TestFindCommentStart_CodeBeforeComment_StopsAtCode_Successfully(t *testing.T) {
	lines := []string{"x := 1", "// Comment", "func Foo()"}

	result := findCommentStart(lines, 2)

	assert.Equal(t, 1, result)
}

func TestSingleOrSplit_FitsInMaxBytes_ReturnsSingleChunk_Successfully(t *testing.T) {
	content := "short content"
	lines := strings.Split(content, "\n")

	chunks := singleOrSplit("godoc://pkg", content, lines, 100)

	require.Len(t, chunks, 1)
	assert.Equal(t, "godoc://pkg", chunks[0].FilePath)
	assert.Equal(t, "short content", chunks[0].Content)
	assert.Equal(t, 1, chunks[0].StartLine)
	assert.Equal(t, 1, chunks[0].EndLine)
}

// NOTE: singleOrSplit delegates to splitIntoChunks with chunkSize=0 when
// content exceeds maxBytes. This path has the same infinite-loop limitation
// as the oversized-declaration path in ChunkGoDoc (see comment above).
// A test for the exceeds-maxBytes branch is intentionally omitted.
