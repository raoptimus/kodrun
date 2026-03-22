package tools

import (
	"context"
	"fmt"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// Tool is the interface for all agent tools.
type Tool interface {
	Name() string
	Description() string
	Schema() ollama.JSONSchema
	Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}

// ToolResult represents the outcome of a tool execution.
type ToolResult struct {
	Output  string         `json:"output"`
	Error   string         `json:"error,omitempty"`
	Success bool           `json:"success"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Registry manages available tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Execute runs a tool by name with the given parameters.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (ToolResult, error) {
	t, ok := r.tools[name]
	if !ok {
		return ToolResult{
			Error:   fmt.Sprintf("unknown tool: %s", name),
			Success: false,
		}, nil
	}
	return t.Execute(ctx, params)
}

// ToolDefs returns Ollama tool definitions for all registered tools.
func (r *Registry) ToolDefs() []ollama.ToolDef {
	defs := make([]ollama.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, ollama.ToolDef{
			Type: "function",
			Function: ollama.ToolFuncDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return defs
}

// Names returns all registered tool names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}
