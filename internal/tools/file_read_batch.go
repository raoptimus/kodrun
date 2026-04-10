package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/projectlang"
)

const (
	// diffContextLines is the number of context lines around each diff hunk.
	diffContextLines = 20
	// minDiffStatusLineLen is the minimum length of a git diff --stat status line
	// (e.g. " M file.go" has a 3-char prefix plus at least 1 char for the path).
	minDiffStatusLineLen = 4
)

// codeExtensions maps project language to file extensions that are relevant
// for code review. Common config extensions are shared across all languages.
var codeExtensions = map[projectlang.Language]map[string]bool{
	projectlang.LangGo: {
		".go": true, ".mod": true,
		".yaml": true, ".yml": true, ".toml": true, ".json": true,
		".sql": true, ".proto": true, ".graphql": true,
		".sh": true, ".bash": true,
		".dockerfile": true,
	},
	projectlang.LangPython: {
		".py": true, ".pyi": true, ".pyx": true,
		".yaml": true, ".yml": true, ".toml": true, ".json": true, ".cfg": true, ".ini": true,
		".sql": true, ".graphql": true,
		".sh": true, ".bash": true,
		".dockerfile": true,
	},
	projectlang.LangJSTS: {
		".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true,
		".vue": true, ".svelte": true,
		".yaml": true, ".yml": true, ".json": true,
		".css": true, ".scss": true, ".less": true,
		".html": true, ".graphql": true,
		".sql": true, ".sh": true, ".bash": true,
		".dockerfile": true,
	},
}

// fallbackCodeExtensions is used when language is unknown.
var fallbackCodeExtensions = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".java": true, ".rs": true, ".c": true, ".cpp": true, ".h": true,
	".yaml": true, ".yml": true, ".toml": true, ".json": true,
	".sql": true, ".proto": true, ".graphql": true,
	".sh": true, ".bash": true, ".dockerfile": true,
}

// ReadChangedFilesTool returns the unified diff with extended context for all
// git-changed source code files. This gives code-review specialists the
// changed lines plus surrounding context without reading entire files.
type ReadChangedFilesTool struct {
	workDir   string
	langState *projectlang.State
}

// NewReadChangedFilesTool creates a new read_changed_files tool.
func NewReadChangedFilesTool(workDir string, langState *projectlang.State) *ReadChangedFilesTool {
	return &ReadChangedFilesTool{workDir: workDir, langState: langState}
}

func (t *ReadChangedFilesTool) Name() string { return "read_changed_files" }
func (t *ReadChangedFilesTool) Description() string {
	return "Read diff with context for all git-changed source code files. " +
		"Returns unified diff with surrounding context lines. " +
		"Use read_file(path) if you need the full content of a specific file."
}

func (t *ReadChangedFilesTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type:       "object",
		Properties: map[string]ollama.JSONSchema{},
	}
}

func (t *ReadChangedFilesTool) Execute(ctx context.Context, _ map[string]any) (*ToolResult, error) {
	files, err := t.changedFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	if len(files) == 0 {
		return &ToolResult{Output: "No changed code files found."}, nil
	}

	// Try git diff HEAD first (unstaged + staged vs HEAD), then --cached
	// (staged only, for unborn branch), then collect untracked files that
	// have no diff at all.
	uCtx := fmt.Sprintf("-U%d", diffContextLines)
	args := []string{"diff", uCtx, "HEAD", "--"}
	args = append(args, files...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.workDir
	out, err := cmd.Output()
	if err != nil {
		// Fallback for unborn branch.
		args[2] = "--cached"
		cmd = exec.CommandContext(ctx, "git", args...)
		cmd.Dir = t.workDir
		out, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff: %w", err)
		}
	}

	diff := strings.TrimSpace(string(out))

	// For untracked files (shown as ?? in porcelain), git diff HEAD produces
	// nothing. Generate a synthetic diff via --no-index so the reviewer sees
	// the full content of new files.
	if diff == "" || !allFilesInDiff(diff, files) {
		for _, f := range files {
			if diff != "" && strings.Contains(diff, "diff --git a/"+f+" ") {
				continue
			}
			nCmd := exec.CommandContext(ctx, "git", "diff", "--no-index", uCtx, "/dev/null", f)
			nCmd.Dir = t.workDir
			nOut, nErr := nCmd.Output()
			if nErr != nil {
				// git diff --no-index returns exit code 1 when files differ; ignore that.
				var exitErr *exec.ExitError
				if !errors.As(nErr, &exitErr) {
					continue
				}
			}
			if len(nOut) > 0 {
				if diff != "" {
					diff += "\n"
				}
				diff += strings.TrimSpace(string(nOut))
			}
		}
	}

	if diff == "" {
		return &ToolResult{Output: "No changes in source code files."}, nil
	}

	return &ToolResult{
		Output: diff,
		Meta: map[string]any{
			"files":     len(files),
			"file_list": files,
		},
	}, nil
}

// allFilesInDiff returns true when every file in the list appears as a
// "diff --git a/<file>" header in the unified diff output.
func allFilesInDiff(diff string, files []string) bool {
	for _, f := range files {
		if !strings.Contains(diff, "diff --git a/"+f+" ") {
			return false
		}
	}
	return true
}

// CachePolicy implements the Cacheable interface.
func (t *ReadChangedFilesTool) CachePolicy() CachePolicy {
	return CachePolicy{
		Cacheable:    true,
		Invalidators: []string{"write_file", "edit_file", "delete_file", "move_file"},
	}
}

// changedFiles returns the list of changed code files (modified, staged,
// untracked), filtered by language-specific extension whitelist and excluding
// hidden directories. Uses `git status --porcelain=v1` to catch all git
// states including untracked files that `git diff --name-only HEAD` misses.
func (t *ReadChangedFilesTool) changedFiles(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1")
	cmd.Dir = t.workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	allowed := fallbackCodeExtensions
	if t.langState != nil {
		if exts, ok := codeExtensions[t.langState.Current()]; ok {
			allowed = exts
		}
	}

	seen := make(map[string]bool)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	fileList := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) < minDiffStatusLineLen {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// Handle rename: "old -> new"
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		if path == "" || seen[path] {
			continue
		}
		if strings.HasPrefix(path, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !allowed[ext] {
			continue
		}
		seen[path] = true
		fileList = append(fileList, path)
	}
	return fileList, nil
}
