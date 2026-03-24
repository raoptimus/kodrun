package agent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/tools"
)

// ErrMaxIterations is returned when the agent exceeds max iterations.
var ErrMaxIterations = errors.New("max iterations reached")

// maxToolResultBytes limits tool output stored in history to prevent unbounded memory growth.
const maxToolResultBytes = 16 * 1024 // 16KB

// Mode represents the agent operating mode.
type Mode int

const (
	// ModePlan is read-only analysis mode.
	ModePlan Mode = iota
	// ModeEdit is full tool access mode.
	ModeEdit
)

// String returns the mode name.
func (m Mode) String() string {
	if m == ModePlan {
		return "plan"
	}
	return "edit"
}

// readOnlyTools is the set of tools allowed in plan mode.
var readOnlyTools = map[string]bool{
	"read_file":   true,
	"list_dir":    true,
	"find_files":  true,
	"grep":        true,
	"get_rule":    true,
	"snippets":    true,
	"search_docs": true,
}

// EventHandler receives agent events for display.
type EventHandler func(event Event)

// Event represents an agent lifecycle event.
type Event struct {
	Type               EventType
	Message            string
	Tool               string
	Success            bool
	PromptTokens       int
	EvalTokens         int
	EvalTkPerSec       float64 // output tokens per second
	ContextUsed        int     // last prompt token count (how full the context is)
	ContextTotal       int     // max context size
	SystemPromptTokens int     // estimated system prompt token count
	FileAction         string  // Add/Update/Delete/Rename
	LinesAdded         int
	LinesRemoved       int
	Diff               string
	Stats              *SessionStats // set on EventDone
}

// SessionStats accumulates statistics for the current task execution.
type SessionStats struct {
	FilesAdded     int
	FilesModified  int
	FilesDeleted   int
	FilesRenamed   int
	LinesAdded     int
	LinesRemoved   int
	ToolCalls      int
	TotalPrompt    int
	TotalEval      int
	PeakContextPct int // peak context usage percentage
	AvgTkPerSec    float64
	tkPerSecSum    float64
	tkPerSecCount  int
}

func (s *SessionStats) reset() {
	*s = SessionStats{}
}

func (s *SessionStats) recordFileAction(action string, added, removed int) {
	switch action {
	case "Add":
		s.FilesAdded++
	case "Update":
		s.FilesModified++
	case "Delete":
		s.FilesDeleted++
	case "Rename":
		s.FilesRenamed++
	}
	s.LinesAdded += added
	s.LinesRemoved += removed
}

func (s *SessionStats) recordTokens(prompt, eval int, tkPerSec float64, contextPct int) {
	s.TotalPrompt += prompt
	s.TotalEval += eval
	if tkPerSec > 0 {
		s.tkPerSecSum += tkPerSec
		s.tkPerSecCount++
	}
	if contextPct > s.PeakContextPct {
		s.PeakContextPct = contextPct
	}
}

func (s *SessionStats) avgTkPerSec() float64 {
	if s.tkPerSecCount == 0 {
		return 0
	}
	return s.tkPerSecSum / float64(s.tkPerSecCount)
}

// EventType categorizes events.
type EventType int

const (
	EventAgent EventType = iota
	EventTool
	EventFix
	EventError
	EventDone
	EventTokens
	EventCompact
	EventGroupStart
	EventGroupEnd
	EventModeChange
)

// Agent orchestrates the LLM-tool loop.
type Agent struct {
	client              *ollama.Client
	model               string
	reg                 *tools.Registry
	history             []ollama.Message
	maxIter             int
	workDir             string
	onEvent             EventHandler
	confirmFn           ConfirmFunc
	permMgr             *PermissionManager
	ctxMgr              *ContextManager
	contextSize         int
	ruleCatalog         string
	projectContext      string
	contextInjected     bool
	mode                Mode
	think               bool
	language            string
	autoCompact         bool
	lastPromptEvalCount int
	lastPlan            string
	planBuf             strings.Builder
	stats               SessionStats
	hasSnippets         bool
	hasRAG              bool
	ragIndex            *rag.Index
	pool                *WorkerPool
	extraConfirmTools   map[string]bool
	extraReadOnlyTools  map[string]bool
}

