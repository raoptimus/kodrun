/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package rag

import (
	"context"
	"time"
)

// Backend is the interface that RAG storage backends must implement.
// Both the local in-memory index (MultiIndex) and remote backends
// (e.g. Muninn) satisfy it.
type Backend interface {
	// LoadCommon loads the common sub-index from persistent storage.
	LoadCommon() error
	// LoadGodoc loads the Go documentation sub-index.
	LoadGodoc() error
	// HasLegacyCodeChunks reports whether the index contains stale
	// source-code entries from older kodrun versions.
	HasLegacyCodeChunks() bool
	// Reset wipes all common index data (in-memory and persistent).
	Reset() error

	// BuildCommon indexes chunks into the common sub-index.
	BuildCommon(ctx context.Context, chunks []Chunk) (int, error)
	// BuildCommonWithProgress is BuildCommon with a progress callback.
	BuildCommonWithProgress(ctx context.Context, chunks []Chunk, progress ProgressFunc) (int, error)
	// Build is a convenience alias for BuildCommon.
	Build(ctx context.Context, chunks []Chunk) (int, error)
	// Save persists the common sub-index.
	Save() error
	// SaveCommon writes the common sub-index to persistent storage.
	SaveCommon() error

	// Search returns top-k matching chunks from the common sub-index.
	Search(ctx context.Context, query string, topK int) ([]SearchResult, error)
	// Size returns the number of entries in the common sub-index.
	Size() int
	// Updated returns the last update timestamp.
	Updated() time.Time
	// BasePath returns the root storage path (local path or remote URL).
	BasePath() string

	// Godoc sub-index methods.
	BuildGodoc(ctx context.Context, chunks []Chunk) (int, error)
	SaveGodoc() error
	SearchGodoc(ctx context.Context, query string, topK int) ([]SearchResult, error)
	ResetGodoc() error
	GodocSize() int

	// Web sub-index methods (session-scoped, in-memory).
	BuildWeb(ctx context.Context, chunks []Chunk) (int, error)
	SearchWeb(ctx context.Context, query string, topK int) ([]SearchResult, error)
	WebSize() int
}

// BackendGodocAdapter wraps a Backend into an adapter that satisfies
// tools.GoDocIndexer by delegating to the godoc sub-index methods.
type BackendGodocAdapter struct {
	B Backend
}

func (a *BackendGodocAdapter) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return a.B.BuildGodoc(ctx, chunks)
}

func (a *BackendGodocAdapter) Save() error {
	return a.B.SaveGodoc()
}

func (a *BackendGodocAdapter) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return a.B.SearchGodoc(ctx, query, topK)
}

// BackendWebAdapter wraps a Backend into an adapter that satisfies
// tools.WebIndexer by delegating to the web sub-index methods.
type BackendWebAdapter struct {
	B Backend
}

func (a *BackendWebAdapter) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return a.B.BuildWeb(ctx, chunks)
}

func (a *BackendWebAdapter) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return a.B.SearchWeb(ctx, query, topK)
}
