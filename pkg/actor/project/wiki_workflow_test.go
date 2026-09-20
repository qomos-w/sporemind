package project

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestSetCardStatusInRaw(t *testing.T) {
	// Replaces existing status.
	raw := "---\nid: t1\ntype: task\nstatus: todo\n---\nbody"
	got := setCardStatusInRaw(raw, "done")
	if !containsLine(got, "status: done") || containsLine(got, "status: todo") {
		t.Errorf("status not replaced:\n%s", got)
	}

	// Inserts status when missing.
	raw2 := "---\nid: t2\ntype: task\n---\nbody"
	got2 := setCardStatusInRaw(raw2, "doing")
	if !containsLine(got2, "status: doing") {
		t.Errorf("status not inserted:\n%s", got2)
	}

	// No frontmatter: prepends.
	raw3 := "Just body."
	got3 := setCardStatusInRaw(raw3, "backlog")
	if !containsLine(got3, "status: backlog") {
		t.Errorf("status not prepended:\n%s", got3)
	}
}

func containsLine(s, line string) bool {
	for _, l := range splitLines(s) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func TestHandleWikiSetStatus(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: set-test\ntype: task\ntags: []\nstatus: todo\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "set-test", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	resp, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "set-test", Status: "doing"})
	if err != nil {
		t.Fatalf("set_status doing: %v", err)
	}
	if resp.Card.Status != "doing" {
		t.Errorf("status = %q, want doing", resp.Card.Status)
	}

	resp, err = a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "set-test", Status: "pending_review"})
	if err != nil {
		t.Fatalf("set_status pending_review: %v", err)
	}
	if resp.Card.Status != "pending_review" {
		t.Errorf("status = %q, want pending_review", resp.Card.Status)
	}

	resp, _ = a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "set-test", Status: "done"})
	if resp.Card.Status != "done" {
		t.Errorf("status = %q, want done", resp.Card.Status)
	}

	// Verify persistence on disk.
	card, err := a.store.Get("set-test")
	if err != nil {
		t.Fatalf("get after set: %v", err)
	}
	if card.Status != "done" {
		t.Errorf("persisted status = %q, want done", card.Status)
	}
}

// TestHandleWikiSetStatus_DoneClearsReviewChangeset verifies that when a task
// card transitions to "done" via wiki_set_status (bypassing reviewApprove),
// any frozen reviewChangeset snapshot is cleared so the card data is
// consistent with its terminal status.
func TestHandleWikiSetStatus_DoneClearsReviewChangeset(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Create a parent workflow map and a task card parented to it.
	mapRaw := "---\nid: rc-map\ntype: workflow\nstatus: doing\ndata:\n  scope:\n    include:\n      - rc-task---\n\nbody"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "rc-map", Raw: mapRaw}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	taskRaw := "---\nid: rc-task\ntype: task\ntags: [rc-map]\nstatus: pending_review\nparent: rc-map\ndata:\n  reviewChangeset: '{\"status\":\"ready\",\"frozenAt\":\"2026-01-01T00:00:00Z\",\"head\":\"abc123\",\"baseline\":\"def456\"}'---\n\nbody"
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "rc-task", Raw: taskRaw}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Set the task to done — should clear the reviewChangeset.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "rc-task", Status: "done"}); err != nil {
		t.Fatalf("set done: %v", err)
	}

	card, err := a.store.Get("rc-task")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if card.Status != "done" {
		t.Fatalf("status = %q, want done", card.Status)
	}
	if _, ok := readReviewChangesetCardData(card); ok {
		t.Fatalf("reviewChangeset should be cleared after done, but still present")
	}
}

func TestHandleWikiSetStatusPersistsReviewEvidence(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	create := func(id string) {
		t.Helper()
		raw := "---\nid: " + id + "\ntype: task\ntags: []\nstatus: todo\n---\n\nBody."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	evidenceJSON := func(id string) (any, bool) {
		t.Helper()
		card, err := a.store.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		value, ok := card.Data["review_evidence"]
		return value, ok
	}

	// Evidence provided → written into the data block as JSON.
	create("evidence-yes")
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{
		ID: "evidence-yes", Status: "pending_review",
		Evidence: []string{"commit abc123", "test/pkg/actor/project/wiki_workflow_test.go"},
	}); err != nil {
		t.Fatalf("set_status with evidence: %v", err)
	}
	value, ok := evidenceJSON("evidence-yes")
	if !ok {
		t.Fatal("review_evidence missing from card data when evidence provided")
	}
	if value != `["commit abc123","test/pkg/actor/project/wiki_workflow_test.go"]` {
		t.Errorf("review_evidence = %v, want JSON array of evidence items", value)
	}

	// No evidence → no review_evidence key is added.
	create("evidence-no")
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "evidence-no", Status: "pending_review"}); err != nil {
		t.Fatalf("set_status without evidence: %v", err)
	}
	if _, ok := evidenceJSON("evidence-no"); ok {
		t.Error("review_evidence present in card data though no evidence was provided")
	}

	// Empty slice behaves like absence.
	create("evidence-empty")
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "evidence-empty", Status: "pending_review", Evidence: []string{}}); err != nil {
		t.Fatalf("set_status with empty evidence: %v", err)
	}
	if _, ok := evidenceJSON("evidence-empty"); ok {
		t.Error("review_evidence present in card data though evidence slice was empty")
	}
}

func TestHandleWikiFrontier(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Root map card with Data.include = [ticketA, ticketB, ticketC].
	rootRaw := "---\nid: wf-root\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - ticketA\n    - ticketB\n    - ticketC\n---\n\nRoot map."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "wf-root", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	// Three task cards, all backlog initially; C directly records its prerequisites.
	for _, id := range []string{"ticketA", "ticketB", "ticketC"} {
		data := ""
		if id == "ticketC" {
			data = "data:\n  depends_on:\n    - ticketA\n    - ticketB\n"
		}
		raw := "---\nid: " + id + "\ntype: task\ntags: [wf-root]\nstatus: backlog\nparent: wf-root\n" + data + "---\n\nTicket."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// Initial frontier: A and B (no deps), C blocked.
	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "wf-root"})
	if err != nil {
		t.Fatalf("frontier 1: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"ticketA", "ticketB"})

	// A done → frontier: B only.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ticketA", Status: "done"}); err != nil {
		t.Fatalf("set A done: %v", err)
	}
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "wf-root"})
	assertFrontierIDs(t, resp.TaskCards, []string{"ticketB"})

	// B done → frontier: C (deps cleared).
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ticketB", Status: "done"}); err != nil {
		t.Fatalf("set B done: %v", err)
	}
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "wf-root"})
	assertFrontierIDs(t, resp.TaskCards, []string{"ticketC"})

	// C claimed (doing) → frontier empty.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ticketC", Status: "doing"}); err != nil {
		t.Fatalf("set C doing: %v", err)
	}
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "wf-root"})
	assertFrontierIDs(t, resp.TaskCards, nil)

	// C done → frontier empty.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ticketC", Status: "done"}); err != nil {
		t.Fatalf("set C done: %v", err)
	}
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "wf-root"})
	assertFrontierIDs(t, resp.TaskCards, nil)
}

