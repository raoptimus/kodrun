/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileTool(t *testing.T) {
	dir := t.TempDir()
	content := "hello world"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 500)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	expected := "   1 | hello world\n"
	if result.Output != expected {
		t.Errorf("got %q, want %q", result.Output, expected)
	}
}

func TestReadFileTool_Forbidden(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, []string{"*.env"}, 500)
	_, err := tool.Execute(context.Background(), map[string]any{"path": ".env"})
	if err == nil {
		t.Error("expected forbidden access to fail")
	}
}

func TestReadFileTool_SmallFile(t *testing.T) {
	dir := t.TempDir()
	lines := []string{"line1", "line2", "line3"}
	if err := os.WriteFile(filepath.Join(dir, "small.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 500)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "small.txt"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if strings.Contains(result.Output, "[truncated]") {
		t.Error("small file should not be truncated")
	}
	if !strings.Contains(result.Output, "   1 | line1") {
		t.Errorf("expected line numbers, got: %s", result.Output)
	}
}

func TestReadFileTool_Truncation(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	totalLines := 20
	for i := range totalLines {
		fmt.Fprintf(&b, "line %d\n", i+1)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 5)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "big.txt"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !strings.Contains(result.Output, "[truncated]") {
		t.Error("large file should be truncated")
	}
	if !strings.Contains(result.Output, "   1 | line 1") {
		t.Errorf("expected first line, got: %s", result.Output)
	}
	if result.Meta["truncated"] != true {
		t.Error("meta.truncated should be true")
	}
}

func TestReadFileTool_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "line %d\n", i+1)
	}
	if err := os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 500)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   "lines.txt",
		"offset": float64(10),
		"limit":  float64(5),
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if !strings.Contains(result.Output, "  11 | line 11") {
		t.Errorf("expected line 11, got: %s", result.Output)
	}
	if !strings.Contains(result.Output, "  15 | line 15") {
		t.Errorf("expected line 15, got: %s", result.Output)
	}
	if result.Meta["offset"] != 10 {
		t.Errorf("expected offset=10, got %v", result.Meta["offset"])
	}
}

func TestReadFileTool_OffsetBeyondEnd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "short.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 500)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":   "short.txt",
		"offset": float64(100),
	})
	if err == nil {
		t.Error("expected failure for offset beyond file end")
	}
}

func TestReadFileTool_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.txt"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil, 500)
	_, err := tool.Execute(context.Background(), map[string]any{"path": "empty.txt"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
}

func TestWriteFileTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "sub/new.txt",
		"content": "new content",
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sub", "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Errorf("got %q, want %q", string(data), "new content")
	}
}

func TestEditFileTool(t *testing.T) {
	dir := t.TempDir()
	original := "func main() {\n\tfmt.Println(\"hello\")\n}"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir, nil)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "main.go",
		"old_str": "hello",
		"new_str": "world",
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "func main() {\n\tfmt.Println(\"world\")\n}"
	if string(data) != expected {
		t.Errorf("got %q, want %q", string(data), expected)
	}
}

func TestEditFileTool_NotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.go"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewEditFileTool(dir, nil)
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.go",
		"old_str": "xyz",
		"new_str": "123",
	})
	if err == nil {
		t.Error("expected failure when old_str not found")
	}
}

func TestListDirTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	tool := NewListDirTool(dir, nil)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.Output == "" {
		t.Error("expected non-empty output")
	}
}

func TestDeleteFileTool(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "delete-me.txt")
	os.WriteFile(filePath, []byte("bye"), 0o644)

	tool := NewDeleteFileTool(dir, nil)
	_, err := tool.Execute(context.Background(), map[string]any{"path": "delete-me.txt"})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}
