/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSnippet_HappyPath(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\n"
	if err := os.WriteFile(filepath.Join(dir, "example.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := extractSnippet(dir, Example{File: "example.go", Line: 5, Note: "test pattern"}, 6)
	if snippet == "" {
		t.Fatal("expected non-empty snippet")
	}
	if !strings.Contains(snippet, "example.go:5 (test pattern)") {
		t.Errorf("header missing, got:\n%s", snippet)
	}
	// Should start from line 2 (5 - snippetContext=3)
	if !strings.Contains(snippet, "2 | line2") {
		t.Errorf("expected line 2, got:\n%s", snippet)
	}
	if !strings.Contains(snippet, "5 | line5") {
		t.Errorf("expected line 5, got:\n%s", snippet)
	}
}

func TestExtractSnippet_LineZero(t *testing.T) {
	dir := t.TempDir()
	content := "a\nb\nc\nd\ne\n"
	if err := os.WriteFile(filepath.Join(dir, "f.go"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := extractSnippet(dir, Example{File: "f.go", Line: 0}, 3)
	if !strings.Contains(snippet, "1 | a") {
		t.Errorf("expected start from line 1, got:\n%s", snippet)
	}
}

func TestExtractSnippet_FileMissing(t *testing.T) {
	snippet := extractSnippet(t.TempDir(), Example{File: "nonexistent.go", Line: 1}, 10)
	if snippet != "" {
		t.Errorf("expected empty for missing file, got: %s", snippet)
	}
}

func TestExtractSnippet_LineOutOfBounds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "short.go"), []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	snippet := extractSnippet(dir, Example{File: "short.go", Line: 100}, 10)
	if snippet != "" {
		t.Errorf("expected empty for out-of-bounds line, got: %s", snippet)
	}
}

func TestFormatStepExamples_Budget(t *testing.T) {
	dir := t.TempDir()
	// Create a file with 20 lines.
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, "code")
	}
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	examples := []Example{
		{File: "big.go", Line: 1, Note: "first"},
		{File: "big.go", Line: 10, Note: "second"},
	}

	// Budget of 10 lines — second example should be truncated or skipped.
	result := formatStepExamples(dir, examples, 10)
	if result == "" {
		t.Fatal("expected non-empty result")
	}
	// Count actual code lines (lines with " | " pattern).
	codeLines := 0
	for _, l := range strings.Split(result, "\n") {
		if strings.Contains(l, " | ") {
			codeLines++
		}
	}
	if codeLines > 10 {
		t.Errorf("budget exceeded: got %d code lines, want <= 10", codeLines)
	}
}

func TestFormatStepExamples_Empty(t *testing.T) {
	result := formatStepExamples(t.TempDir(), nil, 60)
	if result != "" {
		t.Errorf("expected empty for nil examples, got: %s", result)
	}
}

func TestFormatStepExamples_MissingFiles(t *testing.T) {
	examples := []Example{
		{File: "gone.go", Line: 1, Note: "missing"},
	}
	result := formatStepExamples(t.TempDir(), examples, 60)
	if result != "" {
		t.Errorf("expected empty when all files missing, got: %s", result)
	}
}
