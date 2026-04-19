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

func TestListDirTool_Execute_ListsFilesAndDirs_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte(""), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "a.go")
	assert.Contains(t, result.Output, "b.txt")
	assert.Contains(t, result.Output, "sub/")
}

func TestListDirTool_Execute_DefaultPath_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.go"), []byte(""), 0o644))

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "file.go")
}

func TestListDirTool_Execute_ForbiddenDirHidden_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "visible"), 0o755))

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})

	require.NoError(t, err)
	assert.NotContains(t, result.Output, ".git")
	assert.Contains(t, result.Output, "visible/")
}

func TestListDirTool_Execute_ForbiddenFileHidden_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte(""), 0o644))

	tool := NewListDirTool(dir, []string{"*.env"})

	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "ok.go")
	assert.NotContains(t, result.Output, ".env")
}

func TestListDirTool_Execute_Recursive_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	sub := filepath.Join(src, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "root.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "child.go"), []byte(""), 0o644))

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":      "src",
		"recursive": "true",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "root.go")
	assert.Contains(t, result.Output, "child.go")
}

func TestListDirTool_Execute_RecursiveHiddenDirSkipped_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	hidden := filepath.Join(src, ".hidden")
	require.NoError(t, os.MkdirAll(hidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hidden, "secret.txt"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "visible.go"), []byte(""), 0o644))

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":      "src",
		"recursive": "true",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "visible.go")
	assert.NotContains(t, result.Output, "secret.txt")
}

func TestListDirTool_Execute_EmptyDir_Successfully(t *testing.T) {
	dir := t.TempDir()

	tool := NewListDirTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "."})

	require.NoError(t, err)
	assert.Empty(t, result.Output)
}
