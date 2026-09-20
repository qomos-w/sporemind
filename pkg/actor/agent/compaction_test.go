package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestComputeSplitIdx_WindowLargerThanProtectedTail(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 100)}
	idx := a.computeSplitIdx(20, 10)
	if idx != 80 {
		t.Fatalf("expected split index 80 (keep 20), got %d", idx)
	}
}

func TestComputeSplitIdx_ProtectedTailLargerThanWindow(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 100)}
	idx := a.computeSplitIdx(20, 30)
	if idx != 70 {
		t.Fatalf("expected split index 70 (keep 30), got %d", idx)
	}
}

func TestComputeSplitIdx_ZeroWindow(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 100)}
	idx := a.computeSplitIdx(0, 10)
	if idx != 90 {
		t.Fatalf("expected split index 90 (keep 10), got %d", idx)
	}
}

func TestComputeSplitIdx_NoSteps(t *testing.T) {
	a := &Actor{}
	idx := a.computeSplitIdx(20, 0)
	if idx != -1 {
		t.Fatalf("expected -1 with no steps, got %d", idx)
	}
}

func TestComputeSplitIdx_AllStepsProtected(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 10)}
	idx := a.computeSplitIdx(0, 10)
	if idx != -1 {
		t.Fatalf("expected -1 when all steps are protected, got %d", idx)
	}
}

func TestComputeSplitIdx_AlreadyCovered(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 100)}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 85, Level: 1, Text: "summary"},
	}
	idx := a.computeSplitIdx(20, 10)
	if idx != -1 {
		t.Fatalf("expected -1 when target range is already covered, got %d", idx)
	}
}

func TestComputeSplitIdx_PartiallyCovered(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 100)}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 70, Level: 1, Text: "summary"},
	}
	idx := a.computeSplitIdx(20, 10)
	if idx != 80 {
		t.Fatalf("expected split index 80, got %d", idx)
	}
}

func TestComputeProtectedTail_AssistantOnly(t *testing.T) {
	steps := []domain.Step{
		{Role: "user"},
		{Role: "assistant"},
	}
	tail := computeProtectedTail(steps)
	if tail != 1 {
		t.Fatalf("expected protected tail 1, got %d", tail)
	}
}

func TestComputeProtectedTail_AssistantWithTools(t *testing.T) {
	steps := []domain.Step{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "tool"},
	}
	tail := computeProtectedTail(steps)
	if tail != 3 {
		t.Fatalf("expected protected tail 3 (assistant + 2 tools), got %d", tail)
	}
}

func TestComputeProtectedTail_TrailingUser(t *testing.T) {
	steps := []domain.Step{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "user"},
	}
	tail := computeProtectedTail(steps)
	if tail != 1 {
		t.Fatalf("expected protected tail 1, got %d", tail)
	}
}

func TestComputeProtectedTail_SkipsCompactionStep(t *testing.T) {
	steps := []domain.Step{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "tool"},
		{Role: "system", Type: "text", Content: []domain.ContentBlock{{Type: "compaction", Text: "{}"}}},
	}
	tail := computeProtectedTail(steps)
	if tail != 2 {
		t.Fatalf("expected protected tail 2, got %d", tail)
	}
}

func TestRebuildSteps_RestoresCompactionFrames(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{{ID: "turn-1"}},
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
			},
			CompactionEvents: []domain.CompactionEvent{
				{
					TurnID:       "turn-1",
					Trigger:      "budget",
					BeforeTokens: 1000,
					AfterTokens:  500,
					Unit:         gen.ModelUnit{Model: "claude-sonnet-4-6"},
					CreatedAt:    "2026-06-14T00:00:00Z",
					Rounds: []gen.CompactionRoundRecord{
						{
							Round:                 1,
							SourceStartIndex:      0,
							SourceEndIndex:        4,
							Level:                 1,
							CompactedMessageCount: 5,
							BeforeLayout:          []gen.ContextSegment{{Kind: "message", Tokens: 1000}},
							AfterLayout:           []gen.ContextSegment{{Kind: "summary", Tokens: 500}},
						},
						{
							Round:                 2,
							SourceStartIndex:      0,
							SourceEndIndex:        4,
							Level:                 2,
							CompactedMessageCount: 1,
							AfterLayout:           []gen.ContextSegment{{Kind: "summary", Tokens: 200}},
						},
					},
				},
			},
		},
	}
	_ = a.rebuildSteps()
	if len(a.steps) != 3 {
		t.Fatalf("expected 3 steps (2 turn + 1 compaction), got %d", len(a.steps))
	}
	compactionStep := a.steps[2]
	if compactionStep.Role != "system" || compactionStep.Type != "text" {
		t.Fatalf("expected synthetic compaction step, got %+v", compactionStep)
	}
	if len(compactionStep.Content) != 1 || compactionStep.Content[0].Type != "compaction" {
		t.Fatalf("expected compaction content block, got %+v", compactionStep.Content)
	}
	var frame gen.CompactionFrameData
	if err := json.Unmarshal([]byte(compactionStep.Content[0].Text), &frame); err != nil {
		t.Fatalf("failed to parse compaction frame: %v", err)
	}
	if frame.Status != "completed" || frame.BeforeTokens != 1000 || frame.AfterTokens != 500 {
		t.Fatalf("unexpected frame content: %+v", frame)
	}
	if len(frame.Rounds) != 2 {
		t.Fatalf("expected 2 rounds, got %d", len(frame.Rounds))
	}
	if frame.Rounds[0].Round != 1 || frame.Rounds[0].Level != 1 || frame.Rounds[0].SourceEndIndex != 4 || frame.Rounds[0].CompactedMessageCount != 5 {
		t.Fatalf("expected first round fields preserved, got %+v", frame.Rounds[0])
	}
	if frame.Rounds[1].Round != 2 || frame.Rounds[1].Level != 2 || frame.Rounds[1].CompactedMessageCount != 1 {
		t.Fatalf("expected second round fields preserved, got %+v", frame.Rounds[1])
	}
}

