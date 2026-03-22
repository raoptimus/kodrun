package ollama

// Message represents a chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall represents a tool invocation from the model.
type ToolCall struct {
	ID       string         `json:"id,omitempty"`
	Function ToolCallFunc   `json:"function"`
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
	Stream   bool           `json:"stream"`
}

// ToolDef describes a tool for the Ollama API.
type ToolDef struct {
	Type     string       `json:"type"`
	Function ToolFuncDef  `json:"function"`
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

// ChatResponse is the response from the chat API.
type ChatResponse struct {
	Model     string  `json:"model"`
	Message   Message `json:"message"`
	Done      bool    `json:"done"`
	DoneReason string `json:"done_reason,omitempty"`
}

// ChatChunk represents a streaming chunk or final aggregated response.
type ChatChunk struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Error     error
}

// Model represents an Ollama model.
type Model struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
}

// ModelsResponse is the response from the tags API.
type ModelsResponse struct {
	Models []Model `json:"models"`
}
