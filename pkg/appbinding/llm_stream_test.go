package appbinding

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func mustLLMRoute(t *testing.T) StreamRoute {
	t.Helper()
	route, ok := LookupStreamRoute("llm.complete")
	if !ok {
		t.Fatalf("llm.complete route not registered")
	}
	if route.Service != "aiaggregator" || route.Callable != "aiaggregator.dispatch" {
		t.Fatalf("route target = %s/%s, want aiaggregator/aiaggregator.dispatch", route.Service, route.Callable)
	}
	if !route.InjectCallerContext {
		t.Fatalf("llm route must inject caller context")
	}
	return route
}

// TestEncodeAggregatorChunk_WireShape pins the generic envelope 0x07 payload
// shape for the LLM vocabulary: lowercase keys, kind passthrough, per-kind
// data payload, and forward-compat relay of unknown kinds.
func TestEncodeAggregatorChunk_WireShape(t *testing.T) {
	route := mustLLMRoute(t)

	raw, err := route.Encode(domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hi"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if raw.Kind != "text_delta" || !strings.Contains(string(raw.Data), `"text":"hi"`) {
		t.Errorf("envelope = %+v, want kind text_delta and data.text", raw)
	}

	usageEnv, err := route.Encode(domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 1, OutputTokens: 2}})
	if err != nil {
		t.Fatalf("encode usage: %v", err)
	}
	if usageEnv.Kind != "usage" || !strings.Contains(string(usageEnv.Data), `"InputTokens":1`) {
		t.Errorf("usage envelope = %+v, want usage data payload", usageEnv)
	}

	// Non-aggregator values are a shape drift and must fail loudly.
	if _, err := route.Encode("not-a-chunk"); err == nil {
		t.Errorf("expected error for unexpected chunk type")
	}

	// Unknown kinds relay with kind preserved and empty data.
	env, err := route.Encode(domain.AggregatorChunk{Kind: "tool_use_delta"})
	if err != nil {
		t.Fatalf("encode unknown: %v", err)
	}
	if env.Kind != "tool_use_delta" || len(env.Data) != 0 {
		t.Errorf("unknown kind envelope = %+v, want kind passthrough and empty data", env)
	}
}

// TestLLMAggregator_Terminal pins the {Text, Reasoning, Usage} terminal the
// route's aggregator produces — the shape synchronous SDK clients consume.
func TestLLMAggregator_Terminal(t *testing.T) {
	route := mustLLMRoute(t)
	agg := route.Aggregate()
	for _, v := range []any{
		domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hello, "},
		domain.AggregatorChunk{Kind: domain.AggregatorChunkReasoning, Text: "think "},
		domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "world"},
		domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &domain.UsageData{InputTokens: 3, OutputTokens: 7}},
	} {
		if err := agg.Push(v); err != nil {
			t.Fatalf("Push: %v", err)
		}
	}
	raw, err := json.Marshal(agg.Terminal())
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["Text"] != "hello, world" || got["Reasoning"] != "think " {
		t.Errorf("terminal = %s, want joined text+reasoning", raw)
	}
	if !strings.Contains(string(raw), `"InputTokens":3`) {
		t.Errorf("terminal = %s, want usage", raw)
	}

	// A non-aggregator value aborts aggregation (shape drift must surface).
	if err := agg.Push("bogus"); err == nil {
		t.Errorf("expected error pushing unexpected chunk type")
	}
}

