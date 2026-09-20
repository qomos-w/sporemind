package workbench

import (
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestOrderedIDsExcludesTerminal(t *testing.T) {
	cards := map[string]*cardState{
		"a":    {id: "a", kind: kindApp, score: 5, seq: 1},
		"term": {id: "term", kind: kindTerminal, score: 5, seq: 2},
		"b":    {id: "b", kind: kindApp, score: 25, seq: 3},
	}
	// The visible pair swaps (b leads a by 20 > 15); the terminal card is held
	// out of the score ordering and placed after it.
	got := orderedIDs(cards, []string{"a", "term", "b"})
	if len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "term" {
		t.Fatalf("order = %v, want [b a term]", got)
	}
}

func TestTerminalShouldRetreat(t *testing.T) {
	now := int64(1_000_000_000_000)
	if terminalShouldRetreat(now, 0) {
		t.Fatal("a never-summoned terminal must not retreat")
	}
	if terminalShouldRetreat(now, now-int64(10*time.Second)) {
		t.Fatal("recent activity must not retreat")
	}
	if !terminalShouldRetreat(now, now-int64(terminalRetreatAfter)) {
		t.Fatal("a stale terminal must retreat")
	}
}

func TestIngestLifecycleActivatesAndRetiresAppCard(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	if err := a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "running", ID: "demo", State: "running"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	c := a.cards[appCardPrefix+"demo"]
	if c == nil || c.hidden {
		t.Fatalf("app card should be visible after running: %+v", c)
	}
	if err := a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "unloaded", ID: "demo", State: "unloaded"},
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !a.cards[appCardPrefix+"demo"].hidden {
		t.Fatal("app card should retire after unloaded")
	}
}

func TestIngestLifecycleHeartbeatDoesNotChurn(t *testing.T) {
	// A steady-state "running" re-announcement (heartbeat / re-emit) must not
	// keep climbing the score — that drift makes the layout reshuffle forever.
	a := &Actor{cards: map[string]*cardState{}}
	_ = a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
		Event: gen.AppLifecycleEvent{Kind: "running", ID: "demo", State: "running"},
	})
	c := a.cards[appCardPrefix+"demo"]
	scoreAfterFirst := c.score
	for i := 0; i < 20; i++ {
		_ = a.handleIngestLifecycle(nil, gen.WorkbenchLifecycleIngestReq{
			Event: gen.AppLifecycleEvent{Kind: "running", ID: "demo", State: "running"},
		})
	}
	if c.score != scoreAfterFirst {
		t.Fatalf("heartbeat must not boost score: %v -> %v", scoreAfterFirst, c.score)
	}
}

func TestIngestTurnCreatesAgentCard(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	if err := a.handleIngestTurn(nil, gen.WorkbenchTurnIngestReq{EmitterID: "ag", Turn: gen.TurnEvent{}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	c := a.cards[agentCardPrefix+"ag"]
	if c == nil || c.hidden {
		t.Fatalf("agent card should be projected: %+v", c)
	}
}

func TestDriftTickLeavesScoresAlone(t *testing.T) {
	// The synthetic attention drift is intentionally not ported: the periodic
	// tick must only retire an idle terminal — scores never move on their own,
	// or the board reshuffles forever.
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["a"] = &cardState{id: "a", kind: kindApp, score: 20, seq: 1}
	a.cards["b"] = &cardState{id: "b", kind: kindApp, score: 20, seq: 2}
	a.order = []string{"a", "b"}

	if err := a.handleDriftTick(nil, gen.WorkbenchSnapshotReq{}); err != nil {
		t.Fatalf("drift tick: %v", err)
	}
	if a.cards["a"].score != 20 || a.cards["b"].score != 20 {
		t.Fatalf("scores must not drift: a=%v b=%v", a.cards["a"].score, a.cards["b"].score)
	}
}

func TestIngestStepPresenceOnly(t *testing.T) {
	// Running only establishes presence: once visible, step chatter must not
	// raise the score nor refresh lastActive, or background conversation
	// churns the layout forever (the reported complaint).
	a := &Actor{cards: map[string]*cardState{}}
	_ = a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{EmitterID: "ag"})
	c := a.cards[agentCardPrefix+"ag"]
	if c == nil || c.hidden || c.score != DefaultScore {
		t.Fatalf("first step should surface card at normal base: %+v", c)
	}
	last := c.lastActive
	for i := 0; i < 30; i++ {
		_ = a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{EmitterID: "ag"})
		_ = a.handleIngestTurn(nil, gen.WorkbenchTurnIngestReq{EmitterID: "ag"})
	}
	if c.score != DefaultScore {
		t.Fatalf("chatter must not climb score: %v", c.score)
	}
	if c.lastActive != last {
		t.Fatal("chatter must not refresh lastActive (tie-break churn)")
	}
}

