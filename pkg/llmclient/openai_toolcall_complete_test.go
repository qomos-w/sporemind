package llmclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestOpenAIStreamedToolCallsEmitComplete pins the terminal tool-call
// emission: on finish_reason "tool_calls" the stream must emit one
// EventToolUseComplete per assembled call — in tool-index order, with the
// function name and the assembled arguments — before the stop event.
// Terminal-only consumers (the plugin LLM bridge's ToolCalls) depend on it;
// previously the assembled arguments were only logged.
func TestOpenAIStreamedToolCallsEmitComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"extract_meta","arguments":""}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"second_tool","arguments":"{}"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`+"\n\n")
		fmt.Fprint(w, `data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), Request{
		Model: "deepseek-v4-flash",
		Tools: []ToolSpec{
			{Name: "extract_meta", InputSchema: `{"type":"object"}`},
			{Name: "second_tool", InputSchema: `{"type":"object"}`},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()

	var order []string
	var completes []Event
	var stops []Event
	for ev := range stream.Events() {
		switch ev.Kind {
		case EventToolUseStart:
			order = append(order, "start:"+ev.ToolUseID)
		case EventToolUseComplete:
			order = append(order, "complete:"+ev.ToolUseID)
			completes = append(completes, ev)
		case EventStop:
			stops = append(stops, ev)
		case EventError:
			t.Fatalf("stream error: %v", ev.Err)
		}
	}

	wantOrder := []string{
		"start:call_a", "start:call_b",
		"complete:call_a", "complete:call_b",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("event order = %v, want %v", order, wantOrder)
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Fatalf("event order = %v, want %v", order, wantOrder)
		}
	}

	if len(completes) != 2 {
		t.Fatalf("completes = %d, want 2", len(completes))
	}
	if completes[0].ToolName != "extract_meta" || completes[0].Input != `{"a":1}` {
		t.Errorf("completes[0] = %s/%s, want extract_meta/{\"a\":1}", completes[0].ToolName, completes[0].Input)
	}
	if completes[1].ToolName != "second_tool" || completes[1].Input != "{}" {
		t.Errorf("completes[1] = %s/%s, want second_tool/{}", completes[1].ToolName, completes[1].Input)
	}
	if len(stops) != 1 || stops[0].StopReason != "tool_use" {
		t.Errorf("stops = %+v, want one stop with reason tool_use", stops)
	}
}
