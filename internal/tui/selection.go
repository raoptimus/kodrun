package tui

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
