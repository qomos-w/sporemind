package appbinding

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestDerivedCapabilities pins the manifest translation: host callIDs derive
// their capabilities (sorted, deduped), app-internal labels pass through.
func TestDerivedCapabilities(t *testing.T) {
	got := DerivedCapabilities([]string{"state.set", "llm.chat", "public", "state.get", "llm.complete", "project.read_file", "registry.query"})
	want := []string{"app.state", "fs.read", "llm.invoke", "public", "registry.read"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DerivedCapabilities = %v, want %v", got, want)
	}
}

// TestSDKCallCatalogIntegrity pins the catalog invariants: every callID maps
// to a runtime capability (the gate never sees an unmapped entry), streaming
// entries delegate to an SDK client method, and request/response types (when
// present) are struct types this package owns so the codegen reflect walker
// can extract them.
func TestSDKCallCatalogIntegrity(t *testing.T) {
	if len(SDKCallCatalog) == 0 {
		t.Fatal("SDKCallCatalog is empty")
	}
	for _, c := range SDKCallCatalog {
		if HostCallCapability(c.CallID) == "" {
			t.Errorf("catalog callID %q has no capability mapping in HostCallCapability", c.CallID)
		}
		if c.CallID == "" {
			t.Errorf("catalog entry with empty CallID: %+v", c)
		}
		if c.StreamChunkKind != "" && !IsSDKCallStreaming(c.CallID) {
			t.Errorf("callID %q: StreamChunkKind set on a call with no registered stream route", c.CallID)
		}
		if IsSDKCallStreaming(c.CallID) {
			route, _ := LookupStreamRoute(c.CallID)
			if route.TerminalType != c.RespType {
				t.Errorf("callID %q: route TerminalType %v != catalog RespType %v (drift between the bridge route and the SDK wire contract)", c.CallID, route.TerminalType, c.RespType)
			}
		}
		for _, typ := range []reflect.Type{c.ReqType, c.RespType} {
			if typ == nil {
				continue
			}
			if typ.Kind() != reflect.Struct || typ.PkgPath() == "" {
				t.Errorf("callID %q: contract type %s must be a named struct", c.CallID, typ)
			}
		}
	}
}