func TestScopeIncludeNestedParsing(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: nested-root\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  scope:\n    include:\n      - na\n      - nb\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "nested-root", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for _, id := range []string{"na", "nb"} {
		raw := "---\nid: " + id + "\ntype: task\ntags: [nested-root]\nstatus: backlog\nparent: nested-root\n---\n\nTicket."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "nested-root"})
	if err != nil {
		t.Fatalf("frontier: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"na", "nb"})
}

// Regression: a map whose data.scope.include survived (or was written in) the
// inline flow-style form `include: [a, b]` must still produce a frontier.
// parseDataBlock stores such values as plain strings; toStringSlice must
// accept that form.
func TestScopeIncludeInlineParsing(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: inline-root\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  scope:\n    include: [ia, ib]\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "inline-root", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for _, id := range []string{"ia", "ib"} {
		raw := "---\nid: " + id + "\ntype: task\ntags: [inline-root]\nstatus: backlog\nparent: inline-root\n---\n\nTicket."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "inline-root"})
	if err != nil {
		t.Fatalf("frontier: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"ia", "ib"})
}

// TestCreateCardUnderWorkflowMapHealsMembership is the regression test for the
// novelking incident: an agent creates task cards through the generic
// wiki_create_card with parent pointing at a workflow map instead of using
// wiki_create_task_card. The create path must heal scope membership
// immediately (include append + topo node from the card's own
// status/tags/data.depends_on), and the frontier must respect the healed
// dependencies.
func TestCreateCardUnderWorkflowMapHealsMembership(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "heal-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	for _, tc := range []struct{ id, raw string }{
		{"heal-a", "---\nid: heal-a\ntype: task\ntags: [heal-map]\nstatus: backlog\nparent: heal-map\n---\n\nA."},
		{"heal-b", "---\nid: heal-b\ntype: task\ntags: [heal-map]\nstatus: backlog\nparent: heal-map\ndata:\n  depends_on:\n    - heal-a\n---\n\nB."},
	} {
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: tc.id, Raw: tc.raw}); err != nil {
			t.Fatalf("create %s: %v", tc.id, err)
		}
	}

	// Membership healed at create time: both ids in the map's include list.
	mapCard, err := a.store.Get("heal-map")
	if err != nil {
		t.Fatalf("get map: %v", err)
	}
	include := scopeIncludeIDs(mapCard)
	if len(include) != 2 {
		t.Fatalf("include = %v, want [heal-a heal-b]", include)
	}

	// Frontier respects the healed edges: only heal-a is unblocked.
	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "heal-map"})
	if err != nil {
		t.Fatalf("frontier 1: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"heal-a"})

	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "heal-a", Status: "done"}); err != nil {
		t.Fatalf("set heal-a done: %v", err)
	}
	resp, err = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "heal-map"})
	if err != nil {
		t.Fatalf("frontier 2: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"heal-b"})
}

// TestReconcileAdoptsLegacyOrphanTaskCards covers orphans that predate the
// create-path heal (created by an older binary or seeded by a direct store
// write, bypassing every handler): the first frontier/claim/graph read must
// adopt them into scope.include and compute the frontier over them. Adoption
// must be idempotent — repeated reads never duplicate include entries.
func TestReconcileAdoptsLegacyOrphanTaskCards(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "adopt-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	// Seed orphans directly through the store — no handler side effects.
	orphans := map[string]string{
		"orphan-a": "---\nid: orphan-a\ntype: task\ntags: [adopt-map]\nstatus: backlog\nparent: adopt-map\n---\n\nA.",
		"orphan-b": "---\nid: orphan-b\ntype: task\ntags: [adopt-map]\nstatus: backlog\nparent: adopt-map\ndata:\n  depends_on:\n    - orphan-a\n---\n\nB.",
	}
	for id, raw := range orphans {
		if err := a.store.Save(&CardRecord{Title: id, Raw: raw}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	// Sanity: the map still has an empty include projection.
	if include := scopeIncludeIDs(mustCard(t, a, "adopt-map")); len(include) != 0 {
		t.Fatalf("precondition: include = %v, want empty", include)
	}

	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "adopt-map"})
	if err != nil {
		t.Fatalf("frontier 1: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"orphan-a"})

	// Adoption persisted the include projection.
	include := scopeIncludeIDs(mustCard(t, a, "adopt-map"))
	if len(include) != 2 {
		t.Fatalf("adopted include = %v, want 2 ids", include)
	}

	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "orphan-a", Status: "done"}); err != nil {
		t.Fatalf("set orphan-a done: %v", err)
	}
	resp, err = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "adopt-map"})
	if err != nil {
		t.Fatalf("frontier 2: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"orphan-b"})

	// Idempotency: further reads never grow the include list.
	resp, err = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "adopt-map"})
	if err != nil {
		t.Fatalf("frontier 3: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"orphan-b"})
	if include := scopeIncludeIDs(mustCard(t, a, "adopt-map")); len(include) != 2 {
		t.Fatalf("include grew on repeated reads: %v", include)
	}
}

// TestAdoptionIgnoresNonWorkflowParent pins the adoption predicate: only
// cards whose Parent resolves to an existing workflow map are adopted. Task
// cards under plan/wiki parents (a legitimate pattern for plan task lists)
// and cards with a dangling literal parent (the novelking `parent: workflow`
// shape) must stay untouched — no error, no scope mutation.
func TestAdoptionIgnoresNonWorkflowParent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	planRaw := "---\nid: some-plan\ntype: plan\ntags: []\nstatus: doing\n---\n\nPlan."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "some-plan", Raw: planRaw}); err != nil {
		t.Fatalf("create plan: %v", err)
	}
	planTaskRaw := "---\nid: plan-task\ntype: task\ntags: [some-plan]\nstatus: backlog\nparent: some-plan\n---\n\nT."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "plan-task", Raw: planTaskRaw}); err != nil {
		t.Fatalf("create plan-task: %v", err)
	}
	// The novelking shape: literal parent "workflow" that names no card.
	danglingRaw := "---\nid: dangling-task\ntype: task\ntags: [workflow]\nstatus: todo\nparent: workflow\n---\n\nT."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "dangling-task", Raw: danglingRaw}); err != nil {
		t.Fatalf("create dangling-task: %v", err)
	}

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "lonely-map"}); err != nil {
		t.Fatalf("create map: %v", err)
	}
	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "lonely-map"})
	if err != nil {
		t.Fatalf("frontier: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, nil)
	if include := scopeIncludeIDs(mustCard(t, a, "lonely-map")); len(include) != 0 {
		t.Fatalf("lonely-map include = %v, want empty (nothing adopted)", include)
	}
}

// TestConcurrentGenericCreateUnderMapNoTruncation mirrors
// TestCreateTaskCardConcurrentNoIncludeTruncation for the generic create path:
// N concurrent wiki_create_card calls under one workflow map each heal the
// include list; the serialized appends must not last-writer-win, and the
// reconciling adoption pass must not truncate or duplicate entries.
func TestConcurrentGenericCreateUnderMapNoTruncation(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "generic-race-map"})

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("gtask-%02d", i)
			raw := "---\nid: " + id + "\ntype: task\ntags: [generic-race-map]\nstatus: backlog\nparent: generic-race-map\n---\n\nT."
			_, errs[i] = a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	// A frontier read reconciles and adopts anything the create-time heal
	// missed; afterwards the include list must carry every id exactly once.
	if _, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "generic-race-map"}); err != nil {
		t.Fatalf("frontier: %v", err)
	}
	got := scopeIncludeIDs(mustCard(t, a, "generic-race-map"))
	if len(got) != n {
		t.Fatalf("include length = %d, want %d; include=%v", len(got), n, got)
	}
	seen := make(map[string]bool, n)
	for _, id := range got {
		if seen[id] {
			t.Fatalf("duplicate id %q in include: %v", id, got)
		}
		seen[id] = true
	}
	for i := 0; i < n; i++ {
		if id := fmt.Sprintf("gtask-%02d", i); !seen[id] {
			t.Errorf("missing %q in include: %v", id, got)
		}
	}
}

