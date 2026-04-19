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

	"github.com/raoptimus/kodrun/internal/llm"
)

const maxErrorBodyBytes = 1024 * 1024

const (
	defaultMaxRetries     = 3
	streamChanBuffer      = 16
	streamScannerInitSize = 256 * 1024
	streamScannerMaxSize  = 1024 * 1024

	dialTimeout       = 30 * time.Second
	dialKeepAlive     = 30 * time.Second
	idleConnTimeout   = 90 * time.Second
	streamIdleTimeout = 2 * time.Minute
)

func init() {
	llm.RegisterFactory("ollama", func(cfg llm.ProviderConfig) (llm.Client, error) {
		return New(cfg.BaseURL, cfg.Timeout), nil
	})
}

// Client communicates with the Ollama API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
}

// New creates a new Ollama API client.
func New(baseURL string, timeout time.Duration) *Client {
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

func (c *Client) Models(ctx context.Context) ([]llm.Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", http.NoBody)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.WithMessage(err, "list models")
	}
	defer resp.Body.Close()

	var result modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Models, nil
}

func (c *Client) Chat(ctx context.Context, chatReq *llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	wireReq := chatRequest{
		Model:    chatReq.Model,
		Messages: chatReq.Messages,
		Tools:    chatReq.Tools,
		Options:  chatReq.Options,
		Format:   chatReq.Format,
		Stream:   true,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, errors.WithMessage(err, "marshal request")
	}

	var resp *http.Response

	for attempt := range c.maxRetries {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
		if reqErr != nil {
			return nil, errors.WithMessage(reqErr, "create request")
		}
		req.Header.Set("Content-Type", "application/json")

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
			return nil, errors.Errorf("ollama returned %d after %d retries", resp.StatusCode, c.maxRetries)
		}

		if resp.StatusCode != http.StatusOK {
			bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
			resp.Body.Close()
			if readErr != nil {
				return nil, errors.Errorf("ollama returned %d and failed to read body: %v", resp.StatusCode, readErr)
			}

			if resp.StatusCode == http.StatusInternalServerError &&
				strings.Contains(string(bodyBytes), "XML syntax error") &&
				len(wireReq.Tools) > 0 {
				wireReq.Tools = nil
				chatReq.Tools = nil
				body, err = json.Marshal(wireReq)
				if err != nil {
					return nil, errors.WithMessage(err, "marshal retry request")
				}
				continue
			}

			return nil, errors.Errorf("ollama error %d: %s", resp.StatusCode, string(bodyBytes))
		}

		break
	}

	ch := make(chan llm.ChatChunk, streamChanBuffer)
	go c.streamResponse(ctx, resp, ch)

	return ch, nil
}

func (c *Client) streamResponse(ctx context.Context, resp *http.Response, ch chan<- llm.ChatChunk) {
	defer close(ch)
	defer resp.Body.Close()

	idle := time.AfterFunc(streamIdleTimeout, func() { resp.Body.Close() })
	defer idle.Stop()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, streamScannerInitSize), streamScannerMaxSize)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chatResp chatResponse
		if err := json.Unmarshal(line, &chatResp); err != nil {
			select {
			case ch <- llm.ChatChunk{Error: errors.WithMessage(err, "decode chunk")}:
			case <-ctx.Done():
			}
			return
		}

		// Reset idle timer only on meaningful content (not empty keepalive chunks).
		if chatResp.Message.Content != "" || chatResp.Done || len(chatResp.Message.ToolCalls) > 0 {
			idle.Reset(streamIdleTimeout)
		}

		chunk := llm.ChatChunk{
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
		case ch <- llm.ChatChunk{Error: errors.WithMessage(err, "read stream")}:
		case <-ctx.Done():
		}
	}
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
		Model:    req.Model,
		Input:    req.Input,
		Truncate: req.Truncate,
	}

	body, err := json.Marshal(wireReq)
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

	var wireResp embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&wireResp); err != nil {
		return nil, errors.WithMessage(err, "decode embed response")
	}

	return &llm.EmbedResponse{
		Model:      wireResp.Model,
		Embeddings: wireResp.Embeddings,
	}, nil
}
