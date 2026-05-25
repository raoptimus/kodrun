/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import "github.com/urfave/cli/v3"

// Flags holds parsed top-level CLI flags. The struct lives in cliapp so
// every internal helper can take it by pointer without depending on the
// main package.
type Flags struct {
	Model     string
	WorkDir   string
	OllamaURL string
	NoTUI     bool
	NoFix     bool
	Config    string
	Verbose   bool
}

// CLIFlags returns the urfave/cli flag descriptors bound to f. Defining
// the slice here keeps cmd/kodrun/main.go free of flag plumbing details.
func CLIFlags(f *Flags) []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{Name: "model", Sources: cli.EnvVars("KODRUN_MODEL"), Destination: &f.Model, Usage: "Ollama model (overrides config)"},
		&cli.StringFlag{Name: "work-dir", Value: ".", Sources: cli.EnvVars("KODRUN_WORK_DIR"), Destination: &f.WorkDir, Usage: "Working directory"},
		&cli.StringFlag{Name: "ollama-url", Sources: cli.EnvVars("KODRUN_OLLAMA_URL"), Destination: &f.OllamaURL, Usage: "Ollama API URL (overrides config)"},
		&cli.BoolFlag{Name: "no-tui", Sources: cli.EnvVars("KODRUN_NO_TUI"), Destination: &f.NoTUI, Usage: "Plain stdout mode"},
		&cli.BoolFlag{Name: "no-fix", Destination: &f.NoFix, Usage: "Disable auto-fix"},
		&cli.StringFlag{Name: "config", Destination: &f.Config, Usage: "Config file path"},
		&cli.BoolFlag{Name: "verbose", Destination: &f.Verbose, Usage: "Verbose output"},
	}
}
