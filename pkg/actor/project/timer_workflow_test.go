package project

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestDecideNotify(t *testing.T) {
	const maxContinue = 5
	const hint = "review 2 ready for review"
	cases := []struct {
		name        string
		message     string
		history     []string
		wantAction  wfNotifyAction
		wantMessage string
	}{
		// 1. Empty history → Send original message.
		{name: "empty history sends", message: "A", history: nil, wantAction: wfNotifySend, wantMessage: "A"},
		// 2. Different message from last sent → Send.
		{name: "different message sends", message: "B", history: []string{"A", makeNudge(hint)}, wantAction: wfNotifySend, wantMessage: "B"},
		// 3. Immediate repeat of the last message → Continue.
		{name: "immediate repeat continues", message: "A", history: []string{"A"}, wantAction: wfNotifyContinue, wantMessage: makeNudge(hint)},
		// 4. Repeat after one nudge → Continue.
		{name: "repeat after one continue continues", message: "A", history: []string{"A", makeNudge(hint)}, wantAction: wfNotifyContinue, wantMessage: makeNudge(hint)},
		// 5. Repeat after N continues (4, i.e. maxContinue-1) → Continue.
		{name: "repeat after N continues continues", message: "A", history: []string{"A", makeNudge(hint), makeNudge(hint), makeNudge(hint), makeNudge(hint)}, wantAction: wfNotifyContinue, wantMessage: makeNudge(hint)},
		// 6. Repeat after N+1 continues (5, i.e. maxContinue) → Unbind.
		{name: "repeat after N+1 continues unbinds", message: "A", history: []string{"A", makeNudge(hint), makeNudge(hint), makeNudge(hint), makeNudge(hint), makeNudge(hint)}, wantAction: wfNotifyUnbind, wantMessage: ""},
		// 7. Repeat, but with a non-nudge entry in between → Send.
		{name: "repeat with non-continue in between sends", message: "A", history: []string{"A", "B"}, wantAction: wfNotifySend, wantMessage: "A"},
		// 8. Message found within N-window with all-nudge gap → Continue.
		{name: "match in window with all-continue gap continues", message: "A", history: []string{"A", makeNudge(hint), makeNudge(hint)}, wantAction: wfNotifyContinue, wantMessage: makeNudge(hint)},
		// 9. Message found within N-window but with non-nudge gap → Send.
		{name: "match in window with non-continue gap sends", message: "A", history: []string{"A", "B", makeNudge(hint)}, wantAction: wfNotifySend, wantMessage: "A"},
		// 10. No match within N-window → Send.
		{name: "no match in window sends", message: "A", history: []string{"B", "B", "B", "B", "B"}, wantAction: wfNotifySend, wantMessage: "A"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, msg := decideNotify(tc.message, tc.history, maxContinue, hint)
			if action != tc.wantAction || msg != tc.wantMessage {
				t.Fatalf("decideNotify(%q, %v, %d) = (%v, %q), want (%v, %q)",
					tc.message, tc.history, maxContinue, action, msg, tc.wantAction, tc.wantMessage)
			}
		})
	}
}

func TestNudgeHintForEvents(t *testing.T) {
	cases := []struct {
		name  string
		input []wfWorkerEvent
		want  string
	}{
		{name: "nil", input: nil, want: ""},
		{name: "single review", input: []wfWorkerEvent{{EventType: "ready_for_review"}}, want: "review 1 ready for review"},
		{name: "two reviews one dead", input: []wfWorkerEvent{
			{EventType: "ready_for_review"},
			{EventType: "ready_for_review"},
			{EventType: "dead"},
		}, want: "review 2 ready for review, handle 1 unresponsive"},
		{name: "one failed", input: []wfWorkerEvent{{EventType: "failed"}}, want: "retry 1 failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nudgeHintForEvents(tc.input)
			if got != tc.want {
				t.Fatalf("nudgeHintForEvents(%v) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNudgeHintForFrontier(t *testing.T) {
	if got := nudgeHintForFrontier(0); got != "" {
		t.Fatalf("nudgeHintForFrontier(0) = %q, want empty", got)
	}
	if got := nudgeHintForFrontier(1); got != "advance 1 frontier ticket" {
		t.Fatalf("nudgeHintForFrontier(1) = %q, want %q", got, "advance 1 frontier ticket")
	}
	if got := nudgeHintForFrontier(3); got != "advance 3 frontier tickets" {
		t.Fatalf("nudgeHintForFrontier(3) = %q, want %q", got, "advance 3 frontier tickets")
	}
}

func TestMakeNudge(t *testing.T) {
	if got := makeNudge(""); got != wfNudgeSentinel {
		t.Fatalf("makeNudge(empty) = %q, want %q", got, wfNudgeSentinel)
	}
	if got := makeNudge("review 2 ready for review"); got != "We'll continue: review 2 ready for review." {
		t.Fatalf("makeNudge(hint) = %q, want %q", got, "We'll continue: review 2 ready for review.")
	}
	if got := makeNudge("  "); got != wfNudgeSentinel {
		t.Fatalf("makeNudge(whitespace) = %q, want %q", got, wfNudgeSentinel)
	}
}

func TestNudgeWithCloser(t *testing.T) {
	base := makeNudge("review 2 ready for review")
	if got := nudgeWithCloser(base, 0); got != base {
		t.Fatalf("nudgeWithCloser(idx 0) = %q, want base %q", got, base)
	}
	if got := nudgeWithCloser(base, 1); got != base+wfNudgeClosers[1] {
		t.Fatalf("nudgeWithCloser(idx 1) = %q, want %q", got, base+wfNudgeClosers[1])
	}
	// Index wraps into the pool.
	if nudgeWithCloser(base, len(wfNudgeClosers)) != nudgeWithCloser(base, 0) {
		t.Fatal("nudgeWithCloser should wrap idx into the closer pool")
	}
	// Every varied nudge must still classify as a nudge so decideNotify's
	// dedup and escalation counting keep working.
	for i := range wfNudgeClosers {
		if !isNudge(nudgeWithCloser(base, i)) {
			t.Fatalf("varied nudge %d lost its nudge prefix: %q", i, nudgeWithCloser(base, i))
		}
	}
}

func TestVaryNudgeVaries(t *testing.T) {
	base := makeNudge("advance 1 frontier ticket")
	distinct := map[string]bool{}
	for range 50 {
		got := varyNudge(base)
		if !isNudge(got) {
			t.Fatalf("varyNudge result lost nudge prefix: %q", got)
		}
		distinct[got] = true
	}
	if len(distinct) < 2 {
		t.Fatalf("varyNudge over 50 draws produced %d distinct texts, want >= 2", len(distinct))
	}
}

func TestUpdaterDecide_RootDone(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{RootDone: true, OwnerBusy: false, Frontier: []string{"card-x"}})
	if d.Notify {
		t.Fatalf("root done should produce no notification, got %+v", d)
	}
}

func TestUpdaterDecide_OwnerBusy(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		RootDone:  false,
		OwnerBusy: true,
		Frontier:  []string{"card-x"},
	})
	if d.Notify {
		t.Fatalf("owner busy should skip all notifications, got %+v", d)
	}
}

// TestUpdaterDecide_OwnerWaitingNotBusy documents the semantic chain: when the
// owner's state is "waiting", pollAgentBusy calls isAgentBusyForWorkflowUpdate
// which returns false ("waiting" is not "running" or "paused"), so OwnerBusy=false
// reaches updaterDecide. With a non-empty Frontier, the updater must notify the
// owner — the waiting owner is reachable and should be woken for frontier tickets.
func TestUpdaterDecide_OwnerWaitingNotBusy(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		OwnerBusy: false,
		Frontier:  []string{"card-x"},
	})
	if !d.Notify {
		t.Fatalf("waiting owner is not busy, so OwnerBusy=false with frontier should notify, got %+v", d)
	}
}

func TestIsAgentBusyForWorkflowUpdate(t *testing.T) {
	if !isAgentBusyForWorkflowUpdate("running") {
		t.Error("running owner should be busy")
	}
	if !isAgentBusyForWorkflowUpdate("paused") {
		t.Error("paused owner (user/task/crash-recovery) should suppress updater notifications")
	}
	if isAgentBusyForWorkflowUpdate("idle") {
		t.Error("idle owner should not be busy")
	}
	if isAgentBusyForWorkflowUpdate("waiting") {
		t.Error("waiting owner is not busy — pollAgentBusy returns false so updater can notify about frontier/worker events")
	}
}

func TestUpdaterDecide_FrontierNonEmpty(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		MapID:          "my-map",
		Frontier:       []string{"task-a", "task-b"},
		HasActiveOwner: true,
	})
	if !d.Notify || !strings.Contains(d.Message, "frontier") {
		t.Fatalf("non-empty frontier should notify, got %+v", d)
	}
	for _, want := range []string{"[[my-map]]", "[[task-a]]", "[[task-b]]", "2 frontier tickets"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("frontier message should list %s, got:\n%s", want, d.Message)
		}
	}
}

func TestUpdaterDecide_NoActiveOwner(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		HasActiveOwner: false,
	})
	if !d.Notify || !strings.Contains(d.Message, "Assess whether the overall goal is met") {
		t.Fatalf("tree exhaustion should request owner assessment, got %+v", d)
	}
}

func TestUpdaterDecide_AllIdleOwnerAssesses(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		HasActiveOwner: false,
	})
	if !d.Notify || !strings.Contains(d.Message, "Assess whether the overall goal is met") {
		t.Fatalf("exhausted tree should be notified to assess, got %+v", d)
	}
}

func TestUpdaterDecide_FrontierBeatsNoOwner(t *testing.T) {
	d := updaterDecide(wfUpdaterInput{
		Frontier:       []string{"card-x"},
		HasActiveOwner: false,
	})
	if !strings.Contains(d.Message, "frontier") {
		t.Fatalf("frontier should take priority over no active owner, got %+v", d)
	}
}

func TestFormatFrontierMessage_ReorderHint(t *testing.T) {
	msg := formatFrontierMessage("my-map", []string{"task-a"})
	if !strings.Contains(msg, "truly the right next tickets") {
		t.Errorf("frontier message should prompt owner to verify the frontier, got:\n%s", msg)
	}
	if !strings.Contains(msg, "Reorder") {
		t.Errorf("frontier message should instruct owner to reorder if the frontier is wrong, got:\n%s", msg)
	}
	if !strings.Contains(msg, "defer any you do not start") {
		t.Errorf("frontier message should instruct owner to defer tickets it does not start, got:\n%s", msg)
	}
	if !strings.Contains(msg, "advance per your Workflow Tools") {
		t.Errorf("frontier message should still instruct to advance, got:\n%s", msg)
	}
}

func TestIsAgentDead_Failed(t *testing.T) {
	if !isAgentDead(&gen.AgentStatusResp{State: "failed"}) {
		t.Error("failed state should be dead")
	}
	if isAgentDead(&gen.AgentStatusResp{State: "running"}) {
		t.Error("running state should not be dead")
	}
}

func TestIsAgentDead_StaleLastTurn(t *testing.T) {
	stale := time.Now().Add(-15 * time.Minute).UTC().Format(time.RFC3339)
	recent := time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339)

	// Idle + active goal + stale turn = dead.
	if !isAgentDead(&gen.AgentStatusResp{State: "", LastTurnCompletedAt: stale, Goal: &gen.GoalSummary{Status: "active"}}) {
		t.Error("idle + active goal + stale turn should be dead")
	}
	// Idle + active goal + recent turn = not dead.
	if isAgentDead(&gen.AgentStatusResp{State: "", LastTurnCompletedAt: recent, Goal: &gen.GoalSummary{Status: "active"}}) {
		t.Error("idle + active goal + recent turn should not be dead")
	}
	// Idle + ready_for_review + stale turn = NOT dead (intentionally paused).
	if isAgentDead(&gen.AgentStatusResp{State: "", LastTurnCompletedAt: stale, Goal: &gen.GoalSummary{Status: "ready_for_review"}}) {
		t.Error("idle + ready_for_review should not be dead")
	}
	// Idle + no goal = not dead.
	if isAgentDead(&gen.AgentStatusResp{State: "", LastTurnCompletedAt: stale}) {
		t.Error("idle + no goal should not be dead")
	}
}

