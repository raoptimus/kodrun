package agent

import (
	"fmt"
	"io"
)

// PlainOutput writes agent events to an io.Writer as plain text.
type PlainOutput struct {
	w io.Writer
}

// NewPlainOutput creates a plain text output handler.
func NewPlainOutput(w io.Writer) *PlainOutput {
	return &PlainOutput{w: w}
}

// Handle processes an agent event and writes it to the output.
func (p *PlainOutput) Handle(e Event) {
	switch e.Type {
	case EventAgent:
		fmt.Fprintf(p.w, "%s\n", e.Message)
	case EventTool:
		status := "✓"
		if !e.Success {
			status = "✗"
		}
		if e.Tool != "" {
			fmt.Fprintf(p.w, "%s(%s)\n", e.Tool, status)
		}
		if e.Message != "" && e.Message != "executing..." {
			fmt.Fprintf(p.w, "        %s\n", e.Message)
		}
	case EventFix:
		fmt.Fprintf(p.w, "[fix]   %s\n", e.Message)
	case EventError:
		fmt.Fprintf(p.w, "[error] %s: %s\n", e.Tool, e.Message)
	case EventTokens:
		fmt.Fprintf(p.w, "[tokens] prompt: %d, eval: %d\n", e.PromptTokens, e.EvalTokens)
	case EventCompact:
		fmt.Fprintf(p.w, "[compact] %s\n", e.Message)
	case EventDone:
		// Intentionally left blank for clean output
	}
}
