package llmclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// ResponsesClient talks to OpenAI Responses-API-compatible endpoints
// (POST {base}/responses). It reuses the package's Client/Stream/Event
// vocabulary so callers treat it exactly like OpenAIClient.
type ResponsesClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewResponsesClient constructs a client with sensible defaults.
func NewResponsesClient(baseURL, apiKey string) *ResponsesClient {
	return &ResponsesClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}
}

// SetHTTPClient replaces the default HTTP client (llmclient.HTTPClientCarrier).
func (c *ResponsesClient) SetHTTPClient(hc *http.Client) { c.HTTPClient = hc }

// responsesReq is the Responses API request body. Note the flat tool shape
// (no nested "function" object) and the instructions/input split instead of
// a chat "messages" array.
type responsesReq struct {
	Model           string               `json:"model"`
	Instructions    string               `json:"instructions,omitempty"`
	Stream          bool                 `json:"stream"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	Input           []responsesInputItem `json:"input,omitempty"`
	Tools           []any                `json:"tools,omitempty"`
	ToolChoice      any                  `json:"tool_choice,omitempty"`
	Reasoning       *responsesReasoning  `json:"reasoning,omitempty"`
	Temperature     *float64             `json:"temperature,omitempty"`
	TopP            *float64             `json:"top_p,omitempty"`
}

// responsesInputItem is one entry of the Responses API "input" array. All
// fields except Type are omitempty so a single struct covers message,
// function_call, function_call_output, and reasoning items.
type responsesInputItem struct {
	Type      string                 `json:"type"`
	Role      string                 `json:"role,omitempty"`
	Content   []responsesContentPart `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output    string                 `json:"output,omitempty"`
}

type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesReasoning struct {
	Effort string `json:"effort,omitempty"`
}

// Stream issues a request and returns a streaming reader.
func (c *ResponsesClient) Stream(ctx context.Context, req Request) (Stream, error) {
	payload, err := lowerResponses(req)
	if err != nil {
		return nil, err
	}

	baseURL := strings.TrimRight(c.BaseURL, "/")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	ua := req.UserAgent
	if ua == "" {
		ua = userAgent
	}
	httpReq.Header.Set("User-Agent", ua)

	client := c.HTTPClient
	if client == nil {
		client = httpClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		respSnippet := strings.TrimSpace(string(body))
		if len(respSnippet) > 512 {
			respSnippet = respSnippet[:512] + "...(truncated)"
		}
		slog.Error("responses: request failed",
			"status", resp.StatusCode,
			"body", respSnippet,
			"payloadSize", len(payload),
		)
		return nil, UpstreamErrorFromResponse(resp, body)
	}

	s := &responsesStream{
		body:      resp.Body,
		events:    make(chan Event, 16),
		toolCalls: make(map[string]string),
		argsBuf:   make(map[string]string),
		completed: make(map[string]bool),
	}
	s.telemetry.setStart()
	s.telemetry.setIDs("", req.Model, "", resp.Header.Get("X-Request-Id"), resp.Header.Get("X-Response-Id"), resp.Header.Get("X-Client-Request-Id"))
	go s.run()
	return s, nil
}

// lowerResponses lowers a provider-agnostic Request into an OpenAI Responses
// API JSON body. Message history reuses the same sanitization as the chat
// completions adapter; tools use the flat Responses shape.
func lowerResponses(req Request) ([]byte, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("responses: model is required")
	}

	body := responsesReq{
		Model:           req.Model,
		Instructions:    buildResponsesInstructions(req),
		Stream:          true,
		MaxOutputTokens: req.MaxTokens,
		Temperature:     req.Temperature,
		TopP:            req.TopP,
	}

	var input []responsesInputItem
	if len(req.Messages) > 0 {
		req.Messages = sanitizeOpenAIMessages(req.Messages)
		for _, m := range req.Messages {
			switch m.Role {
			case RoleAssistant:
				input = append(input, lowerResponsesAssistant(m)...)
			case RoleTool:
				for _, b := range m.Content {
					if b.Type == BlockTypeToolResult {
						input = append(input, responsesInputItem{
							Type:   "function_call_output",
							CallID: b.ToolUseID,
							Output: b.Text,
						})
					}
				}
			default:
				input = append(input, lowerResponsesUser(m)...)
			}
		}
	} else if req.UserText != "" {
		input = append(input, responsesInputItem{
			Type: "message",
			Role: RoleUser,
			Content: []responsesContentPart{{
				Type: "input_text",
				Text: req.UserText,
			}},
		})
	}
	body.Input = input

	if len(req.Tools) > 0 {
		body.Tools = lowerResponsesTools(req.Tools)
	}

	if req.ToolChoice != "" {
		body.ToolChoice = lowerResponsesToolChoice(req.ToolChoice)
	}

	if req.ReasoningEffort != "" {
		body.Reasoning = &responsesReasoning{Effort: req.ReasoningEffort}
	}

	// Merge provider-specific request body extensions, mirroring the chat
	// completions adapter.
	if len(req.ExtraBody) > 0 {
		base, _ := json.Marshal(body)
		var bodyMap map[string]any
		if err := json.Unmarshal(base, &bodyMap); err == nil {
			for k, v := range req.ExtraBody {
				bodyMap[k] = v
			}
			return json.Marshal(bodyMap)
		}
	}

	return json.Marshal(body)
}

