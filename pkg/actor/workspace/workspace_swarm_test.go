package workspace

import (
	"strings"
	"testing"

	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestAgentSpawnSwarm_SpawnsFullChildWithGoalAndSwarmBundle verifies the happy
// path: the child is spawned as a full agent (no ChildConfig), carries a
// spawn-time goal, the swarm bundle (recursion capability), and the caller as
// parent; the registry entry is stamped with LifecycleScope "swarm".
func TestAgentSpawnSwarm_SpawnsFullChildWithGoalAndSwarmBundle(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	var spawnSink domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, &spawnSink, nil)

	resp, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{
		Description:   "Port the parser",
		Prompt:        "Port parser.go to v2 API and run tests.",
		CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnSwarm: %v", err)
	}

	if resp.ChildActorID != childActorID {
		t.Errorf("ChildActorID = %q, want %q", resp.ChildActorID, childActorID)
	}
	if resp.Depth != 1 {
		t.Errorf("Depth = %d, want 1 (top-level parent is depth 0)", resp.Depth)
	}
	if spawnSink.ChildConfig != nil {
		t.Errorf("ChildConfig must stay nil: a swarm child is a full persistent agent, not a fork child")
	}
	if spawnSink.ParentAgentID != callerAgentID {
		t.Errorf("ParentAgentID = %q, want %q", spawnSink.ParentAgentID, callerAgentID)
	}
	if spawnSink.GoalCondition != "Port parser.go to v2 API and run tests." {
		t.Errorf("GoalCondition = %q, want the request Prompt as the goal", spawnSink.GoalCondition)
	}
	if !strings.Contains(spawnSink.PromptPrelude, callerAgentID) {
		t.Errorf("PromptPrelude must name the parent for the report-back instruction, got %q", spawnSink.PromptPrelude)
	}
	foundSwarm := false
	for _, id := range spawnSink.ExtraBundleIDs {
		if id == agentkit.SwarmBundleID {
			foundSwarm = true
		}
	}
	if !foundSwarm {
		t.Errorf("ExtraBundleIDs = %v, want the swarm bundle so the child can recurse", spawnSink.ExtraBundleIDs)
	}

	var child *domain.AgentRef
	for i := range a.Agents {
		if a.Agents[i].ActorID == childActorID {
			child = &a.Agents[i]
		}
	}
	if child == nil {
		t.Fatal("swarm child missing from the agent registry")
	}
	if child.LifecycleScope != domain.LifecycleScopeSwarm {
		t.Errorf("LifecycleScope = %q, want %q", child.LifecycleScope, domain.LifecycleScopeSwarm)
	}
	if !strings.HasPrefix(child.DisplayName, "Swarm: ") {
		t.Errorf("DisplayName = %q, want the Swarm: prefix", child.DisplayName)
	}
}

// TestAgentSpawnSwarm_DepthCap verifies the recursion guard: an agent already
// at MaxSwarmDepth cannot spawn another level.
func TestAgentSpawnSwarm_DepthCap(t *testing.T) {
	a, ctx := freshActorAnon(t)
	root := genID()
	d1 := genID()
	d2 := genID()
	d3 := genID()
	a.Agents = []domain.AgentRef{
		{ID: root, ActorID: root, ProjectID: "p", AgentKind: "coder"},
		{ID: d1, ActorID: d1, ParentAgentID: root, LifecycleScope: domain.LifecycleScopeSwarm, AgentKind: "general"},
		{ID: d2, ActorID: d2, ParentAgentID: d1, LifecycleScope: domain.LifecycleScopeSwarm, AgentKind: "general"},
		{ID: d3, ActorID: d3, ParentAgentID: d2, LifecycleScope: domain.LifecycleScopeSwarm, AgentKind: "general"},
	}

	if got := a.swarmDepthOf(root); got != 0 {
		t.Errorf("swarmDepthOf(root) = %d, want 0", got)
	}
	if got := a.swarmDepthOf(d3); got != domain.MaxSwarmDepth {
		t.Errorf("swarmDepthOf(d3) = %d, want %d", got, domain.MaxSwarmDepth)
	}

	_, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{
		Description:   "One too many",
		CallerAgentID: d3,
	})
	if err == nil || !strings.Contains(err.Error(), "recursion cap") {
		t.Fatalf("depth-%d caller must be rejected with a recursion cap error, got %v", domain.MaxSwarmDepth, err)
	}
}

