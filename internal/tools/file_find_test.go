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

func TestFindFilesTool_Execute_FindsMatchingFiles_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "util.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "readme.md"), []byte(""), 0o644))

	tool := NewFindFilesTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.go",
		"root":    "src",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "main.go")
	assert.Contains(t, result.Output, "util.go")
	assert.NotContains(t, result.Output, "readme.md")
}

func TestFindFilesTool_Execute_NoMatches_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "readme.md"), []byte(""), 0o644))

	tool := NewFindFilesTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.go",
		"root":    "src",
	})

	require.NoError(t, err)
	assert.Equal(t, "no files found", result.Output)
}

func TestFindFilesTool_Execute_EmptyPattern_Failure(t *testing.T) {
	tool := NewFindFilesTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestFindFilesTool_Execute_ForbiddenDirSkipped_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(filepath.Join(src, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, ".git", "config"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "main.go"), []byte(""), 0o644))

	tool := NewFindFilesTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*",
		"root":    "src",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "main.go")
	assert.NotContains(t, result.Output, "config")
}

func TestFindFilesTool_Execute_ForbiddenFileSkipped_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	require.NoError(t, os.MkdirAll(src, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "app.go"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "secrets.env"), []byte(""), 0o644))

	tool := NewFindFilesTool(dir, []string{"*.env"})

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*",
		"root":    "src",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "app.go")
	assert.NotContains(t, result.Output, "secrets.env")
}

func TestFindFilesTool_Execute_WithRoot_Successfully(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "child.go"), []byte(""), 0o644))

	tool := NewFindFilesTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.go",
		"root":    "sub",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "child.go")
}

func TestFindFilesTool_Name_Successfully(t *testing.T) {
	tool := NewFindFilesTool(t.TempDir(), nil)

	assert.Equal(t, "find_files", tool.Name())
}
