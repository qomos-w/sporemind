package llmclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sseTextChunk builds an OpenAI chat-completions SSE data line carrying a
// content delta. The tag markers are assembled from package constants so no
// literal marker token appears in source.
func sseTextChunk(content string) string {
	return fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
}

// TestOpenAILeadingThinkDiverted verifies that a leading inline think block
// leaked into the content stream is routed as reasoning.
func TestOpenAILeadingThinkDiverted(t *testing.T) {
	content := thinkOpenTag + "hmm" + thinkCloseTag + "answer"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextChunk(content))
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x", IsReasoning: true})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text, reasoning string
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventReasoningDelta:
			reasoning += ev.Text
		}
	}
	if reasoning != "hmm" {
		t.Errorf("reasoning = %q, want %q", reasoning, "hmm")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

// TestOpenAILeadingThinkDivertedNoReasoningFlag verifies that a leading
// inline think block is diverted to reasoning even when the unit is NOT a
// reasoning model: the diversion is unconditional so non-thinking models
// (MiniMax, etc.) that leak think blocks into content are covered too.
func TestOpenAILeadingThinkDivertedNoReasoningFlag(t *testing.T) {
	content := thinkOpenTag + "hmm" + thinkCloseTag + "answer"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextChunk(content))
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

	var text, reasoning string
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventReasoningDelta:
			reasoning += ev.Text
		}
	}
	if reasoning != "hmm" {
		t.Errorf("reasoning = %q, want %q", reasoning, "hmm")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

// TestOpenAIOllamaReasoningField verifies that thinking streamed under the
// "reasoning" field name — Ollama's OpenAI-compatible API, unlike DeepSeek's
// "reasoning_content" — is surfaced as reasoning instead of being dropped.
func TestOpenAIOllamaReasoningField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"think a\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"nd think b\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n")
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

	var text, reasoning string
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventReasoningDelta:
			reasoning += ev.Text
		}
	}
	if reasoning != "think and think b" {
		t.Errorf("reasoning = %q, want %q", reasoning, "think and think b")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

// TestOpenAIThinkDivertRearmsAfterToolCall verifies that content arriving
// after tool-call deltas in the same response diverts its own leading think
// block: a tool call ends the text segment and re-arms the filter.
func TestOpenAIThinkDivertRearmsAfterToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"A \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"get\",\"arguments\":\"{}\"}}]}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\""+thinkOpenTag+"R"+thinkCloseTag+"B\"}}]}\n\n")
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

	var text, reasoning string
	var toolName string
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventReasoningDelta:
			reasoning += ev.Text
		case EventToolUseStart:
			toolName = ev.ToolName
		}
	}
	if reasoning != "R" {
		t.Errorf("reasoning = %q, want %q", reasoning, "R")
	}
	if text != "A B" {
		t.Errorf("text = %q, want %q", text, "A B")
	}
	if toolName != "get" {
		t.Errorf("toolName = %q, want %q", toolName, "get")
	}
}

// TestOpenAILeadingThinkSplitAcrossChunks verifies the diversion still works
// when the opening tag is split across SSE deltas.
func TestOpenAILeadingThinkSplitAcrossChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sseTextChunk("<"))
		_, _ = io.WriteString(w, sseTextChunk(thinkOpenTag[1:]+"hmm"))
		_, _ = io.WriteString(w, sseTextChunk(thinkCloseTag+"answer"))
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x", IsReasoning: true})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var text, reasoning string
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventReasoningDelta:
			reasoning += ev.Text
		}
	}
	if reasoning != "hmm" {
		t.Errorf("reasoning = %q, want %q", reasoning, "hmm")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}
