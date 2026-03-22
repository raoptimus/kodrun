package runner

import (
	"testing"
)

func TestParseErrors_Build(t *testing.T) {
	output := `./internal/agent/loop.go:45:10: undefined: Context
./internal/config/config.go:12:5: declared and not used: x`

	errors := ParseErrors(output)
	if len(errors) != 2 {
		t.Fatalf("expected 2 errors, got %d", len(errors))
	}

	if errors[0].File != "internal/agent/loop.go" {
		t.Errorf("file = %q, want %q", errors[0].File, "internal/agent/loop.go")
	}
	if errors[0].Line != 45 {
		t.Errorf("line = %d, want 45", errors[0].Line)
	}
	if errors[0].Col != 10 {
		t.Errorf("col = %d, want 10", errors[0].Col)
	}
}

func TestParseErrors_Test(t *testing.T) {
	output := `--- FAIL: TestAgentLoop (0.05s)
    loop_test.go:32: expected 3 iterations, got 2`

	errors := ParseErrors(output)
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}

	if errors[0].File != "loop_test.go" {
		t.Errorf("file = %q, want %q", errors[0].File, "loop_test.go")
	}
	if errors[0].Line != 32 {
		t.Errorf("line = %d, want 32", errors[0].Line)
	}
}

func TestParseErrors_Lint(t *testing.T) {
	output := `internal/ollama/client.go:12:1: exported function Chat should have comment (revive)`

	errors := ParseErrors(output)
	if len(errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errors))
	}

	if errors[0].File != "internal/ollama/client.go" {
		t.Errorf("file = %q, want %q", errors[0].File, "internal/ollama/client.go")
	}
}

func TestParseErrors_Empty(t *testing.T) {
	errors := ParseErrors("")
	if len(errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(errors))
	}
}

func TestAffectedFiles(t *testing.T) {
	errors := []ParsedError{
		{File: "a.go", Line: 1},
		{File: "b.go", Line: 5},
		{File: "a.go", Line: 10},
	}

	files := AffectedFiles(errors)
	if len(files) != 2 {
		t.Fatalf("expected 2 unique files, got %d", len(files))
	}
}
