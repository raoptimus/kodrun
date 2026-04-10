package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const codeFenceLen = 3 // length of "```" code fence marker

// Plan is the structured representation of an executable plan produced by the
// extractor role. It is the contract between the planner/extractor pair and
// the executor: each Step is a self-contained unit of work the executor
// applies, and Files lists the only paths the executor is allowed to read for
// that step.
//
// The structured form is the long-term direction. Today the orchestrator still
// passes the markdown plan to runExecutor, but Plan.Files is already used to
// build the read-path whitelist that prevents the executor from drifting into
// open-ended exploration.
type Plan struct {
	Context string `json:"context,omitempty"`
	Steps   []Step `json:"steps,omitempty"`
	// Raw is the original markdown text the planner produced. It is preserved
	// so the executor still receives a human-readable plan even when the
	// extractor only produced a partial JSON skeleton.
	Raw string `json:"-"`
}

// Example points to existing project code that demonstrates the correct
// pattern for a step. The actual code is loaded lazily from disk at execution
// time (see snippet.go) so the JSON plan stays compact and snippets are
// always fresh even when earlier steps modify the referenced file.
type Example struct {
	File string `json:"file"`
	Line int    `json:"line,omitempty"`
	Note string `json:"note,omitempty"`
}

// Step is one actionable unit inside a Plan.
type Step struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Files     []string  `json:"files,omitempty"`
	Action    string    `json:"action,omitempty"`
	Rationale string    `json:"rationale,omitempty"`
	RuleNames []string  `json:"rule_names,omitempty"`
	DependsOn []int     `json:"depends_on,omitempty"`
	Examples  []Example `json:"examples,omitempty"`
}

