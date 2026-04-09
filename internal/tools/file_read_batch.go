package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/projectlang"
)

// diffContextLines is the number of context lines around each diff hunk.
const diffContextLines = 20

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

func (t *ReadChangedFilesTool) Execute(ctx context.Context, _ map[string]any) (ToolResult, error) {
	files, err := t.changedFiles(ctx)
	if err != nil {
		return ToolResult{Error: fmt.Sprintf("git diff: %s", err), Success: false}, nil
	}

	if len(files) == 0 {
		return ToolResult{Output: "No changed code files found.", Success: true}, nil
	}

	// git diff -U<context> HEAD -- <file1> <file2> ...
	args := []string{"diff", fmt.Sprintf("-U%d", diffContextLines), "HEAD", "--"}
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
			return ToolResult{Error: fmt.Sprintf("git diff: %s", err), Success: false}, nil
		}
	}

	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return ToolResult{Output: "No changes in source code files.", Success: true}, nil
	}

	return ToolResult{
		Output:  diff,
		Success: true,
		Meta: map[string]any{
			"files":     len(files),
			"file_list": files,
		},
	}, nil
}

// CachePolicy implements the Cacheable interface.
func (t *ReadChangedFilesTool) CachePolicy() CachePolicy {
	return CachePolicy{
		Cacheable:    true,
		Invalidators: []string{"write_file", "edit_file", "delete_file", "move_file"},
	}
}

// changedFiles returns the list of changed code files, filtered by language-specific
// extension whitelist and excluding hidden directories.
func (t *ReadChangedFilesTool) changedFiles(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "HEAD")
	cmd.Dir = t.workDir
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.CommandContext(ctx, "git", "diff", "--name-only", "--cached")
		cmd.Dir = t.workDir
		out, err = cmd.Output()
		if err != nil {
			return nil, err
		}
	}

	allowed := fallbackCodeExtensions
	if t.langState != nil {
		if exts, ok := codeExtensions[t.langState.Current()]; ok {
			allowed = exts
		}
	}

	seen := make(map[string]bool)
	var fileList []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		if strings.HasPrefix(line, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(line))
		if !allowed[ext] {
			continue
		}
		seen[line] = true
		fileList = append(fileList, line)
	}
	return fileList, nil
}