func TestEnforceSingleRootBinding(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Two workflow cards bound to the same owner.
	m1Raw := "---\nid: map1\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include: []\n  ownerAgentId: arch-1\n---\n\nM1."
	m2Raw := "---\nid: map2\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include: []\n  ownerAgentId: arch-1\n---\n\nM2."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "map1", Raw: m1Raw})
	// Slight delay so map2 has a later Modified timestamp.
	m2Card := setOwnerInDataBlock(m2Raw, "arch-1")
	m2Card = ensureCardMeta("map2", m2Card, time.Now().Add(1*time.Second).UTC().Format(time.RFC3339))
	a.store.Save(&CardRecord{Title: "map2", Raw: m2Card})

	cards, _ := a.store.List()
	var mapCards []*CardRecord
	for _, c := range cards {
		if c.Type == "workflow" {
			mapCards = append(mapCards, c)
		}
	}
	a.enforceSingleRootBinding(ctx, mapCards)

	c1, _ := a.store.Get("map1")
	c2, _ := a.store.Get("map2")
	owner1, _ := c1.Data["ownerAgentId"].(string)
	owner2, _ := c2.Data["ownerAgentId"].(string)
	if owner1 != "" {
		t.Errorf("older card map1 should be unbound, got owner %q", owner1)
	}
	if owner2 != "arch-1" {
		t.Errorf("newer card map2 should keep binding, got owner %q", owner2)
	}
}

// ── worker event aggregation tests ──

func TestCollectWorkerEvents_MultipleReady(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review"},
		{AgentID: "w2", TaskCardID: "t-B", GoalStatus: "ready_for_review"},
		{AgentID: "w3", TaskCardID: "t-C", State: "running"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	// Both ready agents must be collected (no first-match short-circuit).
	seen := map[string]bool{}
	for _, e := range events {
		if e.EventType != "ready_for_review" {
			t.Errorf("event type = %q, want ready_for_review", e.EventType)
		}
		seen[e.AgentID] = true
	}
	if !seen["w1"] || !seen["w2"] {
		t.Errorf("expected w1 and w2, got %v", seen)
	}
}

func TestCollectWorkerEvents_ReadyAndFailed(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review"},
		{AgentID: "w2", TaskCardID: "t-B", State: "failed"},
		{AgentID: "w3", TaskCardID: "t-C", State: "idle", GoalStatus: "active"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 2 {
		t.Fatalf("expected 2 events (ready + failed), got %d: %+v", len(events), events)
	}
	types := map[string]string{}
	for _, e := range events {
		types[e.AgentID] = e.EventType
	}
	if types["w1"] != "ready_for_review" {
		t.Errorf("w1 = %q, want ready_for_review", types["w1"])
	}
	if types["w2"] != "failed" {
		t.Errorf("w2 = %q, want failed", types["w2"])
	}
}

func TestCollectWorkerEvents_AllTypes(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review"},
		{AgentID: "w2", TaskCardID: "t-B", State: "failed"},
		{AgentID: "w3", TaskCardID: "t-C", IsDead: true},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(events), events)
	}
}

// TestClassifyWorkerEvent_OrphanReviewWhenCardResolved pins the terminal-card
// reconciliation: a ready_for_review worker whose bound task card is already
// resolved (done/failed/cancelled) is reclassified as orphan_review, because
// approve would fail the card CAS — the owner must agent_terminate instead.
// A pending_review / blocked / unknown card status stays ready_for_review.
func TestClassifyWorkerEvent_OrphanReviewWhenCardResolved(t *testing.T) {
	cases := []struct {
		name           string
		goal, cardStat string
		want           string
	}{
		{"resolved-done", "ready_for_review", "done", "orphan_review"},
		{"resolved-failed", "ready_for_review", "failed", "orphan_review"},
		{"resolved-cancelled", "ready_for_review", "cancelled", "orphan_review"},
		{"pending-review", "ready_for_review", "pending_review", "ready_for_review"},
		{"blocked-not-resolved", "ready_for_review", "blocked", "ready_for_review"},
		{"unknown-card", "ready_for_review", "", "ready_for_review"},
	}
	for _, c := range cases {
		got := classifyWorkerEvent(wfOwnerState{GoalStatus: c.goal, BoundCardStatus: c.cardStat})
		if got != c.want {
			t.Errorf("%s: classifyWorkerEvent(card=%q) = %q, want %q", c.name, c.cardStat, got, c.want)
		}
	}
}

// TestCollectWorkerEvents_OrphanReview verifies the reconciliation flows through
// collectWorkerEvents: a ready_for_review worker with a done card yields an
// orphan_review event (distinct EventType → distinct wfEventKey → fresh
// notify), while one with a pending_review card stays ready_for_review.
func TestCollectWorkerEvents_OrphanReview(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review", BoundCardStatus: "done"},
		{AgentID: "w2", TaskCardID: "t-B", GoalStatus: "ready_for_review", BoundCardStatus: "pending_review"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	typ := map[string]string{}
	for _, e := range events {
		typ[e.AgentID] = e.EventType
	}
	if typ["w1"] != "orphan_review" {
		t.Errorf("w1 (done card) = %q, want orphan_review", typ["w1"])
	}
	if typ["w2"] != "ready_for_review" {
		t.Errorf("w2 (pending_review card) = %q, want ready_for_review", typ["w2"])
	}
}

func TestCollectWorkerEvents_EmptyWhenHealthy(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", State: "running"},
		{AgentID: "w2", TaskCardID: "t-B", State: "idle", GoalStatus: "active"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 0 {
		t.Fatalf("expected 0 events for healthy workers, got %d: %+v", len(events), events)
	}
}

func TestCollectWorkerEvents_UserPausedWorkerNotReported(t *testing.T) {
	owners := []wfOwnerState{
		// User-paused mid-work: must not be reported, even if it looks dead.
		{AgentID: "w1", TaskCardID: "t-A", State: "paused", PauseKind: "user", GoalStatus: "active", IsDead: true},
		// User-paused after declaring ready_for_review: still suppressed.
		{AgentID: "w2", TaskCardID: "t-B", State: "paused", PauseKind: "user", GoalStatus: "ready_for_review"},
		// Task-paused and genuinely dead: still reported (not a user pause).
		{AgentID: "w3", TaskCardID: "t-C", State: "paused", PauseKind: "task", IsDead: true},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 1 {
		t.Fatalf("expected 1 event (w3 dead), got %d: %+v", len(events), events)
	}
	if events[0].AgentID != "w3" || events[0].EventType != "dead" {
		t.Errorf("unexpected event: %+v", events[0])
	}
}

func TestCollectWorkerEvents_IncludesTaskCardID(t *testing.T) {
	owners := []wfOwnerState{
		{ActorID: "actor-42", AgentID: "w1", TaskCardID: "ticket-42", GoalStatus: "ready_for_review"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 1 || events[0].TaskCardID != "ticket-42" {
		t.Fatalf("task card id not propagated: %+v", events)
	}
	// The actor id must ride along so the owner can pass it straight to
	// workspace.agent_review instead of guessing from the spawn name.
	if events[0].ActorID != "actor-42" {
		t.Fatalf("actor id not propagated: %+v", events)
	}
}

// ── message format tests ──

func TestFormatWorkerEventSummary_Format(t *testing.T) {
	events := []wfWorkerEvent{
		{ActorID: "actor-w1", AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review"},
		{ActorID: "actor-w2", AgentID: "w2", TaskCardID: "t-B", EventType: "failed"},
		{ActorID: "actor-w3", AgentID: "w3", TaskCardID: "", EventType: "dead"},
		{AgentID: "w4", TaskCardID: "t-D", EventType: "dead"}, // no actor id (legacy)
	}
	msg := formatWorkerEventSummary(events)
	if !strings.Contains(msg, "Agent w1 (actor: actor-w1, task: t-A): ready for review") {
		t.Errorf("missing w1 line in:\n%s", msg)
	}
	if !strings.Contains(msg, "Agent w2 (actor: actor-w2, task: t-B): failed") {
		t.Errorf("missing w2 line in:\n%s", msg)
	}
	if !strings.Contains(msg, "Agent w3 (actor: actor-w3): unresponsive") {
		t.Errorf("missing w3 line (no task card) in:\n%s", msg)
	}
	if !strings.Contains(msg, "Agent w4 (task: t-D): unresponsive") {
		t.Errorf("missing w4 line (no actor id) in:\n%s", msg)
	}
	if !strings.Contains(msg, "4 items") {
		t.Errorf("missing count in:\n%s", msg)
	}
}

func TestFormatWorkerEventSummary_SingleItem(t *testing.T) {
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review"},
	}
	msg := formatWorkerEventSummary(events)
	if !strings.Contains(msg, "1 item") {
		t.Errorf("singular noun expected in:\n%s", msg)
	}
}

// TestFormatWorkerEventSummary_MandatoryDisposition verifies the summary carries
// a lightweight mandatory reminder pointing to the Workflow Tools bundle for the
// full review procedure and edge-case handling. The detailed disposition rules
// now live in the workflow-tools bundle, not in the per-notification injection.
// A weak "please review and take action" caused workers to dangle in pending_review.
func TestFormatWorkerEventSummary_MandatoryDisposition(t *testing.T) {
	// Non-ready_for_review events keep the generic mandatory instruction.
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "failed"},
		{AgentID: "w2", TaskCardID: "t-B", EventType: "dead"},
	}
	msg := formatWorkerEventSummary(events)
	if strings.Contains(msg, "Please review and take action.") {
		t.Errorf("weak closing instruction must be gone:\n%s", msg)
	}
	for _, want := range []string{
		"Mandatory: every item above must reach an explicit disposition",
		"Workflow Tools",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("summary missing mandatory instruction %q:\n%s", want, msg)
		}
	}
	// Detailed disposition rules must no longer be in the injection — they
	// are now in the workflow-tools bundle to keep the notification lightweight.
	for _, stale := range []string{
		"project.review_changeset",
		"reject with concrete actionable feedback",
		"workspace.agent_terminate",
		"cannot be completed in the current state",
		"create a new task card (or adjust the bound card)",
		"workspace.agent_send_message",
		"reports agent not found",
		"workspace.list_agents",
		"project.wiki_set_status",
		"append a short audit note",
	} {
		if strings.Contains(msg, stale) {
			t.Errorf("injection must not carry detailed rule %q (now in bundle):\n%s", stale, msg)
		}
	}
}

// TestFormatWorkerEventSummary_OrphanReviewInstruction verifies an orphan_review
// event yields the agent_terminate disposition (no merge) and suppresses the
// ready_for_review merge instruction. A done-card orphan cannot be approved
// (card CAS expects pending_review), so the owner must terminate to release
// the worker + worktree.
func TestFormatWorkerEventSummary_OrphanReviewInstruction(t *testing.T) {
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", ActorID: "act-1", EventType: "orphan_review"},
	}
	msg := formatWorkerEventSummary(events)
	for _, want := range []string{
		"orphaned review",
		"already resolved",
		"workspace.agent_terminate",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("summary missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "merge its branch") {
		t.Errorf("orphan_review must not carry the ready_for_review merge instruction:\n%s", msg)
	}
}

func TestFormatWorkerEventSummary_ReadyForReviewWithBranch(t *testing.T) {
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review", WorktreeBranch: "feature/t4-merge"},
	}
	msg := formatWorkerEventSummary(events)
	if !strings.Contains(msg, "branch: feature/t4-merge") {
		t.Errorf("summary missing branch:\n%s", msg)
	}
	for _, want := range []string{
		"git merge feature/t4-merge",
		"a true merge, not cherry-pick/squash",
		"workspace.agent_review to approve",
		"cleans up the child worktree",
		"system rebases the child for you",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("summary missing merge instruction %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "git merge <branch>") {
		t.Errorf("known branch must not leave the <branch> placeholder:\n%s", msg)
	}
	if strings.Contains(msg, "Mandatory: every item above must reach an explicit disposition") {
		t.Errorf("ready_for_review summary must use merge instructions, not generic disposition:\n%s", msg)
	}
}

func TestFormatWorkerEventSummary_ReadyForReviewMultipleBranches(t *testing.T) {
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review", WorktreeBranch: "wf-one"},
		{AgentID: "w2", TaskCardID: "t-B", EventType: "ready_for_review", WorktreeBranch: "wf-two"},
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review", WorktreeBranch: "wf-one"}, // duplicate branch
	}
	msg := formatWorkerEventSummary(events)
	want := "git merge wf-one; git merge wf-two"
	if !strings.Contains(msg, want) {
		t.Errorf("summary missing joined merge commands %q:\n%s", want, msg)
	}
	if strings.Contains(msg, "wf-one; git merge wf-one") {
		t.Errorf("duplicate branch must be deduped:\n%s", msg)
	}
}

func TestFormatWorkerEventSummary_ReadyForReviewNoBranch(t *testing.T) {
	// Empty branch still triggers the ready_for_review merge guidance, but no branch suffix.
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review", WorktreeBranch: ""},
	}
	msg := formatWorkerEventSummary(events)
	if strings.Contains(msg, "branch:") {
		t.Errorf("empty branch must not emit branch suffix:\n%s", msg)
	}
	if !strings.Contains(msg, "workspace.agent_review to approve") {
		t.Errorf("ready_for_review merge guidance expected even without branch:\n%s", msg)
	}
	if !strings.Contains(msg, "git merge <branch>") {
		t.Errorf("unknown branch must fall back to the <branch> placeholder:\n%s", msg)
	}
	// A no-worktree ready_for_review worker must surface the no-branch
	// hint so the reviewer knows no merge is required for that worker.
	if !strings.Contains(msg, "no merge is needed for them") {
		t.Errorf("empty branch ready_for_review must surface the no-merge hint:\n%s", msg)
	}
}

