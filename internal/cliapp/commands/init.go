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

	"github.com/urfave/cli/v3"

	"github.com/raoptimus/kodrun/internal/cliapp"
	"github.com/raoptimus/kodrun/internal/kodruninit"
)

// Init wires the `kodrun init` subcommand which scaffolds .kodrun/ via
// LLM-assisted scanning of the project.
func Init(f *cliapp.Flags) *cli.Command {
	return &cli.Command{
		Name:  "init",
		Usage: "Create .kodrun/ starter structure",
		Action: func(ctx context.Context, _ *cli.Command) error {
			cfg, err := cliapp.LoadConfig(ctx, f)
			if err != nil {
				return err
			}
			chatProv := cfg.ChatProvider()
			client := cliapp.NewLLMClient(&chatProv)
			fmt.Println("Scanning project and generating AGENTS.md via LLM...")
			res, err := kodruninit.Run(ctx, f.WorkDir, client, chatProv.Model)
			if err != nil {
				return err
			}
			for _, path := range res.Created {
				fmt.Println("  created", path)
			}
			fmt.Printf("Done: %d items created\n", len(res.Created))
			return nil
		},
	}
}
