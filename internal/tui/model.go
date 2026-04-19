package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"

	"github.com/raoptimus/kodrun/internal/agent"
)

const (
	maxVisibleItems    = 5
	maxLogLines        = 10000 // trim logs to prevent unbounded memory growth
	inputPrompt        = " > "
	paddingConfirmNorm = 4
	paddingConfirmSel  = 2
	halfPageDivisor    = 2
	scrollStep         = 3
	menuPadding        = 2    // extra lines around menu options
	layoutExtraLines   = 5    // fixed layout lines (gaps, toolbar, etc.)
	logTrimDivisor     = 5    // trim 1/5 of logs when exceeding max
	percentMultiplier  = 100  // for percentage calculations
	diffPreviewLines   = 20   // max lines in diff preview
	kilo               = 1000 // 1K tokens
	kibi               = 1024 // 1Ki context units
	paddingLeftDefault = 4    // default PaddingLeft for styles

	spinnerFrameCount = 10 // number of frames in the braille spinner
	dividerLabelPad   = 2  // spaces around phase divider label
	dividerMinPad     = 4  // minimum total padding for phase divider
	dividerHalf       = 2  // divisor for centering divider label
)

var (
	titleStyle         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	agentStyle         = lipgloss.NewStyle()
	toolStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	toolBoldStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	fixStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	errorStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	confirmHeaderStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("4"))
	confirmNormal      = lipgloss.NewStyle().PaddingLeft(paddingConfirmNorm)
	confirmSelected    = lipgloss.NewStyle().PaddingLeft(paddingConfirmSel).Bold(true).Foreground(lipgloss.Color("2"))
	confirmHintStyle   = lipgloss.NewStyle().PaddingLeft(paddingLeftDefault).Foreground(lipgloss.Color("8"))
	augmentStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	tokenStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	systemStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	diffAddStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	diffDelStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	diffHunkStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	diffStatStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	toolbarStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Left-border accent characters for user/agent messages.
	userAccent  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("▎") + " "
	agentAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Render("▎") + " "

	completeBorder   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("8"))
	completeNormal   = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1)
	completeSelected = lipgloss.NewStyle().PaddingLeft(1).PaddingRight(1).Bold(true).Reverse(true)

	// spinnerFrames contains braille dot characters for animated spinner.
	spinnerFrames = [spinnerFrameCount]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// CommandItem describes a slash command available in autocomplete.
type CommandItem struct {
	Name        string
	Description string
	Template    string
}

// TaskFunc is a function that runs the agent for a given input.
type TaskFunc func(input string)

// CancelFunc cancels the currently running task.
type CancelFunc func()

// ContextFunc returns the current conversation context formatted as a string.
type ContextFunc func() string

// SetModeFn is a callback to set the agent mode and think flag.
type SetModeFn func(mode agent.Mode, think bool)

// confirmState tracks the sub-state of the confirmation prompt.
type confirmState int

const (
	confirmChoose confirmState = iota // Choosing Y/A/E/N
	confirmEdit                       // Typing augment text
)

// confirmOption describes one item in the confirm menu.
type confirmOption struct {
	labelKey string
	shortcut string
	action   agent.ConfirmAction
}

var confirmMenuOptions = []confirmOption{
	{labelKey: "confirm.opt.allow_once", shortcut: "1", action: agent.ConfirmAllowOnce},
	{labelKey: "confirm.opt.allow_session", shortcut: "2", action: agent.ConfirmAllowSession},
	{labelKey: "confirm.opt.edit", shortcut: "3", action: agent.ConfirmAugment},
	{labelKey: "confirm.opt.deny", shortcut: "4", action: agent.ConfirmDeny},
}

// ConfirmRequest represents a pending confirmation from the agent.
type ConfirmRequest struct {
	Payload agent.ConfirmPayload
	Result  chan agent.ConfirmResult
}

// ConfirmMsg wraps a ConfirmRequest as a tea.Msg.
type ConfirmMsg struct {
	Request ConfirmRequest
}

// planConfirmOption describes one item in the plan confirm menu.
type planConfirmOption struct {
	labelKey string
	shortcut string
	action   agent.PlanConfirmAction
}

var planConfirmMenuOptions = []planConfirmOption{
	{labelKey: "plan_confirm.auto_accept", shortcut: "1", action: agent.PlanAutoAccept},
	{labelKey: "plan_confirm.manual_approve", shortcut: "2", action: agent.PlanManualApprove},
	{labelKey: "plan_confirm.augment", shortcut: "3", action: agent.PlanAugment},
}

// PlanConfirmRequest represents a pending plan confirmation from the orchestrator.
type PlanConfirmRequest struct {
	Plan   string
	Result chan agent.PlanConfirmResult
}

// PlanConfirmMsg wraps a PlanConfirmRequest as a tea.Msg.
type PlanConfirmMsg struct {
	Request PlanConfirmRequest
}

// stepConfirmOption describes one item in the step confirm menu.
type stepConfirmOption struct {
	labelKey string
	shortcut string
	action   agent.StepConfirmAction
}

var stepConfirmMenuOptions = []stepConfirmOption{
	{labelKey: "step_confirm.allow", shortcut: "1", action: agent.StepAllow},
	{labelKey: "step_confirm.skip", shortcut: "2", action: agent.StepSkip},
	{labelKey: "step_confirm.deny", shortcut: "3", action: agent.StepDenyAll},
}

// StepConfirmRequest represents a pending step confirmation from the orchestrator.
type StepConfirmRequest struct {
	Description string
	Result      chan agent.StepConfirmAction
}

// StepConfirmMsg wraps a StepConfirmRequest as a tea.Msg.
type StepConfirmMsg struct {
	Request StepConfirmRequest
}

// EventMsg wraps an agent event as a tea.Msg.
type EventMsg struct {
	Event agent.Event
}

// tickMsg is emitted every second while running for timer updates.
type tickMsg time.Time

type focusTarget int

const (
	focusInput focusTarget = iota
	focusViewport
)

// Model is the bubbletea model for the KodRun TUI.
type Model struct {
	viewport    viewport.Model
	textinput   textarea.Model
	logs        []string
	model       string
	version     string
	contextSize int
	taskFn      TaskFunc
	cancelFn    CancelFunc
	events      chan agent.Event
	ready       bool
	width       int
	height      int
	running     bool
	workDir     string

	// RAG indexing progress (background). When ragProgressTotal > 0 the status
	// bar shows a percentage indicator. Reset to zero on completion.
	ragProgressDone  int
	ragProgressTotal int
	ragProgressLabel string

	commands     []CommandItem
	showComplete bool
	filtered     []CommandItem
	selectedIdx  int

	// Input history (persisted to .kodrun/history)
	inputHistory []string
	historyIdx   int    // -1 = current input, 0..len-1 = history position
	currentInput string // saved current input when navigating history

	// Confirmation
	confirmCh      chan ConfirmRequest
	pendingConfirm *ConfirmRequest
	confirmSt      confirmState
	confirmIdx     int // selected menu item (0-based)
	savedInput     string

	// Plan confirmation (3-option dialog)
	planConfirmCh      chan PlanConfirmRequest
	pendingPlanConfirm *PlanConfirmRequest
	planConfirmSt      confirmState
	planConfirmIdx     int

	// Step confirmation (per-step in DAG execution)
	stepConfirmCh      chan StepConfirmRequest
	pendingStepConfirm *StepConfirmRequest
	stepConfirmIdx     int

	// Group collapsing — legacy single active group (used by Analyze() in
	// the planner path and any caller that doesn't set GroupID).
	groupActive   bool
	groupTitle    string
	groupLogs     []string
	groupStartIdx int
	groupExpanded bool

	// Multi-group state: used when events carry a non-empty GroupID
	// (parallel specialist reviewers). Each group occupies a contiguous
	// range in m.logs which is spliced in place as new events arrive.
	groups            map[string]*groupState
	groupsExpandedAll bool

	// Timer and token tracking
	taskStart      time.Time
	pauseStart     time.Time     // when timer was paused (zero if not paused)
	pausedDuration time.Duration // accumulated paused time
	totalPrompt    int
	totalEval      int
	liveEval       int // tokens generated during current inference (reset on EventTokens)
	lastTkPerSec   float64

	// Context usage tracking
	contextUsed  int
	contextTotal int

	// Spinner animation frame index (cycles through spinnerFrames).
	spinnerFrame int

	// Block 6: observability fields populated by orchestrator events.
	phase       string
	cacheHits   int64
	cacheMisses int64
	replans     int

	// Mode and thinking
	mode      agent.Mode
	think     bool
	setModeFn SetModeFn

	// System prompt tokens
	systemPromptTokens int

	// Context dump (legacy, unused)
	contextFn ContextFunc

	// Transcript viewer: full tool output + reasoning.
	transcriptLogs []string
	transcriptMode bool

	focus focusTarget

	mouseEnabled bool

	maxHistory int

	locale *Locale

	// Markdown rendering
	mdRenderer *markdownRenderer

	// Number of log entries occupied by the header block.
	headerLineCount int
}