func assertFrontierIDs(t *testing.T, tickets []gen.FrontierTaskCard, want []string) {
	t.Helper()
	if len(tickets) != len(want) {
		var got []string
		for _, tk := range tickets {
			got = append(got, tk.ID)
		}
		t.Errorf("frontier = %v, want %v", got, want)
		return
	}
	gotSet := make(map[string]bool, len(tickets))
	for _, tk := range tickets {
		gotSet[tk.ID] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Errorf("frontier missing %q in %v", w, tickets)
		}
	}
}

func TestHandleWikiSetStatusCAS(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: cas-test\ntype: task\ntags: []\nstatus: backlog\n---\n\nBody."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "cas-test", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// CAS match: backlog → doing succeeds.
	resp, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "cas-test", Status: "doing", ExpectedStatus: "backlog"})
	if err != nil {
		t.Fatalf("CAS match failed: %v", err)
	}
	if resp.PreviousStatus != "backlog" {
		t.Errorf("PreviousStatus = %q, want backlog", resp.PreviousStatus)
	}
	if resp.Card.Status != "doing" {
		t.Errorf("Card.Status = %q, want doing", resp.Card.Status)
	}

	// CAS mismatch: doing card, expected backlog → fails.
	_, err = a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "cas-test", Status: "done", ExpectedStatus: "backlog"})
	if err == nil {
		t.Fatal("CAS mismatch should fail, got nil")
	}

	// Card status unchanged after failed CAS.
	card, _ := a.store.Get("cas-test")
	if card.Status != "doing" {
		t.Errorf("status after failed CAS = %q, want doing", card.Status)
	}

	// No ExpectedStatus → unconditional (backward compat).
	_, err = a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "cas-test", Status: "done"})
	if err != nil {
		t.Fatalf("unconditional set failed: %v", err)
	}
}

func TestHandleWikiSetStatus_ClaimBlockedByDependencies(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// dep card starts backlog (not done).
	depRaw := "---\nid: dep-1\ntype: task\ntags: []\nstatus: backlog\n---\n\nDep."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "dep-1", Raw: depRaw}); err != nil {
		t.Fatalf("create dep: %v", err)
	}
	// task card depends on dep-1.
	taskRaw := "---\nid: dep-task\ntype: task\ntags: []\nstatus: backlog\ndata:\n  depends_on:\n    - dep-1\n---\n\nTask."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "dep-task", Raw: taskRaw}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Claim while dep backlog → blocked.
	_, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "dep-task", Status: "doing"})
	if err == nil {
		t.Fatal("claiming task with unmet dependency should fail")
	}
	if !strings.Contains(err.Error(), "dep-1") {
		t.Errorf("error should name the unmet dependency, got: %v", err)
	}
	// Status unchanged.
	card, _ := a.store.Get("dep-task")
	if card.Status != "backlog" {
		t.Errorf("status after blocked claim = %q, want backlog", card.Status)
	}

	// Complete the dependency.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "dep-1", Status: "done"}); err != nil {
		t.Fatalf("set dep done: %v", err)
	}

	// Now claim succeeds.
	resp, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "dep-task", Status: "doing"})
	if err != nil {
		t.Fatalf("claim after deps done should succeed: %v", err)
	}
	if resp.Card.Status != "doing" {
		t.Errorf("status = %q, want doing", resp.Card.Status)
	}
}

func TestHandleWikiClaimTaskCard(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	raw := "---\nid: claim-1\ntype: task\ntags: []\nstatus: backlog\n---\n\nImplement the widget."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "claim-1", Raw: raw}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// ExpectedStatuses is mandatory.
	if _, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{ID: "claim-1", Status: "doing"}); err == nil {
		t.Fatal("claim without ExpectedStatuses should fail")
	}

	// Successful claim: status flips, previous status and raw returned in one
	// round trip.
	resp, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID:               "claim-1",
		Status:           "doing",
		ExpectedStatuses: []string{"backlog", "todo"},
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if resp.PreviousStatus != "backlog" {
		t.Errorf("PreviousStatus = %q, want backlog", resp.PreviousStatus)
	}
	if resp.Card.Status != "doing" {
		t.Errorf("Card.Status = %q, want doing", resp.Card.Status)
	}
	if !strings.Contains(resp.Raw, "status: doing") || !strings.Contains(resp.Raw, "Implement the widget.") {
		t.Errorf("Raw should carry updated status and body, got:\n%s", resp.Raw)
	}
	card, _ := a.store.Get("claim-1")
	if card.Status != "doing" {
		t.Errorf("persisted status = %q, want doing", card.Status)
	}

	// Orphan reclaim: doing is claimable when listed in ExpectedStatuses.
	resp, err = a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID:               "claim-1",
		Status:           "doing",
		ExpectedStatuses: []string{"doing", "pending_review"},
	})
	if err != nil {
		t.Fatalf("orphan reclaim: %v", err)
	}
	if resp.PreviousStatus != "doing" {
		t.Errorf("PreviousStatus on reclaim = %q, want doing", resp.PreviousStatus)
	}

	// Status not in the expected set → CAS miss, status untouched.
	_, err = a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID:               "claim-1",
		Status:           "doing",
		ExpectedStatuses: []string{"backlog"},
	})
	if err == nil {
		t.Fatal("claim with mismatched expected set should fail")
	}
	card, _ = a.store.Get("claim-1")
	if card.Status != "doing" {
		t.Errorf("status after CAS miss = %q, want doing", card.Status)
	}
}

