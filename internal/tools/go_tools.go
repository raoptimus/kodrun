package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// goTool is the base for all Go command tools.
type goTool struct {
	workDir     string
	name        string
	description string
	command     string
	defaultArgs []string
	schema      ollama.JSONSchema
}

func (t *goTool) Name() string              { return t.name }
func (t *goTool) Description() string        { return t.description }
func (t *goTool) Schema() ollama.JSONSchema  { return t.schema }

func (t *goTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	args := make([]string, len(t.defaultArgs))
	copy(args, t.defaultArgs)

	if packages, ok := params["packages"].(string); ok && packages != "" {
		args = append(args, packages)
	} else if len(t.defaultArgs) > 0 && t.defaultArgs[len(t.defaultArgs)-1] != "tidy" {
		args = append(args, "./...")
	}

	if flags, ok := params["flags"].(string); ok && flags != "" {
		args = append(args, strings.Fields(flags)...)
	}

	if run, ok := params["run"].(string); ok && run != "" {
		args = append(args, "-run", run)
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
		if exitErr, ok := err.(*exec.ExitError); ok {
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

func goToolSchema(extraProps map[string]ollama.JSONSchema) ollama.JSONSchema {
	props := map[string]ollama.JSONSchema{
		"packages": {Type: "string", Description: "Go packages (default: ./...)"},
		"flags":    {Type: "string", Description: "Additional flags"},
	}
	for k, v := range extraProps {
		props[k] = v
	}
	return ollama.JSONSchema{
		Type:       "object",
		Properties: props,
	}
}

// NewGoBuildTool creates a go_build tool.
func NewGoBuildTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_build",
		description: "Run go build",
		command:     "go",
		defaultArgs: []string{"build"},
		schema:      goToolSchema(nil),
	}
}

// NewGoTestTool creates a go_test tool.
func NewGoTestTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_test",
		description: "Run go test",
		command:     "go",
		defaultArgs: []string{"test"},
		schema: goToolSchema(map[string]ollama.JSONSchema{
			"run": {Type: "string", Description: "Test name pattern (-run)"},
		}),
	}
}

// NewGoVetTool creates a go_vet tool.
func NewGoVetTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_vet",
		description: "Run go vet",
		command:     "go",
		defaultArgs: []string{"vet"},
		schema:      goToolSchema(nil),
	}
}

// NewGoFmtTool creates a go_fmt tool.
func NewGoFmtTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_fmt",
		description: "Run gofmt -w on files",
		command:     "gofmt",
		defaultArgs: []string{"-w"},
		schema: ollama.JSONSchema{
			Type: "object",
			Properties: map[string]ollama.JSONSchema{
				"path": {Type: "string", Description: "File or directory to format (default: .)"},
			},
		},
	}
}

// NewGoLintTool creates a go_lint tool.
func NewGoLintTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_lint",
		description: "Run golangci-lint",
		command:     "golangci-lint",
		defaultArgs: []string{"run"},
		schema: goToolSchema(map[string]ollama.JSONSchema{
			"config": {Type: "string", Description: "Path to lint config"},
		}),
	}
}

// NewGoModTidyTool creates a go_mod_tidy tool.
func NewGoModTidyTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_mod_tidy",
		description: "Run go mod tidy",
		command:     "go",
		defaultArgs: []string{"mod", "tidy"},
		schema: ollama.JSONSchema{
			Type:       "object",
			Properties: map[string]ollama.JSONSchema{},
		},
	}
}

// RegisterAllTools registers all built-in tools into a registry.
func RegisterAllTools(reg *Registry, workDir string, forbidden []string) {
	reg.Register(NewReadFileTool(workDir, forbidden))
	reg.Register(NewWriteFileTool(workDir, forbidden))
	reg.Register(NewEditFileTool(workDir, forbidden))
	reg.Register(NewListDirTool(workDir))
	reg.Register(NewFindFilesTool(workDir))
	reg.Register(NewGrepTool(workDir))
	reg.Register(NewDeleteFileTool(workDir, forbidden))
	reg.Register(NewCreateDirTool(workDir))
	reg.Register(NewMoveFileTool(workDir, forbidden))
	reg.Register(NewGoBuildTool(workDir))
	reg.Register(NewGoTestTool(workDir))
	reg.Register(NewGoVetTool(workDir))
	reg.Register(NewGoFmtTool(workDir))
	reg.Register(NewGoLintTool(workDir))
	reg.Register(NewGoModTidyTool(workDir))
	reg.Register(&BashTool{workDir: workDir})
}

// BashTool executes arbitrary shell commands.
type BashTool struct {
	workDir string
}

func (t *BashTool) Name() string        { return "bash" }
func (t *BashTool) Description() string  { return "Execute a shell command" }

func (t *BashTool) Schema() ollama.JSONSchema {
	return ollama.JSONSchema{
		Type: "object",
		Properties: map[string]ollama.JSONSchema{
			"command": {Type: "string", Description: "Shell command to execute"},
		},
		Required: []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return ToolResult{Error: "command is required", Success: false}, nil
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
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
			"duration":  fmt.Sprintf("%s", duration),
		},
	}, nil
}