func (m *Model) defaultPlaceholder() string {
	if m.mode == agent.ModePlan {
		return m.locale.Get("placeholder.plan")
	}
	return m.locale.Get("placeholder.edit")
}

// NewModel creates a new TUI model.
func NewModel(modelName, version string, contextSize int, taskFn TaskFunc, cancelFn CancelFunc, events chan agent.Event, commands []CommandItem, confirmCh chan ConfirmRequest, planConfirmCh chan PlanConfirmRequest, stepConfirmCh chan StepConfirmRequest, workDir string, mode agent.Mode, think bool, setModeFn SetModeFn, contextFn ContextFunc, lang string, maxHistory int) Model {
	locale := NewLocale(lang)
	ti := textarea.New()
	if mode == agent.ModePlan {
		ti.Placeholder = locale.Get("placeholder.plan")
	} else {
		ti.Placeholder = locale.Get("placeholder.edit")
	}
	ti.Prompt = inputPrompt
	ti.ShowLineNumbers = false
	ti.EndOfBufferCharacter = ' '
	inputBg := lipgloss.Color("255")
	ti.FocusedStyle.Base = lipgloss.NewStyle().Background(inputBg)
	ti.FocusedStyle.Prompt = lipgloss.NewStyle().Background(inputBg).Foreground(lipgloss.Color("0")).Bold(true)
	ti.FocusedStyle.Placeholder = lipgloss.NewStyle().Background(inputBg).Foreground(lipgloss.Color("240"))
	ti.FocusedStyle.Text = lipgloss.NewStyle().Background(inputBg).Foreground(lipgloss.Color("0"))
	ti.FocusedStyle.CursorLine = lipgloss.NewStyle().Background(inputBg).Foreground(lipgloss.Color("0"))
	ti.BlurredStyle = ti.FocusedStyle
	ti.SetPromptFunc(1, func(line int) string {
		if line == 0 {
			return inputPrompt
		}
		return "   "
	})
	ti.CharLimit = 2000
	ti.SetHeight(1)
	ti.MaxHeight = 6
	ti.Focus()

	if maxHistory <= 0 {
		maxHistory = 100
	}
	history := LoadHistory(workDir, maxHistory)

	m := Model{
		textinput:     ti,
		model:         modelName,
		version:       version,
		contextSize:   contextSize,
		taskFn:        taskFn,
		cancelFn:      cancelFn,
		events:        events,
		commands:      commands,
		confirmCh:     confirmCh,
		planConfirmCh: planConfirmCh,
		stepConfirmCh: stepConfirmCh,
		workDir:       workDir,
		historyIdx:    -1,
		inputHistory:  history,
		mode:          mode,
		think:         think,
		setModeFn:     setModeFn,
		contextFn:     contextFn,
		focus:         focusInput,
		mouseEnabled:  true,
		maxHistory:    maxHistory,
		locale:        locale,
		mdRenderer:    &markdownRenderer{},
	}
	header := m.headerLines()
	m.logs = header
	m.headerLineCount = len(header)
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.textinput.Focus(),
		m.waitForEvent(),
		m.waitForConfirm(),
	}
	if m.planConfirmCh != nil {
		cmds = append(cmds, m.waitForPlanConfirm())
	}
	if m.stepConfirmCh != nil {
		cmds = append(cmds, m.waitForStepConfirm())
	}
	return tea.Batch(cmds...)
}

func (m *Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.events
		if !ok {
			return nil
		}
		return EventMsg{Event: event}
	}
}

// executeConfirmAction sends the given action and resets confirm state.
func (m *Model) executeConfirmAction(action agent.ConfirmAction) tea.Cmd {
	m.pendingConfirm.Result <- agent.ConfirmResult{Action: action}
	if !m.pauseStart.IsZero() {
		m.pausedDuration += time.Since(m.pauseStart)
		m.pauseStart = time.Time{}
	}
	m.pendingConfirm = nil
	m.focus = focusInput
	m.textinput.Focus()
	m.recalcViewport()
	return m.waitForConfirm()
}

// executeConfirmOption handles selection of a confirm menu option.
func (m *Model) executeConfirmOption(opt confirmOption) tea.Cmd {
	if opt.action == agent.ConfirmAugment {
		m.confirmSt = confirmEdit
		m.savedInput = m.textinput.Value()
		m.textinput.SetValue("")
		m.textinput.Placeholder = m.locale.Get("placeholder.constraint")
		m.textinput.Focus()
		m.addLog(augmentStyle.Render(m.locale.Get("confirm.enter_constraint")))
		m.recalcViewport()
		return nil
	}
	return m.executeConfirmAction(opt.action)
}

func (m Model) waitForConfirm() tea.Cmd {
	return func() tea.Msg {
		req, ok := <-m.confirmCh
		if !ok {
			return nil
		}
		return ConfirmMsg{Request: req}
	}
}

func (m Model) waitForPlanConfirm() tea.Cmd {
	return func() tea.Msg {
		req, ok := <-m.planConfirmCh
		if !ok {
			return nil
		}
		return PlanConfirmMsg{Request: req}
	}
}

// executePlanConfirmAction sends the given plan confirm action and resets state.
func (m *Model) executePlanConfirmAction(action agent.PlanConfirmAction) tea.Cmd {
	m.pendingPlanConfirm.Result <- agent.PlanConfirmResult{Action: action}
	if !m.pauseStart.IsZero() {
		m.pausedDuration += time.Since(m.pauseStart)
		m.pauseStart = time.Time{}
	}
	for _, opt := range planConfirmMenuOptions {
		if opt.action == action {
			m.addLog(confirmHeaderStyle.Render(fmt.Sprintf("  → %s", m.locale.Get(opt.labelKey))))
			break
		}
	}
	m.pendingPlanConfirm = nil
	m.focus = focusInput
	m.textinput.Focus()
	m.recalcViewport()
	return m.waitForPlanConfirm()
}

// executePlanConfirmOption handles selection of a plan confirm menu option.
func (m *Model) executePlanConfirmOption(opt planConfirmOption) tea.Cmd {
	if opt.action == agent.PlanAugment {
		m.planConfirmSt = confirmEdit
		m.savedInput = m.textinput.Value()
		m.textinput.SetValue("")
		m.textinput.Placeholder = m.locale.Get("placeholder.constraint")
		m.textinput.Focus()
		m.addLog(augmentStyle.Render(m.locale.Get("plan_confirm.enter_feedback")))
		m.recalcViewport()
		return nil
	}
	return m.executePlanConfirmAction(opt.action)
}

func (m Model) waitForStepConfirm() tea.Cmd {
	return func() tea.Msg {
		req, ok := <-m.stepConfirmCh
		if !ok {
			return nil
		}
		return StepConfirmMsg{Request: req}
	}
}

// executeStepConfirmAction sends the given step confirm action and resets state.
func (m *Model) executeStepConfirmAction(action agent.StepConfirmAction) tea.Cmd {
	m.pendingStepConfirm.Result <- action
	if !m.pauseStart.IsZero() {
		m.pausedDuration += time.Since(m.pauseStart)
		m.pauseStart = time.Time{}
	}
	for _, opt := range stepConfirmMenuOptions {
		if opt.action == action {
			m.addLog(confirmHeaderStyle.Render(fmt.Sprintf("  → %s", m.locale.Get(opt.labelKey))))
			break
		}
	}
	m.pendingStepConfirm = nil
	m.focus = focusInput
	m.textinput.Focus()
	m.recalcViewport()
	return m.waitForStepConfirm()
}

