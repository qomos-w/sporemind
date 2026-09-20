package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestRequestLedger_PersistAndRestore verifies that the session-level request
// ledger survives a save/restart cycle: ordinary LLM requests (TurnRequestStat
// via handleTurnComplete) and compaction requests (via compactAsStep) produce
// SessionRequestRecord entries that are persisted to requests/ledger.jsonl and
// correctly reloaded by a new Actor instance, with both kinds interleaved by
// Seq.
func TestRequestLedger_PersistAndRestore(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	actorID := "test-ledger-actor"
	ctx := &fakeCompactionContext{} // reuse from compaction_test.go

	// ── Phase 1: write a compaction and a turn with tool calls ──
	a1 := newChunkTestActor(250)
	a1.actorID = actorID
	a1.snapshotReady = true
	a1.ActiveTurnRef = "turn-1"
	a1.status.TurnID = "turn-1"
	a1.status.State = "running"
	a1.status.StartStepCount = 0
	a1.Session = domain.Session{
		Turns: []domain.Turn{
			{ID: "user-turn-1", Role: "user", State: "completed"},
		},
		ActiveHead: 0,
	}
	a1.RawSession.NextSeq = 10
	a1.takeSnapshot()

	// Simulate a compaction (stub summarizeViaPlan + token probe).
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	if err := a1.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("Phase 1: compactAsStep failed: %v", err)
	}

	// Simulate a turn with tool calls (RequestStats with usage).
	err := a1.handleTurnComplete(ctx, domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:        "turn-1",
			Role:      "assistant",
			State:     "completed",
			Timestamp: "2026-08-17T00:00:00Z",
			RequestStats: []domain.TurnRequestStat{
				{
					ID:          "turn-1-1",
					Provider:    "test-provider",
					Model:       "test-model",
					Usage:       &domain.UsageData{InputTokens: 100, OutputTokens: 50},
					StopReason:  "stop",
					StartedAt:   "2026-08-17T00:00:00Z",
					CompletedAt: "2026-08-17T00:00:05Z",
				},
				{
					ID:          "turn-1-2",
					Provider:    "test-provider",
					Model:       "test-model",
					Usage:       &domain.UsageData{InputTokens: 200, OutputTokens: 100},
					StopReason:  "toolUse",
					StartedAt:   "2026-08-17T00:00:06Z",
					CompletedAt: "2026-08-17T00:00:10Z",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Phase 1: handleTurnComplete failed: %v", err)
	}
	// handleTurnComplete calls saveMailbox internally; ensure the ledger is
	// flushed even if the internal save was a no-op path.
	a1.saveMailbox(nil)

	// ── Phase 2: reload ledger from disk ──
	a2 := &Actor{actorID: actorID}
	a2.loadRequestRecords()

	if len(a2.requestRecords) == 0 {
		t.Fatal("Phase 2: requestRecords is empty after reload")
	}

	// Check LLM records are present.
	llmCount := 0
	compactionCount := 0
	var lastSeq int64 = -1
	for _, rec := range a2.requestRecords {
		if rec.Seq <= lastSeq {
			t.Errorf("record Seq out of order: %d after %d", rec.Seq, lastSeq)
		}
		lastSeq = rec.Seq
		switch rec.Kind {
		case "llm":
			llmCount++
			if rec.TurnID != "turn-1" {
				t.Errorf("LLM record turnId = %q, want turn-1", rec.TurnID)
			}
		case "compaction":
			compactionCount++
			if rec.TurnID != "turn-1" {
				t.Errorf("compaction record turnId = %q, want turn-1", rec.TurnID)
			}
		default:
			t.Errorf("unexpected record Kind = %q", rec.Kind)
		}
	}
	if llmCount != 2 {
		t.Errorf("expected 2 LLM records, got %d", llmCount)
	}
	if compactionCount != 1 {
		t.Errorf("expected 1 compaction record, got %d", compactionCount)
	}

	// Verify LLM record usage carried through.
	var llmRecs []domain.SessionRequestRecord
	for _, rec := range a2.requestRecords {
		if rec.Kind == "llm" {
			llmRecs = append(llmRecs, rec)
		}
	}
	if len(llmRecs) >= 1 && llmRecs[0].Usage != nil {
		if llmRecs[0].Usage.InputTokens != 100 {
			t.Errorf("first LLM record InputTokens = %d, want 100", llmRecs[0].Usage.InputTokens)
		}
	} else {
		t.Error("first LLM record missing Usage")
	}
}
