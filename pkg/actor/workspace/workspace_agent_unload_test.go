package workspace

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// unloadFixture builds a fresh workspace with a single loaded agent whose
// actor is considered addressable (LookupIDFn returns a live ref) so the
// handler's cancel + Destroy path is exercised.
func unloadFixture(t *testing.T, agentID, actorID string) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a, ctx := freshActor(t)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, nil), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	a.Agents = []domain.AgentRef{{
		ID:             agentID,
		ActorID:        actorID,
		AgentKind:      domain.AgentKindWorker,
		LifecycleScope: "workflow",
		LoadState:      "loaded",
		Status:         "running",
		ActiveTurnRef:  "turn-123",
		Degraded:       true,
		DegradedReason: "boom",
	}}
	if a.agentRuntime == nil {
		a.agentRuntime = make(map[string]gen.AgentRuntimeState)
	}
	a.agentRuntime[actorID] = gen.AgentRuntimeState{State: "running", ActiveTurnRef: "turn-123"}
	return a, ctx
}

// TestHandleAgentUnload_PreservesRefAndMarksUnloaded is the core acceptance
// case: unload keeps the AgentRef (ID, ActorID, LifecycleScope) in the
// registry, flips LoadState to "unloaded", normalizes runtime status exactly
// like the restart path, clears the ephemeral runtime cache, and emits so the
// frontend grays the sidebar item. It must NOT set DeletionStatus (no
// tombstone) and must NOT remove the agent from the projection.
func TestHandleAgentUnload_PreservesRefAndMarksUnloaded(t *testing.T) {
	actorID := genID()
	a, ctx := unloadFixture(t, "W#1", actorID)

	var destroyed bool
	ctx.DestroyFn = func(ref.Ref) error { destroyed = true; return nil }

	resp, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"})
	if err != nil {
		t.Fatalf("handleAgentUnload: %v", err)
	}
	if !resp.Unloaded {
		t.Fatalf("Unloaded = false, want true")
	}
	if !destroyed {
		t.Fatal("agent actor was not destroyed")
	}

	if len(a.Agents) != 1 {
		t.Fatalf("AgentRef was removed from registry (len=%d); unload must preserve it", len(a.Agents))
	}
	ag := a.Agents[0]
	if ag.ID != "W#1" {
		t.Errorf("ID changed: %q", ag.ID)
	}
	if ag.ActorID != actorID {
		t.Errorf("ActorID must be preserved for lazy-load identity, got %q want %q", ag.ActorID, actorID)
	}
	if ag.LifecycleScope != "workflow" {
		t.Errorf("LifecycleScope not preserved: %q", ag.LifecycleScope)
	}
	if ag.LoadState != "unloaded" {
		t.Errorf("LoadState = %q, want unloaded", ag.LoadState)
	}
	if ag.Status != "paused" {
		t.Errorf("Status = %q, want paused (restart normalization of running)", ag.Status)
	}
	if ag.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef not cleared: %q", ag.ActiveTurnRef)
	}
	if ag.Degraded || ag.DegradedReason != "" {
		t.Errorf("Degraded runtime flags not cleared: %v %q", ag.Degraded, ag.DegradedReason)
	}
	if ag.DeletionStatus != "" {
		t.Errorf("DeletionStatus = %q; unload must NOT set a deletion tombstone", ag.DeletionStatus)
	}
	if _, ok := a.agentRuntime[actorID]; ok {
		t.Error("ephemeral runtime cache not cleared after unload")
	}

	// The agent must remain in the interactive projection as a gray item,
	// NOT filtered out like a deleting tombstone.
	state := a.buildAgentListState(true)
	if len(state.Items) != 1 {
		t.Fatalf("projection item count = %d, want 1 (agent must stay visible)", len(state.Items))
	}
	item := state.Items[0]
	if item.ID != "W#1" || item.LoadState != "unloaded" || item.ActorID != actorID {
		t.Errorf("projection item mismatch: ID=%s LoadState=%s ActorID=%s", item.ID, item.LoadState, item.ActorID)
	}
	if item.DeletionStatus != "" {
		t.Errorf("projection DeletionStatus = %q, want empty", item.DeletionStatus)
	}
}

