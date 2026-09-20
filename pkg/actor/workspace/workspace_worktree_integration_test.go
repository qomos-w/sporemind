package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── Item 9: Worker spawn creates child worktree from owner worktree ──

// TestWorkerSpawn_CreatesChildWorktree verifies that when the owner agent
// has an active workflow worktree, Execute creates a child worktree via
// project.worktree.create with the correct ParentWorktreeID and
// WorkflowMapID, and passes the child worktree ID to spawnAgentViaProject.
func TestWorkerSpawn_CreatesChildWorktree(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ownerWorktreeID := "owner-wt-1"
	ownerMapCardID := "map-1"
	childWorktreeID := "child-wt-1"

	var createReq gen.ProjectWorktreeCreateReq
	var spawnReq domain.ProjectSpawnAgentReq
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				if req, ok := payload.(gen.ProjectWorktreeCreateReq); ok {
					createReq = req
				}
				return gen.ProjectWorktree{ID: childWorktreeID, Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_map_owner":
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
		PermissionMode:  "auto",
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}
	if resp.AgentActorID != agentActorID {
		t.Errorf("expected AgentActorID %q, got %q", agentActorID, resp.AgentActorID)
	}

	// Verify worktree.create was called with the owner's worktree ID.
	if createReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected ParentWorktreeID %q, got %q", ownerWorktreeID, createReq.ParentWorktreeID)
	}
	if createReq.WorkflowMapID != ownerMapCardID {
		t.Errorf("expected WorkflowMapID %q, got %q", ownerMapCardID, createReq.WorkflowMapID)
	}
	if createReq.BaseRef != "" {
		t.Errorf("expected empty BaseRef (resolved by project from parent HEAD), got %q", createReq.BaseRef)
	}

	// Verify the child worktree ID was passed to spawnAgentViaProject.
	if spawnReq.WorktreeID != childWorktreeID {
		t.Errorf("expected spawn WorktreeID %q, got %q", childWorktreeID, spawnReq.WorktreeID)
	}

	// Verify the spawned agent's Mode has the child worktree ID stamped.
	ag := a.Agents[len(a.Agents)-1]
	if ag.Mode == nil || ag.Mode.ActiveWorkflowWorktreeID != childWorktreeID {
		t.Errorf("expected Mode.ActiveWorkflowWorktreeID %q, got %+v", childWorktreeID, ag.Mode)
	}
}

// TestWorkerSpawn_NoGitOwnerSkipsChildWorktree pins the no-git workflow spawn
// path: when the workflow owner's agent_status reports an empty
// ActiveWorkflowWorktreeID (no-git mode activation — the owner runs directly
// on the project root), Execute must NOT create a child worktree, must spawn
// the worker with an empty WorktreeID (no worktree path binding — the worker's
// file operations resolve to the project's main roots), and must not stamp any
// child worktree ID into the worker's Mode.
//
// The bound card here omits data.category (legacy behavior). The coding-card
// gate is opt-in via explicit category declaration, so the no-category case
// continues to follow the silent-skip path. The explicit coding-set guard is
// verified separately by TestWorkerSpawn_CodingCardNoOwnerWorktree_Rejected.
func TestWorkerSpawn_NoGitOwnerSkipsChildWorktree(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ownerMapCardID := "map-1"
	worktreeCreateCalls := 0
	var spawnReq domain.ProjectSpawnAgentReq
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: "", // no-git workflow owner: no worktree
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				worktreeCreateCalls++
				return gen.ProjectWorktree{ID: "child-wt-1", Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_map_owner":
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
		PermissionMode:  "auto",
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}
	if resp.AgentActorID != agentActorID {
		t.Errorf("expected AgentActorID %q, got %q", agentActorID, resp.AgentActorID)
	}

	// No owner worktree → no child worktree creation, no worktree path binding.
	if worktreeCreateCalls != 0 {
		t.Errorf("project.worktree_create must not be called when the owner has no worktree, got %d calls", worktreeCreateCalls)
	}
	if spawnReq.WorktreeID != "" {
		t.Errorf("expected spawn WorktreeID empty for no-worktree owner, got %q", spawnReq.WorktreeID)
	}

	// The worker Mode must carry no child worktree ID (review approve/reject
	// will skip merge/rebase, and the worker's file ops target the main root).
	ag := a.Agents[len(a.Agents)-1]
	if ag.Mode == nil {
		t.Fatal("expected worker Mode initialized (bound task card present)")
	}
	if ag.Mode.ActiveWorkflowWorktreeID != "" {
		t.Errorf("expected Mode.ActiveWorkflowWorktreeID empty, got %q", ag.Mode.ActiveWorkflowWorktreeID)
	}
}

