// Package llmclient provides streaming LLM HTTP clients for the
// Anthropic and OpenAI wire formats. Used by aiaggregator to talk
// to provider endpoints; no actor / business logic lives here.
package llmclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Request is the minimal per-call surface.
//
// For a first-turn user message, set UserText. For multi-turn tool-use
// loops, set Messages (the provider adapter ignores UserText when Messages
// is non-empty). Tools is the JSON-Schema tool catalog exposed to the
// model; an empty slice disables tool-use for the call.
type Request struct {
	Model           string
	System          string
	SystemBlocks    []SystemBlock // Structured system prompt; takes precedence over System when non-empty
	UserText        string
	MaxTokens       int
	ThinkingBudget  int    // Anthropic: >0 enables extended thinking
	ReasoningEffort string // OpenAI: "low" | "medium" | "high"
	Messages        []Message
	Tools           []ToolSpec

	// Generation parameters. Nil pointer = use provider default.
	Temperature      *float64 // OpenAI / OpenAI-compatible
	TopP             *float64
	FrequencyPenalty *float64
	PresencePenalty  *float64
	Seed             *int
	Stop             []string // Custom stop sequences
	ToolChoice       string   // "auto" | "none" | "required" | or "{type:\"function\",function:{name:\"…\"}}" JSON

	// UserAgent overrides the default SporeMind/<version> header for this call.
	// Some providers (e.g. Kimi For Coding) check User-Agent for access control.
	UserAgent string

	// ExtraBody contains provider-specific request body extensions that are not
	// part of the standard OpenAI/Anthropic fields. The OpenAI-compatible
	// adapter merges these keys into the serialized JSON body before sending.
	ExtraBody map[string]any

	// IsReasoning reports whether the dispatching unit is a reasoning/thinking
	// model (config-derived). When true (or when the request otherwise activates
	// thinking mode), the OpenAI adapter diverts a leading inline think block
	// that some providers leak into the content stream into reasoning events.
	IsReasoning bool
}

// SystemBlock is one block in a structured system prompt.
type SystemBlock struct {
	Type         string // "text"
	Text         string
	CacheControl string // "ephemeral" or ""
}

// Role values used inside Message.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ContentBlock types.
const (
	BlockTypeText       = "text"
	BlockTypeToolUse    = "tool_use"
	BlockTypeToolResult = "tool_result"
	BlockTypeImage      = "image"
)

// Message is one multi-content message in conversation history.
type Message struct {
	Role             string
	Content          []Block
	ReasoningContent string // reasoning/thinking text; preserved for OpenAI-compatible providers
}

// Block is one piece of a Message. Type discriminates fields.
type Block struct {
	Type         string
	Text         string
	ToolUseID    string
	ToolName     string
	Input        string // raw JSON string for tool_use; tool input from model
	IsError      bool   // for tool_result blocks
	CacheControl string // "ephemeral" or ""; only meaningful for Anthropic
	ImageURL     string // base64 data URL or external image URL
	MimeType     string // e.g. image/png, image/jpeg
}

// ToolSpec describes one callable exposed to the LLM as a tool.
// InputSchema is a JSON Schema object (raw JSON string).
// Type is the provider-native tool type (e.g. "web_search_20250305" for
// Anthropic web search). When empty the spec is treated as a standard
// function tool.
type ToolSpec struct {
	Name         string
	Description  string
	InputSchema  string
	Type         string         // provider-native tool type; empty = standard function
	NativeConfig map[string]any // provider-native tool extra config (e.g. web_search: {enable: true})
	CacheControl string         // "ephemeral" or ""; only meaningful for Anthropic
}

// EventKind identifies streamed client events.
type EventKind string

const (
	EventTextDelta         EventKind = "text_delta"
	EventReasoningDelta    EventKind = "reasoning_delta"
	EventUsage             EventKind = "usage"
	EventToolUseStart      EventKind = "tool_use_start"
	EventToolUseInputDelta EventKind = "tool_use_input_delta"
	EventToolUseComplete   EventKind = "tool_use_complete"
	EventStop              EventKind = "stop"
	EventDone              EventKind = "done"
	EventError             EventKind = "error"
)

// Event is one chunk in the streamed response.
//
// For tool_use_start: ToolUseID and ToolName are set.
// For tool_use_input_delta: ToolUseID and InputDelta (a JSON fragment) are set.
// For tool_use_complete: ToolUseID and Input (full assembled JSON) are set.
// For stop: StopReason ("end_turn" | "tool_use" | …) is set.
type Event struct {
	Kind       EventKind
	Text       string
	Usage      *Usage
	Err        error
	ToolUseID  string
	ToolName   string
	InputDelta string
	Input      string
	StopReason string
}