func TestRebuildSteps_LegacyCompactionEventSeqIsFixed(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1"},
				{ID: "turn-2"},
			},
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Seq: 1, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Seq: 2, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
				{ID: "u2", Role: "user", Type: "text", TurnID: "turn-2", Seq: 3, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "next"}}},
				{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-2", Seq: 4, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "ok"}}},
			},
			CompactionEvents: []domain.CompactionEvent{
				{
					TurnID:       "turn-1",
					Trigger:      "budget",
					BeforeTokens: 1000,
					AfterTokens:  500,
					Unit:         gen.ModelUnit{Model: "claude-sonnet-4-6"},
					CreatedAt:    "2026-06-14T00:00:00Z",
					// Seq intentionally left as 0 to simulate legacy events.
				},
			},
		},
	}
	a.initNextSeq()
	_ = a.rebuildSteps()
	if len(a.steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(a.steps))
	}
	compactionStep := a.steps[2]
	if compactionStep.ID != "compaction-turn-1-0" {
		t.Fatalf("expected compaction step at index 2, got %q", compactionStep.ID)
	}
	if compactionStep.Seq <= 2 {
		t.Fatalf("expected compaction step Seq > turn-1's last step Seq (2), got %d", compactionStep.Seq)
	}
	if a.RawSession.CompactionEvents[0].Seq != compactionStep.Seq {
		t.Fatalf("expected CompactionEvent.Seq updated to %d, got %d", compactionStep.Seq, a.RawSession.CompactionEvents[0].Seq)
	}
	// Stable sort keeps the steps in order, but ensure turn-1 steps stay before turn-2.
	wantIDs := []string{"u1", "a1", "compaction-turn-1-0", "u2", "a2"}
	for i, want := range wantIDs {
		if a.steps[i].ID != want {
			t.Fatalf("expected step %d to be %q, got %q", i, want, a.steps[i].ID)
		}
	}
}

func TestRebuildSteps_KeepsCompactionStepsInsideTurn(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1"},
				{ID: "turn-2"},
			},
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
				{ID: "u2", Role: "user", Type: "text", TurnID: "turn-2", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "next"}}},
				{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-2", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "ok"}}},
			},
			CompactionEvents: []domain.CompactionEvent{
				{
					TurnID:       "turn-1",
					Trigger:      "budget",
					BeforeTokens: 1000,
					AfterTokens:  500,
					Unit:         gen.ModelUnit{Model: "claude-sonnet-4-6"},
					CreatedAt:    "2026-06-14T00:00:00Z",
				},
			},
		},
	}
	_ = a.rebuildSteps()
	if len(a.steps) != 5 {
		t.Fatalf("expected 5 steps (turn-1 x2 + compaction + turn-2 x2), got %d", len(a.steps))
	}
	wantIDs := []string{"u1", "a1", "compaction-turn-1-0", "u2", "a2"}
	for i, want := range wantIDs {
		if a.steps[i].ID != want {
			t.Fatalf("expected step %d to be %q, got %q (%+v)", i, want, a.steps[i].ID, a.steps[i])
		}
	}
}

func TestRebuildSteps_CompactionStepUsesTurnTimestamp(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", CompletedAt: "2026-06-14T09:59:35Z"},
				{ID: "turn-2", Timestamp: "2026-06-14T09:59:45Z"},
			},
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "u1", Role: "user", Type: "text", TurnID: "turn-1", Timestamp: "2026-06-14T09:59:00Z", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hello"}}},
				{ID: "a1", Role: "assistant", Type: "text", TurnID: "turn-1", Timestamp: "2026-06-14T09:59:30Z", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "hi"}}},
				{ID: "u2", Role: "user", Type: "text", TurnID: "turn-2", Timestamp: "2026-06-14T09:59:45Z", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "next"}}},
				{ID: "a2", Role: "assistant", Type: "text", TurnID: "turn-2", Timestamp: "2026-06-14T09:59:50Z", Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "ok"}}},
			},
			CompactionEvents: []domain.CompactionEvent{
				{
					TurnID:       "turn-1",
					Trigger:      "budget",
					BeforeTokens: 1000,
					AfterTokens:  500,
					Unit:         gen.ModelUnit{Model: "claude-sonnet-4-6"},
					CreatedAt:    "2026-06-14T10:05:00Z",
				},
			},
		},
	}
	_ = a.rebuildSteps()
	if len(a.steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(a.steps))
	}
	wantIDs := []string{"u1", "a1", "compaction-turn-1-0", "u2", "a2"}
	for i, want := range wantIDs {
		if a.steps[i].ID != want {
			t.Fatalf("expected step %d to be %q, got %q", i, want, a.steps[i].ID)
		}
	}
	compactionStep := a.steps[2]
	if compactionStep.Timestamp != "2026-06-14T09:59:35Z" {
		t.Fatalf("expected compaction timestamp to inherit turn CompletedAt, got %q", compactionStep.Timestamp)
	}
}