// TestFormatWorkerEventSummary_ReadyForReviewMixedBranches pins that a
// mixed list (worktree worker with a branch + no-worktree worker without a
// branch) renders both the per-branch merge commands and the no-branch
// hint, so the reviewer does not look for a missing branch on the no-worktree
// entry.
func TestFormatWorkerEventSummary_ReadyForReviewMixedBranches(t *testing.T) {
	events := []wfWorkerEvent{
		{AgentID: "w1", TaskCardID: "t-A", EventType: "ready_for_review", WorktreeBranch: "feature/with-branch"},
		{AgentID: "w2", TaskCardID: "t-B", EventType: "ready_for_review", WorktreeBranch: ""},
	}
	msg := formatWorkerEventSummary(events)
	for _, want := range []string{
		"branch: feature/with-branch",
		"git merge feature/with-branch",
		"no merge is needed for them",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("mixed-branch summary missing %q:\n%s", want, msg)
		}
	}
}

func TestCollectWorkerEvents_PropagatesWorktreeBranch(t *testing.T) {
	owners := []wfOwnerState{
		{ActorID: "actor-w1", AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review", WorktreeBranch: "feature/t4-merge"},
	}
	events := collectWorkerEvents(owners)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d: %+v", len(events), events)
	}
	if events[0].WorktreeBranch != "feature/t4-merge" {
		t.Errorf("WorktreeBranch not propagated: got %q, want %q", events[0].WorktreeBranch, "feature/t4-merge")
	}
}

func TestLookupWorktreeBranch(t *testing.T) {
	a := newTestActor(t.TempDir())
	a.worktrees = map[string]gen.ProjectWorktree{
		"wt-123": {ID: "wt-123", Branch: "feature/t4-merge"},
	}

	if got := a.lookupWorktreeBranch("wt-123"); got != "feature/t4-merge" {
		t.Errorf("lookupWorktreeBranch(wt-123) = %q, want %q", got, "feature/t4-merge")
	}
	if got := a.lookupWorktreeBranch(""); got != "" {
		t.Errorf("lookupWorktreeBranch(\"\") = %q, want empty", got)
	}
	if got := a.lookupWorktreeBranch("wt-missing"); got != "" {
		t.Errorf("lookupWorktreeBranch(wt-missing) = %q, want empty", got)
	}
}

// ── event aggregation tests ──

// TestCollectWorkerEvents_MixedEventsAggregated verifies that multiple different event
// types in one tick produce a single aggregated message listing all of them.
func TestCollectWorkerEvents_MixedEventsAggregated(t *testing.T) {
	owners := []wfOwnerState{
		{AgentID: "w1", TaskCardID: "t-A", GoalStatus: "ready_for_review"},
		{AgentID: "w2", TaskCardID: "t-B", State: "failed"},
		{AgentID: "w3", TaskCardID: "t-C", IsDead: true},
	}
	events := collectWorkerEvents(owners)
	msg := formatWorkerEventSummary(events)

	// All three must appear in the single message.
	for _, want := range []string{"w1", "w2", "w3", "ready for review", "failed", "unresponsive"} {
		if !strings.Contains(msg, want) {
			t.Errorf("aggregated message missing %q:\n%s", want, msg)
		}
	}
}

// ── dispatchOwnerChatSubmit tests ──
// dispatchOwnerChatSubmit is fire-and-forget: once Invoke accepts the message
// for delivery it returns true without waiting for the owner's handler. This
// prevents a circular wait when the owner's turn startup queries workspace.
// A nil Invoke is the only dispatch failure.

// successNotifyRef is a mock ref.Ref whose Invoke returns a call that succeeds
// with a non-empty TurnActorID.
type successNotifyRef struct{}

func (successNotifyRef) ID() id.ActorID          { return id.ActorID{} }
func (successNotifyRef) Service() (string, bool) { return "", false }
func (successNotifyRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentChatSubmitResp{TurnActorID: "turn-1"}})
}

// errorNotifyRef is a mock ref.Ref whose Invoke returns a call that errors on
// Recv (handler rejection). Final surfaces the error → dispatch returns false.
type errorNotifyRef struct{ err error }

func (errorNotifyRef) ID() id.ActorID          { return id.ActorID{} }
func (errorNotifyRef) Service() (string, bool) { return "", false }
func (errorNotifyRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(errors.New("handler rejected")))
}

// blockingNotifyRef is a mock ref.Ref whose Invoke returns a call that blocks
// forever (simulating a hung agent handler). Used to prove dispatch never
// waits on the handler.
type blockingNotifyRef struct{}

func (blockingNotifyRef) ID() id.ActorID          { return id.ActorID{} }
func (blockingNotifyRef) Service() (string, bool) { return "", false }
func (blockingNotifyRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, newBlockingStream())
}

// nilNotifyRef is a mock ref.Ref whose Invoke returns nil (creation failure).
type nilNotifyRef struct{}

func (nilNotifyRef) ID() id.ActorID          { return id.ActorID{} }
func (nilNotifyRef) Service() (string, bool) { return "", false }
func (nilNotifyRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return nil
}

// oneShotStream yields one value then io.EOF — simulates a successful unary call.
type oneShotStream struct {
	value any
	sent  bool
}

func (s *oneShotStream) Recv() (any, error) {
	if !s.sent {
		s.sent = true
		return s.value, nil
	}
	return nil, io.EOF
}
func (s *oneShotStream) RecvRaw() ([]byte, error) { return nil, io.EOF }
func (s *oneShotStream) Close() error             { return nil }

// blockingStream blocks in Recv until Close is called. This simulates a hung
// agent handler that never responds. When a bounded Final's context expires,
// the invoke layer calls Cancel() → Close(), unblocking Recv with an error so
// Final returns the context error.
type blockingStream struct {
	done chan struct{}
	once sync.Once
}

func newBlockingStream() *blockingStream {
	return &blockingStream{done: make(chan struct{})}
}

func (s *blockingStream) Recv() (any, error) {
	<-s.done // blocks until Close() closes the channel
	return nil, errors.New("stream cancelled")
}
func (s *blockingStream) RecvRaw() ([]byte, error) {
	<-s.done
	return nil, errors.New("stream cancelled")
}
func (s *blockingStream) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// Compile-time interface assertions.
var _ ref.Ref = successNotifyRef{}
var _ ref.Ref = errorNotifyRef{}
var _ ref.Ref = blockingNotifyRef{}
var _ ref.Ref = nilNotifyRef{}

func TestDispatchOwnerChatSubmit_Accepted(t *testing.T) {
	if !dispatchOwnerChatSubmit(context.Background(), successNotifyRef{}, "test message") {
		t.Fatal("dispatchOwnerChatSubmit should return true when Invoke accepts delivery")
	}
	if !dispatchOwnerChatSubmit(context.Background(), errorNotifyRef{}, "test message") {
		t.Fatal("dispatchOwnerChatSubmit should not wait for handler errors")
	}
}

func TestDispatchOwnerChatSubmit_NilCall(t *testing.T) {
	if dispatchOwnerChatSubmit(context.Background(), nilNotifyRef{}, "test message") {
		t.Fatal("dispatchOwnerChatSubmit should return false when Invoke returns nil")
	}
}

func TestDispatchOwnerChatSubmit_BlockingTargetReturnsPromptly(t *testing.T) {
	start := time.Now()
	ok := dispatchOwnerChatSubmit(context.Background(), blockingNotifyRef{}, "test message")
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("dispatchOwnerChatSubmit should accept a message without waiting for the blocked handler")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("dispatchOwnerChatSubmit waited for the target handler: %v", elapsed)
	}
}

// ── integration test: workflowUpdateOneMap with mock agent chain ──

// mockAgentStatusRef responds to "agent_status" with a fixed status.
type mockAgentStatusRef struct {
	actorID id.ActorID
	status  gen.AgentStatusResp
}

func (r *mockAgentStatusRef) ID() id.ActorID          { return r.actorID }
func (r *mockAgentStatusRef) Service() (string, bool) { return "", false }
func (r *mockAgentStatusRef) Invoke(_ context.Context, callID string, _ any, _ ...map[string]string) *invoke.Call {
	if callID != "agent_status" {
		return nil
	}
	return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: r.status})
}

// controllableOwnerRef responds to "agent_status" (idle unless busy, in which
// case running) and "chat_submit" with configurable dispatch behavior via the
// nilChatSubmit / blockChatSubmit flags. chatSubmitCalls counts dispatches so
// tests can assert how often the updater notifies. By default agent_status
// reports a fresh LastTurnCompletedAt (owner consumes notifications);
// unconsumedNotify flips it to a stale timestamp to simulate a backlogged
// owner whose chat_submit queue accepted the frame but never processed it.
type controllableOwnerRef struct {
	actorID          id.ActorID
	busy             bool
	state            string
	pauseKind        string
	workflowMapID    string
	blockChatSubmit  bool
	nilChatSubmit    bool
	unconsumedNotify bool
	// goalStatus, when set, is reported as Goal.Status; staleTurn makes
	// LastTurnCompletedAt one hour old so the owner satisfies isAgentDead.
	goalStatus      string
	staleTurn       bool
	mu              sync.Mutex
	chatSubmitCalls int
	sentTexts       []string
	sentMeta        []string
}

func (r *controllableOwnerRef) ID() id.ActorID          { return r.actorID }
func (r *controllableOwnerRef) Service() (string, bool) { return "", false }
func (r *controllableOwnerRef) chatSubmitCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.chatSubmitCalls
}
func (r *controllableOwnerRef) sentMessages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sentTexts...)
}
func (r *controllableOwnerRef) sentMetas() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sentMeta...)
}
func (r *controllableOwnerRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	switch callID {
	case "agent_status":
		state := r.state
		if state == "" {
			state = "idle"
		}
		if r.busy {
			state = "running"
		}
		lastDone := time.Now().UTC()
		if r.unconsumedNotify || r.staleTurn {
			lastDone = lastDone.Add(-time.Hour)
		}
		resp := gen.AgentStatusResp{
			State: state, PauseKind: r.pauseKind, ActiveWorkflowMapCardID: r.workflowMapID,
			LastTurnCompletedAt: lastDone.Format(time.RFC3339Nano),
		}
		if r.goalStatus != "" {
			resp.Goal = &gen.GoalSummary{Status: r.goalStatus}
		}
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: resp})
	case "chat_submit":
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.nilChatSubmit {
			return nil
		}
		r.chatSubmitCalls++
		if req, ok := payload.(gen.AgentChatSubmitReq); ok {
			r.sentTexts = append(r.sentTexts, req.Text)
			r.sentMeta = append(r.sentMeta, req.Meta)
		}
		if r.blockChatSubmit {
			return invoke.NewCall(invoke.CallModeUnary, newBlockingStream())
		}
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentChatSubmitResp{TurnActorID: "turn-1"}})
	}
	return nil
}

