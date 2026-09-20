package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type donePureContext struct {
	actor.PureContext
	done <-chan struct{}
}

func (c donePureContext) Done() <-chan struct{} { return c.done }

func TestHandleSessionImport_ReplacesTurns(t *testing.T) {
	turns := []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
		{ID: "t3", Role: "user"},
	}
	steps := []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
		{ID: "s3", TurnID: "t2"},
		{ID: "s4", TurnID: "t3"},
	}
	a := &Actor{RawSession: domain.RawSession{Steps: steps}}
	resp, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{Turns: turns, ActiveHead: 1},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AcceptedTurns != 3 {
		t.Errorf("AcceptedTurns = %d, want 3", resp.AcceptedTurns)
	}
	if resp.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1", resp.ActiveHead)
	}
	if len(a.Session.Turns) != 3 {
		t.Errorf("len(Session.Turns) = %d, want 3", len(a.Session.Turns))
	}
	if a.Session.ActiveHead != 1 {
		t.Errorf("Session.ActiveHead = %d, want 1", a.Session.ActiveHead)
	}
	if wantSteps := 4; len(a.steps) != wantSteps {
		t.Errorf("len(steps) = %d, want %d after rebuild", len(a.steps), wantSteps)
	}
	snap := a.snapshot.Load()
	if snap == nil {
		t.Fatalf("snapshot not refreshed")
	}
}

func TestHandleSessionImport_ClampsOutOfRangeHead(t *testing.T) {
	a := &Actor{}
	turns := []domain.Turn{
		{ID: "t1"},
		{ID: "t2"},
	}
	resp, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{Turns: turns, ActiveHead: 99},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1 (clamped to last turn)", resp.ActiveHead)
	}
	if a.Session.ActiveHead != 1 {
		t.Errorf("Session.ActiveHead = %d, want 1", a.Session.ActiveHead)
	}
}

func TestHandleSessionImport_EmptyIsNotError(t *testing.T) {
	a := &Actor{}
	resp, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{Turns: nil, ActiveHead: 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AcceptedTurns != 0 {
		t.Errorf("AcceptedTurns = %d, want 0", resp.AcceptedTurns)
	}
	if resp.ActiveHead != -1 {
		t.Errorf("ActiveHead = %d, want -1 (empty session)", resp.ActiveHead)
	}
}

func TestHandleSessionImport_IsolatesCallerSlice(t *testing.T) {
	a := &Actor{}
	turns := []domain.Turn{
		{ID: "t1", UserInput: "original"},
	}
	_, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{Turns: turns, ActiveHead: 0},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	turns[0].UserInput = "mutated by caller"
	if a.Session.Turns[0].UserInput != "original" {
		t.Errorf("agent Turns mutated by caller — defensive copy missing")
	}
}

func TestHandleTurnsList_IncludesStepsForReturnedTurns(t *testing.T) {
	turns := []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
		{ID: "t3", Role: "user"},
	}
	steps := []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
		{ID: "s3", TurnID: "t2"},
		{ID: "s4", TurnID: "t3"},
	}
	a := &Actor{}
	a.snapshot.Store(&sessionSnapshotData{
		turns: turns,
		steps: steps,
	})

	resp, err := a.handleTurnsList(nil, domain.AgentTurnsListReq{BeforeTurnID: "t3", Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Turns) != 2 {
		t.Fatalf("len(Turns) = %d, want 2", len(resp.Turns))
	}
	if len(resp.Steps) != 3 {
		t.Errorf("len(Steps) = %d, want 3 (steps for t1 and t2)", len(resp.Steps))
	}
	stepIDs := make(map[string]struct{}, len(resp.Steps))
	for _, s := range resp.Steps {
		stepIDs[s.ID] = struct{}{}
	}
	for _, want := range []string{"s1", "s2", "s3"} {
		if _, ok := stepIDs[want]; !ok {
			t.Errorf("missing step %s", want)
		}
	}
	if _, ok := stepIDs["s4"]; ok {
		t.Errorf("unexpected step s4 (belongs to unloaded turn t3)")
	}
}

func TestHandleTurnsList_PaginatesBySeqNotPhysicalOrder(t *testing.T) {
	// Physical order is disturbed: a legacy/ghost turn (seq 1) sits at the end.
	// Pagination must follow Seq order, not the slice position.
	turns := []domain.Turn{
		{ID: "t2", Role: "assistant", Seq: 10},
		{ID: "t3", Role: "user", Seq: 20},
		{ID: "t4", Role: "assistant", Seq: 30},
		{ID: "ghost", Role: "assistant", Seq: 1},
	}
	a := &Actor{}
	a.snapshot.Store(&sessionSnapshotData{turns: turns})

	// Anchor on t2 (seq 10): the only turn older than it is ghost (seq 1).
	resp, err := a.handleTurnsList(nil, domain.AgentTurnsListReq{BeforeTurnID: "t2", Limit: 20})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Turns) != 1 || resp.Turns[0].ID != "ghost" {
		got := make([]string, 0, len(resp.Turns))
		for _, tu := range resp.Turns {
			got = append(got, tu.ID)
		}
		t.Fatalf("Turns = %v, want [ghost]", got)
	}
	if resp.HasMore {
		t.Errorf("HasMore = true, want false (ghost is the oldest)")
	}
}

func TestInitNextSeq_SetsMaxPlusOne(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Seq: 10},
			},
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{ID: "live", Seq: 20},
			},
			CompactionEvents: []domain.CompactionEvent{
				{TurnID: "t1", Seq: 15},
			},
		},
		steps: []domain.Step{
			{ID: "live", Seq: 20},
		},
	}
	a.initNextSeq()
	if a.RawSession.NextSeq != 21 {
		t.Fatalf("expected NextSeq=21, got %d", a.RawSession.NextSeq)
	}
}

func TestInitNextSeq_DefaultsToOne(t *testing.T) {
	a := &Actor{}
	a.initNextSeq()
	if a.RawSession.NextSeq != 1 {
		t.Fatalf("expected NextSeq=1 for empty session, got %d", a.RawSession.NextSeq)
	}
}

func TestHandleSessionFork_TransfersSummaryAndArtifacts(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "user"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "seg-0-2", SourceStartIndex: 0, SourceEndIndex: 2},
				{Text: "seg-0-4", SourceStartIndex: 0, SourceEndIndex: 4},
				{Text: "seg-15-20", SourceStartIndex: 15, SourceEndIndex: 20},
			},
			ExploreResults: []domain.ExploreResult{
				{TurnID: "t1", Summary: "explore-t1"},
				{TurnID: "t2", Summary: "explore-t2"},
				{TurnID: "t3", Summary: "explore-t3"},
			},
			NextIdx: 42,
			NextSeq: 99,
			Goal:    &gen.SessionGoal{Condition: "finish X", MaxTurns: 10, TurnCount: 2},
		},
	}
	// Simulate steps owned by each turn. The head is t2, so steps for t1/t2
	// (indices 0-3) are retained and steps for t3 are dropped.
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t1"},
		{ID: "s3", TurnID: "t2"},
		{ID: "s4", TurnID: "t2"},
		{ID: "s5", TurnID: "t3"},
		{ID: "s6", TurnID: "t3"},
	}
	a.takeSnapshot()

	resp, err := a.handleSessionFork(nil, domain.AgentSessionForkReq{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, want := len(resp.Session.Turns), 2; got != want {
		t.Errorf("fork kept %d turns, want %d", got, want)
	}
	if resp.Goal == nil || resp.Goal.Condition != "finish X" {
		t.Errorf("goal not transferred: %+v", resp.Goal)
	}
	if resp.NextIdx != 42 {
		t.Errorf("NextIdx = %d, want 42", resp.NextIdx)
	}
	if resp.NextSeq != 99 {
		t.Errorf("NextSeq = %d, want 99", resp.NextSeq)
	}
	if len(resp.Steps) != 4 {
		t.Errorf("fork kept %d steps, want 4", len(resp.Steps))
	}
	for _, step := range resp.Steps {
		if step.TurnID == "t3" {
			t.Errorf("leaked step for dropped turn t3: %s", step.ID)
		}
	}
	if len(resp.SummarySegments) != 2 {
		t.Errorf("expected 2 summary segments (seg-0-2, seg-0-4), got %d: %+v", len(resp.SummarySegments), resp.SummarySegments)
	}
	for _, seg := range resp.SummarySegments {
		if seg.Text == "seg-15-20" {
			t.Errorf("leaked summary segment for dropped turns")
		}
	}
	if len(resp.ExploreResults) != 2 {
		t.Errorf("expected 2 explore results (t1,t2), got %d: %+v", len(resp.ExploreResults), resp.ExploreResults)
	}
	for _, er := range resp.ExploreResults {
		if er.TurnID == "t3" {
			t.Errorf("leaked explore result for dropped turn t3")
		}
	}
}

func TestHandleSessionFork_AtTurnIDOverridesHead(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "user"},
			},
			ActiveHead: 2,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "early", SourceStartIndex: 0, SourceEndIndex: 2},
				{Text: "late", SourceStartIndex: 15, SourceEndIndex: 20},
			},
			ExploreResults: []domain.ExploreResult{
				{TurnID: "t1"},
				{TurnID: "t2"},
				{TurnID: "t3"},
			},
		},
	}
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
		{ID: "s3", TurnID: "t3"},
	}
	a.takeSnapshot()

	resp, err := a.handleSessionFork(nil, domain.AgentSessionForkReq{AtTurnID: "t2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := len(resp.Session.Turns), 2; got != want {
		t.Errorf("fork at t2 kept %d turns, want %d", got, want)
	}
	if len(resp.Steps) != 2 {
		t.Errorf("fork at t2 kept %d steps, want 2", len(resp.Steps))
	}
	if len(resp.SummarySegments) != 1 || resp.SummarySegments[0].Text != "early" {
		t.Errorf("expected only 'early' summary segment, got %+v", resp.SummarySegments)
	}
	if len(resp.ExploreResults) != 2 {
		t.Errorf("expected 2 explore results (t1,t2), got %d", len(resp.ExploreResults))
	}
}

