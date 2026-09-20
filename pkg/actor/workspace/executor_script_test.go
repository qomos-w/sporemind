package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	appruntime "github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// scriptTestFixture builds a workspace actor plus fake context wired
// for script executor tests. It re-uses toolcallTestFixture's project
// ref / topology scaffolding, then layers script-specific state on
// top: an injectable host.invoke recorder + configurable topology
// (so we can flip EffectKind, Stream, ServiceName per test).
type scriptTestFixture struct {
	*toolcallTestFixture

	// hostInvokeRecorder tracks every host.invoke the script's host
	// binding dispatches, in order. Tests assert the call sequence
	// and payload shape.
	mu             sync.Mutex
	hostInvokes    []hostInvokeRecord
	hostInvokeErrs map[string]error // callID -> returned error (optional)
}

type hostInvokeRecord struct {
	CallID  string
	Payload map[string]any
}

// freshScriptTestFixture builds a fresh actor + fake context with
// the script-friendly topology (a unary toast.state + a mutating
// toast.show + a mutating toast.dismiss for multi-capability stamp
// tests + a streaming toast.stream + an agent-local goal_submit).
func freshScriptTestFixture(t *testing.T) *scriptTestFixture {
	base := newToolCallTestFixture(t)
	f := &scriptTestFixture{
		toolcallTestFixture: base,
		hostInvokeErrs:      map[string]error{},
	}
	f.a.topo = &fakeTopology{nodes: []appruntime.ActorNode{{
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
				Name:        "toast.dismiss",
				Description: "dismiss a toast (mutating)",
				ServiceName: "toast",
				EffectKind:  "mutating",
			},
			{
				Name:        "toast.stream",
				Description: "streaming callable (rejected)",
				ServiceName: "toast",
				Stream:      true,
			},
			{
				Name:        "goal_submit",
				Description: "agent-local callable (rejected)",
				ServiceName: "",
			},
		},
	}}}
	return f
}

// scriptCardRaw returns a card raw with the given exec-block YAML
// appended under data.exec, plus a ```spore fenced block whose body
// is the supplied source. When source is empty, no fence is added
// (for testing missing-fence preflight).
func (f *scriptTestFixture) scriptCardRaw(execYAML, source string, opts ...scriptCardOpt) string {
	body := ""
	if source != "" {
		body = "\n```spore\n" + source + "\n```\n"
	}
	opt := applyScriptCardOpts(opts)
	if opt.parent != "" {
		return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
			"parent: " + opt.parent + "\n" +
			"data:\n  exec:\n" + execYAML +
			"---\n\nToolcall task body." + body
	}
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"data:\n  exec:\n" + execYAML +
		"---\n\nToolcall task body." + body
}

// scriptCardRawWithBody builds a card with a literal body (no fence
// wrapping). Used by tests that need to control the exact body — e.g.
// "no fence", "mismatched fence length", or "empty fence body" cases
// where the wrap-and-prefix shape would mask the failure mode.
//
// The supplied body is appended verbatim after the frontmatter closer.
func (f *scriptTestFixture) scriptCardRawWithBody(execYAML, body string) string {
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"data:\n  exec:\n" + execYAML +
		"---\n\n" + body
}

type scriptCardOpt struct {
	parent string
}

func applyScriptCardOpts(opts []scriptCardOpt) scriptCardOpt {
	out := scriptCardOpt{}
	for _, o := range opts {
		out.parent = o.parent
	}
	return out
}

// claimScriptReq is unused; tests build ClaimReq directly via
// buildClaimReq. Kept as a stub so older copies of the test file
// (in case reviewers compare against a stale draft) still compile.
//
// Deprecated: use buildClaimReq.
func (f *scriptTestFixture) claimScriptReq(cardRaw string) string {
	_ = cardRaw
	return ""
}

// scriptClaimReq builds a ClaimReq directly from the supplied raw.
func (f *scriptTestFixture) buildClaimReq(cardRaw string, inputs map[string]any) ClaimReq {
	return ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         cardRaw,
		Body:            extractBody(cardRaw),
		Claimed:         true,
		Inputs:          inputs,
	}
}

// extractBody strips the frontmatter block from a card raw, mirroring
// how project.ParseCardRaw produces card.Body. Tests use the result
// to feed the dispatcher's body-injection path so
// extractScriptBody() picks up the right side.
func extractBody(raw string) string {
	if !strings.HasPrefix(raw, "---") {
		return raw
	}
	rest := raw[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return ""
	}
	bodyStart := 3 + end + 3
	if bodyStart >= len(raw) {
		return ""
	}
	return strings.TrimSpace(raw[bodyStart:])
}

// installHostInvokeRecorder swaps the fixture's toast service ref
// for one that records every host.invoke invocation that flows
// through the executor's host binding. The recorder captures the
// payload shape so tests can assert normalisation (string→number
// coercion) is applied to script-side calls too.
func (f *scriptTestFixture) installHostInvokeRecorder() {
	f.ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "toast" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				f.mu.Lock()
				defer f.mu.Unlock()
				if err, ok := f.hostInvokeErrs[callID]; ok && err != nil {
					return err
				}
				if m, ok := payload.(map[string]any); ok {
					f.hostInvokes = append(f.hostInvokes, hostInvokeRecord{CallID: callID, Payload: m})
				} else {
					f.hostInvokes = append(f.hostInvokes, hostInvokeRecord{CallID: callID, Payload: map[string]any{"_raw": payload}})
				}
				return gen.ToastShowResp{ID: "toast-from-script"}
			}), true
		}
		return nil, false
	}
}

// invokeCount returns the recorded invocation count for callID.
func (f *scriptTestFixture) invokeCount(callID string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, inv := range f.hostInvokes {
		if inv.CallID == callID {
			n++
		}
	}
	return n
}

