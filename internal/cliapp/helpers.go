/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/term"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tui"
)

// Constants used by helpers and the runtime layer.
const (
	CmdNameEdit       = "edit"
	CmdNameCodeReview = "code-review"
	PathDevNull       = "/dev/null"

	pollInterval    = 250 * time.Millisecond
	builtinCmdCount = 3 // number of built-in commands added beyond user commands
)

// SafeGo launches a goroutine tracked by wg with panic recovery.
// On panic, it sends an error event to the events channel (non-blocking).
// The send itself is also panic-guarded: if events is closed by an early
// shutdown path, the secondary panic is swallowed instead of propagating.
func SafeGo(wg *sync.WaitGroup, events chan agent.Event, fn func()) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				SafeSendError(events, fmt.Sprintf("background panic: %v", r))
			}
		}()
		fn()
	}()
}

// SafeSendError performs a non-blocking send of an error event and
// recovers from a secondary panic if events has been closed by an
// early-shutdown path. It is a best-effort report — losing the message
// is preferable to crashing the whole process.
func SafeSendError(events chan agent.Event, msg string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("kodrun: dropped error event on closed channel", "msg", msg, "recover", r)
		}
	}()
	select {
	case events <- agent.Event{Type: agent.EventError, Message: msg}:
	default:
	}
}

// RestoreTerminal disables mouse reporting and exits alt screen via raw ANSI sequences.
// Used as a safety net when bubbletea cannot clean up (panic, SIGTERM, etc.).
func RestoreTerminal() {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return
	}
	// Disable mouse reporting modes (1000=basic, 1002=cell motion, 1003=all, 1006=SGR extended)
	fmt.Fprint(os.Stderr, "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l")
	// Exit alt screen
	fmt.Fprint(os.Stderr, "\x1b[?1049l")
	// Show cursor
	fmt.Fprint(os.Stderr, "\x1b[?25h")
}

// ExitWithCode exits with the given code. Extracted to avoid exitAfterDefer lint.
func ExitWithCode(code int) { os.Exit(code) }

// BuildCommandItems builds the slash-command palette shown in the TUI:
// merge built-in commands with user-defined ones and sort by name.
func BuildCommandItems(loader *rules.Loader) []tui.CommandItem {
	cmds := loader.Commands()
	items := make([]tui.CommandItem, 0, len(cmds)+builtinCmdCount)

	// Built-in commands
	items = append(items,
		tui.CommandItem{Name: "compact", Description: "Summarize conversation to free context"},
		tui.CommandItem{Name: CmdNameEdit, Description: "Switch to edit mode (with plan if available)"},
		tui.CommandItem{Name: "init", Description: "Create .kodrun/ starter structure"},
		tui.CommandItem{Name: "clear", Description: "Clear conversation context"},
		tui.CommandItem{Name: "diff", Description: "Show git diff (uncommitted changes)"},
		tui.CommandItem{Name: "resume", Description: "Resume last saved session"},
		tui.CommandItem{Name: "sessions", Description: "List saved sessions"},
		tui.CommandItem{Name: "reindex", Description: "Rebuild RAG index"},
		tui.CommandItem{Name: "rag", Description: "Show RAG index status"},
		tui.CommandItem{Name: "add_doc", Description: "Add a document to RAG index"},
		tui.CommandItem{Name: "orchestrate", Description: "Run Plan→Execute→Review pipeline"},
		tui.CommandItem{Name: CmdNameCodeReview, Description: "Parallel specialist code review: rules, idiomaticity, best practices, security, structure, architecture"},
		tui.CommandItem{Name: "model", Description: "Switch LLM model for this session"},
		tui.CommandItem{Name: "exit", Description: "Exit KodRun"},
	)

	for _, cmd := range cmds {
		items = append(items, tui.CommandItem{
			Name:        cmd.Name,
			Description: cmd.Description,
			Template:    cmd.Template,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

// GitDiff runs git diff and returns formatted output.
func GitDiff(ctx context.Context, workDir string, args []string) (string, error) {
	cmdArgs := []string{"diff", "--stat"}
	cmdArgs = append(cmdArgs, args...)

	// First get stat summary
	statCmd := exec.CommandContext(ctx, "git", cmdArgs...)
	statCmd.Dir = workDir
	var statOut bytes.Buffer
	statCmd.Stdout = &statOut
	statCmd.Stderr = &statOut
	if err := statCmd.Run(); err != nil {
		slog.Debug("git diff --stat failed", "error", err)
	}

	// Then get full diff
	fullArgs := []string{"diff"}
	fullArgs = append(fullArgs, args...)
	fullCmd := exec.CommandContext(ctx, "git", fullArgs...)
	fullCmd.Dir = workDir
	var fullOut bytes.Buffer
	fullCmd.Stdout = &fullOut
	fullCmd.Stderr = &fullOut
	if err := fullCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", errors.WithMessage(err, "git diff")
		}
	}

	stat := strings.TrimSpace(statOut.String())
	full := strings.TrimSpace(fullOut.String())

	if full == "" {
		return "", nil
	}

	// Format: stat summary + full diff (truncated to 32KB)
	var result strings.Builder
	if stat != "" {
		result.WriteString("## Summary\n```\n")
		result.WriteString(stat)
		result.WriteString("\n```\n\n")
	}
	result.WriteString("## Diff\n```diff\n")
	if len(full) > 32*1024 {
		result.WriteString(full[:32*1024])
		result.WriteString("\n... (truncated)")
	} else {
		result.WriteString(full)
	}
	result.WriteString("\n```")

	return result.String(), nil
}

// WaitForRAGReady blocks until the background RAG indexing flag clears or
// the timeout/context fires. It is a best-effort gate used by review
// commands so they do not query a half-built index right after startup.
func WaitForRAGReady(ctx context.Context, indexing *atomic.Bool, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for indexing.Load() {
		if ctx.Err() != nil {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

// FormatContext renders the agent message history in a compact textual form
// suitable for the TUI context viewer.
func FormatContext(history []llm.Message) string {
	var b strings.Builder
	for i, msg := range history {
		role := strings.ToUpper(msg.Role)
		b.WriteString(fmt.Sprintf("─── [%d] %s ───\n", i, role))
		content := msg.Content
		if content != "" {
			b.WriteString(content)
			b.WriteString("\n")
		}
		for _, tc := range msg.ToolCalls {
			b.WriteString(fmt.Sprintf("  → tool_call: %s(%v)\n", tc.Function.Name, tc.Function.Arguments))
		}
		if msg.ToolCallID != "" {
			b.WriteString(fmt.Sprintf("  tool_call_id: %s\n", msg.ToolCallID))
		}
	}
	b.WriteString(fmt.Sprintf("\nTotal messages: %d", len(history)))
	return b.String()
}

// CleanupLegacyLangDirs removes per-language sub-index directories left over
// from earlier kodrun versions that partitioned RAG by language. Safe and
// idempotent — missing directories are silently ignored.
func CleanupLegacyLangDirs(basePath string) {
	for _, lang := range []string{"go", "python", "jsts"} {
		_ = os.RemoveAll(filepath.Join(basePath, lang))
	}
}
