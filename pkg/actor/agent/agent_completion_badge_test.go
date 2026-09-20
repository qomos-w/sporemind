package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestCompletedTurnPushesBadgeFields reproduces the reported "agent completed
// its turn but the avatar/sidebar never shows the green check badge". After a
// normal inline turn completion the agent's workspace status push must carry
// State="completed" and a LastTurnCompletedAt inside the frontend badge window
// (5 min), so hasUnreadCompletion can light up for an unselected agent.
func TestCompletedTurnPushesBadgeFields(t *testing.T) {
	ws := &captureRef{actorID: id.ActorID{}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return ws, true
		}
		return nil, false
	}

	startedAt := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	completedAt := time.Now().UTC().Format(time.RFC3339Nano)

	a := &Actor{
		actorID:       "agent-actor-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
				{ID: "turn-1", Role: "assistant", State: "running", StartedAt: startedAt},
			},
		},
		RawSession: domain.RawSession{NextSeq: 2},
		status:     turnStatus{TurnID: "turn-1", State: "running"},
	}
	defer a.stopStatusNotifications()

	if err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-1",
			Role:        "assistant",
			State:       "completed",
			StartedAt:   startedAt,
			CompletedAt: completedAt,
		},
	}); err != nil {
		t.Fatalf("handleTurnComplete: %v", err)
	}

	// Mirror the inline engine path's final notify (agent_turn.go, after
	// handleTurnComplete returns).
	a.notifyWorkspaceStatus(ctx)
	time.Sleep(150 * time.Millisecond)

	ws.mu.Lock()
	defer ws.mu.Unlock()
	req, ok := ws.lastPayload.(gen.WorkspaceAgentStatusUpdateReq)
	if !ok {
		t.Fatalf("payload type = %T, want WorkspaceAgentStatusUpdateReq", ws.lastPayload)
	}
	if req.State != "completed" {
		t.Fatalf("State = %q, want completed", req.State)
	}
	if req.LastTurnCompletedAt == "" {
		t.Fatal("LastTurnCompletedAt empty in completion push")
	}
	ts, err := time.Parse(time.RFC3339Nano, req.LastTurnCompletedAt)
	if err != nil {
		t.Fatalf("LastTurnCompletedAt %q unparseable: %v", req.LastTurnCompletedAt, err)
	}
	if age := time.Since(ts); age < 0 || age > 5*time.Minute {
		t.Fatalf("LastTurnCompletedAt age = %v, want within the 5m badge window", age)
	}
}
