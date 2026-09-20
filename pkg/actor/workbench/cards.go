package workbench

import (
	"sort"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Card kinds. The frontend maps these onto card-base renderers.
const (
	kindApp      = "app"
	kindPlugin   = "plugin"
	kindTerminal = "terminal"
	kindChat     = "chat"
	kindFile     = "file"
	kindNote     = "note"
)

// Layout slots. "rail" is assigned by the frontend while a card is maximized /
// captured; the actor assigns every other slot.
const (
	slotMain   = "main"
	slotSide   = "side"
	slotTerm   = "term"
	slotRail   = "rail"
	slotHidden = "hidden"
)

// cardState is the actor-owned per-card attention + layout cell. The wire
// projection (gen.WorkbenchCardState) is derived from it by buildCards.
type cardState struct {
	id     string
	kind   string
	title  string
	icon   string
	color  string
	status string
	why    string
	visual *gen.ComponentVisual
	score  float64
	pinned bool
	hidden bool
	// mode is the attention mode for agent cards (coordinator / workflow /
	// worker / normal). It selects the presence base; "" behaves as normal.
	mode string
	seq  int64 // insertion order; stable tie-break for equal scores
	// lastActive is the unix-nano timestamp of the last interaction (promote,
	// ingest, summon). It is the secondary tie-breaker for equal scores: the
	// more recently active card wins the main slot. The terminal card also
	// uses it for its retreat-to-hidden timeout.
	lastActive int64
}

// orderedIDs returns every card id: visible cards first — prev order preserved,
// newcomers appended by insertion seq, then a single adjacent-swap pass bounded
// by Hysteresis — followed by a pinned card hoisted to the head, then the
// terminal card(s), then hidden cards.
//
// The terminal card is excluded from the score ordering (it owns the fixed
// bottom band, “term” slot) exactly like the reference's stableOrder, which
// runs over ids.filter(id => id !== 'term').
//
// Pure function so the ordering contract is unit-testable without an actor.
func orderedIDs(cards map[string]*cardState, prev []string) []string {
	visible := make([]string, 0, len(cards))
	terminal := make([]string, 0)
	hidden := make([]string, 0)
	for id, c := range cards {
		switch {
		case c.hidden:
			hidden = append(hidden, id)
		case c.kind == kindTerminal:
			terminal = append(terminal, id)
		default:
			visible = append(visible, id)
		}
	}
	order := orderedVisible(cards, prev, visible)
	sort.Slice(terminal, func(i, j int) bool { return cards[terminal[i]].seq < cards[terminal[j]].seq })
	sort.Slice(hidden, func(i, j int) bool { return cards[hidden[i]].seq < cards[hidden[j]].seq })
	order = append(order, terminal...)
	return append(order, hidden...)
}

func orderedVisible(cards map[string]*cardState, prev, visible []string) []string {
	if len(visible) == 0 {
		return nil
	}
	isVisible := make(map[string]bool, len(visible))
	for _, id := range visible {
		isVisible[id] = true
	}
	order := make([]string, 0, len(visible))
	seen := make(map[string]bool, len(visible))
	for _, id := range prev {
		if isVisible[id] && !seen[id] {
			order = append(order, id)
			seen[id] = true
		}
	}
	newcomers := make([]string, 0, len(visible))
	for _, id := range visible {
		if !seen[id] {
			newcomers = append(newcomers, id)
		}
	}
	sort.Slice(newcomers, func(i, j int) bool {
		ci, cj := cards[newcomers[i]], cards[newcomers[j]]
		if ci.score != cj.score {
			return ci.score > cj.score
		}
		if ci.lastActive != cj.lastActive {
			return ci.lastActive > cj.lastActive
		}
		return ci.title < cj.title
	})
	order = append(order, newcomers...)

	// Hysteresis-stable ordering: keep the previous order, swapping adjacent
	// entries only when the lower one leads by more than Hysteresis. Repeat
	// the pass until no swap fires, so a promoted card (strictly highest after
	// the score squeeze) reaches the front in one refresh instead of advancing
	// a single position per mutation. Each swap strictly decreases
	// Σ score(order[j])·j, so the loop terminates.
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(order)-1; i++ {
			cur, next := cards[order[i]], cards[order[i+1]]
			if next.score > cur.score+Hysteresis {
				order[i], order[i+1] = order[i+1], order[i]
				changed = true
			} else if next.score == cur.score {
				if next.lastActive > cur.lastActive {
					order[i], order[i+1] = order[i+1], order[i]
					changed = true
				} else if next.lastActive == cur.lastActive && next.title < cur.title {
					order[i], order[i+1] = order[i+1], order[i]
					changed = true
				}
			}
		}
	}

	// A pinned card is forced to the head (and therefore the main slot).
	for i, id := range order {
		if cards[id].pinned {
			copy(order[1:i+1], order[0:i])
			order[0] = id
			break
		}
	}
	return order
}

