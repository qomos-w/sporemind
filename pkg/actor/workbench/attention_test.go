package workbench

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestAttentionReportGlobalAwareness feeds the three ingest sources plus a
// user operation through the actor and asserts the report returns the full
// board (hidden included) and the recent ring, most recent first, with user
// operations visible to the controller.
func TestAttentionReportGlobalAwareness(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}

	if err := a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "running", ID: "demo", State: "running"},
	}); err != nil {
		t.Fatalf("lifecycle ingest: %v", err)
	}
	if err := a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{
		EmitterID: "agent-1",
		Step:      gen.StepEvent{Kind: "block.appended", Delta: "npm install"},
	}); err != nil {
		t.Fatalf("step ingest: %v", err)
	}
	if _, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: appCardPrefix + "demo"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if _, err := a.handleSetHidden(nil, gen.WorkbenchSetHiddenReq{ID: agentCardPrefix + "agent-1", Hidden: true}); err != nil {
		t.Fatalf("set_hidden: %v", err)
	}

	resp, err := a.handleAttentionReport(nil, gen.WorkbenchAttentionReportReq{})
	if err != nil {
		t.Fatalf("attention report: %v", err)
	}
	if resp.Frozen {
		t.Fatal("fresh board must not be frozen")
	}
	if resp.Maximized != "" {
		t.Fatalf("fresh board maximized = %q, want none", resp.Maximized)
	}

	if len(resp.Cards) == 0 {
		t.Fatal("report must carry the board")
	}
	seenHidden := false
	for _, c := range resp.Cards {
		if c.Slot == slotHidden {
			seenHidden = true
		}
	}
	if !seenHidden {
		t.Fatal("report must include hidden cards (controller may summon them back)")
	}

	if len(resp.Recent) != 4 {
		t.Fatalf("recent = %d entries, want 4 (lifecycle, step, promote, hide)", len(resp.Recent))
	}
	// Most recent first: the user hide op lands on top.
	if resp.Recent[0].Kind != attentionKindUser || !strings.Contains(resp.Recent[0].Summary, "hide") {
		t.Fatalf("newest entry = %+v, want user hide", resp.Recent[0])
	}
	if resp.Recent[1].Kind != attentionKindUser || !strings.Contains(resp.Recent[1].Summary, "promote") {
		t.Fatalf("second entry = %+v, want user promote", resp.Recent[1])
	}
	if resp.Recent[2].Kind != attentionKindStep || resp.Recent[2].ActorID != "agent-1" {
		t.Fatalf("third entry = %+v, want agent-1 step", resp.Recent[2])
	}
	if resp.Recent[3].Kind != attentionKindLifecycle {
		t.Fatalf("oldest entry = %+v, want lifecycle", resp.Recent[3])
	}
	for _, ev := range resp.Recent {
		if ev.Time <= 0 {
			t.Fatalf("entry %+v missing timestamp", ev)
		}
	}
}

// TestAttentionReportLimitAndRingCap verifies the LimitEvents cap and the ring
// bound: the ring keeps the newest entries and never exceeds attentionRingCap.
func TestAttentionReportLimitAndRingCap(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	for i := 0; i < attentionRingCap+10; i++ {
		a.mu.Lock()
		a.appendRecentLocked("ag", attentionKindTurn, "turn t")
		a.mu.Unlock()
	}
	if len(a.recent) != attentionRingCap {
		t.Fatalf("ring = %d entries, want capped at %d", len(a.recent), attentionRingCap)
	}

	resp, err := a.handleAttentionReport(nil, gen.WorkbenchAttentionReportReq{LimitEvents: 5})
	if err != nil {
		t.Fatalf("attention report: %v", err)
	}
	if len(resp.Recent) != 5 {
		t.Fatalf("recent = %d entries, want LimitEvents=5", len(resp.Recent))
	}
}

// TestAttentionSummaryTruncation guards the log/field truncation discipline:
// every summary the ring emits is bounded regardless of input size.
func TestAttentionSummaryTruncation(t *testing.T) {
	long := strings.Repeat("长", attentionSummaryMax*3)
	if got := truncateRunes(long, attentionSummaryMax); len([]rune(got)) > attentionSummaryMax+1 {
		t.Fatalf("truncated summary = %d runes, want ≤ %d+1", len([]rune(got)), attentionSummaryMax)
	}
	a := &Actor{cards: map[string]*cardState{}}
	if err := a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{
		EmitterID: "ag",
		Step:      gen.StepEvent{Kind: "block.appended", Delta: long},
	}); err != nil {
		t.Fatalf("step ingest: %v", err)
	}
	a.mu.RLock()
	last := a.recent[len(a.recent)-1]
	a.mu.RUnlock()
	if len([]rune(last.Summary)) > attentionSummaryMax+1 {
		t.Fatalf("ring summary = %d runes, want ≤ %d+1", len([]rune(last.Summary)), attentionSummaryMax)
	}
}

// Compile-time guard: the report handler keeps the pure (stateless) signature.
var _ func(actor.PureContext, gen.WorkbenchAttentionReportReq) (gen.WorkbenchAttentionReportResp, error) = (*Actor)(nil).handleAttentionReport