func TestCompactAsStep_AssignsSeqToEventAndStep(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(250)
	a.RawSession.Steps = append([]domain.Step(nil), a.steps...)
	a.RawSession.NextSeq = 1
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if len(a.RawSession.CompactionEvents) != 1 {
		t.Fatalf("expected 1 compaction event, got %d", len(a.RawSession.CompactionEvents))
	}
	ev := a.RawSession.CompactionEvents[0]
	if ev.Seq == 0 {
		t.Fatalf("compaction event has zero Seq")
	}
	if a.RawSession.NextSeq <= ev.Seq {
		t.Fatalf("NextSeq should be greater than event Seq, got NextSeq=%d event.Seq=%d", a.RawSession.NextSeq, ev.Seq)
	}
	var compactionStep *domain.Step
	for i := range a.steps {
		if a.steps[i].TurnID == "turn-1" && len(a.steps[i].Content) > 0 && a.steps[i].Content[0].Type == "compaction" {
			compactionStep = &a.steps[i]
			break
		}
	}
	if compactionStep == nil {
		t.Fatalf("compaction step not found in a.steps")
	}
	if compactionStep.Seq != ev.Seq {
		t.Fatalf("compaction step Seq=%d does not match event Seq=%d", compactionStep.Seq, ev.Seq)
	}
}

func TestComputeChunkSplitIdx_CompressesLargeHistoryByThreeQuarters(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 250)}
	idx := a.computeChunkSplitIdx(20, 1)
	if idx != 172 {
		t.Fatalf("expected dynamic split index 172, got %d", idx)
	}
}

func TestComputeChunkSplitIdx_CapsAt100WhenRemainingAround200(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 220)}
	idx := a.computeChunkSplitIdx(20, 1)
	if idx != 150 {
		t.Fatalf("expected split index 150, got %d", idx)
	}
}

func TestComputeChunkSplitIdx_UncoveredSmallerThanChunk(t *testing.T) {
	a := &Actor{steps: make([]domain.Step, 50)}
	idx := a.computeChunkSplitIdx(20, 1)
	if idx != 30 {
		t.Fatalf("expected split index 30 (keep 20), got %d", idx)
	}
}

func TestCompactAsStep_UsesL1PromptForSteps(t *testing.T) {
	var capturedPrompt string
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, systemPrompt string, _ time.Duration) (string, error) {
		capturedPrompt = systemPrompt
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if capturedPrompt == "" {
		t.Fatal("expected system prompt to be captured")
	}
	if capturedPrompt != agentkit.CompactionL1SummaryPrompt {
		t.Fatal("expected L1 summary prompt for step compaction")
	}
}

func TestCompactAsStep_UsesL2PromptForMerge(t *testing.T) {
	var capturedPrompt string
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, systemPrompt string, _ time.Duration) (string, error) {
		capturedPrompt = systemPrompt
		return "merged summary", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(10)
	longText := strings.Repeat("word ", 2000)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: longText},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: longText},
	}
	a.cfg.CompactionOverride.TokenBudget = 1000
	a.cfg.CompactionOverride.BudgetMode = "absolute"
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if capturedPrompt == "" {
		t.Fatal("expected system prompt to be captured")
	}
	if capturedPrompt != agentkit.CompactionL2PlusSummaryPrompt {
		t.Fatal("expected L2+ summary prompt for merge compaction")
	}
}

func TestCompactAsStep_RecompactResetsAndRuns(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		return "fresh summary", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(50)
	a.RawSession.Steps = append([]domain.Step(nil), a.steps...)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 20, Level: 1, Text: "old summary"},
	}
	a.RawSession.CompactionEvents = []domain.CompactionEvent{
		{TurnID: "turn-old", Trigger: "user", BeforeTokens: 1000, AfterTokens: 500, Unit: gen.ModelUnit{Model: "claude-3-5-haiku-20241022"}},
	}

	a.RawSession.SummarySegments = nil
	a.RawSession.CompactionEvents = nil
	_ = a.rebuildSteps()

	ctx := newFakeCompactionContext()
	if err := a.compactAsStep(ctx, "turn-1", "recompact", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}

	if len(a.RawSession.SummarySegments) == 0 {
		t.Fatal("expected new summary segments after recompact")
	}
	if len(a.RawSession.CompactionEvents) != 1 {
		t.Fatalf("expected 1 new compaction event, got %d", len(a.RawSession.CompactionEvents))
	}
	if a.RawSession.CompactionEvents[0].Trigger != "recompact" {
		t.Fatalf("expected trigger=recompact, got %q", a.RawSession.CompactionEvents[0].Trigger)
	}
	if a.RawSession.SummarySegments[0].SourceStartIndex != 0 {
		t.Fatalf("expected new summary to start at 0, got %d", a.RawSession.SummarySegments[0].SourceStartIndex)
	}
}

func TestCompactAsStep_RecompactIsForced(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		return "forced summary", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(10)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 4, Level: 1, Text: "old"},
		{SourceStartIndex: 5, SourceEndIndex: 9, Level: 1, Text: "old"},
	}
	a.cfg.CompactionOverride.TokenBudget = 50
	a.cfg.CompactionOverride.BudgetMode = "absolute"

	ctx := newFakeCompactionContext()
	if err := a.compactAsStep(ctx, "turn-1", "recompact", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}

	if len(a.RawSession.CompactionEvents) != 1 {
		t.Fatalf("expected recompact to run despite under budget, got %d events", len(a.RawSession.CompactionEvents))
	}
	if a.RawSession.CompactionEvents[0].Trigger != "recompact" {
		t.Fatalf("expected trigger=recompact, got %q", a.RawSession.CompactionEvents[0].Trigger)
	}
}