// buildResponsesInstructions renders the system prompt as a single string:
// structured SystemBlocks take precedence over the plain System field.
func buildResponsesInstructions(req Request) string {
	if len(req.SystemBlocks) > 0 {
		texts := make([]string, 0, len(req.SystemBlocks))
		for _, sb := range req.SystemBlocks {
			if sb.Text != "" {
				texts = append(texts, sb.Text)
			}
		}
		return strings.Join(texts, "\n\n")
	}
	return req.System
}

// lowerResponsesAssistant converts one assistant history message into the
// lowerResponsesAssistant converts one assistant history message into output items.
func lowerResponsesAssistant(m Message) []responsesInputItem {
	var items []responsesInputItem

	var textParts []string
	var callItems []responsesInputItem
	for _, b := range m.Content {
		switch b.Type {
		case BlockTypeText:
			textParts = append(textParts, b.Text)
		case BlockTypeToolUse:
			args := b.Input
			if args == "" {
				args = "{}"
			}
			callItems = append(callItems, responsesInputItem{
				Type:      "function_call",
				CallID:    b.ToolUseID,
				Name:      b.ToolName,
				Arguments: args,
			})
		}
	}

	if len(textParts) == 0 && len(callItems) == 0 && len(items) == 0 {
		slog.Warn("responses: dropping empty assistant message")
		return nil
	}
	if len(textParts) > 0 {
		items = append(items, responsesInputItem{
			Type: "message",
			Role: RoleAssistant,
			Content: []responsesContentPart{{
				Type: "output_text",
				Text: strings.Join(textParts, ""),
			}},
		})
	}
	return append(items, callItems...)
}

// lowerResponsesUser converts one user history message into a message item
// with input_text/input_image content parts. A message with no recognized
// content blocks yields no item.
func lowerResponsesUser(m Message) []responsesInputItem {
	var parts []responsesContentPart
	for _, b := range m.Content {
		switch b.Type {
		case BlockTypeText:
			parts = append(parts, responsesContentPart{Type: "input_text", Text: b.Text})
		case BlockTypeImage:
			parts = append(parts, responsesContentPart{Type: "input_image", ImageURL: b.ImageURL})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return []responsesInputItem{{Type: "message", Role: RoleUser, Content: parts}}
}

// lowerResponsesTools converts ToolSpecs into the flat Responses tool array.
// Standard function tools get {type,name,description,parameters}; provider
// native tools (Type != "") are passed through as flat maps.
func lowerResponsesTools(specs []ToolSpec) []any {
	tools := make([]any, 0, len(specs))
	for _, t := range specs {
		if t.Type != "" {
			native := map[string]any{"type": t.Type}
			if t.Name != "" {
				native["name"] = t.Name
			}
			for k, v := range t.NativeConfig {
				native[k] = v
			}
			tools = append(tools, native)
			continue
		}
		schema := stripNullSchemaValues(t.InputSchema)
		if schema == "" || !json.Valid([]byte(schema)) {
			schema = `{"type":"object"}`
		}
		tools = append(tools, responsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  json.RawMessage(schema),
		})
	}
	return tools
}

// lowerResponsesToolChoice maps a ToolChoice string onto the Responses API
// wire form: "auto"|"none"|"required" or {"type":"function","name":"…"}.
// The OpenAI-chat nested form {"type":"function","function":{"name":"…"}}
// is flattened to the Responses form; a bare tool name forces that tool.
func lowerResponsesToolChoice(choice string) any {
	switch choice {
	case "auto", "none", "required":
		return choice
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(choice), &obj); err != nil {
		return map[string]any{"type": "function", "name": choice}
	}
	if fn, ok := obj["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok && name != "" {
			return map[string]any{"type": "function", "name": name}
		}
	}
	return obj
}

// ---------------------------------------------------------------------------
// SSE stream
// ---------------------------------------------------------------------------

