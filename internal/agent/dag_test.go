package agent

import (
	"sort"
	"sync"
	"testing"
)

func TestBuildStepDAG_RootsAndDependents(t *testing.T) {
	plan := &Plan{
		Steps: []Step{
			{ID: 1, Title: "a"},
			{ID: 2, Title: "b", DependsOn: []int{1}},
			{ID: 3, Title: "c", DependsOn: []int{1}},
			{ID: 4, Title: "d", DependsOn: []int{2, 3}},
		},
	}
	d := buildStepDAG(plan)

	roots := d.readyRoots()
	if len(roots) != 1 || roots[0] != 1 {
		t.Fatalf("readyRoots=%v, want [1]", roots)
	}

	newly := d.markDone(1)
	sort.Ints(newly)
	if len(newly) != 2 || newly[0] != 2 || newly[1] != 3 {
		t.Fatalf("after markDone(1) ready=%v, want [2 3]", newly)
	}

	newly = d.markDone(2)
	if len(newly) != 0 {
		t.Fatalf("after markDone(2) ready=%v, want [] (3 still pending)", newly)
	}
	newly = d.markDone(3)
	if len(newly) != 1 || newly[0] != 4 {
		t.Fatalf("after markDone(3) ready=%v, want [4]", newly)
	}
}

func TestFileLockSet_SerializesSamePath(t *testing.T) {
	locks := newFileLockSet()
	var (
		mu      sync.Mutex
		active  int
		maxSeen int
	)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release := locks.acquire([]string{"a.go"})
			mu.Lock()
			active++
			if active > maxSeen {
				maxSeen = active
			}
			mu.Unlock()
			// Critical section.
			mu.Lock()
			active--
			mu.Unlock()
			release()
		}()
	}
	wg.Wait()
	if maxSeen != 1 {
		t.Errorf("expected serialised access (max=1), got max=%d", maxSeen)
	}
}

func TestFileLockSet_AllowsParallelDifferentPaths(t *testing.T) {
	locks := newFileLockSet()
	r1 := locks.acquire([]string{"a.go"})
	r2 := locks.acquire([]string{"b.go"})
	// If acquire on b.go blocked, this test would deadlock and time out.
	r1()
	r2()
}

func TestParseStructuredPlan_Valid(t *testing.T) {
	raw := `{
		"context": "fix the bug",
		"steps": [
			{"id": 1, "title": "edit a", "files": ["a.go"], "action": "edit"},
			{"id": 2, "title": "edit b", "files": ["b.go"], "depends_on": [1]}
		]
	}`
	plan, err := parseStructuredPlan(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("steps=%d, want 2", len(plan.Steps))
	}
	if plan.Steps[1].DependsOn[0] != 1 {
		t.Errorf("step 2 depends on %v, want [1]", plan.Steps[1].DependsOn)
	}
}

func TestParseStructuredPlan_StripsCodeFences(t *testing.T) {
	raw := "```json\n{\"steps\":[{\"id\":1,\"title\":\"x\",\"files\":[\"a.go\"]}]}\n```"
	plan, err := parseStructuredPlan(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("steps=%d, want 1", len(plan.Steps))
	}
}

func TestParseStructuredPlan_AssignsMissingIDs(t *testing.T) {
	raw := `{"steps":[{"title":"a","files":["a.go"]},{"title":"b","files":["b.go"]}]}`
	plan, err := parseStructuredPlan(raw)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if plan.Steps[0].ID != 1 || plan.Steps[1].ID != 2 {
		t.Errorf("ids=%d,%d want 1,2", plan.Steps[0].ID, plan.Steps[1].ID)
	}
}
