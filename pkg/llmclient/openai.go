package llmclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// OpenAIClient talks to OpenAI Chat-Completions-compatible endpoints
// (including bigmodel.cn's `/api/coding/paas/v4` gateway).
type OpenAIClient struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// NewOpenAIClient constructs a client with sensible defaults.
func NewOpenAIClient(baseURL, apiKey string) *OpenAIClient {
	return &OpenAIClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		HTTPClient: httpClient,
	}
}

// SetHTTPClient replaces the default HTTP client (llmclient.HTTPClientCarrier).
func (c *OpenAIClient) SetHTTPClient(hc *http.Client) { c.HTTPClient = hc }

type openaiReq struct {
	Model            string          `json:"model"`
	Messages         []openaiMessage `json:"messages"`
	Stream           bool            `json:"stream"`
	MaxTokens        int             `json:"max_tokens,omitempty"`
	Tools            []any           `json:"tools,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	StreamOptions    *streamOptions  `json:"stream_options,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	Seed             *int            `json:"seed,omitempty"`
	Stop             []string        `json:"stop,omitempty"`
	ToolChoice       any             `json:"tool_choice,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openaiMessage struct {
	Role             string           `json:"role"`
	Content          json.RawMessage  `json:"content,omitempty"`
	ReasoningContent *string          `json:"reasoning_content,omitempty"`
	ToolCalls        []openaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string           `json:"tool_call_id,omitempty"`
	Name             string           `json:"name,omitempty"`
}

type openaiContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openaiImageURL `json:"image_url,omitempty"`
}

type openaiImageURL struct {
	URL string `json:"url"`
}

type openaiToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openaiSSE struct {
	ID      string         `json:"id,omitempty"`
	Choices []openaiChoice `json:"choices,omitempty"`
	Usage   *openaiUsage   `json:"usage,omitempty"`
}

type openaiChoice struct {
	Delta        openaiDelta `json:"delta,omitempty"`
	FinishReason *string     `json:"finish_reason,omitempty"`
}

type openaiDelta struct {
	Content          string           `json:"content,omitempty"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	// Reasoning is the same thinking content under the field name Ollama's
	// OpenAI-compatible API uses ("reasoning" instead of "reasoning_content").
	Reasoning string           `json:"reasoning,omitempty"`
	ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
}

type openaiUsage struct {
	PromptTokens            int                            `json:"prompt_tokens"`
	CompletionTokens        int                            `json:"completion_tokens"`
	TotalTokens             int                            `json:"total_tokens"`
	PromptTokensDetails     *openaiPromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *openaiCompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

type openaiPromptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

type openaiCompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Stream issues the request and returns a streaming reader.
//
// Self-heal: when a provider rejects the request with the thinking-mode
// replay error ("The `reasoning_content` in the thinking mode must be
// passed back to the API") and no explicit thinking signal was set, the
// model thinks by server default but was not detected by
// isOpenAIThinkingMode. Retry once with thinking explicitly enabled so
// the empty reasoning_content injection kicks in. Explicit thinking
// settings are never overridden, and detection-covered models never
// take this path (their first attempt already carried reasoning_content).
func (c *OpenAIClient) Stream(ctx context.Context, req Request) (Stream, error) {
	s, err := c.openStream(ctx, req)
	if err == nil {
		return s, nil
	}
	if !isReasoningReplayReject(req, err) {
		return nil, err
	}
	if req.ExtraBody == nil {
		req.ExtraBody = map[string]any{}
	}
	req.ExtraBody["thinking"] = map[string]any{"type": "enabled"}
	return c.openStream(ctx, req)
}

// isReasoningReplayReject reports whether err is the thinking-mode
// reasoning-replay 400 (DeepSeek family wording: reasoning_content /
// reasoning_text variants) AND the request had no explicit thinking
// signal (an explicit signal means the first attempt already injected
// reasoning_content, so a retry cannot fix the failure).
func isReasoningReplayReject(req Request, err error) bool {
	if isOpenAIThinkingMode(req) {
		return false
	}
	var ue *UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(ue.Message)
	return strings.Contains(msg, "must be passed back to the api") &&
		strings.Contains(msg, "reasoning")
}

func (c *OpenAIClient) openStream(ctx context.Context, req Request) (Stream, error) {
	payload, err := lowerOpenAIChat(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(payload))
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
		slog.Error("openai: request failed",
			"status", resp.StatusCode,
			"body", respSnippet,
			"payloadSize", len(payload),
		)
		return nil, UpstreamErrorFromResponse(resp, body)
	}

	s := &openaiStream{
		body:        resp.Body,
		events:      make(chan Event, 16),
		toolCallIDs: make(map[int]string),
		model:       req.Model,
		toolNames:   make(map[int]string),
		toolArgBuf:  make(map[int]string),
		// Always on: a leading inline think block leaked into content is
		// diverted to reasoning for every request, not only thinking-mode
		// ones — non-thinking models (e.g. MiniMax) leak it too.
		divertThink: true,
	}
	s.telemetry.setStart()
	s.telemetry.setIDs("", req.Model, "", resp.Header.Get("X-Request-Id"), resp.Header.Get("X-Response-Id"), resp.Header.Get("X-Client-Request-Id"))
	go s.run()
	return s, nil
}

