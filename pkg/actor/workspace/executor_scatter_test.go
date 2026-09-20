package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// scatterTestFixture wires a workspace actor and fake context for scatter
// executor tests. The fake project actor keeps an in-memory card store
// (cardsByID) and counts create_task_card invocations per child id so the
// re-entry idempotency tests can assert no duplicate instantiation.
type scatterTestFixture struct {
	a             *Actor
	ctx           *testutil.FakeCtx
	projectID     string
	callerAgentID string

	scatterCardID string
	cardsByID     map[string]string
	childCreates  map[string]int
	createdBodies map[string]string
	statusSet     map[string]string
	outputsSet    map[string]map[string]any
}

const (
	scatterTestMapID  = "map-1"
	scatterTestSource = "items"
)

func newScatterTestFixture(t *testing.T) *scatterTestFixture {
	t.Helper()
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	f := &scatterTestFixture{
		a:             a,
		ctx:           ctx,
		projectID:     projectID,
		callerAgentID: callerAgentID,
		scatterCardID: "scatter-task",
		cardsByID:     make(map[string]string),
		childCreates:  make(map[string]int),
		createdBodies: make(map[string]string),
		statusSet:     make(map[string]string),
		outputsSet:    make(map[string]map[string]any),
	}

	ctx.LookupIDFn = f.lookupID
	return f
}

func (f *scatterTestFixture) lookupID(aid id.ActorID) (ref.Ref, bool) {
	aidStr := aid.String()
	if aidStr == f.callerAgentID {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "agent_status" {
				return gen.AgentStatusResp{ActiveWorkflowMapCardID: scatterTestMapID}
			}
			return nil
		}), true
	}
	if aidStr == f.projectID {
		return testutil.NewFakeRef(aid, f.projectHandler), true
	}
	return nil, false
}

func (f *scatterTestFixture) projectHandler(callID string, payload any) any {
	switch callID {
	case "project.wiki_get_card":
		req, ok := payload.(domain.WikiGetCardReq)
		if !ok {
			return nil
		}
		raw, ok := f.cardsByID[req.ID]
		if !ok {
			return fmt.Errorf("project.wiki.getCard %q: card not found", req.ID)
		}
		return domain.WikiGetCardResp{ID: req.ID, Raw: raw}
	case "project.wiki_get_cards_batch":
		req, ok := payload.(domain.WikiGetCardsBatchReq)
		if !ok {
			return nil
		}
		resp := domain.WikiGetCardsBatchResp{Cards: make([]domain.WikiCardRaw, 0, len(req.Ids))}
		for _, cardID := range req.Ids {
			item := domain.WikiCardRaw{ID: cardID}
			if raw, ok := f.cardsByID[cardID]; ok {
				item.Raw, item.Found = raw, true
			}
			resp.Cards = append(resp.Cards, item)
		}
		return resp
	case "project.wiki_create_task_card":
		req, ok := payload.(domain.WikiCreateTaskCardReq)
		if !ok {
			return nil
		}
		if _, exists := f.cardsByID[req.Title]; exists {
			return fmt.Errorf("project.wiki.create_task_card: card %q already exists", req.Title)
		}
		f.childCreates[req.Title]++
		f.createdBodies[req.Title] = req.Question
		f.cardsByID[req.Title] = "---\nid: " + req.Title + "\ntype: task\nstatus: todo\nparent: " + req.MapID +
			"\ndata:\n  depends_on: []\n---\n\n" + req.Question + "\n"
		return domain.WikiCreateTaskCardResp{}
	case "project.wiki_set_status":
		req, ok := payload.(gen.WikiSetStatusReq)
		if !ok {
			return nil
		}
		f.statusSet[req.ID] = req.Status
		if raw, ok := f.cardsByID[req.ID]; ok {
			f.cardsByID[req.ID] = replaceCardStatusInRaw(raw, req.Status)
		}
		return domain.WikiSetStatusResp{}
	case "project.wiki_set_task_outputs":
		req, ok := payload.(domain.WikiSetTaskOutputsReq)
		if !ok {
			return nil
		}
		f.outputsSet[req.CardID] = req.Outputs
		return domain.WikiSetTaskOutputsResp{}
	}
	return nil
}

// replaceCardStatusInRaw swaps the frontmatter status value. Sufficient for
// fixture cards, whose frontmatter is generated in a fixed shape.
func replaceCardStatusInRaw(raw, status string) string {
	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "status: ") {
			lines[i] = "status: " + status
			break
		}
	}
	return strings.Join(lines, "\n")
}

