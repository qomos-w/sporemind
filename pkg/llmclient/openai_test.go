package llmclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenAIBuildsStreamingRequest(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "test-key")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-4",
		System:   "be terse",
		UserText: "hello",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if captured.Model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", captured.Model)
	}
	if !captured.Stream {
		t.Error("Stream should be true")
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" || string(captured.Messages[0].Content) != `"be terse"` {
		t.Errorf("system msg = %#v", captured.Messages[0])
	}
	if captured.Messages[1].Role != "user" || string(captured.Messages[1].Content) != `"hello"` {
		t.Errorf("user msg = %#v", captured.Messages[1])
	}
}

func TestOpenAIStreamsTextDeltasAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var (
		text  string
		usage *Usage
		done  bool
	)
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventUsage:
			usage = ev.Usage
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
	if usage == nil || usage.TotalTokens != 7 {
		t.Errorf("usage = %#v", usage)
	}
	if !done {
		t.Error("expected done event")
	}
}

func TestOpenAIReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	_, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestOpenAIBuildsPayloadWithReasoningEffort(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:           "o3-mini",
		UserText:        "hello",
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if captured.ReasoningEffort != "high" {
		t.Errorf("reasoningEffort = %q, want high", captured.ReasoningEffort)
	}
}

func TestOpenAIBuildsPayloadWithSystemAndMessages(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-4",
		System:   "be helpful",
		Messages: []Message{{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(captured.Messages))
	}
	if captured.Messages[0].Role != "system" {
		t.Errorf("first msg role = %q, want system", captured.Messages[0].Role)
	}
	if captured.Messages[1].Role != "user" {
		t.Errorf("second msg role = %q, want user", captured.Messages[1].Role)
	}
}

