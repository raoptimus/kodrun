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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_ThinkingProvider_ExplicitProvider_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat":  {Model: "chat-model"},
			"think": {Model: "think-model"},
		},
		Agent: AgentConfig{
			Provider:         "chat",
			ThinkingProvider: "think",
		},
	}

	got := cfg.ThinkingProvider()

	assert.Equal(t, "think-model", got.Model)
}

func TestConfig_ThinkingProvider_FallbackToChatProvider_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat": {Model: "chat-model"},
		},
		Agent: AgentConfig{
			Provider:         "chat",
			ThinkingProvider: "",
		},
	}

	got := cfg.ThinkingProvider()

	assert.Equal(t, "chat-model", got.Model)
}

func TestConfig_ThinkingProvider_InvalidProviderFallback_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat": {Model: "chat-model"},
		},
		Agent: AgentConfig{
			Provider:         "chat",
			ThinkingProvider: "nonexistent",
		},
	}

	got := cfg.ThinkingProvider()

	assert.Equal(t, "chat-model", got.Model)
}

func TestConfig_ExecutorProvider_ExplicitProvider_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat": {Model: "chat-model"},
			"exec": {Model: "exec-model"},
		},
		Agent: AgentConfig{
			Provider:         "chat",
			ExecutorProvider: "exec",
		},
	}

	got := cfg.ExecutorProvider()

	assert.Equal(t, "exec-model", got.Model)
}

func TestConfig_ExecutorProvider_FallbackToChatProvider_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat": {Model: "chat-model"},
		},
		Agent: AgentConfig{
			Provider:         "chat",
			ExecutorProvider: "",
		},
	}

	got := cfg.ExecutorProvider()

	assert.Equal(t, "chat-model", got.Model)
}

func TestConfig_ExtractorProvider_ExplicitProvider_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat":    {Model: "chat-model"},
			"extract": {Model: "extract-model", Temperature: 0.5, Format: "text"},
		},
		Agent: AgentConfig{
			Provider:          "chat",
			ExtractorProvider: "extract",
		},
	}

	got := cfg.ExtractorProvider()

	assert.Equal(t, "extract-model", got.Model)
	// User set Format explicitly, but default fills empty.
	// Format was "text", not empty — no override.
	assert.Equal(t, "text", got.Format)
	// Temperature is preserved because ExtractorProvider is set.
	assert.InDelta(t, 0.5, got.Temperature, 0.001)
}

func TestConfig_ExtractorProvider_FallbackDefaults_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat": {Model: "chat-model", Temperature: 0.7},
		},
		Agent: AgentConfig{
			Provider:          "chat",
			ExtractorProvider: "",
		},
	}

	got := cfg.ExtractorProvider()

	assert.Equal(t, "chat-model", got.Model)
	assert.Equal(t, "json", got.Format)
	// Temperature forced to 0 when ExtractorProvider is empty.
	assert.InDelta(t, 0.0, got.Temperature, 0.001)
}

func TestConfig_ExtractorProvider_EmptyFormatDefaultsToJSON_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"chat":    {Model: "chat-model"},
			"extract": {Model: "extract-model", Format: ""},
		},
		Agent: AgentConfig{
			Provider:          "chat",
			ExtractorProvider: "extract",
		},
	}

	got := cfg.ExtractorProvider()

	assert.Equal(t, "json", got.Format)
}

func TestConfig_Validate_MissingAgentProvider_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"local": {Model: "test"},
		},
		Agent: AgentConfig{
			Provider: "nonexistent",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent.provider")
}

func TestConfig_Validate_MissingThinkingProvider_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test"},
		},
		Agent: AgentConfig{
			Provider:         "main",
			ThinkingProvider: "missing",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "thinking_provider")
}

func TestConfig_Validate_MissingExecutorProvider_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test"},
		},
		Agent: AgentConfig{
			Provider:         "main",
			ExecutorProvider: "missing",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "executor_provider")
}

func TestConfig_Validate_MissingExtractorProvider_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test"},
		},
		Agent: AgentConfig{
			Provider:          "main",
			ExtractorProvider: "missing",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "extractor_provider")
}

func TestConfig_Validate_RAGProviderMissing_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test"},
		},
		Agent: AgentConfig{
			Provider: "main",
		},
		RAG: RAGConfig{
			Enabled:  true,
			Backend:  RAGBackendLocal,
			Provider: "missing",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rag.provider")
}

