package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/raoptimus/go-agent/internal/agent"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	agentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	fixStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// TaskFunc is a function that runs the agent for a given input.
type TaskFunc func(input string)

// EventMsg wraps an agent event as a tea.Msg.
type EventMsg struct {
	Event agent.Event
}

// Model is the bubbletea model for the GoAgent TUI.
type Model struct {
	viewport  viewport.Model
	textinput textinput.Model
	logs      []string
	model     string
	taskFn    TaskFunc
	events    chan agent.Event
	ready     bool
	width     int
	height    int
	running   bool
}

// NewModel creates a new TUI model.
func NewModel(modelName string, taskFn TaskFunc, events chan agent.Event) Model {
	ti := textinput.New()
	ti.Placeholder = "Enter task or /command..."
	ti.Focus()

	return Model{
		textinput: ti,
		model:     modelName,
		taskFn:    taskFn,
		events:    events,
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.waitForEvent(),
	)
}

func (m Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return nil
		}
		return EventMsg{Event: event}
	}
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			input := m.textinput.Value()
			if input != "" {
				m.textinput.SetValue("")
				m.addLog(fmt.Sprintf("> %s", input))
				m.running = true
				go m.taskFn(input)
			}
		case tea.KeyCtrlL:
			m.logs = nil
			m.updateViewport()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 2
		footerHeight := 3
		contentHeight := m.height - headerHeight - footerHeight
		if !m.ready {
			m.viewport = viewport.New(m.width, contentHeight)
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = contentHeight
		}
		m.textinput.Width = m.width - 4
		m.updateViewport()
	case EventMsg:
		e := msg.Event
		switch e.Type {
		case agent.EventAgent:
			m.addLog(agentStyle.Render("[agent] " + e.Message))
		case agent.EventTool:
			status := "✓"
			if !e.Success {
				status = "✗"
			}
			m.addLog(toolStyle.Render(fmt.Sprintf("[tool]  %s %s", e.Tool, status)))
		case agent.EventFix:
			m.addLog(fixStyle.Render("[fix]   " + e.Message))
		case agent.EventError:
			m.addLog(errorStyle.Render(fmt.Sprintf("[error] %s: %s", e.Tool, e.Message)))
		case agent.EventDone:
			m.running = false
		}
		cmds = append(cmds, m.waitForEvent())
	}

	var cmd tea.Cmd
	m.textinput, cmd = m.textinput.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *Model) addLog(line string) {
	m.logs = append(m.logs, line)
	m.updateViewport()
}

func (m *Model) updateViewport() {
	m.viewport.SetContent(strings.Join(m.logs, "\n"))
	m.viewport.GotoBottom()
}

// View implements tea.Model.
func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	header := titleStyle.Render(fmt.Sprintf("─ GoAgent ─── %s ", m.model))

	statusText := "ready"
	if m.running {
		statusText = "working..."
	}
	footer := statusStyle.Render(fmt.Sprintf("─ %s ── Esc: exit ", statusText))

	return fmt.Sprintf("%s\n%s\n%s\n%s",
		header,
		m.viewport.View(),
		footer,
		m.textinput.View(),
	)
}
