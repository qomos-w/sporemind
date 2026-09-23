package workspace

import (
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

var schedulerSpawnTestIDSeq uint64

func schedulerSpawnGenID() string {
	schedulerSpawnTestIDSeq++
	g := id.NewCanonical(99, 0, func() uint64 { return schedulerSpawnTestIDSeq })
	return g.Next().String()
}

func schedulerSpawnFixture(t *testing.T) (*Actor, *testutil.FakeCtx, string) {
	t.Helper()
	a, ctx := freshActorAnon(t)
	projectID := schedulerSpawnGenID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	return a, ctx, projectID
}

// TestHandleAgentSpawnScheduler_RegistersEphemeralAgent covers the core
// contract: an internal (zero-identity) caller — the project scheduler loop —
// can register a scheduler-scoped agent without spawning it, and the registry
// row carries the LifecycleScope="scheduler" marker plus LoadState="unloaded".
func TestHandleAgentSpawnScheduler_RegistersEphemeralAgent(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)

	resp, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder),
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnScheduler: %v", err)
	}
	if resp.AgentID == "" {
		t.Fatal("AgentID empty")
	}
	if resp.AgentKind != string(domain.AgentKindCoder) {
		t.Fatalf("AgentKind = %q, want coder", resp.AgentKind)
	}
	if len(a.Agents) != 1 {
		t.Fatalf("registry len = %d, want 1", len(a.Agents))
	}
	ag := a.Agents[0]
	if ag.ID != resp.AgentID {
		t.Fatalf("registry ID = %q, want %q", ag.ID, resp.AgentID)
	}
	if ag.LifecycleScope != "scheduler" {
		t.Fatalf("LifecycleScope = %q, want scheduler", ag.LifecycleScope)
	}
	if ag.LoadState != "unloaded" {
		t.Fatalf("LoadState = %q, want unloaded (spawn happens on the project side)", ag.LoadState)
	}
	if ag.Status != "idle" {
		t.Fatalf("Status = %q, want idle", ag.Status)
	}
	if ag.DeletionStatus != "" {
		t.Fatalf("DeletionStatus = %q, want empty (no tombstone)", ag.DeletionStatus)
	}
}

// TestHandleAgentSpawnScheduler_RejectsExternalNonAdmin verifies the
// authorization boundary: external callers need an admin role, mirroring
// workspace.agent_unload.
func TestHandleAgentSpawnScheduler_RejectsExternalNonAdmin(t *testing.T) {
	a, _, projectID := schedulerSpawnFixture(t)
	// A non-zero identity without an admin role must be denied.
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: "guest"} // external, non-admin
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK

	_, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder),
	})
	if err == nil || !strings.Contains(err.Error(), "requires admin role") {
		t.Fatalf("error = %v, want 'requires admin role'", err)
	}
	if len(a.Agents) != 0 {
		t.Fatalf("registry len = %d, want 0 (denied)", len(a.Agents))
	}
}

// TestHandleAgentSpawnScheduler_AllowsSystemRole reproduces the production
// failure: the project scheduler loop reaches this handler through the
// workspace service ref, which gospore stamps with the workspace cell's own
// "system" role. The old zero-identity-only carve-out rejected it with
// `requires admin role, got "system"`, breaking every timer fire. A
// system-stamped internal call must now succeed.
func TestHandleAgentSpawnScheduler_AllowsSystemRole(t *testing.T) {
	a, _, projectID := schedulerSpawnFixture(t)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Kind: id.IdentityToken, Role: "system"} // service-ref-stamped internal call
	ctx.SpawnFn = noOpSpawn
	ctx.LookupIDFn = lookupOK

	resp, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder),
	})
	if err != nil {
		t.Fatalf("system-role internal call: %v", err)
	}
	if resp.AgentID == "" {
		t.Fatalf("AgentID empty on system-role success")
	}
	if len(a.Agents) != 1 || a.Agents[0].LifecycleScope != "scheduler" {
		t.Fatalf("registry = %+v, want 1 scheduler-scoped entry", a.Agents)
	}
}

// TestHandleAgentSpawnScheduler_ValidatesInput verifies the guard rails:
// missing project, unknown kind, and system projects are rejected.
func TestHandleAgentSpawnScheduler_ValidatesInput(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)

	if _, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		AgentKind: string(domain.AgentKindCoder),
	}); err == nil || !strings.Contains(err.Error(), "ProjectId is required") {
		t.Fatalf("error = %v, want 'ProjectId is required'", err)
	}
	if _, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: "bogus",
	}); err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
		t.Fatalf("error = %v, want 'unknown agent kind'", err)
	}
	if len(a.Agents) != 0 {
		t.Fatalf("registry len = %d, want 0 after validation failures", len(a.Agents))
	}

	// System meta project cannot host scheduler agents.
	sysID := schedulerSpawnGenID()
	a.Mounts = append(a.Mounts, domain.ProjectRef{Name: "sys", Path: t.TempDir(), ActorID: sysID, System: true})
	if _, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: sysID, AgentKind: string(domain.AgentKindCoder),
	}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("error = %v, want 'not available'", err)
	}
	if len(a.Agents) != 0 {
		t.Fatalf("registry len = %d, want 0", len(a.Agents))
	}
}

// TestHandleAgentSpawnScheduler_UniqueSpawnNames verifies repeated spawns
// allocate distinct registry IDs so the sidebar and lazy load never collide.
func TestHandleAgentSpawnScheduler_UniqueSpawnNames(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)

	first, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder), DisplayName: "Sched",
	})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	second, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder), DisplayName: "Sched",
	})
	if err != nil {
		t.Fatalf("second spawn: %v", err)
	}
	if first.AgentID == second.AgentID {
		t.Fatalf("spawn names collided: %q", first.AgentID)
	}
	if len(a.Agents) != 2 {
		t.Fatalf("registry len = %d, want 2", len(a.Agents))
	}
}

