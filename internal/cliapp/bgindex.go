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
	"fmt"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/config"
	"github.com/raoptimus/kodrun/internal/projectlang"
	"github.com/raoptimus/kodrun/internal/rag"
	"github.com/raoptimus/kodrun/internal/rules"
	"github.com/raoptimus/kodrun/internal/snippets"
)

// ChunkConventions builds the convention chunks fed into the RAG common
// sub-index: snippets, rules, reference docs, and built-in language docs
// (e.g. Effective Go). Project source code is intentionally excluded — it
// changes too fast for chunked snapshots to stay current. The function
// short-circuits as soon as ctx is cancelled.
func ChunkConventions(
	ctx context.Context,
	loader *rules.Loader,
	snipLoader *snippets.Loader,
	langState *projectlang.State,
	cfg *config.Config,
) []rag.Chunk {
	snips := snipLoader.Snippets()
	techStack := langState.EnsureTechDetected().Strings()
	var chunks []rag.Chunk
	for i := range snips {
		if ctx.Err() != nil {
			return chunks
		}
		chunks = append(chunks, rag.ChunkSnippets([]rag.SnippetInfo{{
			Name:        snips[i].Name,
			Description: snips[i].Description,
			Tags:        snips[i].Tags,
			Requires:    snips[i].Requires,
			Content:     snips[i].Content,
			SourcePath:  snips[i].SourcePath,
		}}, techStack)...)
	}
	for _, r := range loader.AllRules() {
		if ctx.Err() != nil {
			return chunks
		}
		chunks = append(chunks, rag.ChunkRules([]rag.RuleInfo{{
			Name:    filepath.Base(r.Path),
			Content: r.Content,
			Path:    r.Path,
		}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
	}
	for path, content := range loader.ReferenceDocPaths() {
		if ctx.Err() != nil {
			return chunks
		}
		chunks = append(chunks, rag.ChunkRefDocs([]rag.RefDocInfo{{
			Path:    path,
			Content: content,
		}}, cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
	}
	chunks = append(chunks, rag.ChunkEmbeddedDocs(string(langState.Current()), cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap)...)
	return chunks
}

// MigrateLegacyChunks resets the common index when it still holds chunks
// from a kodrun version that indexed source code. Idempotent: no-op when
// the index is empty or already convention-only.
func MigrateLegacyChunks(ragIndex rag.Backend, emit agent.EventHandler) {
	if ragIndex == nil || !ragIndex.HasLegacyCodeChunks() {
		return
	}
	emit(&agent.Event{Type: agent.EventAgent, Message: "RAG: dropping legacy code chunks from common index"})
	if err := ragIndex.Reset(); err != nil {
		emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG reset failed: %s", err)})
	}
}

// indexConventions runs BuildCommonWithProgress + Save with a progress
// emitter. Caller is responsible for messaging on the returned error.
func indexConventions(
	ctx context.Context,
	ragIndex rag.Backend,
	chunks []rag.Chunk,
	emit agent.EventHandler,
) (int, error) {
	progressFn := func(label string, done, total int) {
		emit(&agent.Event{
			Type:          agent.EventRAGProgress,
			ProgressDone:  done,
			ProgressTotal: total,
			ProgressLabel: label,
		})
	}
	n, err := ragIndex.BuildCommonWithProgress(ctx, chunks, progressFn)
	if err != nil {
		return 0, err
	}
	if err := ragIndex.Save(); err != nil {
		return 0, errors.WithMessage(err, "save")
	}
	return n, nil
}

// RunInitialIndex chunks conventions and builds the common sub-index at
// startup. Silent on a fully-cached run (n == 0) — the user gets no banner
// for an index that did not change.
func RunInitialIndex(
	ctx context.Context,
	ragIndex rag.Backend,
	loader *rules.Loader,
	snipLoader *snippets.Loader,
	langState *projectlang.State,
	cfg *config.Config,
	emit agent.EventHandler,
) {
	if ctx.Err() != nil {
		return
	}
	chunks := ChunkConventions(ctx, loader, snipLoader, langState, cfg)
	n, err := indexConventions(ctx, ragIndex, chunks, emit)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG index (common): %s", err)})
		return
	}
	if ctx.Err() != nil {
		return
	}
	if n > 0 {
		emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG ready: %d new, %d total", n, ragIndex.Size())})
	}
}

// RunReindex rebuilds the common sub-index from scratch on user request:
// reloads rules and snippets from disk, drops legacy per-language dirs,
// resets the index, and rebuilds the convention corpus. Reports both
// success and error via emit; the caller emits EventDone after return.
func RunReindex(
	ctx context.Context,
	ragIndex rag.Backend,
	loader *rules.Loader,
	snipLoader *snippets.Loader,
	langState *projectlang.State,
	cfg *config.Config,
	emit agent.EventHandler,
) {
	if err := loader.Load(ctx); err != nil {
		emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("rules reload: %s", err)})
		return
	}
	if err := snipLoader.Load(ctx); err != nil {
		emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("snippets reload: %s", err)})
		return
	}
	CleanupLegacyLangDirs(ragIndex.BasePath())
	if err := ragIndex.Reset(); err != nil {
		emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("reset: %s", err)})
		return
	}
	chunks := ChunkConventions(ctx, loader, snipLoader, langState, cfg)
	n, err := indexConventions(ctx, ragIndex, chunks, emit)
	if err != nil {
		emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index: %s", err)})
		return
	}
	emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG reindex: %d new, %d total", n, ragIndex.Size())})
}
