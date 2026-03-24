package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/tools"

	"slices"
)

// ServerConfig holds configuration for a single MCP server.
type ServerConfig struct {
	Command          string            `mapstructure:"command"`
	Args             []string          `mapstructure:"args"`
	Env              map[string]string `mapstructure:"env"`
	AutoApprove      bool              `mapstructure:"auto_approve"`
	AutoApproveTools []string          `mapstructure:"auto_approve_tools"`
	ReadOnlyTools    []string          `mapstructure:"read_only_tools"`
	Disabled         bool              `mapstructure:"disabled"`
}

// Manager orchestrates the lifecycle of multiple MCP servers.
type Manager struct {
	clients   map[string]*Client
	adapters  []*ToolAdapter
	configs   map[string]ServerConfig
	errors    []string
	closeOnce sync.Once
	closeErr  error
}

// NewManager creates a new MCP manager.
func NewManager() *Manager {
	return &Manager{
		clients: make(map[string]*Client),
		configs: make(map[string]ServerConfig),
	}
}

// Start connects to all enabled MCP servers, initializes them, and discovers tools.
func (m *Manager) Start(ctx context.Context, servers map[string]ServerConfig, workDir string) error {
	var errs []string

	for name, cfg := range servers {
		if cfg.Disabled {
			continue
		}

		transport, err := NewStdioTransport(cfg.Command, cfg.Args, cfg.Env, workDir)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", name, err))
			continue
		}

		client := NewClient(transport)
		if err := client.Initialize(ctx); err != nil {
			_ = client.Close()
			errs = append(errs, fmt.Sprintf("%s: init: %s", name, err))
			continue
		}

		toolDefs, err := client.ListTools(ctx)
		if err != nil {
			_ = client.Close()
			errs = append(errs, fmt.Sprintf("%s: list tools: %s", name, err))
			continue
		}

		m.clients[name] = client
		m.configs[name] = cfg

		for _, def := range toolDefs {
			adapter := NewToolAdapter(client, name, def)
			m.adapters = append(m.adapters, adapter)
		}
	}

	m.errors = errs
	if len(errs) > 0 && len(m.clients) == 0 {
		// All servers failed — return error.
		return errors.Errorf("all mcp servers failed: %s", strings.Join(errs, "; "))
	}
	// Partial failures are non-fatal; retrieve them via Errors().
	return nil
}

// RegisterTools adds all discovered MCP tool adapters to the registry.
func (m *Manager) RegisterTools(reg *tools.Registry) {
	for _, adapter := range m.adapters {
		reg.Register(adapter)
	}
}

// ConfirmTools returns the set of MCP tool names that require user confirmation.
func (m *Manager) ConfirmTools() map[string]bool {
	result := make(map[string]bool)
	for _, adapter := range m.adapters {
		cfg := m.configs[adapter.serverName]
		if cfg.AutoApprove {
			continue
		}
		if slices.Contains(cfg.AutoApproveTools, adapter.mcpName) {
			continue
		}
		result[adapter.registryName] = true
	}
	return result
}

// ReadOnlyTools returns the set of MCP tool names that are safe for parallel execution.
func (m *Manager) ReadOnlyTools() map[string]bool {
	result := make(map[string]bool)
	for _, adapter := range m.adapters {
		cfg := m.configs[adapter.serverName]
		if slices.Contains(cfg.ReadOnlyTools, adapter.mcpName) {
			result[adapter.registryName] = true
		}
	}
	return result
}

// ToolCount returns the number of discovered MCP tools.
func (m *Manager) ToolCount() int {
	return len(m.adapters)
}

// Errors returns any non-fatal errors from server startup.
func (m *Manager) Errors() []string {
	return m.errors
}

// Close gracefully shuts down all MCP server connections.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		var errs []string
		for name, client := range m.clients {
			if err := client.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %s", name, err))
			}
		}
		if len(errs) > 0 {
			m.closeErr = errors.Errorf("close errors: %s", strings.Join(errs, "; "))
		}
	})
	return m.closeErr
}