// TestCompactAsStep_ProbeFailureUsesLayoutFallback verifies that when the probe
// never reports usage (provider omits usage / probe errors), compaction still
// completes and the persisted frame carries non-zero before/after tokens taken
// from the tiktoken-backed layout estimate. Previously a failed probe left
// BeforeTokens/AfterTokens at 0, distorting the compaction UI, and made
// belowBudgetGate return false forever so compaction could not terminate.
func TestCompactAsStep_ProbeFailureUsesLayoutFallback(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, _ domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		return "fallback-path summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectFailingProbe(t)

	a := newChunkTestActor(50)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 20, Level: 1, Text: "old"},
		{SourceStartIndex: 21, SourceEndIndex: 40, Level: 1, Text: "old"},
	}
	a.cfg.CompactionOverride.TokenBudget = 50
	a.cfg.CompactionOverride.BudgetMode = "absolute"

	ctx := newFakeCompactionContext()
	if err := a.compactAsStep(ctx, "turn-1", "recompact", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}

	if len(a.RawSession.CompactionEvents) != 1 {
		t.Fatalf("expected 1 compaction event, got %d", len(a.RawSession.CompactionEvents))
	}
	ce := a.RawSession.CompactionEvents[0]
	if ce.BeforeTokens <= 0 {
		t.Fatalf("expected non-zero BeforeTokens from layout fallback, got %d", ce.BeforeTokens)
	}
	if ce.AfterTokens <= 0 {
		t.Fatalf("expected non-zero AfterTokens from layout fallback, got %d", ce.AfterTokens)
	}
	// AfterTokens should reflect the post-compaction layout (summaries + tail),
	// not the pre-compaction one; it must differ from the layout that included
	// the full uncompacted chunk when the layout actually shrank.
	if ce.AfterTokens > ce.BeforeTokens {
		t.Fatalf("expected AfterTokens <= BeforeTokens after compaction, got before=%d after=%d", ce.BeforeTokens, ce.AfterTokens)
	}
}

func TestCompactAsStep_AutoChunksLongHistory(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "auto", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if len(a.RawSession.SummarySegments) != 1 {
		t.Fatalf("expected 1 summary chunk, got %d", len(a.RawSession.SummarySegments))
	}
	seg := a.RawSession.SummarySegments[0]
	if seg.SourceStartIndex != 0 || seg.SourceEndIndex != 171 {
		t.Fatalf("expected chunk [0,171], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}
	if seg.Level != 1 {
		t.Fatalf("expected Level 1 summary, got %d", seg.Level)
	}
}

func TestCompactAsStep_UserForceRunsUnderBudget(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	a.cfg.CompactionOverride.TokenBudget = 200000
	a.cfg.CompactionOverride.BudgetMode = "absolute"
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if len(a.RawSession.SummarySegments) != 1 {
		t.Fatalf("expected forced summary chunk, got %d", len(a.RawSession.SummarySegments))
	}
	seg := a.RawSession.SummarySegments[0]
	if seg.SourceStartIndex != 0 || seg.SourceEndIndex != 171 {
		t.Fatalf("expected forced chunk [0,171], got [%d,%d]", seg.SourceStartIndex, seg.SourceEndIndex)
	}
}

func TestResolveCompactionPolicy_RespectsExplicitDisable(t *testing.T) {
	a := &Actor{}
	policy := a.resolveCompactionPolicy(domain.AgentKindConfig{
		CompactionPolicy: &domain.CompactionPolicy{Enabled: false, TokenBudget: 10},
	})
	if policy.Enabled {
		t.Fatal("explicitly disabled compaction policy was enabled")
	}
	if policy.TokenBudget != 10 {
		t.Fatalf("policy token budget = %d, want 10", policy.TokenBudget)
	}
}

// -- compaction lock bracket --

// TestCompactAsStep_RejectsConcurrentRun pins the busy semantics: a live lock
// blocks every compaction entry point and is left untouched.
func TestCompactAsStep_RejectsConcurrentRun(t *testing.T) {
	a := newChunkTestActor(50)
	held := &gen.CompactionLockState{TurnID: "turn-other", Trigger: "user", StartedAt: "2026-01-01T00:00:00Z", Seq: 7}
	a.RawSession.CompactionLock = held
	ctx := newFakeCompactionContext()

	err := a.compactAsStep(ctx, "turn-1", "user", "")
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("expected busy error, got %v", err)
	}
	if a.RawSession.CompactionLock != held {
		t.Fatal("busy path must not steal or clear the held lock")
	}
}

// TestCompactAsStep_LockReleasedOnSuccess pins the success half of the
// bracket: acquire → compact → release leaves no lock behind.
func TestCompactAsStep_LockReleasedOnSuccess(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(250)
	a.RawSession.NextSeq = 1
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if a.RawSession.CompactionLock != nil {
		t.Fatalf("lock not released after success: %+v", a.RawSession.CompactionLock)
	}
}

// TestCompactAsStep_LockReleasedOnFailure pins the failure half: a failed
// attempt also closes its lock so only a crash can leave it open.
func TestCompactAsStep_LockReleasedOnFailure(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "", fmt.Errorf("summarize exploded")
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(250)
	a.RawSession.NextSeq = 1
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err == nil {
		t.Fatal("expected compaction failure")
	}
	if a.RawSession.CompactionLock != nil {
		t.Fatalf("lock not released after failure: %+v", a.RawSession.CompactionLock)
	}
}

// TestCompactAsStep_EmptySummaryFailsExplicitly pins the guard behind the
// "empty compaction summary" error: a summarizer that returns "" with a nil
// error must fail the round loudly (never create false coverage), and the
// compaction lock must still be released.
func TestCompactAsStep_EmptySummaryFailsExplicitly(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "", nil
	}
	defer func() { summarizeViaPlan = original }()

	a := newChunkTestActor(250)
	a.RawSession.NextSeq = 1
	ctx := newFakeCompactionContext()

	err := a.compactAsStep(ctx, "turn-1", "user", "")
	if err == nil {
		t.Fatal("expected empty summary to fail compaction")
	}
	if !strings.Contains(err.Error(), "empty compaction summary") {
		t.Fatalf("error should name the empty summary, got: %v", err)
	}
	if len(a.RawSession.SummarySegments) != 0 {
		t.Fatalf("empty summary must not create coverage, got %d segments", len(a.RawSession.SummarySegments))
	}
	if a.RawSession.CompactionLock != nil {
		t.Fatalf("lock not released after failure: %+v", a.RawSession.CompactionLock)
	}
}

