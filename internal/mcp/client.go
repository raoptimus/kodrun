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
	"sync/atomic"

	"github.com/pkg/errors"
)

const protocolVersion = "2025-03-26"

// MCPToolDef represents a tool definition returned by the MCP server.
type MCPToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// MCPToolResult is the result of calling an MCP tool.
type MCPToolResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

// ContentBlock is a single content item in a tool result.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ServerInfo holds information about the connected MCP server.
type ServerInfo struct {
	Name            string         `json:"name"`
	Version         string         `json:"version"`
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
}

// Client communicates with an MCP server over a transport.
type Client struct {
	transport  Transport
	nextID     atomic.Int64
	serverInfo ServerInfo
	closed     atomic.Bool
}

// NewClient creates a new MCP client.
func NewClient(transport Transport) *Client {
	return &Client{transport: transport}
}

// Initialize performs the MCP handshake with the server.
func (c *Client) Initialize(ctx context.Context) error {
	if c.closed.Load() {
		return errors.New("client is closed")
	}

	type clientInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type initParams struct {
		ProtocolVersion string     `json:"protocolVersion"`
		Capabilities    struct{}   `json:"capabilities"`
		ClientInfo      clientInfo `json:"clientInfo"`
	}

	resp, err := c.call(ctx, "initialize", initParams{
		ProtocolVersion: protocolVersion,
		ClientInfo:      clientInfo{Name: "kodrun", Version: "0.1.0"},
	})
	if err != nil {
		return errors.WithMessage(err, "initialize")
	}

	var initResult struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		return errors.WithMessage(err, "parse init result")
	}

	c.serverInfo = ServerInfo{
		Name:            initResult.ServerInfo.Name,
		Version:         initResult.ServerInfo.Version,
		ProtocolVersion: initResult.ProtocolVersion,
		Capabilities:    initResult.Capabilities,
	}

	// Send initialized notification (no ID = notification).
	return c.transport.Send(Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	})
}

// ServerInfo returns information about the connected server.
func (c *Client) ServerInfo() ServerInfo {
	return c.serverInfo
}

// ListTools retrieves the list of available tools from the server.
func (c *Client) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	if c.closed.Load() {
		return nil, errors.New("client is closed")
	}

	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, errors.WithMessage(err, "tools/list")
	}

	var result struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, errors.WithMessage(err, "parse tools")
	}
	return result.Tools, nil
}

// CallTool invokes a tool on the server.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (MCPToolResult, error) {
	if c.closed.Load() {
		return MCPToolResult{}, errors.New("client is closed")
	}

	type callParams struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}

	resp, err := c.call(ctx, "tools/call", callParams{
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		return MCPToolResult{}, errors.WithMessagef(err, "tools/call %s", name)
	}

	var result MCPToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return MCPToolResult{}, errors.WithMessage(err, "parse call result")
	}
	return result, nil
}

// Close shuts down the client and transport.
func (c *Client) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	return c.transport.Close()
}

// call sends a JSON-RPC request and waits for the response.
// It skips server notifications (responses without an ID).
func (c *Client) call(ctx context.Context, method string, params any) (Response, error) {
	id := c.nextID.Add(1)
	req := Request{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  params,
	}

	if err := c.transport.Send(req); err != nil {
		return Response{}, errors.WithMessage(err, "send")
	}

	// Read responses, skipping notifications (no ID) until we get ours.
	for {
		resp, err := c.transport.Receive(ctx)
		if err != nil {
			return Response{}, errors.WithMessage(err, "receive")
		}

		// Skip notifications (messages without ID).
		if resp.ID == nil {
			continue
		}

		if *resp.ID != id {
			continue
		}

		if resp.Error != nil {
			return Response{}, resp.Error
		}
		return resp, nil
	}
}