func TestHandleSessionImport_WritesRawSessionFields(t *testing.T) {
	a := &Actor{}
	segs := []domain.SummarySegment{
		{Text: "imported-seg", SourceStartIndex: 0, SourceEndIndex: 9},
	}
	explores := []domain.ExploreResult{
		{TurnID: "t1", Summary: "imported-explore"},
	}
	steps := []domain.Step{
		{ID: "s1", TurnID: "t1", Role: "user", Type: "text"},
		{ID: "s2", TurnID: "t1", Role: "assistant", Type: "text"},
	}
	goal := &gen.SessionGoal{Condition: "do Y", MaxTurns: 5, TurnCount: 1}

	_, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{
			Turns: []domain.Turn{{ID: "t1", Role: "user", Seq: 100}},
		},
		SummarySegments: segs,
		ExploreResults:  explores,
		Steps:           steps,
		Goal:            goal,
		NextIdx:         7,
		NextSeq:         200,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(a.RawSession.SummarySegments) != 1 || a.RawSession.SummarySegments[0].Text != "imported-seg" {
		t.Errorf("SummarySegments not written: %+v", a.RawSession.SummarySegments)
	}
	if len(a.RawSession.ExploreResults) != 1 || a.RawSession.ExploreResults[0].TurnID != "t1" {
		t.Errorf("ExploreResults not written: %+v", a.RawSession.ExploreResults)
	}
	if len(a.RawSession.Steps) != 2 || a.RawSession.Steps[0].ID != "s1" {
		t.Errorf("Steps not written: %+v", a.RawSession.Steps)
	}
	if a.RawSession.Goal == nil || a.RawSession.Goal.Condition != "do Y" {
		t.Errorf("Goal not written: %+v", a.RawSession.Goal)
	}
	if a.RawSession.NextIdx != 7 {
		t.Errorf("NextIdx = %d, want 7", a.RawSession.NextIdx)
	}
	if a.RawSession.NextSeq < 200 {
		t.Errorf("NextSeq = %d, want >= 200 (imported floor)", a.RawSession.NextSeq)
	}

	// Mutating the caller's slice must not leak into actor state.
	segs[0].Text = "mutated"
	steps[0].Role = "mutated"
	if a.RawSession.SummarySegments[0].Text != "imported-seg" {
		t.Errorf("SummarySegments not defensively copied on import")
	}
	if a.RawSession.Steps[0].Role != "user" {
		t.Errorf("Steps not defensively copied on import")
	}

	snap := a.snapshot.Load()
	if snap == nil || len(snap.summarySegments) != 1 {
		t.Errorf("snapshot not refreshed with summary segments")
	}
	if snap == nil || len(snap.steps) != 2 {
		t.Errorf("snapshot not refreshed with steps")
	}
	if snap == nil || snap.nextIdx != 7 || snap.nextSeq < 200 {
		t.Errorf("snapshot NextIdx/NextSeq not refreshed: %+v", snap)
	}
}

func TestApplyStepEvent_ExecutionProgressStoresProgress(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Type: "tool_call", Role: "assistant", Content: []domain.ContentBlock{{Type: domain.ContentBlockToolUse, ToolUseID: "tu1"}}},
	}

	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.execution_progress",
		StepID:   "s1",
		TurnID:   "t1",
		Progress: `{"phase":"searching","searchCount":3,"summaryText":"partial"}`,
	})

	if a.steps[0].ExecutionStatus != "in_progress" {
		t.Errorf("ExecutionStatus = %q, want in_progress", a.steps[0].ExecutionStatus)
	}
	if a.steps[0].Progress != `{"phase":"searching","searchCount":3,"summaryText":"partial"}` {
		t.Errorf("Progress = %q, want raw progress JSON", a.steps[0].Progress)
	}
}

// TestApplyStepEvent_ResetClearsStreamedContent verifies step.reset: text and
// reasoning content are cleared, the step stays open, and the explorer child
// summary loses exactly the removed suffix.
func TestApplyStepEvent_ResetClearsStreamedContent(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}
	a.agentKind = "explorer"
	a.child.Mode = true
	a.child.SummaryText = "earlier. partial text"
	a.steps = []domain.Step{
		{ID: "llm-1", TurnID: "t1", Type: "text", Role: "assistant", Closed: false,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "partial text"}}},
		{ID: "rs-1", TurnID: "t1", Type: "reasoning", Role: "assistant", Closed: false,
			ReasoningContent: "partial thinking"},
	}

	a.applyStepEvent(domain.StepEvent{Kind: "step.reset", StepID: "llm-1", TurnID: "t1"})
	a.applyStepEvent(domain.StepEvent{Kind: "step.reset", StepID: "rs-1", TurnID: "t1"})

	if a.steps[0].Content[0].Text != "" {
		t.Errorf("llm step text = %q, want cleared", a.steps[0].Content[0].Text)
	}
	if a.steps[0].Closed {
		t.Error("llm step must stay open across the retry")
	}
	if a.steps[1].ReasoningContent != "" {
		t.Errorf("reasoning content = %q, want cleared", a.steps[1].ReasoningContent)
	}
	if a.child.SummaryText != "earlier. " {
		t.Errorf("SummaryText = %q, want trimmed suffix", a.child.SummaryText)
	}
}

// TestApplyStepEvent_ResetNoTrimWithoutSuffix verifies the summary trim is
// skipped when the removed text is not a suffix of the summary (defensive —
// no corruption, just no trim).
func TestApplyStepEvent_ResetNoTrimWithoutSuffix(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}
	a.agentKind = "explorer"
	a.child.Mode = true
	a.child.SummaryText = "unrelated"
	a.steps = []domain.Step{
		{ID: "llm-1", TurnID: "t1", Type: "text", Role: "assistant",
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "partial"}}},
	}

	a.applyStepEvent(domain.StepEvent{Kind: "step.reset", StepID: "llm-1", TurnID: "t1"})

	if a.child.SummaryText != "unrelated" {
		t.Errorf("SummaryText = %q, want untouched", a.child.SummaryText)
	}
	if a.steps[0].Content[0].Text != "" {
		t.Errorf("text = %q, want cleared regardless", a.steps[0].Content[0].Text)
	}
}

func TestApplyStepEvent_ToolResultMirrorsSummaryIntoProgress(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Type: "tool_call", Role: "assistant", Progress: `{"phase":"summarizing","searchCount":3,"summaryText":"partial"}`, Content: []domain.ContentBlock{{Type: domain.ContentBlockToolUse, ToolUseID: "tu1"}}},
	}

	a.applyStepEvent(domain.StepEvent{
		Kind:   "block.appended",
		StepID: "s1",
		TurnID: "t1",
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolResult,
			ToolUseID: "tu1",
			Text:      "final explore summary",
		},
	})

	var progress map[string]any
	if err := json.Unmarshal([]byte(a.steps[0].Progress), &progress); err != nil {
		t.Fatalf("Progress is not valid JSON: %v", err)
	}
	if progress["summaryText"] != "final explore summary" {
		t.Errorf("Progress.summaryText = %q, want %q", progress["summaryText"], "final explore summary")
	}
	if progress["searchCount"] != float64(3) {
		t.Errorf("Progress.searchCount = %v, want 3", progress["searchCount"])
	}
}

func TestApplyStepEvent_UserStepOpenedOriginMessageId(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}
	msgId := "msg-user-123"

	a.applyStepEvent(domain.StepEvent{
		Kind:            "step.opened",
		StepID:          msgId,
		TurnID:          msgId,
		StepType:        "text",
		Role:            "user",
		Block:           &domain.ContentBlock{Type: domain.ContentBlockText, Text: "hello"},
		OriginMessageID: msgId,
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	s := a.steps[0]
	if s.ID != msgId {
		t.Errorf("step ID = %q, want %q", s.ID, msgId)
	}
	if s.Role != "user" {
		t.Errorf("step Role = %q, want user", s.Role)
	}
}

func TestApplyStepEvent_AssistantStepOpenedEmptyOriginMessageId(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}

	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "step-asst-1",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: "thinking"},
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	s := a.steps[0]
	if s.Role != "assistant" {
		t.Errorf("step Role = %q, want assistant", s.Role)
	}
	if s.ID != "step-asst-1" {
		t.Errorf("step ID = %q, want step-asst-1", s.ID)
	}
}

func TestApplyStepEvent_AssistantStepDefaultsToAgentMeta(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}, actorID: "actor-1"}

	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "step-asst-1",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: "thinking"},
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	if a.steps[0].Meta != "agent|actor-1|" {
		t.Errorf("assistant step Meta = %q, want agent|actor-1|", a.steps[0].Meta)
	}
}

func TestApplyStepEvent_UserInjectStepDefaultsToUserMeta(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}

	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "step-inject-1",
		TurnID:   "turn-1",
		StepType: "user_inject",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: "follow-up"},
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	if a.steps[0].Meta != "user" {
		t.Errorf("user_inject step Meta = %q, want user", a.steps[0].Meta)
	}
}

func TestApplyStepEvent_SystemStepDefaultsToSystemMeta(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}

	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "step-sys-1",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "system",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: "context"},
	})

	if len(a.steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(a.steps))
	}
	if a.steps[0].Meta != "system" {
		t.Errorf("system step Meta = %q, want system", a.steps[0].Meta)
	}
}