// TestSDKCallCatalogLLMRequestMatchesAdapter pins that a marshaled LLMReq is
// fully consumed by adaptLLMPayload: every field the SDK contract exposes must
// reach the backing SendSessionMessageReq, otherwise the generated typed
// caller would silently drop app input.
func TestSDKCallCatalogLLMRequestMatchesAdapter(t *testing.T) {
	temp := 0.7
	topP := 0.9
	freq := 1.5
	pres := 0.2
	budget := int32(4096)
	req := LLMReq{
		Prompt:           "hello",
		System:           "be terse",
		Model:            "gpt-x",
		Provider:         "prov",
		Temperature:      &temp,
		TopP:             &topP,
		FrequencyPenalty: &freq,
		PresencePenalty:  &pres,
		ReasoningEffort:  "high",
		ThinkingBudget:   &budget,
		Tools: []LLMToolSpec{{
			Name:        "summarize",
			Description: "summarize a page",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}}}`),
		}},
		ToolChoice: "summarize",
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal LLMReq: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal LLMReq wire: %v", err)
	}
	adapted, err := adaptLLMPayload(payload)
	if err != nil {
		t.Fatalf("adaptLLMPayload: %v", err)
	}
	if adapted.Messages == nil || len(adapted.Messages) != 1 || adapted.Messages[0].Content[0].Text != "hello" {
		t.Errorf("adaptLLMPayload lost the prompt: %+v", adapted.Messages)
	}
	if adapted.System != "be terse" {
		t.Errorf("System = %q, want %q", adapted.System, "be terse")
	}
	if adapted.Unit == nil || adapted.Unit.Model != "gpt-x" || adapted.Unit.Provider != "prov" {
		t.Errorf("Unit = %+v, want model gpt-x provider prov", adapted.Unit)
	}
	if adapted.Temperature != 0.7 || adapted.TopP != 0.9 {
		t.Errorf("Temperature/TopP = %v/%v, want 0.7/0.9", adapted.Temperature, adapted.TopP)
	}
	if adapted.FrequencyPenalty != 1.5 || adapted.PresencePenalty != 0.2 {
		t.Errorf("penalties = %v/%v, want 1.5/0.2", adapted.FrequencyPenalty, adapted.PresencePenalty)
	}
	if adapted.ReasoningEffort != "high" || adapted.ThinkingBudget != 4096 {
		t.Errorf("reasoning = %q/%d, want high/4096", adapted.ReasoningEffort, adapted.ThinkingBudget)
	}
	if len(adapted.Tools) != 1 {
		t.Fatalf("Tools = %+v, want one entry", adapted.Tools)
	}
	if adapted.Tools[0].Name != "summarize" || adapted.Tools[0].Description != "summarize a page" {
		t.Errorf("Tools[0] = %+v, want summarize/summarize a page", adapted.Tools[0])
	}
	if !strings.Contains(adapted.Tools[0].InputSchema, `"url"`) {
		t.Errorf("Tools[0].InputSchema = %q, want the url property JSON", adapted.Tools[0].InputSchema)
	}
	if adapted.ToolChoice != "summarize" {
		t.Errorf("ToolChoice = %q, want summarize", adapted.ToolChoice)
	}
}

// TestSDKCallCatalogLLMRespMatchesAggregator pins that the llmAggregator
// terminal decodes into LLMResp — the generated unary/stream callers return
// exactly what the host produces.
func TestSDKCallCatalogLLMRespMatchesAggregator(t *testing.T) {
	agg := &llmAggregator{}
	_ = agg.Push(domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "he"})
	_ = agg.Push(domain.AggregatorChunk{Kind: domain.AggregatorChunkReasoning, Text: "thinking"})
	_ = agg.Push(domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseComplete, ToolUseID: "tu_1", ToolName: "summarize", Input: `{"url":"https://example.com"}`})
	usage := domain.UsageData{InputTokens: 2, OutputTokens: 3}
	_ = agg.Push(domain.AggregatorChunk{Kind: domain.AggregatorChunkUsage, Usage: &usage})
	body, err := json.Marshal(agg.Terminal())
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	var resp LLMResp
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode terminal into LLMResp: %v (wire %s)", err, body)
	}
	if resp.Text != "he" || resp.Reasoning != "thinking" {
		t.Errorf("Text/Reasoning = %q/%q, want he/thinking", resp.Text, resp.Reasoning)
	}
	if !strings.Contains(string(resp.Usage), `"InputTokens":2`) && !strings.Contains(string(resp.Usage), `"InputTokens": 2`) {
		t.Errorf("Usage = %s, want input 2 tokens", resp.Usage)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want one entry (wire %s)", resp.ToolCalls, body)
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "tu_1" || tc.Name != "summarize" || !strings.Contains(string(tc.Arguments), "example.com") {
		t.Errorf("ToolCalls[0] = %+v, want tu_1/summarize with url argument", tc)
	}
}

// TestSDKCallCatalogLLMToolLoopRoundTrip pins the agentic-loop message shape:
// an assistant tool_use block and a user tool_result block survive the SDK →
// domain adaptation with their tool fields intact, so a plugin can echo a
// completed tool call back for the next turn.
func TestSDKCallCatalogLLMToolLoopRoundTrip(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{"role": "user", "content": "summarize example.com"},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "tool_use_id": "tu_1", "tool_name": "summarize", "input": map[string]any{"url": "https://example.com"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tu_1", "text": "Example Domain.", "is_error": false},
			}},
		},
	}
	adapted, err := adaptLLMPayload(payload)
	if err != nil {
		t.Fatalf("adaptLLMPayload: %v", err)
	}
	if len(adapted.Messages) != 3 {
		t.Fatalf("messages = %d, want 3", len(adapted.Messages))
	}
	use := adapted.Messages[1].Content[0]
	if use.Type != "tool_use" || use.ToolUseID != "tu_1" || use.ToolName != "summarize" {
		t.Errorf("tool_use block = %+v", use)
	}
	if !strings.Contains(use.Input, "example.com") {
		t.Errorf("tool_use input = %q, want the object stringified", use.Input)
	}
	res := adapted.Messages[2].Content[0]
	if res.Type != "tool_result" || res.ToolUseID != "tu_1" || res.Text != "Example Domain." || res.IsError {
		t.Errorf("tool_result block = %+v", res)
	}
}

// TestEncodeAggregatorChunkToolChunks pins the streaming wire shape of the
// tool vocabulary: tool_use_start/input_delta/complete and stop each carry a
// decodable data payload, not just the kind.
func TestEncodeAggregatorChunkToolChunks(t *testing.T) {
	cases := []struct {
		chunk domain.AggregatorChunk
		want  string
	}{
		{domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseStart, ToolUseID: "tu_1", ToolName: "summarize"}, `{"tool_use_id":"tu_1","name":"summarize"}`},
		{domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseInputDelta, ToolUseID: "tu_1", InputDelta: `{"url"`}, `{"tool_use_id":"tu_1","input_delta":"{\"url\""}`},
		{domain.AggregatorChunk{Kind: domain.AggregatorChunkToolUseComplete, ToolUseID: "tu_1", ToolName: "summarize", Input: `{"url":"https://example.com"}`}, `{"tool_use_id":"tu_1","name":"summarize","input":{"url":"https://example.com"}}`},
		{domain.AggregatorChunk{Kind: domain.AggregatorChunkStop, StopReason: "tool_use"}, `{"stop_reason":"tool_use"}`},
	}
	for _, tc := range cases {
		env, err := encodeAggregatorChunk(tc.chunk)
		if err != nil {
			t.Fatalf("encodeAggregatorChunk(%s): %v", tc.chunk.Kind, err)
		}
		if env.Kind != tc.chunk.Kind {
			t.Errorf("kind = %q, want %q", env.Kind, tc.chunk.Kind)
		}
		if string(env.Data) != tc.want {
			t.Errorf("%s data = %s, want %s", tc.chunk.Kind, env.Data, tc.want)
		}
	}
}

// TestSDKCallCatalogStateWireMatchesHost pins the state.* wire contract
// against the host-side gen types: base64 Value semantics on set, and the
// exact response keys on get/delete.
func TestSDKCallCatalogStateWireMatchesHost(t *testing.T) {
	setReq := StateSetReq{Key: "k", Value: []byte("hello")}
	body, err := json.Marshal(setReq)
	if err != nil {
		t.Fatalf("marshal StateSetReq: %v", err)
	}
	var hostSet gen.PluginStateSetReq
	if err := json.Unmarshal(body, &hostSet); err != nil {
		t.Fatalf("host decode StateSetReq: %v", err)
	}
	if hostSet.Key != "k" || string(hostSet.Value) != "hello" {
		t.Errorf("host set req = %+v, want key k value hello", hostSet)
	}
	if !strings.Contains(string(body), `"aGVsbG8="`) {
		t.Errorf("StateSetReq.Value must marshal as base64 text, got %s", body)
	}

	hostGet := gen.PluginStateGetResp{Value: []byte("v"), Found: true}
	getBody, err := json.Marshal(hostGet)
	if err != nil {
		t.Fatalf("marshal host get resp: %v", err)
	}
	var getResp StateGetResp
	if err := json.Unmarshal(getBody, &getResp); err != nil {
		t.Fatalf("decode get resp: %v", err)
	}
	if string(getResp.Value) != "v" || !getResp.Found {
		t.Errorf("StateGetResp = %+v, want value v found true", getResp)
	}
}

// TestSDKCallCatalogRegistryQueryWire pins the registry.query wire contract:
// the appbinding-side request/response types must marshal/unmarshal with the
// exact JSON keys the pluginhost-side registry_handler decodes/encodes.
func TestSDKCallCatalogRegistryQueryWire(t *testing.T) {
	// Request: lowercase SDK-facing keys reach the pluginhost handler.
	req := RegistryQueryReq{Service: "aimanager", Callable: "list", Limit: 50, Cursor: "25"}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal RegistryQueryReq: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}
	for _, k := range []string{"service", "callable", "limit", "cursor"} {
		if _, ok := m[k]; !ok {
			t.Errorf("request wire missing key %q; got %s", k, body)
		}
	}
	if _, ok := m["Service"]; ok {
		t.Errorf("request wire must use lowercase keys; got %s", body)
	}

	// Response: pluginhost handler writes Items/NextCursor; the appbinding
	// type must decode them.
	hostResp := map[string]any{
		"Items": []map[string]any{
			{
				"CallID":      "aimanager.list",
				"Service":     "aimanager",
				"Kind":        "unary",
				"Description": "list apps",
				"Params": []map[string]any{
					{"Name": "Id", "Type": "string", "Required": true},
				},
				"FinalType":    "AppManagerListResp",
				"ReqSchemaId":  float64(4096),
				"RespSchemaId": float64(4097),
			},
		},
		"NextCursor": "200",
	}
	hostBody, err := json.Marshal(hostResp)
	if err != nil {
		t.Fatalf("marshal host resp: %v", err)
	}
	var resp RegistryQueryResp
	if err := json.Unmarshal(hostBody, &resp); err != nil {
		t.Fatalf("decode host resp: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("resp.Items = %d, want 1", len(resp.Items))
	}
	item := resp.Items[0]
	if item.CallID != "aimanager.list" || item.Service != "aimanager" || item.Kind != "unary" {
		t.Errorf("item = %+v, want aimanager.list/aimanager/unary", item)
	}
	if item.ReqSchemaId != 4096 || item.RespSchemaId != 4097 {
		t.Errorf("schema IDs = %d/%d, want 4096/4097", item.ReqSchemaId, item.RespSchemaId)
	}
	if len(item.Params) != 1 || item.Params[0].Name != "Id" || !item.Params[0].Required {
		t.Errorf("Params = %+v, want one required Id param", item.Params)
	}
	if resp.NextCursor != "200" {
		t.Errorf("NextCursor = %q, want 200", resp.NextCursor)
	}
}
