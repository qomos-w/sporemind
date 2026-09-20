package llmclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsOpenAIThinkingMode_DeepSeekHybridDefaults(t *testing.T) {
	cases := []struct {
		model string
		want  bool
	}{
		{"deepseek-v4-flash", true},
		{"deepseek-flash", true},
		{"deepseek/deepseek-v4.1-flash", true},
		{"deepseek-v3.1", true},
		{"deepseek-chat", false},
		{"gpt-4o", false},
		{"kimi-k2-thinking", true},
	}
	for _, tc := range cases {
		if got := isOpenAIThinkingMode(Request{Model: tc.model}); got != tc.want {
			t.Errorf("isOpenAIThinkingMode(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
	// Explicit effort still wins for non-hybrid names.
	if !isOpenAIThinkingMode(Request{Model: "gpt-4o", ReasoningEffort: "low"}) {
		t.Error("explicit ReasoningEffort should mark thinking mode")
	}
}

func TestLowerOpenAIChat_InjectsEmptyReasoningForDeepSeekHybrid(t *testing.T) {
	payload, err := lowerOpenAIChat(Request{
		Model: "deepseek-v4-flash",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
			{Role: RoleAssistant, Content: []Block{{Type: BlockTypeText, Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerOpenAIChat: %v", err)
	}
	var req openaiReq
	if err := json.Unmarshal(payload, &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var sawAssistant bool
	for _, m := range req.Messages {
		if m.Role != RoleAssistant {
			continue
		}
		sawAssistant = true
		if m.ReasoningContent == nil || *m.ReasoningContent != "" {
			t.Fatalf("assistant message reasoning_content = %#v, want pointer to empty string", m.ReasoningContent)
		}
	}
	if !sawAssistant {
		t.Fatal("no assistant message in lowered request")
	}
}

// TestStream_SelfHealsReasoningReplayRejection pins the crash-storm fix:
// a server-default thinking model (name undetected by isOpenAIThinkingMode)
// rejects the first request with the reasoning-replay 400; the client
// retries once with thinking explicitly enabled so the empty
// reasoning_content injection applies, and the second request succeeds.
func TestStream_SelfHealsReasoningReplayRejection(t *testing.T) {
	var calls int32
	var secondBody openaiReq
	var secondRaw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, "{\"error\":{\"message\":\"The `reasoning_content` in the thinking mode must be passed back to the API.\",\"type\":\"invalid_request_error\"}}")
			return
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &secondBody); err != nil {
			t.Errorf("decode second body: %v", err)
		}
		if err := json.Unmarshal(body, &secondRaw); err != nil {
			t.Errorf("decode second raw body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "test-key")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		// A name the detection does NOT cover: only the self-heal path
		// can recover it.
		Model: "future-flash-x",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
			{Role: RoleAssistant, Content: []Block{{Type: BlockTypeText, Text: "hello"}}},
		},
	})
	if err != nil {
		t.Fatalf("Stream (self-heal retry): %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server calls = %d, want 2 (initial 400 + healed retry)", got)
	}
	var sawAssistant bool
	for _, m := range secondBody.Messages {
		if m.Role != RoleAssistant {
			continue
		}
		sawAssistant = true
		if m.ReasoningContent == nil || *m.ReasoningContent != "" {
			t.Fatalf("healed request assistant reasoning_content = %#v, want empty string", m.ReasoningContent)
		}
	}
	if !sawAssistant {
		t.Fatal("healed request has no assistant message")
	}
	if thinking, ok := secondRaw["thinking"].(map[string]any); !ok || thinking["type"] != "enabled" {
		t.Fatalf("healed request thinking = %#v, want {type: enabled}", secondRaw["thinking"])
	}
}

// TestStream_DoesNotSelfHealWhenAlreadyThinking: when the first request
// already carried thinking signals, a replay 400 is a genuine provider
// failure — no second attempt.
func TestStream_DoesNotSelfHealWhenAlreadyThinking(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "{\"error\":{\"message\":\"The `reasoning_text` in the thinking mode must be passed back to the API.\"}}")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "test-key")
	c.HTTPClient = server.Client()

	_, err := c.Stream(context.Background(), Request{
		Model:           "deepseek-v4-flash",
		ReasoningEffort: "high",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "hi"}}},
		},
	})
	if err == nil {
		t.Fatal("expected the 400 to surface")
	}
	if !strings.Contains(err.Error(), "must be passed back") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server calls = %d, want 1 (no self-heal retry)", got)
	}
}

func TestAggregatorHealthRegistry_ExpiredEntriesDeletedOnRead(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.SetTTL(time.Millisecond)
	r.RegisterUnavailableUntil("agg-a", time.Now().Add(50*time.Millisecond))
	if r.IsAvailable("agg-a", time.Now()) {
		t.Fatal("agg-a should be unavailable before deadline")
	}
	if !r.IsAvailable("agg-a", time.Now().Add(time.Second)) {
		t.Fatal("agg-a should be available after deadline")
	}
	r.mu.RLock()
	_, present := r.entries["agg-a"]
	r.mu.RUnlock()
	if present {
		t.Fatal("expired entry should be deleted on read")
	}
}
