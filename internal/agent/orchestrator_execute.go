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
	"maps"
	"strings"

	"github.com/pkg/errors"
)

// RunExecutor executes an already-approved plan without running the planner phase.
// It is intended for callers that obtained a plan outside of the orchestrator
// (e.g. via the standalone agent + classifier path) and want to delegate just
// the execution to the orchestrator's executor sub-agent.
func (o *Orchestrator) RunExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc, autoAccept bool) error {
	o.emit(&Event{Type: EventModeChange, Message: "edit"})
	stats, err := o.runExecutor(ctx, plan, confirmFn, autoAccept)
	if err != nil {
		return errors.WithMessage(err, "executor")
	}
	o.emit(&Event{Type: EventModeChange, Message: "plan"})
	o.emit(&Event{Type: EventDone, Message: "Executor completed", Stats: &stats})
	return nil
}

func (o *Orchestrator) runExecutor(ctx context.Context, plan string, confirmFn ConfirmFunc, autoAccept bool) (SessionStats, error) {
	// Always attempt to structure the plan into a JSON DAG. maxParallelTasks
	// controls only the number of concurrent workers, not whether we use the
	// DAG path. This ensures every step gets its own clean sub-agent context
	// (and examples, RAG, whitelist) regardless of parallelism settings.
	structured := o.structurePlan(ctx, plan)
	if structured != nil && len(structured.Steps) > 0 {
		// TODO: parallel executor support requires proper TUI multiplexing;
		// hardcode to 1 until that is implemented.
		parallel := 1
		o.emit(&Event{
			Type:    EventAgent,
			Message: fmt.Sprintf("Executing plan as DAG: %d steps, max %d parallel", len(structured.Steps), parallel),
		})
		return o.runPlanDAG(ctx, structured, parallel, confirmFn, autoAccept)
	}

	// Fallback: structurer unavailable or returned empty plan. Use single
	// monolithic executor (no per-step examples in this path).
	o.emit(&Event{Type: EventAgent, Message: "Structurer unavailable; falling back to sequential executor"})

	// Pre-read project files so executor doesn't waste iterations on read_file/list_dir.
	o.emit(&Event{Type: EventGroupStart, Message: "Reading project files..."})
	codeContext := o.collectProjectFiles()
	o.emit(&Event{Type: EventGroupEnd})

	ag := o.newAgent(RoleExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleExecutor, o.language, o.progLang(), o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
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

	ltc := langToolsForLang(o.progLang())
	verifyHint := ""
	if ltc.buildTool != "" {
		verifyHint = fmt.Sprintf(" After all changes run %s, %s, %s.", ltc.buildTool, ltc.lintTool, ltc.testTool)
	}
	execTask := fmt.Sprintf("## Source Code (already read — do NOT call read_file or list_dir)\n%s\n## Approved Plan\n\n%s\n\n---\nImplement each step by calling edit_file/write_file.%s", codeContext, plan, verifyHint)
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

// verifyOnce runs a single verification pass with optional timeout. Returns
// the fix plan (empty if all steps verified) or an error.
func (o *Orchestrator) verifyOnce(ctx context.Context, plan string, execStats *SessionStats) (string, error) {
	verifyCtx := ctx
	if o.specialistTimeout > 0 {
		var cancel context.CancelFunc
		verifyCtx, cancel = context.WithTimeout(ctx, o.specialistTimeout)
		defer cancel()
	}

	fixPlan, err := o.runVerifier(verifyCtx, plan, execStats)
	if err != nil {
		if verifyCtx.Err() != nil {
			o.emit(&Event{Type: EventAgent, Message: "⚠ Verification timed out — skipping."})
			return "", nil
		}
		return "", errors.WithMessage(err, "verifier")
	}
	return fixPlan, nil
}

func (o *Orchestrator) runVerifier(ctx context.Context, plan string, execStats *SessionStats) (string, error) {
	ag := o.newAgent(RoleVerifier, o.maxRevIter)

	ltc := langToolsForLang(o.progLang())
	verifyTools := make(map[string]bool)
	if ltc.buildTool != "" {
		verifyTools[ltc.buildTool] = true
	}
	if ltc.lintTool != "" {
		verifyTools[ltc.lintTool] = true
	}
	if len(verifyTools) > 0 {
		ag.AddReadOnlyTools(verifyTools)
	}

	roTools := ag.readOnlyTools()
	mergedNames := make(map[string]bool, len(roTools)+len(verifyTools))
	maps.Copy(mergedNames, roTools)
	maps.Copy(mergedNames, verifyTools)
	toolNames := ag.reg.NamesFiltered(mergedNames)

	prompt := systemPromptForRole(RoleVerifier, o.language, o.progLang(), o.ruleCatalog, toolNames, o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)

	// Pre-read affected files so the verifier does not waste iterations on
	// read_file calls. The code is injected into the task prompt, same
	// approach as the executor uses for step context.
	o.emit(&Event{Type: EventGroupStart, Message: "Reading files for verification..."})
	codeContext := o.collectProjectFiles()
	o.emit(&Event{Type: EventGroupEnd})

	var taskBuf strings.Builder
	if codeContext != "" {
		fmt.Fprintf(&taskBuf, "## Source Code (already read — do NOT call read_file or list_dir)\n%s\n\n", codeContext)
	}
	if o.lastPlannerTask != "" {
		fmt.Fprintf(&taskBuf, "## Original Task\n\n%s\n\n", o.lastPlannerTask)
	}
	fmt.Fprintf(&taskBuf, "## Approved Plan\n\n%s\n\n", plan)
	fmt.Fprintf(&taskBuf,
		"## Execution Stats\n\n%d files added, %d modified, %d deleted, +%d -%d lines, %d tool calls.\n\n",
		execStats.FilesAdded, execStats.FilesModified, execStats.FilesDeleted,
		execStats.LinesAdded, execStats.LinesRemoved, execStats.ToolCalls,
	)
	taskBuf.WriteString("Verify that every step in the approved plan was implemented correctly. The source code is provided above — do NOT call read_file. If all steps are done, respond with VERIFIED. Otherwise, list only the incomplete or incorrect items as a numbered plan.")

	verifyTask := taskBuf.String()
	if ragContext := o.ragPrefetchForReview(ctx, plan); ragContext != "" {
		verifyTask = ragContext + "\n" + verifyTask
	}

	if err := ag.Send(ctx, verifyTask); err != nil && !errors.Is(err, ErrMaxIterations) {
		return "", err
	}

	// Check if verifier confirmed all steps.
	lastPlan := ag.LastPlan()
	if lastPlan == "" || strings.Contains(strings.ToUpper(lastPlan), "VERIFIED") {
		return "", nil
	}

	// Normalize incomplete items through extractor for re-execution.
	return o.extractPlan(ctx, lastPlan)
}
