package project

import (
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleSpawnAgent_InvalidActorID_ReturnsError locks down the strict
// validation contract: a malformed ActorID must produce an explicit error
// rather than silently falling back to auto-generated actor ID. Silent
// fallback was the root cause of persistence-key drift (history orphaned
// under one actorID, new actor writes to another).
func TestHandleSpawnAgent_InvalidActorID_ReturnsError(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName: "Coder#abcd",
		ProjectID: "proj-1",
		AgentKind: "coder",
		ActorID:   "garbage-not-a-ulid",
	})
	if err == nil {
		t.Fatal("expected error for invalid ActorID, got nil")
	}
	if !strings.Contains(err.Error(), "invalid ActorId") {
		t.Errorf("expected 'invalid ActorId' in error, got %v", err)
	}
}

// TestHandleSpawnAgent_MissingSpawnName_ReturnsError locks down SpawnName as
// required input. Without it the persistence key (which derives from agentID
// = SpawnName) would be empty and history would never persist.
func TestHandleSpawnAgent_MissingSpawnName_ReturnsError(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName: "",
		ProjectID: "proj-1",
		AgentKind: "coder",
	})
	if err == nil {
		t.Fatal("expected error for missing SpawnName, got nil")
	}
	if !strings.Contains(err.Error(), "SpawnName required") {
		t.Errorf("expected 'SpawnName required' in error, got %v", err)
	}
}

// TestHandleSpawnAgent_ReturnsImmediatelyWithoutBlocking locks down the
// non-blocking contract: spawn_agent must return as soon as the agent is
// allocated, WITHOUT waiting for OnStart. The agent's OnStart synchronously
// calls back into the workspace owner loop (fetchAgentKindConfig), and
// load_agent drives this spawn from that same loop — blocking here would
// deadlock the workspace→project→agent chain. Cold-start readiness is handled
// off-loop by the pure session.summary handler.
func TestHandleSpawnAgent_ReturnsImmediatelyWithoutBlocking(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	agentID := testutil.GenActorID()
	invokeCalls := 0
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(agentID, func(string, any) any {
			invokeCalls++
			return nil
		}), nil
	}

	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName: "Coder#abcd",
		ProjectID: "proj-1",
		AgentKind: "coder",
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if resp.ActorID != agentID.String() {
		t.Errorf("ActorID = %q, want %q", resp.ActorID, agentID.String())
	}
	if invokeCalls != 0 {
		t.Errorf("spawn_agent invoked new agent %d times before returning; this can deadlock OnStart callbacks", invokeCalls)
	}
}

func TestHandleSpawnAgent_CloneInheritsSourceWorktree(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	sourceID := testutil.GenActorID().String()
	cloneID := testutil.GenActorID()
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-source": {ID: "wt-source", Status: "active"},
	}
	a.agentWorktree = map[string]string{sourceID: "wt-source"}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(cloneID, nil), nil
	}

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:          "Clone#abcd",
		ProjectID:          "proj-1",
		AgentKind:          "coder",
		CloneSourceActorID: sourceID,
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if got := a.agentWorktree[cloneID.String()]; got != "wt-source" {
		t.Fatalf("clone worktree = %q, want wt-source", got)
	}
}

func TestHandleSpawnAgent_ExplicitWorktreeOverridesCloneSource(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	sourceID := testutil.GenActorID().String()
	cloneID := testutil.GenActorID()
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-source":   {ID: "wt-source", Status: "active"},
		"wt-explicit": {ID: "wt-explicit", Status: "active"},
	}
	a.agentWorktree = map[string]string{sourceID: "wt-source"}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(cloneID, nil), nil
	}

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:          "Clone#abcd",
		ProjectID:          "proj-1",
		AgentKind:          "coder",
		WorktreeID:         "wt-explicit",
		CloneSourceActorID: sourceID,
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if got := a.agentWorktree[cloneID.String()]; got != "wt-explicit" {
		t.Fatalf("clone worktree = %q, want wt-explicit", got)
	}
}

