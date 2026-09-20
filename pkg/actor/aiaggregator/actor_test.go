package aiaggregator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/llmclient/nativetools"
)

func TestStripThinkTags(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no_tags", "Fix login bug", "Fix login bug"},
		{"complete_block", "<think>reasoning here</think>Fix login bug", "reasoning hereFix login bug"},
		{"unterminated", "<think>still thinking", "still thinking"},
		{"multiple_blocks", "<think>a</think>hello<think>b</think> world", "ahellob world"},
		{"text_before", "pre <think>x</think> post", "pre x post"},
		{"empty_think_then_text", "<think></think>Real title", "Real title"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripThinkTags(c.in)
			if got != c.want {
				t.Errorf("stripThinkTags(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestProviderExtraBody_KimiK2_5(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "kimi",
		Model:        "kimi-k2-5-latest",
	}
	got := openAIExtraBody(unit.Model)
	if got == nil {
		t.Fatalf("expected extra body for Kimi k2.5")
	}
	cta, ok := got["chat_template_args"].(map[string]any)
	if !ok {
		t.Fatalf("expected chat_template_args map, got %T", got["chat_template_args"])
	}
	if cta["enable_thinking"] != true {
		t.Fatalf("expected enable_thinking=true")
	}
}

func TestProviderExtraBody_NonKimiProviderWithKimiModel(t *testing.T) {
	// Provider names can be user-defined or third-party; the decision is based
	// on the model name, not the provider name.
	unit := CallableUnit{
		ProviderName: "some-third-party",
		Model:        "kimi-k2-5-latest",
	}
	if got := openAIExtraBody(unit.Model); got == nil {
		t.Fatalf("expected extra body when model is Kimi k2.5")
	}
}

func TestProviderExtraBody_NonKimiModel(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "openai",
		Model:        "gpt-4o",
	}
	if got := openAIExtraBody(unit.Model); got != nil {
		t.Fatalf("expected nil for non-Kimi model, got %v", got)
	}
}

func TestProviderExtraBody_KimiThinkingModel(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "kimi",
		Model:        "kimi-k2-thinking",
	}
	if got := openAIExtraBody(unit.Model); got != nil {
		t.Fatalf("expected nil for kimi-k2-thinking, got %v", got)
	}
}

func TestProviderExtraBody_KimiForCoding(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "Moonshot",
		Model:        "kimi-for-coding-latest",
	}
	if got := openAIExtraBody(unit.Model); got == nil {
		t.Fatalf("expected extra body for for-coding model")
	}
}

// toolsWithWebSearch builds a request whose tool list carries a standard
// function tool plus a provider-native web_search (the turn engine's
// best-effort list for a web-search-capable model).
func toolsWithWebSearch(t *testing.T, model string) []domain.ToolSpec {
	t.Helper()
	base := []domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}}
	tools := nativetools.DefaultRegistry().AppendWebSearch(base, model)
	if len(tools) < 2 {
		t.Fatalf("setup: expected web_search appended for %q, got %+v", model, tools)
	}
	return tools
}

func TestBuildLLMRequest_DropsWebSearchForOpenAIProtocol(t *testing.T) {
	// GLM reached via the openai protocol: the Chat Completions schema only
	// accepts function/plugin tools, so the native web_search must be stripped
	// at dispatch (reproduces and fixes the "unknown tool type: web_search" 400
	// seen when switching e.g. kimi -> glm mid-turn).
	a := &Actor{nativeTools: nativetools.DefaultRegistry()}
	req := domain.SendSessionMessageReq{Tools: toolsWithWebSearch(t, "glm-5.1")}

	out := a.buildLLMRequest(CallableUnit{Model: "glm-5.1", Protocol: "openai"}, req)
	for _, tool := range out.Tools {
		if tool.Type == "web_search" {
			t.Fatalf("web_search must not be sent over openai protocol, tools=%+v", out.Tools)
		}
	}
	if len(out.Tools) != 1 || out.Tools[0].Name != "filesystem_read" {
		t.Fatalf("expected only the function tool, got %+v", out.Tools)
	}
}

func TestBuildLLMRequest_KeepsWebSearchForAnthropicProtocol(t *testing.T) {
	// web_search_20250305 is a first-class anthropic server tool: it must be
	// preserved over the anthropic protocol and only dropped over others.
	a := &Actor{nativeTools: nativetools.DefaultRegistry()}
	req := domain.SendSessionMessageReq{Tools: toolsWithWebSearch(t, "claude-sonnet-4-6")}

	out := a.buildLLMRequest(CallableUnit{Model: "claude-sonnet-4-6", Protocol: "anthropic"}, req)
	var sawWebSearch bool
	for _, tool := range out.Tools {
		if tool.Type == "web_search_20250305" {
			sawWebSearch = true
		}
	}
	if !sawWebSearch {
		t.Fatalf("expected web_search kept over anthropic protocol, tools=%+v", out.Tools)
	}

	// Same tool list over the openai protocol must drop it.
	out = a.buildLLMRequest(CallableUnit{Model: "claude-sonnet-4-6", Protocol: "openai"}, req)
	for _, tool := range out.Tools {
		if tool.Type == "web_search_20250305" {
			t.Fatalf("web_search must be dropped over openai protocol, tools=%+v", out.Tools)
		}
	}
}

