/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
	"context"
	"testing"

	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubTool is a minimal Tool implementation for registry tests.
type stubTool struct {
	name   string
	desc   string
	schema llm.JSONSchema
	execFn func(ctx context.Context, params map[string]any) (*ToolResult, error)
}

func (s *stubTool) Name() string           { return s.name }
func (s *stubTool) Description() string    { return s.desc }
func (s *stubTool) Schema() llm.JSONSchema { return s.schema }
func (s *stubTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	if s.execFn != nil {
		return s.execFn(ctx, params)
	}
	return &ToolResult{Output: "ok"}, nil
}

// stubCacheableTool implements both Tool and Cacheable.
type stubCacheableTool struct {
	stubTool
	policy CachePolicy
}

func (s *stubCacheableTool) CachePolicy() CachePolicy { return s.policy }

// stubPathResolver implements Tool and PathResolver.
type stubPathResolverTool struct {
	stubTool
	paths []string
}

func (s *stubPathResolverTool) ResolvePaths(_ map[string]any) []string { return s.paths }

func TestRegistry_Register_Names_ReturnsSortedNames_Successfully(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubTool{name: "zebra"})
	reg.Register(&stubTool{name: "alpha"})
	reg.Register(&stubTool{name: "mid"})

	names := reg.Names()
	require.Len(t, names, 3)
	assert.Equal(t, []string{"alpha", "mid", "zebra"}, names)
}

func TestRegistry_Execute_UnknownTool_Failure(t *testing.T) {
	reg := NewRegistry()

	_, err := reg.Execute(context.Background(), "nonexistent", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool: nonexistent")
}

func TestRegistry_Execute_KnownTool_Successfully(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubTool{
		name: "echo",
		execFn: func(_ context.Context, params map[string]any) (*ToolResult, error) {
			return &ToolResult{Output: "hello"}, nil
		},
	})

	result, err := reg.Execute(context.Background(), "echo", nil)

	require.NoError(t, err)
	assert.Equal(t, "hello", result.Output)
}

func TestRegistry_ToolDefs_ReturnsAllDefs_Successfully(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubTool{name: "a", desc: "desc-a"})
	reg.Register(&stubTool{name: "b", desc: "desc-b"})

	defs := reg.ToolDefs()

	require.Len(t, defs, 2)
	for _, d := range defs {
		assert.Equal(t, "function", d.Type)
	}
}

func TestRegistry_ToolDefsFiltered_ReturnsOnlyAllowed_Successfully(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubTool{name: "a", desc: "desc-a"})
	reg.Register(&stubTool{name: "b", desc: "desc-b"})
	reg.Register(&stubTool{name: "c", desc: "desc-c"})

	defs := reg.ToolDefsFiltered(map[string]bool{"a": true, "c": true})

	require.Len(t, defs, 2)
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Function.Name] = true
	}
	assert.True(t, names["a"])
	assert.True(t, names["c"])
	assert.False(t, names["b"])
}

func TestRegistry_NamesFiltered_ReturnsOnlyAllowed_Successfully(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&stubTool{name: "x"})
	reg.Register(&stubTool{name: "y"})
	reg.Register(&stubTool{name: "z"})

	names := reg.NamesFiltered(map[string]bool{"x": true, "z": true})

	require.Len(t, names, 2)
	assert.Equal(t, []string{"x", "z"}, names)
}

func TestRegistry_WithCache_SetsAndReturnsCachee_Successfully(t *testing.T) {
	reg := NewRegistry()

	assert.Nil(t, reg.Cache())

	cache := NewResultCache()
	reg.WithCache(cache)

	assert.NotNil(t, reg.Cache())
}

func TestBuildCacheKey_DefaultKey_Successfully(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		params   map[string]any
		want     string
	}{
		{
			name:     "no params",
			toolName: "read_file",
			params:   map[string]any{},
			want:     "read_file",
		},
		{
			name:     "single param",
			toolName: "read_file",
			params:   map[string]any{"path": "foo.go"},
			want:     "read_file|path=foo.go",
		},
		{
			name:     "multiple params sorted",
			toolName: "read_file",
			params:   map[string]any{"path": "bar.go", "limit": 10},
			want:     "read_file|limit=10|path=bar.go",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := CachePolicy{}

			got := buildCacheKey(tt.toolName, policy, tt.params)

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildCacheKey_CustomKeyFunc_Successfully(t *testing.T) {
	policy := CachePolicy{
		KeyFunc: func(params map[string]any) string {
			return "custom-key"
		},
	}

	got := buildCacheKey("tool", policy, map[string]any{"a": 1})

	assert.Equal(t, "tool:custom-key", got)
}

func TestResolveParamPaths_WithPathResolver_Successfully(t *testing.T) {
	tool := &stubPathResolverTool{
		stubTool: stubTool{name: "test"},
		paths:    []string{"/abs/path/a.go", "/abs/path/b.go"},
	}
	policy := CachePolicy{PathParams: []string{"path"}}

	got := resolveParamPaths(tool, policy, map[string]any{"path": "a.go"})

	assert.Equal(t, []string{"/abs/path/a.go", "/abs/path/b.go"}, got)
}

func TestResolveParamPaths_WithoutPathResolver_Successfully(t *testing.T) {
	tool := &stubTool{name: "test"}
	policy := CachePolicy{PathParams: []string{"path", "other"}}
	params := map[string]any{"path": "/tmp/a.go", "other": "/tmp/b.go"}

	got := resolveParamPaths(tool, policy, params)

	assert.Equal(t, []string{"/tmp/a.go", "/tmp/b.go"}, got)
}

func TestResolveParamPaths_EmptyParam_Successfully(t *testing.T) {
	tool := &stubTool{name: "test"}
	policy := CachePolicy{PathParams: []string{"path"}}
	params := map[string]any{"path": ""}

	got := resolveParamPaths(tool, policy, params)

	assert.Empty(t, got)
}

func TestWriteToolPaths_WithPathResolver_Successfully(t *testing.T) {
	tool := &stubPathResolverTool{
		stubTool: stubTool{name: "write"},
		paths:    []string{"/resolved/path"},
	}

	got := writeToolPaths(tool, map[string]any{"path": "ignored"})

	assert.Equal(t, []string{"/resolved/path"}, got)
}

func TestWriteToolPaths_FallbackToPathParam_Successfully(t *testing.T) {
	tool := &stubTool{name: "write"}

	got := writeToolPaths(tool, map[string]any{"path": "/tmp/file.go"})

	assert.Equal(t, []string{"/tmp/file.go"}, got)
}

func TestWriteToolPaths_NoPathParam_ReturnsNil_Successfully(t *testing.T) {
	tool := &stubTool{name: "write"}

	got := writeToolPaths(tool, map[string]any{"content": "data"})

	assert.Nil(t, got)
}

func TestCloneMetaWithCacheHit_NilMeta_Successfully(t *testing.T) {
	result := cloneMetaWithCacheHit(nil)

	require.NotNil(t, result)
	assert.Equal(t, true, result["cache_hit"])
	assert.Len(t, result, 1)
}

func TestCloneMetaWithCacheHit_ExistingMeta_Successfully(t *testing.T) {
	original := map[string]any{"key": "value", "count": 42}

	result := cloneMetaWithCacheHit(original)

	assert.Equal(t, true, result["cache_hit"])
	assert.Equal(t, "value", result["key"])
	assert.Equal(t, 42, result["count"])
	// Original is not mutated.
	_, exists := original["cache_hit"]
	assert.False(t, exists)
}
