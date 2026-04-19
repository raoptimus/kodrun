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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/raoptimus/kodrun/internal/llm"
)

const (
	dirPermission = 0o755
	movePathCap   = 2 // from + to paths
	boolTrue      = "true"
	boolFalse     = "false"
)

// stringParam safely extracts a string value from a map[string]any.
func stringParam(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// boolParam safely extracts a bool value from a map[string]any.
func boolParam(m map[string]any, key string) bool {
	v, ok := m[key].(bool)
	return ok && v
}

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

func (t *DeleteFileTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"path": {Type: "string", Description: "File path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *DeleteFileTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		return nil, &ToolError{Msg: "path is required"}
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	if reason := IsPathBlocked(ctx, path, resolved, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}

	if err := os.Remove(resolved); err != nil {
		return nil, fmt.Errorf("delete file: %w", err)
	}

	return &ToolResult{
		Output: fmt.Sprintf("deleted %s", path),
		Meta:   map[string]any{"action": "Delete"},
	}, nil
}

// CreateDirTool creates a directory.
type CreateDirTool struct {
	workDir           string
	forbiddenPatterns []string
}

// NewCreateDirTool creates a new create_dir tool.
func NewCreateDirTool(workDir string, forbiddenPatterns []string) *CreateDirTool {
	return &CreateDirTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns}
}

func (t *CreateDirTool) Name() string        { return "create_dir" }
func (t *CreateDirTool) Description() string { return "Create a directory (with parents)" }

func (t *CreateDirTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"path": {Type: "string", Description: "Directory path relative to work directory"},
		},
		Required: []string{"path"},
	}
}

func (t *CreateDirTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		return nil, &ToolError{Msg: "path is required"}
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	if reason := IsPathBlocked(ctx, path, resolved, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}

	if err := os.MkdirAll(resolved, dirPermission); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}

	return &ToolResult{
		Output: fmt.Sprintf("created %s", path),
		Meta:   map[string]any{"action": "Add"},
	}, nil
}

// ResolvePaths returns the absolute path of the file being deleted.
func (t *DeleteFileTool) ResolvePaths(params map[string]any) []string {
	p := stringParam(params, "path")
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

func (t *MoveFileTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"from": {Type: "string", Description: "Source file path"},
			"to":   {Type: "string", Description: "Destination file path"},
		},
		Required: []string{"from", "to"},
	}
}

// ResolvePaths returns the absolute paths affected by the move (both source
// and destination so the cache invalidates entries on either side).
func (t *MoveFileTool) ResolvePaths(params map[string]any) []string {
	from := stringParam(params, "from")
	to := stringParam(params, "to")
	out := make([]string, 0, movePathCap)
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

func (t *MoveFileTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	from := stringParam(params, "from")
	to := stringParam(params, "to")

	if from == "" || to == "" {
		return nil, &ToolError{Msg: "from and to are required"}
	}

	resolvedFrom, err := SafePath(ctx, t.workDir, from)
	if err != nil {
		return nil, fmt.Errorf("resolve source path: %w", err)
	}

	resolvedTo, err := SafePath(ctx, t.workDir, to)
	if err != nil {
		return nil, fmt.Errorf("resolve destination path: %w", err)
	}

	if reason := IsPathBlocked(ctx, from, resolvedFrom, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}
	if reason := IsPathBlocked(ctx, to, resolvedTo, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}

	// Ensure the destination directory exists.
	if dir := filepath.Dir(resolvedTo); dir != "" {
		if err := os.MkdirAll(dir, dirPermission); err != nil {
			return nil, fmt.Errorf("create destination dir: %w", err)
		}
	}

	// Use git mv when the project is a git repository to preserve file
	// history. Fall back to os.Rename when git is not available.
	if isGitRepo(t.workDir) {
		cmd := exec.CommandContext(ctx, "git", "mv", resolvedFrom, resolvedTo)
		cmd.Dir = t.workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git mv: %s (%w)", string(out), err)
		}
	} else {
		if err := os.Rename(resolvedFrom, resolvedTo); err != nil {
			return nil, fmt.Errorf("rename: %w", err)
		}
	}

	return &ToolResult{
		Output: fmt.Sprintf("moved %s → %s", from, to),
		Meta:   map[string]any{"action": "Rename"},
	}, nil
}

// isGitRepo checks whether workDir is inside a git repository by looking for
// a .git directory or file (submodule). Walks up to 10 parent directories.
func isGitRepo(workDir string) bool {
	dir := workDir
	for range 10 {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			_ = info
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}
