package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRecoverTurnStatus_RestoresFailed verifies that after a restart, an agent
// whose last turn ended in "failed" (with no active turn) recovers the failed
// status. Without recovery a.status.State stays "" and the frontend shows idle,
// hiding the error.
func TestRecoverTurnStatus_RestoresFailed(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "failed"},
			},
		},
		// ActiveTurnRef empty — a failed turn clears it (agent_turn.go:1088).
	}

	if a.recoverTurnStatus() {
		t.Fatal("should not report mutation when only deriving failed state")
	}
	if a.status.State != "failed" {
		t.Fatalf("status.State = %q, want %q", a.status.State, "failed")
	}
}

// TestRecoverTurnStatus_LeavesCompletedIdle verifies that a completed last turn
// does NOT set a non-idle status. A completed agent is effectively idle.
func TestRecoverTurnStatus_LeavesCompletedIdle(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "completed"},
			},
		},
	}

	a.recoverTurnStatus()
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle) for completed turn", a.status.State)
	}
}

// TestRecoverTurnStatus_PausedFromActiveTurn verifies that a running turn with
// an ActiveTurnRef is recovered as paused/recovery via the canonical lifecycle
// reducer, and that the session mutation is reported so the caller persists it.
func TestRecoverTurnStatus_PausedFromActiveTurn(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "running"},
			},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (running turn → paused)")
	}
	if a.status.State != "paused" {
		t.Fatalf("status.State = %q, want %q", a.status.State, "paused")
	}
	if a.status.PauseKind != "recovery" {
		t.Fatalf("status.PauseKind = %q, want recovery", a.status.PauseKind)
	}
	turn := a.Session.Turns[1]
	if turn.State != "paused" {
		t.Fatalf("turn state = %q, want %q", turn.State, "paused")
	}
	if turn.PauseReason != "recovery" {
		t.Fatalf("turn PauseReason = %q, want recovery", turn.PauseReason)
	}
	if turn.Error != "" {
		t.Fatalf("turn Error = %q, want empty", turn.Error)
	}
	if turn.Revision != 1 {
		t.Fatalf("turn Revision = %d, want 1", turn.Revision)
	}
	if turn.CompletedAt != "" {
		t.Fatalf("turn CompletedAt = %q, want empty", turn.CompletedAt)
	}
}

// TestRecoverTurnStatus_RunningWithoutActiveRef verifies that a persisted
// running turn is rebound and converted to paused/recovery instead of remaining
// visible as running after restart.
func TestRecoverTurnStatus_RunningWithoutActiveRef(t *testing.T) {
	a := &Actor{
		Session: domain.Session{Turns: []domain.Turn{
			{ID: "t1", Role: "user", State: "completed"},
			{ID: "t2", Role: "assistant", State: "running", TurnOrder: 2},
		}},
	}

	if !a.recoverTurnStatus() {
		t.Fatal("should report mutation (unreferenced running turn → paused/recovery)")
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2", a.ActiveTurnRef)
	}
	turn := a.Session.Turns[1]
	if turn.State != domain.TurnStatePaused || turn.PauseReason != domain.PauseReasonRecovery {
		t.Fatalf("turn = %+v, want paused/recovery", turn)
	}
	if a.status.State != domain.TurnStatePaused || a.status.PauseKind != domain.PauseReasonRecovery {
		t.Fatalf("status = %+v, want paused/recovery", a.status)
	}
}

// TestRecoverTurnStatus_RestoresPausedTurnWithoutActiveRef verifies that a
// user-paused turn remains resumable when the runtime ref was not persisted.
func TestRecoverTurnStatus_RestoresPausedTurnWithoutActiveRef(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2},
			},
		},
	}

	if a.recoverTurnStatus() {
		t.Fatal("should not report mutation when restoring an already paused turn")
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2", a.ActiveTurnRef)
	}
	if a.status.State != "paused" || a.status.TurnID != "t2" {
		t.Fatalf("status = %#v, want paused t2", a.status)
	}
}

// TestRecoverTurnStatus_AlreadyPausedTurn verifies that a turn already marked
// paused (not running) still sets status paused but does not report mutation.
func TestRecoverTurnStatus_AlreadyPausedTurn(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused"},
			},
		},
	}

	if a.recoverTurnStatus() {
		t.Fatal("should not report mutation when turn is already paused")
	}
	if a.status.State != "paused" {
		t.Fatalf("status.State = %q, want %q", a.status.State, "paused")
	}
}

