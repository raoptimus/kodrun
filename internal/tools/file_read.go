package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// ReadFileTool reads a file's contents.
type ReadFileTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewReadFileTool creates a new read_file tool.
func NewReadFileTool(workDir string, forbiddenPatterns []string) *ReadFileTool {
	return &ReadFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string  { return "Read the contents of a file" }

func (t *ReadFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "File path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *ReadFileTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
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

	data, err := os.ReadFile(resolved)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: string(data), Success: true}, nil
}
