package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// A peer message delivered via message_receive must surface in the receiving
// agent's conversation stream. Since T3 the message flows through the standard
// user-message path: on an idle agent it becomes a user turn (Role=user) with
// meta=agent|<id>|<name> distinguishing the agent origin; the timeline renders
// from step events, and flushClosedSteps only persists steps that carry a TurnID.
func TestHandleReceiveMessage_SurfacesInConversationStream(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session:    domain.Session{ActiveHead: -1},
		RawSession: domain.RawSession{NextSeq: 1},
	}

	if err := a.handleReceiveMessage(ctx, domain.AgentMessageReceiveReq{
		FromName: "Coder#0001",
		Text:     "please continue",
	}); err != nil {
		t.Fatalf("handleReceiveMessage: %v", err)
	}

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	step := a.steps[0]
	if step.TurnID == "" {
		t.Errorf("peer message step.TurnID is empty: flushClosedSteps will drop it")
	}
	if step.TurnID != step.ID {
		t.Errorf("peer message step.TurnID = %q, want own turn id %q", step.TurnID, step.ID)
	}
	if !step.Closed {
		t.Errorf("peer message step not closed")
	}
	if step.Meta != "agent||Coder#0001" {
		t.Errorf("peer message step.Meta = %q, want agent||Coder#0001", step.Meta)
	}
	if step.Role != "user" {
		t.Errorf("peer message step.Role = %q, want user (agent message is a user turn distinguished by meta)", step.Role)
	}
	if len(step.Content) != 1 || step.Content[0].Text != "please continue" {
		t.Errorf("peer message step content = %+v", step.Content)
	}

	if len(a.Session.Turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(a.Session.Turns))
	}
	turn := a.Session.Turns[0]
	if turn.ID != step.ID || turn.Role != "user" || turn.TurnOrder == 0 {
		t.Errorf("peer message turn = %+v", turn)
	}

	var opened, closed bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "step" {
			continue
		}
		se, ok := ev.Payload.(domain.StepEvent)
		if !ok {
			t.Fatalf("step event payload type %T", ev.Payload)
		}
		switch se.Kind {
		case "step.opened":
			opened = true
			if se.StepID != step.ID || se.TurnID != step.ID {
				t.Errorf("step.opened ids = %q/%q, want %q/%q", se.StepID, se.TurnID, step.ID, step.ID)
			}
			if se.Role != "user" || se.StepType != "text" {
				t.Errorf("step.opened role/type = %q/%q, want user/text", se.Role, se.StepType)
			}
			if se.Block == nil || se.Block.Text != "please continue" {
				t.Errorf("step.opened block = %+v", se.Block)
			}
		case "step.closed":
			closed = true
			if se.StepID != step.ID {
				t.Errorf("step.closed StepID = %q, want %q", se.StepID, step.ID)
			}
		}
	}
	if !opened {
		t.Errorf("no step.opened event emitted for peer message")
	}
	if !closed {
		t.Errorf("no step.closed event emitted for peer message")
	}
}

// A peer message delivered while the recipient has a crash-recovery paused
// turn (engine gone, record paused, resumable) must queue as a PendingSubmit
// keyed by the paused turn so turn_resume injects it in place. The mailbox
// append and agent_message_received event are preserved.
func TestHandleReceiveMessage_PausedTurnQueuesPendingSubmit(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			ActiveHead: 0,
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "assistant", State: "paused"},
			},
		},
		RawSession:    domain.RawSession{NextSeq: 1},
		ActiveTurnRef: "turn-1",
	}

	if err := a.handleReceiveMessage(ctx, domain.AgentMessageReceiveReq{
		FromAgentID: "sender-42",
		FromName:    "Sender42",
		Text:        "please continue",
	}); err != nil {
		t.Fatalf("handleReceiveMessage: %v", err)
	}

	if len(a.steps) != 0 {
		t.Fatalf("paused-turn path must not create steps, got %d", len(a.steps))
	}
	if len(a.Session.Turns) != 1 {
		t.Fatalf("paused-turn path must not create turns, got %d", len(a.Session.Turns))
	}
	ps := a.pendingSubmits["turn-1"]
	if len(ps) != 1 {
		t.Fatalf("expected 1 pending submit for turn-1, got %d", len(ps))
	}
	if ps[0].Text != "please continue" {
		t.Errorf("pending submit text = %q, want %q", ps[0].Text, "please continue")
	}
	if ps[0].Meta != "agent|sender-42|Sender42" {
		t.Errorf("pending submit meta = %q, want agent|sender-42|Sender42", ps[0].Meta)
	}

	if len(a.mailbox) != 1 {
		t.Fatalf("expected 1 mailbox entry, got %d", len(a.mailbox))
	}
	var eventSeen bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "agent_message_received" {
			eventSeen = true
		}
	}
	if !eventSeen {
		t.Errorf("agent_message_received event not emitted")
	}
}