func tickEvery() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tickMsg:
		if m.running {
			m.spinnerFrame = (m.spinnerFrame + 1) % spinnerFrameCount
		}
		if m.running || m.ragProgressTotal > 0 {
			cmds = append(cmds, tickEvery())
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Handle confirmation prompt
		if m.pendingConfirm != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			switch m.confirmSt {
			case confirmChoose:
				switch msg.Type {
				case tea.KeyUp:
					if m.confirmIdx > 0 {
						m.confirmIdx--
					}
					return m, nil
				case tea.KeyDown:
					if m.confirmIdx < len(confirmMenuOptions)-1 {
						m.confirmIdx++
					}
					return m, nil
				case tea.KeyEnter:
					cmd := m.executeConfirmOption(confirmMenuOptions[m.confirmIdx])
					return m, cmd
				case tea.KeyEsc:
					cmd := m.executeConfirmAction(agent.ConfirmDeny)
					return m, cmd
				default:
					switch msg.String() {
					case "1":
						cmd := m.executeConfirmAction(agent.ConfirmAllowOnce)
						return m, cmd
					case "2":
						cmd := m.executeConfirmAction(agent.ConfirmAllowSession)
						return m, cmd
					case "3":
						cmd := m.executeConfirmOption(confirmMenuOptions[2])
						return m, cmd
					case "4":
						cmd := m.executeConfirmAction(agent.ConfirmDeny)
						return m, cmd
					}
				}
				return m, nil
			case confirmEdit:
				switch msg.Type {
				case tea.KeyEsc:
					m.textinput.SetValue(m.savedInput)
					m.textinput.Placeholder = m.defaultPlaceholder()
					m.confirmSt = confirmChoose
					return m, nil
				case tea.KeyEnter:
					text := strings.TrimSpace(m.textinput.Value())
					if text == "" {
						return m, nil
					}
					m.pendingConfirm.Result <- agent.ConfirmResult{Action: agent.ConfirmAugment, Augment: text}
					m.addLog(augmentStyle.Render(m.locale.Get("confirm.augment") + text))
					m.textinput.SetValue(m.savedInput)
					m.textinput.Placeholder = m.defaultPlaceholder()
					m.pendingConfirm = nil
					m.confirmSt = confirmChoose
					m.focus = focusInput
					m.textinput.Focus()
					return m, m.waitForConfirm()
				default:
					var cmd tea.Cmd
					m.textinput, cmd = m.textinput.Update(msg)
					m.resizeInput()
					return m, cmd
				}
			}
			return m, nil
		}

		// Handle plan confirmation prompt (3-option)
		if m.pendingPlanConfirm != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			switch m.planConfirmSt {
			case confirmChoose:
				switch msg.Type {
				case tea.KeyUp:
					if m.planConfirmIdx > 0 {
						m.planConfirmIdx--
					}
					return m, nil
				case tea.KeyDown:
					if m.planConfirmIdx < len(planConfirmMenuOptions)-1 {
						m.planConfirmIdx++
					}
					return m, nil
				case tea.KeyEnter:
					cmd := m.executePlanConfirmOption(planConfirmMenuOptions[m.planConfirmIdx])
					return m, cmd
				case tea.KeyEsc:
					cmd := m.executePlanConfirmAction(agent.PlanDeny)
					return m, cmd
				default:
					switch msg.String() {
					case "1":
						cmd := m.executePlanConfirmAction(agent.PlanAutoAccept)
						return m, cmd
					case "2":
						cmd := m.executePlanConfirmAction(agent.PlanManualApprove)
						return m, cmd
					case "3":
						cmd := m.executePlanConfirmOption(planConfirmMenuOptions[2])
						return m, cmd
					}
				}
				return m, nil
			case confirmEdit:
				switch msg.Type {
				case tea.KeyEsc:
					m.textinput.SetValue(m.savedInput)
					m.textinput.Placeholder = m.defaultPlaceholder()
					m.planConfirmSt = confirmChoose
					return m, nil
				case tea.KeyEnter:
					text := strings.TrimSpace(m.textinput.Value())
					if text == "" {
						return m, nil
					}
					m.pendingPlanConfirm.Result <- agent.PlanConfirmResult{Action: agent.PlanAugment, Augment: text}
					m.addLog(augmentStyle.Render(m.locale.Get("plan_confirm.feedback") + text))
					m.textinput.SetValue(m.savedInput)
					m.textinput.Placeholder = m.defaultPlaceholder()
					m.pendingPlanConfirm = nil
					m.planConfirmSt = confirmChoose
					m.focus = focusInput
					m.textinput.Focus()
					return m, m.waitForPlanConfirm()
				default:
					var cmd tea.Cmd
					m.textinput, cmd = m.textinput.Update(msg)
					m.resizeInput()
					return m, cmd
				}
			}
			return m, nil
		}

		// Handle step confirmation prompt (3-option: execute/skip/cancel)
		if m.pendingStepConfirm != nil {
			if msg.Type == tea.KeyCtrlC {
				return m, tea.Quit
			}
			switch msg.Type {
			case tea.KeyUp:
				if m.stepConfirmIdx > 0 {
					m.stepConfirmIdx--
				}
				return m, nil
			case tea.KeyDown:
				if m.stepConfirmIdx < len(stepConfirmMenuOptions)-1 {
					m.stepConfirmIdx++
				}
				return m, nil
			case tea.KeyEnter:
				cmd := m.executeStepConfirmAction(stepConfirmMenuOptions[m.stepConfirmIdx].action)
				return m, cmd
			case tea.KeyEsc:
				cmd := m.executeStepConfirmAction(agent.StepDenyAll)
				return m, cmd
			default:
				switch msg.String() {
				case "1":
					cmd := m.executeStepConfirmAction(agent.StepAllow)
					return m, cmd
				case "2":
					cmd := m.executeStepConfirmAction(agent.StepSkip)
					return m, cmd
				case "3":
					cmd := m.executeStepConfirmAction(agent.StepDenyAll)
					return m, cmd
				}
			}
			return m, nil
		}

		if m.showComplete {
			switch msg.Type {
			case tea.KeyUp:
				if m.selectedIdx > 0 {
					m.selectedIdx--
				}
				return m, nil
			case tea.KeyDown:
				if m.selectedIdx < len(m.filtered)-1 {
					m.selectedIdx++
				}
				return m, nil
			case tea.KeyTab, tea.KeyEnter:
				if msg.Alt {
					break
				}
				if len(m.filtered) > 0 {
					selected := m.filtered[m.selectedIdx]
					m.textinput.SetValue("/" + selected.Name + " ")
					m.textinput.CursorEnd()
					m.showComplete = false
				}
				return m, nil
			case tea.KeyEsc:
				m.showComplete = false
				return m, nil
			}
		}

		// Return focus to input on any key except viewport navigation keys.
		switch msg.Type {
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyCtrlU, tea.KeyCtrlD:
			// keep focusViewport
		default:
			m.focus = focusInput
		}

		switch msg.Type {
		case tea.KeyShiftTab:
			wasPlan := m.mode == agent.ModePlan
			if wasPlan {
				m.mode = agent.ModeEdit
				m.think = false
				m.textinput.Placeholder = m.locale.Get("placeholder.edit")
			} else {
				m.mode = agent.ModePlan
				m.think = true
				m.textinput.Placeholder = m.locale.Get("placeholder.plan")
			}
			if m.setModeFn != nil {
				m.setModeFn(m.mode, m.think)
			}
			if wasPlan {
				m.addLog(systemStyle.Render(m.locale.Get("status.switched_edit")))
			} else {
				m.addLog(systemStyle.Render(m.locale.Get("status.switched_plan")))
			}
			return m, nil
		case tea.KeyF2:
			m.mouseEnabled = !m.mouseEnabled
			if m.mouseEnabled {
				m.addLog(systemStyle.Render(m.locale.Get("status.mouse_on")))
				return m, tea.EnableMouseCellMotion
			}
			m.addLog(systemStyle.Render(m.locale.Get("status.mouse_off")))
			return m, tea.DisableMouse
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if m.running {
				// Cancel current task, don't quit
				if m.cancelFn != nil {
					m.cancelFn()
				}
				m.addLog(systemStyle.Render(m.locale.Get("status.cancelled")))
			}
			return m, nil
		case tea.KeyCtrlJ:
			// Ctrl+J inserts a newline (alternative to Shift+Enter).
			m.textinput.InsertRune('\n')
			m.resizeInput()
			return m, nil
		case tea.KeyEnter:
			if msg.Alt {
				m.textinput.InsertRune('\n')
				m.resizeInput()
				return m, nil
			}
			m.focus = focusInput
			input := strings.TrimSpace(m.textinput.Value())
			if input == "" {
				break
			}

			// Handle /exit locally
			if input == "/exit" || input == "/quit" {
				return m, tea.Quit
			}

			m.inputHistory = append(m.inputHistory, input)
			m.inputHistory = dedup(m.inputHistory)
			if len(m.inputHistory) > m.maxHistory {
				m.inputHistory = m.inputHistory[len(m.inputHistory)-m.maxHistory:]
			}
			m.historyIdx = -1
			m.currentInput = ""
			m.textinput.Reset()
			m.textinput.SetHeight(1)
			m.textinput.Focus()
			m.recalcViewport()
			m.addLog(userAccent + input)

			// Persist to file
			AppendHistory(m.workDir, input, m.maxHistory)

			// Don't start a new task if one is already running
			if m.running {
				m.addLog(systemStyle.Render(m.locale.Get("status.task_running")))
				return m, nil
			}

			m.running = true
			m.taskStart = time.Now()
			m.pauseStart = time.Time{}
			m.pausedDuration = 0
			m.totalPrompt = 0
			m.totalEval = 0
			m.liveEval = 0
			m.lastTkPerSec = 0
			m.recalcViewport()

			go func() {
				defer func() {
					if r := recover(); r != nil {
						select {
						case m.events <- agent.Event{Type: agent.EventError, Message: fmt.Sprintf("task panic: %v\n%s", r, debug.Stack())}:
						default:
						}
						select {
						case m.events <- agent.Event{Type: agent.EventDone}:
						default:
						}
					}
				}()
				m.taskFn(input)
			}()
			return m, tea.Batch(append(cmds, tickEvery())...)
		case tea.KeyUp:
			if m.focus == focusInput && len(m.inputHistory) > 0 && m.historyIdx < len(m.inputHistory)-1 {
				if m.historyIdx == -1 {
					m.currentInput = m.textinput.Value()
				}
				m.historyIdx++
				m.textinput.SetValue(m.inputHistory[len(m.inputHistory)-1-m.historyIdx])
				m.textinput.CursorEnd()
				return m, nil
			}
			return m, nil
		case tea.KeyDown:
			if m.focus == focusInput && m.historyIdx > -1 {
				m.historyIdx--
				if m.historyIdx == -1 {
					m.textinput.SetValue(m.currentInput)
				} else {
					m.textinput.SetValue(m.inputHistory[len(m.inputHistory)-1-m.historyIdx])
				}
				m.textinput.CursorEnd()
				return m, nil
			}
			return m, nil
		case tea.KeyPgUp:
			m.focus = focusViewport
			m.viewport.ScrollUp(m.viewport.Height / halfPageDivisor)
			return m, nil
		case tea.KeyPgDown:
			m.focus = focusViewport
			m.viewport.ScrollDown(m.viewport.Height / halfPageDivisor)
			return m, nil
		case tea.KeyCtrlU:
			m.focus = focusViewport
			m.viewport.ScrollUp(m.viewport.Height / halfPageDivisor)
			return m, nil
		case tea.KeyCtrlD:
			m.focus = focusViewport
			m.viewport.ScrollDown(m.viewport.Height / halfPageDivisor)
			return m, nil
		case tea.KeyCtrlL:
			header := m.headerLines()
			m.logs = header
			m.headerLineCount = len(header)
			m.transcriptLogs = nil
			m.updateViewport()
		case tea.KeyCtrlO:
			m.transcriptMode = !m.transcriptMode
			m.syncViewport()
		}
	case tea.MouseMsg:
		if msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress {
			if m.mouseInViewport(msg.Y) {
				m.focus = focusViewport
			} else {
				m.focus = focusInput
			}
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if !m.mouseInViewport(msg.Y) {
				return m, nil
			}
			m.focus = focusViewport
			m.viewport.ScrollUp(scrollStep)
			return m, nil
		case tea.MouseButtonWheelDown:
			if !m.mouseInViewport(msg.Y) {
				return m, nil
			}
			m.focus = focusViewport
			m.viewport.ScrollDown(scrollStep)
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.textinput.SetWidth(max(1, m.width))
		if !m.ready {
			m.viewport = viewport.New(m.width, m.viewportHeight())
			m.viewport.MouseWheelEnabled = false // we handle mouse wheel ourselves
			m.ready = true
		} else {
			m.viewport.Width = m.width
			m.viewport.Height = m.viewportHeight()
		}
		m.rebuildHeader()
		m.updateViewport()
	case EventMsg:
		e := msg.Event
		switch e.Type {
		case agent.EventAgent:
			if e.SystemPromptTokens > 0 {
				m.systemPromptTokens = e.SystemPromptTokens
				m.totalPrompt = e.SystemPromptTokens
			}
			if e.Message != "" {
				// Transcript gets agent messages too.
				m.addTranscriptLog(agentStyle.Render(e.Message))

				switch {
				case e.GroupID != "" && m.groups[e.GroupID] != nil:
					m.appendToGroup(e.GroupID, agentStyle.Render(e.Message))
				case m.groupActive:
					// Route intermediate agent text into the collapsed group.
					m.groupLogs = append(m.groupLogs, agentStyle.Render(e.Message))
					m.rebuildGroupView()
				default:
					prefix := agentAccent
					prefixW := ansi.StringWidth(prefix)
					avail := max(m.width-prefixW-paddingConfirmSel, diffPreviewLines) // diffPreviewLines reused as minAvailWidth

					// Render markdown if the message contains markdown markers.
					rendered := m.renderAgentMessage(e.Message, avail)
					lines := strings.Split(rendered, "\n")
					indent := agentAccent
					var buf strings.Builder
					for i, line := range lines {
						if i > 0 {
							buf.WriteByte('\n')
							buf.WriteString(indent)
						}
						buf.WriteString(line)
					}
					m.addLog(prefix + buf.String())
					// Fallback: if the agent emitted a text-form tool call
					// (edit_file with old_str/new_str as prose), render it
					// as a colored diff so the user sees additions/removals.
					if diff := agent.ExtractDiffFromText(e.Message); diff != "" {
						m.addDiffLog(diff)
					}
				}
			}
		case agent.EventTool:
			toolLine := m.formatToolEvent(&e)

			// Transcript always gets the full tool output.
			m.addTranscriptLog(toolLine)
			if e.FullOutput != "" {
				m.addTranscriptLog(toolStyle.Render("──────────────────────"))
				m.addTranscriptLog(e.FullOutput)
				m.addTranscriptLog(toolStyle.Render("──────────────────────"))
			}

			// Skip cache hits in group display to reduce visual noise
			// (e.g. 6 specialists reading the same file during code review).
			// The call is still visible in the transcript (ctrl+o).
			if e.CacheHit && e.GroupID != "" {
				break
			}

			switch {
			case e.GroupID != "" && m.groups[e.GroupID] != nil:
				m.appendToGroup(e.GroupID, toolLine)
			case m.groupActive:
				m.groupLogs = append(m.groupLogs, toolLine)
				m.rebuildGroupView()
			default:
				m.addLog(toolLine)
				if e.FileAction != "" && e.Diff != "" {
					m.addDiffLog(e.Diff)
				}
			}
		case agent.EventGroupStart:
			if e.GroupID != "" {
				m.openGroup(e.GroupID, e.Message)
			} else {
				m.groupActive = true
				m.groupTitle = e.Message
				m.groupLogs = nil
				m.groupExpanded = false
				m.addLog(agentAccent + toolStyle.Render(e.Message))
				m.groupStartIdx = len(m.logs)
			}
		case agent.EventGroupEnd:
			if e.GroupID != "" {
				m.closeGroup(e.GroupID)
			} else {
				m.groupActive = false
				m.rebuildGroupView()
			}
		case agent.EventGroupTitleUpdate:
			if e.GroupID != "" {
				if g, ok := m.groups[e.GroupID]; ok {
					g.title = e.Message
					m.updateGroup(g)
				}
			}
			m.addTranscriptLog(systemStyle.Render("─── " + e.Message + " ───"))
		case agent.EventFix:
			m.addLog(fixStyle.Render(m.locale.Get("event.fix") + e.Message))
		case agent.EventError:
			m.addLog(errorStyle.Render(fmt.Sprintf("%s%s: %s", m.locale.Get("event.error"), e.Tool, e.Message)))
		case agent.EventTokens:
			m.liveEval = 0
			m.totalPrompt += e.PromptTokens
			m.totalEval += e.EvalTokens
			if e.EvalTkPerSec > 0 {
				m.lastTkPerSec = e.EvalTkPerSec
			}
			if e.ContextUsed > 0 {
				m.contextUsed = e.ContextUsed
			}
			if e.ContextTotal > 0 {
				m.contextTotal = e.ContextTotal
			}
		case agent.EventInferenceProgress:
			m.liveEval = e.InferenceTokens
			if e.InferenceElapsed > 0 && e.InferenceTokens > 0 {
				m.lastTkPerSec = float64(e.InferenceTokens) / e.InferenceElapsed.Seconds()
			}
			if e.InferenceContent != "" {
				width := m.width
				if width <= 0 {
					width = 80
				}
				for _, line := range strings.Split(e.InferenceContent, "\n") {
					wrapped := wrap.String(wordwrap.String(line, width), width)
					m.addTranscriptLog(agentStyle.Render(wrapped))
				}
			}
		case agent.EventCompact:
			m.addLog(systemStyle.Render(m.locale.Get("event.compact") + e.Message))
			if e.ContextUsed > 0 {
				m.contextUsed = e.ContextUsed
			}
			if e.ContextTotal > 0 {
				m.contextTotal = e.ContextTotal
			}
		case agent.EventModeChange:
			if e.Message == "edit" {
				m.mode = agent.ModeEdit
			} else {
				m.mode = agent.ModePlan
			}
		case agent.EventPhase:
			m.phase = e.Message
			label := strings.ToUpper(e.Message)
			pad := m.width - len(label) - dividerLabelPad
			if pad < dividerMinPad {
				pad = dividerMinPad
			}
			left := pad / dividerHalf
			right := pad - left
			divider := strings.Repeat("═", left) + " " + label + " " + strings.Repeat("═", right)
			m.addLog(systemStyle.Render(divider))
		case agent.EventCacheStats:
			m.cacheHits = e.CacheHits
			m.cacheMisses = e.CacheMisses
			m.addLog(systemStyle.Render(e.Message))
		case agent.EventReplan:
			m.replans++
			m.addLog(systemStyle.Render(fmt.Sprintf("⟳ REPLAN requested: %s", e.Message)))
		case agent.EventRAGProgress:
			m.ragProgressDone = e.ProgressDone
			m.ragProgressTotal = e.ProgressTotal
			m.ragProgressLabel = e.ProgressLabel
			if e.ProgressTotal == 0 || e.ProgressDone >= e.ProgressTotal {
				// Completion or no-op: clear shortly so the bar disappears.
				m.ragProgressDone = 0
				m.ragProgressTotal = 0
				m.ragProgressLabel = ""
			}
		case agent.EventDone:
			elapsed := m.elapsed()
			if e.Stats != nil {
				m.addLog(tokenStyle.Render(m.renderSummary(e.Stats, elapsed)))
			} else {
				m.addLog(tokenStyle.Render(fmt.Sprintf("%s%s", m.locale.Get("event.done"), elapsed)))
			}
			m.running = false
			m.textinput.Reset()
			m.textinput.SetHeight(1)
			m.textinput.Placeholder = m.defaultPlaceholder()
			m.textinput.Focus()
			m.recalcViewport()
		}
		cmds = append(cmds, m.waitForEvent())
	case ConfirmMsg:
		m.pendingConfirm = &msg.Request
		m.pauseStart = time.Now()
		m.focus = focusInput
		m.confirmSt = confirmChoose
		m.confirmIdx = 0
		m.recalcViewport()
		return m, nil
	case PlanConfirmMsg:
		m.pendingPlanConfirm = &msg.Request
		m.pauseStart = time.Now()
		m.focus = focusInput
		m.planConfirmSt = confirmChoose
		m.planConfirmIdx = 0
		m.addLog("")
		m.addLog("")
		m.recalcViewport()
		return m, nil
	case StepConfirmMsg:
		m.pendingStepConfirm = &msg.Request
		m.pauseStart = time.Now()
		m.focus = focusInput
		m.stepConfirmIdx = 0
		m.addLog("")
		m.addLog(confirmHeaderStyle.Render(m.locale.Get("step_confirm.header")))
		// Show step description lines
		for _, line := range strings.Split(strings.TrimSpace(msg.Request.Description), "\n") {
			m.addLog("  " + line)
		}
		m.addLog("")
		m.recalcViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.textinput, cmd = m.textinput.Update(msg)
	cmds = append(cmds, cmd)
	m.resizeInput()

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	m.updateComplete()

	return m, tea.Batch(cmds...)
}