func newChunkTestActor(n int) *Actor {
	steps := make([]domain.Step, 0, n)
	for i := 0; i < n; i++ {
		steps = append(steps, domain.Step{
			ID:   fmt.Sprintf("a%d", i),
			Role: "assistant",
			Type: "text",
			Content: []domain.ContentBlock{{
				Type: domain.ContentBlockText,
				Text: strings.Repeat("x ", 500),
			}},
			Closed: true,
		})
	}
	return &Actor{
		steps: steps,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{},
		},
		status: turnStatus{
			Unit: domain.ModelUnit{Model: "claude-3-5-haiku-20241022", Provider: "anthropic"},
		},
		cfg: runtimeConfig{
			CompactionOverride: &domain.CompactionPolicy{
				Enabled:          true,
				BudgetMode:       "absolute",
				TokenBudget:      50000,
				RecentWindow:     20,
				MaxSummaryTokens: 4000,
			},
		},
	}
}

// injectUnderBudgetProbe replaces probeActualTokensFn with a stub that returns
// a real-looking token count below the configured budget, so belowBudgetGate
// stops compaction after the first round. Tests must defer-restore. This
// injects deterministic provider-reported tokens; it never estimates.
func injectUnderBudgetProbe(t *testing.T) {
	t.Helper()
	original := probeActualTokensFn
	probeActualTokensFn = func(*Actor, actor.Context, string) (int64, error) {
		return 1, nil
	}
	t.Cleanup(func() { probeActualTokensFn = original })
}

// injectFailingProbe replaces probeActualTokensFn with a stub that simulates a
// provider/probe that never reports usage (returns 0 + error). Used to verify
// the tiktoken layout fallback keeps compaction able to decide and the frame
// able to show a non-zero size. Tests must defer-restore.
func injectFailingProbe(t *testing.T) {
	t.Helper()
	original := probeActualTokensFn
	probeActualTokensFn = func(*Actor, actor.Context, string) (int64, error) {
		return 0, fmt.Errorf("probe unavailable")
	}
	t.Cleanup(func() { probeActualTokensFn = original })
}

// -- test doubles for compaction --

type fakeCompactionContext struct {
	actor.Context
	idSeq   int64
	planner actor.Planner
}

func newFakeCompactionContext() *fakeCompactionContext {
	return &fakeCompactionContext{}
}

func (c *fakeCompactionContext) Self() ref.Ref {
	return fakeCompactionRef{}
}

func (c *fakeCompactionContext) Parent() ref.Ref {
	return nil
}

func (c *fakeCompactionContext) NewID() id.ActorID {
	n := atomic.AddInt64(&c.idSeq, 1)
	s := fmt.Sprintf("%032x", n)
	aid, _ := id.Parse(s)
	return aid
}

func (c *fakeCompactionContext) EmitEvent(string, any) error {
	return nil
}

type recordedEvent struct {
	topic string
	event any
}

type recordingCompactionContext struct {
	fakeCompactionContext
	events []recordedEvent
}

func newRecordingCompactionContext() *recordingCompactionContext {
	return &recordingCompactionContext{}
}

func (c *recordingCompactionContext) EmitEvent(topic string, event any) error {
	c.events = append(c.events, recordedEvent{topic: topic, event: event})
	return nil
}

func (c *fakeCompactionContext) Lifecycle() context.Context {
	return context.Background()
}

func (c *fakeCompactionContext) Planner() actor.Planner {
	return c.planner
}

func (c *fakeCompactionContext) Destroy(ref.Ref) error {
	return nil
}

func (c *fakeCompactionContext) Logger() actor.Logger {
	return fakeCompactionLogger{}
}

func (c *fakeCompactionContext) After(_ time.Duration, _ string, _ any) error {
	return nil
}

func (c *fakeCompactionContext) LookupService(_ string) (ref.Ref, bool) {
	return nil, false
}

type fakeCompactionRef struct{ tag string }

