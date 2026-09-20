package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func setupDiscardTest(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a := &Actor{
		actorID: "agent-discard-test",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "user"},
				{ID: "t4", Role: "assistant"},
			},
			ActiveHead: 3,
		},
		steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Closed: true, Timestamp: "2025-01-01T00:00:00Z", Content: []domain.ContentBlock{{Type: "text", Text: "hello"}}},
			{ID: "s2", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Timestamp: "2025-01-01T00:00:01Z", Content: []domain.ContentBlock{{Type: "text", Text: "world"}}, ReasoningContent: "thinking about the answer"},
			{ID: "s3", TurnID: "t3", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "foo"}}},
			{ID: "s4", TurnID: "t4", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "bar"}}},
		},
	}
	return a, testutil.HumanCtx(testutil.GenActorID())
}

func TestFindEarliestDiscardableTurnGroup(t *testing.T) {
	a, _ := setupDiscardTest(t)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Text: "summary1"},
	}

	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group")
	}
	if len(group.turnIndices) != 2 || group.turnIndices[0] != 0 || group.turnIndices[1] != 1 {
		t.Errorf("turnIndices = %v, want [0 1]", group.turnIndices)
	}
	if len(group.stepIndices) != 2 || group.stepIndices[0] != 0 || group.stepIndices[1] != 1 {
		t.Errorf("stepIndices = %v, want [0 1]", group.stepIndices)
	}
}

func TestFindEarliestDiscardableTurnGroup_NotFullySummarized(t *testing.T) {
	a, _ := setupDiscardTest(t)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 0, Text: "summary0"},
	}

	group := a.findEarliestDiscardableTurnGroup()
	if group != nil {
		t.Fatalf("expected no discardable group, got turnIndices=%v", group.turnIndices)
	}
}

func TestFindEarliestDiscardableTurnGroup_ConsecutiveAssistants(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a := &Actor{
		actorID: "agent-consecutive-test",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "assistant"},
				{ID: "t4", Role: "user"},
				{ID: "t5", Role: "assistant"},
			},
			ActiveHead: 4,
		},
		steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q"}}},
			{ID: "s2", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a1"}}},
			{ID: "s3", TurnID: "t3", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a2"}}},
			{ID: "s4", TurnID: "t4", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q2"}}},
			{ID: "s5", TurnID: "t5", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a3"}}},
		},
	}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 2, Text: "summary"},
	}

	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group")
	}
	// Group must include t1(user) + t2(asst) + t3(asst), stopping before t4(user).
	if len(group.turnIndices) != 3 {
		t.Fatalf("turnIndices = %v, want 3 entries [0 1 2]", group.turnIndices)
	}
	if len(group.stepIndices) != 3 {
		t.Fatalf("stepIndices = %v, want 3 entries [0 1 2]", group.stepIndices)
	}
}

func TestDiscardTurnGroupPreservesRemainingTurnOrder(t *testing.T) {
	a, ctx := setupDiscardTest(t)
	a.Session.Turns[0].TurnOrder = 10
	a.Session.Turns[1].TurnOrder = 10
	a.Session.Turns[2].TurnOrder = 20
	a.Session.Turns[3].TurnOrder = 20
	a.RawSession.NextTurnOrder = 21
	group := &discardableTurnGroup{turnIndices: []int{0, 1}, stepIndices: []int{0, 1}}
	if err := a.discardTurnGroup(ctx, group); err != nil {
		t.Fatalf("discardTurnGroup failed: %v", err)
	}
	if len(a.Session.Turns) != 2 || a.Session.Turns[0].TurnOrder != 20 || a.Session.Turns[1].TurnOrder != 20 {
		t.Fatalf("discard rewrote remaining turn order: %+v", a.Session.Turns)
	}
	if a.RawSession.NextTurnOrder != 21 {
		t.Fatalf("NextTurnOrder=%d, want 21", a.RawSession.NextTurnOrder)
	}
}

