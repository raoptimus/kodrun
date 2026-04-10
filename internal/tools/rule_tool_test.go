package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuleTool_Execute_ReturnsRuleContent_Successfully(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".kodrun", "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "service.md"), []byte("Service rule content"), 0o644))

	loader := rules.NewLoader(dir, 0)
	require.NoError(t, loader.Load(context.Background()))

	tool := NewRuleTool(loader, rules.ScopeAll)

	result, err := tool.Execute(context.Background(), map[string]any{"name": "service"})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "Service rule content")
}

func TestRuleTool_Execute_EmptyName_Failure(t *testing.T) {
	tool := NewRuleTool(nil, rules.ScopeAll)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "name is required")
}

func TestRuleTool_Execute_RuleNotFound_Failure(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, ".kodrun", "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))

	loader := rules.NewLoader(dir, 0)
	require.NoError(t, loader.Load(context.Background()))

	tool := NewRuleTool(loader, rules.ScopeAll)

	_, err := tool.Execute(context.Background(), map[string]any{"name": "nonexistent"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule not found")
}

func TestRuleTool_Name_Successfully(t *testing.T) {
	tool := NewRuleTool(nil, rules.ScopeAll)

	assert.Equal(t, "get_rule", tool.Name())
}

func TestRuleTool_Schema_RequiresName_Successfully(t *testing.T) {
	tool := NewRuleTool(nil, rules.ScopeAll)

	schema := tool.Schema()

	assert.Contains(t, schema.Required, "name")
}
