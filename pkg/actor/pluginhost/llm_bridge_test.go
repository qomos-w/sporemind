package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	gosporeactor "github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// fakeRef returns a scripted invoke.Call for every Invoke.
type fakeRef struct {
	call *invoke.Call
	// lastPayload records the most recent Invoke payload (nil-safe).
	lastPayload any
}

func (r *fakeRef) ID() id.ActorID          { return id.ActorID{} }
func (r *fakeRef) Service() (string, bool) { return "aiaggregator", true }
func (r *fakeRef) Invoke(_ context.Context, _ string, payload any, _ ...map[string]string) *invoke.Call {
	r.lastPayload = payload
	return r.call
}

// fakeStream yields a fixed list of values followed by io.EOF.
type fakeStream struct {
	values []any
	idx    int
}

func (s *fakeStream) Recv() (any, error) {
	if s.idx >= len(s.values) {
		return nil, io.EOF
	}
	v := s.values[s.idx]
	s.idx++
	return v, nil
}

func (s *fakeStream) RecvRaw() ([]byte, error) { return nil, errors.New("raw not supported") }
func (s *fakeStream) Close() error             { return nil }

// mustRoute fetches the registered llm stream route (appbinding registers
// llm.complete / llm.chat from its init), so bridge tests exercise the same
// catalog the production gate reads.
func mustRoute(t *testing.T, callID string) appbinding.StreamRoute {
	t.Helper()
	route, ok := appbinding.LookupStreamRoute(callID)
	if !ok {
		t.Fatalf("no stream route registered for %q", callID)
	}
	return route
}

func TestHandleHostBridgeStream_AggregatesStream(t *testing.T) {
	stream := &fakeStream{
		values: []any{
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hello, "},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkReasoning, Text: "think "},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "world"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 3, OutputTokens: 7}},
		},
	}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	a := &Actor{}

	resp, err := a.handleHostBridgeStream(DispatchContext{}, aggRef, mustRoute(t, "llm.complete"), "llm.complete", []byte(`{"prompt":"hi","model":"gpt-4o","provider":"openai"}`), nil)
	if err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if got["Text"] != "hello, world" {
		t.Errorf("Text = %q, want %q", got["Text"], "hello, world")
	}
	if got["Reasoning"] != "think " {
		t.Errorf("Reasoning = %q, want %q", got["Reasoning"], "think ")
	}
	usage, ok := got["Usage"].(map[string]any)
	if !ok {
		t.Fatalf("Usage missing or wrong type: %T", got["Usage"])
	}
	if usage["InputTokens"] != float64(3) || usage["OutputTokens"] != float64(7) {
		t.Errorf("Usage = %v, want InputTokens=3 OutputTokens=7", usage)
	}
}

// TestHandleHostBridgeStream_ForcedToolCallAggregates pins the bridge path a
// forced-tool-call plugin request takes: tools + tool_choice survive
// AdaptReq into the backing dispatch payload, and tool_use chunks land in the
// terminal ToolCalls with the stop reason preserved as a stream chunk.
func TestHandleHostBridgeStream_ForcedToolCallAggregates(t *testing.T) {
	stream := &fakeStream{
		values: []any{
			domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseStart, ToolUseID: "tu_1", ToolName: "extract"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseInputDelta, ToolUseID: "tu_1", InputDelta: `{"title"`},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseComplete, ToolUseID: "tu_1", ToolName: "extract", Input: `{"title":"Example Domain"}`},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkStop, StopReason: "tool_use"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 5, OutputTokens: 9}},
		},
	}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	a := &Actor{}

	payload := []byte(`{"prompt":"extract the title of example.com","model":"m","provider":"p","tools":[{"name":"extract","description":"extract a title","input_schema":{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}}],"tool_choice":"extract"}`)
	resp, err := a.handleHostBridgeStream(DispatchContext{}, aggRef, mustRoute(t, "llm.complete"), "llm.complete", payload, nil)
	if err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}

	// The adapted dispatch payload must carry tools + tool_choice through.
	raw, ok := aggRef.lastPayload.([]byte)
	if !ok {
		t.Fatalf("adapted payload type = %T, want []byte", aggRef.lastPayload)
	}
	var adapted domain.SendSessionMessageReq
	if err := json.Unmarshal(raw, &adapted); err != nil {
		t.Fatalf("decode adapted payload %s: %v", raw, err)
	}
	if len(adapted.Tools) != 1 || adapted.Tools[0].Name != "extract" || adapted.ToolChoice != "extract" {
		t.Errorf("adapted tools/tool_choice = %+v / %q", adapted.Tools, adapted.ToolChoice)
	}

	var got map[string]any
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	calls, ok := got["ToolCalls"].([]any)
	if !ok || len(calls) != 1 {
		t.Fatalf("ToolCalls = %#v, want one entry (wire %s)", got["ToolCalls"], resp)
	}
	call := calls[0].(map[string]any)
	if call["Name"] != "extract" || call["Id"] != "tu_1" {
		t.Errorf("ToolCalls[0] = %#v, want tu_1/extract", call)
	}
	args, _ := call["Arguments"].(map[string]any)
	if args["title"] != "Example Domain" {
		t.Errorf("ToolCalls[0].Arguments = %#v, want title Example Domain", call["Arguments"])
	}
}