// ── Coding-card gate: code/execute cards require an owner workflow worktree ──

// spawnAssignFixtureForCategory sets up a workspace, project, and
// caller-agent refs sufficient to drive handleAgentSpawnAssign through
// the worker_task executor with a specific data.category on the bound
// task card. Returns counters the caller asserts on:
//   - worktreeCreateCalls: how many times project.worktree_create was invoked
//   - spawnCalls:          how many times project.spawn_agent was invoked
//   - setStatusCalls:      recorded status transitions (for rollback checks)
//   - callerAgentID:       valid canonical id for the spawned worker to look up
//
// The owner's agent_status is hard-wired to return an empty
// ActiveWorkflowWorktreeID (no-git workflow activation) so the gate
// fires when the card is coding. Research/explore/review cards must
// skip the gate; code/execute cards must error out before spawn.
func spawnAssignFixtureForCategory(t *testing.T, category string) (a *Actor, ctx *testutil.FakeCtx, worktreeCreateCalls, spawnCalls *int, setStatusCalls *[]string, callerAgentID string) {
	t.Helper()
	a, ctx = freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID = g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}
	a.accountPrefs.Preferences = map[string]string{"permissionMode": "auto"}

	worktreeCreateCalls = new(int)
	spawnCalls = new(int)
	setStatusCalls = &[]string{}

	ownerMapCardID := "map-1"
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: "", // no-git owner — triggers the gate for coding cards
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		bodyByID := map[string]string{"card-1": "Implement the feature"}
		categoryByID := map[string]string{"card-1": category}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				*worktreeCreateCalls++
				return gen.ProjectWorktree{ID: "child-wt-1", Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				*spawnCalls++
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCardWithCategory(cardStatus, bodyByID, categoryByID)(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCardWithCategory(cardStatus, bodyByID, categoryByID)(payload)
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					*setStatusCalls = append(*setStatusCalls, req.ID+":"+req.Status)
				}
				return gen.WikiSetStatusResp{}
			case "project.wiki_set_map_owner":
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}
	return a, ctx, worktreeCreateCalls, spawnCalls, setStatusCalls, callerAgentID
}

// TestWorkerSpawn_CodingCardNoOwnerWorktree_Rejected verifies the new
// coding-card gate: when the owner has no workflow worktree (no-git
// activation), and the bound card declares a coding category (code or
// execute), Execute must return an explicit error BEFORE any
// project.spawn_agent call, and the dispatcher must roll the card
// status back to its previous value so the card stays re-claimable.
// The error message must point the operator at the two remediations:
// restart the workflow with coding=true, or downgrade the card to
// research/explore/review.
func TestWorkerSpawn_CodingCardNoOwnerWorktree_Rejected(t *testing.T) {
	cases := []struct {
		name     string
		category string
	}{
		{"code", "code"},
		{"execute", "execute"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx, worktreeCreateCalls, spawnCalls, setStatusCalls, callerAgentID := spawnAssignFixtureForCategory(t, tc.category)

			_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
				To:              "Worker One",
				AgentKind:       domain.AgentKindWorker,
				InterpretedGoal: "interpreted goal",
				BoundTaskCardID: "card-1",
				MaxTurns:        5,
				ProjectID:       "p1",
				CallerAgentID:   callerAgentID,
				PermissionMode:  "auto",
			})
			if err == nil {
				t.Fatalf("expected error for %s card with no owner worktree, got nil", tc.category)
			}
			// Error must surface the gate intent + both remediations.
			for _, want := range []string{
				"workspace.executor.worker_task",
				"coding task card",
				"workflow worktree",
				"coding=true",
				"research/explore/review",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must mention %q, got: %v", want, err)
				}
			}

			// Gate fires before any spawn — no worktree create, no
			// spawn_agent invoke, no worker registered.
			if *worktreeCreateCalls != 0 {
				t.Errorf("project.worktree_create must not be called when gate fires, got %d", *worktreeCreateCalls)
			}
			if *spawnCalls != 0 {
				t.Errorf("project.spawn_agent must not be called when gate fires, got %d", *spawnCalls)
			}
			if got := len(a.Agents); got != 0 {
				t.Errorf("no worker must be registered when gate fires, got %d agents", got)
			}

			// Dispatcher must roll back the claim: status flipped to
			// doing, then back to backlog. The exact transitions are:
			//   1. claim backlog → doing
			//   2. Execute errors → rollback to previousStatus (backlog)
			foundRollback := false
			for _, s := range *setStatusCalls {
				if s == "card-1:backlog" {
					foundRollback = true
				}
			}
			if !foundRollback {
				t.Errorf("dispatcher must roll back card status to previous value, got transitions %v", *setStatusCalls)
			}
		})
	}
}