func TestHandleWikiClaimTaskCard_BlockedByDependencies(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	depRaw := "---\nid: cdep-1\ntype: task\ntags: []\nstatus: backlog\n---\n\nDep."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "cdep-1", Raw: depRaw}); err != nil {
		t.Fatalf("create dep: %v", err)
	}
	taskRaw := "---\nid: cdep-task\ntype: task\ntags: []\nstatus: backlog\ndata:\n  depends_on:\n    - cdep-1\n---\n\nTask."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "cdep-task", Raw: taskRaw}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID:               "cdep-task",
		Status:           "doing",
		ExpectedStatuses: []string{"backlog", "todo"},
	})
	if err == nil {
		t.Fatal("claiming task with unmet dependency should fail")
	}
	if !strings.Contains(err.Error(), "dependencies not done") {
		t.Errorf("error must keep the 'dependencies not done' phrasing (workspace string-matches it), got: %v", err)
	}
	card, _ := a.store.Get("cdep-task")
	if card.Status != "backlog" {
		t.Errorf("status after blocked claim = %q, want backlog", card.Status)
	}

	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "cdep-1", Status: "done"}); err != nil {
		t.Fatalf("set dep done: %v", err)
	}
	if _, err := a.handleWikiClaimTaskCard(ctx, domain.WikiClaimTaskCardReq{
		ID:               "cdep-task",
		Status:           "doing",
		ExpectedStatuses: []string{"backlog", "todo"},
	}); err != nil {
		t.Fatalf("claim after deps done should succeed: %v", err)
	}
}

func TestRootMapAutoDoneWhenAllTasksDone(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: root-auto\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - ta\n    - tb\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "root-auto", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for _, id := range []string{"ta", "tb"} {
		raw := "---\nid: " + id + "\ntype: task\ntags: [root-auto]\nstatus: doing\nparent: root-auto\n---\n\nTicket."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// ta done → root still doing (tb pending).
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ta", Status: "done"})
	root, _ := a.store.Get("root-auto")
	if root.Status != "doing" {
		t.Fatalf("root status after 1/2 done = %q, want doing", root.Status)
	}

	// tb done → all task cards done → root auto-done (ownerless map).
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "tb", Status: "done"})
	root, _ = a.store.Get("root-auto")
	if root.Status != "done" {
		t.Errorf("root status after 2/2 done = %q, want done", root.Status)
	}
}

// TestRootMapNotAutoDoneWhileOwnerBound verifies the unified completion rule:
// while the workflow card has a bound owner, all task cards done does NOT
// auto-complete the root — the owner stays in-progress and must finish via
// workflow_stop. The updater's tree-exhausted notification relies on the root
// staying non-done to keep urging the owner.
func TestRootMapNotAutoDoneWhileOwnerBound(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: root-owned\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - oa\n    - ob\n  ownerAgentId: owner-1\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "root-owned", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for _, id := range []string{"oa", "ob"} {
		raw := "---\nid: " + id + "\ntype: task\ntags: [root-owned]\nstatus: doing\nparent: root-owned\n---\n\nTicket."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "oa", Status: "done"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "ob", Status: "done"})
	root, _ := a.store.Get("root-owned")
	if root.Status != "doing" {
		t.Errorf("root with bound owner must stay doing (workflow_stop completes it), got %q", root.Status)
	}

	// workflow_stop's wiki_set_status done is the owner-driven completion
	// path and must still work even with ownerAgentId set.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "root-owned", Status: "done"}); err != nil {
		t.Fatalf("explicit root done: %v", err)
	}
	root, _ = a.store.Get("root-owned")
	if root.Status != "done" {
		t.Errorf("explicit root done should persist, got %q", root.Status)
	}
}

// TestWorkflowEndToEndLifecycle exercises the full deterministic workflow
// lifecycle: chart (create root + task cards + deps + bind owner) → work
// (claim → pending_review → approve via set_status, frontier recomputed) →
// completion (root stays doing while the owner is bound; workflow_stop's
// explicit done settles it). Also verifies updaterDecide at each phase
// transition to ensure all notification states are correct.
func TestRootMapAutoFailedWhenOwnerDies(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: root-dead-owner\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - td\n  ownerAgentId: owner-dead\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "root-dead-owner", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	taskRaw := "---\nid: td\ntype: task\ntags: [root-dead-owner]\nstatus: doing\nparent: root-dead-owner\n---\n\nTicket."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "td", Raw: taskRaw}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Simulate owner agent deletion: unbind_agent is called by the workspace.
	if err := a.handleWikiUnbindAgent(ctx, gen.ProjectWikiUnbindAgentReq{AgentActorID: "owner-dead"}); err != nil {
		t.Fatalf("unbind_agent: %v", err)
	}

	root, _ := a.store.Get("root-dead-owner")
	if root.Status != "failed" {
		t.Errorf("root status after owner death = %q, want failed", root.Status)
	}
	if _, ok := root.Data["ownerAgentId"]; ok && root.Data["ownerAgentId"].(string) != "" {
		t.Errorf("ownerAgentId should be cleared after unbind, got %q", root.Data["ownerAgentId"])
	}
}

func TestRootMapAutoDoneWhenOwnerDiesWithAllTasksDone(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: root-dead-done\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - te\n  ownerAgentId: owner-gone\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "root-dead-done", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	taskRaw := "---\nid: te\ntype: task\ntags: [root-dead-done]\nstatus: done\nparent: root-dead-done\n---\n\nTicket."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "te", Raw: taskRaw}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Owner dies but work is already complete -> root done, not failed.
	if err := a.handleWikiUnbindAgent(ctx, gen.ProjectWikiUnbindAgentReq{AgentActorID: "owner-gone"}); err != nil {
		t.Fatalf("unbind_agent: %v", err)
	}

	root, _ := a.store.Get("root-dead-done")
	if root.Status != "done" {
		t.Errorf("root status after owner death with all tasks done = %q, want done", root.Status)
	}
}

