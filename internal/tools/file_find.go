package tools

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// FindFilesTool finds files matching a glob pattern.
type FindFilesTool struct {
	workDir   string
	forbidden []string
}

// NewFindFilesTool creates a new find_files tool.
func NewFindFilesTool(workDir string, forbidden []string) *FindFilesTool {
	return &FindFilesTool{workDir: workDir, forbidden: forbidden}
}

func (t *FindFilesTool) Name() string        { return "find_files" }
func (t *FindFilesTool) Description() string { return "Find files matching a glob pattern" }

func (t *FindFilesTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"pattern": {Type: "string", Description: "Glob pattern (e.g. '**/*.go')"},
			"root":    {Type: "string", Description: "Root directory for search (default: work dir)"},
		},
		Required: []string{"pattern"},
	}
}

func (t *FindFilesTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	pattern, _ := params["pattern"].(string)
	root, _ := params["root"].(string)

	if pattern == "" {
		return ToolResult{Error: "pattern is required", Success: false}, nil
	}

	searchRoot := t.workDir
	if root != "" {
		resolved, err := SafePath(ctx, t.workDir, root)
		if err != nil {
			return ToolResult{Error: err.Error(), Success: false}, nil
		}
		searchRoot = resolved
	}

	var matches []string
	err := filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(t.workDir, path)
		if d.IsDir() {
			if IsForbiddenDir(ctx, rel, t.forbidden) {
				return filepath.SkipDir
			}
			return nil
		}
		if IsForbidden(ctx, rel, t.forbidden) {
			return nil
		}
		if matched, _ := filepath.Match(pattern, filepath.Base(path)); matched {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if len(matches) == 0 {
		return ToolResult{Output: "no files found", Success: true}, nil
	}

	return ToolResult{Output: strings.Join(matches, "\n"), Success: true}, nil
}
