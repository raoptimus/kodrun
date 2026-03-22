package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/raoptimus/go-agent/internal/agent"
	"github.com/raoptimus/go-agent/internal/config"
	"github.com/raoptimus/go-agent/internal/goagentinit"
	"github.com/raoptimus/go-agent/internal/ollama"
	"github.com/raoptimus/go-agent/internal/rules"
	"github.com/raoptimus/go-agent/internal/runner"
	"github.com/raoptimus/go-agent/internal/tools"
	"github.com/raoptimus/go-agent/internal/tui"
)

var (
	version = "dev"

	flagModel   string
	flagWorkDir string
	flagNoTUI   bool
	flagNoFix   bool
	flagConfig  string
	flagVerbose bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "goagent [task]",
		Short:   "GoAgent — CLI agent for Go code",
		Version: version,
		RunE:    runRoot,
	}

	rootCmd.Flags().StringVar(&flagModel, "model", "", "Ollama model (overrides config)")
	rootCmd.Flags().StringVar(&flagWorkDir, "work-dir", ".", "Working directory")
	rootCmd.Flags().BoolVar(&flagNoTUI, "no-tui", false, "Plain stdout mode")
	rootCmd.Flags().BoolVar(&flagNoFix, "no-fix", false, "Disable auto-fix")
	rootCmd.Flags().StringVar(&flagConfig, "config", "", "Config file path")
	rootCmd.Flags().BoolVar(&flagVerbose, "verbose", false, "Verbose output")

	// Subcommands
	rootCmd.AddCommand(buildCmd())
	rootCmd.AddCommand(testCmd())
	rootCmd.AddCommand(lintCmd())
	rootCmd.AddCommand(fixCmd())
	rootCmd.AddCommand(initCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func loadConfig() (config.Config, error) {
	cfg, err := config.Load(flagConfig, flagWorkDir)
	if err != nil {
		return cfg, err
	}
	if flagModel != "" {
		cfg.Ollama.Model = flagModel
	}
	if v := os.Getenv("GOAGENT_WORK_DIR"); v != "" {
		flagWorkDir = v
	}
	if os.Getenv("GOAGENT_NO_TUI") == "1" || os.Getenv("GOAGENT_NO_TUI") == "true" {
		flagNoTUI = true
	}
	return cfg, nil
}

func setupAgent(cfg config.Config) (*agent.Agent, *tools.Registry, *rules.Loader) {
	client := ollama.NewClient(cfg.Ollama.BaseURL, cfg.Ollama.Timeout)

	reg := tools.NewRegistry()
	tools.RegisterAllTools(reg, flagWorkDir, cfg.Tools.ForbiddenPatterns)

	loader := rules.NewLoader(cfg.Rules.Dirs)
	_ = loader.Load()

	ag := agent.New(client, cfg.Ollama.Model, reg, cfg.Agent.MaxIterations, flagWorkDir)
	return ag, reg, loader
}

func runRoot(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ag, reg, loader := setupAgent(cfg)
	_ = reg

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Check Ollama connectivity
	client := ollama.NewClient(cfg.Ollama.BaseURL, cfg.Ollama.Timeout)
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if err := client.Ping(pingCtx); err != nil {
		return fmt.Errorf("cannot connect to Ollama: %w\nMake sure 'ollama serve' is running", err)
	}

	rulesContent := loader.AllRulesContent(rules.ScopeCoding)

	// One-shot task from args
	if len(args) > 0 {
		task := strings.Join(args, " ")
		output := agent.NewPlainOutput(os.Stdout)
		ag.SetEventHandler(output.Handle)
		return ag.Run(ctx, task, rulesContent)
	}

	// Interactive mode
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	if !isTTY || flagNoTUI {
		// Plain stdin mode
		output := agent.NewPlainOutput(os.Stdout)
		ag.SetEventHandler(output.Handle)

		// Read from stdin
		buf := make([]byte, 1024*1024)
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		task := strings.TrimSpace(string(buf[:n]))
		if task == "" {
			return fmt.Errorf("no task provided")
		}
		return ag.Run(ctx, task, rulesContent)
	}

	// TUI mode
	events := make(chan agent.Event, 100)
	ag.SetEventHandler(func(e agent.Event) {
		events <- e
	})

	taskFn := func(input string) {
		_ = ag.Run(ctx, input, rulesContent)
	}

	model := tui.NewModel(cfg.Ollama.Model, taskFn, events)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}

	return nil
}

func buildCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "build [packages]",
		Short: "Run go build with auto-fix",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGoTool("go_build", args)
		},
	}
}

func testCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test [packages]",
		Short: "Run go test with auto-fix",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGoTool("go_test", args)
		},
	}
}

func lintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "lint [packages]",
		Short: "Run golangci-lint with auto-fix",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGoTool("go_lint", args)
		},
	}
}

func fixCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fix <file>",
		Short: "Fix issues in a specific file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			ag, _, loader := setupAgent(cfg)
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
			defer cancel()

			output := agent.NewPlainOutput(os.Stdout)
			ag.SetEventHandler(output.Handle)
			rulesContent := loader.AllRulesContent(rules.ScopeFix)

			task := fmt.Sprintf("Read file %s, find and fix all issues (bugs, style, errors). Run go_build and go_test after fixing.", args[0])
			return ag.Run(ctx, task, rulesContent)
		},
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create .goagent/ starter structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := goagentinit.Run(flagWorkDir); err != nil {
				return err
			}
			fmt.Println("Created .goagent/ structure")
			return nil
		},
	}
}

func runGoTool(toolName string, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ag, reg, loader := setupAgent(cfg)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

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

	if !result.Success && cfg.Agent.AutoFix && !flagNoFix {
		fmt.Println("\n[auto-fix] Attempting to fix errors...")
		client := ollama.NewClient(cfg.Ollama.BaseURL, cfg.Ollama.Timeout)
		fixer := runner.NewFixer(client, cfg.Ollama.Model, reg, 3)
		fixed, err := fixer.Fix(ctx, toolName, result.Output, func(msg string) {
			fmt.Println(msg)
		})
		if err != nil {
			return fmt.Errorf("auto-fix: %w", err)
		}
		if fixed {
			fmt.Println("[auto-fix] Errors fixed successfully!")
		} else {
			fmt.Println("[auto-fix] Could not fix all errors")
			rulesContent := loader.AllRulesContent(rules.ScopeFix)
			_ = rulesContent
		}
	}

	if !result.Success {
		os.Exit(1)
	}
	return nil
}
