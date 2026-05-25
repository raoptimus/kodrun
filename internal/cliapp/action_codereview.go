/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/agent"
)

// actionCodeReview waits for any in-flight RAG indexing, collects the
// review diff (optionally scoped to a package) and dispatches into the
// parallel-specialist code review pipeline.
func actionCodeReview(taskCtx context.Context, d *TaskDeps, parts []string) {
	if d.Indexing.Load() {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Waiting for RAG indexing to finish before review..."})
		WaitForRAGReady(taskCtx, d.Indexing, ragWaitTimeout)
	}
	diffArgs, packageScope := parseCodeReviewArgs(parts)
	if d.Flags.Verbose {
		if packageScope != "" {
			d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Collecting diff for code review (scope: %s)...", packageScope)})
		} else {
			d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Collecting diff for code review..."})
		}
	}
	diffOut, err := GitDiff(taskCtx, d.Flags.WorkDir, diffArgs)
	if err != nil {
		d.Emit(&agent.Event{Type: agent.EventError, Message: err.Error()})
		d.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	if packageScope != "" {
		diffOut = FilterDiffByPackage(diffOut, packageScope)
	}
	if strings.TrimSpace(diffOut) == "" {
		if packageScope != "" {
			d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("No changes to review under %q.", packageScope)})
		} else {
			d.Emit(&agent.Event{Type: agent.EventAgent, Message: "No changes to review."})
		}
		d.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	RunCodeReview(taskCtx, &CodeReviewParams{
		WorkDir:        d.Flags.WorkDir,
		Cfg:            d.Cfg,
		Client:         d.Client,
		ChatProv:       d.ChatProv,
		Registry:       d.Registry,
		Agent:          d.Agent,
		Emit:           d.Emit,
		PlanConfirm:    d.PlanConfirm,
		StepConfirm:    d.StepConfirm,
		RuleCatalog:    d.RuleCatalog,
		RAGIndex:       d.RAGIndex,
		GodocIndexer:   d.GodocIndexer,
		LangState:      d.LangState,
		RulesLoader:    d.RulesLoader,
		SnippetsLoader: d.SnippetsLoader,
		Flags:          d.Flags,
	})
}

// parseCodeReviewArgs peels `--package <path>` (or `--package=<path>`) out
// of the raw arg slice; remaining args are forwarded to git diff.
func parseCodeReviewArgs(parts []string) (diffArgs []string, packageScope string) {
	if len(parts) <= 1 {
		return nil, ""
	}
	rawArgs := strings.Fields(parts[1])
	for i := 0; i < len(rawArgs); i++ {
		a := rawArgs[i]
		if a == "--package" && i+1 < len(rawArgs) {
			packageScope = strings.TrimSuffix(rawArgs[i+1], "/")
			i++
			continue
		}
		if v, ok := strings.CutPrefix(a, "--package="); ok {
			packageScope = strings.TrimSuffix(v, "/")
			continue
		}
		diffArgs = append(diffArgs, a)
	}
	return diffArgs, packageScope
}