func TestApplyStepEvent_SetsStartedAtAndCompletedAt(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextSeq: 1}}

	// step.opened -> step.closed
	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "s-opened",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "assistant",
	})
	a.applyStepEvent(domain.StepEvent{
		Kind:   "step.closed",
		StepID: "s-opened",
		TurnID: "turn-1",
	})
	if got := a.steps[0]; got.StartedAt == "" {
		t.Errorf("step.opened->step.closed: StartedAt empty")
	} else if got.CompletedAt == "" {
		t.Errorf("step.opened->step.closed: CompletedAt empty")
	}

	// step.opened -> step.error
	a.applyStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "s-error",
		TurnID:   "turn-1",
		StepType: "text",
		Role:     "assistant",
	})
	a.applyStepEvent(domain.StepEvent{
		Kind:   "step.error",
		StepID: "s-error",
		TurnID: "turn-1",
		Error:  "boom",
	})
	if got := a.steps[1]; got.StartedAt == "" {
		t.Errorf("step.opened->step.error: StartedAt empty")
	} else if got.CompletedAt == "" {
		t.Errorf("step.opened->step.error: CompletedAt empty")
	}

	// step.interaction_requested -> step.interaction_resolved
	a.applyStepEvent(domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "s-interaction",
		TurnID:          "turn-1",
		InteractionType: "confirm",
		RequestID:       "req-1",
	})
	a.applyStepEvent(domain.StepEvent{
		Kind:      "step.interaction_resolved",
		StepID:    "s-interaction",
		TurnID:    "turn-1",
		RequestID: "req-1",
	})
	if got := a.steps[2]; got.StartedAt == "" {
		t.Errorf("step.interaction_requested->step.interaction_resolved: StartedAt empty")
	} else if got.CompletedAt == "" {
		t.Errorf("step.interaction_requested->step.interaction_resolved: CompletedAt empty")
	}
}

func TestClearSession_ResetsConversationState(t *testing.T) {
	a := &Actor{
		title: "Inferred Title",
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "t1", Role: "user"}},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			SummarySegments:  []gen.SummarySegment{{SourceEndIndex: 1}},
			CompactionEvents: []gen.CompactionEvent{{TurnID: "t1"}},
			ExploreResults:   []domain.ExploreResult{{TurnID: "t1"}},
			Goal:             &gen.SessionGoal{Condition: "test"},
			Tasks:            []gen.TurnTask{{ID: "task-1"}},
			Steps:            []domain.Step{{ID: "s1"}},
			NextSeq:          5,
		},
		steps:  []domain.Step{{ID: "s1", TurnID: "t1"}},
		status: turnStatus{StartStepCount: 2, Unit: domain.ModelUnit{Model: "gpt-4"}},
	}

	if err := a.clearSession(nil); err != nil {
		t.Fatalf("clearSession failed: %v", err)
	}

	if len(a.Session.Turns) != 0 {
		t.Errorf("Session.Turns len = %d, want 0", len(a.Session.Turns))
	}
	if a.Session.ActiveHead != -1 {
		t.Errorf("Session.ActiveHead = %d, want -1", a.Session.ActiveHead)
	}
	if len(a.steps) != 0 {
		t.Errorf("steps len = %d, want 0", len(a.steps))
	}
	if a.RawSession.SummarySegments != nil {
		t.Errorf("RawSession.SummarySegments not cleared")
	}
	if a.RawSession.CompactionEvents != nil {
		t.Errorf("RawSession.CompactionEvents not cleared")
	}
	if a.RawSession.ExploreResults != nil {
		t.Errorf("RawSession.ExploreResults not cleared")
	}
	if a.RawSession.Goal != nil {
		t.Errorf("RawSession.Goal not cleared")
	}
	if a.RawSession.Tasks != nil {
		t.Errorf("RawSession.Tasks not cleared")
	}
	if a.RawSession.Steps != nil {
		t.Errorf("RawSession.Steps not cleared")
	}
	if a.RawSession.NextSeq != 5 {
		t.Errorf("RawSession.NextSeq = %d, want preserved 5", a.RawSession.NextSeq)
	}
	if a.status.StartStepCount != 0 {
		t.Errorf("status.StartStepCount = %d, want 0", a.status.StartStepCount)
	}
	if a.status.Unit.Model != "gpt-4" {
		t.Errorf("status.Unit.Model = %q, want preserved gpt-4", a.status.Unit.Model)
	}
	if a.title != "" {
		t.Errorf("title = %q, want empty", a.title)
	}
	// Must not panic with stale StartStepCount > len(a.steps).
	_ = a.buildTurnStatusReadOnly("", 0)
}

func TestClearSession_CancelsActiveTurn(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		title:         "Inferred Title",
		ActiveTurnRef: "turn-1",
		Session: domain.Session{
			Turns:      []domain.Turn{{ID: "user-turn-1", Role: "user", State: "completed"}},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{NextSeq: 1},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: false, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 1,
		},
	}

	if err := a.clearSession(ctx); err != nil {
		t.Fatalf("clearSession failed: %v", err)
	}

	if a.ActiveTurnRef != "" {
		t.Errorf("ActiveTurnRef = %q, want empty", a.ActiveTurnRef)
	}
	if len(a.Session.Turns) != 0 {
		t.Errorf("Session.Turns len = %d, want 0", len(a.Session.Turns))
	}
	if a.status.StartStepCount != 0 {
		t.Errorf("status.StartStepCount = %d, want 0", a.status.StartStepCount)
	}
	if a.title != "" {
		t.Errorf("title = %q, want empty", a.title)
	}
	// Must not panic with stale StartStepCount > len(a.steps).
	_ = a.buildTurnStatusReadOnly("", 0)
}

// TestBuildTurnStatusReadOnly_EventSeqReflectsActiveTurnEngine verifies that
// TurnStatus.EventSeq mirrors the active turn engine's monotonic step-event
// counter, and is 0 when no turn engine is active. The frontend reconcile
// compares this watermark against its local state to detect a stale active
// turn (background→foreground / reconnect) and authoritatively replace it.
func TestBuildTurnStatusReadOnly_EventSeqReflectsActiveTurnEngine(t *testing.T) {
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		status: turnStatus{
			TurnID:         "turn-1",
			State:          "running",
			StartStepCount: 0,
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-1", Closed: false, Seq: 1},
		},
	}

	// No active turn engine → EventSeq stays 0.
	if got := a.buildTurnStatusReadOnly("", 0).EventSeq; got != 0 {
		t.Errorf("EventSeq without engine = %d, want 0", got)
	}

	// Active engine with a non-zero step-event counter is reflected verbatim.
	eng := &turnEngine{stepEventSeq: 7}
	a.turnEngineStore(eng)
	if got := a.buildTurnStatusReadOnly("", 0).EventSeq; got != 7 {
		t.Errorf("EventSeq with engine = %d, want 7", got)
	}

	// Counter growth between snapshots is observable.
	eng.stepEventSeq = 42
	if got := a.buildTurnStatusReadOnly("", 0).EventSeq; got != 42 {
		t.Errorf("EventSeq after growth = %d, want 42", got)
	}

	// Engine cleared (turn ended) → EventSeq falls back to 0.
	a.turnEngineStore(nil)
	if got := a.buildTurnStatusReadOnly("", 0).EventSeq; got != 0 {
		t.Errorf("EventSeq after engine clear = %d, want 0", got)
	}
}

// TestExpirePlanApproval_ClearsPendingState verifies that when a turn is
// cancelled while a plan is awaiting approval, expirePlanApproval flips
// status to "rejected" and drops PendingTasks so a fresh plan_submit can
// succeed. Without this guard the agent would lock future plans with
// "plan already submitted (requestId=%s); await approval".
func TestExpirePlanApproval_ClearsPendingState(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:       "pending_approval",
			RequestID:    "req-1",
			Plan:         "do thing",
			PendingTasks: []gen.PlanTaskRef{{ID: "t1", Subject: "step 1"}},
		},
	}

	a.expirePlanApproval(ctx, "req-1")

	if a.plan.Status != "rejected" {
		t.Errorf("plan.Status = %q, want rejected", a.plan.Status)
	}
	if len(a.plan.PendingTasks) != 0 {
		t.Errorf("PendingTasks len = %d, want 0", len(a.plan.PendingTasks))
	}
	if a.plan.RequestID != "req-1" {
		t.Errorf("RequestID = %q, want req-1 (preserved for diagnostics)", a.plan.RequestID)
	}
}

// TestExpirePlanApproval_NoopsOnStateMismatch verifies the guards: a stale
// expire callback (wrong requestID or already-resolved state) must not
// clobber a newer plan's state.
func TestExpirePlanApproval_NoopsOnStateMismatch(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Wrong requestID: should not transition.
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:       "pending_approval",
			RequestID:    "req-current",
			PendingTasks: []gen.PlanTaskRef{{ID: "t1"}},
		},
	}
	a.expirePlanApproval(ctx, "req-stale")
	if a.plan.Status != "pending_approval" {
		t.Errorf("wrong-requestID: plan.Status = %q, want pending_approval (no-op)", a.plan.Status)
	}
	if len(a.plan.PendingTasks) != 1 {
		t.Errorf("wrong-requestID: PendingTasks mutated, want 1")
	}

	// Already resolved: should not transition back.
	a2 := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:    "approved",
			RequestID: "req-2",
		},
	}
	a2.expirePlanApproval(ctx, "req-2")
	if a2.plan.Status != "approved" {
		t.Errorf("already-approved: plan.Status = %q, want approved (no-op)", a2.plan.Status)
	}

	// Empty requestID matches anything: should still respect the status guard.
	a3 := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:       "pending_approval",
			RequestID:    "req-3",
			PendingTasks: []gen.PlanTaskRef{{ID: "t1"}, {ID: "t2"}},
		},
	}
	a3.expirePlanApproval(ctx, "")
	if a3.plan.Status != "rejected" {
		t.Errorf("empty-requestID on pending: plan.Status = %q, want rejected", a3.plan.Status)
	}
}

