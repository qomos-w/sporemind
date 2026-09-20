package agent

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type nopActorLogger struct{ l *slog.Logger }

func (n nopActorLogger) Debug(msg string, args ...any) { n.l.Debug(msg, args...) }
func (n nopActorLogger) Info(msg string, args ...any)  { n.l.Info(msg, args...) }
func (n nopActorLogger) Warn(msg string, args ...any)  { n.l.Warn(msg, args...) }
func (n nopActorLogger) Error(msg string, args ...any) { n.l.Error(msg, args...) }

func newNopActorLogger() actor.Logger {
	return nopActorLogger{l: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func newReconcileTestEngine(history []domain.ChatMessage) *turnEngine {
	return &turnEngine{
		history:       history,
		nextIdx:       int32(len(history)) + 1,
		openToolCalls: 0,
		stepByID:      map[string]domain.TurnAction{},
		logger:        newNopActorLogger(),
	}
}

func TestReconcileOrphanToolUses_SynthesizesMissingResults(t *testing.T) {
	e := newReconcileTestEngine([]domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{
			Role: domain.ChatRoleAssistant,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "toolA", ToolName: "fs.read"},
				{Type: domain.ContentBlockToolUse, ToolUseID: "toolB", ToolName: "fs.read"},
			},
		},
		{
			Role: domain.ChatRoleTool,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolResult, ToolUseID: "toolA", Text: "ok"},
			},
		},
	})
	e.openToolCalls = 1

	e.reconcileOrphanToolUses("turn-1")

	var gotB *domain.ContentBlock
	for _, msg := range e.history {
		if msg.Role != domain.ChatRoleTool {
			continue
		}
		for i := range msg.Content {
			if msg.Content[i].Type == domain.ContentBlockToolResult && msg.Content[i].ToolUseID == "toolB" {
				gotB = &msg.Content[i]
			}
		}
	}
	if gotB == nil {
		t.Fatalf("expected synthesized tool_result for orphan toolB, history=%+v", e.history)
	}
	if !gotB.IsError {
		t.Errorf("synthesized tool_result should be marked IsError")
	}
	if gotB.Text == "" {
		t.Errorf("synthesized tool_result should carry an explanatory error text")
	}

	if e.openToolCalls != 0 {
		t.Errorf("openToolCalls = %d, want 0 after reconcile", e.openToolCalls)
	}

	countA := 0
	for _, msg := range e.history {
		if msg.Role == domain.ChatRoleTool {
			for _, b := range msg.Content {
				if b.Type == domain.ContentBlockToolResult && b.ToolUseID == "toolA" {
					countA++
				}
			}
		}
	}
	if countA != 1 {
		t.Errorf("toolA result count = %d, want 1 (must not duplicate existing result)", countA)
	}
}

func TestReconcileOrphanToolUses_NoopOnBalancedHistory(t *testing.T) {
	before := []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{
			Role: domain.ChatRoleAssistant,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "toolA", ToolName: "fs.read"},
			},
		},
		{
			Role: domain.ChatRoleTool,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolResult, ToolUseID: "toolA", Text: "ok"},
			},
		},
	}
	e := newReconcileTestEngine(before)

	e.reconcileOrphanToolUses("turn-1")

	if len(e.history) != len(before) {
		t.Errorf("history length = %d, want %d (no-op expected)", len(e.history), len(before))
	}
}

func TestReconcileOrphanToolUses_NoopOnPureText(t *testing.T) {
	before := []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
		{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
	}
	e := newReconcileTestEngine(before)

	e.reconcileOrphanToolUses("turn-1")

	if len(e.history) != len(before) {
		t.Errorf("history length = %d, want %d for pure-text turn", len(e.history), len(before))
	}
}

func TestWaitExit_NoNilSafe(t *testing.T) {
	e := newReconcileTestEngine(nil)
	e.waitExit()
}

func TestWaitExit_BlocksUntilClosed(t *testing.T) {
	e := newReconcileTestEngine(nil)
	e.runExited = make(chan struct{})

	done := make(chan struct{})
	go func() {
		e.waitExit()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("waitExit returned before runExited was closed")
	default:
	}

	close(e.runExited)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitExit did not unblock after runExited was closed")
	}
}