// mockWorkspaceRef responds to "workspace.list_agents" with a fixed agent list
// and counts "workspace.load_agent" and "workspace.agent_loaded" calls so tests
// can assert owner-wake behavior.
type mockWorkspaceRef struct {
	actorID id.ActorID
	agents  []gen.AgentRef

	mu             sync.Mutex
	loadAgentCalls int
	loadAgentIDs   []string
	agentLoadedCalls int
	listAgentsCalls int
}

func (r *mockWorkspaceRef) ID() id.ActorID          { return r.actorID }
func (r *mockWorkspaceRef) Service() (string, bool) { return "workspace", true }
func (r *mockWorkspaceRef) loadAgentCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadAgentCalls
}
func (r *mockWorkspaceRef) agentLoadedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.agentLoadedCalls
}
func (r *mockWorkspaceRef) listAgentsCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listAgentsCalls
}

func (r *mockWorkspaceRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	switch callID {
	case "workspace.list_agents":
		r.mu.Lock()
		r.listAgentsCalls++
		r.mu.Unlock()
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentRefListResp{Items: r.agents}})
	case "workspace.load_agent":
		agentID := ""
		switch req := payload.(type) {
		case gen.WorkspaceLoadAgentReq:
			agentID = req.AgentID
		}
		r.mu.Lock()
		r.loadAgentCalls++
		r.loadAgentIDs = append(r.loadAgentIDs, agentID)
		r.mu.Unlock()
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: domain.AgentRef{}})
	case "workspace.agent_loaded":
		r.mu.Lock()
		r.agentLoadedCalls++
		r.mu.Unlock()
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.WorkspaceAgentLoadedResp{}})
	}
	return nil
}

var _ ref.Ref = (*mockAgentStatusRef)(nil)
var _ ref.Ref = (*controllableOwnerRef)(nil)
var _ ref.Ref = (*mockWorkspaceRef)(nil)

func waitForChatSubmitCalls(t *testing.T, owner *controllableOwnerRef, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if owner.chatSubmitCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("chat_submit calls = %d, want %d", owner.chatSubmitCount(), want)
}

// TestDispatchOwnerChatSubmit_PreservesTextAndCarriesMeta is a focused check of
// the single chat_submit exit used by every updater notification path: the Text
// payload is byte-identical to the message passed in (even when the text itself
// contains the meta value) and the Meta field is wfNotifyMeta, so the frontend
// can distinguish system continuation messages from human submits.
func TestDispatchOwnerChatSubmit_PreservesTextAndCarriesMeta(t *testing.T) {
	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 850)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ref := &controllableOwnerRef{actorID: id.From(ownerCID)}
	// Deliberately includes the word "workflow" so a naive text-substitution of
	// the meta value would be caught by the byte equality check.
	message := "Workflow status update (1 item):\n\n- Agent w1 (actor: a1, task: task-A): ready for review\n\nThe review request is waiting: workflow advance is blocked until you assess the result."
	if !dispatchOwnerChatSubmit(context.Background(), ref, message) {
		t.Fatal("dispatchOwnerChatSubmit returned false (Invoke rejected the frame)")
	}
	msgs := ref.sentMessages()
	metas := ref.sentMetas()
	if len(msgs) != 1 || len(metas) != 1 {
		t.Fatalf("expected 1 dispatched chat_submit, got texts=%v metas=%v", msgs, metas)
	}
	if msgs[0] != message {
		t.Fatalf("chat_submit Text changed: got %q, want %q", msgs[0], message)
	}
	if metas[0] != wfNotifyMeta {
		t.Fatalf("chat_submit Meta = %q, want %q", metas[0], wfNotifyMeta)
	}
}

// TestWorkflowUpdateOneMap_NotifyCarriesWorkflowMeta verifies the full
// notifyOwnerWithDedup → notifyOwner → dispatchOwnerChatSubmit path: the
// aggregated worker-event summary reaches chat_submit with Meta "workflow" and
// byte-identical Text, and the nudge degradation path (same exit) also carries
// the Meta. dedup/nudge behavior is unchanged: tick 1 sends the full message,
// tick 2 degrades to a nudge.
func TestWorkflowUpdateOneMap_NotifyCarriesWorkflowMeta(t *testing.T) {
	mapID := "wf-meta"
	a, fctx, ownerRef, workerActorID, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "doing"}); err != nil {
		t.Fatalf("set task-A doing: %v", err)
	}

	// Expected aggregated worker-event summary, computed from the same fields
	// the updater observes (w1 bound to task-A, ready for review).
	want := formatWorkerEventSummary([]wfWorkerEvent{
		{ActorID: workerActorID, AgentID: "w1", TaskCardID: "task-A", EventType: "ready_for_review"},
	})

	mapCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	msgs := ownerRef.sentMessages()
	metas := ownerRef.sentMetas()
	if len(msgs) != 1 || len(metas) != 1 {
		t.Fatalf("tick 1: expected 1 dispatch, got texts=%v metas=%v", msgs, metas)
	}
	if msgs[0] != want {
		t.Fatalf("tick 1: dispatched Text differs from generated message:\n got: %q\nwant: %q", msgs[0], want)
	}
	if metas[0] != wfNotifyMeta {
		t.Fatalf("tick 1: chat_submit Meta = %q, want %q", metas[0], wfNotifyMeta)
	}

	// Tick 2: identical pending condition, owner idle and has consumed → the
	// second dispatch is a nudge. It must still carry the workflow Meta, and
	// the nudge/decideNotify logic must be untouched (isNudge + dedup still
	// classify on text alone).
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	msgs = ownerRef.sentMessages()
	metas = ownerRef.sentMetas()
	if len(msgs) != 2 || len(metas) != 2 {
		t.Fatalf("tick 2: expected 2 dispatches, got texts=%v metas=%v", msgs, metas)
	}
	if !isNudge(msgs[1]) {
		t.Fatalf("tick 2: expected a nudge, got %q", msgs[1])
	}
	if metas[1] != wfNotifyMeta {
		t.Fatalf("tick 2 (nudge): chat_submit Meta = %q, want %q", metas[1], wfNotifyMeta)
	}
}

// TestWorkflowUpdateOneMap_ReNotifiesIdleOwner is an integration test that
// exercises the full workflowUpdateOneMap path: workspace.list_agents →
// agent_status polling → collectWorkerEvents → notifyOwner (synchronous
// chat_submit). It verifies the no-dedup contract: a failed dispatch retries
// on the next tick, and an unresolved ready_for_review re-notifies on every
// tick while the owner stays idle — the owner must never be left with a
// pending review and no further wake-ups.
func TestWorkflowUpdateOneMap_ReNotifiesIdleOwner(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Valid canonical IDs so lookupAgentRef can parse them.
	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 100)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	workerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 200)
	if err != nil {
		t.Fatalf("worker CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	workerActorID := workerCID.String()
	ownerAID := id.From(ownerCID)
	workerAID := id.From(workerCID)

	// Create map + owner + task card.
	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-map"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-map", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-map", Title: "task-A", Question: "Q1"})

	// Tick 1: nilChatSubmit causes Invoke to return nil → dispatch fails.
	ownerRef := &controllableOwnerRef{actorID: ownerAID, nilChatSubmit: true, workflowMapID: "wf-map"}
	workerRef := &mockAgentStatusRef{actorID: workerAID, status: gen.AgentStatusResp{
		State: "idle",
		Goal:  &gen.GoalSummary{Status: "ready_for_review", BoundTaskCardID: "task-A", Confirmed: true},
	}}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-map"}},
		{ID: "w1", ActorID: workerActorID, LoadState: "loaded"},
	}}

	// FakeCtx wired with all mock refs.
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		if aid == workerAID {
			return workerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-map")

	// Tick 1: dispatch fails (Invoke returns nil) → no chat_submit accepted.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 0 {
		t.Fatalf("tick 1 (failed dispatch): expected 0 dispatches, got %d", ownerRef.chatSubmitCount())
	}

	// Tick 2: fix the owner ref so dispatch succeeds.
	ownerRef.nilChatSubmit = false
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 1 {
		t.Fatalf("tick 2 (successful dispatch): expected 1 dispatch, got %d", ownerRef.chatSubmitCount())
	}

	// Tick 3: same state, owner still idle → re-notify. The pending review
	// must keep generating wake-ups until the owner resolves it.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 2 {
		t.Fatalf("tick 3 (idle owner, unresolved review): expected 2 dispatches, got %d", ownerRef.chatSubmitCount())
	}
}

// TestWorkflowUpdateOneMap_AllTasksDoneWithOwnerUrgesWorkflowStop verifies the
// unified completion rule end to end: a map with a bound owner whose task
// cards are all done must NOT be auto-completed (root stays doing), and the
// updater keeps urging the idle owner to finish via workflow_stop.
func TestWorkflowUpdateOneMap_AllTasksDoneWithOwnerUrgesWorkflowStop(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 130)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-done"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-done", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-done", Title: "task-Z", Question: "Q"})
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-Z", Status: "done"}); err != nil {
		t.Fatalf("set task done: %v", err)
	}

	// All tasks done but the owner is bound: the root must stay doing —
	// completion belongs to workflow_stop, not auto-complete.
	root, _ := a.store.Get("wf-done")
	if root.Status != "doing" {
		t.Fatalf("root with bound owner must stay doing after all tasks done, got %q", root.Status)
	}

	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: "wf-done"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-done"}},
	}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	a.workflowUpdateOneMap(fctx, root, wfTickAgents{})
	sent := ownerRef.sentMessages()
	if len(sent) != 1 {
		t.Fatalf("expected 1 tree-exhausted notification, got %d: %v", len(sent), sent)
	}
	if !strings.Contains(sent[0], "workflow_stop") {
		t.Fatalf("notification should urge workflow_stop, got:\n%s", sent[0])
	}

	// Root is still doing, so a later tick keeps urging (dedup degrades to
	// nudges but never goes silent while the owner ignores the message).
	root, _ = a.store.Get("wf-done")
	if root.Status != "doing" {
		t.Fatalf("root must remain doing until workflow_stop, got %q", root.Status)
	}
}

// TestWorkflowUpdateOneMap_DoneRootWithLiveOwnerKeepsReminding verifies the
// unified completion rule for the done-state itself: a done root whose owner
// is still bound and live is NOT terminal — the updater keeps reminding the
// owner to release the binding (workflow_stop / unbind) instead of going
// silent. Once the owner no longer has the map active (or is unbound), the
// updater resets its dedup state and stops.
func TestWorkflowUpdateOneMap_DoneRootWithLiveOwnerKeepsReminding(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 140)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-settled"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-settled", OwnerActorID: ownerActorID})
	// Mark the map done while the owner stays bound (e.g. done written by a
	// path other than a clean workflow_stop, or stop ran before unbind).
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "wf-settled", Status: "done"}); err != nil {
		t.Fatalf("set root done: %v", err)
	}

	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: "wf-settled"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-settled"}},
	}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-settled")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	sent := ownerRef.sentMessages()
	if len(sent) != 1 || !strings.Contains(sent[0], "still bound") {
		t.Fatalf("done+owner-bound map should remind the owner to release, got %v", sent)
	}

	// Owner released the workflow (mode cleared): the next tick must go
	// silent and reset dedup state.
	wsRef.agents = []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded"},
	}
	mapCard, _ = a.store.Get("wf-settled")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if n := ownerRef.chatSubmitCount(); n != 1 {
		t.Fatalf("owner released the workflow; updater must stay silent, got %d dispatches", n)
	}
}