func TestConfig_Validate_RAGProviderEmptyModel_Failure(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main":    {Model: "test"},
			"rag-emb": {Model: ""},
		},
		Agent: AgentConfig{
			Provider: "main",
		},
		RAG: RAGConfig{
			Enabled:  true,
			Backend:  RAGBackendLocal,
			Provider: "rag-emb",
		},
	}

	err := cfg.Validate(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty model")
}

func TestConfig_Validate_AppliesDefaultsForInvalidValues_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test", ContextSize: 0, Timeout: 0},
		},
		Agent: AgentConfig{
			Provider:         "main",
			MaxIterations:    -1,
			MaxToolWorkers:   0,
			MaxParallelTasks: -5,
			MaxReplans:       -1,
		},
		Tools: ToolsConfig{
			MaxReadLines: 0,
		},
		TUI: TUIConfig{
			MaxHistory: -10,
		},
	}

	err := cfg.Validate(context.Background())

	require.NoError(t, err)
	assert.Equal(t, defaultContextSize, cfg.Providers["main"].ContextSize)
	assert.Equal(t, defaultProviderTimeout, cfg.Providers["main"].Timeout)
	assert.Equal(t, defaultMaxIterations, cfg.Agent.MaxIterations)
	assert.Equal(t, 1, cfg.Agent.MaxToolWorkers)
	assert.Equal(t, 1, cfg.Agent.MaxParallelTasks)
	assert.Equal(t, 2, cfg.Agent.MaxReplans)
	assert.Equal(t, 500, cfg.Tools.MaxReadLines)
	assert.Equal(t, 100, cfg.TUI.MaxHistory)
}

func TestConfig_Validate_PreservesValidValues_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"main": {Model: "test", ContextSize: 65536, Timeout: defaultProviderTimeout},
		},
		Agent: AgentConfig{
			Provider:         "main",
			MaxIterations:    25,
			MaxToolWorkers:   8,
			MaxParallelTasks: 4,
			MaxReplans:       5,
		},
		Tools: ToolsConfig{
			MaxReadLines: 1000,
		},
		TUI: TUIConfig{
			MaxHistory: 200,
		},
	}

	err := cfg.Validate(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 65536, cfg.Providers["main"].ContextSize)
	assert.Equal(t, 25, cfg.Agent.MaxIterations)
	assert.Equal(t, 8, cfg.Agent.MaxToolWorkers)
	assert.Equal(t, 4, cfg.Agent.MaxParallelTasks)
	assert.Equal(t, 5, cfg.Agent.MaxReplans)
	assert.Equal(t, 1000, cfg.Tools.MaxReadLines)
	assert.Equal(t, 200, cfg.TUI.MaxHistory)
}

func TestConfig_Migrate_CreatesDefaultProviderFromOllama_Successfully(t *testing.T) {
	cfg := Config{
		Ollama: OllamaConfig{
			BaseURL:     "http://myhost:11434",
			Model:       "llama3:8b",
			ContextSize: 16384,
		},
	}

	cfg.migrate(context.Background())

	require.Len(t, cfg.Providers, 1)
	p, ok := cfg.Providers["default"]
	require.True(t, ok)
	assert.Equal(t, "http://myhost:11434", p.BaseURL)
	assert.Equal(t, "llama3:8b", p.Model)
	assert.Equal(t, 16384, p.ContextSize)
	assert.Equal(t, "default", cfg.Agent.Provider)
	assert.Equal(t, "default", cfg.RAG.Provider)
}

func TestConfig_Migrate_SkipsWhenProvidersExist_Successfully(t *testing.T) {
	cfg := Config{
		Providers: map[string]ProviderConfig{
			"custom": {Model: "custom-model"},
		},
		Agent: AgentConfig{
			Provider: "custom",
		},
	}

	cfg.migrate(context.Background())

	require.Len(t, cfg.Providers, 1)
	_, ok := cfg.Providers["custom"]
	assert.True(t, ok)
}

func TestConfig_Validate_ContextSizeBoundary_Successfully(t *testing.T) {
	tests := []struct {
		name        string
		contextSize int
		want        int
	}{
		{
			name:        "below minimum resets to default",
			contextSize: 1023,
			want:        defaultContextSize,
		},
		{
			name:        "at minimum preserved",
			contextSize: 1024,
			want:        1024,
		},
		{
			name:        "above minimum preserved",
			contextSize: 1025,
			want:        1025,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Providers: map[string]ProviderConfig{
					"main": {Model: "test", ContextSize: tt.contextSize, Timeout: defaultProviderTimeout},
				},
				Agent: AgentConfig{Provider: "main"},
			}

			err := cfg.Validate(context.Background())

			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.Providers["main"].ContextSize)
		})
	}
}
