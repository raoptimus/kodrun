package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/raoptimus/kodrun/internal/ollama"
)

const (
	writeDiffMaxLines = 30    // max lines in write tool diff output
	newFilePermission = 0o600 // permissions for newly created files
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

func (t *WriteFileTool) Name() string { return "write_file" }
func (t *WriteFileTool) Description() string {
	return "Write content to a file, creating directories as needed"
}

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

// ResolvePaths returns the absolute path that this write affects, used by the
// registry to invalidate dependent cache entries.
func (t *WriteFileTool) ResolvePaths(params map[string]any) []string {
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

func (t *WriteFileTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path := stringParam(params, "path")
	content := stringParam(params, "content")

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

	// Read old content for diff
	var oldContent string
	existed := false
	if data, readErr := os.ReadFile(resolved); readErr == nil {
		oldContent = string(data)
		existed = true
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, dirPermission); err != nil {
		return nil, fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(resolved, []byte(content), newFilePermission); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	action := FileActionType(existed)
	added, removed := LineStats(oldContent, content)

	meta := map[string]any{
		"action":  action,
		"added":   added,
		"removed": removed,
	}
	if existed {
		meta["diff"] = SimpleDiff(oldContent, content, path, writeDiffMaxLines)
	}

	return &ToolResult{
		Output: fmt.Sprintf("wrote %d bytes to %s", len(content), path),
		Meta:   meta,
	}, nil
}