func TestReconcilePersistedOrphans_SynthesizesMissingToolResultSteps(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-old", Timestamp: "2026-01-01T00:00:00Z"},
			},
		},
		steps: []domain.Step{
			{ID: "u1", Role: "user", Type: "text", TurnID: "turn-old",
				Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
			{ID: "a1", Role: "assistant", Type: "tool_call", TurnID: "turn-old",
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockText, Text: "calling"},
					{Type: domain.ContentBlockToolUse, ToolUseID: "toolA", ToolName: "fs.read"},
					{Type: domain.ContentBlockToolUse, ToolUseID: "toolB", ToolName: "fs.read"},
				}},
			{ID: "t1", Role: "tool", Type: "tool_result", TurnID: "turn-old",
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockToolResult, ToolUseID: "toolA", Text: "ok"},
				}},
		},
	}

	a.reconcilePersistedOrphans()

	var gotB *domain.ContentBlock
	for _, s := range a.steps {
		for j := range s.Content {
			b := &s.Content[j]
			if b.Type == domain.ContentBlockToolResult && b.ToolUseID == "toolB" {
				gotB = b
			}
		}
	}
	if gotB == nil {
		t.Fatalf("expected synthesized tool_result for orphan toolB, steps=%+v", a.steps)
	}
	if !gotB.IsError {
		t.Errorf("synthesized tool_result must be IsError=true")
	}
	if gotB.Text == "" {
		t.Errorf("synthesized tool_result must carry explanatory text")
	}

	countA := 0
	for _, s := range a.steps {
		for _, b := range s.Content {
			if b.Type == domain.ContentBlockToolResult && b.ToolUseID == "toolA" {
				countA++
			}
		}
	}
	if countA != 1 {
		t.Errorf("toolA result count = %d, want 1 (must not duplicate existing)", countA)
	}

	// Second run is a no-op.
	a.reconcilePersistedOrphans()
	countB := 0
	for _, s := range a.steps {
		for _, b := range s.Content {
			if b.Type == domain.ContentBlockToolResult && b.ToolUseID == "toolB" {
				countB++
			}
		}
	}
	if countB != 1 {
		t.Errorf("toolB result count after second reconcile = %d, want 1 (re-run must be idempotent)", countB)
	}
}

func TestReconcilePersistedOrphans_NoopOnCleanHistory(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1"},
			},
		},
		steps: []domain.Step{
			{ID: "u1", Role: "user", TurnID: "t1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
			{ID: "a1", Role: "assistant", TurnID: "t1", Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "x", ToolName: "fs.read"},
			}},
			{ID: "t1r", Role: "tool", TurnID: "t1", Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolResult, ToolUseID: "x", Text: "ok"},
			}},
		},
	}
	beforeLen := len(a.steps)

	a.reconcilePersistedOrphans()

	if len(a.steps) != beforeLen {
		t.Errorf("clean history mutated: got %d steps, want %d", len(a.steps), beforeLen)
	}
}

func TestReconcilePersistedOrphans_NoopOnPureText(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1"},
			},
		},
		steps: []domain.Step{
			{ID: "u1", Role: "user", TurnID: "t1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
			{ID: "a1", Role: "assistant", TurnID: "t1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
		},
	}

	a.reconcilePersistedOrphans()

	if len(a.steps) != 2 {
		t.Errorf("pure-text turn mutated: got %d steps, want 2", len(a.steps))
	}
}

func TestCloseRemainingOpenSteps_CancellationPreservesStreamContent(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.loopState = LoopCancelled
	e.stepByID = map[string]domain.TurnAction{
		"llm1":    {ID: "llm1", Kind: string(domain.TurnActionLLMCall), State: "running", Text: "partial stream text"},
		"reason1": {ID: "reason1", Kind: string(domain.TurnActionReasoning), State: "running", Text: "thinking..."},
		"tool1":   {ID: "tool1", Kind: string(domain.TurnActionToolCall), State: "running"},
	}
	e.stepOrder = []string{"llm1", "reason1", "tool1"}

	e.closeRemainingOpenSteps(ctx, "turn-1")

	wantEvents := map[string]string{
		"llm1":    "step.closed",
		"reason1": "step.closed",
		"tool1":   "step.error",
	}
	if len(e.stepEvents) != len(wantEvents) {
		t.Fatalf("expected %d step events, got %d", len(wantEvents), len(e.stepEvents))
	}
	for _, ev := range e.stepEvents {
		want, ok := wantEvents[ev.StepID]
		if !ok {
			t.Errorf("unexpected event for step %s: %s", ev.StepID, ev.Kind)
			continue
		}
		if ev.Kind != want {
			t.Errorf("step %s event kind = %s, want %s", ev.StepID, ev.Kind, want)
		}
	}

	if e.stepByID["llm1"].State != "cancelled" {
		t.Errorf("llm1 state = %s, want cancelled", e.stepByID["llm1"].State)
	}
	if e.stepByID["llm1"].Text != "partial stream text" {
		t.Errorf("llm1 text was mutated: %q", e.stepByID["llm1"].Text)
	}
	if e.stepByID["llm1"].Error != "" {
		t.Errorf("llm1 error should remain empty, got %q", e.stepByID["llm1"].Error)
	}

	if e.stepByID["reason1"].State != "cancelled" {
		t.Errorf("reason1 state = %s, want cancelled", e.stepByID["reason1"].State)
	}
	if e.stepByID["reason1"].Error != "" {
		t.Errorf("reason1 error should remain empty, got %q", e.stepByID["reason1"].Error)
	}

	if e.stepByID["tool1"].State != "failed" {
		t.Errorf("tool1 state = %s, want failed", e.stepByID["tool1"].State)
	}
	if e.stepByID["tool1"].Error == "" {
		t.Errorf("tool1 error should be set on cancellation")
	}
}