// TestEmitPlanApprovalEvent_ReflectedInSummarySnapshot verifies that after the
// plan_approval interaction step is emitted, the atomic snapshot used by
// session.summary includes it. Without a fresh takeSnapshot after adding the
// step, a browser refresh during pending_approval loses the approval card
// because the snapshot still reflects pre-emit state.
func TestEmitPlanApprovalEvent_ReflectedInSummarySnapshot(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		snapshotReady: true,
		plan: planState{
			Status:    "pending_approval",
			RequestID: "req-1",
			Plan:      "# Plan",
		},
		status: turnStatus{
			TurnID: "turn-1",
			State:  "running",
		},
	}
	a.takeSnapshot() // matches applyPlanSubmit's defer timing (pre-emit)
	a.emitPlanApprovalEvent(ctx, "turn-1", "req-1")

	resp, err := a.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("handleSessionSummaryPure failed: %v", err)
	}

	var found *domain.Step
	for i := range resp.Steps {
		if resp.Steps[i].Type == "plan_approval" {
			found = &resp.Steps[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("plan_approval step missing from summary snapshot; steps=%+v", resp.Steps)
	}
	if found.RequestID != "req-1" {
		t.Errorf("RequestID = %q, want req-1", found.RequestID)
	}
	if found.InteractionStatus != "pending" {
		t.Errorf("InteractionStatus = %q, want pending", found.InteractionStatus)
	}
	if found.TurnID != "turn-1" {
		t.Errorf("TurnID = %q, want turn-1", found.TurnID)
	}
}

// TestFormatPlanCardRaw verifies that the plan card raw markdown has correct
// frontmatter (tags, parent, status, data) and the plan body.
func TestFormatPlanCardRaw(t *testing.T) {
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:    "pending_approval",
			RequestID: "req-abc123def456",
			Title:     "Migrate Storage Backend",
			Plan:      "## Plan: Migrate Storage Backend\n\nDo important things.",
			Policy:    &gen.PlanPolicy{Mode: "auto"},
			PendingTasks: []gen.PlanTaskRef{
				{ID: "t1", Subject: "Step 1", Status: "pending"},
				{ID: "t2", Subject: "Step 2", Status: "pending"},
			},
		},
	}

	raw := a.formatPlanCardRaw()

	// Frontmatter fields
	if !strings.Contains(raw, "tags: [plan]") {
		t.Errorf("raw missing tags: [plan]\n%s", raw)
	}
	if !strings.Contains(raw, "  type: plan\n  storage: runtime\n  visibility: runtime") {
		t.Errorf("raw missing runtime metadata\n%s", raw)
	}
	if strings.Contains(raw, "__builtin_plan__") {
		t.Errorf("raw must not reference removed __builtin_plan__:\n%s", raw)
	}
	if !strings.Contains(raw, "status: pending_approval") {
		t.Errorf("raw missing status: pending_approval\n%s", raw)
	}
	if !strings.Contains(raw, "requestId: req-abc123def456") {
		t.Errorf("raw missing requestId\n%s", raw)
	}
	if !strings.Contains(raw, "agentId: agent-1") {
		t.Errorf("raw missing agentId\n%s", raw)
	}
	if !strings.Contains(raw, "mode: auto") {
		t.Errorf("raw missing policy mode\n%s", raw)
	}

	// Task JSON should be present
	if !strings.Contains(raw, "tasksJson:") {
		t.Errorf("raw missing tasksJson\n%s", raw)
	}
	if !strings.Contains(raw, `"id":"t1"`) {
		t.Errorf("raw missing task t1 in tasksJson\n%s", raw)
	}

	// Plan body should be after frontmatter
	if !strings.Contains(raw, "## Plan: Migrate Storage Backend") {
		t.Errorf("raw missing plan body\n%s", raw)
	}

	// Title should be extracted from plan content, not the request ID.
	if !strings.Contains(raw, "id: Migrate Storage Backend") {
		t.Errorf("raw missing meaningful id\n%s", raw)
	}
}

// TestPlanCardTitleAndID verifies that the card title comes from the explicit
// submission title and the card ID is a slugified version with a unique suffix.
func TestPlanCardTitleAndID(t *testing.T) {
	a := &Actor{
		actorID: "agent-1",
		plan: planState{
			Title:     "Migrate Storage Backend",
			RequestID: "019f66969d5d0000000000000000039e",
			Plan:      "Goal\n\nThe first line is not the title.",
		},
	}
	if got := a.planCardTitle(); got != "Migrate Storage Backend" {
		t.Errorf("planCardTitle() = %q, want explicit title", got)
	}
	cardID := a.derivePlanCardID("019f66969d5d0000000000000000039e")
	if cardID != "plan-migrate-storage-backend-00039e" {
		t.Errorf("cardID = %q, want explicit-title slug", cardID)
	}
}

func TestApplyPlanSubmit_RequiresTitleAndBody(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{actorID: "agent-1", agentKind: "coder", ComponentMounts: []domain.AgentComponentMount{{CardID: "skill:plan-module", Enabled: true}}}

	_, err := a.applyPlanSubmit(ctx, gen.PlanSubmitReq{Title: "Only Title"})
	if err == nil || !strings.Contains(err.Error(), "Body is required") {
		t.Fatalf("expected Body required error, got %v", err)
	}

	_, err = a.applyPlanSubmit(ctx, gen.PlanSubmitReq{Body: "# Plan\n..."})
	if err == nil || !strings.Contains(err.Error(), "Title is required") {
		t.Fatalf("expected Title required error, got %v", err)
	}
}

func TestApplyPlanSubmit_ValidInputPassesValidation(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// Empty actorID so saveMailbox skips disk persistence.
	a := &Actor{agentKind: "coder", ComponentMounts: []domain.AgentComponentMount{{CardID: "skill:plan-module", Enabled: true}}}

	_, err := a.applyPlanSubmit(ctx, gen.PlanSubmitReq{
		Title: "Test Plan Title",
		Body:  "# Goal\n\nDo things.",
	})
	if err != nil {
		t.Fatalf("expected valid input to succeed, got error: %v", err)
	}
	if a.plan.Title != "Test Plan Title" {
		t.Fatalf("Title not set: %q", a.plan.Title)
	}
	if a.plan.Plan != "# Goal\n\nDo things." {
		t.Fatalf("Plan not set: %q", a.plan.Plan)
	}
	if a.plan.Status != "pending_approval" {
		t.Fatalf("expected pending_approval status, got %q", a.plan.Status)
	}
}

// TestEmitStepImmediate_AskUser_ReflectedInSummarySnapshot guards the fix
// for the "browser refresh during ask_user wait loses the card" bug.
//
// Scenario: phaseAudit buffers step.opened for the tool_call step; before
// phaseCommit runs, executeWaitUser calls emitStepImmediate(step.interaction_requested).
// emitStepImmediate must (1) flush the buffered tool_call step, (2) apply
// both steps to a.steps, and (3) takeSnapshot so session.summary sees them.
//
// Without the fix, the snapshot still reflects pre-emit state and a refresh
// drops the ask_user card.
func TestEmitStepImmediate_AskUser_ReflectedInSummarySnapshot(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:       "agent-1",
		agentKind:     "coder",
		snapshotReady: true,
		status: turnStatus{
			TurnID: "turn-1",
			State:  "running",
		},
	}
	a.takeSnapshot()

	e := &turnEngine{
		logger:         newNopActorLogger(),
		projectID:      "proj-1",
		turnID:         "turn-1",
		stepByID:       map[string]domain.TurnAction{},
		stepEventMu:    sync.Mutex{},
		openStepEvents: map[string][]domain.StepEvent{},
		onStepEvent:    a.applyStepEvent,
		onAfterFlush:   a.takeSnapshot,
	}

	// Simulate phaseAudit emitting step.opened for the ask_user tool_call.
	e.emitStepEvent(domain.StepEvent{
		Kind:     "step.opened",
		StepID:   "turn-1-tool-001",
		TurnID:   "turn-1",
		StepType: "tool_call",
		Role:     "assistant",
		Block: &domain.ContentBlock{
			Type:      domain.ContentBlockToolUse,
			ToolUseID: "tool-use-1",
			ToolName:  "ask_user",
			Input:     `{"questions":[{"header":"h","question":"q?","options":[{"label":"a"}],"multiSelect":false}]}`,
		},
	})

	// Simulate executeWaitUser emitting step.interaction_requested before
	// blocking on resumeCh.
	if err := e.emitStepImmediate(ctx, domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          "turn-1-ask-tool-use-1",
		TurnID:          "turn-1",
		InteractionType: "ask_user",
		RequestID:       "tool-use-1",
		Task: map[string]any{
			"questions": `{"questions":[{"header":"h","question":"q?","options":[{"label":"a"}],"multiSelect":false}]}`,
		},
	}); err != nil {
		t.Fatalf("emitStepImmediate returned error: %v", err)
	}

	// Browser refresh path — pure read of atomic snapshot.
	resp, err := a.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("handleSessionSummaryPure failed: %v", err)
	}

	var toolStep, askStep *domain.Step
	for i := range resp.Steps {
		s := &resp.Steps[i]
		switch s.Type {
		case "tool_call":
			if toolStep == nil {
				toolStep = s
			}
		case "ask_user":
			askStep = s
		}
	}
	if toolStep == nil {
		t.Fatalf("tool_call step missing from summary snapshot; steps=%+v", resp.Steps)
	}
	if toolStep.ID != "turn-1-tool-001" {
		t.Errorf("tool_step ID = %q, want turn-1-tool-001", toolStep.ID)
	}
	if len(toolStep.Content) == 0 || toolStep.Content[0].Type != domain.ContentBlockToolUse {
		t.Errorf("tool_step missing tool_use block; content=%+v", toolStep.Content)
	}

	if askStep == nil {
		t.Fatalf("ask_user step missing from summary snapshot; steps=%+v", resp.Steps)
	}
	if askStep.ID != "turn-1-ask-tool-use-1" {
		t.Errorf("ask_step ID = %q, want turn-1-ask-tool-use-1", askStep.ID)
	}
	if askStep.RequestID != "tool-use-1" {
		t.Errorf("ask_step RequestID = %q, want tool-use-1", askStep.RequestID)
	}
	if askStep.InteractionStatus != "pending" {
		t.Errorf("ask_step InteractionStatus = %q, want pending", askStep.InteractionStatus)
	}
}

func TestHandleSessionSummaryPure_RequiresReadySnapshot(t *testing.T) {
	a := &Actor{}

	// Seed an unready snapshot with real turns. Before the fix this would return
	// an empty summary, leaving the frontend thinking the agent had no history.
	a.snapshot.Store(&sessionSnapshotData{
		ready: false,
		turns: []domain.Turn{{ID: "t1", Role: "assistant"}},
		steps: []domain.Step{{ID: "s1", TurnID: "t1"}},
	})
	_, err := a.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err == nil {
		t.Fatalf("expected error for unready snapshot, got nil")
	}

	// Mark the actor ready and refresh the snapshot. Summary should now succeed.
	a.snapshot.Store(&sessionSnapshotData{
		ready: true,
		turns: []domain.Turn{{ID: "t1", Role: "assistant"}},
		steps: []domain.Step{{ID: "s1", TurnID: "t1"}},
	})
	resp, err := a.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("handleSessionSummaryPure failed after ready: %v", err)
	}
	if len(resp.Turns) != 1 || resp.Turns[0].ID != "t1" {
		t.Fatalf("Turns = %+v, want [t1]", resp.Turns)
	}
}

