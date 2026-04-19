package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
)

// Fixer uses an LLM to automatically fix Go errors.
type Fixer struct {
	client  llm.Client
	model   string
	reg     *tools.Registry
	maxIter int
}

// NewFixer creates a new error fixer.
func NewFixer(_ context.Context, client llm.Client, model string, reg *tools.Registry, maxIter int) *Fixer {
	return &Fixer{
		client:  client,
		model:   model,
		reg:     reg,
		maxIter: maxIter,
	}
}

// Fix attempts to fix errors from a tool run.
func (f *Fixer) Fix(ctx context.Context, toolName, output string, onEvent func(string)) (bool, error) {
	errs := ParseErrors(ctx, output)
	if len(errs) == 0 {
		return false, nil
	}

	// Read affected files for context
	files := AffectedFiles(ctx, errs)
	var fileContents strings.Builder
	for _, file := range files {
		result, err := f.reg.Execute(ctx, "read_file", map[string]any{"path": file})
		if err != nil {
			continue
		}
		fmt.Fprintf(&fileContents, "=== %s ===\n%s\n\n", file, result.Output)
	}

	prompt := fmt.Sprintf(`Fix the following Go errors. Use the edit_file or write_file tools to apply fixes.

Errors:
%s

File contents:
%s

Fix each error. Use edit_file with old_str/new_str for targeted changes.`,
		FormatErrors(ctx, errs),
		fileContents.String(),
	)

	for iter := range f.maxIter {
		if onEvent != nil {
			onEvent(fmt.Sprintf("[fix] attempt %d/%d", iter+1, f.maxIter))
		}

		resp, err := f.client.ChatSync(ctx, &llm.ChatRequest{
			Model: f.model,
			Messages: []llm.Message{
				{Role: "system", Content: "You are a Go expert. Fix errors using available tools. Be precise and minimal in changes."},
				{Role: "user", Content: prompt},
			},
			Tools: f.reg.ToolDefs(),
		})
		if err != nil {
			return false, errors.WithMessage(err, "fixer chat")
		}

		if len(resp.ToolCalls) == 0 {
			return false, nil
		}

		for _, tc := range resp.ToolCalls {
			_, err := f.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if onEvent != nil {
				status := "✓"
				if err != nil {
					status = "✗"
				}
				onEvent(fmt.Sprintf("[fix] %s %s", tc.Function.Name, status))
			}
		}

		// Re-run the original tool to check if errors are fixed
		result, err := f.reg.Execute(ctx, toolName, map[string]any{})
		if err != nil {
			return false, err
		}

		// Update prompt with remaining errors
		errs = ParseErrors(ctx, result.Output)
		if len(errs) == 0 {
			return true, nil
		}
		prompt = fmt.Sprintf("Still have errors:\n%s\n\nFix them.", FormatErrors(ctx, errs))
	}

	return false, nil
}
