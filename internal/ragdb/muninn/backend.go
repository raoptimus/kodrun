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
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
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
	stateDirPerm  = 0o755
	stateFilePerm = 0o600
)

// Backend implements rag.Backend using Muninn DB as the storage engine.
// Muninn handles embeddings internally, so no external LLM client is needed.
type Backend struct {
	common   *Client
	godoc    *Client
	web      *Client
	stateDir string // local directory for chunk-set hash cache

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
		common:   NewClient(&Options{URL: opts.URL, Vault: vault}),
		godoc:    NewClient(&Options{URL: opts.URL, Vault: vault + "-godoc"}),
		web:      NewClient(&Options{URL: opts.URL, Vault: vault + "-web"}),
		stateDir: opts.StateDir,
	}
}

type muninnState struct {
	ChunkHash string `json:"chunk_hash"`
}

func (b *Backend) stateFile() string {
	if b.stateDir == "" {
		return ""
	}
	return filepath.Join(b.stateDir, "muninn_state.json")
}

func (b *Backend) loadHash() string {
	f := b.stateFile()
	if f == "" {
		return ""
	}
	data, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	var s muninnState
	if err := json.Unmarshal(data, &s); err != nil {
		return ""
	}
	return s.ChunkHash
}

func (b *Backend) saveHash(hash string) error {
	f := b.stateFile()
	if f == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f), stateDirPerm); err != nil {
		return err
	}
	data, err := json.Marshal(muninnState{ChunkHash: hash})
	if err != nil {
		return err
	}
	return os.WriteFile(f, data, stateFilePerm)
}

func (b *Backend) LoadCommon() error { return nil }
func (b *Backend) LoadGodoc() error  { return nil }

func (b *Backend) HasLegacyCodeChunks() bool { return false }

func (b *Backend) Reset() error {
	return b.deleteAll(context.Background(), b.common)
}

func (b *Backend) BuildCommon(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return b.BuildCommonWithProgress(ctx, chunks, nil)
}

func (b *Backend) BuildCommonWithProgress(ctx context.Context, chunks []rag.Chunk, progress rag.ProgressFunc) (int, error) {
	if b.stateDir != "" {
		currentHash := rag.ChunkSetHash(chunks)
		storedHash := b.loadHash()
		if storedHash == currentHash {
			if progress != nil {
				progress("up to date", 0, 0)
			}
			return 0, nil
		}
		// Hash changed: reset to avoid stale/duplicate engrams, then re-index.
		if storedHash != "" {
			if err := b.deleteAll(ctx, b.common); err != nil {
				slog.Warn("muninn: reset before reindex failed", "error", err)
			}
		}
	}

	n, err := b.buildChunks(ctx, b.common, tagCommon, chunks, progress)
	if err != nil {
		return n, err
	}

	if b.stateDir != "" {
		if saveErr := b.saveHash(rag.ChunkSetHash(chunks)); saveErr != nil {
			slog.Warn("muninn: save state failed", "error", saveErr)
		}
	}

	return n, nil
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
	return b.deleteAll(context.Background(), b.godoc)
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

func (b *Backend) deleteAll(ctx context.Context, client *Client) error {
	engrams, err := client.ListEngrams(ctx)
	if err != nil {
		return err
	}

	for _, e := range engrams {
		if err := client.DeleteEngram(ctx, e.ID); err != nil {
			// Skip errors for individual deletions during bulk reset.
			if !strings.Contains(err.Error(), "not found") {
				return err
			}
		}
	}

	return nil
}
