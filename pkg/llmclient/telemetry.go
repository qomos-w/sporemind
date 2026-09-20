package llmclient

import (
	"sync"
	"time"
)

// Usage carries cumulative token counters, including provider-normalized cache
// and reasoning breakdowns.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	// Cache tokens (Anthropic-specific; also parsed from OpenAI-compatible
	// prompt_tokens_details when providers emit it).
	CacheCreationInputTokens int
	CacheReadInputTokens     int
	// ReasoningTokens is the subset of OutputTokens spent on reasoning/thinking.
	ReasoningTokens int
}

// StopReason values are normalized across providers.
const (
	StopReasonStop    = "stop"
	StopReasonLength  = "length"
	StopReasonToolUse = "toolUse"
	StopReasonError   = "error"
	StopReasonAborted = "aborted"
)

// RequestTelemetry captures metadata for a single provider request. It is
// populated during streaming and returned after the stream closes.
type RequestTelemetry struct {
	// Provider/Model are the identifiers used to route the request.
	Provider string
	Model    string

	// ResponseModel is the model the provider actually served (e.g. after
	// routing or fallback). Empty when not reported.
	ResponseModel string

	// RequestID/ResponseID are provider-side identifiers, when available.
	RequestID       string
	ResponseID      string
	ClientRequestID string

	// Usage is the final token accounting from the provider.
	Usage *Usage

	// StopReason is normalized from the provider's finish reason.
	StopReason string

	// ErrorCode is a canonical failure category (see llmclient.ClassifyErrorCode)
	// derived from the dispatch error. Empty when the request did not error.
	ErrorCode string

	// ErrorMessage is set when the stream ends with an error rather than a
	// normal stop reason.
	ErrorMessage string

	// LatencyMs is the total wall time from the first byte sent to the end of
	// the stream. FirstTokenMs is the time from request start to the first
	// streamed content event (text/reasoning/tool). Both are zero when timing
	// is unavailable.
	LatencyMs    int64
	FirstTokenMs int64
}

// telemetryState collects timing and metadata during a stream. It is safe to
// read from the consumer goroutine and write from the stream goroutine.
type telemetryState struct {
	mu            sync.Mutex
	startedAt     time.Time
	firstTokenAt  time.Time
	completedAt   time.Time
	provider      string
	model         string
	responseModel string
	requestID     string
	responseID    string
	clientReqID   string
	stopReason    string
	errorCode     string
	errorMessage  string
	usage         *Usage
	firstTokenSet bool
	completed     bool
}

func (t *telemetryState) setStart() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.startedAt = time.Now()
}

func (t *telemetryState) setFirstToken() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.firstTokenSet || t.startedAt.IsZero() {
		return
	}
	t.firstTokenAt = time.Now()
	t.firstTokenSet = true
}

func (t *telemetryState) setComplete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.completed {
		return
	}
	t.completedAt = time.Now()
	t.completed = true
}

func (t *telemetryState) setIDs(provider, model, responseModel, requestID, responseID, clientReqID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.provider = provider
	t.model = model
	if responseModel != "" {
		t.responseModel = responseModel
	}
	if requestID != "" {
		t.requestID = requestID
	}
	if responseID != "" {
		t.responseID = responseID
	}
	if clientReqID != "" {
		t.clientReqID = clientReqID
	}
}

func (t *telemetryState) setStopReason(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopReason = reason
}

func (t *telemetryState) setError(err error) {
	if err == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errorMessage = err.Error()
	t.errorCode = ClassifyErrorCode(err, t.stopReason)
	if t.stopReason == "" {
		t.stopReason = StopReasonError
	}
}

func (t *telemetryState) setUsage(u *Usage) {
	if u == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.usage == nil {
		t.usage = &Usage{}
	}
	// Cumulative usage is reported once by most providers; overwrite.
	t.usage.InputTokens = u.InputTokens
	t.usage.OutputTokens = u.OutputTokens
	t.usage.TotalTokens = u.TotalTokens
	t.usage.CacheCreationInputTokens = u.CacheCreationInputTokens
	t.usage.CacheReadInputTokens = u.CacheReadInputTokens
	t.usage.ReasoningTokens = u.ReasoningTokens
}

func (t *telemetryState) snapshot() RequestTelemetry {
	t.mu.Lock()
	defer t.mu.Unlock()

	var latencyMs, firstTokenMs int64
	if !t.startedAt.IsZero() {
		if !t.completedAt.IsZero() {
			latencyMs = t.completedAt.Sub(t.startedAt).Milliseconds()
		} else {
			latencyMs = time.Since(t.startedAt).Milliseconds()
		}
		if !t.firstTokenAt.IsZero() {
			firstTokenMs = t.firstTokenAt.Sub(t.startedAt).Milliseconds()
		}
	}

	return RequestTelemetry{
		Provider:        t.provider,
		Model:           t.model,
		ResponseModel:   t.responseModel,
		RequestID:       t.requestID,
		ResponseID:      t.responseID,
		ClientRequestID: t.clientReqID,
		Usage:           t.usage,
		StopReason:      t.stopReason,
		ErrorCode:       t.errorCode,
		ErrorMessage:    t.errorMessage,
		LatencyMs:       latencyMs,
		FirstTokenMs:    firstTokenMs,
	}
}

// normalizeStopReason maps provider-native reasons to the canonical set.
func normalizeStopReason(reason string) string {
	switch reason {
	case "stop", "end_turn", "stop_sequence":
		return StopReasonStop
	case "length", "max_tokens":
		return StopReasonLength
	case "tool_calls", "tool_use":
		return StopReasonToolUse
	case "content_filter":
		return StopReasonAborted
	default:
		if reason == "" {
			return ""
		}
		return StopReasonError
	}
}
