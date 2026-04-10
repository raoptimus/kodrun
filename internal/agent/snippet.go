package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultSnippetMaxLines = 15
	defaultBudgetLines     = 60
	snippetContext         = 3 // lines before the target line
)

// extractSnippet reads a region of a file and returns a formatted code block
// with line numbers. When ex.Line is zero the first maxLines of the file are
// returned. On any I/O error an empty string is returned (non-fatal).
func extractSnippet(workDir string, ex Example, maxLines int) string {
	if maxLines <= 0 {
		maxLines = defaultSnippetMaxLines
	}
	path := filepath.Join(workDir, ex.File)
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	startLine := 1
	if ex.Line > 0 {
		startLine = ex.Line - snippetContext
		if startLine < 1 {
			startLine = 1
		}
	}
	endLine := startLine + maxLines - 1

	var b strings.Builder
	scanner := bufio.NewScanner(f)
	lineNum := 0
	collected := 0
	for scanner.Scan() {
		lineNum++
		if lineNum < startLine {
			continue
		}
		if lineNum > endLine {
			break
		}
		fmt.Fprintf(&b, "%d | %s\n", lineNum, scanner.Text())
		collected++
	}
	if collected == 0 {
		return ""
	}

	note := ex.Note
	if note == "" {
		note = "reference"
	}
	header := fmt.Sprintf("--- %s:%d (%s) ---\n", ex.File, ex.Line, note)
	return header + b.String() + "---\n"
}

// formatStepExamples collects snippets for all examples in a step, respecting
// a total line budget. Returns an empty string when no snippets could be
// resolved.
func formatStepExamples(workDir string, examples []Example, budgetLines int) string {
	if len(examples) == 0 {
		return ""
	}
	if budgetLines <= 0 {
		budgetLines = defaultBudgetLines
	}

	parts := make([]string, 0, len(examples))
	totalLines := 0
	for _, ex := range examples {
		remaining := budgetLines - totalLines
		if remaining <= 0 {
			break
		}
		maxLines := defaultSnippetMaxLines
		if maxLines > remaining {
			maxLines = remaining
		}
		snippet := extractSnippet(workDir, ex, maxLines)
		if snippet == "" {
			continue
		}
		lines := strings.Count(snippet, "\n")
		totalLines += lines
		parts = append(parts, snippet)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}
