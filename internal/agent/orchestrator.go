package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tools"
)

// Orchestrator coordinates sub-agents in a Plan → Execute → Review pipeline.
type Orchestrator struct {
	client      *ollama.Client
	model       string
	reg         *tools.Registry
	workDir     string
	contextSize int
	// Optional dedicated client/model for the executor role.
	// When nil/empty, the orchestrator falls back to client/model/contextSize.
	execClient      *ollama.Client
	execModel       string
	execContextSize int
	// Optional dedicated extractor wiring (Block 5: planner/extractor split).
	extractorClient      *ollama.Client
	extractorModel       string
	extractorContextSize int
	extractorTemperature float64
	extractorFormat      string
	language        string
	ruleCatalog     string
	onEvent         EventHandler
	confirmFn       ConfirmFunc
	planConfirm     PlanConfirmFunc
	review          bool
	hasSnippets     bool
	hasRAG          bool
	ragIndex        tools.RAGSearcher
	langState       *projectlang.State
	rulesLoader     *rules.Loader
	ruleNames       []string

	prefetchCode bool

	maxPlanIter      int
	maxExecIter      int
	maxRevIter       int
	maxParallelTasks int
	maxReplans       int

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
	RAGIndex     tools.RAGSearcher
	LangState    *projectlang.State
	RulesLoader  *rules.Loader
	PrefetchCode bool

	// Optional dedicated executor wiring. When ExecutorClient is nil
	// or ExecutorModel is empty, the orchestrator reuses the main client/model
	// for the executor role.
	ExecutorClient      *ollama.Client
	ExecutorModel       string
	ExecutorContextSize int

	// Optional dedicated extractor wiring. The extractor is always invoked
	// with deterministic settings (Temperature=0, Format="json") to coerce
	// structured output. When ExtractorClient is nil or ExtractorModel is
	// empty, the orchestrator reuses the main client/model.
	ExtractorClient      *ollama.Client
	ExtractorModel       string
	ExtractorContextSize int
	ExtractorTemperature float64
	ExtractorFormat      string

	// Parallel DAG execution (Block 3). When >1, the executor splits the
	// approved plan into a DAG and runs independent steps concurrently with
	// per-file locking. Default 1 keeps current sequential behaviour.
	MaxParallelTasks int
	// Maximum number of REPLAN cycles allowed inside a single Run. Default 2.
	MaxReplans int
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
	o := &Orchestrator{
		client:          client,
		model:           model,
		reg:             reg,
		workDir:         workDir,
		contextSize:     contextSize,
		execClient:           cfg.ExecutorClient,
		execModel:            cfg.ExecutorModel,
		execContextSize:      cfg.ExecutorContextSize,
		extractorClient:      cfg.ExtractorClient,
		extractorModel:       cfg.ExtractorModel,
		extractorContextSize: cfg.ExtractorContextSize,
		extractorTemperature: cfg.ExtractorTemperature,
		extractorFormat:      cfg.ExtractorFormat,
		maxParallelTasks:     cfg.MaxParallelTasks,
		maxReplans:           cfg.MaxReplans,
		onEvent:         cfg.EventHandler,
		confirmFn:       cfg.ConfirmFunc,
		planConfirm:     cfg.PlanConfirm,
		language:        cfg.Language,
		ruleCatalog:     cfg.RuleCatalog,
		review:          cfg.Review,
		hasSnippets:     cfg.HasSnippets,
		hasRAG:          cfg.HasRAG,
		ragIndex:        cfg.RAGIndex,
		langState:       cfg.LangState,
		rulesLoader:     cfg.RulesLoader,
		ruleNames:       collectRuleNames(cfg.RulesLoader),
		prefetchCode:    cfg.PrefetchCode,
		maxPlanIter:     100,
		maxExecIter:     50,
		maxRevIter:      15,
	}
	if o.execClient == nil {
		o.execClient = client
	}
	if o.execModel == "" {
		o.execModel = model
	}
	if o.execContextSize <= 0 {
		o.execContextSize = contextSize
	}
	return o
}

// SetEventHandler sets the event handler shared by all sub-agents.
func (o *Orchestrator) SetEventHandler(h EventHandler) { o.onEvent = h }

func (o *Orchestrator) emit(e Event) {
	if o.onEvent != nil {
		o.onEvent(e)
	}
}

// Run executes the full Plan → Execute → (Review) pipeline.
// ensureLanguageDetected re-runs project language detection if it is still
// unknown and lazily registers the matching language-specific tools.
func (o *Orchestrator) ensureLanguageDetected() {
	if o.langState == nil {
		return
	}
	lang, changed := o.langState.EnsureDetected()
	if !changed || lang == projectlang.LangUnknown {
		return
	}
	tools.RegisterLanguageTools(o.reg, lang, o.workDir)
	o.emit(Event{Type: EventAgent, Message: fmt.Sprintf("Project language detected: %s — language tools registered", lang)})
}

