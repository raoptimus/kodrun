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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMarkdownRenderer_Render_PlainText_Successfully(t *testing.T) {
	r := &markdownRenderer{}

	got := r.render("Hello world", 80)

	assert.Contains(t, got, "Hello world")
}

func TestMarkdownRenderer_Render_BoldText_Successfully(t *testing.T) {
	r := &markdownRenderer{}

	got := r.render("**bold**", 80)

	stripped := stripANSI(got)
	assert.Contains(t, stripped, "bold")
}

func TestMarkdownRenderer_Render_CodeBlock_Successfully(t *testing.T) {
	r := &markdownRenderer{}
	input := "```go\nfunc main() {}\n```"

	got := r.render(input, 80)

	stripped := stripANSI(got)
	assert.Contains(t, stripped, "func main()")
}

func TestMarkdownRenderer_Render_WidthChange_Successfully(t *testing.T) {
	r := &markdownRenderer{}

	r.render("text", 80)
	assert.Equal(t, 80, r.width)

	r.render("text", 120)
	assert.Equal(t, 120, r.width)
}

func TestMarkdownRenderer_Render_TrimsTrailingNewlines_Successfully(t *testing.T) {
	r := &markdownRenderer{}

	got := r.render("Hello", 80)

	assert.False(t, strings.HasSuffix(got, "\n"))
}

func TestMarkdownRenderer_Render_CachesRendererForSameWidth_Successfully(t *testing.T) {
	r := &markdownRenderer{}

	r.render("first", 80)
	renderer1 := r.renderer

	r.render("second", 80)
	renderer2 := r.renderer

	// Same renderer instance when width is unchanged.
	assert.Same(t, renderer1, renderer2)
}

// stripANSI removes ANSI escape sequences for easier assertion.
func stripANSI(s string) string {
	result := strings.Builder{}
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEsc = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}
