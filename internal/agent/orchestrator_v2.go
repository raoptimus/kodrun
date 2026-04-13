package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"

	"github.com/raoptimus/kodrun/internal/snippets"
	"github.com/raoptimus/kodrun/internal/tools"
)

const (
	// depSignaturesMaxBytes caps the dependency signatures block per file
	// to avoid blowing the model context window.
	depSignaturesMaxBytes = 32_000 // ~8k tokens

	// reviewRAGTopK is the number of RAG results per file query.
	reviewRAGTopK = 3
)

// RunCodeReview implements the orchestrator-driven code review pipeline.
// Pre-loads all context (file contents, RAG snippets, dependency signatures)
// and sends a simple analysis prompt — no tool-calling required.
func (o *Orchestrator) RunCodeReview(ctx context.Context, files []string, allSnippets []snippets.Snippet) error {
	if len(files) == 0 {
		o.emit(&Event{Type: EventAgent, Message: "No source files to review."})
		o.emit(&Event{Type: EventDone, Message: "Code review completed"})
		return nil
	}

	o.ensureLanguageDetected()

	reviewStart := time.Now()

	cache := tools.NewResultCache()
	o.reg.WithCache(cache)
	defer func() {
		o.reg.WithCache(nil)
		o.emitCacheStats(cache)
	}()

	o.emitPhase("reviewing")

	// Phase 0: Preparation.
	o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("▸ Preparing context for %d files...", len(files))})

	modulePath := readModulePath(o.workDir)
	depStructures := prefetchPackageStructures(ctx, o.reg, o.workDir, files, modulePath)
	ragMap := buildPerFileRAGMap(ctx, o.ragIndex, files, reviewRAGTopK)
	reviewCache := NewReviewCache(o.workDir)

	o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("▸ Prefetched %d package structures, %d RAG blocks", len(depStructures), len(ragMap))})

	// Phase 1: Per-file review (parallel).
	const reviewGroupID = "code-review"
	o.emit(&Event{Type: EventGroupStart, GroupID: reviewGroupID, Message: "CodeReview(per-file)"})

	maxPar := o.maxParallelTasks
	if maxPar < 1 {
		maxPar = 1
	}

	results := make([]reviewResult, len(files))
	sem := make(chan struct{}, maxPar)
	var wg sync.WaitGroup

	reviewCtx, reviewCancel := context.WithCancel(ctx)
	defer reviewCancel()

	for i, f := range files {
		if reviewCtx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, filePath string) {
			defer wg.Done()
			defer func() { <-sem }()

			o.emit(&Event{
				Type:    EventGroupTitleUpdate,
				GroupID: reviewGroupID,
				Message: fmt.Sprintf("Reviewing(%s) [%d/%d]", filePath, i+1, len(files)),
			})

			specStart := time.Now()
			r := o.reviewSingleFile(reviewCtx, filePath, ragMap[filePath], modulePath, depStructures, reviewCache, reviewGroupID)
			r.duration = time.Since(specStart)
			results[i] = r

			if r.err != nil && isConnectionError(r.err) {
				o.emit(&Event{Type: EventError, Message: "Ollama unreachable — aborting remaining reviews."})
				reviewCancel()
			}
		}(i, f)
	}
	wg.Wait()

	// Phase 2: Architecture review (single call).
	o.emit(&Event{Type: EventAgent, Message: "▸ Running architecture review..."})
	archResult := o.reviewArchitecture(ctx, depStructures, allSnippets, reviewGroupID)

	o.emit(&Event{Type: EventGroupEnd, GroupID: reviewGroupID})

	// Timing.
	if o.verbose {
		for i := range results {
			r := &results[i]
			status := perfStatusOK
			if r.err != nil {
				status = perfStatusErr
			} else if strings.TrimSpace(r.text) == "" {
				status = "empty"
			}
			o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
				"[perf] file=%s wall=%s status=%s", files[i], r.duration.Truncate(time.Millisecond), status)})
		}
		o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf("[perf] review_total=%s files=%d", time.Since(reviewStart).Truncate(time.Millisecond), len(files))})
	}

	// Phase 3: Aggregate results.
	allResults := make([]reviewResult, 0, len(results)+1)
	allResults = append(allResults, results...)
	allResults = append(allResults, archResult)

	sections := make([]string, 0, len(allResults))
	var failedCount, skippedCount int
	for i := range allResults {
		r := &allResults[i]
		if r.err != nil {
			failedCount++
			continue
		}
		text := strings.TrimSpace(r.text)
		if text == "" {
			failedCount++
			continue
		}
		if isNoIssues(text) {
			if r.toolCalls == 0 && r.role != RoleCodeReviewer && r.role != RoleArchReviewer {
				skippedCount++
			}
			continue
		}
		label := string(r.role)
		if r.role == RoleCodeReviewer && i < len(files) {
			label = files[i]
		}
		sections = append(sections, fmt.Sprintf("## [%s]\n\n%s", strings.ToUpper(label), text))
	}

	if len(sections) == 0 {
		switch {
		case skippedCount > 0:
			o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
				"⚠ %d reviewer(s) responded without reading files — review is unreliable.", skippedCount)})
		case failedCount > 0:
			o.emit(&Event{Type: EventAgent, Message: fmt.Sprintf(
				"⚠ %d reviewer(s) failed, but no issues found by the rest — treating as LGTM.", failedCount)})
		default:
			o.emit(&Event{Type: EventAgent, Message: "LGTM — no issues found."})
		}
		o.emit(&Event{Type: EventModeChange, Message: "plan"})
		o.emit(&Event{Type: EventDone, Message: "Code review completed"})
		return nil
	}

	o.emit(&Event{Type: EventAgent, Message: "▸ Merging reviewer findings..."})
	finalPlan := mergeSpecialistFindings(allResults)
	if finalPlan == "" {
		o.emit(&Event{Type: EventAgent, Message: "⚠ No strict finding lines parsed — showing raw reports."})
		finalPlan = strings.Join(sections, "\n\n---\n\n")
	}

	o.emit(&Event{Type: EventAgent, Message: "▸ Extracting structured plan..."})
	extracted, err := o.runExtractor(ctx, finalPlan)
	if err != nil {
		o.emit(&Event{Type: EventError, Message: fmt.Sprintf("Extractor failed, using raw merge: %v", err)})
	} else {
		finalPlan = RenderExtractorOutput(extracted)
	}

	return o.confirmAndExecute(ctx, finalPlan, "Code review completed")
}

