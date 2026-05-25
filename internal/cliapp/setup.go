/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/mcp"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/ragdb/muninn"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/snippets"
	"github.com/raoptimus/kodrun/internal/tools"
)

// defaultRAGTopK is the fallback Top-K used when the config does not set one.
const defaultRAGTopK = 5

// LoadConfig reads kodrun configuration and applies CLI flag overrides
// (model, ollama URL) on top of the file/env-derived values.
func LoadConfig(ctx context.Context, f *Flags) (config.Config, error) {
	cfg, err := config.Load(ctx, f.Config, f.WorkDir)
	if err != nil {
		return cfg, err
	}
	if f.Model != "" {
		p := cfg.ChatProvider()
		p.Model = f.Model
		cfg.Providers[cfg.Agent.Provider] = p
	}
	if f.OllamaURL != "" {
		p := cfg.ChatProvider()
		p.BaseURL = f.OllamaURL
		cfg.Providers[cfg.Agent.Provider] = p
	}
	return cfg, nil
}

// AgentSetup holds the wired dependencies produced by SetupAgent. Keeping
// every collaborator addressable (loader, ragIndex, mcpMgr, ...) lets the
// caller drive lifecycle hooks (shutdown, telemetry) without re-deriving
// state from the agent object.
type AgentSetup struct {
	Agent         *agent.Agent
	Registry      *tools.Registry
	Loader        *rules.Loader
	SnippetLoader *snippets.Loader
	RAGIndex      rag.Backend
	GodocIndexer  tools.GoDocIndexer
	LangState     *projectlang.State
	MCPManager    *mcp.Manager
	Client        llm.Client
}

// SetupAgent wires every agent dependency from configuration. It is split
// into small builder helpers (build*) so individual concerns can be tested
// or replaced without rewriting the entire wiring flow.
func SetupAgent(ctx context.Context, cfg *config.Config, scope rules.Scope, f *Flags) AgentSetup {
	chatProv := cfg.ChatProvider()
	chatClient := NewLLMClient(&chatProv)

	loader, snippetLoader := buildRulesAndSnippets(ctx, f, cfg)
	langState := buildLangState(cfg, f)
	currentLang, _ := langState.EnsureDetected()

	ragIndex, godocIndexer := buildRAG(cfg, f)

	reg := buildToolRegistry(cfg, f, loader, snippetLoader, scope, currentLang, godocIndexer, langState, ragIndex)

	mcpMgr := buildMCP(ctx, cfg, f, reg)

	ag := agent.New(chatClient, chatProv.Model, reg, cfg.Agent.MaxIterations, f.WorkDir, chatProv.ContextSize)
	ag.SetLanguageState(langState)
	ag.SetGodocIndexer(godocIndexer)
	ag.SetVerbose(f.Verbose)
	return AgentSetup{
		Agent:         ag,
		Registry:      reg,
		Loader:        loader,
		SnippetLoader: snippetLoader,
		RAGIndex:      ragIndex,
		GodocIndexer:  godocIndexer,
		LangState:     langState,
		MCPManager:    mcpMgr,
		Client:        chatClient,
	}
}

// buildRulesAndSnippets loads the rules catalog and snippet library from
// the working directory. Both loaders log warnings on partial failures
// and continue with whatever they could parse.
func buildRulesAndSnippets(ctx context.Context, f *Flags, cfg *config.Config) (rulesLoader *rules.Loader, snippetLoader *snippets.Loader) {
	rulesLoader = rules.NewLoader(f.WorkDir, cfg.Rules.MaxRefSize)
	if err := rulesLoader.Load(ctx); err != nil {
		slog.Warn("rules load failed", "error", err)
	}
	for _, ref := range rulesLoader.UnresolvedRefs() {
		slog.Warn("rule references missing file",
			"rule", ref.RulePath,
			"ref", "@"+ref.RefPath,
		)
	}
	snippetLoader = snippets.NewLoader(f.WorkDir)
	if err := snippetLoader.Load(ctx); err != nil {
		slog.Warn("snippets load failed", "error", err)
	}
	return rulesLoader, snippetLoader
}

// buildLangState detects the project's primary programming language. A
// configured override wins over marker-file detection; an empty result
// means "unknown" and language tools attach lazily on first Run.
func buildLangState(cfg *config.Config, f *Flags) *projectlang.State {
	langState := projectlang.NewState(projectlang.New(f.WorkDir), projectlang.Language(cfg.Agent.ProjectLanguage))
	langState.SetTechDetector(projectlang.NewTechDetector(f.WorkDir))
	return langState
}