func TestDiscardTurnGroup(t *testing.T) {
	a, ctx := setupDiscardTest(t)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Text: "summary1"},
	}
	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group")
	}

	if err := a.discardTurnGroup(ctx, group); err != nil {
		t.Fatalf("discardTurnGroup failed: %v", err)
	}

	if !a.steps[0].Discarded || !a.steps[1].Discarded {
		t.Errorf("steps 0-1 not discarded")
	}
	if a.steps[0].Content != nil || a.steps[1].Content != nil {
		t.Errorf("Content not cleared")
	}
	if a.steps[1].ReasoningContent != "" {
		t.Errorf("step 1 ReasoningContent not cleared")
	}
	if a.steps[0].Timestamp != "2025-01-01T00:00:00Z" || a.steps[1].Timestamp != "2025-01-01T00:00:01Z" {
		t.Errorf("discard changed step timestamps: %q, %q", a.steps[0].Timestamp, a.steps[1].Timestamp)
	}
	if a.steps[2].Discarded || a.steps[3].Discarded {
		t.Errorf("steps 2-3 should not be discarded")
	}

	if len(a.Session.Turns) != 2 {
		t.Fatalf("len(Session.Turns) = %d, want 2", len(a.Session.Turns))
	}
	if a.Session.Turns[0].ID != "t3" || a.Session.Turns[1].ID != "t4" {
		t.Errorf("remaining turns = %v, want [t3 t4]", []string{a.Session.Turns[0].ID, a.Session.Turns[1].ID})
	}
	if a.Session.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1", a.Session.ActiveHead)
	}
}

func TestDiscardTurnGroup_ConsecutiveAssistants(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a := &Actor{
		actorID: "agent-consecutive-discard",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
				{ID: "t3", Role: "assistant"},
				{ID: "t4", Role: "user"},
				{ID: "t5", Role: "assistant"},
			},
			ActiveHead: 4,
		},
		steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q"}}},
			{ID: "s2", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a1"}}},
			{ID: "s3", TurnID: "t3", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a2"}}},
			{ID: "s4", TurnID: "t4", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q2"}}},
			{ID: "s5", TurnID: "t5", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a3"}}},
		},
	}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 2, Text: "summary"},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group")
	}
	if err := a.discardTurnGroup(ctx, group); err != nil {
		t.Fatalf("discardTurnGroup failed: %v", err)
	}

	if !a.steps[0].Discarded || !a.steps[1].Discarded || !a.steps[2].Discarded {
		t.Errorf("steps 0-2 should be discarded")
	}
	if a.steps[3].Discarded || a.steps[4].Discarded {
		t.Errorf("steps 3-4 should not be discarded")
	}

	if len(a.Session.Turns) != 2 {
		t.Fatalf("len(Session.Turns) = %d, want 2", len(a.Session.Turns))
	}
	if a.Session.Turns[0].ID != "t4" || a.Session.Turns[1].ID != "t5" {
		t.Errorf("remaining turns = [%s %s], want [t4 t5]", a.Session.Turns[0].ID, a.Session.Turns[1].ID)
	}
	if a.Session.Turns[0].Role != "user" {
		t.Errorf("first remaining turn role = %s, want user", a.Session.Turns[0].Role)
	}
	if a.Session.ActiveHead != 1 {
		t.Errorf("ActiveHead = %d, want 1", a.Session.ActiveHead)
	}
}

func TestFindEarliestDiscardableTurnGroup_WithCompactionStep(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a := &Actor{
		actorID: "agent-compaction-discard-test",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
			},
			ActiveHead: 1,
		},
		steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q"}}},
			{ID: "s2", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a1"}}},
			// Compaction step created during t2 — its index (2) is > coveredEnd (1).
			{ID: "compaction-t2-0", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "compaction", Text: "{\"frame\":{}}"}}},
		},
	}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Text: "summary"},
	}
	// Simulate the CompactionEvent that produced the compaction step.
	a.RawSession.CompactionEvents = []domain.CompactionEvent{
		{TurnID: "t2", Trigger: "auto"},
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Without the fix, the compaction step at index 2 > coveredEnd=1 would
	// block t2 from being discarded.
	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group despite compaction step")
	}

	if err := a.discardTurnGroup(ctx, group); err != nil {
		t.Fatalf("discardTurnGroup failed: %v", err)
	}

	// The compaction step should also be discarded.
	if !a.steps[2].Discarded {
		t.Errorf("compaction step not discarded")
	}
	if a.steps[2].Content != nil {
		t.Errorf("compaction step content not cleared")
	}

	// CompactionEvents for discarded turns should be removed.
	if len(a.RawSession.CompactionEvents) != 0 {
		t.Errorf("CompactionEvents not cleaned, len=%d", len(a.RawSession.CompactionEvents))
	}

	if len(a.Session.Turns) != 0 {
		t.Errorf("expected 0 remaining turns, got %d", len(a.Session.Turns))
	}
}