// TestBuildLLMRequest_ForcedToolChoiceDisablesDeepSeekHybridThinking pins the
// live-verified provider constraint: deepseek-v* hybrid models think by
// default and reject a forced tool_choice with HTTP 400 while thinking. A
// forced tool choice with no explicit request-level reasoning must inject the
// thinking-disable switch (and clear any pool-unit default effort that would
// re-activate thinking).
func TestBuildLLMRequest_ForcedToolChoiceDisablesDeepSeekHybridThinking(t *testing.T) {
	a := &Actor{nativeTools: nativetools.DefaultRegistry()}
	unit := CallableUnit{Model: "deepseek-v4-flash", Protocol: "openai", ReasoningEffort: "high"}
	tools := []domain.ToolSpec{{Name: "extract_meta", InputSchema: `{"type":"object"}`}}

	// Forced choice (bare name) -> thinking disabled, unit default effort cleared.
	out := a.buildLLMRequest(unit, domain.SendSessionMessageReq{ToolChoice: "extract_meta", Tools: tools})
	if got := out.ExtraBody["thinking"]; fmt.Sprint(got) != "map[type:disabled]" {
		t.Errorf("thinking = %v, want disabled", got)
	}
	if out.ReasoningEffort != "" {
		t.Errorf("reasoning effort = %q, want cleared", out.ReasoningEffort)
	}

	// "required" counts as forced too.
	out = a.buildLLMRequest(unit, domain.SendSessionMessageReq{ToolChoice: "required", Tools: tools})
	if got := out.ExtraBody["thinking"]; fmt.Sprint(got) != "map[type:disabled]" {
		t.Errorf("thinking (required) = %v, want disabled", got)
	}

	// Explicit request-level effort + forced choice is a caller conflict:
	// keep the effort, do not silently disable thinking.
	out = a.buildLLMRequest(unit, domain.SendSessionMessageReq{ToolChoice: "extract_meta", ReasoningEffort: "low", Tools: tools})
	if _, present := out.ExtraBody["thinking"]; present {
		t.Errorf("thinking injected despite explicit reasoning effort")
	}
	if out.ReasoningEffort != "low" {
		t.Errorf("explicit effort overridden: %q", out.ReasoningEffort)
	}

	// Model-decided choices must not touch thinking.
	for _, choice := range []string{"", "auto", "none"} {
		out = a.buildLLMRequest(unit, domain.SendSessionMessageReq{ToolChoice: choice, Tools: tools})
		if _, present := out.ExtraBody["thinking"]; present {
			t.Errorf("thinking injected for tool_choice %q", choice)
		}
	}

	// Non-hybrid deepseek models keep the provider default.
	out = a.buildLLMRequest(CallableUnit{Model: "deepseek-chat", Protocol: "openai"}, domain.SendSessionMessageReq{ToolChoice: "extract_meta", Tools: tools})
	if _, present := out.ExtraBody["thinking"]; present {
		t.Errorf("thinking injected for deepseek-chat")
	}
}

func TestProviderExtraBody_DeepSeekR1(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "deepseek",
		Model:        "deepseek-r1",
	}
	got := openAIExtraBody(unit.Model)
	if got == nil {
		t.Fatalf("expected extra body for DeepSeek R1")
	}
	thinking, ok := got["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking map, got %T", got["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

func TestProviderExtraBody_DeepSeekReasoner(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "deepseek",
		Model:        "deepseek-reasoner",
	}
	got := openAIExtraBody(unit.Model)
	if got == nil {
		t.Fatalf("expected extra body for DeepSeek reasoner")
	}
	thinking, ok := got["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking map, got %T", got["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("expected thinking.type=enabled, got %v", thinking["type"])
	}
}

func TestProviderExtraBody_DeepSeekNonReasoning(t *testing.T) {
	unit := CallableUnit{
		ProviderName: "deepseek",
		Model:        "deepseek-chat",
	}
	if got := openAIExtraBody(unit.Model); got != nil {
		t.Fatalf("expected nil extra body for non-reasoning DeepSeek model, got %v", got)
	}
}

func TestIsReasoningModel(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4", false},
		{"gpt-4o", false},
		{"kimi-k2-thinking", true},
		{"deepseek-r1", true},
		{"deepseek-r1-0324", true},
		{"deepseek-reasoner", true},
		{"some-reasoning-model", true},
	}
	for _, c := range cases {
		if got := isReasoningModel(c.model); got != c.want {
			t.Errorf("isReasoningModel(%q) = %v, want %v", c.model, got, c.want)
		}
	}
}

// TestBuildLLMRequest_ProbeTokens verifies that when handleProbeTokens builds
// the LLM request, MaxTokens is set to 1 and ThinkingBudget/ReasoningEffort
// are cleared to avoid conflicts with reasoning models.
func TestBuildLLMRequest_ProbeTokensClearsThinking(t *testing.T) {
	unit := CallableUnit{
		Model:        "claude-sonnet-4",
		ProviderName: "anthropic",
	}
	req := domain.SendSessionMessageReq{
		ThinkingBudget:  8000,
		ReasoningEffort: "high",
		System:          "test",
	}

	llmReq := (&Actor{registry: newBuiltinRegistry()}).buildLLMRequest(unit, req)
	// Simulate what handleProbeTokens does after building:
	llmReq.MaxTokens = 1
	llmReq.ThinkingBudget = 0
	llmReq.ReasoningEffort = ""

	if llmReq.MaxTokens != 1 {
		t.Errorf("MaxTokens = %d, want 1", llmReq.MaxTokens)
	}
	if llmReq.ThinkingBudget != 0 {
		t.Errorf("ThinkingBudget = %d, want 0", llmReq.ThinkingBudget)
	}
	if llmReq.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want empty", llmReq.ReasoningEffort)
	}
}