// lowerOpenAIChat lowers a provider-agnostic Request into an OpenAI Chat
// Completions JSON body. It applies model-family defaults (Kimi, etc.),
// message sanitization, tool schema transforms, and thinking extra params
// before serializing.
func lowerOpenAIChat(req Request) ([]byte, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("openai: model is required")
	}

	applyModelFamilyDefaults(&req)
	sanitizeNonVisionImages(&req)

	thinking := isOpenAIThinkingMode(req)

	var messages []openaiMessage
	if len(req.Messages) > 0 {
		req.Messages = sanitizeOpenAIMessages(req.Messages)
		if req.System != "" {
			sysJSON, _ := json.Marshal(req.System)
			messages = append(messages, openaiMessage{Role: "system", Content: json.RawMessage(sysJSON)})
		}
		for _, m := range req.Messages {
			om := openaiMessage{Role: m.Role}
			switch m.Role {
			case RoleAssistant:
				var textParts []string
				var toolCalls []openaiToolCall
				for _, b := range m.Content {
					switch b.Type {
					case BlockTypeText:
						textParts = append(textParts, b.Text)
					case BlockTypeToolUse:
						toolCalls = append(toolCalls, openaiToolCall{
							ID:   b.ToolUseID,
							Type: "function",
							Function: openaiToolFunction{
								Name:      b.ToolName,
								Arguments: b.Input,
							},
						})
					}
				}
				if len(textParts) == 0 && len(toolCalls) == 0 {
					// An interrupted turn can leave a reasoning-only assistant step.
					// OpenAI-compatible APIs reject it because neither content nor
					// tool_calls is present.
					slog.Warn("openai: dropping empty assistant message", "index", len(messages))
					continue
				}
				if len(textParts) > 0 {
					om.Content, _ = json.Marshal(strings.Join(textParts, ""))
				} else {
					// Some providers (DeepSeek) reject assistant messages with
					// tool_calls but no content field at all.
					om.Content = json.RawMessage("null")
				}
				if len(toolCalls) > 0 {
					om.ToolCalls = toolCalls
				}
			// Reasoning content: use stored thinking text from history, or
			// inject empty string when thinking mode is active. DeepSeek and
			// other thinking-mode providers reject requests that omit
			// reasoning_content on any assistant message produced under
			// thinking mode.
			if m.ReasoningContent != "" {
				om.ReasoningContent = &m.ReasoningContent
			} else if thinking {
				empty := ""
				om.ReasoningContent = &empty
			}
			case "tool":
				for _, b := range m.Content {
					if b.Type == BlockTypeToolResult {
						om.Content, _ = json.Marshal(b.Text)
						om.ToolCallID = b.ToolUseID
						break
					}
				}
			default:
				var hasImage bool
				var parts []openaiContentPart
				var texts []string
				for _, b := range m.Content {
					switch b.Type {
					case BlockTypeText:
						texts = append(texts, b.Text)
					case BlockTypeImage:
						hasImage = true
						parts = append(parts, openaiContentPart{
							Type:     "image_url",
							ImageURL: &openaiImageURL{URL: b.ImageURL},
						})
					}
				}
				if len(texts) > 0 {
					if hasImage {
						parts = append([]openaiContentPart{{Type: "text", Text: strings.Join(texts, "")}}, parts...)
					} else {
						om.Content, _ = json.Marshal(strings.Join(texts, ""))
					}
				}
				if hasImage {
					om.Content, _ = json.Marshal(parts)
				}
			}
			messages = append(messages, om)
		}
	} else {
		if req.System != "" {
			sysJSON, _ := json.Marshal(req.System)
			messages = append(messages, openaiMessage{Role: "system", Content: json.RawMessage(sysJSON)})
		}
		userJSON, _ := json.Marshal(req.UserText)
		messages = append(messages, openaiMessage{Role: "user", Content: json.RawMessage(userJSON)})
	}

	var tools []any
	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			if t.Type != "" {
				native := map[string]any{"type": t.Type}
				if t.Name != "" {
					native["name"] = t.Name
				}
				for k, v := range t.NativeConfig {
					native[k] = v
				}
				tools = append(tools, native)
			} else {
			schema := stripNullSchemaValues(t.InputSchema)
			if modelFamily(req.Model) == familyKimi {
				schema = sanitizeMoonshotSchema(schema)
			}
				if schema == "" || !json.Valid([]byte(schema)) {
					schema = `{"type":"object"}`
				}
				tools = append(tools, openaiTool{
					Type: "function",
					Function: openaiToolFunctionDef{
						Name:        t.Name,
						Description: t.Description,
						Parameters:  json.RawMessage(schema),
					},
				})
			}
		}
	}

	body := openaiReq{
		Model:            req.Model,
		Messages:         messages,
		Stream:           true,
		StreamOptions:    &streamOptions{IncludeUsage: true},
		MaxTokens:        req.MaxTokens,
		Tools:            tools,
		ReasoningEffort:  req.ReasoningEffort,
		Temperature:      req.Temperature,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Seed:             req.Seed,
		Stop:             req.Stop,
	}

	if req.ToolChoice != "" {
		body.ToolChoice = parseToolChoice(req.ToolChoice)
	}

	// Merge provider-specific body extensions (e.g. Kimi chat_template_args)
	// supplied by the caller. The aggregator decides whether these apply based
	// on the actual provider configuration, not on model-name heuristics here.
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

	// Trace final message sequence for debugging tool_call/tool pairing issues.
	var seq []string
	for i, m := range messages {
		switch m.Role {
		case RoleAssistant:
			var tcIDs []string
			for _, tc := range m.ToolCalls {
				tcIDs = append(tcIDs, tc.ID)
			}
			if len(tcIDs) > 0 {
				seq = append(seq, fmt.Sprintf("[%d]assistant(tool_calls=%v)", i, tcIDs))
			} else {
				textLen := 0
				if m.Content != nil {
					textLen = len(m.Content)
				}
				seq = append(seq, fmt.Sprintf("[%d]assistant(textLen=%d)", i, textLen))
			}
		case RoleTool:
			seq = append(seq, fmt.Sprintf("[%d]tool(call_id=%s)", i, m.ToolCallID))
		default:
			seq = append(seq, fmt.Sprintf("[%d]%s", i, m.Role))
		}
	}
	slog.Debug("openai: message sequence", "model", req.Model, "sequence", seq)

	return json.Marshal(body)
}

