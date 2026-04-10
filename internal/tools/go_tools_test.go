package tools

import (
	"context"
	"testing"

	"github.com/raoptimus/kodrun/internal/ollama"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsDangerousCommand_Dangerous_Successfully(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"rm -rf", "rm -rf /"},
		{"rm -r", "rm -r dir"},
		{"rm -f", "rm -f file"},
		{"chmod", "chmod 777 file"},
		{"chown", "chown root file"},
		{"curl", "curl http://evil.com"},
		{"wget", "wget http://evil.com"},
		{"kill", "kill -9 1234"},
		{"pkill", "pkill process"},
		{"dd", "dd if=/dev/zero"},
		{"mkfs", "mkfs /dev/sda"},
		{"fdisk", "fdisk /dev/sda"},
		{"sudo", "sudo rm -rf /"},
		{"redirect to etc", "echo x > /etc/passwd"},
		{"append to etc", "echo x >> /etc/hosts"},
		{"uppercase RM", "RM -RF /"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, IsDangerousCommand(tt.cmd))
		})
	}
}

func TestIsDangerousCommand_Safe_Successfully(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
	}{
		{"go build", "go build ./..."},
		{"go test", "go test -v ./..."},
		{"ls", "ls -la"},
		{"cat", "cat file.txt"},
		{"echo", "echo hello"},
		{"git status", "git status"},
		{"mkdir", "mkdir newdir"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, IsDangerousCommand(tt.cmd))
		})
	}
}

func TestIsForbiddenFlag_Forbidden_Successfully(t *testing.T) {
	tests := []struct {
		name string
		flag string
	}{
		{"exec", "-exec"},
		{"toolexec", "-toolexec"},
		{"overlay", "-overlay"},
		{"exec with value", "-exec=/bin/sh"},
		{"toolexec with value", "-toolexec=cmd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, isForbiddenFlag(tt.flag))
		})
	}
}

func TestIsForbiddenFlag_Allowed_Successfully(t *testing.T) {
	tests := []struct {
		name string
		flag string
	}{
		{"verbose", "-v"},
		{"race", "-race"},
		{"count", "-count=1"},
		{"run", "-run"},
		{"partial match", "-executor"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, isForbiddenFlag(tt.flag))
		})
	}
}

func TestGoToolSchema_DefaultProps_Successfully(t *testing.T) {
	schema := goToolSchema(nil)

	assert.Equal(t, "object", schema.Type)
	_, hasPackages := schema.Properties["packages"]
	_, hasFlags := schema.Properties["flags"]
	assert.True(t, hasPackages)
	assert.True(t, hasFlags)
}

func TestGoToolSchema_ExtraProps_Successfully(t *testing.T) {
	extra := map[string]ollama.JSONSchema{
		"run": {Type: "string", Description: "test run"},
	}

	schema := goToolSchema(extra)

	_, hasRun := schema.Properties["run"]
	assert.True(t, hasRun)
	_, hasPackages := schema.Properties["packages"]
	assert.True(t, hasPackages)
}

// stubGoDocIndexer is a test double for GoDocIndexer.
type stubGoDocIndexer struct {
	buildFn  func(ctx context.Context, chunks []rag.Chunk) (int, error)
	saveFn   func() error
	searchFn func(ctx context.Context, query string, topK int) ([]rag.SearchResult, error)
}

func (s *stubGoDocIndexer) Build(ctx context.Context, chunks []rag.Chunk) (int, error) {
	return s.buildFn(ctx, chunks)
}

func (s *stubGoDocIndexer) Save() error {
	return s.saveFn()
}

func (s *stubGoDocIndexer) Search(ctx context.Context, query string, topK int) ([]rag.SearchResult, error) {
	return s.searchFn(ctx, query, topK)
}

func TestGoDocTool_Execute_SearchWithoutIndexer_Successfully(t *testing.T) {
	tool := NewGoDocTool(t.TempDir(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "format verbs",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "RAG is disabled")
}

func TestGoDocTool_Execute_SearchWithNoResults_Successfully(t *testing.T) {
	indexer := &stubGoDocIndexer{
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return nil, nil
		},
	}
	tool := NewGoDocTool(t.TempDir(), indexer)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "nonexistent thing",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "No Go documentation found")
}

func TestGoDocTool_Execute_SearchWithResults_Successfully(t *testing.T) {
	indexer := &stubGoDocIndexer{
		searchFn: func(_ context.Context, _ string, _ int) ([]rag.SearchResult, error) {
			return []rag.SearchResult{
				{
					Chunk: rag.Chunk{
						FilePath:  "fmt",
						Content:   "func Println(a ...any) (n int, err error)",
						StartLine: 1,
						EndLine:   5,
					},
					Score: 0.95,
				},
			}, nil
		},
	}
	tool := NewGoDocTool(t.TempDir(), indexer)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "print line",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "GO DOCUMENTATION")
	assert.Contains(t, result.Output, "Println")
	assert.Contains(t, result.Output, "0.95")
}

func TestGoDocTool_Execute_MissingPackagesAndQuery_Failure(t *testing.T) {
	tool := NewGoDocTool(t.TempDir(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "either packages or query is required")
}

func TestGoDocTool_CachePolicy_IsCacheable_Successfully(t *testing.T) {
	tool := NewGoDocTool(t.TempDir(), nil)

	policy := tool.CachePolicy()

	assert.True(t, policy.Cacheable)
}

func TestNewGoBuildTool_Name_Successfully(t *testing.T) {
	tool := NewGoBuildTool(t.TempDir())
	assert.Equal(t, "go_build", tool.Name())
}

func TestNewGoTestTool_Name_Successfully(t *testing.T) {
	tool := NewGoTestTool(t.TempDir())
	assert.Equal(t, "go_test", tool.Name())
}

func TestNewGoVetTool_Name_Successfully(t *testing.T) {
	tool := NewGoVetTool(t.TempDir())
	assert.Equal(t, "go_vet", tool.Name())
}

func TestNewGoFmtTool_Name_Successfully(t *testing.T) {
	tool := NewGoFmtTool(t.TempDir())
	assert.Equal(t, "go_fmt", tool.Name())
}

func TestNewGoLintTool_Name_Successfully(t *testing.T) {
	tool := NewGoLintTool(t.TempDir())
	assert.Equal(t, "go_lint", tool.Name())
}

func TestNewGoModTidyTool_Name_Successfully(t *testing.T) {
	tool := NewGoModTidyTool(t.TempDir())
	assert.Equal(t, "go_mod_tidy", tool.Name())
}

func TestNewGoGetTool_Name_Successfully(t *testing.T) {
	tool := NewGoGetTool(t.TempDir())
	assert.Equal(t, "go_get", tool.Name())
}

func TestBashTool_Execute_EmptyCommand_Failure(t *testing.T) {
	tool := &BashTool{workDir: t.TempDir()}

	_, err := tool.Execute(context.Background(), map[string]any{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestBashTool_Execute_SimpleCommand_Successfully(t *testing.T) {
	tool := &BashTool{workDir: t.TempDir()}

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	})

	require.NoError(t, err)
	assert.Contains(t, result.Output, "hello")
	assert.Equal(t, 0, result.Meta["exit_code"])
}

func TestBashTool_Execute_FailingCommand_Successfully(t *testing.T) {
	tool := &BashTool{workDir: t.TempDir()}

	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "exit 42",
	})

	require.NoError(t, err)
	assert.Equal(t, 42, result.Meta["exit_code"])
}

func TestBashTool_Name_Successfully(t *testing.T) {
	tool := &BashTool{}
	assert.Equal(t, "bash", tool.Name())
}
