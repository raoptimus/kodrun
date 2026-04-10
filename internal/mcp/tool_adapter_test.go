package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestConvertSchema_Basic(t *testing.T) {
	raw := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path",
			},
			"recursive": map[string]any{
				"type":        "boolean",
				"description": "Recurse into subdirectories",
			},
		},
		"required": []any{"path"},
	}

	schema := convertSchema(raw)
	if schema.Type != "object" {
		t.Errorf("type: got %q", schema.Type)
	}
	if len(schema.Properties) != 2 {
		t.Fatalf("properties: got %d, want 2", len(schema.Properties))
	}
	if schema.Properties["path"].Type != "string" {
		t.Errorf("path type: got %q", schema.Properties["path"].Type)
	}
	if schema.Properties["path"].Description != "File path" {
		t.Errorf("path desc: got %q", schema.Properties["path"].Description)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "path" {
		t.Errorf("required: got %v", schema.Required)
	}
}

func TestConvertSchema_WithEnum(t *testing.T) {
	raw := map[string]any{
		"type": "string",
		"enum": []any{"json", "yaml", "toml"},
	}

	schema := convertSchema(raw)
	if len(schema.Enum) != 3 {
		t.Fatalf("enum: got %d, want 3", len(schema.Enum))
	}
	if schema.Enum[1] != "yaml" {
		t.Errorf("enum[1]: got %q", schema.Enum[1])
	}
}

func TestConvertSchema_WithItems(t *testing.T) {
	raw := map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "string",
		},
	}

	schema := convertSchema(raw)
	if schema.Type != "array" {
		t.Errorf("type: got %q", schema.Type)
	}
	if schema.Items == nil {
		t.Fatal("items: nil")
	}
	if schema.Items.Type != "string" {
		t.Errorf("items type: got %q", schema.Items.Type)
	}
}

func TestConvertSchema_Nil(t *testing.T) {
	schema := convertSchema(nil)
	if schema.Type != "object" {
		t.Errorf("nil schema type: got %q, want 'object'", schema.Type)
	}
}

func TestToolAdapter_Name(t *testing.T) {
	adapter := NewToolAdapter(nil, "github", MCPToolDef{
		Name:        "get_issue",
		Description: "Get a GitHub issue",
		InputSchema: map[string]any{"type": "object"},
	})

	if adapter.Name() != "mcp_github_get_issue" {
		t.Errorf("name: got %q", adapter.Name())
	}
	if adapter.Description() != "[MCP:github] Get a GitHub issue" {
		t.Errorf("description: got %q", adapter.Description())
	}
}

func TestToolAdapter_Execute(t *testing.T) {
	callResult := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "Issue #1: Bug fix"},
		},
		"isError": false,
	}
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "s", "version": "1"},
	}

	mt := newMockTransport(
		respWithID(1, initResult),
		respWithID(2, callResult),
	)
	client := NewClient(mt)
	_ = client.Initialize(context.Background())

	adapter := NewToolAdapter(client, "github", MCPToolDef{
		Name:        "get_issue",
		Description: "Get a GitHub issue",
		InputSchema: map[string]any{"type": "object"},
	})

	result, err := adapter.Execute(context.Background(), map[string]any{"number": json.Number("1")})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Output != "Issue #1: Bug fix" {
		t.Errorf("output: got %q", result.Output)
	}
}

func TestToolAdapter_Execute_MCPError(t *testing.T) {
	callResult := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "not found"},
		},
		"isError": true,
	}
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "s", "version": "1"},
	}

	mt := newMockTransport(
		respWithID(1, initResult),
		respWithID(2, callResult),
	)
	client := NewClient(mt)
	_ = client.Initialize(context.Background())

	adapter := NewToolAdapter(client, "github", MCPToolDef{
		Name:        "get_issue",
		Description: "Get issue",
		InputSchema: map[string]any{"type": "object"},
	})

	_, err := adapter.Execute(context.Background(), map[string]any{"number": json.Number("999")})
	if err == nil {
		t.Error("expected failure")
	}
	if err.Error() != "not found" {
		t.Errorf("error: got %q", err.Error())
	}
}

func TestFormatContent(t *testing.T) {
	blocks := []ContentBlock{
		{Type: "text", Text: "line 1"},
		{Type: "image", Text: "ignored"},
		{Type: "text", Text: "line 2"},
	}

	got := formatContent(blocks)
	want := "line 1\nline 2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
