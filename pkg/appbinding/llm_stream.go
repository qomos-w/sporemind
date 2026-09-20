package appbinding

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// init registers the llm.* host-call streaming routes via the typed
// registration so the backing chunk type (domain.AggregatorChunk — what
// aiaggregator declared with actor.Streaming[T]()) and the SDK terminal type
// (LLMResp) are checked here at compile time and stamped onto the route for
// the contract tests. This package is the single home of the llm.* host-call
// binding (aliases live here too), so the SDK→aiaggregator translation —
// payload adaptation, chunk encoding, and delta aggregation — lives beside
// them. The host bridge looks the route up in the catalog and never imports
// the aggregator's domain types.
func init() {
	route := StreamRoute{
		Service:             "aiaggregator",
		Callable:            "aiaggregator.dispatch",
		AdaptReq:            adaptLLMReq,
		InjectCallerContext: true,
		// LLM generation legitimately runs minutes (chapter-length outputs).
		// Without a route budget, HTTP-data-path reverse calls fall back to
		// the generic 30s host-bridge cap and long generations die with
		// "context deadline exceeded" while the model is still streaming.
		// 5min matches the appdef generator's default timeout_ms for LLM
		// callables (300000) on the agent-invoke path.
		Budget: 5 * time.Minute,
	}
	RegisterTypedStreamRoute[domain.AggregatorChunk, LLMResp]("llm.complete", route, encodeAggregatorChunk, func() TypedStreamAggregator[domain.AggregatorChunk, LLMResp] { return &llmAggregator{} })
	RegisterTypedStreamRoute[domain.AggregatorChunk, LLMResp]("llm.chat", route, encodeAggregatorChunk, func() TypedStreamAggregator[domain.AggregatorChunk, LLMResp] { return &llmAggregator{} })
}

// llmChunkKindTextData is the data payload shape for text-bearing chunks:
// one string delta per chunk.
type llmTextChunkData struct {
	Text string `json:"text"`
}

// llmToolChunkData is the data payload shape for tool-bearing chunks:
// tool_use_start carries the id+name, tool_use_input_delta the argument
// fragment, tool_use_complete the joined input JSON.
type llmToolChunkData struct {
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	InputDelta string          `json:"input_delta,omitempty"`
}

// llmStopChunkData is the data payload for the stop chunk.
type llmStopChunkData struct {
	StopReason string `json:"stop_reason,omitempty"`
}

// encodeAggregatorChunk adapts one domain.AggregatorChunk (already typed by
// the generic route wrapper — no re-assertion here) into the generic
// envelope. Text and reasoning deltas map to {"text": "..."} data; the usage
// chunk carries the usage object itself as data; tool chunks carry
// {tool_use_id, name, input|input_delta} so streaming consumers can follow a
// forced-tool-call loop; the stop chunk carries its stop_reason (e.g.
// "tool_use" when the model ended on a tool call). Unknown kinds relay
// verbatim with the kind preserved and empty data — forward compatibility
// with new aggregator chunk kinds (the plugin sees the kind and decides).
func encodeAggregatorChunk(chunk domain.AggregatorChunk) (StreamChunkEnvelope, error) {
	switch chunk.Kind {
	case domain.AggregatorChunkText, domain.AggregatorChunkReasoning:
		data, err := json.Marshal(llmTextChunkData{Text: chunk.Text})
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal %s: %w", chunk.Kind, err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	case domain.AggregatorChunkUsage:
		if chunk.Usage == nil {
			return StreamChunkEnvelope{Kind: chunk.Kind}, nil
		}
		data, err := json.Marshal(chunk.Usage)
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal usage: %w", err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	case domain.AggregatorChunkToolUseStart:
		data, err := json.Marshal(llmToolChunkData{ToolUseID: chunk.ToolUseID, Name: chunk.ToolName})
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal %s: %w", chunk.Kind, err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	case domain.AggregatorChunkToolUseInputDelta:
		data, err := json.Marshal(llmToolChunkData{ToolUseID: chunk.ToolUseID, InputDelta: chunk.InputDelta})
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal %s: %w", chunk.Kind, err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	case domain.AggregatorChunkToolUseComplete:
		data, err := json.Marshal(llmToolChunkData{ToolUseID: chunk.ToolUseID, Name: chunk.ToolName, Input: json.RawMessage(chunk.Input)})
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal %s: %w", chunk.Kind, err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	case domain.AggregatorChunkStop:
		data, err := json.Marshal(llmStopChunkData{StopReason: chunk.StopReason})
		if err != nil {
			return StreamChunkEnvelope{}, fmt.Errorf("llm stream chunk: marshal stop: %w", err)
		}
		return StreamChunkEnvelope{Kind: chunk.Kind, Data: data}, nil
	default:
		return StreamChunkEnvelope{Kind: chunk.Kind}, nil
	}
}