func (m *Model) updateComplete() {
	prev := m.showComplete

	val := m.textinput.Value()
	if !strings.HasPrefix(val, "/") || len(m.commands) == 0 {
		m.showComplete = false
		m.filtered = nil
		if prev != m.showComplete {
			m.recalcViewport()
		}
		return
	}

	query := strings.ToLower(strings.TrimPrefix(val, "/"))
	if strings.Contains(query, " ") {
		m.showComplete = false
		m.filtered = nil
		if prev != m.showComplete {
			m.recalcViewport()
		}
		return
	}

	prevCount := len(m.filtered)
	m.filtered = m.filtered[:0]
	for _, c := range m.commands {
		if query == "" || strings.Contains(strings.ToLower(c.Name), query) {
			m.filtered = append(m.filtered, c)
		}
	}

	m.showComplete = len(m.filtered) > 0
	if m.selectedIdx >= len(m.filtered) {
		m.selectedIdx = max(0, len(m.filtered)-1)
	}

	if prev != m.showComplete || prevCount != len(m.filtered) {
		m.recalcViewport()
	}
}

// viewportHeight calculates the available viewport height based on current state.
// Base layout: header(3) + \n + viewport + \n + completePopup + timeLine + \n + inputLine + \n + \n + toolbar(1)
func (m *Model) viewportHeight() int {
	var inputH int
	switch {
	case m.pendingStepConfirm != nil:
		inputH = len(stepConfirmMenuOptions) + menuPadding
	case m.pendingPlanConfirm != nil && m.planConfirmSt == confirmChoose:
		// 3 options + blank line + hint line
		inputH = len(planConfirmMenuOptions) + menuPadding
	case m.pendingConfirm != nil && m.confirmSt == confirmChoose:
		// 4 options + blank line + hint line
		inputH = len(confirmMenuOptions) + menuPadding
	default:
		inputH = max(1, m.textinput.Height()) + menuPadding // input + 2 padding lines
	}
	// viewport + \n + \n + inputH + \n + \n + toolbar(1)
	extra := layoutExtraLines + inputH
	if m.running {
		extra += menuPadding // timeLine: blank line + status line
	}
	if m.showComplete && len(m.filtered) > 0 {
		visible := min(len(m.filtered), maxVisibleItems)
		extra += visible + menuPadding // items + top/bottom borders
	}
	return max(1, m.height-extra)
}

