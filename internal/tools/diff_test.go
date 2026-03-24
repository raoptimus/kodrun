package tools

import (
	"strings"
	"testing"
)

func TestLineStats(t *testing.T) {
	tests := []struct {
		name           string
		old, new       string
		wantAdd, wantRm int
	}{
		{"empty to empty", "", "", 0, 0},
		{"empty to content", "", "a\nb\n", 2, 0},
		{"content to empty", "a\nb\n", "", 0, 2},
		{"no change", "a\nb\n", "a\nb\n", 0, 0},
		{"add line", "a\n", "a\nb\n", 1, 0},
		{"remove line", "a\nb\n", "a\n", 0, 1},
		{"replace line", "a\nb\n", "a\nc\n", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := LineStats(tt.old, tt.new)
			if added != tt.wantAdd || removed != tt.wantRm {
				t.Errorf("LineStats() = (%d, %d), want (%d, %d)", added, removed, tt.wantAdd, tt.wantRm)
			}
		})
	}
}

func TestFileActionType(t *testing.T) {
	if got := FileActionType(true); got != "Update" {
		t.Errorf("FileActionType(true) = %q, want Update", got)
	}
	if got := FileActionType(false); got != "Add" {
		t.Errorf("FileActionType(false) = %q, want Add", got)
	}
}

func TestSimpleDiff(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nmodified\nline3\n"

	diff := SimpleDiff(old, new, "test.go", 30)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "-line2") {
		t.Errorf("diff should contain removed line, got:\n%s", diff)
	}
	if !strings.Contains(diff, "+modified") {
		t.Errorf("diff should contain added line, got:\n%s", diff)
	}
}

func TestSimpleDiff_NoChange(t *testing.T) {
	content := "a\nb\nc\n"
	diff := SimpleDiff(content, content, "test.go", 30)
	if diff != "" {
		t.Errorf("expected empty diff for identical content, got: %q", diff)
	}
}

func TestSimpleDiff_MaxLines(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	new := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"

	diff := SimpleDiff(old, new, "test.go", 5)
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	// Should be limited
	if len(lines) > 8 { // 5 + header + "..." lines
		t.Errorf("expected limited diff lines, got %d lines:\n%s", len(lines), diff)
	}
}
