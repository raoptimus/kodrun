package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLangFromExt_ReturnsLanguage_Successfully(t *testing.T) {
	tests := []struct {
		ext  string
		want string
	}{
		{"go", "Go"},
		{"py", "Python"},
		{"js", "JavaScript"},
		{"ts", "TypeScript"},
		{"tsx", "TypeScript (JSX)"},
		{"jsx", "JavaScript (JSX)"},
		{"rs", "Rust"},
		{"java", "Java"},
		{"rb", "Ruby"},
		{"c", "C"},
		{"h", "C"},
		{"cpp", "C++"},
		{"cc", "C++"},
		{"cxx", "C++"},
		{"hpp", "C++"},
		{"cs", "C#"},
		{"yaml", "YAML"},
		{"yml", "YAML"},
		{"json", "JSON"},
		{"md", "Markdown"},
		{"sql", "SQL"},
		{"sh", "Shell"},
		{"bash", "Shell"},
		{"proto", "Protobuf"},
		{"xyz", "xyz"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := langFromExt(tt.ext)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFileStatTool_Execute_ReturnsMetadata_Successfully(t *testing.T) {
	dir := t.TempDir()
	content := "line1\nline2\nline3\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.go"), []byte(content), 0o644))

	tool := NewFileStatTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "test.go"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "path: test.go")
	assert.Contains(t, result.Output, "total_lines: 3")
	assert.Contains(t, result.Output, "ext: go")
	assert.Contains(t, result.Output, "language: Go")
	assert.Equal(t, int64(len(content)), result.Meta["size_bytes"])
	assert.Equal(t, 3, result.Meta["total_lines"])
	assert.Equal(t, "go", result.Meta["ext"])
	assert.Equal(t, "Go", result.Meta["language"])
}

func TestFileStatTool_Execute_EmptyFile_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.txt"), []byte(""), 0o644))

	tool := NewFileStatTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "empty.txt"})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Meta["total_lines"])
	assert.Equal(t, int64(0), result.Meta["size_bytes"])
}

func TestFileStatTool_Execute_FileWithoutTrailingNewline_Successfully(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "no_nl.txt"), []byte("line1\nline2"), 0o644))

	tool := NewFileStatTool(dir, nil)

	result, err := tool.Execute(context.Background(), map[string]any{"path": "no_nl.txt"})

	require.NoError(t, err)
	assert.Equal(t, 2, result.Meta["total_lines"])
}

func TestFileStatTool_Execute_EmptyPath_Failure(t *testing.T) {
	tool := NewFileStatTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestFileStatTool_Execute_ForbiddenPath_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET"), 0o644))

	tool := NewFileStatTool(dir, []string{"*.env"})

	_, err := tool.Execute(context.Background(), map[string]any{"path": ".env"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestFileStatTool_Execute_Directory_Failure(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))

	tool := NewFileStatTool(dir, nil)

	_, err := tool.Execute(context.Background(), map[string]any{"path": "subdir"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
}

func TestFileStatTool_Execute_NonexistentFile_Failure(t *testing.T) {
	tool := NewFileStatTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{"path": "missing.go"})

	require.Error(t, err)
}