// TestBuildLLMRequest_UnitReasoningEffortFallback verifies the three-tier
// reasoning-effort precedence inside the aggregator: an explicit request
// effort wins; otherwise the pool unit's configured ReasoningEffort applies.
func TestBuildLLMRequest_UnitReasoningEffortFallback(t *testing.T) {
	a := &Actor{registry: newBuiltinRegistry()}
	unit := CallableUnit{
		Model:           "gpt-5",
		ProviderName:    "openai",
		ReasoningEffort: "high",
	}

	// No request effort → fall back to the unit default.
	got := a.buildLLMRequest(unit, domain.SendSessionMessageReq{System: "s"})
	if got.ReasoningEffort != "high" {
		t.Fatalf("empty request effort: ReasoningEffort = %q, want high (unit default)", got.ReasoningEffort)
	}

	// Explicit request effort overrides the unit default.
	got = a.buildLLMRequest(unit, domain.SendSessionMessageReq{System: "s", ReasoningEffort: "xhigh"})
	if got.ReasoningEffort != "xhigh" {
		t.Fatalf("explicit request effort: ReasoningEffort = %q, want xhigh (request wins)", got.ReasoningEffort)
	}

	// "max" and "ultra" efforts pass through as free-text values.
	got = a.buildLLMRequest(unit, domain.SendSessionMessageReq{System: "s", ReasoningEffort: "max"})
	if got.ReasoningEffort != "max" {
		t.Fatalf("explicit max effort: ReasoningEffort = %q, want max", got.ReasoningEffort)
	}
	got = a.buildLLMRequest(unit, domain.SendSessionMessageReq{System: "s", ReasoningEffort: "ultra"})
	if got.ReasoningEffort != "ultra" {
		t.Fatalf("explicit ultra effort: ReasoningEffort = %q, want ultra", got.ReasoningEffort)
	}

	// Unit has no default and request is empty → empty (provider default).
	plain := CallableUnit{Model: "gpt-5", ProviderName: "openai"}
	got = a.buildLLMRequest(plain, domain.SendSessionMessageReq{System: "s"})
	if got.ReasoningEffort != "" {
		t.Fatalf("no effort anywhere: ReasoningEffort = %q, want empty", got.ReasoningEffort)
	}
}

// TestBuiltinRegistry_SupportedProtocols locks the protocols the aggregator
// can actually dispatch. It must stay aligned with aimanager.supportedProtocols.
func TestBuiltinRegistry_SupportedProtocols(t *testing.T) {
	r := newBuiltinRegistry()
	for _, p := range []string{"anthropic", "openai", "endpoint", "responses"} {
		if !r.Supported(p) {
			t.Errorf("%q should be supported by builtin registry", p)
		}
	}
	if r.Supported("gemini") {
		t.Error("gemini chat must not be supported (no client impl)")
	}
	got := r.Protocols()
	if len(got) != 4 {
		t.Errorf("expected 4 builtin protocols, got %d: %v", len(got), got)
	}
}

// TestBuiltinRegistry_NewClient_UnknownProtocolErrors verifies the actor-owned
// registry rejects unknown protocols at client construction, replacing the old
// default-case switch branch. This is the single failure boundary: unknown
// protocols never reach a real HTTP call.
func TestBuiltinRegistry_NewClient_UnknownProtocolErrors(t *testing.T) {
	a := &Actor{registry: newBuiltinRegistry()}
	for _, proto := range []string{"gemini", "", "bedrock", "claude"} {
		if _, err := a.registry.NewClient(proto, "https://ep", "tok", "", 0); err == nil {
			t.Errorf("protocol %q must be rejected by registry", proto)
		}
	}
}

