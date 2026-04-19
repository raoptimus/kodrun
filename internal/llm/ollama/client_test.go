package ollama_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/raoptimus/kodrun/internal/llm"
	llmollama "github.com/raoptimus/kodrun/internal/llm/ollama"
)

// ollamaChatResponse mirrors the Ollama wire-format response for test fixtures.
type ollamaChatResponse struct {
	Model              string      `json:"model"`
	Message            llm.Message `json:"message"`
	Done               bool        `json:"done"`
	PromptEvalCount    int         `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64       `json:"prompt_eval_duration,omitempty"`
	EvalCount          int         `json:"eval_count,omitempty"`
	EvalDuration       int64       `json:"eval_duration,omitempty"`
	TotalDuration      int64       `json:"total_duration,omitempty"`
	LoadDuration       int64       `json:"load_duration,omitempty"`
}

func newTestClient(t *testing.T, handler http.HandlerFunc) llm.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return llmollama.New(srv.URL, 5*time.Second)
}

// --- Ping ---

func TestClient_Ping_ServerReturns200_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	err := c.Ping(context.Background())

	assert.NoError(t, err)
}

func TestClient_Ping_ServerReturnsNon200_Failure(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{
			name:       "404 not found",
			statusCode: http.StatusNotFound,
		},
		{
			name:       "500 internal server error",
			statusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			err := c.Ping(context.Background())

			assert.ErrorContains(t, err, fmt.Sprintf("ollama returned status %d", tt.statusCode))
		})
	}
}

func TestClient_Ping_ServerDown_Failure(t *testing.T) {
	c := llmollama.New("http://127.0.0.1:1", 1*time.Second)

	err := c.Ping(context.Background())

	assert.ErrorContains(t, err, "ollama unreachable")
}

// --- Models ---

func TestClient_Models_ReturnsModels_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Models []llm.Model `json:"models"`
		}{
			Models: []llm.Model{
				{Name: "llama3:latest", ModifiedAt: "2024-01-01T00:00:00Z", Size: 4000000000},
				{Name: "codellama:7b", ModifiedAt: "2024-02-01T00:00:00Z", Size: 3500000000},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	models, err := c.Models(context.Background())

	require.NoError(t, err)
	require.Len(t, models, 2)
	assert.Equal(t, "llama3:latest", models[0].Name)
	assert.Equal(t, "codellama:7b", models[1].Name)
}

func TestClient_Models_EmptyList_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Models []llm.Model `json:"models"`
		}{Models: []llm.Model{}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	models, err := c.Models(context.Background())

	require.NoError(t, err)
	assert.Empty(t, models)
}

func TestClient_Models_InvalidJSON_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid json`))
	})

	_, err := c.Models(context.Background())

	assert.Error(t, err)
}

// --- Chat streaming ---

func TestClient_Chat_StreamsChunksInOrder_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{Message: llm.Message{Content: "Hello"}, Done: false},
			{Message: llm.Message{Content: " world"}, Done: false},
			{Message: llm.Message{Content: ""}, Done: true, EvalCount: 10, TotalDuration: 5000},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	ch, err := c.Chat(context.Background(), chatReq)

	require.NoError(t, err)

	var received []llm.ChatChunk
	for chunk := range ch {
		received = append(received, chunk)
	}

	require.Len(t, received, 3)
	assert.Equal(t, "Hello", received[0].Content)
	assert.False(t, received[0].Done)
	assert.Equal(t, " world", received[1].Content)
	assert.False(t, received[1].Done)
	assert.True(t, received[2].Done)
	assert.Equal(t, 10, received[2].EvalCount)
	assert.Equal(t, int64(5000), received[2].TotalDuration)
}

func TestClient_Chat_RetryOn429_Successfully(t *testing.T) {
	var attempts atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(ollamaChatResponse{Message: llm.Message{Content: "ok"}, Done: true})
		_, _ = fmt.Fprintf(w, "%s\n", data)
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	ch, err := c.Chat(context.Background(), chatReq)

	require.NoError(t, err)

	var received []llm.ChatChunk
	for chunk := range ch {
		received = append(received, chunk)
	}

	assert.Equal(t, int32(3), attempts.Load())
	require.Len(t, received, 1)
	assert.Equal(t, "ok", received[0].Content)
}

func TestClient_Chat_RetryOnXMLSyntaxError_Successfully(t *testing.T) {
	var attempts atomic.Int32
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("XML syntax error in tool call"))
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		data, _ := json.Marshal(ollamaChatResponse{Message: llm.Message{Content: "plain response"}, Done: true})
		_, _ = fmt.Fprintf(w, "%s\n", data)
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
		Tools: []llm.ToolDef{
			{Type: "function", Function: llm.ToolFuncDef{Name: "read_file"}},
		},
	}
	ch, err := c.Chat(context.Background(), chatReq)

	require.NoError(t, err)

	var received []llm.ChatChunk
	for chunk := range ch {
		received = append(received, chunk)
	}

	assert.Equal(t, int32(2), attempts.Load())
	require.Len(t, received, 1)
	assert.Equal(t, "plain response", received[0].Content)
}

func TestClient_Chat_NoRetryOnDialError_Failure(t *testing.T) {
	c := llmollama.New("http://127.0.0.1:1", 1*time.Second)

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	_, err := c.Chat(context.Background(), chatReq)

	assert.ErrorContains(t, err, "chat request")
}

func TestClient_Chat_Non200Error_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("model not found"))
	})

	chatReq := &llm.ChatRequest{
		Model:    "nonexistent",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	_, err := c.Chat(context.Background(), chatReq)

	assert.ErrorContains(t, err, "ollama error 400")
	assert.ErrorContains(t, err, "model not found")
}

