/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// reviewResult holds the outcome of a single specialist reviewer sub-agent.
type reviewResult struct {
	role      Role
	text      string
	err       error
	duration  time.Duration
	stats     SessionStats
	toolCalls int
}

// specialistFinding is a parsed finding from a specialist's output.
// Supports both single-line (`path:LINE — SEVERITY — body`) and multi-line
// block format (header + WHAT/WHY/FIX/BEFORE/AFTER/RULES fields).
type specialistFinding struct {
	file      string
	line      int
	severity  string
	body      string    // WHAT: description of the problem
	why       string    // WHY: rationale for fixing
	fix       string    // FIX: concrete suggestion
	before    string    // BEFORE: existing code snippet
	after     string    // AFTER: corrected code snippet
	ruleNames []string  // RULES: referenced rule names
	roles     []Role    // specialists that reported this finding (for dedup)
	examples  []Example // EXAMPLE: continuation lines parsed from reviewer output
}

// severityRank orders findings: blocker first, then major, then minor.
func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "blocker":
		return 0
	case "major":
		return 1
	case "minor":
		return severityMinor
	}
	return severityUnknown
}

// specialistFindingRe captures file, line, severity and body from a single
// finding line. Accepts em-dash, en-dash or hyphen as separators and
// tolerates optional leading list markers.
var specialistFindingRe = regexp.MustCompile(
	`(?i)^[\s\-*>0-9.)]*\**\s*([\w./\\-]+?):(\d+)\**\s*[—–\-:]+\s*\**(blocker|major|minor)\**\s*[—–\-:]+\s*(.+?)\**$`,
)

// specialistFindingHeaderRe captures the header of a multi-line finding block:
// path:LINE — SEVERITY (no body on the same line).
var specialistFindingHeaderRe = regexp.MustCompile(
	`(?i)^[\s\-*>0-9.)]*\**\s*([\w./\\-]+?):(\d+)\**\s*[—–\-:]+\s*\**(blocker|major|minor)\**\s*$`,
)

// findingFieldRe captures structured fields (WHAT/WHY/FIX/BEFORE/AFTER/RULES)
// from continuation lines of a multi-line finding block.
var findingFieldRe = regexp.MustCompile(
	`(?i)^\s*\**\s*(WHAT|WHY|FIX|BEFORE|AFTER|RULES)\s*:\s*\**\s*(.+)$`,
)

// exampleLineRe captures EXAMPLE: continuation lines from specialist output.
// Format: EXAMPLE: path/to/file.go:LINE — reason
var exampleLineRe = regexp.MustCompile(
	`(?i)^\s*EXAMPLE:\s*([\w./\\-]+?):(\d+)\s*[—–\-]\s*(.+)$`,
)

// splitBodyAndFix splits a legacy single-line body that contains both
// description and fix suggestion separated by "— FIX:".
func splitBodyAndFix(body string) (description, fix string) {
	upper := strings.ToUpper(body)
	for _, sep := range []string{" — FIX: ", " – FIX: ", " - FIX: "} {
		idx := strings.Index(upper, sep)
		if idx >= 0 {
			return strings.TrimSpace(body[:idx]), strings.TrimSpace(body[idx+len(sep):])
		}
	}
	return body, ""
}

// parseSpecialistFindings extracts findings from a specialist's raw output.
// Supports both multi-line block format (header + WHAT/WHY/FIX/... fields)
// and legacy single-line format. Non-matching lines are ignored. EXAMPLE:
// continuation lines are attached to the immediately preceding finding.
func parseSpecialistFindings(text string, role Role) []specialistFinding {
	lines := strings.Split(text, "\n")
	out := make([]specialistFinding, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Try EXAMPLE: continuation line first — attach to last finding.
		if em := exampleLineRe.FindStringSubmatch(line); em != nil {
			if len(out) > 0 {
				exLine, atoiErr := strconv.Atoi(em[2])
				if atoiErr != nil {
					continue
				}
				out[len(out)-1].examples = append(out[len(out)-1].examples, Example{
					File: em[1],
					Line: exLine,
					Note: strings.TrimSpace(em[3]),
				})
			}
			continue
		}
		// Try structured field continuation (WHAT/WHY/FIX/BEFORE/AFTER/RULES).
		if fm := findingFieldRe.FindStringSubmatch(line); fm != nil {
			if len(out) > 0 {
				last := &out[len(out)-1]
				val := strings.TrimSpace(fm[2])
				switch strings.ToUpper(fm[1]) {
				case "WHAT":
					last.body = val
				case "WHY":
					last.why = val
				case "FIX":
					last.fix = val
				case "BEFORE":
					last.before = val
				case "AFTER":
					last.after = val
				case "RULES":
					last.ruleNames = parseRuleNames(val)
				}
			}
			continue
		}
		// Try multi-line header: file:LINE — SEVERITY (no body).
		if hm := specialistFindingHeaderRe.FindStringSubmatch(line); hm != nil {
			lineNo, err := strconv.Atoi(hm[2])
			if err != nil {
				continue
			}
			out = append(out, specialistFinding{
				file:     hm[1],
				line:     lineNo,
				severity: strings.ToLower(hm[3]),
				roles:    []Role{role},
			})
			continue
		}
		// Fallback: legacy single-line format.
		m := specialistFindingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		desc, fixPart := splitBodyAndFix(strings.TrimSpace(m[4]))
		out = append(out, specialistFinding{
			file:     m[1],
			line:     lineNo,
			severity: strings.ToLower(m[3]),
			body:     desc,
			fix:      fixPart,
			roles:    []Role{role},
		})
	}
	return out
}

