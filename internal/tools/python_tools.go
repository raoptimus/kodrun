package tools

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
)

// pythonTool is the base for Python ecosystem command tools.
type pythonTool struct {
	workDir     string
	name        string
	description string
	command     string
	defaultArgs []string
	schema      ollama.JSONSchema
}

func (t *pythonTool) Name() string              { return t.name }
func (t *pythonTool) Description() string       { return t.description }
func (t *pythonTool) Schema() ollama.JSONSchema { return t.schema }

func (t *pythonTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	args := make([]string, len(t.defaultArgs))
	copy(args, t.defaultArgs)

	if extra, ok := params["args"].(string); ok && extra != "" {
		args = append(args, strings.Fields(extra)...)
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, t.command, args...)
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return ToolResult{Error: err.Error(), Success: false}, nil
		}
	}

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	return ToolResult{
		Output:  output,
		Success: exitCode == 0,
		Meta: map[string]any{
			"exit_code": exitCode,
			"duration":  duration.String(),
		},
	}, nil
}

func pythonToolSchema(argsDesc string) ollama.JSONSchema {
	if argsDesc == "" {
		argsDesc = "Additional arguments"
	}
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"args": {Type: "string", Description: argsDesc},
		},
	}
}

// RegisterPythonTools registers Python-specific tools.
func RegisterPythonTools(reg *Registry, workDir string) {
	reg.Register(&pythonTool{
		workDir: workDir, name: "python_run",
		description: "Run a Python script (python <args>)",
		command:     "python", defaultArgs: nil,
		schema: pythonToolSchema("Script path and arguments, e.g. main.py --flag"),
	})
	reg.Register(&pythonTool{
		workDir: workDir, name: "pytest",
		description: "Run pytest", command: "pytest", defaultArgs: nil,
		schema: pythonToolSchema("pytest arguments, e.g. -k test_name -q"),
	})
	reg.Register(&pythonTool{
		workDir: workDir, name: "pip_install",
		description: "Install a Python package via pip",
		command:     "pip", defaultArgs: []string{"install"},
		schema: pythonToolSchema("Package(s) to install"),
	})
	reg.Register(&pythonTool{
		workDir: workDir, name: "ruff",
		description: "Run ruff linter/formatter",
		command:     "ruff", defaultArgs: []string{"check"},
		schema: pythonToolSchema("ruff arguments, e.g. --fix path/"),
	})
	reg.Register(&pythonTool{
		workDir: workDir, name: "black",
		description: "Run black formatter",
		command:     "black", defaultArgs: []string{"."},
		schema: pythonToolSchema("black arguments"),
	})
}