func TestWorkflowEndToEndLifecycle(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// --- Chart phase ---

	// Root map card with ownerAgentId binding + Data.include.
	rootRaw := "---\nid: e2e-root\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - e2e-A\n    - e2e-B\n    - e2e-C\n---\n\nE2E Root."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "e2e-root", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	// Bind owner.
	if _, err := a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "e2e-root", OwnerActorID: "arch-e2e"}); err != nil {
		t.Fatalf("set_map_owner: %v", err)
	}

	// Create 3 task cards; C directly records its prerequisites.
	for _, id := range []string{"e2e-A", "e2e-B", "e2e-C"} {
		data := ""
		if id == "e2e-C" {
			data = "data:\n  depends_on:\n    - e2e-A\n    - e2e-B\n"
		}
		raw := fmt.Sprintf("---\nid: %s\ntype: task\ntags: [e2e-root]\nstatus: backlog\nparent: e2e-root\n%s---\n\nTicket.", id, data)
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	// --- Verify initial frontier: [A, B] ---
	resp, err := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	if err != nil {
		t.Fatalf("frontier initial: %v", err)
	}
	assertFrontierIDs(t, resp.TaskCards, []string{"e2e-A", "e2e-B"})

	// --- Updater: frontier non-empty, owner idle → notify ---
	d := updaterDecide(wfUpdaterInput{
		RootDone:       false,
		OwnerBusy:      false,
		Frontier:       []string{"e2e-A", "e2e-B"},
		HasActiveOwner: false,
	})
	if !d.Notify || !strings.Contains(d.Message, "frontier") {
		t.Fatalf("updater should notify frontier non-empty, got %+v", d)
	}

	// --- Work phase: task card A lifecycle ---
	// Claim A.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-A", Status: "doing"})
	// A is now doing (claimed) → not in frontier.
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	assertFrontierIDs(t, resp.TaskCards, []string{"e2e-B"})

	// A ready_for_review.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-A", Status: "pending_review"})
	// Updater collects ready_for_review as a worker event (no longer via updaterDecide).
	events := collectWorkerEvents([]wfOwnerState{{AgentID: "w-A", TaskCardID: "e2e-A", GoalStatus: "ready_for_review"}})
	if len(events) != 1 || events[0].EventType != "ready_for_review" {
		t.Fatalf("collectWorkerEvents should find ready_for_review, got %+v", events)
	}
	msg := formatWorkerEventSummary(events)
	if !strings.Contains(msg, "ready for review") {
		t.Fatalf("summary should mention ready for review, got %q", msg)
	}

	// Approve A → done.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-A", Status: "done"})

	// --- Work phase: task card B lifecycle (parallel with A was possible) ---
	// Claim B.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-B", Status: "doing"})
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	assertFrontierIDs(t, resp.TaskCards, nil) // C still blocked by B

	// B ready → approved → done.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-B", Status: "pending_review"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-B", Status: "done"})

	// --- C graduates into frontier ---
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	assertFrontierIDs(t, resp.TaskCards, []string{"e2e-C"})

	// C lifecycle: claim → ready → done.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-C", Status: "doing"})
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	assertFrontierIDs(t, resp.TaskCards, nil)

	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-C", Status: "pending_review"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-C", Status: "done"})

	// --- Completion: all task cards done → frontier empty ---
	resp, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "e2e-root"})
	assertFrontierIDs(t, resp.TaskCards, nil)

	// All task cards are done, but the owner ("arch-e2e") is still bound:
	// the root must stay doing — completion belongs to workflow_stop.
	root, _ := a.store.Get("e2e-root")
	if root.Status != "doing" {
		t.Errorf("root status after all tasks done with bound owner = %q, want doing", root.Status)
	}

	// The updater keeps urging the idle owner to call workflow_stop.
	d = updaterDecide(wfUpdaterInput{RootDone: false, OwnerBusy: false})
	if !d.Notify || !strings.Contains(d.Message, "workflow_stop") {
		t.Fatalf("updater should urge workflow_stop on an exhausted tree, got %+v", d)
	}

	// workflow_stop's explicit done write settles the map.
	if _, err := a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "e2e-root", Status: "done"}); err != nil {
		t.Fatalf("explicit root done: %v", err)
	}
	root, _ = a.store.Get("e2e-root")
	if root.Status != "done" {
		t.Errorf("root status after workflow_stop done = %q, want done", root.Status)
	}

	// Updater treats a done root as settled (no notification noise).
	d = updaterDecide(wfUpdaterInput{RootDone: true})
	if d.Notify {
		t.Fatalf("updater should not notify on a done root, got %+v", d)
	}
}

// TestWorkflowUpdaterAllStates verifies the updater decision function handles
// every structural notification state correctly and with correct priority.
// Worker events (ready/failed/dead) are tested separately via collectWorkerEvents.
func TestWorkflowUpdaterAllStates(t *testing.T) {
	tests := []struct {
		name  string
		input wfUpdaterInput
		want  string
	}{
		{"root done", wfUpdaterInput{RootDone: true}, ""},
		{"owner busy", wfUpdaterInput{OwnerBusy: true, Frontier: []string{"card-x"}}, ""},
		{"frontier non-empty", wfUpdaterInput{Frontier: []string{"card-x"}}, "frontier"},
		{"tree exhausted no owner", wfUpdaterInput{HasActiveOwner: false}, "Assess whether the overall goal is met"},
		{"worker active", wfUpdaterInput{HasActiveOwner: true}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := updaterDecide(tt.input)
			if tt.want == "" {
				if d.Notify {
					t.Errorf("expected no notification, got %+v", d)
				}
				return
			}
			if !d.Notify {
				t.Errorf("expected notification containing %q, got none", tt.want)
				return
			}
			if !strings.Contains(d.Message, tt.want) {
				t.Errorf("message = %q, want substring %q", d.Message, tt.want)
			}
		})
	}
}

func TestSetOwnerInDataBlock(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "insert into existing data block",
			raw:  "---\nid: m\ntype: workflow\ndata:\n  include:\n    - a\n---\n\nBody.",
			want: "ownerAgentId: agent-1",
		},
		{
			name: "replace existing owner",
			raw:  "---\nid: m\ntype: workflow\ndata:\n  ownerAgentId: old\n---\n\nBody.",
			want: "ownerAgentId: agent-1",
		},
		{
			name: "add data block when missing",
			raw:  "---\nid: m\ntype: workflow\n---\n\nBody.",
			want: "ownerAgentId: agent-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := setOwnerInDataBlock(tt.raw, "agent-1")
			if !strings.Contains(result, tt.want) {
				t.Errorf("result does not contain %q:\n%s", tt.want, result)
			}
			if strings.Contains(result, "ownerAgentId: old") {
				t.Error("old owner not replaced")
			}
		})
	}
}

