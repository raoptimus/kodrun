package tools

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
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

func (t *FindFilesTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"pattern": {Type: "string", Description: "Glob pattern (e.g. '**/*.go')"},
			"root":    {Type: "string", Description: "Root directory for search (default: work dir)"},
		},
		Required: []string{"pattern"},
	}
}

func (t *FindFilesTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return nil, &ToolError{Msg: "pattern is required"}
	}
	var root string
	if v, ok := params["root"].(string); ok {
		root = v
	}

	searchRoot := t.workDir
	if root != "" {
		resolved, err := SafePath(ctx, t.workDir, root)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
		}
		searchRoot = resolved
	}

	var matches []string
	err := filepath.WalkDir(searchRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(t.workDir, path)
		if relErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if IsForbiddenDir(ctx, rel, t.forbidden) {
				return filepath.SkipDir
			}
			return nil
		}
		if IsForbidden(ctx, rel, t.forbidden) {
			return nil
		}
		if matched, matchErr := filepath.Match(pattern, filepath.Base(path)); matchErr == nil && matched {
			matches = append(matches, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk dir: %w", err)
	}

	if len(matches) == 0 {
		return &ToolResult{Output: "no files found"}, nil
	}

	return &ToolResult{Output: strings.Join(matches, "\n")}, nil
}
