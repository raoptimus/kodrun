package rag

import (
	"context"
	"path/filepath"
	"time"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// commonKey is the subdirectory used for the single on-disk sub-index.
// The name is preserved for backward compatibility with existing index
// layouts on disk (`.kodrun/rag_index/common/index.json`).
const commonKey = "common"

// godocKey is the subdirectory for the Go documentation sub-index.
const godocKey = "godoc"

// MultiIndex wraps a single Index sub-directory. Historically it supported
// per-language sub-indexes so that switching the active project language
// would not require re-embedding language-neutral chunks, but RAG now only
// indexes project conventions (rules / snippets / docs / embedded language
// standards) — none of which are language-partitioned — so a single common
// sub-index is sufficient. The MultiIndex wrapper is kept purely to avoid
// churning every caller; the name is historical.
type MultiIndex struct {
	basePath string
	common   *Index
	godoc    *Index
	web      *Index // in-memory, session-scoped; never persisted to disk
}

// NewMultiIndex creates a new MultiIndex rooted at basePath.
func NewMultiIndex(client *ollama.Client, model, basePath string) *MultiIndex {
	return &MultiIndex{
		basePath: basePath,
		common:   NewIndex(client, model, filepath.Join(basePath, commonKey)),
		godoc:    NewIndex(client, model, filepath.Join(basePath, godocKey)),
		web:      NewIndex(client, model, ""), // in-memory only
	}
}

// BasePath returns the root directory the index is stored under.
func (m *MultiIndex) BasePath() string { return m.basePath }

// LoadCommon loads the sub-index from disk.
func (m *MultiIndex) LoadCommon() error { return m.common.Load() }

// Reset wipes the sub-index both in memory and on disk. /reindex uses it
// to start every rebuild from a clean slate.
func (m *MultiIndex) Reset() error { return m.common.Reset() }

// HasLegacyCodeChunks reports whether the loaded index contains entries
// whose FilePath points at real source files (not rules://, snippets://,
// embedded:// or files under a /docs/ directory). Such entries are leftovers
// from earlier kodrun versions that indexed project code; they must be
// wiped on startup so stale snippets cannot surface in /code-review.
func (m *MultiIndex) HasLegacyCodeChunks() bool {
	return m.common.hasLegacyCodeChunks()
}

// BuildCommon embeds chunks into the sub-index.
func (m *MultiIndex) BuildCommon(ctx context.Context, chunks []Chunk) (int, error) {
	return m.common.Build(ctx, chunks)
}

// BuildCommonWithProgress is BuildCommon with a progress callback. See
// Index.BuildWithProgress for the callback contract.
func (m *MultiIndex) BuildCommonWithProgress(ctx context.Context, chunks []Chunk, progress ProgressFunc) (int, error) {
	return m.common.BuildWithProgress(ctx, chunks, progress)
}

// Build is a convenience alias for BuildCommon used by callers that add
// language-neutral content (e.g. /add_doc, /reindex).
func (m *MultiIndex) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return m.BuildCommon(ctx, chunks)
}

// Save persists the sub-index.
func (m *MultiIndex) Save() error { return m.common.Save() }

// SaveCommon writes the sub-index to disk.
func (m *MultiIndex) SaveCommon() error { return m.common.Save() }

// Search returns the top-k chunks matching query from the sub-index.
func (m *MultiIndex) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return m.common.Search(ctx, query, topK)
}

// Size returns the number of indexed entries.
func (m *MultiIndex) Size() int { return m.common.Size() }

// Updated returns the most recent update timestamp.
func (m *MultiIndex) Updated() time.Time { return m.common.Updated() }

// LoadGodoc loads the godoc sub-index from disk.
func (m *MultiIndex) LoadGodoc() error { return m.godoc.Load() }

// BuildGodoc embeds Go documentation chunks into the godoc sub-index.
func (m *MultiIndex) BuildGodoc(ctx context.Context, chunks []Chunk) (int, error) {
	return m.godoc.Build(ctx, chunks)
}

// SaveGodoc persists the godoc sub-index to disk.
func (m *MultiIndex) SaveGodoc() error { return m.godoc.Save() }

// SearchGodoc returns the top-k chunks matching query from the godoc sub-index.
func (m *MultiIndex) SearchGodoc(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return m.godoc.Search(ctx, query, topK)
}

// ResetGodoc wipes the godoc sub-index both in memory and on disk.
func (m *MultiIndex) ResetGodoc() error { return m.godoc.Reset() }

// GodocSize returns the number of entries in the godoc sub-index.
func (m *MultiIndex) GodocSize() int { return m.godoc.Size() }

// BuildWeb embeds chunks into the in-memory web sub-index.
func (m *MultiIndex) BuildWeb(ctx context.Context, chunks []Chunk) (int, error) {
	return m.web.Build(ctx, chunks)
}

// SearchWeb returns the top-k chunks matching query from the web sub-index.
func (m *MultiIndex) SearchWeb(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return m.web.Search(ctx, query, topK)
}

// WebSize returns the number of entries in the web sub-index.
func (m *MultiIndex) WebSize() int { return m.web.Size() }

// GodocIndexer returns an adapter that satisfies the tools.GoDocIndexer
// interface by delegating to the godoc sub-index.
func (m *MultiIndex) GodocIndexer() *godocIndexerAdapter {
	return &godocIndexerAdapter{m: m}
}

// godocIndexerAdapter adapts MultiIndex to the GoDocIndexer interface
// (Build + Save) by routing to the godoc sub-index methods.
type godocIndexerAdapter struct {
	m *MultiIndex
}

func (a *godocIndexerAdapter) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return a.m.BuildGodoc(ctx, chunks)
}

func (a *godocIndexerAdapter) Save() error {
	return a.m.SaveGodoc()
}

func (a *godocIndexerAdapter) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return a.m.SearchGodoc(ctx, query, topK)
}

// WebIndexer returns an adapter for the in-memory web sub-index.
func (m *MultiIndex) WebIndexer() *webIndexerAdapter {
	return &webIndexerAdapter{m: m}
}

// webIndexerAdapter adapts MultiIndex to the tools.WebIndexer interface.
type webIndexerAdapter struct {
	m *MultiIndex
}

func (a *webIndexerAdapter) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return a.m.BuildWeb(ctx, chunks)
}

func (a *webIndexerAdapter) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return a.m.SearchWeb(ctx, query, topK)
}
