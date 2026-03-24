package agent

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	"github.com/raoptimus/kodrun/internal/tools"
)

// WorkerPool executes tasks concurrently with a bounded number of goroutines.
type WorkerPool struct {
	sem chan struct{}
}

// NewWorkerPool creates a pool with the given concurrency limit.
// If maxWorkers <= 1, Execute runs tasks sequentially.
func NewWorkerPool(maxWorkers int) *WorkerPool {
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	return &WorkerPool{
		sem: make(chan struct{}, maxWorkers),
	}
}

// TaskFunc is a function that executes a tool call and returns the result.
type TaskFunc func(ctx context.Context) (tools.ToolResult, error)

// TaskResult holds the outcome of a single task, preserving its original index.
type TaskResult struct {
	Index  int
	Result tools.ToolResult
	Err    error
}

// Execute runs all tasks concurrently (up to pool limit) and returns results
// in the same order as the input tasks slice.
func (p *WorkerPool) Execute(ctx context.Context, tasks []TaskFunc) []TaskResult {
	if len(tasks) == 0 {
		return nil
	}

	results := make([]TaskResult, len(tasks))

	// Sequential execution when pool size is 1.
	if cap(p.sem) <= 1 {
		for i, task := range tasks {
			res, err := task(ctx)
			results[i] = TaskResult{Index: i, Result: res, Err: err}
		}
		return results
	}

	var wg sync.WaitGroup

	for i, task := range tasks {
		// Try to acquire semaphore or bail on context cancel.
		select {
		case p.sem <- struct{}{}:
		case <-ctx.Done():
			for j := i; j < len(tasks); j++ {
				results[j] = TaskResult{Index: j, Err: ctx.Err()}
			}
			wg.Wait()
			return results
		}

		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					results[i] = TaskResult{Index: i, Err: errors.Errorf("task panic: %v", r)}
				}
				<-p.sem
				wg.Done()
			}()
			res, err := task(ctx)
			results[i] = TaskResult{Index: i, Result: res, Err: err}
		}()
	}

	wg.Wait()
	return results
}