// Model family constants for provider-specific transform logic.
const (
	familyUnknown  = ""
	familyKimi     = "kimi"
	familyDeepSeek = "deepseek"
)

// modelFamily returns the family name for a given model ID string.
func modelFamily(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "kimi") {
		return familyKimi
	}
	if strings.Contains(m, "deepseek") {
		return familyDeepSeek
	}
	return familyUnknown
}

// modelLacksVision reports whether the model is known to reject image content
// blocks on its chat-completions endpoint. The check is model-level, not
// family-level: OpenRouter exposes deepseek-v4.1-flash and the
// deepseek-v4-flash-vision-exp variant as text+image while the rest of the
// deepseek family (chat / reasoner / r1 / v3 / v4-flash / v4-pro) is text-only,
// so a whole-family match would strip images from a genuinely vision-capable
// model. Unknown/non-deepseek models default to vision-capable; an upstream
// "image input unsupported" 400 still corrects the record at runtime
// (aiaggregator marks the unit).
func modelLacksVision(model string) bool {
	m := strings.ToLower(model)
	if !strings.Contains(m, "deepseek") {
		return false
	}
	if strings.Contains(m, "vision") || strings.Contains(m, "v4.1") {
		return false
	}
	return true
}

// ModelSupportsImageInput reports whether the model is known — by a model-level
// name check — to accept image content blocks on its chat endpoint. Unknown
// families default to vision-capable; upstream "image input unsupported"
// errors correct the record at runtime (aiaggregator marks the unit).
func ModelSupportsImageInput(model string) bool {
	return !modelLacksVision(model)
}

