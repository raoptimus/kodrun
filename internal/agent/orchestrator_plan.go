/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// runPlanner runs the planning phase: invokes the planner sub-agent,
// optionally normalises review output via the extractor, and surfaces
// quality / path validation warnings.
func (o *Orchestrator) runPlanner(ctx context.Context, task string) (string, error) {
	var plan string
	var err error

	if o.prefetchCode {
		plan, err = o.runPlannerPrefetch(ctx, task)
	} else {
		plan, err = o.runPlannerWithTools(ctx, task)
	}
	if err != nil {
		return "", err
	}

	// The extractor normalizes review/analysis output into structured JSON
	// findings (severity, what, fix). For implementation tasks the planner
	// output is already an actionable plan; the structurer (called later by
	// runExecutor) handles JSON conversion and file grouping.
	if o.review {
		plan, err = o.extractPlan(ctx, plan)
		if err != nil {
			return "", err
		}
	}

	// Log quality issues as warnings (informational only, not a gate).
	if issues := validatePlanQuality(plan); len(issues) > 0 {
		o.emit(&Event{Type: EventAgent, Message: "Plan quality notes: " + strings.Join(issues, "; ")})
	}

	// Validate that the plan references real files.
	if invalid := o.validatePlanPaths(plan); len(invalid) > 0 {
		o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Warning: plan references %d non-existent file(s): %s", len(invalid), strings.Join(invalid, ", "))})
	}

	return plan, nil
}

// extractPlan runs the extractor agent to normalize raw analysis into a structured plan.
// Always called — this is the second phase of the two-phase planner→extractor architecture.
func (o *Orchestrator) extractPlan(ctx context.Context, rawPlan string) (string, error) {
	o.emit(&Event{Type: EventGroupStart, Message: "Extract(plan)"})
	defer o.emit(&Event{Type: EventGroupEnd})

	extracted, err := o.runExtractor(ctx, rawPlan)
	if err != nil {
		return rawPlan, errors.WithMessage(err, "extractor")
	}
	if extracted == "" {
		return rawPlan, nil
	}
	// Local models often return JSON even when the prompt asks for markdown
	// (e.g. when format=json is forced at the API level). Re-render to a
	// readable form so the user does not see raw JSON.
	return RenderExtractorOutput(extracted, o.language), nil
}

// runPlannerPrefetch reads all project files programmatically and injects them into the prompt.
func (o *Orchestrator) runPlannerPrefetch(ctx context.Context, task string) (string, error) {
	codeContext := o.collectProjectFiles()

	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.progLang(), o.ruleCatalog, ag.reg.NamesFiltered(ag.readOnlyTools()), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	ragContext := o.ragPrefetch(ctx, task)
	userMsg := fmt.Sprintf("[Source code of the project — already read for you]\n%s\n[Task]\n%s", codeContext, task)
	if ragContext != "" {
		userMsg = ragContext + "\n" + userMsg
	}
	o.lastPlannerTask = userMsg
	if err := ag.Send(ctx, userMsg); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	plan := ag.LastPlan()
	if plan == "" {
		return "", errors.New("planner produced empty plan")
	}
	return plan, nil
}

// runPlannerWithTools runs the planner relying on the model to call read-only tools.
func (o *Orchestrator) runPlannerWithTools(ctx context.Context, task string) (string, error) {
	plan, toolCalls, err := o.runPlannerOnce(ctx, task)
	if err != nil {
		return "", err
	}

	// If planner didn't use any tools, retry once with a stronger hint.
	if toolCalls == 0 && plan == "" {
		o.emit(&Event{Type: EventAgent, Message: "Planner did not read any files. Retrying with reinforced prompt..."})
		reinforced := "IMPORTANT: You MUST call list_dir(\".\") first, then read_file on each .go file. Do NOT generate a plan without reading files.\n\n" + task
		plan, _, err = o.runPlannerOnce(ctx, reinforced)
		if err != nil {
			return "", err
		}
	}

	if toolCalls == 0 && plan != "" {
		o.emit(&Event{Type: EventAgent, Message: "Warning: planner did not read source files — plan may contain hallucinations"})
	}

	return plan, nil
}

// collectProjectFiles reads all .go files and go.mod from the project directory.
// Results are cached for the duration of the orchestrator run.
func (o *Orchestrator) collectProjectFiles() string {
	if o.cachedProjectFiles != "" {
		return o.cachedProjectFiles
	}
	result := o.doCollectProjectFiles()
	o.cachedProjectFiles = result
	return result
}

