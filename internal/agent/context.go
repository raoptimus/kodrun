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
	"strings"

	"github.com/raoptimus/kodrun/internal/llm"
)

const (
	summarizeTruncateLen  = 500 // max chars per message when building summarization input
	keepFirstMessages     = 2   // system + first user message to preserve on trim
	keepLastMessages      = 6   // recent messages to preserve on trim
	maxASCIICodepoint     = 127 // highest ASCII code point
	nonASCIIThresholdPct  = 30  // percentage of non-ASCII runes triggering non-ASCII ratio
	charsPerTokenNonASCII = 2   // approximate chars per token for CJK/Cyrillic text
	charsPerTokenASCII    = 4   // approximate chars per token for ASCII text
	calibrationWeight     = 0.3 // EMA weight for new calibration samples (0..1)

	responseReserveRatio   = 0.15 // fraction of maxTokens reserved for model response
	responseReserveMin     = 1024 // minimum tokens reserved for model response
	smartTruncateHead      = 300  // runes to keep from the beginning
	smartTruncateTail      = 200  // runes to keep from the end
	smartTruncateSep       = "\n... [truncated] ...\n"
	smartTruncateHeadRatio = 3 // numerator for proportional head split (3/5 = 60%)
	smartTruncateRatioDen  = 5 // denominator for proportional split

	summaryMarker = "[Summary of earlier conversation.]"
)

// ContextManager handles context window management.
type ContextManager struct {
	maxTokens        int
	keepFirst        int
	keepLast         int
	client           llm.Client
	model            string
	language         string
	runningSummary   string
	calibrationRatio float64 // EMA correction ratio: actual/estimated tokens
}

// SetLanguage sets the language for summarization prompts.
func (cm *ContextManager) SetLanguage(lang string) {
	cm.language = lang
}

// NewContextManager creates a new context manager.
func NewContextManager(maxTokens int, client llm.Client, model string) *ContextManager {
	return &ContextManager{
		maxTokens: maxTokens,
		keepFirst: keepFirstMessages,
		keepLast:  keepLastMessages,
		client:    client,
		model:     model,
	}
}

// effectiveBudget returns the token budget for message history,
// reserving space for the model's response.
func (cm *ContextManager) effectiveBudget() int {
	reserve := max(int(float64(cm.maxTokens)*responseReserveRatio), responseReserveMin)
	budget := cm.maxTokens - reserve
	if budget < 0 {
		return 0
	}
	return budget
}

// Trim reduces message history if it exceeds the token budget.
func (cm *ContextManager) Trim(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	estimated := cm.estimateTokens(messages)
	if estimated <= cm.effectiveBudget() {
		return messages, nil
	}
	return cm.doTrim(ctx, messages, "")
}

// ForceTrim summarizes conversation history regardless of token count.
// Instructions are optional hints for the summarizer (e.g. "focus on file changes").
func (cm *ContextManager) ForceTrim(ctx context.Context, messages []llm.Message, instructions string) ([]llm.Message, error) {
	return cm.doTrim(ctx, messages, instructions)
}

func (cm *ContextManager) doTrim(ctx context.Context, messages []llm.Message, instructions string) ([]llm.Message, error) {
	if len(messages) <= cm.keepFirst+cm.keepLast {
		return messages, nil
	}

	kf, kl := adjustBoundaries(messages, cm.keepFirst, cm.keepLast)
	if kf+kl >= len(messages) {
		return messages, nil
	}

	head := messages[:kf]
	tail := messages[len(messages)-kl:]
	middle := messages[kf : len(messages)-kl]

	// Extract previous summary from middle for incremental summarization.
	var previousSummary string
	var nonSummaryMiddle []llm.Message
	for _, m := range middle {
		if m.Role == "user" && strings.HasPrefix(m.Content, summaryMarker) {
			previousSummary = m.Content
		} else {
			nonSummaryMiddle = append(nonSummaryMiddle, m)
		}
	}
	if previousSummary != "" && cm.runningSummary == "" {
		cm.runningSummary = previousSummary
	}

	summary, err := cm.summarize(ctx, nonSummaryMiddle, instructions)

	msg := fmt.Sprintf("[Earlier conversation was trimmed to save context.]\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", langName(cm.language))
	if err == nil {
		msg = fmt.Sprintf("%s\n%s\n\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", summaryMarker, summary, langName(cm.language))
	}

	return cm.buildTrimmed(head, tail, msg), nil
}

func (cm *ContextManager) buildTrimmed(head, tail []llm.Message, summaryMsg string) []llm.Message {
	result := make([]llm.Message, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, llm.Message{Role: "user", Content: summaryMsg})
	result = append(result, tail...)
	return result
}

