package llm

import (
	"bytes"
	"context"

	"github.com/pkg/errors"
)

// AggregateChatStream reads all chunks from the channel and returns
// an aggregated ChatChunk. It applies fallback tool-call parsing and
// error-JSON detection on the final result.
func AggregateChatStream(ctx context.Context, ch <-chan ChatChunk, cb ChunkCallback) (ChatChunk, error) {
	var result ChatChunk
	var contentBuf bytes.Buffer
	var evalTokens int

	for chunk := range ch {
		if chunk.Error != nil {
			// If we already accumulated content, return it instead of
			// failing completely. This handles truncated streams where
			// most of the response was received successfully.
			if contentBuf.Len() > 0 {
				break
			}
			return ChatChunk{}, chunk.Error
		}
		if chunk.Content != "" {
			contentBuf.WriteString(chunk.Content)
			evalTokens++
		}
		if len(chunk.ToolCalls) > 0 {
			result.ToolCalls = append(result.ToolCalls, chunk.ToolCalls...)
		}
		result.Done = chunk.Done
		if chunk.PromptEvalCount > 0 {
			result.PromptEvalCount = chunk.PromptEvalCount
		}
		if chunk.EvalCount > 0 {
			result.EvalCount = chunk.EvalCount
		}
		if chunk.EvalDuration > 0 {
			result.EvalDuration = chunk.EvalDuration
		}
		if chunk.PromptEvalDuration > 0 {
			result.PromptEvalDuration = chunk.PromptEvalDuration
		}
		if chunk.TotalDuration > 0 {
			result.TotalDuration = chunk.TotalDuration
		}
		if chunk.LoadDuration > 0 {
			result.LoadDuration = chunk.LoadDuration
		}
		if cb != nil {
			cb(evalTokens, chunk.Content)
		}
	}

	result.Content = contentBuf.String()

	if msg := DetectErrorJSON(result.Content); msg != "" {
		return ChatChunk{}, errors.Errorf("llm error: %s", msg)
	}

	if len(result.ToolCalls) == 0 && result.Content != "" {
		if parsed, ok := ParseToolCalls(result.Content); ok {
			result.ToolCalls = parsed
			result.Content = CleanToolCallText(result.Content)
		}
	}

	return result, nil
}
