/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE.md
 * @link https://github.com/raoptimus/kodrun
 */

package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// maxErrorBodyBytes limits how much error response body we read to prevent OOM.
const maxErrorBodyBytes = 1024 * 1024 // 1MB

const (
	defaultMaxRetries     = 3  // total retry attempts for chat requests
	streamChanBuffer      = 16 // buffered channel size for streaming responses
	streamScannerInitSize = 256 * 1024
	streamScannerMaxSize  = 1024 * 1024

	dialTimeout       = 30 * time.Second
	dialKeepAlive     = 30 * time.Second
	idleConnTimeout   = 90 * time.Second
	streamIdleTimeout = 2 * time.Minute
)

// Client communicates with the Ollama API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// NewClient creates a new Ollama API client.
// The timeout parameter controls how long to wait for the first response byte
// (ResponseHeaderTimeout). There is no total-duration timeout — active streams
// are protected by per-read idle timeout and context cancellation.
func NewClient(baseURL string, timeout time.Duration) *Client {
	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: dialKeepAlive,
		}).DialContext,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       idleConnTimeout,
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
		},
		maxRetries: defaultMaxRetries,
	}
}

// Ping checks if Ollama is reachable.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", http.NoBody)
	if err != nil {
		return errors.WithMessage(err, "create request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.WithMessagef(err, "ollama unreachable at %s (is 'ollama serve' running?)", c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("ollama returned status %d", resp.StatusCode)
	}

	return nil
}

// Models returns available models.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WithMessage(err, "list models")
	}
	defer resp.Body.Close()

	var result ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Models, nil
}

// Chat sends a chat request and returns a channel of streaming chunks.
func (c *Client) Chat(ctx context.Context, chatReq *ChatRequest) (<-chan ChatChunk, error) {
	chatReq.Stream = true

	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal request")
	}

	var resp *http.Response

	for attempt := range c.maxRetries {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
		if err != nil {
			return nil, errors.WithMessage(err, "create request")
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = c.httpClient.Do(req)
		if err != nil {
			// Connection refused / DNS failure — ollama is down, don't waste
			// time retrying because it won't come back in 2 seconds.
			if isDialError(err) {
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
			return nil, errors.Errorf("ollama returned %d after %d retries", resp.StatusCode, c.maxRetries)
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body.Close()
			if readErr != nil {
				return nil, errors.Errorf("ollama returned %d and failed to read body: %v", resp.StatusCode, readErr)
			}

			// Ollama 500 with "XML syntax error" means the model produced
			// a malformed tool call that Ollama couldn't parse. Retry once
			// without tools so the model responds with plain text instead.
			if resp.StatusCode == http.StatusInternalServerError &&
				strings.Contains(string(bodyBytes), "XML syntax error") &&
				len(chatReq.Tools) > 0 {
				chatReq.Tools = nil
				body, err = json.Marshal(chatReq)
				if err != nil {
					return nil, errors.WithMessage(err, "marshal retry request")
				}
				continue
			}

			return nil, errors.Errorf("ollama error %d: %s", resp.StatusCode, string(bodyBytes))
		}

		break
	}

	ch := make(chan ChatChunk, streamChanBuffer)
	go c.streamResponse(ctx, resp, ch)

	return ch, nil
}

func (c *Client) streamResponse(ctx context.Context, resp *http.Response, ch chan<- ChatChunk) {
	defer close(ch)
	defer resp.Body.Close()

	// Idle timer: if no data arrives for streamIdleTimeout, close the body
	// to unblock the scanner. This prevents indefinite hangs when the model
	// stops producing tokens without closing the connection.
	idle := time.AfterFunc(streamIdleTimeout, func() { resp.Body.Close() })
	defer idle.Stop()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, streamScannerInitSize), streamScannerMaxSize)

	for scanner.Scan() {
		idle.Reset(streamIdleTimeout)
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chatResp ChatResponse
		if err := json.Unmarshal(line, &chatResp); err != nil {
			select {
			case ch <- ChatChunk{Error: errors.WithMessage(err, "decode chunk")}:
			case <-ctx.Done():
			}
			return
		}

		chunk := ChatChunk{
			Content:            chatResp.Message.Content,
			ToolCalls:          chatResp.Message.ToolCalls,
			Done:               chatResp.Done,
			PromptEvalCount:    chatResp.PromptEvalCount,
			PromptEvalDuration: chatResp.PromptEvalDuration,
			EvalCount:          chatResp.EvalCount,
			EvalDuration:       chatResp.EvalDuration,
			TotalDuration:      chatResp.TotalDuration,
			LoadDuration:       chatResp.LoadDuration,
		}

		select {
		case ch <- chunk:
		case <-ctx.Done():
			return
		}

		if chatResp.Done {
			return
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case ch <- ChatChunk{Error: errors.WithMessage(err, "read stream")}:
		case <-ctx.Done():
		}
	}
}

