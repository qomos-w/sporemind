package agent

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestChildLastActivity_FallbackToSpawnedAt verifies that when no progress
// has been recorded in childProgressAt, childLastActivity falls back to SpawnedAt.
func TestChildLastActivity_FallbackToSpawnedAt(t *testing.T) {
	spawnedAt := time.Now().Add(-30 * time.Second)
	e := &turnEngine{}
	pc := pendingChild{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: spawnedAt}
	got := e.childLastActivity(pc)
	if !got.Equal(spawnedAt) {
		t.Fatalf("childLastActivity = %v, want SpawnedAt %v", got, spawnedAt)
	}
}

// TestChildLastActivity_UsesProgressTimestamp verifies that when a progress
// timestamp exists in childProgressAt, it takes precedence over SpawnedAt.
func TestChildLastActivity_UsesProgressTimestamp(t *testing.T) {
	spawnedAt := time.Now().Add(-5 * time.Minute)
	progressAt := time.Now().Add(-10 * time.Second)
	e := &turnEngine{}
	e.childProgressAt.Store("step-1", progressAt.UnixNano())
	pc := pendingChild{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: spawnedAt}
	got := e.childLastActivity(pc)
	if !got.Equal(progressAt) {
		t.Fatalf("childLastActivity = %v, want progressAt %v", got, progressAt)
	}
}

// TestEarliestChildDeadline_BasedOnLastActivity verifies that the deadline
// is computed from the last progress time, not SpawnedAt.
func TestEarliestChildDeadline_BasedOnLastActivity(t *testing.T) {
	spawnedAt := time.Now().Add(-5 * time.Minute)
	progressAt := time.Now().Add(-10 * time.Second)
	e := &turnEngine{}
	e.pendingChildren = []pendingChild{{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: spawnedAt}}
	e.childProgressAt.Store("step-1", progressAt.UnixNano())
	deadline := e.earliestChildDeadline()
	want := progressAt.Add(domain.ChildAgentIdleTimeout)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}
}

// TestEarliestChildDeadline_MultipleChildrenPicksEarliest verifies that with
// multiple children at different activity levels, the earliest deadline (the
// most idle child) is returned.
func TestEarliestChildDeadline_MultipleChildrenPicksEarliest(t *testing.T) {
	now := time.Now()
	e := &turnEngine{}
	e.pendingChildren = []pendingChild{
		{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: now.Add(-50 * time.Second)},
		{ToolUseID: "tool-2", StepID: "step-2", SpawnedAt: now.Add(-10 * time.Second)},
	}
	e.childProgressAt.Store("step-2", now.Add(-5*time.Second).UnixNano())
	deadline := e.earliestChildDeadline()
	// step-1: no progress → SpawnedAt + idle = (now-50s) + timeout
	// step-2: progress 5s ago → (now-5s) + timeout
	// Earliest = step-1
	want := now.Add(-50 * time.Second).Add(domain.ChildAgentIdleTimeout)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v (earliest = most idle child)", deadline, want)
	}
}

// TestEarliestChildDeadline_NoAbsoluteLifetimeCap verifies that there is no
// absolute lifetime cap: a child spawned long ago but with fresh heartbeats
// has a deadline based on last activity, not SpawnedAt. The old
// ChildAgentMaxLifetime cap is intentionally removed because legitimate
// dispatch iterations can exceed it.
func TestEarliestChildDeadline_NoAbsoluteLifetimeCap(t *testing.T) {
	spawnedAt := time.Now().Add(-30 * time.Minute)  // very old
	progressAt := time.Now().Add(-10 * time.Second) // fresh heartbeat
	e := &turnEngine{}
	e.pendingChildren = []pendingChild{{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: spawnedAt}}
	e.childProgressAt.Store("step-1", progressAt.UnixNano())
	deadline := e.earliestChildDeadline()
	want := progressAt.Add(domain.ChildAgentIdleTimeout)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v (fresh heartbeat must set deadline regardless of SpawnedAt)", deadline, want)
	}
}

// TestHandleChildTimeout_RemovesIdleKeepsActive verifies that handleChildTimeout
// removes only children whose idle period has exceeded ChildAgentIdleTimeout,
// while keeping active children with fresh heartbeats.
func TestHandleChildTimeout_RemovesIdleKeepsActive(t *testing.T) {
	e := &turnEngine{
		logger:      newNopActorLogger(),
		stepByID:    map[string]domain.TurnAction{},
		stepEventMu: sync.Mutex{},
	}
	// Child A: idle — spawned long ago, no heartbeat → should timeout.
	// Child B: active — spawned long ago, but heartbeat 10s ago → survives.
	e.pendingChildren = []pendingChild{
		{ToolUseID: "tool-A", StepID: "step-A", SpawnedAt: time.Now().Add(-domain.ChildAgentIdleTimeout - time.Minute)},
		{ToolUseID: "tool-B", StepID: "step-B", SpawnedAt: time.Now().Add(-domain.ChildAgentIdleTimeout - time.Minute)},
	}
	e.childProgressAt.Store("step-B", time.Now().Add(-10*time.Second).UnixNano())

	e.handleChildTimeout(nil, "turn-1")

	if len(e.pendingChildren) != 1 {
		t.Fatalf("pendingChildren len = %d, want 1", len(e.pendingChildren))
	}
	if e.pendingChildren[0].ToolUseID != "tool-B" {
		t.Fatalf("remaining child = %q, want tool-B", e.pendingChildren[0].ToolUseID)
	}
	if _, ok := e.childProgressAt.Load("step-A"); ok {
		t.Error("childProgressAt should not contain step-A after timeout")
	}
	if _, ok := e.childProgressAt.Load("step-B"); !ok {
		t.Error("childProgressAt should still contain step-B")
	}
}

