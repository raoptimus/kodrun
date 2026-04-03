package rag

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// commonKey is the subdirectory and map key used for the language-neutral
// portion of a MultiIndex (project files, project rules, snippets).
const commonKey = "common"

// MultiIndex composes multiple per-language Index instances plus a shared
// "common" Index for language-neutral chunks. Persistence is split: each
// sub-index lives in its own subdirectory under basePath, so switching the
// active language does not require re-embedding common chunks.
type MultiIndex struct {
	mu         sync.RWMutex
	basePath   string
	client     *ollama.Client
	model      string
	common     *Index
	byLang     map[string]*Index
	activeLang string
}

// NewMultiIndex creates a new MultiIndex rooted at basePath.
// The common sub-index is created eagerly; per-language sub-indices are
// instantiated on demand.
func NewMultiIndex(client *ollama.Client, model, basePath string) *MultiIndex {
	return &MultiIndex{
		basePath: basePath,
		client:   client,
		model:    model,
		common:   NewIndex(client, model, filepath.Join(basePath, commonKey)),
		byLang:   make(map[string]*Index),
	}
}

// SetActiveLanguage sets the language whose sub-index participates in Search.
// Empty string disables per-language search and uses only the common index.
func (m *MultiIndex) SetActiveLanguage(lang string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeLang = lang
}

// ActiveLanguage returns the currently active language tag.
func (m *MultiIndex) ActiveLanguage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeLang
}

// langIndex returns the sub-index for lang, creating it if needed.
func (m *MultiIndex) langIndex(lang string) *Index {
	m.mu.Lock()
	defer m.mu.Unlock()
	if idx, ok := m.byLang[lang]; ok {
		return idx
	}
	idx := NewIndex(m.client, m.model, filepath.Join(m.basePath, lang))
	m.byLang[lang] = idx
	return idx
}

// LoadCommon loads the common sub-index from disk.
func (m *MultiIndex) LoadCommon() error { return m.common.Load() }

// LoadLanguage loads the per-language sub-index from disk, creating an
// empty one if no file exists yet.
func (m *MultiIndex) LoadLanguage(lang string) error {
	if lang == "" {
		return nil
	}
	return m.langIndex(lang).Load()
}

// BuildCommon embeds chunks into the common sub-index.
func (m *MultiIndex) BuildCommon(ctx context.Context, chunks []Chunk) (int, error) {
	return m.common.Build(ctx, chunks)
}

// BuildCommonWithProgress is BuildCommon with a progress callback. See
// Index.BuildWithProgress for the callback contract.
func (m *MultiIndex) BuildCommonWithProgress(ctx context.Context, chunks []Chunk, progress ProgressFunc) (int, error) {
	return m.common.BuildWithProgress(ctx, chunks, progress)
}

// BuildLanguage embeds chunks into the per-language sub-index.
func (m *MultiIndex) BuildLanguage(ctx context.Context, lang string, chunks []Chunk) (int, error) {
	if lang == "" {
		return 0, nil
	}
	return m.langIndex(lang).Build(ctx, chunks)
}

// Build is a convenience alias for BuildCommon used by callers that
// add language-neutral content (e.g. /add_doc, /reindex).
func (m *MultiIndex) Build(ctx context.Context, chunks []Chunk) (int, error) {
	return m.BuildCommon(ctx, chunks)
}

// Save persists every loaded sub-index. Alias for SaveAll.
func (m *MultiIndex) Save() error { return m.SaveAll() }

// SaveCommon writes the common sub-index to disk.
func (m *MultiIndex) SaveCommon() error { return m.common.Save() }

// SaveLanguage writes the per-language sub-index to disk.
func (m *MultiIndex) SaveLanguage(lang string) error {
	if lang == "" {
		return nil
	}
	return m.langIndex(lang).Save()
}

// SaveAll writes the common index and every loaded per-language index.
func (m *MultiIndex) SaveAll() error {
	if err := m.common.Save(); err != nil {
		return err
	}
	m.mu.RLock()
	langs := make([]string, 0, len(m.byLang))
	for l := range m.byLang {
		langs = append(langs, l)
	}
	m.mu.RUnlock()
	for _, l := range langs {
		if err := m.SaveLanguage(l); err != nil {
			return err
		}
	}
	return nil
}

// Search returns the top-k chunks across the common sub-index and the
// active language sub-index combined. Results from both pools compete on
// similarity score and the global top-k is returned.
func (m *MultiIndex) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	common, err := m.common.Search(ctx, query, topK)
	if err != nil {
		return nil, err
	}

	m.mu.RLock()
	lang := m.activeLang
	langIdx := m.byLang[lang]
	m.mu.RUnlock()

	var langResults []SearchResult
	if lang != "" && langIdx != nil {
		langResults, err = langIdx.Search(ctx, query, topK)
		if err != nil {
			return nil, err
		}
	}

	if len(langResults) == 0 {
		return common, nil
	}

	merged := make([]SearchResult, 0, len(common)+len(langResults))
	merged = append(merged, common...)
	merged = append(merged, langResults...)
	sort.Slice(merged, func(i, j int) bool { return merged[i].Score > merged[j].Score })
	if topK > 0 && len(merged) > topK {
		merged = merged[:topK]
	}
	return merged, nil
}

// Size returns the total number of indexed entries across all sub-indices.
func (m *MultiIndex) Size() int {
	total := m.common.Size()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, idx := range m.byLang {
		total += idx.Size()
	}
	return total
}

// Updated returns the most recent update timestamp across sub-indices.
func (m *MultiIndex) Updated() time.Time {
	latest := m.common.Updated()
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, idx := range m.byLang {
		if u := idx.Updated(); u.After(latest) {
			latest = u
		}
	}
	return latest
}
