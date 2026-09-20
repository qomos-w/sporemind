package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// genID (from workspace_cascade_delete_test.go) is reused for unique ActorIDs.

// activeWorkflowLookupFn returns a LookupIDFn that makes the given parentID
// resolve to a live agent with an active workflow (agent_status with
// ActiveWorkflowMapCardID set). Other IDs fall back to the default lookupOK.
func activeWorkflowLookupFn(parentID string) func(id.ActorID) (ref.Ref, bool) {
	return func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == parentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}
}

// ── requireDirectParent authorization logic ───────────────────────────────

// TestRequireDirectParent_HumanBypasses verifies that a human/admin identity
// bypasses the parent check entirely, regardless of CallerAgentId.
func TestRequireDirectParent_HumanBypasses(t *testing.T) {
	_, ctx := freshActor(t) // human ctx
	err := requireDirectParent(ctx, "test", "", "parent-A")
	if err != nil {
		t.Errorf("human with empty CallerAgentId: expected nil, got %v", err)
	}
	err = requireDirectParent(ctx, "test", "anyone", "parent-A")
	if err != nil {
		t.Errorf("human with non-matching CallerAgentId: expected nil, got %v", err)
	}
}

// TestRequireDirectParent_AnonEmptyCallerRejected verifies that an anonymous
// (non-human) caller with an empty CallerAgentId is rejected — this covers
// direct Public invocation bypassing the turn engine.
func TestRequireDirectParent_AnonEmptyCallerRejected(t *testing.T) {
	_, ctx := freshActorAnon(t)
	err := requireDirectParent(ctx, "test", "", "parent-A")
	if err == nil {
		t.Fatal("expected error for anon caller with empty CallerAgentId")
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("expected 'not trusted' error, got %v", err)
	}
}

// TestRequireDirectParent_NonParentRejected verifies that a non-human caller
// whose CallerAgentId does not match the target's parent is rejected.
func TestRequireDirectParent_NonParentRejected(t *testing.T) {
	_, ctx := freshActorAnon(t)
	err := requireDirectParent(ctx, "test", "parent-B", "parent-A")
	if err == nil {
		t.Fatal("expected error for non-parent caller")
	}
	if !strings.Contains(err.Error(), "direct parent") {
		t.Errorf("expected 'direct parent' error, got %v", err)
	}
}

// ── Creation chain: ParentAgentId persistence ──────────────────────────────

// TestSpawnAssign_PersistsParentAgentId verifies that after
// workspace.agent_spawn_assign, the new agent's ParentAgentId equals the
// caller's (parent) actor ID, and the agent list projection shows the child
// under the parent's Children.
func TestSpawnAssign_PersistsParentAgentId(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	agentActorID := genID()
	callerAgentID := genID()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Task body"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Task body"})(payload)
			case "project.wiki_set_status":
				return gen.WikiSetStatusResp{PreviousStatus: "backlog"}
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	// The new agent's ParentAgentId must equal the caller's actor ID.
	child := a.Agents[len(a.Agents)-1]
	if child.ParentAgentID != callerAgentID {
		t.Errorf("expected ParentAgentID %q, got %q", callerAgentID, child.ParentAgentID)
	}
	if child.LifecycleScope != "workflow" {
		t.Errorf("expected LifecycleScope 'workflow', got %q", child.LifecycleScope)
	}

	// The agent list projection must derive the child under the parent.
	children := a.deriveUnifiedChildren(callerAgentID)
	if len(children) != 1 {
		t.Fatalf("expected 1 derived child for parent %q, got %d", callerAgentID, len(children))
	}
	if children[0].ActorID != agentActorID {
		t.Errorf("expected child ActorID %q, got %q", agentActorID, children[0].ActorID)
	}
	if children[0].ParentAgentID != callerAgentID {
		t.Errorf("expected child ParentAgentID %q, got %q", callerAgentID, children[0].ParentAgentID)
	}
	if children[0].LifecycleScope != "workflow" {
		t.Errorf("expected child LifecycleScope 'workflow', got %q", children[0].LifecycleScope)
	}

	// Also verify the child's own list item carries the lifecycle fields.
	state := a.buildAgentListState(true)
	var childItem *gen.AgentListItem
	for i := range state.Items {
		if state.Items[i].ActorID == agentActorID {
			childItem = &state.Items[i]
		}
	}
	if childItem == nil {
		t.Fatal("child agent not found in list state")
	}
	if childItem.ParentAgentID != callerAgentID {
		t.Errorf("expected child item ParentAgentID %q, got %q", callerAgentID, childItem.ParentAgentID)
	}
	if childItem.LifecycleScope != "workflow" {
		t.Errorf("expected child item LifecycleScope 'workflow', got %q", childItem.LifecycleScope)
	}
	if childItem.ConversationTarget != "actor:"+agentActorID {
		t.Errorf("expected ConversationTarget 'actor:%s', got %q", agentActorID, childItem.ConversationTarget)
	}
}