// TestRecoverTurnStatus_UnknownToAbandoned verifies that a turn in an unknown or
// non-canonical state is abandoned on restart with a diagnostic Error and the
// active turn ref is cleared.
func TestRecoverTurnStatus_UnknownToAbandoned(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "weird_state"},
			},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (unknown state → abandoned)")
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if a.status.State != "abandoned" {
		t.Fatalf("status.State = %q, want abandoned", a.status.State)
	}
	turn := a.Session.Turns[1]
	if turn.State != "abandoned" {
		t.Fatalf("turn state = %q, want abandoned", turn.State)
	}
	if turn.Error == "" {
		t.Fatalf("turn Error should contain diagnostic, got empty")
	}
	if turn.PauseReason != "" {
		t.Fatalf("turn PauseReason = %q, want empty for abandoned", turn.PauseReason)
	}
	if turn.Revision != 1 {
		t.Fatalf("turn Revision = %d, want 1", turn.Revision)
	}
	if turn.CompletedAt == "" {
		t.Fatalf("turn CompletedAt should be set for abandoned")
	}
}

// TestRecoverTurnStatus_TerminalCannotRevive verifies that a terminal turn is
// never revived to a different state. For completed/cancelled/abandoned the stale
// ActiveTurnRef is cleared; for failed the ref is retained for inspection but the
// state is left untouched.
func TestRecoverTurnStatus_TerminalCannotRevive(t *testing.T) {
	states := []struct {
		state       string
		clearRef    bool
		statusState string
	}{
		{"completed", true, ""},
		{"cancelled", true, ""},
		{"abandoned", true, ""},
		{"failed", false, "failed"},
	}
	for _, tc := range states {
		t.Run(tc.state, func(t *testing.T) {
			turn := domain.Turn{ID: "t2", Role: "assistant", State: tc.state}
			if tc.state == "failed" {
				turn.Error = "boom"
			}
			a := &Actor{
				ActiveTurnRef: "t2",
				Session: domain.Session{Turns: []domain.Turn{
					{ID: "t1", Role: "user", State: "completed"},
					turn,
				}},
			}
			mutated := a.recoverTurnStatus()
			if a.Session.Turns[1].State != tc.state {
				t.Fatalf("turn state mutated from %q to %q", tc.state, a.Session.Turns[1].State)
			}
			if a.ActiveTurnRef != "" && !tc.clearRef {
				// failed keeps the ref for inspection.
			} else if a.ActiveTurnRef != "" && tc.clearRef {
				t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
			} else if a.ActiveTurnRef == "" && !tc.clearRef {
				t.Fatalf("ActiveTurnRef = %q, want t2 for failed turn inspection", a.ActiveTurnRef)
			}
			if tc.statusState != "" && a.status.State != tc.statusState {
				t.Fatalf("status.State = %q, want %q", a.status.State, tc.statusState)
			}
			if tc.statusState == "" && a.status.State != "" {
				t.Fatalf("status.State = %q, want empty", a.status.State)
			}
			if mutated != tc.clearRef {
				t.Fatalf("mutated = %v, want %v", mutated, tc.clearRef)
			}
		})
	}
}

// TestRecoverTurnStatus_EmptySession verifies no panic and idle state when there
// are no turns at all.
func TestRecoverTurnStatus_EmptySession(t *testing.T) {
	a := &Actor{}
	a.recoverTurnStatus()
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty", a.status.State)
	}
}

