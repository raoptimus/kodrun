package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// CellPos represents a position in the content (line and column, visual width).
type CellPos struct {
	Line int
	Col  int // visual column (display width, not rune index)
}

// Selection tracks the current text selection state.
type Selection struct {
	Active   bool    // true while dragging
	Start    CellPos // drag start position
	End      CellPos // current/final position
	HasRange bool    // true if Start != End
}

// Normalized returns start and end ordered so that start <= end.
func (s Selection) Normalized() (start, end CellPos) {
	a, b := s.Start, s.End
	if a.Line > b.Line || (a.Line == b.Line && a.Col > b.Col) {
		a, b = b, a
	}
	return a, b
}

// Contains checks whether the given line/col falls within the selection.
func (s Selection) Contains(line, col int) bool {
	if !s.HasRange {
		return false
	}
	start, end := s.Normalized()
	if line < start.Line || line > end.Line {
		return false
	}
	if start.Line == end.Line {
		return col >= start.Col && col < end.Col
	}
	if line == start.Line {
		return col >= start.Col
	}
	if line == end.Line {
		return col < end.Col
	}
	return true
}

// screenToContent maps screen coordinates to content coordinates.
// viewportTop is the Y of the first viewport row on screen (typically 1 for header).
// Returns false when the point is outside the viewport or below the actual content.
func screenToContent(screenX, screenY, viewportTop, viewportHeight, yOffset, logsLen int) (CellPos, bool) {
	row := screenY - viewportTop
	if row < 0 || row >= viewportHeight {
		return CellPos{}, false
	}
	if logsLen == 0 {
		return CellPos{}, false
	}
	contentLine := yOffset + row
	if contentLine >= logsLen {
		return CellPos{}, false
	}
	if contentLine < 0 {
		contentLine = 0
	}
	col := max(screenX, 0)
	return CellPos{Line: contentLine, Col: col}, true
}

// extractSelectedText extracts plain text from the selected region, stripping ANSI.
// Col values are visual columns (display widths), matched via ansi.Cut.
func extractSelectedText(logs []string, sel Selection) string {
	if !sel.HasRange {
		return ""
	}
	start, end := sel.Normalized()

	var b strings.Builder
	for line := start.Line; line <= end.Line; line++ {
		if line < 0 || line >= len(logs) {
			continue
		}
		plain := ansi.Strip(logs[line])
		w := ansi.StringWidth(plain)

		from := 0
		to := w
		if line == start.Line {
			from = start.Col
		}
		if line == end.Line {
			to = end.Col
		}
		from = min(from, w)
		to = min(to, w)

		if from < to {
			b.WriteString(ansi.Cut(plain, from, to))
		}
		if line < end.Line {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// renderLogsWithSelection renders logs with underlined highlighting for selected text.
// Uses ansi.Cut for visual-width-aware slicing.
func renderLogsWithSelection(logs []string, sel Selection, width int) string {
	_ = width
	if !sel.HasRange {
		return strings.Join(logs, "\n")
	}

	start, end := sel.Normalized()
	var b strings.Builder
	for i, line := range logs {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < start.Line || i > end.Line {
			b.WriteString(line)
			continue
		}

		w := ansi.StringWidth(line)

		selStart := 0
		selEnd := w
		if i == start.Line {
			selStart = start.Col
		}
		if i == end.Line {
			selEnd = end.Col
		}
		selStart = min(selStart, w)
		selEnd = min(selEnd, w)

		// Build line with underline for selected portion.
		if selStart > 0 {
			b.WriteString(ansi.Cut(line, 0, selStart))
		}
		if selStart < selEnd {
			b.WriteString("\x1b[4m")
			b.WriteString(ansi.Cut(line, selStart, selEnd))
			b.WriteString("\x1b[24m")
		}
		if selEnd < w {
			b.WriteString(ansi.Cut(line, selEnd, w))
		}
	}
	return b.String()
}

// clamp restricts v to the range [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