// ── Children derivation from ParentAgentId ─────────────────────────────────

// TestBuildAgentListState_DerivesChildren verifies that unloaded parent and
// child agents retain the parent's Children projection before either actor loads.
// The projection is derived from ParentAgentId rather than persisted separately,
// and copies the child's LifecycleScope.
func TestBuildAgentListState_DerivesChildren(t *testing.T) {
	a, _ := freshActor(t)
	parentActorID := genID()
	childActorID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Parent#0001", ActorID: parentActorID, AgentKind: domain.AgentKindCoder, LifecycleScope: "workflow", LoadState: "unloaded"},
		{ID: "Worker#0001", ActorID: childActorID, AgentKind: domain.AgentKindWorker, ParentAgentID: parentActorID, LifecycleScope: "workflow", LoadState: "unloaded"},
	}

	state := a.buildAgentListState(true)

	var parentItem, childItem *gen.AgentListItem
	for i := range state.Items {
		switch state.Items[i].ActorID {
		case parentActorID:
			parentItem = &state.Items[i]
		case childActorID:
			childItem = &state.Items[i]
		}
	}
	if parentItem == nil {
		t.Fatal("expected parent in list state")
	}
	if childItem == nil {
		t.Fatal("expected child in list state")
	}

	// Parent's Children must include the child with matching LifecycleScope.
	if len(parentItem.Children) != 1 {
		t.Fatalf("expected 1 child projection on parent, got %d", len(parentItem.Children))
	}
	if parentItem.Children[0].ActorID != childActorID {
		t.Errorf("expected child ActorID %q, got %q", childActorID, parentItem.Children[0].ActorID)
	}
	if parentItem.Children[0].LifecycleScope != "workflow" {
		t.Errorf("expected child LifecycleScope copied from agent, got %q", parentItem.Children[0].LifecycleScope)
	}

	// Child carries its own ParentAgentId.
	if childItem.ParentAgentID != parentActorID {
		t.Errorf("expected child ParentAgentID %q, got %q", parentActorID, childItem.ParentAgentID)
	}

	// Child must not have children of its own (leaf node).
	if len(childItem.Children) != 0 {
		t.Errorf("expected leaf child to have 0 children, got %d", len(childItem.Children))
	}
}

// TestBuildAgentListState_NoChildrenForRootAgents verifies that agents with no
// ParentAgentId (root agents) are not projected as children of anyone.
func TestBuildAgentListState_NoChildrenForRootAgents(t *testing.T) {
	a, _ := freshActor(t)
	rootA := genID()
	rootB := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: rootA, AgentKind: domain.AgentKindCoder},
		{ID: "Coder#0002", ActorID: rootB, AgentKind: domain.AgentKindCoder},
	}

	state := a.buildAgentListState(true)
	for _, item := range state.Items {
		if len(item.Children) != 0 {
			t.Errorf("expected 0 children for root agent %q, got %d", item.ActorID, len(item.Children))
		}
		if item.ParentAgentID != "" {
			t.Errorf("expected empty ParentAgentID for root agent %q, got %q", item.ActorID, item.ParentAgentID)
		}
	}
}

// ── Authorization: terminate ───────────────────────────────────────────────