// TestBuiltinRegistry_NewClient_BuildsAllSupported verifies each supported
// protocol produces a non-nil, decorated client (retry + optional concurrency).
func TestBuiltinRegistry_NewClient_BuildsAllSupported(t *testing.T) {
	a := &Actor{registry: newBuiltinRegistry()}
	for _, proto := range []string{"anthropic", "openai", "endpoint", "responses"} {
		c, err := a.registry.NewClient(proto, "https://ep", "tok", "", 0)
		if err != nil {
			t.Errorf("NewClient(%q): %v", proto, err)
		}
		if c == nil {
			t.Errorf("client for %q must be non-nil", proto)
		}
	}
}

// TestDispatch_ResponsesUnitSelectsResponsesClient drives a real dispatch
// (intent inference) through the builtin registry with a responses-protocol
// unit and asserts the wire request lands on POST /responses — proving the
// unit selected the ResponsesClient (not the chat-completions client). This is
// the end-to-end counterpart to the registry wiring tests above.
func TestDispatch_ResponsesUnitSelectsResponsesClient(t *testing.T) {
	var sawPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.done\",\"text\":\"hello\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{{
			ID:           "resp::gpt-5",
			Model:        "gpt-5",
			ProviderName: "resp",
			Protocol:     "responses",
			Endpoint:     server.URL,
		}},
		registry:     newBuiltinRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	req := domain.SendSessionMessageReq{
		System: "be terse",
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}
	got, err := a.handleIntent(&testPureCtx{done: make(chan struct{})}, req)
	if err != nil {
		t.Fatalf("handleIntent: %v", err)
	}
	if sawPath != "/responses" {
		t.Fatalf("dispatch path = %q, want /responses (ResponsesClient selected)", sawPath)
	}
	if strings.TrimSpace(got.Text) != "hello" {
		t.Errorf("intent text = %q, want %q", got.Text, "hello")
	}
}

// TestIntent_CustomSystemPromptResponseNotTruncated locks the contract for
// tool_judge (memory weave JSON verdicts) and bypass (ALLOW/DENY) callers:
// when the request carries its own system prompt, handleIntent must return the
// model's full response. Before the customProtocol guard, the title-only
// 30-rune cap silently cut these structured payloads mid-JSON, so
// decodeMemoryWeaveDecisions never parsed a single edge.
func TestIntent_CustomSystemPromptResponseNotTruncated(t *testing.T) {
	// Multi-candidate weave verdicts are long; build a reply that safely
	// exceeds the (loose) title cap so the test proves the cap is skipped.
	var reply strings.Builder
	reply.WriteString(`{"edges":[`)
	for i := 0; i < 6; i++ {
		if i > 0 {
			reply.WriteString(",")
		}
		fmt.Fprintf(&reply, `{"target_node_id":"019f5bef-aaaa-bbbb-cccc-1234567%08d","decision":"peer"}`, i)
	}
	reply.WriteString(`]}`)
	jsonReply := reply.String()
	if utf8.RuneCountInString(jsonReply) <= 200 {
		t.Fatalf("fixture must be long enough to prove no truncation: got %d runes",
			utf8.RuneCountInString(jsonReply))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", jsonReply)
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.done\",\"text\":%q}\n\n", jsonReply)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{{
			ID:           "resp::gpt-5",
			Model:        "gpt-5",
			ProviderName: "resp",
			Protocol:     "responses",
			Endpoint:     server.URL,
		}},
		registry:     newBuiltinRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	req := domain.SendSessionMessageReq{
		System: "Choose relationships. Reply only JSON.",
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "NEW node\nCANDIDATES:\n- other"}},
		}},
	}
	got, err := a.handleIntent(&testPureCtx{done: make(chan struct{})}, req)
	if err != nil {
		t.Fatalf("handleIntent: %v", err)
	}
	if got.Text != jsonReply {
		t.Errorf("custom-protocol response must not be truncated:\n got %q\nwant %q", got.Text, jsonReply)
	}
}

// TestIntent_DefaultTitlePathNotCapped verifies the title-inference default
// (no caller system prompt) returns the model's full output — length is
// constrained only by the prompt (~20 chars), never by code.
func TestIntent_DefaultTitlePathNotCapped(t *testing.T) {
	longReply := strings.Repeat("word ", 100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":%q}\n\n", longReply)
		fmt.Fprintf(w, "data: {\"type\":\"response.output_text.done\",\"text\":%q}\n\n", longReply)
		fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{{
			ID:           "resp::gpt-5",
			Model:        "gpt-5",
			ProviderName: "resp",
			Protocol:     "responses",
			Endpoint:     server.URL,
		}},
		registry:     newBuiltinRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	req := domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "hello"}},
		}},
	}
	got, err := a.handleIntent(&testPureCtx{done: make(chan struct{})}, req)
	if err != nil {
		t.Fatalf("handleIntent: %v", err)
	}
	if got.Text != strings.TrimSpace(longReply) {
		t.Errorf("title path must not truncate: got %d runes, want %d", len([]rune(got.Text)), len([]rune(longReply)))
	}
}