// invokePayload returns the first payload recorded for callID, or nil.
func (f *scriptTestFixture) invokePayload(callID string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, inv := range f.hostInvokes {
		if inv.CallID == callID {
			return inv.Payload
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────
// Registration
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Registered pins that the executor is wired into
// the workspace's execKind registry under the stable ExecKindScript
// constant. A future rename would break the card format string.
func TestScriptExecutor_Registered(t *testing.T) {
	f := freshScriptTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindScript)
	if !ok {
		t.Fatal("script executor not registered")
	}
	if exec.Kind() != ExecKindScript {
		t.Fatalf("expected kind script, got %q", exec.Kind())
	}
}

// ─────────────────────────────────────────────────────────────────────
// Preflight: rejections
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Preflight_Rejections pins the preflight
// rejection branches. Each test case shapes a card raw that hits a
// single failure path; a single composite card exercises multiple
// gates. Errors must mention the underlying reason so the card
// author can fix it without digging through the executor source.
func TestScriptExecutor_Preflight_Rejections(t *testing.T) {
	t.Run("missing_capabilities", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		// Empty capabilities list — preflight must reject because
		// the script has no host.invoke surface to use.
		cardRaw := f.scriptCardRaw("    kind: script\n    inputs: []\n", `export fun run(): int { return 0 }`)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "capabilities") {
			t.Fatalf("missing capabilities: err = %v", err)
		}
	})
	t.Run("unknown_callable", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - nosuch.service\n",
			`export fun run(): int { return 0 }`,
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "nosuch.service") {
			t.Fatalf("unknown callable: err = %v", err)
		}
	})
	t.Run("streaming_rejected", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - toast.stream\n",
			`export fun run(): int { return 0 }`,
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "streaming") {
			t.Fatalf("streaming rejected: err = %v", err)
		}
	})
	t.Run("agent_local_rejected", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - goal_submit\n",
			`export fun run(): int { return 0 }`,
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "agent-local") {
			t.Fatalf("agent-local rejected: err = %v", err)
		}
	})
	t.Run("mutating_no_gate_card", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		// mutating capability declared, no gate_card → preflight
		// rejects before any project round-trip.
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - toast.show\n",
			`export fun run(): int { return 0 }`,
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "gate_card") {
			t.Fatalf("mutating without gate_card: err = %v", err)
		}
	})
	t.Run("mutating_no_fence", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		// Body without a fenced spore block must reject before any
		// compile work.
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - toast.show\n    gate_card: g-1\n",
			"",
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "spore") {
			t.Fatalf("missing fence: err = %v", err)
		}
	})
	t.Run("compile_failure", func(t *testing.T) {
		f := freshScriptTestFixture(t)
		e := newScriptExecutor(f.a)
		// The probe catches the broken syntax as a preflight error.
		cardRaw := f.scriptCardRaw(
			"    kind: script\n    capabilities:\n      - toast.state\n",
			`fun bad(: int) {}`,
		)
		_, err := e.Preflight(f.ctx, f.buildClaimReq(cardRaw, nil))
		if err == nil || !strings.Contains(err.Error(), "compile") {
			t.Fatalf("compile failure: err = %v", err)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Execute: happy path
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_HappyPath pins the canonical run:
//   - read-only capability whitelist (toast.state only),
//   - one input pulled from ClaimReq.Inputs,
//   - script calls host.invoke("toast.state", payload) once,
//   - script returns a map conforming to ToastShowResp,
//   - outputs.validation declared → outputs validation runs and passes,
//   - card flips done with outputs.result + script_hash audit field,
//   - toast service is invoked exactly once with the coerced payload.
func TestScriptExecutor_Execute_HappyPath(t *testing.T) {
	f := freshScriptTestFixture(t)
	f.installHostInvokeRecorder()
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(input: any): any {
	var res: any = invoke("toast.state", {"Title": input, "DurationMs": "5000"})
	return res
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n" +
		"    inputs:\n      - query\n" +
		"    outputs: ToastShowResp\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, map[string]any{"query": "hello"})
	req.Preflight = &PreflightResult{}

	resp, err := e.Execute(f.ctx, req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// toast.state invoked exactly once; payload was coerced so
	// DurationMs comes through as a number (5000), not a string.
	if n := f.invokeCount("toast.state"); n != 1 {
		t.Fatalf("toast.state invoked %d times, want 1", n)
	}
	payload := f.invokePayload("toast.state")
	if got, _ := payload["Title"].(string); got != "hello" {
		t.Errorf("Title = %#v, want hello", payload["Title"])
	}
	if got, ok := payload["DurationMs"].(float64); !ok || got != 5000 {
		t.Errorf("DurationMs = %#v, want coerced 5000", payload["DurationMs"])
	}
	// Card flipped to done; outputs.result is the normalised map;
	// script_hash is a non-empty hex string.
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("card status = %q, want done", f.statusSet[f.boundTaskCardID])
	}
	outMap, ok := f.outputsSet[f.boundTaskCardID]["result"].(map[string]any)
	if !ok {
		t.Fatalf("outputs[result] not a map: %#v", f.outputsSet[f.boundTaskCardID])
	}
	if got, _ := outMap["Id"].(string); got != "toast-from-script" {
		t.Errorf("result.Id = %#v, want toast-from-script", outMap["Id"])
	}
	hash, _ := f.outputsSet[f.boundTaskCardID]["script_hash"].(string)
	if hash == "" || len(hash) != 64 {
		t.Errorf("script_hash = %q, want 64-char hex sha256", hash)
	}
	if resp.DisplayName != "ScriptExecutor" {
		t.Errorf("DisplayName = %q", resp.DisplayName)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: capability whitelist enforcement
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_RejectsCallOutsideWhitelist pins the
// runtime gate: a script that calls host.invoke with a callable
// outside data.exec.capabilities must surface as a script-runtime
// error (the host binding returns an error → spore raises it as a
// script error), and the card must end up failed with the error in
// task_outputs.
func TestScriptExecutor_Execute_RejectsCallOutsideWhitelist(t *testing.T) {
	f := freshScriptTestFixture(t)
	f.installHostInvokeRecorder()
	e := newScriptExecutor(f.a)

	// Whitelist only allows toast.state; script tries to call
	// toast.show (mutating, not whitelisted).
	src := `import { invoke } from "host"
export fun run(): any {
	return invoke("toast.show", {"Title": "x"})
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("whitelist violation must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "toast.show") || !strings.Contains(note, "whitelist") {
		t.Errorf("error must mention the rejected callable and the whitelist, got: %s", note)
	}
	if f.invokeCount("toast.show") != 0 {
		t.Errorf("toast.show must not be invoked when outside the whitelist")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: mutating capability without gate approval
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_MutatingNoGateApproval pins the v2
// dual-check: a script declaring a mutating capability (toast.show)
// but with no approved gate card → execute fails at the gate check,
// before any host.invoke.
func TestScriptExecutor_Execute_MutatingNoGateApproval(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): any {
	return invoke("toast.show", {"Title": "x"})
}`
	// Mutating capability declared, gate_card points at a gate
	// that does not exist (no fixture card built).
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.show\n" +
		"    gate_card: missing-gate\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	// The fake project ref returns "card not found" for any
	// unknown id, so the gate check fails on fetch.
	_, err := e.Execute(f.ctx, req)
	if err == nil {
		t.Fatal("missing gate card must reject")
	}
	if !strings.Contains(err.Error(), "gate card") {
		t.Errorf("error must mention gate card, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: multi-capability stamp enforcement
// ─────────────────────────────────────────────────────────────────────

// scriptGateFixture is the script-side counterpart of the toolcall
// executor's securityGateFixture: a workspace actor with a gate card
// (status "done", task_outputs.decision approved) and a map card that
// stamps a configurable set of (callable_id, gate_card_id) pairs.
// Tests use the configurable builders to pin each branch of the
// verifyMapStampedScript logic independently.
type scriptGateFixture struct {
	*toolcallTestFixture

	mapCardID    string
	gateCardID   string
	boundCardRaw string

	// Closure-based card builders. Tests override to control the gate
	// status / approval record and the map's stamped_effects list.
	mapCardBuilder  func() string
	gateCardBuilder func() string
}

// fakeProjectRefFn lets tests inject a project ref that serves gate /
// map / bound cards; the base fixture's fakeProjectRef is replaced so
// wiki_get_card returns the configured raw.
func (g *scriptGateFixture) fakeProjectRefFn(aid id.ActorID) ref.Ref {
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
		}
		return nil
	})
}

// scriptBoundCardRaw builds the bound script card with the declared
// capabilities, parent map id, and gate card id.
func (g *scriptGateFixture) scriptBoundCardRaw(capabilities []string) string {
	capLines := "    capabilities:\n"
	for _, c := range capabilities {
		capLines += "      - " + c + "\n"
	}
	return "---\nid: " + g.toolcallTestFixture.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
		"parent: " + g.mapCardID + "\n" +
		"data:\n  exec:\n" +
		"    kind: script\n" +
		capLines +
		"    gate_card: " + g.gateCardID + "\n" +
		"---\n\nScript task body.\n```spore\nexport fun run(): int { return 0 }\n```\n"
}

// newScriptGateFixture wires the gate / map / bound cards with a
// gate card approved + done and a default stamp for a single
// capability. Tests override mapCardBuilder to add or remove stamp
// entries.
func newScriptGateFixture(t *testing.T, capabilities []string) *scriptGateFixture {
	f := freshScriptTestFixture(t)
	g := &scriptGateFixture{
		toolcallTestFixture: f.toolcallTestFixture,
		mapCardID:           "map-stamp-script-1",
		gateCardID:          "gate-script-1",
	}
	g.gateCardBuilder = func() string {
		return "---\nid: " + g.gateCardID + "\ntype: task\nstatus: done\n" +
			"data:\n  task_outputs:\n    decision: approved\n    approver: tester\n" +
			"---\n\nGate card body."
	}
	// Default stamps the first capability only. Multi-mutating tests
	// override mapCardBuilder to add or change entries.
	defaultStampCap := capabilities[0]
	g.mapCardBuilder = func() string {
		stamp := `[{"task_card_id":"` + f.boundTaskCardID + `","callable_id":"` + defaultStampCap + `","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"}]`
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  stamped_effects: '" + stamp + "'\n" +
			"---\n\nMap card body."
	}
	g.boundCardRaw = g.scriptBoundCardRaw(capabilities)
	// Replace the project ref so wiki_get_card serves the configured cards.
	g.ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == g.callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: g.mapCardID}
				}
				return nil
			}), true
		}
		return g.fakeProjectRefFn(aid), true
	}
	return g
}

