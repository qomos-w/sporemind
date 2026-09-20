package workbench

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestOrderedIDsHysteresis(t *testing.T) {
	cards := map[string]*cardState{
		"a": {id: "a", score: 20, seq: 1},
		"b": {id: "b", score: 30, seq: 2},
	}
	// b leads by 10 (< HYSTERESIS 15): the incumbent order is preserved.
	if got := orderedIDs(cards, []string{"a", "b"}); got[0] != "a" {
		t.Fatalf("expected incumbents to hold order (a,b), got %v", got)
	}
	// b now leads by 16 (> 15): the challenger swaps in.
	cards["b"].score = 36
	if got := orderedIDs(cards, []string{"a", "b"}); got[0] != "b" {
		t.Fatalf("expected challenger to swap in (b,a), got %v", got)
	}
}

func TestOrderedIDsPinnedFront(t *testing.T) {
	cards := map[string]*cardState{
		"a": {id: "a", score: 10, seq: 1, pinned: true},
		"b": {id: "b", score: 12, seq: 2},
	}
	got := orderedIDs(cards, []string{"b", "a"})
	if got[0] != "a" {
		t.Fatalf("expected pinned card first, got %v", got)
	}
}

func TestOrderedIDsNewcomersAndHiddenTail(t *testing.T) {
	cards := map[string]*cardState{
		"a": {id: "a", score: 10, seq: 1},
		"b": {id: "b", score: 10, seq: 2, hidden: true},
		"c": {id: "c", score: 10, seq: 3},
	}
	got := orderedIDs(cards, []string{"a"})
	// visible order starts from prev then appends c; hidden b is last.
	if len(got) != 3 || got[0] != "a" || got[1] != "c" || got[2] != "b" {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestBuildCardsSlots(t *testing.T) {
	cards := map[string]*cardState{
		"main": {id: "main", kind: kindApp, score: 30, seq: 1},
		"side": {id: "side", kind: kindApp, score: 10, seq: 2},
		"term": {id: "term", kind: kindTerminal, score: 12, seq: 3},
		"gone": {id: "gone", kind: kindApp, score: 5, seq: 4, hidden: true},
	}
	order := []string{"main", "side", "term", "gone"}
	out := buildCards(cards, order)
	slotByID := map[string]string{}
	for _, c := range out {
		slotByID[c.ID] = c.Slot
	}
	if slotByID["main"] != slotMain {
		t.Fatalf("main slot = %q", slotByID["main"])
	}
	if slotByID["side"] != slotSide {
		t.Fatalf("side slot = %q", slotByID["side"])
	}
	if slotByID["term"] != slotTerm {
		t.Fatalf("term slot = %q", slotByID["term"])
	}
	if slotByID["gone"] != slotHidden {
		t.Fatalf("hidden slot = %q", slotByID["gone"])
	}
}

func TestPromoteScoreAndCap(t *testing.T) {
	if got := promoteScore(20); got != 20+Hysteresis+1 {
		t.Fatalf("promoteScore(20) = %v", got)
	}
	if got := capScore(100); got != ScoreCap {
		t.Fatalf("capScore(100) = %v", got)
	}
	if got := capScore(1); got != ScoreFloor {
		t.Fatalf("capScore(1) = %v", got)
	}
}

func TestMatchTerminalKeyword(t *testing.T) {
	ev := &gen.StepEvent{Block: &gen.ContentBlock{Type: "tool_use", ToolName: "Bash", Input: `{"command":"go test ./..."}`}}
	if got := matchTerminalKeyword(ev); got == "" {
		t.Fatal("expected a terminal keyword match for a Bash tool step")
	}
	plain := &gen.StepEvent{Block: &gen.ContentBlock{Type: "text", Text: "just thinking about the design"}}
	if got := matchTerminalKeyword(plain); got != "" {
		t.Fatalf("unexpected match %q", got)
	}
}

func TestActiveLifecycle(t *testing.T) {
	cases := []struct {
		kind, state string
		want        bool
	}{
		{"running", "running", true},
		{"reloaded", "running", true},
		{"unloaded", "unloaded", false},
		{"stopped", "stopped", false},
		{"failed", "failed", false},
	}
	for _, tc := range cases {
		if got := activeLifecycle(tc.kind, tc.state); got != tc.want {
			t.Fatalf("activeLifecycle(%q,%q) = %v want %v", tc.kind, tc.state, got, tc.want)
		}
	}
}

func TestPromoteHandler(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, title: "A", score: 10, seq: 1}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: 30, seq: 2}
	a.order = []string{"b", "a"}
	a.refreshLocked()

	snap, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "a"})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	// Decay-first promote: b (30) decays to 22, then a = 22+16 = 38 —
	// strictly highest, under the cap, no squeeze needed.
	if a.cards["a"].score != 38 {
		t.Fatalf("promoted score = %v, want 38 (decayed top 22 + 16)", a.cards["a"].score)
	}
	if a.cards["b"].score != 22 {
		t.Fatalf("peer must decay to 22, got %v", a.cards["b"].score)
	}
	// The promoted card is now the main slot.
	var main *gen.WorkbenchCardState
	for i := range snap.Cards {
		if snap.Cards[i].Slot == slotMain {
			main = &snap.Cards[i]
		}
	}
	if main == nil || main.ID != "a" {
		t.Fatalf("expected a to hold main slot, snapshot=%+v", snap.Cards)
	}
}