// TestWorkflowUpdaterTick_SharesOneAgentList verifies the per-tick shared
// agent-list snapshot: one workspace.list_agents RPC per tick regardless of
// how many maps are processed, instead of one per map per tick.
func TestWorkflowUpdaterTick_SharesOneAgentList(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 150)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()

	ctx := newTestContext(t)
	for _, id := range []string{"wf-share-1", "wf-share-2", "wf-share-3"} {
		a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: id})
		a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: id, OwnerActorID: ownerActorID})
	}

	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}

	a.workflowUpdaterTick(fctx)
	if n := wsRef.listAgentsCount(); n != 1 {
		t.Fatalf("one tick over 3 maps must issue exactly 1 workspace.list_agents, got %d", n)
	}
}

// TestWorkflowUpdateOneMap_AutoFailedWhenOwnerDead verifies the updater path
// for an owner that is still listed by workspace.list_agents but reports a
// failed state: the root map is auto-marked failed rather than notifying a
// dead owner.
func TestWorkflowUpdateOneMap_AutoFailedWhenOwnerDead(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 101)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-dead-owner"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-dead-owner", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-dead-owner", Title: "task-X", Question: "Q"})

	ownerRef := &controllableOwnerRef{actorID: ownerAID, state: "failed", workflowMapID: "wf-dead-owner"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-dead-owner"}},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-dead-owner")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})

	root, _ := a.store.Get("wf-dead-owner")
	if root.Status != "failed" {
		t.Errorf("root status after owner dead = %q, want failed", root.Status)
	}
	if ownerRef.chatSubmitCount() != 0 {
		t.Errorf("dead owner should not be notified, got %d chat_submit calls", ownerRef.chatSubmitCount())
	}
}

// TestFreshWorkerEventsLifecycle exercises the resolution-aware worker-event
// dedup. A re-fired ready_for_review (byte-identical summary) after the worker
// was rejected and resumed must notify in full again, not collapse into a
// bare nudge. A respawned worker on the same task is also fresh.
func TestWorkflowUpdateOneMap_WakesUnloadedOwner(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 103)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-wake"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-wake", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-wake", Title: "task-A", Question: "Q"})
	// Mark the task done by writing the card directly: handleWikiSetStatus
	// would auto-complete the root (maybeAutoCompleteRoot), but this test needs
	// the live wf-codemirror-references shape — all tasks done, map "doing".
	taskCard, _ := a.store.Get("task-A")
	if err := a.store.Save(&CardRecord{Title: "task-A", Raw: setCardStatusInRaw(taskCard.Raw, "done")}); err != nil {
		t.Fatalf("mark task done: %v", err)
	}

	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "unloaded"},
	}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	// Mock SpawnFn so wakeMapOwner can spawn the agent locally (no deadlock).
	spawnedRef := testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any {
		return nil
	})
	fctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return spawnedRef, nil
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == spawnedRef.ID() {
			return spawnedRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-wake")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})

	// wakeMapOwner no longer calls workspace.load_agent — it spawns locally
	// and fire-and-forget notifies via workspace.agent_loaded. Verify the
	// new path: 0 load_agent calls, 1 agent_loaded notification.
	if got := wsRef.loadAgentCount(); got != 0 {
		t.Fatalf("load_agent calls = %d, want 0 (deadlock elimination: local spawn, no sync load_agent)", got)
	}
	if got := wsRef.agentLoadedCount(); got != 1 {
		t.Fatalf("agent_loaded calls = %d, want 1 (fire-and-forget notification)", got)
	}
	root, _ := a.store.Get("wf-wake")
	if root.Status != "doing" {
		t.Fatalf("unloaded owner must not auto-fail the root, got %q", root.Status)
	}

	// Backoff: an immediate second tick must not re-wake.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if got := wsRef.agentLoadedCount(); got != 1 {
		t.Fatalf("agent_loaded calls after tick 2 = %d, want 1 (backoff)", got)
	}
}

// TestWorkflowUpdateOneMap_WakeGraceDefersDeadOwner covers the post-wake tick:
// a freshly loaded owner reports an active goal plus a LastTurnCompletedAt
// from before the restart, which satisfies isAgentDead. Within the wake grace
// window the dead-owner auto-fail must be deferred so the tree-exhausted
// notification is delivered; once the grace lapses without the owner acting,
// the existing auto-fail path terminates the map.
func TestWorkflowUpdateOneMap_WakeGraceDefersDeadOwner(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 104)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-grace"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-grace", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-grace", Title: "task-A", Question: "Q"})
	// Same direct-write trick as WakesUnloadedOwner: task done, map still doing.
	taskCard, _ := a.store.Get("task-A")
	if err := a.store.Save(&CardRecord{Title: "task-A", Raw: setCardStatusInRaw(taskCard.Raw, "done")}); err != nil {
		t.Fatalf("mark task done: %v", err)
	}

	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: "wf-grace", goalStatus: "active", staleTurn: true}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-grace"}},
	}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	// Inside the grace window: notification delivered, no auto-fail.
	a.saveWfDedupState("wf-grace", &wfDedupState{WakeAt: time.Now().UTC().Format(time.RFC3339Nano)})

	mapCard, _ := a.store.Get("wf-grace")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})

	waitForChatSubmitCalls(t, ownerRef, 1)
	msgs := ownerRef.sentMessages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "Assess whether the overall goal is met") {
		t.Fatalf("expected tree-exhausted notification, got %v", msgs)
	}
	root, _ := a.store.Get("wf-grace")
	if root.Status != "doing" {
		t.Fatalf("grace must defer the dead-owner auto-fail, got %q", root.Status)
	}

	// Grace lapsed: the stale idle owner is declared dead and the root is
	// auto-completed (all task cards done).
	a.saveWfDedupState("wf-grace", &wfDedupState{WakeAt: time.Now().UTC().Add(-deadOwnerThreshold - time.Minute).Format(time.RFC3339Nano)})

	mapCard, _ = a.store.Get("wf-grace")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	root, _ = a.store.Get("wf-grace")
	if root.Status != "done" {
		t.Fatalf("post-grace dead owner should auto-complete the root, got %q", root.Status)
	}
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("dead owner must not be notified again, got %d dispatches", got)
	}
}

func TestFreshWorkerEventsLifecycle(t *testing.T) {
	ready := wfWorkerEvent{ActorID: "w1", TaskCardID: "t1", EventType: "ready_for_review"}

	st := &wfDedupState{}

	// First sighting → fresh; persisting after commit → not fresh.
	if !pruneNotifiedKeys(st, []wfWorkerEvent{ready}) {
		t.Fatal("first sighting should be fresh")
	}
	commitNotifiedKeys(st, []wfWorkerEvent{ready})
	if pruneNotifiedKeys(st, []wfWorkerEvent{ready}) {
		t.Fatal("still-pending event should not be fresh")
	}

	// Re-fired after the cycle resolves (notified set emptied) → fresh again.
	st.NotifiedKeys = nil
	if !pruneNotifiedKeys(st, []wfWorkerEvent{ready}) {
		t.Fatal("re-fired event after resolution should be fresh")
	}

	// Mixed: one event resolves while another stays pending. The resolved one
	// is dropped from the notified set even though the tick had events, so its
	// re-fire counts as new.
	other := wfWorkerEvent{ActorID: "w2", TaskCardID: "t2", EventType: "failed"}
	commitNotifiedKeys(st, []wfWorkerEvent{ready, other})
	if pruneNotifiedKeys(st, []wfWorkerEvent{other}) {
		t.Fatal("persisting event alone should not be fresh")
	}
	if !pruneNotifiedKeys(st, []wfWorkerEvent{ready, other}) {
		t.Fatal("re-fired event after resolution should be fresh (mixed set)")
	}

	// A respawned worker on the same task has a different actor ID → new event.
	st2 := &wfDedupState{}
	commitNotifiedKeys(st2, []wfWorkerEvent{ready})
	respawned := wfWorkerEvent{ActorID: "w1-respawn", TaskCardID: "t1", EventType: "ready_for_review"}
	if !pruneNotifiedKeys(st2, []wfWorkerEvent{respawned}) {
		t.Fatal("respawned worker on the same task should be fresh")
	}
}

// TestWorkflowUpdateOneMap_RefiredEventSendsFullMessage is the end-to-end
// regression: a worker reaches ready_for_review, the owner (in a real flow)
// rejects it, the worker resumes and reaches ready_for_review again with a
// byte-identical summary. The second occurrence must re-notify in full
// (the message body, not a bare nudge).
func TestWorkflowUpdateOneMap_RefiredEventSendsFullMessage(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 500)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	workerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 600)
	if err != nil {
		t.Fatalf("worker CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	workerActorID := workerCID.String()
	ownerAID := id.From(ownerCID)
	workerAID := id.From(workerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "rf-map"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "rf-map", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rf-map", Title: "task-A", Question: "Q1"})
	// Claim the task so the structural frontier is empty; only the worker
	// event drives notifications.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "doing"}); err != nil {
		t.Fatalf("set task-A doing: %v", err)
	}

	ready := func() gen.AgentStatusResp {
		return gen.AgentStatusResp{
			State: "idle",
			Goal:  &gen.GoalSummary{Status: "ready_for_review", BoundTaskCardID: "task-A", Confirmed: true},
		}
	}
	resumed := func() gen.AgentStatusResp {
		return gen.AgentStatusResp{
			State: "running",
			Goal:  &gen.GoalSummary{Status: "active", BoundTaskCardID: "task-A", Confirmed: true},
		}
	}

	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: "rf-map"}
	workerRef := &mockAgentStatusRef{actorID: workerAID, status: ready()}
	wsRef := &mockWorkspaceRef{agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "rf-map"}},
		{ID: "w1", ActorID: workerActorID, LoadState: "loaded"},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		if aid == workerAID {
			return workerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("rf-map")

	// Tick 1: worker ready → full summary dispatched.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("tick 1: expected 1 dispatch, got %d", got)
	}
	msgs := ownerRef.sentMessages()
	if len(msgs) != 1 || !strings.Contains(msgs[0], "ready for review") {
		t.Fatalf("tick 1 should send full summary, got %q", msgs)
	}

	// Tick 2: worker resumed (owner rejected) → no worker event; worker still
	// running so structural path also stays quiet.
	workerRef.status = resumed()
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("tick 2 (worker resumed): expected 1 dispatch, got %d", got)
	}

	// Tick 3: worker ready again (byte-identical summary). Must re-notify in
	// full — NOT a bare nudge (the pre-fix bug).
	workerRef.status = ready()
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if got := ownerRef.chatSubmitCount(); got != 2 {
		t.Fatalf("tick 3 (re-fired ready): expected 2 dispatches, got %d", got)
	}
	msgs = ownerRef.sentMessages()
	if len(msgs) != 2 {
		t.Fatalf("want 2 sent messages, got %d", len(msgs))
	}
	if isNudge(msgs[1]) {
		t.Fatalf("re-fired event must send the full summary, got bare %q", msgs[1])
	}
	if !strings.Contains(msgs[1], "ready for review") {
		t.Fatalf("re-fired event should send full summary, got %q", msgs[1])
	}

	// Tick 4: still ready, unresolved → now degrades to a nudge
	// (anti-spam), and only after the owner has consumed the tick-3 message.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	msgs = ownerRef.sentMessages()
	if len(msgs) != 3 {
		t.Fatalf("want 3 sent messages, got %d", len(msgs))
	}
	if !isNudge(msgs[2]) {
		t.Fatalf("persisting unresolved event should degrade to a nudge, got %q", msgs[2])
	}
}

