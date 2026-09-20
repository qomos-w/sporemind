package workspace

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestCompletedAgentListStateCarriesBadgeFields closes the agent→workspace
// seam for the unread-completion badge: a live agent's terminal push carries
// State="completed" plus a UTC RFC3339Nano LastTurnCompletedAt (what the turn
// engine now produces); the emitted agent_list_state item must surface both
// with LastAccessedAt empty, which is exactly the combination hasUnreadCompletion
// needs to show the green check for an unselected agent.
func TestCompletedAgentListStateCarriesBadgeFields(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder", Status: "running"}}

	// Turn engine now stamps CompletedAt as UTC RFC3339Nano. Mirror that exactly.
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)

	state, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID:        actorID,
		State:               "completed",
		LastTurnCompletedAt: completedAt,
	})
	if err != nil {
		t.Fatalf("handleAgentStatusUpdate: %v", err)
	}
	if len(state.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(state.Items))
	}
	rt := state.Items[0].Runtime
	if rt == nil {
		t.Fatal("runtime missing after completion push")
	}
	if rt.State != "completed" {
		t.Fatalf("State = %q, want completed", rt.State)
	}
	if rt.LastTurnCompletedAt == "" {
		t.Fatal("LastTurnCompletedAt dropped from projection")
	}
	ts, err := time.Parse(time.RFC3339Nano, rt.LastTurnCompletedAt)
	if err != nil {
		t.Fatalf("LastTurnCompletedAt %q unparseable: %v", rt.LastTurnCompletedAt, err)
	}
	if age := time.Since(ts); age < 0 || age > 5*time.Minute {
		t.Fatalf("LastTurnCompletedAt age = %v, want within the 5m badge window", age)
	}
	if rt.LastAccessedAt != "" {
		t.Fatalf("LastAccessedAt = %q, want empty for a never-viewed agent", rt.LastAccessedAt)
	}
}
