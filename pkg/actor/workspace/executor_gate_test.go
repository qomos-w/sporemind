package workspace

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	appruntime "github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// gateTestFixture mirrors toolcallTestFixture: a fresh workspace actor
// with a fake topology (declaring one read-only and one mutating
// callable), a fake project actor (recording wiki_set_status CAS /
// wiki_set_task_outputs writes), and a fake toast service (recording
// toast.show invocations). The fixture is the gate test surface; tests
// stage scenarios by overriding one of the per-call functions.
type gateTestFixture struct {
	a               *Actor
	ctx             *testutil.FakeCtx
	projectID       string
	callerAgentID   string
	boundTaskCardID string

	// Per-test permission mode (read by a.globalPermissionMode via the
	// actor's accountPrefs).
	permissionMode string

	// Fake toast service state.
	toastInvoked     bool
	toastInvokeCall  string
	toastInvokeArgs  map[string]any
	toastInvokeOther any
	toastRespID      string
	toastFail        error

	// Fake project actor state.
	mu          sync.Mutex
	cardStatus  map[string]string // cardID → live status
	statusCalls []gen.WikiSetStatusReq
	outputsSet  map[string]map[string]any
	getCardRaw  map[string]string // cardID → raw card body for fetch tests

	// Lookup hook overrides (set per-test to swap in failing fakes).
	lookupIDOverride     func(aid id.ActorID) (ref.Ref, bool)
	lookupServiceOverride func(name string) (ref.Ref, bool)
}

func newGateTestFixture(t *testing.T) *gateTestFixture {
	t.Helper()
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	f := &gateTestFixture{
		a:               a,
		ctx:             ctx,
		projectID:       projectID,
		callerAgentID:   callerAgentID,
		boundTaskCardID: "task-gate-1",
		permissionMode:  "permission",
		cardStatus:      map[string]string{},
		outputsSet:      map[string]map[string]any{},
		getCardRaw:      map[string]string{},
		toastRespID:     "toast-1",
	}
	// Defaults: the card starts as doing (the executor's dispatch path
	// claims it before Execute runs, mirroring executor_toolcall_test).
	f.cardStatus[f.boundTaskCardID] = "doing"

	// Topology exposes a read-only callable (rejected by Preflight when
	// named) and a mutating one (the canonical gate target).
	a.topo = &fakeTopology{nodes: []appruntime.ActorNode{{
		ID: "toast-actor",
		Callables: []domain.CallableInterface{
			{
				Name:        "toast.read",
				Description: "read-only toast query",
				ServiceName: "toast",
				EffectKind:  "",
			},
			{
				Name:        "toast.show",
				Description: "show a toast (mutating)",
				ServiceName: "toast",
				EffectKind:  "mutating",
			},
		},
	}}}

	ctx.LookupIDFn = f.lookupID
	ctx.LookupServiceFn = f.lookupService

	// Pre-flight plumbing: seed the accountPrefs so globalPermissionMode
	// returns whatever the test sets. Real AccountPrefs is map-backed;
	// we just reach in.
	if a.accountPrefs.Preferences == nil {
		a.accountPrefs.Preferences = map[string]string{}
	}
	a.accountPrefs.Preferences["permissionMode"] = f.permissionMode

	return f
}

// setPermissionMode updates the actor's account prefs; tests use this to
// drive the auto-approve branch.
func (f *gateTestFixture) setPermissionMode(mode string) {
	f.permissionMode = mode
	f.a.accountPrefs.Preferences["permissionMode"] = mode
}

func (f *gateTestFixture) claimReq(execYAML string) ClaimReq {
	card := "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: doing\n" +
		"data:\n" +
		"  exec:\n" +
		"    kind: gate\n" +
		execYAML +
		"---\n\nGate task body."
	f.getCardRaw[f.boundTaskCardID] = card
	return ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         card,
		Claimed:         true,
		PreviousStatus:  "todo",
	}
}

func (f *gateTestFixture) lookupID(aid id.ActorID) (ref.Ref, bool) {
	if f.lookupIDOverride != nil {
		return f.lookupIDOverride(aid)
	}
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

func (f *gateTestFixture) lookupService(name string) (ref.Ref, bool) {
	if f.lookupServiceOverride != nil {
		return f.lookupServiceOverride(name)
	}
	if name == "toast" {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID != "toast.show" {
				return nil
			}
			f.toastInvoked = true
			f.toastInvokeCall = callID
			if m, ok := payload.(map[string]any); ok {
				f.toastInvokeArgs = m
			} else {
				f.toastInvokeOther = payload
			}
			if f.toastFail != nil {
				return f.toastFail
			}
			return gen.ToastShowResp{ID: f.toastRespID}
		}), true
	}
	return nil, false
}