// lazyLookupCtx returns nil for aiaggregator on the first call, then returns a
// fake ref on the second call.
type lazyLookupCtx struct {
	gosporeactor.Context
	calls int
	ref   ref.Ref
}

func (c *lazyLookupCtx) LookupService(name string) (ref.Ref, bool) {
	if name != "aiaggregator" {
		return nil, false
	}
	c.calls++
	if c.calls == 1 {
		return nil, false
	}
	return c.ref, true
}

func TestStreamServiceRef_LazyLookupAndCache(t *testing.T) {
	stream := &fakeStream{values: []any{domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "ok"}}}
	fakeAgg := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	ctx := &lazyLookupCtx{ref: fakeAgg}

	a := &Actor{actorCtx: ctx}
	// First call misses the snapshot and must do a live lookup.
	ref1 := a.streamServiceRef("aiaggregator")
	if ref1 != nil {
		t.Fatalf("expected nil on first lookup, got %v", ref1)
	}
	// Simulate aiaggregator exposing itself after pluginhost started.
	ref2 := a.streamServiceRef("aiaggregator")
	if ref2 == nil {
		t.Fatalf("expected live ref on second lookup")
	}
	if ctx.calls != 2 {
		t.Fatalf("expected 2 LookupService calls, got %d", ctx.calls)
	}
	// Third call should be cached and not increase LookupService count.
	ref3 := a.streamServiceRef("aiaggregator")
	if ref3 != ref2 {
		t.Fatalf("expected cached ref")
	}
	if ctx.calls != 2 {
		t.Fatalf("expected cache hit, got %d calls", ctx.calls)
	}

	_, err := a.handleHostBridgeStream(DispatchContext{}, a.streamServiceRef("aiaggregator"), mustRoute(t, "llm.complete"), "llm.complete", []byte(`{"prompt":"x"}`), nil)
	if err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}
}

// TestInjectLLMCallerContext pins the bridge-side injection: the outer
// deadline and workspace scope land in the JSON payload as reserved fields
// that the route's payload adapter consumes. The caller AgentID must NEVER
// be injected — plugin LLM usage is the plugin's own, not the invoking
// agent's (agent affinity routed plugin calls into nested child-aggregator
// waits; see injectLLMCallerContext). Malformed payloads are returned
// untouched.
func TestInjectLLMCallerContext(t *testing.T) {
	out := injectLLMCallerContext([]byte(`{"prompt":"hi"}`), DispatchContext{AgentID: "a1", WorkspaceID: "w1", DeadlineAt: 123})
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["__WorkspaceId"] != "w1" {
		t.Errorf("injected fields = %v", m)
	}
	if _, present := m["__AgentId"]; present {
		t.Errorf("__AgentId must not be injected into plugin LLM calls: %v", m)
	}
	if m["prompt"] != "hi" {
		t.Errorf("original payload lost: %v", m)
	}

	// Empty context leaves the payload untouched.
	same := injectLLMCallerContext([]byte(`{"prompt":"hi"}`), DispatchContext{})
	if string(same) != `{"prompt":"hi"}` {
		t.Errorf("empty context should not modify payload, got %s", same)
	}
	// Malformed JSON is passed through, never silently replaced.
	bad := injectLLMCallerContext([]byte(`{bad`), DispatchContext{AgentID: "a1"})
	if string(bad) != `{bad` {
		t.Errorf("malformed payload should pass through, got %s", bad)
	}
}

