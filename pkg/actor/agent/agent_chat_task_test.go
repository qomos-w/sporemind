package agent

import (
	"fmt"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleTurnResume_ContinuesPausedTurn verifies that crash-recovery resume
// continues the SAME paused turn (reuses its ID, flips it back to running) and
// keeps runtime tasks live instead of snapshotting them into history and
// spawning a new turn below the paused one.
func TestHandleTurnResume_ContinuesPausedTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
				{ID: "turn-2", Role: "assistant", State: "paused"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Tasks: []gen.TurnTask{
				{ID: "task-1", Subject: "old task", Status: "completed"},
				{ID: "task-2", Subject: "active task", Status: "in_progress"},
			},
		},
		steps: []domain.Step{
			{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 2},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "paused",
			StartStepCount: 1,
		},
	}

	if err := a.handleTurnResume(ctx); err != nil {
		t.Fatalf("handleTurnResume failed: %v", err)
	}

	// Resume reuses the paused turn's ID — no new turn must be created.
	if a.ActiveTurnRef != "turn-2" {
		t.Errorf("ActiveTurnRef = %q, want turn-2 (reused)", a.ActiveTurnRef)
	}
	if got := len(a.Session.Turns); got != 2 {
		t.Errorf("Session.Turns len = %d, want 2 (no new turn spawned)", got)
	}
	var continued *domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == "turn-2" {
			continued = &a.Session.Turns[i]
			break
		}
	}
	if continued == nil {
		t.Fatalf("continued turn-2 not found in Session.Turns")
	}
	if continued.State != "running" {
		t.Errorf("continued turn state = %q, want running", continued.State)
	}
	// The turn continues, so its tasks remain live in RawSession instead of
	// being snapshotted into history and cleared.
	if len(a.RawSession.Tasks) != 2 {
		t.Errorf("RawSession.Tasks = %d, want 2 (tasks stay live on continued turn)", len(a.RawSession.Tasks))
	}
}

// TestHandleChatSubmit_CancelPausedTurnPreventsTaskLeak verifies that when a
// paused turn is superseded by a new user message, the old turn is cancelled
// and its task snapshot is persisted to history instead of leaking into the
// next turn.
// TestCreatePlanTasks_DeduplicatesExistingIDs verifies that approving a plan
// referencing a task ID that already exists in RawSession.Tasks updates the
// existing entry instead of appending a duplicate.
func TestCreatePlanTasks_DeduplicatesExistingIDs(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		status: turnStatus{
			TurnID: "turn-1",
			State:  "running",
		},
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{
				{ID: "task-1", Subject: "old subject", Status: "completed"},
			},
		},
		plan: planState{
			PendingTasks: []gen.PlanTaskRef{
				{ID: "task-1", Subject: "new subject", Status: "pending"},
				{ID: "task-2", Subject: "brand new", Status: "pending"},
			},
		},
	}

	a.createPlanTasks(ctx)

	if len(a.RawSession.Tasks) != 2 {
		t.Fatalf("RawSession.Tasks len = %d, want 2", len(a.RawSession.Tasks))
	}
	var t1, t2 gen.TurnTask
	for _, task := range a.RawSession.Tasks {
		switch task.ID {
		case "task-1":
			t1 = task
		case "task-2":
			t2 = task
		}
	}
	if t1.Subject != "new subject" {
		t.Errorf("task-1 subject = %q, want new subject", t1.Subject)
	}
	if t1.Status != "pending" {
		t.Errorf("task-1 status = %q, want pending", t1.Status)
	}
	if t2.Subject != "brand new" {
		t.Errorf("task-2 subject = %q, want brand new", t2.Subject)
	}
}

func TestHandleTaskCreate_RejectsEmptySubject(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{},
		},
	}

	_, err := a.handleTaskCreate(ctx, domain.AgentTaskCreateReq{Subject: ""})
	if err == nil {
		t.Fatalf("handleTaskCreate with empty Subject should fail")
	}
	if len(a.RawSession.Tasks) != 0 {
		t.Errorf("RawSession.Tasks len = %d, want 0", len(a.RawSession.Tasks))
	}

	_, err = a.handleTaskCreate(ctx, domain.AgentTaskCreateReq{Subject: "Add retry logic"})
	if err != nil {
		t.Fatalf("handleTaskCreate with valid Subject failed: %v", err)
	}
	if len(a.RawSession.Tasks) != 1 {
		t.Fatalf("RawSession.Tasks len = %d, want 1", len(a.RawSession.Tasks))
	}
	if a.RawSession.Tasks[0].Subject != "Add retry logic" {
		t.Errorf("task subject = %q, want Add retry logic", a.RawSession.Tasks[0].Subject)
	}
}