func TestCloseRemainingOpenSteps_NonCancelledEmitsError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.loopState = LoopFailed
	e.stepByID = map[string]domain.TurnAction{
		"llm1": {ID: "llm1", Kind: string(domain.TurnActionLLMCall), State: "running", Text: "partial text"},
	}
	e.stepOrder = []string{"llm1"}

	e.closeRemainingOpenSteps(ctx, "turn-1")

	if len(e.stepEvents) != 1 {
		t.Fatalf("expected 1 step event, got %d", len(e.stepEvents))
	}
	if e.stepEvents[0].Kind != "step.error" {
		t.Errorf("event kind = %s, want step.error", e.stepEvents[0].Kind)
	}
	if e.stepByID["llm1"].State != "failed" {
		t.Errorf("llm1 state = %s, want failed", e.stepByID["llm1"].State)
	}
	if e.stepByID["llm1"].Error == "" {
		t.Errorf("llm1 error should be set")
	}
}

func TestTrackOpenStepEvents_PreservesOpenAndClearsOnClose(t *testing.T) {
	e := newReconcileTestEngine(nil)

	e.emitStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s1", TurnID: "t1"})
	e.emitStepEvent(domain.StepEvent{Kind: "block.appended", StepID: "s1", TurnID: "t1"})
	e.emitStepEvent(domain.StepEvent{Kind: "block.delta", StepID: "s1", TurnID: "t1"})
	e.emitStepEvent(domain.StepEvent{Kind: "step.opened", StepID: "s2", TurnID: "t1"})
	e.emitStepEvent(domain.StepEvent{Kind: "step.closed", StepID: "s2", TurnID: "t1"})

	if len(e.openStepEvents["s1"]) != 3 {
		t.Errorf("s1 open events = %d, want 3", len(e.openStepEvents["s1"]))
	}
	if _, ok := e.openStepEvents["s2"]; ok {
		t.Errorf("s2 should be removed from openStepEvents after step.closed")
	}

	e.emitStepEvent(domain.StepEvent{Kind: "step.error", StepID: "s1", TurnID: "t1"})
	if _, ok := e.openStepEvents["s1"]; ok {
		t.Errorf("s1 should be removed from openStepEvents after step.error")
	}
}

// TestFailTurn_PropagatesErrorToGetDelta verifies that a turn failure records
// its error message and surfaces it through getDelta().Turn.Error so the
// turn.failed event payload reaches the frontend.
func TestFailTurn_PropagatesErrorToGetDelta(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)
	e.turnID = "turn-fail"
	e.startedAt = time.Now()

	e.failTurn(ctx, "turn-fail", &turnErrorForTest{msg: "provider returned 429 rate limit"})

	if e.loopState != LoopFailed {
		t.Fatalf("loopState = %v, want LoopFailed", e.loopState)
	}
	if e.turnError == "" {
		t.Fatalf("turnError should be set after failTurn")
	}

	delta := e.getDelta()
	if delta.Turn.State != "failed" {
		t.Errorf("delta turn state = %q, want failed", delta.Turn.State)
	}
	if delta.Turn.Error != "provider returned 429 rate limit" {
		t.Errorf("delta turn error = %q, want provider error message", delta.Turn.Error)
	}
}

// TestFailTurn_KeepsFirstError verifies that a subsequent failure does not
// overwrite the original error message.
func TestFailTurn_KeepsFirstError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := newReconcileTestEngine(nil)

	e.failTurn(ctx, "turn-1", &turnErrorForTest{msg: "first failure"})
	e.failTurn(ctx, "turn-1", &turnErrorForTest{msg: "second failure"})

	if e.turnError != "first failure" {
		t.Errorf("turnError = %q, want first failure", e.turnError)
	}
}

type turnErrorForTest struct{ msg string }

func (e *turnErrorForTest) Error() string { return e.msg }
