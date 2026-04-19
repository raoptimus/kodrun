/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package config

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	pkgerrors "github.com/pkg/errors"
	"github.com/spf13/viper"
)

const (
	providerDefault = "default"

	// RAGBackendLocal is the default RAG backend using local in-memory vector
	// index with disk persistence.
	RAGBackendLocal = "local"
	// RAGBackendMuninn selects Muninn DB as the RAG backend.
	RAGBackendMuninn = "muninn"
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

// ProviderConfig describes a single LLM provider instance.
//
// Type selects the backend: "ollama" (default) or "openai" (for vllm and
// other OpenAI-compatible servers). APIKey is sent as a Bearer token when
// set (used by the "openai" backend).
//
// Temperature and Format are optional generation parameters used by roles that
// need different behaviour than the default chat profile. The extractor role,
// for instance, uses Temperature=0 and Format="json" to coerce structured
// output from the same underlying model.
type ProviderConfig struct {
	Type        string        `mapstructure:"type"`
	APIKey      string        `mapstructure:"api_key"`
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
	MaxToolWorkers    int    `mapstructure:"max_tool_workers"`
	AutoFix           bool   `mapstructure:"auto_fix"`
	AutoCommit        bool   `mapstructure:"auto_commit"`
	DefaultMode       string `mapstructure:"default_mode"`
	Think             bool   `mapstructure:"think"`
	Language          string `mapstructure:"language"`
	AutoCompact       bool   `mapstructure:"auto_compact"`
	Orchestrator      bool   `mapstructure:"orchestrator"`
	Review            bool   `mapstructure:"review"`
	PrefetchCode      bool   `mapstructure:"prefetch_code"`
	// ProjectLanguage overrides automatic project-language detection.
	// Valid values: "go", "python", "jsts". Empty enables auto-detection.
	ProjectLanguage string `mapstructure:"project_language"`

	// SpecialistTimeout caps wall time for a single review phase.
	// 0 means no deadline (only the HTTP client timeout applies).
	SpecialistTimeout time.Duration `mapstructure:"specialist_timeout"`
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
// Backend selects the storage engine: "local" (default, in-memory vector
// index with disk persistence) or "muninn" (Muninn DB server that handles
// embeddings internally).
//
// When Backend is "local", the embedding model and connection details come
// from the provider profile referenced by Provider. When Backend is
// "muninn", the Provider field is not required — Muninn handles embeddings
// on its own.
//
// Scope: RAG indexes project conventions only — files under
// `.kodrun/rules/`, `.kodrun/snippets/`, `.kodrun/docs/`, plus embedded
// language standards (e.g. Effective Go). Project source code is NOT
// indexed: chunked code snapshots go stale between reindexes and lead to
// reviewers citing code that no longer exists. Live `read_file` calls are
// the authoritative view of source files.
type RAGConfig struct {
	Provider string `mapstructure:"provider"`
	Enabled  bool   `mapstructure:"enabled"`
	// Backend selects the RAG storage engine: "local" (default) or "muninn".
	Backend string       `mapstructure:"backend"`
	Muninn  MuninnConfig `mapstructure:"muninn"`
	// Deprecated: kodrun no longer indexes project source code. Only
	// .kodrun/rules, .kodrun/snippets, .kodrun/docs and embedded language
	// standards are indexed. The field is kept so old configs continue to
	// parse, but it is ignored at runtime.
	IndexDirs []string `mapstructure:"index_dirs"`
	// Deprecated: no longer used — see IndexDirs.
	ExcludeDirs  []string `mapstructure:"exclude_dirs"`
	ChunkSize    int      `mapstructure:"chunk_size"`
	ChunkOverlap int      `mapstructure:"chunk_overlap"`
	// Deprecated: no longer used — see IndexDirs.
	MaxChunksPerFile int `mapstructure:"max_chunks_per_file"`
	TopK             int `mapstructure:"top_k"`
	// ReviewBudgetBytes caps the size of the RAG prefetch block injected
	// into /code-review prompts. 0 falls back to the built-in default.
	ReviewBudgetBytes int    `mapstructure:"review_budget_bytes"`
	IndexPath         string `mapstructure:"index_path"`
}

// MuninnConfig holds Muninn DB connection settings.
type MuninnConfig struct {
	URL   string `mapstructure:"url"`
	Vault string `mapstructure:"vault"`
}

// Default configuration values.
const (
	defaultOllamaTimeout   = 5 * time.Minute
	defaultContextSize     = 32768
	defaultMaxIterations   = 50
	defaultMaxToolWorkers  = 4
	defaultMaxReplans      = 2
	defaultSpecTimeout     = 5 * time.Minute
	defaultMaxReadLines    = 500
	defaultMaxRefSize      = 4096
	defaultRAGChunkSize    = 128
	defaultRAGChunkOverlap = 16
	defaultRAGMaxChunks    = 8
	defaultRAGTopK         = 5
	defaultRAGReviewBudget = 24 * 1024
	defaultMaxHistory      = 100
	minProviderContextSize = 1024
	defaultProviderTimeout = 5 * time.Minute
)

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Ollama: OllamaConfig{
			BaseURL:     "http://localhost:11434",
			Model:       "qwen3-coder:30b",
			Timeout:     defaultOllamaTimeout,
			ContextSize: defaultContextSize,
		},
		Agent: AgentConfig{
			Provider:          providerDefault,
			MaxIterations:     defaultMaxIterations,
			MaxToolWorkers:    defaultMaxToolWorkers,
			MaxParallelTasks:  1,
			MaxReplans:        defaultMaxReplans,
			AutoFix:           true,
			AutoCommit:        false,
			DefaultMode:       "plan",
			Think:             true,
			Language:          "en",
			AutoCompact:       true,
			SpecialistTimeout: defaultSpecTimeout,
		},
		Tools: ToolsConfig{
			AllowedDirs: []string{"."},
			ForbiddenPatterns: []string{
				"*.env", "*.pem", "*.key",
				"node_modules/**", "vendor/**",
				"*.exe", "*.bin", "*.o", "*.a", "*.so", "*.dylib",
			},
			MaxReadLines: defaultMaxReadLines,
		},
		Rules: RulesConfig{
			MaxRefSize: defaultMaxRefSize,
			UseTool:    true,
		},
		Snippets: SnippetsConfig{
			UseTool: true,
		},
		RAG: RAGConfig{
			Enabled:           false,
			Backend:           RAGBackendLocal,
			Muninn:            MuninnConfig{URL: "http://127.0.0.1:8475", Vault: "kodrun"},
			IndexDirs:         []string{"."},
			ExcludeDirs:       []string{".claude", ".git", "vendor", "node_modules", ".kodrun/rag_index"},
			ChunkSize:         defaultRAGChunkSize,
			ChunkOverlap:      defaultRAGChunkOverlap,
			MaxChunksPerFile:  defaultRAGMaxChunks,
			TopK:              defaultRAGTopK,
			ReviewBudgetBytes: defaultRAGReviewBudget,
			IndexPath:         ".kodrun/rag_index",
		},
		TUI: TUIConfig{
			MaxHistory: defaultMaxHistory,
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
	v.SetDefault("agent.max_tool_workers", cfg.Agent.MaxToolWorkers)
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
		home, homeErr := os.UserHomeDir()
		if homeErr == nil && home != "" {
			v.AddConfigPath(filepath.Join(home, ".config", "kodrun"))
			v.SetConfigName("config")
		}
		if err := v.ReadInConfig(); err != nil {
			// Global config is optional — ignore "not found" errors.
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				slog.Debug("failed to read global config", "error", err)
			}
		}

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
			providerDefault: {
				BaseURL:     c.Ollama.BaseURL,
				Model:       c.Ollama.Model,
				Timeout:     c.Ollama.Timeout,
				ContextSize: c.Ollama.ContextSize,
			},
		}
	}
	if c.Agent.Provider == "" {
		c.Agent.Provider = providerDefault
	}
	if c.RAG.Provider == "" {
		c.RAG.Provider = providerDefault
	}
	if c.RAG.Backend == "" {
		c.RAG.Backend = RAGBackendLocal
	}
}