// TestHandleAgentSpawnScheduler_ConcurrentSpawnsAllocateDistinctNames
// verifies the stateless conversion is race-free on registry state: now that
// concurrent handler goroutines run off the owner loop, the unique-name
// allocation and append share one agentsMu critical section. Each concurrent
// call must land exactly one registry row with a distinct ID.
func TestHandleAgentSpawnScheduler_ConcurrentSpawnsAllocateDistinctNames(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)

	const goroutines, perGoroutine = 4, 4
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
					ProjectID: projectID, AgentKind: string(domain.AgentKindCoder),
				}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent spawn: %v", err)
	}

	a.agentsMu.RLock()
	defer a.agentsMu.RUnlock()
	if len(a.Agents) != goroutines*perGoroutine {
		t.Fatalf("registry len = %d, want %d", len(a.Agents), goroutines*perGoroutine)
	}
	seen := make(map[string]bool, len(a.Agents))
	for _, ag := range a.Agents {
		if seen[ag.ID] {
			t.Fatalf("duplicate spawn ID %q", ag.ID)
		}
		seen[ag.ID] = true
	}
}

// TestHandleAgentSpawnScheduler_RegistrySaveFailureReturnsError pins actor #9:
// when the registry card cannot be persisted the handler must surface the
// error instead of silently succeeding, and must not leave a phantom row in
// the in-memory registry that would vanish on reload.
func TestHandleAgentSpawnScheduler_RegistrySaveFailureReturnsError(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)
	// Force saveAgentRegistry to fail: the fail-stop guard refuses every write
	// until the registry card has been loaded.
	a.registryCardLoaded.Store(false)

	_, err := a.handleAgentSpawnScheduler(ctx, gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID, AgentKind: string(domain.AgentKindCoder),
	})
	if err == nil || !strings.Contains(err.Error(), "save agent registry") {
		t.Fatalf("error = %v, want 'save agent registry'", err)
	}
	if len(a.Agents) != 0 {
		t.Fatalf("registry len = %d, want 0 after a failed persist (phantom row left behind)", len(a.Agents))
	}
}

// TestHandleAgentSpawnScheduler_AppliesModelSlots verifies that the handler
// applies the ModelSlot fields from the request to the registry row, with a
// nil slot falling back to the agent-kind config default (aligning with
// handleWorkspaceCreateAgent's resolveAgentModelSlots).
func TestHandleAgentSpawnScheduler_AppliesModelSlots(t *testing.T) {
	a, ctx, projectID := schedulerSpawnFixture(t)

	// Seed a kind config with defaults so the nil-fallback path is exercised
	// for the slots the request leaves unset. Seeded directly on the actor
	// (the config-save callable requires an admin role, which the anonymous
	// scheduler-spawn fixture does not carry).
	a.AgentKindConfigs = []domain.AgentKindConfig{{
		Kind:      string(domain.AgentKindCoder),
		Primary:   &gen.ModelSlot{Candidates: []gen.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "default-model", Provider: "default-prov"}}}},
		Fast:      &gen.ModelSlot{Candidates: []gen.ModelRef{{Kind: "auto"}}},
		Execution: &gen.ModelSlot{Candidates: []gen.ModelRef{{Kind: "auto"}}},
		Review:    &gen.ModelSlot{Candidates: []gen.ModelRef{{Kind: "auto"}}},
		Summary:   &gen.ModelSlot{Candidates: []gen.ModelRef{{Kind: "auto"}}},
	}}

	// Request: only Primary is set; the other four slots are nil and should
	// fall back to the kind-config defaults.
	req := gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID: projectID,
		AgentKind: string(domain.AgentKindCoder),
		Primary: &gen.ModelSlot{Candidates: []gen.ModelRef{
			{Kind: "unit", Unit: &gen.ModelUnit{Model: "gpt-test", Provider: "openai-test"}},
		}},
	}
	resp, err := a.handleAgentSpawnScheduler(ctx, req)
	if err != nil {
		t.Fatalf("handleAgentSpawnScheduler: %v", err)
	}

	if len(a.Agents) != 1 {
		t.Fatalf("registry len = %d, want 1", len(a.Agents))
	}
	ag := a.Agents[0]
	if ag.ID != resp.AgentID {
		t.Fatalf("registry ID = %q, want %q", ag.ID, resp.AgentID)
	}

	// Primary: the request value should win (not the config default).
	if ag.Primary == nil || len(ag.Primary.Candidates) != 1 || ag.Primary.Candidates[0].Unit == nil {
		t.Fatalf("Primary = %+v, want gpt-test/openai-test", ag.Primary)
	}
	if ag.Primary.Candidates[0].Unit.Model != "gpt-test" {
		t.Fatalf("Primary Unit.Model = %q, want gpt-test", ag.Primary.Candidates[0].Unit.Model)
	}

	// Fast: nil in request → fell back to config default.
	if ag.Fast == nil || len(ag.Fast.Candidates) == 0 {
		t.Fatalf("Fast = %+v, want config default (auto)", ag.Fast)
	}
	if ag.Fast.Candidates[0].Kind != "auto" {
		t.Fatalf("Fast candidate kind = %q, want auto", ag.Fast.Candidates[0].Kind)
	}

	// Execution, Review, Summary: all from config defaults.
	for _, pair := range []struct {
		slot string
		v    *gen.ModelSlot
	}{{slot: "Execution", v: ag.Execution}, {slot: "Review", v: ag.Review}, {slot: "Summary", v: ag.Summary}} {
		if pair.v == nil {
			t.Fatalf("%s = nil, want config default", pair.slot)
		}
	}
}