// TestRecoverPendingInteraction_RestoresFlags verifies that each interaction
// type restores its corresponding blocking flag after a restart, drives the
// associated running turn to paused/interaction, and returns true so the caller
// persists the canonicalized record.
func TestRecoverPendingInteraction_RestoresFlags(t *testing.T) {
	cases := []struct {
		name         string
		typ          string
		wantAsk      bool
		wantPerm     bool
		wantGoal     bool
		wantGoalReq  string
		wantGoalCard bool
		wantCardReq  string
	}{
		{"ask_user", "ask_user", true, false, false, "", false, ""},
		{"permission", "permission", false, true, false, "", false, ""},
		{"goal_submit", "goal_submit", false, false, true, "req-g1", false, ""},
		{"goal_card_submit", "goal_card_submit", false, false, false, "", true, "req-g1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := testutil.HumanCtx(testutil.GenActorID())
			a := &Actor{
				Session: domain.Session{Turns: []domain.Turn{
					{ID: "t1", Role: "assistant", State: "running"},
				}},
				pendingInteraction: &pendingInteractionState{Type: c.typ, RequestID: "req-g1", TurnID: "t1"},
			}
			persist := a.recoverPendingInteraction(ctx)
			if !persist {
				t.Errorf("recoverPendingInteraction = false, want true (record canonicalized)")
			}
			if a.pendingAskUser != c.wantAsk {
				t.Errorf("pendingAskUser = %v, want %v", a.pendingAskUser, c.wantAsk)
			}
			if a.pendingApproval != c.wantPerm {
				t.Errorf("pendingApproval = %v, want %v", a.pendingApproval, c.wantPerm)
			}
			if a.goalSubmitPending != c.wantGoal {
				t.Errorf("goalSubmitPending = %v, want %v", a.goalSubmitPending, c.wantGoal)
			}
			if c.wantGoal && a.goalSubmitRequestID != c.wantGoalReq {
				t.Errorf("goalSubmitRequestID = %q, want %q", a.goalSubmitRequestID, c.wantGoalReq)
			}
			if a.goalCardSubmitPending != c.wantGoalCard {
				t.Errorf("goalCardSubmitPending = %v, want %v", a.goalCardSubmitPending, c.wantGoalCard)
			}
			if c.wantGoalCard && a.goalCardSubmitRequestID != c.wantCardReq {
				t.Errorf("goalCardSubmitRequestID = %q, want %q", a.goalCardSubmitRequestID, c.wantCardReq)
			}
			if a.status.State != "paused" {
				t.Errorf("status.State = %q, want paused (interaction wait)", a.status.State)
			}
			if a.status.PauseKind != "interaction" {
				t.Errorf("status.PauseKind = %q, want interaction", a.status.PauseKind)
			}
			var found bool
			for _, turn := range a.Session.Turns {
				if turn.ID == "t1" {
					found = true
					if turn.State != "paused" || turn.PauseReason != "interaction" {
						t.Errorf("turn %s state = %q/%q, want paused/interaction", turn.ID, turn.State, turn.PauseReason)
					}
					if turn.Revision != 1 {
						t.Errorf("turn.Revision = %d, want 1", turn.Revision)
					}
				}
			}
			if !found {
				t.Fatalf("turn t1 not found in Session.Turns")
			}
		})
	}
}

// TestRecoverPendingInteraction_NilIsNoop verifies a nil record does nothing.
func TestRecoverPendingInteraction_NilIsNoop(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{}
	a.recoverPendingInteraction(ctx)
	if a.pendingAskUser || a.pendingApproval || a.goalSubmitPending || a.status.State != "" {
		t.Fatalf("nil pendingInteraction should not mutate any state; status=%q", a.status.State)
	}
}

// TestRecoverPendingInteraction_PreservesFailedStatus verifies that when a
// pending interaction coincides with a failed turn, the interaction is treated as
// stale and cleared, and the existing failed status is not overwritten.
func TestRecoverPendingInteraction_PreservesFailedStatus(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		Session: domain.Session{Turns: []domain.Turn{
			{ID: "t1", Role: "assistant", State: "failed", Error: "boom"},
		}},
		pendingInteraction: &pendingInteractionState{Type: "ask_user", TurnID: "t1"},
		status:             turnStatus{State: "failed"},
	}
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("expected stale interaction to be cleared and persisted")
	}
	if a.pendingInteraction != nil {
		t.Fatalf("pendingInteraction should be cleared for failed turn")
	}
	if a.status.State != "failed" {
		t.Fatalf("status.State = %q, want failed (not overwritten by paused/interaction)", a.status.State)
	}
}

