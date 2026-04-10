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

func (t *GrepTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return nil, &ToolError{Msg: "pattern is required"}
	}

	var path string
	if v, ok2 := params["path"].(string); ok2 {
		path = v
	}
	var recursive string
	if v, ok2 := params["recursive"].(string); ok2 {
		recursive = v
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, &ToolError{Msg: fmt.Sprintf("invalid regex: %s", err)}
	}

	searchPath := t.workDir
	if path != "" {
		resolved, err := SafePath(ctx, t.workDir, path)
		if err != nil {
			return nil, fmt.Errorf("resolve path: %w", err)
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

		rel, relErr := filepath.Rel(t.workDir, filePath)
		if relErr != nil {
			rel = filePath
		}
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
		return nil, fmt.Errorf("stat path: %w", err)
	}

	switch {
	case !info.IsDir():
		searchFile(searchPath)
	case recursive == boolTrue:
		if walkErr := filepath.WalkDir(searchPath, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return filepath.SkipDir
			}
			if len(results) >= maxResults {
				return filepath.SkipAll
			}
			rel, relErr := filepath.Rel(t.workDir, p)
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
			searchFile(p)
			return nil
		}); walkErr != nil {
			return nil, fmt.Errorf("walk dir: %w", walkErr)
		}
	default:
		entries, readErr := os.ReadDir(searchPath)
		if readErr != nil {
			return nil, fmt.Errorf("read dir: %w", readErr)
		}
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
		return &ToolResult{Output: "no matches found"}, nil
	}

	return &ToolResult{Output: strings.Join(results, "\n")}, nil
}
