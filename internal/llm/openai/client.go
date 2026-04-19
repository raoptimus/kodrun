/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/llm"
)

const maxErrorBodyBytes = 1024 * 1024

const (
	defaultMaxRetries    = 3
	streamChanBuffer     = 16
	streamScannerMaxSize = 1024 * 1024

	dialTimeout     = 30 * time.Second
	dialKeepAlive   = 30 * time.Second
	idleConnTimeout = 90 * time.Second
)

func init() {
	llm.RegisterFactory("openai", func(cfg llm.ProviderConfig) (llm.Client, error) {
		return New(cfg.BaseURL, cfg.APIKey, cfg.Timeout), nil
	})
}

// Client communicates with an OpenAI-compatible API (e.g. vllm).
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	maxRetries int
}

// New creates a new OpenAI-compatible API client.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: dialKeepAlive,
		}).DialContext,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       idleConnTimeout,
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Transport: transport,
		},
		maxRetries: defaultMaxRetries,
	}
}

func (c *Client) setAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", http.NoBody)
	if err != nil {
		return errors.WithMessage(err, "create request")
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.WithMessagef(err, "openai-compatible server unreachable at %s", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("openai-compatible server returned status %d", resp.StatusCode)
	}

	return nil
}

func (c *Client) Models(ctx context.Context) ([]llm.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/models", http.NoBody)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WithMessage(err, "list models")
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	models := make([]llm.Model, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, llm.Model{
			Name: m.ID,
		})
	}

	return models, nil
}

func (c *Client) Chat(ctx context.Context, chatReq *llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	wireReq := c.buildChatRequest(chatReq)

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal request")
	}

	var resp *http.Response

	for attempt := range c.maxRetries {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if reqErr != nil {
			return nil, errors.WithMessage(reqErr, "create request")
		}
		req.Header.Set("Content-Type", "application/json")
		c.setAuth(req)

		resp, err = c.httpClient.Do(req)
		if err != nil {
			if llm.IsDialError(err) {
				return nil, errors.WithMessage(err, "chat request")
			}
			if attempt < c.maxRetries-1 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(1<<attempt) * time.Second):
					continue
				}
			}
			return nil, errors.WithMessage(err, "chat request")
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			resp.Body.Close()
			if attempt < c.maxRetries-1 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(1<<attempt) * time.Second):
					continue
				}
			}
			return nil, errors.Errorf("server returned %d after %d retries", resp.StatusCode, c.maxRetries)
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body.Close()
			if readErr != nil {
				return nil, errors.Errorf("server returned %d and failed to read body: %v", resp.StatusCode, readErr)
			}
			return nil, errors.Errorf("openai error %d: %s", resp.StatusCode, string(bodyBytes))
		}

		break
	}

	ch := make(chan llm.ChatChunk, streamChanBuffer)
	go c.streamResponse(ctx, resp, ch)

	return ch, nil
}

func (c *Client) buildChatRequest(chatReq *llm.ChatRequest) chatRequest {
	msgs := make([]chatMsg, 0, len(chatReq.Messages))
	for _, m := range chatReq.Messages {
		msg := chatMsg{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			argsJSON, err := json.Marshal(tc.Function.Arguments)
			if err != nil {
				argsJSON = []byte("{}")
			}
			msg.ToolCalls = append(msg.ToolCalls, toolCall{
				ID:   tc.ID,
				Type: "function",
				Function: toolCallFunc{
					Name:      tc.Function.Name,
					Arguments: string(argsJSON),
				},
			})
		}
		msgs = append(msgs, msg)
	}

	tools := make([]toolDef, 0, len(chatReq.Tools))
	for i := range chatReq.Tools {
		td := &chatReq.Tools[i]
		tools = append(tools, toolDef{
			Type: td.Type,
			Function: toolFuncDef{
				Name:        td.Function.Name,
				Description: td.Function.Description,
				Parameters:  convertSchema(&td.Function.Parameters),
			},
		})
	}

	req := chatRequest{
		Model:    chatReq.Model,
		Messages: msgs,
		Tools:    tools,
		Stream:   true,
		StreamOptions: &streamOpts{
			IncludeUsage: true,
		},
	}

	if chatReq.Options != nil {
		if temp, ok := chatReq.Options["temperature"]; ok {
			if v, ok := temp.(float64); ok {
				req.Temperature = &v
			}
		}
		if maxTok, ok := chatReq.Options["num_ctx"]; ok {
			if v, ok := maxTok.(int); ok {
				req.MaxTokens = v
			}
		}
	}

	if chatReq.Format == "json" {
		req.ResponseFormat = &respFormat{Type: "json_object"}
	}

	return req
}

