package tools

import (
	"context"
	"fmt"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rules"
)

// RuleTool provides access to project rules via the get_rule tool call.
type RuleTool struct {
	loader *rules.Loader
	scope  rules.Scope
}

// NewRuleTool creates a new get_rule tool.
func NewRuleTool(loader *rules.Loader, scope rules.Scope) *RuleTool {
	return &RuleTool{loader: loader, scope: scope}
}

func (t *RuleTool) Name() string { return "get_rule" }
func (t *RuleTool) Description() string {
	return "Get full content of a project rule with referenced documentation"
}

func (t *RuleTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"name": {Type: "string", Description: "Rule name from the catalog (e.g. service, model, repository)"},
		},
		Required: []string{"name"},
	}
}

func (t *RuleTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	name := stringParam(params, "name")
	if name == "" {
		return nil, &ToolError{Msg: "name is required"}
	}

	content, err := t.loader.GetRuleContent(ctx, name, t.scope)
	if err != nil {
		return nil, &ToolError{Msg: fmt.Sprintf("rule not found: %s", name)}
	}

	return &ToolResult{Output: content}, nil
}
