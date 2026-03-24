package tools

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// ListDirTool lists files in a directory.
type ListDirTool struct {
	workDir   string
	forbidden []string
}

// NewListDirTool creates a new list_dir tool.
func NewListDirTool(workDir string, forbidden []string) *ListDirTool {
	return &ListDirTool{workDir: workDir, forbidden: forbidden}
}

func (t *ListDirTool) Name() string        { return "list_dir" }
func (t *ListDirTool) Description() string { return "List files and directories in a path" }

func (t *ListDirTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path":      {Type: "string", Description: "Directory path relative to work directory"},
			"recursive": {Type: "string", Description: "If 'true', list recursively", Enum: []string{"true", "false"}},
		},
		Required: []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		path = "."
	}
	recursive, _ := params["recursive"].(string)

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	var entries []string
	if recursive == "true" {
		err = filepath.WalkDir(resolved, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(t.workDir, p)
			if d.IsDir() {
				if IsForbiddenDir(ctx, rel, t.forbidden) {
					return filepath.SkipDir
				}
				entries = append(entries, rel+"/")
			} else {
				if !IsForbidden(ctx, rel, t.forbidden) {
					entries = append(entries, rel)
				}
			}
			return nil
		})
	} else {
		dirEntries, readErr := os.ReadDir(resolved)
		if readErr != nil {
			return ToolResult{Error: readErr.Error(), Success: false}, nil
		}
		for _, e := range dirEntries {
			name := e.Name()
			if e.IsDir() {
				if IsForbiddenDir(ctx, name, t.forbidden) {
					continue
				}
				name += "/"
			} else {
				if IsForbidden(ctx, name, t.forbidden) {
					continue
				}
			}
			entries = append(entries, name)
		}
	}

	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: strings.Join(entries, "\n"), Success: true}, nil
}