// recordingRef captures the context passed to Invoke so tests can assert the
// budget derived by handleHostBridgeStream.
type recordingRef struct {
	ctx  context.Context
	call *invoke.Call
}

func (r *recordingRef) ID() id.ActorID          { return id.ActorID{} }
func (r *recordingRef) Service() (string, bool) { return "aiaggregator", true }
func (r *recordingRef) Invoke(ctx context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	r.ctx = ctx
	return r.call
}

// delayedStream sleeps before returning the first value; subsequent values
// stream immediately.
type delayedStream struct {
	fakeStream
	delay time.Duration
}

func (s *delayedStream) Recv() (any, error) {
	if s.idx == 0 && s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.fakeStream.Recv()
}

// TestHandleHostBridgeStream_InheritsOuterDeadline verifies that the reverse
// llm.* call inherits the outer invoke deadline forwarded via __DeadlineAt
// instead of using the fixed 30s host-bridge cap: even if the first
// aggregator chunk arrives after 30s, the reverse call must not explode
// early.
func TestHandleHostBridgeStream_InheritsOuterDeadline(t *testing.T) {
	outerDeadline := time.Now().Add(300 * time.Second)
	payload, _ := json.Marshal(map[string]any{
		"prompt":       "hi",
		"__DeadlineAt": outerDeadline.UnixMilli(),
	})

	rec := &recordingRef{call: invoke.NewCall(invoke.CallModeStream, &fakeStream{
		values: []any{domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "ok"}},
	})}
	a := &Actor{}
	if _, err := a.handleHostBridgeStream(DispatchContext{}, rec, mustRoute(t, "llm.complete"), "llm.complete", payload, nil); err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}
	if rec.ctx == nil {
		t.Fatal("Invoke context not recorded")
	}
	d, ok := rec.ctx.Deadline()
	if !ok {
		t.Fatal("expected derived deadline")
	}
	want := outerDeadline.Add(-reverseHeadroom)
	if d.Before(want.Add(-time.Second)) || d.After(want.Add(time.Second)) {
		t.Errorf("deadline = %v, want ~%v", d, want)
	}
}

// TestHandleHostBridgeStream_SlowFirstChunkWithinDerivedBudget verifies that
// a chunk delayed past the old 30s fallback but still inside the outer budget
// streams through without timing out. The actual 30s+ wait is replaced by the
// deadline assertion above; here we just ensure the derived-budget path does
// not reject a modestly delayed first chunk.
func TestHandleHostBridgeStream_SlowFirstChunkWithinDerivedBudget(t *testing.T) {
	outerDeadline := time.Now().Add(300 * time.Second)
	payload, _ := json.Marshal(map[string]any{
		"prompt":       "hi",
		"__DeadlineAt": outerDeadline.UnixMilli(),
	})

	stream := &delayedStream{delay: 50 * time.Millisecond, fakeStream: fakeStream{
		values: []any{domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "ok"}},
	}}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	a := &Actor{}

	start := time.Now()
	resp, err := a.handleHostBridgeStream(DispatchContext{}, aggRef, mustRoute(t, "llm.complete"), "llm.complete", payload, nil)
	if err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("call took too long: %v", time.Since(start))
	}
	var got map[string]any
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if got["Text"] != "ok" {
		t.Errorf("Text = %q, want ok", got["Text"])
	}
}