func (o *Orchestrator) doCollectProjectFiles() string {
	var buf strings.Builder
	var files []string

	if err := filepath.WalkDir(o.workDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return filepath.SkipDir
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(o.workDir, path)
		if relErr != nil {
			return filepath.SkipDir
		}
		if strings.HasSuffix(rel, ".go") || rel == "go.mod" {
			files = append(files, rel)
		}
		return nil
	}); err != nil {
		return ""
	}

	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(o.workDir, rel))
		if err != nil {
			continue
		}
		o.emit(&Event{Type: EventTool, Tool: "read_file", Message: rel, Success: true})

		lines := strings.Split(string(data), "\n")
		fmt.Fprintf(&buf, "=== %s ===\n", rel)
		for i, line := range lines {
			fmt.Fprintf(&buf, "%d\t%s\n", i+1, line)
		}
		buf.WriteByte('\n')
	}

	return buf.String()
}

func (o *Orchestrator) runPlannerOnce(ctx context.Context, task string) (plan string, toolCalls int, err error) {
	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.progLang(), o.ruleCatalog, ag.reg.NamesFiltered(ag.readOnlyTools()), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	enrichedTask := task
	if ragContext := o.ragPrefetchForReview(ctx, task); ragContext != "" {
		enrichedTask = ragContext + "\n" + task
	}
	// When the task mentions specific file paths, tell the planner to read
	// them directly instead of scanning the entire project tree.
	if hint := buildFileHint(enrichedTask); hint != "" {
		enrichedTask = hint + "\n" + enrichedTask
	}
	o.lastPlannerTask = enrichedTask
	if err := ag.Send(ctx, enrichedTask); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", 0, err
	}

	return ag.LastPlan(), ag.Stats().ToolCalls, nil
}

// buildFileHint extracts source file paths mentioned in the task and returns
// an instruction telling the planner to read those files first. Returns ""
// if no paths are found.
func buildFileHint(task string) string {
	matches := sourceFilePathRe.FindAllStringSubmatch(task, -1)
	if len(matches) == 0 {
		return ""
	}
	seen := make(map[string]bool)
	files := make([]string, 0, len(matches))
	for _, m := range matches {
		p := m[1]
		if seen[p] {
			continue
		}
		seen[p] = true
		files = append(files, p)
	}
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"MANDATORY: The task references specific file(s): %s\n"+
			"Your FIRST tool call(s) MUST be read_file for these files. Do NOT call list_dir or find_files before reading them.\n"+
			"After reading these files, only read additional files that are directly imported or referenced by the code you found.",
		strings.Join(files, ", "),
	)
}

// validatePlanPaths extracts source file paths from the plan and checks they exist.
// Returns list of non-existent paths.
var sourceFilePathRe = regexp.MustCompile(`\b([\w./-]+\.(?:go|py|ts|tsx|js|jsx))(?::\d+)?`)

func (o *Orchestrator) validatePlanPaths(plan string) []string {
	matches := sourceFilePathRe.FindAllStringSubmatch(plan, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var invalid []string
	for _, m := range matches {
		p := m[1]
		if seen[p] {
			continue
		}
		seen[p] = true

		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(o.workDir, abs)
		}
		if _, err := os.Stat(abs); err != nil {
			invalid = append(invalid, p)
		}
	}
	return invalid
}

// validatePlanQuality checks if the plan has concrete findings vs vague suggestions.
func validatePlanQuality(plan string) []string {
	var issues []string

	// Check for placeholder line numbers like ":line" or ":line)"
	if strings.Contains(plan, ":line") {
		issues = append(issues, "contains placeholder ':line' instead of real line numbers")
	}

	// Vague phrases that indicate observations rather than concrete action items.
	// These phrases suggest "something should change" without specifying HOW.
	vaguePhrases := []string{
		// Russian vague patterns
		"Проверить", "Убедиться", "проверить", "убедиться",
		"должна быть", "должно быть", "должен быть",
		"может быть заменен", "может быть замене",
		"можно добавить", "можно улучшить", "можно заменить",
		"следует добавить", "следует улучшить", "следует заменить",
		"необходимо добавить", "необходимо улучшить",
		"более строг", "более корректн", "более безопасн",
		"не нужна", "не нужен", "не нужно",
		"должно содержать", "должна содержать",
		"рекомендуется", "желательно",
		"стоит рассмотреть", "стоит добавить",
		// English vague patterns
		"Check ", "Verify ", "Ensure ",
		"check if", "verify that", "ensure that",
		"should be", "could be", "might be",
		"consider ", "recommended", "advisable",
		"more strict", "more robust", "more secure",
		"is not needed", "is unnecessary",
		"should contain", "should include",
	}

	// Count vague lines among ALL actionable lines (numbered + bulleted).
	lines := strings.Split(plan, "\n")
	var actionLines, vagueLines int
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < minStructuredSteps {
			continue
		}
		// Detect actionable lines: numbered (1. / 1)), bulleted (- ), or bold (**).
		isAction := false
		if trimmed[0] >= '0' && trimmed[0] <= '9' {
			isAction = true
		} else if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			isAction = true
		}
		if !isAction {
			continue
		}
		actionLines++
		for _, phrase := range vaguePhrases {
			if strings.Contains(trimmed, phrase) {
				vagueLines++
				break
			}
		}
	}
	if actionLines > 0 && vagueLines*2 > actionLines {
		issues = append(issues, fmt.Sprintf("%d of %d steps are vague suggestions instead of concrete action items", vagueLines, actionLines))
	}

	return issues
}