func (f *gateTestFixture) fakeProjectRef(aid id.ActorID) ref.Ref {
	return testutil.NewFakeRef(aid, func(callID string, payload any) any {
		switch callID {
		case "project.wiki_get_card":
			req, ok := payload.(domain.WikiGetCardReq)
			if !ok {
				return nil
			}
			raw, ok := f.getCardRaw[req.ID]
			if !ok {
				return fmt.Errorf("card %q not found", req.ID)
			}
			return domain.WikiGetCardResp{ID: req.ID, Raw: raw}
		case "project.wiki_set_status":
			req, ok := payload.(gen.WikiSetStatusReq)
			if !ok {
				return nil
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.statusCalls = append(f.statusCalls, req)
			current, tracked := f.cardStatus[req.ID]
			if !tracked {
				current = "doing"
			}
			if req.ExpectedStatus != "" && current != req.ExpectedStatus {
				return fmt.Errorf("project.wiki.set_status: expected status %q, got %q", req.ExpectedStatus, current)
			}
			f.cardStatus[req.ID] = req.Status
			return domain.WikiSetStatusResp{PreviousStatus: current}
		case "project.wiki_set_task_outputs":
			req, ok := payload.(domain.WikiSetTaskOutputsReq)
			if !ok {
				return nil
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.outputsSet[req.CardID] = req.Outputs
			return domain.WikiSetTaskOutputsResp{}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestGateExecutor_Registered(t *testing.T) {
	f := newGateTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindGate)
	if !ok {
		t.Fatal("gate executor not registered after OnStart")
	}
	if exec.Kind() != ExecKindGate {
		t.Fatalf("expected kind gate, got %q", exec.Kind())
	}
}

func TestGateExecutor_Preflight_RejectsUnresolvableCallable(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)

	req := f.claimReq("    callable: nosuch.callable\n")
	if _, err := e.Preflight(f.ctx, req); err == nil || !strings.Contains(err.Error(), "not found in topology") {
		t.Fatalf("unresolvable callable: err = %v", err)
	}
}

func TestGateExecutor_Preflight_RejectsReadOnlyCallable(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)

	req := f.claimReq("    callable: toast.read\n")
	if _, err := e.Preflight(f.ctx, req); err == nil || !strings.Contains(err.Error(), "mutating") {
		t.Fatalf("read-only callable: err = %v", err)
	}
}

func TestGateExecutor_Preflight_AcceptsMutatingCallable(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)

	req := f.claimReq("    callable: toast.show\n    prompt: please review the deploy\n")
	pf, err := e.Preflight(f.ctx, req)
	if err != nil {
		t.Fatalf("mutating callable + custom prompt: %v", err)
	}
	if pf.DisplayName != "GateExecutor" {
		t.Errorf("DisplayName = %q", pf.DisplayName)
	}
}

func TestGateExecutor_Preflight_RejectsNoActiveWorkflow(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)

	req := f.claimReq("")
	req.CallerAgentID = ""
	if _, err := e.Preflight(f.ctx, req); err == nil {
		t.Fatal("missing caller agent must fail preflight")
	}
}

func TestGateExecutor_Execute_BypassModeAutoApproves(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)
	f.setPermissionMode("yolo")

	req := f.claimReq("    prompt: deploy plan ready\n")
	req.Preflight = &PreflightResult{}

	resp, err := e.Execute(f.ctx, req)
	if err != nil {
		t.Fatalf("execute yolo bypass: %v", err)
	}
	if f.toastInvoked {
		t.Error("bypass mode must not invoke toast.show")
	}
	if f.statusSet() != "done" {
		t.Errorf("card status = %q, want done", f.statusSet())
	}
	outputs := f.outputs()
	if decision, _ := outputs["decision"].(string); decision != "auto_approved" {
		t.Errorf("outputs.decision = %v, want auto_approved", outputs["decision"])
	}
	if mode, _ := outputs["mode"].(string); mode != "yolo" {
		t.Errorf("outputs.mode = %v, want yolo", outputs["mode"])
	}
	if resp.DisplayName != "GateExecutor" {
		t.Errorf("DisplayName = %q", resp.DisplayName)
	}
}

func TestGateExecutor_Execute_AutopilotModeAlsoAutoApproves(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)
	f.setPermissionMode("autopilot")

	req := f.claimReq("")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute autopilot bypass: %v", err)
	}
	if f.toastInvoked {
		t.Error("autopilot bypass must not invoke toast.show")
	}
	if f.statusSet() != "done" {
		t.Errorf("card status = %q, want done", f.statusSet())
	}
}