// buildRAG constructs the RAG backend (local vector store or Muninn) and
// returns nil values when RAG is disabled in config. The godoc indexer
// shares the same backend so go_doc can hit the embeddings index.
func buildRAG(cfg *config.Config, f *Flags) (rag.Backend, tools.GoDocIndexer) {
	if !cfg.RAG.Enabled {
		return nil, nil
	}
	var ragIndex rag.Backend
	switch cfg.RAG.Backend {
	case config.RAGBackendMuninn:
		indexPath := cfg.RAG.IndexPath
		if !filepath.IsAbs(indexPath) {
			indexPath = filepath.Join(f.WorkDir, indexPath)
		}
		ragIndex = muninn.NewBackend(&muninn.Options{
			URL:      cfg.RAG.Muninn.URL,
			Vault:    cfg.RAG.Muninn.Vault,
			APIKey:   cfg.RAG.Muninn.APIKey,
			StateDir: indexPath,
		})
	default: // "local"
		indexPath := cfg.RAG.IndexPath
		if !filepath.IsAbs(indexPath) {
			indexPath = filepath.Join(f.WorkDir, indexPath)
		}
		ragProv := cfg.RAGProvider()
		ragClient := NewLLMClient(&ragProv)
		ragIndex = rag.NewMultiIndex(ragClient, ragProv.Model, indexPath)
		// Legacy per-language sub-indexes (go/python/jsts) are no longer
		// used — RAG indexes only project conventions, which are not
		// partitioned by language. Remove any leftover directories from
		// earlier kodrun versions so stale code chunks cannot survive.
		CleanupLegacyLangDirs(indexPath)
	}
	if err := ragIndex.LoadCommon(); err != nil {
		slog.Warn("RAG common index load failed", "error", err)
	}
	if err := ragIndex.LoadGodoc(); err != nil {
		slog.Warn("RAG godoc index load failed", "error", err)
	}
	return ragIndex, &rag.BackendGodocAdapter{B: ragIndex}
}

// buildToolRegistry creates the tools registry, registers all built-in
// tools, RAG search (when enabled) and web_fetch. The returned registry
// is ready for MCP tool merging.
func buildToolRegistry(
	cfg *config.Config,
	f *Flags,
	loader *rules.Loader,
	snippetLoader *snippets.Loader,
	scope rules.Scope,
	currentLang projectlang.Language,
	godocIndexer tools.GoDocIndexer,
	langState *projectlang.State,
	ragIndex rag.Backend,
) *tools.Registry {
	reg := tools.NewRegistry()
	tools.Register(reg, &tools.RegisterConfig{
		WorkDir:        f.WorkDir,
		Forbidden:      cfg.Tools.ForbiddenPatterns,
		MaxReadLines:   cfg.Tools.MaxReadLines,
		Loader:         loader,
		SnippetLoader:  snippetLoader,
		Scope:          scope,
		UseRuleTool:    cfg.Rules.UseTool,
		UseSnippetTool: cfg.Snippets.UseTool,
		RAGEnabled:     cfg.RAG.Enabled,
		Lang:           currentLang,
		Indexer:        godocIndexer,
		LangState:      langState,
	})

	if ragIndex != nil {
		reg.Register(tools.NewRAGSearchTool(ragIndex, cfg.RAG.TopK))
	}

	var webIndexer tools.WebIndexer
	if ragIndex != nil {
		webIndexer = &rag.BackendWebAdapter{B: ragIndex}
	}
	topK := cfg.RAG.TopK
	if topK <= 0 {
		topK = defaultRAGTopK
	}
	reg.Register(tools.NewWebFetchTool(webIndexer, topK))
	return reg
}

// buildMCP starts every configured MCP server and merges their tools into
// the registry. Returns nil when no MCP server is configured. Failures
// are logged (warn/error) but never abort startup so the agent stays
// usable with the built-in toolset.
func buildMCP(ctx context.Context, cfg *config.Config, f *Flags, reg *tools.Registry) *mcp.Manager {
	if len(cfg.MCP) == 0 {
		return nil
	}
	mgr := mcp.NewManager()
	mcpConfigs := make(map[string]mcp.ServerConfig, len(cfg.MCP))
	for name, sc := range cfg.MCP {
		mcpConfigs[name] = mcp.ServerConfig{
			Command:          sc.Command,
			Args:             sc.Args,
			Env:              sc.Env,
			AutoApprove:      sc.AutoApprove,
			AutoApproveTools: sc.AutoApproveTools,
			ReadOnlyTools:    sc.ReadOnlyTools,
			Disabled:         sc.Disabled,
		}
	}
	if err := mgr.Start(ctx, mcpConfigs, f.WorkDir); err != nil {
		slog.Error("MCP start failed", "error", err)
	}
	for _, e := range mgr.Errors() {
		slog.Warn("MCP server warning", "error", e)
	}
	mgr.RegisterTools(reg)
	return mgr
}

// ConfigureSlog sets the default slog handler so that --verbose flips
// the log level from Info to Debug. We log to stderr so structured
// agent events on stdout are not interleaved with diagnostic noise.
func ConfigureSlog(verbose bool) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

// NewLLMClient constructs an llm.Client for the given provider. A
// construction failure is fatal — without an LLM there is nothing the
// agent can do — so we exit immediately with a logged error.
func NewLLMClient(prov *config.ProviderConfig) llm.Client {
	c, err := llm.NewClient(llm.ProviderConfig{
		Type:    prov.Type,
		APIKey:  prov.APIKey,
		BaseURL: prov.BaseURL,
		Timeout: prov.Timeout,
	})
	if err != nil {
		slog.Error("failed to create LLM client", "type", prov.Type, "error", err)
		os.Exit(1)
	}
	return c
}
