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
)

// ContextManager handles context window management.
type ContextManager struct {
	maxTokens int
	keepFirst int
	keepLast  int
	client    llm.Client
	model     string
	language  string
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

// Trim reduces message history if it exceeds the token budget.
func (cm *ContextManager) Trim(ctx context.Context, messages []llm.Message) ([]llm.Message, error) {
	estimated := cm.estimateTokens(messages)
	if estimated <= cm.maxTokens {
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

	// Split into head, middle, tail
	head := messages[:cm.keepFirst]
	tail := messages[len(messages)-cm.keepLast:]
	middle := messages[cm.keepFirst : len(messages)-cm.keepLast]

	// Summarize middle
	summary, err := cm.summarize(ctx, middle, instructions)

	msg := fmt.Sprintf("[Earlier conversation was trimmed to save context.]\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", langName(cm.language))
	if err == nil {
		msg = fmt.Sprintf("[Summary of earlier conversation.]\n%s\n\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", summary, langName(cm.language))
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
	for _, m := range messages {
		fmt.Fprintf(&content, "[%s]: %s\n", m.Role, truncate(m.Content, summarizeTruncateLen))
	}

	sysPrompt := "Summarize the following conversation concisely, preserving key decisions, file changes, and errors encountered."
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

	return resp.Content, nil
}

func (cm *ContextManager) estimateTokens(messages []llm.Message) int {
	total := 0
	for _, m := range messages {
		total += estimateStringTokens(m.Content)
		total += 4 // message framing overhead (role, separators)
	}
	return total
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
