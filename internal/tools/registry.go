package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/raoptimus/kodrun/internal/ollama"
)

// Tool is the interface for all agent tools.
type Tool interface {
	Name() string
	Description() string
	Schema() ollama.JSONSchema
	Execute(ctx context.Context, params map[string]any) (ToolResult, error)
}

// ToolResult represents the outcome of a tool execution.
type ToolResult struct {
	Output  string         `json:"output"`
	Error   string         `json:"error,omitempty"`
	Success bool           `json:"success"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// Registry manages available tools.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool

	// cache is an optional shared result cache. When set, calls to Cacheable
	// tools are served from cache when fresh, and write-tool calls invalidate
	// dependent entries.
	cache *ResultCache

	// invalidatedBy maps a write-tool name to the set of read-tool names whose
	// CachePolicy lists it in Invalidators. Built lazily on Register and used by
	// Execute to know whether a write-tool call should poke the cache.
	invalidatedBy map[string]map[string]struct{}
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools:         make(map[string]Tool),
		invalidatedBy: make(map[string]map[string]struct{}),
	}
}

// WithCache attaches a shared result cache to the registry. Pass nil to
// disable caching. Returns the registry for chaining.
func (r *Registry) WithCache(c *ResultCache) *Registry {
	r.mu.Lock()
	r.cache = c
	r.mu.Unlock()
	return r
}

// Cache returns the attached result cache, or nil if caching is disabled.
func (r *Registry) Cache() *ResultCache {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cache
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Name()] = t
	if c, ok := t.(Cacheable); ok {
		policy := c.CachePolicy()
		for _, inv := range policy.Invalidators {
			set, exists := r.invalidatedBy[inv]
			if !exists {
				set = make(map[string]struct{})
				r.invalidatedBy[inv] = set
			}
			set[t.Name()] = struct{}{}
		}
	}
}

// Get returns a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Execute runs a tool by name with the given parameters.
//
// When a result cache is attached, Execute serves cacheable read-tool results
// from the cache when fresh, stores new results, and invalidates dependent
// entries on successful write-tool calls.
func (r *Registry) Execute(ctx context.Context, name string, params map[string]any) (ToolResult, error) {
	r.mu.RLock()
	t, ok := r.tools[name]
	cache := r.cache
	invalidates := r.invalidatedBy[name]
	r.mu.RUnlock()
	if !ok {
		return ToolResult{
			Error:   fmt.Sprintf("unknown tool: %s", name),
			Success: false,
		}, nil
	}

	// Cache lookup for cacheable tools.
	var (
		cacheKey      string
		resolvedPaths []string
		policy        CachePolicy
		cacheable     bool
	)
	if cache != nil {
		if c, ok := t.(Cacheable); ok {
			policy = c.CachePolicy()
			if policy.Cacheable {
				cacheable = true
				cacheKey = buildCacheKey(name, policy, params)
				resolvedPaths = resolveParamPaths(t, policy, params)
				if hit, found := cache.Get(cacheKey); found {
					hit.Meta = cloneMetaWithCacheHit(hit.Meta)
					return hit, nil
				}
			}
		}
	}

	result, err := t.Execute(ctx, params)

	// Store on success.
	if cache != nil && cacheable && err == nil && result.Success {
		cache.Put(cacheKey, result, resolvedPaths)
	}

	// Write-tool invalidation: if this tool is referenced as an invalidator by
	// any cached read-tool, drop entries depending on the affected paths.
	if cache != nil && len(invalidates) > 0 && err == nil && result.Success {
		for _, p := range writeToolPaths(t, params) {
			cache.InvalidatePath(p)
		}
	}

	return result, err
}

// buildCacheKey produces a deterministic key for a tool call. Tools may
// override key construction via CachePolicy.KeyFunc.
func buildCacheKey(name string, policy CachePolicy, params map[string]any) string {
	if policy.KeyFunc != nil {
		return name + ":" + policy.KeyFunc(params)
	}
	// Fallback: sort param keys and concatenate.
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys)*2+1)
	parts = append(parts, name)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, params[k]))
	}
	return strings.Join(parts, "|")
}

// resolveParamPaths returns absolute filesystem paths for all PathParams of a
// cacheable tool. Tools that need custom resolution can implement
// PathResolver.
func resolveParamPaths(t Tool, policy CachePolicy, params map[string]any) []string {
	if pr, ok := t.(PathResolver); ok {
		return pr.ResolvePaths(params)
	}
	// Best-effort: extract string params named in PathParams. Without a
	// workdir resolver here, we trust that tools storing in CachePolicy provide
	// either a custom resolver or absolute params.
	out := make([]string, 0, len(policy.PathParams))
	for _, k := range policy.PathParams {
		if s, ok := params[k].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// writeToolPaths returns the absolute filesystem paths a write-tool will
// affect. Write tools should implement PathResolver to participate in
// invalidation; otherwise this falls back to common params.
func writeToolPaths(t Tool, params map[string]any) []string {
	if pr, ok := t.(PathResolver); ok {
		return pr.ResolvePaths(params)
	}
	if s, ok := params["path"].(string); ok && s != "" {
		return []string{s}
	}
	return nil
}

// cloneMetaWithCacheHit returns a shallow copy of meta with "cache_hit" set
// to true. A new map is always returned to avoid mutating the cached entry.
func cloneMetaWithCacheHit(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out["cache_hit"] = true
	return out
}

// PathResolver is implemented by tools whose params reference filesystem
// paths. The cache uses it to compute mtime keys (for cacheable read-tools)
// and invalidation keys (for write-tools).
//
// Implementations should return absolute paths after applying any workdir
// resolution they perform internally.
type PathResolver interface {
	ResolvePaths(params map[string]any) []string
}

// ToolDefs returns Ollama tool definitions for all registered tools.
func (r *Registry) ToolDefs() []ollama.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ollama.ToolDef, 0, len(r.tools))
	for _, t := range r.tools {
		defs = append(defs, ollama.ToolDef{
			Type: "function",
			Function: ollama.ToolFuncDef{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.Schema(),
			},
		})
	}
	return defs
}

// Names returns all registered tool names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ToolDefsFiltered returns tool definitions only for tools in the allowed set.
func (r *Registry) ToolDefsFiltered(allowed map[string]bool) []ollama.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defs := make([]ollama.ToolDef, 0, len(allowed))
	for _, t := range r.tools {
		if allowed[t.Name()] {
			defs = append(defs, ollama.ToolDef{
				Type: "function",
				Function: ollama.ToolFuncDef{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Schema(),
				},
			})
		}
	}
	return defs
}

// NamesFiltered returns tool names that are in the allowed set.
func (r *Registry) NamesFiltered(allowed map[string]bool) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(allowed))
	for name := range r.tools {
		if allowed[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
