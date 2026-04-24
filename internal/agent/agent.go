/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/tools"
)

// ErrMaxIterations is returned when the agent exceeds max iterations.
var ErrMaxIterations = errors.New("max iterations reached")

// maxToolResultBytes limits tool output stored in history to prevent unbounded memory growth.
const maxToolResultBytes = 16 * 1024 // 16KB

const (
	maxFingerprintLen         = 80              // max chars for fingerprint/display truncation
	ragTopK                   = 5               // number of top RAG results to include
	truncatePreviewLen        = 200             // max chars for tool result preview
	diffPreviewMaxLines       = 30              // max lines in edit/write diff preview
	previewNewFileLines       = 20              // max lines when previewing a new file
	toolDetailTruncate        = 60              // max chars for tool detail truncation
	planPrefixScanLen         = 5               // max chars to scan for list markers in plan detection
	charsPerToken             = 4               // approximate chars per token for ASCII text
	autoCompactThreshold      = 0.99            // context fill ratio that triggers auto-compact
	inferenceProgressInterval = 2 * time.Second // throttle interval for inference progress events
	nanosPerSec               = 1e9             // nanoseconds in one second
	pctMultiplier             = 100             // multiplier to convert ratio to percentage
	fileListMaxLen            = 120             // max chars for file list in tool result
	toolArgTruncateLen        = 80              // max chars for tool argument display

	actionDelete       = "Delete"
	toolNameReadFile   = "read_file"
	toolNameWriteFile  = "write_file"
	toolNameEditFile   = "edit_file"
	toolNameDeleteFile = "delete_file"
	toolNameMoveFile   = "move_file"
	toolNameFindFiles  = "find_files"
	toolNameListDir    = "list_dir"
	toolNameCreateDir  = "create_dir"
	toolNameGrep       = "grep"
	toolNameBash       = "bash"
	toolNameGoBuild    = "go_build"
	toolNameGoTest     = "go_test"
	toolNameGoLint     = "go_lint"
	toolNameGoVet      = "go_vet"
	toolNameGoDoc      = "go_doc"
	toolNameSearchDocs = "search_docs"
	toolNameSnippets   = "snippets"
	toolNameGitStatus  = "git_status"
	toolNameGitDiff    = "git_diff"
	toolNameGitLog     = "git_log"
	toolNameGitCommit  = "git_commit"
	toolNameGetRule    = "get_rule"
	toolNameWebFetch   = "web_fetch"
	headerPLAN         = "PLAN"
	headerPLANRu       = "ПЛАН"
	langEnglish        = "English"
	roleAssistant      = "assistant"
)

// Mode represents the agent operating mode.
type Mode int

const (
	// ModePlan is read-only analysis mode.
	ModePlan Mode = iota
	// ModeEdit is full tool access mode.
	ModeEdit
	// ModeChat is free-form discussion mode with read-only tool access.
	ModeChat
)

const modeChatStr = "chat"

// String returns the mode name.
func (m Mode) String() string {
	switch m {
	case ModePlan:
		return string(ClassifyKindPlan)
	case ModeChat:
		return modeChatStr
	default:
		return "edit"
	}
}

// readOnlyTools is the set of tools allowed in plan mode.
var readOnlyTools = map[string]bool{
	"file_stat":          true,
	toolNameReadFile:     true,
	"read_changed_files": true,
	toolNameWebFetch:     true,
	toolNameListDir:      true,
	toolNameFindFiles:    true,
	toolNameGrep:         true,
	toolNameGetRule:      true,
	toolNameSnippets:     true,
	toolNameSearchDocs:   true,
	toolNameGoDoc:        true,
	"go_structure":       true,
	toolNameGitStatus:    true,
	toolNameGitDiff:      true,
	toolNameGitLog:       true,
}

// EventHandler receives agent events for display.
type EventHandler func(event *Event)

// Event represents an agent lifecycle event.
type Event struct {
	Type EventType
	// GroupID optionally associates the event with a named collapsible group
	// in the TUI. When non-empty, EventAgent/EventTool events are routed
	// into that group's log buffer instead of the main log. The group is
	// created by an EventGroupStart and closed by an EventGroupEnd carrying
	// the same GroupID. Events without GroupID retain the legacy single-
	// active-group behaviour.
	GroupID            string
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

	// CacheHits / CacheMisses are populated on EventCacheStats events.
	CacheHits   int64
	CacheMisses int64

	// Progress is set on EventRAGProgress events. Done == Total signals
	// completion; the TUI hides the indicator on the next event.
	ProgressDone  int
	ProgressTotal int
	ProgressLabel string

	// CacheHit is true when the tool result was served from cache.
	CacheHit bool

	// FullOutput carries the complete tool result for the transcript view.
	// Only populated on EventTool events; empty for other event types.
	FullOutput string

	// InferenceTokens is the running count of eval tokens during inference.
	// Set on EventInferenceProgress events.
	InferenceTokens int
	// InferenceElapsed is the time since inference started.
	// Set on EventInferenceProgress events.
	InferenceElapsed time.Duration
	// InferenceContent carries accumulated LLM output since the last progress event.
	// Set on EventInferenceProgress events for the transcript view.
	InferenceContent string
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
	ChangedFiles   []string // paths of files added/modified/deleted/renamed
	tkPerSecSum    float64
	tkPerSecCount  int

	// Performance breakdown (populated per iteration).
	Iterations    int
	TotalLLMTime  time.Duration
	TotalToolTime time.Duration
}

func (s *SessionStats) reset() {
	*s = SessionStats{}
}

func (s *SessionStats) recordFileAction(action, path string, added, removed int) {
	switch action {
	case "Add":
		s.FilesAdded++
	case "Update":
		s.FilesModified++
	case actionDelete:
		s.FilesDeleted++
	case "Rename":
		s.FilesRenamed++
	}
	s.LinesAdded += added
	s.LinesRemoved += removed
	if path != "" {
		s.ChangedFiles = append(s.ChangedFiles, path)
	}
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
	EventGroupTitleUpdate
	EventModeChange
	// EventPhase signals an orchestrator phase transition (planning, executing,
	// reviewing). Message holds the phase name.
	EventPhase
	// EventCacheStats reports the latest tool-cache hit/miss counters and hit
	// rate. The TUI uses these to render a compact footer.
	EventCacheStats
	// EventReplan signals that the executor requested a replan because the
	// approved plan lacked necessary context.
	EventReplan
	// EventRAGProgress reports background RAG indexing progress. Fields:
	// ProgressDone, ProgressTotal, ProgressLabel. When Done == Total the TUI
	// stops displaying the indicator.
	EventRAGProgress
	// EventInferenceProgress reports live token generation during LLM inference.
	// Fields: InferenceTokens, InferenceElapsed. Emitted periodically (throttled)
	// while the model is generating tokens so the TUI can show progress.
	EventInferenceProgress
	// EventModelChange signals that the active model was changed at runtime.
	// Message holds the new model name. The TUI updates the toolbar display.
	EventModelChange
)

