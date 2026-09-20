package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// buildCompletedSource returns an actor whose session mirrors the steady state
// after one finished exchange: user turn + completed assistant turn, ActiveHead
// at the assistant turn, steps closed, counters past everything.
func buildCompletedSource(t *testing.T) *Actor {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a := &Actor{
		actorID:       "agent-src",
		agentKind:     "coder",
		snapshotReady: true,
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-turn-1", Role: "user", State: "completed", Seq: 1, TurnOrder: 1, Timestamp: now, UserInput: "hello"},
				{ID: "turn-1", Role: "assistant", State: domain.TurnStateCompleted, Seq: 2, TurnOrder: 2, StartedAt: now, CompletedAt: now, Revision: 2},
			},
			ActiveHead: 1,
		},
		RawSession: gen.RawSession{
			NextIdx:       3,
			NextSeq:       3,
			NextTurnOrder: 3,
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "user-turn-1", Closed: true, Seq: 1, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: true, Seq: 2, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi there"}}},
			},
		},
	}
	_ = a.rebuildSteps()
	a.takeSnapshot()
	return a
}

// importForkIntoClone runs the server-side fork branch of
// workspace.cloneSourceSessionInto: session_fork on the source, session_import
// (goal dropped) into a fresh clone.
func importForkIntoClone(t *testing.T, src *Actor, atTurnID string, lastState string) *Actor {
	t.Helper()
	forkResp, err := src.handleSessionFork(nil, domain.AgentSessionForkReq{AtTurnID: atTurnID})
	if err != nil {
		t.Fatalf("session_fork: %v", err)
	}
	clone := &Actor{actorID: "agent-clone", agentKind: "coder", snapshotReady: true}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if _, err := clone.handleSessionImport(ctx, domain.AgentSessionImportReq{
		Session:         forkResp.Session,
		SummarySegments: forkResp.SummarySegments,
		ExploreResults:  forkResp.ExploreResults,
		Steps:           forkResp.Steps,
		NextIdx:         forkResp.NextIdx,
		NextSeq:         forkResp.NextSeq,
		NextTurnOrder:   forkResp.NextTurnOrder,
	}); err != nil {
		t.Fatalf("session_import: %v", err)
	}
	_ = lastState
	return clone
}

// assertSubmitStartsTurn submits a message and asserts the message is not
// swallowed: the RPC returns a TurnActorID, a user turn is appended, and
// internal_start_turn is scheduled for a fresh turn.
func assertSubmitStartsTurn(t *testing.T, a *Actor, label string) domain.AgentChatSubmitResp {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	var scheduled []startTurnInternalReq
	ctx.AfterFn = func(_ time.Duration, callID string, payload any) error {
		if callID == "internal_start_turn" {
			if req, ok := payload.(startTurnInternalReq); ok {
				scheduled = append(scheduled, req)
			}
		}
		return nil
	}
	before := len(a.Session.Turns)
	resp, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "new message after fork"})
	if err != nil {
		t.Fatalf("%s: handleChatSubmit: %v", label, err)
	}
	if resp.MessageID == "" {
		t.Fatalf("%s: resp.MessageID empty — message swallowed: %+v", label, resp)
	}
	if resp.TurnActorID == "" {
		t.Fatalf("%s: resp.TurnActorID empty — submit parked as pending/no turn started: %+v", label, resp)
	}
	if len(a.Session.Turns) != before+1 {
		t.Fatalf("%s: user turn not appended: turns %d -> %d", label, before, len(a.Session.Turns))
	}
	if len(scheduled) == 0 {
		t.Fatalf("%s: internal_start_turn never scheduled", label)
	}
	if len(a.pendingSubmits) != 0 {
		t.Fatalf("%s: message parked as pending submit on %+v — no engine will consume it", label, a.pendingSubmits)
	}
	return resp
}

func TestForkFromLastTurn_CloneSubmitsNewMessage(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	src := buildCompletedSource(t)
	clone := importForkIntoClone(t, src, "turn-1", domain.TurnStateCompleted)
	assertSubmitStartsTurn(t, clone, "completed-last-turn")
}

func TestForkFromWaitingLastTurn_CloneSubmitsNewMessage(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	src := buildCompletedSource(t)
	src.Session.Turns[1].State = domain.TurnStateWaiting
	src.Session.Turns[1].CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	_ = src.rebuildSteps()
	src.takeSnapshot()

	clone := importForkIntoClone(t, src, "turn-1", domain.TurnStateWaiting)
	assertSubmitStartsTurn(t, clone, "waiting-last-turn")
}

func TestForkFromLastTurn_CloneAfterReloadSubmitsNewMessage(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	src := buildCompletedSource(t)
	clone := importForkIntoClone(t, src, "turn-1", domain.TurnStateCompleted)

	// Persist, then reload the actor the way OnStart does and run the recovery
	// pipeline before submitting.
	clone.saveMailbox(nil)
	reloaded := &Actor{actorID: clone.actorID, agentKind: "coder", snapshotReady: true}
	reloaded.loadMailbox(nil)
	reloaded.initNextSeq()
	reloaded.normalizeSessionTurns()
	reloaded.migrateTurnOrders()
	_ = reloaded.rebuildSteps()
	reloaded.closeOpenStepsForTerminalTurns()
	reloaded.recoverOrphanTurnSteps()
	reloaded.recoverTurnStatus()

	if got := reloaded.getActiveTurnRef(); got != "" {
		t.Fatalf("after reload ActiveTurnRef = %q, want empty (no zombie turn)", got)
	}
	assertSubmitStartsTurn(t, reloaded, "after-reload")
}
