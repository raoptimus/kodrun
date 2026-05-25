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
	"slices"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/llm"
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

	perfStatusOK  = "ok"
	perfStatusErr = "err"
)

// roleProfile bundles every per-role knob the orchestrator needs to spin up
// a sub-agent: which LLM client/model/context to use plus optional sampling
// hints (used by the extractor / structurer roles).
//
// Roles other than RolePlanner that have nil profiles inherit RolePlanner's
// wiring at lookup time — that's the "fall back to main" rule.
type roleProfile struct {
	Client      llm.Client
	Model       string
	ContextSize int
	Temperature float64
	Format      string
}

// Orchestrator coordinates sub-agents in a Plan → Execute → Review pipeline.
type Orchestrator struct {
	reg     *tools.Registry
	workDir string
	// roles maps a Role to its profile. RolePlanner is always set (it doubles
	// as the default profile). Other roles may be nil — see profileFor.
	roles         map[Role]*roleProfile
	language      string
	ruleCatalog   string
	onEvent       EventHandler
	confirmFn     ConfirmFunc
	planConfirm   PlanConfirmFunc
	stepConfirmFn StepConfirmFunc
	review        bool
	hasSnippets   bool
	hasRAG        bool
	ragIndex      tools.RAGSearcher
	godocIndexer  tools.GoDocIndexer
	langState     *projectlang.State
	rulesLoader   *rules.Loader
	ruleNames     []string

	prefetchCode bool

	maxPlanIter       int
	maxExecIter       int
	maxRevIter        int
	maxParallelTasks  int
	maxReplans        int
	specialistTimeout time.Duration
	autoCommit        bool
	think             bool
	verbose           bool

	cachedProjectFiles string
	lastPlannerTask    string

	// stepRAGBundles is populated once at the start of runPlanDAG and read
	// by runStep. Sharing the per-step RAG payload across the DAG run avoids
	// re-issuing identical embedding searches for every parallel sub-agent.
	// nil means "fall back to live perStepRAG()" (used outside DAG mode).
	stepRAGBundles map[int]string
}

// profileFor returns the role profile or RolePlanner's profile as fallback.
// The extractor profile inherits the planner's client/model/context when its
// own values are zero — sampling hints (Temperature/Format) always come from
// the role-specific profile so deterministic-extraction tuning is preserved.
func (o *Orchestrator) profileFor(role Role) *roleProfile {
	main := o.roles[RolePlanner]
	p := o.roles[role]
	if p == nil {
		return main
	}
	out := *p
	if out.Client == nil {
		out.Client = main.Client
	}
	if out.Model == "" {
		out.Model = main.Model
	}
	if out.ContextSize <= 0 {
		out.ContextSize = main.ContextSize
	}
	return &out
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
	ExecutorClient      llm.Client
	ExecutorModel       string
	ExecutorContextSize int

	// Optional dedicated extractor wiring. The extractor is always invoked
	// with deterministic settings (Temperature=0, Format="json") to coerce
	// structured output. When ExtractorClient is nil or ExtractorModel is
	// empty, the orchestrator reuses the main client/model.
	ExtractorClient      llm.Client
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
	// Think enables thinking mode for planners and reviewers.
	// When false, planners and reviewers skip the thinking phase,
	// which speeds up inference on slower models.
	Think bool
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
	client llm.Client,
	model string,
	reg *tools.Registry,
	workDir string,
	contextSize int,
	cfg *OrchestratorConfig,
) *Orchestrator {
	o := &Orchestrator{
		reg:     reg,
		workDir: workDir,
		roles: map[Role]*roleProfile{
			RolePlanner: {
				Client:      client,
				Model:       model,
				ContextSize: contextSize,
			},
			RoleExecutor: {
				Client:      cfg.ExecutorClient,
				Model:       cfg.ExecutorModel,
				ContextSize: cfg.ExecutorContextSize,
			},
			RoleExtractor: {
				Client:      cfg.ExtractorClient,
				Model:       cfg.ExtractorModel,
				ContextSize: cfg.ExtractorContextSize,
				Temperature: cfg.ExtractorTemperature,
				Format:      cfg.ExtractorFormat,
			},
		},
		maxParallelTasks:  cfg.MaxParallelTasks,
		maxReplans:        cfg.MaxReplans,
		onEvent:           cfg.EventHandler,
		confirmFn:         cfg.ConfirmFunc,
		planConfirm:       cfg.PlanConfirm,
		stepConfirmFn:     cfg.StepConfirmFn,
		language:          cfg.Language,
		ruleCatalog:       cfg.RuleCatalog,
		review:            cfg.Review,
		hasSnippets:       cfg.HasSnippets,
		hasRAG:            cfg.HasRAG,
		ragIndex:          cfg.RAGIndex,
		godocIndexer:      cfg.GodocIndexer,
		langState:         cfg.LangState,
		rulesLoader:       cfg.RulesLoader,
		ruleNames:         collectRuleNames(cfg.RulesLoader),
		prefetchCode:      cfg.PrefetchCode,
		maxPlanIter:       maxPlanIterations,
		maxExecIter:       maxExecIterations,
		maxRevIter:        cfg.MaxIterations,
		specialistTimeout: cfg.SpecialistTimeout,
		autoCommit:        cfg.AutoCommit,
		think:             cfg.Think,
		verbose:           cfg.Verbose,
	}
	if o.maxRevIter <= 0 {
		o.maxRevIter = maxExecIterations
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
// progLang returns the detected project programming language as a string,
// or "" if unknown.
func (o *Orchestrator) progLang() string {
	if o.langState == nil {
		return ""
	}
	return string(o.langState.Current())
}

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
// should be one of: planning, awaiting_approval, executing, verifying.
func (o *Orchestrator) emitPhase(name string) {
	o.emit(&Event{Type: EventPhase, Message: name})
}

// Run executes the full Plan → Execute → (Review) pipeline. The planner sub-agent
// drafts a plan from task; if non-empty it is shown to the user and approved
// (or revised) via planConfirm; then the executor sub-agent applies the plan
// step-by-step, with optional verification/review afterwards. Returns the
// first error from any phase, or nil on success.
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
		for {
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

				continue
			}

			break
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

	execStats, err := o.runExecutor(execCtx, plan, confirmForExec, autoAccept)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}

	// Verification loop (optional, planner path only).
	// The verifier checks that every step in the approved plan was actually
	// implemented. If incomplete items are found, it produces a fix plan and
	// re-runs the executor. Repeats up to maxReplans times.
	if o.review {
		for attempt := 0; attempt < o.maxReplans; attempt++ {
			o.emitPhase("verifying")
			o.emit(&Event{
				Type:    EventAgent,
				Message: fmt.Sprintf("▸ Verifying plan completion (%d/%d)...", attempt+1, o.maxReplans),
			})

			fixPlan, err := o.verifyOnce(execCtx, plan, &execStats)
			if err != nil {
				return err
			}

			if fixPlan == "" {
				o.emit(&Event{Type: EventAgent, Message: "✓ All plan steps verified."})
				break
			}

			o.emit(&Event{Type: EventAgent, Message: "▸ Applying fix plan for incomplete items..."})
			fixStats, err := o.runExecutor(execCtx, fixPlan, o.confirmFn, false)
			if err != nil {
				return errors.WithMessage(err, "executor (verification fix)")
			}
			mergeStats(&execStats, &fixStats)
		}
	}

	// Restore plan mode in TUI after orchestrator completes.
	o.emit(&Event{Type: EventModeChange, Message: "plan"})
	o.emit(&Event{Type: EventDone, Message: doneMsg, Stats: &execStats})
	return nil
}

