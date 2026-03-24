package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/tools"
)

// Orchestrator coordinates sub-agents in a Plan → Execute → Review pipeline.
type Orchestrator struct {
	client      *ollama.Client
	model       string
	reg         *tools.Registry
	workDir     string
	contextSize int
	language    string
	ruleCatalog string
	onEvent     EventHandler
	confirmFn   ConfirmFunc
	planConfirm PlanConfirmFunc
	review      bool
	hasSnippets bool
	hasRAG      bool
	ragIndex    *rag.Index

	prefetchCode bool

	maxPlanIter int
	maxExecIter int
	maxRevIter  int

	cachedProjectFiles string
}

// OrchestratorConfig holds configuration for the orchestrator.
type OrchestratorConfig struct {
	EventHandler EventHandler
	ConfirmFunc  ConfirmFunc
	PlanConfirm  PlanConfirmFunc
	Language     string
	RuleCatalog  string
	Review       bool
	HasSnippets  bool
	HasRAG       bool
	RAGIndex     *rag.Index
	PrefetchCode bool
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(
	client *ollama.Client,
	model string,
	reg *tools.Registry,
	workDir string,
	contextSize int,
	cfg OrchestratorConfig,
) *Orchestrator {
	return &Orchestrator{
		client:       client,
		model:        model,
		reg:          reg,
		workDir:      workDir,
		contextSize:  contextSize,
		onEvent:      cfg.EventHandler,
		confirmFn:    cfg.ConfirmFunc,
		planConfirm:  cfg.PlanConfirm,
		language:     cfg.Language,
		ruleCatalog:  cfg.RuleCatalog,
		review:       cfg.Review,
		hasSnippets:  cfg.HasSnippets,
		hasRAG:       cfg.HasRAG,
		ragIndex:     cfg.RAGIndex,
		prefetchCode: cfg.PrefetchCode,
		maxPlanIter:  100,
		maxExecIter:  50,
		maxRevIter:   15,
	}
}

// SetEventHandler sets the event handler shared by all sub-agents.
func (o *Orchestrator) SetEventHandler(h EventHandler) { o.onEvent = h }

func (o *Orchestrator) emit(e Event) {
	if o.onEvent != nil {
		o.onEvent(e)
	}
}

// Run executes the full Plan → Execute → (Review) pipeline.
func (o *Orchestrator) Run(ctx context.Context, task string) error {
	// Phase 1: Planning
	o.emit(Event{Type: EventAgent, Message: "▸ Phase 1: Planning..."})
	o.emit(Event{Type: EventGroupStart, Message: fmt.Sprintf("Analyze(%s)", truncateTask(task, 40))})

	plan, err := o.runPlanner(ctx, task)

	o.emit(Event{Type: EventGroupEnd})

	if err != nil {
		return errors.WithMessage(err, "planner")
	}
	if plan == "" {
		return errors.New("planner produced empty plan")
	}

	// Show the plan to the user
	o.emit(Event{Type: EventAgent, Message: plan})

	// Confirm before execution (3-option dialog)
	autoAccept := false
	if o.planConfirm != nil {
		cr := o.planConfirm(plan)
		switch cr.Action {
		case PlanDeny:
			o.emit(Event{Type: EventAgent, Message: "Execution cancelled by user."})
			return nil
		case PlanAutoAccept:
			autoAccept = true
		case PlanManualApprove:
			// keep confirmFn as-is
		case PlanAugment:
			// Re-run planner with feedback
			o.emit(Event{Type: EventAgent, Message: "▸ Revising plan..."})
			o.emit(Event{Type: EventGroupStart, Message: "Revise(plan)"})

			revised, err := o.runPlannerRevision(ctx, plan, cr.Augment)

			o.emit(Event{Type: EventGroupEnd})

			if err != nil {
				return errors.WithMessage(err, "planner revision")
			}
			if revised == "" {
				return errors.New("planner revision produced empty plan")
			}
			plan = revised
			o.emit(Event{Type: EventAgent, Message: plan})

			// Ask again after revision
			cr2 := o.planConfirm(plan)
			switch cr2.Action {
			case PlanDeny:
				o.emit(Event{Type: EventAgent, Message: "Execution cancelled by user."})
				return nil
			case PlanAutoAccept:
				autoAccept = true
			case PlanManualApprove:
				// keep confirmFn
			case PlanAugment:
				o.emit(Event{Type: EventAgent, Message: "Execution cancelled (too many revisions)."})
				return nil
			}
		}
	}

	// Phase 2: Execution
	o.emit(Event{Type: EventModeChange, Message: "edit"})
	o.emit(Event{Type: EventAgent, Message: "▸ Phase 2: Executing plan..."})

	var confirmForExec ConfirmFunc
	if !autoAccept {
		confirmForExec = o.confirmFn
	}

	execStats, err := o.runExecutor(ctx, plan, confirmForExec)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}

	// Phase 3: Review (optional)
	if o.review {
		o.emit(Event{Type: EventAgent, Message: "▸ Phase 3: Reviewing changes..."})

		feedback, err := o.runReviewer(ctx, plan, execStats)
		if err != nil {
			return errors.WithMessage(err, "reviewer")
		}

		// If reviewer found issues, run one more executor pass.
		if feedback != "" {
			o.emit(Event{Type: EventAgent, Message: "▸ Phase 3b: Applying review feedback..."})
			if _, err := o.runExecutor(ctx, feedback, o.confirmFn); err != nil {
				return errors.WithMessage(err, "executor (review fix)")
			}
		}
	}

	// Restore plan mode in TUI after orchestrator completes.
	o.emit(Event{Type: EventModeChange, Message: "plan"})
	o.emit(Event{Type: EventDone, Message: "Orchestrator completed", Stats: &execStats})
	return nil
}

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

	// Validate plan quality: check for placeholder line numbers and vague language.
	if issues := validatePlanQuality(plan); len(issues) > 0 {
		o.emit(Event{Type: EventAgent, Message: "Plan quality issues: " + strings.Join(issues, "; ")})
	}

	// Validate that the plan references real files.
	if invalid := o.validatePlanPaths(plan); len(invalid) > 0 {
		o.emit(Event{Type: EventAgent, Message: fmt.Sprintf("Warning: plan references %d non-existent file(s): %s", len(invalid), strings.Join(invalid, ", "))})
	}

	return plan, nil
}

