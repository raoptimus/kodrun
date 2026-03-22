package config

import (
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration structure.
type Config struct {
	Ollama OllamaConfig `mapstructure:"ollama"`
	Agent  AgentConfig  `mapstructure:"agent"`
	Tools  ToolsConfig  `mapstructure:"tools"`
	Rules  RulesConfig  `mapstructure:"rules"`
}

// OllamaConfig holds Ollama API connection settings.
type OllamaConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Model       string        `mapstructure:"model"`
	Timeout     time.Duration `mapstructure:"timeout"`
	ContextSize int           `mapstructure:"context_size"`
}

// AgentConfig holds agent behavior settings.
type AgentConfig struct {
	MaxIterations int  `mapstructure:"max_iterations"`
	AutoFix       bool `mapstructure:"auto_fix"`
	AutoCommit    bool `mapstructure:"auto_commit"`
}

// ToolsConfig holds file tool restrictions.
type ToolsConfig struct {
	AllowedDirs       []string `mapstructure:"allowed_dirs"`
	ForbiddenPatterns []string `mapstructure:"forbidden_patterns"`
}

// RulesConfig holds paths for rules/docs/commands.
type RulesConfig struct {
	Dirs []string `mapstructure:"dirs"`
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
			MaxIterations: 50,
			AutoFix:       true,
			AutoCommit:    false,
		},
		Tools: ToolsConfig{
			AllowedDirs:       []string{"."},
			ForbiddenPatterns: []string{"*.env", ".git/**"},
		},
		Rules: RulesConfig{
			Dirs: []string{".goagent/rules", ".goagent/docs", ".goagent/commands"},
		},
	}
}

// Load reads configuration from files and environment variables.
func Load(configPath, workDir string) (Config, error) {
	cfg := Defaults()

	v := viper.New()
	v.SetConfigType("yaml")

	// Set defaults from struct
	v.SetDefault("ollama.base_url", cfg.Ollama.BaseURL)
	v.SetDefault("ollama.model", cfg.Ollama.Model)
	v.SetDefault("ollama.timeout", cfg.Ollama.Timeout)
	v.SetDefault("ollama.context_size", cfg.Ollama.ContextSize)
	v.SetDefault("agent.max_iterations", cfg.Agent.MaxIterations)
	v.SetDefault("agent.auto_fix", cfg.Agent.AutoFix)
	v.SetDefault("agent.auto_commit", cfg.Agent.AutoCommit)
	v.SetDefault("tools.allowed_dirs", cfg.Tools.AllowedDirs)
	v.SetDefault("tools.forbidden_patterns", cfg.Tools.ForbiddenPatterns)
	v.SetDefault("rules.dirs", cfg.Rules.Dirs)

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
			v.AddConfigPath(filepath.Join(home, ".config", "goagent"))
			v.SetConfigName("config")
		}
		_ = v.ReadInConfig()

		// Project-local config (higher priority)
		localV := viper.New()
		localV.SetConfigType("yaml")
		localV.SetConfigFile(filepath.Join(workDir, ".goagent.yaml"))
		if err := localV.ReadInConfig(); err == nil {
			if err := v.MergeConfigMap(localV.AllSettings()); err != nil {
				return cfg, err
			}
		}
	}

	// Env overrides
	v.SetEnvPrefix("GOAGENT")
	v.AutomaticEnv()

	if val := os.Getenv("GOAGENT_MODEL"); val != "" {
		v.Set("ollama.model", val)
	}
	if val := os.Getenv("GOAGENT_OLLAMA_URL"); val != "" {
		v.Set("ollama.base_url", val)
	}

	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}