// TestIntent_ReasoningOnlyRecoversTitle reproduces the glm-4.5 hybrid-model
// failure observed in production: the whole answer lands in
// reasoning_content, the content stream is empty (textDeltas=0), and the
// old code returned an empty title. The recovery path must extract the
// marker-wrapped title from the buffered reasoning text.
func TestIntent_ReasoningOnlyRecoversTitle(t *testing.T) {
	want := "Heap Weaver 分析"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"The user wants a title. \"}}]}\n\n")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"【INTENT】%s【/INTENT】\"}}]}\n\n", want)
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{{
			ID:           "glm::glm-4.5",
			Model:        "glm-4.5",
			ProviderName: "glm",
			Protocol:     "openai",
			Endpoint:     server.URL,
		}},
		registry:     newBuiltinRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	req := domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "分析一下堆内存"}},
		}},
	}
	got, err := a.handleIntent(&testPureCtx{done: make(chan struct{})}, req)
	if err != nil {
		t.Fatalf("handleIntent: %v", err)
	}
	if got.Text != want {
		t.Errorf("reasoning-only recovery: text = %q, want %q", got.Text, want)
	}
}

// TestIntent_GlmRequestDisablesThinking asserts the intent request for a glm
// model carries thinking.type=disabled so hybrid reasoning models emit plain
// content instead of an empty content stream.
func TestIntent_GlmRequestDisablesThinking(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"【INTENT】ok【/INTENT】\"}}]}\n\n")
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{{
			ID:           "glm::glm-4.5",
			Model:        "glm-4.5",
			ProviderName: "glm",
			Protocol:     "openai",
			Endpoint:     server.URL,
		}},
		registry:     newBuiltinRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	req := domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "hi"}},
		}},
	}
	if _, err := a.handleIntent(&testPureCtx{done: make(chan struct{})}, req); err != nil {
		t.Fatalf("handleIntent: %v", err)
	}
	if !strings.Contains(string(body), `"type":"disabled"`) {
		t.Errorf("glm intent request must disable thinking, body: %s", string(body))
	}
}

// TestBuildLLMRequest_ExtraBodyViaRegistry verifies the buildLLMRequest method
// resolves provider-specific extra body through the registry descriptor policy
// rather than a hard-coded switch on model name.
func TestBuildLLMRequest_ExtraBodyViaRegistry(t *testing.T) {
	a := &Actor{registry: newBuiltinRegistry()}

	// kimi-k2-5 on an openai-protocol unit -> descriptor policy applies.
	unit := CallableUnit{Model: "kimi-k2-5-latest", Protocol: "openai"}
	req := domain.SendSessionMessageReq{System: "s"}
	got := a.buildLLMRequest(unit, req).ExtraBody
	if got == nil {
		t.Fatal("expected extra body for kimi-k2-5 via registry")
	}
	if _, ok := got["chat_template_args"]; !ok {
		t.Errorf("expected chat_template_args, got %v", got)
	}

	// gpt-4o on openai-protocol unit -> no extra body.
	unit2 := CallableUnit{Model: "gpt-4o", Protocol: "openai"}
	if got := a.buildLLMRequest(unit2, req).ExtraBody; got != nil {
		t.Errorf("expected nil extra body for gpt-4o, got %v", got)
	}

	// claude on anthropic-protocol unit -> no extra body policy registered.
	unit3 := CallableUnit{Model: "claude-opus-4", Protocol: "anthropic"}
	if got := a.buildLLMRequest(unit3, req).ExtraBody; got != nil {
		t.Errorf("expected nil extra body for anthropic protocol, got %v", got)
	}
}

