/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tui

import (
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
)

const mdIndent = 2 // indent / margin for markdown rendering

func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func uintPtr(u uint) *uint       { return &u }

// kodrunStyle is a minimal glamour style that works on both light and dark
// terminals. No background colors. Headings are bold.
// Inline code is bold (no colored background). Lists use bullet markers.
var kodrunStyle = ansi.StyleConfig{
	Document: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			BlockPrefix: "\n",
		},
		Margin: uintPtr(0),
	},
	Heading: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold:        boolPtr(true),
			BlockSuffix: "\n",
		},
	},
	H1: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold: boolPtr(true),
		},
	},
	H2: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold: boolPtr(true),
		},
	},
	H3: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold: boolPtr(true),
		},
	},
	Paragraph: ansi.StyleBlock{},
	Code: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Bold: boolPtr(true),
		},
	},
	CodeBlock: ansi.StyleCodeBlock{
		StyleBlock: ansi.StyleBlock{
			Margin: uintPtr(mdIndent),
		},
		Chroma: &ansi.Chroma{},
	},
	Strong: ansi.StylePrimitive{
		Bold: boolPtr(true),
	},
	Emph: ansi.StylePrimitive{
		Italic: boolPtr(true),
	},
	List: ansi.StyleList{
		LevelIndent: mdIndent,
		StyleBlock: ansi.StyleBlock{
			Margin: uintPtr(0),
		},
	},
	Item: ansi.StylePrimitive{
		BlockPrefix: "• ",
	},
	Enumeration: ansi.StylePrimitive{
		BlockPrefix: ". ",
	},
	BlockQuote: ansi.StyleBlock{
		StylePrimitive: ansi.StylePrimitive{
			Italic: boolPtr(true),
		},
		Indent:      uintPtr(1),
		IndentToken: stringPtr("│ "),
	},
	Link: ansi.StylePrimitive{
		Underline: boolPtr(true),
	},
	LinkText: ansi.StylePrimitive{
		Bold: boolPtr(true),
	},
	Task: ansi.StyleTask{
		Ticked:   "[x] ",
		Unticked: "[ ] ",
	},
	Table: ansi.StyleTable{},
}

// markdownRenderer renders markdown text for terminal display.
// Lazily initialized with the given width.
type markdownRenderer struct {
	mu       sync.Mutex
	renderer *glamour.TermRenderer
	width    int
}

// render converts markdown text to styled terminal output.
// Falls back to raw text on error.
func (m *markdownRenderer) render(text string, width int) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.renderer == nil || m.width != width {
		r, err := glamour.NewTermRenderer(
			glamour.WithStyles(kodrunStyle),
			glamour.WithWordWrap(width),
		)
		if err != nil {
			return text
		}
		m.renderer = r
		m.width = width
	}

	out, err := m.renderer.Render(text)
	if err != nil {
		return text
	}

	// glamour adds trailing newlines; trim them for inline use.
	return strings.TrimRight(out, "\n")
}
