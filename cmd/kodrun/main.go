/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/raoptimus/kodrun/internal/cliapp"
	"github.com/raoptimus/kodrun/internal/cliapp/commands"
	_ "github.com/raoptimus/kodrun/internal/llm/ollama"
	_ "github.com/raoptimus/kodrun/internal/llm/openai"
)

// version is the kodrun release tag. Declared as a var (not const) so the
// release pipeline can override it at link time via
// `-ldflags "-X main.version=..."`.
var version = "v1.4.0-beta"

func main() {
	defer func() {
		if r := recover(); r != nil {
			cliapp.RestoreTerminal()
			slog.Error("kodrun panic", "recover", r)
			os.Exit(1)
		}
	}()

	f := &cliapp.Flags{}

	app := &cli.Command{
		Name:    "kodrun",
		Usage:   "CLI agent for Go code",
		Version: version,
		Flags:   cliapp.CLIFlags(f),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cliapp.Run(ctx, cmd, f, version)
		},
		Commands: []*cli.Command{
			commands.Build(f), commands.Test(f), commands.Lint(f),
			commands.Fix(f), commands.Init(f),
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		cliapp.ExitWithCode(1)
	}
}