// TestWorkerSpawn_NonCodingCardNoOwnerWorktree_SilentSkip pins the
// read-only exception: when the owner has no workflow worktree and the
// bound card declares a non-coding category (explore/review) or omits
// data.category, Execute must NOT create a child worktree, must spawn
// the worker with an empty WorktreeID, and must not error out. The
// non-coding set is read-only by design — the worker cannot corrupt
// the main repo even without isolation. Note: the "research" case is
// deliberately excluded — research cards are owner-only and rejected
// upstream by resolveSpawnAgentKind ("category research tasks are
// self-done by the owner via fork_explore"), so they never reach the
// worker_task executor. The "no category" case uses the legacy default
// (worker AgentKind) and pins the "no-category = silent skip"
// preservation pinned by TestWorkerSpawn_NoGitOwnerSkipsChildWorktree.
func TestWorkerSpawn_NonCodingCardNoOwnerWorktree_SilentSkip(t *testing.T) {
	cases := []struct {
		name     string
		category string
	}{
		{"explore", "explore"},
		{"review", "review"},
		{"no category", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx, worktreeCreateCalls, spawnCalls, _, callerAgentID := spawnAssignFixtureForCategory(t, tc.category)

			resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
				To:              "Worker One",
				AgentKind:       domain.AgentKindWorker,
				InterpretedGoal: "interpreted goal",
				BoundTaskCardID: "card-1",
				MaxTurns:        5,
				ProjectID:       "p1",
				CallerAgentID:   callerAgentID,
				PermissionMode:  "auto",
			})
			if err != nil {
				t.Fatalf("handleAgentSpawnAssign must succeed for non-coding %q card with no owner worktree, got: %v", tc.category, err)
			}
			if resp.AgentActorID == "" {
				t.Errorf("expected AgentActorID, got empty")
			}
			// Non-coding card skips the gate — no worktree create, but
			// spawn_agent must still run (worker is registered).
			if *worktreeCreateCalls != 0 {
				t.Errorf("project.worktree_create must not be called for non-coding card, got %d", *worktreeCreateCalls)
			}
			if *spawnCalls != 1 {
				t.Errorf("project.spawn_agent must be called once for non-coding card, got %d", *spawnCalls)
			}
			if got := len(a.Agents); got != 1 {
				t.Errorf("expected exactly 1 worker registered, got %d", got)
			}
			ag := a.Agents[len(a.Agents)-1]
			if ag.Mode == nil {
				t.Fatal("expected worker Mode initialized (bound task card present)")
			}
			if ag.Mode.ActiveWorkflowWorktreeID != "" {
				t.Errorf("Mode.ActiveWorkflowWorktreeID must be empty for non-coding no-worktree spawn, got %q", ag.Mode.ActiveWorkflowWorktreeID)
			}
		})
	}
}

