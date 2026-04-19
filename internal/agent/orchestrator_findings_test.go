/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"strings"
	"testing"
)

func TestParseSpecialistFindings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []specialistFinding
	}{
		{
			name: "empty",
			in:   "",
			want: nil,
		},
		{
			name: "em-dash separator",
			in:   "internal/agent/agent.go:42 — blocker — race on counter",
			want: []specialistFinding{{
				file: "internal/agent/agent.go", line: 42, severity: "blocker",
				body: "race on counter",
			}},
		},
		{
			name: "en-dash separator",
			in:   "cmd/kodrun/main.go:10 – major – missing error check",
			want: []specialistFinding{{
				file: "cmd/kodrun/main.go", line: 10, severity: "major",
				body: "missing error check",
			}},
		},
		{
			name: "hyphen separator",
			in:   "a/b.go:7 - minor - magic number",
			want: []specialistFinding{{
				file: "a/b.go", line: 7, severity: "minor", body: "magic number",
			}},
		},
		{
			name: "list marker prefix",
			in:   "- x/y.go:1 — major — foo\n* z.go:2 — minor — bar\n1. q.go:3 — blocker — baz",
			want: []specialistFinding{
				{file: "x/y.go", line: 1, severity: "major", body: "foo"},
				{file: "z.go", line: 2, severity: "minor", body: "bar"},
				{file: "q.go", line: 3, severity: "blocker", body: "baz"},
			},
		},
		{
			name: "severity case-insensitive",
			in:   "f.go:1 — BLOCKER — up\nf.go:2 — Major — mid",
			want: []specialistFinding{
				{file: "f.go", line: 1, severity: "blocker", body: "up"},
				{file: "f.go", line: 2, severity: "major", body: "mid"},
			},
		},
		{
			name: "non-matching lines are ignored",
			in:   "## Heading\n\nSome prose.\nf.go:5 — minor — real\nnot a finding line\n",
			want: []specialistFinding{
				{file: "f.go", line: 5, severity: "minor", body: "real"},
			},
		},
		{
			name: "legacy body with FIX separator",
			in:   "a.go:10 — blocker — bad name — FIX: rename to good",
			want: []specialistFinding{{
				file: "a.go", line: 10, severity: "blocker",
				body: "bad name", fix: "rename to good",
			}},
		},
		{
			name: "multi-line block format",
			in: strings.Join([]string{
				"a.go:10 — blocker",
				"WHAT: bad variable name",
				"WHY: violates naming convention",
				"FIX: rename x to count",
				"BEFORE: `x := 0`",
				"AFTER: `count := 0`",
				"RULES: naming, variables",
			}, "\n"),
			want: []specialistFinding{{
				file: "a.go", line: 10, severity: "blocker",
				body: "bad variable name", why: "violates naming convention",
				fix: "rename x to count", before: "`x := 0`", after: "`count := 0`",
				ruleNames: []string{"naming", "variables"},
			}},
		},
		{
			name: "multi-line block minimal (WHAT + FIX only)",
			in: strings.Join([]string{
				"b.go:5 — major",
				"WHAT: missing error check",
				"FIX: add if err != nil check",
			}, "\n"),
			want: []specialistFinding{{
				file: "b.go", line: 5, severity: "major",
				body: "missing error check", fix: "add if err != nil check",
			}},
		},
		{
			name: "mixed old and new format",
			in: strings.Join([]string{
				"a.go:1 — minor — old style finding",
				"b.go:2 — major",
				"WHAT: new style finding",
				"FIX: do something",
			}, "\n"),
			want: []specialistFinding{
				{file: "a.go", line: 1, severity: "minor", body: "old style finding"},
				{file: "b.go", line: 2, severity: "major", body: "new style finding", fix: "do something"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSpecialistFindings(tc.in, RoleReviewer)
			if len(got) != len(tc.want) {
				t.Fatalf("len=%d want %d; got=%+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].file != tc.want[i].file ||
					got[i].line != tc.want[i].line ||
					got[i].severity != tc.want[i].severity ||
					got[i].body != tc.want[i].body {
					t.Errorf("item %d: got file=%q line=%d sev=%q body=%q, want file=%q line=%d sev=%q body=%q",
						i, got[i].file, got[i].line, got[i].severity, got[i].body,
						tc.want[i].file, tc.want[i].line, tc.want[i].severity, tc.want[i].body)
				}
				if got[i].fix != tc.want[i].fix {
					t.Errorf("item %d: got fix=%q, want fix=%q", i, got[i].fix, tc.want[i].fix)
				}
				if got[i].why != tc.want[i].why {
					t.Errorf("item %d: got why=%q, want why=%q", i, got[i].why, tc.want[i].why)
				}
				if got[i].before != tc.want[i].before {
					t.Errorf("item %d: got before=%q, want before=%q", i, got[i].before, tc.want[i].before)
				}
				if got[i].after != tc.want[i].after {
					t.Errorf("item %d: got after=%q, want after=%q", i, got[i].after, tc.want[i].after)
				}
				if len(got[i].ruleNames) != len(tc.want[i].ruleNames) {
					t.Errorf("item %d: got ruleNames=%v, want ruleNames=%v", i, got[i].ruleNames, tc.want[i].ruleNames)
				}
			}
		})
	}
}

func TestSplitBodyAndFix(t *testing.T) {
	cases := []struct {
		in       string
		wantBody string
		wantFix  string
	}{
		{"bad name — FIX: rename", "bad name", "rename"},
		{"bad name – FIX: rename", "bad name", "rename"},
		{"bad name - FIX: rename", "bad name", "rename"},
		{"no fix here", "no fix here", ""},
		{"has — but not FIX", "has — but not FIX", ""},
	}
	for _, tc := range cases {
		body, fix := splitBodyAndFix(tc.in)
		if body != tc.wantBody || fix != tc.wantFix {
			t.Errorf("splitBodyAndFix(%q) = (%q, %q), want (%q, %q)",
				tc.in, body, fix, tc.wantBody, tc.wantFix)
		}
	}
}

func TestMergeSpecialistFindings_SortAndFormat(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "b.go:20 — minor — z\na.go:10 — blocker — x"},
		{role: RoleArchReviewer, text: "a.go:5 — major — y"},
	}
	out := mergeSpecialistFindings(results, "en")
	if out == "" {
		t.Fatal("expected non-empty plan")
	}
	if !strings.Contains(out, "## Tasks") {
		t.Errorf("expected tasks section header, got:\n%s", out)
	}
	// Ordering: blocker < major < minor; within severity by file:line.
	blockerIdx := strings.Index(out, "blocker")
	majorIdx := strings.Index(out, "major")
	minorIdx := strings.Index(out, "minor")
	if !(blockerIdx >= 0 && blockerIdx < majorIdx && majorIdx < minorIdx) {
		t.Errorf("severity order wrong (blocker=%d major=%d minor=%d)\n%s",
			blockerIdx, majorIdx, minorIdx, out)
	}
	// New sections.
	if !strings.Contains(out, "## Affected files") {
		t.Errorf("expected affected files section, got:\n%s", out)
	}
	if !strings.Contains(out, "## Post-execution verification") {
		t.Errorf("expected verification section, got:\n%s", out)
	}
}

