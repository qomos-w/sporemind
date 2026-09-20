package workspace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	appruntime "github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakeTopology satisfies TopologyProvider for executor tests: only
// Snapshot() is exercised; the embedded nil interface covers the rest.
type fakeTopology struct {
	appruntime.TopologyProvider
	nodes []appruntime.ActorNode
}

func (f *fakeTopology) Snapshot() []appruntime.ActorNode { return f.nodes }

// toolcallTestFixture builds a workspace actor plus fake context wired for
// toolcall executor tests.
type toolcallTestFixture struct {
	a               *Actor
	ctx             *testutil.FakeCtx
	projectID       string
	callerAgentID   string
	boundTaskCardID string

	// Fake target service state.
	toastInvoked     bool
	toastInvokeCall  string
	toastInvokeArgs  map[string]any
	toastInvokeOther any

	// Fake project actor state.
	statusSet  map[string]string
	outputsSet map[string]map[string]any
}

func newToolCallTestFixture(t *testing.T) *toolcallTestFixture {
	t.Helper()
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	f := &toolcallTestFixture{
		a:               a,
		ctx:             ctx,
		projectID:       projectID,
		callerAgentID:   callerAgentID,
		boundTaskCardID: "task-toolcall-1",
		statusSet:       make(map[string]string),
		outputsSet:      make(map[string]map[string]any),
	}

	// The topology exposes three callables: a routed read-only one used by
	// happy-path tests (typed against ToastShowReq: Title required,
	// DurationMs int64 optional — pins required-field validation and
	// string→number coercion), a mutating one, and an unrouted agent-local
	// one.
	a.topo = &fakeTopology{nodes: []appruntime.ActorNode{{
		ID: "toast-actor",
		Callables: []domain.CallableInterface{
			{
				Name:        "toast.state",
				Description: "query toast queue state",
				ServiceName: "toast",
				ReqSchemaID: schemaIDForTest(t, "ToastShowReq"),
			},
			{
				Name:        "toast.show",
				Description: "show a toast (mutating)",
				ServiceName: "toast",
				EffectKind:  "mutating",
			},
			{
				Name:        "goal_submit",
				Description: "agent-local callable",
				ServiceName: "",
			},
		},
	}}}

	ctx.LookupIDFn = f.lookupID
	ctx.LookupServiceFn = f.lookupService
	return f
}

func schemaIDForTest(t *testing.T, name string) int32 {
	t.Helper()
	for id, n := range gen.SchemaIDs {
		if n == name {
			return int32(id)
		}
	}
	t.Fatalf("schema %s not registered", name)
	return 0
}

func (f *toolcallTestFixture) lookupID(aid id.ActorID) (ref.Ref, bool) {
	aidStr := aid.String()
	if aidStr == f.callerAgentID {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "agent_status" {
				return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
			}
			return nil
		}), true
	}
	return f.fakeProjectRef(aid), true
}

func (f *toolcallTestFixture) lookupService(name string) (ref.Ref, bool) {
	if name == "toast" {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			f.toastInvoked = true
			f.toastInvokeCall = callID
			if m, ok := payload.(map[string]any); ok {
				f.toastInvokeArgs = m
			} else {
				f.toastInvokeOther = payload
			}
			return gen.ToastShowResp{ID: "toast-1"}
		}), true
	}
	return nil, false
}

func (f *toolcallTestFixture) fakeProjectRef(aid id.ActorID) ref.Ref {
	return testutil.NewFakeRef(aid, func(callID string, payload any) any {
		switch callID {
		case "project.wiki_get_card":
			req, ok := payload.(domain.WikiGetCardReq)
			if !ok {
				return nil
			}
			if req.ID == f.boundTaskCardID {
				return domain.WikiGetCardResp{ID: req.ID, Raw: f.boundTaskCardRaw("    args: {}\n")}
			}
			return fmt.Errorf("card %q not found", req.ID)
		case "project.wiki_set_status":
			req, ok := payload.(gen.WikiSetStatusReq)
			if ok {
				f.statusSet[req.ID] = req.Status
			}
			return domain.WikiSetStatusResp{}
		case "project.wiki_set_task_outputs":
			req, ok := payload.(domain.WikiSetTaskOutputsReq)
			if ok {
				f.outputsSet[req.CardID] = req.Outputs
			}
			return domain.WikiSetTaskOutputsResp{}
		}
		return nil
	})
}