// TestHandleSpawnAgent_ForkChildInheritsParentWorktree locks down fork-child
// worktree inheritance: a fork child (ChildConfig != nil) spawned without an
// explicit WorktreeID inherits the parent agent's binding so file/git/shell
// tools route into the parent's worktree sandbox.
func TestHandleSpawnAgent_ForkChildInheritsParentWorktree(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	// Distinct canonical IDs: GenActorID() is deterministic, so derive the
	// child from a second generator step or the test reads back its own
	// seeded parent binding.
	parentID := testutil.GenActorID().String()
	childID := id.NewCanonical(1, 0, func() uint64 { return 2 }).Next()
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-parent": {ID: "wt-parent", Status: "active"},
	}
	a.agentWorktree = map[string]string{parentID: "wt-parent"}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(childID, nil), nil
	}

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:     "explorer-1-tu1",
		ProjectID:     "proj-1",
		AgentKind:     "explorer",
		ParentAgentID: parentID,
		ChildConfig: &domain.ChildSpawnConfig{
			ParentActorID: parentID,
			Task:          "explore",
			Prompt:        "explore",
		},
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if got := a.agentWorktree[childID.String()]; got != "wt-parent" {
		t.Fatalf("fork child worktree = %q, want wt-parent", got)
	}
}

// TestHandleSpawnAgent_WorkflowWorkerDoesNotInheritParentWorktree locks down
// that workflow worker spawns (ChildConfig == nil, no explicit WorktreeID) do
// NOT inherit the parent's binding — workers either get their own child
// worktree (explicit WorktreeID, set by executor_worker_task when the owner has
// a workflow worktree) or run unbound.
func TestHandleSpawnAgent_WorkflowWorkerDoesNotInheritParentWorktree(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())
	parentID := testutil.GenActorID().String()
	workerID := id.NewCanonical(1, 0, func() uint64 { return 3 }).Next()
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-parent": {ID: "wt-parent", Status: "active"},
	}
	a.agentWorktree = map[string]string{parentID: "wt-parent"}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(workerID, nil), nil
	}

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:     "worker-1",
		ProjectID:     "proj-1",
		AgentKind:     "coder",
		ParentAgentID: parentID,
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}
	if _, bound := a.agentWorktree[workerID.String()]; bound {
		t.Fatalf("workflow worker must not inherit parent worktree, got binding %q", a.agentWorktree[workerID.String()])
	}
}

// TestHandleSpawnAgent_RecordsAgentParent_ForReviewAuthorization locks down
// the parent-child lineage contract: spawn_agent (a PureContext handler)
// must record agentParent[child]=parent so the review changeset
// authorization path (authorizeReviewChangeset) can verify the caller is the
// child's direct parent. This is the security boundary that gates who may
// read a frozen review changeset.
func TestHandleSpawnAgent_RecordsAgentParent_ForReviewAuthorization(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	// Use a non-human context so authorizeReviewChangeset does not bypass
	// parent authorization (human callers are always trusted).
	parentID := testutil.GenActorID().String()
	childID := id.NewCanonical(1, 0, func() uint64 { return 2 }).Next()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(childID, nil), nil
	}

	_, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:     "worker-1",
		ProjectID:     "proj-1",
		AgentKind:     "coder",
		ParentAgentID: parentID,
	})
	if err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}

	a.worktreeParentMu.RLock()
	gotParent := a.agentParent[childID.String()]
	a.worktreeParentMu.RUnlock()
	if gotParent != parentID {
		t.Fatalf("agentParent[%s] = %q, want parent %q", childID, gotParent, parentID)
	}

	// The authorization gate must accept the recorded direct parent and
	// reject other callers.
	childAgentID := childID.String()
	if err := a.authorizeReviewChangeset(ctx, childAgentID, parentID); err != nil {
		t.Fatalf("direct parent must be authorized to read the child changeset: %v", err)
	}
	otherID := id.NewCanonical(1, 0, func() uint64 { return 3 }).Next().String()
	if err := a.authorizeReviewChangeset(ctx, childAgentID, otherID); err == nil {
		t.Fatal("non-parent caller must be rejected by authorizeReviewChangeset")
	}
}