func TestHandleWikiSetMapOwner(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	rootRaw := "---\nid: owner-root\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include:\n    - t1\n---\n\nRoot."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "owner-root", Raw: rootRaw}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	// Bind owner-1.
	resp, err := a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "owner-root", OwnerActorID: "agent-arch-1"})
	if err != nil {
		t.Fatalf("set_map_owner: %v", err)
	}
	if resp.PreviousOwnerActorID != "" {
		t.Errorf("PreviousOwnerActorID = %q, want empty", resp.PreviousOwnerActorID)
	}

	card, _ := a.store.Get("owner-root")
	owner, _ := card.Data["ownerAgentId"].(string)
	if owner != "agent-arch-1" {
		t.Errorf("ownerAgentId = %q, want agent-arch-1", owner)
	}

	// Re-bind same owner (idempotent).
	_, err = a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "owner-root", OwnerActorID: "agent-arch-1"})
	if err != nil {
		t.Fatalf("re-bind same owner should succeed: %v", err)
	}

	// Bind different owner → reject (single-binding).
	_, err = a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "owner-root", OwnerActorID: "agent-arch-2"})
	if err == nil {
		t.Fatal("binding a second owner should fail")
	}

	// Non-workflow/non-task card → reject.
	otherRaw := "---\nid: not-a-card\ntype: skill\ntags: []\nstatus: backlog\n---\n\nSkill."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "not-a-card", Raw: otherRaw})
	_, err = a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "not-a-card", OwnerActorID: "agent-arch-1"})
	if err == nil {
		t.Fatal("binding to a non-workflow/non-task card should fail")
	}

	// Task card → accepted (overwrite semantics, no single-binding).
	taskRaw := "---\nid: task-1\ntype: task\ntags: []\nstatus: backlog\n---\n\nTask."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "task-1", Raw: taskRaw})
	_, err = a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "task-1", OwnerActorID: "worker-1"})
	if err != nil {
		t.Fatalf("binding a worker to a task card should succeed: %v", err)
	}
	taskCard, _ := a.store.Get("task-1")
	taskOwner, _ := taskCard.Data["ownerAgentId"].(string)
	if taskOwner != "worker-1" {
		t.Errorf("task card ownerAgentId = %q, want worker-1", taskOwner)
	}

	// Task card overwrite: a second worker replaces the first (no
	// single-binding rejection — the CAS claim guards ownership).
	_, err = a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "task-1", OwnerActorID: "worker-2"})
	if err != nil {
		t.Fatalf("overwriting task card owner should succeed: %v", err)
	}
	taskCard2, _ := a.store.Get("task-1")
	taskOwner2, _ := taskCard2.Data["ownerAgentId"].(string)
	if taskOwner2 != "worker-2" {
		t.Errorf("task card ownerAgentId = %q, want worker-2", taskOwner2)
	}
}

func TestHandleWikiUnbindAgent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Two maps bound to the deleted agent, one bound to a different agent,
	// one unbound.
	mkMap := func(id string) {
		raw := "---\nid: " + id + "\ntype: workflow\ntags: []\nstatus: doing\ndata:\n  include: []\n---\n\nMap."
		if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: id, Raw: raw}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mkMap("map-a")
	mkMap("map-b")
	mkMap("map-other")
	mkMap("map-free")
	for _, id := range []string{"map-a", "map-b"} {
		if _, err := a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: id, OwnerActorID: "agent-dead"}); err != nil {
			t.Fatalf("bind %s: %v", id, err)
		}
	}
	if _, err := a.handleWikiSetMapOwner(ctx, domain.WikiSetMapOwnerReq{MapID: "map-other", OwnerActorID: "agent-alive"}); err != nil {
		t.Fatalf("bind map-other: %v", err)
	}

	// Empty id → error.
	if err := a.handleWikiUnbindAgent(ctx, gen.ProjectWikiUnbindAgentReq{}); err == nil {
		t.Fatal("empty AgentActorID should fail")
	}

	if err := a.handleWikiUnbindAgent(ctx, gen.ProjectWikiUnbindAgentReq{AgentActorID: "agent-dead"}); err != nil {
		t.Fatalf("unbind_agent: %v", err)
	}

	for _, id := range []string{"map-a", "map-b"} {
		card, err := a.store.Get(id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if owner, _ := card.Data["ownerAgentId"].(string); owner != "" {
			t.Errorf("%s ownerAgentId = %q, want cleared", id, owner)
		}
		if strings.Contains(card.Raw, "ownerAgentId") {
			t.Errorf("%s raw still mentions ownerAgentId:\n%s", id, card.Raw)
		}
	}
	card, _ := a.store.Get("map-other")
	if owner, _ := card.Data["ownerAgentId"].(string); owner != "agent-alive" {
		t.Errorf("map-other ownerAgentId = %q, want agent-alive (untouched)", owner)
	}

	// Idempotent: second call unbinds nothing and succeeds.
	if err := a.handleWikiUnbindAgent(ctx, gen.ProjectWikiUnbindAgentReq{AgentActorID: "agent-dead"}); err != nil {
		t.Fatalf("second unbind_agent: %v", err)
	}
}

func TestHandleWikiCreateMap(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	resp, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{
		ID:          "my-map",
		Destination: "A working auth module",
	})
	if err != nil {
		t.Fatalf("create_map: %v", err)
	}
	if resp.Card.Type != "workflow" {
		t.Errorf("type = %q, want workflow", resp.Card.Type)
	}

	card, err := a.store.Get("my-map")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if card.Type != "workflow" {
		t.Errorf("persisted type = %q, want workflow", card.Type)
	}
	if card.Status != "doing" {
		t.Errorf("status = %q, want doing", card.Status)
	}
	// Verify scope.include exists and is empty.
	scope, ok := card.Data["scope"].(map[string]any)
	if !ok {
		t.Fatalf("data.scope not found in: %v", card.Data)
	}
	include, ok := scope["include"]
	if !ok {
		t.Fatal("data.scope.include not found")
	}
	if len(toStringSlice(include)) != 0 {
		t.Errorf("include = %v, want empty", include)
	}

	// Duplicate id → reject.
	_, err = a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "my-map"})
	if err == nil {
		t.Fatal("duplicate create should fail")
	}
}

func TestHandleWikiCreateTaskCard(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "task-map"})

	resp, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "task-map",
		Title:    "task-A",
		Question: "How should sessions be stored?",
	})
	if err != nil {
		t.Fatalf("create_task_card: %v", err)
	}
	if resp.Card.Type != "task" {
		t.Errorf("type = %q, want task", resp.Card.Type)
	}

	// Verify task card frontmatter.
	card, _ := a.store.Get("task-A")
	if card.Parent != "task-map" {
		t.Errorf("parent = %q, want task-map", card.Parent)
	}
	found := false
	for _, tag := range card.Tags {
		if tag == "task-map" {
			found = true
		}
	}
	if !found {
		t.Errorf("tags = %v, want task-map in tags", card.Tags)
	}
	if card.Status != "todo" {
		t.Errorf("status = %q, want todo", card.Status)
	}

	// Verify map's include list was updated.
	mapCard, _ := a.store.Get("task-map")
	scope, _ := mapCard.Data["scope"].(map[string]any)
	ids := toStringSlice(scope["include"])
	if len(ids) != 1 || ids[0] != "task-A" {
		t.Errorf("map include = %v, want [task-A]", ids)
	}

	// Frontier should see the task card.
	frontier, _ := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "task-map"})
	if len(frontier.TaskCards) != 1 || frontier.TaskCards[0].ID != "task-A" {
		t.Errorf("frontier = %v, want [task-A]", frontier.TaskCards)
	}
}