// New creates a new Agent.
func New(client *ollama.Client, model string, reg *tools.Registry, maxIter int, workDir string, contextSize int) *Agent {
	return &Agent{
		client:      client,
		model:       model,
		reg:         reg,
		maxIter:     maxIter,
		workDir:     workDir,
		contextSize: contextSize,
		permMgr:     NewPermissionManager(),
	}
}

// SetEventHandler sets the handler for agent events.
func (a *Agent) SetEventHandler(h EventHandler) {
	a.onEvent = h
}

// SetConfirmFunc sets the confirmation callback for destructive operations.
// If nil, all operations are allowed without confirmation.
func (a *Agent) SetConfirmFunc(fn ConfirmFunc) {
	a.confirmFn = fn
}

// GetConfirmFunc returns the current confirmation callback.
func (a *Agent) GetConfirmFunc() ConfirmFunc {
	return a.confirmFn
}

// ClearSessionPermissions resets all session-level tool permissions.
func (a *Agent) ClearSessionPermissions() {
	a.permMgr.Reset()
}

// SetLanguage sets the language for assistant responses.
func (a *Agent) SetLanguage(lang string) {
	a.language = lang
}

// SetAutoCompact enables or disables automatic context compaction.
func (a *Agent) SetAutoCompact(enabled bool) {
	a.autoCompact = enabled
}

// SetHasSnippets marks that the snippets tool is available.
func (a *Agent) SetHasSnippets(v bool) {
	a.hasSnippets = v
}

// SetHasRAG marks that the RAG search_docs tool is available.
func (a *Agent) SetHasRAG(v bool) {
	a.hasRAG = v
}

// SetRAGIndex sets the RAG index for automatic context prefetch.
func (a *Agent) SetRAGIndex(idx *rag.Index) {
	a.ragIndex = idx
}

// SetMaxWorkers configures the worker pool for parallel tool execution.
func (a *Agent) SetMaxWorkers(n int) {
	a.pool = NewWorkerPool(n)
}

// AddConfirmTools adds tool names that require user confirmation (e.g. MCP tools).
func (a *Agent) AddConfirmTools(names map[string]bool) {
	if a.extraConfirmTools == nil {
		a.extraConfirmTools = make(map[string]bool)
	}
	maps.Copy(a.extraConfirmTools, names)
}

// AddReadOnlyTools adds tool names that are safe for parallel execution (e.g. MCP read-only tools).
func (a *Agent) AddReadOnlyTools(names map[string]bool) {
	if a.extraReadOnlyTools == nil {
		a.extraReadOnlyTools = make(map[string]bool)
	}
	maps.Copy(a.extraReadOnlyTools, names)
}

// SetMode sets the agent operating mode.
func (a *Agent) SetMode(mode Mode) {
	a.mode = mode
}

// Mode returns the current operating mode.
func (a *Agent) Mode() Mode {
	return a.mode
}

// SetThink enables or disables thinking mode.
func (a *Agent) SetThink(think bool) {
	a.think = think
}

// Think returns whether thinking is enabled.
func (a *Agent) Think() bool {
	return a.think
}

// SavePlan stores a plan for later execution in edit mode.
func (a *Agent) SavePlan(plan string) {
	a.lastPlan = plan
}

// LastPlan returns the last saved plan.
func (a *Agent) LastPlan() string {
	return a.lastPlan
}

// EnterEditWithPlan switches to edit mode, clears context,
// and injects the approved plan as the first user message.
func (a *Agent) EnterEditWithPlan() {
	a.mode = ModeEdit
	a.think = false
	systemPrompt := a.buildSystemPrompt()
	a.history = []ollama.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Execute the following approved plan:\n\n%s\n\nImplement each step. Confirm plan steps as you go. Always respond in %s.", a.lastPlan, langName(a.language))},
	}
	a.contextInjected = false
}

