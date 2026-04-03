package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/ollama"
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
func (o *Orchestrator) runStep(ctx context.Context, step Step, confirmFn ConfirmFunc) (SessionStats, error) {
	ag := o.newAgent(RoleStepExecutor, o.maxExecIter)
	prompt := systemPromptForRole(RoleStepExecutor, o.language, o.ruleCatalog, ag.reg.Names(), o.hasSnippets, o.hasRAG)
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

	// Per-step RAG bundle: rule names declared on the step go through the
	// rules loader if available, plus a focused RAG search on the step title.
	if rag := o.perStepRAG(ctx, step); rag != "" {
		b.WriteString("\n")
		b.WriteString(rag)
	}

	if err := ag.Send(ctx, b.String()); err != nil && !errors.Is(err, ErrMaxIterations) {
		return ag.Stats(), err
	}

	// Surface REPLAN if the sub-agent asked for one.
	if last := ag.LastAssistantMessage(); strings.Contains(last, "REPLAN:") {
		o.emit(Event{Type: EventReplan, Message: extractReplanReason(last)})
	}

	return ag.Stats(), nil
}

// perStepRAG builds a compact per-step RAG payload combining declared rule
// names and a focused search on the step title + rationale. Returns "" when
// nothing relevant is available.
func (o *Orchestrator) perStepRAG(ctx context.Context, step Step) string {
	var parts []string

	if o.rulesLoader != nil {
		for _, name := range step.RuleNames {
			if content, err := o.rulesLoader.GetRuleContent(ctx, name, rules.ScopeAll); err == nil && content != "" {
				parts = append(parts, fmt.Sprintf("--- RULE %s ---\n%s\n", name, content))
			}
		}
	}

	if o.ragIndex != nil {
		query := strings.TrimSpace(step.Title + " " + step.Rationale)
		if query != "" {
			results, err := o.ragIndex.Search(ctx, query, 3)
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
var _ = ollama.Message{}