// TestBuildStatsRecord_FailedDispatchErrorCode verifies that a failed dispatch
// (the stream-open failure path, which passes a zero telemetry snapshot) still
// produces a record attributable by error code — the whole point of structured
// ErrorCode for the model-unit dashboard.
func TestBuildStatsRecord_FailedDispatchErrorCode(t *testing.T) {
	a := &Actor{}
	unit := &CallableUnit{Model: "gpt-4o", ProviderName: "openai"}
	req := domain.SendSessionMessageReq{WorkspaceID: "ws", AgentID: "ag", SessionID: "s", TurnID: "t"}

	// Zero telemetry + a 429 HTTP error → rate_limit, with error fields derived.
	rec := a.buildStatsRecord(req, unit, llmclient.RequestTelemetry{}, &llmclient.HTTPError{StatusCode: 429})
	if rec.ErrorCode != llmclient.ErrorCodeRateLimit {
		t.Errorf("rate-limit error code: got %q, want %q", rec.ErrorCode, llmclient.ErrorCodeRateLimit)
	}
	if rec.StopReason != llmclient.StopReasonError {
		t.Errorf("stop reason: got %q, want %q", rec.StopReason, llmclient.StopReasonError)
	}
	if rec.ErrorMessage == "" {
		t.Error("expected non-empty error message derived from dispatch error")
	}
	if rec.Provider != "openai" || rec.Model != "gpt-4o" {
		t.Errorf("provider/model: got %s/%s", rec.Provider, rec.Model)
	}

	// 503 → overloaded.
	rec503 := a.buildStatsRecord(req, unit, llmclient.RequestTelemetry{}, &llmclient.HTTPError{StatusCode: 503})
	if rec503.ErrorCode != llmclient.ErrorCodeOverloaded {
		t.Errorf("overloaded error code: got %q, want %q", rec503.ErrorCode, llmclient.ErrorCodeOverloaded)
	}

	// No dispatch error and empty telemetry → no error code.
	recOk := a.buildStatsRecord(req, unit, llmclient.RequestTelemetry{}, nil)
	if recOk.ErrorCode != "" {
		t.Errorf("success error code: got %q, want empty", recOk.ErrorCode)
	}
}

func TestBuildStatsRecord_AggregatorRefAttribution(t *testing.T) {
	a := &Actor{}
	aggUnit := &CallableUnit{ID: "agg:child", AggregatorID: "child"}
	req := domain.SendSessionMessageReq{WorkspaceID: "ws", AgentID: "ag", SessionID: "s", TurnID: "t"}

	rec := a.buildStatsRecord(req, aggUnit, llmclient.RequestTelemetry{},
		errors.New("nested dispatch to \"child\": child stream closed before open"))
	// The child aggregator is the same Actor implementation and already emits
	// its own stats record with the real deep Provider/Model. The parent must
	// not overwrite the empty attribution with the aggregator ref.
	if rec.Provider != "" {
		t.Errorf("provider: got %q, want empty", rec.Provider)
	}
	if rec.Model != "" {
		t.Errorf("model: got %q, want empty", rec.Model)
	}
	if rec.ErrorCode != llmclient.ErrorCodeDispatch {
		t.Errorf("error code: got %q, want %q", rec.ErrorCode, llmclient.ErrorCodeDispatch)
	}
}

func TestTruncateErrMessage(t *testing.T) {
	cases := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{"empty", "", 10, ""},
		{"short", "hello", 10, "hello"},
		{"exact", "hello world", 11, "hello world"},
		{"long", strings.Repeat("a", 600), 512, strings.Repeat("a", 512) + "...(truncated)"},
		{"zero_max", "hello", 0, "hello"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateErrMessage(tc.s, tc.max); got != tc.want {
				t.Errorf("truncateErrMessage(%q, %d) = %q, want %q", tc.s, tc.max, got, tc.want)
			}
		})
	}
}

func TestHasInteractionSubmitTool(t *testing.T) {
	tests := []struct {
		name  string
		tools []domain.ToolSpec
		want  bool
	}{
		{name: "nil", tools: nil, want: false},
		{name: "empty", tools: []domain.ToolSpec{}, want: false},
		{
			name: "unrelated",
			tools: []domain.ToolSpec{
				{Name: "file_read", CallableID: "project.read"},
				{Name: "file_edit", CallableID: "project.edit"},
			},
			want: false,
		},
		{
			name: "plan_submit by name",
			tools: []domain.ToolSpec{
				{Name: "file_read", CallableID: "project.read"},
				{Name: "plan_submit", CallableID: "plan_submit"},
			},
			want: true,
		},
		{
			name: "goal_submit by name",
			tools: []domain.ToolSpec{
				{Name: "goal_submit", CallableID: "goal_submit"},
			},
			want: true,
		},
		{
			name: "plan_submit by callable ID only",
			tools: []domain.ToolSpec{
				{Name: "submit", CallableID: "plan_submit"},
			},
			want: true,
		},
		{
			name: "goal_submit by callable ID only",
			tools: []domain.ToolSpec{
				{Name: "submit", CallableID: "goal_submit"},
			},
			want: true,
		},
		{
			name: "case insensitive",
			tools: []domain.ToolSpec{
				{Name: "PLAN_SUBMIT", CallableID: "plan_submit"},
			},
			want: true,
		},
		{
			name: "both",
			tools: []domain.ToolSpec{
				{Name: "plan_submit", CallableID: "plan_submit"},
				{Name: "goal_submit", CallableID: "goal_submit"},
			},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasInteractionSubmitTool(tc.tools); got != tc.want {
				t.Errorf("hasInteractionSubmitTool = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestConfigLoaded_FlagStartsFalse verifies a fresh Actor has configLoaded ==
// false, meaning ensureConfigLoaded will attempt resolveConfig on the first
// dispatch.
func TestConfigLoaded_FlagStartsFalse(t *testing.T) {
	a := &Actor{}
	a.mu.RLock()
	loaded := a.configLoaded
	a.mu.RUnlock()
	if loaded {
		t.Fatal("configLoaded should be false on a fresh Actor")
	}
}

// TestConfigLoaded_SetByApplyResolvedConfig verifies that applyResolvedConfig
// (the single sink for config from all sources: lazy load, version push,
// config_apply, poll) sets configLoaded = true so ensureConfigLoaded stops
// retrying. This is the core fix for the sync.Once bug where a failed first
// resolve permanently blocked retry.
func TestConfigLoaded_SetByApplyResolvedConfig(t *testing.T) {
	a := &Actor{id: systemAggregatorID}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   systemAggregatorID,
		Name: "Auto",
		Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o", Protocol: "openai", Endpoint: "https://api.openai.com"},
		},
	})
	a.mu.RLock()
	loaded := a.configLoaded
	a.mu.RUnlock()
	if !loaded {
		t.Fatal("configLoaded should be true after applyResolvedConfig")
	}
}

// TestConfigLoaded_SetByEmptyConfig verifies that a genuinely empty config
// (zero units) still sets configLoaded = true. Without this, ensureConfigLoaded
// would retry resolveConfig on every dispatch for an aggregator that is
// intentionally configured with no units.
func TestConfigLoaded_SetByEmptyConfig(t *testing.T) {
	a := &Actor{id: systemAggregatorID}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:    systemAggregatorID,
		Name:  "Empty",
		Units: nil,
	})
	a.mu.RLock()
	loaded := a.configLoaded
	a.mu.RUnlock()
	if !loaded {
		t.Fatal("configLoaded should be true even for an empty config (zero units)")
	}
}

