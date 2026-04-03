package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Ollama.BaseURL != "http://localhost:11434" {
		t.Errorf("base_url = %q, want %q", cfg.Ollama.BaseURL, "http://localhost:11434")
	}
	if cfg.Ollama.Timeout != 5*time.Minute {
		t.Errorf("timeout = %v, want %v", cfg.Ollama.Timeout, 5*time.Minute)
	}
	if cfg.Agent.MaxIterations != 50 {
		t.Errorf("max_iterations = %d, want 50", cfg.Agent.MaxIterations)
	}
	if !cfg.Agent.AutoFix {
		t.Error("auto_fix should be true by default")
	}
	if !cfg.Snippets.UseTool {
		t.Error("snippets.use_tool should be true by default")
	}
	if cfg.Agent.Provider != "default" {
		t.Errorf("agent.provider = %q, want %q", cfg.Agent.Provider, "default")
	}
}

func TestLoad_ProjectConfig(t *testing.T) {
	dir := t.TempDir()
	configContent := `
ollama:
  model: "test-model:latest"
  timeout: 60s
agent:
  max_iterations: 10
`
	if err := os.WriteFile(filepath.Join(dir, ".kodrun.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(context.Background(), "", dir)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Ollama.Model != "test-model:latest" {
		t.Errorf("model = %q, want %q", cfg.Ollama.Model, "test-model:latest")
	}
	if cfg.Agent.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", cfg.Agent.MaxIterations)
	}

	// Legacy ollama config should be migrated to providers
	p := cfg.ChatProvider()
	if p.Model != "test-model:latest" {
		t.Errorf("ChatProvider().Model = %q, want %q", p.Model, "test-model:latest")
	}
	if p.Timeout != 60*time.Second {
		t.Errorf("ChatProvider().Timeout = %v, want %v", p.Timeout, 60*time.Second)
	}
}

func TestLoad_Providers(t *testing.T) {
	dir := t.TempDir()
	configContent := `
providers:
  gpu-server:
    base_url: "http://gpu:11434"
    model: "qwen3-coder:30b"
    timeout: 10m
    context_size: 65536
  local:
    base_url: "http://localhost:11434"
    model: "nomic-embed-text"
    timeout: 2m
    context_size: 8192

agent:
  provider: gpu-server

rag:
  enabled: true
  provider: local
`
	if err := os.WriteFile(filepath.Join(dir, ".kodrun.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(context.Background(), "", dir)
	if err != nil {
		t.Fatal(err)
	}

	chatP := cfg.ChatProvider()
	if chatP.BaseURL != "http://gpu:11434" {
		t.Errorf("ChatProvider().BaseURL = %q, want %q", chatP.BaseURL, "http://gpu:11434")
	}
	if chatP.Model != "qwen3-coder:30b" {
		t.Errorf("ChatProvider().Model = %q, want %q", chatP.Model, "qwen3-coder:30b")
	}
	if chatP.ContextSize != 65536 {
		t.Errorf("ChatProvider().ContextSize = %d, want 65536", chatP.ContextSize)
	}

	ragP := cfg.RAGProvider()
	if ragP.BaseURL != "http://localhost:11434" {
		t.Errorf("RAGProvider().BaseURL = %q, want %q", ragP.BaseURL, "http://localhost:11434")
	}
	if ragP.Model != "nomic-embed-text" {
		t.Errorf("RAGProvider().Model = %q, want %q", ragP.Model, "nomic-embed-text")
	}
}

func TestLoad_Providers_InvalidReference(t *testing.T) {
	dir := t.TempDir()
	configContent := `
providers:
  local:
    base_url: "http://localhost:11434"
    model: "test"

agent:
  provider: nonexistent
`
	if err := os.WriteFile(filepath.Join(dir, ".kodrun.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(context.Background(), "", dir)
	if err == nil {
		t.Fatal("expected error for invalid provider reference, got nil")
	}
}

func TestLoad_LegacyBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	configContent := `
ollama:
  base_url: "http://myserver:11434"
  model: "llama3:8b"
  timeout: 3m
  context_size: 16384
`
	if err := os.WriteFile(filepath.Join(dir, ".kodrun.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(context.Background(), "", dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should have exactly one provider "default" migrated from ollama
	if len(cfg.Providers) != 1 {
		t.Fatalf("len(providers) = %d, want 1", len(cfg.Providers))
	}
	p, ok := cfg.Providers["default"]
	if !ok {
		t.Fatal("expected 'default' provider")
	}
	if p.BaseURL != "http://myserver:11434" {
		t.Errorf("provider.BaseURL = %q, want %q", p.BaseURL, "http://myserver:11434")
	}
	if p.Model != "llama3:8b" {
		t.Errorf("provider.Model = %q, want %q", p.Model, "llama3:8b")
	}
	if p.ContextSize != 16384 {
		t.Errorf("provider.ContextSize = %d, want 16384", p.ContextSize)
	}
}