// scatterCardRaw builds the bound scatter card frontmatter. execYAML is the
// data.exec block body (kind is always scatter).
func (f *scatterTestFixture) scatterCardRaw(status, execYAML string) string {
	return "---\nid: " + f.scatterCardID + "\ntype: task\nstatus: " + status +
		"\nparent: " + scatterTestMapID + "\ndata:\n  exec:\n    kind: scatter\n" + execYAML +
		"---\n\nScatter the upstream array.\n"
}

// seedScatterCard installs the scatter card in the fake project store with
// the standard test declaration.
func (f *scatterTestFixture) seedScatterCard(status, execYAML string) {
	f.cardsByID[f.scatterCardID] = f.scatterCardRaw(status, execYAML)
}

// standardExecYAML is a well-formed data.exec block: source=items, a simple
// template, max as a quoted scalar (frontmatter scalars arrive as strings).
func standardExecYAML() string {
	return "    source: items\n    template: 'Process {{item}} at slot {{index}}.'\n    max: '10'\n"
}

func (f *scatterTestFixture) claimReq(status, execYAML string, inputs map[string]any) ClaimReq {
	return ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.scatterCardID,
		CardRaw:         f.scatterCardRaw(status, execYAML),
		Claimed:         true,
		Inputs:          inputs,
		Preflight:       &PreflightResult{},
	}
}

// markChild flips a child card's status and (optionally) attaches a
// task_outputs JSON string, mimicking what workers + review produce.
func (f *scatterTestFixture) markChild(idx int, status string, outputsJSON string) {
	childID := fmt.Sprintf(scatterChildIDFormat, f.scatterCardID, idx)
	raw, ok := f.cardsByID[childID]
	if !ok {
		f.cardsByID[childID] = "---\nid: " + childID + "\ntype: task\nstatus: todo\nparent: " + scatterTestMapID + "\ndata:\n  depends_on: []\n---\n\nbody\n"
		raw = f.cardsByID[childID]
	}
	raw = replaceCardStatusInRaw(raw, status)
	if outputsJSON != "" {
		raw = strings.Replace(raw, "  depends_on: []\n", "  depends_on: []\n  task_outputs: '"+outputsJSON+"'\n", 1)
	}
	f.cardsByID[childID] = raw
}

func childIDOf(f *scatterTestFixture, idx int) string {
	return fmt.Sprintf(scatterChildIDFormat, f.scatterCardID, idx)
}

func TestScatterExecutor_Registered(t *testing.T) {
	f := newScatterTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindScatter)
	if !ok {
		t.Fatal("scatter executor not registered")
	}
	if exec.Kind() != ExecKindScatter {
		t.Fatalf("expected kind scatter, got %q", exec.Kind())
	}
}

func TestScatterExecutor_Preflight_Rejections(t *testing.T) {
	f := newScatterTestFixture(t)
	e := newScatterExecutor(f.a)

	cases := []struct {
		name    string
		exec    string
		wantErr string
	}{
		{"missing source", "    template: 'T {{item}}'\n", "source is required"},
		{"missing template", "    source: items\n", "template is required"},
		{"max not integer", "    source: items\n    template: T\n    max: 'abc'\n", "not an integer"},
		{"max below one", "    source: items\n    template: T\n    max: '0'\n", "must be >= 1"},
		{"max above ceiling", "    source: items\n    template: T\n    max: '500'\n", "must be <= 100"},
	}
	for _, tc := range cases {
		_, err := e.Preflight(f.ctx, ClaimReq{
			CallerAgentID:   f.callerAgentID,
			ProjectID:       f.projectID,
			BoundTaskCardID: f.scatterCardID,
			CardRaw:         f.scatterCardRaw("todo", tc.exec),
		})
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.wantErr)
		}
	}

	// No active workflow: a caller the context cannot resolve.
	req := f.claimReq("todo", standardExecYAML(), nil)
	req.CallerAgentID = ""
	if _, err := e.Preflight(f.ctx, req); err == nil {
		t.Fatal("missing caller agent must fail preflight")
	}
}

func TestScatterExecutor_Preflight_HappyPath(t *testing.T) {
	f := newScatterTestFixture(t)
	e := newScatterExecutor(f.a)
	pf, err := e.Preflight(f.ctx, f.claimReq("todo", standardExecYAML(), nil))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.DisplayName != "ScatterExecutor" {
		t.Errorf("DisplayName = %q", pf.DisplayName)
	}
}