// TestIsCodingTaskCardCategory pins the category decoder in isolation
// so future category additions cannot silently change the gate's
// coding set without a test break.
func TestIsCodingTaskCardCategory(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want bool
	}{
		{"empty raw", "", false},
		{"no data block", "---\ntype: task\nstatus: backlog\n---\nbody", false},
		{"category code", "---\ntype: task\ndata:\n  category: code\n---\nbody", true},
		{"category execute", "---\ntype: task\ndata:\n  category: execute\n---\nbody", true},
		{"category research", "---\ntype: task\ndata:\n  category: research\n---\nbody", false},
		{"category explore", "---\ntype: task\ndata:\n  category: explore\n---\nbody", false},
		{"category review", "---\ntype: task\ndata:\n  category: review\n---\nbody", false},
		{"unknown category", "---\ntype: task\ndata:\n  category: bogus\n---\nbody", false},
		{"nested unrelated key", "---\ntype: task\ndata:\n  other: x\n---\nbody", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCodingTaskCardCategory(tc.raw); got != tc.want {
				t.Errorf("isCodingTaskCardCategory(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// TestWorkerSpawn_WorktreeBranchFromRequest verifies that when the caller
// passes WorktreeBranch in the spawn_assign request, the child worktree's
// branch name is derived from that value instead of the display name.
func TestWorkerSpawn_WorktreeBranchFromRequest(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ownerWorktreeID := "owner-wt-1"
	ownerMapCardID := "map-1"
	childWorktreeID := "child-wt-1"

	var createReq gen.ProjectWorktreeCreateReq
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				if req, ok := payload.(gen.ProjectWorktreeCreateReq); ok {
					createReq = req
				}
				return gen.ProjectWorktree{ID: childWorktreeID, Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_map_owner":
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
		PermissionMode:  "auto",
		WorktreeBranch:  "refactor-panel",
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	// The branch name should start with the caller-supplied slug.
	if !strings.HasPrefix(createReq.Name, "refactor-panel-") {
		t.Errorf("expected branch name prefix \"refactor-panel-\", got %q", createReq.Name)
	}
}

// ── Item 9 (leak #10): Spawn failure discards orphan worktree ──

// TestWorkerSpawn_FailureDiscardsOrphanWorktree verifies that when
// spawnAgentViaProject fails, the child worktree created earlier is
// discarded via project.worktree_discard_by_id to prevent leaks.
func TestWorkerSpawn_FailureDiscardsOrphanWorktree(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ownerWorktreeID := "owner-wt-1"
	ownerMapCardID := "map-1"
	childWorktreeID := "child-wt-1"
	discardCalled := false
	var discardReq gen.ProjectWorktreeDiscardByIDReq
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				return gen.ProjectWorktree{ID: childWorktreeID, Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				return fmt.Errorf("spawn failed: project unavailable")
			case "project.worktree_discard_by_id":
				if req, ok := payload.(gen.ProjectWorktreeDiscardByIDReq); ok {
					discardReq = req
					discardCalled = true
				}
				return gen.ProjectWorktreeDiscardByIDResp{Status: "discarded"}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
	})
	if err == nil {
		t.Fatal("expected spawn failure error, got nil")
	}

	if !discardCalled {
		t.Fatal("expected project.worktree_discard_by_id to be called on spawn failure")
	}
	if discardReq.WorktreeID != childWorktreeID {
		t.Errorf("expected discard WorktreeID %q, got %q", childWorktreeID, discardReq.WorktreeID)
	}
	if !discardReq.Force {
		t.Error("expected discard Force=true")
	}
}

// ── Item 10: Review approve merges child→parent before cascade-delete ──

// TestReviewApprove_MergesChildToParent verifies that reviewApprove calls
// project.worktree_merge_to_parent with the worker's child worktree ID and
// the parent's owner worktree ID before cascade-deleting the agent.
func TestReviewApprove_MergesChildToParent(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	mergeCalled := false
	var mergeReq gen.ProjectWorktreeMergeToParentReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_merge_to_parent":
				if req, ok := payload.(gen.ProjectWorktreeMergeToParentReq); ok {
					mergeReq = req
					mergeCalled = true
				}
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
	if !mergeCalled {
		t.Fatal("expected project.worktree_merge_to_parent to be called")
	}
	if mergeReq.ChildWorktreeID != childWorktreeID {
		t.Errorf("expected ChildWorktreeID %q, got %q", childWorktreeID, mergeReq.ChildWorktreeID)
	}
	if mergeReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected ParentWorktreeID %q, got %q", ownerWorktreeID, mergeReq.ParentWorktreeID)
	}
	// Cascade delete should have marked the agent as deleting.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting after merge + cascade")
	}
}

// ── Item 10 (conflict): Review approve with merge conflict is blocked, not auto-rejected ──

// TestReviewApprove_MergeConflictBlocksApprove verifies that when the merge
// returns a conflict, the approve fails with an error surfaced to the caller
// (the workflow owner): the card is left untouched (stays pending_review),
// the worker is NOT resumed, and the agent is NOT torn down. The owner
// resolves the conflict and retries the approve.
func TestReviewApprove_MergeConflictBlocksApprove(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	var setStatusReqs []gen.WikiSetStatusReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		if aidStr == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "internal_resume_from_review" {
					if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
						resumeReq = req
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_merge_to_parent":
				// Simulate merge conflict: return error.
				return fmt.Errorf("merge branch %q into parent %q: conflict", "child-branch", "parent-branch")
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReqs = append(setStatusReqs, req)
				}
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err == nil {
		t.Fatal("expected approve to fail on merge conflict")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected error to mention the merge conflict, got %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false on merge conflict")
	}
	// The card must be left untouched (stays pending_review): no status
	// change to "done" and no bounce back to "doing".
	for _, r := range setStatusReqs {
		t.Errorf("expected no card status change on merge conflict, got set_status %q", r.Status)
	}
	// The worker must NOT be resumed — conflict resolution is the owner's job.
	if resumeReq.Feedback != "" {
		t.Errorf("expected worker not resumed on merge conflict, got feedback %q", resumeReq.Feedback)
	}
	// Verify the agent was NOT cascade-deleted.
	if a.Agents[0].DeletionStatus == "deleting" {
		t.Error("agent should not be cascade-deleted on merge conflict")
	}
}

// ── Item 11: Review reject rebases child to parent before resume ──

// TestReviewReject_RebasesChildToParent verifies that reviewReject calls
// project.worktree_rebase_to_parent before resuming the worker, and
// prepends a rebase note to the feedback.
func TestReviewReject_RebasesChildToParent(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	rebaseCalled := false
	var rebaseReq gen.ProjectWorktreeRebaseToParentReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		if aidStr == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "internal_resume_from_review" {
					if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
						resumeReq = req
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_rebase_to_parent":
				if req, ok := payload.(gen.ProjectWorktreeRebaseToParentReq); ok {
					rebaseReq = req
					rebaseCalled = true
				}
				return gen.ProjectWorktreeRebaseToParentResp{Status: "rebased"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		Feedback:     "Fix the failing tests",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	if !rebaseCalled {
		t.Fatal("expected project.worktree_rebase_to_parent to be called")
	}
	if rebaseReq.ChildWorktreeID != childWorktreeID {
		t.Errorf("expected ChildWorktreeID %q, got %q", childWorktreeID, rebaseReq.ChildWorktreeID)
	}
	if rebaseReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected ParentWorktreeID %q, got %q", ownerWorktreeID, rebaseReq.ParentWorktreeID)
	}
	// Verify the rebase note was prepended to the feedback.
	if !strings.Contains(resumeReq.Feedback, "rebased to the latest owner branch HEAD") {
		t.Errorf("expected rebase note in feedback, got %q", resumeReq.Feedback)
	}
	if !strings.Contains(resumeReq.Feedback, "Fix the failing tests") {
		t.Errorf("expected original feedback preserved, got %q", resumeReq.Feedback)
	}
}

// ── Item 11 (conflict): Review reject with rebase conflict ──

// TestReviewReject_RebaseConflictPrependsConflictNote verifies that when
// the rebase fails, the conflict note is prepended to the feedback.
func TestReviewReject_RebaseConflictPrependsConflictNote(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		if aidStr == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "internal_resume_from_review" {
					if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
						resumeReq = req
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			switch callID {
			case "project.worktree_rebase_to_parent":
				return fmt.Errorf("rebase conflict: CONFLICT (content): Merge conflict in main.go")
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		Feedback:     "Fix the failing tests",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	if !strings.Contains(resumeReq.Feedback, "Rebase conflict") {
		t.Errorf("expected rebase conflict note in feedback, got %q", resumeReq.Feedback)
	}
	if !strings.Contains(resumeReq.Feedback, "Fix the failing tests") {
		t.Errorf("expected original feedback preserved, got %q", resumeReq.Feedback)
	}
}

// ── Backward compatibility: non-workflow workers skip worktree operations ──

// TestReviewApprove_NonWorkflowWorkerSkipsMerge verifies that when the
// worker has no workflow worktree (Mode.ActiveWorkflowWorktreeID is empty),
// reviewApprove skips the merge and proceeds directly to cascade-delete.
func TestReviewApprove_NonWorkflowWorkerSkipsMerge(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()

	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
			Mode:        &gen.AgentModeState{BoundTaskCardID: "card-1"},
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	mergeCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			switch callID {
			case "project.worktree_merge_to_parent":
				mergeCalled = true
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
	if mergeCalled {
		t.Error("merge should not be called for non-workflow worker")
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Error("expected agent marked deleting (cascade-delete without merge)")
	}
}

// TestReviewReject_NonWorkflowWorkerSkipsRebase verifies that when the
// worker has no workflow worktree, reviewReject skips the rebase.
func TestReviewReject_NonWorkflowWorkerSkipsRebase(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()

	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
			Mode:        &gen.AgentModeState{BoundTaskCardID: "card-1"},
		},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	rebaseCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "internal_resume_from_review":
				if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
					resumeReq = req
				}
				return nil
			case "project.worktree_rebase_to_parent":
				rebaseCalled = true
				return gen.ProjectWorktreeRebaseToParentResp{Status: "rebased"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		Feedback:     "Fix the tests",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	if rebaseCalled {
		t.Error("rebase should not be called for non-workflow worker")
	}
	if resumeReq.Feedback != "Fix the tests" {
		t.Errorf("expected unmodified feedback for non-workflow worker, got %q", resumeReq.Feedback)
	}
}

// ── T8: Full chain — spawn stamp → worker empty status push → approve merge ──

// TestWorkerSpawn_FullChain_StatusPushApproveMerge verifies the complete
// spawn-worker lifecycle in one flow: the owner spawns a worker whose
// Mode.ActiveWorkflowWorktreeID is stamped with the child worktree ID
// (executor_worker_task.go), the worker's ordinary status pushes (empty
// ActiveWorkflowWorktreeID — owner-only field) preserve the stamp (D2 guard,
// workspace.go), and review approve invokes project.worktree_merge_to_parent
// with exactly that child worktree ID before teardown.
func TestWorkerSpawn_FullChain_StatusPushApproveMerge(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	spawnedAgentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ownerWorktreeID := "owner-wt-1"
	ownerMapCardID := "map-1"
	childWorktreeID := "child-wt-1"

	var createReq gen.ProjectWorktreeCreateReq
	mergeCalled := false
	var mergeReq gen.ProjectWorktreeMergeToParentReq
	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}
	cardStatus := map[string]string{"card-1": "backlog"}

	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			// Owner agent: reports its workflow worktree; resolves child slot.
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  ownerMapCardID,
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		// Project actor (and any other aid): worktree/spawn/merge callables.
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_create":
				if req, ok := payload.(gen.ProjectWorktreeCreateReq); ok {
					createReq = req
				}
				return gen.ProjectWorktree{ID: childWorktreeID, Name: "name", Path: "/tmp", Branch: "branch", Status: "active"}
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: spawnedAgentActorID}
			case "project.worktree_merge_to_parent":
				if req, ok := payload.(gen.ProjectWorktreeMergeToParentReq); ok {
					mergeReq = req
					mergeCalled = true
				}
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_map_owner":
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}

	// Phase 1: Owner spawns a worker — child worktree created and stamped.
	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
		PermissionMode:  "auto",
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}
	if resp.AgentActorID != spawnedAgentActorID {
		t.Fatalf("expected AgentActorID %q, got %q", spawnedAgentActorID, resp.AgentActorID)
	}
	if createReq.ParentWorktreeID != ownerWorktreeID || createReq.WorkflowMapID != ownerMapCardID {
		t.Fatalf("expected child worktree created from owner %q (map %q), got parent=%q map=%q",
			ownerWorktreeID, ownerMapCardID, createReq.ParentWorktreeID, createReq.WorkflowMapID)
	}
	worker := agentByActorID(a.Agents, spawnedAgentActorID)
	if worker == nil {
		t.Fatal("spawned worker not found in a.Agents")
	}
	if worker.Mode == nil || worker.Mode.ActiveWorkflowWorktreeID != childWorktreeID {
		t.Fatalf("expected spawn-stamped ActiveWorkflowWorktreeID %q, got %+v", childWorktreeID, worker.Mode)
	}

	// Phase 2: Worker status push with empty worktree ID (owner-only field).
	// The D2 guard must preserve the spawn stamp.
	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: spawnedAgentActorID,
		State:        "running",
		// ActiveWorkflowWorktreeID intentionally empty — worker never owns a workflow.
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	worker = agentByActorID(a.Agents, spawnedAgentActorID)
	if worker == nil || worker.Mode == nil || worker.Mode.ActiveWorkflowWorktreeID != childWorktreeID {
		t.Fatalf("status push clobbered spawn-stamped worktree id: got %+v, want %q", worker.Mode, childWorktreeID)
	}

	// Phase 3: Review approve — merge must be called with the preserved
	// child worktree ID, targeting the owner worktree.
	reviewResp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: spawnedAgentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !reviewResp.Approved {
		t.Fatal("expected Approved=true")
	}
	if !mergeCalled {
		t.Fatal("expected project.worktree_merge_to_parent to be called")
	}
	if mergeReq.ChildWorktreeID != childWorktreeID {
		t.Errorf("expected merge ChildWorktreeID %q, got %q", childWorktreeID, mergeReq.ChildWorktreeID)
	}
	if mergeReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected merge ParentWorktreeID %q, got %q", ownerWorktreeID, mergeReq.ParentWorktreeID)
	}
	// Teardown intent: cascade delete marks the worker deleting.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected worker marked deleting after approve, got %+v", a.Agents)
	}
}