func TestOpenAISanitizesImagesForDeepSeek(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model: "deepseek-chat",
		Messages: []Message{{
			Role: RoleUser,
			Content: []Block{
				{Type: BlockTypeText, Text: "what is this?"},
				{Type: BlockTypeImage, ImageURL: "data:image/png;base64,xxx"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(captured.Messages))
	}
	var raw string
	if err := json.Unmarshal(captured.Messages[0].Content, &raw); err != nil {
		t.Fatalf("user content should be a plain string for non-vision models: %v (body=%s)", err, string(captured.Messages[0].Content))
	}
	if !strings.Contains(raw, "[image]") {
		t.Errorf("expected image placeholder in content, got %q", raw)
	}
	if strings.Contains(raw, "image_url") {
		t.Errorf("image_url leaked into non-vision payload: %q", raw)
	}
}

func TestOpenAIPreservesImagesForVisionModels(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model: "gpt-4o",
		Messages: []Message{{
			Role: RoleUser,
			Content: []Block{
				{Type: BlockTypeText, Text: "what is this?"},
				{Type: BlockTypeImage, ImageURL: "data:image/png;base64,xxx"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(captured.Messages))
	}
	bodyStr := string(captured.Messages[0].Content)
	if !strings.Contains(bodyStr, "image_url") {
		t.Errorf("vision model should receive image_url block, got %s", bodyStr)
	}
}

func TestModelSupportsImageInput_ModelLevel(t *testing.T) {
	// Only the deepseek v4.1 line and the *vision* variant accept images; the
	// rest of the family plus unknown-name models follow the model-level rule.
	vision := []string{
		"deepseek-v4.1-flash",
		"deepseek/deepseek-v4.1-flash",
		"deepseek-v4-flash-vision-exp",
		"gpt-4o",
		"gemini-3.8-flash",
		"kimi-k3",
	}
	for _, m := range vision {
		if !ModelSupportsImageInput(m) {
			t.Errorf("ModelSupportsImageInput(%q) = false, want true", m)
		}
	}
	textOnly := []string{
		"deepseek-chat",
		"deepseek-reasoner",
		"deepseek-r1",
		"deepseek-v3.2",
		"deepseek-v4-flash",
		"deepseek-v4-pro",
	}
	for _, m := range textOnly {
		if ModelSupportsImageInput(m) {
			t.Errorf("ModelSupportsImageInput(%q) = true, want false", m)
		}
	}
}

func TestOpenAIPreservesImagesForDeepSeekVisionModel(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model: "deepseek-v4.1-flash",
		Messages: []Message{{
			Role: RoleUser,
			Content: []Block{
				{Type: BlockTypeText, Text: "what is this?"},
				{Type: BlockTypeImage, ImageURL: "data:image/png;base64,xxx"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(captured.Messages))
	}
	bodyStr := string(captured.Messages[0].Content)
	if !strings.Contains(bodyStr, "image_url") {
		t.Errorf("deepseek-v4.1-flash should receive image_url block, got %s", bodyStr)
	}
	if strings.Contains(bodyStr, "[image]") {
		t.Errorf("deepseek-v4.1-flash image was stripped to a placeholder: %s", bodyStr)
	}
}

func TestOpenAIBuildsPayloadWithoutSystem(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-4",
		Messages: []Message{{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hello"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Messages) != 1 {
		t.Fatalf("messages = %d, want 1 (user only)", len(captured.Messages))
	}
	if captured.Messages[0].Role != "user" {
		t.Errorf("msg role = %q, want user", captured.Messages[0].Role)
	}
}

func TestOpenAIReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"hmm\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var reasoning string
	for ev := range stream.Events() {
		if ev.Kind == EventReasoningDelta {
			reasoning += ev.Text
		}
	}
	if reasoning != "hmm" {
		t.Errorf("reasoning = %q, want hmm", reasoning)
	}
}

func TestOpenAIBuildPayload_NativeTool(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-5",
		UserText: "search",
		Tools: []ToolSpec{
			{Name: "web_search", Type: "web_search"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(captured.Tools))
	}
	tool := captured.Tools[0]
	native, ok := tool.(map[string]any)
	if !ok {
		t.Fatalf("tool is not map[string]any: %T", tool)
	}
	if native["type"] != "web_search" {
		t.Errorf("tool.type = %v, want web_search", native["type"])
	}
	if _, hasFunction := native["function"]; hasFunction {
		t.Error("native tool should not have 'function' key")
	}
}

func TestOpenAIBuildPayload_NativeToolWithConfig(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "glm-5.1",
		UserText: "search",
		Tools: []ToolSpec{
			{
				Name:         "web_search",
				Type:         "web_search",
				NativeConfig: map[string]any{"web_search": map[string]any{"enable": true}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(captured.Tools))
	}
	tool := captured.Tools[0].(map[string]any)
	if tool["type"] != "web_search" {
		t.Errorf("tool.type = %v, want web_search", tool["type"])
	}
	ws, ok := tool["web_search"].(map[string]any)
	if !ok {
		t.Fatalf("tool.web_search is not map[string]any: %T", tool["web_search"])
	}
	if ws["enable"] != true {
		t.Errorf("tool.web_search.enable = %v, want true", ws["enable"])
	}
	if _, hasFunction := tool["function"]; hasFunction {
		t.Error("native tool should not have 'function' key")
	}
}

func TestOpenAIBuildPayload_MixedNativeAndStandardTools(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-5",
		UserText: "do both",
		Tools: []ToolSpec{
			{Name: "filesystem_read", Description: "Read a file", InputSchema: `{"type":"object","properties":{"path":{"type":"string"}}}`},
			{Name: "web_search", Type: "web_search"},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(captured.Tools))
	}

	// First: standard function tool
	funcTool, ok := captured.Tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool[0] is not map[string]any: %T", captured.Tools[0])
	}
	if funcTool["type"] != "function" {
		t.Errorf("tool[0].type = %v, want function", funcTool["type"])
	}

	// Second: native tool
	nativeTool, ok := captured.Tools[1].(map[string]any)
	if !ok {
		t.Fatalf("tool[1] is not map[string]any: %T", captured.Tools[1])
	}
	if nativeTool["type"] != "web_search" {
		t.Errorf("tool[1].type = %v, want web_search", nativeTool["type"])
	}
}

// TestOpenAIWebSearch_EndToEnd exercises the real OpenAI-compatible endpoint
// (bigmodel.cn) to see how web search behaves. The API key is read from the
// same sources as e2e_agent_test.go.
func TestOpenAIWebSearch_EndToEnd(t *testing.T) {
	apiKey := os.Getenv("SPOREMIND_OPENAI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("SPOREMIND_BIGMODEL_API_KEY")
	}
	if apiKey == "" {
		keyPaths := []string{
			filepath.Join("pkg", "env", "key"),
			filepath.Join("..", "..", "pkg", "env", "key"),
		}
		for _, p := range keyPaths {
			if data, err := os.ReadFile(p); err == nil {
				if v := strings.TrimSpace(string(data)); v != "" {
					apiKey = v
					break
				}
			}
		}
	}
	endpoint := os.Getenv("SPOREMIND_OPENAI_BASE_URL")
	if endpoint == "" {
		endpoint = "https://open.bigmodel.cn/api/coding/paas/v4"
	}
	if apiKey == "" {
		t.Skip("no API key found: set SPOREMIND_OPENAI_API_KEY or write key to pkg/env/key")
	}

	c := NewOpenAIClient(endpoint, apiKey)

	stream, err := c.Stream(context.Background(), Request{
		Model:    "glm-5.1",
		UserText: "What is the latest news today? Please search the web.",
		Tools: []ToolSpec{
			{Name: "web_search", Type: "web_search", NativeConfig: map[string]any{"web_search": map[string]any{"enable": true}}},
		},
	})
	if err != nil {
		t.Fatalf("Stream open failed: %v", err)
	}
	defer stream.Close()

	var (
		text     string
		toolUses int
		done     bool
		hasError bool
	)

	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventToolUseStart:
			toolUses++
			t.Logf("tool_use_start: id=%s name=%s", ev.ToolUseID, ev.ToolName)
		case EventToolUseInputDelta:
			// partial JSON
		case EventToolUseComplete:
			t.Logf("tool_use_complete: id=%s name=%s input=%s", ev.ToolUseID, ev.ToolName, ev.Input)
		case EventUsage:
			if ev.Usage != nil {
				t.Logf("usage: input=%d output=%d total=%d", ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.TotalTokens)
			}
		case EventDone:
			done = true
		case EventError:
			hasError = true
			t.Logf("stream error: %v", ev.Err)
		}
	}

	t.Logf("text_len=%d, tool_uses=%d, done=%v, error=%v", len(text), toolUses, done, hasError)
	t.Logf("response text:\n%s", text)

	if hasError {
		t.Fatal("stream emitted an error event")
	}
	if text == "" && toolUses == 0 {
		t.Fatal("no text and no tool uses — empty response")
	}
}

func TestLowerOpenAIChatDropsEmptyAssistantMessage(t *testing.T) {
	payload, err := lowerOpenAIChat(Request{
		Model: "m",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "before"}}},
			{Role: RoleAssistant, Content: nil, ReasoningContent: "interrupted"},
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "resume"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerOpenAIChat: %v", err)
	}
	var got openaiReq
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != RoleUser || got.Messages[1].Role != RoleUser {
		t.Fatalf("roles = %q, %q; want user, user", got.Messages[0].Role, got.Messages[1].Role)
	}
}

// TestLowerOpenAIChatThinkingModeInjectsReasoningContent verifies that when
// thinking mode is active (DeepSeek R1 via ExtraBody), every assistant
// message gets a reasoning_content field — actual text or empty string —
// so the provider does not reject the request with:
//   "The `reasoning_content` in the thinking mode must be passed back to the API."
func TestLowerOpenAIChatThinkingModeInjectsReasoningContent(t *testing.T) {
	payload, err := lowerOpenAIChat(Request{
		Model: "deepseek-r1",
		ExtraBody: map[string]any{
			"thinking": map[string]any{"type": "enabled"},
		},
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
			// Assistant with tool_calls but no stored reasoning (the plan_submit
			// scenario after an interrupt/restart that lost reasoning text).
			{Role: RoleAssistant, Content: []Block{{
				Type: BlockTypeToolUse, ToolUseID: "tc_1", ToolName: "plan_submit", Input: "{}",
			}}},
			{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "tc_1", Text: "approved"}}},
			// Assistant with text content but no stored reasoning.
			{Role: RoleAssistant, Content: []Block{{Type: BlockTypeText, Text: "working on it"}}},
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "ok"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerOpenAIChat: %v", err)
	}
	var got openaiReq
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	for i, m := range got.Messages {
		if m.Role != RoleAssistant {
			continue
		}
		if m.ReasoningContent == nil {
			t.Errorf("messages[%d] (assistant): reasoning_content is nil; thinking mode requires it on every assistant message", i)
		} else if *m.ReasoningContent != "" {
			t.Errorf("messages[%d] (assistant): reasoning_content = %q, want empty string (no stored reasoning)", i, *m.ReasoningContent)
		}
	}
}