// TestRecoverPendingInteraction_PausesInteraction mirrors the OnStart restart path
// for a turn that was blocked on a user interaction when the process exited. The
// agent must come back as paused/interaction (status "paused" + interaction flags),
// not running, so the reloaded conversation presents the interaction UI and the
// canonical record carries PauseReason=interaction.
func TestRecoverPendingInteraction_PausesInteraction(t *testing.T) {
	cases := []string{"ask_user", "permission", "plan_approval", "goal_submit", "goal_card_submit"}
	for _, typ := range cases {
		t.Run(typ, func(t *testing.T) {
			ctx := testutil.HumanCtx(testutil.GenActorID())
			a := &Actor{
				Session: domain.Session{Turns: []domain.Turn{
					{ID: "t1", Role: "assistant", State: "running"},
				}},
				pendingInteraction: &pendingInteractionState{Type: typ, RequestID: "r1", TurnID: "t1"},
			}
			// recoverTurnStatus with a pending-interaction turn should skip it.
			if a.recoverTurnStatus() {
				t.Fatal("recoverTurnStatus should not mutate a pending-interaction turn")
			}
			if a.status.State != "" {
				t.Fatalf("status.State = %q, want empty before recoverPendingInteraction", a.status.State)
			}
			if a.recoverPendingInteraction(ctx) {
				// Session.Turns was canonicalized to paused/interaction.
			}
			if a.status.State != "paused" {
				t.Fatalf("status.State = %q, want paused (interaction wait)", a.status.State)
			}
			if a.status.PauseKind != "interaction" {
				t.Fatalf("status.PauseKind = %q, want interaction", a.status.PauseKind)
			}
			if a.ActiveTurnRef != "t1" {
				t.Fatalf("ActiveTurnRef = %q, want t1", a.ActiveTurnRef)
			}
		})
	}
}

// TestRecoverTurnStatus_PendingInteractionBecomesPaused verifies that when
// ActiveTurnRef resolves to the blocked turn (the less common restart case where
// the ref survived), recoverTurnStatus does not touch it because the pending
// interaction is canonicalized as paused/interaction by recoverPendingInteraction.
func TestRecoverTurnStatus_PendingInteractionBecomesPaused(t *testing.T) {
	a := &Actor{
		ActiveTurnRef:      "t2",
		pendingInteraction: &pendingInteractionState{Type: "ask_user", TurnID: "t2"},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "running"},
			},
		},
	}
	if a.recoverTurnStatus() {
		t.Fatal("should not report mutation when a pending-interaction turn is skipped")
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty before recoverPendingInteraction", a.status.State)
	}
	if a.Session.Turns[1].State != "running" {
		t.Fatalf("turn state = %q, want running (not touched by recoverTurnStatus)", a.Session.Turns[1].State)
	}
}

// TestRecoverTurnStatus_CompletedTurnClearsActiveRef verifies that a turn that
// already finished before the restart does not leave the agent stuck as paused.
// The stale ActiveTurnRef is cleared and the agent reports idle.
func TestRecoverTurnStatus_CompletedTurnClearsActiveRef(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "completed"},
			},
		},
	}
	if !a.recoverTurnStatus() {
		t.Fatal("should report mutation (clearing stale ActiveTurnRef)")
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle) for completed turn", a.status.State)
	}
	if a.Session.Turns[1].State != "completed" {
		t.Fatalf("turn state = %q, want completed (must not be rewritten)", a.Session.Turns[1].State)
	}
}

// TestRecoverTurnStatus_FailedTurnKeepsFailed verifies that a failed turn with a
// surviving ActiveTurnRef is recovered as failed, not paused.
func TestRecoverTurnStatus_FailedTurnKeepsFailed(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "failed", Error: "boom"},
			},
		},
	}
	if a.recoverTurnStatus() {
		t.Fatal("should not report mutation when only deriving failed state")
	}
	if a.status.State != "failed" {
		t.Fatalf("status.State = %q, want failed", a.status.State)
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2 (failed turn retains ref for inspection)", a.ActiveTurnRef)
	}
}

// TestRecoverTurnStatus_MissingActiveTurnRefCleared verifies that an ActiveTurnRef
// pointing to a turn that no longer exists (e.g. compaction/crash) is cleared.
func TestRecoverTurnStatus_MissingActiveTurnRefCleared(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "missing",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
			},
		},
	}
	if !a.recoverTurnStatus() {
		t.Fatal("should report mutation (clearing phantom ActiveTurnRef)")
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle)", a.status.State)
	}
}

// TestRecoverPendingInteraction_ClearsStaleCompletedTurn verifies that a
// pendingInteraction whose turn already finished is treated as stale and cleared,
// so the agent does not appear stuck waiting for input after restart.
func TestRecoverPendingInteraction_ClearsStaleCompletedTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		pendingInteraction: &pendingInteractionState{Type: "ask_user", TurnID: "t2"},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "completed"},
			},
		},
	}
	a.recoverTurnStatus()
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("should report mutation (clearing stale pendingInteraction)")
	}
	if a.pendingInteraction != nil {
		t.Fatalf("pendingInteraction should be cleared for completed turn")
	}
	if a.pendingAskUser {
		t.Fatalf("pendingAskUser should be false")
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle)", a.status.State)
	}
}

