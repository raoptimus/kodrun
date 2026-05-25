/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/llm"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/tools"
)

// NewOrchestrator builds an *agent.Orchestrator wired to the configured
// thinking, executor, and extractor providers. The chat client/model are
// reused as the primary "thinking" role unless the config specifies a
// dedicated thinking provider.
func NewOrchestrator(
	client llm.Client,
	chatProv *config.ProviderConfig,
	reg *tools.Registry,
	cfg *config.Config,
	emit agent.EventHandler,
	confirmFn agent.ConfirmFunc,
	planConfirmFn agent.PlanConfirmFunc,
	stepConfirmFn agent.StepConfirmFunc,
	ruleCatalog string,
	ragIndex tools.RAGSearcher,
	godocIndexer tools.GoDocIndexer,
	langState *projectlang.State,
	rulesLoader *rules.Loader,
	f *Flags,
) *agent.Orchestrator {
	thinkProv := cfg.ThinkingProvider()
	execProv := cfg.ExecutorProvider()

	primaryClient := client
	primaryModel := chatProv.Model
	primaryCtx := chatProv.ContextSize
	if cfg.Agent.ThinkingProvider != "" && cfg.Agent.ThinkingProvider != cfg.Agent.Provider {
		primaryClient = NewLLMClient(&thinkProv)
		primaryModel = thinkProv.Model
		primaryCtx = thinkProv.ContextSize
	}

	var (
		execClient  llm.Client
		execModel   string
		execCtxSize int
	)
	if cfg.Agent.ExecutorProvider != "" {
		execClient = NewLLMClient(&execProv)
		execModel = execProv.Model
		execCtxSize = execProv.ContextSize
	}

	extractorProv := cfg.ExtractorProvider()
	extractorClient := NewLLMClient(&extractorProv)
	if cfg.Agent.ExtractorProvider == "" {
		extractorClient = client
	}

	return agent.NewOrchestrator(primaryClient, primaryModel, reg, f.WorkDir, primaryCtx, &agent.OrchestratorConfig{
		EventHandler:         emit,
		ConfirmFunc:          confirmFn,
		PlanConfirm:          planConfirmFn,
		StepConfirmFn:        stepConfirmFn,
		Language:             cfg.Agent.Language,
		RuleCatalog:          ruleCatalog,
		Review:               cfg.Agent.Review,
		HasSnippets:          cfg.Snippets.UseTool && !cfg.RAG.Enabled,
		HasRAG:               cfg.RAG.Enabled,
		PrefetchCode:         cfg.Agent.PrefetchCode,
		RAGIndex:             ragIndex,
		GodocIndexer:         godocIndexer,
		LangState:            langState,
		RulesLoader:          rulesLoader,
		ExecutorClient:       execClient,
		ExecutorModel:        execModel,
		ExecutorContextSize:  execCtxSize,
		ExtractorClient:      extractorClient,
		ExtractorModel:       extractorProv.Model,
		ExtractorContextSize: extractorProv.ContextSize,
		ExtractorTemperature: extractorProv.Temperature,
		ExtractorFormat:      extractorProv.Format,
		MaxParallelTasks:     cfg.Agent.MaxParallelTasks,
		MaxReplans:           cfg.Agent.MaxReplans,
		MaxIterations:        cfg.Agent.MaxIterations,
		SpecialistTimeout:    cfg.Agent.SpecialistTimeout,
		AutoCommit:           cfg.Agent.AutoCommit,
		Think:                cfg.Agent.Think,
		Verbose:              f.Verbose,
	})
}