// TestHandleAgentUnload_ReloadRecovers is the second half of the acceptance
// case: after unload, workspace.load_agent (loadAgentByID) re-spawns the
// agent from its preserved ActorID and flips it back to "loaded".
func TestHandleAgentUnload_ReloadRecovers(t *testing.T) {
	actorID := genID()
	a, ctx := unloadFixture(t, "W#1", actorID)
	// Simulate the destroyed actor being absent from the tree so
	// loadAgentByID must go through the (re)spawn path.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) { return nil, false }

	if _, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"}); err != nil {
		t.Fatalf("handleAgentUnload: %v", err)
	}

	ag, alreadyLoaded, err := a.loadAgentByID(ctx, "W#1")
	if err != nil {
		t.Fatalf("loadAgentByID after unload failed: %v", err)
	}
	if alreadyLoaded {
		t.Fatalf("loadAgentByID reported alreadyLoaded=true, want a fresh spawn after unload")
	}
	if ag.ID != "W#1" {
		t.Errorf("ID = %q, want W#1", ag.ID)
	}
	if ag.LoadState != "loaded" {
		t.Errorf("LoadState = %q, want loaded after reload", ag.LoadState)
	}
	if ag.ActorID == "" {
		t.Error("ActorID empty after reload")
	}
	if len(a.Agents) != 1 {
		t.Fatalf("AgentRef lost during reload (len=%d)", len(a.Agents))
	}
}

// TestHandleAgentUnload_Idempotent verifies unloading an already-unloaded
// agent (no addressable actor) is a no-op that still reports Unloaded=true.
func TestHandleAgentUnload_Idempotent(t *testing.T) {
	a, ctx := unloadFixture(t, "W#1", genID())
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }
	destroyed := false
	ctx.DestroyFn = func(ref.Ref) error { destroyed = true; return nil }

	if _, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"}); err != nil {
		t.Fatalf("first unload: %v", err)
	}
	if destroyed {
		t.Fatal("no live actor existed; Destroy should not have been called")
	}
	resp, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"})
	if err != nil {
		t.Fatalf("second (idempotent) unload: %v", err)
	}
	if !resp.Unloaded {
		t.Fatalf("idempotent unload returned Unloaded=false")
	}
}

// TestHandleAgentUnload_NotFound verifies an unknown agent ID is rejected.
func TestHandleAgentUnload_NotFound(t *testing.T) {
	a, ctx := unloadFixture(t, "W#1", genID())
	if _, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "does-not-exist"}); err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
}

// TestHandleAgentUnload_AllowsInternalZeroIdentity models the project-side
// fire-and-forget path (C4): an internal actor-to-actor call carries a zero
// identity and must be allowed without a human-role check.
func TestHandleAgentUnload_AllowsInternalZeroIdentity(t *testing.T) {
	ps := persist.NewFSPersist(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: ps}
	ctx := testutil.AnonCtx(testutil.GenActorID()) // Identity{} → IsZero true
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	actorID := genID()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, LoadState: "loaded"}}

	resp, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"})
	if err != nil {
		t.Fatalf("internal (zero-identity) unload must be allowed: %v", err)
	}
	if !resp.Unloaded {
		t.Fatal("Unloaded = false")
	}
}

// TestHandleAgentUnload_AllowsSystemRole mirrors the production scheduler
// path: the project fire-and-forget to workspace.agent_unload crosses the
// workspace service ref and arrives stamped role "system" (the workspace
// cell's own Props role). The old zero-identity-only carve-out rejected it;
// it must now be allowed.
func TestHandleAgentUnload_AllowsSystemRole(t *testing.T) {
	ps := persist.NewFSPersist(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: ps}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: "system"} // service-ref-stamped internal call
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	actorID := genID()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, LoadState: "loaded"}}

	resp, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"})
	if err != nil {
		t.Fatalf("system-role internal unload must be allowed: %v", err)
	}
	if !resp.Unloaded {
		t.Fatal("Unloaded = false")
	}
}

// TestHandleAgentUnload_DeniesExternalNonHuman models an external caller with
// a non-human role: the call must be rejected by the admin/owner gate.
func TestHandleAgentUnload_DeniesExternalNonHuman(t *testing.T) {
	ps := persist.NewFSPersist(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := &Actor{store: ps}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: "guest"} // external, non-human
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	actorID := genID()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, LoadState: "loaded"}}

	if _, err := a.handleAgentUnload(ctx, gen.AgentUnloadReq{AgentID: "W#1"}); err == nil {
		t.Fatal("expected permission denied for external non-human role, got nil")
	}
}