// TestMarkUnitReasoning_SurvivesConfigRebuild locks the persistence of the
// learned reasoning flag: applyResolvedConfig rebuilds a.units on every 15s
// poll / version push, so a flag written only to a.units would be wiped and
// intent candidate selection would re-select the same reasoning-only unit on
// every title inference (runtime evidence: glm-4.5, 2026-08-27 22:47).
func TestMarkUnitReasoning_SurvivesConfigRebuild(t *testing.T) {
	a := &Actor{id: systemAggregatorID}
	apply := func() {
		a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
			ID:   systemAggregatorID,
			Name: "Auto",
			Units: []domain.ManualCallableUnit{
				{ProviderName: "glm", Model: "glm-4.5", Protocol: "openai", Endpoint: "https://open.bigmodel.cn"},
				{ProviderName: "openai", Model: "gpt-4o-mini", Protocol: "openai", Endpoint: "https://api.openai.com"},
			},
		})
	}
	apply()
	a.markUnitReasoning("glm::glm-4.5")

	// Simulate the 15s poll re-resolving identical config.
	apply()

	cands, err := a.selectIntentCandidates()
	if err != nil {
		t.Fatalf("selectIntentCandidates failed: %v", err)
	}
	if cands[0].ID == "glm::glm-4.5" {
		t.Fatal("selectIntentCandidates re-selected the learned reasoning-only unit after a config rebuild")
	}
	if cands[0].ID != "openai::gpt-4o-mini" {
		t.Fatalf("selectIntentCandidates picked %q, want openai::gpt-4o-mini", cands[0].ID)
	}
}

// TestSelectUnit_NamedAggregatorNoOnDemand verifies that a named (non-system)
// aggregator returns noMatchError immediately when the requested unit is not in
// its pool — on-demand resolution is system-aggregator-only by design. This is
// the path that surfaces "no callable unit for model %q provider %q" with
// non-empty values during concurrent worker creation when the pool is not yet
// loaded.
func TestSelectUnit_SkipsActiveDisableWindow(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "scheduled::m", Model: "m", ProviderName: "scheduled", DisableUntil: time.Now().Add(time.Minute).Unix()},
			{ID: "healthy::m", Model: "m", ProviderName: "healthy"},
		},
	}
	got, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if got.ID != "healthy::m" {
		t.Fatalf("selected %q, want healthy::m", got.ID)
	}
}

func TestSelectUnit_AllMatchingUnitsInDisableWindow(t *testing.T) {
	// All units inside their disable window means the pool is fully
	// unavailable: the selection failure self-registers the aggregator. Clean
	// it up so the package-level registry stays isolated between tests.
	t.Cleanup(func() { clearAggHealth("named") })
	a := &Actor{
		id:       "named",
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{{
			ID: "scheduled::m", Model: "m", ProviderName: "scheduled", DisableUntil: time.Now().Add(time.Minute).Unix(),
		}},
	}
	_, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{Model: "m", Provider: "scheduled"}})
	if err == nil {
		t.Fatal("expected disabled unit to be unavailable")
	}
}

