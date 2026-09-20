package sporeapp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/builtin/sporecall"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/sporebridge"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// --- minimal fakes: a ServiceHost seam plus a ref that answers every
// Invoke with one raw JSON frame and records the calls it saw. ---

type fakeSeams struct {
	services map[string]ref.Ref
	self     ref.Ref
}

func (s fakeSeams) LookupService(name string) (ref.Ref, bool) {
	r, ok := s.services[name]
	return r, ok
}

func (s fakeSeams) Self() ref.Ref { return s.self }

var _ sporebridge.ServiceHost = fakeSeams{}

type capturedCall struct {
	callID  string
	payload any
}

type capturingRef struct {
	id     id.ActorID
	body   []byte
	mu     sync.Mutex
	calls  []capturedCall
}

func (r *capturingRef) ID() id.ActorID        { return r.id }
func (*capturingRef) Service() (string, bool) { return "", false }

func (r *capturingRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	r.mu.Lock()
	r.calls = append(r.calls, capturedCall{callID: callID, payload: payload})
	r.mu.Unlock()
	return invoke.NewCall(invoke.CallModeUnary, &rawStream{body: r.body})
}

func (r *capturingRef) recorded() []capturedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedCall(nil), r.calls...)
}

// rawStream yields exactly one raw JSON frame, mirroring how a real callee
// surfaces a unary JSON response on RecvRaw.
type rawStream struct {
	body     []byte
	consumed bool
}

func (s *rawStream) Recv() (any, error)  { return nil, io.EOF }
func (s *rawStream) RecvRaw() ([]byte, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	return s.body, nil
}
func (*rawStream) Close() error { return nil }