func (r fakeCompactionRef) ID() id.ActorID {
	if r.tag != "" {
		aid, _ := id.Parse(strings.Repeat("0", 31-len(r.tag)) + r.tag)
		return aid
	}
	aid, _ := id.Parse(strings.Repeat("0", 31) + "1")
	return aid
}

func (fakeCompactionRef) Service() (string, bool) {
	return "", false
}

func (fakeCompactionRef) Invoke(ctx context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	return nil
}

type fakeCompactionLogger struct{}

func (fakeCompactionLogger) Debug(string, ...any) {}
func (fakeCompactionLogger) Info(string, ...any)  {}
func (fakeCompactionLogger) Warn(string, ...any)  {}
func (fakeCompactionLogger) Error(string, ...any) {}

type capturePlanner struct {
	payload any
	node    plan.Node
}

func (p *capturePlanner) Plan(_ ref.Ref, _ string, payload any, _ ...plan.Option) (plan.Node, error) {
	p.payload = payload
	return p.node, nil
}

func (p *capturePlanner) Call(context.Context, ref.Ref, string, any) *promise.Promise[any] {
	return nil
}

func (p *capturePlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

type fakePlanNode struct {
	value    any
	consumed bool
}

func (n *fakePlanNode) Ref() ref.Ref                { return fakeCompactionRef{} }
func (n *fakePlanNode) State() plan.State           { return plan.StateCompleted }
func (n *fakePlanNode) Start(context.Context) error { return nil }
func (n *fakePlanNode) Stop() error                 { return nil }
func (n *fakePlanNode) Recv() (any, error) {
	if !n.consumed {
		n.consumed = true
		return n.value, nil
	}
	return nil, io.EOF
}
func (n *fakePlanNode) Result() (any, error)  { return n.value, nil }
func (n *fakePlanNode) Done() <-chan struct{} { return nil }
func (n *fakePlanNode) Target() ref.Ref       { return fakeCompactionRef{} }
func (n *fakePlanNode) CallID() string        { return "" }
func (n *fakePlanNode) Payload() any          { return nil }

func TestSummarizeViaPlan_SetsModelAndTitle(t *testing.T) {
	node := &fakePlanNode{value: domain.SummarizeResp{Text: "summary text"}}
	planner := &capturePlanner{node: node}
	ctx := &fakeCompactionContext{planner: planner}

	_, err := summarizeViaPlanImpl(ctx, fakeCompactionRef{}, "session-1", domain.ModelUnit{Model: "claude-3-5-haiku-20241022", Provider: "primary-provider"}, "input text", "test-system-prompt", time.Minute)
	if err != nil {
		t.Fatalf("summarizeViaPlanImpl failed: %v", err)
	}
	if planner.payload == nil {
		t.Fatalf("expected payload to be captured")
	}

	req, ok := planner.payload.(domain.SendSessionMessageReq)
	if !ok {
		t.Fatalf("expected payload to be SendSessionMessageReq, got %T", planner.payload)
	}
	if req.Unit == nil || req.Unit.Model != "claude-3-5-haiku-20241022" {
		t.Fatalf("expected Unit.Model to be passed through, got %+v", req.Unit)
	}
	if req.Unit == nil || req.Unit.Provider != "primary-provider" {
		t.Fatalf("expected Unit.Provider to be passed through, got %+v", req.Unit)
	}
	if req.System != "test-system-prompt" {
		t.Fatalf("expected System prompt to be passed through, got %q", req.System)
	}
}

type blockingCompactionNode struct {
	started  chan context.Context
	done     chan struct{}
	stopOnce sync.Once
}

func (n *blockingCompactionNode) Ref() ref.Ref      { return fakeCompactionRef{} }
func (n *blockingCompactionNode) State() plan.State { return plan.StateRunning }
func (n *blockingCompactionNode) Start(ctx context.Context) error {
	n.started <- ctx
	return nil
}
func (n *blockingCompactionNode) Stop() error {
	n.stopOnce.Do(func() { close(n.done) })
	return nil
}
func (n *blockingCompactionNode) Recv() (any, error) {
	<-n.done
	return nil, context.Canceled
}
func (n *blockingCompactionNode) Result() (any, error)  { return nil, context.Canceled }
func (n *blockingCompactionNode) Done() <-chan struct{} { return n.done }
func (n *blockingCompactionNode) Target() ref.Ref       { return fakeCompactionRef{} }
func (n *blockingCompactionNode) CallID() string        { return "aiaggregator.summarize" }
func (n *blockingCompactionNode) Payload() any          { return nil }

func TestSummarizeViaPlan_EngineCancelPropagates(t *testing.T) {
	node := &blockingCompactionNode{started: make(chan context.Context, 1), done: make(chan struct{})}
	planner := &capturePlanner{node: node}
	baseCtx := &fakeCompactionContext{planner: planner}
	e := &turnEngine{done: make(chan struct{})}
	ctx := turnActorContext{Context: baseCtx, lifecycle: e.turnContext(baseCtx.Lifecycle())}
	result := make(chan error, 1)
	go func() {
		_, err := summarizeViaPlanImpl(ctx, fakeCompactionRef{}, "session-1", domain.ModelUnit{Model: "m"}, "input", "system", 5*time.Minute)
		result <- err
	}()

	var callCtx context.Context
	select {
	case callCtx = <-node.started:
	case <-time.After(time.Second):
		t.Fatal("compaction LLM call did not start")
	}
	e.cancel()
	select {
	case <-callCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("engine cancellation did not reach compaction context")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("compaction error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("compaction remained blocked after engine cancellation")
	}
}

// TestSummarizeViaPlan_PrefersArrivedResultOverTimeout is a regression test for
// the boundary race: when the LLM finishes right at the deadline, the buffered
// result channel is ready at the same instant as timeoutCtx.Done(). A symmetric
// select discards the completed summary ~50% of the time. The post-timeout
// drain must always prefer the already-arrived result.
func TestSummarizeViaPlan_PrefersArrivedResultOverTimeout(t *testing.T) {
	for i := 0; i < 200; i++ {
		node := &fakePlanNode{value: domain.SummarizeResp{Text: "late summary"}}
		planner := &capturePlanner{node: node}
		ctx := &fakeCompactionContext{planner: planner}

		// A near-zero deadline forces both channels to be ready together; the
		// result is delivered to the buffered ch essentially immediately.
		text, err := summarizeViaPlanImpl(ctx, fakeCompactionRef{}, "s", domain.ModelUnit{Model: "m"}, "input", "sys", time.Millisecond)
		if err != nil {
			t.Fatalf("iter %d: expected the completed summary despite a tight deadline, got timeout error: %v", i, err)
		}
		if text != "late summary" {
			t.Fatalf("iter %d: expected %q, got %q", i, "late summary", text)
		}
	}
}

func TestMergeSummarySegments_DropsEmptyAndOverlaps(t *testing.T) {
	existing := []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 49, Text: "old", Level: 1},
		{SourceStartIndex: 50, SourceEndIndex: 99, Text: "", Level: 1},
	}
	incoming := []domain.SummarySegment{
		{SourceStartIndex: 50, SourceEndIndex: 99, Text: "new", Level: 1},
		{SourceStartIndex: 100, SourceEndIndex: 149, Text: "", Level: 1},
	}
	merged := mergeSummarySegments(existing, incoming)
	if len(merged) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(merged))
	}
	if merged[0].Text != "old" || merged[0].SourceEndIndex != 49 {
		t.Fatalf("expected old non-overlapping segment, got %+v", merged[0])
	}
	if merged[1].Text != "new" || merged[1].SourceStartIndex != 50 || merged[1].SourceEndIndex != 99 {
		t.Fatalf("expected new overlapping segment, got %+v", merged[1])
	}
}