// Stream exposes the streamed event channel and final telemetry after close.
type Stream interface {
	Events() <-chan Event
	Close() error
	Telemetry() RequestTelemetry
}

// Client streams a Request against a provider endpoint.
type Client interface {
	Stream(ctx context.Context, req Request) (Stream, error)
}

const defaultMaxTokens = 4096

// HTTPError carries the HTTP status code for a failed provider request.
// It lets upstream callers distinguish retryable or observable codes such
// as 429 or 502 without parsing error strings.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e HTTPError) Error() string {
	return fmt.Sprintf("http %d: %s", e.StatusCode, e.Body)
}

// HTTPErrorFromResponse builds an HTTPError from a non-2xx HTTP response.
func HTTPErrorFromResponse(resp *http.Response, body []byte) *HTTPError {
	return &HTTPError{
		StatusCode: resp.StatusCode,
		Body:       strings.TrimSpace(string(body)),
	}
}

// ExtractHTTPStatus returns the HTTP status code embedded in err if available.
// It first checks for a structured *UpstreamError, then a legacy *HTTPError,
// then falls back to parsing the error string for patterns like "http 404:
// ..." produced when the error is serialized across the actor wire.
//
// This function is retained as a compatibility fallback. New health/cooldown
// logic should prefer errors.As for *UpstreamError to access Code, Type, and
// RetryAfter directly.
func ExtractHTTPStatus(err error) int {
	if err == nil {
		return 0
	}
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.StatusCode
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode
	}
	msg := err.Error()
	for _, prefix := range []string{"http ", "HTTP "} {
		idx := strings.Index(msg, prefix)
		if idx < 0 {
			continue
		}
		start := idx + len(prefix)
		end := start
		for end < len(msg) && msg[end] >= '0' && msg[end] <= '9' {
			end++
		}
		if end <= start {
			continue
		}
		code, convErr := strconv.Atoi(msg[start:end])
		if convErr == nil && code >= 100 && code < 1000 {
			return code
		}
	}
	return 0
}

// IsProviderFailureStatus reports whether an HTTP status code indicates the
// provider itself is unavailable (rate limit or server error) rather than a
// caller-side issue.
func IsProviderFailureStatus(statusCode int) bool {
	return statusCode == 429 || statusCode >= 500
}

// Canonical ErrorCode values normalized across providers. They are stored on
// AIStatsRecord so the model-unit performance dashboard can break failures
// down by type without parsing free-form error strings.
const (
	ErrorCodeRateLimit     = "rate_limit"
	ErrorCodeOverloaded    = "overloaded"
	ErrorCodeServer        = "server_error"
	ErrorCodeAuth          = "auth"
	ErrorCodeTimeout       = "timeout"
	ErrorCodeClient        = "client_error"
	ErrorCodeContentFilter = "content_filter"
	ErrorCodeConnection    = "connection_error"
	ErrorCodeDispatch      = "dispatch_error"
	ErrorCodeUnknown       = "unknown"
)

// ClassifyErrorCode maps a dispatch error to a canonical ErrorCode for
// telemetry and the aistats dashboard. stopReason is the already-normalized
// stop reason from the stream (may be ""). Returns "" when err is nil and the
// stop reason does not indicate an error-like outcome.
func ClassifyErrorCode(err error, stopReason string) string {
	if err == nil {
		if stopReason == StopReasonAborted {
			return ErrorCodeContentFilter
		}
		return ""
	}
	switch status := ExtractHTTPStatus(err); {
	case status == 429:
		return ErrorCodeRateLimit
	case status == 401 || status == 403:
		return ErrorCodeAuth
	case status == 408:
		return ErrorCodeTimeout
	case status == 503 || status == 529:
		return ErrorCodeOverloaded
	case status >= 500 && status < 600:
		return ErrorCodeServer
	case status >= 400 && status < 500:
		return ErrorCodeClient
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorCodeTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCodeClient
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "timeout") || strings.Contains(lower, "idle timeout") {
		return ErrorCodeTimeout
	}
	if strings.Contains(lower, "nested dispatch") ||
		strings.Contains(lower, "no callable unit") ||
		strings.Contains(lower, "token plan") ||
		strings.Contains(lower, "resolve token") ||
		strings.Contains(lower, "child stream") ||
		strings.Contains(lower, "child exhausted") ||
		strings.Contains(lower, "aiaggregator.dispatch") {
		return ErrorCodeDispatch
	}
	if strings.Contains(lower, "dial tcp") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "tls:") ||
		strings.Contains(lower, "eof") {
		return ErrorCodeConnection
	}
	return ErrorCodeUnknown
}