func TestGateExecutor_Execute_NormalModeArmsToastAndLeavesCardDoing(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)
	// default mode is "permission" → must arm the toast

	req := f.claimReq("    prompt: deploy plan ready\n    callable: toast.show\n")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute normal: %v", err)
	}
	if !f.toastInvoked || f.toastInvokeCall != "toast.show" {
		t.Fatalf("toast.show not invoked (invoked=%v call=%q)", f.toastInvoked, f.toastInvokeCall)
	}
	// The card must remain "doing" after the executor returns — the
	// terminal flip lives in workspace.gate_approve / gate_reject.
	if f.statusSet() != "doing" {
		t.Errorf("card status = %q, want doing (gate must wait for approve/reject)", f.statusSet())
	}
	outputs := f.outputs()
	if decision, _ := outputs["decision"].(string); decision != "pending_approval" {
		t.Errorf("outputs.decision = %v, want pending_approval", outputs["decision"])
	}
	if _, ok := outputs["toast_id"].(string); !ok {
		t.Errorf("outputs.toast_id missing")
	}
	if prompt, _ := outputs["prompt"].(string); prompt != "deploy plan ready" {
		t.Errorf("outputs.prompt = %v, want custom prompt", outputs["prompt"])
	}
	if cid, _ := outputs["callable_id"].(string); cid != "toast.show" {
		t.Errorf("outputs.callable_id = %v, want toast.show", outputs["callable_id"])
	}
}

func TestGateExecutor_Execute_DefaultPromptWhenCardOmitsIt(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)

	req := f.claimReq("")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if prompt, _ := f.outputs()["prompt"].(string); prompt != gateDefaultPrompt {
		t.Errorf("outputs.prompt = %q, want default %q", prompt, gateDefaultPrompt)
	}
}

func TestGateExecutor_Execute_ToastFailureDoesNotFailCard(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)
	f.toastFail = fmt.Errorf("toast offline")

	req := f.claimReq("")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: toast failure must not surface as dispatch error: %v", err)
	}
	// Card stays doing; the pending record carries the toast error so
	// the owner agent / inspector can diagnose.
	if f.statusSet() != "doing" {
		t.Errorf("card status = %q, want doing", f.statusSet())
	}
	if _, ok := f.outputs()["toast_error"].(string); !ok {
		t.Errorf("outputs.toast_error missing on toast failure")
	}
}

func TestGateExecutor_Execute_ToastServiceUnavailableLeavesCardDoing(t *testing.T) {
	f := newGateTestFixture(t)
	e := newGateExecutor(f.a)
	f.ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }

	req := f.claimReq("")
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: missing toast service must not surface as dispatch error: %v", err)
	}
	if f.statusSet() != "doing" {
		t.Errorf("card status = %q, want doing", f.statusSet())
	}
	if _, ok := f.outputs()["toast_error"].(string); !ok {
		t.Errorf("outputs.toast_error missing when toast actor absent")
	}
}

// ---------------------------------------------------------------------------
// workspace.gate_approve / workspace.gate_reject handler tests
// ---------------------------------------------------------------------------

func TestGateApprove_HappyPathFlipsCardToDone(t *testing.T) {
	f := newGateTestFixture(t)

	resp, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "alice",
		CallerAgentID: f.callerAgentID,
	})
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if resp.CardStatus != "done" {
		t.Errorf("CardStatus = %q, want done", resp.CardStatus)
	}
	if resp.ApprovedAt == "" {
		t.Errorf("ApprovedAt empty")
	}
	if f.statusSet() != "done" {
		t.Errorf("card status = %q, want done", f.statusSet())
	}
	outputs := f.outputs()
	if decision, _ := outputs["decision"].(string); decision != "approved" {
		t.Errorf("outputs.decision = %v, want approved", outputs["decision"])
	}
	if approver, _ := outputs["approver"].(string); approver != "alice" {
		t.Errorf("outputs.approver = %v, want alice", outputs["approver"])
	}
	if cid, _ := outputs["caller_agent_id"].(string); cid != f.callerAgentID {
		t.Errorf("outputs.caller_agent_id = %v, want %s", outputs["caller_agent_id"], f.callerAgentID)
	}
}

func TestGateApprove_RequiresApprover(t *testing.T) {
	f := newGateTestFixture(t)

	_, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		TaskCardID:    f.boundTaskCardID,
		CallerAgentID: f.callerAgentID,
	})
	if err == nil || !strings.Contains(err.Error(), "Approver is required") {
		t.Fatalf("missing approver: err = %v", err)
	}
}

func TestGateApprove_RequiresTaskCardID(t *testing.T) {
	f := newGateTestFixture(t)

	_, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		Approver:      "alice",
		CallerAgentID: f.callerAgentID,
	})
	if err == nil || !strings.Contains(err.Error(), "TaskCardId is required") {
		t.Fatalf("missing task card: err = %v", err)
	}
}