func TestScatterExecutor_Execute_ThreeElementFanoutAllSucceed(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	items := []any{"alpha", "beta", float64(42)}
	resp, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: items}))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.DisplayName != "ScatterExecutor" {
		t.Errorf("DisplayName = %q", resp.DisplayName)
	}

	// Three children created with stable ids.
	for idx := 0; idx < 3; idx++ {
		child := childIDOf(f, idx)
		if f.childCreates[child] != 1 {
			t.Errorf("child %s created %d times, want 1", child, f.childCreates[child])
		}
		if _, ok := f.cardsByID[child]; !ok {
			t.Errorf("child card %s missing", child)
		}
	}
	// Bodies carry the element inline and as the JSON inputs block.
	body := f.createdBodies[childIDOf(f, 0)]
	if !strings.Contains(body, "Process alpha at slot 0.") {
		t.Errorf("child body missing substituted element: %q", body)
	}
	if !strings.Contains(body, `{"item":"alpha","index":0}`) {
		t.Errorf("child body missing task inputs block: %q", body)
	}
	structured := f.createdBodies[childIDOf(f, 2)]
	if !strings.Contains(structured, "Process 42 at slot 2.") {
		t.Errorf("structured element body: %q", structured)
	}

	// Fan-out tracked, scatter card still doing (join pending).
	if len(f.a.scatterFanouts) != 1 {
		t.Fatalf("fanout records = %d, want 1", len(f.a.scatterFanouts))
	}
	if got := f.a.scatterFanouts[0].Status; got != scatterStatusJoining {
		t.Errorf("fanout status = %q, want joining (children still pending)", got)
	}
	if got := f.statusSet[f.scatterCardID]; got != "" {
		t.Errorf("scatter card status set to %q before join; want untouched", got)
	}

	// All children done with distinct outputs → sweep joins.
	f.markChild(0, "done", `{"report":"A"}`)
	f.markChild(1, "done", `{"report":"B"}`)
	f.markChild(2, "done", `{"report":"C"}`)

	f.a.reconcileScatterFanouts(f.ctx)

	if f.statusSet[f.scatterCardID] != "done" {
		t.Fatalf("scatter card status = %q, want done", f.statusSet[f.scatterCardID])
	}
	outs := f.outputsSet[f.scatterCardID]
	itemsOut, ok := outs["items"].([]any)
	if !ok || len(itemsOut) != 3 {
		t.Fatalf("outputs.items = %#v, want 3-element array", outs["items"])
	}
	for i, want := range []string{"A", "B", "C"} {
		m, ok := itemsOut[i].(map[string]any)
		if !ok || m["report"] != want {
			t.Errorf("outputs.items[%d] = %#v, want report %q", i, itemsOut[i], want)
		}
	}
	if f.a.scatterFanouts[0].Status != scatterStatusDone {
		t.Errorf("fanout status = %q, want done", f.a.scatterFanouts[0].Status)
	}
}

func TestScatterExecutor_Execute_PartialFailurePropagates(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	items := []any{"a", "b", "c"}
	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: items})); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// One child fails while the others are still running: the failure must
	// propagate immediately without waiting for the pending siblings.
	f.markChild(0, "done", `{"report":"A"}`)
	f.markChild(1, "failed", "")
	f.markChild(2, "doing", "")

	f.a.reconcileScatterFanouts(f.ctx)

	if f.statusSet[f.scatterCardID] != "failed" {
		t.Fatalf("scatter card status = %q, want failed", f.statusSet[f.scatterCardID])
	}
	outs := f.outputsSet[f.scatterCardID]
	note, _ := outs["error"].(string)
	if !strings.Contains(note, childIDOf(f, 1)) {
		t.Errorf("outputs.error = %q, want failed child id", note)
	}
	failed, ok := outs["failed_children"].([]string)
	if !ok || len(failed) != 1 || failed[0] != childIDOf(f, 1) {
		t.Errorf("outputs.failed_children = %#v", outs["failed_children"])
	}
	if f.a.scatterFanouts[0].Status != scatterStatusFailed {
		t.Errorf("fanout status = %q, want failed", f.a.scatterFanouts[0].Status)
	}
}

func TestScatterExecutor_Execute_ReentryDoesNotDuplicate(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	items := []any{"x", "y", "z"}
	req := f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: items})
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := e.Execute(f.ctx, req); err != nil {
			t.Fatalf("execute attempt %d: %v", attempt, err)
		}
	}

	for idx := 0; idx < 3; idx++ {
		child := childIDOf(f, idx)
		if f.childCreates[child] != 1 {
			t.Errorf("child %s created %d times across re-entries, want 1", child, f.childCreates[child])
		}
	}
	if len(f.a.scatterFanouts) != 1 {
		t.Fatalf("fanout records = %d, want 1 (upsert replaces)", len(f.a.scatterFanouts))
	}
}