func (cm *ContextManager) summarize(ctx context.Context, messages []llm.Message, instructions string) (string, error) {
	var content strings.Builder

	if cm.runningSummary != "" {
		fmt.Fprintf(&content, "[Previous summary — incorporate and update, do not discard]:\n%s\n\n[New messages to summarize]:\n", cm.runningSummary)
	}

	for _, m := range messages {
		fmt.Fprintf(&content, "[%s]: %s\n", m.Role, smartTruncate(m.Content, summarizeTruncateLen))
	}

	sysPrompt := "Summarize the following conversation concisely, preserving key decisions, file changes, and errors encountered."
	if cm.runningSummary != "" {
		sysPrompt += " A previous summary is provided — merge it with new information, preserving all key facts."
	}
	if instructions != "" {
		sysPrompt += " Also focus on: " + instructions
	}
	if cm.language != "" {
		sysPrompt += fmt.Sprintf(" Always respond in %s.", langName(cm.language))
	}

	resp, err := cm.client.ChatSync(ctx, &llm.ChatRequest{
		Model: cm.model,
		Messages: []llm.Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: content.String()},
		},
		Options: map[string]any{
			"num_ctx": cm.maxTokens,
		},
	})
	if err != nil {
		return "", err
	}

	cm.runningSummary = resp.Content

	return resp.Content, nil
}

// Calibrate updates the internal correction ratio using a real token count
// from the API response. Call this after each API response with the actual
// prompt token count (PromptEvalCount) and the messages that were sent.
func (cm *ContextManager) Calibrate(messages []llm.Message, actualTokens int) {
	if actualTokens <= 0 {
		return
	}
	estimated := cm.estimateTokensRaw(messages)
	if estimated <= 0 {
		return
	}
	sample := float64(actualTokens) / float64(estimated)
	if cm.calibrationRatio == 0 {
		cm.calibrationRatio = sample
	} else {
		cm.calibrationRatio = cm.calibrationRatio*(1-calibrationWeight) + sample*calibrationWeight
	}
}

func (cm *ContextManager) estimateTokens(messages []llm.Message) int {
	raw := cm.estimateTokensRaw(messages)
	if cm.calibrationRatio > 0 {
		return int(float64(raw) * cm.calibrationRatio)
	}
	return raw
}

func (cm *ContextManager) estimateTokensRaw(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateStringTokens(m.Content)
		total += 4 // message framing overhead (role, separators)
	}
	return total
}

// toolCallIndex maps tool call IDs to the index of the assistant message that produced them.
func toolCallIndex(messages []llm.Message) map[string]int {
	idx := make(map[string]int)
	for i, m := range messages {
		if m.Role == roleAssistant && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					idx[tc.ID] = i
				}
			}
		}
	}
	return idx
}

// adjustBoundaries ensures tool_call/tool_result pairs are not split across
// head/middle/tail boundaries. Returns adjusted (keepFirst, keepLast).
func adjustBoundaries(messages []llm.Message, keepFirst, keepLast int) (adjustedFirst, adjustedLast int) {
	n := len(messages)
	if keepFirst+keepLast >= n {
		return keepFirst, keepLast
	}

	tcIdx := toolCallIndex(messages)

	// Expand head: if messages at the head border are assistant with tool calls,
	// include all corresponding tool results.
	for keepFirst < n-keepLast {
		last := messages[keepFirst-1]
		if last.Role != roleAssistant || len(last.ToolCalls) == 0 {
			break
		}
		maxResultIdx := keepFirst - 1
		for _, tc := range last.ToolCalls {
			for j := keepFirst; j < n-keepLast; j++ {
				if messages[j].ToolCallID == tc.ID {
					if j > maxResultIdx {
						maxResultIdx = j
					}
					break
				}
			}
		}
		if maxResultIdx < keepFirst {
			break
		}
		keepFirst = maxResultIdx + 1
		if keepFirst+keepLast >= n {
			return keepFirst, keepLast
		}
	}

	// Expand tail: if messages at the tail border are tool results,
	// include the assistant message that produced them.
	for keepLast < n-keepFirst {
		tailStart := n - keepLast
		first := messages[tailStart]
		if first.ToolCallID == "" {
			break
		}
		assistantIdx, ok := tcIdx[first.ToolCallID]
		if !ok || assistantIdx >= tailStart {
			break
		}
		keepLast = n - assistantIdx
		if keepFirst+keepLast >= n {
			return keepFirst, keepLast
		}
	}

	return keepFirst, keepLast
}

// smartTruncate preserves both the beginning and end of long strings.
// This is important for code and error messages where the end often
// contains critical information (error names, stack trace endings).
func smartTruncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	headLen := smartTruncateHead
	tailLen := smartTruncateTail
	if headLen+tailLen > maxLen {
		headLen = maxLen * smartTruncateHeadRatio / smartTruncateRatioDen
		tailLen = maxLen - headLen
	}
	sepRunes := []rune(smartTruncateSep)
	result := make([]rune, 0, headLen+len(sepRunes)+tailLen)
	result = append(result, runes[:headLen]...)
	result = append(result, sepRunes...)
	result = append(result, runes[len(runes)-tailLen:]...)
	return string(result)
}

// estimateStringTokens estimates token count for a string.
// Uses different ratios for ASCII vs non-ASCII (CJK, Cyrillic) text.
func estimateStringTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	var nonASCII, totalRunes int
	for _, r := range s {
		totalRunes++
		if r > maxASCIICodepoint {
			nonASCII++
		}
	}
	if totalRunes > 0 && nonASCII*pctMultiplier/totalRunes > nonASCIIThresholdPct {
		// Non-ASCII heavy text (Cyrillic, CJK): ~2 chars per token
		return totalRunes / charsPerTokenNonASCII
	}
	// ASCII-heavy text: ~4 chars per token
	return len(s) / charsPerTokenASCII
}
