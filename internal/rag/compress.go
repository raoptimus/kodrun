/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"path/filepath"
	"strings"
)

// CompressChunk applies conservative, idempotent heuristics to strip
// low-information tokens from a chunk before it is injected into a prompt.
// The goal is LongLLMLingua-style budget savings without a dedicated ML
// model: we only drop things whose absence does not change the semantics
// a reviewer cares about (blank lines, pure-comment lines in Go, import
// blocks, trailing whitespace, HTML comments in markdown, etc.).
//
// CompressChunk is file-type aware via the filePath suffix. Unknown types
// fall through to a generic whitespace pass. The function never touches
// content inside fenced code blocks in markdown to preserve example code.
func CompressChunk(filePath, content string) string {
	if content == "" {
		return content
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return compressGo(content)
	case ".md", ".markdown":
		return compressMarkdown(content)
	default:
		return compressGeneric(content)
	}
}

// compressGo drops blank lines, single-line comments, import blocks and
// trailing whitespace. It intentionally keeps doc comments attached to
// exported declarations (heuristic: comment immediately followed by a
// func/type/var/const whose first identifier rune is uppercase).
func compressGo(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inImport := false
	inBlockComment := false

	isDecl := func(s string) bool {
		s = strings.TrimSpace(s)
		for _, kw := range []string{"func ", "type ", "var ", "const "} {
			if strings.HasPrefix(s, kw) {
				rest := strings.TrimSpace(strings.TrimPrefix(s, kw))
				if rest == "" {
					return false
				}
				r := rest[0]
				return r >= 'A' && r <= 'Z'
			}
		}
		return false
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)

		// Block comments — drop entirely (docs rarely land inside a chunk).
		if inBlockComment {
			if strings.Contains(line, "*/") {
				inBlockComment = false
			}
			continue
		}
		if strings.HasPrefix(trim, "/*") && !strings.Contains(trim, "*/") {
			inBlockComment = true
			continue
		}

		// Import blocks — drop the whole thing.
		if inImport {
			if trim == ")" {
				inImport = false
			}
			continue
		}
		if trim == "import (" {
			inImport = true
			continue
		}
		if strings.HasPrefix(trim, "import \"") || strings.HasPrefix(trim, "import `") {
			continue
		}

		// Blank lines — drop.
		if trim == "" {
			continue
		}

		// Line comments — keep only if attached to an exported decl
		// on the next non-blank line.
		if strings.HasPrefix(trim, "//") {
			keep := false
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if next == "" || strings.HasPrefix(next, "//") {
					continue
				}
				keep = isDecl(next)
				break
			}
			if !keep {
				continue
			}
		}

		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.Join(out, "\n")
}

// compressMarkdown drops HTML comments, blank-line runs and trailing
// whitespace. Content inside fenced code blocks is preserved verbatim.
func compressMarkdown(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	prevBlank := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			out = append(out, line)
			prevBlank = false
			continue
		}
		if inFence {
			out = append(out, line)
			prevBlank = false
			continue
		}
		// HTML comments on their own line.
		if strings.HasPrefix(trim, "<!--") && strings.HasSuffix(trim, "-->") {
			continue
		}
		if trim == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.Join(out, "\n")
}

// compressGeneric collapses blank-line runs and strips trailing whitespace.
func compressGeneric(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			continue
		}
		prevBlank = false
		out = append(out, strings.TrimRight(line, " \t"))
	}
	return strings.Join(out, "\n")
}