func (f *toolcallTestFixture) boundTaskCardRaw(argsYAML string) string {
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"data:\n" +
		"  exec:\n" +
		"    kind: toolcall\n" +
		"    callable: toast.state\n" +
		argsYAML +
		"---\n\nToolcall task body."
}

func (f *toolcallTestFixture) claimReq(argsYAML string) ClaimReq {
	return ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.boundTaskCardRaw(argsYAML),
		Claimed:         true,
	}
}

func TestToolCallExecutor_Registered(t *testing.T) {
	f := newToolCallTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindToolCall)
	if !ok {
		t.Fatal("toolcall executor not registered")
	}
	if exec.Kind() != ExecKindToolCall {
		t.Fatalf("expected kind toolcall, got %q", exec.Kind())
	}
}

func TestToolCallExecutor_Preflight_Rejections(t *testing.T) {
	f := newToolCallTestFixture(t)
	e := newToolCallExecutor(f.a)

	// Missing callable.
	_, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID: f.callerAgentID,
		ProjectID:     f.projectID,
		CardRaw:       "---\nid: t\ntype: task\ndata:\n  exec:\n    kind: toolcall\n---\nBody.",
	})
	if err == nil || !strings.Contains(err.Error(), "callable is required") {
		t.Fatalf("missing callable: err = %v", err)
	}

	// Unknown callable.
	_, err = e.Preflight(f.ctx, f.claimReqFor("nosuch.callable"))
	if err == nil || !strings.Contains(err.Error(), "not found in topology") {
		t.Fatalf("unknown callable: err = %v", err)
	}

	// Mutating effect.
	mutating := f.claimReqFor("toast.show")
	_, err = e.Preflight(f.ctx, mutating)
	if err == nil || !strings.Contains(err.Error(), "effect") {
		t.Fatalf("mutating callable: err = %v", err)
	}

	// Agent-local (no service routing).
	local := f.claimReqFor("goal_submit")
	_, err = e.Preflight(f.ctx, local)
	if err == nil || !strings.Contains(err.Error(), "no service routing") {
		t.Fatalf("agent-local callable: err = %v", err)
	}

	// No active workflow.
	noWorkflow := f.claimReq("")
	noWorkflow.CallerAgentID = ""
	if _, err := e.Preflight(f.ctx, noWorkflow); err == nil {
		t.Fatal("missing caller agent must fail preflight")
	}
}

func (f *toolcallTestFixture) claimReqFor(callable string) ClaimReq {
	req := f.claimReq("")
	req.CardRaw = strings.Replace(f.boundTaskCardRaw(""), "toast.state", callable, 1)
	return req
}

func TestToolCallExecutor_Execute_HappyPath(t *testing.T) {
	f := newToolCallTestFixture(t)
	e := newToolCallExecutor(f.a)

	// Static args + upstream inputs merged; card scalars arrive as strings
	// so DurationMs "5000" must coerce to a number before the wire.
	req := f.claimReq("    args:\n      Title: hello\n      DurationMs: '5000'\n")
	req.Inputs = map[string]any{"Body": "from-upstream"}
	req.Preflight = &PreflightResult{}

	resp, err := e.Execute(f.ctx, req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !f.toastInvoked || f.toastInvokeCall != "toast.state" {
		t.Fatalf("toast.state not invoked (invoked=%v call=%q)", f.toastInvoked, f.toastInvokeCall)
	}
	if got, _ := f.toastInvokeArgs["Title"].(string); got != "hello" {
		t.Errorf("Title = %#v, want hello", f.toastInvokeArgs["Title"])
	}
	if got, ok := f.toastInvokeArgs["DurationMs"].(float64); !ok || got != 5000 {
		t.Errorf("DurationMs = %#v, want coerced 5000", f.toastInvokeArgs["DurationMs"])
	}
	if got, _ := f.toastInvokeArgs["Body"].(string); got != "from-upstream" {
		t.Errorf("Body = %#v, want upstream binding passthrough", f.toastInvokeArgs["Body"])
	}
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("card status = %q, want done", f.statusSet[f.boundTaskCardID])
	}
	result, ok := f.outputsSet[f.boundTaskCardID]["result"].(map[string]any)
	if !ok || result["Id"] != "toast-1" {
		t.Errorf("outputs[result] = %#v, want normalized ToastShowResp", f.outputsSet[f.boundTaskCardID]["result"])
	}
	if resp.DisplayName != "ToolCallExecutor" {
		t.Errorf("DisplayName = %q", resp.DisplayName)
	}
}