// mainID returns the id occupying the main slot: the first visible non-terminal
// card in order ("" when none).
func mainID(cards map[string]*cardState, order []string) string {
	for _, id := range order {
		c := cards[id]
		if c == nil || c.hidden || c.kind == kindTerminal {
			continue
		}
		return id
	}
	return ""
}

// buildCards projects the internal card table + order into the wire card list.
func buildCards(cards map[string]*cardState, order []string) []gen.WorkbenchCardState {
	main := mainID(cards, order)
	out := make([]gen.WorkbenchCardState, 0, len(order))
	for _, id := range order {
		c := cards[id]
		if c == nil {
			continue
		}
		slot := slotSide
		switch {
		case c.hidden:
			slot = slotHidden
		case c.kind == kindTerminal:
			slot = slotTerm
		case id == main:
			slot = slotMain
		}
		out = append(out, gen.WorkbenchCardState{
			ID:         c.id,
			Kind:       c.kind,
			Title:      c.title,
			Icon:       c.icon,
			Color:      c.color,
			Score:      c.score,
			Slot:       slot,
			Pinned:     c.pinned,
			Hidden:     c.hidden,
			StatusText: c.status,
			Why:        c.why,
			Visual:     c.visual,
			Mode:       c.mode,
		})
	}
	return out
}

// promoteScore is the user-click promotion target: max score + Hysteresis + 1,
// matching promote() in blackboard-reference.html.
func promoteScore(top float64) float64 { return top + Hysteresis + 1 }

// baseForMode returns the presence base score for an attention mode. Running
// only ever establishes presence at this base — it never climbs above it.
func baseForMode(mode string) float64 {
	switch mode {
	case modeCoordinator:
		return baseCoordinator
	case modeWorkflow:
		return baseWorkflow
	case modeWorker:
		return baseWorker
	default:
		return DefaultScore
	}
}

// normalizeMode maps any input onto a known attention mode.
func normalizeMode(mode string) string {
	switch mode {
	case modeCoordinator, modeWorkflow, modeWorker:
		return mode
	default:
		return modeNormal
	}
}

// applyModeLocked sets a card's attention mode and lifts a resting card to the
// new base (never lowers — a user-promoted score must not be eroded by a mode
// flip). Mutating the caller's cardState is fine: it is already lane-owned.
func applyModeLocked(c *cardState, mode string) {
	m := normalizeMode(mode)
	if c.mode == m {
		return
	}
	c.mode = m
	if !c.hidden && c.score < baseForMode(m) {
		c.score = baseForMode(m)
	}
}

// capScore clamps an attention score into [ScoreFloor, ScoreCap].
func capScore(score float64) float64 {
	if score < ScoreFloor {
		return ScoreFloor
	}
	if score > ScoreCap {
		return ScoreCap
	}
	return score
}

// terminalShouldRetreat reports whether the terminal card should fall back to
// hidden: it was summoned (lastActive != 0) and no new output has arrived within
// terminalRetreatAfter. nowNanos/lastActive are unix-nano timestamps.
func terminalShouldRetreat(nowNanos, lastActive int64) bool {
	if lastActive == 0 {
		return false
	}
	return nowNanos-lastActive >= int64(terminalRetreatAfter)
}