// setupUpdaterFixtures builds a map + owner + ready worker. The worker's
// BoundTaskCardID is set to taskID. Returns the actor, the FakeCtx (so tests
// can read its refs), the owner ref, the worker actorID, and the map card.
func setupUpdaterFixtures(t *testing.T, mapID, taskID string) (*Actor, *testutil.FakeCtx, *controllableOwnerRef, string, *CardRecord) {
	t.Helper()
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 500)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	workerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 600)
	if err != nil {
		t.Fatalf("worker CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	workerActorID := workerCID.String()
	ownerAID := id.From(ownerCID)
	workerAID := id.From(workerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: mapID})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: mapID, OwnerActorID: ownerActorID})

	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: mapID}
	workerRef := &mockAgentStatusRef{actorID: workerAID, status: gen.AgentStatusResp{
		State: "idle",
		Goal:  &gen.GoalSummary{Status: "ready_for_review", BoundTaskCardID: taskID, Confirmed: true},
	}}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: mapID}},
		{ID: "w1", ActorID: workerActorID, LoadState: "loaded"},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		if aid == workerAID {
			return workerRef, true
		}
		return nil, false
	}
	mapCard, _ := a.store.Get(mapID)
	return a, fctx, ownerRef, workerActorID, mapCard
}

// TestWorkflowUpdateOneMap_ReadyForReviewTruncatedInclude is a regression for
// the silent-owner bug: concurrent create_task_card calls truncated the map's
// data.scope.include (only some appends survived), and gatherUpdaterInputs
// dropped every worker whose bound card was missing from that projection —
// so the worker's ready_for_review never reached the owner. The topo graph
// still knows the card; the union must rescue it.
func TestWorkflowUpdateOneMap_ReadyForReviewTruncatedInclude(t *testing.T) {
	mapID := "trunc-map"
	a, fctx, ownerRef, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})

	// Simulate the observed truncation: rewrite the map raw with an empty
	// include list. The task card, its parent field and the topo graph node
	// all remain.
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		t.Fatalf("get map: %v", err)
	}
	truncatedRaw := strings.ReplaceAll(mapCard.Raw, "      - task-A\n", "")
	if truncatedRaw == mapCard.Raw {
		t.Fatalf("expected include entry in raw:\n%s", mapCard.Raw)
	}
	if err := validateCard(mapID, truncatedRaw); err != nil {
		t.Fatalf("truncated raw invalid: %v", err)
	}
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: truncatedRaw}); err != nil {
		t.Fatalf("save truncated map: %v", err)
	}
	survivors := scopeIncludeIDs(mustCard(t, a, mapID))
	if len(survivors) != 0 {
		t.Fatalf("include should be empty after simulated truncation, got %v", survivors)
	}

	mapCard = mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	// The worker event must reach the owner despite the truncated include.
	// (Pre-fix this dispatched nothing.)
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("expected 1 dispatch with truncated include, got %d", got)
	}
}

// TestWorkflowUpdateOneMap_ParentFallbackWhenScopeLost covers the worst case:
// the include projection lost the task card. The generic create path now
// heals membership immediately (healTaskCardMapMembership), so the test
// simulates the loss the way it actually happens — a raw rewrite dropping the
// include entry — and asserts the updater still recovers and notifies: the
// reconcile adoption pass re-adopts the card via its parent field before the
// frontier/notification computation. gatherUpdaterInputs' parent-field
// fallback remains as defense-in-depth for cards the adoption predicate
// rejects (e.g. non-task types).
func TestWorkflowUpdateOneMap_ParentFallbackWhenScopeLost(t *testing.T) {
	mapID := "lost-map"
	a, fctx, ownerRef, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	// Generic create: the heal appends task-A to the map's include list right
	// away, so the updater's inputs are complete without any fallback.
	taskRaw := "---\nid: task-A\ntype: task\ntags: [" + mapID + "]\nstatus: doing\nparent: " + mapID + "\ncreated: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\ndata:\n  depends_on: []---\n\nbody\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-A", Raw: taskRaw}); err != nil {
		t.Fatalf("create task card: %v", err)
	}
	if ids := scopeIncludeIDs(mustCard(t, a, mapID)); len(ids) != 1 {
		t.Fatalf("create-time heal: include = %v, want [task-A]", ids)
	}

	// Simulate the projection loss: rewrite the map raw with an empty include
	// list. The task card and its parent field remain.
	mapCard, err := a.store.Get(mapID)
	if err != nil {
		t.Fatalf("get map: %v", err)
	}
	truncatedRaw := strings.ReplaceAll(mapCard.Raw, "      - task-A\n", "")
	if truncatedRaw == mapCard.Raw {
		t.Fatalf("expected include entry in raw:\n%s", mapCard.Raw)
	}
	if err := validateCard(mapID, truncatedRaw); err != nil {
		t.Fatalf("truncated raw invalid: %v", err)
	}
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: truncatedRaw}); err != nil {
		t.Fatalf("save truncated map: %v", err)
	}
	if ids := scopeIncludeIDs(mustCard(t, a, mapID)); len(ids) != 0 {
		t.Fatalf("include should be empty after simulated truncation, got %v", ids)
	}
	mapCard = mustCard(t, a, mapID)

	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("expected 1 dispatch after scope recovery, got %d", got)
	}
	// The reconcile adoption pass must have re-persisted the healed include.
	if ids := scopeIncludeIDs(mustCard(t, a, mapID)); len(ids) != 1 {
		t.Fatalf("adoption should have re-healed include, got %v", ids)
	}
}

// TestWorkflowUpdateOneMap_UnrelatedWorkerIgnored pins the negative case of
// the parent fallback: an agent bound to a card that belongs to a DIFFERENT
// map must not produce notifications for this map. Because the empty-frontier
// branch fires a tree-exhausted nudge, we assert that the dispatched message
// is the structural nudge, not a worker-event summary mentioning task-A.
func TestWorkflowUpdateOneMap_UnrelatedWorkerIgnored(t *testing.T) {
	mapID := "other-map"
	a, fctx, ownerRef, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	// A card parented to a DIFFERENT map.
	otherRaw := "---\nid: task-A\ntype: task\ntags: [elsewhere-map]\nstatus: doing\nparent: elsewhere-map\ncreated: \"2026-01-01T00:00:00Z\"\nmodified: \"2026-01-01T00:00:00Z\"\ndata:\n  depends_on: []---\n\nbody\n"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-A", Raw: otherRaw}); err != nil {
		t.Fatalf("create task card: %v", err)
	}

	mapCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	msgs := ownerRef.sentMessages()
	if len(msgs) == 0 {
		t.Fatalf("expected structural nudge for empty frontier, got no messages")
	}
	for _, m := range msgs {
		if strings.Contains(m, "task-A") || strings.Contains(m, "ready for review") {
			t.Fatalf("unrelated worker leaked into notification: %q", m)
		}
	}
}

func mustCard(t *testing.T, a *Actor, id string) *CardRecord {
	t.Helper()
	card, err := a.store.Get(id)
	if err != nil {
		t.Fatalf("store.Get(%q): %v", id, err)
	}
	return card
}

// TestWorkflowUpdateOneMap_OwnerlessRebindDoneMap verifies that the rebind
// fires even when the map status is "done": a done map with no owner but a live
// agent still bound via ActiveWorkflow needs the owner to be nudged to call
// workflow_stop. Without the rebind, the ownerless branch would skip directly to
// resetWfDedupState+return, never reaching the rootDone path that tells the
// owner to stop.
func TestWorkflowUpdateOneMap_OwnerlessRebindDoneMap(t *testing.T) {
	mapID := "rebind-done-map"
	a, fctx, ownerRef, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})

	// Simulate owner unbind.
	a.unbindMapOwner(ctx, mapID)

	// Set the task to done and the root to done.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "done"}); err != nil {
		t.Fatalf("set done: %v", err)
	}
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: mapID, Status: "done"}); err != nil {
		t.Fatalf("set map done: %v", err)
	}

	// Run the updater. The ownerless branch should detect the stalled owner
	// (still loaded with ActiveWorkflowMapCardID matching) even though the
	// map is done, re-stamp ownerAgentId, and fall through to the rootDone
	// path which nudges the owner to call workflow_stop.
	mapCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})

	// The map card should have ownerAgentId re-stamped.
	rebound := mustCard(t, a, mapID)
	ownerID, ok := rebound.Data["ownerAgentId"].(string)
	if !ok || ownerID == "" {
		t.Fatalf("expected ownerAgentId re-stamped after rebind on done map, got %q", ownerID)
	}

	// The owner should have been nudged (at least one chat_submit dispatched).
	if got := ownerRef.chatSubmitCount(); got == 0 {
		t.Fatalf("expected owner nudge after rebind on done map, got 0 dispatches")
	}
}

// TestWorkflowUpdateOneMap_OwnerlessRebindStalledOwner verifies the stall
// recovery path: when the owner was unbound by nudge escalation (ownerAgentId
// cleared from the map card) but the agent is still alive with an
// ActiveWorkflow binding to this map, the updater detects the stall, re-stamps
// ownerAgentId, and resumes the normal nudge path so the owner is prompted to
// finish the pending_review tasks instead of the map being silently abandoned.
func TestWorkflowUpdateOneMap_OwnerlessRebindStalledOwner(t *testing.T) {
	mapID := "rebind-map"
	a, fctx, ownerRef, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})

	// Simulate nudge-escalation unbind: clear ownerAgentId from the map card.
	// The workspace registry (wsRef in setupUpdaterFixtures) still lists the
	// owner as loaded with ActiveWorkflowMapCardID = mapID.
	a.unbindMapOwner(ctx, mapID)

	// Set the task to pending_review so there is work the owner must finish.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "pending_review"}); err != nil {
		t.Fatalf("set pending_review: %v", err)
	}

	// Run the updater. The ownerless branch should detect the stalled owner
	// (still loaded with ActiveWorkflowMapCardID matching this map), re-stamp
	// ownerAgentId, and fall through to the normal nudge path.
	mapCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})

	// The map card should have ownerAgentId re-stamped.
	rebound := mustCard(t, a, mapID)
	ownerID, ok := rebound.Data["ownerAgentId"].(string)
	if !ok || ownerID == "" {
		t.Fatalf("expected ownerAgentId re-stamped after rebind, got %q", ownerID)
	}

	// The owner should have been nudged (at least one chat_submit dispatched).
	if got := ownerRef.chatSubmitCount(); got == 0 {
		t.Fatalf("expected owner nudge after rebind, got 0 dispatches")
	}
}

// TestWorkflowUpdateOneMap_OwnerlessAutoComplete verifies the ownerless-map
// recovery path: when the owner was unbound (nudge escalation) but all task
// cards are done, the updater clears the stale ownerWorktreeId stamp and
// auto-completes the root so the map does not linger in "doing" with no
// owner to finish it.
//
// pending_review tasks must NOT trigger auto-complete: their worker branches
// have not been merged into the owner worktree, and auto-completing would
// skip the merge and lose the work.
func TestWorkflowUpdateOneMap_OwnerlessAutoComplete(t *testing.T) {
	mapID := "ownerless-ac-map"
	a, _, _, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})

	// Simulate owner unbind: clear ownerAgentId from the map card.
	a.unbindMapOwner(ctx, mapID)

	// Set the task card to done (owner merged + approved before unbind).
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "done"}); err != nil {
		t.Fatalf("set done: %v", err)
	}

	// Stamp an ownerWorktreeId to simulate the dangling worktree reference.
	mapCard := mustCard(t, a, mapID)
	mapCard.Raw = setCardDataFieldInRaw(mapCard.Raw, "ownerWorktreeId", "test-wt-id")
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: mapCard.Raw}); err != nil {
		t.Fatalf("save map: %v", err)
	}

	// Run the updater — the ownerless recovery path should fire.
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) { return nil, false }
	updatedCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, updatedCard, wfTickAgents{})

	// The root should now be done.
	root := mustCard(t, a, mapID)
	if root.Status != "done" {
		t.Fatalf("expected root status done, got %q", root.Status)
	}
}

