package config

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"github.com/spf13/viper"
)

// Config is the root configuration structure.
type Config struct {
	Ollama    OllamaConfig               `mapstructure:"ollama"`
	Providers map[string]ProviderConfig  `mapstructure:"providers"`
	Agent     AgentConfig                `mapstructure:"agent"`
	Tools     ToolsConfig                `mapstructure:"tools"`
	Rules     RulesConfig                `mapstructure:"rules"`
	Snippets  SnippetsConfig             `mapstructure:"snippets"`
	RAG       RAGConfig                  `mapstructure:"rag"`
	MCP       map[string]MCPServerConfig `mapstructure:"mcp"`
	TUI       TUIConfig                  `mapstructure:"tui"`
}

// TUIConfig holds TUI-related settings.
type TUIConfig struct {
	MaxHistory int `mapstructure:"max_history"`
}

// ChatProvider returns the provider config for chat/agent use.
func (c *Config) ChatProvider() ProviderConfig {
	return c.Providers[c.Agent.Provider]
}

// RAGProvider returns the provider config for RAG embedding.
func (c *Config) RAGProvider() ProviderConfig {
	return c.Providers[c.RAG.Provider]
}

// MCPServerConfig holds configuration for a single MCP server.
type MCPServerConfig struct {
	Command          string            `mapstructure:"command"`
	Args             []string          `mapstructure:"args"`
	Env              map[string]string `mapstructure:"env"`
	AutoApprove      bool              `mapstructure:"auto_approve"`
	AutoApproveTools []string          `mapstructure:"auto_approve_tools"`
	ReadOnlyTools    []string          `mapstructure:"read_only_tools"`
	Disabled         bool              `mapstructure:"disabled"`
}

// ProviderConfig describes a single LLM provider instance (currently only Ollama).
type ProviderConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	Timeout     time.Duration `mapstructure:"timeout"`
	ContextSize int           `mapstructure:"context_size"`
}

// OllamaConfig holds Ollama API connection settings (legacy, kept for backward compatibility).
type OllamaConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	Timeout     time.Duration `mapstructure:"timeout"`
	ContextSize int           `mapstructure:"context_size"`
}

// AgentConfig holds agent behavior settings.
type AgentConfig struct {
	Provider      string `mapstructure:"provider"`
	MaxIterations int    `mapstructure:"max_iterations"`
	MaxWorkers    int    `mapstructure:"max_workers"`
	AutoFix       bool   `mapstructure:"auto_fix"`
	AutoCommit    bool   `mapstructure:"auto_commit"`
	DefaultMode   string `mapstructure:"default_mode"`
	Think         bool   `mapstructure:"think"`
	Language      string `mapstructure:"language"`
	AutoCompact   bool   `mapstructure:"auto_compact"`
	Orchestrator  bool   `mapstructure:"orchestrator"`
	Review        bool   `mapstructure:"review"`
	PrefetchCode  bool   `mapstructure:"prefetch_code"`
}

// ToolsConfig holds file tool restrictions.
type ToolsConfig struct {
	AllowedDirs       []string `mapstructure:"allowed_dirs"`
	ForbiddenPatterns []string `mapstructure:"forbidden_patterns"`
	MaxReadLines      int      `mapstructure:"max_read_lines"`
}

// RulesConfig holds settings for rules, commands, and docs.
type RulesConfig struct {
	MaxRefSize int  `mapstructure:"max_ref_size"`
	UseTool    bool `mapstructure:"use_tool"`
}

// SnippetsConfig holds settings for project snippets.
type SnippetsConfig struct {
	UseTool bool `mapstructure:"use_tool"`
}

