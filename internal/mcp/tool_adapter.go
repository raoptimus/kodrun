/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
)

// ToolAdapter wraps an MCP tool as a tools.Tool for the agent registry.
type ToolAdapter struct {
	client       *Client
	serverName   string
	mcpName      string // original tool name on the MCP server
	registryName string // "mcp_<server>_<tool>"
	description  string
	schema       llm.JSONSchema
}

// NewToolAdapter creates a tool adapter that proxies calls to an MCP server.
func NewToolAdapter(client *Client, serverName string, def MCPToolDef) *ToolAdapter {
	return &ToolAdapter{
		client:       client,
		serverName:   serverName,
		mcpName:      def.Name,
		registryName: fmt.Sprintf("mcp_%s_%s", serverName, def.Name),
		description:  fmt.Sprintf("[MCP:%s] %s", serverName, def.Description),
		schema:       convertSchema(def.InputSchema),
	}
}

func (a *ToolAdapter) Name() string           { return a.registryName }
func (a *ToolAdapter) Description() string    { return a.description }
func (a *ToolAdapter) Schema() llm.JSONSchema { return a.schema }

// Execute calls the MCP tool and converts the result to ToolResult.
func (a *ToolAdapter) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	result, err := a.client.CallTool(ctx, a.mcpName, params)
	if err != nil {
		return nil, fmt.Errorf("mcp call %s: %w", a.mcpName, err)
	}

	output := formatContent(result.Content)

	if result.IsError {
		return nil, &tools.ToolError{Msg: output}
	}

	return &tools.ToolResult{
		Output: output,
	}, nil
}

// formatContent concatenates text content blocks into a single string.
func formatContent(blocks []ContentBlock) string {
	var sb strings.Builder
	for i, b := range blocks {
		if b.Type == "text" {
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(b.Text)
		}
	}
	return sb.String()
}

// convertSchema converts an MCP JSON Schema (map[string]any) to llm.JSONSchema.
func convertSchema(raw map[string]any) llm.JSONSchema {
	if raw == nil {
		return llm.JSONSchema{Type: "object"}
	}

	s := llm.JSONSchema{}

	if t, ok := raw["type"].(string); ok {
		s.Type = t
	}
	if d, ok := raw["description"].(string); ok {
		s.Description = d
	}

	if props, ok := raw["properties"].(map[string]any); ok {
		s.Properties = make(map[string]llm.JSONSchema, len(props))
		for key, val := range props {
			if propMap, ok := val.(map[string]any); ok {
				s.Properties[key] = convertSchema(propMap)
			}
		}
	}

	if req, ok := raw["required"].([]any); ok {
		for _, v := range req {
			if str, ok := v.(string); ok {
				s.Required = append(s.Required, str)
			}
		}
	}

	if enum, ok := raw["enum"].([]any); ok {
		for _, v := range enum {
			if str, ok := v.(string); ok {
				s.Enum = append(s.Enum, str)
			}
		}
	}

	if items, ok := raw["items"].(map[string]any); ok {
		converted := convertSchema(items)
		s.Items = &converted
	}

	return s
}
