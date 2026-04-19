/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package openai

// chatRequest is the OpenAI wire-format request.
type chatRequest struct {
	Model          string      `json:"model"`
	Messages       []chatMsg   `json:"messages"`
	Tools          []toolDef   `json:"tools,omitempty"`
	Temperature    *float64    `json:"temperature,omitempty"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	Stream         bool        `json:"stream"`
	ResponseFormat *respFormat `json:"response_format,omitempty"`
	StreamOptions  *streamOpts `json:"stream_options,omitempty"`
}

type streamOpts struct {
	IncludeUsage bool `json:"include_usage"`
}

type respFormat struct {
	Type string `json:"type"`
}

type chatMsg struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type toolCall struct {
	Index_   int          `json:"index"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type"`
	Function toolCallFunc `json:"function"`
}

type toolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type toolDef struct {
	Type     string      `json:"type"`
	Function toolFuncDef `json:"function"`
}

type toolFuncDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  jsonSchema `json:"parameters"`
}

type jsonSchema struct {
	Type        string                `json:"type"`
	Properties  map[string]jsonSchema `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Description string                `json:"description,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Items       *jsonSchema           `json:"items,omitempty"`
}

// chatStreamChunk is the OpenAI SSE streaming response.
type chatStreamChunk struct {
	ID      string         `json:"id"`
	Choices []streamChoice `json:"choices"`
	Usage   *usageInfo     `json:"usage,omitempty"`
}

type streamChoice struct {
	Index        int      `json:"index"`
	Delta        deltaMsg `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type deltaMsg struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// modelsResponse is the OpenAI wire-format models list response.
type modelsResponse struct {
	Data []modelData `json:"data"`
}

type modelData struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
}

// embedRequest is the OpenAI wire-format embeddings request.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embedResponse is the OpenAI wire-format embeddings response.
type embedResponse struct {
	Model string          `json:"model"`
	Data  []embeddingData `json:"data"`
}

type embeddingData struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// toolCallAccumulator accumulates streamed tool call deltas.
type toolCallAccumulator struct {
	id        string
	name      string
	argsBytes []byte
}
