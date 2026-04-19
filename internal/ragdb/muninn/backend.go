/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package muninn

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/raoptimus/kodrun/internal/rag"
)

// Compile-time assertion.
var _ rag.Backend = (*Backend)(nil)

const (
	tagCommon = "common"
	tagGodoc  = "godoc"
	tagWeb    = "web"

	writeThrottle = 50 * time.Millisecond
)

// Backend implements rag.Backend using Muninn DB as the storage engine.
// Muninn handles embeddings internally, so no external LLM client is needed.
type Backend struct {
	common *Client
	godoc  *Client
	web    *Client

	mu      sync.RWMutex
	updated time.Time
	size    int
}

// NewBackend creates a Muninn-backed RAG backend. Sub-indexes are isolated
// via vault suffixes: "{vault}", "{vault}-godoc", "{vault}-web".
func NewBackend(opts *Options) *Backend {
	vault := opts.Vault
	if vault == "" {
		vault = "default"
	}

	return &Backend{
		common: NewClient(&Options{URL: opts.URL, Vault: vault}),
		godoc:  NewClient(&Options{URL: opts.URL, Vault: vault + "-godoc"}),
		web:    NewClient(&Options{URL: opts.URL, Vault: vault + "-web"}),
	}
}

func (b *Backend) LoadCommon() error { return nil }
func (b *Backend) LoadGodoc() error  { return nil }

func (b *Backend) HasLegacyCodeChunks() bool { return false }

func (b *Backend) Reset() error {
	return b.deleteAll(b.common)
}

func (b *Backend) BuildCommon(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return b.BuildCommonWithProgress(ctx, chunks, nil)
}

func (b *Backend) BuildCommonWithProgress(ctx context.Context, chunks []rag.Chunk, progress rag.ProgressFunc) (int, error) {
	return b.buildChunks(ctx, b.common, tagCommon, chunks, progress)
}

func (b *Backend) Build(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return b.BuildCommon(ctx, chunks)
}

func (b *Backend) Save() error       { return nil }
func (b *Backend) SaveCommon() error { return nil }

func (b *Backend) Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return b.search(ctx, b.common, query, topK)
}

func (b *Backend) Size() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.size
}

func (b *Backend) Updated() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.updated
}

func (b *Backend) BasePath() string { return b.common.baseURL }

// Godoc sub-index methods.

func (b *Backend) BuildGodoc(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return b.buildChunks(ctx, b.godoc, tagGodoc, chunks, nil)
}

func (b *Backend) SaveGodoc() error { return nil }

func (b *Backend) SearchGodoc(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return b.search(ctx, b.godoc, query, topK)
}

func (b *Backend) ResetGodoc() error {
	return b.deleteAll(b.godoc)
}

func (b *Backend) GodocSize() int { return 0 }

// Web sub-index methods.

func (b *Backend) BuildWeb(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return b.buildChunks(ctx, b.web, tagWeb, chunks, nil)
}

func (b *Backend) SearchWeb(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return b.search(ctx, b.web, query, topK)
}

func (b *Backend) WebSize() int { return 0 }

// Internal helpers.

func (b *Backend) buildChunks(ctx context.Context, client *Client, tag string, chunks []rag.Chunk, progress rag.ProgressFunc) (int, error) {
	written := 0

	for i, ch := range chunks {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}

		err := client.WriteEngram(ctx, &WriteEngramIn{
			Concept: ch.FilePath,
			Content: ch.Content,
			Tags:    []string{tag},
		})
		if err != nil {
			return written, err
		}

		written++

		if progress != nil {
			progress(tag, i+1, len(chunks))
		}

		// Throttle writes to avoid overloading Muninn.
		if i < len(chunks)-1 {
			select {
			case <-ctx.Done():
				return written, ctx.Err()
			case <-time.After(writeThrottle):
			}
		}
	}

	b.mu.Lock()
	b.size += written
	b.updated = time.Now()
	b.mu.Unlock()

	return written, nil
}

func (b *Backend) search(ctx context.Context, client *Client, query string, topK int) ([]rag.SearchResult, error) {
	engrams, err := client.Activate(ctx, &ActivateIn{
		Context:    []string{query},
		MaxResults: topK,
	})
	if err != nil {
		return nil, err
	}

	results := make([]rag.SearchResult, 0, len(engrams))

	for _, e := range engrams {
		results = append(results, rag.SearchResult{
			Chunk: rag.Chunk{
				FilePath: e.Concept,
				Content:  e.Content,
			},
			Score: e.Score,
		})
	}

	return results, nil
}

func (b *Backend) deleteAll(client *Client) error {
	engrams, err := client.ListEngrams(context.Background())
	if err != nil {
		return err
	}

	for _, e := range engrams {
		if err := client.DeleteEngram(context.Background(), e.ID); err != nil {
			// Skip errors for individual deletions during bulk reset.
			if !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	}

	return nil
}
