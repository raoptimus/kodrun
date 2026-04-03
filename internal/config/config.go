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

// ThinkingProvider returns the provider used by "thinking" roles
// (planner, reviewer, response classifier). Falls back to ChatProvider.
func (c *Config) ThinkingProvider() ProviderConfig {
	if c.Agent.ThinkingProvider != "" {
		if p, ok := c.Providers[c.Agent.ThinkingProvider]; ok {
			return p
		}
	}
	return c.ChatProvider()
}

// ExecutorProvider returns the provider used by the executor role.
// Falls back to ChatProvider.
func (c *Config) ExecutorProvider() ProviderConfig {
	if c.Agent.ExecutorProvider != "" {
		if p, ok := c.Providers[c.Agent.ExecutorProvider]; ok {
			return p
		}
	}
	return c.ChatProvider()
}

// ExtractorProvider returns the provider used by the extractor role for
// converting free-form planner output into a structured plan. Defaults to
// ChatProvider with Temperature=0 and Format="json" overlaid so that the same
// underlying model produces deterministic JSON without any user configuration.
func (c *Config) ExtractorProvider() ProviderConfig {
	var p ProviderConfig
	if c.Agent.ExtractorProvider != "" {
		if pp, ok := c.Providers[c.Agent.ExtractorProvider]; ok {
			p = pp
		} else {
			p = c.ChatProvider()
		}
	} else {
		p = c.ChatProvider()
	}
	// Default to deterministic JSON if the user did not override.
	if p.Format == "" {
		p.Format = "json"
	}
	// Temperature 0 is intended unless the user explicitly set something
	// non-zero in their dedicated extractor profile.
	if c.Agent.ExtractorProvider == "" {
		p.Temperature = 0
	}
	return p
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
//
// Temperature and Format are optional generation parameters used by roles that
// need different behaviour than the default chat profile. The extractor role,
// for instance, uses Temperature=0 and Format="json" to coerce structured
// output from the same underlying model.
type ProviderConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	Timeout     time.Duration `mapstructure:"timeout"`
	ContextSize int           `mapstructure:"context_size"`
	Temperature float64       `mapstructure:"temperature"`
	Format      string        `mapstructure:"format"`
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
	Provider          string `mapstructure:"provider"`
	ThinkingProvider  string `mapstructure:"thinking_provider"`
	ExecutorProvider  string `mapstructure:"executor_provider"`
	ExtractorProvider string `mapstructure:"extractor_provider"`
	MaxIterations     int    `mapstructure:"max_iterations"`
	MaxParallelTasks  int    `mapstructure:"max_parallel_tasks"`
	MaxReplans        int    `mapstructure:"max_replans"`
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
	// ProjectLanguage overrides automatic project-language detection.
	// Valid values: "go", "python", "jsts". Empty enables auto-detection.
	ProjectLanguage string `mapstructure:"project_language"`
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
//
// The embedding model and connection details come from the provider profile
// referenced by Provider — there is no per-RAG embedding_model field. To
// switch embedding model, define a dedicated profile in `providers:` and
// point `rag.provider` at it.
type RAGConfig struct {
	Provider     string   `mapstructure:"provider"`
	Enabled      bool     `mapstructure:"enabled"`
	IndexDirs    []string `mapstructure:"index_dirs"`
	ChunkSize    int      `mapstructure:"chunk_size"`
	ChunkOverlap int      `mapstructure:"chunk_overlap"`
	TopK         int      `mapstructure:"top_k"`
	IndexPath    string   `mapstructure:"index_path"`
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
			Provider:         "default",
			MaxIterations:    50,
			MaxWorkers:       4,
			MaxParallelTasks: 1,
			MaxReplans:       2,
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
			Enabled:      false,
			IndexDirs:    []string{"."},
			ChunkSize:    128,
			ChunkOverlap: 64,
			TopK:         5,
			IndexPath:    ".kodrun/rag_index",
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
	if c.Agent.ThinkingProvider != "" {
		if _, ok := c.Providers[c.Agent.ThinkingProvider]; !ok {
			return errors.Errorf("agent.thinking_provider %q not found in providers", c.Agent.ThinkingProvider)
		}
	}
	if c.Agent.ExecutorProvider != "" {
		if _, ok := c.Providers[c.Agent.ExecutorProvider]; !ok {
			return errors.Errorf("agent.executor_provider %q not found in providers", c.Agent.ExecutorProvider)
		}
	}
	if c.Agent.ExtractorProvider != "" {
		if _, ok := c.Providers[c.Agent.ExtractorProvider]; !ok {
			return errors.Errorf("agent.extractor_provider %q not found in providers", c.Agent.ExtractorProvider)
		}
	}
	if c.RAG.Enabled {
		ragProv, ok := c.Providers[c.RAG.Provider]
		if !ok {
			return errors.Errorf("rag.provider %q not found in providers", c.RAG.Provider)
		}
		if ragProv.Model == "" {
			return errors.Errorf("rag.provider %q has empty model — set providers.%s.model to your embedding model (e.g. nomic-embed-text)", c.RAG.Provider, c.RAG.Provider)
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
	if c.Agent.MaxParallelTasks <= 0 {
		c.Agent.MaxParallelTasks = 1
	}
	if c.Agent.MaxReplans < 0 {
		c.Agent.MaxReplans = 0
	}
	if c.Agent.MaxReplans == 0 {
		c.Agent.MaxReplans = 2
	}
	if c.Tools.MaxReadLines <= 0 {
		c.Tools.MaxReadLines = 500
	}
	if c.TUI.MaxHistory <= 0 {
		c.TUI.MaxHistory = 100
	}
	return nil
}