// TestHandleSessionSummaryPure_WaitsForOnStartDone verifies the cold-start
// bridge: when the snapshot is unready but onStartDone is still open, the pure
// handler blocks until OnStart finishes (markStarted closes the channel), then
// returns the freshly-seeded snapshot. This is the deadlock-safe replacement
// for blocking in the spawn path: it runs off the owner loop, so OnStart's
// synchronous callbacks (fetchAgentKindConfig → workspace) cannot deadlock.
func TestHandleSessionSummaryPure_WaitsForOnStartDone(t *testing.T) {
	a := &Actor{onStartDone: make(chan struct{})}
	// Snapshot unready — handler must wait.
	a.snapshot.Store(&sessionSnapshotData{
		ready: false,
		turns: []domain.Turn{{ID: "t1", Role: "assistant"}},
		steps: []domain.Step{{ID: "s1", TurnID: "t1"}},
	})

	// requestDone never closes, so the handler can only unblock via onStartDone.
	reqCtx := donePureContext{done: make(chan struct{})}

	type res struct {
		resp domain.AgentSessionSummaryResp
		err  error
	}
	done := make(chan res, 1)
	go func() {
		resp, err := a.handleSessionSummaryPure(reqCtx, domain.AgentSessionSummaryReq{})
		done <- res{resp, err}
	}()

	// Must not return while OnStart is still running.
	select {
	case <-done:
		t.Fatal("summary returned before onStartDone closed")
	case <-time.After(20 * time.Millisecond):
	}

	// OnStart finishes: seed the ready snapshot and close the gate.
	a.snapshot.Store(&sessionSnapshotData{
		ready: true,
		turns: []domain.Turn{{ID: "t1", Role: "assistant"}},
		steps: []domain.Step{{ID: "s1", TurnID: "t1"}},
	})
	close(a.onStartDone)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("summary error after ready: %v", r.err)
		}
		if len(r.resp.Turns) != 1 || r.resp.Turns[0].ID != "t1" {
			t.Fatalf("Turns = %+v, want [t1]", r.resp.Turns)
		}
	case <-time.After(time.Second):
		t.Fatal("summary did not return after onStartDone closed")
	}
}

// TestHandleSessionSummaryPure_ContextCancelReturnsError verifies the wait is
// bounded by the request context: if OnStart never finishes within the
// request's lifetime, the handler returns the not-ready error so the frontend
// reconciler can retry, rather than hanging forever.
func TestHandleSessionSummaryPure_ContextCancelReturnsError(t *testing.T) {
	a := &Actor{onStartDone: make(chan struct{})}
	a.snapshot.Store(&sessionSnapshotData{ready: false})

	reqDone := make(chan struct{})
	reqCtx := donePureContext{done: reqDone}

	type res struct{ err error }
	done := make(chan res, 1)
	go func() {
		_, err := a.handleSessionSummaryPure(reqCtx, domain.AgentSessionSummaryReq{})
		done <- res{err}
	}()

	close(reqDone) // request deadline exceeded
	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("expected error when request context cancelled during cold start")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not return after request context cancelled")
	}
}

func TestHandleSessionSummaryPure_IncludesGoal(t *testing.T) {
	a := &Actor{}
	goal := &gen.SessionGoal{Condition: "implement goal display", MaxTurns: 10, TurnCount: 3}
	a.snapshot.Store(&sessionSnapshotData{
		ready: true,
		turns: []domain.Turn{{ID: "t1", Role: "assistant"}},
		steps: []domain.Step{{ID: "s1", TurnID: "t1"}},
		goal:  goal,
	})
	resp, err := a.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("handleSessionSummaryPure failed: %v", err)
	}
	if resp.Goal == nil {
		t.Fatalf("resp.Goal = nil, want non-nil")
	}
	if resp.Goal.Condition != goal.Condition {
		t.Errorf("resp.Goal.Condition = %q, want %q", resp.Goal.Condition, goal.Condition)
	}
}

func TestHandleSessionExportRange_ExportsRecentTurns(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
		{ID: "t3", Role: "user"},
		{ID: "t4", Role: "assistant"},
		{ID: "t5", Role: "user"},
		{ID: "t6", Role: "assistant"},
	}, []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
		{ID: "s3", TurnID: "t3"},
		{ID: "s4", TurnID: "t4"},
		{ID: "s5", TurnID: "t5"},
		{ID: "s6", TurnID: "t6"},
	})

	resp, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Turns) != 3 {
		t.Fatalf("Turns = %d, want 3", len(resp.Turns))
	}
	if resp.Turns[0].ID != "t4" || resp.Turns[2].ID != "t6" {
		t.Errorf("unexpected turn IDs: %v", ids(resp.Turns))
	}
	if len(resp.Steps) != 3 {
		t.Errorf("Steps = %d, want 3", len(resp.Steps))
	}
	if !resp.HasMore {
		t.Errorf("HasMore = false, want true")
	}
	if resp.TotalTurns != 6 {
		t.Errorf("TotalTurns = %d, want 6", resp.TotalTurns)
	}
	if resp.NextBeforeTurnID != "t4" {
		t.Errorf("NextBeforeTurnID = %q, want t4", resp.NextBeforeTurnID)
	}
}

func TestHandleSessionExportRange_BeforeTurnID(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
		{ID: "t3", Role: "user"},
		{ID: "t4", Role: "assistant"},
	}, nil)

	resp, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{BeforeTurnID: "t3", Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Turns) != 2 {
		t.Fatalf("Turns = %d, want 2", len(resp.Turns))
	}
	if resp.Turns[0].ID != "t1" || resp.Turns[1].ID != "t2" {
		t.Errorf("unexpected turn IDs: %v", ids(resp.Turns))
	}
	if resp.HasMore {
		t.Errorf("HasMore = true, want false")
	}
}

func TestHandleSessionExportRange_IncludesActiveTurnOnFirstPage(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t1", Role: "user", Seq: 1},
		{ID: "t2", Role: "assistant", Seq: 2},
		{ID: "t3", Role: "user", Seq: 3},
		{ID: "t4", Role: "assistant", Seq: 4},
	}, []domain.Step{
		{ID: "s1", TurnID: "t1", Seq: 1},
		{ID: "s2", TurnID: "t2", Seq: 2},
		{ID: "s3", TurnID: "t3", Seq: 3},
		{ID: "s4", TurnID: "t4", Seq: 4},
	})

	// Simulate an in-progress assistant turn that has not yet been committed to
	// Session.Turns. This is the "last assistant message" that cloning must preserve.
	a.status.TurnID = "t5"
	a.status.StartStepCount = len(a.steps)
	a.steps = append(a.steps, domain.Step{
		ID:        "s5",
		TurnID:    "t5",
		Role:      "assistant",
		Type:      "text",
		Seq:       5,
		Timestamp: "2026-07-13T10:00:00Z",
		Closed:    false,
	})
	a.takeSnapshot()

	resp, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Turns) != 3 {
		t.Fatalf("Turns = %d, want 3", len(resp.Turns))
	}
	if resp.Turns[0].ID != "t3" || resp.Turns[1].ID != "t4" || resp.Turns[2].ID != "t5" {
		t.Errorf("unexpected turn IDs: %v", ids(resp.Turns))
	}
	if resp.Turns[2].State != "completed" {
		t.Errorf("active turn State = %q, want completed", resp.Turns[2].State)
	}
	if resp.Turns[2].CompletedAt == "" {
		t.Errorf("active turn CompletedAt empty")
	}
	if len(resp.Steps) != 3 {
		t.Fatalf("Steps = %d, want 3", len(resp.Steps))
	}
	foundActiveStep := false
	for _, step := range resp.Steps {
		if step.ID == "s5" && step.TurnID == "t5" {
			foundActiveStep = true
			break
		}
	}
	if !foundActiveStep {
		t.Errorf("active step s5 not found in exported steps: %v", idsFromSteps(resp.Steps))
	}
	if !resp.HasMore {
		t.Errorf("HasMore = false, want true")
	}
	if resp.NextBeforeTurnID != "t3" {
		t.Errorf("NextBeforeTurnID = %q, want t3", resp.NextBeforeTurnID)
	}

	// The active turn should only appear on the first page; subsequent pages must
	// not include it again.
	resp2, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{BeforeTurnID: resp.NextBeforeTurnID, Limit: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, turn := range resp2.Turns {
		if turn.ID == "t5" {
			t.Errorf("active turn t5 returned on second page")
		}
	}
}

func TestHandleSessionImportTurns_PrependsTurns(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t3", Role: "assistant"},
		{ID: "t4", Role: "user"},
	}, []domain.Step{
		{ID: "s3", TurnID: "t3"},
		{ID: "s4", TurnID: "t4"},
	})

	resp, err := a.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns: []domain.Turn{
			{ID: "t1", Role: "user"},
			{ID: "t2", Role: "assistant"},
		},
		Steps: []domain.Step{
			{ID: "s1", TurnID: "t1"},
			{ID: "s2", TurnID: "t2"},
		},
		SummarySegments: []domain.SummarySegment{
			{SourceStartIndex: 0, SourceEndIndex: 1},
		},
		ExploreResults: []domain.ExploreResult{
			{TurnID: "t1"},
		},
		SourceAgentID:    "src-actor",
		SourceTotalTurns: 4,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AcceptedTurns != 2 {
		t.Errorf("AcceptedTurns = %d, want 2", resp.AcceptedTurns)
	}
	if len(a.Session.Turns) != 4 {
		t.Fatalf("Session.Turns = %d, want 4", len(a.Session.Turns))
	}
	if a.Session.Turns[0].ID != "t1" || a.Session.Turns[3].ID != "t4" {
		t.Errorf("unexpected order: %v", ids(a.Session.Turns))
	}
	if len(a.RawSession.Steps) != 4 {
		t.Errorf("Steps = %d, want 4", len(a.RawSession.Steps))
	}
	if len(a.RawSession.SummarySegments) != 1 {
		t.Errorf("SummarySegments = %d, want 1", len(a.RawSession.SummarySegments))
	}
	if len(a.RawSession.ExploreResults) != 1 {
		t.Errorf("ExploreResults = %d, want 1", len(a.RawSession.ExploreResults))
	}
	if a.cloneSourceActorID != "src-actor" {
		t.Errorf("cloneSourceActorID = %q, want src-actor", a.cloneSourceActorID)
	}
	if a.cloneSourceTotalTurns != 4 {
		t.Errorf("cloneSourceTotalTurns = %d, want 4", a.cloneSourceTotalTurns)
	}
	if !a.clonePendingHistory {
		t.Errorf("clonePendingHistory = false, want true")
	}
}

