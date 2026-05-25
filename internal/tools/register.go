/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package tools

import (
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

// RegisterConfig groups everything Register needs. Build it once at startup
// and pass by pointer; this avoids the 13-positional-argument shape that
// used to break every call site whenever a new dependency was added.
type RegisterConfig struct {
	WorkDir        string
	Forbidden      []string
	MaxReadLines   int
	Loader         *rules.Loader
	SnippetLoader  *snippets.Loader
	Scope          rules.Scope
	UseRuleTool    bool
	UseSnippetTool bool
	RAGEnabled     bool
	Lang           projectlang.Language
	Indexer        GoDocIndexer
	LangState      *projectlang.State
}

// Register registers core tools, the language-specific tool set, and the
// optional rule/snippet tools when RAG is disabled.
func Register(reg *Registry, cfg *RegisterConfig) {
	RegisterCoreTools(reg, cfg.WorkDir, cfg.Forbidden, cfg.MaxReadLines, cfg.LangState)
	RegisterLanguageTools(reg, cfg.Lang, cfg.WorkDir, cfg.Indexer)
	if cfg.Loader != nil && cfg.UseRuleTool && !cfg.RAGEnabled {
		reg.Register(NewRuleTool(cfg.Loader, cfg.Scope))
	}
	if cfg.SnippetLoader != nil && cfg.UseSnippetTool && !cfg.RAGEnabled {
		st := NewSnippetTool(cfg.SnippetLoader)
		st.SetTechStack(cfg.LangState.EnsureTechDetected().Strings())
		reg.Register(st)
	}
}
