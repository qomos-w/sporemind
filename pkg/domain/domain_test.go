package domain

import (
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"testing"
)

func TestConceptCardContract(t *testing.T) {
	card := ConceptCard{
		Title: "concept-1", Desc: "semantic node", State: "real",
		Relations: []ConceptCardRelation{
			{From: "concept-1", To: "parent", Relation: "part_of"},
			{From: "concept-1", To: "dependency", Relation: "depends_on"},
			{From: "concept-1", To: "capability", Relation: "enables"},
			{From: "constraint", To: "concept-1", Relation: "constrains"},
		},
		Anchors:    []SourceAnchor{{Path: "pkg/domain/domain.go", Symbol: "ConceptCard", StartLine: 1, EndLine: 2, FileHash: "file", RegionHash: "region"}},
		Evidence:   []ConceptEvidence{{ID: "e1", Kind: "test", Statement: "contract", Anchor: SourceAnchor{Path: "pkg/domain/domain_test.go"}, Confidence: 0.9}},
		Confidence: 0.9, Status: "verified", Stale: true,
	}
	if len(card.Relations) != 4 || card.Relations[3].Relation != "constrains" {
		t.Fatalf("relations = %#v", card.Relations)
	}
	if card.Anchors[0].FileHash == "" || card.Anchors[0].RegionHash == "" {
		t.Fatal("expected source hashes")
	}
	if card.Evidence[0].Confidence != 0.9 || card.Status != "verified" || !card.Stale || card.Missing {
		t.Fatalf("evidence/status = %#v", card)
	}
}

func TestPromptFragment_Fields(t *testing.T) {
	f := PromptFragment{
		ID:       "f1",
		Name:     "system",
		Kind:     "system",
		Priority: 1,
		Content:  "Be helpful.",
		Scope:    "s1",
		Role:     "coding",
		Source:   "user",
		Path:     "system/be-helpful",
		Editable: true,
	}
	if f.ID != "f1" {
		t.Errorf("expected f1, got %q", f.ID)
	}
	if !f.Editable {
		t.Error("expected editable")
	}
}

func TestPromptContextSegment_Fields(t *testing.T) {
	s := PromptContextSegment{
		ID:         "seg-1",
		FragmentID: "f1",
		Chars:      42,
	}
	if s.Chars != 42 {
		t.Errorf("expected 42 chars, got %d", s.Chars)
	}
}

func TestPromptArtifact_Fields(t *testing.T) {
	a := PromptArtifact{
		Sections:        []string{"a", "b"},
		Fragments:       []PromptFragment{{ID: "f1", Name: "n", Kind: "k", Priority: 1, Editable: true}},
		ContextSegments: []PromptContextSegment{{ID: "seg-1", Chars: 10}},
	}
	if len(a.Sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(a.Sections))
	}
	if len(a.ContextSegments) != 1 {
		t.Errorf("expected 1 segment, got %d", len(a.ContextSegments))
	}
}

func TestAgentRef_Fields(t *testing.T) {
	a := AgentRef{
		ID:          "agent-1",
		ActorID:     "0123456789abcdef0123456789abcdef",
		ProjectID:   "p1",
		DisplayName: "Coder",
		AgentKind:   "coding",
		Status:      "active",
		Primary:     unitSlot(ModelUnit{Model: "claude-sonnet", Provider: "anthropic"}),
	}
	if a.DisplayName != "Coder" {
		t.Errorf("expected Coder, got %q", a.DisplayName)
	}
	if a.ActorID == "" {
		t.Error("expected non-empty ActorID")
	}
	if a.Primary == nil || len(a.Primary.Candidates) == 0 {
		t.Error("expected non-empty Primary slot")
	}
}

// unitSlot builds a ModelSlot wrapping a unit-kind candidate.
func unitSlot(u ModelUnit) *ModelSlot {
	return &ModelSlot{Candidates: []ModelRef{{Kind: "unit", Unit: &u}}}
}

func TestAggregatorDescriptor_Fields(t *testing.T) {
	d := AggregatorDescriptor{
		Name:    "anthropic",
		ActorID: "0123456789abcdef0123456789abcdef",
	}
	if d.Name != "anthropic" {
		t.Errorf("name = %q", d.Name)
	}
	if d.ActorID == "" {
		t.Error("expected non-empty ActorID")
	}
}

func TestAgentChatSubmitReq_Fields(t *testing.T) {
	r := AgentChatSubmitReq{
		Text:  "hello",
		Unit:  &gen.ModelUnit{Model: "claude-sonnet-4-6", Provider: "anthropic"},
		Title: "chat",
	}
	if r.Text != "hello" {
		t.Errorf("text = %q", r.Text)
	}
	if r.Unit.Model == "" {
		t.Error("expected non-empty model")
	}
}

func TestTurnEvent_Kind(t *testing.T) {
	cases := []struct {
		got  TurnEventKind
		want string
	}{
		{TurnStarted, "turn.started"},
		{TurnContextBudget, "turn.context_budget"},
		{TurnDispatchRetry, "turn.dispatch_retry"},
		{TurnPlanApprovalRequested, "turn.plan_approval_requested"},
		{TurnCompleted, "turn.completed"},
		{TurnFailed, "turn.failed"},
		{TurnCancelled, "turn.cancelled"},
		{TurnHistoryImported, "turn.history_imported"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("%v != %q", c.got, c.want)
		}
	}
}

func TestAggregatorChunk_Kind(t *testing.T) {
	cases := []struct {
		got  AggregatorChunkKind
		want string
	}{
		{AggregatorChunkText, "text_delta"},
		{AggregatorChunkReasoning, "reasoning_delta"},
		{AggregatorChunkUsage, "usage"},
		{AggregatorChunkResolvedUnit, "resolved_unit"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("%v != %q", c.got, c.want)
		}
	}
}

func TestAggregatorChunk_Fields(t *testing.T) {
	c := AggregatorChunk{
		Kind:  AggregatorChunkUsage,
		Usage: &UsageData{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}
	if c.Usage == nil || c.Usage.TotalTokens != 3 {
		t.Errorf("usage = %#v", c.Usage)
	}
}