type responsesStream struct {
	body   io.ReadCloser
	events chan Event

	// toolCalls maps call_id → tool name; argsBuf accumulates argument JSON
	// fragments per call_id; completed guards against emitting
	// EventToolUseComplete twice for the same call (arguments.done and
	// output_item.done can both carry the assembled arguments).
	toolCalls    map[string]string
	argsBuf      map[string]string
	completed    map[string]bool
	hasToolCalls bool

	telemetry telemetryState
}

func (s *responsesStream) Events() <-chan Event { return s.events }
func (s *responsesStream) Close() error         { return s.body.Close() }
func (s *responsesStream) Telemetry() RequestTelemetry {
	s.telemetry.setComplete()
	return s.telemetry.snapshot()
}

func (s *responsesStream) run() {
	defer close(s.events)
	defer s.body.Close()
	defer s.telemetry.setComplete()

	scanner := bufio.NewScanner(s.body)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// The Responses API emits both "event:" and "data:" lines; the type is
		// always present inside the data JSON, so parsing data lines only is
		// sufficient (and matches the chat-completions SSE format).
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				return
			}
			continue
		}
		stop, err := s.handleEvent(data)
		if err != nil {
			s.telemetry.setError(err)
			s.events <- Event{Kind: EventError, Err: err}
			return
		}
		if stop {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		s.telemetry.setError(err)
		s.events <- Event{Kind: EventError, Err: err}
		return
	}
	// The server closed without a terminal event (completed/failed/error);
	// treat it as an unexpected cut like the chat-completions adapter does.
	s.events <- Event{Kind: EventError, Err: ErrStreamClosed}
}

// responsesSSEEvent is the union of all Responses API stream events; the Type
// field discriminates them.
type responsesSSEEvent struct {
	Type      string                `json:"type"`
	Delta     string                `json:"delta,omitempty"`
	Text      string                `json:"text,omitempty"`
	Arguments string                `json:"arguments,omitempty"`
	CallID    string                `json:"call_id,omitempty"`
	ItemID    string                `json:"item_id,omitempty"`
	Item      *responsesOutputItem  `json:"item,omitempty"`
	Response  *responsesResponse    `json:"response,omitempty"`
	Error     *responsesErrorDetail `json:"error,omitempty"`
}

type responsesOutputItem struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	Status    string `json:"status,omitempty"`
}

type responsesResponse struct {
	ID                string                     `json:"id,omitempty"`
	Status            string                     `json:"status,omitempty"`
	Usage             *responsesUsage            `json:"usage,omitempty"`
	Error             *responsesErrorDetail      `json:"error,omitempty"`
	IncompleteDetails *responsesIncompleteDetail `json:"incomplete_details,omitempty"`
}

type responsesErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

type responsesIncompleteDetail struct {
	Reason string `json:"reason"`
}

type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details,omitempty"`
	OutputTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details,omitempty"`
}

