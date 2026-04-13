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
					t.Errorf("item %d: got %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestMergeSpecialistFindings_SortAndFormat(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "b.go:20 — minor — z\na.go:10 — blocker — x"},
		{role: RoleArchReviewer, text: "a.go:5 — major — y"},
	}
	out := mergeSpecialistFindings(results)
	if out == "" {
		t.Fatal("expected non-empty plan")
	}
	if !strings.HasPrefix(out, "## Plan") {
		t.Errorf("expected ## Plan header, got:\n%s", out)
	}
	// Ordering: blocker < major < minor; within severity by file:line.
	blockerIdx := strings.Index(out, "blocker")
	majorIdx := strings.Index(out, "major")
	minorIdx := strings.Index(out, "minor")
	if !(blockerIdx >= 0 && blockerIdx < majorIdx && majorIdx < minorIdx) {
		t.Errorf("severity order wrong (blocker=%d major=%d minor=%d)\n%s",
			blockerIdx, majorIdx, minorIdx, out)
	}
}

func TestMergeSpecialistFindings_AllLGTM(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "LGTM"},
		{role: RoleArchReviewer, text: ""},
	}
	if out := mergeSpecialistFindings(results); out != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestMergeSpecialistFindings_AllNoIssues(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "NO_ISSUES"},
		{role: RoleArchReviewer, text: "NO_ISSUES"},
	}
	if out := mergeSpecialistFindings(results); out != "" {
		t.Errorf("expected empty, got %q", out)
	}
}

func TestMergeSpecialistFindings_UnparsedFallback(t *testing.T) {
	results := []reviewResult{
		{role: RoleReviewer, text: "Something bad somewhere\nAnother concern"},
	}
	out := mergeSpecialistFindings(results)
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
	out := mergeSpecialistFindings(results)
	// Should produce exactly 1 finding with grouped lines.
	lines := countPlanLines(out)
	if lines != 1 {
		t.Errorf("expected 1 grouped finding, got %d plan lines:\n%s", lines, out)
	}
	if !strings.Contains(out, "строки:") {
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
	out := mergeSpecialistFindings(results)
	lines := countPlanLines(out)
	if lines != 1 {
		t.Errorf("expected 1 merged finding, got %d plan lines:\n%s", lines, out)
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
	out := mergeSpecialistFindings(results)
	lines := countPlanLines(out)
	if lines != 1 {
		t.Errorf("expected 1 combined finding, got %d plan lines:\n%s", lines, out)
	}
	if !strings.Contains(out, string(RoleCodeReviewer)) ||
		!strings.Contains(out, string(RoleArchReviewer)) {
		t.Errorf("expected both roles in output:\n%s", out)
	}
	if !strings.Contains(out, "строки:") {
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
	out := mergeSpecialistFindings(results)
	lines := countPlanLines(out)
	if lines != 2 {
		t.Errorf("expected 2 findings (different bodies), got %d plan lines:\n%s", lines, out)
	}
}

func TestMergeSpecialistFindings_SeverityUpgrade(t *testing.T) {
	// Same finding from two specialists with different severity → keep max (blocker > major).
	results := []reviewResult{
		{role: RoleCodeReviewer, text: "a.go:10 — major — race condition"},
		{role: RoleReviewer, text: "a.go:10 — blocker — race condition"},
	}
	out := mergeSpecialistFindings(results)
	lines := countPlanLines(out)
	if lines != 1 {
		t.Errorf("expected 1 merged finding, got %d plan lines:\n%s", lines, out)
	}
	if !strings.Contains(out, "blocker") {
		t.Errorf("expected blocker severity (highest), got:\n%s", out)
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

// countPlanLines counts numbered lines (findings) in the plan output.
func countPlanLines(plan string) int {
	count := 0
	for _, line := range strings.Split(plan, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 0 && line[0] >= '1' && line[0] <= '9' {
			count++
		}
	}
	return count
}