// TestWorkflowUpdateOneMap_OwnerlessPendingReviewNotAutoComplete verifies that
// an ownerless map with pending_review tasks is NOT auto-completed: the worker
// branches have not been merged, and auto-completing would lose the work.
func TestWorkflowUpdateOneMap_OwnerlessPendingReviewNotAutoComplete(t *testing.T) {
	mapID := "ownerless-pr-map"
	a, _, _, _, _ := setupUpdaterFixtures(t, mapID, "task-A")
	ctx := newTestContext(t)
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: mapID, Title: "task-A", Question: "Q1"})

	// Simulate owner unbind: clear ownerAgentId from the map card.
	a.unbindMapOwner(ctx, mapID)

	// Set the task card to pending_review (worker finished, awaiting merge+approve).
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "task-A", Status: "pending_review"}); err != nil {
		t.Fatalf("set pending_review: %v", err)
	}

	// Stamp an ownerWorktreeId to simulate the dangling worktree reference.
	mapCard := mustCard(t, a, mapID)
	mapCard.Raw = setCardDataFieldInRaw(mapCard.Raw, "ownerWorktreeId", "test-wt-id")
	if err := a.store.Save(&CardRecord{Title: mapID, Raw: mapCard.Raw}); err != nil {
		t.Fatalf("save map: %v", err)
	}

	// Run the updater — the ownerless recovery path should NOT fire.
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) { return nil, false }
	updatedCard := mustCard(t, a, mapID)
	a.workflowUpdateOneMap(fctx, updatedCard, wfTickAgents{})

	// The root should still be doing (not auto-completed).
	root := mustCard(t, a, mapID)
	if root.Status != "doing" {
		t.Fatalf("expected root status doing (pending_review must not auto-complete), got %q", root.Status)
	}
}

// TestWorkflowUpdateOneMap_BlockingChatSubmitReturnsPromptly is a regression
// for the updater deadlock: the owner's chat_submit handler blocks forever,
// but workflowUpdateOneMap must return immediately after delivery rather than
// waiting for the handler.
func TestWorkflowUpdateOneMap_BlockingChatSubmitReturnsPromptly(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 300)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	workerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 400)
	if err != nil {
		t.Fatalf("worker CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	workerActorID := workerCID.String()
	ownerAID := id.From(ownerCID)
	workerAID := id.From(workerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-map-to"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-map-to", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-map-to", Title: "task-A", Question: "Q1"})

	ownerRef := &controllableOwnerRef{actorID: ownerAID, blockChatSubmit: true, workflowMapID: "wf-map-to"}
	workerRef := &mockAgentStatusRef{actorID: workerAID, status: gen.AgentStatusResp{
		State: "idle",
		Goal:  &gen.GoalSummary{Status: "ready_for_review", BoundTaskCardID: "task-A", Confirmed: true},
	}}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-map-to"}},
		{ID: "w1", ActorID: workerActorID, LoadState: "loaded"},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == ownerAID {
			return ownerRef, true
		}
		if aid == workerAID {
			return workerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-map-to")

	// Tick 1: chat_submit blocks in the owner, but the updater must return
	// after Invoke accepts delivery instead of waiting for the handler.
	start := time.Now()
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("workflowUpdateOneMap waited for blocked chat_submit: %v", elapsed)
	}
	if ownerRef.chatSubmitCount() != 1 {
		t.Fatalf("expected 1 chat_submit dispatch, got %d", ownerRef.chatSubmitCount())
	}
}

// TestPollAgentStatus_ReturnsAfterDeadline is a regression for the updater
// deadlock: agent_status polling must return after its deadline instead of
// hanging forever when the target agent is unresponsive. It uses a short
// helper timeout; the production timeout (workflowPollTimeout) is unchanged.
func TestPollAgentStatus_ReturnsAfterDeadline(t *testing.T) {
	a := newTestActor(t.TempDir())

	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 500)
	if err != nil {
		t.Fatalf("CID: %v", err)
	}
	aid := id.From(cid)

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == aid {
			return blockingNotifyRef{}, true
		}
		return nil, false
	}

	timeout := 100 * time.Millisecond
	start := time.Now()
	resp := a.pollAgentStatusTimeout(fctx, cid.String(), timeout)
	elapsed := time.Since(start)

	if resp != nil {
		t.Fatalf("blocking agent_status should return nil, got %+v", resp)
	}
	// Must have waited until (close to) the deadline, not returned instantly
	// or hung past it.
	if elapsed < 50*time.Millisecond {
		t.Fatalf("poll returned too fast: %v (expected ~%v deadline)", elapsed, timeout)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("poll blocked too long: %v (expected ~%v)", elapsed, timeout)
	}
}

// TestCheckAgentIdle_ReturnsAfterDeadline verifies the scheduler monitor's
// status poll is bounded too, rather than blocking the project timer loop.
func TestCheckAgentIdle_ReturnsAfterDeadline(t *testing.T) {
	a := newTestActor(t.TempDir())

	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 550)
	if err != nil {
		t.Fatalf("CID: %v", err)
	}
	aid := id.From(cid)

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == aid {
			return blockingNotifyRef{}, true
		}
		return nil, false
	}

	start := time.Now()
	idle, err := a.checkAgentIdleTimeout(fctx, cid.String(), 100*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil || idle {
		t.Fatalf("blocking agent_status = (idle=%t, err=%v), want (false, error)", idle, err)
	}
	if elapsed < 50*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("checkAgentIdleTimeout duration = %v, want approximately 100ms", elapsed)
	}
}

// TestPollAgentStatus_Success verifies a healthy agent_status poll still
// decodes the response through the Final path.
func TestPollAgentStatus_Success(t *testing.T) {
	a := newTestActor(t.TempDir())

	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 600)
	if err != nil {
		t.Fatalf("CID: %v", err)
	}
	aid := id.From(cid)
	status := gen.AgentStatusResp{
		State: "running",
		Goal:  &gen.GoalSummary{Status: "active", BoundTaskCardID: "task-A"},
	}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == aid {
			return &mockAgentStatusRef{actorID: aid, status: status}, true
		}
		return nil, false
	}

	resp := a.pollAgentStatusTimeout(fctx, cid.String(), time.Second)
	if resp == nil {
		t.Fatal("healthy agent_status poll should return a status")
	}
	if resp.State != "running" {
		t.Fatalf("state = %q, want running", resp.State)
	}
	if resp.Goal == nil || resp.Goal.BoundTaskCardID != "task-A" {
		t.Fatalf("goal not decoded: %+v", resp.Goal)
	}
}

// TestWorkflowUpdateOneMap_PausedOwnerNotWoken verifies that a paused owner
// (user pause, task pause, or crash-recovery pause) is treated as busy and
// receives no updater messages, and becomes wakeable again once idle.
func TestWorkflowUpdateOneMap_PausedOwnerNotWoken(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 650)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-pause"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-pause", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-pause", Title: "task-A", Question: "Q1"})

	ownerRef := &controllableOwnerRef{actorID: ownerAID, state: "paused", pauseKind: "user", workflowMapID: "wf-pause"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-pause"}}}}
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-pause")
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 0 {
		t.Fatalf("user-paused owner should not be woken, got %d dispatches", ownerRef.chatSubmitCount())
	}

	// Task pause and crash-recovery pause (no explicit kind) are also busy.
	ownerRef.pauseKind = "task"
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	ownerRef.pauseKind = ""
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 0 {
		t.Fatalf("paused owner should not be woken regardless of pause kind, got %d dispatches", ownerRef.chatSubmitCount())
	}

	// Back to idle: the pending notification re-fires.
	ownerRef.state = "idle"
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	waitForChatSubmitCalls(t, ownerRef, 1)
}

// TestWorkflowUpdateOneMap_FrontierNotificationRepeatsUntilHandled verifies an
// idle owner is reminded until it advances or reorders the frontier.
func TestWorkflowUpdateOneMap_FrontierNotificationRepeatsUntilHandled(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 700)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-struct"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-struct", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-struct", Title: "task-A", Question: "Q1"})

	// Owner only (idle); no workers.
	ownerRef := &controllableOwnerRef{actorID: ownerAID, workflowMapID: "wf-struct"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-struct"}},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-struct")

	// Tick 1: frontier non-empty, owner idle → notification dispatched.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	waitForChatSubmitCalls(t, ownerRef, 1)

	// Tick 2: unchanged frontier and idle owner → re-dispatch.
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	waitForChatSubmitCalls(t, ownerRef, 2)

	// Tick 3: owner busy → notification skipped.
	ownerRef.busy = true
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	if ownerRef.chatSubmitCount() != 2 {
		t.Fatalf("tick 3 (owner busy): notification must not dispatch, got %d", ownerRef.chatSubmitCount())
	}

	// Tick 4: owner idle again → notification re-fires.
	ownerRef.busy = false
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	waitForChatSubmitCalls(t, ownerRef, 3)
}

// TestWorkflowUpdateOneMap_BackloggedOwnerNotUnbound is a regression for the
// 2026-08-19 incident: the owner agent's pipeline was backlogged, so updater
// chat_submit frames were accepted for delivery but never processed (the agent
// stayed idle/"completed" and never flipped to running). The old loop counted
// five nudges against the unconsumed message, unbound the map
// owner, and split the workflow state (card unowned, agent still in workflow
// mode, updater permanently silenced). The fix holds nudges until the owner
// completes a turn after the real message was dispatched.
func TestWorkflowUpdateOneMap_BackloggedOwnerNotUnbound(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	ownerCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 750)
	if err != nil {
		t.Fatalf("owner CID: %v", err)
	}
	ownerActorID := ownerCID.String()
	ownerAID := id.From(ownerCID)

	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-hold"})
	a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "wf-hold", OwnerActorID: ownerActorID})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "wf-hold", Title: "task-A", Question: "Q1"})

	ownerRef := &controllableOwnerRef{actorID: ownerAID, unconsumedNotify: true, workflowMapID: "wf-hold"}
	wsRef := &mockWorkspaceRef{actorID: id.ActorID{}, agents: []gen.AgentRef{
		{ID: "owner", ActorID: ownerActorID, LoadState: "loaded", Mode: &gen.AgentModeState{ActiveWorkflowMapCardID: "wf-hold"}},
	}}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.LookupIDFn = func(a id.ActorID) (ref.Ref, bool) {
		if a == ownerAID {
			return ownerRef, true
		}
		return nil, false
	}

	mapCard, _ := a.store.Get("wf-hold")

	// Tick 1: the real message dispatches unconditionally (fresh history).
	a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	waitForChatSubmitCalls(t, ownerRef, 1)

	// Ticks 2-12: identical pending message never consumed — nudges are held,
	// the binding survives, and the jammed queue receives nothing further.
	for i := 0; i < 11; i++ {
		a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	}
	if got := ownerRef.chatSubmitCount(); got != 1 {
		t.Fatalf("backlogged owner must not be nudged: chat_submit calls = %d, want 1", got)
	}
	card, err := a.store.Get("wf-hold")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if owner, _ := card.Data["ownerAgentId"].(string); owner != ownerActorID {
		t.Fatalf("ownerAgentId = %q, want %q (binding must survive an unconsumed notification)", owner, ownerActorID)
	}

	// Owner finally consumes (LastTurnCompletedAt fresh again): nudges resume
	// and, with the frontier never advancing, escalate to unbind as designed.
	ownerRef.unconsumedNotify = false
	for i := 0; i < 8; i++ {
		a.workflowUpdateOneMap(fctx, mapCard, wfTickAgents{})
	}
	card, err = a.store.Get("wf-hold")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if owner, _ := card.Data["ownerAgentId"].(string); owner != "" {
		t.Fatalf("ownerAgentId = %q after consumed nudges, want cleared (unbind should escalate)", owner)
	}
}

// ── notify dedup simulation tests ──

func TestOwnerConsumedSince(t *testing.T) {
	now := time.Now().UTC()
	stamp := func(t time.Time) string { return t.Format(time.RFC3339Nano) }

	if ownerConsumedSince(nil, now) {
		t.Fatal("nil status must report unconsumed")
	}
	if ownerConsumedSince(&gen.AgentStatusResp{LastTurnCompletedAt: stamp(now)}, time.Time{}) {
		t.Fatal("zero dispatchedAt must report unconsumed")
	}
	if ownerConsumedSince(&gen.AgentStatusResp{LastTurnCompletedAt: ""}, now) {
		t.Fatal("empty LastTurnCompletedAt must report unconsumed")
	}
	if ownerConsumedSince(&gen.AgentStatusResp{LastTurnCompletedAt: "not-a-timestamp"}, now) {
		t.Fatal("unparseable LastTurnCompletedAt must report unconsumed")
	}
	if ownerConsumedSince(&gen.AgentStatusResp{LastTurnCompletedAt: stamp(now.Add(-time.Hour))}, now) {
		t.Fatal("turn completed before dispatch must report unconsumed")
	}
	if !ownerConsumedSince(&gen.AgentStatusResp{LastTurnCompletedAt: stamp(now)}, now.Add(-time.Minute)) {
		t.Fatal("turn completed after dispatch must report consumed")
	}
}

