package tools

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestResultCache_HitMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	reg.WithCache(NewResultCache())
	reg.Register(NewReadFileTool(dir, nil, 100))

	ctx := context.Background()

	// First call: miss + store.
	r1, err := reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})
	if err != nil || !r1.Success {
		t.Fatalf("first call failed: %v %+v", err, r1)
	}
	if got := reg.Cache().Misses(); got != 1 {
		t.Fatalf("misses=%d, want 1", got)
	}

	// Second call: hit.
	r2, _ := reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})
	if r2.Output != r1.Output {
		t.Fatalf("hit returned different output")
	}
	if got := reg.Cache().Hits(); got != 1 {
		t.Fatalf("hits=%d, want 1", got)
	}
}

func TestResultCache_MtimeInvalidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	reg.WithCache(NewResultCache())
	reg.Register(NewReadFileTool(dir, nil, 100))

	ctx := context.Background()
	_, _ = reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})

	// Mutate the file with a noticeably newer mtime.
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	r2, _ := reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})
	if !contains(r2.Output, "v2") {
		t.Fatalf("expected fresh content, got %q", r2.Output)
	}
	if reg.Cache().Misses() < 2 {
		t.Fatalf("expected at least 2 misses after mtime change, got %d", reg.Cache().Misses())
	}
}

func TestResultCache_WriteInvalidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := NewRegistry()
	reg.WithCache(NewResultCache())
	reg.Register(NewReadFileTool(dir, nil, 100))
	reg.Register(NewWriteFileTool(dir, nil))

	ctx := context.Background()
	_, _ = reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})
	if reg.Cache().Stores() != 1 {
		t.Fatalf("expected 1 store, got %d", reg.Cache().Stores())
	}

	// Write triggers invalidation.
	_, _ = reg.Execute(ctx, "write_file", map[string]any{"path": "a.txt", "content": "v2\n"})
	if reg.Cache().Invalidations() == 0 {
		t.Fatalf("expected invalidation after write_file, got 0")
	}

	// Subsequent read should miss again and return fresh content.
	r3, _ := reg.Execute(ctx, "read_file", map[string]any{"path": "a.txt"})
	if !contains(r3.Output, "v2") {
		t.Fatalf("expected fresh content after invalidation, got %q", r3.Output)
	}
}

func TestResultCache_FailureNotCached(t *testing.T) {
	dir := t.TempDir()
	reg := NewRegistry()
	reg.WithCache(NewResultCache())
	reg.Register(NewReadFileTool(dir, nil, 100))

	ctx := context.Background()
	r, _ := reg.Execute(ctx, "read_file", map[string]any{"path": "missing.txt"})
	if r.Success {
		t.Fatal("expected failure for missing file")
	}
	if reg.Cache().Stores() != 0 {
		t.Fatal("failures should not be stored in cache")
	}
}

func TestResultCache_ConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		_ = os.WriteFile(filepath.Join(dir, "f.txt"), []byte("data"), 0o644)
	}

	reg := NewRegistry()
	reg.WithCache(NewResultCache())
	reg.Register(NewReadFileTool(dir, nil, 100))

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = reg.Execute(ctx, "read_file", map[string]any{"path": "f.txt"})
		}()
	}
	wg.Wait()

	hits := reg.Cache().Hits()
	misses := reg.Cache().Misses()
	if hits+misses != 50 {
		t.Fatalf("expected 50 lookups, got hits=%d misses=%d", hits, misses)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