func newSporecallActor(t *testing.T, seams sporebridge.ServiceHost) *Actor {
	t.Helper()
	a := &Actor{
		Manifest:          sporecall.Manifest,
		EntryModule:       sporecall.EntryModule,
		Modules:           sporecall.Modules,
		allowedCapabilities: map[string]struct{}{"spore.invoke": {}},
		State:             map[string]any{},
		codec:             codec.NewBinary(),
		hostSeams:         seams,
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	t.Cleanup(func() {
		if a.runtime != nil {
			_ = a.runtime.Close()
		}
	})
	return a
}

func callSporecall(t *testing.T, a *Actor, callable string, argsJSON string) (gen.SporeAppInvokeResp, error) {
	t.Helper()
	return a.handleInvoke(testutil.HumanCtx(testutil.GenActorID()), gen.SporeAppInvokeReq{
		ID: sporecall.Manifest.ID, Callable: callable, Payload: []byte(argsJSON),
	})
}

// TestSporecallRequiresInvokeCapability verifies enforcement by structural
// absence: without the "spore.invoke" capability the host namespace is
// unbound and the module fails to load.
func TestSporecallRequiresInvokeCapability(t *testing.T) {
	a := &Actor{
		Manifest:            sporecall.Manifest,
		EntryModule:         sporecall.EntryModule,
		Modules:             sporecall.Modules,
		allowedCapabilities: map[string]struct{}{},
		State:               map[string]any{},
	}
	if err := a.loadRuntime(); err == nil {
		t.Fatal("expected load failure when the invoke capability is not granted")
	}
}

// TestSporecallPing exercises the bundle without any host call.
func TestSporecallPing(t *testing.T) {
	a := newSporecallActor(t, fakeSeams{})
	resp, err := callSporecall(t, a, "ping", `[]`)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if string(resp.Payload) != `"pong"` {
		t.Fatalf("ping payload = %s, want \"pong\"", resp.Payload)
	}
}

// TestSporecallCallRelaysToService drives the generic relay end to end:
// script call(callId, payload) → host.invoke → service ref, with the
// response map decoded back onto the wire.
func TestSporecallCallRelaysToService(t *testing.T) {
	probe := &capturingRef{id: testutil.GenActorID(), body: []byte(`{"Echo":"hi"}`)}
	a := newSporecallActor(t, fakeSeams{services: map[string]ref.Ref{"probe": probe}})

	resp, err := callSporecall(t, a, "call", `["probe.echo", {"Text":"hi"}]`)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(string(resp.Payload), `"Echo":"hi"`) {
		t.Fatalf("call payload = %s, want Echo:hi", resp.Payload)
	}
	calls := probe.recorded()
	if len(calls) != 1 || calls[0].callID != "probe.echo" {
		t.Fatalf("probe recorded %v, want one probe.echo call", calls)
	}
	body, ok := calls[0].payload.([]byte)
	if !ok {
		t.Fatalf("probe payload type = %T, want []byte", calls[0].payload)
	}
	if !strings.Contains(string(body), `"Text":"hi"`) {
		t.Fatalf("probe payload = %s, want Text:hi", body)
	}
}

// TestSporecallNamedObjectPayload mirrors the agent tool path: the turn
// engine forwards the LLM's arguments as a JSON object, so a schema-less
// callable must accept {"callId": ..., "payload": ...} named after the script
// signature parameters — and reject unknown keys loudly.
func TestSporecallNamedObjectPayload(t *testing.T) {
	probe := &capturingRef{id: testutil.GenActorID(), body: []byte(`{"Echo":"hi"}`)}
	a := newSporecallActor(t, fakeSeams{services: map[string]ref.Ref{"probe": probe}})

	resp, err := callSporecall(t, a, "call", `{"callId": "probe.echo", "payload": {"Text": "hi"}}`)
	if err != nil {
		t.Fatalf("named-object call: %v", err)
	}
	if !strings.Contains(string(resp.Payload), `"Echo":"hi"`) {
		t.Fatalf("named-object payload = %s, want Echo:hi", resp.Payload)
	}
	calls := probe.recorded()
	if len(calls) != 1 || calls[0].callID != "probe.echo" {
		t.Fatalf("probe recorded %v, want one probe.echo call", calls)
	}

	// Empty object = zero args (the no-argument tool-call shape).
	if _, err := callSporecall(t, a, "ping", `{}`); err != nil {
		t.Fatalf("ping via empty object: %v", err)
	}

	// Unknown key must fail loudly, not silently drop the argument.
	if _, err := callSporecall(t, a, "call", `{"callID": "probe.echo"}`); err == nil {
		t.Fatal("unknown key accepted, want explicit error")
	} else if !strings.Contains(err.Error(), "unknown argument") {
		t.Fatalf("error = %v, want unknown argument diagnostic", err)
	}
}

// TestSporecallCallAppWrapsAppManagerInvoke drives host.invoke_app: the
// appmanager.invoke wire (bytes payload + agent identity) is assembled on
// the Go side and the response payload decoded back to a map.
func TestSporecallCallAppWrapsAppManagerInvoke(t *testing.T) {
	appmgr := &capturingRef{id: testutil.GenActorID(), body: mustJSONT(t, gen.AppManagerInvokeResp{Payload: []byte(`{"ok":true}`)})}
	a := newSporecallActor(t, fakeSeams{services: map[string]ref.Ref{"appmanager": appmgr}})

	resp, err := callSporecall(t, a, "call_app", `["builtin.demo", "twice", [21], "agent-1"]`)
	if err != nil {
		t.Fatalf("call_app: %v", err)
	}
	if !strings.Contains(string(resp.Payload), `"ok":true`) {
		t.Fatalf("call_app payload = %s, want ok:true", resp.Payload)
	}
	calls := appmgr.recorded()
	if len(calls) != 1 || calls[0].callID != "appmanager.invoke" {
		t.Fatalf("appmanager recorded %v, want one appmanager.invoke call", calls)
	}
	var req gen.AppManagerInvokeReq
	rawReq, ok := calls[0].payload.([]byte)
	if !ok {
		t.Fatalf("captured payload type = %T, want []byte", calls[0].payload)
	}
	if err := json.Unmarshal(rawReq, &req); err != nil {
		t.Fatalf("decode captured request: %v (raw: %s)", err, rawReq)
	}
	if req.ID != "builtin.demo" || req.Callable != "twice" || req.AgentID != "agent-1" {
		t.Fatalf("captured request = %+v, want builtin.demo/twice/agent-1", req)
	}
	if string(req.Payload) != `[21]` {
		t.Fatalf("captured nested payload = %s, want [21]", req.Payload)
	}
}

// TestSporecallCallUnknownService asserts the failure mode is an explicit
// error surfaced to the caller, not a silent empty result.
func TestSporecallCallUnknownService(t *testing.T) {
	a := newSporecallActor(t, fakeSeams{})
	_, err := callSporecall(t, a, "call", `["missing.echo", {}]`)
	if err == nil {
		t.Fatal("expected error for unknown service")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v, want 'not found'", err)
	}
}

func mustJSONT(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
