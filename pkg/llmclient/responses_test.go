package llmclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type responsesRun struct {
	captured  responsesReq
	events    []Event
	telemetry RequestTelemetry
}

// runResponses drives one request through a mock server, capturing the
// decoded request body and the full event sequence.
func runResponses(t *testing.T, req Request, respond func(w http.ResponseWriter)) responsesRun {
	t.Helper()
	var run responsesRun
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &run.captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		respond(w)
	}))
	defer server.Close()

	c := NewResponsesClient(server.URL, "test-key")
	c.HTTPClient = server.Client()

	stream, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for ev := range stream.Events() {
		run.events = append(run.events, ev)
	}
	run.telemetry = stream.Telemetry()
	return run
}

func (r responsesRun) eventsOf(kind EventKind) []Event {
	var out []Event
	for _, ev := range r.events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestResponsesBuildsStreamingRequest(t *testing.T) {
	var path string
	var auth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	c := NewResponsesClient(server.URL, "test-key")
	c.HTTPClient = server.Client()
	stream, err := c.Stream(context.Background(), Request{
		Model:    "gpt-5",
		System:   "be terse",
		UserText: "hello",
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}

	if path != "/responses" {
		t.Errorf("path = %q, want /responses", path)
	}
	if auth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want Bearer test-key", auth)
	}
}

func TestResponsesBuildsStreamingRequestWithTrailingSlash(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	}))
	defer server.Close()

	c := NewResponsesClient(server.URL+"///", "test-key")
	c.HTTPClient = server.Client()
	stream, err := c.Stream(context.Background(), Request{Model: "gpt-5", UserText: "hello"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	defer stream.Close()
	for range stream.Events() {
	}
	if path != "/responses" {
		t.Fatalf("got path %q, want /responses", path)
	}
}

func TestResponsesLowerRequestFields(t *testing.T) {
	run := runResponses(t, Request{
		Model:           "gpt-5",
		System:          "be terse",
		UserText:        "hello",
		MaxTokens:       2048,
		ReasoningEffort: "high",
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	if run.captured.Model != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", run.captured.Model)
	}
	if !run.captured.Stream {
		t.Error("Stream should be true")
	}
	if run.captured.Instructions != "be terse" {
		t.Errorf("instructions = %q, want %q", run.captured.Instructions, "be terse")
	}
	if run.captured.MaxOutputTokens != 2048 {
		t.Errorf("max_output_tokens = %d, want 2048", run.captured.MaxOutputTokens)
	}
	if run.captured.Reasoning == nil || run.captured.Reasoning.Effort != "high" {
		t.Errorf("reasoning = %#v, want effort high", run.captured.Reasoning)
	}
	if len(run.captured.Input) != 1 {
		t.Fatalf("input items = %d, want 1", len(run.captured.Input))
	}
	item := run.captured.Input[0]
	if item.Type != "message" || item.Role != RoleUser {
		t.Errorf("input[0] = %#v, want user message", item)
	}
	if len(item.Content) != 1 || item.Content[0].Type != "input_text" || item.Content[0].Text != "hello" {
		t.Errorf("input[0].content = %#v, want input_text hello", item.Content)
	}
}

func TestResponsesInstructionsFromSystemBlocks(t *testing.T) {
	run := runResponses(t, Request{
		Model:  "gpt-5",
		System: "ignored",
		SystemBlocks: []SystemBlock{
			{Type: "text", Text: "policy: be safe"},
			{Type: "text", Text: "task: answer"},
		},
		UserText: "hi",
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	if run.captured.Instructions != "policy: be safe\n\ntask: answer" {
		t.Errorf("instructions = %q, want blocks joined", run.captured.Instructions)
	}
}

func TestResponsesStreamsTextDeltasAndUsage(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.done\",\"text\":\"hello world\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"input_tokens_details\":{\"cached_tokens\":4},\"output_tokens\":20,\"output_tokens_details\":{\"reasoning_tokens\":10},\"total_tokens\":32}}}\n\n")
	})

	var text string
	var usage *Usage
	var stop string
	done := false
	for _, ev := range run.events {
		switch ev.Kind {
		case EventTextDelta:
			text += ev.Text
		case EventUsage:
			usage = ev.Usage
		case EventStop:
			stop = ev.StopReason
		case EventDone:
			done = true
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}

	if text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
	if usage == nil {
		t.Fatal("expected usage event")
	}
	if usage.InputTokens != 8 || usage.OutputTokens != 20 || usage.TotalTokens != 32 {
		t.Errorf("usage = %#v, want input 8 / output 20 / total 32", usage)
	}
	if usage.CacheReadInputTokens != 4 {
		t.Errorf("cacheRead = %d, want 4", usage.CacheReadInputTokens)
	}
	if usage.ReasoningTokens != 10 {
		t.Errorf("reasoningTokens = %d, want 10", usage.ReasoningTokens)
	}
	if stop != "end_turn" {
		t.Errorf("stop reason = %q, want end_turn", stop)
	}
	if !done {
		t.Error("expected done event")
	}
	if run.telemetry.ResponseID != "resp_1" {
		t.Errorf("telemetry responseID = %q, want resp_1", run.telemetry.ResponseID)
	}
	if run.telemetry.StopReason != StopReasonStop {
		t.Errorf("telemetry stop reason = %q, want %q", run.telemetry.StopReason, StopReasonStop)
	}
}

func TestResponsesStreamsReasoning(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"hmm\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\" more\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	var reasoning, text string
	for _, ev := range run.events {
		switch ev.Kind {
		case EventReasoningDelta:
			reasoning += ev.Text
		case EventTextDelta:
			text += ev.Text
		case EventError:
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	if reasoning != "hmm more" {
		t.Errorf("reasoning = %q, want %q", reasoning, "hmm more")
	}
	if text != "answer" {
		t.Errorf("text = %q, want %q", text, "answer")
	}
}

func TestResponsesSingleToolCallRound(t *testing.T) {
	run := runResponses(t, Request{
		Model:    "m",
		UserText: "search",
		Tools: []ToolSpec{
			{Name: "search", Description: "Search the web", InputSchema: `{"type":"object","properties":{"q":{"type":"string"}}}`},
		},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"search\",\"arguments\":\"\",\"status\":\"in_progress\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"call_id\":\"call_1\",\"delta\":\"{\\\"q\\\":\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.delta\",\"call_id\":\"call_1\",\"delta\":\"\\\"x\\\"}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.function_call_arguments.done\",\"call_id\":\"call_1\",\"arguments\":\"{\\\"q\\\":\\\"x\\\"}\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\\\"x\\\"}\",\"status\":\"completed\"}}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n\n")
	})

	starts := run.eventsOf(EventToolUseStart)
	if len(starts) != 1 || starts[0].ToolUseID != "call_1" || starts[0].ToolName != "search" {
		t.Fatalf("tool start events = %#v, want one call_1/search", starts)
	}

	deltas := run.eventsOf(EventToolUseInputDelta)
	if len(deltas) != 2 {
		t.Fatalf("input delta events = %d, want 2", len(deltas))
	}
	if deltas[0].ToolUseID != "call_1" || deltas[1].ToolUseID != "call_1" {
		t.Errorf("delta tool IDs = %q/%q, want call_1", deltas[0].ToolUseID, deltas[1].ToolUseID)
	}

	completes := run.eventsOf(EventToolUseComplete)
	if len(completes) != 1 {
		t.Fatalf("tool complete events = %d, want 1 (no double-emit from output_item.done)", len(completes))
	}
	if completes[0].ToolUseID != "call_1" || completes[0].ToolName != "search" || completes[0].Input != `{"q":"x"}` {
		t.Errorf("tool complete = %#v, want call_1/search/%q", completes[0], `{"q":"x"}`)
	}

	stops := run.eventsOf(EventStop)
	if len(stops) != 1 || stops[0].StopReason != "tool_use" {
		t.Errorf("stop events = %#v, want one tool_use", stops)
	}
	if len(run.eventsOf(EventDone)) != 1 {
		t.Error("expected one done event")
	}
}

func TestResponsesToolChoiceMapping(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"auto", "auto"},
		{"none", "none"},
		{"required", "required"},
		{`{"type":"function","name":"search"}`, map[string]any{"type": "function", "name": "search"}},
		{`{"type":"function","function":{"name":"search"}}`, map[string]any{"type": "function", "name": "search"}},
	}
	for _, tc := range cases {
		got := lowerResponsesToolChoice(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("lowerResponsesToolChoice(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestResponsesBodyToolChoiceAndFlatTools(t *testing.T) {
	run := runResponses(t, Request{
		Model:      "gpt-5",
		UserText:   "search",
		ToolChoice: `{"type":"function","function":{"name":"search"}}`,
		Tools: []ToolSpec{
			{Name: "search", Description: "Search", InputSchema: `{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}`},
		},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	wantChoice := map[string]any{"type": "function", "name": "search"}
	if !reflect.DeepEqual(run.captured.ToolChoice, wantChoice) {
		t.Errorf("tool_choice = %#v, want %#v", run.captured.ToolChoice, wantChoice)
	}

	if len(run.captured.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(run.captured.Tools))
	}
	tool, ok := run.captured.Tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool is not map[string]any: %T", run.captured.Tools[0])
	}
	if tool["type"] != "function" {
		t.Errorf("tool.type = %v, want function", tool["type"])
	}
	if tool["name"] != "search" {
		t.Errorf("tool.name = %v, want search", tool["name"])
	}
	if tool["description"] != "Search" {
		t.Errorf("tool.description = %v, want Search", tool["description"])
	}
	if _, hasFunction := tool["function"]; hasFunction {
		t.Error("Responses tool must be flat — no nested 'function' key")
	}
	params, ok := tool["parameters"].(map[string]any)
	if !ok {
		t.Fatalf("tool.parameters is not map[string]any: %T", tool["parameters"])
	}
	if _, hasQ := params["properties"].(map[string]any)["q"]; !hasQ {
		t.Error("tool.parameters should carry the input schema")
	}
}

func TestResponsesBodyNativeTool(t *testing.T) {
	run := runResponses(t, Request{
		Model:    "gpt-5",
		UserText: "search",
		Tools: []ToolSpec{
			{Name: "web_search", Type: "web_search"},
		},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	if len(run.captured.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(run.captured.Tools))
	}
	tool, ok := run.captured.Tools[0].(map[string]any)
	if !ok {
		t.Fatalf("native tool is not map[string]any: %T", run.captured.Tools[0])
	}
	if tool["type"] != "web_search" {
		t.Errorf("tool.type = %v, want web_search", tool["type"])
	}
	if _, hasFunction := tool["function"]; hasFunction {
		t.Error("native tool should not have 'function' key")
	}
}

func TestResponsesMultiTurnHistory(t *testing.T) {
	run := runResponses(t, Request{
		Model:  "gpt-5",
		System: "be helpful",
		Messages: []Message{
			{Role: RoleUser, Content: []Block{{Type: BlockTypeText, Text: "summarize files"}}},
			{
				Role: RoleAssistant,
				Content: []Block{
					{Type: BlockTypeText, Text: "Let me look."},
					{Type: BlockTypeToolUse, ToolUseID: "call_1", ToolName: "ls", Input: `{"path":"/"}`},
				},
			},
			{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "call_1", Text: "a.txt\nb.txt"}}},
		},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	if run.captured.Instructions != "be helpful" {
		t.Errorf("instructions = %q, want %q", run.captured.Instructions, "be helpful")
	}

	items := run.captured.Input
	if len(items) != 4 {
		t.Fatalf("input items = %d, want 4 (user, assistant, function_call, function_call_output)", len(items))
	}

	user := items[0]
	if user.Type != "message" || user.Role != RoleUser || len(user.Content) != 1 || user.Content[0].Type != "input_text" {
		t.Errorf("input[0] = %#v, want user input_text message", user)
	}

	asst := items[1]
	if asst.Type != "message" || asst.Role != RoleAssistant || len(asst.Content) != 1 || asst.Content[0].Type != "output_text" || asst.Content[0].Text != "Let me look." {
		t.Errorf("input[1] = %#v, want assistant output_text message", asst)
	}

	fc := items[2]
	if fc.Type != "function_call" || fc.CallID != "call_1" || fc.Name != "ls" || fc.Arguments != `{"path":"/"}` {
		t.Errorf("input[2] = %#v, want function_call call_1/ls", fc)
	}

	out := items[3]
	if out.Type != "function_call_output" || out.CallID != "call_1" || out.Output != "a.txt\nb.txt" {
		t.Errorf("input[3] = %#v, want function_call_output call_1", out)
	}
}

func TestResponsesHistoryReasoningItem(t *testing.T) {
	run := runResponses(t, Request{
		Model: "gpt-5",
		Messages: []Message{
			{Role: RoleAssistant, ReasoningContent: "thought hard", Content: []Block{{Type: BlockTypeText, Text: "done"}}},
		},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	items := run.captured.Input
	if len(items) != 1 {
		t.Fatalf("input items = %d, want 1 (assistant message; reasoning summary lacks provider identity)", len(items))
	}
	if items[0].Type != "message" || items[0].Role != RoleAssistant {
		t.Errorf("input[0] = %#v, want assistant message", items[0])
	}
}

func TestResponsesUserImagesBecomeInputImage(t *testing.T) {
	run := runResponses(t, Request{
		Model: "gpt-4o",
		Messages: []Message{{
			Role: RoleUser,
			Content: []Block{
				{Type: BlockTypeText, Text: "what is this?"},
				{Type: BlockTypeImage, ImageURL: "data:image/png;base64,xxx"},
			},
		}},
	}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\"}}\n\n")
	})

	items := run.captured.Input
	if len(items) != 1 || len(items[0].Content) != 2 {
		t.Fatalf("input = %#v, want one user message with text+image parts", items)
	}
	if items[0].Content[0].Type != "input_text" {
		t.Errorf("content[0].type = %q, want input_text", items[0].Content[0].Type)
	}
	if items[0].Content[1].Type != "input_image" || items[0].Content[1].ImageURL != "data:image/png;base64,xxx" {
		t.Errorf("content[1] = %#v, want input_image", items[0].Content[1])
	}
}

func TestResponsesEmptyArgumentsDefaultToObject(t *testing.T) {
	payload, err := lowerResponses(Request{
		Model: "gpt-5",
		Messages: []Message{
			{Role: RoleAssistant, Content: []Block{{Type: BlockTypeToolUse, ToolUseID: "call_1", ToolName: "noop", Input: ""}}},
			{Role: RoleTool, Content: []Block{{Type: BlockTypeToolResult, ToolUseID: "call_1", Text: "ok"}}},
		},
	})
	if err != nil {
		t.Fatalf("lowerResponses: %v", err)
	}
	var body responsesReq
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(body.Input) != 2 {
		t.Fatalf("input = %d items, want 2", len(body.Input))
	}
	if body.Input[0].Arguments != "{}" {
		t.Errorf("function_call arguments = %q, want %q", body.Input[0].Arguments, "{}")
	}
}

func TestResponsesReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_request_error","message":"bad input","type":"invalid_request_error"}}`)
	}))
	defer server.Close()

	c := NewResponsesClient(server.URL, "k")
	c.HTTPClient = server.Client()
	_, err := c.Stream(context.Background(), Request{Model: "m", UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error is not *UpstreamError: %T", err)
	}
	if upstream.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", upstream.StatusCode)
	}
	if upstream.Message != "bad input" {
		t.Errorf("message = %q, want %q", upstream.Message, "bad input")
	}
}

func TestResponsesFailedEvent(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"boom\"}}}\n\n")
	})

	errors_ := run.eventsOf(EventError)
	if len(errors_) != 1 {
		t.Fatalf("error events = %d, want 1", len(errors_))
	}
	var upstream *UpstreamError
	if !errors.As(errors_[0].Err, &upstream) {
		t.Fatalf("error is not *UpstreamError: %T", errors_[0].Err)
	}
	if upstream.Code != "server_error" || upstream.Type != "response_failed" || upstream.Message != "boom" {
		t.Errorf("upstream error = %#v, want server_error/response_failed/boom", upstream)
	}
	if len(run.eventsOf(EventDone)) != 0 {
		t.Error("failed response must not emit a done event")
	}
	if run.telemetry.ErrorMessage == "" {
		t.Error("telemetry should record the error message")
	}
}

