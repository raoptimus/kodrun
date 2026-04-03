package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

const defaultMaxLines = 500

// ReadFileTool reads a file's contents with optional offset/limit pagination.
type ReadFileTool struct {
	workDir           string
	forbiddenPatterns []string
	maxLines          int
}

// NewReadFileTool creates a new read_file tool.
func NewReadFileTool(workDir string, forbiddenPatterns []string, maxLines int) *ReadFileTool {
	if maxLines <= 0 {
		maxLines = defaultMaxLines
	}
	return &ReadFileTool{workDir: workDir, forbiddenPatterns: forbiddenPatterns, maxLines: maxLines}
}

func (t *ReadFileTool) Name() string        { return "read_file" }
func (t *ReadFileTool) Description() string { return "Read the contents of a file" }

func (t *ReadFileTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"path":   {Type: "string", Description: "File path relative to work directory"},
			"offset": {Type: "integer", Description: "Start line (0-based). Default: 0"},
			"limit":  {Type: "integer", Description: "Max lines to read. Default: 500"},
		},
		Required: []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	path, _ := params["path"].(string)
	if path == "" {
		return ToolResult{Error: "path is required", Success: false}, nil
	}

	resolved, err := SafePath(ctx, t.workDir, path)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	if IsForbidden(ctx, path, t.forbiddenPatterns) || IsForbidden(ctx, resolved, t.forbiddenPatterns) {
		return ToolResult{Error: fmt.Sprintf("access to %s is forbidden", path), Success: false}, nil
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return ToolResult{Error: err.Error(), Success: false}, nil
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	offset := toInt(params["offset"], 0)
	limit := toInt(params["limit"], t.maxLines)

	if offset < 0 {
		offset = 0
	}
	if offset >= totalLines {
		return ToolResult{
			Error:   fmt.Sprintf("offset %d beyond file end (%d lines)", offset, totalLines),
			Success: false,
		}, nil
	}

	end := offset + limit
	if end > totalLines {
		end = totalLines
	}

	truncated := end < totalLines

	var buf strings.Builder
	for i := offset; i < end; i++ {
		fmt.Fprintf(&buf, "%4d | %s\n", i+1, lines[i])
	}

	output := buf.String()

	if truncated {
		output += fmt.Sprintf(
			"\n[truncated] Showing lines %d-%d of %d total.\n"+
				"Use read_file with offset/limit to read other sections.\n"+
				"Example: read_file(path=%q, offset=%d, limit=%d)\n",
			offset+1, end, totalLines,
			path, end, limit,
		)
	}

	return ToolResult{
		Output:  output,
		Success: true,
		Meta: map[string]any{
			"total_lines": totalLines,
			"offset":      offset,
			"limit":       limit,
			"truncated":   truncated,
		},
	}, nil
}

// CachePolicy declares read_file results as cacheable, keyed by path/offset/limit
// and invalidated by any write to the same path.
func (t *ReadFileTool) CachePolicy() CachePolicy {
	return CachePolicy{
		Cacheable:    true,
		PathParams:   []string{"path"},
		Invalidators: []string{"write_file", "edit_file", "delete_file", "move_file"},
	}
}

// ResolvePaths returns the absolute filesystem path the call depends on.
func (t *ReadFileTool) ResolvePaths(params map[string]any) []string {
	path, _ := params["path"].(string)
	if path == "" {
		return nil
	}
	resolved, err := SafePath(context.TODO(), t.workDir, path)
	if err != nil {
		return nil
	}
	return []string{resolved}
}

// toInt extracts an integer from a param value with a fallback default.
func toInt(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return def
	}
}