func TestToolCallExecutor_Execute_ValidationFailureMarksCardFailed(t *testing.T) {
	f := newToolCallTestFixture(t)
	e := newToolCallExecutor(f.a)

	// ToastShowReq requires Title; supply only an optional field.
	req := f.claimReq("    args:\n      Body: x\n")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("card-level validation failure must not surface as dispatch error: %v", err)
	}
	if f.toastInvoked {
		t.Error("callable must not be invoked when validation fails")
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	if note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string); !strings.Contains(note, "schema validation") {
		t.Errorf("outputs[error] = %#v, want schema validation note", f.outputsSet[f.boundTaskCardID]["error"])
	}
}

func TestToolCallExecutor_Execute_InvokeFailureMarksCardFailed(t *testing.T) {
	f := newToolCallTestFixture(t)
	e := newToolCallExecutor(f.a)

	// Fail every toast invoke.
	f.ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "toast" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any {
				return fmt.Errorf("boom")
			}), true
		}
		return nil, false
	}

	req := f.claimReq("    args:\n      Title: hi\n")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("invoke failure must map to card failure, not dispatch error: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	if note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string); !strings.Contains(note, "boom") {
		t.Errorf("outputs[error] = %#v, want boom note", f.outputsSet[f.boundTaskCardID]["error"])
	}
}

func TestToolCallExecutor_Execute_InputsOverrideStaticArgs(t *testing.T) {
	f := newToolCallTestFixture(t)
	e := newToolCallExecutor(f.a)

	req := f.claimReq("    args:\n      Title: static-title\n")
	req.Inputs = map[string]any{"Title": "runtime-title"}
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got, _ := f.toastInvokeArgs["Title"].(string); got != "runtime-title" {
		t.Errorf("Title = %#v, want upstream override to runtime-title", f.toastInvokeArgs["Title"])
	}
}

// ─────────────────────────────────────────────────────────────────────────
// Security gate v2 tests
//
// The toolcall executor admits mutating / reversible callables only when
// the bound card declares data.exec.gate_card AND that gate card is done
// with an approval record AND the workflow map card has stamped the
// effect. The tests below pin each branch independently so a regression
// in one branch (e.g. stamp drift after a map edit) surfaces here rather
// than in production.
// ─────────────────────────────────────────────────────────────────────────

// securityGateFixture builds on toolcallTestFixture but adds the gate / map
// card factory hooks. Tests pass closures that produce the raw markdown for
// each card ID; the fake project ref serves it back on wiki_get_card.
type securityGateFixture struct {
	*toolcallTestFixture

	mapCardID    string
	gateCardID   string
	boundCardRaw string

	// Closure-based card builders. Tests set these to shape the gate / map
	// cards the executor will fetch.
	mapCardBuilder  func() string
	gateCardBuilder func() string

	// Capture project.wiki_edit_card writes so the stamp test can assert
	// the persisted data.stamped_effects.
	editedMapRaw string
}

