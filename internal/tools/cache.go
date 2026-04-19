/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CachePolicy declares how a tool's results should be cached.
//
// Cacheable tools (typically read-only) implement the Cacheable interface and
// return a non-zero CachePolicy. Write tools may declare themselves as
// invalidators of read-tool entries via the Invalidators field of the readers,
// and the registry uses that index to drop stale entries on write.
type CachePolicy struct {
	// Cacheable is true if results of this tool can be cached.
	Cacheable bool

	// KeyFunc builds a deterministic cache key from the call params. If nil,
	// the registry falls back to a generic JSON-based key.
	KeyFunc func(params map[string]any) string

	// PathParams lists param keys that resolve to filesystem paths. The registry
	// uses these both for mtime-based invalidation (re-stat on hit) and for
	// write-driven invalidation (a write to one of these paths drops the entry).
	PathParams []string

	// Invalidators lists tool names that invalidate this entry when they run.
	// This is the read-tool's view: "if any of these write tools fires for one
	// of my PathParams, drop me".
	Invalidators []string
}

// Cacheable is implemented by tools that opt into result caching.
type Cacheable interface {
	Tool
	CachePolicy() CachePolicy
}

// cacheEntry stores a tool result together with the mtimes of its dependency
// paths so the registry can detect on-disk changes.
type cacheEntry struct {
	result    *ToolResult
	paths     []string
	mtimes    map[string]time.Time
	createdAt time.Time
}

// ResultCache is a thread-safe per-session cache of tool results.
//
// It is safe for concurrent use by multiple sub-agents sharing the same
// orchestrator session. Entries are keyed by tool name plus a policy-supplied
// key. On hit, the registry re-stats the dependency paths to make sure they
// have not changed since the entry was stored.
type ResultCache struct {
	mu       sync.RWMutex
	entries  map[string]cacheEntry
	pathIdx  map[string]map[string]struct{} // path -> set of cache keys depending on it
	hits     atomic.Int64
	misses   atomic.Int64
	stores   atomic.Int64
	invalids atomic.Int64
}

// NewResultCache creates an empty cache.
func NewResultCache() *ResultCache {
	return &ResultCache{
		entries: make(map[string]cacheEntry),
		pathIdx: make(map[string]map[string]struct{}),
	}
}

// Hits returns the total number of cache hits.
func (c *ResultCache) Hits() int64 { return c.hits.Load() }

// Misses returns the total number of cache misses.
func (c *ResultCache) Misses() int64 { return c.misses.Load() }

// Stores returns the total number of entries written to the cache.
func (c *ResultCache) Stores() int64 { return c.stores.Load() }

// Invalidations returns the total number of entries dropped due to writes.
func (c *ResultCache) Invalidations() int64 { return c.invalids.Load() }

// HitRate returns the cache hit ratio in [0, 1]. Returns 0 if there have been
// no lookups yet.
func (c *ResultCache) HitRate() float64 {
	h := c.hits.Load()
	m := c.misses.Load()
	total := h + m
	if total == 0 {
		return 0
	}
	return float64(h) / float64(total)
}

// Get returns a cached result if present and still valid (paths unchanged).
// Returns ok=false on miss or staleness; staleness causes the entry to be
// dropped from the cache.
func (c *ResultCache) Get(key string) (*ToolResult, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	// Re-stat all dependency paths.
	for _, p := range entry.paths {
		want := entry.mtimes[p]
		info, err := os.Stat(p)
		if err != nil || !info.ModTime().Equal(want) {
			// Stale: drop and report miss.
			c.dropKey(key)
			c.misses.Add(1)
			return nil, false
		}
	}

	c.hits.Add(1)
	return entry.result, true
}

// Put stores a result for the given key, recording the mtimes of the paths it
// depends on so future lookups can validate freshness.
//
// Resolved paths are absolute filesystem paths.
func (c *ResultCache) Put(key string, result *ToolResult, resolvedPaths []string) {
	mtimes := make(map[string]time.Time, len(resolvedPaths))
	for _, p := range resolvedPaths {
		info, err := os.Stat(p)
		if err != nil {
			// If we cannot stat a dependency, skip caching: we would not be
			// able to validate freshness later.
			return
		}
		mtimes[p] = info.ModTime()
	}

	c.mu.Lock()
	c.entries[key] = cacheEntry{
		result:    result,
		paths:     append([]string(nil), resolvedPaths...),
		mtimes:    mtimes,
		createdAt: time.Now(),
	}
	for _, p := range resolvedPaths {
		set, ok := c.pathIdx[p]
		if !ok {
			set = make(map[string]struct{})
			c.pathIdx[p] = set
		}
		set[key] = struct{}{}
	}
	c.mu.Unlock()
	c.stores.Add(1)
}

// InvalidatePath drops every cache entry that depends on the given path. It
// also walks parent directories so that operations like delete_dir or move on
// a containing directory invalidate everything below them.
//
// path should be an absolute filesystem path.
func (c *ResultCache) InvalidatePath(path string) {
	if path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Direct match.
	if set, ok := c.pathIdx[path]; ok {
		for k := range set {
			c.dropKeyLocked(k)
		}
	}

	// Prefix match: anything under `path` (treat as directory) is also stale.
	prefix := path
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	for p, set := range c.pathIdx {
		if strings.HasPrefix(p, prefix) {
			for k := range set {
				c.dropKeyLocked(k)
			}
		}
	}
}

// Clear removes every entry from the cache.
func (c *ResultCache) Clear() {
	c.mu.Lock()
	c.entries = make(map[string]cacheEntry)
	c.pathIdx = make(map[string]map[string]struct{})
	c.mu.Unlock()
}

func (c *ResultCache) dropKey(key string) {
	c.mu.Lock()
	c.dropKeyLocked(key)
	c.mu.Unlock()
}

func (c *ResultCache) dropKeyLocked(key string) {
	entry, ok := c.entries[key]
	if !ok {
		return
	}
	delete(c.entries, key)
	for _, p := range entry.paths {
		if set, ok := c.pathIdx[p]; ok {
			delete(set, key)
			if len(set) == 0 {
				delete(c.pathIdx, p)
			}
		}
	}
	c.invalids.Add(1)
}