// isOpenAIThinkingMode reports whether the request activates a provider's
// thinking / reasoning mode. When true, every assistant message in the
// history must carry a reasoning_content field — actual text or an empty
// string — because OpenAI-compatible providers (DeepSeek, Kimi) reject
// requests that omit it on a message produced under thinking mode with:
//
//	"The `reasoning_content` in the thinking mode must be passed back to the API."
//
// Detection covers three activation paths:
//   - OpenAI o-series: ReasoningEffort is set ("low"/"medium"/"high").
//   - DeepSeek R1/reasoner: ExtraBody carries {"thinking":{"type":"enabled"}}.
//   - Kimi k2.5/for-coding: ExtraBody carries chat_template_args.enable_thinking.
//   - Kimi k2-thinking: always-on, detected by model name.
//   - DeepSeek hybrid models (deepseek-v3.1/v3.2/v4 and the flash aliases):
//     thinking is ON by server default (mirrors aiaggregator's
//     isDeepSeekHybridModel); history replay must carry reasoning_content.
func isOpenAIThinkingMode(req Request) bool {
	if req.ReasoningEffort != "" {
		return true
	}
	if _, ok := req.ExtraBody["thinking"]; ok {
		return true
	}
	if args, ok := req.ExtraBody["chat_template_args"].(map[string]any); ok {
		if v, _ := args["enable_thinking"].(bool); v {
			return true
		}
	}
	m := strings.ToLower(req.Model)
	if strings.Contains(m, "thinking") {
		return true
	}
	if modelFamily(req.Model) == familyDeepSeek &&
		(strings.Contains(m, "deepseek-v") || strings.Contains(m, "flash")) {
		return true
	}
	return false
}

// sanitizeNonVisionImages rewrites image blocks as text placeholders when the
// target model does not accept image_url parts. Each image becomes a short
// "[image]" marker so the conversation still references the attachment.
func sanitizeNonVisionImages(req *Request) {
	if !modelLacksVision(req.Model) {
		return
	}
	for i := range req.Messages {
		msg := &req.Messages[i]
		for j := range msg.Content {
			if msg.Content[j].Type == BlockTypeImage {
				msg.Content[j] = Block{
					Type: BlockTypeText,
					Text: "[image]",
				}
			}
		}
	}
}