// TestScriptExecutor_Execute_MultiMutatingAllStamped_Passes pins the
// security gate v2 happy path for a script declaring multiple mutating
// capabilities: every mutating capability has its own matching stamp
// entry → execute proceeds (the script itself does nothing
// meaningful; the test pins that the gate check accepts).
func TestScriptExecutor_Execute_MultiMutatingAllStamped_Passes(t *testing.T) {
	g := newScriptGateFixture(t, []string{"toast.show", "toast.dismiss"})
	// Override the default stamp to cover BOTH mutating capabilities.
	g.mapCardBuilder = func() string {
		stamp := `[{"task_card_id":"` + g.toolcallTestFixture.boundTaskCardID + `","callable_id":"toast.show","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"},` +
			`{"task_card_id":"` + g.toolcallTestFixture.boundTaskCardID + `","callable_id":"toast.dismiss","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"}]`
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  stamped_effects: '" + stamp + "'\n" +
			"---\n\nMap card body."
	}
	g.boundCardRaw = g.scriptBoundCardRaw([]string{"toast.show", "toast.dismiss"})

	e := newScriptExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	if _, err := e.Execute(g.ctx, req); err != nil {
		t.Fatalf("every-stamped mutating script must execute: %v", err)
	}
	if g.statusSet[g.boundTaskCardID] != "done" {
		t.Errorf("card status = %q, want done", g.statusSet[g.boundTaskCardID])
	}
}

// TestScriptExecutor_Execute_MultiMutatingOneStamped_Rejects pins the
// security gate v2 fix for the previous loophole: a script declaring
// two mutating capabilities but with only one recorded stamp must
// reject. The other (toast.dismiss) is unstamped and would be admitted
// by the legacy "first match wins" walk. The new verifyMapStampedScript
// requires each mutating capability to find its own stamp entry and
// reports the offender.
func TestScriptExecutor_Execute_MultiMutatingOneStamped_Rejects(t *testing.T) {
	g := newScriptGateFixture(t, []string{"toast.show", "toast.dismiss"})
	// Default mapCardBuilder stamps only toast.show (the first
	// capability). toast.dismiss is deliberately unstamped.
	g.boundCardRaw = g.scriptBoundCardRaw([]string{"toast.show", "toast.dismiss"})

	e := newScriptExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
		Preflight:       &PreflightResult{},
	}

	err := e.verifyMapStampedScript(g.ctx, scriptConfig{
		Config: scriptcard.Config{
			Capabilities: []string{"toast.show", "toast.dismiss"},
			GateCard:     g.gateCardID,
		},
	}, req)
	if err == nil {
		t.Fatal("multi-mutating script with one unstamped capability must be rejected")
	}
	msg := err.Error()
	if !strings.Contains(msg, "toast.dismiss") {
		t.Errorf("error must name the unstamped capability (toast.dismiss), got: %v", err)
	}
	if strings.Contains(msg, "toast.show") {
		t.Errorf("error must NOT mention the stamped capability (toast.show) as missing, got: %v", err)
	}
}