// runPlannerPrefetch reads all project files programmatically and injects them into the prompt.
func (o *Orchestrator) runPlannerPrefetch(ctx context.Context, task string) (string, error) {
	codeContext := o.collectProjectFiles()

	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	ragContext := o.ragPrefetch(ctx, task)
	userMsg := fmt.Sprintf("[Source code of the project — already read for you]\n%s\n[Task]\n%s", codeContext, task)
	if ragContext != "" {
		userMsg = ragContext + "\n" + userMsg
	}
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
		o.emit(Event{Type: EventAgent, Message: "Planner did not read any files. Retrying with reinforced prompt..."})
		reinforced := "IMPORTANT: You MUST call list_dir(\".\") first, then read_file on each .go file. Do NOT generate a plan without reading files.\n\n" + task
		plan, _, err = o.runPlannerOnce(ctx, reinforced)
		if err != nil {
			return "", err
		}
	}

	if toolCalls == 0 && plan != "" {
		o.emit(Event{Type: EventAgent, Message: "Warning: planner did not read source files — plan may contain hallucinations"})
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

	_ = filepath.WalkDir(o.workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(o.workDir, path)
		if strings.HasSuffix(rel, ".go") || rel == "go.mod" {
			files = append(files, rel)
		}
		return nil
	})

	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(o.workDir, rel))
		if err != nil {
			continue
		}
		o.emit(Event{Type: EventTool, Tool: "read_file", Message: rel, Success: true})

		lines := strings.Split(string(data), "\n")
		buf.WriteString(fmt.Sprintf("=== %s ===\n", rel))
		for i, line := range lines {
			fmt.Fprintf(&buf, "%d\t%s\n", i+1, line)
		}
		buf.WriteByte('\n')
	}

	return buf.String()
}

func (o *Orchestrator) runPlannerOnce(ctx context.Context, task string) (plan string, toolCalls int, err error) {
	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	enrichedTask := task
	if ragContext := o.ragPrefetch(ctx, task); ragContext != "" {
		enrichedTask = ragContext + "\n" + task
	}
	if err := ag.Send(ctx, enrichedTask); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", 0, err
	}

	return ag.LastPlan(), ag.Stats().ToolCalls, nil
}

// validatePlanPaths extracts .go file paths from the plan and checks they exist.
// Returns list of non-existent paths.
var goFilePathRe = regexp.MustCompile(`\b([\w./-]+\.go)(?::\d+)?`)

func (o *Orchestrator) validatePlanPaths(plan string) []string {
	matches := goFilePathRe.FindAllStringSubmatch(plan, -1)
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

	// Count vague phrases vs total plan lines.
	vaguePhrases := []string{
		"Проверить", "Убедиться", "проверить", "убедиться",
		"Check ", "Verify ", "Ensure ",
		"check if", "verify that", "ensure that",
	}
	lines := strings.Split(plan, "\n")
	var planLines, vagueLines int
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 3 && trimmed[0] >= '0' && trimmed[0] <= '9' {
			planLines++
			for _, phrase := range vaguePhrases {
				if strings.Contains(trimmed, phrase) {
					vagueLines++
					break
				}
			}
		}
	}
	if planLines > 0 && vagueLines*2 > planLines {
		issues = append(issues, fmt.Sprintf("%d of %d steps are vague suggestions instead of concrete findings", vagueLines, planLines))
	}

	return issues
}