func TestMergeSpecialistFindings_MultiLineFormat(t *testing.T) {
	results := []reviewResult{
		{role: RoleCodeReviewer, text: strings.Join([]string{
			"a.go:10 — blocker",
			"WHAT: bad name",
			"WHY: violates convention",
			"FIX: rename to good",
			"BEFORE: `bad`",
			"AFTER: `good`",
			"RULES: naming",
		}, "\n")},
	}
	out := mergeSpecialistFindings(results, "en")
	if !strings.Contains(out, "### 1.") {
		t.Errorf("expected ### heading, got:\n%s", out)
	}
	if !strings.Contains(out, "- **What:** bad name") {
		t.Errorf("expected What field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Why:** violates convention") {
		t.Errorf("expected Why field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Fix:** rename to good") {
		t.Errorf("expected Fix field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Before:** `bad`") {
		t.Errorf("expected Before field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **After:** `good`") {
		t.Errorf("expected After field, got:\n%s", out)
	}
	if !strings.Contains(out, "- **Rules:** naming") {
		t.Errorf("expected Rules field, got:\n%s", out)
	}
}

func TestMergeSpecialistFindings_AllLGTM(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "LGTM"},
		{role: RoleArchReviewer, text: ""},
	}
	if out := mergeSpecialistFindings(results, "en"); out != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestMergeSpecialistFindings_AllNoIssues(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "NO_ISSUES"},
		{role: RoleArchReviewer, text: "NO_ISSUES"},
	}
	if out := mergeSpecialistFindings(results, "en"); out != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestMergeSpecialistFindings_UnparsedFallback(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "Something bad somewhere\nAnother concern"},
	}
	out := mergeSpecialistFindings(results, "en")
	if !strings.Contains(out, "(unstructured)") {
		t.Errorf("expected unstructured fallback entries, got:\n%s", out)
	}
	if !strings.Contains(out, "Something bad") || !strings.Contains(out, "Another concern") {
		t.Errorf("fallback should preserve raw lines, got:\n%s", out)
	}
}

func TestMergeSpecialistFindings_DedupSameBodyDifferentLines(t *testing.T) {
	// Same file, same body, different lines → should be grouped into one finding.
	results := []reviewResult{
		{role: RoleCodeReviewer, text: strings.Join([]string{
			"f_test.go:10 — major — context.Background() in tests",
			"f_test.go:25 — major — context.Background() in tests",
			"f_test.go:40 — major — context.Background() in tests",
		}, "\n")},
	}
	out := mergeSpecialistFindings(results, "en")
	// Should produce exactly 1 finding with grouped lines.
	lines := countPlanFindings(out)
	if lines != 1 {
		t.Errorf("expected 1 grouped finding, got %d findings:\n%s", lines, out)
	}
	if !strings.Contains(out, "lines:") {
		t.Errorf("expected grouped lines marker, got:\n%s", out)
	}
	if !strings.Contains(out, "10") && !strings.Contains(out, "25") && !strings.Contains(out, "40") {
		t.Errorf("expected all line numbers in grouped output, got:\n%s", out)
	}
}