func (a *Agent) emit(e Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// Init initializes the agent with system prompt. Call once before Send().
func (a *Agent) Init(ruleCatalog string) {
	a.ruleCatalog = ruleCatalog
	systemPrompt := a.buildSystemPrompt()
	a.projectContext = a.buildProjectContext()
	a.contextInjected = false
	a.history = []ollama.Message{
		{Role: "system", Content: systemPrompt},
	}
	a.ctxMgr = NewContextManager(a.contextSize, a.client, a.model)
	a.ctxMgr.SetLanguage(a.language)
	a.emit(Event{
		Type:               EventAgent,
		SystemPromptTokens: len(systemPrompt) / 4,
	})
}

// InitWithPrompt initializes the agent with a custom system prompt.
// Used by the Orchestrator to set role-specific prompts.
func (a *Agent) InitWithPrompt(systemPrompt string) {
	a.projectContext = a.buildProjectContext()
	a.contextInjected = false
	a.history = []ollama.Message{
		{Role: "system", Content: systemPrompt},
	}
	a.ctxMgr = NewContextManager(a.contextSize, a.client, a.model)
	a.ctxMgr.SetLanguage(a.language)
}

// ClearHistory resets conversation history, keeping the system prompt.
func (a *Agent) ClearHistory() {
	systemPrompt := a.buildSystemPrompt()
	a.history = []ollama.Message{
		{Role: "system", Content: systemPrompt},
	}
	a.contextInjected = false
	a.lastPromptEvalCount = 0
}

// Compact summarizes the conversation history to free context space.
// Instructions are optional hints for the summarizer (e.g. "focus on file changes").
func (a *Agent) Compact(ctx context.Context, instructions string) error {
	if a.ctxMgr == nil {
		return errors.New("context manager not initialized")
	}

	before := a.ctxMgr.estimateTokens(a.history)

	trimmed, err := a.ctxMgr.ForceTrim(ctx, a.history, instructions)
	if err != nil {
		return errors.WithMessage(err, "compact")
	}
	a.history = trimmed

	after := a.ctxMgr.estimateTokens(a.history)

	a.emit(Event{
		Type:         EventCompact,
		Message:      fmt.Sprintf("Context compacted: ~%d → ~%d tokens (freed ~%d)", before, after, before-after),
		ContextUsed:  after,
		ContextTotal: a.contextSize,
	})

	return nil
}

// History returns a copy of the current conversation history.
func (a *Agent) History() []ollama.Message {
	h := make([]ollama.Message, len(a.history))
	copy(h, a.history)
	return h
}

// Stats returns the current session statistics.
func (a *Agent) Stats() SessionStats {
	return a.stats
}

// ContextUsage returns estimated token usage and max context size.
func (a *Agent) ContextUsage() (used, total int) {
	if a.ctxMgr != nil {
		used = a.ctxMgr.estimateTokens(a.history)
	}
	return used, a.contextSize
}

// Send processes a single user message, preserving conversation history.
func (a *Agent) Send(ctx context.Context, task string) error {
	userContent := task
	if a.ragIndex != nil {
		if results, err := a.ragIndex.Search(ctx, task, 5); err == nil && len(results) > 0 {
			userContent = formatRAGResults(results) + "\n" + userContent
		}
	}
	if a.projectContext != "" && !a.contextInjected {
		userContent = "[Project context]\n" + a.projectContext + "\n\n[Task]\n" + userContent
		a.contextInjected = true
	}
	a.history = append(a.history, ollama.Message{Role: "user", Content: userContent})
	a.stats.reset()
	a.planBuf.Reset()

	a.emit(Event{Type: EventAgent, Message: "Processing task..."})

	for range a.maxIter {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Auto-compact by real prompt_eval_count when context is >=99% full
		if a.autoCompact && a.ctxMgr != nil && a.lastPromptEvalCount > 0 && a.contextSize > 0 {
			ratio := float64(a.lastPromptEvalCount) / float64(a.contextSize)
			if ratio >= 0.99 {
				if err := a.Compact(ctx, ""); err == nil {
					a.lastPromptEvalCount = 0
				}
			}
		}

		// Trim context if needed (safety net by estimate)
		if a.ctxMgr != nil {
			before := a.ctxMgr.estimateTokens(a.history)
			trimmed, err := a.ctxMgr.Trim(ctx, a.history)
			if err == nil && a.ctxMgr.estimateTokens(trimmed) < before {
				a.history = trimmed
				after := a.ctxMgr.estimateTokens(a.history)
				a.emit(Event{
					Type:         EventCompact,
					Message:      fmt.Sprintf("Auto-trimmed: ~%d → ~%d tokens", before, after),
					ContextUsed:  after,
					ContextTotal: a.contextSize,
				})
			} else if err == nil {
				a.history = trimmed
			}
		}

		resp, err := a.client.ChatSync(ctx, ollama.ChatRequest{
			Model:    a.model,
			Messages: a.history,
			Tools:    a.toolDefsForMode(),
			Options: map[string]any{
				"num_ctx": a.contextSize,
				"think":   a.think,
			},
		})
		if err != nil {
			return errors.WithMessage(err, "chat")
		}

		// Track real prompt token count for auto-compact
		if resp.PromptEvalCount > 0 {
			a.lastPromptEvalCount = resp.PromptEvalCount
		}

		// Emit token usage
		if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
			var tkPerSec float64
			if resp.EvalDuration > 0 {
				tkPerSec = float64(resp.EvalCount) / float64(resp.EvalDuration) * 1e9
			}
			a.emit(Event{
				Type:         EventTokens,
				PromptTokens: resp.PromptEvalCount,
				EvalTokens:   resp.EvalCount,
				EvalTkPerSec: tkPerSec,
				ContextUsed:  resp.PromptEvalCount,
				ContextTotal: a.contextSize,
			})
			var ctxPct int
			if a.contextSize > 0 {
				ctxPct = resp.PromptEvalCount * 100 / a.contextSize
			}
			a.stats.recordTokens(resp.PromptEvalCount, resp.EvalCount, tkPerSec, ctxPct)
		}

		// Add assistant response to history
		msg := ollama.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		a.history = append(a.history, msg)

		// No tool calls = final response
		if len(resp.ToolCalls) == 0 {
			if a.mode == ModePlan {
				if resp.Content != "" {
					a.planBuf.WriteString(resp.Content)
				}
				raw := a.planBuf.String()
				a.lastPlan = extractPlan(raw)
				// If extractPlan found no structured plan, use raw text as fallback.
				if a.lastPlan == "" && len(raw) > 0 {
					a.lastPlan = strings.TrimSpace(raw)
				}
			} else if resp.Content != "" {
				a.emit(Event{Type: EventAgent, Message: resp.Content})
			}
			a.stats.AvgTkPerSec = a.stats.avgTkPerSec()
			a.emit(Event{Type: EventDone, Message: "Done", Stats: &a.stats})
			return nil
		}

		// Split tool calls into parallel (read-only) and sequential groups.
		var parallelCalls []ollama.ToolCall
		var sequentialCalls []ollama.ToolCall
		for _, tc := range resp.ToolCalls {
			if a.canRunParallel(tc.Function.Name) {
				parallelCalls = append(parallelCalls, tc)
			} else {
				sequentialCalls = append(sequentialCalls, tc)
			}
		}

		// Execute read-only tools in parallel via WorkerPool.
		if len(parallelCalls) > 0 {
			a.executeParallel(ctx, parallelCalls)
		}

		// Execute remaining tools sequentially (write ops, confirm, blocked).
		augmented := false
		for _, tc := range sequentialCalls {
			detail := toolDetail(tc.Function.Name, tc.Function.Arguments)
			a.emit(Event{Type: EventTool, Tool: tc.Function.Name, Message: detail})

			// Block destructive tools in plan mode
			if a.isToolBlocked(tc.Function.Name) {
				a.history = append(a.history, ollama.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Tool %q is not available in plan mode. Switch to edit mode to use it.", tc.Function.Name),
					ToolCallID: tc.ID,
				})
				a.emit(Event{Type: EventTool, Tool: tc.Function.Name, Message: "blocked in plan mode", Success: false})
				continue
			}

			// Check confirmation for destructive operations
			if a.needsConfirm(tc.Function.Name) && a.confirmFn != nil {
				fp := Fingerprint(tc.Function.Name, tc.Function.Arguments)
				if !a.permMgr.IsAllowed(fp) {
					cr := a.confirmFn(tc.Function.Name, detail)
					switch cr.Action {
					case ConfirmDeny:
						a.history = append(a.history, ollama.Message{
							Role:       "tool",
							Content:    "Operation denied by user",
							ToolCallID: tc.ID,
						})
						a.emit(Event{Type: EventTool, Tool: tc.Function.Name, Message: "denied by user", Success: false})
						continue
					case ConfirmAllowOnce:
						// proceed to execute
					case ConfirmAllowSession:
						a.permMgr.AllowSession(fp)
					case ConfirmAugment:
						a.history = append(a.history, ollama.Message{
							Role:       "tool",
							Content:    fmt.Sprintf("User rejected this call and provided a constraint: %s\nPlease rebuild this %s call accordingly.", cr.Augment, tc.Function.Name),
							ToolCallID: tc.ID,
						})
						a.emit(Event{Type: EventTool, Tool: tc.Function.Name, Message: "augmented: " + cr.Augment, Success: false})
						augmented = true
					}
					if augmented {
						break
					}
				}
			}

			a.executeSingle(ctx, tc)
		}

		if augmented {
			continue // re-send to LLM with constraint
		}

		// Emit intermediate text if any (skip for plan mode — only final response goes into planBuf)
		if resp.Content != "" && a.mode != ModePlan {
			a.emit(Event{Type: EventAgent, Message: resp.Content})
		}
	}

	// Extract plan from accumulated text on max iterations.
	if a.mode == ModePlan {
		if a.planBuf.Len() > 0 {
			a.lastPlan = extractPlan(a.planBuf.String())
		}
		if a.lastPlan == "" && a.planBuf.Len() > 0 {
			// Max iterations exhausted with content but no structured plan.
			// Use the raw text as-is — better than nothing.
			a.lastPlan = strings.TrimSpace(a.planBuf.String())
		}
	}

	a.stats.AvgTkPerSec = a.stats.avgTkPerSec()
	a.emit(Event{Type: EventDone, Message: "Max iterations reached", Stats: &a.stats})
	return errors.WithStack(ErrMaxIterations)
}

