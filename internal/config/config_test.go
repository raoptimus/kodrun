package config

import (
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
	if err := os.WriteFile(filepath.Join(dir, ".goagent.yaml"), []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load("", dir)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Ollama.Model != "test-model:latest" {
		t.Errorf("model = %q, want %q", cfg.Ollama.Model, "test-model:latest")
	}
	if cfg.Agent.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", cfg.Agent.MaxIterations)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("GOAGENT_MODEL", "env-model")
	t.Setenv("GOAGENT_OLLAMA_URL", "http://remote:11434")

	dir := t.TempDir()
	cfg, err := Load("", dir)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Ollama.Model != "env-model" {
		t.Errorf("model = %q, want %q", cfg.Ollama.Model, "env-model")
	}
	if cfg.Ollama.BaseURL != "http://remote:11434" {
		t.Errorf("base_url = %q, want %q", cfg.Ollama.BaseURL, "http://remote:11434")
	}
}
