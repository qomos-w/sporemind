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

const anthropicVersion = "2023-06-01"

// AnthropicClient talks to Anthropic-compatible `/v1/messages` endpoints
// (including bigmodel.cn's Anthropic gateway).
type AnthropicClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewAnthropicClient constructs a client with sensible defaults.
// The endpoint may be supplied with or without the `/v1` suffix; both forms
// are normalised to the same request URL.
func NewAnthropicClient(baseURL, apiKey string) *AnthropicClient {
	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL = baseURL + "/v1"
	}
	return &AnthropicClient{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}
}

// SetHTTPClient replaces the default HTTP client (llmclient.HTTPClientCarrier).
func (c *AnthropicClient) SetHTTPClient(hc *http.Client) { c.HTTPClient = hc }

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type anthropicReq struct {
	Model      string                `json:"model"`
	System     json.RawMessage       `json:"system,omitempty"`
	Messages   []anthropicReqMessage `json:"messages"`
	Stream     bool                  `json:"stream"`
	MaxTokens  int                   `json:"max_tokens"`
	Tools      []any                 `json:"tools,omitempty"`
	ToolChoice any                   `json:"tool_choice,omitempty"`
	Thinking   *anthropicThinking    `json:"thinking,omitempty"`
}

type anthropicReqMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicContentBlock struct {
	Type         string                `json:"type"`
	Text         string                `json:"text,omitempty"`
	ID           string                `json:"id,omitempty"`
	Name         string                `json:"name,omitempty"`
	Input        json.RawMessage       `json:"input,omitempty"`
	ToolUseID    string                `json:"tool_use_id,omitempty"`
	Content      string                `json:"content,omitempty"`
	IsError      bool                  `json:"is_error,omitempty"`
	CacheControl any                   `json:"cache_control,omitempty"`
	Source       *anthropicImageSource `json:"source,omitempty"`
}

type anthropicSystemBlock struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	CacheControl any    `json:"cache_control,omitempty"`
}

// anthropicToolSpec is the wire shape for a tool in the Anthropic API.
// For standard function tools: name + description + input_schema.
// For provider-native tools (e.g. web_search): type + name + optional fields.
type anthropicToolSpec struct {
	Name         string          `json:"name,omitempty"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema,omitempty"`
	Type         string          `json:"type,omitempty"`
	CacheControl any             `json:"cache_control,omitempty"`
}

type anthropicSSE struct {
	Type         string                  `json:"type"`
	Index        int                     `json:"index,omitempty"`
	ContentBlock *anthropicSSEBlock      `json:"content_block,omitempty"`
	Delta        *anthropicDelta         `json:"delta,omitempty"`
	Usage        *anthropicUsage         `json:"usage,omitempty"`
	Message      *anthropicMessageHeader `json:"message,omitempty"`
	Error        *anthropicError         `json:"error,omitempty"`
}

type anthropicSSEBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Text  string          `json:"text,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthropicDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	StopReason  string `json:"stop_reason,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type anthropicMessageHeader struct {
	Usage *anthropicUsage `json:"usage,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Stream issues the request and returns a streaming reader.
func (c *AnthropicClient) Stream(ctx context.Context, req Request) (Stream, error) {
	payload, err := c.buildPayload(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	ua := req.UserAgent
	if ua == "" {
		ua = userAgent
	}
	httpReq.Header.Set("User-Agent", ua)
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	client := c.HTTPClient
	if client == nil {
		client = httpClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http request: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return nil, UpstreamErrorFromResponse(resp, body)
	}

	s := &anthropicStream{
		body:       resp.Body,
		events:     make(chan Event, 16),
		blockState: make(map[int]*anthropicBlockState),
	}
	s.telemetry.setStart()
	s.telemetry.setIDs("", req.Model, "", resp.Header.Get("X-Request-Id"), resp.Header.Get("X-Response-Id"), resp.Header.Get("Anthropic-Rq-Id"))
	go s.run()
	return s, nil
}

func (c *AnthropicClient) buildPayload(req Request) ([]byte, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = defaultMaxTokens
	}

	messages, err := buildAnthropicMessages(req)
	if err != nil {
		return nil, err
	}

	// Build system blocks: structured SystemBlocks take precedence over
	// the plain System string. The last block gets a cache_control marker.
	var systemRaw json.RawMessage
	if len(req.SystemBlocks) > 0 {
		blocks := make([]anthropicSystemBlock, len(req.SystemBlocks))
		for i, sb := range req.SystemBlocks {
			blocks[i] = anthropicSystemBlock{Type: sb.Type, Text: sb.Text}
		}
		if blocks[len(blocks)-1].CacheControl == nil {
			blocks[len(blocks)-1].CacheControl = cacheEphemeral
		}
		systemRaw, _ = json.Marshal(blocks)
	} else if req.System != "" {
		systemRaw, _ = json.Marshal([]anthropicSystemBlock{
			{Type: "text", Text: req.System},
		})
	}

	body := anthropicReq{
		Model:     req.Model,
		System:    systemRaw,
		Stream:    true,
		MaxTokens: maxTok,
		Messages:  messages,
	}
	if req.ThinkingBudget > 0 {
		body.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: req.ThinkingBudget}
	}

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for i, t := range req.Tools {
			if t.Type != "" {
				native := map[string]any{"type": t.Type}
				if t.Name != "" {
					native["name"] = t.Name
				}
				for k, v := range t.NativeConfig {
					native[k] = v
				}
				// Add cache_control on the last tool.
				if i == len(req.Tools)-1 || t.CacheControl == "ephemeral" {
					native["cache_control"] = cacheEphemeral
				}
				tools = append(tools, native)
			} else {
				schema := json.RawMessage(stripNullSchemaValues(t.InputSchema))
				if len(schema) == 0 {
					schema = json.RawMessage(`{"type":"object"}`)
				}
				ts := anthropicToolSpec{
					Name:        t.Name,
					Description: t.Description,
					InputSchema: schema,
				}
				// Add cache_control on the last tool or if explicitly set.
				if i == len(req.Tools)-1 || t.CacheControl == "ephemeral" {
					ts.CacheControl = cacheEphemeral
				}
				tools = append(tools, ts)
			}
		}
		body.Tools = tools
	}

	// Forced tool choice must survive a failover onto an Anthropic unit —
	// dropping it would silently let the model answer in text.
	if req.ToolChoice != "" {
		body.ToolChoice = anthropicToolChoice(req.ToolChoice)
	}

	return json.Marshal(body)
}

// anthropicToolChoice lowers the protocol-neutral ToolChoice string into
// Anthropic's wire form: "auto" → {"type":"auto"}, "required" → {"type":"any"},
// and a named function (bare name or the OpenAI object form) →
// {"type":"tool","name":…}. "none" has no Anthropic equivalent and degrades
// to the default auto. An already-Anthropic object passes through as-is.
func anthropicToolChoice(choice string) any {
	switch choice {
	case "auto", "none":
		return map[string]any{"type": "auto"}
	case "required":
		return map[string]any{"type": "any"}
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(choice), &obj); err == nil && obj.Type != "" {
		if obj.Type == "function" || obj.Type == "tool" {
			if name := obj.Function.Name; name != "" {
				return map[string]any{"type": "tool", "name": name}
			}
			if obj.Name != "" {
				return map[string]any{"type": "tool", "name": obj.Name}
			}
		}
		out := map[string]any{"type": obj.Type}
		if obj.Name != "" {
			out["name"] = obj.Name
		}
		return out
	}
	return map[string]any{"type": "tool", "name": choice}
}

// buildAnthropicMessages translates the protocol-neutral Request into
// Anthropic's content-block message array. If req.Messages is non-empty
// it is used verbatim; otherwise req.UserText is wrapped into a single
// user text block.
// parseDataURL parses a data URL (data:image/png;base64,...) into an anthropicImageSource.
// For non-data URLs (external http/https), it returns an anthropicImageSource with type "url".
func parseDataURL(url string) (*anthropicImageSource, error) {
	const prefix = "data:"
	if !strings.HasPrefix(url, prefix) {
		// External URL — Anthropic supports image URLs directly.
		return &anthropicImageSource{Type: "url", MediaType: "", Data: url}, nil
	}
	// data:[<mediatype>][;base64],<data>
	afterData := url[len(prefix):]
	commaIdx := strings.Index(afterData, ",")
	if commaIdx < 0 {
		return nil, fmt.Errorf("invalid data URL: missing comma")
	}
	meta := afterData[:commaIdx]
	data := afterData[commaIdx+1:]
	mediaType := ""
	if strings.HasSuffix(meta, ";base64") {
		mediaType = strings.TrimSuffix(meta, ";base64")
	} else if meta != "" {
		mediaType = meta
	}
	return &anthropicImageSource{Type: "base64", MediaType: mediaType, Data: data}, nil
}

func buildAnthropicMessages(req Request) ([]anthropicReqMessage, error) {
	if len(req.Messages) == 0 {
		return []anthropicReqMessage{{
			Role:    "user",
			Content: []anthropicContentBlock{{Type: "text", Text: req.UserText}},
		}}, nil
	}

	out := make([]anthropicReqMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		blocks := make([]anthropicContentBlock, 0, len(m.Content))
		for _, b := range m.Content {
			var cc any
			if b.CacheControl == "ephemeral" {
				cc = cacheEphemeral
			}
			switch b.Type {
			case BlockTypeText:
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: b.Text, CacheControl: cc})
			case BlockTypeToolUse:
				input := sanitizeRawJSON(b.Input)
				blocks = append(blocks, anthropicContentBlock{
					Type:         "tool_use",
					ID:           b.ToolUseID,
					Name:         b.ToolName,
					Input:        input,
					CacheControl: cc,
				})
			case BlockTypeToolResult:
				blocks = append(blocks, anthropicContentBlock{
					Type:         "tool_result",
					ToolUseID:    b.ToolUseID,
					Content:      b.Text,
					IsError:      b.IsError,
					CacheControl: cc,
				})
			case BlockTypeImage:
				imgSource, err := parseDataURL(b.ImageURL)
				if err != nil {
					return nil, fmt.Errorf("anthropic: invalid image URL: %w", err)
				}
				blocks = append(blocks, anthropicContentBlock{
					Type:         "image",
					Source:       imgSource,
					CacheControl: cc,
				})
			default:
				return nil, fmt.Errorf("anthropic: unknown content block type %q", b.Type)
			}
		}
		// Anthropic has no "tool" role — tool_result blocks must live in a
		// "user" message. Remap to keep the API happy.
		role := m.Role
		if role == RoleTool {
			role = RoleUser
		}
		// Anthropic rejects assistant messages with empty content. Skip them
		// rather than letting the whole request fail; upstream should not emit
		// empty assistant messages, so this is a defensive last resort.
		if role == RoleAssistant && len(blocks) == 0 {
			slog.Warn("anthropic: dropping empty assistant message", "index", len(out))
			continue
		}
		out = append(out, anthropicReqMessage{Role: role, Content: blocks})
	}
	return out, nil
}

type anthropicBlockState struct {
	blockType string
	toolUseID string
	toolName  string
	inputBuf  strings.Builder
}

type anthropicStream struct {
	body       io.ReadCloser
	events     chan Event
	blockState map[int]*anthropicBlockState

	telemetry telemetryState
}

func (s *anthropicStream) Events() <-chan Event { return s.events }
func (s *anthropicStream) Close() error         { return s.body.Close() }
func (s *anthropicStream) Telemetry() RequestTelemetry {
	s.telemetry.setComplete()
	return s.telemetry.snapshot()
}

func (s *anthropicStream) run() {
	defer close(s.events)
	defer s.body.Close()
	defer s.telemetry.setComplete()

	scanner := bufio.NewScanner(s.body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			eventType = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			s.events <- Event{Kind: EventDone}
			return
		}
		stop, err := s.handleData(eventType, data)
		if err != nil {
			s.events <- Event{Kind: EventError, Err: err}
			return
		}
		if stop {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		s.events <- Event{Kind: EventError, Err: err}
		return
	}
	s.events <- Event{Kind: EventError, Err: ErrStreamClosed}
}

func (s *anthropicStream) handleData(eventType, data string) (bool, error) {
	var env anthropicSSE
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		return false, fmt.Errorf("anthropic: decode event: %w", err)
	}
	typeName := env.Type
	if typeName == "" {
		typeName = eventType
	}
	switch typeName {
	case "message_start":
		if env.Message != nil && env.Message.Usage != nil {
			s.telemetry.setUsage(convAnthropicUsage(env.Message.Usage))
			s.events <- Event{Kind: EventUsage, Usage: convAnthropicUsage(env.Message.Usage)}
		}
	case "content_block_start":
		if env.ContentBlock == nil {
			return false, nil
		}
		bs := &anthropicBlockState{blockType: env.ContentBlock.Type}
		if env.ContentBlock.Type == "tool_use" {
			bs.toolUseID = env.ContentBlock.ID
			bs.toolName = env.ContentBlock.Name
			s.telemetry.setFirstToken()
			s.events <- Event{
				Kind:      EventToolUseStart,
				ToolUseID: bs.toolUseID,
				ToolName:  bs.toolName,
			}
		}
		s.blockState[env.Index] = bs
	case "content_block_delta":
		if env.Delta == nil {
			return false, nil
		}
		switch env.Delta.Type {
		case "text_delta":
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventTextDelta, Text: env.Delta.Text}
		case "thinking_delta":
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventReasoningDelta, Text: env.Delta.Thinking}
		case "input_json_delta":
			bs := s.blockState[env.Index]
			if bs == nil {
				return false, nil
			}
			bs.inputBuf.WriteString(env.Delta.PartialJSON)
			s.events <- Event{
				Kind:       EventToolUseInputDelta,
				ToolUseID:  bs.toolUseID,
				InputDelta: env.Delta.PartialJSON,
			}
		}
	case "content_block_stop":
		bs := s.blockState[env.Index]
		if bs != nil && bs.blockType == "tool_use" {
			input := bs.inputBuf.String()
			if input == "" {
				input = "{}"
			}
			s.events <- Event{
				Kind:      EventToolUseComplete,
				ToolUseID: bs.toolUseID,
				ToolName:  bs.toolName,
				Input:     input,
			}
		}
		delete(s.blockState, env.Index)
	case "message_delta":
		if env.Usage != nil {
			s.telemetry.setUsage(convAnthropicUsage(env.Usage))
			s.events <- Event{Kind: EventUsage, Usage: convAnthropicUsage(env.Usage)}
		}
		if env.Delta != nil && env.Delta.StopReason != "" {
			stop := normalizeStopReason(env.Delta.StopReason)
			s.telemetry.setStopReason(stop)
			s.events <- Event{Kind: EventStop, StopReason: stop}
		}
	case "message_stop":
		s.events <- Event{Kind: EventDone}
		return true, nil
	case "error":
		if env.Error != nil {
			return false, fmt.Errorf("anthropic: %s", env.Error.Message)
		}
		return false, fmt.Errorf("anthropic: error event without payload")
	}
	return false, nil
}

func convAnthropicUsage(u *anthropicUsage) *Usage {
	if u == nil {
		return nil
	}
	total := u.InputTokens + u.OutputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
	return &Usage{
		InputTokens:              u.InputTokens,
		OutputTokens:             u.OutputTokens,
		TotalTokens:              total,
		CacheCreationInputTokens: u.CacheCreationInputTokens,
		CacheReadInputTokens:     u.CacheReadInputTokens,
	}
}

// sanitizeRawJSON 把可能畸形的 JSON 字符串转为合法的 json.RawMessage。
// LLM 偶尔会生成不完整的 tool_use input，直接 cast 为 json.RawMessage
// 会在序列化时触发 "invalid character '}' after top-level value" 错误。
func sanitizeRawJSON(s string) json.RawMessage {
	if s == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	// 畸形 JSON：退回为字符串形式，避免破坏整个请求的序列化。
	b, _ := json.Marshal(s)
	return json.RawMessage(b)
}