func TestHandleWikiCreateTaskCard_WithCategory(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "cat-map"})

	resp, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "cat-map",
		Title:    "task-with-cat",
		Question: "What is the API?",
		Category: "research",
	})
	if err != nil {
		t.Fatalf("create_task_card with category: %v", err)
	}
	if resp.Card.Type != "task" {
		t.Errorf("type = %q, want task", resp.Card.Type)
	}

	card, _ := a.store.Get("task-with-cat")
	if got, ok := card.Data["category"].(string); !ok || got != "research" {
		t.Errorf("data.category = %q (%T), want research", got, card.Data["category"])
	}
	if !strings.Contains(card.Raw, "category: \"research\"") {
		t.Errorf("frontmatter missing category:\n%s", card.Raw)
	}
	// The closing --- must start its own line. A glued `category: "research"---`
	// closer is accepted by the tolerant backend parser but rejected by the
	// frontend's line-anchored regex, making the card unopenable in the UI.
	if !strings.Contains(card.Raw, "category: \"research\"\n---\n") {
		t.Errorf("frontmatter closer glued to category value:\n%s", card.Raw)
	}
}

func TestHandleWikiCreateTaskCard_WithoutCategoryOmitsField(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "nocat-map"})

	_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "nocat-map",
		Title:    "task-no-cat",
		Question: "Plain task.",
	})
	if err != nil {
		t.Fatalf("create_task_card without category: %v", err)
	}

	card, _ := a.store.Get("task-no-cat")
	if _, ok := card.Data["category"]; ok {
		t.Errorf("data.category should be absent when not provided; got %v", card.Data["category"])
	}
	if strings.Contains(card.Raw, "category:") {
		t.Errorf("frontmatter should not contain category line:\n%s", card.Raw)
	}
}

func TestCreateCardIDTooLongRejected(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	longID := strings.Repeat("a", maxCardIDLen+1)
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "len-map",
		Title:    longID,
		Question: "Q",
	}); err == nil {
		t.Fatal("create_task_card: expected error for over-length id, got nil")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("create_task_card: error should mention length, got: %v", err)
	}
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: longID}); err == nil {
		t.Fatal("create_map: expected error for over-length id, got nil")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("create_map: error should mention length, got: %v", err)
	}
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{
		ID:  longID,
		Raw: "---\nid: " + longID + "\ntype: wiki\ntags: []\n---\n\nbody\n",
	}); err == nil {
		t.Fatal("create_card: expected error for over-length id, got nil")
	} else if !strings.Contains(err.Error(), "too long") {
		t.Fatalf("create_card: error should mention length, got: %v", err)
	}
	// Boundary: exactly maxCardIDLen must be accepted (create_map covers the
	// generic path too via the same guard).
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: strings.Repeat("b", maxCardIDLen)}); err != nil {
		t.Fatalf("create_map at exact limit should succeed, got: %v", err)
	}
}

func TestCreateTaskCardDescriptiveIDRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "react-appdef-refactor"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "react-appdef-refactor",
		Title:    "adopt-orphan-task-cards",
		Question: "Q",
	})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:    "react-appdef-refactor",
		Title:    "heal-frontier-projection",
		Question: "Q",
		DependsOn: []string{"adopt-orphan-task-cards"},
	})

	frontier, _ := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "react-appdef-refactor"})
	if len(frontier.TaskCards) != 1 || frontier.TaskCards[0].ID != "adopt-orphan-task-cards" {
		t.Fatalf("frontier = %v, want [adopt-orphan-task-cards]", frontier.TaskCards)
	}

	_, err := a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID:     "react-appdef-refactor",
		TaskID:    "heal-frontier-projection",
		DependsOn: []string{"adopt-orphan-task-cards"},
	})
	if err != nil {
		t.Fatalf("set_task_dependencies with descriptive ids: %v", err)
	}
}

func TestHandleWikiCreateTaskCard_WithDeps(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "dep-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "dep-map", Title: "leaf-1", Question: "Q1"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "dep-map", Title: "leaf-2", Question: "Q2"})

	// Create task depending on both leaves.
	_, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID:     "dep-map",
		Title:     "depender",
		Question:  "Q3",
		DependsOn: []string{"leaf-1", "leaf-2"},
	})
	if err != nil {
		t.Fatalf("create with deps: %v", err)
	}
	created, _ := a.store.Get("depender")
	if !strings.Contains(created.Raw, "data:\n  depends_on: \n    - leaf-1\n    - leaf-2") {
		t.Fatalf("created task should persist dependencies in Markdown:\n%s", created.Raw)
	}

	// depender should NOT be in frontier (deps not done).
	frontier, _ := a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "dep-map"})
	for _, tc := range frontier.TaskCards {
		if tc.ID == "depender" {
			t.Fatal("depender should not be in frontier while deps are not done")
		}
	}

	// Complete both leaves → depender graduates.
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "leaf-1", Status: "done"})
	a.handleWikiSetStatus(ctx, domain.WikiSetStatusReq{ID: "leaf-2", Status: "done"})
	frontier, _ = a.handleWikiFrontier(ctx, domain.WikiFrontierReq{MapID: "dep-map"})
	found := false
	for _, tc := range frontier.TaskCards {
		if tc.ID == "depender" {
			found = true
		}
	}
	if !found {
		t.Fatal("depender should be in frontier after deps are done")
	}
}

func TestHandleWikiSetTaskDependencies(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "rewire-map"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-map", Title: "t-a", Question: "QA"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-map", Title: "t-b", Question: "QB"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "rewire-map", Title: "t-c", Question: "QC"})

	// Set deps: t-c depends on t-a.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-map", TaskID: "t-c", DependsOn: []string{"t-a"},
	})
	deps := a.collectDependsOn("rewire-map")
	updated, _ := a.store.Get("t-c")
	if !strings.Contains(updated.Raw, "depends_on: \n    - t-a") {
		t.Fatalf("rewired task should persist dependencies in Markdown:\n%s", updated.Raw)
	}
	if len(deps["t-c"]) != 1 || deps["t-c"][0] != "t-a" {
		t.Errorf("deps[t-c] = %v, want [t-a]", deps["t-c"])
	}

	// Replace: t-c now depends on t-b instead.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-map", TaskID: "t-c", DependsOn: []string{"t-b"},
	})
	deps = a.collectDependsOn("rewire-map")
	if len(deps["t-c"]) != 1 || deps["t-c"][0] != "t-b" {
		t.Errorf("deps[t-c] after replace = %v, want [t-b]", deps["t-c"])
	}

	// Clear: no deps.
	a.handleWikiSetTaskDependencies(ctx, domain.WikiSetTaskDependenciesReq{
		MapID: "rewire-map", TaskID: "t-c", DependsOn: []string{},
	})
	deps = a.collectDependsOn("rewire-map")
	if len(deps["t-c"]) != 0 {
		t.Errorf("deps[t-c] after clear = %v, want empty", deps["t-c"])
	}
}