// applyModelFamilyDefaults sets provider-specific defaults on a Request
// before it is lowered to the wire format.
func applyModelFamilyDefaults(req *Request) {
	family := modelFamily(req.Model)
	switch family {
	case familyKimi:
		// Kimi K2 non-thinking default temperature: 0.6
		// Kimi thinking models (k2., k2p, k2-5, thinking, for-coding): 1.0
		if req.Temperature == nil {
			m := strings.ToLower(req.Model)
			isThinking := strings.Contains(m, "thinking") ||
				strings.Contains(m, "k2.") ||
				strings.Contains(m, "k2p") ||
				strings.Contains(m, "k2-5") ||
				strings.Contains(m, "for-coding")
			if isThinking {
				one := 1.0
				req.Temperature = &one
			} else {
				six := 0.6
				req.Temperature = &six
			}
		}
	}
}

// parseToolChoice decodes a ToolChoice string into the OpenAI wire format.
// Accepts: "auto", "none", "required", a JSON object like
// `{"type":"function","function":{"name":"toolName"}}`, or a bare tool name
// (forced single tool, normalized to the object form).
func parseToolChoice(choice string) any {
	switch choice {
	case "auto", "none", "required":
		return choice
	}
	// Try JSON object (e.g. {"type":"function","function":{"name":"search"}}).
	var obj any
	if err := json.Unmarshal([]byte(choice), &obj); err == nil {
		return obj
	}
	// Anything else is a tool name: force that exact tool.
	return map[string]any{"type": "function", "function": map[string]any{"name": choice}}
}

// sanitizeMoonshotSchema strips keywords that Moonshot AI's validator
// rejects: $ref siblings (description, etc.) and tuple-style items arrays.
func sanitizeMoonshotSchema(schema string) string {
	if schema == "" {
		return schema
	}
	var obj any
	if err := json.Unmarshal([]byte(schema), &obj); err != nil {
		return schema // return as-is on parse failure
	}
	sanitized := sanitizeMoonshotNode(obj)
	out, err := json.Marshal(sanitized)
	if err != nil {
		return schema
	}
	return string(out)
}

