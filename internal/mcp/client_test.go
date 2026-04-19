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
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

// mockTransport simulates an MCP server for testing.
type mockTransport struct {
	mu        sync.Mutex
	requests  []Request
	responses []Response
	respIdx   int
	closed    bool
}

func newMockTransport(responses ...Response) *mockTransport {
	return &mockTransport{responses: responses}
}

func (m *mockTransport) Send(req Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("transport closed")
	}
	m.requests = append(m.requests, req)
	return nil
}

func (m *mockTransport) Receive(_ context.Context) (Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Response{}, fmt.Errorf("transport closed")
	}
	if m.respIdx >= len(m.responses) {
		return Response{}, fmt.Errorf("no more responses")
	}
	resp := m.responses[m.respIdx]
	m.respIdx++
	return resp, nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func respWithID(id int64, result any) Response {
	data, _ := json.Marshal(result)
	return Response{
		JSONRPC: "2.0",
		ID:      &id,
		Result:  json.RawMessage(data),
	}
}

func TestClient_Initialize(t *testing.T) {
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "test-server", "version": "1.0"},
	}

	mt := newMockTransport(respWithID(1, initResult))
	client := NewClient(mt)

	if err := client.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	info := client.ServerInfo()
	if info.Name != "test-server" {
		t.Errorf("server name: got %q, want %q", info.Name, "test-server")
	}
	if info.Version != "1.0" {
		t.Errorf("server version: got %q, want %q", info.Version, "1.0")
	}

	// Should have sent 2 messages: initialize request + initialized notification.
	if len(mt.requests) != 2 {
		t.Fatalf("requests: got %d, want 2", len(mt.requests))
	}
	if mt.requests[0].Method != "initialize" {
		t.Errorf("request[0] method: got %q", mt.requests[0].Method)
	}
	if mt.requests[1].Method != "notifications/initialized" {
		t.Errorf("request[1] method: got %q", mt.requests[1].Method)
	}
	// Notification should have no ID.
	if mt.requests[1].ID != nil {
		t.Error("notification should not have ID")
	}
}

func TestClient_ListTools(t *testing.T) {
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "s", "version": "1"},
	}
	toolsResult := map[string]any{
		"tools": []any{
			map[string]any{
				"name":        "read_file",
				"description": "Read a file",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "File path"},
					},
					"required": []any{"path"},
				},
			},
		},
	}

	mt := newMockTransport(
		respWithID(1, initResult),
		respWithID(2, toolsResult),
	)
	client := NewClient(mt)
	ctx := context.Background()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools: got %d, want 1", len(tools))
	}
	if tools[0].Name != "read_file" {
		t.Errorf("tool name: got %q", tools[0].Name)
	}
	if tools[0].Description != "Read a file" {
		t.Errorf("tool description: got %q", tools[0].Description)
	}
}

func TestClient_CallTool(t *testing.T) {
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "s", "version": "1"},
	}
	callResult := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "file contents here"},
		},
		"isError": false,
	}

	mt := newMockTransport(
		respWithID(1, initResult),
		respWithID(2, callResult),
	)
	client := NewClient(mt)
	ctx := context.Background()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	result, err := client.CallTool(ctx, "read_file", map[string]any{"path": "test.go"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Error("unexpected isError=true")
	}
	if len(result.Content) != 1 {
		t.Fatalf("content blocks: got %d, want 1", len(result.Content))
	}
	if result.Content[0].Text != "file contents here" {
		t.Errorf("content text: got %q", result.Content[0].Text)
	}
}

func TestClient_CallTool_Error(t *testing.T) {
	initResult := map[string]any{
		"protocolVersion": "2025-03-26",
		"capabilities":    map[string]any{},
		"serverInfo":      map[string]any{"name": "s", "version": "1"},
	}
	callResult := map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "permission denied"},
		},
		"isError": true,
	}

	mt := newMockTransport(
		respWithID(1, initResult),
		respWithID(2, callResult),
	)
	client := NewClient(mt)
	ctx := context.Background()

	_ = client.Initialize(ctx)

	result, err := client.CallTool(ctx, "delete_file", map[string]any{"path": "/etc/passwd"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true")
	}
}

func TestClient_ClosedClient(t *testing.T) {
	mt := newMockTransport()
	client := NewClient(mt)
	_ = client.Close()

	if err := client.Initialize(context.Background()); err == nil {
		t.Error("expected error from closed client")
	}
	if _, err := client.ListTools(context.Background()); err == nil {
		t.Error("expected error from closed client")
	}
	if _, err := client.CallTool(context.Background(), "x", nil); err == nil {
		t.Error("expected error from closed client")
	}
}
