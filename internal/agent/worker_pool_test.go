/**
 * This file is part of the raoptimus/kodrun library
 *
 * @copyright Copyright (c) Evgeniy Urvantsev
 * @license https://github.com/raoptimus/kodrun/blob/master/LICENSE
 * @link https://github.com/raoptimus/kodrun
 */

package agent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/raoptimus/kodrun/internal/tools"
)

func TestWorkerPool_Execute_Parallel(t *testing.T) {
	pool := NewWorkerPool(4)
	tasks := make([]TaskFunc, 4)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			time.Sleep(50 * time.Millisecond)
			return &tools.ToolResult{Output: "ok"}, nil
		}
	}

	start := time.Now()
	results := pool.Execute(context.Background(), tasks)
	elapsed := time.Since(start)

	if elapsed > 150*time.Millisecond {
		t.Errorf("expected parallel execution < 150ms, got %v", elapsed)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Err != nil {
			t.Errorf("task %d: unexpected error: %v", i, r.Err)
		}
	}
}

func TestWorkerPool_Execute_PreservesOrder(t *testing.T) {
	pool := NewWorkerPool(4)
	tasks := make([]TaskFunc, 8)
	for i := range tasks {
		val := fmt.Sprintf("result-%d", i)
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			return &tools.ToolResult{Output: val}, nil
		}
	}

	results := pool.Execute(context.Background(), tasks)
	for i, r := range results {
		expected := fmt.Sprintf("result-%d", i)
		if r.Result.Output != expected {
			t.Errorf("index %d: got %q, want %q", i, r.Result.Output, expected)
		}
		if r.Index != i {
			t.Errorf("index %d: got Index=%d", i, r.Index)
		}
	}
}

func TestWorkerPool_Execute_Sequential(t *testing.T) {
	pool := NewWorkerPool(1)
	var maxConcurrent atomic.Int32
	var current atomic.Int32

	tasks := make([]TaskFunc, 4)
	for i := range tasks {
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			c := current.Add(1)
			for {
				old := maxConcurrent.Load()
				if c <= old || maxConcurrent.CompareAndSwap(old, c) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			current.Add(-1)
			return &tools.ToolResult{Output: "ok"}, nil
		}
	}

	results := pool.Execute(context.Background(), tasks)
	if maxConcurrent.Load() != 1 {
		t.Errorf("expected max concurrency 1, got %d", maxConcurrent.Load())
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
}

func TestWorkerPool_Execute_ContextCancel(t *testing.T) {
	pool := NewWorkerPool(2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	tasks := make([]TaskFunc, 4)
	// First two tasks will acquire the semaphore and block long enough
	// for the context to expire before tasks 2-3 can start.
	for i := range 2 {
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			time.Sleep(100 * time.Millisecond)
			return &tools.ToolResult{Output: "slow"}, nil
		}
	}
	for i := 2; i < 4; i++ {
		tasks[i] = func(ctx context.Context) (*tools.ToolResult, error) {
			return &tools.ToolResult{Output: "should-not-run"}, nil
		}
	}

	results := pool.Execute(ctx, tasks)
	// Tasks 2-3 should have context error since semaphore was full when ctx expired.
	canceledCount := 0
	for _, r := range results[2:] {
		if r.Err != nil {
			canceledCount++
		}
	}
	if canceledCount == 0 {
		t.Error("expected at least one canceled task")
	}
}

func TestWorkerPool_Execute_Error(t *testing.T) {
	pool := NewWorkerPool(4)
	tasks := []TaskFunc{
		func(ctx context.Context) (*tools.ToolResult, error) {
			return &tools.ToolResult{Output: "ok"}, nil
		},
		func(ctx context.Context) (*tools.ToolResult, error) {
			return nil, fmt.Errorf("task failed")
		},
		func(ctx context.Context) (*tools.ToolResult, error) {
			return &tools.ToolResult{Output: "ok"}, nil
		},
	}

	results := pool.Execute(context.Background(), tasks)
	if results[0].Err != nil {
		t.Errorf("task 0: unexpected error: %v", results[0].Err)
	}
	if results[1].Err == nil {
		t.Error("task 1: expected error")
	}
	if results[2].Err != nil {
		t.Errorf("task 2: unexpected error: %v", results[2].Err)
	}
}

func TestWorkerPool_Execute_Empty(t *testing.T) {
	pool := NewWorkerPool(4)
	results := pool.Execute(context.Background(), nil)
	if results != nil {
		t.Errorf("expected nil for empty tasks, got %v", results)
	}
}
