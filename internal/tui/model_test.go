package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/raoptimus/kodrun/internal/agent"
)

func newTestModel(t *testing.T) Model {
	t.Helper()

	m := NewModel(
		"test-model",
		"v0.0.0-test",
		32768,
		func(string) {},
		func() {},
		make(chan agent.Event),
		nil,
		make(chan ConfirmRequest),
		make(chan PlanConfirmRequest),
		t.TempDir(),
		agent.ModeEdit,
		false,
		nil,
		nil,
		"en",
		100,
	)

	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	return model.(Model)
}

func TestMouseWheelScrollPreservesViewportOffset(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 30; i++ {
		m.addLog(fmt.Sprintf("line %d", i))
	}

	before := m.viewport.YOffset
	if before == 0 {
		t.Fatalf("expected viewport to start below top after adding logs")
	}

	model, _ := m.Update(tea.MouseMsg{
		X:      0,
		Y:      1,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	m = model.(Model)

	if got := m.viewport.YOffset; got >= before {
		t.Fatalf("expected wheel-up to move viewport up: before=%d after=%d", before, got)
	}
}

func TestMouseWheelOutsideViewportDoesNotScroll(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 30; i++ {
		m.addLog(fmt.Sprintf("line %d", i))
	}

	before := m.viewport.YOffset
	model, _ := m.Update(tea.MouseMsg{
		X:      0,
		Y:      m.viewport.Height + 1,
		Button: tea.MouseButtonWheelUp,
		Action: tea.MouseActionPress,
	})
	m = model.(Model)

	if got := m.viewport.YOffset; got != before {
		t.Fatalf("expected wheel outside viewport not to scroll: before=%d after=%d", before, got)
	}
}

func TestArrowKeysUseInputHistory(t *testing.T) {
	m := newTestModel(t)
	for i := 0; i < 30; i++ {
		m.addLog(fmt.Sprintf("line %d", i))
	}
	m.inputHistory = []string{"first", "second"}
	m.textinput.SetValue("draft")

	initialOffset := m.viewport.YOffset
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(Model)

	if got := m.textinput.Value(); got != "second" {
		t.Fatalf("expected input history on KeyUp with input focus, got %q", got)
	}
	if got := m.viewport.YOffset; got != initialOffset {
		t.Fatalf("expected viewport offset to stay unchanged with input focus, before=%d after=%d", initialOffset, got)
	}
}

func TestAltEnterInsertsNewline(t *testing.T) {
	m := newTestModel(t)
	m.textinput.SetValue("draft")

	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	m = model.(Model)

	if got := m.textinput.Value(); got != "draft\n" {
		t.Fatalf("expected modified enter to insert newline, got %q", got)
	}
}

func TestF2TogglesMouseCapture(t *testing.T) {
	m := newTestModel(t)

	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = model.(Model)
	if m.mouseEnabled {
		t.Fatal("expected mouse capture to be disabled after first F2")
	}
	if cmd == nil {
		t.Fatal("expected toggle to return a Bubble Tea command")
	}

	model, cmd = m.Update(tea.KeyMsg{Type: tea.KeyF2})
	m = model.(Model)
	if !m.mouseEnabled {
		t.Fatal("expected mouse capture to be enabled after second F2")
	}
	if cmd == nil {
		t.Fatal("expected toggle to return a Bubble Tea command")
	}
}

func TestRenderInputFullWidth(t *testing.T) {
	m := newTestModel(t)
	// Ensure textarea has focus and placeholder is visible.
	m.textinput.Focus()

	view := m.renderInput()
	lines := strings.Split(view, "\n")

	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w < m.width {
			stripped := ansi.Strip(line)
			t.Errorf("line %d: visible width %d < terminal width %d (stripped: %q)",
				i, w, m.width, stripped)
		}
	}
}

func TestRenderInputFullWidthWithText(t *testing.T) {
	m := newTestModel(t)
	m.textinput.Focus()
	m.textinput.SetValue("hello world")

	view := m.renderInput()
	lines := strings.Split(view, "\n")

	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w < m.width {
			stripped := ansi.Strip(line)
			t.Errorf("line %d: visible width %d < terminal width %d (stripped: %q)",
				i, w, m.width, stripped)
		}
	}
}