// Agent orchestrates the LLM-tool loop.
type Agent struct {
	client              llm.Client
	model               string
	reg                 *tools.Registry
	history             []llm.Message
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
	taskLabel           string
	groupID             string
	ragIndex            tools.RAGSearcher
	godocIndexer        tools.GoDocIndexer
	langState           *projectlang.State
	pool                *WorkerPool
	extraConfirmTools   map[string]bool
	extraReadOnlyTools  map[string]bool
	disabledTools       map[string]bool
	toolsDisabled       bool
	sessionDir          string
	sessionID           string

	// Generation overrides used by specialised roles (extractor uses
	// temperature=0 + format="json" to coerce structured output from the same
	// underlying model).
	temperature    float64
	hasTemperature bool
	format         string

	// allowedReadPaths, when non-nil, restricts which paths read-only file
	// tools may access. The executor uses this to prevent the model from
	// drifting into open-ended exploration after a plan has been approved.
	// Empty/nil means no restriction.
	allowedReadPaths map[string]struct{}

	// editNudges counts how many times the current Send() invocation has
	// pushed a "stop describing, call tools" follow-up message into history
	// because the model returned a markdown plan instead of tool calls in
	// EDIT mode. Reset at the start of every Send. See nudgeOrTerminate.
	editNudges int

	// planToolNudges counts how many times the current Send() invocation has
	// pushed a "you must call tools first" nudge in PLAN mode because the
	// model returned NO_ISSUES without calling any tools. Reset in Send().
	planToolNudges int

	// toolCallCount counts the total number of tool calls executed during
	// the current Send() invocation. Reset at the start of every Send.
	toolCallCount int

	// planBlockedStreak counts consecutive iterations where ALL tool calls
	// were blocked (write tools in plan mode). When the streak reaches
	// maxPlanBlocked the agent stops early to avoid wasting iterations.
	planBlockedStreak int

	// hasCalledReadFile tracks whether read_file was called during the
	// current Send() invocation. Used by the guided-flow nudge to detect
	// when a model did file_stat but skipped read_file.
	hasCalledReadFile bool

	// readFileNudges counts how many times the current Send() pushed a
	// "now call read_file" nudge after detecting file_stat without read_file.
	readFileNudges int

	// verbose enables per-iteration timing diagnostics.
	verbose bool
}

// New creates a new Agent.
func New(client llm.Client, model string, reg *tools.Registry, maxIter int, workDir string, contextSize int) *Agent {
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

// SetTaskLabel sets a human-readable label that Send() emits instead of the
// generic "Processing task..." message, so the TUI can show which sub-agent
// is currently working (e.g. "Reviewing: security"). Empty string disables
// the start-of-task status line entirely (useful for sub-agents that run
// inside a group and whose activity is expressed by tool events).
func (a *Agent) SetTaskLabel(label string) {
	a.taskLabel = label
}

// SetGroupID associates every event this agent emits with the given TUI
// group. Sub-agents running concurrently inside their own collapsible
// group (e.g. parallel specialist reviewers) set this so their tool calls
// and status messages do not pollute the main log or another agent's group.
func (a *Agent) SetGroupID(id string) {
	a.groupID = id
}

// SetVerbose enables per-iteration timing diagnostics.
func (a *Agent) SetVerbose(v bool) { a.verbose = v }

// SetHasSnippets marks that the snippets tool is available.
func (a *Agent) SetHasSnippets(v bool) {
	a.hasSnippets = v
}

// SetHasRAG marks that the RAG search_docs tool is available.
func (a *Agent) SetHasRAG(v bool) {
	a.hasRAG = v
}

// SetRAGIndex sets the RAG index for automatic context prefetch.
func (a *Agent) SetRAGIndex(idx tools.RAGSearcher) {
	a.ragIndex = idx
}

// SetGodocIndexer sets the Go documentation RAG indexer used by go_doc
// for lazy language tool re-registration.
func (a *Agent) SetGodocIndexer(idx tools.GoDocIndexer) {
	a.godocIndexer = idx
}

// SetLanguageState wires a project-language state for lazy re-detection.
// When set, Send/Run will re-detect on each call and lazily register
// language-specific tools if the language transitions from unknown.
func (a *Agent) SetLanguageState(s *projectlang.State) {
	a.langState = s
}

// LanguageState returns the configured language state, or nil.
func (a *Agent) LanguageState() *projectlang.State {
	return a.langState
}

// currentProgLang returns the detected project programming language as a string,
// or "" if unknown.
func (a *Agent) currentProgLang() string {
	if a.langState == nil {
		return ""
	}
	return string(a.langState.Current())
}

// ToolRegistry exposes the tool registry for callers that need to register
// additional tools after construction (e.g. lazy language detection).
func (a *Agent) ToolRegistry() *tools.Registry {
	return a.reg
}

// WorkDir returns the agent's working directory.
func (a *Agent) WorkDir() string {
	return a.workDir
}

// SetMaxToolWorkers configures the worker pool for parallel tool execution.
func (a *Agent) SetMaxToolWorkers(n int) {
	a.pool = NewWorkerPool(n)
}

// SetTemperature pins the sampling temperature used in chat requests. This is
// primarily used by the extractor role to force deterministic JSON output.
func (a *Agent) SetTemperature(t float64) {
	a.temperature = t
	a.hasTemperature = true
}

// SetFormat sets the response format hint for the LLM (e.g. "json"). When set,
// it is forwarded to ollama via the chat request Format field.
func (a *Agent) SetFormat(f string) {
	a.format = f
}

// SetAllowedReadPaths constrains which paths the agent's read-only file tools
// may access. Pass nil to remove the restriction. The path strings are
// normalised relative to the agent's work directory.
//
// This is used by the executor role after plan approval to enforce the
// "executor does not re-explore the codebase" contract.
func (a *Agent) SetAllowedReadPaths(paths []string) {
	if len(paths) == 0 {
		a.allowedReadPaths = nil
		return
	}
	m := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		m[p] = struct{}{}
	}
	a.allowedReadPaths = m
}

// pathAllowed returns true if the given path may be read under the current
// whitelist. When no whitelist is set, all paths are allowed.
func (a *Agent) pathAllowed(path string) bool {
	if a.allowedReadPaths == nil {
		return true
	}
	if path == "" {
		return false
	}
	if _, ok := a.allowedReadPaths[path]; ok {
		return true
	}
	// Allow basename matches so that path/to/foo.go and foo.go both work when
	// either form appears in the plan.
	base := filepath.Base(path)
	if _, ok := a.allowedReadPaths[base]; ok {
		return true
	}
	return false
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

// DisableTools marks tool names that must never be offered to the model or
// executed, regardless of mode. Used to honour config flags like auto_commit.
func (a *Agent) DisableTools(names ...string) {
	if a.disabledTools == nil {
		a.disabledTools = make(map[string]bool, len(names))
	}
	for _, n := range names {
		a.disabledTools[n] = true
	}
}

// SetModel changes the model used for subsequent LLM calls.
func (a *Agent) SetModel(model string) {
	a.model = model
}

// Model returns the current model name.
func (a *Agent) Model() string {
	return a.model
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

// ToolCallCount returns the number of tool calls executed during the last Send.
func (a *Agent) ToolCallCount() int {
	return a.toolCallCount
}

// LastAssistantMessage returns the content of the most recent assistant
// message in the agent's history, or "" if there is none. Used by the
// structurer/extractor pipeline to fetch raw model output without going
// through the lastPlan extraction.
func (a *Agent) LastAssistantMessage() string {
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == roleAssistant {
			return a.history[i].Content
		}
	}
	return ""
}

// EnterEditWithPlan switches to edit mode, clears context,
// and injects the approved plan as the first user message.
func (a *Agent) EnterEditWithPlan() {
	a.mode = ModeEdit
	a.think = false
	systemPrompt := a.buildSystemPrompt()
	a.history = []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Execute the following approved plan:\n\n%s\n\nImplement each step. Confirm plan steps as you go. Always respond in %s.", a.lastPlan, langName(a.language))},
	}
	a.contextInjected = false
}

