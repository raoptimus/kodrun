package tools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/raoptimus/kodrun/internal/ollama"
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
	mu    sync.RWMutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Execute runs a tool by name with the given parameters.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (ToolResult, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ToolDefsFiltered returns tool definitions only for tools in the allowed set.
func (r *Registry) ToolDefsFiltered(allowed map[string]bool) []ollama.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ollama.ToolDef, 0, len(allowed))
	for _, t := range r.tools {
		if allowed[t.Name()] {
			defs = append(defs, ollama.ToolDef{
				Type: "function",
				Function: ollama.ToolFuncDef{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Schema(),
				},
			})
		}
	}
	return defs
}

// NamesFiltered returns tool names that are in the allowed set.
func (r *Registry) NamesFiltered(allowed map[string]bool) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(allowed))
	for name := range r.tools {
		if allowed[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