// ── Item 10 (agent owner): approve verifies lineage instead of auto-merging ──

// TestReviewApprove_AgentCallerNotMergedBlocksApprove verifies that when the
// caller is an agent (the workflow owner) and the worker's branch has NOT yet
// been merged into the owner worktree, approve is blocked with an error that
// includes the branch name and git merge guidance. The card stays
// pending_review, the worker is not resumed, the agent is not torn down, and
// no auto-merge is attempted.
func TestReviewApprove_AgentCallerNotMergedBlocksApprove(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}

	// Non-human caller: the agent owner acting through the turn engine.
	ctx.Identity_ = id.Identity{Role: "agent"}

	var verifyReq gen.ProjectWorktreeVerifyMergedReq
	mergeCalled := false
	var setStatusReqs []gen.WikiSetStatusReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_verify_merged_to_parent":
				if req, ok := payload.(gen.ProjectWorktreeVerifyMergedReq); ok {
					verifyReq = req
				}
				return gen.ProjectWorktreeVerifyMergedResp{
					Status:     "not_merged",
					Branch:     "worker/child-1",
					ChildHead:  "abc123",
					ParentHead: "def456",
				}
			case "project.worktree_merge_to_parent":
				mergeCalled = true
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReqs = append(setStatusReqs, req)
				}
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID:  agentActorID,
		Decision:      "approve",
		TaskCardID:    "card-1",
		CallerAgentID: parentAgentID,
	})
	if err == nil {
		t.Fatal("expected approve to fail when the worker branch is not merged")
	}
	if !strings.Contains(err.Error(), "worker/child-1") {
		t.Errorf("expected error to mention the worker branch, got %v", err)
	}
	if !strings.Contains(err.Error(), "not yet merged into your worktree") {
		t.Errorf("expected error to explain the branch is not merged, got %v", err)
	}
	if !strings.Contains(err.Error(), "git merge") {
		t.Errorf("expected error to include git merge guidance, got %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	if verifyReq.ChildWorktreeID != childWorktreeID {
		t.Errorf("expected verify ChildWorktreeID %q, got %q", childWorktreeID, verifyReq.ChildWorktreeID)
	}
	if verifyReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected verify ParentWorktreeID %q, got %q", ownerWorktreeID, verifyReq.ParentWorktreeID)
	}
	if mergeCalled {
		t.Error("agent owner approve must NOT auto-merge")
	}
	// Card stays pending_review: no status change was issued.
	for _, r := range setStatusReqs {
		t.Errorf("expected no card status change on blocked approve, got set_status %q", r.Status)
	}
	// Agent not torn down.
	if a.Agents[0].DeletionStatus == "deleting" {
		t.Error("agent should not be cascade-deleted when approve is blocked")
	}
}

