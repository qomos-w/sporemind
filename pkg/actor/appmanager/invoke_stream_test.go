package appmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// streamTestPlanner is a Planner whose Stream delivers a fixed chunk sequence
// through onChunk on the same goroutine the promise executor runs on, then
// resolves (or rejects when a chunk delivery callback fails) — mirroring the
// real planner.Stream contract so the appmanager relay runs its full
// planning-await cycle in a unit test.
type streamTestPlanner struct {
	chunks []gen.PluginInvokeChunk
	err    error
}

func (p streamTestPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}
func (p streamTestPlanner) Call(_ context.Context, _ ref.Ref, _ string, _ any) *promise.Promise[any] {
	return nil
}
func (p streamTestPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Async[any](func(resolve func(any), reject func(any)) {
		for _, chunk := range p.chunks {
			if err := onChunk(chunk); err != nil {
				reject(err)
				return
			}
		}
		if p.err != nil {
			reject(p.err)
			return
		}
		resolve(nil)
	})
}

// streamTestActor builds an Actor with one registered app (manifest + running
// record + child route) plus the security policy and session machinery needed
// for resolveInvokeAuth to progress past the authorization preamble.
func streamTestActor(t *testing.T, manifest gen.AppManifest) *Actor {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return &Actor{
		Apps:       map[string]gen.AppManifest{manifest.ID: manifest},
		Records:    map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running", PackageHash: "h"}},
		children:   map[string]string{manifest.ID: testutil.GenActorID().String()},
		sessions:   map[string]appSession{},
		sessionKey: key,
		bindings:   appbinding.NewRegistry(),
	}
}

// streamTestCtx returns a FakeCtx with an authenticated external identity and
// the pluginhost service lookup + planner wired. Zero identity is used by the
// external-caller-without-session test.
func streamTestCtx(planner actor.Planner, lookupService func(string) (ref.Ref, bool)) *testutil.FakeCtx {
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"},
		PlannerFn: func() actor.Planner { return planner },
	}
	if lookupService != nil {
		ctx.LookupServiceFn = lookupService
	}
	return ctx
}

// TestInvokeStreamNativeRelaysChunksAndTerminal proves the native path proxies
// pluginhost.invoke_stream: every intermediate forward chunk is re-emitted in
// order, followed by exactly one Terminal=true chunk carrying the plugin's
// terminal payload.
func TestInvokeStreamNativeRelaysChunksAndTerminal(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.stream", Runtime: "native",
		Callables: []gen.AppCallableDescriptor{{ID: "stream.call"}},
	}
	a := streamTestActor(t, manifest)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	planner := streamTestPlanner{chunks: []gen.PluginInvokeChunk{
		{Payload: []byte("chunk-1")},
		{Payload: []byte("chunk-2")},
		{Payload: []byte("terminal"), Terminal: true},
	}}
	ctx := streamTestCtx(planner, func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	})
	emit := testutil.NewFakeEmitter()

	err := a.handleInvokeStream(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "stream.call", Payload: []byte(`{}`)}, emit)
	if err != nil {
		t.Fatalf("handleInvokeStream: %v", err)
	}
	if len(emit.Chunks) != 3 {
		t.Fatalf("expected 3 emitted chunks, got %d: %+v", len(emit.Chunks), emit.Chunks)
	}
	for i, want := range [][]byte{[]byte("chunk-1"), []byte("chunk-2")} {
		chunk, ok := emit.Chunks[i].(gen.PluginInvokeChunk)
		if !ok || string(chunk.Payload) != string(want) || chunk.Terminal {
			t.Fatalf("chunk[%d] = %+v, want payload %q, Terminal=false", i, emit.Chunks[i], want)
		}
	}
	terminal, ok := emit.Chunks[2].(gen.PluginInvokeChunk)
	if !ok || !terminal.Terminal || string(terminal.Payload) != "terminal" {
		t.Fatalf("terminal chunk = %+v, want Payload=%q Terminal=true", emit.Chunks[2], "terminal")
	}
	// The dispatcher records a successful audit for the invoked callable,
	// same shape as the unary handleInvoke (auditRuntime stamps the runtime).
	if len(a.AuditRecords) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(a.AuditRecords))
	}
	rec := a.AuditRecords[0]
	if !rec.Allowed || rec.Callable != "stream.call" || rec.AppID != manifest.ID || rec.Runtime != "native" {
		t.Fatalf("unexpected audit record: %+v", rec)
	}
}