// emitCacheStats logs the final cache hit/miss summary if the cache saw any
// activity. Used at the end of an orchestrator Run for visibility.
func (o *Orchestrator) emitCacheStats(c *tools.ResultCache) {
	if c == nil {
		return
	}
	hits := c.Hits()
	misses := c.Misses()
	if hits == 0 && misses == 0 {
		return
	}
	rate := c.HitRate() * 100
	o.emit(Event{
		Type:        EventCacheStats,
		Message:     fmt.Sprintf("Tool cache: %d hits / %d misses (%.0f%% hit rate)", hits, misses, rate),
		CacheHits:   hits,
		CacheMisses: misses,
	})
}

// emitPhase fires an EventPhase so the TUI can render a phase indicator. Name
// should be one of: planning, awaiting_approval, executing, reviewing.
func (o *Orchestrator) emitPhase(name string) {
	o.emit(Event{Type: EventPhase, Message: name})
}

// extractReplanReason pulls the reason text from a REPLAN sentinel emitted by
// the executor (line of the form "REPLAN: <reason>").
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

func (o *Orchestrator) Run(ctx context.Context, task string) error {
	o.ensureLanguageDetected()

	// Attach a per-run result cache so that read-only tool calls (read_file in
	// particular) are served from memory across phases and sub-agents. The
	// cache is invalidated automatically by write tools via the registry.
	cache := tools.NewResultCache()
	o.reg.WithCache(cache)
	defer func() {
		o.reg.WithCache(nil)
		o.emitCacheStats(cache)
	}()

	// Phase 1: Planning
	o.emitPhase("planning")
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
	o.emitPhase("executing")
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
		o.emitPhase("reviewing")
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

	// Two-phase architecture: always run extractor to normalize planner output
	// into a structured plan. This eliminates heuristics — the extractor handles
	// thinking text, vague suggestions, scattered findings, and already-good plans.
	plan, err = o.extractPlan(ctx, plan)
	if err != nil {
		return "", err
	}

	// Log quality issues as warnings (informational only, not a gate).
	if issues := validatePlanQuality(plan); len(issues) > 0 {
		o.emit(Event{Type: EventAgent, Message: "Plan quality notes: " + strings.Join(issues, "; ")})
	}

	// Validate that the plan references real files.
	if invalid := o.validatePlanPaths(plan); len(invalid) > 0 {
		o.emit(Event{Type: EventAgent, Message: fmt.Sprintf("Warning: plan references %d non-existent file(s): %s", len(invalid), strings.Join(invalid, ", "))})
	}

	return plan, nil
}

