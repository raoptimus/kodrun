/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package kodruninit

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/llm"
)

const (
	dirPermission   = 0o755
	filePermission  = 0o644
	maxReadmeBytes  = 2000 // max bytes to include from README.md
	maxGoFileLines  = 100  // max lines per key Go file in project context
	maxGoFilesCount = 101  // SplitN limit (maxGoFileLines + 1)
	maxKeyGoFiles   = 10   // max number of key Go files to collect
	maxGoFileDepth  = 2    // max directory depth for key Go files
	dirGit          = ".git"
	dirKodrun       = ".kodrun"
	dirNodeModules  = "node_modules"
	dirVendor       = "vendor"
)

//go:embed all:examples
var examples embed.FS

// Result holds the outcome of the init operation.
type Result struct {
	Created []string // relative paths of created dirs and files
}

// Run creates the .kodrun/ starter structure and AGENTS.md.
// Returns an error if AGENTS.md already exists.
func Run(ctx context.Context, workDir string, client llm.Client, model string) (*Result, error) {
	agentsPath := filepath.Join(workDir, "AGENTS.md")
	if _, err := os.Stat(agentsPath); err == nil {
		return nil, errors.New("AGENTS.md already exists")
	}

	res := &Result{}

	dirs := []string{
		".kodrun/rules",
		".kodrun/docs",
		".kodrun/commands",
		".kodrun/snippets",
	}

	for _, d := range dirs {
		full := filepath.Join(workDir, d)
		if _, err := os.Stat(full); err == nil {
			continue
		}
		if err := os.MkdirAll(full, dirPermission); err != nil {
			return nil, errors.WithMessagef(err, "create %s", d)
		}
		res.Created = append(res.Created, d+"/")
	}

	// Copy embedded examples to .kodrun/ (rules, commands, docs, snippets).
	embeddedDirs := []struct{ src, dest string }{
		{"examples/rules", ".kodrun/rules"},
		{"examples/commands", ".kodrun/commands"},
		{"examples/docs", ".kodrun/docs"},
		{"examples/snippets", ".kodrun/snippets"},
	}
	for _, ed := range embeddedDirs {
		created, err := copyEmbeddedDir(examples, ed.src, workDir, ed.dest)
		if err != nil {
			return nil, err
		}
		res.Created = append(res.Created, created...)
	}

	// .kodrun/.gitignore
	gitignorePath := filepath.Join(workDir, dirKodrun, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		if err := os.WriteFile(gitignorePath, []byte("# KodRun local state\n*.log\n"), filePermission); err != nil {
			return nil, errors.WithMessage(err, "write .kodrun/.gitignore")
		}
		res.Created = append(res.Created, ".kodrun/.gitignore")
	}

	// Scan the project and generate AGENTS.md via LLM.
	agentsContent, err := generateAgentsMD(ctx, workDir, client, model)
	if err != nil {
		return nil, errors.WithMessage(err, "generate AGENTS.md")
	}
	if err := os.WriteFile(agentsPath, []byte(agentsContent), filePermission); err != nil {
		return nil, errors.WithMessage(err, "write AGENTS.md")
	}
	res.Created = append(res.Created, "AGENTS.md")

	sort.Strings(res.Created)
	return res, nil
}

// generateAgentsMD collects project context and asks the LLM to produce AGENTS.md.
func generateAgentsMD(ctx context.Context, workDir string, client llm.Client, model string) (string, error) {
	projectCtx := collectProjectContext(ctx, workDir)

	prompt := `Analyze the following Go project information and generate an AGENTS.md file.

The file must include:
1. Project name and brief description (1-2 sentences) based on the code structure and go.mod module name
2. Go version requirement (minimum Go 1.25+)
3. Architecture overview — describe the main packages and their responsibilities
4. Directory structure (concise tree)
5. Commands to build, test, lint the project (based on Makefile if present, otherwise standard go commands)
6. Key conventions or patterns visible from the code structure

Output ONLY the markdown content for AGENTS.md, starting with "# AGENTS.md". Be concise but informative. Write in the same language as any existing documentation found in the project (default to English if none found).

Project information:
` + projectCtx

	resp, err := client.ChatSync(ctx, &llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", errors.WithMessage(err, "LLM request")
	}

	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", errors.New("LLM returned empty response")
	}

	return content + "\n", nil
}