func TestDiscardTurnGroup_MultipleCompactionSteps(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	a := &Actor{
		actorID: "agent-multi-compaction-test",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user"},
				{ID: "t2", Role: "assistant"},
			},
			ActiveHead: 1,
		},
		steps: []domain.Step{
			{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "q"}}},
			{ID: "s2", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "text", Text: "a1"}}},
			// Two compaction steps from two separate compactions within t2.
			{ID: "t2-compaction-001", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "compaction", Text: "{\"frame\":1}"}}},
			{ID: "t2-compaction-002", TurnID: "t2", Role: "assistant", Type: "text", Closed: true, Content: []domain.ContentBlock{{Type: "compaction", Text: "{\"frame\":2}"}}},
		},
	}
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 1, Text: "summary"},
	}
	a.RawSession.CompactionEvents = []domain.CompactionEvent{
		{TurnID: "t2", Trigger: "auto"},
		{TurnID: "t2", Trigger: "auto"},
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())

	group := a.findEarliestDiscardableTurnGroup()
	if group == nil {
		t.Fatal("expected a discardable group")
	}
	if err := a.discardTurnGroup(ctx, group); err != nil {
		t.Fatalf("discardTurnGroup failed: %v", err)
	}

	// All steps should be discarded.
	for i := range a.steps {
		if !a.steps[i].Discarded {
			t.Errorf("step %d (%s) not discarded", i, a.steps[i].ID)
		}
		if a.steps[i].Content != nil {
			t.Errorf("step %d (%s) content not cleared", i, a.steps[i].ID)
		}
	}

	// All CompactionEvents for t2 should be removed.
	if len(a.RawSession.CompactionEvents) != 0 {
		t.Errorf("CompactionEvents not cleaned, len=%d", len(a.RawSession.CompactionEvents))
	}

	if len(a.Session.Turns) != 0 {
		t.Errorf("expected 0 remaining turns, got %d", len(a.Session.Turns))
	}
}

func TestEstimateSessionChars(t *testing.T) {
	a, _ := setupDiscardTest(t)
	got := a.estimateSessionChars()
	want := int64(len("hello") + len("world") + len("thinking about the answer") + len("foo") + len("bar"))
	if got != want {
		t.Errorf("estimateSessionChars() = %d, want %d", got, want)
	}

	a.steps[0].Discarded = true
	a.steps[0].Content = nil
	a.steps[0].ReasoningContent = ""
	got = a.estimateSessionChars()
	want = int64(len("world") + len("thinking about the answer") + len("foo") + len("bar"))
	if got != want {
		t.Errorf("estimateSessionChars() after discard = %d, want %d", got, want)
	}
}

func TestMaybeDiscardForStorage(t *testing.T) {
	a, ctx := setupDiscardTest(t)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 3, Text: "summary-all"},
	}
	kindCfg := domain.AgentKindConfig{
		StoragePolicy: &domain.StoragePolicy{
			Enabled:          true,
			MaxSessionChars:  10,
			DiscardBatchSize: 2,
		},
	}

	a.maybeDiscardForStorage(ctx, kindCfg)

	if !a.steps[0].Discarded || !a.steps[1].Discarded {
		t.Errorf("first group not discarded")
	}
	if a.steps[2].Discarded || a.steps[3].Discarded {
		t.Errorf("second group should not be discarded")
	}
	if a.estimateSessionChars() > 10 {
		t.Errorf("session chars = %d, want <= 10", a.estimateSessionChars())
	}
}

func TestMaybeDiscardForStorage_Disabled(t *testing.T) {
	a, ctx := setupDiscardTest(t)
	a.RawSession.SummarySegments = []domain.SummarySegment{
		{SourceStartIndex: 0, SourceEndIndex: 3, Text: "summary-all"},
	}
	kindCfg := domain.AgentKindConfig{
		StoragePolicy: &domain.StoragePolicy{
			Enabled:          false,
			MaxSessionChars:  1,
			DiscardBatchSize: 2,
		},
	}

	a.maybeDiscardForStorage(ctx, kindCfg)

	for i, s := range a.steps {
		if s.Discarded {
			t.Errorf("step %d discarded when policy disabled", i)
		}
	}
}
