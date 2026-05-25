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

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/tools"
)

// toolExecutor runs tool calls on behalf of *Agent. It currently keeps a
// back-reference to its owning agent so methods can read mutable state
// (mode, disabled-tool sets, pool, etc.); later stages narrow this surface.
type toolExecutor struct {
	reg     *tools.Registry
	permMgr *PermissionManager
	workDir string
	agent   *Agent
}

// newToolExecutor builds a toolExecutor wired to the supplied registry and
// permission manager. The agent back-ref is attached separately via attach
// once the *Agent has been fully constructed.
func newToolExecutor(reg *tools.Registry, permMgr *PermissionManager, workDir string) *toolExecutor {
	return &toolExecutor{
		reg:     reg,
		permMgr: permMgr,
		workDir: workDir,
	}
}

// attach connects the executor to its owning agent. Must be called once after
// New() so methods can read live agent state.
func (te *toolExecutor) attach(a *Agent) {
	te.agent = a
}

// readOnlyTools returns the executor's view of read-only tool names. Mirrors
// (a *Agent).readOnlyTools so future migrations can drop the back-reference.
func (te *toolExecutor) readOnlyTools() map[string]bool {
	return readOnlyToolsFor(te.reg)
}

// toolDefsForMode returns the tool definitions to advertise for the agent's
// current mode, honouring toolsDisabled and the per-call disabled set.
func (te *toolExecutor) toolDefsForMode() []llm.ToolDef {
	a := te.agent
	if a.toolsDisabled {
		return nil
	}
	var defs []llm.ToolDef
	if a.mode == ModeEdit {
		defs = te.reg.ToolDefs()
	} else {
		allowed := te.readOnlyTools()
		if len(a.extraReadOnlyTools) > 0 {
			merged := make(map[string]bool, len(allowed)+len(a.extraReadOnlyTools))
			maps.Copy(merged, allowed)
			maps.Copy(merged, a.extraReadOnlyTools)
			defs = te.reg.ToolDefsFiltered(merged)
		} else {
			defs = te.reg.ToolDefsFiltered(allowed)
		}
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

// canRunParallel reports whether toolName may run inside the worker pool.
// Only registered read-only tools that are not blocked qualify.
func (te *toolExecutor) canRunParallel(toolName string) bool {
	a := te.agent
	if a.pool == nil || cap(a.pool.sem) <= 1 {
		return false
	}
	return (te.readOnlyTools()[toolName] || a.extraReadOnlyTools[toolName]) && !te.isToolBlocked(toolName)
}

// isToolBlocked reports whether the named tool is forbidden in the current
// agent mode (PLAN/CHAT block writes) or has been explicitly disabled.
func (te *toolExecutor) isToolBlocked(tool string) bool {
	a := te.agent
	if a.toolsDisabled {
		return true
	}
	if a.disabledTools[tool] {
		return true
	}
	if a.mode == ModeEdit {
		return false
	}
	return !te.readOnlyTools()[tool] && !a.extraReadOnlyTools[tool]
}

// needsConfirm reports whether the named tool requires user confirmation
// before running. Built-in destructive tools always need confirm; the agent
// can opt additional tools in via extraConfirmTools.
func (te *toolExecutor) needsConfirm(tool string) bool {
	switch tool {
	case toolNameWriteFile, toolNameEditFile, toolNameDeleteFile, toolNameMoveFile, toolNameBash, toolNameGitCommit, toolNameWebFetch:
		return true
	}
	return te.agent.extraConfirmTools[tool]
}

// buildConfirmPayload assembles the ConfirmPayload presented to the user for
// destructive tool calls. For edit/write tools it tries to read the current
// file content and produce a unified diff preview so the user can see the
// change before approving.
func (te *toolExecutor) buildConfirmPayload(ctx context.Context, name string, args map[string]any) ConfirmPayload {
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
		if resolved, err := tools.SafePath(ctx, te.workDir, path); err == nil {
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
		if resolved, err := tools.SafePath(ctx, te.workDir, path); err == nil {
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

// pathAllowed reports whether path is permitted by the agent's
// allowedReadPaths whitelist. An empty whitelist means unrestricted; a nil
// path or one outside the whitelist (by exact match or basename) is rejected.
func (te *toolExecutor) pathAllowed(path string) bool {
	a := te.agent
	if a.allowedReadPaths == nil {
		return true
	}
	if path == "" {
		return false
	}
	if _, ok := a.allowedReadPaths[path]; ok {
		return true
	}
	base := filepath.Base(path)
	if _, ok := a.allowedReadPaths[base]; ok {
		return true
	}
	return false
}

// guardReadWhitelist returns a synthetic refusal result if the tool call is a
// read-only file tool targeting a path outside the current allowed-read
// whitelist. Returns nil when the call is permitted.
func (te *toolExecutor) guardReadWhitelist(tc llm.ToolCall) *tools.ToolResult {
	if te.agent.allowedReadPaths == nil {
		return nil
	}
	switch tc.Function.Name {
	case toolNameReadFile, toolNameListDir, toolNameFindFiles, toolNameGrep:
	default:
		return nil
	}
	var p string
	if v, ok := tc.Function.Arguments["path"].(string); ok {
		p = v
	} else if v, ok := tc.Function.Arguments["root"].(string); ok {
		p = v
	}
	if p == "" {
		return nil
	}
	if te.pathAllowed(p) {
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

// guardDuplicateRead returns a synthetic short result if read_file is called
// on a path the model has already read in the current Send() invocation.
// Returns nil otherwise. The synthetic result points the model back to the
// prior tool result in history, saving tokens spent re-emitting full file
// contents and discouraging the read-loop failure mode.
func (te *toolExecutor) guardDuplicateRead(tc llm.ToolCall) *tools.ToolResult {
	if tc.Function.Name != toolNameReadFile {
		return nil
	}
	p, ok := tc.Function.Arguments["path"].(string)
	if !ok || p == "" {
		return nil
	}
	if _, seen := te.agent.readPathsSession[p]; !seen {
		return nil
	}
	msg := fmt.Sprintf(
		"skipped: %q was already read earlier in this turn. "+
			"Reuse the prior tool result from the conversation above. "+
			"If the file was modified since, call edit_file/write_file first.",
		p,
	)
	return &tools.ToolResult{Output: msg, Meta: map[string]any{"dedup_hit": true}}
}

// recordReadPath remembers a successful read_file path for dedup tracking.
func (te *toolExecutor) recordReadPath(tc llm.ToolCall) {
	if tc.Function.Name != toolNameReadFile {
		return
	}
	if p, ok := tc.Function.Arguments["path"].(string); ok && p != "" {
		te.agent.readPathsSession[p] = struct{}{}
	}
}

// clearReadPathOnWrite drops the dedup record for a path touched by a write
// tool, so a re-read after modification is allowed to fetch fresh content.
func (te *toolExecutor) clearReadPathOnWrite(tc llm.ToolCall) {
	if te.readOnlyTools()[tc.Function.Name] {
		return
	}
	if p, ok := tc.Function.Arguments["path"].(string); ok && p != "" {
		delete(te.agent.readPathsSession, p)
	}
}

// runResult is returned by toolExecutor.Run to drive Send()'s outer chat loop.
//
//	AllBlocked   — every sequential call was refused due to mode (PLAN/CHAT).
//	HadParallel  — at least one read-only call ran via the worker pool.
//	Augmented    — user rejected a call with a follow-up constraint; Send()
//	               must re-issue the prompt instead of advancing.
type runResult struct {
	AllBlocked  bool
	HadParallel bool
	Augmented   bool
}

// Run executes one tool-call iteration: splits parallel/sequential, runs both
// groups, applies mode/confirm gating, and reports a runResult so Send() can
// advance the chat loop accordingly.
func (te *toolExecutor) Run(ctx context.Context, calls []llm.ToolCall) runResult {
	a := te.agent
	var parallelCalls, sequentialCalls []llm.ToolCall
	for _, tc := range calls {
		if te.canRunParallel(tc.Function.Name) {
			parallelCalls = append(parallelCalls, tc)
		} else {
			sequentialCalls = append(sequentialCalls, tc)
		}
	}

	res := runResult{
		AllBlocked:  len(sequentialCalls) > 0,
		HadParallel: len(parallelCalls) > 0,
	}

	if res.HadParallel {
		a.toolCallCount += len(parallelCalls)
		te.executeParallel(ctx, parallelCalls)
	}

	for _, tc := range sequentialCalls {
		if te.isToolBlocked(tc.Function.Name) {
			a.history = append(a.history, llm.Message{
				Role: "tool",
				Content: fmt.Sprintf(
					"Tool %q is NOT available in %s mode. Do NOT call write tools. Use ONLY: %s",
					tc.Function.Name, a.mode.String(),
					strings.Join(a.reg.NamesFiltered(a.readOnlyTools()), ", "),
				),
				ToolCallID: tc.ID,
			})
			a.emit(&Event{
				Type:    EventTool,
				Tool:    tc.Function.Name,
				Message: fmt.Sprintf("blocked in %s mode", a.mode.String()),
				Success: false,
			})
			continue
		}
		res.AllBlocked = false

		detail := toolDetail(tc.Function.Name, tc.Function.Arguments)
		a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: detail})

		if te.needsConfirm(tc.Function.Name) && a.confirmFn != nil {
			fp := Fingerprint(tc.Function.Name, tc.Function.Arguments)
			if !te.permMgr.IsAllowed(fp) {
				payload := te.buildConfirmPayload(ctx, tc.Function.Name, tc.Function.Arguments)
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
				case ConfirmAllowSession:
					te.permMgr.AllowSession(fp)
				case ConfirmAugment:
					a.history = append(a.history, llm.Message{
						Role:       "tool",
						Content:    fmt.Sprintf("User rejected this call and provided a constraint: %s\nPlease rebuild this %s call accordingly.", cr.Augment, tc.Function.Name),
						ToolCallID: tc.ID,
					})
					a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "augmented: " + cr.Augment, Success: false})
					res.Augmented = true
				}
				if res.Augmented {
					return res
				}
			}
		}

		a.toolCallCount++
		te.executeSingle(ctx, tc)
	}

	return res
}

// executeParallel runs read-only tool calls concurrently via the worker pool
// and appends results to history in the original order.
func (te *toolExecutor) executeParallel(ctx context.Context, calls []llm.ToolCall) {
	a := te.agent
	tasks := make([]TaskFunc, len(calls))
	guards := make([]*tools.ToolResult, len(calls))
	roTools := te.readOnlyTools()
	for i, tc := range calls {
		guards[i] = te.guardReadWhitelist(tc)
		if guards[i] == nil {
			guards[i] = te.guardDuplicateRead(tc)
		}
		name := tc.Function.Name
		args := tc.Function.Arguments
		idx := i
		if name == toolNameReadFile {
			a.hasCalledReadFile = true
		}
		if !roTools[name] {
			a.hasCalledWriteTool = true
		}
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			if guards[idx] != nil {
				return guards[idx], nil
			}
			return te.reg.Execute(ctx, name, args)
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
		if guards[i] == nil {
			te.recordReadPath(tc)
		}
		te.emitToolResult(tc, tr.Result)
	}
}

// executeSingle runs a single tool call and records the result.
func (te *toolExecutor) executeSingle(ctx context.Context, tc llm.ToolCall) {
	a := te.agent
	if guard := te.guardReadWhitelist(tc); guard != nil {
		a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "blocked by whitelist", Success: false})
		te.emitToolResult(tc, guard)
		return
	}
	if guard := te.guardDuplicateRead(tc); guard != nil {
		a.emit(&Event{Type: EventTool, Tool: tc.Function.Name, Message: "skipped: duplicate read", Success: true})
		te.emitToolResult(tc, guard)
		return
	}
	if tc.Function.Name == toolNameReadFile {
		a.hasCalledReadFile = true
	}
	if !te.readOnlyTools()[tc.Function.Name] {
		a.hasCalledWriteTool = true
	}
	result, err := te.reg.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
	if err != nil {
		a.emit(&Event{Type: EventError, Tool: tc.Function.Name, Message: err.Error()})
		a.history = append(a.history, llm.Message{
			Role:       "tool",
			Content:    "Error: " + err.Error(),
			ToolCallID: tc.ID,
		})
		return
	}
	te.recordReadPath(tc)
	te.clearReadPathOnWrite(tc)
	te.emitToolResult(tc, result)
}

// emitToolResult emits an event and appends the tool result to history.
func (te *toolExecutor) emitToolResult(tc llm.ToolCall, result *tools.ToolResult) {
	a := te.agent
	const success = true
	msg := truncate(result.Output, truncatePreviewLen)
	if te.readOnlyTools()[tc.Function.Name] {
		detail := toolDetail(tc.Function.Name, tc.Function.Arguments)
		if detail != "" {
			msg = detail
		}
	}
	if msg == "" && result.Meta != nil {
		if fl, ok := result.Meta["file_list"].([]string); ok && len(fl) > 0 {
			msg = strings.Join(fl, ", ")
			if len(msg) > fileListMaxLen {
				msg = msg[:fileListMaxLen] + "..."
			}
		}
	}
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
	isCachedReadOnly := ev.CacheHit && te.readOnlyTools()[tc.Function.Name]

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
