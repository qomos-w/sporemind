package llmclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEndpointClient_Stream verifies a full round-trip against a local
// OpenAI-compatible SSE server.
func TestEndpointClient_Stream(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat" {
			t.Errorf("path = %q, want /v1/chat", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer test-key") {
			t.Errorf("Authorization = %q, want Bearer test-key...", auth)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["model"] != "gpt-test" {
			t.Errorf("model = %v, want gpt-test", body["model"])
		}
		if body["stream"] != true {
			t.Errorf("stream = %v, want true", body["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		chunks := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{"content":" world"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			`data: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}` + "\n\n",
			"data: [DONE]\n\n",
		}
		for _, c := range chunks {
			fmt.Fprint(w, c)
			flusher.Flush()
			time.Sleep(5 * time.Millisecond) // ensure measurable latency
		}
	}))
	defer ts.Close()

	client := NewEndpointClient(ts.URL+"/v1/chat", "test-key")
	stream, err := client.Stream(context.Background(), Request{
		Model:    "gpt-test",
		System:   "You are a test",
		UserText: "Say hello",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text string
	var done bool
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want %q", text, "Hello world")
	}
	if !done {
		t.Error("expected EventDone")
	}

	// Verify telemetry timing and usage are populated. Before the setStart
	// fix, endpoint streams left startedAt zero, so LatencyMs and
	// FirstTokenMs were always 0.
	telemetry := stream.Telemetry()
	if telemetry.LatencyMs <= 0 {
		t.Errorf("LatencyMs = %d, want > 0", telemetry.LatencyMs)
	}
	if telemetry.FirstTokenMs < 0 {
		t.Errorf("FirstTokenMs = %d, want >= 0", telemetry.FirstTokenMs)
	}
	if telemetry.Model != "gpt-test" {
		t.Errorf("telemetry Model = %q, want gpt-test", telemetry.Model)
	}
	if telemetry.Usage == nil {
		t.Fatal("Usage is nil; endpoint stream did not capture usage")
	}
	if telemetry.Usage.InputTokens != 10 || telemetry.Usage.OutputTokens != 5 {
		t.Errorf("usage tokens: in=%d out=%d, want 10/5", telemetry.Usage.InputTokens, telemetry.Usage.OutputTokens)
	}
}

// TestEndpointClient_NoDoneTerminator verifies that a provider (e.g.
// Moonshot/Kimi) which closes the connection after finish_reason without
// sending [DONE] terminates cleanly — no ErrStreamClosed, usage still
// captured from the trailing chunk.
func TestEndpointClient_NoDoneTerminator(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		// finish_reason chunk, then usage chunk, then connection closes
		// (no [DONE]).
		chunks := []string{
			`data: {"choices":[{"delta":{"content":"hi"}}]}` + "\n\n",
			`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n",
			`data: {"choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}` + "\n\n",
		}
		for _, c := range chunks {
			fmt.Fprint(w, c)
			flusher.Flush()
		}
	}))
	defer ts.Close()

	client := NewEndpointClient(ts.URL, "k")
	stream, err := client.Stream(context.Background(), Request{Model: "m", UserText: "x"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text string
	var hadError bool
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventError:
			hadError = true
		}
	}
	if hadError {
		t.Error("unexpected EventError on clean finish without [DONE]")
	}
	if text != "hi" {
		t.Errorf("text = %q, want hi", text)
	}
	tel := stream.Telemetry()
	if tel.Usage == nil || tel.Usage.InputTokens != 8 {
		t.Errorf("usage not captured: %+v", tel.Usage)
	}
}

// TestEndpointClient_StatusError verifies non-2xx responses surface the body.
func TestEndpointClient_StatusError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid key"}`)
	}))
	defer ts.Close()

	client := NewEndpointClient(ts.URL, "bad-key")
	_, err := client.Stream(context.Background(), Request{Model: "m", UserText: "hi"})
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !strings.Contains(err.Error(), "invalid key") {
		t.Errorf("error = %q, want contain 'invalid key'", err.Error())
	}
}

// TestCallEndpointText verifies the blocking helper.
func TestCallEndpointText(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"42"}}]}`+"\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := CallEndpointText(ctx, ts.URL, "", "m", "", "What is the answer?")
	if err != nil {
		t.Fatalf("CallEndpointText: %v", err)
	}
	if result != "42" {
		t.Errorf("result = %q, want %q", result, "42")
	}
}

// TestEndpointClient_NoAuthKey verifies requests work without an API key.
func TestEndpointClient_NoAuthKey(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	client := NewEndpointClient(ts.URL, "")
	stream, err := client.Stream(context.Background(), Request{Model: "m", UserText: "hi"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range stream.Events() {
	}
	stream.Close()

	if gotAuth != "" {
		t.Errorf("Authorization = %q, want empty", gotAuth)
	}
}