// A peer message delivered to a workflow owner parked "waiting" must not be
// swallowed by the parked turn. The waiting turn is finalized (waiting →
// completed), the message surfaces as a user turn, and a fresh assistant turn
// is scheduled — mirroring the chat_submit carve-out.
func TestHandleReceiveMessage_WaitingOwnerFinalizesAndStartsTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			ActiveHead: 0,
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", TurnOrder: 1},
				{ID: "turn-2", Role: "assistant", State: domain.TurnStateWaiting, TurnOrder: 2},
			},
		},
		RawSession:    domain.RawSession{NextSeq: 1},
		ActiveTurnRef: "turn-2",
	}
	var afterCalls []string
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		afterCalls = append(afterCalls, callID)
		return nil
	}

	if err := a.handleReceiveMessage(ctx, domain.AgentMessageReceiveReq{
		FromAgentID: "sender-42",
		FromName:    "Sender42",
		Text:        "status update",
	}); err != nil {
		t.Fatalf("handleReceiveMessage: %v", err)
	}

	if got := a.Session.Turns[1].State; got != "completed" {
		t.Errorf("waiting turn state = %q, want completed (finalized for peer message)", got)
	}
	if a.getActiveTurnRef() != "" {
		t.Errorf("ActiveTurnRef = %q, want cleared after waiting finalize", a.getActiveTurnRef())
	}
	if len(a.Session.Turns) != 3 || a.Session.Turns[2].Role != "user" {
		t.Fatalf("expected a new user turn appended, got %+v", a.Session.Turns)
	}
	if got := a.Session.Turns[2].UserInput; got != "status update" {
		t.Errorf("peer user turn input = %q, want %q", got, "status update")
	}
	if len(a.steps) != 1 || a.steps[0].Role != "user" || a.steps[0].Meta != "agent|sender-42|Sender42" {
		t.Fatalf("peer message step = %+v", a.steps)
	}
	if int(a.Session.ActiveHead) != len(a.Session.Turns)-1 {
		t.Errorf("ActiveHead = %d, want %d (peer user turn)", a.Session.ActiveHead, len(a.Session.Turns)-1)
	}
	if len(afterCalls) != 1 || afterCalls[0] != "internal_start_turn" {
		t.Errorf("after calls = %v, want one internal_start_turn", afterCalls)
	}
}

// A peer message delivered while ActiveTurnRef points at a "running" record
// with no live engine (zombie after a crash without recovery pause) must
// surface as a user turn, supersede the zombie record, and schedule a fresh
// turn — the old gate queued it into pendingSubmits where nothing ever
// consumed it.
func TestHandleReceiveMessage_ZombieRunningRecordSurfacesAndStartsTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{
			ActiveHead: 0,
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "assistant", State: "running", TurnOrder: 1},
			},
		},
		RawSession:    domain.RawSession{NextSeq: 1},
		ActiveTurnRef: "turn-1",
	}
	var afterCalls []string
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		afterCalls = append(afterCalls, callID)
		return nil
	}

	if err := a.handleReceiveMessage(ctx, domain.AgentMessageReceiveReq{
		FromAgentID: "sender-42",
		FromName:    "Sender42",
		Text:        "ping",
	}); err != nil {
		t.Fatalf("handleReceiveMessage: %v", err)
	}

	if got := a.Session.Turns[0].State; got != "cancelled" {
		t.Errorf("zombie turn state = %q, want cancelled (superseded)", got)
	}
	userTurns := 0
	for _, turn := range a.Session.Turns {
		if turn.Role == "user" {
			userTurns++
		}
	}
	if userTurns != 1 {
		t.Fatalf("expected 1 peer user turn, got %d (turns=%+v)", userTurns, a.Session.Turns)
	}
	if len(afterCalls) != 1 || afterCalls[0] != "internal_start_turn" {
		t.Errorf("after calls = %v, want one internal_start_turn", afterCalls)
	}
}
