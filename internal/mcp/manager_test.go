/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_ConfirmTools_ReturnsToolsRequiringConfirmation_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		adapters []*ToolAdapter
		configs  map[string]ServerConfig
		expected map[string]bool
	}{
		{
			name:     "No adapters",
			adapters: nil,
			configs:  map[string]ServerConfig{},
			expected: map[string]bool{},
		},
		{
			name: "All tools require confirmation",
			adapters: []*ToolAdapter{
				{serverName: "srv1", mcpName: "tool_a", registryName: "mcp_srv1_tool_a"},
				{serverName: "srv1", mcpName: "tool_b", registryName: "mcp_srv1_tool_b"},
			},
			configs: map[string]ServerConfig{
				"srv1": {AutoApprove: false},
			},
			expected: map[string]bool{
				"mcp_srv1_tool_a": true,
				"mcp_srv1_tool_b": true,
			},
		},
		{
			name: "AutoApprove server skips all tools",
			adapters: []*ToolAdapter{
				{serverName: "srv1", mcpName: "tool_a", registryName: "mcp_srv1_tool_a"},
			},
			configs: map[string]ServerConfig{
				"srv1": {AutoApprove: true},
			},
			expected: map[string]bool{},
		},
		{
			name: "Specific tool in AutoApproveTools is skipped",
			adapters: []*ToolAdapter{
				{serverName: "srv1", mcpName: "tool_a", registryName: "mcp_srv1_tool_a"},
				{serverName: "srv1", mcpName: "tool_b", registryName: "mcp_srv1_tool_b"},
			},
			configs: map[string]ServerConfig{
				"srv1": {
					AutoApprove:      false,
					AutoApproveTools: []string{"tool_a"},
				},
			},
			expected: map[string]bool{
				"mcp_srv1_tool_b": true,
			},
		},
		{
			name: "Multiple servers mixed config",
			adapters: []*ToolAdapter{
				{serverName: "approved", mcpName: "t1", registryName: "mcp_approved_t1"},
				{serverName: "manual", mcpName: "t2", registryName: "mcp_manual_t2"},
				{serverName: "manual", mcpName: "t3", registryName: "mcp_manual_t3"},
			},
			configs: map[string]ServerConfig{
				"approved": {AutoApprove: true},
				"manual":   {AutoApprove: false, AutoApproveTools: []string{"t3"}},
			},
			expected: map[string]bool{
				"mcp_manual_t2": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				adapters: tt.adapters,
				configs:  tt.configs,
			}

			result := m.ConfirmTools()

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManager_ReadOnlyTools_ReturnsReadOnlyToolSet_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		adapters []*ToolAdapter
		configs  map[string]ServerConfig
		expected map[string]bool
	}{
		{
			name:     "No adapters",
			adapters: nil,
			configs:  map[string]ServerConfig{},
			expected: map[string]bool{},
		},
		{
			name: "No read-only tools configured",
			adapters: []*ToolAdapter{
				{serverName: "srv1", mcpName: "write_file", registryName: "mcp_srv1_write_file"},
			},
			configs: map[string]ServerConfig{
				"srv1": {},
			},
			expected: map[string]bool{},
		},
		{
			name: "Tool listed in ReadOnlyTools",
			adapters: []*ToolAdapter{
				{serverName: "srv1", mcpName: "read_file", registryName: "mcp_srv1_read_file"},
				{serverName: "srv1", mcpName: "write_file", registryName: "mcp_srv1_write_file"},
			},
			configs: map[string]ServerConfig{
				"srv1": {ReadOnlyTools: []string{"read_file"}},
			},
			expected: map[string]bool{
				"mcp_srv1_read_file": true,
			},
		},
		{
			name: "Multiple servers with read-only tools",
			adapters: []*ToolAdapter{
				{serverName: "s1", mcpName: "list", registryName: "mcp_s1_list"},
				{serverName: "s2", mcpName: "get", registryName: "mcp_s2_get"},
				{serverName: "s2", mcpName: "set", registryName: "mcp_s2_set"},
			},
			configs: map[string]ServerConfig{
				"s1": {ReadOnlyTools: []string{"list"}},
				"s2": {ReadOnlyTools: []string{"get"}},
			},
			expected: map[string]bool{
				"mcp_s1_list": true,
				"mcp_s2_get":  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{
				adapters: tt.adapters,
				configs:  tt.configs,
			}

			result := m.ReadOnlyTools()

			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManager_ToolCount_ReturnsAdapterCount_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		adapters []*ToolAdapter
		expected int
	}{
		{
			name:     "No adapters",
			adapters: nil,
			expected: 0,
		},
		{
			name:     "One adapter",
			adapters: []*ToolAdapter{{}},
			expected: 1,
		},
		{
			name:     "Three adapters",
			adapters: []*ToolAdapter{{}, {}, {}},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{adapters: tt.adapters}

			assert.Equal(t, tt.expected, m.ToolCount())
		})
	}
}

func TestManager_Errors_ReturnsStoredErrors_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		errors   []string
		expected []string
	}{
		{
			name:     "No errors",
			errors:   nil,
			expected: nil,
		},
		{
			name:     "Single error",
			errors:   []string{"srv1: connection refused"},
			expected: []string{"srv1: connection refused"},
		},
		{
			name:     "Multiple errors",
			errors:   []string{"srv1: timeout", "srv2: auth failed"},
			expected: []string{"srv1: timeout", "srv2: auth failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Manager{errors: tt.errors}

			assert.Equal(t, tt.expected, m.Errors())
		})
	}
}

func TestManager_Close_ClosesAllClients_Successfully(t *testing.T) {
	mt1 := newMockTransport()
	mt2 := newMockTransport()

	m := &Manager{
		clients: map[string]*Client{
			"srv1": NewClient(mt1),
			"srv2": NewClient(mt2),
		},
		configs: make(map[string]ServerConfig),
	}

	err := m.Close()

	require.NoError(t, err)
	assert.True(t, mt1.closed)
	assert.True(t, mt2.closed)
}

func TestManager_Close_CalledTwice_ReturnsNil_Successfully(t *testing.T) {
	mt := newMockTransport()

	m := &Manager{
		clients: map[string]*Client{
			"srv1": NewClient(mt),
		},
		configs: make(map[string]ServerConfig),
	}

	err1 := m.Close()
	err2 := m.Close()

	require.NoError(t, err1)
	assert.NoError(t, err2)
}

func TestManager_Close_NoClients_Successfully(t *testing.T) {
	m := NewManager()

	err := m.Close()

	assert.NoError(t, err)
}

func TestNewManager_ReturnsInitialized_Successfully(t *testing.T) {
	m := NewManager()

	require.NotNil(t, m)
	assert.NotNil(t, m.clients)
	assert.NotNil(t, m.configs)
	assert.Nil(t, m.adapters)
	assert.Equal(t, 0, m.ToolCount())
}
