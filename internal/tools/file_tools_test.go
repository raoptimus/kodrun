package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileTool(t *testing.T) {
	dir := t.TempDir()
	content := "hello world"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "test.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if result.Output != content {
		t.Errorf("got %q, want %q", result.Output, content)
	}
}

func TestReadFileTool_Forbidden(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir, []string{"*.env"})
	result, err := tool.Execute(context.Background(), map[string]any{"path": ".env"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected forbidden access to fail")
	}
}

func TestWriteFileTool(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "sub/new.txt",
		"content": "new content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
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
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "main.go",
		"old_str": "hello",
		"new_str": "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
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
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "test.go",
		"old_str": "xyz",
		"new_str": "123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success {
		t.Error("expected failure when old_str not found")
	}
}

func TestListDirTool(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)

	tool := NewListDirTool(dir)
	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
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
	result, err := tool.Execute(context.Background(), map[string]any{"path": "delete-me.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success {
		t.Fatalf("expected success, got error: %s", result.Error)
	}
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}