// resizeInput adjusts textarea height to fit content and recalculates viewport.
func (m *Model) resizeInput() {
	lines := strings.Count(m.textinput.Value(), "\n") + 1
	h := max(1, min(lines, m.textinput.MaxHeight))
	if m.textinput.Height() != h {
		m.textinput.SetHeight(h)
		m.recalcViewport()
	}
}

// recalcViewport adjusts viewport height for dynamic elements and refreshes content.
func (m *Model) recalcViewport() {
	if !m.ready {
		return
	}
	stickBottom := m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	h := m.viewportHeight()
	if m.viewport.Height != h {
		m.viewport.Height = h
		m.updateViewportWithStickBottom(stickBottom)
	}
}

// formatToolEvent formats a tool event as a single log line.
func (m Model) formatToolEvent(e *agent.Event) string {
	statusIcon := "✓"
	if !e.Success {
		statusIcon = "✗"
	}
	statusStr := toolStyle.Render(statusIcon)

	if e.FileAction != "" {
		name := toolBoldStyle.Render(e.FileAction)
		args := toolStyle.Render(fmt.Sprintf("(%s)", e.Message))
		line := fmt.Sprintf("%s %s%s", statusStr, name, args)
		if e.LinesAdded > 0 || e.LinesRemoved > 0 {
			stat := diffAddStyle.Render(fmt.Sprintf("+%d", e.LinesAdded)) + " " +
				diffDelStyle.Render(fmt.Sprintf("-%d", e.LinesRemoved))
			line += "  " + stat
		}
		return line
	}

	displayName := toolBoldStyle.Render(agent.ToolDisplayName(e.Tool))
	if e.Message == "" {
		return fmt.Sprintf("%s %s", statusStr, displayName)
	}
	args := toolStyle.Render(fmt.Sprintf("(%s)", e.Message))
	return fmt.Sprintf("%s %s%s", statusStr, displayName, args)
}

