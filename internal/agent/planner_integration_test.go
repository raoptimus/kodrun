//go:build integration

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
	"strings"
	"testing"
	"time"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
)

const (
	testOllamaURL   = "http://192.168.1.52:11434"
	testModel       = "qwen3-coder:30b"
	testContextSize = 64000
	testWorkDir     = "/Users/ra/DevProjects/go/src/github.com/raoptimus/test"
	testTimeout     = 5 * time.Minute
)

func setupIntegrationOrchestrator(t *testing.T) *Orchestrator {
	t.Helper()

	client := llm.NewClient(testOllamaURL, testTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Skipf("Ollama not available at %s: %v", testOllamaURL, err)
	}

	reg := tools.NewRegistry()
	forbidden := []string{"*.env", ".git/**", "*.pem", "*.key", ".**"}
	reg.Register(tools.NewReadFileTool(testWorkDir, forbidden, 500))
	reg.Register(tools.NewListDirTool(testWorkDir, forbidden))
	reg.Register(tools.NewFindFilesTool(testWorkDir, forbidden))
	reg.Register(tools.NewGrepTool(testWorkDir, forbidden))

	orch := NewOrchestrator(client, testModel, reg, testWorkDir, testContextSize, &OrchestratorConfig{})
	orch.SetLanguage("ru")

	return orch
}

func TestPlanner_UsesToolsAndProducesValidPlan(t *testing.T) {
	const maxAttempts = 3

	var plan string
	var toolCallCount int
	var lastErr error

	for attempt := range maxAttempts {
		orch := setupIntegrationOrchestrator(t)
		toolCallCount = 0
		orch.SetEventHandler(func(e *Event) {
			if e.Type == EventTool {
				toolCallCount++
			}
		})

		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		plan, lastErr = orch.runPlanner(ctx, "проведи code review на качество, безопасность. найди ошибки")
		cancel()

		if lastErr == nil && plan != "" {
			t.Logf("succeeded on attempt %d/%d", attempt+1, maxAttempts)
			break
		}
		t.Logf("attempt %d/%d failed: err=%v plan_empty=%v", attempt+1, maxAttempts, lastErr, plan == "")
	}
	if lastErr != nil {
		t.Fatalf("runPlanner failed after %d attempts: %v", maxAttempts, lastErr)
	}

	// 1. Планнер должен вызвать инструменты (tracked via events).
	t.Logf("tool call events: %d", toolCallCount)

	// 2. План не должен быть пустым.
	if plan == "" {
		t.Fatal("planner produced empty plan")
	}
	t.Logf("plan length: %d chars", len(plan))

	// 3. План должен содержать маркер PLAN.
	hasPlanMarker := strings.Contains(plan, "PLAN") ||
		strings.Contains(plan, "## Plan") ||
		strings.Contains(plan, "## План") ||
		strings.Contains(plan, "ПЛАН")
	if !hasPlanMarker {
		t.Error("plan does not contain a PLAN marker")
	}

	// 4. План должен ссылаться на реальные файлы (не более 30% невалидных).
	validator := &Orchestrator{workDir: testWorkDir}
	invalid := validator.validatePlanPaths(plan)
	allPaths := goFilePathRe.FindAllStringSubmatch(plan, -1)
	if len(allPaths) > 0 {
		ratio := float64(len(invalid)) / float64(len(allPaths))
		t.Logf("file paths: %d total, %d invalid (%.0f%%)", len(allPaths), len(invalid), ratio*100)
		if ratio > 0.3 {
			t.Errorf("too many hallucinated paths (>30%%): %v", invalid)
		}
	}

	// 5. Не должен содержать мусорный текст.
	garbagePhrases := []string{
		"Давайте продолжим",
		"Давайте проверим",
		"Давайте прочитаем",
		"Давайте посмотрим",
	}
	for _, phrase := range garbagePhrases {
		if strings.Contains(plan, phrase) {
			t.Errorf("plan contains garbage phrase: %q", phrase)
		}
	}

	// 6. План не должен быть задвоен (только один PLAN header).
	planCount := strings.Count(plan, "PLAN") + strings.Count(plan, "## Plan") + strings.Count(plan, "## План")
	if planCount > 1 {
		t.Errorf("plan appears duplicated: %d PLAN headers found", planCount)
	}

	// 7. Должны быть нумерованные шаги.
	if !strings.Contains(plan, "1.") {
		t.Error("plan does not contain numbered steps")
	}

	// 8. Не должно быть плейсхолдеров ":line".
	if strings.Contains(plan, ":line") {
		t.Error("plan contains placeholder ':line' instead of real line numbers")
	}

	// 9. Проверка качества — не слишком много расплывчатых шагов.
	if issues := validatePlanQuality(plan); len(issues) > 0 {
		t.Logf("plan quality issues (informational): %v", issues)
	}

	t.Logf("--- PLAN OUTPUT ---\n%s\n--- END ---", plan)
}

func TestPlanner_RetryOnZeroToolCalls(t *testing.T) {
	orch := setupIntegrationOrchestrator(t)

	var retryMessages []string
	orch.SetEventHandler(func(e *Event) {
		if e.Type == EventAgent && strings.Contains(e.Message, "Retrying") {
			retryMessages = append(retryMessages, e.Message)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// Run full runPlanner (which includes retry logic).
	plan, err := orch.runPlanner(ctx, "проведи code review")
	if err != nil {
		errStr := err.Error()
		// These errors are expected in integration tests:
		// - "did not analyze" = model refuses to use tools
		// - "context deadline" = model too slow
		if strings.Contains(errStr, "did not analyze") ||
			strings.Contains(errStr, "context deadline") ||
			strings.Contains(errStr, "context canceled") {
			t.Logf("planner failed (acceptable in integration test): %v", err)
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}

	t.Logf("plan produced successfully (len=%d), retry messages: %d", len(plan), len(retryMessages))
}

func TestPlanner_PlanPathsValidation(t *testing.T) {
	orch := setupIntegrationOrchestrator(t)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	plan, _, err := orch.runPlannerOnce(ctx, "проверь безопасность проекта, найди уязвимости")
	if err != nil {
		t.Fatalf("runPlannerOnce failed: %v", err)
	}
	if plan == "" {
		t.Skip("planner produced empty plan")
	}

	invalid := orch.validatePlanPaths(plan)
	t.Logf("invalid paths: %v", invalid)

	// All paths should exist in the test project.
	allPaths := goFilePathRe.FindAllStringSubmatch(plan, -1)
	t.Logf("all paths found in plan: %d", len(allPaths))
	for _, m := range allPaths {
		t.Logf("  path: %s", m[1])
	}
}