func TestHandleChatSubmit_CancelPausedTurnPreventsTaskLeak(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-2",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Tasks: []gen.TurnTask{
				{ID: "task-1", Subject: "old task", Status: "completed"},
				{ID: "task-2", Subject: "active task", Status: "in_progress"},
			},
		},
		steps: []domain.Step{
			{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-2", Closed: true, Seq: 2},
		},
		status: turnStatus{
			TurnID:         "turn-2",
			State:          "paused",
			StartStepCount: 1,
		},
	}

	_, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "continue"})
	if err != nil {
		t.Fatalf("handleChatSubmit failed: %v", err)
	}

	// The paused turn should have been cancelled and appended to history.
	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	var cancelled *domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == "turn-2" {
			cancelled = &a.Session.Turns[i]
			break
		}
	}
	if cancelled == nil {
		t.Fatalf("cancelled turn-2 not found in Session.Turns")
	}
	if cancelled.State != "cancelled" {
		t.Errorf("cancelled turn state = %q, want cancelled", cancelled.State)
	}
	if len(cancelled.Tasks) != 2 {
		t.Errorf("cancelled turn tasks = %d, want 2", len(cancelled.Tasks))
	}

	// Completed tasks must not leak into the new turn.
	for _, task := range a.RawSession.Tasks {
		if task.Status == "completed" {
			t.Errorf("completed task %q leaked into RawSession.Tasks", task.ID)
		}
	}
}

// TestHandleStartTurnInternal_NoAggregator_Retries verifies that when
// startTurnWithName fails due to aggregator not ready, handleStartTurnInternal
// re-schedules the turn start (via ctx.After) instead of immediately failing,
// so the frontend's TurnActorID doesn't become a phantom. The optimistic
// "running" status from handleChatSubmit must be preserved during retries.
func TestIsAggregatorStartupError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "aggregator unavailable", err: fmt.Errorf("agent: no aggregator available (aimanager not ready)"), want: true},
		{name: "unresolved model target", err: fmt.Errorf("agent: no model target available for slot"), want: true},
		{name: "unrelated error", err: fmt.Errorf("dispatch: request rejected"), want: false},
		{name: "nil", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAggregatorStartupError(tc.err); got != tc.want {
				t.Fatalf("isAggregatorStartupError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestHandleStartTurnInternal_NoAggregator_Retries(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		agentKind:     "worker",
		ActiveTurnRef: "", // no aggregator → startTurnWithName won't set it
		// Simulate handleChatSubmit's optimistic pre-set.
		status: turnStatus{State: "running", TurnID: "turn-fail"},
	}

	err := a.handleStartTurnInternal(ctx, startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: "hello"},
		TurnName:  "turn-fail",
	})
	if err != nil {
		t.Fatalf("handleStartTurnInternal returned error: %v (expected nil; retry scheduled)", err)
	}

	// Status should NOT be reset — the turn is being retried, not failed.
	if a.status.State != "running" {
		t.Fatalf("status.State = %q, want running (retry pending)", a.status.State)
	}

	// No TurnFailed event should have been emitted during retry phase.
	for _, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		ev, ok := e.Payload.(domain.TurnEvent)
		if ok && ev.Kind == domain.TurnFailed {
			t.Fatal("did not expect TurnFailed event during retry phase")
		}
	}
}

