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

// jstsTool is the base for JavaScript/TypeScript ecosystem command tools.
type jstsTool struct {
	workDir     string
	name        string
	description string
	command     string
	defaultArgs []string
	schema      ollama.JSONSchema
}

func (t *jstsTool) Name() string              { return t.name }
func (t *jstsTool) Description() string       { return t.description }
func (t *jstsTool) Schema() ollama.JSONSchema { return t.schema }

func (t *jstsTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
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

func jstsToolSchema(argsDesc string) ollama.JSONSchema {
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

// RegisterJSTSTools registers JavaScript/TypeScript ecosystem tools.
func RegisterJSTSTools(reg *Registry, workDir string) {
	reg.Register(&jstsTool{
		workDir: workDir, name: "npm_install",
		description: "Run npm install",
		command:     "npm", defaultArgs: []string{"install"},
		schema: jstsToolSchema("Package(s) or flags"),
	})
	reg.Register(&jstsTool{
		workDir: workDir, name: "npm_run",
		description: "Run an npm script (npm run <script>)",
		command:     "npm", defaultArgs: []string{"run"},
		schema: jstsToolSchema("Script name and arguments"),
	})
	reg.Register(&jstsTool{
		workDir: workDir, name: "npm_test",
		description: "Run npm test",
		command:     "npm", defaultArgs: []string{"test"},
		schema: jstsToolSchema("Additional arguments"),
	})
	reg.Register(&jstsTool{
		workDir: workDir, name: "tsc",
		description: "Run TypeScript compiler",
		command:     "tsc", defaultArgs: []string{"--noEmit"},
		schema: jstsToolSchema("tsc arguments, e.g. -p tsconfig.json"),
	})
	reg.Register(&jstsTool{
		workDir: workDir, name: "eslint",
		description: "Run eslint",
		command:     "eslint", defaultArgs: []string{"."},
		schema: jstsToolSchema("eslint arguments"),
	})
}