// TestRecoverPendingInteraction_ClearsStaleFailedTurn verifies that a
// pendingInteraction whose turn failed is treated as stale and cleared, keeping
// the failed status.
func TestRecoverPendingInteraction_ClearsStaleFailedTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		pendingInteraction: &pendingInteractionState{Type: "ask_user", TurnID: "t2"},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "failed"},
			},
		},
	}
	a.recoverTurnStatus()
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("should report mutation (clearing stale pendingInteraction)")
	}
	if a.pendingInteraction != nil {
		t.Fatalf("pendingInteraction should be cleared for failed turn")
	}
	if a.status.State != "failed" {
		t.Fatalf("status.State = %q, want failed", a.status.State)
	}
}

// TestRecoverPendingInteraction_ClearsStaleNoTurnID verifies that an old
// pendingInteraction record without a TurnID is cleared when there is no active turn
// and the last turn is already terminal, so the agent doesn't spin forever.
func TestRecoverPendingInteraction_ClearsStaleNoTurnID(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		pendingInteraction: &pendingInteractionState{Type: "ask_user"},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "completed"},
			},
		},
	}
	a.recoverTurnStatus()
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("should report mutation (clearing stale pendingInteraction)")
	}
	if a.pendingInteraction != nil {
		t.Fatalf("pendingInteraction should be cleared")
	}
	if a.pendingAskUser {
		t.Fatalf("pendingAskUser should be false")
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle)", a.status.State)
	}
}

// TestRecover_StaleInteractionWithSurvivingActiveRef guards the crash-window
// edge case where BOTH a pendingInteraction record AND an ActiveTurnRef survived
// to disk, but the referenced turn is already terminal (resolved before the
// crash, mailbox save lost). recoverTurnStatus must NOT keep the agent showing
// "running" on a terminal turn: a completed turn reports idle and clears the
// stale ref; a failed turn reports failed and retains its ref.
func TestRecover_StaleInteractionWithSurvivingActiveRef(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	cases := []struct {
		name       string
		turnState  string
		wantStatus string
		wantActive string // expected ActiveTurnRef after recovery
	}{
		{"completed", "completed", "", ""},
		{"failed", "failed", "failed", "t2"},
		{"cancelled", "cancelled", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Actor{
				ActiveTurnRef:      "t2",
				pendingInteraction: &pendingInteractionState{Type: "ask_user", TurnID: "t2"},
				Session: domain.Session{
					Turns: []domain.Turn{
						{ID: "t1", Role: "user", State: "completed"},
						{ID: "t2", Role: "assistant", State: c.turnState},
					},
				},
			}
			a.recoverTurnStatus()
			a.recoverPendingInteraction(ctx)

			if a.status.State != c.wantStatus {
				t.Errorf("status.State = %q, want %q", a.status.State, c.wantStatus)
			}
			if a.ActiveTurnRef != c.wantActive {
				t.Errorf("ActiveTurnRef = %q, want %q", a.ActiveTurnRef, c.wantActive)
			}
			if a.pendingInteraction != nil {
				t.Errorf("pendingInteraction should be cleared for terminal turn")
			}
			if a.pendingAskUser {
				t.Errorf("pendingAskUser should be false")
			}
		})
	}
}

// TestRecoverTurnStatus_WaitingWithWorkflowActive verifies that a waiting turn
// in a workflow-active agent is recovered as paused/recovery with the turn's
// CompletedAt preserved, so turn_resume can restore waiting.
func TestRecoverTurnStatus_WaitingWithWorkflowActive(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "waiting", CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (waiting turn → paused)")
	}
	if a.status.State != "paused" {
		t.Fatalf("status.State = %q, want %q", a.status.State, "paused")
	}
	if a.status.PauseKind != "recovery" {
		t.Fatalf("status.PauseKind = %q, want recovery", a.status.PauseKind)
	}
	if a.status.TurnID != "t2" {
		t.Fatalf("status.TurnID = %q, want t2", a.status.TurnID)
	}
	turn := a.Session.Turns[1]
	if turn.State != "paused" {
		t.Fatalf("turn state = %q, want %q", turn.State, "paused")
	}
	if turn.PauseReason != "recovery" {
		t.Fatalf("turn PauseReason = %q, want recovery", turn.PauseReason)
	}
	// CompletedAt must be preserved from the waiting state.
	if turn.CompletedAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("turn CompletedAt = %q, want 2025-01-01T00:00:00Z", turn.CompletedAt)
	}
	if turn.Revision != 1 {
		t.Fatalf("turn Revision = %d, want 1", turn.Revision)
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2", a.ActiveTurnRef)
	}
}