func TestAppendIncludeID(t *testing.T) {
	// Empty inline list [].
	raw := "---\nid: m\ntype: workflow\ndata:\n  scope:\n    include: []\n---\nbody"
	got := appendIncludeID(raw, "task-1")
	if !containsLine(got, "      - task-1") {
		t.Errorf("item not appended to empty list:\n%s", got)
	}
	// Append second item.
	got = appendIncludeID(got, "task-2")
	if !containsLine(got, "      - task-1") || !containsLine(got, "      - task-2") {
		t.Errorf("second item not appended:\n%s", got)
	}

	// No include: → create data.scope.include.
	raw2 := "---\nid: m2\ntype: workflow\ndata:\n  ownerAgentId: x\n---\nbody"
	got2 := appendIncludeID(raw2, "task-3")
	if !containsLine(got2, "      - task-3") {
		t.Errorf("include not created:\n%s", got2)
	}

	// Non-empty inline flow-style list → expand to block style preserving items.
	raw3 := "---\nid: m3\ntype: workflow\ndata:\n  scope:\n    include: [task-a, task-b]\n---\nbody"
	got3 := appendIncludeID(raw3, "task-c")
	for _, want := range []string{"      - task-a", "      - task-b", "      - task-c"} {
		if !containsLine(got3, want) {
			t.Errorf("inline expansion missing %q:\n%s", want, got3)
		}
	}
	if strings.Contains(got3, "include: [") {
		t.Errorf("inline list should be expanded to block style:\n%s", got3)
	}
}

func TestHandleWikiListDependencies(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "list-map-a"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "list-map-a", Title: "a1", Question: "Q1"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "list-map-a", Title: "a2", Question: "Q2", DependsOn: []string{"a1"}})
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "list-map-b"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "list-map-b", Title: "b1", Question: "Q1"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "list-map-b", Title: "b2", Question: "Q2", DependsOn: []string{"b1"}})

	// All maps.
	resp, err := a.handleWikiListDependencies(ctx, domain.WikiListDependenciesReq{})
	if err != nil {
		t.Fatalf("list_dependencies: %v", err)
	}
	if len(resp.Edges) != 2 {
		t.Errorf("edges = %d, want 2", len(resp.Edges))
	}

	// Filtered to one map.
	resp, _ = a.handleWikiListDependencies(ctx, domain.WikiListDependenciesReq{MapID: "list-map-a"})
	if len(resp.Edges) != 1 || resp.Edges[0].From != "a2" || resp.Edges[0].To != "a1" {
		t.Errorf("filtered edges = %+v, want [{From:a2 To:a1}]", resp.Edges)
	}
}

// TestCreateTaskCardConcurrentNoIncludeTruncation is a regression test for the
// race where concurrent create_task_card calls on one map each did a
// Get→append→Save on the map card and last-writer-won dropped every loser's
// append from data.scope.include. The includeAppendMu mutex serializes the
// append; after N concurrent creates the map's include list must contain all N
// task ids (order-independent).
func TestCreateTaskCardConcurrentNoIncludeTruncation(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "race-map"})

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
				MapID:   "race-map",
				Title:   fmt.Sprintf("task-%02d", i),
				Question: "Q",
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	mapCard, err := a.store.Get("race-map")
	if err != nil {
		t.Fatalf("get map: %v", err)
	}
	got := scopeIncludeIDs(mapCard)
	if len(got) != n {
		t.Fatalf("include length = %d, want %d (race truncated the list); include=%v",
			len(got), n, got)
	}
	seen := make(map[string]bool, n)
	for _, id := range got {
		seen[id] = true
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("task-%02d", i)
		if !seen[id] {
			t.Errorf("missing task card %q in include list: %v", id, got)
		}
	}

	// The graph must also carry every node.
	g := a.reconcileWorkflowTopo("race-map")
	if len(g.Nodes) != n {
		t.Fatalf("graph nodes = %d, want %d", len(g.Nodes), n)
	}
}

// TestCreateTaskCardConcurrentGraphNodesConsistent checks that the graph
// retry loop in handleWikiCreateTaskCard survives the optimistic-lock race:
// every node lands in the authoritative graph snapshot after concurrent
// creates, even though they all load the same revision.
func TestCreateTaskCardConcurrentGraphNodesConsistent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "graph-race-map"})

	const n = 10
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
				MapID:   "graph-race-map",
				Title:   fmt.Sprintf("g-%02d", i),
				Question: "Q",
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	g := a.reconcileWorkflowTopo("graph-race-map")
	if len(g.Nodes) != n {
		t.Fatalf("graph nodes = %d, want %d; nodes=%v", len(g.Nodes), n, g.Nodes)
	}
}

// TestMapTaskIDsGraphUnion verifies that mapTaskIDs derives the task-card id
// set from the frontmatter include list (the stateless authority, since the
// topo graph is recomputed from cards), and drops ids whose backing card was
// deleted (removeCardNodeFromTopo strips them from the include list).
func TestMapTaskIDsGraphUnion(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)
	a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "union-map"})

	// Create three task cards via the proper path (frontmatter + include).
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "union-map", Title: "u-a", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "union-map", Title: "u-b", Question: "Q"})
	a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{MapID: "union-map", Title: "u-c", Question: "Q"})

	mapCard, _ := a.store.Get("union-map")
	ids := a.mapTaskIDs("union-map", mapCard)
	if len(ids) != 3 {
		t.Fatalf("mapTaskIDs = %v (len %d), want 3 ids", ids, len(ids))
	}
	want := map[string]bool{"u-a": true, "u-b": true, "u-c": true}
	for _, id := range ids {
		if !want[id] {
			t.Errorf("unexpected id %q in mapTaskIDs", id)
		}
	}

	// Deleted card: its include entry is stripped on delete, so mapTaskIDs
	// must no longer list it.
	a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "u-c"})
	mapCard, _ = a.store.Get("union-map")
	ids = a.mapTaskIDs("union-map", mapCard)
	for _, id := range ids {
		if id == "u-c" {
			t.Errorf("deleted card 'u-c' should have been dropped from mapTaskIDs")
		}
	}
}
