/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package cliapp

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/raoptimus/kodrun/internal/agent"
	"github.com/raoptimus/kodrun/internal/rag"
)

// actionReindex rebuilds the RAG common sub-index in the background. The
// in-progress flag is checked and atomically set to prevent overlapping
// reindex jobs.
func actionReindex(d *TaskDeps) {
	switch {
	case d.RAGIndex == nil:
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
		d.Emit(&agent.Event{Type: agent.EventDone})
	case !d.Indexing.CompareAndSwap(false, true):
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "RAG indexing already in progress"})
		d.Emit(&agent.Event{Type: agent.EventDone})
	default:
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Reindexing..."})
		SafeGo(d.BgWg, d.Events, func() {
			defer d.Indexing.Store(false)
			defer func() {
				// Always clear the progress indicator on exit so the
				// status bar disappears even on error paths.
				d.Emit(&agent.Event{Type: agent.EventRAGProgress})
			}()
			RunReindex(d.Ctx, d.RAGIndex, d.RulesLoader, d.SnippetsLoader, d.LangState, d.Cfg, d.Emit)
			d.Emit(&agent.Event{Type: agent.EventDone})
		})
	}
}

// actionRAGStatus reports the current RAG index size, embedding model and
// last-update timestamp.
func actionRAGStatus(d *TaskDeps) {
	if d.RAGIndex == nil {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable with rag.enabled: true"})
	} else {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("RAG: %d entries, model: %s, updated: %s",
			d.RAGIndex.Size(), d.Cfg.RAGProvider().Model, d.RAGIndex.Updated().Format(time.RFC3339))})
	}
	d.Emit(&agent.Event{Type: agent.EventDone})
}

// actionAddDoc chunks a single file and merges it into the RAG index in
// the background. Path can be relative to the workdir.
func actionAddDoc(d *TaskDeps, parts []string) {
	if d.RAGIndex == nil {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "RAG is disabled. Enable it in config with rag.enabled: true"})
		d.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	var docPath string
	if len(parts) > 1 {
		docPath = strings.TrimSpace(parts[1])
	}
	if docPath == "" {
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: "Usage: /add_doc <file_path>"})
		d.Emit(&agent.Event{Type: agent.EventDone})
		return
	}
	if !filepath.IsAbs(docPath) {
		docPath = filepath.Join(d.Flags.WorkDir, docPath)
	}
	d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Adding %s to RAG index...", docPath)})
	SafeGo(d.BgWg, d.Events, func() {
		chunks, err := rag.ChunkFile(docPath, d.Cfg.RAG.ChunkSize, d.Cfg.RAG.ChunkOverlap)
		if err != nil {
			d.Emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("read: %s", err)})
			d.Emit(&agent.Event{Type: agent.EventDone})
			return
		}
		if rel, e := filepath.Rel(d.Flags.WorkDir, docPath); e == nil {
			for i := range chunks {
				chunks[i].FilePath = rel
			}
		}
		n, err := d.RAGIndex.Build(d.Ctx, chunks)
		if err != nil {
			d.Emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("index: %s", err)})
			d.Emit(&agent.Event{Type: agent.EventDone})
			return
		}
		if err := d.RAGIndex.Save(); err != nil {
			d.Emit(&agent.Event{Type: agent.EventError, Message: fmt.Sprintf("save: %s", err)})
			d.Emit(&agent.Event{Type: agent.EventDone})
			return
		}
		d.Emit(&agent.Event{Type: agent.EventAgent, Message: fmt.Sprintf("Added %d chunks from %s (%d total)", n, filepath.Base(docPath), d.RAGIndex.Size())})
		d.Emit(&agent.Event{Type: agent.EventDone})
	})
}