func sanitizeMoonshotNode(node any) any {
	if node == nil {
		return node
	}
	switch v := node.(type) {
	case map[string]any:
		// If the node has a $ref, drop all siblings — Moonshot validator
		// rejects properties like "description" next to "$ref".
		if _, hasRef := v["$ref"]; hasRef {
			return map[string]any{"$ref": v["$ref"]}
		}
		result := make(map[string]any, len(v))
		for k, val := range v {
			result[k] = sanitizeMoonshotNode(val)
		}
		// Moonshot does not support tuple-style items arrays; use first schema.
		if items, ok := result["items"]; ok {
			if arr, isArr := items.([]any); isArr && len(arr) > 0 {
				result["items"] = arr[0]
			}
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, val := range v {
			result[i] = sanitizeMoonshotNode(val)
		}
		return result
	default:
		return v
	}
}

type openaiTool struct {
	Type     string                `json:"type"`
	Function openaiToolFunctionDef `json:"function"`
}

type openaiToolFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openaiStream struct {
	body        io.ReadCloser
	events      chan Event
	toolCallIDs map[int]string
	model       string

	// toolNames remembers each streamed tool call's function name by index so
	// the terminal EventToolUseComplete (emitted on finish_reason
	// "tool_calls") can carry it — the OpenAI wire only sends the name once,
	// on the first delta.
	toolNames map[int]string

	// toolArgBuf accumulates argument fragments per tool index for debug logging.
	toolArgBuf map[int]string

	// finished is set when a finish_reason is received. The scan loop continues
	// after finishing so that a trailing usage chunk (sent separately when
	// stream_options.include_usage is set) is not lost. Without this, every
	// OpenAI-compatible provider that emits usage in its own post-finish chunk
	// would report zero token counts.
	finished bool

	// divertThink gates the inline-think diversion: when true, a leading
	// think block leaked into the content stream is routed as reasoning
	// instead of visible text. The filter re-arms after every tool call (each
	// tool call ends a text segment), so post-toolcall content diverts its
	// own leading block. It is enabled unconditionally (see the constructor)
	// because non-thinking models leak think blocks as well.
	divertThink bool
	think       leadingThinkDivert

	telemetry telemetryState
}

func (s *openaiStream) Events() <-chan Event { return s.events }
func (s *openaiStream) Close() error         { return s.body.Close() }
func (s *openaiStream) Telemetry() RequestTelemetry {
	s.telemetry.setComplete()
	return s.telemetry.snapshot()
}

// emitTextDelta routes a content delta through the leading-think diversion
// filter when active, splitting a leading think block into reasoning events;
// otherwise it emits the delta verbatim as a text event.
func (s *openaiStream) emitTextDelta(text string) {
	if !s.divertThink {
		s.events <- Event{Kind: EventTextDelta, Text: text}
		return
	}
	vis, reas := s.think.feed(text)
	if vis != "" {
		s.events <- Event{Kind: EventTextDelta, Text: vis}
	}
	if reas != "" {
		s.events <- Event{Kind: EventReasoningDelta, Text: reas}
	}
}

// flushThinkDivert releases any bytes the diversion filter still holds when the
// stream terminates (an unclosed leading think block, or a buffered partial tag
// prefix). It uses non-blocking sends so a consumer that has already stopped
// reading cannot deadlock the stream goroutine; at most a few bytes of
// end-of-stream residue are dropped in that case.
func (s *openaiStream) flushThinkDivert() {
	if !s.divertThink {
		return
	}
	vis, reas := s.think.flush()
	for _, ev := range []Event{
		{Kind: EventTextDelta, Text: vis},
		{Kind: EventReasoningDelta, Text: reas},
	} {
		if ev.Text == "" {
			continue
		}
		select {
		case s.events <- ev:
		default:
		}
	}
}

func (s *openaiStream) run() {
	defer close(s.events)
	defer s.body.Close()
	defer s.telemetry.setComplete()
	defer s.flushThinkDivert()

	scanner := bufio.NewScanner(s.body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data != "[DONE]" && (strings.Contains(data, "tool_calls") || strings.Contains(data, "function")) {
			slog.Debug("openai: raw SSE chunk", "model", s.model, "data", data)
		}
		if data == "[DONE]" {
			s.events <- Event{Kind: EventDone}
			return
		}
		stop, err := s.handleChunk(data)
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
	// If we already received a finish_reason, the server simply closed the
	// connection without [DONE]. That is a clean termination for providers
	// like Moonshot/Kimi; it must not be treated as an error.
	if s.finished {
		return
	}
	s.events <- Event{Kind: EventError, Err: ErrStreamClosed}
}

func (s *openaiStream) handleChunk(data string) (bool, error) {
	var chunk openaiSSE
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, fmt.Errorf("openai: decode chunk: %w", err)
	}

	if chunk.ID != "" {
		s.telemetry.setIDs("", "", "", "", chunk.ID, "")
	}

	if chunk.Usage != nil {
		s.telemetry.setUsage(convOpenAIUsage(chunk.Usage))
		s.events <- Event{Kind: EventUsage, Usage: convOpenAIUsage(chunk.Usage)}
	}

	for _, ch := range chunk.Choices {
		reasoning := ch.Delta.ReasoningContent
		if reasoning == "" {
			reasoning = ch.Delta.Reasoning
		}
		if reasoning != "" {
			s.telemetry.setFirstToken()
			s.events <- Event{Kind: EventReasoningDelta, Text: reasoning}
		}
		if ch.Delta.Content != "" {
			s.telemetry.setFirstToken()
			s.emitTextDelta(ch.Delta.Content)
		}
		if len(ch.Delta.ToolCalls) > 0 && s.divertThink {
			// A tool call ends the current text segment: re-arm the think
			// filter so content after it diverts a fresh leading block again.
			s.think.rearm()
		}
		for _, tc := range ch.Delta.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				s.telemetry.setFirstToken()
				s.toolCallIDs[tc.Index] = tc.ID
				s.toolNames[tc.Index] = tc.Function.Name
				s.events <- Event{Kind: EventToolUseStart, ToolUseID: tc.ID, ToolName: tc.Function.Name}
			}
			if tc.Function.Arguments != "" {
				toolID := s.toolCallIDs[tc.Index]
				if toolID == "" {
					toolID = tc.ID
				}
				s.toolArgBuf[tc.Index] += tc.Function.Arguments
				slog.Debug("openai: tool_call delta",
					"model", s.model, "index", tc.Index, "toolID", toolID,
					"deltaLen", len(tc.Function.Arguments), "cumLen", len(s.toolArgBuf[tc.Index]))
				s.events <- Event{Kind: EventToolUseInputDelta, ToolUseID: toolID, InputDelta: tc.Function.Arguments}
			}
		}
		if ch.FinishReason != nil {
			s.telemetry.setStopReason(normalizeStopReason(*ch.FinishReason))
			switch *ch.FinishReason {
			case "stop":
				s.events <- Event{Kind: EventDone}
				s.finished = true
				return false, nil
		case "tool_calls":
			// Complete each streamed tool call before the stop event, mirroring
			// the Anthropic client: consumers that assemble arguments from
			// input_delta chunks get the authoritative final input, and
			// terminal-only consumers (e.g. the plugin LLM bridge's ToolCalls)
			// see the calls at all. Emit in tool-index order so parallel calls
			// complete in the order the model produced them.
			indices := make([]int, 0, len(s.toolCallIDs))
			for idx := range s.toolCallIDs {
				indices = append(indices, idx)
			}
			sort.Ints(indices)
			for _, idx := range indices {
				id := s.toolCallIDs[idx]
				raw := s.toolArgBuf[idx]
				valid := json.Valid([]byte(raw))
				slog.Debug("openai: tool_call complete",
					"model", s.model, "index", idx, "toolID", id,
					"argsLen", len(raw), "jsonValid", valid,
					"args", func() string {
						if len(raw) > 500 {
							return raw[:500] + "..."
						}
						return raw
					}())
				input := raw
				if input == "" {
					input = "{}"
				}
				s.events <- Event{
					Kind:      EventToolUseComplete,
					ToolUseID: id,
					ToolName:  s.toolNames[idx],
					Input:     input,
				}
			}
			if len(s.toolCallIDs) > 0 {
					s.events <- Event{Kind: EventStop, StopReason: "tool_use"}
				} else {
					s.events <- Event{Kind: EventDone}
				}
				s.finished = true
				return false, nil
			}
		}
	}
	return false, nil
}

