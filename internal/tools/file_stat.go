package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/raoptimus/kodrun/internal/ollama"
)

const extBash = "bash"

// FileStatTool returns file metadata without reading its contents.
type FileStatTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewFileStatTool creates a new file_stat tool.
func NewFileStatTool(workDir string, forbiddenPatterns []string) *FileStatTool {
	return &FileStatTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *FileStatTool) Name() string { return "file_stat" }
func (t *FileStatTool) Description() string {
	return "Get file metadata (size, lines, modified date) without reading contents"
}

func (t *FileStatTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "File path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *FileStatTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path := stringParam(params, "path")
	if path == "" {
		return nil, &ToolError{Msg: "path is required"}
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	if reason := IsPathBlocked(ctx, path, resolved, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}

	start := time.Now()

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	if info.IsDir() {
		return nil, &ToolError{Msg: fmt.Sprintf("%s is a directory, not a file", path)}
	}

	totalLines := 0
	data, err := os.ReadFile(resolved)
	if err == nil {
		totalLines = bytes.Count(data, []byte{'\n'})
		if len(data) > 0 && data[len(data)-1] != '\n' {
			totalLines++
		}
	}

	duration := time.Since(start)

	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	lang := langFromExt(ext)

	output := fmt.Sprintf("path: %s\nsize_bytes: %d\ntotal_lines: %d\nmodified: %s\next: %s\nlanguage: %s",
		path, info.Size(), totalLines, info.ModTime().Format(time.RFC3339), ext, lang)

	return &ToolResult{
		Output: output,
		Meta: map[string]any{
			"size_bytes":  info.Size(),
			"total_lines": totalLines,
			"ext":         ext,
			"language":    lang,
			"duration":    duration.String(),
		},
	}, nil
}

// CachePolicy declares file_stat results as cacheable, keyed by path
// and invalidated by any write to the same path.
func (t *FileStatTool) CachePolicy() CachePolicy {
	return CachePolicy{
		Cacheable:    true,
		PathParams:   []string{"path"},
		Invalidators: []string{"write_file", "edit_file", "delete_file", "move_file"},
	}
}

// ResolvePaths returns the absolute filesystem path the call depends on.
func (t *FileStatTool) ResolvePaths(params map[string]any) []string {
	path := stringParam(params, "path")
	if path == "" {
		return nil
	}
	resolved, err := SafePath(context.TODO(), t.workDir, path)
	if err != nil {
		return nil
	}
	return []string{resolved}
}

func langFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case "go":
		return "Go"
	case "py":
		return "Python"
	case "js":
		return "JavaScript"
	case "ts":
		return "TypeScript"
	case "tsx":
		return "TypeScript (JSX)"
	case "jsx":
		return "JavaScript (JSX)"
	case "rs":
		return "Rust"
	case "java":
		return "Java"
	case "rb":
		return "Ruby"
	case "c", "h":
		return "C"
	case "cpp", "cc", "cxx", "hpp":
		return "C++"
	case "cs":
		return "C#"
	case "yaml", "yml":
		return "YAML"
	case "json":
		return "JSON"
	case "md":
		return "Markdown"
	case "sql":
		return "SQL"
	case "sh", extBash:
		return "Shell"
	case "proto":
		return "Protobuf"
	default:
		return ext
	}
}