// groupState tracks one collapsible group in the multi-group pipeline.
// Each instance occupies a contiguous range of m.logs starting at
// `startIdx` and spanning `rendered` lines (header + visible body). When a
// new event arrives for the group, its range in m.logs is spliced in place
// and every following group's startIdx is shifted by the line delta.
//
// Concurrency invariant: groupState values (and the Model.groups map) are
// mutated ONLY from within Model.Update, which bubbletea serialises on its
// own goroutine. Agent events arrive as tea.Msg values and are applied
// there — never touch these fields from a background goroutine.
type groupState struct {
	id       string
	title    string
	logs     []string
	expanded bool
	startIdx int
	rendered int
	closed   bool
}

const groupMaxVisible = 5

// renderGroupLines returns the lines a group currently occupies in m.logs
// (header + body). The body is collapsed to the last groupMaxVisible
// entries unless expanded is true.
func (m *Model) renderGroupLines(g *groupState) []string {
	lines := []string{
		agentAccent + toolStyle.Render(g.title),
	}
	total := len(g.logs)
	if total == 0 {
		return lines
	}
	if g.expanded || total <= groupMaxVisible {
		for _, line := range g.logs {
			lines = append(lines, "  "+line)
		}
		return lines
	}
	hidden := total - groupMaxVisible
	lines = append(lines, "  "+diffStatStyle.Render(fmt.Sprintf(m.locale.Get("group.more_tools"), hidden)))
	for _, line := range g.logs[hidden:] {
		lines = append(lines, "  "+line)
	}
	return lines
}

// updateGroup splices the new rendered lines of g back into m.logs at its
// current startIdx and adjusts the startIdx of every following group.
func (m *Model) updateGroup(g *groupState) {
	newLines := m.renderGroupLines(g)
	oldLen := g.rendered
	end := g.startIdx + oldLen
	// Splice: m.logs = before + newLines + after
	after := append([]string(nil), m.logs[end:]...)
	m.logs = append(m.logs[:g.startIdx], newLines...)
	m.logs = append(m.logs, after...)
	delta := len(newLines) - oldLen
	g.rendered = len(newLines)
	if delta != 0 {
		for _, other := range m.groups {
			if other == g {
				continue
			}
			if other.startIdx >= end {
				other.startIdx += delta
			}
		}
	}
	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.updateViewportWithStickBottom(stickBottom)
}

// openGroup registers a new group and appends its header to m.logs.
func (m *Model) openGroup(id, title string) {
	if m.groups == nil {
		m.groups = map[string]*groupState{}
	}
	g := &groupState{
		id:       id,
		title:    title,
		startIdx: len(m.logs),
		expanded: m.groupsExpandedAll,
	}
	m.groups[id] = g
	lines := m.renderGroupLines(g)
	m.logs = append(m.logs, lines...)
	g.rendered = len(lines)
	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.updateViewportWithStickBottom(stickBottom)
}

// appendToGroup routes a rendered line into an existing group.
func (m *Model) appendToGroup(id, line string) {
	g, ok := m.groups[id]
	if !ok {
		return
	}
	g.logs = append(g.logs, line)
	m.updateGroup(g)
}

// closeGroup marks a group as finished. Its lines remain in m.logs.
func (m *Model) closeGroup(id string) {
	if g, ok := m.groups[id]; ok {
		g.closed = true
	}
}

// rebuildGroupView updates the visible group section in logs.
// Shows the group title + last N tool calls, with "+X more" if collapsed.
func (m *Model) rebuildGroupView() {
	const maxVisible = 5

	// Remove old group entries from logs
	if m.groupStartIdx < len(m.logs) {
		m.logs = m.logs[:m.groupStartIdx]
	}

	total := len(m.groupLogs)
	if total == 0 {
		stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
		m.updateViewportWithStickBottom(stickBottom)
		return
	}

	if m.groupExpanded || total <= maxVisible {
		// Show all
		for _, line := range m.groupLogs {
			m.logs = append(m.logs, "  "+line)
		}
	} else {
		// Show last maxVisible, hide the rest
		hidden := total - maxVisible
		m.logs = append(m.logs, diffStatStyle.Render(fmt.Sprintf(m.locale.Get("group.more_tools"), hidden)))
		for _, line := range m.groupLogs[hidden:] {
			m.logs = append(m.logs, "  "+line)
		}
	}

	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.updateViewportWithStickBottom(stickBottom)
}

func (m *Model) headerLines() []string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	title := titleStyle.Render(fmt.Sprintf("KodRun %s", m.version))
	meta := dimStyle.Render(fmt.Sprintf("  ·  %s (%s ctx)", m.model, formatContextSize(m.contextSize)))
	line1 := title + meta
	line2 := dimStyle.Render(shortPath(m.workDir))

	headerBorder := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		PaddingLeft(1).PaddingRight(1)
	if m.width > 0 {
		headerBorder = headerBorder.Width(m.width - dividerLabelPad)
	}
	box := headerBorder.Render(line1 + "\n" + line2)
	return []string{box, ""}
}

// rebuildHeader replaces the header block at the top of m.logs with a fresh
// render (e.g. after terminal resize changes the available width).
func (m *Model) rebuildHeader() {
	header := m.headerLines()
	if m.headerLineCount > 0 && m.headerLineCount <= len(m.logs) {
		m.logs = append(header, m.logs[m.headerLineCount:]...)
	}
	m.headerLineCount = len(header)
}

func (m *Model) addLog(line string) {
	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.logs = append(m.logs, line)
	if len(m.logs) > maxLogLines {
		trim := maxLogLines / logTrimDivisor
		m.logs = append([]string(nil), m.logs[trim:]...)
	}
	if !m.transcriptMode {
		m.updateViewportWithStickBottom(stickBottom)
	}
}

func (m *Model) addTranscriptLog(line string) {
	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.transcriptLogs = append(m.transcriptLogs, line)
	if len(m.transcriptLogs) > maxLogLines {
		trim := maxLogLines / logTrimDivisor
		m.transcriptLogs = append([]string(nil), m.transcriptLogs[trim:]...)
	}
	if m.transcriptMode {
		m.updateViewportWithStickBottom(stickBottom)
	}
}

// syncViewport switches the viewport content between normal and transcript buffers.
func (m *Model) syncViewport() {
	if m.transcriptMode {
		m.viewport.SetContent(strings.Join(m.transcriptLogs, "\n"))
	} else {
		m.viewport.SetContent(strings.Join(m.logs, "\n"))
	}
	m.viewport.GotoBottom()
}

