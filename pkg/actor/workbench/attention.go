package workbench

import (
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Attention-ring kinds (WorkbenchAttentionEvent.Kind).
const (
	attentionKindLifecycle = "app_lifecycle"
	attentionKindStep      = "step"
	attentionKindTurn      = "turn"
	attentionKindUser      = "user"
)

const (
	// attentionRingCap bounds the in-memory recent-activity ring. The ring is
	// deliberately not persisted — it is a live awareness feed, not state.
	attentionRingCap = 48
	// attentionDefaultLimit is the report's default event count.
	attentionDefaultLimit = int64(32)
	// attentionSummaryMax bounds every summary we append (log/field truncation
	// discipline: producers truncate, transport never carries long bodies).
	attentionSummaryMax = 120
)

// appendRecentLocked records one activity entry onto the ring (oldest dropped).
// Caller holds a.mu.
func (a *Actor) appendRecentLocked(actorID, kind, summary string) {
	summary = truncateRunes(strings.TrimSpace(summary), attentionSummaryMax)
	a.recent = append(a.recent, gen.WorkbenchAttentionEvent{
		ActorID: truncateRunes(actorID, 64),
		Kind:    kind,
		Summary: summary,
		Time:    time.Now().UnixNano(),
	})
	if len(a.recent) > attentionRingCap {
		a.recent = a.recent[len(a.recent)-attentionRingCap:]
	}
}

// truncateRunes clamps s to at most max runes, appending an ellipsis marker
// when it actually cut.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// handleAttentionReport is the pure read surface backing the workbench
// controller bundle's global awareness: full board (hidden cards included,
// since the controller may want to summon them back) plus the recent ring.
func (a *Actor) handleAttentionReport(_ actor.PureContext, req gen.WorkbenchAttentionReportReq) (gen.WorkbenchAttentionReportResp, error) {
	limit := attentionDefaultLimit
	if req.LimitEvents > 0 {
		limit = req.LimitEvents
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	cards := append([]gen.WorkbenchCardState(nil), a.Snapshot.Cards...)
	resp := gen.WorkbenchAttentionReportResp{
		Cards:     cards,
		Recent:    []gen.WorkbenchAttentionEvent{},
		Frozen:    a.Snapshot.Frozen,
		Maximized: a.Snapshot.Maximized,
	}

	// Most recent first, capped at the requested limit.
	n := len(a.recent)
	if int64(n) > limit {
		n = int(limit)
	}
	for i := len(a.recent) - 1; i >= len(a.recent)-n; i-- {
		resp.Recent = append(resp.Recent, a.recent[i])
	}
	return resp, nil
}

// stepSummary renders the short human-readable label for a step event: the
// streamed delta or block text, else the step kind.
func stepSummary(ev gen.StepEvent) string {
	if ev.Block != nil && strings.TrimSpace(ev.Block.Text) != "" {
		return ev.Kind + ": " + ev.Block.Text
	}
	if strings.TrimSpace(ev.Delta) != "" {
		return ev.Kind + ": " + ev.Delta
	}
	return ev.Kind
}