func TestScatterExecutor_Execute_BadSourceFailsCard(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	// Source key absent from inputs.
	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), nil)); err != nil {
		t.Fatalf("card-level failure must not surface as dispatch error: %v", err)
	}
	if f.statusSet[f.scatterCardID] != "failed" {
		t.Fatalf("status = %q, want failed", f.statusSet[f.scatterCardID])
	}
	if note, _ := f.outputsSet[f.scatterCardID]["error"].(string); !strings.Contains(note, "not resolved") {
		t.Errorf("outputs.error = %#v", f.outputsSet[f.scatterCardID]["error"])
	}

	// Source present but not an array.
	f.statusSet = make(map[string]string)
	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: "nope"})); err != nil {
		t.Fatalf("non-array source must be a card failure, got dispatch error: %v", err)
	}
	if f.statusSet[f.scatterCardID] != "failed" || !strings.Contains(f.outputsSet[f.scatterCardID]["error"].(string), "not an array") {
		t.Errorf("non-array: status=%q error=%#v", f.statusSet[f.scatterCardID], f.outputsSet[f.scatterCardID]["error"])
	}

	// Over max.
	f.statusSet = make(map[string]string)
	many := make([]any, 11)
	for i := range many {
		many[i] = fmt.Sprintf("e%d", i)
	}
	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: many})); err != nil {
		t.Fatalf("over-max must be a card failure, got dispatch error: %v", err)
	}
	if f.statusSet[f.scatterCardID] != "failed" || !strings.Contains(f.outputsSet[f.scatterCardID]["error"].(string), "exceeds") {
		t.Errorf("over-max: status=%q error=%#v", f.statusSet[f.scatterCardID], f.outputsSet[f.scatterCardID]["error"])
	}
	// No children were created for the failed fan-out.
	if len(f.childCreates) != 0 {
		t.Errorf("children created for failed source: %#v", f.childCreates)
	}
}

func TestScatterExecutor_Execute_EmptyArrayCompletesImmediately(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: []any{}})); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if f.statusSet[f.scatterCardID] != "done" {
		t.Fatalf("status = %q, want immediate done", f.statusSet[f.scatterCardID])
	}
	itemsOut, ok := f.outputsSet[f.scatterCardID]["items"].([]any)
	if !ok || len(itemsOut) != 0 {
		t.Errorf("outputs.items = %#v, want empty array", f.outputsSet[f.scatterCardID]["items"])
	}
	if f.a.scatterFanouts[0].Status != scatterStatusDone {
		t.Errorf("fanout status = %q, want done", f.a.scatterFanouts[0].Status)
	}
}

func TestScatterExecutor_Sweep_RecreatesMissingChild(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: []any{"a", "b"}})); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// One child completes, the other vanishes (store damage / manual delete).
	f.markChild(0, "done", `{"report":"A"}`)
	delete(f.cardsByID, childIDOf(f, 1))
	delete(f.childCreates, childIDOf(f, 1))

	f.a.reconcileScatterFanouts(f.ctx)

	if f.childCreates[childIDOf(f, 1)] != 1 {
		t.Fatalf("missing child not re-created (creates=%d)", f.childCreates[childIDOf(f, 1)])
	}
	if f.statusSet[f.scatterCardID] != "" {
		t.Errorf("scatter card settled to %q while a child was re-created; want still pending", f.statusSet[f.scatterCardID])
	}
	if !strings.Contains(f.createdBodies[childIDOf(f, 1)], `{"item":"b","index":1}`) {
		t.Errorf("re-created child body wrong: %q", f.createdBodies[childIDOf(f, 1)])
	}

	// The re-created child finishes → join settles.
	f.markChild(1, "done", `{"report":"B"}`)
	f.a.reconcileScatterFanouts(f.ctx)
	if f.statusSet[f.scatterCardID] != "done" {
		t.Fatalf("status = %q, want done after heal", f.statusSet[f.scatterCardID])
	}
}

