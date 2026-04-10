package agent

import (
	"regexp"
	"strings"
)

// numberedListItem matches a top-level numbered list bullet at the start of
// a line: "1.", "2)", "10.". Used to detect plan-shaped responses.
var numberedListItem = regexp.MustCompile(`(?m)^[ \t]*\d{1,3}[.)]\s`)

// Heuristic thresholds for looksLikeMarkdownPlan. Tuned against local-model
// failure modes where the model dumps a markdown plan instead of calling
// tools; see the function comment for rationale.
const (
	planMinContentLen      = 80 // shorter replies are treated as real answers
	planMinNumberedItems   = 3  // "1. / 2. / 3." at line start → list of steps
	planMinMarkdownHeaders = 2  // "## A" + "## B" with no code fence → plan
)

// maxEditNudges caps how many times a single Send() invocation will nudge
// the model back into tool-calling mode when it answers EDIT requests with
// prose. Prevents loops on models that refuse to call tools.
const maxEditNudges = 2

// maxPlanBlocked caps consecutive iterations in plan mode where every tool
// call was blocked (model hallucinated write tools). After this many
// iterations the agent stops early to avoid wasting the iteration budget.
const maxPlanBlocked = 3

// planMarkerWords are case-insensitive substrings that strongly indicate a
// plan/analysis dump rather than executed work. Local models in RU/EN both
// fall back to these headings when they refuse to call tools.
var planMarkerWords = []string{
	"план исправлен",
	"план измен",
	"план действ",
	"анализ текущ",
	"подробное описание изменений",
	"plan of changes",
	"implementation plan",
	"step-by-step plan",
	"## plan",
	"## план",
}

// looksLikeMarkdownPlan reports whether content is shaped like a markdown
// plan/analysis the model produced instead of calling tools. The intent is
// to catch the EDIT-mode failure mode where the model lists steps it would
// take instead of taking them. False positives are acceptable (we just
// nudge — at most twice — and on the second time fall back to normal exit).
//
// Heuristic, in order of weight:
//  1. Empty/short content → not a plan (a real answer to a question is fine).
//  2. Contains any planMarkerWords → very strong signal.
//  3. Has 3 or more numbered-list items at line start → likely a plan.
//  4. Has 2+ markdown headers (##) AND no fenced code block → likely a plan.
func looksLikeMarkdownPlan(content string) bool {
	c := strings.TrimSpace(content)
	if len(c) < planMinContentLen {
		return false
	}
	low := strings.ToLower(c)
	for _, w := range planMarkerWords {
		if strings.Contains(low, w) {
			return true
		}
	}
	if matches := numberedListItem.FindAllStringIndex(c, -1); len(matches) >= planMinNumberedItems {
		return true
	}
	headerCount := 0
	for _, line := range strings.Split(c, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "## ") || strings.HasPrefix(t, "### ") {
			headerCount++
		}
	}
	if headerCount >= planMinMarkdownHeaders && !strings.Contains(c, "```") {
		return true
	}
	return false
}