// handleEvent processes one Responses API stream event and reports whether
// the stream should terminate (terminal event seen).
func (s *responsesStream) handleEvent(data string) (bool, error) {
	var ev responsesSSEEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return false, fmt.Errorf("responses: decode event: %w", err)
	}

	switch ev.Type {
	case "response.created":
		if ev.Response != nil {
			s.setResponseID(ev.Response.ID)
		}
	case "response.output_item.added":
		if ev.Item == nil || ev.Item.Type != "function_call" {
			return false, nil
		}
		callID := ev.Item.CallID
		if callID == "" {
			callID = ev.Item.ID
		}
		if callID == "" {
			return false, nil
		}
		s.hasToolCalls = true
		s.toolCalls[callID] = ev.Item.Name
		s.telemetry.setFirstToken()
		s.events <- Event{Kind: EventToolUseStart, ToolUseID: callID, ToolName: ev.Item.Name}
	case "response.output_text.delta":
		if delta := ev.Delta; delta != "" {
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventTextDelta, Text: delta}
		} else if ev.Text != "" {
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventTextDelta, Text: ev.Text}
		}
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if delta := ev.Delta; delta != "" {
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventReasoningDelta, Text: delta}
		} else if ev.Text != "" {
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventReasoningDelta, Text: ev.Text}
		}
	case "response.function_call_arguments.delta":
		if ev.Delta == "" {
			return false, nil
		}
		callID := ev.CallID
		if callID == "" {
			callID = ev.ItemID
		}
		if callID == "" {
			return false, nil
		}
		s.hasToolCalls = true
		s.argsBuf[callID] += ev.Delta
		s.telemetry.setFirstToken()
		s.events <- Event{Kind: EventToolUseInputDelta, ToolUseID: callID, InputDelta: ev.Delta}
	case "response.function_call_arguments.done":
		callID := ev.CallID
		if callID == "" {
			callID = ev.ItemID
		}
		if callID == "" {
			return false, nil
		}
		args := ev.Arguments
		if args == "" {
			args = s.argsBuf[callID]
		}
		s.completeToolCall(callID, args)
	case "response.output_item.done":
		// Wrap-up. A function_call item may carry the fully assembled
		// arguments when no function_call_arguments.done was emitted.
		if ev.Item != nil && ev.Item.Type == "function_call" {
			callID := ev.Item.CallID
			if callID == "" {
				callID = ev.Item.ID
			}
			if callID != "" {
				args := ev.Item.Arguments
				if args == "" {
					args = s.argsBuf[callID]
				}
				s.completeToolCall(callID, args)
			}
		}
	case "response.output_text.done":
		// Wrap-up; deltas already streamed the text.
	case "response.completed":
		if ev.Response != nil {
			s.setResponseID(ev.Response.ID)
			if ev.Response.Usage != nil {
				usage := convResponsesUsage(ev.Response.Usage)
				s.telemetry.setUsage(usage)
				s.events <- Event{Kind: EventUsage, Usage: usage}
			}
		}
		stopReason := "end_turn"
		if s.hasToolCalls {
			stopReason = "tool_use"
		}
		s.telemetry.setStopReason(normalizeStopReason(stopReason))
		s.events <- Event{Kind: EventStop, StopReason: stopReason}
		s.events <- Event{Kind: EventDone}
		return true, nil
	case "response.failed":
		err := s.failedError(ev)
		s.telemetry.setError(err)
		s.events <- Event{Kind: EventError, Err: err}
		return true, nil
	case "response.incomplete":
		err := s.incompleteError(ev)
		s.telemetry.setError(err)
		s.events <- Event{Kind: EventError, Err: err}
		return true, nil
	case "error":
		err := s.streamError(ev)
		s.telemetry.setError(err)
		s.events <- Event{Kind: EventError, Err: err}
		return true, nil
	}
	return false, nil
}

// completeToolCall emits EventToolUseComplete once per call_id, defaulting an
// empty argument payload to "{}" (mirroring the Anthropic adapter).
func (s *responsesStream) completeToolCall(callID, args string) {
	if callID == "" || s.completed[callID] {
		return
	}
	s.completed[callID] = true
	s.hasToolCalls = true
	if args == "" {
		args = "{}"
	}
	s.events <- Event{
		Kind:      EventToolUseComplete,
		ToolUseID: callID,
		ToolName:  s.toolCalls[callID],
		Input:     args,
	}
}

func (s *responsesStream) setResponseID(id string) {
	if id == "" {
		return
	}
	s.telemetry.mu.Lock()
	defer s.telemetry.mu.Unlock()
	s.telemetry.responseID = id
}

func (s *responsesStream) failedError(ev responsesSSEEvent) error {
	if ev.Response != nil && ev.Response.Error != nil {
		return &UpstreamError{
			Code:    ev.Response.Error.Code,
			Type:    "response_failed",
			Message: ev.Response.Error.Message,
		}
	}
	return fmt.Errorf("responses: response failed")
}

func (s *responsesStream) incompleteError(ev responsesSSEEvent) error {
	msg := "responses: response incomplete"
	if ev.Response != nil && ev.Response.IncompleteDetails != nil && ev.Response.IncompleteDetails.Reason != "" {
		msg = fmt.Sprintf("%s: %s", msg, ev.Response.IncompleteDetails.Reason)
	}
	return &UpstreamError{Type: "response_incomplete", Message: msg}
}

func (s *responsesStream) streamError(ev responsesSSEEvent) error {
	if ev.Error != nil {
		return &UpstreamError{
			Code:    ev.Error.Code,
			Type:    ev.Error.Type,
			Message: ev.Error.Message,
		}
	}
	return fmt.Errorf("responses: stream error")
}

// convResponsesUsage maps the Responses API usage object onto the canonical
// Usage struct. Responses input_tokens includes cached tokens; the canonical
// InputTokens field excludes them (same interpretation as convOpenAIUsage).
func convResponsesUsage(u *responsesUsage) *Usage {
	if u == nil {
		return nil
	}
	usage := &Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.InputTokensDetails != nil {
		usage.CacheReadInputTokens = u.InputTokensDetails.CachedTokens
	}
	if u.OutputTokensDetails != nil {
		usage.ReasoningTokens = u.OutputTokensDetails.ReasoningTokens
	}
	if usage.CacheReadInputTokens > 0 {
		usage.InputTokens -= usage.CacheReadInputTokens
		if usage.InputTokens < 0 {
			usage.InputTokens = 0
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens
	}
	return usage
}