// llmAggregator joins text/reasoning deltas and captures the final usage
// chunk into the LLMResp terminal that synchronous SDK clients (Complete/Chat
// and the terminal of CompleteStream/ChatStream) consume. Completed tool
// calls are collected into ToolCalls — a forced-tool-call request ends with
// ToolCalls set and Text empty. One instance per stream session. Typed via
// TypedStreamAggregator — the chunk shape is checked against the route's
// type parameter at compile time.
type llmAggregator struct {
	text      strings.Builder
	reasoning strings.Builder
	usage     *domain.UsageData
	toolCalls []LLMToolCall
}

// Push feeds one backing chunk into the aggregation state.
func (a *llmAggregator) Push(chunk domain.AggregatorChunk) error {
	switch chunk.Kind {
	case domain.AggregatorChunkText:
		a.text.WriteString(chunk.Text)
	case domain.AggregatorChunkReasoning:
		a.reasoning.WriteString(chunk.Text)
	case domain.AggregatorChunkUsage:
		if chunk.Usage != nil {
			a.usage = chunk.Usage
		}
	case domain.AggregatorChunkToolUseComplete:
		a.toolCalls = append(a.toolCalls, LLMToolCall{
			ID:        chunk.ToolUseID,
			Name:      chunk.ToolName,
			Arguments: json.RawMessage(chunk.Input),
		})
	}
	return nil
}

// Terminal returns the aggregated response shape. Reasoning, Usage, and
// ToolCalls are omitted when absent so the terminal matches the
// pre-streaming contract (the LLMResp json tags carry the same omitempty
// semantics).
func (a *llmAggregator) Terminal() LLMResp {
	resp := LLMResp{Text: a.text.String()}
	if a.reasoning.Len() > 0 {
		resp.Reasoning = a.reasoning.String()
	}
	if a.usage != nil {
		data, err := json.Marshal(a.usage)
		if err == nil {
			resp.Usage = json.RawMessage(data)
		}
	}
	if len(a.toolCalls) > 0 {
		resp.ToolCalls = a.toolCalls
	}
	return resp
}

// adaptLLMReq rewrites the SDK llm.complete / llm.chat request payload into
// the aiaggregator.dispatch request shape (SendSessionMessageReq JSON). The
// reserved __AgentId / __WorkspaceId / __DeadlineAt keys injected by the
// host bridge are consumed here (identity) or stripped (deadline — the
// bridge already derived its call context from it).
func adaptLLMReq(req []byte) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(req, &payload); err != nil {
		return nil, fmt.Errorf("llm adapt: decode: %w", err)
	}
	out, err := adaptLLMPayload(payload)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("llm adapt: marshal: %w", err)
	}
	return body, nil
}

