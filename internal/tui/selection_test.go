package tui

import (
	"strings"
	"testing"
)

func TestSelection_Normalized(t *testing.T) {
	tests := []struct {
		name      string
		sel       Selection
		wantStart CellPos
		wantEnd   CellPos
	}{
		{
			name:      "already ordered",
			sel:       Selection{Start: CellPos{0, 2}, End: CellPos{1, 5}, HasRange: true},
			wantStart: CellPos{0, 2},
			wantEnd:   CellPos{1, 5},
		},
		{
			name:      "reversed lines",
			sel:       Selection{Start: CellPos{3, 0}, End: CellPos{1, 4}, HasRange: true},
			wantStart: CellPos{1, 4},
			wantEnd:   CellPos{3, 0},
		},
		{
			name:      "same line reversed cols",
			sel:       Selection{Start: CellPos{2, 10}, End: CellPos{2, 3}, HasRange: true},
			wantStart: CellPos{2, 3},
			wantEnd:   CellPos{2, 10},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, e := tt.sel.Normalized()
			if s != tt.wantStart || e != tt.wantEnd {
				t.Errorf("Normalized() = (%v, %v), want (%v, %v)", s, e, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

func TestSelection_Contains(t *testing.T) {
	tests := []struct {
		name string
		sel  Selection
		line int
		col  int
		want bool
	}{
		{
			name: "single line inside",
			sel:  Selection{Start: CellPos{1, 2}, End: CellPos{1, 8}, HasRange: true},
			line: 1, col: 5, want: true,
		},
		{
			name: "single line at start",
			sel:  Selection{Start: CellPos{1, 2}, End: CellPos{1, 8}, HasRange: true},
			line: 1, col: 2, want: true,
		},
		{
			name: "single line at end (exclusive)",
			sel:  Selection{Start: CellPos{1, 2}, End: CellPos{1, 8}, HasRange: true},
			line: 1, col: 8, want: false,
		},
		{
			name: "multi line middle",
			sel:  Selection{Start: CellPos{1, 3}, End: CellPos{3, 5}, HasRange: true},
			line: 2, col: 0, want: true,
		},
		{
			name: "multi line first line before start",
			sel:  Selection{Start: CellPos{1, 3}, End: CellPos{3, 5}, HasRange: true},
			line: 1, col: 1, want: false,
		},
		{
			name: "multi line last line after end",
			sel:  Selection{Start: CellPos{1, 3}, End: CellPos{3, 5}, HasRange: true},
			line: 3, col: 6, want: false,
		},
		{
			name: "outside above",
			sel:  Selection{Start: CellPos{2, 0}, End: CellPos{4, 0}, HasRange: true},
			line: 1, col: 0, want: false,
		},
		{
			name: "no range",
			sel:  Selection{Start: CellPos{1, 2}, End: CellPos{1, 2}, HasRange: false},
			line: 1, col: 2, want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sel.Contains(tt.line, tt.col); got != tt.want {
				t.Errorf("Contains(%d, %d) = %v, want %v", tt.line, tt.col, got, tt.want)
			}
		})
	}
}

func TestScreenToContent(t *testing.T) {
	tests := []struct {
		name           string
		screenX        int
		screenY        int
		viewportTop    int
		viewportHeight int
		yOffset        int
		logsLen        int
		wantPos        CellPos
		wantOk         bool
	}{
		{
			name:    "inside viewport",
			screenX: 5, screenY: 3,
			viewportTop: 1, viewportHeight: 20, yOffset: 0, logsLen: 30,
			wantPos: CellPos{Line: 2, Col: 5}, wantOk: true,
		},
		{
			name:    "with scroll offset",
			screenX: 10, screenY: 1,
			viewportTop: 1, viewportHeight: 20, yOffset: 15, logsLen: 50,
			wantPos: CellPos{Line: 15, Col: 10}, wantOk: true,
		},
		{
			name:    "above viewport",
			screenX: 0, screenY: 0,
			viewportTop: 1, viewportHeight: 20, yOffset: 0, logsLen: 30,
			wantPos: CellPos{}, wantOk: false,
		},
		{
			name:    "below viewport",
			screenX: 0, screenY: 21,
			viewportTop: 1, viewportHeight: 20, yOffset: 0, logsLen: 30,
			wantPos: CellPos{}, wantOk: false,
		},
		{
			name:    "below actual content is not selectable",
			screenX: 0, screenY: 5,
			viewportTop: 1, viewportHeight: 20, yOffset: 0, logsLen: 3,
			wantPos: CellPos{}, wantOk: false,
		},
		{
			name:    "empty logs",
			screenX: 0, screenY: 1,
			viewportTop: 1, viewportHeight: 20, yOffset: 0, logsLen: 0,
			wantPos: CellPos{}, wantOk: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pos, ok := screenToContent(tt.screenX, tt.screenY, tt.viewportTop, tt.viewportHeight, tt.yOffset, tt.logsLen)
			if ok != tt.wantOk || pos != tt.wantPos {
				t.Errorf("screenToContent() = (%v, %v), want (%v, %v)", pos, ok, tt.wantPos, tt.wantOk)
			}
		})
	}
}

func TestExtractSelectedText(t *testing.T) {
	logs := []string{
		"Hello World",
		"\x1b[31mRed Text\x1b[0m here",
		"Third line of text",
	}

	tests := []struct {
		name string
		sel  Selection
		want string
	}{
		{
			name: "single line partial",
			sel:  Selection{Start: CellPos{0, 0}, End: CellPos{0, 5}, HasRange: true},
			want: "Hello",
		},
		{
			name: "single line middle",
			sel:  Selection{Start: CellPos{0, 6}, End: CellPos{0, 11}, HasRange: true},
			want: "World",
		},
		{
			name: "multi line",
			sel:  Selection{Start: CellPos{0, 6}, End: CellPos{1, 8}, HasRange: true},
			want: "World\nRed Text",
		},
		{
			name: "with ANSI stripping",
			sel:  Selection{Start: CellPos{1, 0}, End: CellPos{1, 8}, HasRange: true},
			want: "Red Text",
		},
		{
			name: "full span",
			sel:  Selection{Start: CellPos{0, 0}, End: CellPos{2, 18}, HasRange: true},
			want: "Hello World\nRed Text here\nThird line of text",
		},
		{
			name: "no range",
			sel:  Selection{HasRange: false},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSelectedText(logs, tt.sel)
			if got != tt.want {
				t.Errorf("extractSelectedText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderLogsWithSelection(t *testing.T) {
	logs := []string{"abc", "def", "ghi"}

	t.Run("single line selection has reverse video", func(t *testing.T) {
		sel := Selection{Start: CellPos{1, 1}, End: CellPos{1, 2}, HasRange: true}
		got := renderLogsWithSelection(logs, sel, 80)
		want := "abc\nd\x1b[4me\x1b[24mf\nghi"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("no selection returns joined logs", func(t *testing.T) {
		sel := Selection{}
		got := renderLogsWithSelection(logs, sel, 80)
		want := "abc\ndef\nghi"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("mid-line selection", func(t *testing.T) {
		sel := Selection{Start: CellPos{0, 1}, End: CellPos{0, 2}, HasRange: true}
		got := renderLogsWithSelection(logs, sel, 80)
		want := "a\x1b[4mb\x1b[24mc\ndef\nghi"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("ANSI colors preserved in non-selected segments", func(t *testing.T) {
		ansiLogs := []string{
			"\x1b[31m[agent]\x1b[0m hello world",
			"\x1b[34m[tool]\x1b[0m result here",
		}
		sel := Selection{Start: CellPos{0, 8}, End: CellPos{0, 13}, HasRange: true}
		got := renderLogsWithSelection(ansiLogs, sel, 80)
		// Before selection should keep red ANSI, selected part gets underline, after stays plain
		if !strings.Contains(got, "\x1b[31m") {
			t.Errorf("expected ANSI red color to be preserved, got %q", got)
		}
		if !strings.Contains(got, "\x1b[4m") {
			t.Errorf("expected underline in selection, got %q", got)
		}
		// Second line should keep blue ANSI untouched
		if !strings.Contains(got, "\x1b[34m") {
			t.Errorf("expected ANSI blue color preserved on non-selected line, got %q", got)
		}
	})

	t.Run("multi-line selection highlights only selected text", func(t *testing.T) {
		sel := Selection{Start: CellPos{0, 1}, End: CellPos{2, 2}, HasRange: true}
		got := renderLogsWithSelection(logs, sel, 10)
		want := "a\x1b[4mbc\x1b[24m\n" +
			"\x1b[4mdef\x1b[24m\n" +
			"\x1b[4mgh\x1b[24mi"
		if got != want {
			t.Errorf("got  %q\nwant %q", got, want)
		}
	})
}
