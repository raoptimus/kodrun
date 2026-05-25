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
	"syscall"

	"github.com/pkg/errors"
	"github.com/urfave/cli/v3"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/cliapp"
	"github.com/raoptimus/kodrun/internal/rules"
)

// Fix wires the `kodrun fix <file>` subcommand: reads a single file and
// asks the agent to repair it (bugs, style, errors), then verifies via
// go_build / go_test.
func Fix(f *cliapp.Flags) *cli.Command {
	return &cli.Command{
		Name:  "fix",
		Usage: "Fix issues in a specific file",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() != 1 {
				return errors.New("fix requires exactly one file argument")
			}
			cfg, err := cliapp.LoadConfig(ctx, f)
			if err != nil {
				return err
			}
			ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer cancel()
			s := cliapp.SetupAgent(ctx, &cfg, rules.ScopeFix, f)
			ag, loader, mcpMgr := s.Agent, s.Loader, s.MCPManager
			if mcpMgr != nil {
				defer mcpMgr.Close()
			}

			output := agent.NewPlainOutput(os.Stdout)
			ag.SetEventHandler(output.Handle)
			ruleCatalog := loader.RuleCatalogString(ctx, rules.ScopeFix, cfg.Rules.UseTool)

			task := fmt.Sprintf("Read file %s, find and fix all issues (bugs, style, errors). Run go_build and go_test after fixing.", cmd.Args().First())
			return ag.Run(ctx, task, ruleCatalog)
		},
	}
}
