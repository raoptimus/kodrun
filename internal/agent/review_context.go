package agent

import (
	"bufio"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/tools"
)

// readModulePath reads the module path from go.mod in workDir.
func readModulePath(workDir string) string {
	f, err := os.Open(filepath.Join(workDir, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// extractLocalImports parses Go imports from filePath and returns
// directories of local (same-module) packages relative to workDir.
func extractLocalImports(workDir, filePath, modulePath string) ([]string, error) {
	if modulePath == "" {
		return nil, nil
	}

	absPath := filePath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workDir, absPath)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, absPath, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse imports %s: %w", filePath, err)
	}

	dirs := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(path, modulePath) {
			continue
		}
		// Convert import path to relative directory.
		rel := strings.TrimPrefix(path, modulePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		dirs = append(dirs, rel)
	}
	return dirs, nil
}

// prefetchPackageStructures collects go_structure output for all local packages
// imported by the given files. Returns map[pkgDir]->structureOutput.
func prefetchPackageStructures(ctx context.Context, reg *tools.Registry, workDir string, files []string, modulePath string) map[string]string {
	// Collect unique package dirs.
	pkgSet := make(map[string]bool)
	for _, f := range files {
		dirs, err := extractLocalImports(workDir, f, modulePath)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			pkgSet[d] = true
		}
	}

	result := make(map[string]string, len(pkgSet))
	for pkgDir := range pkgSet {
		res, err := reg.Execute(ctx, "go_structure", map[string]any{
			"path":          pkgDir,
			"exported_only": true,
		})
		if err != nil {
			continue
		}
		result[pkgDir] = res.Output
	}
	return result
}

// buildPerFileRAGMap runs RAG search for each file and returns
// map[filePath]->formatted RAG block.
func buildPerFileRAGMap(ctx context.Context, ragIndex tools.RAGSearcher, files []string, topK int) map[string]string {
	if ragIndex == nil || topK <= 0 {
		return nil
	}

	result := make(map[string]string, len(files))
	queryDone := make(map[string][]rag.SearchResult)

	for _, f := range files {
		base := filepath.Base(f)
		dir := filepath.Dir(f)
		query := base
		if dir != "." {
			query = filepath.Base(dir) + " " + base
		}

		if cached, ok := queryDone[query]; ok {
			if len(cached) > 0 {
				result[f] = formatRAGResults(cached)
			}
			continue
		}

		results, err := ragIndex.Search(ctx, query, topK)
		if err != nil {
			queryDone[query] = nil
			continue
		}
		queryDone[query] = results
		if len(results) > 0 {
			result[f] = formatRAGResults(results)
		}
	}
	return result
}

// depSignaturesForFile collects go_structure output for packages imported
// by the given file, capped at maxBytes.
func depSignaturesForFile(workDir, filePath, modulePath string, structures map[string]string, maxBytes int) string {
	dirs, err := extractLocalImports(workDir, filePath, modulePath)
	if err != nil || len(dirs) == 0 {
		return ""
	}

	var b strings.Builder
	for _, d := range dirs {
		s, ok := structures[d]
		if !ok || s == "" {
			continue
		}
		entry := fmt.Sprintf("### %s\n%s\n", d, s)
		if maxBytes > 0 && b.Len()+len(entry) > maxBytes {
			break
		}
		b.WriteString(entry)
	}
	return b.String()
}

// buildPerFileReviewPrompt assembles the user message for a per-file code review.
func buildPerFileReviewPrompt(filePath, fileContent, ragBlock, depSignatures string) string {
	var b strings.Builder

	if ragBlock != "" {
		b.WriteString("## Project conventions\n")
		b.WriteString(ragBlock)
		b.WriteString("\n\n")
	}

	b.WriteString("## File under review: ")
	b.WriteString(filePath)
	b.WriteString("\n```\n")
	b.WriteString(fileContent)
	b.WriteString("\n```\n\n")

	if depSignatures != "" {
		b.WriteString("## Dependency signatures (local packages)\n")
		b.WriteString(depSignatures)
		b.WriteByte('\n')
	}

	b.WriteString("## Reminders\n")
	b.WriteString("- Line numbers come from the file content above.\n")
	b.WriteString("- Format: path:LINE — SEVERITY — description — FIX: suggestion\n")
	b.WriteString("- If no issues found, output exactly: NO_ISSUES\n")

	return b.String()
}

// buildArchReviewPrompt assembles the user message for the architecture reviewer.
func buildArchReviewPrompt(projectStructure, archSnippets string) string {
	var b strings.Builder

	if archSnippets != "" {
		b.WriteString("## Project conventions\n")
		b.WriteString(archSnippets)
		b.WriteString("\n\n")
	}

	b.WriteString("## Project structure (all packages)\n")
	b.WriteString(projectStructure)
	b.WriteString("\n\n")

	b.WriteString("## Reminders\n")
	b.WriteString("- Format: path/to/package — SEVERITY — description — FIX: suggestion\n")
	b.WriteString("- If no issues found, output exactly: NO_ISSUES\n")

	return b.String()
}