func TestPromoteUnknownIDCreatesCard(t *testing.T) {
	// A click can land on a card the actor has not been told about yet (the
	// frontend descriptor exists, the upsert has not arrived). Promote must
	// create it and put it in main — never a silent no-op.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: 30, seq: 1}
	a.order = []string{"b"}
	a.refreshLocked()

	snap, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "app:undeclared"})
	if err != nil {
		t.Fatalf("promote unknown: %v", err)
	}
	if a.cards["app:undeclared"] == nil {
		t.Fatal("promote of an unknown id must create the card")
	}
	for i := range snap.Cards {
		if snap.Cards[i].ID == "app:undeclared" && snap.Cards[i].Slot != slotMain {
			t.Fatalf("undeclared card should take main slot, got %s", snap.Cards[i].Slot)
		}
	}
}

func TestIngestStepSummonsTerminal(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	a.seedLocked() // terminal card, hidden
	if !a.cards[terminalCardID].hidden {
		t.Fatal("terminal should start hidden")
	}
	step := gen.WorkbenchStepIngestReq{
		EmitterID: "agent-1",
		Step:      gen.StepEvent{Block: &gen.ContentBlock{Type: "tool_use", ToolName: "Bash", Input: "npm run build"}},
	}
	if err := a.handleIngestStep(nil, step); err != nil {
		t.Fatalf("ingest step: %v", err)
	}
	if a.cards[terminalCardID].hidden {
		t.Fatal("terminal should be summoned by the keyword match")
	}
	agent := a.cards[agentCardPrefix+"agent-1"]
	if agent == nil || agent.hidden {
		t.Fatal("expected a visible agent card")
	}
}

func TestOrderedIDsLastActiveTieBreak(t *testing.T) {
	// Equal scores: the card with the more recent lastActive wins main.
	cards := map[string]*cardState{
		"a": {id: "a", title: "A", score: 30, seq: 1, lastActive: 100},
		"b": {id: "b", title: "B", score: 30, seq: 2, lastActive: 200},
	}
	// prev order has a first (incumbent). b has a more recent lastActive →
	// the tie-swap should promote b.
	got := orderedIDs(cards, []string{"a", "b"})
	if got[0] != "b" {
		t.Fatalf("expected b (more recent lastActive) first, got %v", got)
	}
}

func TestOrderedIDsTitleTieBreak(t *testing.T) {
	// Equal scores AND equal lastActive: alphabetical title wins.
	cards := map[string]*cardState{
		"z": {id: "z", title: "Zeta", score: 30, seq: 1, lastActive: 100},
		"a": {id: "a", title: "Alpha", score: 30, seq: 2, lastActive: 100},
	}
	got := orderedIDs(cards, []string{"z", "a"})
	if got[0] != "a" {
		t.Fatalf("expected a (alphabetical title) first, got %v", got)
	}
}

func TestPromoteSqueezePreservesGaps(t *testing.T) {
	// Two cards both at ScoreCap → promote must squeeze, not tie.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, title: "A", score: ScoreCap, seq: 1}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: ScoreCap, seq: 2}
	a.order = []string{"a", "b"}
	a.refreshLocked()

	snap, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "b"})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	// b must be strictly higher than a after squeeze.
	if a.cards["b"].score <= a.cards["a"].score {
		t.Fatalf("b must be strictly highest after squeeze: a=%v b=%v", a.cards["a"].score, a.cards["b"].score)
	}
	var main *gen.WorkbenchCardState
	for i := range snap.Cards {
		if snap.Cards[i].Slot == slotMain {
			main = &snap.Cards[i]
		}
	}
	if main == nil || main.ID != "b" {
		t.Fatalf("expected b in main after promote, got %+v", main)
	}
}

func TestPromoteNoSqueezeWhenRoomAvailable(t *testing.T) {
	// Decay-first: a (20) decays to 12, b = 12+16 = 28 < ScoreCap — no
	// squeeze, raw promote target preserved.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, title: "A", score: 20, seq: 1}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: 10, seq: 2}
	a.order = []string{"a", "b"}
	a.refreshLocked()

	if _, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "b"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	want := 12 + Hysteresis + 1 // 28, no squeeze needed
	if a.cards["b"].score != want {
		t.Fatalf("promoted score = %v, want %v (no squeeze)", a.cards["b"].score, want)
	}
	if a.cards["a"].score != 20-promoteDecay {
		t.Fatalf("peer must decay by %v, got %v", promoteDecay, a.cards["a"].score)
	}
}