// TestReviewApprove_AgentCallerMergedApproveOk verifies that when the caller
// is an agent (the workflow owner) and the worker's branch HAS already been
// merged into the owner worktree (status "merged"), approve succeeds: card →
// done, agent cascade-deleted (teardown), and no auto-merge is attempted (the
// owner merged manually).
func TestReviewApprove_AgentCallerMergedApproveOk(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.Identity_ = id.Identity{Role: "agent"}

	var verifyReq gen.ProjectWorktreeVerifyMergedReq
	mergeCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_verify_merged_to_parent":
				if req, ok := payload.(gen.ProjectWorktreeVerifyMergedReq); ok {
					verifyReq = req
				}
				return gen.ProjectWorktreeVerifyMergedResp{
					Status:     "merged",
					Branch:     "worker/child-1",
					ChildHead:  "abc123",
					ParentHead: "abc123",
				}
			case "project.worktree_merge_to_parent":
				mergeCalled = true
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID:  agentActorID,
		Decision:      "approve",
		TaskCardID:    "card-1",
		CallerAgentID: parentAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
	if resp.CardStatus != "done" {
		t.Errorf("expected CardStatus 'done', got %q", resp.CardStatus)
	}
	if verifyReq.ChildWorktreeID != childWorktreeID {
		t.Errorf("expected verify ChildWorktreeID %q, got %q", childWorktreeID, verifyReq.ChildWorktreeID)
	}
	if verifyReq.ParentWorktreeID != ownerWorktreeID {
		t.Errorf("expected verify ParentWorktreeID %q, got %q", ownerWorktreeID, verifyReq.ParentWorktreeID)
	}
	if mergeCalled {
		t.Error("agent owner approve must NOT auto-merge")
	}
	// Teardown intent: cascade delete marks the agent deleting.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting after approve, got %+v", a.Agents)
	}
}