// TestAgentSpawnSwarm_ChildCap verifies the per-parent live-children guard.
func TestAgentSpawnSwarm_ChildCap(t *testing.T) {
	a, ctx := freshActorAnon(t)
	caller := genID()
	a.Agents = []domain.AgentRef{{ID: caller, ActorID: caller, ProjectID: "p", AgentKind: "coder"}}
	for i := 0; i < domain.MaxSwarmChildrenPerAgent; i++ {
		a.Agents = append(a.Agents, domain.AgentRef{
			ID: genID(), ActorID: genID(), ParentAgentID: caller,
			LifecycleScope: domain.LifecycleScopeSwarm, AgentKind: "general",
		})
	}
	// A deleting child no longer occupies a slot.
	a.Agents = append(a.Agents, domain.AgentRef{ID: genID(), ParentAgentID: caller, LifecycleScope: domain.LifecycleScopeSwarm, DeletionStatus: "deleting"})

	if got := a.liveSwarmChildrenOf(caller); got != domain.MaxSwarmChildrenPerAgent {
		t.Errorf("liveSwarmChildrenOf = %d, want %d (deleting children are free)", got, domain.MaxSwarmChildrenPerAgent)
	}

	_, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{
		Description:   "Over budget",
		CallerAgentID: caller,
	})
	if err == nil || !strings.Contains(err.Error(), "swarm children") {
		t.Fatalf("caller at the child cap must be rejected, got %v", err)
	}
}

// TestAgentSpawnSwarm_RequestValidation covers the cheap validation failures:
// empty description, read-only kinds, and unknown/unregistered callers.
func TestAgentSpawnSwarm_RequestValidation(t *testing.T) {
	a, ctx := freshActorAnon(t)
	caller := genID()

	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{CallerAgentID: caller}); err == nil {
		t.Fatal("empty Description must be rejected")
	}
	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{Description: "d", AgentKind: "explorer", CallerAgentID: caller}); err == nil ||
		!strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only kind must be rejected, got %v", err)
	}
	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{Description: "d", AgentKind: "worker", CallerAgentID: caller}); err == nil ||
		!strings.Contains(err.Error(), "workflow workers") {
		t.Fatalf("worker kind must be rejected (terminate would strand the child), got %v", err)
	}
	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{Description: "d", AgentKind: "nosuch", CallerAgentID: caller}); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{Description: "d"}); err == nil {
		t.Fatal("missing CallerAgentId must be rejected")
	}
	if _, err := a.handleAgentSpawnSwarm(ctx, domain.WorkspaceAgentSpawnSwarmReq{Description: "d", CallerAgentID: caller}); err == nil {
		t.Fatal("unregistered caller must be rejected")
	}
}

// TestSwarmDepthOf_CycleSafe verifies the parent-chain walk terminates on a
// corrupted (cyclic) registry instead of looping.
func TestSwarmDepthOf_CycleSafe(t *testing.T) {
	a, _ := freshActorAnon(t)
	x, y := genID(), genID()
	a.Agents = []domain.AgentRef{
		{ID: x, ActorID: x, ParentAgentID: y},
		{ID: y, ActorID: y, ParentAgentID: x},
	}
	if got := a.swarmDepthOf(x); got > domain.MaxSwarmDepth+8 {
		t.Errorf("swarmDepthOf on a cyclic chain = %d, must stay bounded", got)
	}
}