// SetSessionDir enables session auto-save. Sessions are stored in dir.
func (a *Agent) SetSessionDir(dir string) {
	a.sessionDir = dir
	if a.sessionID == "" {
		a.sessionID = NewSessionID()
	}
}

// SessionID returns the current session ID.
func (a *Agent) SessionID() string {
	return a.sessionID
}

// LoadFromSession restores agent state from a saved session.
func (a *Agent) LoadFromSession(s *Session) {
	a.history = make([]llm.Message, len(s.Messages))
	copy(a.history, s.Messages)

	switch s.Mode {
	case string(ClassifyKindPlan):
		a.mode = ModePlan
	case modeChatStr:
		a.mode = ModeChat
	default:
		a.mode = ModeEdit
	}

	a.lastPlan = s.Plan
	a.stats = s.Stats
	a.sessionID = s.ID
	a.contextInjected = true // context was already in saved history
}

// autoSave persists the current agent state to disk if sessionDir is set.
func (a *Agent) autoSave() {
	if a.sessionDir == "" {
		return
	}

	session := &Session{
		ID:        a.sessionID,
		CreatedAt: time.Time{}, // will be set on first save
		Model:     a.model,
		Mode:      a.mode.String(),
		Messages:  a.History(),
		Plan:      a.lastPlan,
		Stats:     a.stats,
		WorkDir:   a.workDir,
	}

	// Preserve CreatedAt from existing session on disk.
	existing, err := LoadSession(a.sessionDir, a.sessionID)
	if err == nil {
		session.CreatedAt = existing.CreatedAt
	} else {
		session.CreatedAt = time.Now()
	}

	if err := SaveSession(a.sessionDir, session); err != nil {
		a.emit(&Event{Type: EventError, Message: "session auto-save failed: " + err.Error()})
	}
}

func (a *Agent) emit(e *Event) {
	if a.onEvent == nil {
		return
	}
	// Stamp the caller's group id if the event does not already carry one.
	// This is how sub-agents inherit their parent group in the TUI without
	// every call site having to know about groups.
	if e.GroupID == "" && a.groupID != "" {
		e.GroupID = a.groupID
	}
	a.onEvent(e)
}

// Init initializes the agent with system prompt. Call once before Send().
func (a *Agent) Init(ruleCatalog string) {
	a.ruleCatalog = ruleCatalog
	systemPrompt := a.buildSystemPrompt()
	a.projectContext = a.buildProjectContext()
	a.contextInjected = false
	a.history = []llm.Message{
		{Role: "system", Content: systemPrompt},
	}
	a.ctxMgr = NewContextManager(a.contextSize, a.client, a.model)
	a.ctxMgr.SetLanguage(a.language)
	a.emit(&Event{
		Type:               EventAgent,
		SystemPromptTokens: len(systemPrompt) / charsPerToken,
	})
}

// InitWithPrompt initializes the agent with a custom system prompt.
// Used by the Orchestrator to set role-specific prompts.
func (a *Agent) InitWithPrompt(systemPrompt string) {
	a.projectContext = a.buildProjectContext()
	a.contextInjected = false
	a.history = []llm.Message{
		{Role: "system", Content: systemPrompt},
	}
	a.ctxMgr = NewContextManager(a.contextSize, a.client, a.model)
	a.ctxMgr.SetLanguage(a.language)
}

