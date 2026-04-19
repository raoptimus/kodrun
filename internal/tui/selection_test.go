/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tui

import (
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
