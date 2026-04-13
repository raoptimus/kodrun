package tools

import (
	"context"

	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/snippets"
)

// RegisterCoreTools registers language-neutral tools: file ops, grep/find,
// git, and bash. These are always available regardless of project language.
func RegisterCoreTools(reg *Registry, workDir string, forbidden []string, maxReadLines int, langState *projectlang.State) {
	reg.Register(NewFileStatTool(workDir, forbidden))
	reg.Register(NewReadFileTool(workDir, forbidden, maxReadLines))
	reg.Register(NewWriteFileTool(workDir, forbidden))
	reg.Register(NewEditFileTool(workDir, forbidden))
	reg.Register(NewListDirTool(workDir, forbidden))
	reg.Register(NewFindFilesTool(workDir, forbidden))
	reg.Register(NewGrepTool(workDir, forbidden))
	reg.Register(NewDeleteFileTool(workDir, forbidden))
	reg.Register(NewCreateDirTool(workDir, forbidden))
	reg.Register(NewMoveFileTool(workDir, forbidden))
	reg.Register(NewReadChangedFilesTool(workDir, langState))
	reg.Register(NewGitStatusTool(workDir))
	reg.Register(NewGitDiffTool(workDir))
	reg.Register(NewGitLogTool(workDir))
	reg.Register(NewGitCommitTool(workDir))
	reg.Register(&BashTool{workDir: workDir})
}

// RegisterLanguageTools registers tools specific to the given language.
// It is safe to call multiple times: tools.Registry.Register is idempotent
// (last write wins on the same name). Unknown languages are a no-op.
// The indexer is optional: when non-nil, Go tools index go doc output into RAG.
func RegisterLanguageTools(reg *Registry, lang projectlang.Language, workDir string, indexer GoDocIndexer) {
	switch lang {
	case projectlang.LangGo:
		RegisterGoTools(reg, workDir, indexer)
	case projectlang.LangPython:
		RegisterPythonTools(reg, workDir)
	case projectlang.LangJSTS:
		RegisterJSTSTools(reg, workDir)
	}
}

// RegisterAllTools is the legacy facade used at startup. It registers core
// tools, the language-specific tool set for the given language, and the
// optional rule/snippet tools when RAG is disabled.
func RegisterAllTools(_ context.Context, reg *Registry, workDir string, forbidden []string,
	maxReadLines int, loader *rules.Loader, snippetLoader *snippets.Loader, scope rules.Scope,
	useRuleTool, useSnippetTool, ragEnabled bool, lang projectlang.Language, indexer GoDocIndexer,
	langState *projectlang.State,
) {
	RegisterCoreTools(reg, workDir, forbidden, maxReadLines, langState)
	RegisterLanguageTools(reg, lang, workDir, indexer)
	if loader != nil && useRuleTool && !ragEnabled {
		reg.Register(NewRuleTool(loader, scope))
	}
	if snippetLoader != nil && useSnippetTool && !ragEnabled {
		st := NewSnippetTool(snippetLoader)
		st.SetTechStack(langState.EnsureTechDetected().Strings())
		reg.Register(st)
	}
}
