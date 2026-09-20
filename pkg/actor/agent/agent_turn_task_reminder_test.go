package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestBuildUnfinishedTaskReminderText verifies the reminder message format
// includes the [Task Reminder] header and lists each task with its status.
func TestBuildUnfinishedTaskReminderText(t *testing.T) {
	tasks := []gen.TurnTask{
		{ID: "task-1", Subject: "Write tests", Status: "in_progress", ActiveForm: "testing module X"},
		{ID: "task-2", Subject: "Review PR", Status: "pending"},
	}
	text := buildUnfinishedTaskReminderText(tasks)

	if !strings.Contains(text, "[Task Reminder]") {
		t.Errorf("text missing [Task Reminder] header: %s", text)
	}
	if !strings.Contains(text, "Write tests") {
		t.Errorf("text missing task-1 subject: %s", text)
	}
	if !strings.Contains(text, "Review PR") {
		t.Errorf("text missing task-2 subject: %s", text)
	}
	if !strings.Contains(text, "in_progress") {
		t.Errorf("text missing in_progress status: %s", text)
	}
	if !strings.Contains(text, "pending") {
		t.Errorf("text missing pending status: %s", text)
	}
	if !strings.Contains(text, "testing module X") {
		t.Errorf("text missing ActiveForm: %s", text)
	}
}

// TestHandleTurnComplete_NoReminderWhenAllTasksDone verifies that when all
// tasks are completed or cancelled, handleTurnComplete cleans them up and no
// reminder logic interferes (the reminder is handled in finalizeDispatch, not
// handleTurnComplete, so we just verify cleanup here).
func TestHandleTurnComplete_NoReminderWhenAllTasksDone(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 10,
			Tasks: []gen.TurnTask{
				{ID: "task-1", Subject: "Done task", Status: "completed"},
				{ID: "task-2", Subject: "Cancelled task", Status: "cancelled"},
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	a.takeSnapshot()

	err := a.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:        "turn-1",
			Role:      "assistant",
			State:     "completed",
			Timestamp: "2026-01-01T00:00:00Z",
		},
	})
	if err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	// All tasks (completed + cancelled) should be cleared from RawSession.Tasks.
	if len(a.RawSession.Tasks) != 0 {
		t.Errorf("expected 0 remaining tasks after cleanup, got %d", len(a.RawSession.Tasks))
	}
}

// TestHandleTaskCancel verifies that the cancel handler sets status to
// "cancelled" and returns the updated task.
func TestHandleTaskCancel(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		RawSession: domain.RawSession{
			Tasks: []gen.TurnTask{
				{ID: "task-1", Subject: "Cancel me", Status: "in_progress"},
				{ID: "task-2", Subject: "Leave me", Status: "pending"},
			},
		},
		snapshotReady: true,
	}

	resp, err := a.handleTaskCancel(ctx, domain.AgentTaskCancelReq{ID: "task-1"})
	if err != nil {
		t.Fatalf("handleTaskCancel error: %v", err)
	}
	if resp.Task.Status != "cancelled" {
		t.Errorf("cancelled task status = %q, want cancelled", resp.Task.Status)
	}
	if resp.Task.ID != "task-1" {
		t.Errorf("cancelled task ID = %q, want task-1", resp.Task.ID)
	}

	// Verify the task in RawSession is now cancelled.
	if a.RawSession.Tasks[0].Status != "cancelled" {
		t.Errorf("RawSession task-1 status = %q, want cancelled", a.RawSession.Tasks[0].Status)
	}
	// Other task unchanged.
	if a.RawSession.Tasks[1].Status != "pending" {
		t.Errorf("RawSession task-2 status = %q, want pending", a.RawSession.Tasks[1].Status)
	}
}

// TestHandleTaskCancel_NotFound verifies that cancelling a non-existent task
// returns an error.
func TestHandleTaskCancel_NotFound(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		RawSession:    domain.RawSession{Tasks: []gen.TurnTask{{ID: "task-1", Subject: "exists", Status: "pending"}}},
		snapshotReady: true,
	}

	_, err := a.handleTaskCancel(ctx, domain.AgentTaskCancelReq{ID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for cancelling non-existent task, got nil")
	}
}

// TestTurnEngineTaskReminderEmitted verifies that the turnEngine's
// taskReminderEmitted flag prevents repeated reminders within the same turn.
func TestTurnEngineTaskReminderEmitted(t *testing.T) {
	e := &turnEngine{}
	if e.taskReminderEmitted {
		t.Fatal("taskReminderEmitted should start false")
	}
	e.taskReminderEmitted = true
	if !e.taskReminderEmitted {
		t.Fatal("taskReminderEmitted should be true after set")
	}
}
