package workspace

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Regression for D2 (approve-merge orphaned commits): a worker agent's
// status pushes carry ActiveWorkflowWorktreeID="" because
// RawSession.ActiveWorkflow is owner-only. An unguarded overwrite would
// strip the spawn-stamped child worktree ID, silently skipping the
// merge at review-approve and orphaning the worker's commits.

// TestHandleAgentStatusUpdate_EmptyWorktreeIDDoesNotClobber verifies that
// an ordinary worker status tick (empty ActiveWorkflowWorktreeID) never
// erases the child worktree ID stamped at spawn time.
func TestHandleAgentStatusUpdate_EmptyWorktreeIDDoesNotClobber(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:       "W#1",
		ActorID:  "actor-1",
		AgentKind: domain.AgentKindWorker,
		Mode:     &gen.AgentModeState{ActiveWorkflowWorktreeID: "wt-child-1"},
	}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: "actor-1",
		State:        "running",
		// ActiveWorkflowWorktreeID intentionally empty — worker never owns a workflow.
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if a.Agents[0].Mode.ActiveWorkflowWorktreeID != "wt-child-1" {
		t.Fatalf("worker tick clobbered spawn-stamped worktree id: got %q, want wt-child-1",
			a.Agents[0].Mode.ActiveWorkflowWorktreeID)
	}
}

// TestHandleAgentStatusUpdate_NonEmptyWorktreeIDUpdates verifies the owner
// path still works: a non-empty ActiveWorkflowWorktreeID (owner starting a
// workflow) is accepted and persisted.
func TestHandleAgentStatusUpdate_NonEmptyWorktreeIDUpdates(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:        "W#1",
		ActorID:   "actor-1",
		AgentKind: "coder",
		Mode:      &gen.AgentModeState{},
	}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:             "actor-1",
		State:                    "running",
		ActiveWorkflowWorktreeID: "wt-owner-1",
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if a.Agents[0].Mode.ActiveWorkflowWorktreeID != "wt-owner-1" {
		t.Fatalf("owner workflow worktree id not persisted: got %q, want wt-owner-1",
			a.Agents[0].Mode.ActiveWorkflowWorktreeID)
	}
}

// TestHandleAgentStatusUpdate_OwnerStopClearsWorktreeID verifies that when an
// agent pushes ActiveWorkflowMapCardID="" after previously having a non-empty
// map ID (workflow stopped), the stale ActiveWorkflowWorktreeID is also cleared.
func TestHandleAgentStatusUpdate_OwnerStopClearsWorktreeID(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:        "W#1",
		ActorID:   "actor-owner",
		AgentKind: "coder",
		Mode: &gen.AgentModeState{
			ActiveWorkflowMapCardID:    "wf-test-map",
			ActiveWorkflowWorktreeID:   "wt-owner-1",
		},
	}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: "actor-owner",
		State:        "idle",
		// Both empty — simulates clearWorkflow → notifyWorkspaceStatus.
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if a.Agents[0].Mode.ActiveWorkflowWorktreeID != "" {
		t.Fatalf("expected ActiveWorkflowWorktreeID cleared after workflow stop, got %q",
			a.Agents[0].Mode.ActiveWorkflowWorktreeID)
	}
}

// TestHandleAgentStatusUpdate_SubMapOwnerStopClearsWorktreeID verifies that
// sub-map owners (which have AgentKind=="worker") also get their worktree ID
// cleared on workflow stop. The prevMapID transition guard handles this
// correctly because sub-map owners carry a non-empty MapCardID.
func TestHandleAgentStatusUpdate_SubMapOwnerStopClearsWorktreeID(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:        "W#1",
		ActorID:   "actor-submap-owner",
		AgentKind: domain.AgentKindWorker,
		Mode: &gen.AgentModeState{
			ActiveWorkflowMapCardID:    "wf-sub-map",
			ActiveWorkflowWorktreeID:   "wt-submap-1",
		},
	}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: "actor-submap-owner",
		State:        "idle",
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if a.Agents[0].Mode.ActiveWorkflowWorktreeID != "" {
		t.Fatalf("expected ActiveWorkflowWorktreeID cleared for sub-map owner, got %q",
			a.Agents[0].Mode.ActiveWorkflowWorktreeID)
	}
}

// TestHandleAgentStatusUpdate_WorkerEmptyMapIDDoesNotClobberWorktreeID
// verifies that a worker (which never has a MapCardID) pushing empty
// MapCardID does NOT clear its spawn-stamped worktree ID. This is the
// complement of the D2 guard: prevMapID is "" so the clearing branch
// never fires.
func TestHandleAgentStatusUpdate_WorkerEmptyMapIDDoesNotClobberWorktreeID(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{
		ID:        "W#1",
		ActorID:   "actor-worker",
		AgentKind: domain.AgentKindWorker,
		Mode: &gen.AgentModeState{
			ActiveWorkflowWorktreeID: "wt-child-stamped",
		},
	}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: "actor-worker",
		State:        "running",
	}); err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if a.Agents[0].Mode.ActiveWorkflowWorktreeID != "wt-child-stamped" {
		t.Fatalf("worker tick cleared spawn-stamped worktree id: got %q, want wt-child-stamped",
			a.Agents[0].Mode.ActiveWorkflowWorktreeID)
	}
}