// extractPlan runs the extractor agent to normalize raw analysis into a structured plan.
// Always called — this is the second phase of the two-phase planner→extractor architecture.
func (o *Orchestrator) extractPlan(ctx context.Context, rawPlan string) (string, error) {
	o.emit(Event{Type: EventGroupStart, Message: "Extract(plan)"})
	defer o.emit(Event{Type: EventGroupEnd})

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
	return RenderExtractorOutput(extracted), nil
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
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	enrichedTask := task
	if ragContext := o.ragPrefetchForReview(ctx, task); ragContext != "" {
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
		if len(trimmed) < 4 {
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
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	task := fmt.Sprintf("Revise the following plan based on user feedback.\n\nOriginal plan:\n%s\n\nUser feedback:\n%s\n\nProvide an updated plan.", plan, feedback)
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

// RunExecutor executes an already-approved plan without running the planner phase.
// It is intended for callers that obtained a plan outside of the orchestrator
// (e.g. via the standalone agent + classifier path) and want to delegate just
// the execution to the orchestrator's executor sub-agent.
func (o *Orchestrator) RunExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc) error {
	o.emit(Event{Type: EventModeChange, Message: "edit"})
	stats, err := o.runExecutor(ctx, plan, confirmFn)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}
	o.emit(Event{Type: EventModeChange, Message: "plan"})
	o.emit(Event{Type: EventDone, Message: "Executor completed", Stats: &stats})
	return nil
}

func (o *Orchestrator) runExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc) (SessionStats, error) {
	// Block 3: parallel DAG path. When MaxParallelTasks > 1, attempt to
	// structure the plan into a JSON DAG and run independent steps in
	// parallel. On any failure (structurer unavailable, JSON parse error,
	// empty plan) we fall through to the sequential single-agent path below.
	if o.maxParallelTasks > 1 {
		structured := o.structurePlan(ctx, plan)
		if structured != nil && len(structured.Steps) > 0 {
			o.emit(Event{
				Type:    EventAgent,
				Message: fmt.Sprintf("Executing plan as DAG: %d steps, max %d parallel", len(structured.Steps), o.maxParallelTasks),
			})
			return o.runPlanDAG(ctx, structured, o.maxParallelTasks, confirmFn)
		}
		o.emit(Event{Type: EventAgent, Message: "Structurer unavailable; falling back to sequential executor"})
	}

	// Pre-read project files so executor doesn't waste iterations on read_file/list_dir.
	o.emit(Event{Type: EventGroupStart, Message: "Reading project files..."})
	codeContext := o.collectProjectFiles()
	o.emit(Event{Type: EventGroupEnd})

	ag := o.newAgent(RoleExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleExecutor, o.language, o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)
	ag.SetConfirmFunc(confirmFn)

	// Block 1: derive the read-path whitelist from the plan and lock the
	// executor to it. The executor may only read files mentioned in the plan;
	// any attempt to read elsewhere is refused with a REPLAN hint.
	structured := PlanFromMarkdown(plan)
	whitelist := structured.AffectedFiles()
	if len(whitelist) > 0 {
		ag.SetAllowedReadPaths(whitelist)
		o.emit(Event{
			Type:    EventAgent,
			Message: fmt.Sprintf("Executor read whitelist: %d file(s)", len(whitelist)),
		})
	}

	execTask := fmt.Sprintf("## Source Code (already read — do NOT call read_file or list_dir)\n%s\n## Approved Plan\n\n%s\n\n---\nImplement each step by calling edit_file/write_file. After all changes run go_build, go_lint, go_test.", codeContext, plan)
	if ragContext := o.ragPrefetchBySteps(ctx, plan); ragContext != "" {
		execTask = ragContext + "\n" + execTask
	}
	err := ag.Send(ctx, execTask)
	if err != nil && !errors.Is(err, ErrMaxIterations) {
		return ag.Stats(), err
	}

	// Block 1: detect REPLAN sentinel from the executor and surface it to the
	// orchestrator's caller via an EventAgent message. Full replan loop is
	// implemented in a follow-up; for now we surface the signal so the user
	// sees what happened instead of silently looping.
	if last := ag.LastPlan(); strings.Contains(last, "REPLAN:") {
		o.emit(Event{
			Type:    EventReplan,
			Message: extractReplanReason(last),
		})
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
	if ragContext := o.ragPrefetchForReview(ctx, plan); ragContext != "" {
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

	// Two-phase: normalize review findings through extractor.
	return o.extractPlan(ctx, lastPlan)
}

func (o *Orchestrator) newAgent(role Role, maxIter int) *Agent {
	client, model, ctxSize := o.client, o.model, o.contextSize
	if role == RoleExecutor && o.execClient != nil && o.execModel != "" {
		client, model, ctxSize = o.execClient, o.execModel, o.execContextSize
	}
	if role == RoleExtractor && o.extractorClient != nil && o.extractorModel != "" {
		client, model, ctxSize = o.extractorClient, o.extractorModel, o.extractorContextSize
	}
	ag := New(client, model, o.reg, maxIter, o.workDir, ctxSize)
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
	case RoleExtractor:
		ag.SetMode(ModePlan)
		ag.SetThink(false) // No thinking — just structured extraction
		// Extractor must produce raw JSON only. Hide all tools so local models
		// (qwen3-coder, etc.) cannot hallucinate tool_calls instead of output.
		ag.SetToolsDisabled(true)
		// Force deterministic generation for the extractor. The format hint
		// pushes the local model toward valid JSON.
		ag.SetTemperature(o.extractorTemperature)
		if o.extractorFormat != "" {
			ag.SetFormat(o.extractorFormat)
		}
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

// collectRuleNames extracts all rule names (file basename without .md)
// from the given rules.Loader. Returns nil if the loader is nil or empty.
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

// ragPrefetchForReview combines four RAG signals into a single deduplicated block:
//  1. Semantic search by the raw task text.
//  2. Searches by rule names detected from git-changed .go files in workDir
//     (deterministic mapping file path → .kodrun/rules/<name>.md).
//  3. Searches by rule names detected from .go file paths mentioned directly
//     in the task text.
//  4. Global fan-out: a small search per known rule name from rules.Loader —
//     covers horizontal rules (bootstrap, go-effective, styleguide, etc.) that
//     don't bind to a file type.
//
// Returns "" if RAG is not configured or nothing was found.
func (o *Orchestrator) ragPrefetchForReview(ctx context.Context, task string) string {
	if o.ragIndex == nil {
		return ""
	}

	seen := make(map[string]bool)
	var allResults []rag.SearchResult
	add := func(results []rag.SearchResult) {
		for _, r := range results {
			key := fmt.Sprintf("%s:%d", r.Chunk.FilePath, r.Chunk.StartLine)
			if seen[key] {
				continue
			}
			seen[key] = true
			allResults = append(allResults, r)
		}
	}

	// 1. Semantic search by task text.
	if results, err := o.ragIndex.Search(ctx, task, 5); err == nil {
		add(results)
	}

	// 2. Rule names from git-changed files.
	changed := gitChangedGoFiles(ctx, o.workDir)
	types := entityTypesFromPaths(changed, o.ruleNames)

	// 3. Rule names from .go paths mentioned in the task text.
	for _, m := range goFilePathRe.FindAllStringSubmatch(task, -1) {
		if len(m) >= 2 {
			if t := detectEntityTypeFromPath(m[1], o.ruleNames); t != "" && !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}

	for _, t := range types {
		if results, err := o.ragIndex.Search(ctx, t, 5); err == nil {
			add(results)
		}
	}

	// 4. Global fan-out: every known rule name as a small separate query.
	//    This catches horizontal rules (bootstrap, go-effective, styleguide,
	//    project structure) that don't map to a specific file type.
	for _, name := range o.ruleNames {
		if slices.Contains(types, name) {
			continue // already covered above
		}
		if results, err := o.ragIndex.Search(ctx, name, 2); err == nil {
			add(results)
		}
	}

	if len(allResults) == 0 {
		return ""
	}
	return formatRAGResults(allResults)
}

// ragPrefetchBySteps splits a plan into individual steps and performs
// a separate RAG search for each step. This produces more focused queries
// that match specific conventions (e.g., "create validator" finds validator snippets,
// "configure bootstrap" finds bootstrap patterns).
func (o *Orchestrator) ragPrefetchBySteps(ctx context.Context, plan string) string {
	if o.ragIndex == nil {
		return ""
	}

	steps := splitPlanSteps(plan)
	if len(steps) == 0 {
		return o.ragPrefetch(ctx, plan)
	}

	seen := make(map[string]bool)
	var allResults []rag.SearchResult

	for _, step := range steps {
		results, err := o.ragIndex.Search(ctx, step, 3)
		if err != nil {
			continue
		}
		for _, r := range results {
			key := fmt.Sprintf("%s:%d", r.Chunk.FilePath, r.Chunk.StartLine)
			if !seen[key] {
				seen[key] = true
				allResults = append(allResults, r)
			}
		}
	}

	if len(allResults) == 0 {
		return ""
	}
	return formatRAGResults(allResults)
}

// splitPlanSteps splits a structured plan into individual steps.
// Plans are typically numbered lists (1. ... 2. ...) or paragraph-separated blocks.
var stepSplitRe = regexp.MustCompile(`(?m)^\d+[\.\)]\s`)

func splitPlanSteps(plan string) []string {
	// Try splitting by numbered steps first.
	indices := stepSplitRe.FindAllStringIndex(plan, -1)
	if len(indices) >= 2 {
		steps := make([]string, 0, len(indices))
		for i, idx := range indices {
			var end int
			if i+1 < len(indices) {
				end = indices[i+1][0]
			} else {
				end = len(plan)
			}
			step := strings.TrimSpace(plan[idx[0]:end])
			if len(step) >= 10 {
				steps = append(steps, step)
			}
		}
		if len(steps) >= 2 {
			return steps
		}
	}

	// Fallback: split by empty lines (paragraphs).
	paragraphs := strings.Split(plan, "\n\n")
	var steps []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if len(p) >= 10 {
			steps = append(steps, p)
		}
	}
	return steps
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

	// Build a structurer agent. Reuses the extractor wiring (deterministic
	// profile, dedicated client/model when configured).
	ag := o.newAgent(RoleStructurer, 3)
	prompt := systemPromptForRole(RoleStructurer, o.language, o.ruleCatalog, nil)
	ag.InitWithPrompt(prompt)

	task := "Convert the following plan into the JSON schema described in your instructions:\n\n" + markdownPlan
	if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
		o.emit(Event{Type: EventAgent, Message: "structurer error: " + err.Error()})
		return nil
	}

	raw := strings.TrimSpace(ag.LastAssistantMessage())
	if raw == "" {
		return nil
	}

	plan, err := parseStructuredPlan(raw)
	if err != nil {
		o.emit(Event{Type: EventAgent, Message: "structurer JSON parse failed: " + err.Error()})
		return nil
	}
	plan.Raw = markdownPlan
	return plan
}

// runExtractor takes raw analysis/review text and converts it to a structured plan
// using a separate agent with its own context. This solves the problem of models
// producing "thinking" text instead of actionable plans.
func (o *Orchestrator) runExtractor(ctx context.Context, rawAnalysis string) (string, error) {
	ag := o.newAgent(RoleExtractor, 5)
	prompt := systemPromptForRole(RoleExtractor, o.language, o.ruleCatalog, nil)
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

func truncateTask(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