// TestDeadlineAt_InjectAndConsume verifies the full round-trip:
// injectLLMCallerContext writes __DeadlineAt, streamBudgetContext consumes
// it to derive the context, and the route's payload adapter strips it so the
// aggregator request never sees the reserved key.
func TestDeadlineAt_InjectAndConsume(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	out := injectLLMCallerContext([]byte(`{"prompt":"hi"}`), DispatchContext{
		AgentID: "a1", WorkspaceID: "w1", DeadlineAt: deadline.UnixMilli(),
	})
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["__DeadlineAt"] != float64(deadline.UnixMilli()) {
		t.Errorf("__DeadlineAt = %v, want %v", payload["__DeadlineAt"], deadline.UnixMilli())
	}

	ctx, cancel := streamBudgetContext(nil, out, hostBridgeInvokeTimeout)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected derived deadline")
	}
	want := deadline.Add(-reverseHeadroom)
	if got.Before(want.Add(-time.Second)) || got.After(want.Add(time.Second)) {
		t.Errorf("derived deadline = %v, want ~%v", got, want)
	}

	body, err := mustRoute(t, "llm.complete").AdaptReq(out)
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var req struct {
		AgentID     string `json:"AgentID"`
		WorkspaceID string `json:"WorkspaceID"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal adapted body: %v", err)
	}
	if req.AgentID != "" {
		t.Errorf("plugin LLM calls must stay agent-less, got AgentID=%q", req.AgentID)
	}
	if req.WorkspaceID != "w1" {
		t.Errorf("workspace scope lost: %+v", req)
	}
	// __DeadlineAt is consumed (stripped) and should not leak downstream.
	if strings.Contains(string(body), "__DeadlineAt") {
		t.Errorf("__DeadlineAt leaked into aggregator request: %s", body)
	}
}

// TestDeadlineAt_FallbackToHostBridgeTimeout verifies that an absent or expired
// __DeadlineAt falls back to the fixed hostBridgeInvokeTimeout cap.
func TestDeadlineAt_FallbackToHostBridgeTimeout(t *testing.T) {
	ctx, cancel := streamBudgetContext(nil, []byte(`{"prompt":"hi"}`), hostBridgeInvokeTimeout)
	defer cancel()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected fallback deadline")
	}
	remaining := time.Until(d)
	if remaining < hostBridgeInvokeTimeout-2*time.Second || remaining > hostBridgeInvokeTimeout+2*time.Second {
		t.Errorf("fallback budget = %v, want ~%v", remaining, hostBridgeInvokeTimeout)
	}

	// Expired deadline also falls back.
	past := time.Now().Add(-time.Second).UnixMilli()
	expired, _ := json.Marshal(map[string]any{"__DeadlineAt": float64(past)})
	ctx2, cancel2 := streamBudgetContext(nil, expired, hostBridgeInvokeTimeout)
	defer cancel2()
	if _, ok := ctx2.Deadline(); !ok {
		t.Fatal("expected fallback deadline for expired __DeadlineAt")
	}
}

// TestRouteBudget_HTTPPathFallback verifies the llm.* route budget replaces
// the fixed 30s cap for streaming reverse calls that arrive without an outer
// invoke deadline — the HTTP-data-path origin (plugin handler serving a
// gateway request), where every llm.complete used to die at exactly 30s
// regardless of the appdef-declared timeout_ms.
func TestRouteBudget_HTTPPathFallback(t *testing.T) {
	route, ok := appbinding.LookupStreamRoute("llm.complete")
	if !ok {
		t.Fatal("llm.complete stream route missing")
	}
	if route.Budget <= hostBridgeInvokeTimeout {
		t.Fatalf("llm.complete route Budget = %v, want > generic cap %v", route.Budget, hostBridgeInvokeTimeout)
	}

	// Child side: no __DeadlineAt in the payload -> fallback = route budget.
	ctx, cancel := streamBudgetContext(nil, []byte(`{"prompt":"hi"}`), route.Budget)
	defer cancel()
	d, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected route-budget deadline")
	}
	if remaining := time.Until(d); remaining < route.Budget-2*time.Second || remaining > route.Budget+2*time.Second {
		t.Errorf("route-budget fallback = %v, want ~%v", remaining, route.Budget)
	}

	// Outer budget still wins over the route budget when present.
	outer, oc := context.WithTimeout(context.Background(), 10*time.Second)
	defer oc()
	derived, dc := reverseBudgetContext(outer, "llm.complete")
	defer dc()
	dd, ok := derived.Deadline()
	if !ok {
		t.Fatal("expected derived deadline")
	}
	if want := time.Until(dd); want > 10*time.Second {
		t.Errorf("derived reverse budget %v exceeds outer 10s", want)
	}

	// No outer deadline + llm callID -> route budget, not the 30s generic cap.
	routeCtx, rc := reverseBudgetContext(context.Background(), "llm.complete")
	defer rc()
	rd, ok := routeCtx.Deadline()
	if !ok {
		t.Fatal("expected route-budget deadline")
	}
	if remaining := time.Until(rd); remaining <= hostBridgeInvokeTimeout+2*time.Second {
		t.Errorf("llm.complete reverse budget = %v, want route budget %v (generic cap is %v)", remaining, route.Budget, hostBridgeInvokeTimeout)
	}

	// No outer deadline + non-streaming callID -> generic cap unchanged.
	plainCtx, pc := reverseBudgetContext(context.Background(), "config.get")
	defer pc()
	pd, ok := plainCtx.Deadline()
	if !ok {
		t.Fatal("expected generic-cap deadline")
	}
	if remaining := time.Until(pd); remaining > hostBridgeInvokeTimeout+2*time.Second {
		t.Errorf("non-streaming reverse budget = %v, want generic cap %v", remaining, hostBridgeInvokeTimeout)
	}
}

// TestHandleHostBridgeStream_ForwardsOrderedChunks pins the streaming
// contract: every aggregator chunk reaches onChunk exactly once, in stream
// order, encoded in the generic envelope {kind, data}, while the terminal
// aggregation stays identical to the unary path.
func TestHandleHostBridgeStream_ForwardsOrderedChunks(t *testing.T) {
	stream := &fakeStream{
		values: []any{
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hello, "},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkReasoning, Text: "think "},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "world"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 3, OutputTokens: 7}},
		},
	}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	a := &Actor{}

	// onChunk fires synchronously from the dispatch goroutine, so no lock is
	// needed to collect.
	type wireChunk struct {
		Kind string          `json:"kind"`
		Data json.RawMessage `json:"data"`
	}
	var chunks []wireChunk
	resp, err := a.handleHostBridgeStream(DispatchContext{}, aggRef, mustRoute(t, "llm.complete"), "llm.complete",
		[]byte(`{"prompt":"hi"}`),
		func(wire []byte) error {
			var c wireChunk
			if err := json.Unmarshal(wire, &c); err != nil {
				return err
			}
			chunks = append(chunks, c)
			return nil
		})
	if err != nil {
		t.Fatalf("handleHostBridgeStream: %v", err)
	}

	if len(chunks) != 4 {
		t.Fatalf("got %d chunks, want 4", len(chunks))
	}
	wantKinds := []string{
		domain.AggregatorChunkText,
		domain.AggregatorChunkReasoning,
		domain.AggregatorChunkText,
		domain.AggregatorChunkUsage,
	}
	for i, want := range wantKinds {
		if chunks[i].Kind != want {
			t.Errorf("chunk[%d].Kind = %q, want %q", i, chunks[i].Kind, want)
		}
	}
	var d struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(chunks[0].Data, &d); err != nil || d.Text != "hello, " {
		t.Errorf("chunk[0].Data = %s, want text %q", chunks[0].Data, "hello, ")
	}
	if err := json.Unmarshal(chunks[1].Data, &d); err != nil || d.Text != "think " {
		t.Errorf("chunk[1].Data = %s, want text %q", chunks[1].Data, "think ")
	}
	if err := json.Unmarshal(chunks[2].Data, &d); err != nil || d.Text != "world" {
		t.Errorf("chunk[2].Data = %s, want text %q", chunks[2].Data, "world")
	}
	if !strings.Contains(string(chunks[3].Data), `"InputTokens":3`) {
		t.Errorf("usage chunk data = %s, want InputTokens 3", chunks[3].Data)
	}

	// Terminal aggregation identical to unary.
	var got map[string]any
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if got["Text"] != "hello, world" {
		t.Errorf("Text = %q, want %q", got["Text"], "hello, world")
	}
	if got["Reasoning"] != "think " {
		t.Errorf("Reasoning = %q, want %q", got["Reasoning"], "think ")
	}
}

// TestHandleHostBridgeStream_ConsumerAbort pins the abort contract:
// an onChunk error stops the stream (no further chunks delivered) and
// surfaces as an error return.
func TestHandleHostBridgeStream_ConsumerAbort(t *testing.T) {
	stream := &fakeStream{
		values: []any{
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "a"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "b"},
			domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "c"},
		},
	}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}
	a := &Actor{}

	var delivered int
	_, err := a.handleHostBridgeStream(DispatchContext{}, aggRef, mustRoute(t, "llm.chat"), "llm.chat",
		[]byte(`{"messages":[]}`),
		func([]byte) error {
			delivered++
			if delivered == 2 {
				return errors.New("consumer gone")
			}
			return nil
		})
	if err == nil || !strings.Contains(err.Error(), "aborted by consumer") {
		t.Fatalf("err = %v, want stream aborted by consumer", err)
	}
	if delivered != 2 {
		t.Errorf("delivered = %d, want 2 (abort stops further chunks)", delivered)
	}
}

// TestStreamCatalog_Gates pins that the bridge catalog (not a domain prefix)
// decides streaming and caller-context injection: llm.complete / llm.chat are
// registered with caller-context injection, while callIDs like provider.list
// (a plain unary host call) are absent.
func TestStreamCatalog_Gates(t *testing.T) {
	for _, callID := range []string{"llm.complete", "llm.chat"} {
		if !appbinding.IsStreamingCallable(callID) {
			t.Errorf("IsStreamingCallable(%q) = false, want true", callID)
		}
		if !appbinding.StreamRouteInjectsCallerContext(callID) {
			t.Errorf("StreamRouteInjectsCallerContext(%q) = false, want true", callID)
		}
	}
	for _, callID := range []string{"provider.list", "config.get", "llm.embeddings"} {
		if appbinding.IsStreamingCallable(callID) {
			t.Errorf("IsStreamingCallable(%q) = true, want false", callID)
		}
		if appbinding.StreamRouteInjectsCallerContext(callID) {
			t.Errorf("StreamRouteInjectsCallerContext(%q) = true, want false", callID)
		}
	}
}

// TestHostCallBudget_UnaryMediaGenLiftsGenericCap verifies the unary host-call
// budget path: image.generate / video.generate declare HostCallDef.Budget, so
// an HTTP-data-path reverse call (no outer invoke deadline) inherits the
// per-kind budget instead of dying at the generic 30s cap mid-generation —
// the same failure class the llm route budget fixed for streams.
func TestHostCallBudget_UnaryMediaGenLiftsGenericCap(t *testing.T) {
	for _, c := range []struct {
		callID string
		want   time.Duration
	}{
		{"image.generate", 2 * time.Minute},
		{"video.generate", 12 * time.Minute},
	} {
		ctx, cancel := reverseBudgetContext(context.Background(), c.callID)
		defer cancel()
		d, ok := ctx.Deadline()
		if !ok {
			t.Fatalf("%s: expected budget deadline", c.callID)
		}
		if remaining := time.Until(d); remaining < c.want-2*time.Second || remaining > c.want+2*time.Second {
			t.Errorf("%s reverse budget = %v, want ~%v (generic cap is %v)", c.callID, remaining, c.want, hostBridgeInvokeTimeout)
		}
		// An outer invoke deadline still wins over the per-call budget.
		outer, oc := context.WithTimeout(context.Background(), 5*time.Second)
		defer oc()
		derived, dc := reverseBudgetContext(outer, c.callID)
		defer dc()
		if dd, ok := derived.Deadline(); !ok || time.Until(dd) > 5*time.Second {
			t.Errorf("%s: outer deadline must cap the reverse budget", c.callID)
		}
	}
	// Generic callIDs keep the 30s cap (guard against accidental global lift).
	plainCtx, cancel := reverseBudgetContext(context.Background(), "config.get")
	defer cancel()
	if d, ok := plainCtx.Deadline(); !ok || time.Until(d) > hostBridgeInvokeTimeout+2*time.Second {
		t.Errorf("config.get must keep the generic cap, got %v", time.Until(d))
	}
}