// collectProjectContext gathers files and structure for the LLM prompt.
func collectProjectContext(ctx context.Context, workDir string) string {
	var b strings.Builder

	// go.mod
	if data, err := os.ReadFile(filepath.Join(workDir, "go.mod")); err == nil {
		b.WriteString("=== go.mod ===\n")
		b.Write(data)
		b.WriteString("\n\n")
	}

	// Makefile
	if data, err := os.ReadFile(filepath.Join(workDir, "Makefile")); err == nil {
		b.WriteString("=== Makefile ===\n")
		b.Write(data)
		b.WriteString("\n\n")
	}

	// README.md (if exists)
	if data, err := os.ReadFile(filepath.Join(workDir, "README.md")); err == nil {
		b.WriteString("=== README.md ===\n")
		// Limit to first maxReadmeBytes bytes
		if len(data) > maxReadmeBytes {
			data = data[:maxReadmeBytes]
		}
		b.Write(data)
		b.WriteString("\n\n")
	}

	// Directory tree
	b.WriteString("=== Directory structure ===\n")
	b.WriteString(buildTree(ctx, workDir))
	b.WriteByte('\n')

	// Collect key .go files (main.go, top-level package files) — first 100 lines each
	goFiles := findKeyGoFiles(ctx, workDir)
	for _, rel := range goFiles {
		data, err := os.ReadFile(filepath.Join(workDir, rel))
		if err != nil {
			continue
		}
		lines := strings.SplitN(string(data), "\n", maxGoFilesCount)
		if len(lines) > maxGoFileLines {
			lines = lines[:maxGoFileLines]
		}
		b.WriteString(fmt.Sprintf("=== %s (first %d lines) ===\n", rel, maxGoFileLines))
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteString("\n\n")
	}

	return b.String()
}

// findKeyGoFiles returns relative paths of important .go files for context.
func findKeyGoFiles(_ context.Context, workDir string) []string {
	var files []string
	maxFiles := 10

	if err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if base == dirGit || base == dirVendor || base == dirNodeModules || base == dirKodrun {
				return filepath.SkipDir
			}
			return nil
		}

		if len(files) >= maxFiles {
			return filepath.SkipAll
		}

		if filepath.Ext(path) != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return filepath.SkipDir
		}
		depth := strings.Count(rel, string(filepath.Separator))

		// Prioritize: main.go, cmd/*, top-level, shallow files
		if depth <= maxGoFileDepth {
			files = append(files, rel)
		}

		return nil
	}); err != nil {
		return nil
	}

	return files
}

func buildTree(_ context.Context, workDir string) string {
	var lines []string
	seen := make(map[string]bool)

	if err := filepath.WalkDir(workDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		rel, relErr := filepath.Rel(workDir, path)
		if relErr != nil {
			return filepath.SkipDir
		}
		if rel == "." {
			return nil
		}

		base := filepath.Base(rel)
		if d.IsDir() {
			switch {
			case base == dirGit || base == dirVendor || base == dirNodeModules:
				return filepath.SkipDir
			case strings.HasPrefix(base, ".") && base != dirKodrun:
				return filepath.SkipDir
			}
		}

		depth := strings.Count(rel, string(filepath.Separator))

		if d.IsDir() {
			if depth > maxGoFileDepth {
				return filepath.SkipDir
			}
			if !seen[rel] {
				indent := strings.Repeat("  ", depth)
				lines = append(lines, fmt.Sprintf("%s%s/", indent, base))
				seen[rel] = true
			}
		} else if depth <= 1 {
			indent := strings.Repeat("  ", depth)
			lines = append(lines, fmt.Sprintf("%s%s", indent, base))
		}

		return nil
	}); err != nil {
		return ""
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// copyEmbeddedDir copies .md files from an embed.FS directory to a destination under workDir.
// Existing files are not overwritten.
func copyEmbeddedDir(efs embed.FS, srcDir, workDir, destDir string) ([]string, error) {
	entries, err := fs.ReadDir(efs, srcDir)
	if err != nil || len(entries) == 0 {
		return nil, err
	}

	created := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		destPath := filepath.Join(workDir, destDir, entry.Name())
		if _, err := os.Stat(destPath); err == nil {
			continue
		}
		data, err := efs.ReadFile(srcDir + "/" + entry.Name())
		if err != nil {
			continue
		}
		if err := os.WriteFile(destPath, data, filePermission); err != nil {
			return created, errors.WithMessagef(err, "write %s/%s", destDir, entry.Name())
		}
		created = append(created, destDir+"/"+entry.Name())
	}

	return created, nil
}