func (o *Orchestrator) runPlannerRevision(ctx context.Context, plan, feedback string) (string, error) {
	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.progLang(), o.ruleCatalog, ag.reg.NamesFiltered(ag.readOnlyTools()), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	var task string
	if o.lastPlannerTask != "" {
		task = fmt.Sprintf(
			"Revise the following plan based on user feedback.\n\n"+
				"Original task:\n%s\n\n"+
				"Current plan:\n%s\n\n"+
				"User feedback:\n%s\n\n"+
				"Provide an updated plan that addresses the feedback while staying true to the original task.",
			o.lastPlannerTask, plan, feedback,
		)
	} else {
		task = fmt.Sprintf("Revise the following plan based on user feedback.\n\nCurrent plan:\n%s\n\nUser feedback:\n%s\n\nProvide an updated plan.", plan, feedback)
	}
	if ragContext := o.ragPrefetch(ctx, feedback); ragContext != "" {
		task = ragContext + "\n" + task
	}
	if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	revised := ag.LastPlan()
	if revised == "" {
		return "", nil
	}

	// Two-phase: normalize revised plan through extractor.
	return o.extractPlan(ctx, revised)
}

// structurePlan converts a markdown plan into a strict JSON Plan{Steps[]} via
// the structurer sub-agent. Returns nil on parse failure so callers can
// gracefully fall back to PlanFromMarkdown.
//
// The structurer always runs with format=json and temperature=0 (same profile
// as the extractor) to maximise the chance of valid JSON from local models.
func (o *Orchestrator) structurePlan(ctx context.Context, markdownPlan string) *Plan {
	if markdownPlan == "" {
		return nil
	}

	task := "Convert the following plan into the JSON schema described in your instructions:\n\n" + markdownPlan

	// Retry once on transient errors (e.g. Ollama context eviction after
	// heavy specialist work).
	const maxAttempts = 2
	for attempt := range maxAttempts {
		if ctx.Err() != nil {
			return nil
		}

		ag := o.newAgent(RoleStructurer, structurerMaxIter)
		prompt := systemPromptForRole(RoleStructurer, o.language, o.progLang(), o.ruleCatalog, nil)
		ag.InitWithPrompt(prompt)

		if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
			o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("structurer error (attempt %d/%d): %s", attempt+1, maxAttempts, err.Error())})
			if attempt < maxAttempts-1 {
				time.Sleep(time.Second)
			}
			continue
		}

		raw := strings.TrimSpace(ag.LastAssistantMessage())
		if raw == "" {
			continue
		}

		plan, err := parseStructuredPlan(raw)
		if err != nil {
			o.emit(&Event{Type: EventAgent, Message: "structurer JSON parse failed: " + err.Error()})
			continue
		}
		plan.Raw = markdownPlan
		return plan
	}
	return nil
}

// runExtractor takes raw analysis/review text and converts it to a structured plan
// using a separate agent with its own context. This solves the problem of models
// producing "thinking" text instead of actionable plans.
func (o *Orchestrator) runExtractor(ctx context.Context, rawAnalysis string) (string, error) {
	ag := o.newAgent(RoleExtractor, extractorMaxIter)
	prompt := systemPromptForRole(RoleExtractor, o.language, o.progLang(), o.ruleCatalog, nil)
	ag.InitWithPrompt(prompt)

	task := fmt.Sprintf("Extract a clear, actionable plan from the following analysis:\n\n%s", rawAnalysis)
	if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	plan := ag.LastPlan()
	if plan == "" {
		// Extractor may produce LGTM or structured text without plan markers.
		return strings.TrimSpace(rawAnalysis), nil
	}
	return plan, nil
}
