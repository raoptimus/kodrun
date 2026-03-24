package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// GrepTool searches for a pattern in files.
type GrepTool struct {
	workDir   string
	forbidden []string
}

// NewGrepTool creates a new grep tool.
func NewGrepTool(workDir string, forbidden []string) *GrepTool {
	return &GrepTool{workDir: workDir, forbidden: forbidden}
}

func (t *GrepTool) Name() string        { return "grep" }
func (t *GrepTool) Description() string { return "Search for a regex pattern in files" }

func (t *GrepTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"pattern":   {Type: "string", Description: "Regex pattern to search for"},
			"path":      {Type: "string", Description: "File or directory to search in"},
			"recursive": {Type: "string", Description: "If 'true', search recursively", Enum: []string{"true", "false"}},
		},
		Required: []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	pattern, _ := params["pattern"].(string)
	path, _ := params["path"].(string)
	recursive, _ := params["recursive"].(string)

	if pattern == "" {
		return ToolResult{Error: "pattern is required", Success: false}, nil
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return ToolResult{Error: fmt.Sprintf("invalid regex: %s", err), Success: false}, nil
	}

	searchPath := t.workDir
	if path != "" {
		resolved, err := SafePath(ctx, t.workDir, path)
		if err != nil {
			return ToolResult{Error: err.Error(), Success: false}, nil
		}
		searchPath = resolved
	}

	var results []string
	maxResults := 100

	searchFile := func(filePath string) {
		if len(results) >= maxResults {
			return
		}
		f, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer f.Close()

		rel, _ := filepath.Rel(t.workDir, filePath)
		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				results = append(results, fmt.Sprintf("%s:%d:%s", rel, lineNum, line))
				if len(results) >= maxResults {
					return
				}
			}
		}
		// scanner.Err() intentionally ignored: partial results are acceptable for grep.
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if !info.IsDir() {
		searchFile(searchPath)
	} else if recursive == "true" {
		_ = filepath.WalkDir(searchPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil || len(results) >= maxResults {
				return nil
			}
			rel, _ := filepath.Rel(t.workDir, p)
			if d.IsDir() {
				if IsForbiddenDir(ctx, rel, t.forbidden) {
					return filepath.SkipDir
				}
				return nil
			}
			if IsForbidden(ctx, rel, t.forbidden) {
				return nil
			}
			searchFile(p)
			return nil
		})
	} else {
		entries, _ := os.ReadDir(searchPath)
		for _, e := range entries {
			if e.IsDir() || len(results) >= maxResults {
				continue
			}
			name := e.Name()
			if IsForbidden(ctx, name, t.forbidden) {
				continue
			}
			searchFile(filepath.Join(searchPath, name))
		}
	}

	if len(results) == 0 {
		return ToolResult{Output: "no matches found", Success: true}, nil
	}

	return ToolResult{Output: strings.Join(results, "\n"), Success: true}, nil
}