// TestHandleSpawnAgent_PersistsWorktreeBindingToManifest locks down the
// restart-survival contract for spawn-time worktree bindings: handleSpawnAgent
// must write the binding (and its parentage) to the worktree's ground-truth
// manifest. Before this fix only the owner bind paths (workflow_create_worktree,
// reattach, timer local-spawn) persisted; a worker's binding lived solely in
// the in-memory agentWorktree map, so a crash before the next lifecycle
// persist (timer/exit/merge) lost it and the reloaded worker ran unbound on
// the main repo root.
func TestHandleSpawnAgent_PersistsWorktreeBindingToManifest(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a, ctx := freshProject(t, t.TempDir())
	parentID := testutil.GenActorID().String()
	workerID := id.NewCanonical(1, 0, func() uint64 { return 4 }).Next()
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-worker": {ID: "wt-worker", Status: "active"},
	}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(workerID, nil), nil
	}

	if _, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:     "worker-1",
		ProjectID:     "proj-1",
		AgentKind:     "coder",
		WorktreeID:    "wt-worker",
		ParentAgentID: parentID,
	}); err != nil {
		t.Fatalf("handleSpawnAgent: %v", err)
	}

	// Restart simulation: a fresh Actor over the same data dir rebuilds its
	// binding cache from the on-disk manifests only.
	b := &Actor{actorID: a.actorID}
	if err := b.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache: %v", err)
	}
	if got := b.agentWorktree[workerID.String()]; got != "wt-worker" {
		t.Fatalf("after restart agentWorktree[%s] = %q, want wt-worker", workerID, got)
	}
	if got := b.agentParent[workerID.String()]; got != parentID {
		t.Fatalf("after restart agentParent[%s] = %q, want parent %q", workerID, got, parentID)
	}
}

// TestHandleSpawnAgent_AgentParent_ConcurrentSpawns verifies that concurrent
// PureContext spawn calls do not race on the agentParent map (protected by
// worktreeParentMu). The review changeset authorization depends on every
// spawn atomically recording its parent while other spawns may be in flight.
func TestHandleSpawnAgent_AgentParent_ConcurrentSpawns(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	parentID := testutil.GenActorID().String()

	const n = 32
	childIDs := make([]string, n)
	// Each spawn returns a distinct child ref: FakeCtx.SpawnFn is called from
	// goroutines here, so derive unique IDs without sharing state.
	for i := 0; i < n; i++ {
		childIDs[i] = id.NewCanonical(1, 0, func() uint64 { return uint64(i + 1) }).Next().String()
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		childID := id.NewCanonical(1, 0, func() uint64 { return uint64(i + 1) }).Next()
		go func(cid id.ActorID) {
			defer wg.Done()
			sub := testutil.AdminCtx(testutil.GenActorID())
			sub.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
				return testutil.NewFakeRef(cid, nil), nil
			}
			_, err := a.handleSpawnAgent(sub, domain.ProjectSpawnAgentReq{
				SpawnName:     "worker-" + cid.String(),
				ProjectID:     "proj-1",
				AgentKind:     "coder",
				ParentAgentID: parentID,
			})
			if err != nil {
				t.Errorf("handleSpawnAgent: %v", err)
			}
		}(childID)
	}
	wg.Wait()

	// Every spawn must have recorded its parent.
	a.worktreeParentMu.RLock()
	defer a.worktreeParentMu.RUnlock()
	if len(a.agentParent) != n {
		t.Fatalf("agentParent records = %d, want %d", len(a.agentParent), n)
	}
	for _, cid := range childIDs {
		if got := a.agentParent[cid]; got != parentID {
			t.Errorf("agentParent[%s] = %q, want %q", cid, got, parentID)
		}
	}
}