// Run executes the agent loop for a given task (one-shot, no history preservation).
func (a *Agent) Run(ctx context.Context, task, ruleCatalog string) error {
	a.Init(ruleCatalog)
	return a.Send(ctx, task)
}

func (a *Agent) toolDefsForMode() []ollama.ToolDef {
	if a.mode == ModeEdit {
		return a.reg.ToolDefs()
	}
	return a.reg.ToolDefsFiltered(readOnlyTools)
}

// canRunParallel returns true if a tool call can safely run in the worker pool.
// Only read-only tools that don't need confirmation qualify.
func (a *Agent) canRunParallel(toolName string) bool {
	if a.pool == nil || cap(a.pool.sem) <= 1 {
		return false
	}
	return (readOnlyTools[toolName] || a.extraReadOnlyTools[toolName]) && !a.isToolBlocked(toolName)
}

// executeParallel runs read-only tool calls concurrently via the worker pool
// and appends results to history in the original order.
func (a *Agent) executeParallel(ctx context.Context, calls []ollama.ToolCall) {
	tasks := make([]TaskFunc, len(calls))
	for i, tc := range calls {
		name := tc.Function.Name
		args := tc.Function.Arguments
		tasks[i] = func(ctx context.Context) (tools.ToolResult, error) {
			return a.reg.Execute(ctx, name, args)
		}
	}

	results := a.pool.Execute(ctx, tasks)

	for i, tr := range results {
		tc := calls[i]
		if tr.Err != nil {
			a.emit(Event{Type: EventError, Tool: tc.Function.Name, Message: tr.Err.Error()})
			a.history = append(a.history, ollama.Message{
				Role:       "tool",
				Content:    "Error: " + tr.Err.Error(),
				ToolCallID: tc.ID,
			})
			continue
		}
		a.emitToolResult(tc, tr.Result)
	}
}