// TestInvokeStreamNativeForwardsTimeoutMs proves the streaming proxy forwards
// the callable's declared TimeoutMs budget to pluginhost.invoke_stream exactly
// like the unary invokeNative does.
func TestInvokeStreamNativeForwardsTimeoutMs(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.slow", Runtime: "native",
		Callables: []gen.AppCallableDescriptor{{ID: "slow.call", TimeoutMs: 90_000}},
	}
	a := streamTestActor(t, manifest)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	var captured gen.PluginInvokeReq
	planner := streamTestPlanner{chunks: []gen.PluginInvokeChunk{{Payload: []byte("done"), Terminal: true}}}
	ctx := streamTestCtx(planner, func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	})
	// Override the planner to capture the forwarded PluginInvokeReq.
	capturing := &captureStreamPlanner{inner: planner, capture: func(callID string, payload any) {
		if callID == "pluginhost.invoke_stream" {
			captured = payload.(gen.PluginInvokeReq)
		}
	}}
	ctx.PlannerFn = func() actor.Planner { return capturing }

	err := a.handleInvokeStream(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "slow.call"}, testutil.NewFakeEmitter())
	if err != nil {
		t.Fatalf("handleInvokeStream: %v", err)
	}
	if captured.TimeoutMs != 90_000 {
		t.Fatalf("TimeoutMs = %d, want 90000", captured.TimeoutMs)
	}
}

// captureStreamPlanner delegates Stream to an inner planner while observing
// the callID/payload of each streamed invocation.
type captureStreamPlanner struct {
	inner   actor.Planner
	capture func(callID string, payload any)
}

func (c *captureStreamPlanner) Plan(t ref.Ref, callID string, payload any, opts ...plan.Option) (plan.Node, error) {
	return c.inner.Plan(t, callID, payload, opts...)
}
func (c *captureStreamPlanner) Call(ctx context.Context, t ref.Ref, callID string, payload any) *promise.Promise[any] {
	c.capture(callID, payload)
	return c.inner.Call(ctx, t, callID, payload)
}
func (c *captureStreamPlanner) Stream(ctx context.Context, t ref.Ref, callID string, payload any, onChunk func(any) error) *promise.Promise[any] {
	c.capture(callID, payload)
	return c.inner.Stream(ctx, t, callID, payload, onChunk)
}

// rawReplyStream delivers a single raw body frame then io.EOF, mirroring the
// transport stream whose RecvRaw yields the encoded reply frame body.
type rawReplyStream struct {
	body     []byte
	consumed bool
}

func (s *rawReplyStream) Recv() (any, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	return s.body, nil
}
func (s *rawReplyStream) RecvRaw() ([]byte, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	return s.body, nil
}
func (s *rawReplyStream) Close() error { return nil }

// rawReplyRef is a ref.Ref whose unary Invoke returns a raw reply body —
// unlike testutil.NewFakeRef, whose RecvRaw always returns io.EOF.
type rawReplyRef struct {
	actorID id.ActorID
	body    []byte
}

func (f rawReplyRef) ID() id.ActorID        { return f.actorID }
func (rawReplyRef) Service() (string, bool) { return "", false }
func (f rawReplyRef) Invoke(_ context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, &rawReplyStream{body: f.body})
}