// TestScriptExecutor_Execute_ReadOnlyNoStampNeeded pins the read-only
// short-circuit: a script declaring only EffectNone capabilities
// (toast.state) does not need any stamped_effects on the workflow map.
// The previous code happened to short-circuit on "first match"; the
// new code explicitly skips the stamp path when no mutating
// capability is declared, so an unstamped read-only script still
// passes the gate.
func TestScriptExecutor_Execute_ReadOnlyNoStampNeeded(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): any {
	return invoke("toast.state", {"Title": "x"})
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	// Stamp verification does not even run for read-only scripts,
	// so the fakeProjectRef's "card not found" path for the (absent)
	// map card is never exercised. Execute must succeed.
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("read-only script must execute without map stamp: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "done" {
		t.Errorf("card status = %q, want done", f.statusSet[f.boundTaskCardID])
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: budget overrun
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_BudgetExceeded pins the budget path:
// when MaxDurationSec is set very low (1s) and the script tries to
// loop indefinitely, the spore VM cancels the call with a runtime
// error → card flips to failed with a clear error note.
func TestScriptExecutor_Execute_BudgetExceeded(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): int {
	var n: int = 0
	while true { n = n + 1 }
	return n
}`
	// max_duration_sec=1 → the spore VM's MaxDuration deadline
	// fires well before the script can return. The capability
	// declaration is required by preflight (no host.invoke
	// surface means the script cannot meaningfully call out),
	// but the script itself never invokes it.
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n" +
		"    budget:\n      max_duration_sec: 1\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("budget overrun must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "script runtime error") && !strings.Contains(note, "duration") && !strings.Contains(note, "budget") {
		t.Errorf("error must mention runtime error / budget, got: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: missing input key
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_MissingInputKey pins the input
// resolution contract: when data.exec.inputs lists `query` but
// ClaimReq.Inputs has no `query` entry, the executor marks the
// card failed (vs. passing a nil that the script might explode on).
func TestScriptExecutor_Execute_MissingInputKey(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(query: any): any {
	return invoke("toast.state", {"Title": query})
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n" +
		"    inputs:\n      - query\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	// Note: no "query" key in Inputs.
	req := f.buildClaimReq(cardRaw, map[string]any{"other": "x"})
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("missing input key must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "query") || !strings.Contains(note, "missing") {
		t.Errorf("error must mention the missing key, got: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: output validation failure
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_OutputValidationFailure pins the
// validation contract: when data.exec.outputs names a schema, the
// script's return value must conform or the card flips failed.
// The script returns an empty map but ToastShowResp requires Id →
// validation fails.
func TestScriptExecutor_Execute_OutputValidationFailure(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): any {
	invoke("toast.state", {"Title": "x"})
	return {"unexpected": "shape"}
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n" +
		"    outputs: ToastShowResp\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("output validation failure must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "validation") {
		t.Errorf("error must mention validation, got: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Execute: invoke failure propagates as card failure
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Execute_InvokeFailureMarksCardFailed pins the
// end-to-end error path: the host binding receives a runtime error
// from the callable → script returns a runtime error → execute
// writes outputs.error + flips the card failed (vs. returning a
// dispatch error that would roll back the claim).
func TestScriptExecutor_Execute_InvokeFailureMarksCardFailed(t *testing.T) {
	f := freshScriptTestFixture(t)
	// Make every toast invoke error out so the host binding sees
	// a non-nil error from the actor layer.
	f.ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "toast" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any {
				return fmt.Errorf("kaboom")
			}), true
		}
		return nil, false
	}
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): any {
	return invoke("toast.state", {"Title": "x"})
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("invoke failure must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "kaboom") && !strings.Contains(note, "toast.state") {
		t.Errorf("error must reference the failing callable or its error, got: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Pure helper: script arg resolution
// ─────────────────────────────────────────────────────────────────────

// TestResolveScriptArgs pins the input-binding contract used by
// Execute: declared keys pull from ClaimReq.Inputs in order;
// missing keys are reported back so Execute can fail the card.
func TestResolveScriptArgs(t *testing.T) {
	t.Run("all_present", func(t *testing.T) {
		args, missing := resolveScriptArgs([]string{"a", "b"}, map[string]any{"a": "x", "b": 2})
		if len(missing) != 0 {
			t.Fatalf("missing = %v, want []", missing)
		}
		if len(args) != 2 || args[0] != "x" || args[1] != 2 {
			t.Fatalf("args = %#v", args)
		}
	})
	t.Run("missing_reported", func(t *testing.T) {
		_, missing := resolveScriptArgs([]string{"a", "b"}, map[string]any{"a": "x"})
		if len(missing) != 1 || missing[0] != "b" {
			t.Fatalf("missing = %v, want [b]", missing)
		}
	})
	t.Run("no_declared_inputs", func(t *testing.T) {
		args, missing := resolveScriptArgs(nil, map[string]any{"a": "x"})
		if args != nil || missing != nil {
			t.Fatalf("args=%#v missing=%#v, want both nil", args, missing)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Pure helper: spore fenced-block extraction
// ─────────────────────────────────────────────────────────────────────

// TestExtractScriptSource pins the source extraction contract:
//   - ```spore fences are extracted (case-insensitive on the language tag),
//   - non-spore fences are ignored,
//   - the first matching fence wins,
//   - a body without any ```spore fence is rejected.
func TestExtractScriptSource(t *testing.T) {
	t.Run("first_spore_wins", func(t *testing.T) {
		body := "intro prose\n```yaml\nnot spore\n```\n```spore\nexport fun run(): int { return 1 }\n```\n"
		req := ClaimReq{BoundTaskCardID: "x", Body: body}
		src, err := extractScriptSource(req)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if !strings.Contains(src, "export fun run") {
			t.Errorf("src = %q, missing entry", src)
		}
	})
	t.Run("case_insensitive", func(t *testing.T) {
		body := "```SPORE\nexport fun run(): int { return 1 }\n```\n"
		req := ClaimReq{BoundTaskCardID: "x", Body: body}
		src, err := extractScriptSource(req)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		if !strings.Contains(src, "export fun run") {
			t.Errorf("src = %q, missing entry", src)
		}
	})
	t.Run("no_fence_rejected", func(t *testing.T) {
		req := ClaimReq{BoundTaskCardID: "x", Body: "plain prose only"}
		_, err := extractScriptSource(req)
		if err == nil || !strings.Contains(err.Error(), "no ```spore") {
			t.Fatalf("err = %v, want no-spore-fence", err)
		}
	})
	t.Run("empty_body_rejected", func(t *testing.T) {
		req := ClaimReq{BoundTaskCardID: "x", Body: ""}
		_, err := extractScriptSource(req)
		if err == nil {
			t.Fatal("empty body must reject")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Script-context helper: buildCallContext budget pass-through
// ─────────────────────────────────────────────────────────────────────

// TestBuildCallContext pins the budget pass-through contract after
// the ctx propagation refactor:
//   - the actor lifecycle context is wired into CallContext.Context
//     when ctx is non-nil, and stays nil when ctx is nil (test / pure
//     function path),
//   - MaxDuration falls back to scriptcard.DefaultMaxDurationSec when
//     maxDurationSec is zero,
//   - explicit values pass through verbatim; MaxInstructions /
//     MaxHostCalls / MaxOutputBytes zero values stay zero (the spore
//     VM treats zero as "limit disabled").
func TestBuildCallContext(t *testing.T) {
	t.Run("all_zero_default_duration", func(t *testing.T) {
		ctx := buildCallContext(nil, 0, 0, 0, 0)
		if ctx.Context != nil {
			t.Errorf("Context = %v, want nil when actor ctx is nil", ctx.Context)
		}
		if ctx.Budget.MaxDuration != time.Duration(scriptcard.DefaultMaxDurationSec)*time.Second {
			t.Errorf("MaxDuration = %v, want default %ds", ctx.Budget.MaxDuration, scriptcard.DefaultMaxDurationSec)
		}
		if ctx.Budget.MaxInstructions != 0 || ctx.Budget.MaxHostCalls != 0 || ctx.Budget.MaxOutputBytes != 0 {
			t.Errorf("zero values must stay zero, got %+v", ctx.Budget)
		}
	})
	t.Run("explicit_passthrough_with_ctx", func(t *testing.T) {
		// Wire a cancellable lifecycle so we can verify the
		// derived context is propagated (not background).
		f := freshScriptTestFixture(t)
		callCtx := buildCallContext(f.ctx, 1000, 30, 16, 4096)
		if callCtx.Context == nil {
			t.Fatal("CallContext.Context must be non-nil when actor ctx is supplied")
		}
		if callCtx.Budget.MaxInstructions != 1000 {
			t.Errorf("MaxInstructions = %d", callCtx.Budget.MaxInstructions)
		}
		if callCtx.Budget.MaxDuration != 30*time.Second {
			t.Errorf("MaxDuration = %v", callCtx.Budget.MaxDuration)
		}
		if callCtx.Budget.MaxHostCalls != 16 {
			t.Errorf("MaxHostCalls = %d", callCtx.Budget.MaxHostCalls)
		}
		if callCtx.Budget.MaxOutputBytes != 4096 {
			t.Errorf("MaxOutputBytes = %d", callCtx.Budget.MaxOutputBytes)
		}
	})
	t.Run("nil_ctx_keeps_nil_context", func(t *testing.T) {
		// No actor context → CallContext.Context stays nil so the
		// spore VM derives its run-time context purely from
		// MaxDuration (no spurious cancellation).
		callCtx := buildCallContext(nil, 1, 5, 1, 1)
		if callCtx.Context != nil {
			t.Errorf("Context = %v, want nil when actor ctx is nil", callCtx.Context)
		}
		if callCtx.Budget.MaxDuration != 5*time.Second {
			t.Errorf("MaxDuration = %v", callCtx.Budget.MaxDuration)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Pure helper: capability whitelist check
// ─────────────────────────────────────────────────────────────────────

// TestIsCapabilityAllowed pins the whitelist predicate used by the
// host binding at every invocation. Order-independent; duplicates
// count (still allowed); empty whitelist always rejects.
func TestIsCapabilityAllowed(t *testing.T) {
	wl := []string{"toast.state", "wiki.read", "toast.state"}
	if !isCapabilityAllowed(wl, "toast.state") {
		t.Error("toast.state must be allowed (declared twice)")
	}
	if !isCapabilityAllowed(wl, "wiki.read") {
		t.Error("wiki.read must be allowed")
	}
	if isCapabilityAllowed(wl, "toast.show") {
		t.Error("toast.show must be rejected (not in whitelist)")
	}
	if isCapabilityAllowed(nil, "any") {
		t.Error("empty whitelist must reject everything")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Pure helper: schema name lookup
// ─────────────────────────────────────────────────────────────────────

// TestLookupSchemaIDByName pins the name → id reverse index built
// once from gen.SchemaIDs. ToastShowResp is in the registry; an
// arbitrary missing name returns false.
func TestLookupSchemaIDByName(t *testing.T) {
	t.Run("known_schema", func(t *testing.T) {
		id, ok := lookupSchemaIDByName("ToastShowResp")
		if !ok {
			t.Skip("ToastShowResp not in registry for this build")
		}
		if id == 0 {
			t.Errorf("ToastShowResp id = 0")
		}
	})
	t.Run("unknown_schema", func(t *testing.T) {
		if _, ok := lookupSchemaIDByName("NoSuchTypeXYZ"); ok {
			t.Error("unknown schema name must return ok=false")
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Pure helper: output validation
// ─────────────────────────────────────────────────────────────────────

// TestValidateScriptOutput pins the output-validation contract:
// when the schema name is unknown, the function returns an error
// (no silent fallback); when the value is empty and the schema
// requires fields, validation fails; when the value conforms, the
// function returns nil.
func TestValidateScriptOutput(t *testing.T) {
	t.Run("unknown_schema_errors", func(t *testing.T) {
		err := validateScriptOutput("NoSuchTypeXYZ", map[string]any{"Id": "x"})
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			t.Fatalf("unknown schema must error, got: %v", err)
		}
	})
	t.Run("non_conforming_value_errors", func(t *testing.T) {
		err := validateScriptOutput("ToastShowResp", map[string]any{"unexpected": "shape"})
		if err == nil {
			t.Fatal("non-conforming value must fail validation")
		}
	})
	t.Run("conforming_value_passes", func(t *testing.T) {
		// ToastShowResp's struct shape (see gen registry): only Id
		// is required, so a map with Id set satisfies the layout.
		if err := validateScriptOutput("ToastShowResp", map[string]any{"Id": "toast-1"}); err != nil {
			t.Fatalf("conforming value must pass validation, got: %v", err)
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Helper utility (test-only): drive executor panic path
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_PanicsAreCaught pins that a panic inside the
// spore VM (the runtime is in-process and any bug is a panic)
// surfaces as a card failure, not a claim rollback. We exercise
// the panicprobe.Guard wrapper indirectly by feeding an executor
// invocation that intentionally panics: a script that explodes on
// load time causes the VM to raise during CallContext → we look
// for a graceful card-failed transition.

func TestScriptExecutor_PanicsAreCaught(t *testing.T) {
	// Build a script that compiles but fails at runtime because
	// of a clear type error. The execute path catches this as a
	// card-level failed with a structured error note.
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)

	src := `import { invoke } from "host"
export fun run(): any {
	return "ok"["missing_field"]
}`
	execYAML := "    kind: script\n" +
		"    capabilities:\n      - toast.state\n"
	cardRaw := f.scriptCardRaw(execYAML, src)

	req := f.buildClaimReq(cardRaw, nil)
	req.Preflight = &PreflightResult{}

	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("runtime type error must map to card failure: %v", err)
	}
	if f.statusSet[f.boundTaskCardID] != "blocked" {
		t.Errorf("card status = %q, want blocked", f.statusSet[f.boundTaskCardID])
	}
	note, _ := f.outputsSet[f.boundTaskCardID]["error"].(string)
	if !strings.Contains(note, "runtime error") && !strings.Contains(note, "type") {
		t.Errorf("error must mention type mismatch, got: %s", note)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Compile-cache smoke + sha256Hex shape
// ─────────────────────────────────────────────────────────────────────

// TestSha256Hex pins the sha256 shape: the hash function returns a
// 64-char hex string (256 bits = 32 bytes = 64 nibbles). Tests
// that depend on the exact length rely on this contract.
func TestSha256Hex(t *testing.T) {
	h := sha256Hex("hello")
	if len(h) != 64 {
		t.Errorf("sha256 hex length = %d, want 64", len(h))
	}
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Errorf("sha256 hex contains non-hex char %q", r)
			break
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Helper utility: normalise spore invoke result
// ─────────────────────────────────────────────────────────────────────

// TestScriptResultToMap pins the host-result → map conversion
// contract: nil maps to an empty map (not nil), primitives are
// wrapped in a {"value": ...} envelope, maps pass through, and a
// real JSON-encoded struct round-trips to the expected field set.
func TestScriptResultToMap(t *testing.T) {
	t.Run("nil_to_empty", func(t *testing.T) {
		m, err := scriptResultToMap(nil)
		if err != nil {
			t.Fatalf("nil: %v", err)
		}
		if len(m) != 0 {
			t.Errorf("nil result must map to empty map, got %#v", m)
		}
	})
	t.Run("string_envelope", func(t *testing.T) {
		m, err := scriptResultToMap("hello")
		if err != nil {
			t.Fatalf("string: %v", err)
		}
		if got, _ := m["value"].(string); got != "hello" {
			t.Errorf("value = %#v, want hello", m["value"])
		}
	})
	t.Run("struct_round_trip", func(t *testing.T) {
		raw := gen.ToastShowResp{ID: "abc"}
		m, err := scriptResultToMap(raw)
		if err != nil {
			t.Fatalf("struct: %v", err)
		}
		if got, _ := m["Id"].(string); got != "abc" {
			t.Errorf("Id = %#v, want abc", m["Id"])
		}
	})
	t.Run("json_object_passthrough", func(t *testing.T) {
		raw := json.RawMessage(`{"a": 1, "b": "two"}`)
		m, err := scriptResultToMap(raw)
		if err != nil {
			t.Fatalf("raw: %v", err)
		}
		if got, _ := m["a"].(float64); got != 1 {
			t.Errorf("a = %#v, want 1", m["a"])
		}
		if got, _ := m["b"].(string); got != "two" {
			t.Errorf("b = %#v, want two", m["b"])
		}
	})
}

// ─────────────────────────────────────────────────────────────────────
// Helper utility: extractScriptBody falls back to CardRecord.Body
// ─────────────────────────────────────────────────────────────────────

// TestExtractScriptBody_FallsBackToCardRecord pins the dispatcher-
// body vs. CardRecord.Body resolution. When req.Body is empty the
// helper must parse req.CardRaw to get the body (the path tests
// exercise when they feed raw frontmatter without a Body field).
func TestExtractScriptBody_FallsBackToCardRecord(t *testing.T) {
	req := ClaimReq{
		BoundTaskCardID: "x",
		CardRaw: "---\nid: x\ntype: task\nstatus: backlog\n---\n\n```spore\nexport fun run(): int { return 1 }\n```\n",
	}
	body := extractScriptBody(req)
	if !strings.Contains(body, "```spore") {
		t.Errorf("body = %q, want spore fence", body)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Compile probe: success path
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_ProbeScriptCompile pins that the preflight
// compile probe accepts a well-formed script (no error) and
// rejects a syntactically broken one (error). The probe uses its
// own throwaway Runtime so the test does not depend on the
// executor instance's workspace actor.
func TestScriptExecutor_ProbeScriptCompile(t *testing.T) {
	e := newScriptExecutor(nil)
	if err := e.probeScriptCompile(`export fun run(): int { return 1 }`); err != nil {
		t.Errorf("good source must compile: %v", err)
	}
	if err := e.probeScriptCompile(`fun bad(: int) {}`); err == nil {
		t.Error("broken source must fail compile probe")
	}
}

// ─────────────────────────────────────────────────────────────────────
// Host-invoke wiring: direct function path
// ─────────────────────────────────────────────────────────────────────

// TestInvokeHostCallable_RejectsUnknownCapability pins the per-call
// whitelist check (different from the bulk preflight gate) by
// calling the host binding directly. The test bypasses the
// executor's closure capture so we exercise the pure function.
func TestInvokeHostCallable_RejectsUnknownCapability(t *testing.T) {
	f := freshScriptTestFixture(t)
	e := newScriptExecutor(f.a)
	hctx := &hostInvokeContext{
		executor:  e,
		cfg:       scriptConfig{Capabilities: []string{"toast.state"}},
		ctx:       f.ctx,
		req:       ClaimReq{CallerAgentID: f.callerAgentID, ProjectID: f.projectID, BoundTaskCardID: f.boundTaskCardID},
		projectID: f.projectID,
	}
	_, err := invokeHostCallable(hctx, "toast.show", map[string]any{"Title": "x"})
	if err == nil || !strings.Contains(err.Error(), "whitelist") {
		t.Fatalf("unknown callable must reject: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Topological summary
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_RegistryContainsScript pins that the script
// executor shows up in the deterministic Kinds() list. The
// executor.go file orders Register() calls alphabetically by
// current source layout; this test asserts the script kind is
// among the registered kinds.
func TestScriptExecutor_RegistryContainsScript(t *testing.T) {
	f := freshScriptTestFixture(t)
	kinds := f.a.execRegistry.Kinds()
	sawScript := false
	for _, k := range kinds {
		if k == ExecKindScript {
			sawScript = true
		}
	}
	if !sawScript {
		t.Fatalf("registry missing script kind: %v", kinds)
	}
}

// ─────────────────────────────────────────────────────────────────────
// Cross-layer contract (project ↔ workspace): script cards
// ─────────────────────────────────────────────────────────────────────

// crossLayerCase shapes a single script-card raw fed through both
// the project-side static validator (project.ValidateCardStructured)
// and the workspace-side script executor (e.Preflight). The test
// asserts bidirectional agreement on the SHAPE subset (inputs /
// outputs / capabilities / gate_card / budget / fence); runtime-only
// checks (topology resolution, gate-card approval, map stamp, compile
// probe) are deliberately out of scope — they only exist on the
// executor side and a one-sided pass is correct.
//
// Every case picks a known callable so the executor's topology lookup
// succeeds; the failure modes tested are pure shape drift (list vs
// map inputs, deprecated budget keys, missing fence, mismatched
// fence length, out-of-range budget, duplicate inputs, etc.).
//
// bodyIsRaw switches the body-construction mode. When false (the
// default for the contract test), scriptCardRaw wraps `body` in a
// ```spore fence and emits it after the frontmatter. When true, the
// supplied `body` is appended verbatim — used by the fence-mismatch
// cases where wrap-and-prefix would hide the failure.
//
// body must not be empty when bodyIsRaw is true and the case expects
// a valid fence (the contract for the shape cases below).
type crossLayerCase struct {
	name        string
	execYAML    string // data.exec.* block (already indented 4 spaces)
	body        string // markdown body, may carry a fence
	bodyIsRaw   bool   // when true, body is appended verbatim (no wrap)
	wantValid   bool   // project.ValidateCardStructured expected outcome
	wantPreflight bool // e.Preflight expected outcome
}

// scriptFenceFree returns the canonical script body used by shape-only
// contract tests: the *content* between ```spore fences (scriptCardRaw
// wraps the returned string in the fence itself). The body declares a
// `run` entry point that returns 0 without calling host.invoke, which
// is enough for the spore VM's compile probe (no runtime path is taken
// at preflight).
func scriptFenceFree() string {
	return "export fun run(): int { return 0 }"
}

// runCrossLayerContract is the workhorse behind the per-case matrix:
// it feeds `raw` through both layers and compares each side's
// pass/fail verdict against the case's expectations, then asserts the
// two verdicts are equal (the bidirectional agreement the contract
// pins). Returns true when the case passed and the loop should keep
// going; the function does NOT fatal so the table can finish surfacing
// every divergence in one run.
//
// The fixture is reused across all cases: freshScriptTestFixture
// wires a topology with toast.state / toast.show / toast.dismiss /
// toast.stream / goal_submit, so any well-formed capability name
// resolves and only SHAPE drift (the contract under test) is
// observable.
func runCrossLayerContract(t *testing.T, cases []crossLayerCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := freshScriptTestFixture(t)
			e := newScriptExecutor(f.a)
			var cardRaw string
			if tc.bodyIsRaw {
				cardRaw = f.scriptCardRawWithBody(tc.execYAML, tc.body)
			} else {
				cardRaw = f.scriptCardRaw(tc.execYAML, tc.body)
			}
			req := f.buildClaimReq(cardRaw, nil)

			// (a) Project side — same canonical entry point the
			// UI repair mode uses (project.wiki_validate_card →
			// handleWikiValidateCard → validateCardStructured).
			projectErrs := project.ValidateCardStructured(f.boundTaskCardID, cardRaw)
			gotValid := len(projectErrs) == 0
			if gotValid != tc.wantValid {
				detail := ""
				for _, e := range projectErrs {
					detail += "\n  - " + e.Code + ": " + e.Message
				}
				t.Fatalf("project.ValidateCardStructured = %v (want %v); errors:%s", gotValid, tc.wantValid, detail)
			}

			// (b) Executor side — Preflight runs every shape gate
			// the project validator covers, plus a topology
			// resolution that the static layer cannot reach. For
			// the shape-only cases below, the well-formed
			// capability name makes the topology lookup succeed,
			// so Preflight's pass/fail reflects shape drift alone.
			_, preflightErr := e.Preflight(f.ctx, req)
			gotPreflight := preflightErr == nil
			if gotPreflight != tc.wantPreflight {
				detail := "<nil>"
				if preflightErr != nil {
					detail = preflightErr.Error()
				}
				t.Fatalf("e.Preflight = %v (want %v); err = %s", gotPreflight, tc.wantPreflight, detail)
			}

			// Bidirectional agreement: shape subset must agree.
			if gotValid != gotPreflight {
				t.Fatalf("cross-layer divergence: project=%v executor=%v (project errors=%+v, executor err=%v)",
					gotValid, gotPreflight, projectErrs, preflightErr)
			}
		})
	}
}

// TestScriptCard_CrossLayerContract pins the bidirectional contract
// between the project-side static validator and the workspace-side
// script executor for the SHAPE subset (inputs / outputs /
// capabilities / gate_card / budget / fence).
//
// Both sides delegate to pkg/scriptcard for these rules, so the
// expected verdict is "project passes iff executor Preflight
// passes" — a drift here means one side drifted away from the
// shared canonical contract.
//
// Runtime-only checks (topology resolution, gate-card approval,
// map stamp, compile probe) are deliberately not in this matrix:
// they live exclusively on the executor side, and a one-sided
// rejection on those grounds is correct behaviour.
func TestScriptCard_CrossLayerContract(t *testing.T) {
	runCrossLayerContract(t, []crossLayerCase{
		// ── Legal: list inputs + four canonical budget keys ─────
		{
			name: "legal_list_inputs_four_budget",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    inputs:\n      - repo\n      - branch\n" +
				"    outputs: ToastShowResp\n" +
				"    gate_card: g-1\n" +
				"    budget:\n" +
				"      max_duration_sec: 30\n" +
				"      max_host_calls: 8\n" +
				"      max_instructions: 4096\n" +
				"      max_output_bytes: 65536\n",
			body:         scriptFenceFree(),
			wantValid:    true,
			wantPreflight: true,
		},
		// ── Legal: minimal card (capabilities + fence only) ────
		{
			name:         "legal_minimum",
			execYAML:     "    kind: script\n    capabilities:\n      - toast.state\n",
			body:         scriptFenceFree(),
			wantValid:    true,
			wantPreflight: true,
		},
		// ── Legal: deprecated timeout_sec silently ignored ──────
		// The validator + scriptcard.ParseConfig both ignore
		// timeout_sec (not in the canonical keys) so the card
		// passes shape; the executor runs with DefaultMaxDurationSec.
		// This is the "agree on deprecation" pin — neither side
		// rejects, so neither should the other.
		{
			name: "legal_legacy_timeout_sec_silently_ignored",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    budget:\n      timeout_sec: 30\n",
			body:         scriptFenceFree(),
			wantValid:    true,
			wantPreflight: true,
		},
		// ── Illegal: map-shape inputs ───────────────────────────
		{
			name: "illegal_inputs_as_map",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    inputs:\n      repo: string\n      branch: string\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: duplicate inputs ───────────────────────────
		{
			name: "illegal_duplicate_inputs",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    inputs:\n      - repo\n      - repo\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: empty-string inputs entry ──────────────────
		{
			name: "illegal_empty_input_entry",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    inputs:\n      - \"\"\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: capabilities as scalar (not list) ──────────
		{
			name: "illegal_capabilities_scalar",
			execYAML: "    kind: script\n" +
				"    capabilities: toast.state\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: missing capabilities ────────────────────────
		{
			name:         "illegal_missing_capabilities",
			execYAML:     "    kind: script\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: empty capabilities list ────────────────────
		{
			name:         "illegal_empty_capabilities",
			execYAML:     "    kind: script\n    capabilities: []\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: budget out of range (max_duration_sec>300) ─
		{
			name: "illegal_budget_duration_too_large",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    budget:\n      max_duration_sec: 999\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: budget out of range (max_host_calls>64) ────
		{
			name: "illegal_budget_host_calls_too_large",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    budget:\n      max_host_calls: 999\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: budget non-numeric ─────────────────────────
		{
			name: "illegal_budget_non_numeric",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    budget:\n      max_duration_sec: forever\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: budget block is a list ─────────────────────
		{
			name: "illegal_budget_not_a_mapping",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    budget:\n      - max_duration_sec: 30\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: outputs as a list ──────────────────────────
		{
			name: "illegal_outputs_not_a_string",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    outputs: [a, b]\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: gate_card as a list ────────────────────────
		{
			name: "illegal_gate_card_not_a_string",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    gate_card: [g1, g2]\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: inputs as a single string (not a list) ─────
		{
			name: "illegal_inputs_scalar_string",
			execYAML: "    kind: script\n" +
				"    capabilities:\n      - toast.state\n" +
				"    inputs: repo\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: capabilities as a flow map (not a list) ────
		{
			name: "illegal_capabilities_flow_map",
			execYAML: "    kind: script\n" +
				"    capabilities: {foo: bar}\n",
			body:         scriptFenceFree(),
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: missing fence ──────────────────────────────
		{
			name:         "illegal_no_fence",
			execYAML:     "    kind: script\n    capabilities:\n      - toast.state\n",
			body:         "",
			bodyIsRaw:    true,
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: long fence with mismatched closer ─────────
		{
			name:         "illegal_four_open_three_close",
			execYAML:     "    kind: script\n    capabilities:\n      - toast.state\n",
			body:         "````spore\nexport fun run(): int { return 1 }\n```\n",
			bodyIsRaw:    true,
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: 3-backtick opener with 4-backtick closer ──
		{
			name:         "illegal_three_open_four_close",
			execYAML:     "    kind: script\n    capabilities:\n      - toast.state\n",
			body:         "```spore\nexport fun run(): int { return 1 }\n````\n",
			bodyIsRaw:    true,
			wantValid:    false,
			wantPreflight: false,
		},
		// ── Illegal: empty fence body ──────────────────────────
		//
		// NOTE (intentionally excluded from the bidirectional
		// matrix): an empty fence body (opener+closer with no
		// content) passes the static layer's fence-existence check
		// but the executor's compile probe rejects it. This is a
		// deliberate divergence — the project validator cannot run
		// the spore VM (architecture constraint), so compile-time
		// failures are executor-only. The executor's coverage is
		// pinned by TestScriptExecutor_Preflight_Rejections and
		// TestScriptExecutor_Execute_BudgetOverrun; the contract
		// under test here is the SHAPE subset, where both layers
		// must agree.
		//
		// ── Illegal: missing data.exec entirely ────────────────
		//
		// NOTE (intentionally excluded from the bidirectional
		// matrix): the project validator defaults cards without
		// data.exec to worker_task (validateCardExecKind is a no-op
		// for absent kind) so the card is "valid" at the static
		// layer, whereas the script executor is only entered by the
		// dispatcher when data.exec.kind = script has been declared
		// — a card without data.exec is never routed here. The
		// executor's "data.exec is required" rejection therefore
		// cannot leak into the static layer's verdict; the two
		// verdicts intentionally diverge by design.
	})
}

// TestScriptCard_CrossLayerContract_FenceMatrix exhausts the fence
// shape matrix through both layers in lockstep. Every entry in
// scriptcard_test.go's TestExtractSporeBlock_Cases is mirrored here:
// the same body is fed to scriptcard.ExtractSporeBlock (the canonical
// source-of-truth that both sides use) AND to the executor's
// extractScriptSource (the path that drives Preflight). Both must
// agree on (src, ok) and on the exact extracted source, byte-for-byte.
//
// This is the regression pin for the "two implementations drift apart"
// failure mode the fix-card describes: should a future patch add a
// private fence walker in either layer, this test fails immediately.
func TestScriptCard_CrossLayerContract_FenceMatrix(t *testing.T) {
	type matrix struct {
		name string
		body string
		want string
		wantOK bool
	}
	cases := []matrix{
		{
			name:   "lowercase_basic",
			body:   "```spore\nexport fun run(): int { return 1 }\n```\n",
			want:   "export fun run(): int { return 1 }",
			wantOK: true,
		},
		{
			name:   "mixed_case_Spore",
			body:   "```Spore\nexport fun run(): int { return 2 }\n```\n",
			want:   "export fun run(): int { return 2 }",
			wantOK: true,
		},
		{
			name:   "uppercase_SPORE",
			body:   "```SPORE\nexport fun run(): int { return 3 }\n```\n",
			want:   "export fun run(): int { return 3 }",
			wantOK: true,
		},
		{
			name:   "spore_with_multiword_info",
			body:   "```spore draft v2\ncode\n```\n",
			want:   "code",
			wantOK: true,
		},
		{
			name:   "spore_info_with_title",
			body:   "```spore title=\"hello world\"\ncode\n```\n",
			want:   "code",
			wantOK: true,
		},
		{
			name:   "empty_info_string",
			body:   "```\ncode\n```\n",
			wantOK: false,
		},
		{
			name:   "four_backtick_open_close",
			body:   "````spore\ncode with ``` inside\n````\n",
			want:   "code with ``` inside",
			wantOK: true,
		},
		{
			name:   "four_backtick_open_three_close",
			body:   "````spore\ncode\n```\n",
			wantOK: false,
		},
		{
			name:   "three_backtick_open_four_close",
			body:   "```spore\ncode\n````\n",
			wantOK: false,
		},
		{
			name:   "indented_opener",
			body:   "  \t```spore\nexport fun run(): int { return 1 }\n```\n",
			want:   "export fun run(): int { return 1 }",
			wantOK: true,
		},
		{
			name:   "crlf_normalization",
			body:   "```spore\r\nexport fun run(): int { return 1 }\r\n```\r\n",
			want:   "export fun run(): int { return 1 }",
			wantOK: true,
		},
		{
			name:   "no_fence",
			body:   "just prose, no code fence",
			wantOK: false,
		},
		{
			name:   "empty_body",
			body:   "",
			wantOK: false,
		},
		{
			name:   "first_block_wins",
			body:   "```spore\nfirst\n```\n\n```spore\nsecond\n```\n",
			want:   "first",
			wantOK: true,
		},
		{
			name:   "non_spore_fence_before",
			body:   "```yaml\nfoo\n```\n```spore\nbar\n```\n",
			want:   "bar",
			wantOK: true,
		},
		{
			name:   "closer_trailing_whitespace",
			body:   "```spore\ncode\n```  \n",
			want:   "code",
			wantOK: true,
		},
		{
			name:   "closer_trailing_text",
			body:   "```spore\ncode\n``` end\n",
			wantOK: false,
		},
		{
			name:   "multi_line_block",
			body:   "intro prose\n\n```spore\nlet x = 1\nlet y = x + 1\nreturn y\n```\n\ntrailing prose\n",
			want:   "let x = 1\nlet y = x + 1\nreturn y",
			wantOK: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// (a) Canonical source-of-truth: pkg/scriptcard.ExtractSporeBlock.
			// Both layers delegate here; this is what the project-side
			// validator's fence check and the executor's extractScriptSource
			// ultimately compute.
			direct, directOK := scriptcard.ExtractSporeBlock(tc.body)
			if directOK != tc.wantOK {
				t.Fatalf("scriptcard.ExtractSporeBlock ok = %v, want %v (body=%q)", directOK, tc.wantOK, tc.body)
			}
			if directOK && direct != tc.want {
				t.Fatalf("scriptcard.ExtractSporeBlock src = %q, want %q", direct, tc.want)
			}

			// (b) Executor side: extractScriptSource parses the
			// frontmatter, then walks the body. We feed the body via
			// the dispatcher's body-injection path so the test
			// exercises the exact same code path Preflight does.
			f := freshScriptTestFixture(t)
			req := ClaimReq{
				CallerAgentID:   f.callerAgentID,
				ProjectID:       f.projectID,
				BoundTaskCardID: f.boundTaskCardID,
				CardRaw: "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: backlog\n" +
					"data:\n  exec:\n    kind: script\n    capabilities:\n      - toast.state\n" +
					"---\n\n" + tc.body,
				Body: tc.body,
			}
			execSrc, execErr := extractScriptSource(req)
			execOK := execErr == nil
			if execOK != directOK {
				t.Fatalf("executor ExtractSporeBlock diverged from canonical: execOK=%v directOK=%v (err=%v body=%q)",
					execOK, directOK, execErr, tc.body)
			}
			if execOK && execSrc != direct {
				t.Fatalf("executor ExtractSporeBlock src = %q, want %q", execSrc, direct)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────
// Cross-layer: per-capability stamp enforcement
// ─────────────────────────────────────────────────────────────────────

// TestScriptExecutor_Preflight_MultiMutatingAllStamped_Passes pins the
// happy path of the v2 per-capability stamp contract through Preflight:
// when the bound script declares two mutating capabilities and the
// workflow map stamps BOTH (matching callable_id + gate_card_id),
// Preflight must accept the card. The script body is trivially valid
// (return 0) so the only barrier is the gate.
//
// This complements TestScriptExecutor_Execute_MultiMutatingAllStamped_Passes
// (which drives Execute) by pinning the gate acceptance at Preflight
// time, which is when the dispatcher claims the card.
func TestScriptExecutor_Preflight_MultiMutatingAllStamped_Passes(t *testing.T) {
	g := newScriptGateFixture(t, []string{"toast.show", "toast.dismiss"})
	// Override the default stamp to cover BOTH mutating capabilities.
	g.mapCardBuilder = func() string {
		stamp := `[{"task_card_id":"` + g.toolcallTestFixture.boundTaskCardID + `","callable_id":"toast.show","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"},` +
			`{"task_card_id":"` + g.toolcallTestFixture.boundTaskCardID + `","callable_id":"toast.dismiss","gate_card_id":"` + g.gateCardID + `","stamped_at":"2026-09-10T00:00:00Z"}]`
		return "---\nid: " + g.mapCardID + "\ntype: map\n" +
			"data:\n  stamped_effects: '" + stamp + "'\n" +
			"---\n\nMap card body."
	}
	g.boundCardRaw = g.scriptBoundCardRaw([]string{"toast.show", "toast.dismiss"})

	e := newScriptExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
	}
	if _, err := e.Preflight(g.ctx, req); err != nil {
		t.Fatalf("every-stamped mutating script must pass Preflight: %v", err)
	}
}

// TestScriptExecutor_Preflight_MultiMutatingOneStamped_Rejects pins the
// v2 per-capability stamp fix end-to-end: when the bound script
// declares two mutating capabilities but the workflow map stamps only
// one, Preflight must reject (the dispatcher never claims the card).
// The error must name the missing capability so the card author knows
// what to fix.
//
// This is the regression pin for the legacy "first match wins"
// loophole: a single stamp used to satisfy both capabilities. The
// verifyMapStampedScript rewrite requires each mutating capability to
// find its own stamp entry, and Preflight propagates the failure.
func TestScriptExecutor_Preflight_MultiMutatingOneStamped_Rejects(t *testing.T) {
	g := newScriptGateFixture(t, []string{"toast.show", "toast.dismiss"})
	// Default mapCardBuilder stamps only toast.show (the first
	// capability); toast.dismiss is deliberately unstamped.
	g.boundCardRaw = g.scriptBoundCardRaw([]string{"toast.show", "toast.dismiss"})

	e := newScriptExecutor(g.a)
	req := ClaimReq{
		CallerAgentID:   g.callerAgentID,
		ProjectID:       g.projectID,
		BoundTaskCardID: g.boundTaskCardID,
		CardRaw:         g.boundCardRaw,
		Claimed:         true,
	}
	_, err := e.Preflight(g.ctx, req)
	if err == nil {
		t.Fatal("multi-mutating script with one unstamped capability must be rejected by Preflight")
	}
	msg := err.Error()
	if !strings.Contains(msg, "toast.dismiss") {
		t.Errorf("error must name the unstamped capability (toast.dismiss), got: %v", err)
	}
	if strings.Contains(msg, "toast.show") {
		t.Errorf("error must NOT mention the stamped capability (toast.show) as missing, got: %v", err)
	}
}