// TestTerminate_RejectsAnonEmptyCaller verifies that a direct Public call
// (anon identity, empty CallerAgentId) is rejected before any state
// modification — the empty-string bypass is gone.
func TestTerminate_RejectsAnonEmptyCaller(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	_, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
		// CallerAgentID intentionally empty — direct Public invocation
	})
	if err == nil {
		t.Fatal("expected error for anon empty-caller terminate")
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("expected 'not trusted' error, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent preserved after rejected terminate, got %d agents", len(a.Agents))
	}
}

// TestTerminate_RejectsNonParent verifies that an agent caller (non-empty
// CallerAgentId) that is NOT the direct parent is rejected before any state
// modification.
func TestTerminate_RejectsNonParent(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	otherAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.LookupIDFn = activeWorkflowLookupFn(otherAgentID)

	_, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: otherAgentID, // not the parent
	})
	if err == nil {
		t.Fatal("expected error for non-parent terminate")
	}
	if !strings.Contains(err.Error(), "direct parent") {
		t.Errorf("expected 'direct parent' error, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent preserved after rejected terminate, got %d agents", len(a.Agents))
	}
}

// TestTerminate_AllowsDirectParent verifies that the direct parent agent (non-
// human identity, CallerAgentId matching the target's ParentAgentId, active
// workflow) can terminate its child.
func TestTerminate_AllowsDirectParent(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.LookupIDFn = activeWorkflowLookupFn(parentID)

	resp, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: parentID,
	})
	if err != nil {
		t.Fatalf("expected direct parent to terminate, got %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
}

// TestTerminate_AllowsHumanUI verifies that a human UI call (trusted human
// identity via ctx) can still terminate any worker agent.
func TestTerminate_AllowsHumanUI(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
		// CallerAgentID intentionally empty — human UI path
	})
	if err != nil {
		t.Fatalf("expected human UI terminate to succeed, got %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
}

// ── Authorization: review ──────────────────────────────────────────────────

// TestReview_RejectsAnonEmptyCaller verifies that a direct Public call (anon
// identity, empty CallerAgentId) is rejected before any card/agent state
// modification.
func TestReview_RejectsAnonEmptyCaller(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		// CallerAgentID intentionally empty — direct Public invocation
	})
	if err == nil {
		t.Fatal("expected error for anon empty-caller review")
	}
	if !strings.Contains(err.Error(), "not trusted") {
		t.Errorf("expected 'not trusted' error, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent preserved after rejected review, got %d agents", len(a.Agents))
	}
}

// TestReview_RejectsNonParent verifies that a non-parent agent caller is
// rejected before any card or agent state modification.
func TestReview_RejectsNonParent(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	otherAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.LookupIDFn = activeWorkflowLookupFn(otherAgentID)

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID:  agentActorID,
		Decision:      "approve",
		CallerAgentID: otherAgentID, // not the parent
	})
	if err == nil {
		t.Fatal("expected error for non-parent review")
	}
	if !strings.Contains(err.Error(), "direct parent") {
		t.Errorf("expected 'direct parent' error, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent preserved after rejected review, got %d agents", len(a.Agents))
	}
}

// TestReview_AllowsDirectParent verifies that the direct parent agent (non-
// human identity, CallerAgentId matching, active workflow) can review
// (approve) its child.
func TestReview_AllowsDirectParent(t *testing.T) {
	a, ctx := freshActorAnon(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }
	ctx.LookupIDFn = activeWorkflowLookupFn(parentID)

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID:  agentActorID,
		Decision:      "approve",
		CallerAgentID: parentID,
	})
	if err != nil {
		t.Fatalf("expected direct parent review to succeed, got %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
}

// TestReview_AllowsHumanUI verifies that a human UI call (trusted human
// identity via ctx) can still review any agent.
func TestReview_AllowsHumanUI(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	parentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindWorker,
			ParentAgentID: parentID,
		},
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		// CallerAgentID intentionally empty — human UI path
	})
	if err != nil {
		t.Fatalf("expected human UI review to succeed, got %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false for reject")
	}
}