// fakeProjectRefFn lets tests inject a project ref that serves gate / map
// cards; default delegates to the base fixture's fakeProjectRef.
func (g *securityGateFixture) fakeProjectRefFn(aid id.ActorID) ref.Ref {
	return testutil.NewFakeRef(aid, func(callID string, payload any) any {
		switch callID {
		case "project.wiki_get_card":
			req, ok := payload.(domain.WikiGetCardReq)
			if !ok {
				return fmt.Errorf("bad payload type %T", payload)
			}
			switch req.ID {
			case g.toolcallTestFixture.boundTaskCardID:
				return domain.WikiGetCardResp{ID: req.ID, Raw: g.boundCardRaw}
			case g.gateCardID:
				return domain.WikiGetCardResp{ID: req.ID, Raw: g.gateCardBuilder()}
			case g.mapCardID:
				return domain.WikiGetCardResp{ID: req.ID, Raw: g.mapCardBuilder()}
			}
			return fmt.Errorf("card %q not found", req.ID)
		case "project.wiki_set_status":
			req, ok := payload.(gen.WikiSetStatusReq)
			if ok {
				g.statusSet[req.ID] = req.Status
			}
			return domain.WikiSetStatusResp{}
		case "project.wiki_set_task_outputs":
			req, ok := payload.(domain.WikiSetTaskOutputsReq)
			if ok {
				g.outputsSet[req.CardID] = req.Outputs
			}
			return domain.WikiSetTaskOutputsResp{}
		case "project.wiki_edit_card":
			req, ok := payload.(domain.WikiEditCardReq)
			if ok {
				g.editedMapRaw = req.Raw
			}
			return domain.WikiEditCardResp{}
		}
		return nil
	})
}

func newSecurityGateFixture(t *testing.T) *securityGateFixture {
	f := newToolCallTestFixture(t)
	g := &securityGateFixture{
		toolcallTestFixture: f,
		mapCardID:           "map-stamp-1",
		gateCardID:          "gate-1",
	}
	// Default builders: gate card approved + done, map card stamps the
	// bound task. Individual tests override these to force specific
	// branches (e.g. gate status="doing" to assert the not-done branch).
	g.gateCardBuilder = func() string {
		return "---\nid: " + g.gateCardID + "\ntype: task\nstatus: done\n" +
			"data:\n  task_outputs:\n    decision: approved\n    approver: tester\n" +
			"---\n\nGate card body."
	}
	g.mapCardBuilder = func() string {
		// Mirror the executor's stamped_effects shape exactly so the
		// verification matches.
		stamp := `[{"task_card_id":"` + f.boundTaskCardID + `","callable_id":"toast.show","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"}]`
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  stamped_effects: '" + stamp + "'\n" +
			"---\n\nMap card body."
	}
	// Wire the bound task card's parent to the map ID so verifyMapStampedEffect
	// can find it via CardParent.
	g.boundCardRaw = "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"parent: " + g.mapCardID + "\n" +
		"data:\n  exec:\n" +
		"    kind: toolcall\n" +
		"    callable: toast.show\n" +
		"    gate_card: " + g.gateCardID + "\n" +
		"    args: {}\n" +
		"---\n\nToolcall task body."
	return g
}

// mutateProjectRef swaps the toolcall fixture's fakeProjectRef for one that
// serves the gate / map / bound cards from the configured builders and
// captures project.wiki_edit_card writes.
func (g *securityGateFixture) mutateProjectRef() {
	g.ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == g.callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-stamp-1"}
				}
				return nil
			}), true
		}
		return g.fakeProjectRefFn(aid), true
	}
}

// TestSecurityGateV2_StampedAndApproved_AllowsExecute pins the happy
// path: gate card is done with task_outputs.decision="approved" AND the
// workflow map has stamped the bound task → executor admits the mutating
// callable and the toast service is invoked.
func TestSecurityGateV2_StampedAndApproved_AllowsExecute(t *testing.T) {
	g := newSecurityGateFixture(t)
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	if _, err := e.Execute(g.ctx, req); err != nil {
		t.Fatalf("stamped+approved mutating callable must execute: %v", err)
	}
	if !g.toastInvoked || g.toastInvokeCall != "toast.show" {
		t.Fatalf("toast.show not invoked (invoked=%v call=%q)", g.toastInvoked, g.toastInvokeCall)
	}
	if g.statusSet[g.boundTaskCardID] != "done" {
		t.Errorf("card status = %q, want done", g.statusSet[g.boundTaskCardID])
	}
}

