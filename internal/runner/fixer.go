package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/go-agent/internal/ollama"
	"github.com/raoptimus/go-agent/internal/tools"
)

// Fixer uses an LLM to automatically fix Go errors.
type Fixer struct {
	client  *ollama.Client
	model   string
	reg     *tools.Registry
	maxIter int
}

// NewFixer creates a new error fixer.
func NewFixer(client *ollama.Client, model string, reg *tools.Registry, maxIter int) *Fixer {
	return &Fixer{
		client:  client,
		model:   model,
		reg:     reg,
		maxIter: maxIter,
	}
}

// Fix attempts to fix errors from a tool run.
func (f *Fixer) Fix(ctx context.Context, toolName string, output string, onEvent func(string)) (bool, error) {
	errors := ParseErrors(output)
	if len(errors) == 0 {
		return false, nil
	}

	// Read affected files for context
	files := AffectedFiles(errors)
	var fileContents strings.Builder
	for _, file := range files {
		result, err := f.reg.Execute(ctx, "read_file", map[string]any{"path": file})
		if err != nil || !result.Success {
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
		FormatErrors(errors),
		fileContents.String(),
	)

	for iter := range f.maxIter {
		if onEvent != nil {
			onEvent(fmt.Sprintf("[fix] attempt %d/%d", iter+1, f.maxIter))
		}

		resp, err := f.client.ChatSync(ctx, ollama.ChatRequest{
			Model: f.model,
			Messages: []ollama.Message{
				{Role: "system", Content: "You are a Go expert. Fix errors using available tools. Be precise and minimal in changes."},
				{Role: "user", Content: prompt},
			},
			Tools: f.reg.ToolDefs(),
		})
		if err != nil {
			return false, fmt.Errorf("fixer chat: %w", err)
		}

		if len(resp.ToolCalls) == 0 {
			return false, nil
		}

		for _, tc := range resp.ToolCalls {
			result, err := f.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				continue
			}
			if onEvent != nil {
				status := "✓"
				if !result.Success {
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
		if result.Success {
			return true, nil
		}

		// Update prompt with remaining errors
		errors = ParseErrors(result.Output)
		if len(errors) == 0 {
			return true, nil
		}
		prompt = fmt.Sprintf("Still have errors:\n%s\n\nFix them.", FormatErrors(errors))
	}

	return false, nil
}