// parseRuleNames splits a comma-separated list of rule names.
func parseRuleNames(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// mergeField keeps the longer non-empty value.
func mergeField(dst *string, src string) {
	if src == "" {
		return
	}
	if *dst == "" || len(src) > len(*dst) {
		*dst = src
	}
}

// mergeRuleNames returns the union of two rule name slices.
func mergeRuleNames(dst, src []string) []string {
	for _, s := range src {
		found := false
		for _, d := range dst {
			if d == s {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}

// mergeExamples appends examples from src into dst, deduplicating by file+line.
func mergeExamples(dst, src []Example) []Example {
	for _, s := range src {
		found := false
		for _, d := range dst {
			if d.File == s.File && d.Line == s.Line {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, s)
		}
	}
	return dst
}

// normalizeBody returns a key for deduplication: lowercase, trimmed, trailing
// punctuation stripped.
func normalizeBody(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimRight(s, ".;,!?…")
	return s
}

// isNoIssues reports whether text is a specialist "all clear" response.
// Empty text is NOT treated as "no issues" — it indicates a silent failure.
func isNoIssues(text string) bool {
	return strings.EqualFold(text, "LGTM") ||
		strings.EqualFold(text, "NO_ISSUES")
}

// mergeSpecialistFindings collects findings from all specialist reviewers,
// deduplicates (same file + same description → merge roles and group lines),
// sorts by severity then file, and renders as a markdown plan. Returns "" if
// no strict lines were parsed (caller should fall back to raw concatenation).
func mergeSpecialistFindings(results []reviewResult, lang string) string {
	var all []specialistFinding
	var unparsed []specialistFinding
	for i := range results {
		if results[i].err != nil {
			continue
		}
		parsed := parseSpecialistFindings(results[i].text, results[i].role)
		if len(parsed) > 0 {
			all = append(all, parsed...)
			continue
		}
		txt := strings.TrimSpace(results[i].text)
		if txt == "" || isNoIssues(txt) {
			continue
		}
		for _, line := range strings.Split(results[i].text, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(line, "-*>0123456789.) "))
			if line == "" || isNoIssues(line) {
				continue
			}
			unparsed = append(unparsed, specialistFinding{
				file:     "(unstructured)",
				severity: "minor",
				body:     line,
				roles:    []Role{results[i].role},
			})
		}
	}
	all = append(all, unparsed...)
	if len(all) == 0 {
		return ""
	}

	// --- Dedup phase B: merge same file:line:body from different specialists ---
	type dedupKey struct {
		file string
		line int
		body string
	}
	byExact := make(map[dedupKey]*specialistFinding)
	var deduped []specialistFinding
	for i := range all {
		f := &all[i]
		key := dedupKey{file: f.file, line: f.line, body: normalizeBody(f.body)}
		if existing, ok := byExact[key]; ok {
			// Merge roles, keep highest severity.
			for _, r := range f.roles {
				found := false
				for _, er := range existing.roles {
					if er == r {
						found = true
						break
					}
				}
				if !found {
					existing.roles = append(existing.roles, r)
				}
			}
			if severityRank(f.severity) < severityRank(existing.severity) {
				existing.severity = f.severity
			}
			// Merge structured fields: keep longer/non-empty.
			mergeField(&existing.why, f.why)
			mergeField(&existing.fix, f.fix)
			mergeField(&existing.before, f.before)
			mergeField(&existing.after, f.after)
			existing.ruleNames = mergeRuleNames(existing.ruleNames, f.ruleNames)
			// Merge examples from duplicate findings.
			existing.examples = mergeExamples(existing.examples, f.examples)
		} else {
			clone := *f
			byExact[key] = &clone
			deduped = append(deduped, clone)
		}
	}
	// Update deduped entries from map (roles may have been extended).
	for i := range deduped {
		key := dedupKey{file: deduped[i].file, line: deduped[i].line, body: normalizeBody(deduped[i].body)}
		if updated, ok := byExact[key]; ok {
			deduped[i] = *updated
		}
	}

	// --- Dedup phase A: group same file:body across different lines ---
	type groupKey struct {
		file string
		sev  string
		body string
	}
	type lineGroup struct {
		finding specialistFinding
		lines   []int
	}
	byGroup := make(map[groupKey]*lineGroup)
	var groupOrder []groupKey
	for idx := range deduped {
		f := &deduped[idx]
		key := groupKey{file: f.file, sev: f.severity, body: normalizeBody(f.body)}
		if g, ok := byGroup[key]; ok {
			g.lines = append(g.lines, f.line)
			// Merge roles.
			for _, r := range f.roles {
				found := false
				for _, er := range g.finding.roles {
					if er == r {
						found = true
						break
					}
				}
				if !found {
					g.finding.roles = append(g.finding.roles, r)
				}
			}
			// Merge structured fields.
			mergeField(&g.finding.why, f.why)
			mergeField(&g.finding.fix, f.fix)
			mergeField(&g.finding.before, f.before)
			mergeField(&g.finding.after, f.after)
			g.finding.ruleNames = mergeRuleNames(g.finding.ruleNames, f.ruleNames)
			// Merge examples.
			g.finding.examples = mergeExamples(g.finding.examples, f.examples)
		} else {
			g := &lineGroup{finding: *f, lines: []int{f.line}}
			byGroup[key] = g
			groupOrder = append(groupOrder, key)
		}
	}

	// Build final grouped findings.
	grouped := make([]specialistFinding, 0, len(groupOrder))
	for _, key := range groupOrder {
		g := byGroup[key]
		f := g.finding
		f.line = g.lines[0] // first line for sorting
		// Store all lines comma-separated in a helper field embedded in body.
		if len(g.lines) > 1 {
			sort.Ints(g.lines)
			lineStrs := make([]string, len(g.lines))
			for i, l := range g.lines {
				lineStrs[i] = strconv.Itoa(l)
			}
			f.body = f.body + planLabel(lang, "lines") + strings.Join(lineStrs, ", ") + ")"
		}
		grouped = append(grouped, f)
	}

	// Sort: severity, then file, then first line.
	sort.SliceStable(grouped, func(i, j int) bool {
		if si, sj := severityRank(grouped[i].severity), severityRank(grouped[j].severity); si != sj {
			return si < sj
		}
		if grouped[i].file != grouped[j].file {
			return grouped[i].file < grouped[j].file
		}
		return grouped[i].line < grouped[j].line
	})

	var b strings.Builder

	// --- Section 1: Tasks ---
	b.WriteString(planLabel(lang, "tasks2"))
	for i := range grouped {
		f := &grouped[i]
		var roleStrs []string
		for _, r := range f.roles {
			roleStrs = append(roleStrs, string(r))
		}
		roles := strings.Join(roleStrs, ", ")

		fmt.Fprintf(&b, "### %d. %s:%d [%s]\n\n", i+1, f.file, f.line, f.severity)
		if f.body != "" {
			fmt.Fprintf(&b, "- **What:** %s\n", f.body)
		}
		if f.why != "" {
			fmt.Fprintf(&b, "- **Why:** %s\n", f.why)
		}
		if f.fix != "" {
			fmt.Fprintf(&b, "- **Fix:** %s\n", f.fix)
		}
		if f.before != "" {
			fmt.Fprintf(&b, "- **Before:** %s\n", f.before)
		}
		if f.after != "" {
			fmt.Fprintf(&b, "- **After:** %s\n", f.after)
		}
		if len(f.ruleNames) > 0 {
			fmt.Fprintf(&b, "- **Rules:** %s\n", strings.Join(f.ruleNames, ", "))
		}
		for _, ex := range f.examples {
			fmt.Fprintf(&b, "- **Example:** %s:%d — %s\n", ex.File, ex.Line, ex.Note)
		}
		fmt.Fprintf(&b, "- *Reviewers: %s*\n\n", roles)
	}

	// --- Section 2: Affected files ---
	affectedFiles := collectAffectedFiles(grouped)
	if len(affectedFiles) > 0 {
		b.WriteString(planLabel(lang, "affected2"))
		for _, af := range affectedFiles {
			fmt.Fprintf(&b, "- %s\n", af)
		}
		b.WriteByte('\n')
	}

	// --- Section 3: Verification ---
	b.WriteString(planLabel(lang, "verification2"))
	fmt.Fprintf(&b, "- [ ] %s (`make build`)\n", planLabel(lang, "build"))
	fmt.Fprintf(&b, "- [ ] %s (`make lint`)\n", planLabel(lang, "lint"))
	fmt.Fprintf(&b, "- [ ] %s (`make test-unit`)\n", planLabel(lang, "test"))

	return strings.TrimRight(b.String(), "\n")
}

// collectAffectedFiles extracts unique file paths from grouped findings,
// sorted alphabetically. Skips "(unstructured)" placeholder entries.
func collectAffectedFiles(findings []specialistFinding) []string {
	seen := make(map[string]struct{})
	for i := range findings {
		f := findings[i].file
		if f == "" || f == "(unstructured)" {
			continue
		}
		seen[f] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}