// TestHandleStartTurnInternal_NoAggregator_RetriesExhausted verifies that once
// the retry cap is reached, handleStartTurnInternal falls through to the
// failure-cleanup path: resets status to "failed" and emits TurnFailed.
func TestHandleStartTurnInternal_NoAggregator_RetriesExhausted(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		agentKind:     "worker",
		ActiveTurnRef: "",
		status:        turnStatus{State: "running", TurnID: "turn-fail"},
	}

	err := a.handleStartTurnInternal(ctx, startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: "hello"},
		TurnName:  "turn-fail",
		Retries:   startTurnAggregatorMaxRetries,
	})
	if err != nil {
		t.Fatalf("handleStartTurnInternal returned error: %v (expected nil; failure handled internally)", err)
	}

	if a.status.State != "failed" {
		t.Fatalf("status.State = %q, want failed", a.status.State)
	}
	if a.status.TurnID != "" {
		t.Fatalf("status.TurnID = %q, want empty", a.status.TurnID)
	}
	if a.ActiveTurnRef != "" {
		t.Fatalf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}

	found := false
	for _, e := range ctx.EmittedEvents {
		if e.Kind != "turn" {
			continue
		}
		ev, ok := e.Payload.(domain.TurnEvent)
		if ok && ev.Kind == domain.TurnFailed {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected TurnFailed event to be emitted after retries exhausted")
	}
}

// TestSweepFinishedTasks_HistoricalIDStillResolvable is a round-6 external
// TOTP-run regression: handleTurnComplete used to hard-drop completed tasks,
// so a later update/cancel/delete replaying the id failed "task not found".
// Finished tasks are now archived into RawSession.TaskHistory and stay
// addressable.
func TestSweepFinishedTasks_HistoricalIDStillResolvable(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{},
		},
	}

	created, err := a.handleTaskCreate(ctx, domain.AgentTaskCreateReq{Subject: "fix leak"})
	if err != nil {
		t.Fatalf("handleTaskCreate: %v", err)
	}
	taskID := created.Task.ID

	if _, err := a.handleTaskUpdate(ctx, domain.AgentTaskUpdateReq{ID: taskID, Status: "completed"}); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	// What handleTurnComplete does at the end of every turn.
	a.sweepFinishedTasks()
	if len(a.RawSession.Tasks) != 0 {
		t.Fatalf("active tasks = %d after sweep, want 0", len(a.RawSession.Tasks))
	}
	if _, ok := a.RawSession.TaskHistory[taskID]; !ok {
		t.Fatal("completed task not archived into TaskHistory")
	}

	// Replayed update with the historical id must still resolve.
	upd, err := a.handleTaskUpdate(ctx, domain.AgentTaskUpdateReq{ID: taskID, Status: "completed"})
	if err != nil {
		t.Fatalf("update after sweep: %v", err)
	}
	if upd.Task.Status != "completed" {
		t.Errorf("replayed update status = %q, want completed", upd.Task.Status)
	}

	// Reactivation moves the archived task back into the active list.
	if _, err := a.handleTaskUpdate(ctx, domain.AgentTaskUpdateReq{ID: taskID, Status: "in_progress"}); err != nil {
		t.Fatalf("reactivate after sweep: %v", err)
	}
	if len(a.RawSession.Tasks) != 1 || a.RawSession.Tasks[0].Status != "in_progress" {
		t.Fatalf("reactivated task not back in active list: %+v", a.RawSession.Tasks)
	}
	if _, still := a.RawSession.TaskHistory[taskID]; still {
		t.Error("reactivated task still present in TaskHistory")
	}

	// Re-archive, then exercise the cancel and delete history fallbacks.
	if _, err := a.handleTaskUpdate(ctx, domain.AgentTaskUpdateReq{ID: taskID, Status: "completed"}); err != nil {
		t.Fatalf("re-complete task: %v", err)
	}
	a.sweepFinishedTasks()
	if _, err := a.handleTaskCancel(ctx, domain.AgentTaskCancelReq{ID: taskID}); err != nil {
		t.Fatalf("cancel after sweep: %v", err)
	}
	if a.RawSession.TaskHistory[taskID].Status != "cancelled" {
		t.Errorf("archived task status after cancel = %q, want cancelled", a.RawSession.TaskHistory[taskID].Status)
	}
	if err := a.handleTaskDelete(ctx, domain.AgentTaskDeleteReq{ID: taskID}); err != nil {
		t.Fatalf("delete after sweep: %v", err)
	}
	if len(a.RawSession.TaskHistory) != 0 {
		t.Errorf("TaskHistory not emptied by delete: %+v", a.RawSession.TaskHistory)
	}
}