// TestRecoverTurnStatus_WaitingWithoutWorkflowActive verifies that a waiting
// turn in a non-workflow agent is finalized to completed with the active ref
// cleared, since non-workflow agents should not stay blocked on a waiting turn.
func TestRecoverTurnStatus_WaitingWithoutWorkflowActive(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "waiting", CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (waiting → completed)")
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle)", a.status.State)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want empty", a.status.TurnID)
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	turn := a.Session.Turns[1]
	if turn.State != "completed" {
		t.Fatalf("turn state = %q, want %q", turn.State, "completed")
	}
	if turn.CompletedAt != "2025-01-01T00:00:00Z" {
		t.Fatalf("turn CompletedAt = %q, want 2025-01-01T00:00:00Z", turn.CompletedAt)
	}
}

// TestRecoverTurnStatus_WaitingScanWithoutActiveTurnRef covers the real
// restart shape: ActiveTurnRef is not persisted, so recovery must find the
// waiting turn by scanning Session.Turns. A workflow owner recovered this way
// must land paused/recovery — never back in waiting — because a waiting owner
// is not busy for the workflow updater, which would immediately assign it
// tasks while its child agents are still unloaded after the restart.
func TestRecoverTurnStatus_WaitingScanWithoutActiveTurnRef(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "waiting", CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (waiting turn → paused)")
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2", a.ActiveTurnRef)
	}
	if a.status.State != "paused" {
		t.Fatalf("status.State = %q, want paused", a.status.State)
	}
	if a.status.PauseKind != "recovery" {
		t.Fatalf("status.PauseKind = %q, want recovery", a.status.PauseKind)
	}
	turn := a.Session.Turns[1]
	if turn.State != "paused" {
		t.Fatalf("turn state = %q, want paused", turn.State)
	}
	if turn.PauseReason != "recovery" {
		t.Fatalf("turn PauseReason = %q, want recovery", turn.PauseReason)
	}
}

// TestRecoverTurnStatus_WaitingScanWithoutWorkflowActive: the same lost-ref
// restart shape for a non-workflow agent — the scanned waiting turn is
// finalized to completed so the agent does not idle with a phantom waiting
// turn (and the updater never sees a waiting, not-busy owner).
func TestRecoverTurnStatus_WaitingScanWithoutWorkflowActive(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "waiting", CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
	}

	mutated := a.recoverTurnStatus()
	if !mutated {
		t.Fatal("should report mutation (waiting → completed)")
	}
	if a.status.State != "" {
		t.Fatalf("status.State = %q, want empty (idle)", a.status.State)
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if a.Session.Turns[1].State != "completed" {
		t.Fatalf("turn state = %q, want completed", a.Session.Turns[1].State)
	}
}

// TestRecoverTurnStatus_WaitingRunningDiscriminator verifies the discriminator
// comment: a waiting turn has CompletedAt, a running orphan does not. Both are
// recovered as paused/recovery in a workflow-active agent, but the waiting turn
// keeps CompletedAt, while the running orphan does not.
func TestRecoverTurnStatus_WaitingRunningDiscriminator(t *testing.T) {
	a := &Actor{
		ActiveTurnRef: "t-waiting",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t-waiting", Role: "assistant", State: "waiting", CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.recoverTurnStatus()
	turn := a.Session.Turns[0]
	if turn.CompletedAt == "" {
		t.Fatal("waiting turn should preserve CompletedAt after recovery")
	}

	// Now test a running orphan (no CompletedAt).
	a2 := &Actor{
		ActiveTurnRef: "t-running",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t-running", Role: "assistant", State: "running"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a2.recoverTurnStatus()
	turn2 := a2.Session.Turns[0]
	if turn2.CompletedAt != "" {
		t.Fatal("running orphan should NOT have CompletedAt after recovery")
	}
}

// TestTakeSnapshot_PublishesIdleTurnState verifies that an idle agent publishes
// its TurnState component as "idle" rather than leaving it empty, so the topology
// graph doesn't fall back to a generic "running" default.
func TestTakeSnapshot_PublishesIdleTurnState(t *testing.T) {
	a := &Actor{status: turnStatus{State: ""}}
	a.takeSnapshot()
	if a.TurnState != "idle" {
		t.Fatalf("TurnState = %q, want idle", a.TurnState)
	}
}