func TestCompactAsStep_ResumesAfterGap(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "filled gap summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(220)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 99, Level: 1, Text: "first summary"},
		{SourceStartIndex: 200, SourceEndIndex: 219, Level: 1, Text: "last summary"},
	}
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if len(a.RawSession.SummarySegments) != 3 {
		t.Fatalf("expected 3 summary segments, got %d", len(a.RawSession.SummarySegments))
	}
	var gapSeg *domain.SummarySegment
	for i := range a.RawSession.SummarySegments {
		seg := &a.RawSession.SummarySegments[i]
		if seg.SourceStartIndex == 100 && seg.SourceEndIndex == 199 {
			gapSeg = seg
		}
	}
	if gapSeg == nil {
		t.Fatalf("expected a new segment covering gap 100-199, got %+v", a.RawSession.SummarySegments)
	}
	if gapSeg.Text != "filled gap summary" {
		t.Fatalf("unexpected gap summary text: %q", gapSeg.Text)
	}
}

func TestCompactAsStep_EmitsRunningFramesAndBudgetEvents(t *testing.T) {
	original := summarizeViaPlan
	summarizeViaPlan = func(actor.Context, ref.Ref, string, domain.ModelUnit, string, string, time.Duration) (string, error) {
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	a.cfg.CompactionOverride.TokenBudget = 30000
	a.status.ContextBudget = &domain.TurnContextBudgetPayload{
		EstimatedTokens:   100000,
		ContextWindowSize: 200000,
		TokenBudget:       30000,
	}
	ctx := newRecordingCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "auto", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}

	var runningFrames []gen.CompactionFrameData
	var budgetEvents []domain.TurnEvent
	for _, rec := range ctx.events {
		if rec.topic == "step" {
			ev, ok := rec.event.(domain.StepEvent)
			if !ok || ev.Kind != "block.appended" || ev.Block == nil || ev.Block.Type != "compaction" {
				continue
			}
			var frame gen.CompactionFrameData
			if err := json.Unmarshal([]byte(ev.Block.Text), &frame); err != nil {
				t.Fatalf("failed to parse compaction frame: %v", err)
			}
			if frame.Status == "running" {
				runningFrames = append(runningFrames, frame)
			}
		} else if rec.topic == "turn" {
			ev, ok := rec.event.(domain.TurnEvent)
			if ok && ev.Kind == domain.TurnContextBudget {
				budgetEvents = append(budgetEvents, ev)
			}
		}
	}

	if len(runningFrames) < 2 {
		t.Fatalf("expected at least 2 running frames, got %d", len(runningFrames))
	}

	for i, frame := range runningFrames {
		// The first running frame is the preview with no completed rounds yet.
		if i > 0 && len(frame.Rounds) == 0 {
			t.Fatalf("running frame %d has no rounds", i)
		}
		if i > 0 && len(frame.Rounds) < len(runningFrames[i-1].Rounds) {
			t.Fatalf("running frame rounds should not shrink: frame %d has %d, previous had %d", i, len(frame.Rounds), len(runningFrames[i-1].Rounds))
		}
	}

	last := runningFrames[len(runningFrames)-1]
	meaningful := 0
	for _, r := range last.Rounds {
		if len(r.CompactedRanges) > 0 {
			meaningful++
		}
	}
	if meaningful < 1 {
		t.Fatalf("expected at least 1 meaningful round in final running frame, got %d", meaningful)
	}

	if len(budgetEvents) == 0 {
		t.Fatalf("expected at least one turn.context_budget event during compaction")
	}
	for i, ev := range budgetEvents {
		if ev.ContextBudget == nil {
			t.Fatalf("budget event %d has nil ContextBudget", i)
		}
	}
}

