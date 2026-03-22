package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// WriteFileTool writes content to a file.
type WriteFileTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewWriteFileTool creates a new write_file tool.
func NewWriteFileTool(workDir string, forbiddenPatterns []string) *WriteFileTool {
	return &WriteFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *WriteFileTool) Name() string        { return "write_file" }
func (t *WriteFileTool) Description() string  { return "Write content to a file, creating directories as needed" }

func (t *WriteFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path":    {Type: "string", Description: "File path relative to work directory"},
			"content": {Type: "string", Description: "File content to write"},
		},
		Required: []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	content, _ := params["content"].(string)

	if path == "" {
		return ToolResult{Error: "path is required", Success: false}, nil
	}

	resolved, err := SafePath(t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if IsForbidden(path, t.forbiddenPatterns) {
		return ToolResult{Error: fmt.Sprintf("access to %s is forbidden", path), Success: false}, nil
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ToolResult{Error: fmt.Sprintf("create directory: %s", err), Success: false}, nil
	}

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path), Success: true}, nil
}