func TestHandleSessionImportTurns_IsFinalClearsPending(t *testing.T) {
	a := &Actor{}
	_, err := a.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns:            []domain.Turn{{ID: "t1"}},
		SourceAgentID:    "src-actor",
		SourceTotalTurns: 1,
		IsFinal:          true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.clonePendingHistory {
		t.Errorf("clonePendingHistory = true, want false")
	}
}

func TestHandleSessionImportTurns_EmitsHistoryImportedEvent(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}
	_, err := a.handleSessionImportTurns(ctx, domain.AgentSessionImportTurnsReq{
		Turns: []domain.Turn{
			{ID: "t1", Role: "assistant"},
			{ID: "t2", Role: "user"},
		},
		SourceAgentID:    "src-actor",
		SourceTotalTurns: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !emittedHistoryImported(ctx.EmittedEvents) {
		t.Errorf("expected a turn.history_imported event; got %+v", ctx.EmittedEvents)
	}
}

func TestHandleSessionImportTurns_NoEventWhenNoNewTurns(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}
	// t1 already present; the import chunk only re-sends it.
	a.Session.Turns = []domain.Turn{{ID: "t1"}}
	_, err := a.handleSessionImportTurns(ctx, domain.AgentSessionImportTurnsReq{
		Turns:         []domain.Turn{{ID: "t1"}},
		SourceAgentID: "src-actor",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if emittedHistoryImported(ctx.EmittedEvents) {
		t.Errorf("did not expect turn.history_imported when no new turns were imported")
	}
}

// emittedHistoryImported reports whether the event slice contains a
// turn.history_imported turn event.
func emittedHistoryImported(events []testutil.EmittedEvent) bool {
	for _, ev := range events {
		if ev.Kind != "turn" {
			continue
		}
		te, ok := ev.Payload.(domain.TurnEvent)
		if ok && te.Kind == domain.TurnHistoryImported {
			return true
		}
	}
	return false
}

func TestHandleSessionImportTurns_PreservesSourceSeqCounters(t *testing.T) {
	a := &Actor{}
	_, err := a.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns: []domain.Turn{
			{ID: "t1", Role: "user", Seq: 1},
			{ID: "t2", Role: "assistant", Seq: 2},
		},
		Steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Seq: 1},
			{ID: "s2", TurnID: "t2", Seq: 2},
		},
		SourceAgentID:    "src-actor",
		SourceTotalTurns: 2,
		NextSeq:          10,
		NextIdx:          7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.RawSession.NextSeq != 10 {
		t.Errorf("NextSeq = %d, want 10", a.RawSession.NextSeq)
	}
	if a.RawSession.NextIdx != 7 {
		t.Errorf("NextIdx = %d, want 7", a.RawSession.NextIdx)
	}
	// A new step created after the import must be ordered after the imported history.
	if got := a.allocSeq(); got != 10 {
		t.Errorf("allocSeq() = %d, want 10", got)
	}
}

func TestHandleSessionImportTurns_BumpsSeqFromImportedStepsWhenNoSourceNextSeq(t *testing.T) {
	a := &Actor{}
	_, err := a.handleSessionImportTurns(nil, domain.AgentSessionImportTurnsReq{
		Turns: []domain.Turn{
			{ID: "t1", Role: "user", Seq: 5},
			{ID: "t2", Role: "assistant", Seq: 6},
		},
		Steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Seq: 5},
			{ID: "s2", TurnID: "t2", Seq: 6},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.RawSession.NextSeq <= 6 {
		t.Errorf("NextSeq = %d, want > 6", a.RawSession.NextSeq)
	}
	if got := a.allocSeq(); got <= 6 {
		t.Errorf("allocSeq() = %d, want > 6", got)
	}
}

func TestHandleSessionExportRange_IncludesSeqCounters(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
	}, []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
	})
	a.RawSession.NextSeq = 12
	a.RawSession.NextIdx = 8
	a.takeSnapshot()

	resp, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.NextSeq != 12 {
		t.Errorf("NextSeq = %d, want 12", resp.NextSeq)
	}
	if resp.NextIdx != 8 {
		t.Errorf("NextIdx = %d, want 8", resp.NextIdx)
	}
}

// TestHandleSessionExportRange_CarriesAllSummarySegments is a regression test
// for cloning: export_range paginates backward from the newest turn, so the
// current chunk holds only a few steps while summary segments cover the much
// older compacted history. Every segment must be exported regardless of the
// chunk's step count, otherwise the clone loses its early assistant history.
func TestHandleSessionExportRange_CarriesAllSummarySegments(t *testing.T) {
	a := &Actor{}
	importTurns(t, a, []domain.Turn{
		{ID: "t1", Role: "user"},
		{ID: "t2", Role: "assistant"},
		{ID: "t3", Role: "user"},
		{ID: "t4", Role: "assistant"},
		{ID: "t5", Role: "user"},
		{ID: "t6", Role: "assistant"},
	}, []domain.Step{
		{ID: "s1", TurnID: "t1"},
		{ID: "s2", TurnID: "t2"},
		{ID: "s3", TurnID: "t3"},
		{ID: "s4", TurnID: "t4"},
		{ID: "s5", TurnID: "t5"},
		{ID: "s6", TurnID: "t6"},
	})
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{Text: "seg-0-50", SourceStartIndex: 0, SourceEndIndex: 50},
		{Text: "seg-0-80", SourceStartIndex: 0, SourceEndIndex: 80},
		{Text: "seg-0-2", SourceStartIndex: 0, SourceEndIndex: 2},
	}
	a.takeSnapshot()

	// First page exports only the 3 most recent turns (~3 steps), but the
	// segments covering steps 0-50 and 0-80 must still come along.
	resp, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.SummarySegments) != 3 {
		t.Fatalf("SummarySegments = %d, want 3 (all segments carried): %+v", len(resp.SummarySegments), resp.SummarySegments)
	}
	for _, seg := range resp.SummarySegments {
		if seg.Text == "" {
			t.Errorf("summary segment lost its text: %+v", seg)
		}
	}
}

func importTurns(t *testing.T, a *Actor, turns []domain.Turn, steps []domain.Step) {
	t.Helper()
	if _, err := a.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session: domain.Session{Turns: turns, ActiveHead: int32(len(turns) - 1)},
		Steps:   steps,
	}); err != nil {
		t.Fatalf("import turns: %v", err)
	}
}

func ids(turns []domain.Turn) []string {
	out := make([]string, len(turns))
	for i, t := range turns {
		out[i] = t.ID
	}
	return out
}

func idsFromSteps(steps []domain.Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.ID
	}
	return out
}

// TestHandleSessionFork_KeepsDiscardedStepPlaceholders is a regression test
// for cloning via fork from a specific turn. Compaction removes early turns
// from Session.Turns but keeps their steps as Discarded placeholders so
// summary-segment indices stay aligned to the absolute step count. The fork
// must carry those placeholders along with the retained recent steps;
// otherwise the clone loses summary/step index alignment: the compacted early
// history (which exists only inside the summary) is dropped, and
// compileMessages' coveredEnd check skips the clone's recent steps.
func TestHandleSessionFork_KeepsDiscardedStepPlaceholders(t *testing.T) {
	// t1 was compacted away: removed from Session.Turns, its steps kept as
	// content-less placeholders. t2/t3 are the recent window.
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "user"},
			},
			ActiveHead: 0, // fork at t2
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1},
			},
		},
	}
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Seq: 3},
		{ID: "s4", TurnID: "t2", Seq: 4},
		{ID: "s5", TurnID: "t3", Seq: 5},
		{ID: "s6", TurnID: "t3", Seq: 6},
	}
	a.takeSnapshot()

	resp, err := a.handleSessionFork(nil, domain.AgentSessionForkReq{}) // fork at head=0 (t2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := map[string]bool{}
	for _, id := range idsFromSteps(resp.Steps) {
		seen[id] = true
	}
	for _, w := range []string{"s1", "s2", "s3", "s4"} { // t1 placeholders + t2 retained
		if !seen[w] {
			t.Errorf("fork dropped step %s; clone loses summary/step index alignment. Steps: %v", w, idsFromSteps(resp.Steps))
		}
	}
	for _, drop := range []string{"s5", "s6"} {
		if seen[drop] {
			t.Errorf("fork leaked step %s beyond fork point", drop)
		}
	}
	if len(resp.SummarySegments) != 1 || resp.SummarySegments[0].Text != "t1-summary" {
		t.Errorf("summary segment lost/mismatched: %+v", resp.SummarySegments)
	}
}