// TestEffectiveSummarySlot_OverridePrependedAsHead verifies a policy override
// is prepended as the candidate-chain head (highest priority), ahead of the
// configured summary slot. The override is a unit-kind candidate so resolveTarget
// pins its model.
func TestEffectiveSummarySlot_OverridePrependedAsHead(t *testing.T) {
	a := &Actor{
		summary: slotFromUnit(domain.ModelUnit{Model: "slot-model", Provider: "slot-provider"}),
	}
	policy := &domain.CompactionPolicy{
		SummaryUnit: &domain.ModelUnit{Model: "override-model", Provider: "override-provider"},
	}
	got := a.effectiveSummarySlot(policy)
	if len(got.Candidates) != 2 {
		t.Fatalf("expected 2 candidates (override + slot), got %d", len(got.Candidates))
	}
	head := got.Candidates[0]
	if head.Kind != modelRefKindUnit || head.Unit == nil || head.Unit.Model != "override-model" || head.Unit.Provider != "override-provider" {
		t.Fatalf("override must be unit-kind head, got %+v", head)
	}
	if got.Candidates[1].Unit == nil || got.Candidates[1].Unit.Model != "slot-model" {
		t.Fatalf("configured slot candidate must follow override, got %+v", got.Candidates[1])
	}
}

// TestEffectiveSummarySlot_NoOverrideReturnsConfiguredSlot verifies that
// without a concrete override (nil policy, or SummaryUnit with empty Model)
// the configured summary slot is returned unchanged.
func TestEffectiveSummarySlot_NoOverrideReturnsConfiguredSlot(t *testing.T) {
	configured := slotFromUnit(domain.ModelUnit{Model: "slot-model", Provider: "slot-provider"})
	a := &Actor{summary: configured}
	if got := a.effectiveSummarySlot(nil); got.Candidates[0].Unit.Model != "slot-model" {
		t.Fatalf("nil policy should return configured slot, got %+v", got)
	}
	if got := a.effectiveSummarySlot(&domain.CompactionPolicy{SummaryUnit: &domain.ModelUnit{Model: ""}}); got.Candidates[0].Unit.Model != "slot-model" {
		t.Fatalf("empty-model override should return configured slot, got %+v", got)
	}
}

// TestCompactAsStep_PinsSummarySlotUnit verifies the summary slot's configured
// model unit is the one actually passed to the summarization call — compaction
// honors the summary slot, not the primary model.
func TestCompactAsStep_PinsSummarySlotUnit(t *testing.T) {
	var capturedUnit domain.ModelUnit
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, unit domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		capturedUnit = unit
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	a.summary = slotFromUnit(domain.ModelUnit{Model: "summary-slot-model", Provider: "summary-provider"})
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if capturedUnit.Model != "summary-slot-model" || capturedUnit.Provider != "summary-provider" {
		t.Fatalf("expected summary slot model pinned, got %+v", capturedUnit)
	}
}

// TestCompactAsStep_FallsBackPastUnresolvableSummaryAggregator verifies the
// core fix: when the summary slot's head candidate is a named aggregator that
// is not yet bound, resolveTarget skips it and pins the next candidate's unit.
// Previously the unit and aggregator were resolved by separate code paths, so
// the pinned unit was dropped (empty) even though a fallback unit was configured.
func TestCompactAsStep_FallsBackPastUnresolvableSummaryAggregator(t *testing.T) {
	var capturedUnit domain.ModelUnit
	original := summarizeViaPlan
	summarizeViaPlan = func(_ actor.Context, _ ref.Ref, _ string, unit domain.ModelUnit, _ string, _ string, _ time.Duration) (string, error) {
		capturedUnit = unit
		return "chunk summary", nil
	}
	defer func() { summarizeViaPlan = original }()
	injectUnderBudgetProbe(t)

	a := newChunkTestActor(250)
	// Head is a named aggregator absent from newChunkTestActor's cache (only
	// systemAggID is wired), so it is unresolvable; the unit candidate that
	// follows must be pinned instead.
	a.summary = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "named-agg"},
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fallback-model", Provider: "fallback-provider"}},
	}}
	ctx := newFakeCompactionContext()

	if err := a.compactAsStep(ctx, "turn-1", "user", ""); err != nil {
		t.Fatalf("compactAsStep failed: %v", err)
	}
	if capturedUnit.Model != "fallback-model" || capturedUnit.Provider != "fallback-provider" {
		t.Fatalf("expected fallback unit pinned past unresolvable aggregator, got %+v", capturedUnit)
	}
}

type fakeStatusPlanner struct {
	status gen.AIAggregatorStatusResp
}

func (p *fakeStatusPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p *fakeStatusPlanner) Call(context.Context, ref.Ref, string, any) *promise.Promise[any] {
	return promise.Async(func(resolve func(any), reject func(any)) {
		resolve(p.status)
	})
}

func (p *fakeStatusPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return promise.Reject[any](fmt.Errorf("not implemented"))
}
