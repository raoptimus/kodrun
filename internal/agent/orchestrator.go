package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tools"
)

const (
	taskTruncateLen       = 40  // max chars for task in phase group messages
	maxToolWorkersDefault = 4   // default parallel tool workers for sub-agents
	orchestratorRAGTopK   = 5   // top-K results for orchestrator RAG prefetch
	perStepRAGTopK        = 3   // top-K results for per-step RAG search
	maxPlanIterations     = 100 // max LLM iterations for planning phase
	maxExecIterations     = 50  // max LLM iterations for execution phase
	hitRatePctMultiplier  = 100 // multiplier to convert hit rate ratio to percentage
	minStructuredSteps    = 4   // min steps for a plan to be considered structured
	ragRuleTopK           = 2   // top-K for broad rule-name RAG queries
	minRegexMatches       = 2   // min regex submatch groups / plan steps for validity
	minStepTextLen        = 10  // min chars for a plan step to be meaningful
	structurerMaxIter     = 3   // max iterations for structurer sub-agent
	extractorMaxIter      = 5   // max iterations for extractor sub-agent
	severityMinor         = 2   // severity rank for minor findings
	severityUnknown       = 3   // severity rank for unrecognized severity
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
	language             string
	ruleCatalog          string
	onEvent              EventHandler
	confirmFn            ConfirmFunc
	planConfirm          PlanConfirmFunc
	stepConfirmFn        StepConfirmFunc
	review               bool
	hasSnippets          bool
	hasRAG               bool
	ragIndex             tools.RAGSearcher
	godocIndexer         tools.GoDocIndexer
	langState            *projectlang.State
	rulesLoader          *rules.Loader
	ruleNames            []string

	prefetchCode bool

	maxPlanIter       int
	maxExecIter       int
	maxRevIter        int
	maxParallelTasks  int
	maxReplans        int
	specialistTimeout time.Duration
	autoCommit        bool
	verbose           bool

	cachedProjectFiles string

	// stepRAGBundles is populated once at the start of runPlanDAG and read
	// by runStep. Sharing the per-step RAG payload across the DAG run avoids
	// re-issuing identical embedding searches for every parallel sub-agent.
	// nil means "fall back to live perStepRAG()" (used outside DAG mode).
	stepRAGBundles map[int]string
}

// OrchestratorConfig holds configuration for the orchestrator.
type OrchestratorConfig struct {
	EventHandler  EventHandler
	ConfirmFunc   ConfirmFunc
	PlanConfirm   PlanConfirmFunc
	StepConfirmFn StepConfirmFunc
	Language      string
	RuleCatalog   string
	Review        bool
	HasSnippets   bool
	HasRAG        bool
	RAGIndex      tools.RAGSearcher
	GodocIndexer  tools.GoDocIndexer
	LangState     *projectlang.State
	RulesLoader   *rules.Loader
	PrefetchCode  bool

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

	// AutoCommit controls whether the executor may call git_commit.
	// When false, git_commit is disabled for all sub-agents.
	AutoCommit bool
	// Verbose enables per-iteration and per-specialist timing diagnostics.
	Verbose bool
	// MaxIterations is the agent loop limit, used for review specialists.
	MaxIterations int
	// SpecialistTimeout caps wall time for a single review specialist.
	// 0 means no per-specialist deadline.
	SpecialistTimeout time.Duration
}