// simulateNotifyLoop runs N ticks of the dedup loop against the given message,
// recording what would have been sent. Returns the list of sent messages
// and whether an unbind was triggered.
func simulateNotifyLoop(message string, ticks int) (sent []string, unbound bool) {
	var st wfDedupState
	const hint = "review 2 ready for review"
	for i := 0; i < ticks; i++ {
		action, send := decideNotifyFromCard(message, &st, maxContinueNotifications, hint)
		if action == wfNotifyUnbind {
			return sent, true
		}
		// Simulate successful delivery
		if isNudge(send) {
			st.NudgeCount++
		} else {
			markSendApplied(&st, message, time.Now())
		}
		sent = append(sent, send)
	}
	return sent, false
}

func TestNotifyDedup(t *testing.T) {
	t.Run("same_message_5_continues_then_unbind", func(t *testing.T) {
		// After the initial send, 5 consecutive nudges are sent
		// (ticks 2-6). On tick 7, decideNotify sees trailing=5 >= maxContinueNotifications
		// and returns unbind. The sent list has 6 entries (1 send + 5 nudges);
		// the unbind tick itself does not send.
		sent, unbound := simulateNotifyLoop("A", 7)
		nudgeA := makeNudge("review 2 ready for review")
		want := []string{"A", nudgeA, nudgeA, nudgeA, nudgeA, nudgeA}
		if len(sent) != len(want) {
			t.Fatalf("simulateNotifyLoop sent %d items, want %d\nsent: %v\nwant: %v", len(sent), len(want), sent, want)
		}
		for i := range want {
			if sent[i] != want[i] {
				t.Fatalf("sent[%d] = %q, want %q\nsent: %v\nwant: %v", i, sent[i], want[i], sent, want)
			}
		}
		if !unbound {
			t.Fatal("simulateNotifyLoop should have unbound after 7 ticks")
		}
	})

	t.Run("different_message_resets", func(t *testing.T) {
		// Different messages don't accumulate continues — each is sent fresh.
		var st wfDedupState
		var sent []string
		const hint = "review 2 ready for review"

		// Tick 1: message "A" → Send "A".
		action, send := decideNotifyFromCard("A", &st, maxContinueNotifications, hint)
		if action == wfNotifyUnbind {
			t.Fatal("unexpected unbind on first tick")
		}
		markSendApplied(&st, "A", time.Now())
		sent = append(sent, send)

		// Tick 2: message "B" → Send "B" (not a nudge).
		action, send = decideNotifyFromCard("B", &st, maxContinueNotifications, hint)
		if action == wfNotifyUnbind {
			t.Fatal("unexpected unbind on second tick")
		}
		markSendApplied(&st, "B", time.Now())
		sent = append(sent, send)

		if len(sent) != 2 || sent[0] != "A" || sent[1] != "B" {
			t.Fatalf("expected [A B], got %v", sent)
		}
	})

	t.Run("message_changes_mid_loop", func(t *testing.T) {
		// "A" sent, then "B" sent, then "B" repeated — continues restart for B.
		var st wfDedupState
		var sent []string
		const hint = "review 2 ready for review"
		ticks := []string{"A", "B", "B", "B", "B"}
		for _, msg := range ticks {
			action, send := decideNotifyFromCard(msg, &st, maxContinueNotifications, hint)
			if action == wfNotifyUnbind {
				t.Fatal("unexpected unbind within 5 ticks")
			}
			if isNudge(send) {
				st.NudgeCount++
			} else {
				markSendApplied(&st, msg, time.Now())
			}
			sent = append(sent, send)
		}
		// After "A" (send), "B" (send), "B" (continue), "B" (continue), "B" (continue).
		if len(sent) != 5 {
			t.Fatalf("expected 5 sends, got %d: %v", len(sent), sent)
		}
		if sent[0] != "A" || sent[1] != "B" {
			t.Fatalf("first two should be A, B: got %v", sent[:2])
		}
		for i := 2; i < 5; i++ {
			if !isNudge(sent[i]) {
				t.Fatalf("sent[%d] = %q, want a nudge", i, sent[i])
			}
		}
		// The "A" in the history does not contribute to B's continue count.
		// After only 3 continues for B (below maxContinueNotifications=5),
		// unbind was not triggered.
	})

	t.Run("never_reaches_unbind", func(t *testing.T) {
		sent, unbound := simulateNotifyLoop("A", 3)
		nudgeA := makeNudge("review 2 ready for review")
		want := []string{"A", nudgeA, nudgeA}
		if len(sent) != len(want) {
			t.Fatalf("simulateNotifyLoop sent %d items, want %d\nsent: %v\nwant: %v", len(sent), len(want), sent, want)
		}
		for i := range want {
			if sent[i] != want[i] {
				t.Fatalf("sent[%d] = %q, want %q", i, sent[i], want[i])
			}
		}
		if unbound {
			t.Fatal("3 ticks should not trigger unbind")
		}
	})

	t.Run("exact_unbind_threshold", func(t *testing.T) {
		// 7 ticks: tick 1 sends "A", ticks 2-6 send nudges (5 nudges),
		// tick 7 detects trailing=5 >= maxContinueNotifications → unbind.
		// The sent list has 6 entries (the unbind tick does not send).
		sent, unbound := simulateNotifyLoop("A", 7)
		if len(sent) != 6 {
			t.Fatalf("expected 6 sent entries, got %d: %v", len(sent), sent)
		}
		if !unbound {
			t.Fatal("should have unbound after 7 ticks with same message")
		}
		if sent[0] != "A" {
			t.Fatalf("first entry should be 'A', got %q", sent[0])
		}
		for i := 1; i < len(sent); i++ {
			if !isNudge(sent[i]) {
				t.Fatalf("sent[%d] = %q, want a nudge", i, sent[i])
			}
		}
	})

}

// TestTimerCheck_SchedulesNextTick verifies that handleTimerCheck, now a
// PureContext handler, can still call ctx.After to reschedule itself. This is
// the callback the workflow updater depends on for periodic ticks.
func TestTimerCheck_SchedulesNextTick(t *testing.T) {
	a := newTestActor(t.TempDir())
	fctx := testutil.AdminCtx(testutil.GenActorID())
	var got struct {
		delay   time.Duration
		callID  string
		payload any
	}
	fctx.AfterFn = func(delay time.Duration, callID string, payload any) error {
		got.delay = delay
		got.callID = callID
		got.payload = payload
		return nil
	}

	if err := a.handleTimerCheck(fctx); err != nil {
		t.Fatalf("handleTimerCheck: %v", err)
	}
	if got.callID != "project.timer_check" {
		t.Fatalf("ctx.After callID = %q, want %q", got.callID, "project.timer_check")
	}
	if got.delay != monitorInterval {
		t.Fatalf("ctx.After delay = %v, want %v", got.delay, monitorInterval)
	}
}

// TestWakeMapOwner_NoDeadlockOnProjectOccupied verifies that wakeMapOwner
// does not deadlock when the project loop is occupied — the exact scenario
// that caused the project↔workspace owner-loop deadlock. The old code
// synchronously called workspace.load_agent which called back to
// project.spawn_agent, creating a mutual wait on the project loop. The fix
// spawns the agent locally on the project loop and fire-and-forget notifies
// workspace, eliminating the back-edge entirely.
func TestWakeMapOwner_NoDeadlockOnProjectOccupied(t *testing.T) {
	a, ctx := freshProject(t, t.TempDir())

	// seed the actor ID so the project can identify itself
	a.actorID = "proj-1"

	agentID := "agent-1"
	actorID := testutil.GenActorID().String()

	// mock workspace ref: handles list_agents to return the target agent,
	// and records agent_loaded calls (must be fire-and-forget, never Final).
	agentLoadedCalled := make(chan struct{}, 1)
	wsRef := &wakeMapOwnerWorkspaceRef{
		agents: []gen.AgentRef{{
			ID:          agentID,
			ActorID:     actorID,
			AgentKind:   "coder",
			DisplayName: "Coder Agent",
			ProjectID:   "proj-1",
			LoadState:   "unloaded",
		}},
		onAgentLoaded: func() {
			select {
			case agentLoadedCalled <- struct{}{}:
			default:
			}
		},
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}

	// mock SpawnFn: handleSpawnAgent calls ctx.Spawn; we return a fake ref
	// so the spawn "succeeds" without actually starting an actor.
	spawnedRef := testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any {
		return nil
	})
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return spawnedRef, nil
	}

	// Simulate the deadlock scenario: call wakeMapOwner while the project
	// loop is "occupied" (i.e., the call is inline). The old code would
	// deadlock because workspace.load_agent → spawnAgentViaProject →
	// project.spawn_agent would wait for the occupied project loop.
	// The new code spawns locally and returns immediately.
	done := make(chan struct{})
	go func() {
		a.wakeMapOwner(ctx, "map-1", actorID, &wfDedupState{}, wfTickAgents{})
		close(done)
	}()

	select {
	case <-done:
		// wakeMapOwner completed without deadlock — the fix works.
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: wakeMapOwner did not return within 5s (project loop occupied by workspace callback)")
	}

	// Verify that agent_loaded was fired (fire-and-forget notification).
	select {
	case <-agentLoadedCalled:
		// notification sent — workspace will update its registry
	case <-time.After(time.Second):
		t.Fatal("agent_loaded notification was not sent within 1s")
	}
}

// wakeMapOwnerWorkspaceRef is a mock ref that responds to workspace.list_agents
// and records workspace.agent_loaded calls for the deadlock test.
type wakeMapOwnerWorkspaceRef struct {
	agents        []gen.AgentRef
	onAgentLoaded func()
}

func (r *wakeMapOwnerWorkspaceRef) ID() id.ActorID          { return id.ActorID{} }
func (r *wakeMapOwnerWorkspaceRef) Service() (string, bool) { return "workspace", true }
func (r *wakeMapOwnerWorkspaceRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	switch callID {
	case "workspace.list_agents":
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentRefListResp{Items: r.agents}})
	case "workspace.agent_loaded":
		if r.onAgentLoaded != nil {
			r.onAgentLoaded()
		}
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.WorkspaceAgentLoadedResp{}})
	}
	return nil
}

func TestOrphanPendingReviewCards(t *testing.T) {
	cards := map[string]*CardRecord{
		"t-orphan": {Status: "pending_review"},
		"t-live":   {Status: "pending_review"},
		"t-done":   {Status: "done"},
	}
	get := func(id string) (*CardRecord, error) {
		if c, ok := cards[id]; ok {
			return c, nil
		}
		return nil, errors.New("missing")
	}
	events := orphanPendingReviewCards(
		[]string{"t-orphan", "t-live", "t-done", "t-missing"},
		map[string]bool{"t-live": true},
		get,
	)
	if len(events) != 1 || events[0].TaskCardID != "t-orphan" || events[0].EventType != "orphaned_card" {
		t.Fatalf("expected only t-orphan orphaned_card, got %+v", events)
	}
}

func TestFormatWorkerEventSummary_OrphanedCard(t *testing.T) {
	msg := formatWorkerEventSummary([]wfWorkerEvent{
		{TaskCardID: "T4 card", EventType: "orphaned_card"},
	})
	if !strings.Contains(msg, "- Task card T4 card: orphaned review card") {
		t.Fatalf("expected orphaned-card line, got: %s", msg)
	}
	if !strings.Contains(msg, "workspace.agent_review cannot dispose it") {
		t.Fatalf("expected re-dispose guidance, got: %s", msg)
	}
	if strings.Contains(msg, "Agent :") {
		t.Fatalf("summary must not render an empty agent subject, got: %s", msg)
	}
}