func (o *Orchestrator) runPlannerRevision(ctx context.Context, plan, feedback string) (string, error) {
	ag := o.newAgent(RolePlanner, o.maxPlanIter)
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	task := fmt.Sprintf("Revise the following plan based on user feedback.\n\nOriginal plan:\n%s\n\nUser feedback:\n%s\n\nProvide an updated plan.", plan, feedback)
	if ragContext := o.ragPrefetch(ctx, feedback); ragContext != "" {
		task = ragContext + "\n" + task
	}
	if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	return ag.LastPlan(), nil
}

func (o *Orchestrator) runExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc) (SessionStats, error) {
	// Pre-read project files so executor doesn't waste iterations on read_file/list_dir.
	o.emit(Event{Type: EventGroupStart, Message: "Reading project files..."})
	codeContext := o.collectProjectFiles()
	o.emit(Event{Type: EventGroupEnd})

	ag := o.newAgent(RoleExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleExecutor, o.language, o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)
	ag.SetConfirmFunc(confirmFn)

	execTask := fmt.Sprintf("## Source Code (already read — do NOT call read_file or list_dir)\n%s\n## Approved Plan\n\n%s\n\n---\nImplement each step by calling edit_file/write_file. After all changes run go_build, go_lint, go_test.", codeContext, plan)
	if ragContext := o.ragPrefetch(ctx, plan); ragContext != "" {
		execTask = ragContext + "\n" + execTask
	}
	err := ag.Send(ctx, execTask)
	if err != nil && !errors.Is(err, ErrMaxIterations) {
		return ag.Stats(), err
	}

	return ag.Stats(), nil
}

func (o *Orchestrator) runReviewer(ctx context.Context, plan string, execStats SessionStats) (string, error) {
	ag := o.newAgent(RoleReviewer, o.maxRevIter)
	prompt := systemPromptForRole(RoleReviewer, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	reviewTask := fmt.Sprintf(
		"Review the changes made for this plan:\n\n%s\n\nStats: %d files added, %d modified, %d deleted, +%d -%d lines, %d tool calls.\n\nRead the changed files and check for issues. If everything looks good, respond with 'LGTM'. If there are issues, list them clearly.",
		plan,
		execStats.FilesAdded, execStats.FilesModified, execStats.FilesDeleted,
		execStats.LinesAdded, execStats.LinesRemoved, execStats.ToolCalls,
	)
	if ragContext := o.ragPrefetch(ctx, plan); ragContext != "" {
		reviewTask = ragContext + "\n" + reviewTask
	}

	if err := ag.Send(ctx, reviewTask); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	// Check if reviewer found issues (non-LGTM response).
	lastPlan := ag.LastPlan()
	if lastPlan == "" {
		return "", nil // LGTM or no actionable feedback
	}

	return lastPlan, nil
}

func (o *Orchestrator) newAgent(role Role, maxIter int) *Agent {
	ag := New(o.client, o.model, o.reg, maxIter, o.workDir, o.contextSize)
	ag.SetLanguage(o.language)
	ag.SetAutoCompact(true)
	ag.SetMaxWorkers(4)
	ag.SetHasSnippets(o.hasSnippets)
	ag.SetHasRAG(o.hasRAG)

	switch role {
	case RolePlanner:
		ag.SetMode(ModePlan)
		ag.SetThink(true)
	case RoleExecutor:
		ag.SetMode(ModeEdit)
		ag.SetThink(false)
	case RoleReviewer:
		ag.SetMode(ModePlan)
		ag.SetThink(true)
	}

	// Filter out EventDone from sub-agents — only the orchestrator emits the final Done.
	ag.SetEventHandler(func(e Event) {
		if e.Type == EventDone {
			return
		}
		o.emit(e)
	})
	ag.SetConfirmFunc(o.confirmFn)
	return ag
}

// ragPrefetch performs an automatic RAG search and returns formatted results
// to be injected into the user message. Returns "" if RAG is not configured.
func (o *Orchestrator) ragPrefetch(ctx context.Context, query string) string {
	if o.ragIndex == nil {
		return ""
	}
	results, err := o.ragIndex.Search(ctx, query, 5)
	if err != nil || len(results) == 0 {
		return ""
	}
	return formatRAGResults(results)
}

func truncateTask(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