// TestAdaptLLMReq_PromptAndMessages pins the SDK→aggregator payload mapping
// now owned by the route: prompt flattening to a user message, message
// normalization, sampling params, and unit fields.
func TestAdaptLLMReq_PromptAndMessages(t *testing.T) {
	route := mustLLMRoute(t)

	body, err := route.AdaptReq([]byte(`{
		"prompt": "hello",
		"system": "sys",
		"model": "gpt-4o",
		"provider": "openai",
		"temperature": 0.5,
		"top_p": 0.9
	}`))
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var req domain.SendSessionMessageReq
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal adapted: %v", err)
	}
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" || req.Messages[0].Content[0].Text != "hello" {
		t.Errorf("prompt message = %+v", req.Messages)
	}
	if req.System != "sys" {
		t.Errorf("System = %q, want sys", req.System)
	}
	if req.Unit == nil || req.Unit.Model != "gpt-4o" || req.Unit.Provider != "openai" {
		t.Errorf("Unit = %+v", req.Unit)
	}
	if req.Temperature != 0.5 || req.TopP != 0.9 {
		t.Errorf("Temperature/TopP mismatch: %v %v", req.Temperature, req.TopP)
	}
	if req.SlotKind != "" {
		t.Errorf("SlotKind should be empty, got %q", req.SlotKind)
	}

	body2, err := route.AdaptReq([]byte(`{
		"messages": [
			{"role": "system", "content": "sys"},
			{"role": "user", "content": ["hi", {"type": "text", "text": "there"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("AdaptReq chat: %v", err)
	}
	var req2 domain.SendSessionMessageReq
	if err := json.Unmarshal(body2, &req2); err != nil {
		t.Fatalf("unmarshal adapted chat: %v", err)
	}
	if len(req2.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req2.Messages))
	}
	if req2.Messages[0].Role != "system" || req2.Messages[0].Content[0].Text != "sys" {
		t.Errorf("system message = %+v", req2.Messages[0])
	}
	if req2.Messages[1].Role != "user" || len(req2.Messages[1].Content) != 2 {
		t.Errorf("user message = %+v", req2.Messages[1])
	}
}

// TestAdaptLLMReq_CallerContext pins the telemetry contract: the reserved
// __AgentId/__WorkspaceId fields injected by the host bridge are mapped onto
// SendSessionMessageReq so usage stats are attributed to the originating
// agent, and __DeadlineAt is stripped.
func TestAdaptLLMReq_CallerContext(t *testing.T) {
	route := mustLLMRoute(t)

	body, err := route.AdaptReq([]byte(`{
		"prompt": "hello",
		"__AgentId": "agent-1",
		"__WorkspaceId": "ws-9",
		"__DeadlineAt": 12345
	}`))
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var req domain.SendSessionMessageReq
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.AgentID != "agent-1" {
		t.Errorf("AgentID = %q, want agent-1", req.AgentID)
	}
	if req.WorkspaceID != "ws-9" {
		t.Errorf("WorkspaceID = %q, want ws-9", req.WorkspaceID)
	}
	if strings.Contains(string(body), "__DeadlineAt") {
		t.Errorf("__DeadlineAt leaked: %s", body)
	}

	// Absent reserved fields leave both empty.
	body2, err := route.AdaptReq([]byte(`{"prompt": "x"}`))
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var req2 domain.SendSessionMessageReq
	if err := json.Unmarshal(body2, &req2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req2.AgentID != "" || req2.WorkspaceID != "" {
		t.Errorf("expected empty caller context, got AgentID=%q WorkspaceID=%q", req2.AgentID, req2.WorkspaceID)
	}
}

// TestAdaptLLMReq_UnitPinning pins the unit-selection transport: an explicit
// plugin pick (both provider and model) must arrive as UnitPinned so the
// aggregator honors it or fails loudly — never rotates across the pool.
// Half-specified selections are rejected outright (naked-model policy), and an
// explicit payload flag overrides the derivation both ways.
func TestAdaptLLMReq_UnitPinning(t *testing.T) {
	route := mustLLMRoute(t)

	// Both fields → pinned.
	body, err := route.AdaptReq([]byte(`{"prompt":"x","model":"deepseek-v4-flash","provider":"deepseek"}`))
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var req domain.SendSessionMessageReq
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Unit == nil || req.Unit.Model != "deepseek-v4-flash" || req.Unit.Provider != "deepseek" {
		t.Fatalf("unit = %+v, want deepseek/deepseek-v4-flash", req.Unit)
	}
	if !req.UnitPinned {
		t.Error("provider+model selection must pin the unit")
	}

	// Model only → rejected before dispatch (naked-model policy).
	if _, err := route.AdaptReq([]byte(`{"prompt":"x","model":"deepseek-v4-flash"}`)); err == nil {
		t.Fatal("model-only selection must be rejected, not soft-rotated")
	}

	// Explicit override wins over derivation.
	body, err = route.AdaptReq([]byte(`{"prompt":"x","model":"m","provider":"p","unitPinned":false}`))
	if err != nil {
		t.Fatalf("AdaptReq: %v", err)
	}
	var reqOverride domain.SendSessionMessageReq
	if err := json.Unmarshal(body, &reqOverride); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if reqOverride.UnitPinned {
		t.Error("explicit unitPinned=false must override the both-fields derivation")
	}
}

// TestAdaptLLMReq_NakedModelRejected pins the fail-fast contract for
// half-specified units: a plugin naming a model without a provider (or vice
// versa) must get an immediate error, never a silent degradation to whole-pool
// auto-pick — that sweep pulled aggregator-ref entries into the rotation and
// panel calls hung in nested child streams (2026-09-05 novel per-section hang).
func TestAdaptLLMReq_NakedModelRejected(t *testing.T) {
	route := mustLLMRoute(t)

	if _, err := route.AdaptReq([]byte(`{"prompt":"x","model":"deepseek-v4-flash"}`)); err == nil {
		t.Fatal("model without provider must be rejected with an immediate error")
	}

	if _, err := route.AdaptReq([]byte(`{"prompt":"x","provider":"deepseek"}`)); err == nil {
		t.Fatal("provider without model must be rejected with an immediate error")
	}

	// Both present or both absent stay valid.
	if _, err := route.AdaptReq([]byte(`{"prompt":"x","model":"m","provider":"p"}`)); err != nil {
		t.Fatalf("full unit selection must pass: %v", err)
	}
	if _, err := route.AdaptReq([]byte(`{"prompt":"x"}`)); err != nil {
		t.Fatalf("bare prompt (auto-pick) must pass: %v", err)
	}
}
