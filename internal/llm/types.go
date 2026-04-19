package llm

// Message represents a chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation from the model.
type ToolCall struct {
	ID       string       `json:"id,omitempty"`
	Function ToolCallFunc `json:"function"`
}

// ToolCallFunc holds the function name and arguments.
type ToolCallFunc struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ChatRequest is the request body for the chat API.
type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Tools    []ToolDef      `json:"tools,omitempty"`
	Options  map[string]any `json:"options,omitempty"`
	Format   string         `json:"format,omitempty"`
	Stream   bool           `json:"stream"`
}

// ToolDef describes a tool for the LLM API.
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef describes a tool function.
type ToolFuncDef struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  JSONSchema `json:"parameters"`
}

// JSONSchema is a simplified JSON Schema representation.
type JSONSchema struct {
	Type        string                `json:"type"`
	Properties  map[string]JSONSchema `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Description string                `json:"description,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Items       *JSONSchema           `json:"items,omitempty"`
}

// ChatChunk represents a streaming chunk or final aggregated response.
type ChatChunk struct {
	Content            string
	ToolCalls          []ToolCall
	Done               bool
	Error              error
	PromptEvalCount    int
	PromptEvalDuration int64
	EvalCount          int
	EvalDuration       int64
	TotalDuration      int64
	LoadDuration       int64
}

// EmbedRequest is the request body for the embed API.
type EmbedRequest struct {
	Model    string   `json:"model"`
	Input    []string `json:"input"`
	Truncate bool     `json:"truncate,omitempty"`
}

// EmbedResponse is the response from the embed API.
type EmbedResponse struct {
	Model      string      `json:"model"`
	Embeddings [][]float64 `json:"embeddings"`
}

// Model represents an LLM model.
type Model struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

// ModelsResponse is the response from the models API.
type ModelsResponse struct {
	Models []Model `json:"models"`
}

// ChunkCallback is called during streaming with the number of eval tokens
// received so far and the content of the current chunk.
// Used by the agent to emit live progress events.
type ChunkCallback func(tokensSoFar int, content string)