// NewOrchestrator creates a new orchestrator.
func NewOrchestrator(
	client *ollama.Client,
	model string,
	reg *tools.Registry,
	workDir string,
	contextSize int,
	cfg *OrchestratorConfig,
) *Orchestrator {
	o := &Orchestrator{
		client:               client,
		model:                model,
		reg:                  reg,
		workDir:              workDir,
		contextSize:          contextSize,
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
		onEvent:              cfg.EventHandler,
		confirmFn:            cfg.ConfirmFunc,
		planConfirm:          cfg.PlanConfirm,
		stepConfirmFn:        cfg.StepConfirmFn,
		language:             cfg.Language,
		ruleCatalog:          cfg.RuleCatalog,
		review:               cfg.Review,
		hasSnippets:          cfg.HasSnippets,
		hasRAG:               cfg.HasRAG,
		ragIndex:             cfg.RAGIndex,
		godocIndexer:         cfg.GodocIndexer,
		langState:            cfg.LangState,
		rulesLoader:          cfg.RulesLoader,
		ruleNames:            collectRuleNames(cfg.RulesLoader),
		prefetchCode:         cfg.PrefetchCode,
		maxPlanIter:          maxPlanIterations,
		maxExecIter:          maxExecIterations,
		maxRevIter:           cfg.MaxIterations,
		specialistTimeout:    cfg.SpecialistTimeout,
		autoCommit:           cfg.AutoCommit,
		verbose:              cfg.Verbose,
	}
	if o.maxRevIter <= 0 {
		o.maxRevIter = maxExecIterations
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

func (o *Orchestrator) emit(e *Event) {
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
	tools.RegisterLanguageTools(o.reg, lang, o.workDir, o.godocIndexer)
	o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Project language detected: %s — language tools registered", lang)})
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
	rate := c.HitRate() * hitRatePctMultiplier
	o.emit(&Event{
		Type:        EventCacheStats,
		Message:     fmt.Sprintf("Tool cache: %d hits / %d misses (%.0f%% hit rate)", hits, misses, rate),
		CacheHits:   hits,
		CacheMisses: misses,
	})
}

// emitPhase fires an EventPhase so the TUI can render a phase indicator. Name
// should be one of: planning, awaiting_approval, executing, reviewing.
func (o *Orchestrator) emitPhase(name string) {
	o.emit(&Event{Type: EventPhase, Message: name})
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
	o.emit(&Event{Type: EventAgent, Message: "▸ Phase 1: Planning..."})
	o.emit(&Event{Type: EventGroupStart, Message: fmt.Sprintf("Analyze(%s)", truncateTask(task, taskTruncateLen))})

	plan, err := o.runPlanner(ctx, task)

	o.emit(&Event{Type: EventGroupEnd})

	if err != nil {
		return errors.WithMessage(err, "planner")
	}
	if plan == "" {
		return errors.New("planner produced empty plan")
	}

	return o.confirmAndExecute(ctx, plan, "Orchestrator completed")
}

// confirmAndExecute is the shared second half of the orchestrator pipeline.
// It shows the plan, runs the confirm dialog (with optional revision), then
// executes and optionally reviews. Used by both Run() and RunCodeReview().
func (o *Orchestrator) confirmAndExecute(ctx context.Context, plan, doneMsg string) error {
	// Show the plan to the user
	o.emit(&Event{Type: EventAgent, Message: plan})

	// Confirm before execution (3-option dialog)
	autoAccept := false
	if o.planConfirm != nil {
		cr := o.planConfirm(plan)
		switch cr.Action {
		case PlanDeny:
			o.emit(&Event{Type: EventAgent, Message: "Execution cancelled by user."})
			o.emit(&Event{Type: EventModeChange, Message: "plan"})
			o.emit(&Event{Type: EventDone, Message: doneMsg})
			return nil
		case PlanAutoAccept:
			autoAccept = true
		case PlanManualApprove:
			// keep confirmFn as-is
		case PlanAugment:
			// Re-run planner with feedback
			o.emit(&Event{Type: EventAgent, Message: "▸ Revising plan..."})
			o.emit(&Event{Type: EventGroupStart, Message: "Revise(plan)"})

			revised, err := o.runPlannerRevision(ctx, plan, cr.Augment)

			o.emit(&Event{Type: EventGroupEnd})

			if err != nil {
				return errors.WithMessage(err, "planner revision")
			}
			if revised == "" {
				return errors.New("planner revision produced empty plan")
			}
			plan = revised
			o.emit(&Event{Type: EventAgent, Message: plan})

			// Ask again after revision
			cr2 := o.planConfirm(plan)
			switch cr2.Action {
			case PlanDeny:
				o.emit(&Event{Type: EventAgent, Message: "Execution cancelled by user."})
				o.emit(&Event{Type: EventModeChange, Message: "plan"})
				o.emit(&Event{Type: EventDone, Message: doneMsg})
				return nil
			case PlanAutoAccept:
				autoAccept = true
			case PlanManualApprove:
				// keep confirmFn
			case PlanAugment:
				o.emit(&Event{Type: EventAgent, Message: "Execution cancelled (too many revisions)."})
				o.emit(&Event{Type: EventModeChange, Message: "plan"})
				o.emit(&Event{Type: EventDone, Message: doneMsg})
				return nil
			}
		}
	}

	// Execution phase.
	// If context was cancelled (e.g. ESC during planning) but user approved
	// the plan anyway, use a detached context so the executor can proceed.
	execCtx := ctx
	if ctx.Err() != nil {
		execCtx = context.WithoutCancel(ctx)
	}

	o.emitPhase("executing")
	o.emit(&Event{Type: EventModeChange, Message: "edit"})
	o.emit(&Event{Type: EventAgent, Message: "▸ Executing plan..."})

	var confirmForExec ConfirmFunc
	if !autoAccept {
		confirmForExec = o.confirmFn
	}

	execStats, err := o.runExecutor(execCtx, plan, confirmForExec)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}

	// Review phase (optional, planner path only).
	if o.review {
		o.emitPhase("reviewing")
		o.emit(&Event{Type: EventAgent, Message: "▸ Reviewing changes..."})

		feedback, err := o.runReviewer(ctx, plan, &execStats)
		if err != nil {
			return errors.WithMessage(err, "reviewer")
		}

		// If reviewer found issues, run one more executor pass.
		if feedback != "" {
			o.emit(&Event{Type: EventAgent, Message: "▸ Applying review feedback..."})
			if _, err := o.runExecutor(ctx, feedback, o.confirmFn); err != nil {
				return errors.WithMessage(err, "executor (review fix)")
			}
		}
	}

	// Restore plan mode in TUI after orchestrator completes.
	o.emit(&Event{Type: EventModeChange, Message: "plan"})
	o.emit(&Event{Type: EventDone, Message: doneMsg, Stats: &execStats})
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
	prompt := systemPromptForRole(RolePlanner, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
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
	if err := ag.Send(ctx, enrichedTask); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", 0, err
	}

	return ag.LastPlan(), ag.Stats().ToolCalls, nil
}

// buildFileHint extracts .go file paths mentioned in the task and returns
// an instruction telling the planner to read those files first. Returns ""
// if no paths are found.
func buildFileHint(task string) string {
	matches := goFilePathRe.FindAllStringSubmatch(task, -1)
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
	o.emit(&Event{Type: EventModeChange, Message: "edit"})
	stats, err := o.runExecutor(ctx, plan, confirmFn)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}
	o.emit(&Event{Type: EventModeChange, Message: "plan"})
	o.emit(&Event{Type: EventDone, Message: "Executor completed", Stats: &stats})
	return nil
}