// reviewSingleFile reviews a single source file with pre-loaded context.
func (o *Orchestrator) reviewSingleFile(
	ctx context.Context,
	filePath, ragBlock, modulePath string,
	depStructures map[string]string,
	cache *ReviewCache,
	groupID string,
) reviewResult {
	// Read the file.
	res, err := o.reg.Execute(ctx, "read_file", map[string]any{"path": filePath})
	if err != nil {
		return reviewResult{role: RoleCodeReviewer, err: fmt.Errorf("read %s: %w", filePath, err)}
	}
	fileContent := res.Output

	// Build dependency signatures.
	depSigs := depSignaturesForFile(o.workDir, filePath, modulePath, depStructures, depSignaturesMaxBytes)

	// Check cache.
	info, statErr := os.Stat(filePath)
	if statErr != nil {
		// Try relative to workDir.
		info, statErr = os.Stat(o.workDir + "/" + filePath)
	}
	var modTime time.Time
	if statErr == nil {
		modTime = info.ModTime()
	}

	cacheKey := ReviewCacheKey(filePath, modTime, ragBlock, depSigs)
	if findings, ok := cache.Get(cacheKey); ok {
		o.emit(&Event{Type: EventTool, Tool: "cache_hit", Message: filePath, GroupID: groupID})
		return reviewResult{role: RoleCodeReviewer, text: findings}
	}

	// Build prompt and run LLM.
	prompt := buildPerFileReviewPrompt(filePath, fileContent, ragBlock, depSigs)

	ag := o.newAgent(RoleCodeReviewer, o.maxRevIter)
	ag.SetTaskLabel("")
	ag.SetGroupID(groupID)
	sysPrompt := systemPromptForRole(RoleCodeReviewer, o.language, o.ruleCatalog, nil, o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(sysPrompt)

	if err := ag.Send(ctx, prompt); err != nil && !errors.Is(err, ErrMaxIterations) {
		return reviewResult{role: RoleCodeReviewer, err: err, stats: ag.Stats()}
	}

	text := ag.LastPlan()

	// Save to cache.
	if err := cache.Put(cacheKey, &ReviewCacheEntry{
		Key:       cacheKey,
		FilePath:  filePath,
		ModTime:   modTime,
		Findings:  text,
		CreatedAt: time.Now(),
	}); err != nil {
		o.emit(&Event{Type: EventError, Message: fmt.Sprintf("review cache write: %v", err)})
	}

	return reviewResult{
		role:      RoleCodeReviewer,
		text:      text,
		stats:     ag.Stats(),
		toolCalls: ag.ToolCallCount(),
	}
}

// reviewArchitecture runs a project-wide architecture review.
func (o *Orchestrator) reviewArchitecture(ctx context.Context, depStructures map[string]string, allSnippets []snippets.Snippet, groupID string) reviewResult {
	// Build project structure from all prefetched packages.
	var structBuf strings.Builder
	for pkg, structure := range depStructures {
		fmt.Fprintf(&structBuf, "### %s\n%s\n", pkg, structure)
	}

	// Collect pinned architecture/overview snippets.
	var archSnippetsBuf strings.Builder
	for i := range allSnippets {
		if hasPinnedTag(allSnippets[i].Tags) {
			fmt.Fprintf(&archSnippetsBuf, "### %s\n%s\n\n", allSnippets[i].Name, allSnippets[i].Content)
		}
	}

	prompt := buildArchReviewPrompt(structBuf.String(), archSnippetsBuf.String())

	ag := o.newAgent(RoleArchReviewer, o.maxRevIter)
	ag.SetTaskLabel("")
	ag.SetGroupID(groupID)
	sysPrompt := systemPromptForRole(RoleArchReviewer, o.language, o.ruleCatalog, nil, o.hasSnippets, o.hasRAG)
	ag.InitWithPrompt(sysPrompt)

	if err := ag.Send(ctx, prompt); err != nil && !errors.Is(err, ErrMaxIterations) {
		return reviewResult{role: RoleArchReviewer, err: err, stats: ag.Stats()}
	}

	return reviewResult{
		role:      RoleArchReviewer,
		text:      ag.LastPlan(),
		stats:     ag.Stats(),
		toolCalls: ag.ToolCallCount(),
	}
}

// hasPinnedTag checks if any of the snippet tags match architecture/overview tags.
func hasPinnedTag(tags []string) bool {
	pinned := map[string]bool{
		"architecture": true,
		"overview":     true,
		"structure":    true,
	}
	for _, t := range tags {
		if pinned[strings.ToLower(t)] {
			return true
		}
	}
	return false
}