// hasMarkdown detects whether text contains markdown formatting worth rendering.
func hasMarkdown(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "),
			strings.HasPrefix(trimmed, "### "),
			strings.HasPrefix(trimmed, "# "),
			strings.HasPrefix(trimmed, "- "),
			strings.HasPrefix(trimmed, "* "),
			strings.HasPrefix(trimmed, "> "),
			strings.HasPrefix(trimmed, "```"),
			strings.Contains(trimmed, "**"),
			strings.Contains(trimmed, "`"):
			return true
		}
		// Numbered list: "1. ..." or "1) ..."
		if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' &&
			(trimmed[1] == '.' || trimmed[1] == ')') {
			return true
		}
	}
	return false
}

// renderAgentMessage renders agent output, using glamour for markdown-rich text
// and simple styling for plain text.
func (m Model) renderAgentMessage(text string, width int) string {
	if hasMarkdown(text) {
		return m.mdRenderer.render(text, width)
	}
	// Plain text fallback — no markdown detected.
	if width > 0 {
		wrapped := wrap.String(wordwrap.String(text, width), width)
		return agentStyle.Render(wrapped)
	}
	return agentStyle.Render(text)
}

// addDiffLog renders a diff string with colored lines and appends to logs.
// Limits output to diffPreviewLines visible diff lines.
func (m *Model) addDiffLog(diff string) {
	lines := strings.Split(strings.TrimRight(diff, "\n"), "\n")

	// Extract file path from the first diff line (--- a/path or +++ b/path).
	filePath := extractDiffFilePath(lines)
	if filePath != "" {
		barWidth := m.width - len(filePath) - dividerMinPad
		if barWidth < dividerMinPad {
			barWidth = dividerMinPad
		}
		header := fmt.Sprintf("  ── %s %s", filePath, strings.Repeat("─", barWidth))
		m.logs = append(m.logs, diffStatStyle.Render(header))
	}

	shown := 0
	for _, l := range lines {
		if shown >= diffPreviewLines {
			m.logs = append(m.logs, diffStatStyle.Render(fmt.Sprintf(m.locale.Get("diff.more_lines"), len(lines)-shown)))
			break
		}
		if len(l) == 0 {
			continue
		}
		// Skip --- and +++ header lines (already shown as file path header).
		if (strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "+++ ")) && filePath != "" {
			continue
		}
		var styled string
		switch l[0] {
		case '+':
			styled = diffAddStyle.Render("  " + l)
		case '-':
			styled = diffDelStyle.Render("  " + l)
		case '@':
			styled = diffHunkStyle.Render("  " + l)
		default:
			styled = diffStatStyle.Render("  " + l)
		}
		m.logs = append(m.logs, styled)
		shown++
	}
	stickBottom := !m.ready || m.viewport.AtBottom() || (m.running && m.focus == focusInput)
	m.updateViewportWithStickBottom(stickBottom)
}

// extractDiffFilePath extracts the file path from unified diff headers.
func extractDiffFilePath(lines []string) string {
	for _, l := range lines {
		if strings.HasPrefix(l, "+++ b/") {
			return strings.TrimPrefix(l, "+++ b/")
		}
		if strings.HasPrefix(l, "+++ ") {
			return strings.TrimPrefix(l, "+++ ")
		}
		// Stop scanning after hunk headers.
		if strings.HasPrefix(l, "@@") {
			break
		}
	}
	return ""
}

func (m *Model) updateViewport() {
	m.updateViewportWithStickBottom(false)
}

func (m *Model) updateViewportWithStickBottom(stickBottom bool) {
	if m.transcriptMode {
		m.viewport.SetContent(strings.Join(m.transcriptLogs, "\n"))
	} else {
		m.viewport.SetContent(strings.Join(m.logs, "\n"))
	}
	if stickBottom {
		m.viewport.GotoBottom()
	}
}

func (m Model) mouseInViewport(y int) bool {
	return y >= 0 && y < m.viewport.Height
}

// View implements tea.Model.
func (m Model) View() string {
	if !m.ready {
		return m.locale.Get("status.init")
	}

	var completePopup string
	if m.showComplete && len(m.filtered) > 0 {
		completePopup = m.renderComplete()
	}

	toolbar := "  " + m.renderToolbar()

	var inputLine string
	switch {
	case m.pendingStepConfirm != nil:
		inputLine = m.renderStepConfirmMenu()
	case m.pendingPlanConfirm != nil && m.planConfirmSt == confirmChoose:
		inputLine = m.renderPlanConfirmMenu()
	case m.pendingConfirm != nil && m.confirmSt == confirmChoose:
		inputLine = m.renderConfirmMenu()
	default:
		inputLine = m.renderInput()
	}

	var timeLine string
	if m.running {
		elapsed := m.elapsed()
		spinner := spinnerFrames[m.spinnerFrame]
		timeLine = "\n" + toolbarStyle.Render(fmt.Sprintf("  %s %s ⏱ %s · %s tk", spinner, m.locale.Get("status.working"), elapsed, formatTokens(m.totalEval+m.liveEval))) + "\n"
	}
	if m.ragProgressTotal > 0 {
		pct := m.ragProgressDone * percentMultiplier / m.ragProgressTotal
		label := m.ragProgressLabel
		if label == "" {
			label = "indexing"
		}
		ragLine := toolbarStyle.Render(fmt.Sprintf("  RAG %s %d%% (%d/%d)", label, pct, m.ragProgressDone, m.ragProgressTotal))
		// Trailing newline keeps a blank line between the RAG progress line
		// and the input prompt below, matching the spacing of other status
		// blocks.
		if timeLine == "" {
			timeLine = "\n" + ragLine + "\n"
		} else {
			timeLine += ragLine + "\n"
		}
	}

	out := fmt.Sprintf("%s\n%s%s\n%s\n\n%s",
		m.viewport.View(),
		completePopup,
		timeLine,
		inputLine,
		toolbar,
	)

	// Pad to exactly m.height lines to prevent bubbletea rendering artifacts.
	if lines := strings.Count(out, "\n") + 1; lines < m.height {
		out += strings.Repeat("\n", m.height-lines)
	}

	return out
}

const (
	ansiBg255   = "\x1b[48;5;255m" // set background to color 255 (white)
	ansiReset   = "\x1b[0m"
	ansiBgReset = "\x1b[0;48;5;255m" // reset attributes but keep bg 255
)

func (m Model) renderInput() string {
	w := m.width
	emptyLine := ansiBg255 + strings.Repeat(" ", w) + ansiReset

	raw := m.textinput.View()
	// Replace all resets inside textarea output with "reset + restore bg",
	// so our background color persists through textarea's internal ANSI codes.
	raw = strings.ReplaceAll(raw, ansiReset, ansiBgReset)
	raw = strings.TrimRight(raw, "\n")

	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		visible := ansi.StringWidth(line)
		pad := max(0, w-visible)
		lines[i] = ansiBg255 + line + strings.Repeat(" ", pad) + ansiReset
	}

	return emptyLine + "\n" + strings.Join(lines, "\n") + "\n" + emptyLine
}

func (m Model) renderToolbar() string {
	sep := toolbarStyle.Render(" │ ")

	modelPart := toolbarStyle.Render(m.model)

	modeLabel := m.locale.Get("label.edit_mode")
	modeColor := lipgloss.Color("2") // green for edit
	if m.mode == agent.ModePlan {
		modeLabel = m.locale.Get("label.plan_mode")
		modeColor = lipgloss.Color("0") // black for plan
	}
	modePart := lipgloss.NewStyle().Bold(true).Foreground(modeColor).Render(
		strings.ToUpper(modeLabel),
	) + " " + toolbarStyle.Render(m.locale.Get("label.shift_tab"))

	tokenPart := toolbarStyle.Render(fmt.Sprintf("%s tk", formatTokens(m.totalPrompt+m.totalEval)))

	parts := []string{modelPart, modePart, tokenPart}

	if m.transcriptMode {
		parts = append(parts, toolbarStyle.Render("[Transcript]"))
	}

	if m.lastTkPerSec > 0 {
		parts = append(parts, toolbarStyle.Render(fmt.Sprintf("%.1f tk/s", m.lastTkPerSec)))
	}

	var pct int
	if m.contextTotal > 0 {
		pct = percentMultiplier - (m.contextUsed * percentMultiplier / m.contextTotal)
	}
	parts = append(parts, toolbarStyle.Render(fmt.Sprintf(m.locale.Get("label.context_left"), pct)))

	return strings.Join(parts, sep)
}

