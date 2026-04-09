package rag

import (
	"strings"
)

// ChunkGoDoc splits the output of `go doc` into semantic chunks based on
// Go declaration boundaries (func, type, var, const, package). Each
// declaration plus its preceding comment block forms one chunk. The
// pkgPath is used as a virtual file path prefix (godoc://<pkgPath>).
func ChunkGoDoc(pkgPath, docOutput string, maxBytes int) []Chunk {
	if maxBytes <= 0 {
		maxBytes = MaxChunkBytes
	}
	docOutput = strings.TrimSpace(docOutput)
	if docOutput == "" {
		return nil
	}

	prefix := "godoc://" + pkgPath
	lines := strings.Split(docOutput, "\n")

	// Find declaration boundary lines.
	type boundary struct {
		line int // 0-based index into lines
	}
	var bounds []boundary
	for i, ln := range lines {
		if isGoDeclLine(ln) {
			bounds = append(bounds, boundary{line: i})
		}
	}

	// No declarations found — treat the whole output as a single chunk.
	if len(bounds) == 0 {
		return singleOrSplit(prefix, docOutput, lines, maxBytes)
	}

	// Split into blocks: [start-of-output..first-decl), [decl1..decl2), ..., [last-decl..end)
	var chunks []Chunk
	for i, b := range bounds {
		start := b.line
		// Include preceding comment lines (lines starting with // or blank
		// lines between comments) that belong to this declaration.
		start = findCommentStart(lines, start)

		var end int
		if i+1 < len(bounds) {
			end = findCommentStart(lines, bounds[i+1].line)
		} else {
			end = len(lines)
		}

		content := strings.Join(lines[start:end], "\n")
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}

		if len(content) > maxBytes {
			// Oversized declaration block — fall back to generic splitting.
			sub := splitIntoChunks(prefix, content, 0, 0)
			// Adjust line numbers relative to the doc output.
			for j := range sub {
				sub[j].StartLine += start + 1
				sub[j].EndLine += start
			}
			chunks = append(chunks, sub...)
		} else {
			chunks = append(chunks, Chunk{
				FilePath:  prefix,
				Content:   content,
				StartLine: start + 1,
				EndLine:   end,
			})
		}
	}

	// If there is a preamble before the first declaration (package doc),
	// include it as a separate chunk.
	firstDeclStart := findCommentStart(lines, bounds[0].line)
	if firstDeclStart > 0 {
		preamble := strings.TrimSpace(strings.Join(lines[:firstDeclStart], "\n"))
		if preamble != "" {
			pre := Chunk{
				FilePath:  prefix,
				Content:   preamble,
				StartLine: 1,
				EndLine:   firstDeclStart,
			}
			chunks = append([]Chunk{pre}, chunks...)
		}
	}

	return chunks
}

// isGoDeclLine returns true if the line looks like a Go declaration start.
func isGoDeclLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	// go doc output uses unindented declaration keywords.
	return strings.HasPrefix(trimmed, "func ") ||
		strings.HasPrefix(trimmed, "type ") ||
		strings.HasPrefix(trimmed, "var ") ||
		strings.HasPrefix(trimmed, "const ") ||
		strings.HasPrefix(trimmed, "package ")
}

// findCommentStart walks backwards from line idx to find the first line
// of a contiguous comment/blank block preceding the declaration.
func findCommentStart(lines []string, idx int) int {
	i := idx - 1
	for i >= 0 {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			i--
			continue
		}
		break
	}
	return i + 1
}

// singleOrSplit returns the whole output as one chunk if it fits, or
// falls back to generic line-based splitting.
func singleOrSplit(prefix, content string, lines []string, maxBytes int) []Chunk {
	if len(content) <= maxBytes {
		return []Chunk{{
			FilePath:  prefix,
			Content:   content,
			StartLine: 1,
			EndLine:   len(lines),
		}}
	}
	return splitIntoChunks(prefix, content, 0, 0)
}