// TestReviewApprove_AgentCallerVerifyFailedBlocksApprove verifies that when
// the lineage verification itself fails (internal project error), approve is
// blocked with an error explicitly labeled "verify failed"; nothing is mutated.
func TestReviewApprove_AgentCallerVerifyFailedBlocksApprove(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	childWorktreeID := "child-wt-1"
	ownerWorktreeID := "owner-wt-1"

	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID,
			Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: childWorktreeID},
		},
	}
	ctx.Identity_ = id.Identity{Role: "agent"}

	var setStatusReqs []gen.WikiSetStatusReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == parentAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						ActiveWorkflowMapCardID:  "map-1",
						ActiveWorkflowWorktreeID: ownerWorktreeID,
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_verify_merged_to_parent":
				return fmt.Errorf("internal error: worktree binding missing")
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReqs = append(setStatusReqs, req)
				}
				return nil
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID:  agentActorID,
		Decision:      "approve",
		TaskCardID:    "card-1",
		CallerAgentID: parentAgentID,
	})
	if err == nil {
		t.Fatal("expected approve to fail when verification errors")
	}
	if !strings.Contains(err.Error(), "verify failed") {
		t.Errorf("expected error to be labeled verify failed, got %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	// Card stays pending_review: no status change was issued.
	for _, r := range setStatusReqs {
		t.Errorf("expected no card status change on verify failure, got set_status %q", r.Status)
	}
	if a.Agents[0].DeletionStatus == "deleting" {
		t.Error("agent should not be cascade-deleted on verify failure")
	}
}

// agentByActorID returns the agent ref with the given ActorID, or nil.
func agentByActorID(agents []domain.AgentRef, actorID string) *domain.AgentRef {
	for i := range agents {
		if agents[i].ActorID == actorID {
			return &agents[i]
		}
	}
	return nil
}