func (m Model) renderComplete() string {
	visible := m.filtered
	if len(visible) > maxVisibleItems {
		visible = visible[:maxVisibleItems]
	}

	lines := make([]string, 0, len(visible))
	for i, item := range visible {
		name := fmt.Sprintf("/%s", item.Name)
		line := fmt.Sprintf("  %-16s %s", name, item.Description)
		if i == m.selectedIdx {
			line = completeSelected.Render(line)
		} else {
			line = completeNormal.Render(line)
		}
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	return completeBorder.Render(content) + "\n"
}

// renderConfirmCard renders the action card shown above the confirm menu:
// tool name, full arguments and (when available) a colored diff/preview.
func (m Model) renderConfirmCard() string {
	p := m.pendingConfirm.Payload
	lines := make([]string, 0, len(p.ArgKeys)+paddingConfirmNorm)

	if p.Danger {
		lines = append(lines, errorStyle.Bold(true).Render("⚠ "+m.locale.Get("confirm.card.danger")))
	}

	header := fmt.Sprintf("%s %s", m.locale.Get("confirm.card.tool"), p.Tool)
	lines = append(lines, toolStyle.Bold(true).Render(header))

	for _, k := range p.ArgKeys {
		v := p.Args[k]
		lines = append(lines, diffStatStyle.Render(fmt.Sprintf("  %s: %s", k, v)))
	}

	if p.Preview != "" {
		lines = append(lines, diffStatStyle.Render("  "+m.locale.Get("confirm.card.preview")))
		lines = append(lines, renderDiffPreviewLines(p.Preview, diffPreviewLines, m.locale)...)
	}
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

// renderDiffPreviewLines colorizes a diff string into TUI lines, capped to maxLines.
func renderDiffPreviewLines(diff string, maxLines int, locale *Locale) []string {
	all := strings.Split(strings.TrimRight(diff, "\n"), "\n")
	out := make([]string, 0, len(all))
	shown := 0
	for _, l := range all {
		if shown >= maxLines {
			out = append(out, diffStatStyle.Render(fmt.Sprintf(locale.Get("diff.more_lines"), len(all)-shown)))
			break
		}
		if len(l) == 0 {
			continue
		}
		var styled string
		switch l[0] {
		case '+':
			styled = diffAddStyle.Render("  " + l)
		case '-':
			styled = diffDelStyle.Render("  " + l)
		case '@':
			styled = diffHunkStyle.Render("  " + l)
		default:
			styled = diffStatStyle.Render("  " + l)
		}
		out = append(out, styled)
		shown++
	}
	return out
}

// renderConfirmMenu renders the action card and the numbered confirm menu
// replacing the input area.
func (m Model) renderConfirmMenu() string {
	var lines []string
	lines = append(lines, m.renderConfirmCard())

	for i, opt := range confirmMenuOptions {
		label := fmt.Sprintf("%d. %s", i+1, m.locale.Get(opt.labelKey))
		if i == m.confirmIdx {
			lines = append(lines, confirmSelected.Render("> "+label))
		} else {
			lines = append(lines, confirmNormal.Render(label))
		}
	}
	lines = append(lines, "", confirmHintStyle.Render(m.locale.Get("confirm.hint")))
	return strings.Join(lines, "\n")
}

// renderPlanConfirmMenu renders the 3-option plan confirm menu.
func (m Model) renderPlanConfirmMenu() string {
	var lines []string
	for i, opt := range planConfirmMenuOptions {
		label := fmt.Sprintf("%d. %s", i+1, m.locale.Get(opt.labelKey))
		if i == m.planConfirmIdx {
			lines = append(lines, confirmSelected.Render("> "+label))
		} else {
			lines = append(lines, confirmNormal.Render(label))
		}
	}
	lines = append(lines, "", confirmHintStyle.Render(m.locale.Get("plan_confirm.hint")))
	return strings.Join(lines, "\n")
}

func (m Model) renderStepConfirmMenu() string {
	var lines []string
	for i, opt := range stepConfirmMenuOptions {
		label := fmt.Sprintf("%d. %s", i+1, m.locale.Get(opt.labelKey))
		if i == m.stepConfirmIdx {
			lines = append(lines, confirmSelected.Render("> "+label))
		} else {
			lines = append(lines, confirmNormal.Render(label))
		}
	}
	lines = append(lines, "", confirmHintStyle.Render(m.locale.Get("step_confirm.hint")))
	return strings.Join(lines, "\n")
}

func (m Model) renderSummary(s *agent.SessionStats, elapsed time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s", m.locale.Get("event.done"), elapsed)

	// File stats
	var fileParts []string
	if s.FilesAdded > 0 {
		fileParts = append(fileParts, fmt.Sprintf(m.locale.Get("summary.added"), s.FilesAdded))
	}
	if s.FilesModified > 0 {
		fileParts = append(fileParts, fmt.Sprintf(m.locale.Get("summary.modified"), s.FilesModified))
	}
	if s.FilesDeleted > 0 {
		fileParts = append(fileParts, fmt.Sprintf(m.locale.Get("summary.deleted"), s.FilesDeleted))
	}
	if s.FilesRenamed > 0 {
		fileParts = append(fileParts, fmt.Sprintf(m.locale.Get("summary.renamed"), s.FilesRenamed))
	}
	if len(fileParts) > 0 {
		fmt.Fprintf(&b, "%s%s", m.locale.Get("summary.files"), strings.Join(fileParts, ", "))
	}

	// Line stats
	if s.LinesAdded > 0 || s.LinesRemoved > 0 {
		fmt.Fprintf(&b, m.locale.Get("summary.lines"), s.LinesAdded, s.LinesRemoved)
	}

	// Tool calls and performance
	var perfParts []string
	if s.ToolCalls > 0 {
		perfParts = append(perfParts, fmt.Sprintf(m.locale.Get("summary.tool_calls"), s.ToolCalls))
	}
	if s.AvgTkPerSec > 0 {
		perfParts = append(perfParts, fmt.Sprintf(m.locale.Get("summary.avg_tks"), s.AvgTkPerSec))
	}
	if s.PeakContextPct > 0 {
		perfParts = append(perfParts, fmt.Sprintf(m.locale.Get("summary.peak_ctx"), s.PeakContextPct))
	}
	if len(perfParts) > 0 {
		fmt.Fprintf(&b, "\n  %s", strings.Join(perfParts, " · "))
	}

	// Timing breakdown (populated when iterations > 0).
	if s.Iterations > 0 && s.TotalLLMTime > 0 {
		total := s.TotalLLMTime + s.TotalToolTime
		var llmPct, toolPct int
		if total > 0 {
			llmPct = int(s.TotalLLMTime * 100 / total)
			toolPct = int(s.TotalToolTime * 100 / total)
		}
		fmt.Fprintf(&b, "\n  Perf: LLM %s (%d%%) · Tools %s (%d%%) · %d iter",
			s.TotalLLMTime.Truncate(time.Millisecond), llmPct,
			s.TotalToolTime.Truncate(time.Millisecond), toolPct,
			s.Iterations)
	}

	return b.String()
}

// elapsed returns the task duration excluding paused time.
func (m Model) elapsed() time.Duration {
	d := time.Since(m.taskStart) - m.pausedDuration
	if !m.pauseStart.IsZero() {
		d -= time.Since(m.pauseStart)
	}
	return d.Truncate(time.Second)
}

func shortPath(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return dir
	}
	rel, err := filepath.Rel(home, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	return "~/" + rel
}

func formatContextSize(n int) string {
	if n >= kibi && n%kibi == 0 {
		return fmt.Sprintf("%dk", n/kibi)
	}
	if n >= kilo {
		return fmt.Sprintf("%.1fk", float64(n)/kibi)
	}
	return fmt.Sprintf("%d", n)
}

func formatTokens(n int) string {
	if n < kilo {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fK", float64(n)/kilo)
}
