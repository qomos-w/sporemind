package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func injectSucceedingSummarizer(t *testing.T) {
	t.Helper()
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		return "chunk summary", nil
	}
	t.Cleanup(func() { summarizeViaPlan = original })
}

func injectFailingSummarizer(t *testing.T) {
	t.Helper()
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		return "", errors.New("round 1 summarize failed: boom")
	}
	t.Cleanup(func() { summarizeViaPlan = original })
}

func runningCompactTurn(id string) domain.Turn {
	return domain.Turn{
		ID:        id,
		Role:      "user",
		UserInput: "/compact",
		State:     domain.TurnStateRunning,
		StartedAt: "2026-09-13T10:00:00Z",
		Timestamp: "2026-09-13T10:00:00Z",
		Seq:       1,
		TurnOrder: 5,
		Revision:  1,
	}
}

func (c *recordingCompactionContext) turnEventsOfKind(topic string, kind domain.TurnEventKind) []domain.TurnEvent {
	var out []domain.TurnEvent
	for _, ev := range c.events {
		if ev.topic != topic {
			continue
		}
		if te, ok := ev.event.(domain.TurnEvent); ok && te.Kind == kind {
			out = append(out, te)
		}
	}
	return out
}

// TestHandleCompact_FinalizesTurnCompleted pins the compact turn lifecycle:
// the turn is created running by the chat-submit path and must only reach
// completed after compaction finishes.
func TestHandleCompact_FinalizesTurnCompleted(t *testing.T) {
	injectSucceedingSummarizer(t)
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	a.Session.Turns = []domain.Turn{runningCompactTurn("turn-1")}
	ctx := newRecordingCompactionContext()

	if err := a.handleCompact(ctx, compactReq{TurnID: "turn-1", Trigger: "user"}); err != nil {
		t.Fatalf("handleCompact failed: %v", err)
	}
	turn := a.Session.Turns[0]
	if turn.State != domain.TurnStateCompleted {
		t.Fatalf("turn State = %q, want completed", turn.State)
	}
	if turn.Revision != 2 {
		t.Fatalf("turn Revision = %d, want 2", turn.Revision)
	}
	if turn.CompletedAt == "" {
		t.Fatal("turn CompletedAt empty after completion")
	}
	events := ctx.turnEventsOfKind("turn", domain.TurnLifecycleCompleted)
	if len(events) != 1 {
		t.Fatalf("turn.completed events = %d, want 1", len(events))
	}
	if payloadState, _ := events[0].Payload["state"].(string); payloadState != domain.TurnStateCompleted {
		t.Fatalf("turn.completed payload state = %q, want completed", payloadState)
	}
	// The compact turn is a user turn: it must not mirror into the agent-level
	// active-turn status.
	if a.status.State != "" || a.status.TurnID != "" {
		t.Fatalf("a.status mirrored compact turn: %+v", a.status)
	}
}

// TestHandleCompact_FinalizesTurnFailed pins the failure leg: a compaction
// error must mark the turn failed (terminal, with the error) instead of
// leaving it running.
func TestHandleCompact_FinalizesTurnFailed(t *testing.T) {
	injectFailingSummarizer(t)

	a := newChunkTestActor(250)
	a.Session.Turns = []domain.Turn{runningCompactTurn("turn-1")}
	ctx := newRecordingCompactionContext()

	if err := a.handleCompact(ctx, compactReq{TurnID: "turn-1", Trigger: "user"}); err == nil {
		t.Fatal("expected handleCompact to return the compaction error")
	}
	turn := a.Session.Turns[0]
	if turn.State != domain.TurnStateFailed {
		t.Fatalf("turn State = %q, want failed", turn.State)
	}
	if turn.Error == "" {
		t.Fatal("turn Error empty after failure")
	}
	if turn.CompletedAt == "" {
		t.Fatal("turn CompletedAt empty after failure")
	}
	events := ctx.turnEventsOfKind("turn", domain.TurnLifecycleFailed)
	if len(events) != 1 {
		t.Fatalf("turn.failed events = %d, want 1", len(events))
	}
}

// TestHandleCompact_NoRecordSkipped pins the auto-trigger guard: compaction
// riding the engine's own turn (no dedicated Session.Turns record) must not
// fabricate one at finalize time.
func TestHandleCompact_NoRecordSkipped(t *testing.T) {
	injectSucceedingSummarizer(t)
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	ctx := newRecordingCompactionContext()

	if err := a.handleCompact(ctx, compactReq{TurnID: "engine-turn-7", Trigger: "user"}); err != nil {
		t.Fatalf("handleCompact failed: %v", err)
	}
	if len(a.Session.Turns) != 0 {
		t.Fatalf("finalize fabricated a turn record: %+v", a.Session.Turns)
	}
}

