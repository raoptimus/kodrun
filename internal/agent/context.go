package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// ContextManager handles context window management.
type ContextManager struct {
	maxTokens int
	keepFirst int
	keepLast  int
	client    *ollama.Client
	model     string
	language  string
}

// SetLanguage sets the language for summarization prompts.
func (cm *ContextManager) SetLanguage(lang string) {
	cm.language = lang
}

// NewContextManager creates a new context manager.
func NewContextManager(maxTokens int, client *ollama.Client, model string) *ContextManager {
	return &ContextManager{
		maxTokens: maxTokens,
		keepFirst: 2, // system + first user message
		keepLast:  6,
		client:    client,
		model:     model,
	}
}

// Trim reduces message history if it exceeds the token budget.
func (cm *ContextManager) Trim(ctx context.Context, messages []ollama.Message) ([]ollama.Message, error) {
	estimated := cm.estimateTokens(messages)
	if estimated <= cm.maxTokens {
		return messages, nil
	}
	return cm.doTrim(ctx, messages, "")
}

// ForceTrim summarizes conversation history regardless of token count.
// Instructions are optional hints for the summarizer (e.g. "focus on file changes").
func (cm *ContextManager) ForceTrim(ctx context.Context, messages []ollama.Message, instructions string) ([]ollama.Message, error) {
	return cm.doTrim(ctx, messages, instructions)
}

func (cm *ContextManager) doTrim(ctx context.Context, messages []ollama.Message, instructions string) ([]ollama.Message, error) {
	if len(messages) <= cm.keepFirst+cm.keepLast {
		return messages, nil
	}

	// Split into head, middle, tail
	head := messages[:cm.keepFirst]
	tail := messages[len(messages)-cm.keepLast:]
	middle := messages[cm.keepFirst : len(messages)-cm.keepLast]

	// Summarize middle
	summary, err := cm.summarize(ctx, middle, instructions)
	if err != nil {
		// Fallback: just drop middle
		result := make([]ollama.Message, 0, len(head)+1+len(tail))
		result = append(result, head...)
		result = append(result, ollama.Message{
			Role:    "user",
			Content: fmt.Sprintf("[Earlier conversation was trimmed to save context.]\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", langName(cm.language)),
		})
		result = append(result, tail...)
		return result, nil
	}

	result := make([]ollama.Message, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, ollama.Message{
		Role:    "user",
		Content: fmt.Sprintf("[Summary of earlier conversation.]\n%s\n\nIMPORTANT REMINDER: ALL your responses MUST be in %s. This is mandatory.", summary, langName(cm.language)),
	})
	result = append(result, tail...)

	return result, nil
}

func (cm *ContextManager) summarize(ctx context.Context, messages []ollama.Message, instructions string) (string, error) {
	var content strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&content, "[%s]: %s\n", m.Role, truncate(m.Content, 500))
	}

	sysPrompt := "Summarize the following conversation concisely, preserving key decisions, file changes, and errors encountered."
	if instructions != "" {
		sysPrompt += " Also focus on: " + instructions
	}
	if cm.language != "" {
		sysPrompt += fmt.Sprintf(" Always respond in %s.", langName(cm.language))
	}

	resp, err := cm.client.ChatSync(ctx, ollama.ChatRequest{
		Model: cm.model,
		Messages: []ollama.Message{
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

func (cm *ContextManager) estimateTokens(messages []ollama.Message) int {
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
		if r > 127 {
			nonASCII++
		}
	}
	if totalRunes > 0 && nonASCII*100/totalRunes > 30 {
		// Non-ASCII heavy text (Cyrillic, CJK): ~2 chars per token
		return totalRunes / 2
	}
	// ASCII-heavy text: ~4 chars per token
	return len(s) / 4
}