// TestSecurityGateV2_GateNotDone_RejectsExecute pins the gate-card
// status check: gate card is still "doing" (not "done") → executor
// rejects the mutating callable, even if the map has stamped it.
func TestSecurityGateV2_GateNotDone_RejectsExecute(t *testing.T) {
	g := newSecurityGateFixture(t)
	g.gateCardBuilder = func() string {
		return "---\nid: " + g.gateCardID + "\ntype: task\nstatus: doing\n" +
			"data:\n  task_outputs:\n    decision: pending\n" +
			"---\n\nGate card body (still doing)."
	}
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	_, err := e.Execute(g.ctx, req)
	if err == nil {
		t.Fatal("gate-not-done must reject execute")
	}
	if !strings.Contains(err.Error(), `status is "doing"`) {
		t.Fatalf("error must mention gate card status, got: %v", err)
	}
	if g.toastInvoked {
		t.Error("toast.show must not be invoked when gate is not done")
	}
}

// TestSecurityGateV2_Unstamped_RejectsExecute pins the map-stamp check:
// the workflow map card has no data.stamped_effects entry for the bound
// task → executor rejects the mutating callable, even if the gate card
// is approved. This is the dual-check the card description promises: a
// user must approve both the per-card gate AND the workflow plan.
func TestSecurityGateV2_Unstamped_RejectsExecute(t *testing.T) {
	g := newSecurityGateFixture(t)
	g.mapCardBuilder = func() string {
		// Map card with no data.stamped_effects at all.
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  scope:\n    include: []\n" +
			"---\n\nMap card body."
	}
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	_, err := e.Execute(g.ctx, req)
	if err == nil {
		t.Fatal("unstamped mutating callable must be rejected")
	}
	if !strings.Contains(err.Error(), "stamped_effects") {
		t.Fatalf("error must mention stamped_effects, got: %v", err)
	}
	if g.toastInvoked {
		t.Error("toast.show must not be invoked when the workflow map has no stamp")
	}
}

// TestSecurityGateV2_MissingGateCard_RejectsExecute pins the gate-card
// declaration check: the bound task card has no data.exec.gate_card →
// executor rejects the mutating callable up front, before any project
// round-trip.
func TestSecurityGateV2_MissingGateCard_RejectsExecute(t *testing.T) {
	g := newSecurityGateFixture(t)
	// Build a bound card raw that omits gate_card.
	g.boundCardRaw = "---\nid: " + g.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"parent: " + g.mapCardID + "\n" +
		"data:\n  exec:\n" +
		"    kind: toolcall\n" +
		"    callable: toast.show\n" +
		"    args: {}\n" +
		"---\n\nToolcall task body (no gate_card)."
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	_, err := e.Execute(g.ctx, req)
	if err == nil {
		t.Fatal("missing gate_card must reject")
	}
	if !strings.Contains(err.Error(), "data.exec.gate_card") {
		t.Fatalf("error must mention data.exec.gate_card, got: %v", err)
	}
}

// TestSecurityGateV2_GateDecisionNotApproved_RejectsExecute pins the
// approval-record check: gate card is done but task_outputs.decision is
// "rejected" or "pending" → executor rejects the mutating callable.
func TestSecurityGateV2_GateDecisionNotApproved_RejectsExecute(t *testing.T) {
	g := newSecurityGateFixture(t)
	g.gateCardBuilder = func() string {
		return "---\nid: " + g.gateCardID + "\ntype: task\nstatus: done\n" +
			"data:\n  task_outputs:\n    decision: rejected\n" +
			"---\n\nGate card body (rejected)."
	}
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	_, err := e.Execute(g.ctx, req)
	if err == nil {
		t.Fatal("rejected gate decision must reject")
	}
	if !strings.Contains(err.Error(), `decision is "rejected"`) {
		t.Fatalf("error must mention rejected decision, got: %v", err)
	}
}

