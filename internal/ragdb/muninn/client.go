package muninn

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/pkg/errors"
)

var ErrUnexpectedStatus = errors.New("muninn: unexpected status")

const (
	defaultTimeout    = 2 * time.Minute
	maxRetries        = 3
	initialRetryDelay = 500 * time.Millisecond
	listPageSize      = 100
)

type Options struct {
	URL   string
	Vault string
}

type WriteEngramIn struct {
	Concept string
	Content string
	Tags    []string
}

type ActivateIn struct {
	Context    []string
	MaxResults int
	MaxHops    int
	IncludeWhy bool
}

type Engram struct {
	ID      string  `json:"id"`
	Concept string  `json:"concept"`
	Content string  `json:"content"`
	Score   float64 `json:"score,omitempty"`
	Why     string  `json:"why,omitempty"`
}

type Client struct {
	httpClient *http.Client
	baseURL    string
	vault      string
}

func NewClient(opts *Options) *Client {
	return &Client{
		httpClient: &http.Client{Timeout: defaultTimeout},
		baseURL:    opts.URL,
		vault:      opts.Vault,
	}
}

func (c *Client) WriteEngram(ctx context.Context, in *WriteEngramIn) error {
	body := map[string]any{
		"vault":   c.vault,
		"concept": in.Concept,
		"content": in.Content,
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}

	data, err := json.Marshal(body)
	if err != nil {
		return errors.Wrap(err, "muninn: marshal engram")
	}

	delay := initialRetryDelay

	for attempt := range maxRetries + 1 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}

			delay *= 2
		}

		err = c.doWriteEngram(ctx, data)
		if err == nil {
			return nil
		}

		if !isRetriable(err) {
			return err
		}

		if d := retryAfterDelay(err); d > 0 {
			delay = d
		}
	}

	return err
}

func (c *Client) doWriteEngram(ctx context.Context, data []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/engrams", bytes.NewReader(data))
	if err != nil {
		return errors.Wrap(err, "muninn: create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "muninn: write engram")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		return nil
	}

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return errors.Wrap(readErr, "muninn: read error response")
	}

	statusErr := &statusError{
		statusCode: resp.StatusCode,
		retryAfter: resp.Header.Get("Retry-After"),
	}

	return errors.Wrapf(statusErr, "write engram: status %d: %s", resp.StatusCode, string(respBody))
}

type statusError struct {
	statusCode int
	retryAfter string
}

func (e *statusError) Error() string {
	return ErrUnexpectedStatus.Error()
}

func (e *statusError) Is(target error) bool {
	return target == ErrUnexpectedStatus
}

func isRetriable(err error) bool {
	var se *statusError
	if !errors.As(err, &se) {
		return false
	}

	return se.statusCode == http.StatusTooManyRequests || se.statusCode >= http.StatusInternalServerError
}

func retryAfterDelay(err error) time.Duration {
	var se *statusError
	if !errors.As(err, &se) || se.retryAfter == "" {
		return 0
	}

	seconds, parseErr := strconv.Atoi(se.retryAfter)
	if parseErr != nil {
		return 0
	}

	return time.Duration(seconds) * time.Second
}

func (c *Client) Activate(ctx context.Context, in *ActivateIn) ([]Engram, error) {
	maxResults := in.MaxResults
	if maxResults == 0 {
		maxResults = 5
	}

	body := map[string]any{
		"context":     in.Context,
		"max_results": maxResults,
	}
	if c.vault != "" {
		body["vault"] = c.vault
	}
	if in.MaxHops > 0 {
		body["max_hops"] = in.MaxHops
	}
	if in.IncludeWhy {
		body["include_why"] = true
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "muninn: marshal activate request")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/activate", bytes.NewReader(data))
	if err != nil {
		return nil, errors.Wrap(err, "muninn: create request")
	}

	req.Header.Set("Content-Type", "application/json")
	req.Close = true

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "muninn: activate")
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "muninn: read activate response")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, errors.Wrapf(ErrUnexpectedStatus, "activate: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Activations []Engram `json:"activations"`
	}

	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, errors.Wrap(err, "muninn: decode activate response")
	}

	return result.Activations, nil
}

func (c *Client) ListEngrams(ctx context.Context) ([]Engram, error) {
	var all []Engram

	for offset := 0; ; offset += listPageSize {
		url := fmt.Sprintf("%s/api/engrams?vault=%s&limit=%d&offset=%d",
			c.baseURL, c.vault, listPageSize, offset)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return nil, errors.Wrap(err, "muninn: create list request")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, errors.Wrap(err, "muninn: list engrams")
		}

		respBody, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if err != nil {
			return nil, errors.Wrap(err, "muninn: read list response")
		}

		if resp.StatusCode != http.StatusOK {
			return nil, errors.Wrapf(ErrUnexpectedStatus, "list engrams: status %d: %s", resp.StatusCode, string(respBody))
		}

		var page struct {
			Engrams []Engram `json:"engrams"`
			Total   int      `json:"total"`
		}

		if err := json.Unmarshal(respBody, &page); err != nil {
			return nil, errors.Wrap(err, "muninn: decode list response")
		}

		all = append(all, page.Engrams...)

		if len(all) >= page.Total {
			break
		}
	}

	return all, nil
}

func (c *Client) DeleteEngram(ctx context.Context, id string) error {
	url := fmt.Sprintf("%s/api/engrams/%s", c.baseURL, id)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, http.NoBody)
	if err != nil {
		return errors.Wrap(err, "muninn: create delete request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "muninn: delete engram")
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return errors.Wrap(readErr, "muninn: read delete error response")
		}

		return errors.Wrapf(ErrUnexpectedStatus, "delete engram %s: status %d: %s", id, resp.StatusCode, string(respBody))
	}

	return nil
}
