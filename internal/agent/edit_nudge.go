package agent

import (
	"regexp"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
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

// maxPlanToolNudges caps how many times a single Send() invocation will
// nudge the model to call tools when it returns NO_ISSUES in plan mode
// without having called any tools first.
const maxPlanToolNudges = 1

// maxReadFileNudges caps how many times a single Send() will nudge the
// model to call read_file after it called file_stat but stopped without
// reading any file contents. Part of the guided-flow mechanism for weak
// models that execute only one workflow step per turn.
const maxReadFileNudges = 1

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

// textToolCallKeys are the known key names that appear in text-form tool calls.
// The order matters: we scan for these in sequence to delimit multiline values.
var textToolCallKeys = []string{"path:", "old_str:", "new_str:", "content:"}

// parseTextToolCall attempts to parse a model response that contains an
// edit_file or write_file tool call written as plain text instead of a JSON
// tool call. Returns nil if the pattern is not detected.
//
// Recognised format (lines are trimmed):
//
//	edit_file
//	path: <value>
//	old_str: <value — may span multiple lines>
//	new_str: <value — may span multiple lines>
func parseTextToolCall(content string) *llm.ToolCall {
	lines := strings.Split(content, "\n")

	// Find the tool name line.
	toolName := ""
	startIdx := -1
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == toolNameEditFile || t == toolNameWriteFile {
			toolName = t
			startIdx = i + 1
			break
		}
	}
	if toolName == "" || startIdx >= len(lines) {
		return nil
	}

	// Parse key: value pairs, collecting multiline values.
	kv := make(map[string]string)
	var currentKey string
	var currentVal strings.Builder

	flushKey := func() {
		if currentKey != "" {
			kv[currentKey] = strings.TrimRight(currentVal.String(), "\n")
			currentVal.Reset()
		}
	}

	for i := startIdx; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Check if this line starts a new key.
		foundKey := ""
		for _, k := range textToolCallKeys {
			if strings.HasPrefix(trimmed, k) {
				foundKey = k
				break
			}
		}

		if foundKey != "" {
			flushKey()
			currentKey = strings.TrimSuffix(foundKey, ":")
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, foundKey))
			currentVal.WriteString(val)
		} else if currentKey != "" {
			// Continuation of the current multiline value.
			if currentVal.Len() > 0 {
				currentVal.WriteByte('\n')
			}
			currentVal.WriteString(line)
		}
	}
	flushKey()

	path := kv["path"]
	if path == "" {
		return nil
	}

	args := map[string]any{"path": path}

	switch toolName {
	case toolNameEditFile:
		oldStr, hasOld := kv["old_str"]
		newStr, hasNew := kv["new_str"]
		if !hasOld && !hasNew {
			return nil
		}
		if hasOld {
			args["old_str"] = oldStr
		}
		if hasNew {
			args["new_str"] = newStr
		}
	case toolNameWriteFile:
		c, has := kv["content"]
		if !has {
			return nil
		}
		args["content"] = c
	}

	return &llm.ToolCall{
		ID: "synth_0",
		Function: llm.ToolCallFunc{
			Name:      toolName,
			Arguments: args,
		},
	}
}

// ExtractDiffFromText attempts to extract old_str/new_str from a text-form
// edit_file tool call and returns a SimpleDiff string for TUI rendering.
// Returns "" if the content does not match the pattern.
func ExtractDiffFromText(content string) string {
	tc := parseTextToolCall(content)
	if tc == nil || tc.Function.Name != toolNameEditFile {
		return ""
	}
	oldStr := stringFromMap(tc.Function.Arguments, "old_str")
	newStr := stringFromMap(tc.Function.Arguments, "new_str")
	path := stringFromMap(tc.Function.Arguments, "path")
	if oldStr == "" && newStr == "" {
		return ""
	}
	return tools.SimpleDiff(oldStr, newStr, path, editDiffMaxLines)
}

// editDiffMaxLines limits the number of diff lines shown for text-form tool calls.
const editDiffMaxLines = 30

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