// ChunkCallback is called during streaming with the number of eval tokens
// received so far. Used by the agent to emit live progress events.
type ChunkCallback func(tokensSoFar int)

// ChatSync sends a chat request and returns aggregated response.
func (c *Client) ChatSync(ctx context.Context, chatReq *ChatRequest) (ChatChunk, error) {
	return c.ChatSyncWithCallback(ctx, chatReq, nil)
}

// ChatSyncWithCallback is like ChatSync but calls cb after each chunk
// with the running count of eval tokens. This allows callers to emit
// progress events while the model is generating.
func (c *Client) ChatSyncWithCallback(ctx context.Context, chatReq *ChatRequest, cb ChunkCallback) (ChatChunk, error) {
	ch, err := c.Chat(ctx, chatReq)
	if err != nil {
		return ChatChunk{}, err
	}

	var result ChatChunk
	var contentBuf bytes.Buffer
	var evalTokens int

	for chunk := range ch {
		if chunk.Error != nil {
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
		// Token counts and timings come in the final chunk (done=true)
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
			cb(evalTokens)
		}
	}

	result.Content = contentBuf.String()

	// Detect upstream error JSON returned as content (proxy/policy errors
	// served with HTTP 200). Surface as a real error so the agent loop
	// stops instead of treating the JSON as a normal model reply.
	if msg := detectErrorJSON(result.Content); msg != "" {
		return ChatChunk{}, errors.Errorf("llm error: %s", msg)
	}

	// Try parsing tool calls from text if none were returned structurally
	if len(result.ToolCalls) == 0 && result.Content != "" {
		if parsed, ok := ParseToolCalls(result.Content); ok {
			result.ToolCalls = parsed
			// Clean content: remove parsed tool call markup, keep surrounding text
			result.Content = CleanToolCallText(result.Content)
		}
	}

	return result, nil
}

// detectErrorJSON returns a non-empty message when content is an error envelope
// like {"error":{"type":"...","message":"..."}} or {"error":"..."}.
// Returns "" for normal content.
func detectErrorJSON(content string) string {
	s := strings.TrimSpace(content)
	if len(s) < 2 || s[0] != '{' {
		return ""
	}
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(s), &envelope); err != nil || len(envelope.Error) == 0 {
		return ""
	}
	// error может быть строкой или объектом {type,message}
	var asStr string
	if err := json.Unmarshal(envelope.Error, &asStr); err == nil && asStr != "" {
		return asStr
	}
	var asObj struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(envelope.Error, &asObj); err == nil {
		if asObj.Message != "" {
			return asObj.Message
		}
		if asObj.Type != "" {
			return asObj.Type
		}
	}
	return ""
}

// Embed generates embeddings for the given input texts.
func (c *Client) Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal embed request")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, errors.WithMessage(err, "create embed request")
	}
	httpReq.Header.Set("Content-Type", "application/json")

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

	var result EmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.WithMessage(err, "decode embed response")
	}

	return &result, nil
}

// isDialError returns true when the error is a TCP dial failure (connection
// refused, DNS resolution error, etc.). These indicate that ollama is down
// and retrying immediately is pointless.
func isDialError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}
