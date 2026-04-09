package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/pkg/errors"
)

// stepDAG is the dependency graph built from a structured Plan. It supports
// streaming "ready" steps as their dependencies complete and tracks remaining
// work for the parallel runner.
type stepDAG struct {
	steps     map[int]*Step
	indegree  map[int]int     // step id -> number of unmet dependencies
	dependents map[int][]int  // step id -> ids that depend on it
}

func buildStepDAG(plan *Plan) *stepDAG {
	d := &stepDAG{
		steps:      make(map[int]*Step, len(plan.Steps)),
		indegree:   make(map[int]int, len(plan.Steps)),
		dependents: make(map[int][]int),
	}
	for i := range plan.Steps {
		s := &plan.Steps[i]
		d.steps[s.ID] = s
		d.indegree[s.ID] = 0
	}
	for _, s := range plan.Steps {
		for _, dep := range s.DependsOn {
			if _, ok := d.steps[dep]; !ok {
				continue // unknown dep, ignore
			}
			d.indegree[s.ID]++
			d.dependents[dep] = append(d.dependents[dep], s.ID)
		}
	}
	return d
}

// readyRoots returns all step ids with no remaining dependencies. Sorted by
// id for deterministic execution order.
func (d *stepDAG) readyRoots() []int {
	out := make([]int, 0)
	for id, deg := range d.indegree {
		if deg == 0 {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// markDone decrements the indegree of every dependent of `id` and returns the
// ids that just became ready.
func (d *stepDAG) markDone(id int) []int {
	var newlyReady []int
	for _, child := range d.dependents[id] {
		d.indegree[child]--
		if d.indegree[child] == 0 {
			newlyReady = append(newlyReady, child)
		}
	}
	sort.Ints(newlyReady)
	return newlyReady
}

// hasRemaining returns true if any step still needs to run.
func (d *stepDAG) hasRemaining(done map[int]bool) bool {
	for id := range d.steps {
		if !done[id] {
			return true
		}
	}
	return false
}

// fileLockSet provides per-path mutual exclusion so that two parallel steps
// touching the same file are serialised even when MaxParallelTasks > 1.
//
// Locks are always acquired in sorted path order to avoid deadlock between
// pairs of steps with overlapping file sets.
type fileLockSet struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newFileLockSet() *fileLockSet {
	return &fileLockSet{locks: make(map[string]*sync.Mutex)}
}

func (s *fileLockSet) lockFor(path string) *sync.Mutex {
	abs := filepath.Clean(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.locks[abs]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.locks[abs] = m
	return m
}

// acquire takes locks on every path in the slice in deterministic sorted
// order, returning a release function the caller must defer.
func (s *fileLockSet) acquire(paths []string) func() {
	if len(paths) == 0 {
		return func() {}
	}
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	mutexes := make([]*sync.Mutex, 0, len(sorted))
	for _, p := range sorted {
		m := s.lockFor(p)
		m.Lock()
		mutexes = append(mutexes, m)
	}
	return func() {
		for i := len(mutexes) - 1; i >= 0; i-- {
			mutexes[i].Unlock()
		}
	}
}

// runPlanDAG executes a structured Plan using a topological scheduler with
// bounded parallelism (`maxParallel`) and per-file locking. It returns the
// merged SessionStats from all sub-agent runs.
//
// Sub-agents share the orchestrator's tool result cache (Block 2), so
// repeated reads across steps are free.
func (o *Orchestrator) runPlanDAG(ctx context.Context, plan *Plan, maxParallel int, confirmFn ConfirmFunc) (SessionStats, error) {
	if maxParallel < 1 {
		maxParallel = 1
	}
	dag := buildStepDAG(plan)
	locks := newFileLockSet()

	// Pre-compute per-step RAG bundles once. Each bundle is independent of
	// scheduling order, so doing this up-front lets parallel sub-agents share
	// the result instead of issuing duplicate embedding searches.
	o.stepRAGBundles = make(map[int]string, len(plan.Steps))
	for _, s := range plan.Steps {
		o.stepRAGBundles[s.ID] = o.perStepRAG(ctx, s)
	}
	defer func() { o.stepRAGBundles = nil }()

	type stepResult struct {
		id    int
		stats SessionStats
		err   error
	}

	var (
		mu      sync.Mutex
		done    = make(map[int]bool)
		ready   = dag.readyRoots()
		results = make(chan stepResult, len(plan.Steps))
		active  int
		merged  SessionStats
		firstErr error
	)

	// Cancellable child context so a hard error stops in-flight steps.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	startStep := func(id int) {
		active++
		step := *dag.steps[id]
		go func() {
			release := locks.acquire(step.Files)
			defer release()
			o.emit(Event{Type: EventAgent, Message: fmt.Sprintf("▸ Step %d: %s", step.ID, step.Title)})
			stats, err := o.runStep(runCtx, step, confirmFn)
			results <- stepResult{id: id, stats: stats, err: err}
		}()
	}

	// Prime the first batch.
	for _, id := range ready {
		if active >= maxParallel {
			break
		}
		startStep(id)
	}
	pending := ready[min(len(ready), maxParallel):]

	for active > 0 {
		res := <-results
		active--

		mu.Lock()
		mergeStats(&merged, &res.stats)
		mu.Unlock()

		if res.err != nil && firstErr == nil {
			firstErr = res.err
			cancel()
		}

		done[res.id] = true
		newly := dag.markDone(res.id)
		pending = append(pending, newly...)

		// Schedule next.
		for active < maxParallel && len(pending) > 0 && firstErr == nil {
			next := pending[0]
			pending = pending[1:]
			startStep(next)
		}
	}

	if firstErr != nil {
		return merged, errors.WithMessage(firstErr, "step")
	}
	if dag.hasRemaining(done) {
		return merged, errors.New("plan DAG ended with unscheduled steps (likely a cycle)")
	}
	return merged, nil
}

// mergeStats accumulates child SessionStats into a parent. The parent is
// expected to be the orchestrator-level aggregate.
func mergeStats(dst, src *SessionStats) {
	dst.FilesAdded += src.FilesAdded
	dst.FilesModified += src.FilesModified
	dst.FilesDeleted += src.FilesDeleted
	dst.FilesRenamed += src.FilesRenamed
	dst.LinesAdded += src.LinesAdded
	dst.LinesRemoved += src.LinesRemoved
	dst.ToolCalls += src.ToolCalls
	dst.TotalPrompt += src.TotalPrompt
	dst.TotalEval += src.TotalEval
	if src.PeakContextPct > dst.PeakContextPct {
		dst.PeakContextPct = src.PeakContextPct
	}
	dst.ChangedFiles = append(dst.ChangedFiles, src.ChangedFiles...)
}
