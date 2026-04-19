/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitIntoChunks(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"

	chunks := splitIntoChunks("test.go", content, 4, 1)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}

	// First chunk should start at line 1
	if chunks[0].StartLine != 1 {
		t.Errorf("first chunk start = %d, want 1", chunks[0].StartLine)
	}

	// All chunks should have the correct file path
	for _, c := range chunks {
		if c.FilePath != "test.go" {
			t.Errorf("chunk file = %q, want test.go", c.FilePath)
		}
	}
}

func TestSplitIntoChunks_Small(t *testing.T) {
	content := "a\nb\n"
	chunks := splitIntoChunks("small.go", content, 100, 10)
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}
}

func TestSplitIntoChunks_Empty(t *testing.T) {
	chunks := splitIntoChunks("empty.go", "", 10, 2)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty content, got %d", len(chunks))
	}
}

func TestIsIndexableFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"README.md", true},
		{"Makefile", true},
		{"Dockerfile", true},
		{"image.png", false},
		{"binary.exe", false},
		{"config.yaml", true},
		{"data.json", true},
	}
	for _, tt := range tests {
		if got := isIndexableFile(tt.path); got != tt.want {
			t.Errorf("isIndexableFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestSplitIntoChunks_OverlapGteChunkSize(t *testing.T) {
	content := "a\nb\nc\nd\ne\n"
	// overlap >= chunkSize should not cause infinite loop
	chunks := splitIntoChunks("test.go", content, 2, 10)
	if len(chunks) == 0 {
		t.Fatal("expected chunks")
	}
	// Verify all lines are covered
	lastEnd := chunks[len(chunks)-1].EndLine
	if lastEnd < 5 {
		t.Errorf("last chunk end = %d, want >= 5", lastEnd)
	}
}

func TestSplitIntoChunks_MaxChunkBytes(t *testing.T) {
	// Create a line that exceeds MaxChunkBytes
	longLine := strings.Repeat("x", MaxChunkBytes+100)
	content := longLine + "\nshort\n"
	chunks := splitIntoChunks("test.go", content, 100, 0)
	if len(chunks) == 0 {
		t.Fatal("expected chunks for content with long line")
	}
	// First chunk should contain the long line (even if it exceeds MaxChunkBytes, at least 1 line per chunk)
	if chunks[0].StartLine != 1 {
		t.Errorf("first chunk start = %d, want 1", chunks[0].StartLine)
	}
}

func TestChunkFiles_SkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()

	// Create hidden directory with a Go file
	hiddenDir := filepath.Join(dir, ".hidden")
	if err := os.MkdirAll(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hiddenDir, "secret.go"), []byte("package secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a visible file
	if err := os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	chunks, err := ChunkFiles(context.Background(), dir, []string{"."}, 100, 10)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range chunks {
		if strings.Contains(c.FilePath, ".hidden") {
			t.Errorf("hidden dir file should be skipped, got chunk from %s", c.FilePath)
		}
	}
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk (only visible.go), got %d", len(chunks))
	}
}

func TestChunkFiles_RespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := ChunkFiles(ctx, dir, []string{"."}, 100, 10)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestChunkFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a test file
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a non-indexable file
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50}, 0o644); err != nil {
		t.Fatal(err)
	}

	chunks, err := ChunkFiles(context.Background(), dir, []string{"."}, 100, 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk (only .go), got %d", len(chunks))
	}
}
