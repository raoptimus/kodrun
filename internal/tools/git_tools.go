/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/raoptimus/kodrun/internal/llm"
)

const maxGitOutputBytes = 16 * 1024

// gitStatusTool runs git status and shows the current branch.
type gitStatusTool struct {
	workDir string
}

func NewGitStatusTool(workDir string) *gitStatusTool {
	return &gitStatusTool{workDir: workDir}
}

func (t *gitStatusTool) Name() string        { return "git_status" }
func (t *gitStatusTool) Description() string { return "Show git status and current branch" }

func (t *gitStatusTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type:       "object",
		Properties: map[string]llm.JSONSchema{},
	}
}

func (t *gitStatusTool) Execute(ctx context.Context, _ map[string]any) (*ToolResult, error) {
	branchCmd := exec.CommandContext(ctx, "git", "branch", "--show-current")
	branchCmd.Dir = t.workDir
	var branchOut bytes.Buffer
	branchCmd.Stdout = &branchOut
	if err := branchCmd.Run(); err != nil {
		// Not a git repo or other error — proceed without branch info.
		branchOut.Reset()
	}

	statusCmd := exec.CommandContext(ctx, "git", "status", "--porcelain=v1")
	statusCmd.Dir = t.workDir
	var stdout, stderr bytes.Buffer
	statusCmd.Stdout = &stdout
	statusCmd.Stderr = &stderr

	start := time.Now()
	err := statusCmd.Run()
	duration := time.Since(start)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ToolResult{
				Output: stderr.String(),
				Meta:   map[string]any{"exit_code": exitErr.ExitCode(), "duration": duration.String()},
			}, nil
		}
		return nil, fmt.Errorf("git status: %w", err)
	}

	output := fmt.Sprintf("Branch: %s\n%s", strings.TrimSpace(branchOut.String()), stdout.String())

	return &ToolResult{
		Output: output,
		Meta:   map[string]any{"exit_code": 0, "duration": duration.String()},
	}, nil
}

// gitDiffTool runs git diff with optional staged/ref parameters.
type gitDiffTool struct {
	workDir string
}

func NewGitDiffTool(workDir string) *gitDiffTool {
	return &gitDiffTool{workDir: workDir}
}

func (t *gitDiffTool) Name() string { return "git_diff" }
func (t *gitDiffTool) Description() string {
	return "Show git diff (optionally staged or against a ref)"
}

func (t *gitDiffTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"staged": {Type: "string", Description: "Show staged changes", Enum: []string{"true", "false"}},
			"ref":    {Type: "string", Description: "Commit or branch ref to diff against"},
		},
	}
}

func (t *gitDiffTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	args := []string{"diff"}

	if staged := stringParam(params, "staged"); staged == boolTrue {
		args = append(args, "--staged")
	}

	if ref := stringParam(params, "ref"); ref != "" {
		args = append(args, ref)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ToolResult{
				Output: stderr.String(),
				Meta:   map[string]any{"exit_code": exitErr.ExitCode(), "duration": duration.String()},
			}, nil
		}
		return nil, fmt.Errorf("git diff: %w", err)
	}

	output := stdout.String()
	if len(output) > maxGitOutputBytes {
		output = output[:maxGitOutputBytes] + "\n... [truncated]"
	}

	return &ToolResult{
		Output: output,
		Meta:   map[string]any{"exit_code": 0, "duration": duration.String()},
	}, nil
}

// gitLogTool runs git log --oneline.
type gitLogTool struct {
	workDir string
}

func NewGitLogTool(workDir string) *gitLogTool {
	return &gitLogTool{workDir: workDir}
}

func (t *gitLogTool) Name() string        { return "git_log" }
func (t *gitLogTool) Description() string { return "Show recent git commits" }

func (t *gitLogTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"count": {Type: "string", Description: "Number of commits to show (default: 10)"},
			"path":  {Type: "string", Description: "File path to filter log"},
		},
	}
}

func (t *gitLogTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	count := "10"
	if c := stringParam(params, "count"); c != "" {
		if _, err := strconv.Atoi(c); err == nil {
			count = c
		}
	}

	args := []string{"log", "--oneline", "-n", count}

	if path := stringParam(params, "path"); path != "" {
		args = append(args, "--", path)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return &ToolResult{
				Output: stderr.String(),
				Meta:   map[string]any{"exit_code": exitErr.ExitCode(), "duration": duration.String()},
			}, nil
		}
		return nil, fmt.Errorf("git log: %w", err)
	}

	return &ToolResult{
		Output: stdout.String(),
		Meta:   map[string]any{"exit_code": 0, "duration": duration.String()},
	}, nil
}

// gitCommitTool stages files and creates a git commit.
type gitCommitTool struct {
	workDir string
}

func NewGitCommitTool(workDir string) *gitCommitTool {
	return &gitCommitTool{workDir: workDir}
}

func (t *gitCommitTool) Name() string        { return "git_commit" }
func (t *gitCommitTool) Description() string { return "Stage files and create a git commit" }

func (t *gitCommitTool) Schema() llm.JSONSchema {
	return llm.JSONSchema{
		Type: "object",
		Properties: map[string]llm.JSONSchema{
			"message": {Type: "string", Description: "Commit message"},
			"files":   {Type: "string", Description: "Space-separated files to stage, or \".\" for all"},
		},
		Required: []string{"message"},
	}
}

func (t *gitCommitTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	message := stringParam(params, "message")
	if message == "" {
		return nil, &ToolError{Msg: "message is required"}
	}

	files := stringParam(params, "files")
	if files == "" {
		files = "."
	}

	// Stage files.
	addArgs := append([]string{"add"}, strings.Fields(files)...)
	addCmd := exec.CommandContext(ctx, "git", addArgs...)
	addCmd.Dir = t.workDir

	var addStderr bytes.Buffer
	addCmd.Stderr = &addStderr

	if err := addCmd.Run(); err != nil {
		return nil, &ToolError{Msg: fmt.Sprintf("git add failed: %s", addStderr.String())}
	}

	// Commit.
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", message)
	commitCmd.Dir = t.workDir

	var stdout, stderr bytes.Buffer
	commitCmd.Stdout = &stdout
	commitCmd.Stderr = &stderr

	start := time.Now()
	err := commitCmd.Run()
	duration := time.Since(start)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			output := stdout.String()
			if stderr.Len() > 0 {
				if output != "" {
					output += "\n"
				}
				output += stderr.String()
			}
			return &ToolResult{
				Output: output,
				Meta:   map[string]any{"exit_code": exitErr.ExitCode(), "duration": duration.String()},
			}, nil
		}
		return nil, fmt.Errorf("git commit: %w", err)
	}

	return &ToolResult{
		Output: stdout.String(),
		Meta:   map[string]any{"exit_code": 0, "duration": duration.String()},
	}, nil
}
