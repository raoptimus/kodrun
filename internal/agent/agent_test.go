/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/raoptimus/kodrun/internal/llm"
	ollamabackend "github.com/raoptimus/kodrun/internal/llm/ollama"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tools"
)

func TestAgent_SendPassesNumCtxAndRules(t *testing.T) {
	workDir := t.TempDir()

	// Create AGENTS.md
	agentsMD := "# Test Project\nThis is a test project for KodRun.\n"
	if err := os.WriteFile(filepath.Join(workDir, "AGENTS.md"), []byte(agentsMD), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create rules directory and a rule file
	rulesDir := filepath.Join(workDir, ".kodrun", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ruleContent := "---\npriority: high\nscope: coding\n---\nAlways use context.Context as the first parameter.\n"
	if err := os.WriteFile(filepath.Join(rulesDir, "style.md"), []byte(ruleContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// File content that the tool call will write
	mainGoContent := "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.ListenAndServe(\":8080\", nil)\n}\n"

	// Mock Ollama server
	var mu sync.Mutex
	var captured []llm.ChatRequest

	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}

		var req llm.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		captured = append(captured, req)
		call := callCount
		callCount++
		mu.Unlock()

		w.Header().Set("Content-Type", "application/x-ndjson")

		resp := struct {
			Model           string      `json:"model"`
			Message         llm.Message `json:"message"`
			Done            bool        `json:"done"`
			PromptEvalCount int         `json:"prompt_eval_count,omitempty"`
			EvalCount       int         `json:"eval_count,omitempty"`
		}{
			Model:           "test-model",
			Done:            true,
			PromptEvalCount: 100,
			EvalCount:       50,
		}

		if call == 0 {
			// First call: return a tool call to write_file
			resp.Message = llm.Message{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID: "call_1",
						Function: llm.ToolCallFunc{
							Name: "write_file",
							Arguments: map[string]any{
								"path":    "main.go",
								"content": mainGoContent,
							},
						},
					},
				},
			}
		} else {
			// Second call: return text response to end the loop
			resp.Message = llm.Message{
				Role:    "assistant",
				Content: "Done. Created main.go with an HTTP server.",
			}
		}

		line, _ := json.Marshal(resp)
		_, _ = w.Write(line)
		_, _ = w.Write([]byte("\n"))
	}))
	defer server.Close()

	// Build agent
	client := ollamabackend.New(server.URL, 30*time.Second)
	reg := tools.NewRegistry()
	reg.Register(tools.NewWriteFileTool(workDir, nil))

	contextSize := 32768
	ag := New(client, "test-model", reg, 10, workDir, contextSize)
	ag.SetMode(ModeEdit)
	ag.SetConfirmFunc(func(_ ConfirmPayload) ConfirmResult { return ConfirmResult{Action: ConfirmAllowOnce} })

	// Load rules
	loader := rules.NewLoader(workDir, 0)
	if err := loader.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ruleCatalog := loader.RuleCatalogString(context.Background(), rules.ScopeCoding, true)

	// Run agent
	ctx := context.Background()
	if err := ag.Run(ctx, "напиши HTTP-сервер в main.go", ruleCatalog); err != nil {
		t.Fatalf("agent.Run: %v", err)
	}

	// Assertions
	mu.Lock()
	defer mu.Unlock()

	if len(captured) != 2 {
		t.Fatalf("expected 2 requests to Ollama, got %d", len(captured))
	}

	// Check num_ctx in every request
	for i, req := range captured {
		v, ok := req.Options["num_ctx"]
		if !ok {
			t.Errorf("request %d: num_ctx not set in options", i)
			continue
		}
		numCtx, ok := v.(float64) // JSON numbers decode as float64
		if !ok {
			t.Errorf("request %d: num_ctx is %T, want float64", i, v)
			continue
		}
		if int(numCtx) != contextSize {
			t.Errorf("request %d: num_ctx = %d, want %d", i, int(numCtx), contextSize)
		}
	}

	// Check system prompt contains rules (they stay in system for model compliance)
	if len(captured[0].Messages) == 0 {
		t.Fatal("first request has no messages")
	}
	sysPrompt := captured[0].Messages[0].Content
	if captured[0].Messages[0].Role != "system" {
		t.Errorf("first message role = %q, want \"system\"", captured[0].Messages[0].Role)
	}
	if !strings.Contains(sysPrompt, "style") {
		t.Error("system prompt does not contain rule catalog entry for \"style\"")
	}
	if !strings.Contains(sysPrompt, "get_rule") {
		t.Error("system prompt does not contain get_rule instruction")
	}
	// AGENTS.md should NOT be in system prompt — it's project context in user message
	if strings.Contains(sysPrompt, "Test Project") {
		t.Error("system prompt should NOT contain AGENTS.md content — it belongs in project context")
	}

	// Check project context (AGENTS.md) is injected in first user message
	var userMsg llm.Message
	for _, msg := range captured[0].Messages {
		if msg.Role == "user" {
			userMsg = msg
			break
		}
	}
	if userMsg.Role != "user" {
		t.Fatal("no user message found in first request")
	}
	if !strings.Contains(userMsg.Content, "Test Project") {
		t.Error("first user message does not contain AGENTS.md content (\"Test Project\")")
	}
	if !strings.Contains(userMsg.Content, "напиши HTTP-сервер") {
		t.Error("first user message does not contain the original task")
	}

	// Check tools are passed
	if len(captured[0].Tools) == 0 {
		t.Error("no tools passed in first request")
	}

	// Check that file was created by write_file tool
	data, err := os.ReadFile(filepath.Join(workDir, "main.go"))
	if err != nil {
		t.Fatalf("main.go not created: %v", err)
	}
	if string(data) != mainGoContent {
		t.Errorf("main.go content mismatch:\ngot:  %q\nwant: %q", string(data), mainGoContent)
	}

	// Check second request contains tool result message
	hasToolMsg := false
	for _, msg := range captured[1].Messages {
		if msg.Role == "tool" {
			hasToolMsg = true
			break
		}
	}
	if !hasToolMsg {
		t.Error("second request does not contain a tool result message")
	}
}
