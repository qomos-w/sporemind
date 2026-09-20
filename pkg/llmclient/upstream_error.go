package llmclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UpstreamError is the unified error representation for a failed provider
// (LLM) HTTP request. It normalizes structured error fields from OpenAI,
// Anthropic, Gemini, and other protocols into common semantic fields.
//
// StatusCode is always set. Code carries the provider-native string error
// code (e.g. OpenAI "insufficient_quota", "rate_limit_exceeded") when the
// protocol defines one; it is left empty when no structured code is present
// — no guessing. Type carries the provider-native error type or category
// (e.g. "rate_limit_error", "overloaded_error", "RESOURCE_EXHAUSTED").
// Message is the human-readable error description. RetryAfter is the
// duration parsed from the Retry-After response header (zero when absent).
//
// Callers obtain an *UpstreamError via errors.As on any wrapped error chain.
type UpstreamError struct {
	StatusCode int
	Code       string
	Type       string
	Message    string
	RetryAfter time.Duration
}

// Error renders a human-readable description. The format "http N: …" is
// intentionally compatible with ExtractHTTPStatus's string fallback so the
// status code survives actor-wire serialization where the concrete type is
// lost.
func (e *UpstreamError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if msg == "" {
		msg = "upstream error"
	}
	if e.Code != "" || e.Type != "" {
		return fmt.Sprintf("http %d [%s/%s]: %s", e.StatusCode, e.Type, e.Code, msg)
	}
	return fmt.Sprintf("http %d: %s", e.StatusCode, msg)
}

// ---------------------------------------------------------------------------
// Pure parsing helpers (unit-testable, no I/O)
// ---------------------------------------------------------------------------

// providerErrorEnvelope is the common JSON shape for provider error
// responses. All three major providers (OpenAI, Anthropic, Gemini) nest
// their structured error details under a top-level "error" key.
type providerErrorEnvelope struct {
	Error struct {
		// Code is provider-native: string for OpenAI (e.g.
		// "insufficient_quota"), numeric for Gemini (duplicates HTTP
		// status). Captured as RawMessage so we can distinguish.
		Code json.RawMessage `json:"code"`
		// Type is the error category for OpenAI ("rate_limit_error") and
		// Anthropic ("overloaded_error"). Absent in Gemini.
		Type string `json:"type"`
		// Status is Gemini's error category (e.g.
		// "RESOURCE_EXHAUSTED"). Absent in OpenAI/Anthropic.
		Status string `json:"status"`
		// Message is the human-readable description in all three.
		Message string `json:"message"`
	} `json:"error"`
}

// parseProviderErrorBody extracts structured error fields from a provider
// error response body. It handles OpenAI, Anthropic, and Gemini error
// shapes via a common "error" envelope.
//
// Field mapping:
//   - OpenAI: error.code → Code (string only), error.type → Type,
//     error.message → Message.
//   - Anthropic: error.type → Type, error.message → Message. No Code field.
//   - Gemini: error.status → Type, error.message → Message. error.code is
//     numeric and duplicates the HTTP status — it is not mapped to Code.
//
// Fields not present in the body are returned as empty strings. When the
// body is not valid JSON, the trimmed raw body is returned as the message
// and code/type remain empty.
func parseProviderErrorBody(body []byte) (code, typ, message string) {
	var raw providerErrorEnvelope
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", "", strings.TrimSpace(string(body))
	}
	// Code: accept only string values. Numeric codes (Gemini) are
	// redundant with the HTTP status and must not pollute the Code field.
	if len(raw.Error.Code) > 0 {
		var s string
		if json.Unmarshal(raw.Error.Code, &s) == nil && s != "" {
			code = s
		}
	}
	// Type: prefer error.type (OpenAI/Anthropic), fall back to error.status (Gemini).
	typ = raw.Error.Type
	if typ == "" {
		typ = raw.Error.Status
	}
	message = strings.TrimSpace(raw.Error.Message)
	return code, typ, message
}