// executeSingle runs a single tool call and records the result.
func (a *Agent) executeSingle(ctx context.Context, tc ollama.ToolCall) {
	result, err := a.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
	if err != nil {
		a.emit(Event{Type: EventError, Tool: tc.Function.Name, Message: err.Error()})
		// Still add error to history so the model gets feedback.
		a.history = append(a.history, ollama.Message{
			Role:       "tool",
			Content:    "Error: " + err.Error(),
			ToolCallID: tc.ID,
		})
		return
	}
	a.emitToolResult(tc, result)
}

// emitToolResult emits an event and appends the tool result to history.
func (a *Agent) emitToolResult(tc ollama.ToolCall, result tools.ToolResult) {
	// For read-only tools, show detail (path) instead of output content.
	msg := truncate(result.Output, 200)
	if readOnlyTools[tc.Function.Name] {
		msg = toolDetail(tc.Function.Name, tc.Function.Arguments)
	}
	ev := Event{
		Type:    EventTool,
		Tool:    tc.Function.Name,
		Message: msg,
		Success: result.Success,
	}
	if result.Meta != nil {
		if v, ok := result.Meta["action"].(string); ok {
			ev.FileAction = v
		}
		if v, ok := result.Meta["added"].(int); ok {
			ev.LinesAdded = v
		}
		if v, ok := result.Meta["removed"].(int); ok {
			ev.LinesRemoved = v
		}
		if v, ok := result.Meta["diff"].(string); ok {
			ev.Diff = v
		}
	}
	a.emit(ev)

	a.stats.ToolCalls++
	if ev.FileAction != "" {
		a.stats.recordFileAction(ev.FileAction, ev.LinesAdded, ev.LinesRemoved)
	}

	resultContent := result.Output
	if !result.Success && result.Error != "" {
		resultContent = "Error: " + result.Error
	}
	if len(resultContent) > maxToolResultBytes {
		resultContent = resultContent[:maxToolResultBytes] + "\n... [truncated, total " + fmt.Sprintf("%d", len(result.Output)) + " bytes]"
	}
	a.history = append(a.history, ollama.Message{
		Role:       "tool",
		Content:    resultContent,
		ToolCallID: tc.ID,
	})
}

