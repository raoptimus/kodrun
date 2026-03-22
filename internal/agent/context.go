package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/raoptimus/go-agent/internal/ollama"
)

// ContextManager handles context window management.
type ContextManager struct {
	maxTokens int
	keepFirst int
	keepLast  int
	client    *ollama.Client
	model     string
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

	if len(messages) <= cm.keepFirst+cm.keepLast {
		return messages, nil
	}

	// Split into head, middle, tail
	head := messages[:cm.keepFirst]
	tail := messages[len(messages)-cm.keepLast:]
	middle := messages[cm.keepFirst : len(messages)-cm.keepLast]

	// Summarize middle
	summary, err := cm.summarize(ctx, middle)
	if err != nil {
		// Fallback: just drop middle
		result := make([]ollama.Message, 0, len(head)+1+len(tail))
		result = append(result, head...)
		result = append(result, ollama.Message{
			Role:    "user",
			Content: "[Earlier conversation was trimmed to save context]",
		})
		result = append(result, tail...)
		return result, nil
	}

	result := make([]ollama.Message, 0, len(head)+1+len(tail))
	result = append(result, head...)
	result = append(result, ollama.Message{
		Role:    "user",
		Content: fmt.Sprintf("[Summary of earlier conversation]\n%s", summary),
	})
	result = append(result, tail...)

	return result, nil
}

func (cm *ContextManager) summarize(ctx context.Context, messages []ollama.Message) (string, error) {
	var content strings.Builder
	for _, m := range messages {
		fmt.Fprintf(&content, "[%s]: %s\n", m.Role, truncate(m.Content, 500))
	}

	resp, err := cm.client.ChatSync(ctx, ollama.ChatRequest{
		Model: cm.model,
		Messages: []ollama.Message{
			{Role: "system", Content: "Summarize the following conversation concisely, preserving key decisions, file changes, and errors encountered."},
			{Role: "user", Content: content.String()},
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
		// Rough estimate: 1 token ≈ 4 chars
		total += len(m.Content) / 4
	}
	return total
}