// RAGConfig holds RAG (Retrieval-Augmented Generation) settings.
type RAGConfig struct {
	Provider       string   `mapstructure:"provider"`
	Enabled        bool     `mapstructure:"enabled"`
	EmbeddingModel string   `mapstructure:"embedding_model"`
	IndexDirs      []string `mapstructure:"index_dirs"`
	ChunkSize      int      `mapstructure:"chunk_size"`
	ChunkOverlap   int      `mapstructure:"chunk_overlap"`
	TopK           int      `mapstructure:"top_k"`
	IndexPath      string   `mapstructure:"index_path"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Ollama: OllamaConfig{
			BaseURL:     "http://localhost:11434",
			Model:       "qwen3-coder:30b",
			Timeout:     5 * time.Minute,
			ContextSize: 32768,
		},
		Agent: AgentConfig{
			Provider:      "default",
			MaxIterations: 50,
			MaxWorkers:    4,
			AutoFix:       true,
			AutoCommit:    false,
			DefaultMode:   "plan",
			Think:         true,
			Language:      "en",
			AutoCompact:   true,
		},
		Tools: ToolsConfig{
			AllowedDirs:       []string{"."},
			ForbiddenPatterns: []string{
				"*.env", "*.pem", "*.key",
				"node_modules/**", "vendor/**",
				"*.exe", "*.bin", "*.o", "*.a", "*.so", "*.dylib",
			},
			MaxReadLines:      500,
		},
		Rules: RulesConfig{
			MaxRefSize: 4096,
			UseTool:    true,
		},
		Snippets: SnippetsConfig{
			UseTool: true,
		},
		RAG: RAGConfig{
			Enabled:        false,
			EmbeddingModel: "nomic-embed-text",
			IndexDirs:      []string{"."},
			ChunkSize:      128,
			ChunkOverlap:   64,
			TopK:           5,
			IndexPath:      ".kodrun/rag_index",
		},
		TUI: TUIConfig{
			MaxHistory: 100,
		},
	}
}

// Load reads configuration from files and environment variables.
func Load(ctx context.Context, configPath, workDir string) (Config, error) {
	cfg := Defaults()

	v := viper.New()
	v.SetConfigType("yaml")

	// Set defaults from struct
	v.SetDefault("ollama.base_url", cfg.Ollama.BaseURL)
	v.SetDefault("ollama.model", cfg.Ollama.Model)
	v.SetDefault("ollama.timeout", cfg.Ollama.Timeout)
	v.SetDefault("ollama.context_size", cfg.Ollama.ContextSize)
	v.SetDefault("agent.max_iterations", cfg.Agent.MaxIterations)
	v.SetDefault("agent.max_workers", cfg.Agent.MaxWorkers)
	v.SetDefault("agent.auto_fix", cfg.Agent.AutoFix)
	v.SetDefault("agent.auto_commit", cfg.Agent.AutoCommit)
	v.SetDefault("agent.default_mode", cfg.Agent.DefaultMode)
	v.SetDefault("agent.think", cfg.Agent.Think)
	v.SetDefault("tools.allowed_dirs", cfg.Tools.AllowedDirs)
	v.SetDefault("tools.forbidden_patterns", cfg.Tools.ForbiddenPatterns)
	v.SetDefault("tools.max_read_lines", cfg.Tools.MaxReadLines)
	v.SetDefault("rules.max_ref_size", cfg.Rules.MaxRefSize)
	v.SetDefault("tui.max_history", cfg.TUI.MaxHistory)

	// Explicit config file
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return cfg, err
		}
	} else {
		// Global config
		home, _ := os.UserHomeDir()
		if home != "" {
			v.AddConfigPath(filepath.Join(home, ".config", "kodrun"))
			v.SetConfigName("config")
		}
		_ = v.ReadInConfig()

		// Project-local config (higher priority)
		localV := viper.New()
		localV.SetConfigType("yaml")
		localV.SetConfigFile(filepath.Join(workDir, ".kodrun.yaml"))
		if err := localV.ReadInConfig(); err == nil {
			if err := v.MergeConfigMap(localV.AllSettings()); err != nil {
				return cfg, err
			}
		}
	}

	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	cfg.migrate(ctx)

	if err := cfg.Validate(ctx); err != nil {
		return cfg, err
	}

	return cfg, nil
}

// migrate converts legacy ollama config to providers format.
func (c *Config) migrate(_ context.Context) {
	if len(c.Providers) == 0 {
		c.Providers = map[string]ProviderConfig{
			"default": {
				BaseURL:     c.Ollama.BaseURL,
				Model:       c.Ollama.Model,
				Timeout:     c.Ollama.Timeout,
				ContextSize: c.Ollama.ContextSize,
			},
		}
	}
	if c.Agent.Provider == "" {
		c.Agent.Provider = "default"
	}
	if c.RAG.Provider == "" {
		c.RAG.Provider = "default"
	}
}

// Validate checks that configuration values are within acceptable ranges.
func (c *Config) Validate(_ context.Context) error {
	// Validate provider references
	if _, ok := c.Providers[c.Agent.Provider]; !ok {
		return errors.Errorf("agent.provider %q not found in providers", c.Agent.Provider)
	}
	if c.RAG.Enabled {
		if _, ok := c.Providers[c.RAG.Provider]; !ok {
			return errors.Errorf("rag.provider %q not found in providers", c.RAG.Provider)
		}
	}

	// Apply defaults to each provider
	for name, p := range c.Providers {
		if p.ContextSize < 1024 {
			p.ContextSize = 32768
		}
		if p.Timeout <= 0 {
			p.Timeout = 5 * time.Minute
		}
		c.Providers[name] = p
	}

	if c.Agent.MaxIterations <= 0 {
		c.Agent.MaxIterations = 50
	}
	if c.Agent.MaxWorkers <= 0 {
		c.Agent.MaxWorkers = 1
	}
	if c.Tools.MaxReadLines <= 0 {
		c.Tools.MaxReadLines = 500
	}
	if c.TUI.MaxHistory <= 0 {
		c.TUI.MaxHistory = 100
	}
	return nil
}
