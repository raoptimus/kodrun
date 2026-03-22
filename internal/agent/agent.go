package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/raoptimus/go-agent/internal/ollama"
	"github.com/raoptimus/go-agent/internal/tools"
)

// ErrMaxIterations is returned when the agent exceeds max iterations.
var ErrMaxIterations = errors.New("max iterations reached")

// EventHandler receives agent events for display.
type EventHandler func(event Event)

// Event represents an agent lifecycle event.
type Event struct {
	Type    EventType
	Message string
	Tool    string
	Success bool
}

// EventType categorizes events.
type EventType int

const (
	EventAgent EventType = iota
	EventTool
	EventFix
	EventError
	EventDone
)

// Agent orchestrates the LLM-tool loop.
type Agent struct {
	client   *ollama.Client
	model    string
	reg      *tools.Registry
	history  []ollama.Message
	maxIter  int
	workDir  string
	onEvent  EventHandler
}

// New creates a new Agent.
func New(client *ollama.Client, model string, reg *tools.Registry, maxIter int, workDir string) *Agent {
	return &Agent{
		client:  client,
		model:   model,
		reg:     reg,
		maxIter: maxIter,
		workDir: workDir,
	}
}

// SetEventHandler sets the handler for agent events.
func (a *Agent) SetEventHandler(h EventHandler) {
	a.onEvent = h
}

func (a *Agent) emit(e Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// Run executes the agent loop for a given task.
func (a *Agent) Run(ctx context.Context, task string, systemRules string) error {
	systemPrompt := a.buildSystemPrompt(systemRules)
	a.history = []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task},
	}

	a.emit(Event{Type: EventAgent, Message: "Processing task..."})

	for iter := range a.maxIter {
		_ = iter

		resp, err := a.client.ChatSync(ctx, ollama.ChatRequest{
			Model:    a.model,
			Messages: a.history,
			Tools:    a.reg.ToolDefs(),
		})
		if err != nil {
			return fmt.Errorf("chat: %w", err)
		}

		// Add assistant response to history
		msg := ollama.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		a.history = append(a.history, msg)

		// No tool calls = final response
		if len(resp.ToolCalls) == 0 {
			if resp.Content != "" {
				a.emit(Event{Type: EventAgent, Message: resp.Content})
			}
			a.emit(Event{Type: EventDone, Message: "Done"})
			return nil
		}

		// Execute tool calls
		for _, tc := range resp.ToolCalls {
			a.emit(Event{Type: EventTool, Tool: tc.Function.Name, Message: "executing..."})

			result, err := a.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				a.emit(Event{Type: EventError, Tool: tc.Function.Name, Message: err.Error()})
				continue
			}

			a.emit(Event{
				Type:    EventTool,
				Tool:    tc.Function.Name,
				Message: truncate(result.Output, 200),
				Success: result.Success,
			})

			// Add tool result to history
			resultContent := result.Output
			if !result.Success && result.Error != "" {
				resultContent = "Error: " + result.Error
			}

			a.history = append(a.history, ollama.Message{
				Role:       "tool",
				Content:    resultContent,
				ToolCallID: tc.ID,
			})
		}

		// Emit intermediate text if any
		if resp.Content != "" {
			a.emit(Event{Type: EventAgent, Message: resp.Content})
		}
	}

	return ErrMaxIterations
}

func (a *Agent) buildSystemPrompt(rules string) string {
	var b strings.Builder
	b.WriteString("You are GoAgent, a Go programming assistant. You help write, fix, and maintain Go code.\n\n")
	b.WriteString("Available tools: " + strings.Join(a.reg.Names(), ", ") + "\n\n")

	// Project context
	goMod := filepath.Join(a.workDir, "go.mod")
	if data, err := os.ReadFile(goMod); err == nil {
		b.WriteString("go.mod:\n```\n")
		b.Write(data)
		b.WriteString("\n```\n\n")
	}

	if rules != "" {
		b.WriteString("Project rules:\n")
		b.WriteString(rules)
		b.WriteString("\n\n")
	}

	b.WriteString("Guidelines:\n")
	b.WriteString("- Write idiomatic Go code\n")
	b.WriteString("- Handle errors properly\n")
	b.WriteString("- Use edit_file for targeted changes, write_file for new files\n")
	b.WriteString("- Run go_build after changes to verify compilation\n")
	b.WriteString("- Run go_test to verify correctness\n")
	b.WriteString("- Be concise in responses\n")

	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