// TestInvokeStreamSporeDegradesToUnaryTerminalChunk proves the spore runtime
// runs the same unary dispatch as handleInvoke and emits exactly one
// Terminal=true chunk carrying the callable's response payload.
func TestInvokeStreamSporeDegradesToUnaryTerminalChunk(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "spore.stream", Runtime: "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "answer"}},
	}
	a := streamTestActor(t, manifest)
	// Mirrors handleInvoke's spore branch: the child actor is resolved via
	// canonical actor ID and its reply is the encoded SporeAppInvokeResp.
	canonical, err := identity.ParseCanonicalID(a.children[manifest.ID])
	if err != nil {
		t.Fatal(err)
	}
	childActorID := id.From(canonical)
	resp := gen.SporeAppInvokeResp{Payload: []byte(`{"answer":42}`)}
	body, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	childRef := rawReplyRef{actorID: childActorID, body: body}
	ctx := streamTestCtx(nil, nil)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid == childActorID {
			return childRef, true
		}
		return nil, false
	}
	emit := testutil.NewFakeEmitter()

	err = a.handleInvokeStream(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", Payload: []byte(`{"q":1}`)}, emit)
	if err != nil {
		t.Fatalf("handleInvokeStream: %v", err)
	}
	if len(emit.Chunks) != 1 {
		t.Fatalf("expected exactly 1 terminal chunk, got %d: %+v", len(emit.Chunks), emit.Chunks)
	}
	chunk, ok := emit.Chunks[0].(gen.PluginInvokeChunk)
	if !ok || !chunk.Terminal || string(chunk.Payload) != `{"answer":42}` {
		t.Fatalf("chunk = %+v, want Terminal=true Payload=%q", emit.Chunks[0], `{"answer":42}`)
	}
	if len(a.AuditRecords) != 1 || !a.AuditRecords[0].Allowed {
		t.Fatalf("expected one allowed audit record, got %+v", a.AuditRecords)
	}
}

// TestInvokeStreamExternalCallerWithoutSessionDenied proves the shared auth
// preamble is enforced on the streaming path too: an external caller (zero
// identity) without a session token is denied with CodeSessionRequired and no
// chunk is emitted.
func TestInvokeStreamExternalCallerWithoutSessionDenied(t *testing.T) {
	manifest := gen.AppManifest{ID: "spore.deny", Runtime: "spore", Callables: []gen.AppCallableDescriptor{{ID: "answer"}}}
	a := streamTestActor(t, manifest)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{}} // zero identity = external
	emit := testutil.NewFakeEmitter()

	err := a.handleInvokeStream(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "answer", AgentID: "agent-1"}, emit)
	if err == nil {
		t.Fatal("expected session-required denial for external caller")
	}
	if appbinding.DenialCode(err) != appbinding.CodeSessionRequired {
		t.Fatalf("denial code = %q, want %q", appbinding.DenialCode(err), appbinding.CodeSessionRequired)
	}
	if len(emit.Chunks) != 0 {
		t.Fatalf("no chunks must be emitted on auth denial, got %d", len(emit.Chunks))
	}
	if len(a.AuditRecords) != 1 || a.AuditRecords[0].Allowed {
		t.Fatalf("expected one denied audit record, got %+v", a.AuditRecords)
	}
}

// TestInvokeStreamNativeNonChunkValueErrors proves a malformed upstream value
// (not a PluginInvokeChunk) surfaces as an error from the stream relay.
func TestInvokeStreamNativeNonChunkValueErrors(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.bad", Runtime: "native",
		Callables: []gen.AppCallableDescriptor{{ID: "bad.call"}},
	}
	a := streamTestActor(t, manifest)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	bad := &badValuePlanner{v: "not-a-chunk"}
	ctx := streamTestCtx(bad, func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	})

	err := a.handleInvokeStream(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "bad.call"}, testutil.NewFakeEmitter())
	if err == nil {
		t.Fatal("expected error for non-chunk stream value")
	}
}

// badValuePlanner delivers a single non-chunk value over Stream.
type badValuePlanner struct {
	v any
}

func (p *badValuePlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}
func (p *badValuePlanner) Call(_ context.Context, _ ref.Ref, _ string, _ any) *promise.Promise[any] {
	return nil
}
func (p *badValuePlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Async[any](func(resolve func(any), reject func(any)) {
		if err := onChunk(p.v); err != nil {
			reject(fmt.Errorf("chunk delivery failed: %w", err))
			return
		}
		resolve(nil)
	})
}