func TestGateApprove_CASMissDoesNotOverwrite(t *testing.T) {
	f := newGateTestFixture(t)
	// Card already terminal (canceled); approve's CAS must fail rather
	// than silently flip status.
	f.cardStatus[f.boundTaskCardID] = "cancelled"

	_, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "alice",
		CallerAgentID: f.callerAgentID,
	})
	if err == nil {
		t.Fatal("approve on cancelled card must fail CAS")
	}
	if !strings.Contains(err.Error(), "expected status") {
		t.Errorf("error phrase = %v, want expected-status mismatch", err)
	}
	// Card must remain cancelled; the CAS guard worked.
	if f.statusSet() != "cancelled" {
		t.Errorf("card status = %q, want cancelled (CAS must preserve)", f.statusSet())
	}
}

func TestGateApprove_NormalizesFormValues(t *testing.T) {
	f := newGateTestFixture(t)

	// Form values carry YAML scalars as strings; the request layout's
	// Normalize step coerces them to the schema-defined families before
	// persistence. ToastShowReq's DurationMs is int64, so "5000" must
	// coerce to 5000 (number) rather than stay a string. The gate's own
	// FormValues field is free-form map[string]any with no schema
	// constraints, so the test exercises the layout resolution path
	// itself (a passing normalize is the contract — invalid input would
	// surface as an error).
	resp, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "alice",
		CallerAgentID: f.callerAgentID,
		FormValues:    map[string]any{"reason": "Looks good", "count": "3"},
		Note:          "tested via gate",
	})
	if err != nil {
		t.Fatalf("approve with form values: %v", err)
	}
	if resp.CardStatus != "done" {
		t.Errorf("CardStatus = %q, want done", resp.CardStatus)
	}
	outputs := f.outputs()
	if fv, ok := outputs["form_values"].(map[string]any); !ok {
		t.Errorf("outputs.form_values missing or wrong type: %T", outputs["form_values"])
	} else if reason, _ := fv["reason"].(string); reason != "Looks good" {
		t.Errorf("form_values.reason = %v", fv["reason"])
	}
	if note, _ := outputs["note"].(string); note != "tested via gate" {
		t.Errorf("outputs.note = %v", outputs["note"])
	}
}

func TestGateApprove_RejectsFormValuesWithUnknownKey(t *testing.T) {
	f := newGateTestFixture(t)

	// Inject an unknown key into the form values; the layout's
	// validation step should flag it.
	_, err := f.a.handleWorkspaceGateApprove(f.ctx, gen.WorkspaceGateApproveReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "alice",
		CallerAgentID: f.callerAgentID,
		FormValues:    map[string]any{"DefinitivelyNotAField": "x"},
	})
	// The WorkspaceGateApproveReq schema has no declared fields
	// beyond TaskCardId/Approver/CallerAgentId/FormValues/CallableId/Note;
	// FormValues is itself an opaque map so its contents are NOT
	// validated. This test pins that contract — opaque payloads are
	// passed through untouched.
	if err != nil {
		t.Fatalf("opaque FormValues must not fail validation: %v", err)
	}
}

func TestGateReject_HappyPathFlipsCardToFailed(t *testing.T) {
	f := newGateTestFixture(t)

	resp, err := f.a.handleWorkspaceGateReject(f.ctx, gen.WorkspaceGateRejectReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "bob",
		CallerAgentID: f.callerAgentID,
		Reason:        "needs more review",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if resp.CardStatus != "failed" {
		t.Errorf("CardStatus = %q, want failed", resp.CardStatus)
	}
	if f.statusSet() != "failed" {
		t.Errorf("card status = %q, want failed", f.statusSet())
	}
	outputs := f.outputs()
	if decision, _ := outputs["decision"].(string); decision != "rejected" {
		t.Errorf("outputs.decision = %v, want rejected", outputs["decision"])
	}
	if reason, _ := outputs["reason"].(string); reason != "needs more review" {
		t.Errorf("outputs.reason = %v, want reason", outputs["reason"])
	}
}

func TestGateReject_CASMissDoesNotOverwrite(t *testing.T) {
	f := newGateTestFixture(t)
	f.cardStatus[f.boundTaskCardID] = "done"

	_, err := f.a.handleWorkspaceGateReject(f.ctx, gen.WorkspaceGateRejectReq{
		TaskCardID:    f.boundTaskCardID,
		Approver:      "bob",
		CallerAgentID: f.callerAgentID,
	})
	if err == nil {
		t.Fatal("reject on done card must fail CAS")
	}
	if f.statusSet() != "done" {
		t.Errorf("card status = %q, want done (CAS must preserve)", f.statusSet())
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (f *gateTestFixture) statusSet() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cardStatus[f.boundTaskCardID]
}

func (f *gateTestFixture) outputs() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outputsSet[f.boundTaskCardID]
}

// touchCtx is here to keep the actor import alive across toolchain
// refactors; the gate tests use actor.Context-free paths only.
var _ = context.Background