// adaptLLMPayload rewrites the SDK llm.complete / llm.chat payload into the
// aiaggregator.dispatch request. Plugin-side unit selection is binary: both
// provider and model name one unit (dispatch pins it); neither means
// auto-pick. A naked model (model without provider) is rejected HERE: the
// pool already forbids it in matchUnits, and letting the request fall
// through degraded it to whole-pool auto-pick — aggregator-ref entries
// entered the sweep and panel calls hung in nested child streams
// (2026-09-05 novel per-section hang). Failing in the adapter surfaces a
// clear error to the plugin in milliseconds instead.
func adaptLLMPayload(payload map[string]any) (domain.SendSessionMessageReq, error) {
	req := domain.SendSessionMessageReq{
		SessionID: uuid.NewString(),
	}

	// Caller-identity fields and outer deadline injected by the host bridge
	// (route InjectCallerContext): attribute usage stats to the originating
	// agent/workspace. The deadline key is stripped here — the bridge already
	// consumed it to derive the aggregator call context.
	if agentID, ok := payload["__AgentId"].(string); ok && agentID != "" {
		req.AgentID = agentID
	}
	if workspaceID, ok := payload["__WorkspaceId"].(string); ok && workspaceID != "" {
		req.WorkspaceID = workspaceID
	}
	delete(payload, "__DeadlineAt")

	if system, ok := payload["system"].(string); ok && system != "" {
		req.System = system
	}

	if model, ok := payload["model"].(string); ok && model != "" {
		if req.Unit == nil {
			req.Unit = &domain.ModelUnit{}
		}
		req.Unit.Model = model
	}
	if provider, ok := payload["provider"].(string); ok && provider != "" {
		if req.Unit == nil {
			req.Unit = &domain.ModelUnit{}
		}
		req.Unit.Provider = provider
	}

	// An explicit unit selection (both provider and model named) is a pin.
	// Plugin UIs let the user pick exactly one unit; dispatch must honor it
	// or fail loudly — never silently rotate to another pool entry. Unpinned
	// transport was the 2026-09-05 deepseek grind: provider+model reached
	// the aggregator as soft affinity, the first attempt failed, and the
	// failover loop swept the whole 234-unit pool for minutes. Model-only
	// stays unpinned (rotation across providers serving the same model is
	// the resilience a bare model choice wants), and an explicit
	// unitPinned/unit_pinned payload flag overrides the derivation.
	if req.Unit != nil && req.Unit.Provider != "" && req.Unit.Model != "" {
		req.UnitPinned = true
	}
	if v, ok := payload["unitPinned"].(bool); ok {
		req.UnitPinned = v
	} else if v, ok := payload["unit_pinned"].(bool); ok {
		req.UnitPinned = v
	}

	if msgs, ok := payload["messages"].([]any); ok && len(msgs) > 0 {
		req.Messages = convertSDKMessages(msgs)
	} else if prompt, ok := payload["prompt"].(string); ok && prompt != "" {
		req.Messages = []domain.ChatMessage{
			{Role: "user", Content: []domain.ContentBlock{{Type: "text", Text: prompt}}},
		}
	}

	if v, ok := payload["temperature"].(float64); ok {
		req.Temperature = v
	}
	if v, ok := payload["top_p"].(float64); ok {
		req.TopP = v
	}
	if v, ok := payload["topP"].(float64); ok {
		req.TopP = v
	}
	if v, ok := payload["frequency_penalty"].(float64); ok {
		req.FrequencyPenalty = v
	}
	if v, ok := payload["presence_penalty"].(float64); ok {
		req.PresencePenalty = v
	}
	if v, ok := payload["reasoning_effort"].(string); ok && v != "" {
		req.ReasoningEffort = v
	}
	if v, ok := payload["thinking_budget"].(float64); ok {
		req.ThinkingBudget = int32(v)
	}

	// Tools + tool_choice: function tools offered to the model and whether it
	// must call one. Completed calls stream back as tool_use chunks and land
	// in the terminal ToolCalls — the plugin executes them itself.
	if rawTools, ok := payload["tools"].([]any); ok && len(rawTools) > 0 {
		req.Tools = convertSDKTools(rawTools)
	}
	if tc, ok := payload["tool_choice"].(string); ok && tc != "" {
		req.ToolChoice = tc
	}

	// Half-specified units are rejected: the plugin named a model but not a
	// provider (or vice versa). Pinning is impossible and auto-pick would
	// silently serve some other provider's interpretation of that model.
	if req.Unit != nil && ((req.Unit.Model == "") != (req.Unit.Provider == "")) {
		return domain.SendSessionMessageReq{}, fmt.Errorf(
			"llm unit selection needs both provider and model (got model=%q provider=%q); omit both for host auto-pick",
			req.Unit.Model, req.Unit.Provider)
	}
	return req, nil
}

