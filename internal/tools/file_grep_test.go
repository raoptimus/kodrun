package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGrepTool_Execute_FindsPattern_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("func main() {}\nfunc helper() {}\n"), 0o644))

	tool := NewGrepTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "func.*main",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "a.go:1:func main() {}")
}

func TestGrepTool_Execute_NoMatches_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644))

	tool := NewGrepTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "zzz_nonexistent",
	})

	require.NoError(t, err)
	assert.Equal(t, "no matches found", result.Output)
}

func TestGrepTool_Execute_EmptyPattern_Failure(t *testing.T) {
	tool := NewGrepTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestGrepTool_Execute_InvalidRegex_Failure(t *testing.T) {
	tool := NewGrepTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "[invalid",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid regex")
}

func TestGrepTool_Execute_SingleFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("line1\nline2\nline3\n"), 0o644))

	tool := NewGrepTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "line2",
		"path":    "f.txt",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "f.txt:2:line2")
}

func TestGrepTool_Execute_Recursive_Successfully(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	sub := filepath.Join(src, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "root.go"), []byte("hello\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "child.go"), []byte("hello child\n"), 0o644))

	tool := NewGrepTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":   "hello",
		"path":      "src",
		"recursive": "true",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "root.go")
	assert.Contains(t, result.Output, "child.go")
}

func TestGrepTool_Execute_ForbiddenFileSkipped_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.go"), []byte("secret_data\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("secret_data\n"), 0o644))

	tool := NewGrepTool(dir, []string{"*.env"})

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "secret_data",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "app.go")
	assert.NotContains(t, result.Output, ".env")
}

func TestGrepTool_Execute_NonexistentPath_Failure(t *testing.T) {
	tool := NewGrepTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "test",
		"path":    "nonexistent_dir",
	})

	require.Error(t, err)
}
