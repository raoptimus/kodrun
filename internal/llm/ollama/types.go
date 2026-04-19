/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package ollama

import "github.com/raoptimus/kodrun/internal/llm"

// chatRequest is the Ollama wire-format request.
type chatRequest struct {
	Model    string         `json:"model"`
	Messages []llm.Message  `json:"messages"`
	Tools    []llm.ToolDef  `json:"tools,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
	Format   string         `json:"format,omitempty"`
	Stream   bool           `json:"stream"`
}

// chatResponse is the Ollama wire-format streaming response.
type chatResponse struct {
	Model              string      `json:"model"`
	Message            llm.Message `json:"message"`
	Done               bool        `json:"done"`
	DoneReason         string      `json:"done_reason,omitempty"`
	PromptEvalCount    int         `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64       `json:"prompt_eval_duration,omitempty"`
	EvalCount          int         `json:"eval_count,omitempty"`
	EvalDuration       int64       `json:"eval_duration,omitempty"`
	TotalDuration      int64       `json:"total_duration,omitempty"`
	LoadDuration       int64       `json:"load_duration,omitempty"`
}

// embedRequest is the Ollama wire-format embed request.
type embedRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate,omitempty"`
}

// embedResponse is the Ollama wire-format embed response.
type embedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// modelsResponse is the Ollama wire-format models list response.
type modelsResponse struct {
	Models []llm.Model `json:"models"`
}