// TestLowerOpenAIChatThinkingModePreservesStoredReasoning verifies that
// stored reasoning content is passed through verbatim when thinking mode
// is active.
func TestLowerOpenAIChatThinkingModePreservesStoredReasoning(t *testing.T) {
	payload, err := lowerOpenAIChat(Request{
		Model:           "o3-mini",
		ReasoningEffort: "high",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
			{Role: RoleAssistant, ReasoningContent: "deep thoughts", Content: []Block{{Type: BlockTypeText, Text: "answer"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerOpenAIChat: %v", err)
	}
	var got openaiReq
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// messages[0] = system is absent (no System), so messages[0] = user, [1] = assistant
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}
	if got.Messages[1].ReasoningContent == nil || *got.Messages[1].ReasoningContent != "deep thoughts" {
		t.Errorf("reasoning_content = %v, want \"deep thoughts\"", got.Messages[1].ReasoningContent)
	}
}

// TestLowerOpenAIChatNonThinkingModeNoReasoningContent verifies that when
// thinking mode is NOT active, no reasoning_content is injected for
// assistant messages without stored reasoning.
func TestLowerOpenAIChatNonThinkingModeNoReasoningContent(t *testing.T) {
	payload, err := lowerOpenAIChat(Request{
		Model: "gpt-4o",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
			{Role: RoleAssistant, Content: []Block{{
				Type: BlockTypeToolUse, ToolUseID: "tc_1", ToolName: "search", Input: "{}",
			}}},
			{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "tc_1", Text: "result"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerOpenAIChat: %v", err)
	}
	var got openaiReq
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for i, m := range got.Messages {
		if m.Role == RoleAssistant && m.ReasoningContent != nil {
			t.Errorf("messages[%d] (assistant): reasoning_content should be nil in non-thinking mode, got %v", i, *m.ReasoningContent)
		}
	}
}

func TestSanitizeOpenAIMessages_DropsOrphanedToolResult(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
		{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "tc_1", Text: "result"}}},
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "next"}}},
	}
	got := sanitizeOpenAIMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Role != RoleUser || got[1].Role != RoleUser {
		t.Errorf("roles = %q, %q; want user, user", got[0].Role, got[1].Role)
	}
}

