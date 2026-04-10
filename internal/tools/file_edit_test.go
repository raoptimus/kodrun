package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEditFileTool_Execute_ReplacesOccurrence_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte("hello world\n"), 0o644))

	tool := NewEditFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "f.go",
		"old_str": "hello",
		"new_str": "goodbye",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "replaced 1 occurrence")
	assert.Equal(t, "Update", result.Meta["action"])

	data, readErr := os.ReadFile(filepath.Join(dir, "f.go"))
	require.NoError(t, readErr)
	assert.Equal(t, "goodbye world\n", string(data))
}

func TestEditFileTool_Execute_MultipleOccurrences_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte("aaa bbb aaa\n"), 0o644))

	tool := NewEditFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "f.go",
		"old_str": "aaa",
		"new_str": "ccc",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "replaced 1 of 2 occurrences")
	assert.Contains(t, result.Output, "1 more remain")

	data, readErr := os.ReadFile(filepath.Join(dir, "f.go"))
	require.NoError(t, readErr)
	assert.Equal(t, "ccc bbb aaa\n", string(data))
}

func TestEditFileTool_Execute_OldStrNotFound_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte("abc\n"), 0o644))

	tool := NewEditFileTool(dir, nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "f.go",
		"old_str": "xyz",
		"new_str": "123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "old_str not found in file")
}

func TestEditFileTool_Execute_MissingParams_Failure(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		errMsg string
	}{
		{
			name:   "missing path",
			params: map[string]any{"old_str": "a", "new_str": "b"},
			errMsg: "path must be a string",
		},
		{
			name:   "missing old_str",
			params: map[string]any{"path": "f.go", "new_str": "b"},
			errMsg: "old_str must be a string",
		},
		{
			name:   "missing new_str",
			params: map[string]any{"path": "f.go", "old_str": "a"},
			errMsg: "new_str must be a string",
		},
		{
			name:   "empty path and old_str",
			params: map[string]any{"path": "", "old_str": "", "new_str": "b"},
			errMsg: "path and old_str are required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewEditFileTool(t.TempDir(), nil)

			_, err := tool.Execute(context.Background(), tt.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestEditFileTool_Execute_ForbiddenPath_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644))

	tool := NewEditFileTool(dir, []string{"*.env"})

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    ".env",
		"old_str": "SECRET",
		"new_str": "PUBLIC",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestEditFileTool_Execute_NonexistentFile_Failure(t *testing.T) {
	tool := NewEditFileTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "missing.go",
		"old_str": "a",
		"new_str": "b",
	})

	require.Error(t, err)
}

func TestEditFileTool_ResolvePaths_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.go"), []byte(""), 0o644))

	tool := NewEditFileTool(dir, nil)

	paths := tool.ResolvePaths(map[string]any{"path": "f.go"})

	require.Len(t, paths, 1)
	assert.Equal(t, filepath.Join(dir, "f.go"), paths[0])
}

func TestEditFileTool_ResolvePaths_EmptyPath_Successfully(t *testing.T) {
	tool := NewEditFileTool(t.TempDir(), nil)

	paths := tool.ResolvePaths(map[string]any{})

	assert.Nil(t, paths)
}
