package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/raoptimus/kodrun/internal/ollama"
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
func (t *DeleteFileTool) Description() string { return "Delete a file" }

func (t *DeleteFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "File path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *DeleteFileTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return ToolResult{Error: "path is required", Success: false}, nil
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if IsForbidden(ctx, path, t.forbiddenPatterns) || IsForbidden(ctx, resolved, t.forbiddenPatterns) {
		return ToolResult{Error: fmt.Sprintf("access to %s is forbidden", path), Success: false}, nil
	}

	if err := os.Remove(resolved); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{
		Output:  fmt.Sprintf("deleted %s", path),
		Success: true,
		Meta:    map[string]any{"action": "Delete"},
	}, nil
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
func (t *CreateDirTool) Description() string { return "Create a directory (with parents)" }

func (t *CreateDirTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path": {Type: "string", Description: "Directory path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *CreateDirTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return ToolResult{Error: "path is required", Success: false}, nil
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{
		Output:  fmt.Sprintf("created %s", path),
		Success: true,
		Meta:    map[string]any{"action": "Add"},
	}, nil
}

// ResolvePaths returns the absolute path of the file being deleted.
func (t *DeleteFileTool) ResolvePaths(params map[string]any) []string {
	p, _ := params["path"].(string)
	if p == "" {
		return nil
	}
	resolved, err := SafePath(context.TODO(), t.workDir, p)
	if err != nil {
		return nil
	}
	return []string{resolved}
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
func (t *MoveFileTool) Description() string { return "Move or rename a file" }

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

// ResolvePaths returns the absolute paths affected by the move (both source
// and destination so the cache invalidates entries on either side).
func (t *MoveFileTool) ResolvePaths(params map[string]any) []string {
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)
	out := make([]string, 0, 2)
	if from != "" {
		if r, err := SafePath(context.TODO(), t.workDir, from); err == nil {
			out = append(out, r)
		}
	}
	if to != "" {
		if r, err := SafePath(context.TODO(), t.workDir, to); err == nil {
			out = append(out, r)
		}
	}
	return out
}

func (t *MoveFileTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	from, _ := params["from"].(string)
	to, _ := params["to"].(string)

	if from == "" || to == "" {
		return ToolResult{Error: "from and to are required", Success: false}, nil
	}

	resolvedFrom, err := SafePath(ctx, t.workDir, from)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	resolvedTo, err := SafePath(ctx, t.workDir, to)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if IsForbidden(ctx, from, t.forbiddenPatterns) || IsForbidden(ctx, to, t.forbiddenPatterns) ||
		IsForbidden(ctx, resolvedFrom, t.forbiddenPatterns) || IsForbidden(ctx, resolvedTo, t.forbiddenPatterns) {
		return ToolResult{Error: "access to path is forbidden", Success: false}, nil
	}

	if err := os.Rename(resolvedFrom, resolvedTo); err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{
		Output:  fmt.Sprintf("moved %s → %s", from, to),
		Success: true,
		Meta:    map[string]any{"action": "Rename"},
	}, nil
}
