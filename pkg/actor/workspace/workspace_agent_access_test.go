package workspace

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleAgentAccessStampsLastAccessedAt(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder", Status: "completed"}}

	// Access before any status push: runtime entry is seeded from the
	// persisted agent state so the projection does not drop to idle.
	state, err := a.handleAgentAccess(ctx, gen.WorkspaceAgentAccessReq{AgentActorID: actorID})
	if err != nil {
		t.Fatalf("handleAgentAccess: %v", err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(state.Items))
	}
	rt := state.Items[0].Runtime
	if rt == nil {
		t.Fatal("runtime state missing after access")
	}
	if rt.LastAccessedAt == "" {
		t.Fatal("LastAccessedAt empty after access")
	}
	if rt.State != "completed" {
		t.Fatalf("State = %q, want persisted %q", rt.State, "completed")
	}
}

func TestHandleAgentStatusUpdatePreservesLastAccessedAt(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder"}}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:        actorID,
		State:               "completed",
		LastTurnCompletedAt: "2026-08-15T10:00:00Z",
	}); err != nil {
		t.Fatalf("status update: %v", err)
	}
	if _, err := a.handleAgentAccess(ctx, gen.WorkspaceAgentAccessReq{AgentActorID: actorID}); err != nil {
		t.Fatalf("handleAgentAccess: %v", err)
	}
	accessedAt := a.agentRuntime[actorID].LastAccessedAt
	if accessedAt == "" {
		t.Fatal("LastAccessedAt empty after access")
	}

	// A later status push must not clobber the workspace-owned access stamp.
	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:        actorID,
		State:               "running",
		LastTurnCompletedAt: "2026-08-15T10:00:00Z",
	}); err != nil {
		t.Fatalf("second status update: %v", err)
	}
	if got := a.agentRuntime[actorID].LastAccessedAt; got != accessedAt {
		t.Fatalf("LastAccessedAt = %q after status push, want preserved %q", got, accessedAt)
	}
}
