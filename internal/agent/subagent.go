package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/rules"
)

// runStep executes one Step of a structured Plan via a fresh sub-agent.
//
// Each sub-agent has its own clean message history (no cross-talk between
// parallel steps), is locked to the step's Files via the read-path whitelist
// (Block 1), and shares the orchestrator's tool result cache so repeated
// reads are free across all sub-agents (Block 2).
//
// The returned error is non-nil only on a hard failure of the chat loop;
// REPLAN responses from the sub-agent are surfaced via EventReplan and
// reported as a normal completion to the DAG runner.
func (o *Orchestrator) runStep(ctx context.Context, step *Step, confirmFn ConfirmFunc) (SessionStats, error) {
	ag := o.newAgent(RoleExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleExecutor, o.language, o.progLang(), o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(prompt)
	ag.SetConfirmFunc(confirmFn)

	// Lock the sub-agent to its declared files. The whitelist guard in
	// agent.go refuses any read outside this set.
	if len(step.Files) > 0 {
		ag.SetAllowedReadPaths(step.Files)
	}

	// Build a compact per-step task payload. Only the step's own data is
	// surfaced — the sub-agent never sees the rest of the plan, which
	// eliminates the temptation to "also fix the neighbouring file".
	var b strings.Builder
	fmt.Fprintf(&b, "## Step %d: %s\n\n", step.ID, step.Title)
	if step.Action != "" {
		fmt.Fprintf(&b, "Action: %s\n", step.Action)
	}
	if step.Rationale != "" {
		fmt.Fprintf(&b, "Rationale: %s\n", step.Rationale)
	}
	if len(step.Files) > 0 {
		fmt.Fprintf(&b, "Files: %s\n", strings.Join(step.Files, ", "))
	}
	b.WriteString("\nApply the change and stop. Do not touch any file outside the Files list.\n")

	// Resolve reference examples from disk and inject into the prompt.
	if len(step.Examples) > 0 {
		snippets := formatStepExamples(o.workDir, step.Examples, defaultBudgetLines)
		if snippets != "" {
			b.WriteString("\n## Examples\nCode from this project demonstrating the correct pattern:\n\n")
			b.WriteString(snippets)
		}
	}

	// Per-step RAG bundle: prefer the bundle pre-computed by runPlanDAG so
	// parallel sub-agents share one set of embedding calls instead of N. The
	// live fallback covers non-DAG callers.
	var ragBundle string
	if o.stepRAGBundles != nil {
		ragBundle = o.stepRAGBundles[step.ID]
	} else {
		ragBundle = o.perStepRAG(ctx, step)
	}
	if ragBundle != "" {
		b.WriteString("\n")
		b.WriteString(ragBundle)
	}

	if err := ag.Send(ctx, b.String()); err != nil && !errors.Is(err, ErrMaxIterations) {
		return ag.Stats(), err
	}

	stats := ag.Stats()

	// Surface REPLAN if the sub-agent asked for one.
	last := ag.LastAssistantMessage()
	if strings.Contains(last, "REPLAN:") {
		o.emit(&Event{Type: EventReplan, Message: extractReplanReason(last)})
		return stats, nil
	}

	// A step that finished with zero tool calls and no REPLAN is a model
	// failure: the executor wrote prose instead of applying the change.
	// Surface it loudly so the orchestrator/CI does not treat the step as
	// successful when nothing actually happened on disk.
	if stats.ToolCalls == 0 {
		o.emit(&Event{Type: EventError, Message: fmt.Sprintf("Step %d (%s) finished with zero tool calls — model returned text instead of applying changes. Treating as failed.", step.ID, step.Title)})
		return stats, errors.Errorf("step %d: no tool calls executed", step.ID)
	}

	return stats, nil
}

// perStepRAG builds a compact per-step RAG payload combining declared rule
// names and a focused search on the step title + rationale. Returns "" when
// nothing relevant is available.
func (o *Orchestrator) perStepRAG(ctx context.Context, step *Step) string {
	var parts []string

	if o.rulesLoader != nil {
		for _, name := range step.RuleNames {
			if content, err := o.rulesLoader.GetRuleContent(ctx, name, rules.ScopeAll); err == nil && content != "" {
				parts = append(parts, fmt.Sprintf("--- RULE %s ---\n%s\n", name, content))
			}
		}
	}

	if o.hasRAG && o.ragIndex != nil {
		query := strings.TrimSpace(step.Title + " " + step.Rationale)
		if query != "" {
			results, err := o.ragIndex.Search(ctx, query, perStepRAGTopK)
			if err == nil && len(results) > 0 {
				parts = append(parts, formatRAGResults(results))
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// Compile-time guard so unused-import linters do not strip the ollama import
// when this file is the only consumer.
var _ = llm.Message{}