// TestHandleSessionExportRange_DiscardedPlaceholdersOnlyOnOldestPage is a
// regression test for cloning via export_range after compaction. Early
// discarded placeholder steps (turns removed by compaction) must reach the
// clone so summary-segment indices stay aligned; they are emitted once on the
// oldest page (import_turns dedups by step ID).
func TestHandleSessionExportRange_DiscardedPlaceholdersOnlyOnOldestPage(t *testing.T) {
	a := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant", Seq: 3},
				{ID: "t3", Role: "user", Seq: 5},
				{ID: "t4", Role: "assistant", Seq: 7},
				{ID: "t5", Role: "user", Seq: 9},
			},
			ActiveHead: 3,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1},
			},
		},
	}
	a.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Seq: 3},
		{ID: "s4", TurnID: "t2", Seq: 4},
		{ID: "s5", TurnID: "t3", Seq: 5},
		{ID: "s6", TurnID: "t3", Seq: 6},
		{ID: "s7", TurnID: "t4", Seq: 7},
		{ID: "s8", TurnID: "t4", Seq: 8},
		{ID: "s9", TurnID: "t5", Seq: 9},
		{ID: "s10", TurnID: "t5", Seq: 10},
	}
	a.takeSnapshot()

	// Page 1 (newest): t4,t5, HasMore=true → must NOT carry early placeholders.
	page1, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{Limit: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	p1 := map[string]bool{}
	for _, id := range idsFromSteps(page1.Steps) {
		p1[id] = true
	}
	if p1["s1"] || p1["s2"] {
		t.Errorf("page1 (HasMore) leaked early discarded placeholders: %v", idsFromSteps(page1.Steps))
	}
	if !page1.HasMore {
		t.Fatalf("page1 should have more pages")
	}

	// Page 2 (oldest): t2,t3, HasMore=false → must carry early placeholders.
	page2, err := a.handleSessionExportRange(nil, domain.AgentSessionExportRangeReq{BeforeTurnID: page1.NextBeforeTurnID, Limit: 2})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	p2 := map[string]bool{}
	for _, id := range idsFromSteps(page2.Steps) {
		p2[id] = true
	}
	for _, w := range []string{"s1", "s2", "s3", "s4", "s5", "s6"} {
		if !p2[w] {
			t.Errorf("oldest page dropped step %s; clone loses summary/step alignment. Steps: %v", w, idsFromSteps(page2.Steps))
		}
	}
	if page2.HasMore {
		t.Errorf("page2 should be the oldest page (HasMore=false)")
	}
}

// TestCloneViaFork_PreservesRecentStepsInCompiledMessages is an end-to-end
// regression test: fork a compacted source, import the fork into a fresh clone,
// then compileMessages must still include the recent (non-compacted) steps.
// Before the fix, fork dropped the early discarded placeholders, the clone's
// step array lost alignment with the summary, and compileMessages' coveredEnd
// check skipped the clone's recent steps (their array indices fell under the
// summary's absolute SourceEndIndex).
func TestCloneViaFork_PreservesRecentStepsInCompiledMessages(t *testing.T) {
	// Source: t1 compacted away (steps kept as Discarded placeholders); t2/t3 recent.
	src := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "user"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1, Level: 1},
			},
		},
	}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Role: "assistant", Type: "text", Seq: 3, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t2-assistant"}}},
		{ID: "s4", TurnID: "t3", Role: "user", Type: "text", Seq: 4, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t3-user"}}},
	}
	src.takeSnapshot()

	forkResp, err := src.handleSessionFork(nil, domain.AgentSessionForkReq{}) // fork at head=1 (t3)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}

	clone := &Actor{}
	if _, err := clone.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session:         forkResp.Session,
		Steps:           forkResp.Steps,
		SummarySegments: forkResp.SummarySegments,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	msgs := clone.compileMessages(true)
	gotIDs := map[string]bool{}
	for _, m := range msgs {
		gotIDs[m.ID] = true
	}
	// Recent steps s3 (t2 assistant) and s4 (t3 user) must survive. Before the
	// fix their array indices (0,1) fell under coveredEnd=1 and were skipped.
	for _, want := range []string{"s3", "s4"} {
		if !gotIDs[want] {
			t.Errorf("clone lost recent step %s in compiled messages; got=%v. clone steps: %v", want, gotIDs, idsFromSteps(clone.steps))
		}
	}
}

// TestCloneViaFork_SessionSummaryReturnsAssistantSteps checks the data source
// the frontend actually consumes: the cloned agent's session.summary response
// must include the recent assistant steps (with content) so the frontend can
// render them into assistant turn envelopes. The frontend ignores the fork
// response and refetches via session.summary, so this is the real gate.
func TestCloneViaFork_SessionSummaryReturnsAssistantSteps(t *testing.T) {
	// Source after compaction: t1 compacted away (steps kept as Discarded
	// placeholders); t2 (assistant) + t3 (user) are the recent window.
	src := &Actor{
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t2", Role: "assistant", Seq: 3},
				{ID: "t3", Role: "user", Seq: 4, UserInput: "t3-user-input"},
			},
			ActiveHead: 1,
		},
		RawSession: domain.RawSession{
			SummarySegments: []domain.SummarySegment{
				{Text: "t1-summary", SourceStartIndex: 0, SourceEndIndex: 1, Level: 1},
			},
		},
	}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Discarded: true, Seq: 1},
		{ID: "s2", TurnID: "t1", Discarded: true, Seq: 2},
		{ID: "s3", TurnID: "t2", Role: "assistant", Type: "text", Seq: 3, Closed: true, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t2-assistant"}}},
		{ID: "s4", TurnID: "t3", Role: "user", Type: "text", Seq: 4, Closed: true, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "t3-user"}}},
	}
	src.takeSnapshot()

	forkResp, err := src.handleSessionFork(nil, domain.AgentSessionForkReq{}) // fork at head=1 (t3)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}

	clone := &Actor{snapshotReady: true}
	if _, err := clone.handleSessionImport(nil, domain.AgentSessionImportReq{
		Session:         forkResp.Session,
		Steps:           forkResp.Steps,
		SummarySegments: forkResp.SummarySegments,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// The clone must be snapshot-ready for the pure summary handler.
	if snap := clone.snapshot.Load(); snap == nil || !snap.ready {
		t.Fatalf("clone snapshot not ready: %+v", snap)
	}

	resp, err := clone.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("session.summary: %v", err)
	}

	// The assistant step s3 (t2) MUST appear with its content so the frontend
	// can render the assistant turn. If it is missing, the clone shows an empty
	// assistant turn ("empty turntail").
	foundAssistant := false
	for _, s := range resp.Steps {
		if s.ID == "s3" {
			foundAssistant = true
			if s.Discarded {
				t.Errorf("assistant step s3 returned as Discarded; frontend would skip it: %+v", s)
			}
			if len(s.Content) == 0 || s.Content[0].Text != "t2-assistant" {
				t.Errorf("assistant step s3 lost its content: %+v", s)
			}
		}
	}
	if !foundAssistant {
		t.Fatalf("session.summary omitted assistant step s3; clone will render empty assistant turn. Steps returned: %v", idsFromSteps(resp.Steps))
	}

	// Sanity: turns must include the assistant turn t2.
	turnIDs := map[string]bool{}
	for _, tu := range resp.Turns {
		turnIDs[tu.ID] = true
	}
	if !turnIDs["t2"] {
		t.Errorf("session.summary omitted assistant turn t2; turns: %+v", resp.Turns)
	}
}

func TestComputeReconnectTurnWindow_NoGap(t *testing.T) {
	turns := []domain.Turn{
		{ID: "t1", Seq: 1},
		{ID: "t2", Seq: 2},
		{ID: "t3", Seq: 3},
	}
	steps := []domain.Step{
		{ID: "s1", TurnID: "t1", Seq: 1},
		{ID: "s2", TurnID: "t2", Seq: 2},
		{ID: "s3", TurnID: "t3", Seq: 3},
	}
	got := computeReconnectTurnWindow(turns, steps, 4, map[string]int32{"s3": 3}, 2, 10)
	if got != 2 {
		t.Errorf("no gap: got %d, want 2", got)
	}
}

func TestComputeReconnectTurnWindow_ExpandsToCoverMissing(t *testing.T) {
	// t6/t7 are newest; the client only knows steps up to seq 3, so steps
	// with seq 4-6 are missing and live in t4/t5. The window must expand
	// backward to include those turns while keeping at least baseMaxTurns.
	turns := []domain.Turn{
		{ID: "t1", Seq: 1},
		{ID: "t2", Seq: 2},
		{ID: "t3", Seq: 3},
		{ID: "t4", Seq: 4},
		{ID: "t5", Seq: 5},
		{ID: "t6", Seq: 6},
		{ID: "t7", Seq: 7},
	}
	steps := []domain.Step{
		{ID: "s1", TurnID: "t1", Seq: 1},
		{ID: "s2", TurnID: "t2", Seq: 2},
		{ID: "s3", TurnID: "t3", Seq: 3},
		{ID: "s4", TurnID: "t4", Seq: 4},
		{ID: "s5", TurnID: "t5", Seq: 5},
		{ID: "s6", TurnID: "t6", Seq: 6},
	}
	got := computeReconnectTurnWindow(turns, steps, 7, map[string]int32{"s3": 3}, 2, 10)
	if got != 4 {
		t.Errorf("expanded window: got %d, want 4 (t4-t7 covers all missing turns)", got)
	}
}

func TestComputeReconnectTurnWindow_HardCap(t *testing.T) {
	turns := make([]domain.Turn, 20)
	steps := make([]domain.Step, 20)
	for i := 0; i < 20; i++ {
		turns[i] = domain.Turn{ID: fmt.Sprintf("t%d", i+1), Seq: int64(i + 1)}
		steps[i] = domain.Step{ID: fmt.Sprintf("s%d", i+1), TurnID: fmt.Sprintf("t%d", i+1), Seq: int64(i + 1)}
	}
	// Client knows only the first step; everything else is missing.
	got := computeReconnectTurnWindow(turns, steps, 21, map[string]int32{"s1": 1}, 2, 8)
	if got != 8 {
		t.Errorf("hard cap: got %d, want 8", got)
	}
}