// TestSecurityGateV2_Irreversible_Rejected pins the irreversible-effect
// rule: a callable flagged EffectKind=irreversible is always rejected by
// the toolcall executor, regardless of gate / stamp state.
func TestSecurityGateV2_Irreversible_Rejected(t *testing.T) {
	g := newSecurityGateFixture(t)
	// Override the topology: add an irreversible callable.
	g.a.topo = &fakeTopology{nodes: []appruntime.ActorNode{{
		ID: "toast-actor",
		Callables: []domain.CallableInterface{
			{
				Name:        "toast.state",
				Description: "query toast queue state",
				ServiceName: "toast",
				ReqSchemaID: schemaIDForTest(t, "ToastShowReq"),
			},
			{
				Name:        "toast.show",
				Description: "show a toast (mutating)",
				ServiceName: "toast",
				EffectKind:  "mutating",
			},
			{
				Name:        "toast.deleteAll",
				Description: "irreversible delete",
				ServiceName: "toast",
				EffectKind:  "irreversible",
			},
		},
	}}}
	// Build a bound card that points at the irreversible callable.
	g.boundCardRaw = "---\nid: " + g.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"parent: " + g.mapCardID + "\n" +
		"data:\n  exec:\n" +
		"    kind: toolcall\n" +
		"    callable: toast.deleteAll\n" +
		"    gate_card: " + g.gateCardID + "\n" +
		"    args: {}\n" +
		"---\n\nIrreversible toolcall task body."
	g.mutateProjectRef()

	e := newToolCallExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	_, err := e.Execute(g.ctx, req)
	if err == nil {
		t.Fatal("irreversible effect must be rejected")
	}
	if !strings.Contains(err.Error(), "irreversible") {
		t.Fatalf("error must mention irreversible, got: %v", err)
	}
	if g.toastInvoked {
		t.Error("toast.deleteAll must not be invoked under any gate configuration")
	}
}

// TestSecurityGateV2_StampWorkflowMutatingEffects_StampsToolcalls pins the
// stamp-helper side of the contract: given a map card whose scope includes
// mutating toolcall task cards with gate_card declared, StampWorkflowMutatingEffects
// writes the merged stamped_effects list back via project.wiki_edit_card.
//
// Pure-context helpers (StampWorkflowMutatingEffects, scanStampedCandidate,
// mergeStampedEffects) are also exercised by the table-driven subset below
// — those run without an actor fixture.
func TestSecurityGateV2_StampWorkflowMutatingEffects_StampsToolcalls(t *testing.T) {
	g := newSecurityGateFixture(t)
	// Map card with scope.include pointing at our bound toolcall card.
	g.mapCardBuilder = func() string {
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  scope:\n    include:\n      - " + g.boundTaskCardID + "\n" +
			"---\n\nMap card body."
	}
	g.mutateProjectRef()

	projectRef := g.fakeProjectRefFn(testutil.GenActorID())
	if err := StampWorkflowMutatingEffects(g.ctx, projectRef, g.mapCardID); err != nil {
		t.Fatalf("stamp failed: %v", err)
	}
	if g.editedMapRaw == "" {
		t.Fatal("stamp must call project.wiki_edit_card")
	}
	// The persisted raw must contain the task card ID under data.stamped_effects.
	if !strings.Contains(g.editedMapRaw, g.boundTaskCardID) {
		t.Errorf("edited map raw must reference %q; got:\n%s", g.boundTaskCardID, g.editedMapRaw)
	}
	if !strings.Contains(g.editedMapRaw, "toast.show") {
		t.Errorf("edited map raw must reference callable 'toast.show'; got:\n%s", g.editedMapRaw)
	}
	if !strings.Contains(g.editedMapRaw, g.gateCardID) {
		t.Errorf("edited map raw must reference gate card %q; got:\n%s", g.gateCardID, g.editedMapRaw)
	}
}