func TestAgentListWiresCoordinatorCard(t *testing.T) {
	// The global Coordinator singleton is materialized from the agent list —
	// the board's default stage is its conversation, before any activity.
	a := &Actor{cards: map[string]*cardState{}}
	if err := a.handleIngestAgentList(nil, gen.WorkbenchAgentListIngestReq{Items: []gen.WorkbenchAgentModeInfo{
		{ActorID: "coord", Kind: "coordinator", DisplayName: "管家"},
		{ActorID: "plain", Kind: "coder", DisplayName: "Ada"},
	}}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	coord := a.cards[agentCardPrefix+"coord"]
	if coord == nil || coord.hidden || coord.mode != modeCoordinator || coord.score != baseCoordinator {
		t.Fatalf("coordinator card must be visible at coordinator base: %+v", coord)
	}
	if coord.title != "管家" {
		t.Fatalf("coordinator card title should come from DisplayName: %q", coord.title)
	}
	if a.cards[agentCardPrefix+"plain"] != nil {
		t.Fatal("non-coordinator agents must not materialize from list presence alone")
	}
	if _, ok := a.agentModes["plain"]; !ok {
		t.Fatal("agent modes cache should record every item")
	}
}

func TestAgentListModeBasesApplyToLaterSteps(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	_ = a.handleIngestAgentList(nil, gen.WorkbenchAgentListIngestReq{Items: []gen.WorkbenchAgentModeInfo{
		{ActorID: "w", Kind: "worker", BoundTask: true},
		{ActorID: "f", Kind: "coder", WorkflowActive: true},
	}})
	_ = a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{EmitterID: "w"})
	_ = a.handleIngestStep(nil, gen.WorkbenchStepIngestReq{EmitterID: "f"})
	if got := a.cards[agentCardPrefix+"w"]; got == nil || got.score != baseWorker || got.mode != modeWorker {
		t.Fatalf("worker card should surface at worker base: %+v", got)
	}
	if got := a.cards[agentCardPrefix+"f"]; got == nil || got.score != baseWorkflow || got.mode != modeWorkflow {
		t.Fatalf("workflow-owner card should surface at workflow base: %+v", got)
	}
}

func TestModeFlipNeverLowersUserPromotedScore(t *testing.T) {
	// A user-promoted card that later becomes a worker (workflow assigns it)
	// must not be demoted to the worker base by the mode flip.
	a := &Actor{cards: map[string]*cardState{}}
	c := a.newCardLocked(agentCardPrefix+"ag", kindChat)
	c.score = ScoreCap
	c.mode = modeNormal
	_ = a.handleIngestAgentList(nil, gen.WorkbenchAgentListIngestReq{Items: []gen.WorkbenchAgentModeInfo{
		{ActorID: "ag", Kind: "worker"},
	}})
	if c.mode != modeWorker {
		t.Fatalf("mode should flip to worker: %q", c.mode)
	}
	if c.score != ScoreCap {
		t.Fatalf("mode flip must not erode a promoted score: %v", c.score)
	}
}

func TestDriftTickSkipsPinnedAndHidden(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	a.cards["p"] = &cardState{id: "p", kind: kindApp, score: 20, seq: 1, pinned: true}
	a.cards["h"] = &cardState{id: "h", kind: kindApp, score: 20, seq: 2, hidden: true}
	a.order = []string{"p", "h"}

	if err := a.handleDriftTick(nil, gen.WorkbenchSnapshotReq{}); err != nil {
		t.Fatalf("drift tick: %v", err)
	}
	if a.cards["p"].score != 20 || a.cards["h"].score != 20 {
		t.Fatalf("pinned/hidden cards must not drift: p=%v h=%v", a.cards["p"].score, a.cards["h"].score)
	}
}

func TestDriftTickRetreatsTerminal(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	a.seedLocked()
	term := a.cards[terminalCardID]
	term.hidden = false
	term.lastActive = time.Now().Add(-2 * terminalRetreatAfter).UnixNano()
	a.order = []string{terminalCardID}

	if err := a.handleDriftTick(nil, gen.WorkbenchSnapshotReq{}); err != nil {
		t.Fatalf("drift tick: %v", err)
	}
	if !term.hidden {
		t.Fatal("stale terminal should retreat to hidden")
	}
}

func TestSnapshotCardsNonNull(t *testing.T) {
	a := &Actor{cards: map[string]*cardState{}}
	a.seedLocked()
	a.refreshLocked()
	snap, err := a.handleSnapshot(nil, gen.WorkbenchSnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.Cards == nil {
		t.Fatal("Cards must be non-nil")
	}
	if len(snap.Cards) != 0 {
		t.Fatalf("hidden terminal should be filtered from the default view: %v", snap.Cards)
	}
}
