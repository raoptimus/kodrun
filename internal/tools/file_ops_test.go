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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringParam_ReturnsValue_Successfully(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{
			name: "existing string key",
			m:    map[string]any{"path": "hello.go"},
			key:  "path",
			want: "hello.go",
		},
		{
			name: "missing key",
			m:    map[string]any{"other": "value"},
			key:  "path",
			want: "",
		},
		{
			name: "non-string value",
			m:    map[string]any{"path": 123},
			key:  "path",
			want: "",
		},
		{
			name: "empty map",
			m:    map[string]any{},
			key:  "path",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringParam(tt.m, tt.key)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBoolParam_ReturnsValue_Successfully(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want bool
	}{
		{
			name: "true value",
			m:    map[string]any{"flag": true},
			key:  "flag",
			want: true,
		},
		{
			name: "false value",
			m:    map[string]any{"flag": false},
			key:  "flag",
			want: false,
		},
		{
			name: "missing key",
			m:    map[string]any{},
			key:  "flag",
			want: false,
		},
		{
			name: "non-bool value",
			m:    map[string]any{"flag": "true"},
			key:  "flag",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := boolParam(tt.m, tt.key)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDeleteFileTool_Execute_DeletesFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "to-delete.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o644))

	tool := NewDeleteFileTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "to-delete.txt"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "deleted to-delete.txt")
	assert.Equal(t, "Delete", result.Meta["action"])
	_, statErr := os.Stat(filePath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestDeleteFileTool_Execute_EmptyPath_Failure(t *testing.T) {
	tool := NewDeleteFileTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestDeleteFileTool_Execute_ForbiddenPath_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET"), 0o644))

	tool := NewDeleteFileTool(dir, []string{"*.env"})

	_, err := tool.Execute(context.Background(), map[string]any{"path": ".env"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestDeleteFileTool_Execute_MissingFile_ReturnsResult(t *testing.T) {
	tool := NewDeleteFileTool(t.TempDir(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "nonexistent.txt"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "does not exist")
}

func TestDeleteFileTool_ResolvePaths_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))

	tool := NewDeleteFileTool(dir, nil)

	paths := tool.ResolvePaths(map[string]any{"path": "f.txt"})

	require.Len(t, paths, 1)
	assert.Equal(t, filepath.Join(dir, "f.txt"), paths[0])
}

func TestDeleteFileTool_ResolvePaths_EmptyPath_Successfully(t *testing.T) {
	tool := NewDeleteFileTool(t.TempDir(), nil)

	paths := tool.ResolvePaths(map[string]any{})

	assert.Nil(t, paths)
}

func TestCreateDirTool_Execute_CreatesDir_Successfully(t *testing.T) {
	dir := t.TempDir()
	tool := NewCreateDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "sub/deep/dir"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "created sub/deep/dir")
	assert.Equal(t, "Add", result.Meta["action"])
	info, statErr := os.Stat(filepath.Join(dir, "sub", "deep", "dir"))
	require.NoError(t, statErr)
	assert.True(t, info.IsDir())
}

func TestCreateDirTool_Execute_EmptyPath_Failure(t *testing.T) {
	tool := NewCreateDirTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestMoveFileTool_Execute_EmptyPaths_Failure(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
	}{
		{
			name:   "both empty",
			params: map[string]any{},
		},
		{
			name:   "from empty",
			params: map[string]any{"to": "dest.txt"},
		},
		{
			name:   "to empty",
			params: map[string]any{"from": "src.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewMoveFileTool(t.TempDir(), nil)

			_, err := tool.Execute(context.Background(), tt.params)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "from and to are required")
		})
	}
}

func TestMoveFileTool_Execute_ForbiddenPath_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET"), 0o644))

	tool := NewMoveFileTool(dir, []string{"*.env"})

	_, err := tool.Execute(context.Background(), map[string]any{"from": ".env", "to": "backup.env"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestMoveFileTool_ResolvePaths_BothPaths_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644))

	tool := NewMoveFileTool(dir, nil)

	paths := tool.ResolvePaths(map[string]any{"from": "a.txt", "to": "b.txt"})

	require.Len(t, paths, 2)
	assert.Equal(t, filepath.Join(dir, "a.txt"), paths[0])
	assert.Equal(t, filepath.Join(dir, "b.txt"), paths[1])
}

func TestMoveFileTool_ResolvePaths_EmptyPaths_Successfully(t *testing.T) {
	tool := NewMoveFileTool(t.TempDir(), nil)

	paths := tool.ResolvePaths(map[string]any{})

	assert.Empty(t, paths)
}

func TestIsGitRepo_InGitRepo_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))

	assert.True(t, isGitRepo(dir))
}

func TestIsGitRepo_NotGitRepo_Successfully(t *testing.T) {
	dir := t.TempDir()

	assert.False(t, isGitRepo(dir))
}

func TestIsGitRepo_ParentHasGit_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	child := filepath.Join(dir, "sub", "deep")
	require.NoError(t, os.MkdirAll(child, 0o755))

	assert.True(t, isGitRepo(child))
}
