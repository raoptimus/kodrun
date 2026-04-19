/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/raoptimus/kodrun/internal/rag"
)

const (
	minGitStatusLineLen      = 4 // minimum git status --short line length
	prefetchCategoryTests    = "tests"
	prefetchCategoryGodoc    = "godoc"
	prefetchCategoryRule     = "rule"
	prefetchCategoryStandard = "standard"
	prefetchCategorySnippet  = "snippet"
)

// detectEntityTypeFromPath returns the rule name (matching .kodrun/rules/<name>.md)
// for a given Go file path, or "" if no rule name from ruleNames matches.
// Detection is based on filename suffixes/substrings, not on file contents.
// ruleNames is the dynamic list of available rule names (from rules.Loader).
func detectEntityTypeFromPath(path string, ruleNames []string) string {
	if path == "" || len(ruleNames) == 0 {
		return ""
	}
	base := strings.ToLower(filepath.Base(path))
	base = strings.TrimSuffix(base, ".go")

	// _test.go always wins if "tests" rule exists.
	if strings.HasSuffix(base, "_test") {
		for _, n := range ruleNames {
			if n == prefetchCategoryTests {
				return prefetchCategoryTests
			}
		}
	}

	// Sort by length desc so more specific names match first.
	sorted := make([]string, len(ruleNames))
	copy(sorted, ruleNames)
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })

	for _, name := range sorted {
		if name == "" || name == prefetchCategoryTests {
			continue
		}
		ln := strings.ToLower(name)
		if strings.HasSuffix(base, "_"+ln) || base == ln || strings.Contains(base, ln) {
			return name
		}
	}
	return ""
}

// gitChangedGoFiles returns the list of changed (modified/added/untracked) .go files
// in the working directory according to `git status --porcelain=v1`. Empty on any error.
func gitChangedGoFiles(ctx context.Context, workDir string) []string {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1")
	cmd.Dir = workDir
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	var files []string
	for line := range strings.SplitSeq(out.String(), "\n") {
		if len(line) < minGitStatusLineLen {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// Handle rename: "old -> new"
		if idx := strings.Index(path, " -> "); idx >= 0 {
			path = path[idx+4:]
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
	}
	return files
}

// entityTypesFromPaths returns the unique set of rule names detected
// across the given file paths, given a dynamic list of available rule names.
func entityTypesFromPaths(paths, ruleNames []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		t := detectEntityTypeFromPath(p, ruleNames)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// chunkCategory classifies a RAG chunk by its source path.
// example_*.go files (from .kodrun/docs/) are treated as snippet templates,
// not as plain rule docs.
func chunkCategory(filePath string) string {
	base := filepath.Base(filePath)
	switch {
	case strings.HasPrefix(filePath, "godoc://"):
		return prefetchCategoryGodoc
	case strings.HasPrefix(filePath, "rules://"):
		return prefetchCategoryRule
	case strings.HasPrefix(filePath, "embedded://"):
		return prefetchCategoryStandard
	case strings.HasPrefix(base, "example_"):
		return prefetchCategorySnippet
	case strings.Contains(filePath, "/snippets/") || strings.HasSuffix(filePath, ".snippet"):
		return prefetchCategorySnippet
	case strings.Contains(filePath, "/docs/") || strings.Contains(filePath, "/rules/"):
		return prefetchCategoryRule
	default:
		return "code"
	}
}

// formatRAGResults formats RAG search results into a string block
// suitable for injection into a user message.
// Results are grouped by category: mandatory rules first, then standards,
// snippets, and code examples — so the model clearly sees what is required vs informational.
func formatRAGResults(results []rag.SearchResult) string {
	type categorized struct {
		category string
		result   rag.SearchResult
	}

	var rules, standards, snippets, godocs []categorized
	for _, r := range results {
		// Conservative in-place compression to squeeze more signal
		// into the same injection budget.
		r.Chunk.Content = rag.CompressChunk(r.Chunk.FilePath, r.Chunk.Content)
		cat := chunkCategory(r.Chunk.FilePath)
		c := categorized{category: cat, result: r}
		switch cat {
		case prefetchCategoryRule:
			rules = append(rules, c)
		case prefetchCategoryStandard:
			standards = append(standards, c)
		case prefetchCategorySnippet:
			snippets = append(snippets, c)
		case prefetchCategoryGodoc:
			godocs = append(godocs, c)
		default:
			// "code" chunks should no longer reach this formatter: RAG only
			// indexes conventions (rules/snippets/docs/embedded). If a code
			// chunk slips in (e.g. a stale leftover from an older index on
			// disk), drop it — never inject project source code through RAG,
			// reviewers must read it live via read_file.
		}
	}

	var b strings.Builder
	idx := 1

	if len(rules) > 0 {
		b.WriteString("[MANDATORY PROJECT RULES — you MUST follow these rules in ALL code you write, review, or plan]\n")
		b.WriteString("[These are NOT suggestions. Violations of these rules are BUGS that must be fixed.]\n\n")
		for _, c := range rules {
			r := c.result
			fmt.Fprintf(&b, "--- RULE %d (%.2f) %s:%d-%d ---\n%s\n\n",
				idx, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
			idx++
		}
	}

	if len(standards) > 0 {
		b.WriteString("[GO STANDARDS — idiomatic Go practices you MUST apply]\n\n")
		for _, c := range standards {
			r := c.result
			fmt.Fprintf(&b, "--- STANDARD %d (%.2f) %s:%d-%d ---\n%s\n\n",
				idx, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
			idx++
		}
	}

	if len(snippets) > 0 {
		b.WriteString("[CODE TEMPLATES — MANDATORY patterns you MUST follow when code matches these patterns]\n\n")
		for _, c := range snippets {
			r := c.result
			fmt.Fprintf(&b, "--- TEMPLATE %d (%.2f) %s:%d-%d ---\n%s\n\n",
				idx, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
			idx++
		}
	}

	if len(godocs) > 0 {
		b.WriteString("[GO DOCUMENTATION — relevant Go package docs]\n\n")
		for _, c := range godocs {
			r := c.result
			fmt.Fprintf(&b, "--- DOC %d (%.2f) %s:%d-%d ---\n%s\n\n",
				idx, r.Score, r.Chunk.FilePath, r.Chunk.StartLine, r.Chunk.EndLine, r.Chunk.Content)
			idx++
		}
	}

	return b.String()
}