// Validate checks that configuration values are within acceptable ranges.
func (c *Config) Validate(_ context.Context) error {
	// Validate provider references
	if _, ok := c.Providers[c.Agent.Provider]; !ok {
		return pkgerrors.Errorf("agent.provider %q not found in providers", c.Agent.Provider)
	}
	if c.Agent.ThinkingProvider != "" {
		if _, ok := c.Providers[c.Agent.ThinkingProvider]; !ok {
			return pkgerrors.Errorf("agent.thinking_provider %q not found in providers", c.Agent.ThinkingProvider)
		}
	}
	if c.Agent.ExecutorProvider != "" {
		if _, ok := c.Providers[c.Agent.ExecutorProvider]; !ok {
			return pkgerrors.Errorf("agent.executor_provider %q not found in providers", c.Agent.ExecutorProvider)
		}
	}
	if c.Agent.ExtractorProvider != "" {
		if _, ok := c.Providers[c.Agent.ExtractorProvider]; !ok {
			return pkgerrors.Errorf("agent.extractor_provider %q not found in providers", c.Agent.ExtractorProvider)
		}
	}
	if c.RAG.Enabled {
		switch c.RAG.Backend {
		case RAGBackendLocal:
			ragProv, ok := c.Providers[c.RAG.Provider]
			if !ok {
				return pkgerrors.Errorf("rag.provider %q not found in providers", c.RAG.Provider)
			}
			if ragProv.Model == "" {
				return pkgerrors.Errorf("rag.provider %q has empty model — set providers.%s.model to your embedding model (e.g. nomic-embed-text)", c.RAG.Provider, c.RAG.Provider)
			}
		case RAGBackendMuninn:
			if c.RAG.Muninn.URL == "" {
				return pkgerrors.New("rag.muninn.url is required when backend is \"muninn\"")
			}
		default:
			return pkgerrors.Errorf("rag.backend %q is not supported (use \"local\" or \"muninn\")", c.RAG.Backend)
		}
	}

	// Apply defaults to each provider
	for name, p := range c.Providers {
		if p.ContextSize < minProviderContextSize {
			p.ContextSize = defaultContextSize
		}
		if p.Timeout <= 0 {
			p.Timeout = defaultProviderTimeout
		}
		c.Providers[name] = p
	}

	if c.Agent.MaxIterations <= 0 {
		c.Agent.MaxIterations = defaultMaxIterations
	}
	if c.Agent.MaxToolWorkers <= 0 {
		c.Agent.MaxToolWorkers = 1
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