func TestClient_Chat_MalformedJSONInStream_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		_, _ = fmt.Fprintln(w, `{not valid json}`)
		flusher.Flush()
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	ch, err := c.Chat(context.Background(), chatReq)

	require.NoError(t, err)

	var received []llm.ChatChunk
	for chunk := range ch {
		received = append(received, chunk)
	}

	require.Len(t, received, 1)
	assert.Error(t, received[0].Error)
	assert.ErrorContains(t, received[0].Error, "decode chunk")
}

// --- ChatSync ---

func TestClient_ChatSync_AggregatesContent_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{Message: llm.Message{Content: "Hello"}, Done: false},
			{Message: llm.Message{Content: " world"}, Done: false},
			{Message: llm.Message{Content: "!"}, Done: false},
			{
				Message:            llm.Message{Content: ""},
				Done:               true,
				EvalCount:          15,
				EvalDuration:       3000,
				PromptEvalCount:    5,
				PromptEvalDuration: 1000,
				TotalDuration:      5000,
				LoadDuration:       500,
			},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	result, err := c.ChatSync(context.Background(), chatReq)

	require.NoError(t, err)
	assert.Equal(t, "Hello world!", result.Content)
	assert.True(t, result.Done)
	assert.Equal(t, 15, result.EvalCount)
	assert.Equal(t, int64(3000), result.EvalDuration)
	assert.Equal(t, 5, result.PromptEvalCount)
	assert.Equal(t, int64(1000), result.PromptEvalDuration)
	assert.Equal(t, int64(5000), result.TotalDuration)
	assert.Equal(t, int64(500), result.LoadDuration)
}

func TestClient_ChatSync_StructuredToolCalls_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{
				Message: llm.Message{
					ToolCalls: []llm.ToolCall{
						{
							ID: "call-1",
							Function: llm.ToolCallFunc{
								Name:      "read_file",
								Arguments: map[string]any{"path": "main.go"},
							},
						},
					},
				},
				Done: false,
			},
			{Message: llm.Message{Content: ""}, Done: true, EvalCount: 5},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "read main.go"}},
	}
	result, err := c.ChatSync(context.Background(), chatReq)

	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "call-1", result.ToolCalls[0].ID)
	assert.Equal(t, "read_file", result.ToolCalls[0].Function.Name)
	assert.Equal(t, "main.go", result.ToolCalls[0].Function.Arguments["path"])
}

func TestClient_ChatSync_FallbackToolCallsParsedFromContent_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{Message: llm.Message{Content: `{"name":"read_file","arguments":{"path":"/tmp/test.go"}}`}, Done: false},
			{Message: llm.Message{Content: ""}, Done: true, EvalCount: 3},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "read test.go"}},
	}
	result, err := c.ChatSync(context.Background(), chatReq)

	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "fallback-0", result.ToolCalls[0].ID)
	assert.Equal(t, "read_file", result.ToolCalls[0].Function.Name)
	assert.Equal(t, "/tmp/test.go", result.ToolCalls[0].Function.Arguments["path"])
}

func TestClient_ChatSync_ErrorJSONDetected_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{Message: llm.Message{Content: `{"error":"upstream timeout"}`}, Done: false},
			{Message: llm.Message{Content: ""}, Done: true},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	_, err := c.ChatSync(context.Background(), chatReq)

	assert.ErrorContains(t, err, "llm error")
	assert.ErrorContains(t, err, "upstream timeout")
}

// --- ChatSyncWithCallback ---

func TestClient_ChatSyncWithCallback_CallbackCalledWithTokenCount_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		chunks := []ollamaChatResponse{
			{Message: llm.Message{Content: "a"}, Done: false},
			{Message: llm.Message{Content: "b"}, Done: false},
			{Message: llm.Message{Content: "c"}, Done: false},
			{Message: llm.Message{Content: ""}, Done: true},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "%s\n", data)
			flusher.Flush()
		}
	})

	var callbackTokens []int
	cb := func(tokensSoFar int, _ string) {
		callbackTokens = append(callbackTokens, tokensSoFar)
	}

	chatReq := &llm.ChatRequest{
		Model:    "llama3",
		Messages: []llm.Message{{Role: "user", Content: "Hi"}},
	}
	result, err := c.ChatSyncWithCallback(context.Background(), chatReq, cb)

	require.NoError(t, err)
	assert.Equal(t, "abc", result.Content)
	assert.Equal(t, []int{1, 2, 3, 3}, callbackTokens)
}

// --- Embed ---

func TestClient_Embed_ReturnsEmbeddings_Successfully(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := struct {
			Model      string      `json:"model"`
			Embeddings [][]float64 `json:"embeddings"`
		}{
			Model:      "llama3",
			Embeddings: [][]float64{{0.1, 0.2, 0.3}, {0.4, 0.5, 0.6}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	result, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "llama3",
		Input: []string{"hello", "world"},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "llama3", result.Model)
	require.Len(t, result.Embeddings, 2)
	assert.Equal(t, []float64{0.1, 0.2, 0.3}, result.Embeddings[0])
	assert.Equal(t, []float64{0.4, 0.5, 0.6}, result.Embeddings[1])
}

func TestClient_Embed_ServerError_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	})

	_, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "llama3",
		Input: []string{"hello"},
	})

	assert.ErrorContains(t, err, "embed error 500")
	assert.ErrorContains(t, err, "internal error")
}

func TestClient_Embed_InvalidJSON_Failure(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`not json`))
	})

	_, err := c.Embed(context.Background(), llm.EmbedRequest{
		Model: "llama3",
		Input: []string{"hello"},
	})

	assert.ErrorContains(t, err, "decode embed response")
}