// ClearHistory resets conversation history, keeping the system prompt.
func (a *Agent) ClearHistory() {
	systemPrompt := a.buildSystemPrompt()
	a.history = []llm.Message{
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

	a.emit(&Event{
		Type:         EventCompact,
		Message:      fmt.Sprintf("Context compacted: ~%d → ~%d tokens (freed ~%d)", before, after, before-after),
		ContextUsed:  after,
		ContextTotal: a.contextSize,
	})

	return nil
}

// History returns a copy of the current conversation history.
func (a *Agent) History() []llm.Message {
	h := make([]llm.Message, len(a.history))
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

// ensureLanguageDetected re-runs project language detection if it is still
// unknown and lazily registers the matching language-specific tools when a
// language is discovered for the first time.
func (a *Agent) ensureLanguageDetected() {
	if a.langState == nil {
		return
	}
	lang, changed := a.langState.EnsureDetected()
	if !changed || lang == projectlang.LangUnknown {
		return
	}
	tools.RegisterLanguageTools(a.reg, lang, a.workDir, a.godocIndexer)
	a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Project language detected: %s — language tools registered", lang)})
}

// Send processes a single user message, preserving conversation history.
func (a *Agent) Send(ctx context.Context, task string) error {
	a.ensureLanguageDetected()
	userContent := task
	if a.hasRAG && a.ragIndex != nil {
		if results, err := a.ragIndex.Search(ctx, task, ragTopK); err == nil && len(results) > 0 {
			userContent = formatRAGResults(results) + "\n" + userContent
		}
	}
	if a.projectContext != "" && !a.contextInjected {
		userContent = "[Project context]\n" + a.projectContext + "\n\n[Task]\n" + userContent
		a.contextInjected = true
	}
	a.history = append(a.history, llm.Message{Role: "user", Content: userContent})
	a.stats.reset()
	a.planBuf.Reset()
	a.editNudges = 0
	a.planToolNudges = 0
	a.toolCallCount = 0
	a.planBlockedStreak = 0
	a.hasCalledReadFile = false
	a.readFileNudges = 0

	// Emit a start-of-task status line only when a label was set. Sub-agents
	// that live inside a collapsible group (parallel specialist reviewers)
	// set an empty label on purpose so their activity in the UI is limited
	// to actual tool calls, keeping the group tail free for read_file/etc.
	if a.taskLabel != "" {
		a.emit(&Event{Type: EventAgent, Message: a.taskLabel})
	}

	var iterCount int
	for range a.maxIter {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		iterCount++
		iterStart := time.Now()

		if a.verbose {
			a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Iteration %d/%d: requesting model...", iterCount, a.maxIter)})
		}

		// Auto-compact by real prompt_eval_count when context is >=99% full
		if a.autoCompact && a.ctxMgr != nil && a.lastPromptEvalCount > 0 && a.contextSize > 0 {
			ratio := float64(a.lastPromptEvalCount) / float64(a.contextSize)
			if ratio >= autoCompactThreshold {
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
				a.emit(&Event{
					Type:         EventCompact,
					Message:      fmt.Sprintf("Auto-trimmed: ~%d → ~%d tokens", before, after),
					ContextUsed:  after,
					ContextTotal: a.contextSize,
				})
			} else if err == nil {
				a.history = trimmed
			}
		}

		opts := map[string]any{
			"num_ctx": a.contextSize,
			"think":   a.think,
		}
		if a.hasTemperature {
			opts["temperature"] = a.temperature
		}
		var lastProgressEmit time.Time
		var contentBuf strings.Builder
		progressCb := func(tokensSoFar int, content string) {
			contentBuf.WriteString(content)
			if time.Since(lastProgressEmit) >= inferenceProgressInterval {
				a.emit(&Event{
					Type:             EventInferenceProgress,
					InferenceTokens:  tokensSoFar,
					InferenceElapsed: time.Since(iterStart),
					InferenceContent: contentBuf.String(),
				})
				contentBuf.Reset()
				lastProgressEmit = time.Now()
			}
		}
		resp, err := a.client.ChatSyncWithCallback(ctx, &llm.ChatRequest{
			Model:    a.model,
			Messages: a.history,
			Tools:    a.toolDefsForMode(),
			Options:  opts,
			Format:   a.format,
		}, progressCb)
		llmDuration := time.Since(iterStart)

		// Flush remaining streaming content that didn't hit the throttle interval.
		if contentBuf.Len() > 0 {
			a.emit(&Event{
				Type:             EventInferenceProgress,
				InferenceTokens:  resp.EvalCount,
				InferenceElapsed: llmDuration,
				InferenceContent: contentBuf.String(),
			})
			contentBuf.Reset()
		}

		if err != nil {
			a.emit(&Event{Type: EventError, Message: err.Error()})
			return errors.WithMessage(err, "chat")
		}

		// Track real prompt token count for auto-compact and calibrate estimator
		if resp.PromptEvalCount > 0 {
			a.lastPromptEvalCount = resp.PromptEvalCount
			if a.ctxMgr != nil {
				a.ctxMgr.Calibrate(a.history, resp.PromptEvalCount)
			}
		}

		// Emit token usage
		if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
			var tkPerSec float64
			if resp.EvalDuration > 0 {
				tkPerSec = float64(resp.EvalCount) / float64(resp.EvalDuration) * nanosPerSec
			}
			a.emit(&Event{
				Type:         EventTokens,
				PromptTokens: resp.PromptEvalCount,
				EvalTokens:   resp.EvalCount,
				EvalTkPerSec: tkPerSec,
				ContextUsed:  resp.PromptEvalCount,
				ContextTotal: a.contextSize,
			})
			var ctxPct int
			if a.contextSize > 0 {
				ctxPct = resp.PromptEvalCount * pctMultiplier / a.contextSize
			}
			a.stats.recordTokens(resp.PromptEvalCount, resp.EvalCount, tkPerSec, ctxPct)
		}

		// Add assistant response to history
		msg := llm.Message{
			Role:      roleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		a.history = append(a.history, msg)

		// No tool calls = candidate final response.
		if len(resp.ToolCalls) == 0 {
			// Synthetic tool-call recovery: some models write edit_file /
			// write_file as plain text instead of a JSON tool call. Parse
			// the text and execute the tool synthetically so the step
			// succeeds without a nudge round-trip.
			if a.mode == ModeEdit {
				if synth := parseTextToolCall(resp.Content); synth != nil {
					a.emit(&Event{Type: EventAgent, Message: "Recovered text-form tool call — executing synthetically."})
					// Replace the text-only assistant message (appended above)
					// with one carrying the synthetic tool call so the
					// subsequent tool-result message has a matching ToolCallID.
					a.history[len(a.history)-1] = llm.Message{
						Role:      roleAssistant,
						Content:   "",
						ToolCalls: []llm.ToolCall{*synth},
					}
					a.toolCallCount++
					a.executeSingle(ctx, *synth)
					continue
				}
			}

			// EDIT-mode nudge: local models routinely answer an action task
			// with a markdown plan instead of calling write_file/edit_file.
			// Detect that pattern and push one or two follow-ups before
			// giving up. This is the safety net behind the EDIT system
			// prompt — it does not fix the model, only the symptom.
			if a.mode == ModeEdit && a.editNudges < maxEditNudges && looksLikeMarkdownPlan(resp.Content) {
				a.editNudges++
				a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Model responded with text instead of tool calls; nudging (%d/%d)...", a.editNudges, maxEditNudges)})
				a.history = append(a.history, llm.Message{
					Role:    "user",
					Content: "Stop. You are in EDIT mode. Do NOT describe the changes — apply them now via write_file/edit_file/bash. Begin with the first tool call. Do not explain. Do not output another plan.",
				})
				continue
			}
			if a.mode == ModePlan {
				// PLAN-mode nudge: specialist reviewers must call tools
				// (file_stat, read_file) before concluding. If the model
				// returned NO_ISSUES without any tool calls, nudge it once.
				content := strings.TrimSpace(resp.Content)
				if !a.toolsDisabled && a.toolCallCount == 0 && a.planToolNudges < maxPlanToolNudges &&
					(content == "" || isNoIssues(content)) {
					a.planToolNudges++
					a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
						"Model responded without calling any tools; nudging (%d/%d)...",
						a.planToolNudges, maxPlanToolNudges)})
					a.history = append(a.history, llm.Message{
						Role:    "user",
						Content: "You responded without calling any tools. This is FORBIDDEN. Re-read the task above: it contains a file list section with bullet paths. You MUST call file_stat on EACH file from that list. Do NOT guess file paths — use ONLY the paths listed in the task. Start now.",
					})
					continue
				}
				// Guided-flow nudge: model called file_stat but never
				// read_file — it cannot review code it hasn't read.
				// Push one follow-up directing it to the next step.
				if a.toolCallCount > 0 && !a.hasCalledReadFile && a.readFileNudges < maxReadFileNudges {
					a.readFileNudges++
					a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
						"Model called file_stat but not read_file; nudging (%d/%d)...",
						a.readFileNudges, maxReadFileNudges)})
					a.history = append(a.history, llm.Message{
						Role:    "user",
						Content: "Good, you called file_stat. Now you MUST call read_file(path) on each file to read its contents. Use the EXACT paths from the file list in the task — do NOT guess or modify paths. You cannot review code without reading it. Call read_file now.",
					})
					continue
				}
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
				a.emit(&Event{Type: EventAgent, Message: resp.Content})
				if a.editNudges >= maxEditNudges {
					a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("Model returned text-only response in EDIT mode after %d nudges — aborting without changes. Consider using a stronger model or lowering executor temperature (see executor_provider: precise).", maxEditNudges)})
				}
			}
			a.stats.AvgTkPerSec = a.stats.avgTkPerSec()
			a.emit(&Event{Type: EventDone, Message: "Done", Stats: &a.stats})
			a.autoSave()
			return nil
		}

		// Split tool calls into parallel (read-only) and sequential groups.
		var parallelCalls []llm.ToolCall
		var sequentialCalls []llm.ToolCall
		for _, tc := range resp.ToolCalls {
			if a.canRunParallel(tc.Function.Name) {
				parallelCalls = append(parallelCalls, tc)
			} else {
				sequentialCalls = append(sequentialCalls, tc)
			}
		}

		// Execute read-only tools in parallel via WorkerPool.
		if len(parallelCalls) > 0 {
			a.toolCallCount += len(parallelCalls)
			a.executeParallel(ctx, parallelCalls)
		}

		// Execute remaining tools sequentially (write ops, confirm, blocked).
		augmented := false
		allBlocked := len(sequentialCalls) > 0
		for _, tc := range sequentialCalls {
			// Block destructive tools in plan mode — check BEFORE emitting
			// the tool event so the TUI never shows a write-tool attempt.
			if a.isToolBlocked(tc.Function.Name) {
				a.history = append(a.history, llm.Message{
					Role:       "tool",
					Content:    fmt.Sprintf("Tool %q is NOT available in %s mode. Do NOT call write tools. Use ONLY: %s", tc.Function.Name, a.mode.String(), strings.Join(a.reg.NamesFiltered(readOnlyTools), ", ")),
					ToolCallID: tc.ID,
				})
				a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: fmt.Sprintf("blocked in %s mode", a.mode.String()), Success: false})
				continue
			}
			allBlocked = false

			detail := toolDetail(tc.Function.Name, tc.Function.Arguments)
			a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: detail})

			// Check confirmation for destructive operations
			if a.needsConfirm(tc.Function.Name) && a.confirmFn != nil {
				fp := Fingerprint(tc.Function.Name, tc.Function.Arguments)
				if !a.permMgr.IsAllowed(fp) {
					payload := a.buildConfirmPayload(ctx, tc.Function.Name, tc.Function.Arguments)
					cr := a.confirmFn(payload)
					switch cr.Action {
					case ConfirmDeny:
						a.history = append(a.history, llm.Message{
							Role:       "tool",
							Content:    "Operation denied by user",
							ToolCallID: tc.ID,
						})
						a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "denied by user", Success: false})
						continue
					case ConfirmAllowOnce:
						// proceed to execute
					case ConfirmAllowSession:
						a.permMgr.AllowSession(fp)
					case ConfirmAugment:
						a.history = append(a.history, llm.Message{
							Role:       "tool",
							Content:    fmt.Sprintf("User rejected this call and provided a constraint: %s\nPlease rebuild this %s call accordingly.", cr.Augment, tc.Function.Name),
							ToolCallID: tc.ID,
						})
						a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "augmented: " + cr.Augment, Success: false})
						augmented = true
					}
					if augmented {
						break
					}
				}
			}

			a.toolCallCount++
			a.executeSingle(ctx, tc)
		}

		// Track consecutive iterations where every tool call was blocked.
		// When parallel read-only calls succeeded, the iteration is useful
		// regardless of sequential blocked calls.
		if allBlocked && len(parallelCalls) == 0 {
			a.planBlockedStreak++
			if a.mode == ModePlan && a.planBlockedStreak >= maxPlanBlocked {
				a.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
					"Model called only blocked tools for %d iterations — stopping analysis early.", a.planBlockedStreak)})
				raw := a.planBuf.String()
				a.lastPlan = extractPlan(raw)
				if a.lastPlan == "" && len(raw) > 0 {
					a.lastPlan = strings.TrimSpace(raw)
				}
				a.stats.AvgTkPerSec = a.stats.avgTkPerSec()
				a.emit(&Event{Type: EventDone, Message: "Done (blocked streak)", Stats: &a.stats})
				a.autoSave()
				return nil
			}
		} else {
			a.planBlockedStreak = 0
		}

		toolDuration := time.Since(iterStart) - llmDuration
		if a.verbose {
			var ctxPct int
			if a.contextSize > 0 && resp.PromptEvalCount > 0 {
				ctxPct = resp.PromptEvalCount * pctMultiplier / a.contextSize
			}
			a.emit(&Event{
				Type: EventAgent,
				Message: fmt.Sprintf("[perf] iter=%d llm=%s tools=%s total=%s ctx=%d%% tools_called=%d",
					iterCount, llmDuration.Truncate(time.Millisecond), toolDuration.Truncate(time.Millisecond),
					time.Since(iterStart).Truncate(time.Millisecond), ctxPct, len(resp.ToolCalls)),
			})
		}
		a.stats.Iterations++
		a.stats.TotalLLMTime += llmDuration
		a.stats.TotalToolTime += toolDuration

		if augmented {
			continue // re-send to LLM with constraint
		}

		// Capture intermediate text. In plan mode, accumulate into planBuf
		// so findings emitted alongside tool calls are not lost.
		if resp.Content != "" && a.mode == ModePlan {
			a.planBuf.WriteString(resp.Content)
		} else if resp.Content != "" {
			a.emit(&Event{Type: EventAgent, Message: resp.Content})
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
	a.emit(&Event{Type: EventDone, Message: "Max iterations reached", Stats: &a.stats})
	a.autoSave()
	return errors.WithStack(ErrMaxIterations)
}