func TestPromoteMultiCardReachesMainInOneClick(t *testing.T) {
	// Three visible cards; the card at the last side position must reach the
	// main slot in a single promote — not advance one position per click.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, title: "A", score: 20, seq: 1}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: 10, seq: 2}
	a.cards["c"] = &cardState{id: "c", kind: kindApp, title: "C", score: 10, seq: 3}
	a.order = []string{"a", "b", "c"}
	a.refreshLocked()

	snap, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "c"})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if a.cards["c"].score <= a.cards["a"].score {
		t.Fatalf("c must be strictly highest: a=%v c=%v", a.cards["a"].score, a.cards["c"].score)
	}
	var main *gen.WorkbenchCardState
	for i := range snap.Cards {
		if snap.Cards[i].Slot == slotMain {
			main = &snap.Cards[i]
		}
	}
	if main == nil || main.ID != "c" {
		t.Fatalf("expected c in main after one promote, got %+v", main)
	}
}

func TestPinnedMainBlocksPromoteFromSide(t *testing.T) {
	// A pinned main card always holds the main slot: promoting a side card
	// raises its score but the pinned incumbent stays. This documents the
	// intended semantics — unpin before switching attention.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, title: "A", score: 20, seq: 1, pinned: true}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, title: "B", score: 10, seq: 2}
	a.order = []string{"a", "b"}
	a.refreshLocked()

	snap, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "b"})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	var main *gen.WorkbenchCardState
	for i := range snap.Cards {
		if snap.Cards[i].Slot == slotMain {
			main = &snap.Cards[i]
		}
	}
	if main == nil || main.ID != "a" {
		t.Fatalf("pinned card must keep main, got %+v", main)
	}
}

func TestPromoteDecaysPeers(t *testing.T) {
	// Attention is shared: promoting one card withdraws promoteDecay from its
	// visible peers (floored), exempts pinned/hidden/terminal, and preserves
	// the peers' relative order.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["hi"] = &cardState{id: "hi", kind: kindApp, title: "Hi", score: 30, seq: 1}
	a.cards["lo"] = &cardState{id: "lo", kind: kindApp, title: "Lo", score: 6, seq: 2} // decays to floor
	a.cards["pin"] = &cardState{id: "pin", kind: kindApp, title: "Pin", score: 20, seq: 3, pinned: true}
	a.cards["hid"] = &cardState{id: "hid", kind: kindApp, title: "Hid", score: 20, seq: 4, hidden: true}
	a.cards["tgt"] = &cardState{id: "tgt", kind: kindApp, title: "Tgt", score: 10, seq: 5}
	a.order = []string{"hi", "lo", "pin", "tgt"}
	a.refreshLocked()

	if _, err := a.handlePromote(nil, gen.WorkbenchCardRefReq{ID: "tgt"}); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if got := a.cards["hi"].score; got != 30-promoteDecay {
		t.Fatalf("visible peer must decay to %v, got %v", 30-promoteDecay, got)
	}
	if got := a.cards["lo"].score; got != ScoreFloor {
		t.Fatalf("decay must floor at %v, got %v", ScoreFloor, got)
	}
	if got := a.cards["pin"].score; got != 20 {
		t.Fatalf("pinned peer must be exempt from decay, got %v", got)
	}
	if got := a.cards["hid"].score; got != 20 {
		t.Fatalf("hidden peer must be exempt from decay, got %v", got)
	}
	if a.cards["tgt"].score <= a.cards["hi"].score {
		t.Fatalf("promoted card must lead decayed peers: tgt=%v hi=%v", a.cards["tgt"].score, a.cards["hi"].score)
	}
}

func TestSetPinnedRepeatedClicksKeepBoardStable(t *testing.T) {
	// Regression: a capped main card (m, stale lastActive) vs an agent-fed
	// peer (x, newer lastActive, tied at ScoreCap). Repeated pin/unpin of m
	// must refresh m's lastActive so the tie-break never drops m to a side
	// slot — the board must not shuffle under repeated main-card clicks.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["m"] = &cardState{id: "m", kind: kindApp, title: "M", score: ScoreCap, seq: 1, lastActive: 100, pinned: true}
	a.cards["x"] = &cardState{id: "x", kind: kindApp, title: "X", score: ScoreCap, seq: 2, lastActive: 200}
	a.order = []string{"m", "x"}
	a.refreshLocked()

	for i := 0; i < 4; i++ {
		if _, err := a.handleSetPinned(nil, gen.WorkbenchSetPinnedReq{ID: "m", Pinned: i%2 == 0}); err != nil {
			t.Fatalf("setPinned %d: %v", i, err)
		}
		if a.order[0] != "m" {
			t.Fatalf("click %d dropped main card, order=%v", i, a.order)
		}
	}
	if a.cards["m"].lastActive <= 200 {
		t.Fatalf("pin toggle must refresh lastActive, got %d", a.cards["m"].lastActive)
	}
}