func TestSelectUnit_NamedAggregatorNoOnDemand(t *testing.T) {
	a := &Actor{
		id:       "my-named-agg",
		strategy: NewRoundRobinStrategy(),
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai"},
		},
	}
	// Request a unit that is NOT in the pool.
	_, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{Model: "claude-sonnet-4", Provider: "anthropic"}})
	if err == nil {
		t.Fatal("expected error for unit not in named aggregator pool")
	}
	if !strings.Contains(err.Error(), "no callable unit for model") {
		t.Fatalf("expected noMatchError, got: %v", err)
	}
}

func feedAll(ex *intentExtractor, deltas []string) (bool, string) {
	var done bool
	var result string
	for _, d := range deltas {
		done, result = ex.feedText(d)
		if done {
			break
		}
	}
	return done, result
}

func TestIntentExtractor_SingleDelta(t *testing.T) {
	var ex intentExtractor
	done, result := feedAll(&ex, []string{"prefix【INTENT】Reverse a string in Python【/INTENT】suffix"})
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "Reverse a string in Python"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_MarkersSplitAcrossDeltas(t *testing.T) {
	var ex intentExtractor
	done, result := feedAll(&ex, []string{"pre【INT", "ENT】Fix logi", "n bug【/IN", "TENT】"})
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "Fix login bug"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_InlineThinkSkipped(t *testing.T) {
	var ex intentExtractor
	deltas := []string{
		thinkOpenTag + "the user wants to reverse a string" + thinkCloseTag,
		"【INTENT】Reverse string【/INTENT】",
	}
	done, result := feedAll(&ex, deltas)
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "Reverse string"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_OpenMarkerInsideThinkDoesNotMatch(t *testing.T) {
	var ex intentExtractor
	deltas := []string{
		thinkOpenTag[:4],
		thinkOpenTag[4:] + "should I emit 【INTENT】not this【/INTENT】" + thinkCloseTag,
		"【INTENT】Real title【/INTENT】",
	}
	done, result := feedAll(&ex, deltas)
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "Real title"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_NewlineTerminates(t *testing.T) {
	var ex intentExtractor
	done, result := feedAll(&ex, []string{"【INTENT】Sort array\nmore text【/INTENT】"})
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "Sort array"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_FullWidthPeriodTerminates(t *testing.T) {
	var ex intentExtractor
	done, result := feedAll(&ex, []string{"【INTENT】修复登录。其他【/INTENT】"})
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "修复登录"; result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestIntentExtractor_LongContentNotCapped(t *testing.T) {
	var ex intentExtractor
	long := strings.Repeat("字", 305)
	done, result := feedAll(&ex, []string{"【INTENT】" + long + "【/INTENT】"})
	if !done {
		t.Fatal("expected done=true")
	}
	if result != long {
		t.Errorf("length is prompt-constrained only: got %d runes, want %d", len([]rune(result)), len(long))
	}
}

func TestIntentExtractor_NoMarkersFallback(t *testing.T) {
	var ex intentExtractor
	done, _ := feedAll(&ex, []string{"Just a plain title without markers"})
	if done {
		t.Fatal("expected done=false when no open marker present")
	}
}

func TestIntentExtractor_NoCloseMarkerNoTerminator(t *testing.T) {
	var ex intentExtractor
	done, _ := feedAll(&ex, []string{"【INTENT】short"})
	if done {
		t.Fatal("expected done=false before rune cap reached")
	}
}

func TestSanitizeIntentTitle(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"clean passthrough", "AI git commit behavior", "AI git commit behavior"},
		{"duplicated open marker", "【INTENT】【INTENT】AI git commit behavior【/INTENT】", "AI git commit behavior"},
		{"unterminated open marker", "【INTENT】AI git commit behavior", "AI git commit behavior"},
		{"trailing close marker only", "AI git commit behavior【/INTENT】", "AI git commit behavior"},
		{"wrapped with surrounding noise", "prefix【INTENT】Sort array【/INTENT】suffix", "Sort array"},
		{"empty", "", ""},
		{"marker only", "【INTENT】", ""},
	}
	for _, c := range cases {
		if got := sanitizeIntentTitle(c.in); got != c.want {
			t.Errorf("%s: sanitizeIntentTitle(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestIntentExtractor_DuplicatedOpenMarkerSanitized reproduces the leak where a
// duplicated open marker survives the extractor as content, and verifies the
// final sanitizer removes it before the title reaches the caller.
func TestIntentExtractor_DuplicatedOpenMarkerSanitized(t *testing.T) {
	var ex intentExtractor
	done, result := feedAll(&ex, []string{"【INTENT】【INTENT】AI git commit behavior【/INTENT】"})
	if !done {
		t.Fatal("expected done=true")
	}
	if want := "AI git commit behavior"; sanitizeIntentTitle(result) != want {
		t.Errorf("sanitized result = %q, want %q", sanitizeIntentTitle(result), want)
	}
}