func TestMergeSpecialistFindings_DedupSameBodyDifferentSpecialists(t *testing.T) {
	// Same file:line:body from two specialists → merged roles.
	results := []reviewResult{
		{role: RoleCodeReviewer, text: "a.go:10 — major — missing error check"},
		{role: RoleArchReviewer, text: "a.go:10 — major — missing error check"},
	}
	out := mergeSpecialistFindings(results, "en")
	lines := countPlanFindings(out)
	if lines != 1 {
		t.Errorf("expected 1 merged finding, got %d findings:\n%s", lines, out)
	}
	// Both roles should appear in the output.
	if !strings.Contains(out, string(RoleCodeReviewer)) {
		t.Errorf("expected role %s in output:\n%s", RoleCodeReviewer, out)
	}
	if !strings.Contains(out, string(RoleArchReviewer)) {
		t.Errorf("expected role %s in output:\n%s", RoleArchReviewer, out)
	}
}

func TestMergeSpecialistFindings_DedupCombined(t *testing.T) {
	// Same body on multiple lines from multiple specialists → one finding,
	// merged roles, grouped lines.
	results := []reviewResult{
		{role: RoleCodeReviewer, text: strings.Join([]string{
			"f.go:10 — major — bad pattern",
			"f.go:20 — major — bad pattern",
		}, "\n")},
		{role: RoleArchReviewer, text: "f.go:10 — major — bad pattern"},
	}
	out := mergeSpecialistFindings(results, "en")
	lines := countPlanFindings(out)
	if lines != 1 {
		t.Errorf("expected 1 combined finding, got %d findings:\n%s", lines, out)
	}
	if !strings.Contains(out, string(RoleCodeReviewer)) ||
		!strings.Contains(out, string(RoleArchReviewer)) {
		t.Errorf("expected both roles in output:\n%s", out)
	}
	if !strings.Contains(out, "lines:") {
		t.Errorf("expected grouped lines in output:\n%s", out)
	}
}

func TestMergeSpecialistFindings_DifferentBodiesNotMerged(t *testing.T) {
	// Different bodies should NOT be merged even if same file:line.
	results := []reviewResult{
		{role: RoleReviewer, text: strings.Join([]string{
			"a.go:10 — major — missing error check",
			"a.go:10 — minor — magic number",
		}, "\n")},
	}
	out := mergeSpecialistFindings(results, "en")
	lines := countPlanFindings(out)
	if lines != 2 {
		t.Errorf("expected 2 findings (different bodies), got %d findings:\n%s", lines, out)
	}
}

func TestMergeSpecialistFindings_SeverityUpgrade(t *testing.T) {
	// Same finding from two specialists with different severity → keep max (blocker > major).
	results := []reviewResult{
		{role: RoleCodeReviewer, text: "a.go:10 — major — race condition"},
		{role: RoleReviewer, text: "a.go:10 — blocker — race condition"},
	}
	out := mergeSpecialistFindings(results, "en")
	lines := countPlanFindings(out)
	if lines != 1 {
		t.Errorf("expected 1 merged finding, got %d findings:\n%s", lines, out)
	}
	if !strings.Contains(out, "blocker") {
		t.Errorf("expected blocker severity (highest), got:\n%s", out)
	}
}

func TestMergeSpecialistFindings_RuleNamesMerge(t *testing.T) {
	results := []reviewResult{
		{role: RoleCodeReviewer, text: strings.Join([]string{
			"a.go:10 — major",
			"WHAT: bad pattern",
			"FIX: fix it",
			"RULES: naming",
		}, "\n")},
		{role: RoleArchReviewer, text: strings.Join([]string{
			"a.go:10 — major",
			"WHAT: bad pattern",
			"FIX: fix it",
			"RULES: error-wrapping",
		}, "\n")},
	}
	out := mergeSpecialistFindings(results, "en")
	if !strings.Contains(out, "naming") || !strings.Contains(out, "error-wrapping") {
		t.Errorf("expected merged rule names, got:\n%s", out)
	}
}

func TestNormalizeBody(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"  Hello World.  ", "hello world"},
		{"trailing;", "trailing"},
		{"UPPER CASE!", "upper case"},
		{"no change", "no change"},
		{"dots...", "dots"},
	}
	for _, tc := range cases {
		got := normalizeBody(tc.in)
		if got != tc.want {
			t.Errorf("normalizeBody(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsNoIssues(t *testing.T) {
	for _, s := range []string{"LGTM", "lgtm", "NO_ISSUES", "no_issues"} {
		if !isNoIssues(s) {
			t.Errorf("isNoIssues(%q) should be true", s)
		}
	}
	// Empty string is NOT "no issues" — it's a silent failure.
	for _, s := range []string{"", "some finding", "LGTM but...", "NO"} {
		if isNoIssues(s) {
			t.Errorf("isNoIssues(%q) should be false", s)
		}
	}
}

// countPlanFindings counts ### headings (findings) in the plan output.
func countPlanFindings(plan string) int {
	count := 0
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### ") {
			count++
		}
	}
	return count
}
