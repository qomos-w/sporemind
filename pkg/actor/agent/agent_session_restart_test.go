package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestCloseOpenStepsForTerminalTurns_AbandonedTurn closes an open reasoning
// step that belongs to a turn marked as abandoned after restart. This reproduces
// the reported bug where the last reasoning frame stayed in running state after
// reloading the agent conversation.
func TestCloseOpenStepsForTerminalTurns_AbandonedTurn(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
				{ID: "turn-2", Role: "assistant", State: "abandoned", Cancelled: true},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
				{ID: "rs-1", Role: "assistant", Type: "reasoning", TurnID: "turn-2", Closed: false,
					ReasoningContent: "thinking..."},
				{ID: "s-1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: false,
					Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
			},
		},
	}
	_ = a.rebuildSteps()

	if a.steps[1].Closed {
		t.Fatalf("reasoning step should still be open before repair")
	}

	a.closeOpenStepsForTerminalTurns()

	if !a.steps[1].Closed {
		t.Errorf("reasoning step in abandoned turn should be closed, got Closed=%v", a.steps[1].Closed)
	}
	if !a.steps[2].Closed {
		t.Errorf("text step in abandoned turn should be closed, got Closed=%v", a.steps[2].Closed)
	}
	if a.stepsGen == 0 {
		t.Errorf("stepsGen should be incremented after repair")
	}
}

func TestCloseOpenStepsForTerminalTurns_LeavesPausedTurnOpen(t *testing.T) {
	a := &Actor{
		Session:    domain.Session{Turns: []domain.Turn{{ID: "turn-paused", Role: "assistant", State: "paused"}}},
		RawSession: domain.RawSession{Steps: []domain.Step{{ID: "s-paused", Role: "assistant", Type: "text", TurnID: "turn-paused", Closed: false}}},
	}
	_ = a.rebuildSteps()
	a.closeOpenStepsForTerminalTurns()
	if a.steps[0].Closed {
		t.Fatal("paused turn step should remain open")
	}
	if a.stepsGen != 0 {
		t.Fatal("paused turn should not increment stepsGen")
	}
}

// TestCloseOpenStepsForTerminalTurns_LeavesRunningTurnOpen verifies that steps
// belonging to a running turn are not touched.
func TestCloseOpenStepsForTerminalTurns_LeavesRunningTurnOpen(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
				{ID: "turn-2", Role: "assistant", State: "running"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true},
				{ID: "rs-1", Role: "assistant", Type: "reasoning", TurnID: "turn-2", Closed: false,
					ReasoningContent: "thinking..."},
			},
		},
	}
	_ = a.rebuildSteps()
	a.closeOpenStepsForTerminalTurns()

	if a.steps[1].Closed {
		t.Errorf("reasoning step in running turn should remain open, got Closed=%v", a.steps[1].Closed)
	}
	if a.stepsGen != 0 {
		t.Errorf("stepsGen should not be incremented when no steps are closed")
	}
}