func (o *Orchestrator) runExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc) (SessionStats, error) {
	// Always attempt to structure the plan into a JSON DAG. maxParallelTasks
	// controls only the number of concurrent workers, not whether we use the
	// DAG path. This ensures every step gets its own clean sub-agent context
	// (and examples, RAG, whitelist) regardless of parallelism settings.
	structured := o.structurePlan(ctx, plan)
	if structured != nil && len(structured.Steps) > 0 {
		parallel := o.maxParallelTasks
		if parallel < 1 {
			parallel = 1
		}
		o.emit(&Event{
			Type:    EventAgent,
			Message: fmt.Sprintf("Executing plan as DAG: %d steps, max %d parallel", len(structured.Steps), parallel),
		})
		return o.runPlanDAG(ctx, structured, parallel, confirmFn)
	}

	// Fallback: structurer unavailable or returned empty plan. Use single
	// monolithic executor (no per-step examples in this path).
	o.emit(&Event{Type: EventAgent, Message: "Structurer unavailable; falling back to sequential executor"})

	// Pre-read project files so executor doesn't waste iterations on read_file/list_dir.
	o.emit(&Event{Type: EventGroupStart, Message: "Reading project files..."})
	codeContext := o.collectProjectFiles()
	o.emit(&Event{Type: EventGroupEnd})

	ag := o.newAgent(RoleExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleExecutor, o.language, o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)
	ag.SetConfirmFunc(confirmFn)

	// Derive the read-path whitelist from the plan and lock the executor to
	// it. The executor may only read files mentioned in the plan; any attempt
	// to read elsewhere is refused with a REPLAN hint.
	mdPlan := PlanFromMarkdown(plan)
	whitelist := mdPlan.AffectedFiles()
	if len(whitelist) > 0 {
		ag.SetAllowedReadPaths(whitelist)
		o.emit(&Event{
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

	// Detect REPLAN sentinel from the executor.
	if last := ag.LastPlan(); strings.Contains(last, "REPLAN:") {
		o.emit(&Event{
			Type:    EventReplan,
			Message: extractReplanReason(last),
		})
	}

	return ag.Stats(), nil
}

func (o *Orchestrator) runReviewer(ctx context.Context, plan string, execStats *SessionStats) (string, error) {
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
	ag.SetMaxToolWorkers(maxToolWorkersDefault)
	ag.SetHasSnippets(o.hasSnippets)
	ag.SetHasRAG(o.hasRAG)
	ag.SetTaskLabel(taskLabelForRole(role))
	ag.SetVerbose(o.verbose)

	switch role {
	case RolePlanner:
		ag.SetMode(ModePlan)
		ag.SetThink(true)
	case RoleExecutor:
		ag.SetMode(ModeEdit)
		ag.SetThink(false)
	case RoleReviewer,
		RoleReviewerRules,
		RoleReviewerIdiomatic,
		RoleReviewerBestPractice,
		RoleReviewerSecurity,
		RoleReviewerStructure,
		RoleReviewerArchitecture:
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
	case RoleStructurer:
		ag.SetMode(ModePlan)
		ag.SetThink(false)
		// Structurer only converts text to JSON — no tools needed.
		ag.SetToolsDisabled(true)
		ag.SetTemperature(o.extractorTemperature)
		if o.extractorFormat != "" {
			ag.SetFormat(o.extractorFormat)
		}
	}

	// Disable git_commit when auto_commit is off.
	if !o.autoCommit {
		ag.DisableTools("git_commit")
	}

	// Filter out EventDone from sub-agents — only the orchestrator emits the final Done.
	ag.SetEventHandler(func(e *Event) {
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
	if !o.hasRAG || o.ragIndex == nil {
		return ""
	}
	results, err := o.ragIndex.Search(ctx, query, orchestratorRAGTopK)
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
	if !o.hasRAG || o.ragIndex == nil {
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
	if results, err := o.ragIndex.Search(ctx, task, orchestratorRAGTopK); err == nil {
		add(results)
	}

	// 2. Rule names from git-changed files.
	changed := gitChangedGoFiles(ctx, o.workDir)
	types := entityTypesFromPaths(changed, o.ruleNames)

	// 3. Rule names from .go paths mentioned in the task text.
	for _, m := range goFilePathRe.FindAllStringSubmatch(task, -1) {
		if len(m) >= minRegexMatches {
			if t := detectEntityTypeFromPath(m[1], o.ruleNames); t != "" && !slices.Contains(types, t) {
				types = append(types, t)
			}
		}
	}

	for _, t := range types {
		if results, err := o.ragIndex.Search(ctx, t, orchestratorRAGTopK); err == nil {
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
		if results, err := o.ragIndex.Search(ctx, name, ragRuleTopK); err == nil {
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
	if !o.hasRAG || o.ragIndex == nil {
		return ""
	}

	steps := splitPlanSteps(plan)
	if len(steps) == 0 {
		return o.ragPrefetch(ctx, plan)
	}

	seen := make(map[string]bool)
	var allResults []rag.SearchResult

	for _, step := range steps {
		results, err := o.ragIndex.Search(ctx, step, perStepRAGTopK)
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
var stepSplitRe = regexp.MustCompile(`(?m)^\d+[.\)]\s`)

func splitPlanSteps(plan string) []string {
	// Try splitting by numbered steps first.
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

	// Fallback: split by empty lines (paragraphs).
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
		prompt := systemPromptForRole(RoleStructurer, o.language, o.ruleCatalog, nil)
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

// RunCodeReview runs specialised reviewer sub-agents in parallel, concatenates
// their reports and normalises them via the extractor into a single plan that
// is emitted to the user. The task argument is the fully-prepared review
// prompt (already containing any RAG prefetch and the diff / project context)
// and is sent to every specialist as-is.
//
// Parallelism is bounded by o.maxParallelTasks. When maxParallelTasks <= 1
// the reviewers run sequentially. If len(roles) == 1 the fan-out degenerates
// to a single specialist (used by /arch-review).
func (o *Orchestrator) RunCodeReview(ctx context.Context, task string, roles []Role) error {
	if len(roles) == 0 {
		return errors.New("RunCodeReview: no roles provided")
	}

	o.ensureLanguageDetected()

	reviewStart := time.Now()
	_ = reviewStart // used in verbose timing below

	cache := tools.NewResultCache()
	o.reg.WithCache(cache)
	defer func() {
		o.reg.WithCache(nil)
		o.emitCacheStats(cache)
	}()

	o.emitPhase("reviewing")

	maxPar := o.maxParallelTasks
	if maxPar < 1 {
		maxPar = 1
	}
	if maxPar > len(roles) {
		maxPar = len(roles)
	}

	const reviewGroupID = "code-review"
	o.emit(&Event{Type: EventGroupStart, GroupID: reviewGroupID, Message: "CodeReview(...)"})

	// Circuit breaker: cancel remaining specialists on first connection error.
	reviewCtx, reviewCancel := context.WithCancel(ctx)
	defer reviewCancel()

	results := make([]reviewResult, len(roles))
	sem := make(chan struct{}, maxPar)
	var wg sync.WaitGroup

	for i, role := range roles {
		if reviewCtx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, role Role) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[i] = reviewResult{role: role, err: errors.Errorf("panic: %v\n%s", r, debug.Stack())}
				}
			}()

			// Per-specialist timeout prevents a single slow specialist
			// from blocking the entire review when parallelism is limited.
			specCtx := reviewCtx
			if o.specialistTimeout > 0 {
				var specCancel context.CancelFunc
				specCtx, specCancel = context.WithTimeout(reviewCtx, o.specialistTimeout)
				defer specCancel()
			}

			o.emit(&Event{
				Type:    EventGroupTitleUpdate,
				GroupID: reviewGroupID,
				Message: fmt.Sprintf("CodeReview(%s)", reviewerShortLabel(role)),
			})

			specStart := time.Now()
			text, agStats, err := o.runReviewerSpecialistInGroup(specCtx, role, task, reviewGroupID)
			results[i] = reviewResult{role: role, text: text, err: err, duration: time.Since(specStart), stats: agStats}

			if err != nil && isConnectionError(err) {
				o.emit(&Event{Type: EventError, Message: "Ollama unreachable — aborting remaining reviewers."})
				reviewCancel()
			}
		}(i, role)
	}
	wg.Wait()
	reviewWall := time.Since(reviewStart)

	o.emit(&Event{Type: EventGroupEnd, GroupID: reviewGroupID})

	// Emit per-specialist timing when verbose is enabled.
	if o.verbose {
		for i := range results {
			r := &results[i]
			status := "ok"
			if r.err != nil {
				status = "err"
			}
			o.emit(&Event{
				Type: EventAgent,
				Message: fmt.Sprintf("[perf] specialist=%s wall=%s llm=%s tools=%s iter=%d ctx_peak=%d%% status=%s",
					reviewerShortLabel(r.role), r.duration.Truncate(time.Millisecond),
					r.stats.TotalLLMTime.Truncate(time.Millisecond), r.stats.TotalToolTime.Truncate(time.Millisecond),
					r.stats.Iterations, r.stats.PeakContextPct, status),
			})
		}
		o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("[perf] review_total=%s parallel=%d", reviewWall.Truncate(time.Millisecond), maxPar)})
	}

	// If the context was cancelled but some reviewers succeeded, continue
	// processing their results instead of discarding everything.
	ctxCancelled := ctx.Err() != nil
	hasSuccess := false
	for i := range results {
		if results[i].err == nil {
			hasSuccess = true
			break
		}
	}
	if ctxCancelled && !hasSuccess {
		return ctx.Err()
	}

	sections := make([]string, 0, len(results))
	var failedCount int
	for i := range results {
		r := &results[i]
		if r.err != nil {
			failedCount++
			continue
		}
		text := strings.TrimSpace(r.text)
		if isNoIssues(text) {
			continue
		}
		sections = append(sections, fmt.Sprintf("## [%s]\n\n%s", strings.ToUpper(string(r.role)), text))
	}

	if len(sections) == 0 {
		if failedCount > 0 {
			o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
				"⚠ %d reviewer(s) failed, but no issues found by the rest — treating as LGTM.", failedCount)})
		} else {
			o.emit(&Event{Type: EventAgent, Message: "LGTM — no issues found."})
		}
		o.emit(&Event{Type: EventModeChange, Message: "plan"})
		o.emit(&Event{Type: EventDone, Message: "Code review completed"})
		return nil
	}

	// Merge specialist findings deterministically in Go. Each specialist is
	// prompted to emit strict `path:LINE — SEVERITY — description` lines, so
	// we parse them directly instead of sending the concatenated text to
	// another LLM (extractor), which tends to drop items under aggressive
	// dedup.
	o.emit(&Event{Type: EventAgent, Message: "▸ Merging reviewer findings..."})
	finalPlan := mergeSpecialistFindings(results)
	if finalPlan == "" {
		// Fallback: no strict lines parsed — surface this explicitly so the
		// operator knows why the output looks unstructured, then show raw
		// concatenated sections so nothing is silently lost.
		o.emit(&Event{Type: EventAgent, Message: "⚠ Specialists did not emit strict finding lines — showing raw concatenated reports."})
		finalPlan = strings.Join(sections, "\n\n---\n\n")
	}

	// Pass merged findings through extractor to group by file.
	o.emit(&Event{Type: EventAgent, Message: "▸ Extracting structured plan..."})
	extracted, err := o.runExtractor(ctx, finalPlan)
	if err != nil {
		o.emit(&Event{Type: EventError, Message: fmt.Sprintf("Extractor failed, using raw merge: %v", err)})
	} else {
		finalPlan = RenderExtractorOutput(extracted)
	}

	return o.confirmAndExecute(ctx, finalPlan, "Code review completed")
}

// reviewResult holds the outcome of a single specialist reviewer sub-agent.
type reviewResult struct {
	role     Role
	text     string
	err      error
	duration time.Duration
	stats    SessionStats
}

// specialistFinding is a parsed line from a specialist's output in the
// strict `path:LINE — SEVERITY — description` format.
type specialistFinding struct {
	file     string
	line     int
	severity string
	body     string    // description part after the second separator
	roles    []Role    // specialists that reported this finding (for dedup)
	examples []Example // EXAMPLE: continuation lines parsed from reviewer output
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

// exampleLineRe captures EXAMPLE: continuation lines from specialist output.
// Format: EXAMPLE: path/to/file.go:LINE — reason
var exampleLineRe = regexp.MustCompile(
	`(?i)^\s*EXAMPLE:\s*([\w./\\-]+?):(\d+)\s*[—–\-]\s*(.+)$`,
)

// parseSpecialistFindings extracts strict finding lines from a specialist's
// raw output. Non-matching lines (headers, prose) are ignored. EXAMPLE:
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
		m := specialistFindingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lineNo, err := strconv.Atoi(m[2])
		if err != nil {
			continue
		}
		out = append(out, specialistFinding{
			file:     m[1],
			line:     lineNo,
			severity: strings.ToLower(m[3]),
			body:     strings.TrimSpace(m[4]),
			roles:    []Role{role},
		})
	}
	return out
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
func isNoIssues(text string) bool {
	return text == "" ||
		strings.EqualFold(text, "LGTM") ||
		strings.EqualFold(text, "NO_ISSUES")
}

// mergeSpecialistFindings collects findings from all specialist reviewers,
// deduplicates (same file + same description → merge roles and group lines),
// sorts by severity then file, and renders as a markdown plan. Returns "" if
// no strict lines were parsed (caller should fall back to raw concatenation).
func mergeSpecialistFindings(results []reviewResult) string {
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
		if isNoIssues(txt) {
			continue
		}
		for _, line := range strings.Split(results[i].text, "\n") {
			line = strings.TrimSpace(strings.TrimLeft(line, "-*>0123456789.) "))
			if isNoIssues(line) {
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
	for _, f := range deduped {
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
			// Merge examples.
			g.finding.examples = mergeExamples(g.finding.examples, f.examples)
		} else {
			g := &lineGroup{finding: f, lines: []int{f.line}}
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
			f.body = f.body + " (строки: " + strings.Join(lineStrs, ", ") + ")"
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
	b.WriteString("## Plan\n\n")
	for i, f := range grouped {
		var roleStrs []string
		for _, r := range f.roles {
			roleStrs = append(roleStrs, string(r))
		}
		roles := strings.Join(roleStrs, ", ")
		fmt.Fprintf(&b, "%d. %s:%d — %s — %s [%s]\n", i+1, f.file, f.line, f.severity, f.body, roles)
		for _, ex := range f.examples {
			fmt.Fprintf(&b, "   EXAMPLE: %s:%d — %s\n", ex.File, ex.Line, ex.Note)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// runReviewerSpecialistInGroup runs a single specialist reviewer sub-agent
// whose events are routed into the given TUI group. It returns the agent's
// final plan text (LastPlan). Errors other than ErrMaxIterations are
// returned; an empty result is not an error (treated as LGTM upstream).
func (o *Orchestrator) runReviewerSpecialistInGroup(ctx context.Context, role Role, task, groupID string) (string, SessionStats, error) {
	ag := o.newAgent(role, o.maxRevIter)
	// Specialists should not emit a redundant start-label — the group
	// header already says "Reviewing: security" etc. Their activity in the
	// UI is expressed purely through tool calls landing in the group.
	ag.SetTaskLabel("")
	ag.SetGroupID(groupID)
	prompt := systemPromptForRole(role, o.language, o.ruleCatalog, ag.reg.NamesFiltered(readOnlyTools), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)
	if err := ag.Send(ctx, task); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", ag.Stats(), err
	}
	return ag.LastPlan(), ag.Stats(), nil
}

func truncateTask(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isConnectionError returns true when the error indicates that the LLM backend
// (ollama) is unreachable — connection refused, DNS failure, or dial timeout.
// Used by RunCodeReview as a circuit breaker trigger.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp")
}