// AffectedFiles returns the union of all Files referenced by every step,
// deduplicated and sorted. This is the whitelist of paths the executor may
// read while applying the plan.
func (p *Plan) AffectedFiles() []string {
	if p == nil {
		return nil
	}
	seen := make(map[string]struct{})
	for i := range p.Steps {
		for _, f := range p.Steps[i].Files {
			if f == "" {
				continue
			}
			seen[f] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// parseStructuredPlan extracts a Plan from raw structurer output. The
// structurer is instructed to emit a single JSON object, but local models
// occasionally wrap it in markdown fences or add a leading prose sentence.
// This function tolerates both forms.
func parseStructuredPlan(raw string) (*Plan, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("empty input")
	}
	// Strip markdown code fences if present.
	if strings.HasPrefix(raw, "```") {
		if end := strings.LastIndex(raw, "```"); end > codeFenceLen {
			raw = raw[strings.Index(raw, "\n")+1 : end]
		}
	}
	// Find the outermost JSON object.
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < 0 || end <= start {
		return nil, fmt.Errorf("no JSON object found")
	}
	jsonText := raw[start : end+1]

	var plan Plan
	if err := json.Unmarshal([]byte(jsonText), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("plan has no steps")
	}
	// Assign sequential IDs if the model omitted them.
	for i := range plan.Steps {
		if plan.Steps[i].ID == 0 {
			plan.Steps[i].ID = i + 1
		}
	}
	return &plan, nil
}

// planPathRe matches file paths in markdown plans. It catches typical
// extensions used in this project (.go, .md, .yaml, .json, .py, .ts, .js)
// optionally suffixed with :line.
var planPathRe = regexp.MustCompile(`\b([\w./-]+\.(?:go|md|yaml|yml|json|py|ts|tsx|js|jsx|sh|sql|toml))\b`)

// RenderExtractorOutput converts the extractor's raw response into a
// human-readable markdown plan. Local models often ignore the markdown
// instruction and return JSON anyway (especially when format=json is forced
// at the API level). To keep the user-facing output clean, this function
// detects JSON shaped like the extractor schema and re-renders it as
// markdown. If the input is not JSON it is returned unchanged.
//
// Recognised JSON shapes (in priority order):
//
//  1. {"context": "...", "steps": [{"id":1,"title":"...","files":[...],"rationale":"..."}, ...]}
//     — the canonical structurer schema.
//  2. {"context": "...", "plan": [...]} where each item is a string or an
//     object with title/description/text — the shape qwen3-coder tends to
//     emit when forced into JSON.
//  3. Any object with a "context" field — render the context and dump
//     remaining fields as a fallback.
func RenderExtractorOutput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return raw
	}
	// Strip markdown code fences if present.
	if strings.HasPrefix(trimmed, "```") {
		if end := strings.LastIndex(trimmed, "```"); end > codeFenceLen {
			if nl := strings.Index(trimmed, "\n"); nl >= 0 && nl < end {
				trimmed = strings.TrimSpace(trimmed[nl+1 : end])
			}
		}
	}
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end < 0 || end <= start {
		return raw
	}
	jsonText := trimmed[start : end+1]

	var generic map[string]any
	if err := json.Unmarshal([]byte(jsonText), &generic); err != nil {
		return raw
	}

	var b strings.Builder
	if ctx, ok := generic["context"].(string); ok && strings.TrimSpace(ctx) != "" {
		b.WriteString("## Context\n")
		b.WriteString(strings.TrimSpace(ctx))
		b.WriteString("\n\n")
	}

	wrote := false
	// Shape 1: canonical "steps".
	if stepsRaw, ok := generic["steps"].([]any); ok && len(stepsRaw) > 0 {
		b.WriteString("## Plan\n")
		for i, s := range stepsRaw {
			renderStep(&b, i+1, s)
		}
		wrote = true
	}
	// Shape 2: free-form "plan".
	if !wrote {
		if planRaw, ok := generic["plan"].([]any); ok && len(planRaw) > 0 {
			b.WriteString("## Plan\n")
			for i, item := range planRaw {
				renderStep(&b, i+1, item)
			}
			wrote = true
		}
	}
	if !wrote {
		// No recognisable plan body — bail out and show the original text so
		// at least nothing is lost.
		return raw
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// renderStep renders a single plan item as a markdown bullet. The item can be
// a string (already formatted) or an object with title/description fields.
func renderStep(b *strings.Builder, idx int, item any) {
	switch v := item.(type) {
	case string:
		line := strings.TrimSpace(v)
		// If the string already starts with "<n>." keep it as-is to avoid
		// double-numbering.
		if hasLeadingNumber(line) {
			b.WriteString(line)
		} else {
			fmt.Fprintf(b, "%d. %s", idx, line)
		}
		b.WriteByte('\n')
	case map[string]any:
		title := firstString(v, "title", "task", "name", "description", "text")
		fmt.Fprintf(b, "%d. %s", idx, title)
		if files, ok := v["files"].([]any); ok && len(files) > 0 {
			b.WriteString(" — files: ")
			parts := make([]string, 0, len(files))
			for _, f := range files {
				if s, ok := f.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			b.WriteString(strings.Join(parts, ", "))
		}
		if rationale := firstString(v, "rationale", "why", "reason"); rationale != "" {
			b.WriteString("\n   ")
			b.WriteString(rationale)
		}
		b.WriteByte('\n')
	default:
		fmt.Fprintf(b, "%d. %v\n", idx, v)
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasLeadingNumber(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')')
}

// PlanFromMarkdown is a best-effort fallback that extracts a flat Plan from
// the markdown produced by the planner when the extractor cannot return
// structured JSON. Each unique file mentioned in the plan becomes a single
// Step whose Files list contains that one path.
//
// The resulting Plan has empty Title/Action/Rationale on each Step but is
// sufficient to drive the executor whitelist (see AffectedFiles).
func PlanFromMarkdown(text string) *Plan {
	if text == "" {
		return &Plan{}
	}
	matches := planPathRe.FindAllStringSubmatch(text, -1)
	seen := make(map[string]struct{})
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		p := strings.TrimSpace(m[1])
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		files = append(files, p)
	}

	plan := &Plan{Raw: text}
	for i, f := range files {
		plan.Steps = append(plan.Steps, Step{
			ID:    i + 1,
			Title: f,
			Files: []string{f},
		})
	}
	return plan
}