func TestResponsesIncompleteEvent(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_1\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"}}}\n\n")
	})

	errors_ := run.eventsOf(EventError)
	if len(errors_) != 1 {
		t.Fatalf("error events = %d, want 1", len(errors_))
	}
	var upstream *UpstreamError
	if !errors.As(errors_[0].Err, &upstream) {
		t.Errorf("error is not *UpstreamError: %T", errors_[0].Err)
	}
	if upstream.Type != "response_incomplete" {
		t.Errorf("upstream type = %q, want response_incomplete", upstream.Type)
	}
}

func TestResponsesStreamErrorEvent(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"error\",\"error\":{\"code\":\"rate_limit_exceeded\",\"message\":\"slow down\",\"type\":\"rate_limit_error\"}}\n\n")
	})

	errors_ := run.eventsOf(EventError)
	if len(errors_) != 1 {
		t.Fatalf("error events = %d, want 1", len(errors_))
	}
	var upstream *UpstreamError
	if !errors.As(errors_[0].Err, &upstream) {
		t.Fatalf("error is not *UpstreamError: %T", errors_[0].Err)
	}
	if upstream.Code != "rate_limit_exceeded" || upstream.Type != "rate_limit_error" || upstream.Message != "slow down" {
		t.Errorf("upstream error = %#v", upstream)
	}
}

func TestResponsesUnexpectedEOF(t *testing.T) {
	run := runResponses(t, Request{Model: "m", UserText: "x"}, func(w http.ResponseWriter) {
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		// No terminal event: the server closes the connection.
	})

	errors_ := run.eventsOf(EventError)
	if len(errors_) != 1 {
		t.Fatalf("error events = %d, want 1", len(errors_))
	}
	if !errors.Is(errors_[0].Err, ErrStreamClosed) {
		t.Errorf("error = %v, want ErrStreamClosed", errors_[0].Err)
	}
}

func TestResponsesLowerRequiresModel(t *testing.T) {
	if _, err := lowerResponses(Request{UserText: "x"}); err == nil {
		t.Fatal("expected model-required error")
	}
}