func (a *Agent) isToolBlocked(tool string) bool {
	return a.mode == ModePlan && !readOnlyTools[tool]
}

func (a *Agent) needsConfirm(tool string) bool {
	switch tool {
	case "write_file", "edit_file", "delete_file", "move_file", "bash":
		return true
	}
	return a.extraConfirmTools[tool]
}

func toolDetail(name string, args map[string]any) string {
	switch name {
	case "read_file", "write_file", "edit_file", "delete_file", "list_dir", "find_files", "create_dir":
		if p, ok := args["path"].(string); ok {
			return p
		}
	case "move_file":
		from, _ := args["from"].(string)
		to, _ := args["to"].(string)
		return from + " → " + to
	case "grep":
		pattern, _ := args["pattern"].(string)
		path, _ := args["path"].(string)
		return fmt.Sprintf("%q in %s", pattern, path)
	case "go_build", "go_test", "go_lint", "go_vet":
		if p, ok := args["packages"].(string); ok {
			return p
		}
		return "./..."
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 80 {
				cmd = cmd[:80] + "..."
			}
			return cmd
		}
	case "snippets":
		if action, ok := args["action"].(string); ok && action != "" {
			return action
		}
		return "match"
	}
	return name
}

func langName(code string) string {
	switch code {
	case "ru":
		return "Russian"
	case "en":
		return "English"
	case "de":
		return "German"
	case "fr":
		return "French"
	case "es":
		return "Spanish"
	case "zh":
		return "Chinese"
	case "ja":
		return "Japanese"
	case "":
		return "English"
	default:
		return code
	}
}