// IsQuotaExhausted reports whether an upstream failure represents quota
// exhaustion rather than a generic 403 authorization failure or a transient
// 429 rate limit. Only HTTP 403 and 429 qualify: a structured code/type from
// the quota sets, or (when no structured signal matched) a quota marker in the
// human-readable message. Providers also deliver account-level exhaustion as
// 429 (token-plan / usage-window limits), and those must cool down long
// instead of riding the short rate-limit window.
func IsQuotaExhausted(statusCode int, code, typ, message string) bool {
	if statusCode != http.StatusForbidden && statusCode != http.StatusTooManyRequests {
		return false
	}
	if quotaErrorCodes[strings.ToLower(code)] || quotaErrorTypes[strings.ToLower(typ)] {
		return true
	}
	msg := strings.ToLower(message)
	for _, marker := range quotaMessageMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// IsModelDeprecated reports whether an upstream failure indicates the unit's
// model has been retired by the provider. Only HTTP 400 or 404 qualifies: a
// structured code/type of "model_deprecated" (when present), or a deprecation
// marker in the human-readable message otherwise.
//
// Relays (one-api / new-api "Console Go") wrap an upstream 404 "model has been
// deprecated" into an outer 400 [server_error]; the message scan catches both
// that relayed form and direct 404s. A deprecated model is a per-unit
// condition, so a hit redirects the failure from ClassStop (terminate) to
// ClassModelCooldown (cool the unit, rotate to the next candidate).
func IsModelDeprecated(statusCode int, code, typ, message string) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	if modelDeprecatedCodes[strings.ToLower(code)] || modelDeprecatedTypes[strings.ToLower(typ)] {
		return true
	}
	msg := strings.ToLower(message)
	for _, marker := range modelDeprecatedMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// IsImageInputUnsupported reports whether an upstream failure indicates the
// unit's model cannot accept image (vision) input. Only HTTP 400 or 404
// qualifies, detected via a marker in the human-readable message — no
// provider defines a structured code for this condition. A hit redirects the
// failure from ClassStop (terminate) to the aggregator's strip-images retry:
// the unit is marked text-only and the request is re-sent with image blocks
// replaced by a text placeholder (session history untouched).
func IsImageInputUnsupported(statusCode int, code, typ, message string) bool {
	if statusCode != http.StatusBadRequest && statusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(message)
	for _, marker := range imageUnsupportedMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// IsImageInputUnsupportedErr reports whether err (in any surviving form —
// *UpstreamError, or a string that crossed the actor wire and retains the
// "http N …" rendering) signals unsupported image input.
func IsImageInputUnsupportedErr(err error) bool {
	if err == nil {
		return false
	}
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return IsImageInputUnsupported(upstreamErr.StatusCode, upstreamErr.Code, upstreamErr.Type, upstreamErr.Message)
	}
	status := ExtractHTTPStatus(err)
	if status == 0 {
		return false
	}
	typ, code := ExtractErrorCodeType(err)
	return IsImageInputUnsupported(status, code, typ, err.Error())
}

// ExtractErrorCodeType recovers the provider-native error type and code from
// an error string rendered by (*UpstreamError).Error ("http N [type/code]: …")
// after the concrete type was lost crossing the actor wire. Returns empty
// strings when the pattern is absent.
func ExtractErrorCodeType(err error) (typ, code string) {
	if err == nil {
		return "", ""
	}
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.Type, upstreamErr.Code
	}
	msg := err.Error()
	open := strings.Index(msg, "[")
	closeIdx := strings.Index(msg, "]")
	if open < 0 || closeIdx <= open+1 {
		return "", ""
	}
	inner := msg[open+1 : closeIdx]
	slash := strings.Index(inner, "/")
	if slash < 0 {
		return "", ""
	}
	return inner[:slash], inner[slash+1:]
}

// parseRetryAfter parses an HTTP Retry-After header value into a duration.
// It accepts both delta-seconds ("120") and HTTP-date
// ("Wed, 21 Oct 2015 07:28:00 GMT") formats per RFC 7231 §7.1.3.
//
// For delta-seconds the value is a non-negative integer. For HTTP-date the
// remaining time until the specified instant is computed; a past date
// yields zero. Unparseable or empty input yields zero.
func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	// Delta-seconds: non-negative integer.
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// HTTP-date: compute remaining duration from now.
	for _, layout := range []string{
		time.RFC1123,
		time.RFC1123Z,
		time.RFC850,
		time.ANSIC,
	} {
		if t, err := time.Parse(layout, header); err == nil {
			d := time.Until(t)
			if d > 0 {
				return d
			}
			return 0
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Builder
// ---------------------------------------------------------------------------

// UpstreamErrorFromResponse builds an *UpstreamError from a non-2xx HTTP
// response. It parses structured error fields from the response body
// (OpenAI/Anthropic/Gemini shapes) and the Retry-After header.
//
// The returned error is obtainable via errors.As on any wrapped error chain.
func UpstreamErrorFromResponse(resp *http.Response, body []byte) *UpstreamError {
	code, typ, message := parseProviderErrorBody(body)
	return &UpstreamError{
		StatusCode: resp.StatusCode,
		Code:       code,
		Type:       typ,
		Message:    message,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
	}
}