// convertSDKTools maps SDK tool definitions into domain.ToolSpec entries.
// InputSchema accepts an inline JSON object or a pre-encoded string — the
// domain carries it as a JSON string. Entries without a name are dropped.
func convertSDKTools(raw []any) []domain.ToolSpec {
	out := make([]domain.ToolSpec, 0, len(raw))
	for _, t := range raw {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		ts := domain.ToolSpec{}
		if n, ok := m["name"].(string); ok {
			ts.Name = n
		}
		if d, ok := m["description"].(string); ok {
			ts.Description = d
		}
		switch s := m["input_schema"].(type) {
		case string:
			ts.InputSchema = s
		case map[string]any, []any:
			if b, err := json.Marshal(s); err == nil {
				ts.InputSchema = string(b)
			}
		}
		if ts.Name != "" {
			out = append(out, ts)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func convertSDKMessages(raw []any) []domain.ChatMessage {
	out := make([]domain.ChatMessage, 0, len(raw))
	for _, m := range raw {
		switch v := m.(type) {
		case string:
			out = append(out, domain.ChatMessage{
				Role:    "user",
				Content: []domain.ContentBlock{{Type: "text", Text: v}},
			})
		case map[string]any:
			role, _ := v["role"].(string)
			if role == "" {
				role = "user"
			}
			out = append(out, domain.ChatMessage{
				Role:    role,
				Content: convertSDKContent(v["content"]),
			})
		}
	}
	return out
}

// stringifySDKJSON renders a tool-input payload that arrives either as a
// pre-encoded JSON string or as an inline object into the string form the
// domain content blocks carry. Nil yields "".
func stringifySDKJSON(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func convertSDKContent(c any) []domain.ContentBlock {
	switch v := c.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []domain.ContentBlock{{Type: "text", Text: v}}
		case []any:
			blocks := make([]domain.ContentBlock, 0, len(v))
			for _, e := range v {
				switch x := e.(type) {
				case string:
					blocks = append(blocks, domain.ContentBlock{Type: "text", Text: x})
				case map[string]any:
					cb := domain.ContentBlock{Type: "text"}
					if t, ok := x["type"].(string); ok {
						cb.Type = t
					}
					if t, ok := x["text"].(string); ok {
						cb.Text = t
					}
					// Tool blocks: assistant tool_use (tool_use_id, tool_name,
					// input) and user tool_result (tool_use_id, is_error) —
					// echoed back by a plugin running a tool-call loop.
					if id, ok := x["tool_use_id"].(string); ok {
						cb.ToolUseID = id
					}
					if n, ok := x["tool_name"].(string); ok {
						cb.ToolName = n
					}
					cb.Input = stringifySDKJSON(x["input"])
					if isErr, ok := x["is_error"].(bool); ok {
						cb.IsError = isErr
					}
					blocks = append(blocks, cb)
				default:
					blocks = append(blocks, domain.ContentBlock{Type: "text", Text: fmt.Sprint(x)})
				}
			}
			return blocks
	default:
		if c == nil {
			return nil
		}
		return []domain.ContentBlock{{Type: "text", Text: fmt.Sprint(c)}}
	}
}
