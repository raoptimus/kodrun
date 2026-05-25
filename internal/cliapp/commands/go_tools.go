/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/cliapp"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/runner"
	"github.com/raoptimus/kodrun/internal/tools"
)

// maxFixAttempts caps the auto-fix loop. Three retries is enough for
// straightforward compile/lint errors without spending unbounded LLM time
// on intractable cases.
const maxFixAttempts = 3

// Build wires the `kodrun build` subcommand which runs `go build` and
// optionally invokes the auto-fix loop on failure.
func Build(f *cliapp.Flags) *cli.Command {
	return &cli.Command{
		Name:  "build",
		Usage: "Run go build with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, tools.NameGoBuild, cmd.Args().Slice(), f)
		},
	}
}

// Test wires the `kodrun test` subcommand.
func Test(f *cliapp.Flags) *cli.Command {
	return &cli.Command{
		Name:  "test",
		Usage: "Run go test with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, tools.NameGoTest, cmd.Args().Slice(), f)
		},
	}
}

// Lint wires the `kodrun lint` subcommand.
func Lint(f *cliapp.Flags) *cli.Command {
	return &cli.Command{
		Name:  "lint",
		Usage: "Run golangci-lint with auto-fix",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return runGoTool(ctx, tools.NameGoLint, cmd.Args().Slice(), f)
		},
	}
}

// runGoTool executes a Go tool (build/test/lint) through the registered
// tools.Tool, then runs the auto-fix loop when the tool reported failure
// and AutoFix is enabled.
func runGoTool(ctx context.Context, toolName string, args []string, f *cliapp.Flags) error {
	cfg, err := cliapp.LoadConfig(ctx, f)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	s := cliapp.SetupAgent(ctx, &cfg, rules.ScopeFix, f)
	ag, reg, mcpMgr := s.Agent, s.Registry, s.MCPManager
	if mcpMgr != nil {
		defer mcpMgr.Close()
	}

	output := agent.NewPlainOutput(os.Stdout)
	ag.SetEventHandler(output.Handle)

	params := map[string]any{}
	if len(args) > 0 {
		params["packages"] = strings.Join(args, " ")
	}

	result, err := reg.Execute(ctx, toolName, params)
	if err != nil {
		return err
	}

	fmt.Println(result.Output)

	exitCode, ok := result.Meta["exit_code"].(int)
	if !ok {
		exitCode = 0
	}
	success := exitCode == 0

	if !success && cfg.Agent.AutoFix && !f.NoFix {
		fmt.Println("\n[auto-fix] Attempting to fix errors...")
		chatProv := cfg.ChatProvider()
		client := cliapp.NewLLMClient(&chatProv)
		fixer := runner.NewFixer(ctx, client, chatProv.Model, reg, maxFixAttempts)
		fixed, err := fixer.Fix(ctx, toolName, result.Output, func(msg string) {
			fmt.Println(msg)
		})
		if err != nil {
			return errors.WithMessage(err, "auto-fix")
		}
		if fixed {
			fmt.Println("[auto-fix] Errors fixed successfully!")
		} else {
			fmt.Println("[auto-fix] Could not fix all errors")
		}
	}

	if !success {
		cliapp.ExitWithCode(1)
	}
	return nil
}
