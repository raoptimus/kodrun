package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
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

func (t *ListDirTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"path":      {Type: "string", Description: "Directory path relative to work directory"},
			"recursive": {Type: "string", Description: "If 'true', list recursively", Enum: []string{"true", "false"}},
		},
		Required: []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	path, ok := params["path"].(string)
	if !ok || path == "" {
		path = "."
	}
	var recursive string
	if v, ok2 := params["recursive"].(string); ok2 {
		recursive = v
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	var entries []string
	if recursive == "true" {
		entries, err = t.walkRecursive(ctx, resolved)
	} else {
		entries, err = t.listFlat(ctx, resolved)
	}
	if err != nil {
		return nil, err
	}

	return &ToolResult{Output: strings.Join(entries, "\n")}, nil
}

func (t *ListDirTool) walkRecursive(ctx context.Context, root string) ([]string, error) {
	var entries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(t.workDir, p)
		if relErr != nil {
			return filepath.SkipDir
		}
		switch {
		case d.IsDir() && IsForbiddenDir(ctx, rel, t.forbidden):
			return filepath.SkipDir
		case d.IsDir():
			entries = append(entries, rel+"/")
		case !IsForbidden(ctx, rel, t.forbidden):
			entries = append(entries, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk dir: %w", err)
	}
	return entries, nil
}

func (t *ListDirTool) listFlat(ctx context.Context, resolved string) ([]string, error) {
	dirEntries, err := os.ReadDir(resolved)
	if err != nil {
		return nil, fmt.Errorf("read dir: %w", err)
	}
	entries := make([]string, 0, len(dirEntries))
	for _, e := range dirEntries {
		name := e.Name()
		switch {
		case e.IsDir() && IsForbiddenDir(ctx, name, t.forbidden):
			continue
		case e.IsDir():
			name += "/"
		case IsForbidden(ctx, name, t.forbidden):
			continue
		}
		entries = append(entries, name)
	}
	return entries, nil
}
