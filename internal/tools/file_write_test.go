package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileTool_Execute_CreatesNewFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "new.txt",
		"content": "hello world",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "wrote 11 bytes to new.txt")
	assert.Equal(t, "Add", result.Meta["action"])

	data, readErr := os.ReadFile(filepath.Join(dir, "new.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "hello world", string(data))
}

func TestWriteFileTool_Execute_OverwritesExistingFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("old"), 0o644))

	tool := NewWriteFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "existing.txt",
		"content": "new content",
	})

	require.NoError(t, err)
	assert.Equal(t, "Update", result.Meta["action"])
	assert.NotNil(t, result.Meta["diff"])

	data, readErr := os.ReadFile(filepath.Join(dir, "existing.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "new content", string(data))
}

func TestWriteFileTool_Execute_CreatesParentDirs_Successfully(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "deep/nested/dir/file.txt",
		"content": "data",
	})

	require.NoError(t, err)

	data, readErr := os.ReadFile(filepath.Join(dir, "deep", "nested", "dir", "file.txt"))
	require.NoError(t, readErr)
	assert.Equal(t, "data", string(data))
}

func TestWriteFileTool_Execute_EmptyPath_Failure(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"content": "data",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestWriteFileTool_Execute_ForbiddenPath_Failure(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir(), []string{"*.env"})

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "prod.env",
		"content": "SECRET=x",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestWriteFileTool_Execute_EmptyContent_Successfully(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "empty.txt",
		"content": "",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "wrote 0 bytes")
}

func TestWriteFileTool_ResolvePaths_Successfully(t *testing.T) {
	dir := t.TempDir()
	tool := NewWriteFileTool(dir, nil)

	paths := tool.ResolvePaths(map[string]any{"path": "f.go"})

	require.Len(t, paths, 1)
	assert.Equal(t, filepath.Join(dir, "f.go"), paths[0])
}

func TestWriteFileTool_ResolvePaths_EmptyPath_Successfully(t *testing.T) {
	tool := NewWriteFileTool(t.TempDir(), nil)

	paths := tool.ResolvePaths(map[string]any{})

	assert.Nil(t, paths)
}
