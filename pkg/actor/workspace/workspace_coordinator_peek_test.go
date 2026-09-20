package workspace

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestCoordinatorPeek covers the pure read-only Coordinator query: it answers
// from the agent-list snapshot, includes ActorID only when loaded, and never
// triggers a load.
func TestCoordinatorPeek(t *testing.T) {
	a, ctx := freshActor(t)

	// Empty snapshot → not found.
	resp, err := a.handleCoordinatorPeek(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Found {
		t.Fatal("expected Found=false with no agents")
	}

	// Unloaded coordinator: presence + nickname, no ActorID.
	a.Agents = []domain.AgentRef{{
		ID:          "Coordinator#0001",
		DisplayName: "小明",
		AgentKind:   domain.AgentKindCoordinator,
		Status:      "asleep",
		LoadState:   "unloaded",
	}}
	a.buildAgentListState(true)

	resp, err = a.handleCoordinatorPeek(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true for registered coordinator")
	}
	if resp.Nickname != "小明" {
		t.Errorf("Nickname = %q, want 小明", resp.Nickname)
	}
	if resp.ActorID != "" {
		t.Errorf("ActorID = %q, want empty for unloaded coordinator", resp.ActorID)
	}

	// Loaded coordinator: ActorID included.
	a.Agents[0].Status = "active"
	a.Agents[0].LoadState = "loaded"
	a.Agents[0].ActorID = "coord-actor-1"
	a.buildAgentListState(true)

	resp, err = a.handleCoordinatorPeek(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Found || resp.ActorID != "coord-actor-1" {
		t.Errorf("expected loaded coordinator with ActorID, got %+v", resp)
	}

	// No coordinator among other agents → not found.
	a.Agents = []domain.AgentRef{{
		ID: "Coder#0001", DisplayName: "Coder", AgentKind: domain.AgentKindCoder,
		Status: "active", LoadState: "loaded", ActorID: "coder-1",
	}}
	a.buildAgentListState(true)

	resp, err = a.handleCoordinatorPeek(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Found {
		t.Errorf("expected Found=false without coordinator, got %+v", resp)
	}
}
