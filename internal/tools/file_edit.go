package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// EditFileTool performs find-and-replace editing in a file.
type EditFileTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewEditFileTool creates a new edit_file tool.
func NewEditFileTool(workDir string, forbiddenPatterns []string) *EditFileTool {
	return &EditFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *EditFileTool) Name() string        { return "edit_file" }
func (t *EditFileTool) Description() string  { return "Edit a file by replacing old_str with new_str" }

func (t *EditFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path":    {Type: "string", Description: "File path relative to work directory"},
			"old_str": {Type: "string", Description: "Text to find and replace"},
			"new_str": {Type: "string", Description: "Replacement text"},
		},
		Required: []string{"path", "old_str", "new_str"},
	}
}

func (t *EditFileTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	oldStr, _ := params["old_str"].(string)
	newStr, _ := params["new_str"].(string)

	if path == "" || oldStr == "" {
		return ToolResult{Error: "path and old_str are required", Success: false}, nil
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

	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return ToolResult{Error: "old_str not found in file", Success: false}, nil
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(resolved, []byte(newContent), 0o644); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{
		Output:  fmt.Sprintf("replaced 1 occurrence in %s", path),
		Success: true,
	}, nil
}