// TestSecurityGateV2_StampIdempotent pins the idempotency rule: running
// the stamp twice with the same scope must not duplicate entries (the
// last one wins by task_card_id).
func TestSecurityGateV2_StampIdempotent(t *testing.T) {
	g := newSecurityGateFixture(t)
	g.mapCardBuilder = func() string {
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  scope:\n    include:\n      - " + g.boundTaskCardID + "\n" +
			"---\n\nMap card body."
	}
	g.mutateProjectRef()

	projectRef := g.fakeProjectRefFn(testutil.GenActorID())
	if err := StampWorkflowMutatingEffects(g.ctx, projectRef, g.mapCardID); err != nil {
		t.Fatalf("stamp 1 failed: %v", err)
	}
	first := g.editedMapRaw
	// On the second pass, the fake serves the now-stamped map. After
	// re-running the stamp the helper must produce the same body shape
	// (or no-op when the merged set is identical) — either way, the
	// last call's wiki_edit_card payload must contain exactly one
	// stamped_effects entry for the bound task.
	g.mapCardBuilder = func() string { return first }
	if err := StampWorkflowMutatingEffects(g.ctx, projectRef, g.mapCardID); err != nil {
		t.Fatalf("stamp 2 failed: %v", err)
	}
	second := g.editedMapRaw
	if strings.Count(second, g.boundTaskCardID) != strings.Count(first, g.boundTaskCardID) {
		t.Errorf("idempotent stamp must not duplicate task entries: first=%d second=%d",
			strings.Count(first, g.boundTaskCardID), strings.Count(second, g.boundTaskCardID))
	}
}

// TestSecurityGateV2_MergeStampedEffects_Pure pins the pure merge helper
// independently from the actor fixture. No round-trip; in-memory only.
func TestSecurityGateV2_MergeStampedEffects_Pure(t *testing.T) {
	candidates := []stampedEffectEntry{
		{TaskCardID: "task-a", CallableID: "toast.show", GateCardID: "gate-1", StampedAt: "t1"},
		{TaskCardID: "task-b", CallableID: "toast.show", GateCardID: "gate-2", StampedAt: "t1"},
	}
	existing := map[string]any{
		"stamped_effects": []any{
			map[string]any{"task_card_id": "task-a", "callable_id": "toast.show", "gate_card_id": "gate-old", "stamped_at": "t0"},
			map[string]any{"task_card_id": "task-c", "callable_id": "toast.show", "gate_card_id": "gate-3", "stamped_at": "t0"},
		},
	}
	merged := mergeStampedEffects(existing, candidates)
	if len(merged) != 3 {
		t.Fatalf("merged len = %d, want 3", len(merged))
	}
	// task-a is overwritten by candidate.
	for _, m := range merged {
		if m.TaskCardID == "task-a" && m.GateCardID != "gate-1" {
			t.Errorf("task-a gate_card = %q, want gate-1 (fresh candidate must win)", m.GateCardID)
		}
	}
	// task-b (new) and task-c (existing, untouched) must both be present.
	seen := make(map[string]bool)
	for _, m := range merged {
		seen[m.TaskCardID] = true
	}
	for _, id := range []string{"task-a", "task-b", "task-c"} {
		if !seen[id] {
			t.Errorf("merged missing task %q", id)
		}
	}
}

// TestSecurityGateV2_StampedEffectsForCard_Pure pins the JSON / list
// shim: frontmatter stores data.stamped_effects as a JSON string, but a
// caller that already decoded it as []any must also work.
func TestSecurityGateV2_StampedEffectsForCard_Pure(t *testing.T) {
	rawJSON := `[{"task_card_id":"x","callable_id":"toast.show","gate_card_id":"g","stamped_at":"t"}]`
	fromString := stampedEffectsForCard(map[string]any{"stamped_effects": rawJSON})
	if len(fromString) != 1 || fromString[0]["callable_id"] != "toast.show" {
		t.Errorf("from string: %#v", fromString)
	}
	fromList := stampedEffectsForCard(map[string]any{
		"stamped_effects": []any{map[string]any{"task_card_id": "x", "callable_id": "toast.show"}},
	})
	if len(fromList) != 1 || fromList[0]["callable_id"] != "toast.show" {
		t.Errorf("from list: %#v", fromList)
	}
	if stampedEffectsForCard(nil) != nil {
		t.Error("nil data must return nil")
	}
}