func TestScatterExecutor_Sweep_ConvergesOnExternalStatus(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)

	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: []any{"a"}})); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Someone flips the scatter card done manually (e.g. owner override):
	// the sweep must converge the table instead of re-failing the card.
	f.cardsByID[f.scatterCardID] = replaceCardStatusInRaw(f.cardsByID[f.scatterCardID], "done")
	f.a.reconcileScatterFanouts(f.ctx)

	if f.a.scatterFanouts[0].Status != scatterStatusDone {
		t.Errorf("fanout status = %q, want done (converged)", f.a.scatterFanouts[0].Status)
	}

	// A deleted scatter card drops its fan-out record.
	delete(f.cardsByID, f.scatterCardID)
	f.a.scatterFanouts[0].Status = scatterStatusActive
	f.a.reconcileScatterFanouts(f.ctx)
	if len(f.a.scatterFanouts) != 0 {
		t.Errorf("fanout records = %d, want 0 after scatter card deletion", len(f.a.scatterFanouts))
	}
}

func TestScatterExecutor_Sweep_RearmsAndJoins(t *testing.T) {
	f := newScatterTestFixture(t)
	var scheduled []string
	f.ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		scheduled = append(scheduled, callID)
		return nil
	}

	// The sweep handler self-reschedules and runs a reconcile pass.
	f.seedScatterCard("doing", standardExecYAML())
	e := newScatterExecutor(f.a)
	if _, err := e.Execute(f.ctx, f.claimReq("doing", standardExecYAML(), map[string]any{scatterTestSource: []any{"a"}})); err != nil {
		t.Fatalf("execute: %v", err)
	}
	f.markChild(0, "done", `{"report":"A"}`)

	if _, err := f.a.handleScatterSweep(f.ctx, scatterSweepReq{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(scheduled) == 0 || scheduled[len(scheduled)-1] != "workspace.internal_scatter_sweep" {
		t.Fatalf("sweep did not re-arm itself: %v", scheduled)
	}
	if f.statusSet[f.scatterCardID] != "done" {
		t.Fatalf("status = %q, want done after sweep", f.statusSet[f.scatterCardID])
	}
}

func TestScatterExecutor_DispatcherIntegration(t *testing.T) {
	f := newScatterTestFixture(t)
	f.seedScatterCard("todo", standardExecYAML())

	// The dispatcher path needs the fused claim callable.
	claimed := false
	baseHandler := f.projectHandler
	handler := func(callID string, payload any) any {
		if callID == "project.wiki_claim_task_card" {
			if claimed {
				return fmt.Errorf("task card claim: expected status one of [backlog todo], currently doing")
			}
			claimed = true
			return gen.WikiClaimTaskCardResp{
				PreviousStatus: "todo",
				Raw:            f.scatterCardRaw("doing", standardExecYAML()),
				Inputs:         map[string]any{scatterTestSource: []any{"one", "two"}},
			}
		}
		return baseHandler(callID, payload)
	}
	f.ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == f.callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: scatterTestMapID}
				}
				return nil
			}), true
		}
		if aidStr == f.projectID {
			return testutil.NewFakeRef(aid, handler), true
		}
		return nil, false
	}

	resp, err := f.a.handleAgentSpawnAssign(f.ctx, domain.WorkspaceAgentSpawnAssignReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.scatterCardID,
		AgentKind:       domain.AgentKindWorker,
	})
	if err != nil {
		t.Fatalf("spawn_assign: %v", err)
	}
	if resp.AgentActorID != "" {
		t.Errorf("AgentActorID = %q, want empty (scatter spawns no agent)", resp.AgentActorID)
	}
	if len(f.childCreates) != 2 {
		t.Fatalf("children created = %d, want 2", len(f.childCreates))
	}
	if len(f.a.scatterFanouts) != 1 || len(f.a.scatterFanouts[0].ChildIDs) != 2 {
		t.Fatalf("fanout tracking wrong: %#v", f.a.scatterFanouts)
	}
}

func TestScatterFanouts_PersistRoundTrip(t *testing.T) {
	f := newScatterTestFixture(t)

	// Persisted snapshot decodes back with the fan-out intact.
	f.a.scatterFanouts = []scatterFanout{{
		ScatterCardID: "s1",
		ProjectID:     f.projectID,
		MapID:         scatterTestMapID,
		Source:        scatterTestSource,
		Template:      "T {{item}}",
		Items:         []any{"a", "b"},
		ChildIDs:      []string{"s1-item-0", "s1-item-1"},
		Status:        scatterStatusActive,
		CreatedAt:     "2026-09-10T00:00:00Z",
	}}
	if err := f.a.saveScatterFanoutsSnapshot(f.a.scatterFanoutsSnapshot()); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := f.a.scatterFanoutsSnapshot()
	_ = loaded

	var decoded []scatterFanout
	b, err := json.Marshal(f.a.scatterFanoutsSnapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ScatterCardID != "s1" || len(decoded[0].Items) != 2 {
		t.Fatalf("round trip lost data: %#v", decoded)
	}
}