// TestFinalizeCompactTurn_TerminalRecordUntouched pins idempotence: an
// already-terminal record (e.g. crash recovery raced the finalize) stays as-is.
func TestFinalizeCompactTurn_TerminalRecordUntouched(t *testing.T) {
	a := newChunkTestActor(10)
	a.Session.Turns = []domain.Turn{{
		ID: "turn-1", Role: "user", State: domain.TurnStateCompleted,
		TurnOrder: 5, Revision: 2, CompletedAt: "2026-09-13T10:00:05Z",
	}}
	ctx := newRecordingCompactionContext()

	a.finalizeCompactTurn(ctx, "turn-1", nil)
	if a.Session.Turns[0].State != domain.TurnStateCompleted || a.Session.Turns[0].Revision != 2 {
		t.Fatalf("terminal record mutated: %+v", a.Session.Turns[0])
	}
	if events := ctx.turnEventsOfKind("turn", domain.TurnLifecycleCompleted); len(events) != 0 {
		t.Fatalf("unexpected turn.completed events: %d", len(events))
	}
}

// TestCloseInterruptedCompaction_ClosesStepAndFailsUserTurn pins the crash
// path: a lock orphaned mid-compaction means the run died; its open
// compaction-only step gets an error frame and the owning user turn fails.
func TestCloseInterruptedCompaction_ClosesStepAndFailsUserTurn(t *testing.T) {
	a := newChunkTestActor(10)
	a.Session.Turns = []domain.Turn{runningCompactTurn("turn-1")}
	openFrame := gen.CompactionFrameData{
		Type: "compaction", ID: "c1", Status: "running", Trigger: "user",
		BeforeTokens: 900, AfterTokens: 400, ContextWindowSize: 200000,
	}
	frameJSON, _ := json.Marshal(openFrame)
	a.steps = append(a.steps, domain.Step{
		ID: "c1", Role: "assistant", Type: "text",
		Content: []domain.ContentBlock{{Type: "compaction", Text: string(frameJSON)}},
		TurnID: "turn-1", Timestamp: "2026-09-13T10:00:01Z", Seq: 2,
	})
	ctx := newRecordingCompactionContext()

	a.closeInterruptedCompaction(ctx, "turn-1", "user")

	step := a.steps[len(a.steps)-1]
	if !step.Closed {
		t.Fatal("interrupted compaction step left open")
	}
	if step.Error == "" {
		t.Fatal("interrupted compaction step has no Error")
	}
	last := step.Content[len(step.Content)-1]
	if last.Type != "compaction" {
		t.Fatalf("appended block Type = %q, want compaction", last.Type)
	}
	var frame gen.CompactionFrameData
	if err := json.Unmarshal([]byte(last.Text), &frame); err != nil {
		t.Fatalf("error frame unparseable: %v", err)
	}
	if frame.Status != "error" || frame.Error == "" {
		t.Fatalf("error frame = %+v, want Status=error with Error", frame)
	}
	turn := a.Session.Turns[0]
	if turn.State != domain.TurnStateFailed {
		t.Fatalf("turn State = %q, want failed", turn.State)
	}
	if !strings.Contains(turn.Error, "interrupted") {
		t.Fatalf("turn Error = %q, want interruption reason", turn.Error)
	}
}

// TestCloseInterruptedCompaction_AutoTurnNotFinalized pins the ownership rule:
// an auto-trigger lock names the engine's assistant turn, whose crash recovery
// belongs to recoverTurnStatus — only the compaction steps are closed here.
func TestCloseInterruptedCompaction_AutoTurnNotFinalized(t *testing.T) {
	a := newChunkTestActor(10)
	a.Session.Turns = []domain.Turn{{
		ID: "engine-turn", Role: "assistant", State: domain.TurnStateRunning,
		TurnOrder: 5, Revision: 1,
	}}
	frameJSON, _ := json.Marshal(gen.CompactionFrameData{
		Type: "compaction", ID: "c1", Status: "running", Trigger: "auto",
	})
	a.steps = append(a.steps, domain.Step{
		ID: "c1", Role: "assistant", Type: "text",
		Content: []domain.ContentBlock{{Type: "compaction", Text: string(frameJSON)}},
		TurnID: "engine-turn", Timestamp: "2026-09-13T10:00:01Z", Seq: 2,
	})
	ctx := newRecordingCompactionContext()

	a.closeInterruptedCompaction(ctx, "engine-turn", "auto")

	if !a.steps[len(a.steps)-1].Closed {
		t.Fatal("auto-trigger compaction step left open")
	}
	if a.Session.Turns[0].State != domain.TurnStateRunning {
		t.Fatalf("assistant turn finalized: State = %q, want running untouched", a.Session.Turns[0].State)
	}
}

// TestEmitCompactTurnStarted guards the live announcement: the frontend's
// activeTurnStates learns the compact turn is running before frames stream.
func TestEmitCompactTurnStarted(t *testing.T) {
	a := newChunkTestActor(10)
	ctx := newRecordingCompactionContext()

	a.emitCompactTurnStarted(ctx, runningCompactTurn("turn-1"))

	events := ctx.turnEventsOfKind("turn", domain.TurnLifecycleStarted)
	if len(events) != 1 {
		t.Fatalf("turn.started events = %d, want 1", len(events))
	}
	payload := events[0].Payload
	rev, _ := payload["revision"].(int64)
	if payload["state"] != domain.TurnStateRunning || rev != 1 {
		t.Fatalf("turn.started payload = %+v, want state=running revision=1", payload)
	}
}