func convertSchema(s *llm.JSONSchema) jsonSchema {
	result := jsonSchema{
		Type:        s.Type,
		Required:    s.Required,
		Description: s.Description,
		Enum:        s.Enum,
	}
	if s.Properties != nil {
		result.Properties = make(map[string]jsonSchema, len(s.Properties))
		for k, v := range s.Properties {
			result.Properties[k] = convertSchema(&v)
		}
	}
	if s.Items != nil {
		converted := convertSchema(s.Items)
		result.Items = &converted
	}
	return result
}

func (c *Client) streamResponse(ctx context.Context, resp *http.Response, ch chan<- llm.ChatChunk) {
	defer close(ch)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	const streamScannerInitSize = 64 * 1024
	scanner.Buffer(make([]byte, 0, streamScannerInitSize), streamScannerMaxSize)

	toolCallAccumulators := map[int]*toolCallAccumulator{}

	var usage *usageInfo

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			// Emit final chunk with accumulated tool calls and usage.
			chunk := llm.ChatChunk{Done: true}
			if len(toolCallAccumulators) > 0 {
				chunk.ToolCalls = buildToolCalls(toolCallAccumulators)
			}
			if usage != nil {
				chunk.PromptEvalCount = usage.PromptTokens
				chunk.EvalCount = usage.CompletionTokens
			}
			select {
			case ch <- chunk:
			case <-ctx.Done():
			}
			return
		}

		var sseChunk chatStreamChunk
		if err := json.Unmarshal([]byte(data), &sseChunk); err != nil {
			select {
			case ch <- llm.ChatChunk{Error: errors.WithMessage(err, "decode chunk")}:
			case <-ctx.Done():
			}
			return
		}

		if sseChunk.Usage != nil {
			usage = sseChunk.Usage
		}

		if len(sseChunk.Choices) == 0 {
			continue
		}

		delta := sseChunk.Choices[0].Delta

		// Accumulate tool call deltas.
		for _, tc := range delta.ToolCalls {
			idx := tc.Index_
			acc, ok := toolCallAccumulators[idx]
			if !ok {
				acc = &toolCallAccumulator{}
				toolCallAccumulators[idx] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				acc.argsBytes = append(acc.argsBytes, tc.Function.Arguments...)
			}
		}

		if delta.Content != "" {
			select {
			case ch <- llm.ChatChunk{Content: delta.Content}:
			case <-ctx.Done():
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case ch <- llm.ChatChunk{Error: errors.WithMessage(err, "read stream")}:
		case <-ctx.Done():
		}
	}
}

func buildToolCalls(accs map[int]*toolCallAccumulator) []llm.ToolCall {
	indices := make([]int, 0, len(accs))
	for idx := range accs {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	calls := make([]llm.ToolCall, 0, len(indices))
	for _, idx := range indices {
		acc := accs[idx]
		var args map[string]any
		if len(acc.argsBytes) > 0 {
			if err := json.Unmarshal(acc.argsBytes, &args); err != nil {
				args = map[string]any{"_raw": string(acc.argsBytes)}
			}
		}
		calls = append(calls, llm.ToolCall{
			ID: acc.id,
			Function: llm.ToolCallFunc{
				Name:      acc.name,
				Arguments: args,
			},
		})
	}
	return calls
}

func (c *Client) ChatSync(ctx context.Context, chatReq *llm.ChatRequest) (llm.ChatChunk, error) {
	return c.ChatSyncWithCallback(ctx, chatReq, nil)
}

func (c *Client) ChatSyncWithCallback(ctx context.Context, chatReq *llm.ChatRequest, cb llm.ChunkCallback) (llm.ChatChunk, error) {
	ch, err := c.Chat(ctx, chatReq)
	if err != nil {
		return llm.ChatChunk{}, err
	}
	return llm.AggregateChatStream(ctx, ch, cb)
}

func (c *Client) Embed(ctx context.Context, req llm.EmbedRequest) (*llm.EmbedResponse, error) {
	wireReq := embedRequest{
		Model: req.Model,
		Input: req.Input,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal embed request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, errors.WithMessage(err, "create embed request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.setAuth(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, errors.WithMessage(err, "embed request")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		if readErr != nil {
			return nil, errors.Errorf("embed error %d (failed to read body: %v)", resp.StatusCode, readErr)
		}
		return nil, errors.Errorf("embed error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var wireResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&wireResp); err != nil {
		return nil, errors.WithMessage(err, "decode embed response")
	}

	embeddings := make([][]float64, len(wireResp.Data))
	for i, d := range wireResp.Data {
		embeddings[i] = d.Embedding
	}

	return &llm.EmbedResponse{
		Model:      wireResp.Model,
		Embeddings: embeddings,
	}, nil
}