// Run executes the agent loop for a given task (one-shot, no history preservation).
func (a *Agent) Run(ctx context.Context, task, ruleCatalog string) error {
	a.Init(ruleCatalog)
	return a.Send(ctx, task)
}

func (a *Agent) toolDefsForMode() []llm.ToolDef {
	if a.toolsDisabled {
		return nil
	}
	var defs []llm.ToolDef
	if a.mode == ModeEdit {
		defs = a.reg.ToolDefs()
	} else {
		defs = a.reg.ToolDefsFiltered(readOnlyTools)
	}
	if len(a.disabledTools) == 0 {
		return defs
	}
	filtered := defs[:0]
	for i := range defs {
		if !a.disabledTools[defs[i].Function.Name] {
			filtered = append(filtered, defs[i])
		}
	}
	return filtered
}

// SetToolsDisabled forces the agent to advertise no tools to the model and
// blocks any tool call attempt. Used by the extractor role: a deterministic
// JSON-only pass that must not be tempted into hallucinating tool calls.
func (a *Agent) SetToolsDisabled(disabled bool) {
	a.toolsDisabled = disabled
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
func (a *Agent) executeParallel(ctx context.Context, calls []llm.ToolCall) {
	tasks := make([]TaskFunc, len(calls))
	guards := make([]*tools.ToolResult, len(calls))
	for i, tc := range calls {
		guards[i] = a.guardReadWhitelist(tc)
		name := tc.Function.Name
		args := tc.Function.Arguments
		idx := i
		if name == toolNameReadFile {
			a.hasCalledReadFile = true
		}
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			if guards[idx] != nil {
				return guards[idx], nil
			}
			return a.reg.Execute(ctx, name, args)
		}
	}

	results := a.pool.Execute(ctx, tasks)

	for i, tr := range results {
		tc := calls[i]
		if tr.Err != nil {
			a.emit(&Event{Type: EventError, Tool: tc.Function.Name, Message: tr.Err.Error()})
			a.history = append(a.history, llm.Message{
				Role:       "tool",
				Content:    "Error: " + tr.Err.Error(),
				ToolCallID: tc.ID,
			})
			continue
		}
		a.emitToolResult(tc, tr.Result, nil)
	}
}

