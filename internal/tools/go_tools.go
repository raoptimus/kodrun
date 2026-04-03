package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
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
func (t *goTool) Description() string       { return t.description }
func (t *goTool) Schema() ollama.JSONSchema { return t.schema }

func (t *goTool) Execute(ctx context.Context, params map[string]any) (ToolResult, error) {
	args := make([]string, len(t.defaultArgs))
	copy(args, t.defaultArgs)

	if packages, ok := params["packages"].(string); ok && packages != "" {
		args = append(args, packages)
	} else if len(t.defaultArgs) > 0 && t.defaultArgs[len(t.defaultArgs)-1] != "tidy" {
		args = append(args, "./...")
	}

	if flags, ok := params["flags"].(string); ok && flags != "" {
		for _, f := range strings.Fields(flags) {
			if isForbiddenFlag(f) {
				return ToolResult{Error: fmt.Sprintf("flag %q is not allowed", f), Success: false}, nil
			}
			args = append(args, f)
		}
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

// dangerousPatterns lists shell command patterns that indicate destructive or risky operations.
var dangerousPatterns = []string{
	"rm -rf", "rm -r", "rm -f",
	"chmod", "chown",
	"curl", "wget",
	"kill", "pkill",
	"dd ", "mkfs", "fdisk",
	"sudo",
}

// IsDangerousCommand checks if a shell command contains dangerous patterns.
func IsDangerousCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	for _, p := range dangerousPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return strings.Contains(cmd, "> /etc/") || strings.Contains(cmd, ">> /etc/")
}

// forbiddenGoFlags lists flags that could execute arbitrary code via go toolchain.
var forbiddenGoFlags = []string{"-exec", "-toolexec", "-overlay"}

func isForbiddenFlag(flag string) bool {
	for _, f := range forbiddenGoFlags {
		if flag == f || strings.HasPrefix(flag, f+"=") {
			return true
		}
	}
	return false
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
		defaultArgs: []string{"build", "-o", ".build"},
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

// NewGoGetTool creates a go_get tool.
func NewGoGetTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_get",
		description: "Run go get to add or update a dependency",
		command:     "go",
		defaultArgs: []string{"get"},
		schema: ollama.JSONSchema{
			Type: "object",
			Properties: map[string]ollama.JSONSchema{
				"packages": {Type: "string", Description: "Package path(s) to install, e.g. github.com/pkg/errors@latest"},
			},
			Required: []string{"packages"},
		},
	}
}

// NewGoDocTool creates a go_doc tool.
func NewGoDocTool(workDir string) Tool {
	return &goTool{
		workDir:     workDir,
		name:        "go_doc",
		description: "Run go doc to view documentation for a package or symbol",
		command:     "go",
		defaultArgs: []string{"doc"},
		schema: ollama.JSONSchema{
			Type: "object",
			Properties: map[string]ollama.JSONSchema{
				"packages": {Type: "string", Description: "Package or symbol to get documentation for, e.g. fmt.Println or encoding/json.Decoder"},
				"flags":    {Type: "string", Description: "Additional flags, e.g. -all for full docs"},
			},
			Required: []string{"packages"},
		},
	}
}

// RegisterGoTools registers Go-specific tools (build/test/vet/fmt/lint/etc.).
func RegisterGoTools(reg *Registry, workDir string) {
	reg.Register(NewGoBuildTool(workDir))
	reg.Register(NewGoTestTool(workDir))
	reg.Register(NewGoVetTool(workDir))
	reg.Register(NewGoFmtTool(workDir))
	reg.Register(NewGoLintTool(workDir))
	reg.Register(NewGoModTidyTool(workDir))
	reg.Register(NewGoGetTool(workDir))
	reg.Register(NewGoDocTool(workDir))
}

// BashTool executes arbitrary shell commands.
type BashTool struct {
	workDir string
}

func (t *BashTool) Name() string        { return "bash" }
func (t *BashTool) Description() string { return "Execute a shell command" }

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