func convOpenAIUsage(u *openaiUsage) *Usage {
	if u == nil {
		return nil
	}
	usage := &Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
	if u.PromptTokensDetails != nil {
		usage.CacheReadInputTokens = u.PromptTokensDetails.CachedTokens
		usage.CacheCreationInputTokens = u.PromptTokensDetails.CacheWriteTokens
	}
	if u.CompletionTokensDetails != nil {
		usage.ReasoningTokens = u.CompletionTokensDetails.ReasoningTokens
	}
	// OpenAI semantics: input = prompt - cacheRead - cacheWrite. When the provider
	// reports all three, preserve that interpretation. When only prompt_tokens is
	// available, input equals prompt_tokens (legacy behavior).
	if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
		usage.InputTokens -= usage.CacheReadInputTokens + usage.CacheCreationInputTokens
		if usage.InputTokens < 0 {
			usage.InputTokens = 0
		}
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
	}
	return usage
}

// sanitizeOpenAIMessages drops orphaned tool messages (no preceding
// assistant+tool_calls) and trailing assistant messages with unresolved
// tool_calls. This is a last-resort safety net before the HTTP request.
func sanitizeOpenAIMessages(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if m.Role == "tool" {
			if prevHasToolCall(out, m) {
				out = append(out, m)
			} else {
				var droppedIDs []string
				for _, b := range m.Content {
					if b.Type == BlockTypeToolResult {
						droppedIDs = append(droppedIDs, b.ToolUseID)
					}
				}
				if len(droppedIDs) > 0 {
					slog.Warn("sanitizeOpenAIMessages: dropped orphaned tool message", "toolUseIDs", droppedIDs)
				}
			}
			continue
		}
		out = append(out, m)
	}

	// Reorder: pull matching tool results forward to sit immediately after their
	// assistant(tool_calls) message. This fixes scenarios where messages like a
	// cancel marker got interleaved between tool_use and its tool_result.
	for i := 0; i < len(out); i++ {
		if out[i].Role != RoleAssistant || !msgHasToolUses(out[i]) || msgNextIsToolResult(out, i) {
			continue
		}
		var matched []int
		for j := i + 1; j < len(out); j++ {
			if out[j].Role == "tool" && msgToolResultsMatch(out[i], out[j]) {
				matched = append(matched, j)
			}
		}
		if len(matched) == 0 {
			continue
		}
		toolResults := make([]Message, len(matched))
		for k := len(matched) - 1; k >= 0; k-- {
			idx := matched[k]
			toolResults[k] = out[idx]
			out = append(out[:idx], out[idx+1:]...)
		}
		insertIdx := i + 1
		out = append(out, make([]Message, len(toolResults))...)
		copy(out[insertIdx+len(toolResults):], out[insertIdx:])
		copy(out[insertIdx:], toolResults)
		i += len(toolResults)
	}

	// Strip trailing assistant messages with tool_calls but no result.
	for len(out) > 0 && out[len(out)-1].Role == RoleAssistant {
		hasTC := false
		for _, b := range out[len(out)-1].Content {
			if b.Type == BlockTypeToolUse {
				hasTC = true
				break
			}
		}
		if !hasTC {
			break
		}
		out = out[:len(out)-1]
	}

	// Strip non-trailing orphan tool_use blocks: an assistant(tool_use) that
	// is followed by anything other than a matching tool result. This handles
	// cancelled-turn artifacts (e.g., plan.submit emitted, then user cancelled
	// and a cancel-marker assistant was appended) where the orphan is no longer
	// the trailing message. Without this, providers reject the request with
	// HTTP 400 "tool_call_ids did not have response messages".
	for i := 0; i < len(out); {
		if out[i].Role != RoleAssistant || !msgHasToolUses(out[i]) {
			i++
			continue
		}
		covered := make(map[string]bool)
		j := i + 1
		for j < len(out) && out[j].Role == RoleTool {
			for _, b := range out[j].Content {
				if b.Type == BlockTypeToolResult && b.ToolUseID != "" {
					covered[b.ToolUseID] = true
				}
			}
			j++
		}
		var clean []Block
		hasOrphan := false
		for _, b := range out[i].Content {
			if b.Type == BlockTypeToolUse && b.ToolUseID != "" && !covered[b.ToolUseID] {
				hasOrphan = true
				continue
			}
			clean = append(clean, b)
		}
		if hasOrphan {
			if len(clean) == 0 {
				out = append(out[:i], out[j:]...)
				continue
			}
			out[i].Content = clean
		}
		i++
	}
	return out
}

func msgHasToolUses(m Message) bool {
	for _, b := range m.Content {
		if b.Type == BlockTypeToolUse {
			return true
		}
	}
	return false
}

func msgNextIsToolResult(msgs []Message, idx int) bool {
	return idx+1 < len(msgs) && msgs[idx+1].Role == "tool"
}

func msgToolResultsMatch(asst, msg Message) bool {
	for _, ab := range asst.Content {
		if ab.Type != BlockTypeToolUse {
			continue
		}
		for _, tb := range msg.Content {
			if tb.Type == BlockTypeToolResult && tb.ToolUseID == ab.ToolUseID {
				return true
			}
		}
	}
	return false
}

func prevHasToolCall(out []Message, m Message) bool {
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Role != RoleAssistant {
			continue
		}
		for _, b := range out[i].Content {
			if b.Type != BlockTypeToolUse {
				continue
			}
			for _, rb := range m.Content {
				if rb.Type == BlockTypeToolResult && rb.ToolUseID == b.ToolUseID {
					return true
				}
			}
		}
		// Keep searching earlier assistant messages — the tool result
		// may match an assistant further back, not the nearest one.
	}
	return false
}