func (a *Agent) buildSystemPrompt() string {
	lang := langName(a.language)

	var b strings.Builder
	b.WriteString("You are KodRun, a Go programming assistant.\n")
	fmt.Fprintf(&b, "IMPORTANT: ALL your responses MUST be in %s. This is mandatory.\n\n", lang)

	if a.ruleCatalog != "" {
		b.WriteString(a.ruleCatalog)
		b.WriteString("\n")
	}

	if a.mode == ModePlan {
		b.WriteString("You are in PLAN mode (READ-ONLY).\n")
		b.WriteString("You can ONLY analyze code and create plans. You CANNOT modify files.\n")
		b.WriteString("You MUST NOT call any tools besides: " + strings.Join(a.reg.NamesFiltered(readOnlyTools), ", ") + "\n\n")
		b.WriteString("IMPORTANT — Questions vs Tasks:\n")
		b.WriteString("- If the user asks a QUESTION (about Go, naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		b.WriteString("- If the user gives a TASK (fix, refactor, add feature, etc.) — create a numbered plan.\n\n")
		b.WriteString("STRICT RULES (for tasks):\n")
		b.WriteString("- NEVER generate code blocks, patches, diffs, or file contents\n")
		b.WriteString("- NEVER show code that should be written or changed\n")
		b.WriteString("- NEVER call write_file, edit_file, delete_file, bash, or any write tool\n")
		b.WriteString("- If asked to fix, edit, or write code — describe WHAT to change, not HOW in code\n")
		b.WriteString("- Your plan must be a numbered list with text descriptions only\n")
		b.WriteString("- Do NOT read binary files, build artifacts, or IDE config directories\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Read and analyze only *.go source files and project docs\n")
		b.WriteString("- Identify files that need changes\n")
		b.WriteString("- Propose a step-by-step plan\n")
		b.WriteString("- Estimate complexity and risks\n")
		b.WriteString("- Be concise and actionable\n")
		b.WriteString("- Reference Go best practices and project conventions\n")
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project conventions (from RAG):\n")
			b.WriteString("Project conventions and documentation are automatically included in the task context.\n")
			b.WriteString("You MUST read and follow ALL conventions provided. Incorporate them as requirements in the plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE creating the plan. This is not optional.\n")
			b.WriteString("1. Call snippets(paths=[<list of all .go files you read>]) to get code conventions\n")
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then create the plan, incorporating found conventions as requirements\n")
		}
	} else {
		b.WriteString("You are in EDIT mode.\n")
		b.WriteString("Available tools: " + strings.Join(a.reg.Names(), ", ") + "\n\n")
		b.WriteString("Guidelines:\n")
		b.WriteString("- Write idiomatic Go code\n")
		b.WriteString("- Use go best practice and project's guides\n")
		b.WriteString("- Use go version 1.25+\n")
		b.WriteString("- Handle errors properly\n")
		b.WriteString("- Use edit_file for targeted changes, write_file for new files\n")
		b.WriteString("- Be concise in responses\n")
		b.WriteString("- Do NOT repeat or quote file contents in your responses. Reference files by path only.\n")
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project conventions (from RAG):\n")
			b.WriteString("Project conventions and documentation are automatically included in the task context.\n")
			b.WriteString("You MUST follow ALL conventions provided (naming, structure, patterns, error handling).\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE writing or modifying any file. This is not optional.\n")
			b.WriteString("1. Call snippets(paths=[<file_paths>]) to get code conventions\n")
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then write/edit code, following ALL found conventions (naming, structure, patterns, error handling)\n")
			b.WriteString("4. If no snippets match, proceed without conventions\n")
		}
		b.WriteString("\nAfter completing EVERY task you MUST run this verification sequence:\n")
		b.WriteString("1. Run go_build to verify compilation. If errors — fix them and re-run.\n")
		b.WriteString("2. Run go_lint to check code quality. If errors — fix them and re-run.\n")
		b.WriteString("3. Run go_test to verify correctness. If errors — fix them and re-run.\n")
		b.WriteString("4. Update AGENTS.md if you changed architecture, added/removed files, or modified public APIs.\n")
		b.WriteString("   Use read_file to read AGENTS.md first, then edit_file to update only the relevant sections.\n")
		b.WriteString("   Do NOT rewrite the entire file — only update what changed.\n")
	}

	// Repeat language directive at the end for reinforcement (important for local models).
	if lang != "English" {
		fmt.Fprintf(&b, "\nREMINDER: You MUST respond in %s. Never switch to English.\n", lang)
	}

	return b.String()
}