// guardReadWhitelist returns a synthetic refusal result if the tool call is a
// read-only file tool targeting a path outside the current allowed-read
// whitelist. Returns nil when the call is permitted.
func (a *Agent) guardReadWhitelist(tc llm.ToolCall) *tools.ToolResult {
	if a.allowedReadPaths == nil {
		return nil
	}
	name := tc.Function.Name
	// Only constrain path-bearing read tools.
	switch name {
	case toolNameReadFile, toolNameListDir, toolNameFindFiles, toolNameGrep:
	default:
		return nil
	}
	// Extract the path argument under any of the common keys.
	var p string
	if v, ok := tc.Function.Arguments["path"].(string); ok {
		p = v
	} else if v, ok := tc.Function.Arguments["root"].(string); ok {
		p = v
	}
	if p == "" {
		return nil
	}
	if a.pathAllowed(p) {
		return nil
	}
	msg := fmt.Sprintf(
		"refused: %q is not in the executor whitelist for this plan. "+
			"You may only read files listed in the approved plan. "+
			"If you need additional context, output `REPLAN: <reason>` and stop.",
		p,
	)
	return &tools.ToolResult{Output: msg}
}

// executeSingle runs a single tool call and records the result.
func (a *Agent) executeSingle(ctx context.Context, tc llm.ToolCall) {
	if guard := a.guardReadWhitelist(tc); guard != nil {
		a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "blocked by whitelist", Success: false})
		a.emitToolResult(tc, guard, nil)
		return
	}
	if tc.Function.Name == toolNameReadFile {
		a.hasCalledReadFile = true
	}
	result, err := a.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
	if err != nil {
		a.emit(&Event{Type: EventError, Tool: tc.Function.Name, Message: err.Error()})
		// Still add error to history so the model gets feedback.
		a.history = append(a.history, llm.Message{
			Role:       "tool",
			Content:    "Error: " + err.Error(),
			ToolCallID: tc.ID,
		})
		return
	}
	a.emitToolResult(tc, result, nil)
}

// emitToolResult emits an event and appends the tool result to history.
func (a *Agent) emitToolResult(tc llm.ToolCall, result *tools.ToolResult, toolErr error) {
	success := toolErr == nil
	// For read-only tools, show detail (path) instead of output content.
	msg := truncate(result.Output, truncatePreviewLen)
	if readOnlyTools[tc.Function.Name] {
		detail := toolDetail(tc.Function.Name, tc.Function.Arguments)
		if detail != "" {
			msg = detail
		}
	}
	// For batch-read tools, show file list from result meta.
	if msg == "" && result.Meta != nil {
		if fl, ok := result.Meta["file_list"].([]string); ok && len(fl) > 0 {
			msg = strings.Join(fl, ", ")
			if len(msg) > fileListMaxLen {
				msg = msg[:fileListMaxLen] + "..."
			}
		}
	}
	// Build full output for transcript view (same truncation as LLM history).
	fullOutput := result.Output
	if len(fullOutput) > maxToolResultBytes {
		fullOutput = fullOutput[:maxToolResultBytes] + "\n... [truncated]"
	}

	ev := Event{
		Type:       EventTool,
		Tool:       tc.Function.Name,
		Message:    msg,
		Success:    success,
		FullOutput: fullOutput,
	}
	if result.Meta != nil {
		if v, ok := result.Meta["cache_hit"].(bool); ok && v {
			ev.CacheHit = true
		}
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
	// Cache-hit read-only tools: suppress UI event and shorten history
	// to avoid visual noise and wasted model context.
	isCachedReadOnly := ev.CacheHit && readOnlyTools[tc.Function.Name]

	if !isCachedReadOnly {
		a.emit(&ev)
	}

	a.stats.ToolCalls++
	if ev.FileAction != "" {
		filePath := stringFromMap(tc.Function.Arguments, "path")
		a.stats.recordFileAction(ev.FileAction, filePath, ev.LinesAdded, ev.LinesRemoved)
	}

	resultContent := result.Output
	if isCachedReadOnly {
		resultContent = "[already read — see earlier tool result]"
	} else if len(resultContent) > maxToolResultBytes {
		resultContent = resultContent[:maxToolResultBytes] + "\n... [truncated, total " + fmt.Sprintf("%d", len(result.Output)) + " bytes]"
	}
	a.history = append(a.history, llm.Message{
		Role:       "tool",
		Content:    resultContent,
		ToolCallID: tc.ID,
	})
}

func (a *Agent) isToolBlocked(tool string) bool {
	if a.toolsDisabled {
		return true
	}
	if a.disabledTools[tool] {
		return true
	}
	if a.mode == ModeEdit {
		return false
	}
	// Plan and Chat modes: only read-only tools allowed.
	return !readOnlyTools[tool] && !a.extraReadOnlyTools[tool]
}

func (a *Agent) needsConfirm(tool string) bool {
	switch tool {
	case toolNameWriteFile, toolNameEditFile, toolNameDeleteFile, toolNameMoveFile, toolNameBash, toolNameGitCommit, toolNameWebFetch:
		return true
	}
	return a.extraConfirmTools[tool]
}

// buildConfirmPayload builds a ConfirmPayload for a tool call. For edit/write
// tools it tries to read the current file content and produce a unified diff
// preview so the user can see the change before approving.
func (a *Agent) buildConfirmPayload(ctx context.Context, name string, args map[string]any) ConfirmPayload {
	p := ConfirmPayload{Tool: name, Args: map[string]string{}}
	put := func(k, v string) {
		p.Args[k] = v
		p.ArgKeys = append(p.ArgKeys, k)
	}
	switch name {
	case toolNameEditFile:
		path := stringFromMap(args, "path")
		oldStr := stringFromMap(args, "old_str")
		newStr := stringFromMap(args, "new_str")
		put("path", path)
		if resolved, err := tools.SafePath(ctx, a.workDir, path); err == nil {
			if data, err := os.ReadFile(resolved); err == nil {
				content := string(data)
				if strings.Contains(content, oldStr) {
					newContent := strings.Replace(content, oldStr, newStr, 1)
					p.Preview = tools.SimpleDiff(content, newContent, path, diffPreviewMaxLines)
				}
			}
		}
		if p.Preview == "" {
			p.Preview = fallbackDiffPreview(oldStr, newStr, diffPreviewMaxLines)
		}
	case toolNameWriteFile:
		path := stringFromMap(args, "path")
		content := stringFromMap(args, "content")
		put("path", path)
		var oldContent string
		existed := false
		if resolved, err := tools.SafePath(ctx, a.workDir, path); err == nil {
			if data, err := os.ReadFile(resolved); err == nil {
				oldContent = string(data)
				existed = true
			}
		}
		if existed {
			p.Preview = tools.SimpleDiff(oldContent, content, path, diffPreviewMaxLines)
		} else {
			p.Preview = previewNewFile(content, previewNewFileLines)
		}
	case toolNameDeleteFile:
		path := stringFromMap(args, "path")
		put("path", path)
	case toolNameMoveFile:
		from := stringFromMap(args, "from")
		to := stringFromMap(args, "to")
		put("from", from)
		put("to", to)
	case toolNameBash:
		cmd := stringFromMap(args, "command")
		put("command", cmd)
		p.Danger = tools.IsDangerousCommand(cmd)
	case toolNameGitCommit:
		if msg, ok := args["message"].(string); ok {
			put("message", msg)
		}
	case toolNameWebFetch:
		if u := stringFromMap(args, "url"); u != "" {
			put("url", u)
		}
	default:
		for k, v := range args {
			put(k, fmt.Sprintf("%v", v))
		}
	}
	return p
}

func fallbackDiffPreview(oldStr, newStr string, maxLines int) string {
	var b strings.Builder
	count := 0
	for _, line := range strings.Split(oldStr, "\n") {
		if count >= maxLines {
			break
		}
		b.WriteString("-" + line + "\n")
		count++
	}
	for _, line := range strings.Split(newStr, "\n") {
		if count >= maxLines {
			break
		}
		b.WriteString("+" + line + "\n")
		count++
	}
	return b.String()
}

func truncateOneLine(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "⏎")
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func previewNewFile(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", len(lines)-maxLines)
}

func toolDetail(name string, args map[string]any) string {
	switch name {
	case toolNameReadFile, toolNameWriteFile, toolNameEditFile, toolNameDeleteFile, toolNameListDir, toolNameFindFiles, toolNameCreateDir:
		if p, ok := args["path"].(string); ok {
			return p
		}
	case toolNameMoveFile:
		from := stringFromMap(args, "from")
		to := stringFromMap(args, "to")
		return from + " → " + to
	case toolNameGrep:
		pattern := stringFromMap(args, "pattern")
		path := stringFromMap(args, "path")
		return fmt.Sprintf("%q in %s", pattern, path)
	case toolNameGoBuild, toolNameGoTest, toolNameGoLint, toolNameGoVet:
		if p, ok := args["packages"].(string); ok && p != "" {
			return p
		}
		return "./..."
	case toolNameGoDoc:
		if p, ok := args["packages"].(string); ok && p != "" {
			return p
		}
		if q, ok := args["query"].(string); ok && q != "" {
			if len(q) > toolArgTruncateLen {
				q = q[:toolArgTruncateLen] + "..."
			}
			return q
		}
	case toolNameSearchDocs:
		if q, ok := args["query"].(string); ok && q != "" {
			if len(q) > toolArgTruncateLen {
				q = q[:toolArgTruncateLen] + "..."
			}
			return q
		}
	case toolNameBash:
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > toolArgTruncateLen {
				cmd = cmd[:toolArgTruncateLen] + "..."
			}
			return cmd
		}
	case toolNameSnippets:
		if action, ok := args["action"].(string); ok && action != "" {
			return action
		}
		return "match"
	case toolNameGitStatus:
		return ""
	case toolNameGitDiff, toolNameGitLog:
		if p, ok := args["path"].(string); ok && p != "" {
			return p
		}
		return ""
	case toolNameGitCommit:
		if msg, ok := args["message"].(string); ok && msg != "" {
			return truncateOneLine(msg, toolDetailTruncate)
		}
		return ""
	case toolNameWebFetch:
		if u, ok := args["url"].(string); ok {
			return u
		}
	case toolNameGetRule:
		if n, ok := args["name"].(string); ok && n != "" {
			return n
		}
	}
	// Generic fallback: try common arg keys before giving up.
	for _, key := range []string{"path", "file", "name", "query", "url", "input", "command", "pattern"} {
		if v, ok := args[key].(string); ok && v != "" {
			if len(v) > maxFingerprintLen {
				v = v[:maxFingerprintLen] + "..."
			}
			return v
		}
	}
	return ""
}