// TestResolvePlanApproval_ConfirmGoal_CreatesActiveGoal verifies that the
// "confirm_goal" decision approves the plan (creates tasks) and converts the
// plan body into an active, confirmed goal.
func TestResolvePlanApproval_ConfirmGoal_CreatesActiveGoal(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:       "pending_approval",
			RequestID:    "req-1",
			Title:        "Add Retry Logic",
			Plan:         "## Goal\n\nAdd retry logic to the HTTP client.",
			PendingTasks: []gen.PlanTaskRef{{ID: "t1", Subject: "step 1", Status: "pending"}},
		},
	}

	answer := `{"decision":"confirm_goal"}`
	decision, tasks, requestID, _ := a.resolvePlanApproval(ctx, answer)

	if decision != "confirm_goal" {
		t.Fatalf("decision = %q, want confirm_goal", decision)
	}
	if requestID != "req-1" {
		t.Fatalf("requestID = %q, want req-1", requestID)
	}
	if a.plan.Status != "approved" {
		t.Errorf("plan.Status = %q, want approved", a.plan.Status)
	}
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Errorf("tasks = %+v, want one task t1", tasks)
	}
	if a.RawSession.Goal == nil {
		t.Fatal("expected goal to be set after confirm_goal")
	}
	g := a.RawSession.Goal
	if !g.Confirmed {
		t.Errorf("goal.Confirmed = false, want true")
	}
	if g.Status != "active" {
		t.Errorf("goal.Status = %q, want active", g.Status)
	}
	if g.InterpretedGoal != a.plan.Plan {
		t.Errorf("goal.InterpretedGoal = %q, want plan body %q", g.InterpretedGoal, a.plan.Plan)
	}
	if g.Condition != a.plan.Plan {
		t.Errorf("goal.Condition = %q, want plan body %q", g.Condition, a.plan.Plan)
	}
	if g.MaxTurns != DefaultGoalMaxTurns {
		t.Errorf("goal.MaxTurns = %d, want %d", g.MaxTurns, DefaultGoalMaxTurns)
	}
}

// TestResolvePlanApproval_Approve_CreatesSessionTasks is the regression guard
// for the plain plan_submit "approve" path: it must still materialize the
// plan's PendingTasks into session-level tasks via createPlanTasks. This is the
// old plan_submit behavior that must remain unchanged now that
// workflow_plan_submit exists as a task-less alternative.
func TestResolvePlanApproval_Approve_CreatesSessionTasks(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:    "pending_approval",
			RequestID: "req-1",
			Title:     "Add Retry Logic",
			Plan:      "## Goal\n\nAdd retry logic to the HTTP client.",
			PendingTasks: []gen.PlanTaskRef{
				{ID: "t1", Subject: "step 1", Status: "pending"},
				{ID: "t2", Subject: "step 2", Status: "pending"},
			},
		},
	}

	answer := `{"decision":"approve"}`
	decision, tasks, requestID, _ := a.resolvePlanApproval(ctx, answer)

	if decision != "approve" {
		t.Fatalf("decision = %q, want approve", decision)
	}
	if requestID != "req-1" {
		t.Fatalf("requestID = %q, want req-1", requestID)
	}
	if a.plan.Status != "approved" {
		t.Errorf("plan.Status = %q, want approved", a.plan.Status)
	}
	// createPlanTasks must run on plain approve — tasks are materialized.
	if len(tasks) != 2 {
		t.Fatalf("tasks len = %d, want 2 (approve must create session tasks)", len(tasks))
	}
	// Session tasks are persisted alongside the returned slice.
	if len(a.RawSession.Tasks) != 2 {
		t.Fatalf("RawSession.Tasks len = %d, want 2", len(a.RawSession.Tasks))
	}
	// No workflow activated by the plain approve path.
	if a.workflowActive() {
		t.Fatal("plain approve must not activate a workflow")
	}
}

// TestActivateGoalFromPlan_SkipsWhenGoalActive verifies that calling
// activateGoalFromPlan when a goal is already set is a no-op — the existing
// goal is preserved.
func TestActivateGoalFromPlan_SkipsWhenGoalActive(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	existingCondition := "existing goal condition"
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Plan:       "new plan body",
			PlanCardID: "plan-card-1",
		},
		RawSession: domain.RawSession{
			Goal: &gen.SessionGoal{
				Condition: existingCondition,
				Confirmed: true,
				Status:    "active",
			},
		},
	}

	a.activateGoalFromPlan(ctx)

	if a.RawSession.Goal.Condition != existingCondition {
		t.Errorf("goal.Condition = %q, want %q (should not be overwritten)", a.RawSession.Goal.Condition, existingCondition)
	}
}

// TestHandleTurnHistory_ReturnsStepsWithDurationAndUsage verifies that
// handleTurnHistory returns Steps populated with StartedAt/CompletedAt/Usage
// for the current turn, including after a restart from persisted step files.
// Steps with missing CompletedAt retain StartedAt but have empty CompletedAt
// (frontend renders the duration as "unknown").
func TestHandleTurnHistory_ReturnsStepsWithDurationAndUsage(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	actorID := "test-turn-history-actor"
	startedAt := "2026-08-17T10:00:00.123456789Z"
	completedAt := "2026-08-17T10:00:12.987654321Z"

	// Step 1: text step with StartedAt + CompletedAt (closed, complete)
	// Step 2: tool step with Usage + StartedAt + CompletedAt (like a tool call)
	// Step 3: step with StartedAt but no CompletedAt (still running / historical)
	steps := []domain.Step{
		{
			ID: "s1", TurnID: "turn-1", Role: "assistant", Type: "text",
			Closed: true, Seq: 1,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "I'll read the file."},
			},
		},
		{
			ID: "s2", TurnID: "turn-1", Role: "assistant", Type: "tool_use",
			Closed: true, Seq: 2,
			StartedAt:   startedAt,
			CompletedAt: completedAt,
			Usage: &domain.UsageData{
				InputTokens:  150,
				OutputTokens: 300,
				TotalTokens:  450,
			},
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockToolUse, ToolUseID: "tu-1", ToolName: "project.read", Input: `{"path":"foo.go"}`},
				{Type: domain.ContentBlockToolResult, ToolUseID: "tu-1", Text: "file contents"},
			},
		},
		{
			ID: "s3", TurnID: "turn-1", Role: "assistant", Type: "text",
			Closed: false, Seq: 3,
			StartedAt: "2026-08-17T10:00:15.000000000Z",
			// CompletedAt intentionally empty — simulates in-flight step
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "Still working..."},
			},
		},
	}

	// Phase 1: persist steps to disk via a first actor instance.
	writer := &Actor{actorID: actorID, steps: steps}
	for _, s := range steps {
		if s.Closed {
			if err := writer.persistStepToFile(s); err != nil {
				t.Fatalf("persistStepToFile: %v", err)
			}
		}
	}

	// Phase 2: simulate restart — fresh actor loads steps from disk.
	reader := &Actor{
		actorID: actorID,
		status: turnStatus{
			TurnID: "turn-1",
			State:  "running",
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-1", Role: "assistant", State: "completed"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			Steps: []domain.Step{
				{
					ID: "s3", TurnID: "turn-1", Role: "assistant", Type: "text",
					Closed: false, Seq: 3,
					StartedAt: "2026-08-17T10:00:15.000000000Z",
					// CompletedAt intentionally empty — simulates in-flight step
					Content: []domain.ContentBlock{
						{Type: domain.ContentBlockText, Text: "Still working..."},
					},
				},
			},
		},
	}
	reader.rebuildSteps()
	reader.takeSnapshot()

	resp, err := reader.handleTurnHistory(nil)
	if err != nil {
		t.Fatalf("handleTurnHistory failed: %v", err)
	}

	// Assertions
	if len(resp.Steps) == 0 {
		t.Fatalf("handleTurnHistory returned 0 steps, expected at least 1")
	}

	// Helper to find a step by ID
	findStep := func(id string) *gen.TurnAction {
		for i := range resp.Steps {
			if resp.Steps[i].ID == id {
				return &resp.Steps[i]
			}
		}
		return nil
	}

	// s1: text step — verify StartedAt/CompletedAt
	s1 := findStep("s1")
	if s1 == nil {
		t.Fatalf("step s1 not found in response")
	}
	if s1.StartedAt != startedAt {
		t.Errorf("s1.StartedAt = %q, want %q", s1.StartedAt, startedAt)
	}
	if s1.CompletedAt != completedAt {
		t.Errorf("s1.CompletedAt = %q, want %q", s1.CompletedAt, completedAt)
	}
	if s1.Kind != "assistant_text" {
		t.Errorf("s1.Kind = %q, want assistant_text", s1.Kind)
	}

	// s2: tool step — verify StartedAt, CompletedAt, Usage, and tool metadata
	s2 := findStep("s2")
	if s2 == nil {
		t.Fatalf("step s2 not found in response")
	}
	if s2.StartedAt != startedAt {
		t.Errorf("s2.StartedAt = %q, want %q", s2.StartedAt, startedAt)
	}
	if s2.CompletedAt != completedAt {
		t.Errorf("s2.CompletedAt = %q, want %q", s2.CompletedAt, completedAt)
	}
	if s2.Usage == nil {
		t.Fatalf("s2.Usage is nil; want non-nil UsageData")
	}
	if s2.Usage.InputTokens != 150 {
		t.Errorf("s2.Usage.InputTokens = %d, want 150", s2.Usage.InputTokens)
	}
	if s2.Usage.OutputTokens != 300 {
		t.Errorf("s2.Usage.OutputTokens = %d, want 300", s2.Usage.OutputTokens)
	}
	if s2.Kind != "tool" {
		t.Errorf("s2.Kind = %q, want tool", s2.Kind)
	}
	if s2.CallableID != "project.read" {
		t.Errorf("s2.CallableID = %q, want project.read", s2.CallableID)
	}
	if s2.ToolUseID != "tu-1" {
		t.Errorf("s2.ToolUseID = %q, want tu-1", s2.ToolUseID)
	}

	// s3: step with missing CompletedAt — verify StartedAt is present,
	// CompletedAt is empty (frontend will render "unknown").
	s3 := findStep("s3")
	if s3 == nil {
		t.Fatalf("step s3 not found in response")
	}
	if s3.StartedAt == "" {
		t.Errorf("s3.StartedAt is empty, want non-empty (started time)")
	}
	if s3.CompletedAt != "" {
		t.Errorf("s3.CompletedAt = %q, want empty (step not closed, no fabricated time)", s3.CompletedAt)
	}
	if s3.State != "running" {
		t.Errorf("s3.State = %q, want running (step not closed)", s3.State)
	}
}