func (o *Orchestrator) newAgent(role Role, maxIter int) *Agent {
	prof := o.profileFor(role)
	ag := New(prof.Client, prof.Model, o.reg, maxIter, o.workDir, prof.ContextSize)
	ag.SetLanguage(o.language)
	ag.SetAutoCompact(true)
	ag.SetMaxToolWorkers(maxToolWorkersDefault)
	ag.SetHasSnippets(o.hasSnippets)
	ag.SetHasRAG(o.hasRAG)
	switch role {
	case RolePlanner, RoleExecutor, RoleReviewer, RoleVerifier:
		ag.SetTaskLabel(taskLabelForRole(role))
	default:
		if o.verbose {
			ag.SetTaskLabel(taskLabelForRole(role))
		}
	}
	ag.SetVerbose(o.verbose)

	switch role {
	case RolePlanner:
		ag.SetMode(ModePlan)
		ag.SetThink(o.think)
	case RoleExecutor:
		ag.SetMode(ModeEdit)
		ag.SetThink(false)
	case RoleReviewer, RoleVerifier:
		ag.SetMode(ModePlan)
		ag.SetThink(o.think)
	case RoleCodeReviewer, RoleArchReviewer:
		ag.SetMode(ModePlan)
		ag.SetThink(o.think)
		ag.SetToolsDisabled(true)
	case RoleExtractor:
		ag.SetMode(ModePlan)
		ag.SetThink(false) // No thinking — just structured extraction
		// Extractor must produce raw JSON only. Hide all tools so local models
		// (qwen3-coder, etc.) cannot hallucinate tool_calls instead of output.
		ag.SetToolsDisabled(true)
		// Force deterministic generation for the extractor. The format hint
		// pushes the local model toward valid JSON.
		ag.SetTemperature(prof.Temperature)
		if prof.Format != "" {
			ag.SetFormat(prof.Format)
		}
	case RoleStructurer:
		ag.SetMode(ModePlan)
		ag.SetThink(false)
		// Structurer only converts text to JSON — no tools needed.
		// Structurer reuses the extractor profile so deterministic settings
		// stay in one place.
		ag.SetToolsDisabled(true)
		extProf := o.profileFor(RoleExtractor)
		ag.SetTemperature(extProf.Temperature)
		if extProf.Format != "" {
			ag.SetFormat(extProf.Format)
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
	for _, m := range sourceFilePathRe.FindAllStringSubmatch(task, -1) {
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

	// 5. Embedded language standards fan-out: search by each embedded
	//    reference doc name for the detected project language. This ensures
	//    standards (effective_go, go_common_mistakes, etc.) are reliably
	//    surfaced even when semantic search on task text misses them.
	for _, name := range rag.EmbeddedDocNames(o.progLang()) {
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

	// Embedded language standards fan-out (same as ragPrefetchForReview step 5).
	for _, name := range rag.EmbeddedDocNames(o.progLang()) {
		results, err := o.ragIndex.Search(ctx, name, ragRuleTopK)
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