func TestSanitizeOpenAIMessages_KeepsPairedToolResult(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
		{Role: RoleAssistant, Content: []Block{
			{Type: BlockTypeToolUse, ToolUseID: "tc_1", ToolName: "foo"},
		}},
		{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "tc_1", Text: "result"}}},
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "next"}}},
	}
	got := sanitizeOpenAIMessages(msgs)
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	if got[2].Role != RoleTool {
		t.Errorf("msg[2].Role = %q, want tool", got[2].Role)
	}
}

func TestSanitizeOpenAIMessages_StripsTrailingToolCalls(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
		{Role: RoleAssistant, Content: []Block{
			{Type: BlockTypeText, Text: "let me check"},
			{Type: BlockTypeToolUse, ToolUseID: "tc_1", ToolName: "foo"},
		}},
	}
	got := sanitizeOpenAIMessages(msgs)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (only user)", len(got))
	}
	if got[0].Role != RoleUser {
		t.Errorf("msg[0].Role = %q, want user", got[0].Role)
	}
}

func TestSanitizeOpenAIMessages_PreservesTrailingAssistantWithoutTools(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
		{Role: RoleAssistant, Content: []Block{{Type: BlockTypeText, Text: "hello!"}}},
	}
	got := sanitizeOpenAIMessages(msgs)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

// TestSanitizeOpenAIMessages_StripsNonTrailingOrphanToolUse reproduces the
// user-reported scenario: a cancelled plan.submit leaves an orphan
// assistant(tool_use) followed by a cancel-marker assistant(text). The
// OpenAI provider rejects this with HTTP 400 "tool_call_ids did not have
// response messages". The sanitizer must strip the orphan tool_use even
// when it is not the trailing message.
func TestSanitizeOpenAIMessages_StripsNonTrailingOrphanToolUse(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "refactor"}}},
		{Role: RoleAssistant, Content: []Block{
			{Type: BlockTypeToolUse, ToolUseID: "plan_submit:26", ToolName: "plan_submit"},
		}},
		{Role: RoleAssistant, Content: []Block{{Type: BlockTypeText, Text: "用户停止了生成。"}}},
	}
	got := sanitizeOpenAIMessages(msgs)
	for i, m := range got {
		for _, b := range m.Content {
			if b.Type == BlockTypeToolUse {
				t.Fatalf("orphan tool_use survived at index %d: %+v", i, b)
			}
		}
	}
	var sawCancel bool
	for _, m := range got {
		for _, b := range m.Content {
			if b.Type == BlockTypeText && b.Text == "用户停止了生成。" {
				sawCancel = true
			}
		}
	}
	if !sawCancel {
		t.Fatalf("cancel marker dropped: %+v", got)
	}
}

// TestOpenAIBuildPayload_StripsNullSchemaKeywords reproduces the aggregator
// 400: a tool whose InputSchema carries "required": null must not reach the
// wire — strict providers reject null for array-typed schema keywords.
func TestOpenAIBuildPayload_StripsNullSchemaKeywords(t *testing.T) {
	var captured openaiReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-5",
		UserText: "read a file",
		Tools: []ToolSpec{
			{Name: "file_read", Description: "read", InputSchema: `{"type":"object","properties":{"path":{"type":"string"}},"required":null}`},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if len(captured.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(captured.Tools))
	}
	tool, ok := captured.Tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not map[string]any: %T", captured.Tools[0])
	}
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatalf("function is not map[string]any: %T", tool["function"])
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("parameters is not map[string]any: %T", fn["parameters"])
	}
	if _, ok := params["required"]; ok {
		t.Errorf("required must be stripped from wire parameters, got %v", fn["parameters"])
	}
	if _, ok := params["properties"]; !ok {
		t.Errorf("properties must survive, got %v", fn["parameters"])
	}
}
