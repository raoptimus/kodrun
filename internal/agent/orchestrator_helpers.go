/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/raoptimus/kodrun/internal/rules"
)

// stepSplitRe matches the leading numeric marker of a structured plan step
// like "1. ", "2) ". Used by splitPlanSteps to break a plan into per-step
// substrings for fan-out RAG queries.
var stepSplitRe = regexp.MustCompile(`(?m)^\d+[.\)]\s`)

// splitPlanSteps splits a structured plan into individual steps. Plans are
// typically numbered lists (1. ... 2. ...) or paragraph-separated blocks; the
// numbered form is preferred and the paragraph fallback covers free-form text.
func splitPlanSteps(plan string) []string {
	indices := stepSplitRe.FindAllStringIndex(plan, -1)
	if len(indices) >= minRegexMatches {
		steps := make([]string, 0, len(indices))
		for i, idx := range indices {
			var end int
			if i+1 < len(indices) {
				end = indices[i+1][0]
			} else {
				end = len(plan)
			}
			step := strings.TrimSpace(plan[idx[0]:end])
			if len(step) >= minStepTextLen {
				steps = append(steps, step)
			}
		}
		if len(steps) >= minRegexMatches {
			return steps
		}
	}

	paragraphs := strings.Split(plan, "\n\n")
	var steps []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) >= minStepTextLen {
			steps = append(steps, p)
		}
	}
	return steps
}

// truncateTask shortens s to maxLen runes, appending "..." when truncated.
// Used when emitting human-readable phase labels for long task texts.
func truncateTask(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isConnectionError returns true when the error indicates that the LLM backend
// (ollama) is unreachable — connection refused, DNS failure, or dial timeout.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp")
}

// extractReplanReason pulls the reason text from a REPLAN sentinel emitted by
// the executor (line of the form "REPLAN: <reason>"). Returns the original
// text when no sentinel is present.
func extractReplanReason(text string) string {
	idx := strings.Index(text, "REPLAN:")
	if idx < 0 {
		return text
	}
	tail := text[idx+len("REPLAN:"):]
	if nl := strings.IndexByte(tail, '\n'); nl >= 0 {
		tail = tail[:nl]
	}
	return strings.TrimSpace(tail)
}

// collectRuleNames extracts all rule names (file basename without .md) from
// the given rules.Loader. Returns nil if the loader is nil or empty.
func collectRuleNames(l *rules.Loader) []string {
	if l == nil {
		return nil
	}
	all := l.AllRules()
	if len(all) == 0 {
		return nil
	}
	out := make([]string, 0, len(all))
	for _, r := range all {
		name := strings.TrimSuffix(filepath.Base(r.Path), ".md")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