// TestHandleChildResult_CleansProgressMap verifies that handleChildResult
// removes the child's entry from childProgressAt upon completion.
func TestHandleChildResult_CleansProgressMap(t *testing.T) {
	e := &turnEngine{
		logger:      newNopActorLogger(),
		stepByID:    map[string]domain.TurnAction{},
		stepEventMu: sync.Mutex{},
	}
	e.pendingChildren = []pendingChild{{ToolUseID: "tool-1", StepID: "step-1"}}
	e.childProgressAt.Store("step-1", time.Now().Add(-5*time.Second).UnixNano())

	e.handleChildResult(nil, "turn-1", childResult{
		ToolUseID: "tool-1",
		Result:    domain.ForkResult{Summary: "done"},
	})

	if len(e.pendingChildren) != 0 {
		t.Fatalf("pendingChildren len = %d, want 0 after completion", len(e.pendingChildren))
	}
	if _, ok := e.childProgressAt.Load("step-1"); ok {
		t.Error("childProgressAt should not contain step-1 after child completion")
	}
}

// TestRefreshChildLiveness_RemovesEvictedFromPendingChildren verifies the core
// fix: when refreshChildLiveness detects a dead child (liveness failures >=
// threshold) and evicts it, the child MUST be removed from pendingChildren.
// Without this, executeWaitChildren loops forever on a dead child until the
// 2-minute idle timer fires — the fork_explore hang bug.
func TestRefreshChildLiveness_RemovesEvictedFromPendingChildren(t *testing.T) {
	e := &turnEngine{
		logger:      newNopActorLogger(),
		stepByID:    map[string]domain.TurnAction{},
		stepEventMu: sync.Mutex{},
		turnID:      "turn-1",
		onChildLiveness: func(_ actor.Context, _ string) (time.Time, error) {
			return time.Time{}, fmt.Errorf("child not found")
		},
		onChildTimeout: func(_ actor.Context, _ string) {},
	}
	// Two children: A will be evicted (liveness fails >= threshold), B stays.
	e.pendingChildren = []pendingChild{
		{ToolUseID: "tool-A", StepID: "step-A", SpawnedAt: time.Now()},
		{ToolUseID: "tool-B", StepID: "step-B", SpawnedAt: time.Now()},
	}

	// First call: both fail once (1 < threshold=2, neither evicted).
	e.refreshChildLiveness(nil)
	if len(e.pendingChildren) != 2 {
		t.Fatalf("after 1st liveness check: pendingChildren len = %d, want 2", len(e.pendingChildren))
	}

	// Second call: A reaches threshold=2 and is evicted; B also reaches 2 but
	// we only want to verify A is removed. Actually both reach 2, so both get
	// evicted. Let's adjust: reset B's failures so only A exceeds.
	e.childLivenessFailures.Delete("step-B")

	e.refreshChildLiveness(nil)
	if len(e.pendingChildren) != 1 {
		t.Fatalf("after eviction: pendingChildren len = %d, want 1 (evicted child must be removed)", len(e.pendingChildren))
	}
	if e.pendingChildren[0].ToolUseID != "tool-B" {
		t.Fatalf("remaining child = %q, want tool-B", e.pendingChildren[0].ToolUseID)
	}
}

// TestRefreshChildLiveness_KeepsAliveChild verifies that a child whose
// liveness check succeeds is not evicted and stays in pendingChildren.
func TestRefreshChildLiveness_KeepsAliveChild(t *testing.T) {
	now := time.Now()
	e := &turnEngine{
		logger: newNopActorLogger(),
		onChildLiveness: func(_ actor.Context, _ string) (time.Time, error) {
			return now, nil
		},
	}
	e.pendingChildren = []pendingChild{
		{ToolUseID: "tool-1", StepID: "step-1", SpawnedAt: now},
	}

	e.refreshChildLiveness(nil)
	if len(e.pendingChildren) != 1 {
		t.Fatalf("pendingChildren len = %d, want 1 (alive child must stay)", len(e.pendingChildren))
	}
}

// TestHandleChildResult_PropagatesFileChanges verifies that a successful child
// result carrying FileChanges merges them into the parent turn's fileChanges
// and emits a step.file_changes event.
func TestHandleChildResult_PropagatesFileChanges(t *testing.T) {
	e := &turnEngine{
		logger:      newNopActorLogger(),
		stepByID:    map[string]domain.TurnAction{},
		stepEventMu: sync.Mutex{},
	}
	e.pendingChildren = []pendingChild{{ToolUseID: "tool-1", StepID: "step-1"}}

	childChanges := []domain.TurnFileChange{
		{Path: "web/src/child.ts", Additions: 10, Deletions: 2, DiffContent: "@@ diff @@"},
	}

	e.handleChildResult(nil, "turn-1", childResult{
		ToolUseID: "tool-1",
		Result: domain.ForkResult{
			Summary:     "done",
			FileChanges: childChanges,
		},
	})

	if len(e.fileChanges) != 1 {
		t.Fatalf("fileChanges len = %d, want 1", len(e.fileChanges))
	}
	if e.fileChanges[0].Path != "web/src/child.ts" {
		t.Errorf("fileChanges[0].Path = %q, want web/src/child.ts", e.fileChanges[0].Path)
	}

	events := findStepEventsByKind(e.stepEvents, "step.file_changes")
	if len(events) != 1 {
		t.Fatalf("expected 1 step.file_changes event, got %d", len(events))
	}
	if events[0].StepID != "step-1" {
		t.Errorf("event StepID = %q, want step-1", events[0].StepID)
	}
}
