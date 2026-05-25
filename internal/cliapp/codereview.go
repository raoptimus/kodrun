/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/snippets"
	"github.com/raoptimus/kodrun/internal/tools"
)

// FilterDiffByPackage keeps only the file sections of a unified-diff whose
// `+++ b/<path>` line is rooted under the given package scope. Used by
// `/code-review --package <path>` to narrow review to a single package
// without re-running git. Empty diff or empty scope return the input as-is.
func FilterDiffByPackage(diff, scope string) string {
	scope = strings.TrimSuffix(scope, "/")
	if scope == "" || diff == "" {
		return diff
	}
	prefix := scope + "/"
	lines := strings.Split(diff, "\n")
	var out []string
	keep := false
	for i := range lines {
		line := lines[i]
		if strings.HasPrefix(line, "diff --git ") {
			path := ""
			for j := i + 1; j < len(lines) && !strings.HasPrefix(lines[j], "diff --git "); j++ {
				if p, ok := strings.CutPrefix(lines[j], "+++ "); ok {
					p = strings.TrimPrefix(p, "b/")
					path = strings.TrimSpace(p)
					break
				}
			}
			keep = path != "" && (path == scope || strings.HasPrefix(path, prefix))
		}
		if keep {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// findAllSourceFiles walks workDir and returns relative paths to all source
// code files in the project, skipping hidden directories, vendor, etc.
func findAllSourceFiles(workDir string) []string {
	var files []string
	err := filepath.WalkDir(workDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(workDir, p)
		if relErr != nil {
			return relErr
		}
		if isSourceCodePath(rel) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		slog.Debug("findAllSourceFiles walk failed", "error", err)
	}
	return files
}

// isSourceCodePath reports whether path looks like a real source code file
// worth code-reviewing.
func isSourceCodePath(path string) bool {
	if path == "" || path == PathDevNull {
		return false
	}
	denyPrefixes := []string{
		".kodrun/", ".claude/", ".github/", ".git/",
		"vendor/", "node_modules/", "testdata/", "docs/", "doc/",
	}
	for _, p := range denyPrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	denyNames := map[string]bool{
		"go.sum":            true,
		"package-lock.json": true, "yarn.lock": true,
		"pnpm-lock.yaml": true, "Cargo.lock": true, "poetry.lock": true,
		"Pipfile.lock": true, "AGENTS.md": true, "README.md": true,
		"CLAUDE.md": true,
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if denyNames[base] {
		return false
	}
	allowExts := []string{
		".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs",
		".rs", ".java", ".kt", ".kts", ".scala", ".rb", ".php",
		".c", ".h", ".cc", ".cpp", ".hpp", ".m", ".mm", ".swift",
		".cs", ".fs", ".vb", ".ex", ".exs", ".erl", ".hrl",
		".lua", ".pl", ".pm", ".sh", ".bash", ".zsh", ".sql",
		".proto",
	}
	for _, ext := range allowExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// CodeReviewParams aggregates the dependencies RunCodeReview needs.
type CodeReviewParams struct {
	WorkDir        string
	Cfg            *config.Config
	Client         llm.Client
	ChatProv       *config.ProviderConfig
	Registry       *tools.Registry
	Agent          *agent.Agent
	Emit           agent.EventHandler
	PlanConfirm    agent.PlanConfirmFunc
	StepConfirm    agent.StepConfirmFunc
	RuleCatalog    string
	RAGIndex       tools.RAGSearcher
	GodocIndexer   tools.GoDocIndexer
	LangState      *projectlang.State
	RulesLoader    *rules.Loader
	SnippetsLoader *snippets.Loader
	Flags          *Flags
}

// RunCodeReview handles the orchestrator-driven code review pipeline.
func RunCodeReview(ctx context.Context, p *CodeReviewParams) {
	sourceFiles := findAllSourceFiles(p.WorkDir)
	if len(sourceFiles) == 0 {
		p.Emit(&agent.Event{Type: agent.EventAgent, Message: "No source files found."})
		p.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	var doneSent atomic.Bool
	wrappedEmit := agent.EventHandler(func(e *agent.Event) {
		if e.Type == agent.EventDone {
			doneSent.Store(true)
		}
		p.Emit(e)
	})
	orch := NewOrchestrator(p.Client, p.ChatProv, p.Registry, p.Cfg, wrappedEmit, p.Agent.GetConfirmFunc(), p.PlanConfirm, p.StepConfirm, p.RuleCatalog, p.RAGIndex, p.GodocIndexer, p.LangState, p.RulesLoader, p.Flags)
	if err := orch.RunCodeReview(ctx, sourceFiles, p.SnippetsLoader.Snippets()); err != nil && ctx.Err() == nil {
		p.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
	}
	if !doneSent.Load() {
		p.Emit(&agent.Event{Type: agent.EventDone})
	}
}