func (a *Agent) buildProjectContext() string {
	var b strings.Builder

	agentsMD := filepath.Join(a.workDir, "AGENTS.md")
	if data, err := os.ReadFile(agentsMD); err == nil {
		b.WriteString("Project documentation (AGENTS.md):\n")
		b.Write(data)
		b.WriteString("\n\n")
	}

	goMod := filepath.Join(a.workDir, "go.mod")
	if data, err := os.ReadFile(goMod); err == nil {
		b.WriteString("go.mod:\n```\n")
		b.Write(data)
		b.WriteString("\n```\n\n")
	}

	return b.String()
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// extractPlan extracts the plan section from accumulated planner text.
// Takes the LAST occurrence of a context marker (## Context / CONTEXT).
// If no context marker found, falls back to the last plan marker (## Plan / PLAN).
// Trims trailing garbage (non-plan text after the numbered list).
func extractPlan(text string) string {
	// Priority 1: find last context marker (includes both context + plan sections).
	contextMarkers := []string{"## Context", "## Контекст", "\nCONTEXT", "\nКОНТЕКСТ"}
	for _, marker := range contextMarkers {
		if idx := strings.LastIndex(text, marker); idx >= 0 {
			return trimPlanGarbage(strings.TrimSpace(text[idx:]))
		}
	}
	// Priority 2: find last plan marker (prefix match — "ПЛАН УЛУЧШЕНИЙ" matches "\nПЛАН").
	planMarkers := []string{
		"## Plan", "## План",
		"\nPLAN", "\nПЛАН",
		"\nКОНКРЕТНЫЕ ИЗМЕНЕНИЯ", "\nКОНКРЕТНЫЕ ПРАВКИ",
	}
	for _, marker := range planMarkers {
		if idx := strings.LastIndex(text, marker); idx >= 0 {
			return trimPlanGarbage(strings.TrimSpace(text[idx:]))
		}
	}
	// No markers found. Only return text if it contains a numbered list (actual plan).
	// Otherwise, it's just conversational garbage — return empty.
	if hasNumberedList(text) {
		return trimPlanGarbage(strings.TrimSpace(text))
	}
	return ""
}

// hasNumberedList checks if the text contains at least one numbered list item (e.g. "1. ..." or "1) ...").
func hasNumberedList(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' &&
			(trimmed[1] == '.' || trimmed[1] == ')') {
			return true
		}
	}
	return false
}

// trimPlanGarbage removes trailing non-plan text after the numbered list.
// Keeps the header + numbered items; stops at the first line that is neither
// empty, a header (##/PLAN/CONTEXT), nor a numbered list item / continuation.
func trimPlanGarbage(text string) string {
	lines := strings.Split(text, "\n")
	lastPlanLine := 0
	inPlan := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Headers
		if strings.HasPrefix(trimmed, "##") ||
			trimmed == "PLAN" || trimmed == "ПЛАН" ||
			trimmed == "CONTEXT" || trimmed == "КОНТЕКСТ" {
			lastPlanLine = i
			inPlan = strings.HasPrefix(trimmed, "##") && (strings.Contains(trimmed, "Plan") || strings.Contains(trimmed, "План")) ||
				trimmed == "PLAN" || trimmed == "ПЛАН"
			continue
		}
		// Numbered list items: "1. ..." or "1) ..."
		if len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' &&
			(strings.Contains(trimmed[:min(5, len(trimmed))], ".") || strings.Contains(trimmed[:min(5, len(trimmed))], ")")) {
			lastPlanLine = i
			inPlan = true
			continue
		}
		// Continuation of a numbered item (indented or starts with text after a number was seen).
		if inPlan && (line[0] == ' ' || line[0] == '\t') {
			lastPlanLine = i
			continue
		}
		// Non-plan line after plan started — stop.
		if inPlan {
			break
		}
		// Context section text before PLAN.
		lastPlanLine = i
	}

	return strings.TrimSpace(strings.Join(lines[:lastPlanLine+1], "\n"))
}
