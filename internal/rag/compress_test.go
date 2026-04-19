/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"strings"
	"testing"
)

func TestCompressChunk_Idempotent(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		content string
	}{
		{"go", "svc.go", "package foo\n\nimport (\n\t\"fmt\"\n)\n\n// Bar is exported.\nfunc Bar() {\n\tfmt.Println(\"x\") // inline\n}\n"},
		{"md", "rules/style.md", "# Title\n\n<!-- note -->\n\nA paragraph.\n\n\n```go\nfunc x() {}\n```\n\n\nend\n"},
		{"txt", "notes.txt", "line 1\n\n\nline 2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := CompressChunk(tc.path, tc.content)
			second := CompressChunk(tc.path, first)
			if first != second {
				t.Errorf("not idempotent:\nfirst=%q\nsecond=%q", first, second)
			}
			if len(first) > len(tc.content) {
				t.Errorf("compression grew content: %d > %d", len(first), len(tc.content))
			}
		})
	}
}

func TestCompressChunk_GoStripsImportsAndComments(t *testing.T) {
	in := "package foo\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\n// internal helper\nfunc internal() {\n\t// inline\n\tfmt.Println(strings.ToUpper(\"x\"))\n}\n\n// Bar is exported.\nfunc Bar() {}\n"
	out := CompressChunk("svc.go", in)
	if strings.Contains(out, "import (") || strings.Contains(out, "\"fmt\"") {
		t.Errorf("imports not stripped: %q", out)
	}
	if strings.Contains(out, "// internal helper") {
		t.Errorf("unexported-decl comment not stripped: %q", out)
	}
	if !strings.Contains(out, "// Bar is exported.") {
		t.Errorf("exported-decl comment wrongly stripped: %q", out)
	}
	if !strings.Contains(out, "func Bar()") {
		t.Errorf("code lost: %q", out)
	}
}

func TestCompressChunk_MarkdownKeepsFences(t *testing.T) {
	in := "# H\n\n\n```go\n\n\nfunc x() {}\n```\n"
	out := CompressChunk("doc.md", in)
	if !strings.Contains(out, "func x() {}") {
		t.Errorf("fence content lost: %q", out)
	}
	if !strings.Contains(out, "```go") {
		t.Errorf("fence marker lost: %q", out)
	}
}