func TestRenderInputBackgroundCoversFullLine(t *testing.T) {
	m := newTestModel(t)
	m.textinput.Focus()

	view := m.renderInput()
	t.Logf("renderInput raw output:\n%s", fmt.Sprintf("%q", view))
	lines := strings.Split(view, "\n")

	bgCode := "\x1b[48;5;255m" // expected bg color ANSI code

	for i, line := range lines {
		// The last ANSI reset should NOT appear before the padding ends.
		// Check that bg is active at the end of the line (before final reset).
		// Simple check: the line should not have bare spaces after a reset.
		stripped := ansi.Strip(line)
		if len(stripped) == 0 {
			// Empty padding lines — check they have bg set.
			if !strings.Contains(line, bgCode) {
				t.Errorf("empty line %d: missing background ANSI code", i)
			}
			continue
		}

		// After the last visible char, remaining spaces must have bg active.
		// Find the last reset (\x1b[0m) — spaces after it would have no bg.
		lastReset := strings.LastIndex(line, "\x1b[0m")
		if lastReset == -1 {
			continue // no reset, bg is active throughout
		}
		// Check if there are bare spaces after the last reset.
		afterReset := line[lastReset+len("\x1b[0m"):]
		if len(strings.TrimRight(afterReset, " ")) == 0 && len(afterReset) > 0 {
			t.Errorf("line %d: found %d bare spaces after final ANSI reset (no background)",
				i, len(afterReset))
		}
	}
}

func setupConfirm(t *testing.T) (Model, chan agent.ConfirmResult) {
	t.Helper()
	m := newTestModel(t)
	resultCh := make(chan agent.ConfirmResult, 1)
	req := ConfirmRequest{
		Payload: agent.ConfirmPayload{
			Tool:    "bash",
			Args:    map[string]string{"command": "echo hello"},
			ArgKeys: []string{"command"},
		},
		Result: resultCh,
	}
	model, _ := m.Update(ConfirmMsg{Request: req})
	return model.(Model), resultCh
}

func TestConfirmMenuNavigation(t *testing.T) {
	m, _ := setupConfirm(t)

	if m.confirmIdx != 0 {
		t.Fatalf("expected initial confirmIdx=0, got %d", m.confirmIdx)
	}

	// Down
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(Model)
	if m.confirmIdx != 1 {
		t.Fatalf("expected confirmIdx=1 after down, got %d", m.confirmIdx)
	}

	// Down again
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = model.(Model)
	if m.confirmIdx != 2 {
		t.Fatalf("expected confirmIdx=2, got %d", m.confirmIdx)
	}

	// Up
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(Model)
	if m.confirmIdx != 1 {
		t.Fatalf("expected confirmIdx=1 after up, got %d", m.confirmIdx)
	}

	// Up past zero — should clamp
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(Model)
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = model.(Model)
	if m.confirmIdx != 0 {
		t.Fatalf("expected confirmIdx=0 clamped, got %d", m.confirmIdx)
	}
}

func TestConfirmMenuEnter(t *testing.T) {
	m, resultCh := setupConfirm(t)

	// Default index=0 → ConfirmAllowOnce
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case r := <-resultCh:
		if r.Action != agent.ConfirmAllowOnce {
			t.Fatalf("expected ConfirmAllowOnce, got %d", r.Action)
		}
	default:
		t.Fatal("no result received")
	}
}

func TestConfirmMenuEsc(t *testing.T) {
	m, resultCh := setupConfirm(t)

	m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	select {
	case r := <-resultCh:
		if r.Action != agent.ConfirmDeny {
			t.Fatalf("expected ConfirmDeny, got %d", r.Action)
		}
	default:
		t.Fatal("no result received")
	}
}

func TestConfirmMenuShortcuts(t *testing.T) {
	tests := []struct {
		key    string
		action agent.ConfirmAction
	}{
		{"1", agent.ConfirmAllowOnce},
		{"2", agent.ConfirmAllowSession},
		{"4", agent.ConfirmDeny},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			m, resultCh := setupConfirm(t)
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tt.key)})

			select {
			case r := <-resultCh:
				if r.Action != tt.action {
					t.Fatalf("key %q: expected action %d, got %d", tt.key, tt.action, r.Action)
				}
			default:
				t.Fatalf("key %q: no result received", tt.key)
			}
		})
	}
}

func TestConfirmMenuRenderedInView(t *testing.T) {
	m, _ := setupConfirm(t)

	view := m.View()
	stripped := ansi.Strip(view)

	if !strings.Contains(stripped, "1.") {
		t.Error("expected menu item '1.' in view")
	}
	if !strings.Contains(stripped, "2.") {
		t.Error("expected menu item '2.' in view")
	}
	if !strings.Contains(stripped, ">") {
		t.Error("expected '>' marker for selected item in view")
	}
}

func TestRenderInputFullWidthMultiline(t *testing.T) {
	m := newTestModel(t)
	m.textinput.Focus()
	m.textinput.SetValue("line one\nline two")
	m.resizeInput()

	view := m.renderInput()
	lines := strings.Split(view, "\n")

	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w < m.width {
			stripped := ansi.Strip(line)
			t.Errorf("line %d: visible width %d < terminal width %d (stripped: %q)",
				i, w, m.width, stripped)
		}
	}
}
