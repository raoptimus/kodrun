package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// ListDirTool lists files in a directory.
type ListDirTool struct {
	workDir string
}

// NewListDirTool creates a new list_dir tool.
func NewListDirTool(workDir string) *ListDirTool {
	return &ListDirTool{workDir: workDir}
}

func (t *ListDirTool) Name() string        { return "list_dir" }
func (t *ListDirTool) Description() string  { return "List files and directories in a path" }

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

func (t *ListDirTool) Execute(_ context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		path = "."
	}
	recursive, _ := params["recursive"].(string)

	resolved, err := SafePath(t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	var entries []string
	if recursive == "true" {
		err = filepath.Walk(resolved, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(t.workDir, p)
			if info.IsDir() {
				entries = append(entries, rel+"/")
			} else {
				entries = append(entries, rel)
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
				name += "/"
			}
			entries = append(entries, name)
		}
	}

	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	return ToolResult{Output: strings.Join(entries, "\n"), Success: true}, nil
}
