/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package openai

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/raoptimus/kodrun/internal/llm"
)

func newInternalClient() *Client {
	return New("http://localhost", "test-key", 5*time.Second)
}

func TestClient_buildChatRequest_NoTools_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result := c.buildChatRequest(req)

	assert.Equal(t, "gpt-4", result.Model)
	assert.True(t, result.Stream)
	require.Len(t, result.Messages, 1)
	assert.Equal(t, "user", result.Messages[0].Role)
	assert.Equal(t, "Hello", result.Messages[0].Content)
	assert.Empty(t, result.Tools)
	assert.Nil(t, result.ResponseFormat)
	assert.NotNil(t, result.StreamOptions)
	assert.True(t, result.StreamOptions.IncludeUsage)
}

func TestClient_buildChatRequest_WithSystemMessage_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "What is Go?"},
		},
	}

	result := c.buildChatRequest(req)

	require.Len(t, result.Messages, 2)
	assert.Equal(t, "system", result.Messages[0].Role)
	assert.Equal(t, "You are a helpful assistant.", result.Messages[0].Content)
	assert.Equal(t, "user", result.Messages[1].Role)
	assert.Equal(t, "What is Go?", result.Messages[1].Content)
}

func TestClient_buildChatRequest_WithTools_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "user", Content: "Read the file"},
		},
		Tools: []llm.ToolDef{
			{
				Type: "function",
				Function: llm.ToolFuncDef{
					Name:        "read_file",
					Description: "Reads a file from the filesystem",
					Parameters: llm.JSONSchema{
						Type: "object",
						Properties: map[string]llm.JSONSchema{
							"path": {
								Type:        "string",
								Description: "Path to the file",
							},
						},
						Required: []string{"path"},
					},
				},
			},
		},
	}

	result := c.buildChatRequest(req)

	require.Len(t, result.Tools, 1)
	assert.Equal(t, "function", result.Tools[0].Type)
	assert.Equal(t, "read_file", result.Tools[0].Function.Name)
	assert.Equal(t, "Reads a file from the filesystem", result.Tools[0].Function.Description)
	assert.Equal(t, "object", result.Tools[0].Function.Parameters.Type)
	require.Contains(t, result.Tools[0].Function.Parameters.Properties, "path")
	assert.Equal(t, "string", result.Tools[0].Function.Parameters.Properties["path"].Type)
	assert.Equal(t, []string{"path"}, result.Tools[0].Function.Parameters.Required)
}

func TestClient_buildChatRequest_WithJSONResponseFormat_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "user", Content: "Return JSON"},
		},
		Format: "json",
	}

	result := c.buildChatRequest(req)

	require.NotNil(t, result.ResponseFormat)
	assert.Equal(t, "json_object", result.ResponseFormat.Type)
}

func TestClient_buildChatRequest_WithoutJSONResponseFormat_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
		Format: "",
	}

	result := c.buildChatRequest(req)

	assert.Nil(t, result.ResponseFormat)
}

func TestClient_buildChatRequest_WithTemperatureAndMaxTokens_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
		Options: map[string]any{
			"temperature": float64(0.7),
			"num_ctx":     4096,
		},
	}

	result := c.buildChatRequest(req)

	require.NotNil(t, result.Temperature)
	assert.Equal(t, float64(0.7), *result.Temperature)
	assert.Equal(t, 4096, result.MaxTokens)
}

func TestClient_buildChatRequest_WithToolCallInMessage_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{
				Role: "assistant",
				ToolCalls: []llm.ToolCall{
					{
						ID: "call-abc",
						Function: llm.ToolCallFunc{
							Name:      "read_file",
							Arguments: map[string]any{"path": "main.go"},
						},
					},
				},
			},
		},
	}

	result := c.buildChatRequest(req)

	require.Len(t, result.Messages, 1)
	require.Len(t, result.Messages[0].ToolCalls, 1)
	assert.Equal(t, "call-abc", result.Messages[0].ToolCalls[0].ID)
	assert.Equal(t, "function", result.Messages[0].ToolCalls[0].Type)
	assert.Equal(t, "read_file", result.Messages[0].ToolCalls[0].Function.Name)

	// Arguments must be valid JSON string
	var args map[string]any
	err := json.Unmarshal([]byte(result.Messages[0].ToolCalls[0].Function.Arguments), &args)
	require.NoError(t, err)
	assert.Equal(t, "main.go", args["path"])
}

func TestClient_buildChatRequest_SerializesToValidJSON_Successfully(t *testing.T) {
	c := newInternalClient()
	req := &llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.Message{
			{Role: "system", Content: "You are helpful."},
			{Role: "user", Content: "Hello"},
		},
		Tools: []llm.ToolDef{
			{
				Type: "function",
				Function: llm.ToolFuncDef{
					Name:        "get_time",
					Description: "Returns current time",
					Parameters: llm.JSONSchema{
						Type: "object",
					},
				},
			},
		},
		Format: "json",
	}

	result := c.buildChatRequest(req)

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded map[string]any
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "gpt-4", decoded["model"])
	assert.Equal(t, true, decoded["stream"])
	assert.NotNil(t, decoded["messages"])
	assert.NotNil(t, decoded["tools"])
	assert.NotNil(t, decoded["response_format"])
}
