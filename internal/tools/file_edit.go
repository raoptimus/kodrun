package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

const editDiffMaxLines = 30 // max lines in edit tool diff output

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
func (t *EditFileTool) Description() string { return "Edit a file by replacing old_str with new_str" }

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

// ResolvePaths returns the absolute path edited, used by the registry for
// cache invalidation.
func (t *EditFileTool) ResolvePaths(params map[string]any) []string {
	p, ok := params["path"].(string)
	if !ok || p == "" {
		return nil
	}
	resolved, err := SafePath(context.TODO(), t.workDir, p)
	if err != nil {
		return nil
	}
	return []string{resolved}
}

func (t *EditFileTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path, ok := params["path"].(string)
	if !ok {
		return nil, &ToolError{Msg: "path must be a string"}
	}
	oldStr, ok := params["old_str"].(string)
	if !ok {
		return nil, &ToolError{Msg: "old_str must be a string"}
	}
	newStr, ok := params["new_str"].(string)
	if !ok {
		return nil, &ToolError{Msg: "new_str must be a string"}
	}

	if path == "" || oldStr == "" {
		return nil, &ToolError{Msg: "path and old_str are required"}
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	if reason := IsPathBlocked(ctx, path, resolved, t.forbiddenPatterns); reason != "" {
		return nil, &ToolError{Msg: reason}
	}

	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, oldStr)
	if count == 0 {
		return nil, &ToolError{Msg: "old_str not found in file"}
	}

	newContent := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(resolved, []byte(newContent), fi.Mode().Perm()); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	added, removed := LineStats(content, newContent)
	diff := SimpleDiff(content, newContent, path, editDiffMaxLines)

	msg := fmt.Sprintf("replaced 1 occurrence in %s", path)
	if count > 1 {
		msg = fmt.Sprintf("replaced 1 of %d occurrences in %s (warning: %d more remain)", count, path, count-1)
	}

	return &ToolResult{
		Output: msg,
		Meta: map[string]any{
			"action":  "Update",
			"added":   added,
			"removed": removed,
			"diff":    diff,
		},
	}, nil
}
