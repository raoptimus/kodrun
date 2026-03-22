package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// DeleteFileTool deletes a file.
type DeleteFileTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewDeleteFileTool creates a new delete_file tool.
func NewDeleteFileTool(workDir string, forbiddenPatterns []string) *DeleteFileTool {
	return &DeleteFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *DeleteFileTool) Name() string        { return "delete_file" }
func (t *DeleteFileTool) Description() string  { return "Delete a file" }

func (t *DeleteFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "File path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *DeleteFileTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
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

	if err := os.Remove(resolved); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: fmt.Sprintf("deleted %s", path), Success: true}, nil
}

// CreateDirTool creates a directory.
type CreateDirTool struct {
	workDir string
}

// NewCreateDirTool creates a new create_dir tool.
func NewCreateDirTool(workDir string) *CreateDirTool {
	return &CreateDirTool{workDir: workDir}
}

func (t *CreateDirTool) Name() string        { return "create_dir" }
func (t *CreateDirTool) Description() string  { return "Create a directory (with parents)" }

func (t *CreateDirTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "Directory path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *CreateDirTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return ToolResult{Error: "path is required", Success: false}, nil
	}

	resolved, err := SafePath(t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: fmt.Sprintf("created %s", path), Success: true}, nil
}

// MoveFileTool moves/renames a file.
type MoveFileTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewMoveFileTool creates a new move_file tool.
func NewMoveFileTool(workDir string, forbiddenPatterns []string) *MoveFileTool {
	return &MoveFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *MoveFileTool) Name() string        { return "move_file" }
func (t *MoveFileTool) Description() string  { return "Move or rename a file" }

func (t *MoveFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"from": {Type: "string", Description: "Source file path"},
			"to":   {Type: "string", Description: "Destination file path"},
		},
		Required: []string{"from", "to"},
	}
}

func (t *MoveFileTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)

	if from == "" || to == "" {
		return ToolResult{Error: "from and to are required", Success: false}, nil
	}

	resolvedFrom, err := SafePath(t.workDir, from)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	resolvedTo, err := SafePath(t.workDir, to)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if IsForbidden(from, t.forbiddenPatterns) || IsForbidden(to, t.forbiddenPatterns) {
		return ToolResult{Error: "access to path is forbidden", Success: false}, nil
	}

	if err := os.Rename(resolvedFrom, resolvedTo); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: fmt.Sprintf("moved %s → %s", from, to), Success: true}, nil
}
