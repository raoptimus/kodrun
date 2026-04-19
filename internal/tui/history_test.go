/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDedup_RemovesDuplicatesKeepingLastOccurrence_Successfully(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "no duplicates",
			input: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "duplicates keeps last",
			input: []string{"a", "b", "a", "c"},
			want:  []string{"b", "a", "c"},
		},
		{
			name:  "all same",
			input: []string{"x", "x", "x"},
			want:  []string{"x"},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "nil input",
			input: nil,
			want:  []string{},
		},
		{
			name:  "single element",
			input: []string{"only"},
			want:  []string{"only"},
		},
		{
			name:  "consecutive duplicates",
			input: []string{"a", "a", "b", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dedup(tt.input)

			if len(tt.want) == 0 {
				assert.Empty(t, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestLoadHistory_ReadsAndDeduplicatesEntries_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "first\nsecond\nfirst\nthird\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	got := LoadHistory(dir, 100)

	assert.Equal(t, []string{"second", "first", "third"}, got)
}

func TestLoadHistory_LimitsToMaxSize_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "a\nb\nc\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	got := LoadHistory(dir, 3)

	assert.Equal(t, []string{"c", "d", "e"}, got)
}

func TestLoadHistory_MissingFileReturnsNil_Successfully(t *testing.T) {
	dir := t.TempDir()

	got := LoadHistory(dir, 100)

	assert.Nil(t, got)
}

func TestLoadHistory_SkipsBlankLines_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "one\n\n  \ntwo\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	got := LoadHistory(dir, 100)

	assert.Equal(t, []string{"one", "two"}, got)
}

func TestLoadHistory_MaxSizeZero_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "a\nb\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	got := LoadHistory(dir, 0)

	assert.Empty(t, got)
}

func TestLoadHistory_MaxSizeOne_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "a\nb\nc\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	got := LoadHistory(dir, 1)

	assert.Equal(t, []string{"c"}, got)
}

func TestAppendHistory_AppendsEntryToFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	// Reset global counter to avoid trim side effects.
	appendCount = 0

	AppendHistory(dir, "hello", 100)

	data, err := os.ReadFile(filepath.Join(dir, ".kodrun", "history"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
}

func TestAppendHistory_SkipsEmptyEntry_Successfully(t *testing.T) {
	dir := t.TempDir()
	appendCount = 0

	AppendHistory(dir, "", 100)
	AppendHistory(dir, "   ", 100)

	_, err := os.Stat(filepath.Join(dir, ".kodrun", "history"))
	assert.True(t, os.IsNotExist(err))
}

func TestAppendHistory_TrimsEntryWhitespace_Successfully(t *testing.T) {
	dir := t.TempDir()
	appendCount = 0

	AppendHistory(dir, "  trimmed  ", 100)

	data, err := os.ReadFile(filepath.Join(dir, ".kodrun", "history"))
	require.NoError(t, err)
	assert.Equal(t, "trimmed\n", string(data))
}

func TestTrimHistory_DeduplicatesAndTruncatesFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	histDir := filepath.Join(dir, ".kodrun")
	require.NoError(t, os.MkdirAll(histDir, 0o755))

	content := "a\nb\na\nc\nd\ne\n"
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "history"), []byte(content), 0o644))

	TrimHistory(dir, 3)

	got := LoadHistory(dir, 100)
	assert.Equal(t, []string{"c", "d", "e"}, got)
}

func TestTrimHistory_EmptyFileDoesNothing_Successfully(t *testing.T) {
	dir := t.TempDir()

	TrimHistory(dir, 10)

	_, err := os.Stat(filepath.Join(dir, ".kodrun", "history"))
	assert.True(t, os.IsNotExist(err))
}