func langName(code string) string {
	switch code {
	case "ru":
		return "Russian"
	case "en":
		return langEnglish
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
		return langEnglish
	default:
		return code
	}
}

func (a *Agent) buildSystemPrompt() string {
	lang := langName(a.language)
	pl := a.currentProgLang()
	ltc := langToolsForLang(pl)

	var b strings.Builder
	if pl != "" {
		fmt.Fprintf(&b, "You are KodRun, a %s programming assistant.\n", pl)
	} else {
		b.WriteString("You are KodRun, a programming assistant.\n")
	}
	fmt.Fprintf(&b, "IMPORTANT: ALL your responses MUST be in %s. This is mandatory.\n\n", lang)

	if a.ruleCatalog != "" {
		b.WriteString(a.ruleCatalog)
		b.WriteString("\n")
	}

	switch a.mode {
	case ModePlan:
		b.WriteString("You are in PLAN mode (READ-ONLY).\n")
		b.WriteString("You can ONLY analyze code and create plans. You CANNOT modify files.\n")
		b.WriteString("You MUST NOT call any tools besides: " + strings.Join(a.reg.NamesFiltered(readOnlyTools), ", ") + "\n\n")
		b.WriteString("IMPORTANT — Questions vs Tasks:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- If the user asks a QUESTION (about Go, naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		case progLangPython:
			b.WriteString("- If the user asks a QUESTION (about Python naming, PEP conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		case progLangJSTS:
			b.WriteString("- If the user asks a QUESTION (about TypeScript naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		default:
			b.WriteString("- If the user asks a QUESTION (about naming, conventions, architecture, etc.) — answer it DIRECTLY and concisely. Do NOT create a plan for questions.\n")
		}
		b.WriteString("- If the user gives a TASK (fix, refactor, add feature, etc.) — create a numbered plan.\n\n")
		b.WriteString("STRICT RULES (for tasks):\n")
		b.WriteString("- NEVER generate code blocks, patches, diffs, or file contents\n")
		b.WriteString("- NEVER show code that should be written or changed\n")
		b.WriteString("- NEVER call write_file, edit_file, delete_file, bash, or any write tool\n")
		b.WriteString("- If asked to fix, edit, or write code — describe WHAT to change, not HOW in code\n")
		b.WriteString("- Your plan must be a numbered list with text descriptions only\n")
		b.WriteString("- Do NOT read binary files, build artifacts, or IDE config directories\n\n")
		b.WriteString("Guidelines:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Read and analyze *.go source files and project docs\n")
		case progLangPython:
			b.WriteString("- Read and analyze *.py source files and project docs\n")
		case progLangJSTS:
			b.WriteString("- Read and analyze *.ts, *.tsx, *.js, *.jsx source files and project docs\n")
		default:
			b.WriteString("- Read and analyze source files and project docs\n")
		}
		b.WriteString("- Identify files that need changes\n")
		b.WriteString("- Propose a step-by-step plan\n")
		b.WriteString("- Estimate complexity and risks\n")
		b.WriteString("- Be concise and actionable\n")
		b.WriteString("- Verification section MUST only include commands that match the actual project stack and task scope. Do NOT invent commands for tools, servers, linters or formatters not present in the project.\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Reference Effective Go, Go Code Review Comments, Go Common Mistakes and project conventions\n")
		case progLangPython:
			b.WriteString("- Reference PEP 8, PEP 20 and project conventions\n")
		case progLangJSTS:
			b.WriteString("- Reference TypeScript best practices and project conventions\n")
		default:
			// No language-specific best practices when language is unknown.
		}
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are NOT suggestions — they are REQUIREMENTS. Treat violations as bugs.\n")
			b.WriteString("These include naming conventions, error handling, code structure, and all documented standards.\n")
			b.WriteString("You MUST check code against ALL provided rules. Include violations in your plan.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE creating the plan. This is not optional.\n")
			switch pl {
			case progLangGo:
				b.WriteString("1. Call snippets(paths=[<list of all .go files you read>]) to get code conventions\n")
			case progLangPython:
				b.WriteString("1. Call snippets(paths=[<list of all .py files you read>]) to get code conventions\n")
			default:
				b.WriteString("1. Call snippets(paths=[<list of all source files you read>]) to get code conventions\n")
			}
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then create the plan, incorporating found conventions as requirements\n")
		}
	case ModeChat:
		b.WriteString("You are in CHAT mode.\n")
		b.WriteString("Answer questions, explain code, discuss architecture and design decisions.\n")
		b.WriteString("You can read files for context using read-only tools: " + strings.Join(a.reg.NamesFiltered(readOnlyTools), ", ") + "\n")
		b.WriteString("Do NOT create numbered plans, do NOT write or edit files, do NOT call write tools.\n")
		b.WriteString("Be concise and helpful.\n")
	default:
		b.WriteString("You are in EDIT mode. EDIT mode is for ACTING on the code, not for describing changes.\n\n")
		b.WriteString("CRITICAL — Action vs description:\n")
		b.WriteString("- If the user's input is a TASK to change code (fix, refactor, create, edit, move, restructure, apply plan, implement, ...) you MUST start your response with tool calls (write_file, edit_file, bash, read_file as needed). Do NOT output a markdown plan, do NOT write \"ANALYSIS\" / \"АНАЛИЗ\" or \"IMPLEMENTATION PLAN\" / \"ПЛАН ИСПРАВЛЕНИЙ\" sections, do NOT explain what you are about to do — just call the tools.\n")
		b.WriteString("- A textual response without tool calls is allowed ONLY when the user asked a pure question (no action verb). When in doubt, assume it is a task and call tools.\n")
		b.WriteString("- If you need to read a file before editing it, call read_file as the first tool — that still counts as \"starting with a tool\".\n")
		b.WriteString("- If the user pasted a numbered plan, your job is to EXECUTE it, not to rewrite it back. Skip directly to the first edit_file/write_file. Do not produce an \"Implementation plan\" / \"План исправлений\" of your own.\n")
		b.WriteString("- PLAN mode is the place for descriptions. EDIT mode is for actions. Stay in your lane.\n\n")
		b.WriteString("Available tools: " + strings.Join(a.reg.Names(), ", ") + "\n\n")
		b.WriteString("Guidelines:\n")
		switch pl {
		case progLangGo:
			b.WriteString("- Write idiomatic Go code following Effective Go, Go Code Review Comments, Go Common Mistakes. Use Go 1.25+.\n")
			b.WriteString("- Handle errors properly\n")
		case progLangPython:
			b.WriteString("- Write idiomatic Python code following PEP 8 and project conventions.\n")
			b.WriteString("- Handle errors properly\n")
		case progLangJSTS:
			b.WriteString("- Write idiomatic TypeScript/JavaScript code following project conventions.\n")
			b.WriteString("- Handle errors properly\n")
		default:
			// No language-specific guidelines when language is unknown.
		}
		b.WriteString("- Use edit_file for targeted changes, write_file for new files\n")
		b.WriteString("- Be concise in responses\n")
		b.WriteString("- Do NOT repeat or quote file contents in your responses. Reference files by path only.\n")
		if a.hasRAG {
			b.WriteString("\nIMPORTANT — Project rules and conventions (from RAG):\n")
			fmt.Fprintf(&b, "The task context includes MANDATORY RULES marked [MANDATORY PROJECT RULES] and %s marked [%s].\n", ltc.standardsLabel, ltc.standardsLabel)
			b.WriteString("These are REQUIREMENTS, not suggestions. Apply them to every line you write.\n")
			b.WriteString("These include naming conventions, error handling, code structure, and all documented standards.\n")
			b.WriteString("You may call search_docs for additional targeted searches if needed.\n")
		} else if a.hasSnippets {
			b.WriteString("\nIMPORTANT — Documentation check (MANDATORY):\n")
			b.WriteString("You MUST call snippets BEFORE writing or modifying any file. This is not optional.\n")
			b.WriteString("1. Call snippets(paths=[<file_paths>]) to get code conventions\n")
			b.WriteString("2. Read and understand the found conventions\n")
			b.WriteString("3. Only then write/edit code, following ALL found conventions (naming, structure, patterns, error handling)\n")
			b.WriteString("4. If no snippets match, proceed without conventions\n")
		}
		if ltc.buildTool != "" || ltc.lintTool != "" || ltc.testTool != "" {
			b.WriteString("\nAfter completing EVERY task you MUST run this verification sequence:\n")
			step := 1
			if ltc.buildTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to verify compilation. If errors — fix them and re-run.\n", step, ltc.buildTool)
				step++
			}
			if ltc.lintTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to check code quality. If errors — fix them and re-run.\n", step, ltc.lintTool)
				step++
			}
			if ltc.testTool != "" {
				fmt.Fprintf(&b, "%d. Run %s to verify correctness. If errors — fix them and re-run.\n", step, ltc.testTool)
				step++
			}
			fmt.Fprintf(&b, "%d. Update AGENTS.md if you changed architecture, added/removed files, or modified public APIs.\n", step)
			b.WriteString("   Use read_file to read AGENTS.md first, then edit_file to update only the relevant sections.\n")
			b.WriteString("   Do NOT rewrite the entire file — only update what changed.\n")
		}
	}

	// Repeat language directive at the end for reinforcement (important for local models).
	if lang != langEnglish {
		fmt.Fprintf(&b, "\nREMINDER: You MUST respond in %s. Never switch to any other language.\n", lang)
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
			trimmed == headerPLAN || trimmed == headerPLANRu ||
			trimmed == "CONTEXT" || trimmed == "КОНТЕКСТ" {
			lastPlanLine = i
			inPlan = strings.HasPrefix(trimmed, "##") && (strings.Contains(trimmed, "Plan") || strings.Contains(trimmed, "План")) ||
				trimmed == headerPLAN || trimmed == headerPLANRu
			continue
		}
		// Numbered list items: "1. ..." or "1) ..."
		if len(trimmed) > 2 && trimmed[0] >= '0' && trimmed[0] <= '9' &&
			(strings.Contains(trimmed[:min(planPrefixScanLen, len(trimmed))], ".") || strings.Contains(trimmed[:min(planPrefixScanLen, len(trimmed))], ")")) {
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

// stringFromMap safely extracts a string value from a map[string]any.
func stringFromMap(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
