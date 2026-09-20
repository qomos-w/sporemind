package llmclient

import (
	"context"
	"errors"
	"net"
	"strings"
)

// StreamOpenClass determines the failover action for a stream-open failure and
// the local health treatment for the failed unit. It is returned by the pure
// classification functions in this file and has no dependencies on actor state.
type StreamOpenClass int

const (
	// ClassFailover: transient availability errors (5xx, network, timeout). The
	// unit is marked as tried and the next healthy candidate is attempted.
	ClassFailover StreamOpenClass = iota
	// ClassShortCooldown: rate limit (429). The unit is cooled down
	// immediately (respecting Retry-After) and the next candidate is attempted.
	ClassShortCooldown
	// ClassQuotaCooldown: quota exhausted (403 + quota signal). The unit is
	// cooled down with a longer duration so quota has time to refresh, and the
	// next candidate is attempted.
	ClassQuotaCooldown
	// ClassLongDisable: authentication or authorization failure. The unit is
	// disabled long-term and the next candidate is attempted.
	ClassLongDisable
	// ClassModelCooldown: the unit's model has been deprecated or removed by the
	// provider (400/404 carrying a deprecation signal, including relays that
	// wrap an upstream 404 into an outer 400 [server_error]). Unlike a generic
	// 400, a deprecated model is a per-unit condition: the failed unit enters a
	// self-healing cooldown and the candidate loop rotates to the next unit.
	ClassModelCooldown
	// ClassStop: request-side error (400/404) or caller cancellation. The
	// error is not unit-specific — the candidate loop terminates without trying
	// another unit, except when a stop-retry budget sees a stop code that
	// differs from the first (then it rotates once more).
	ClassStop
)

func (c StreamOpenClass) String() string {
	switch c {
	case ClassFailover:
		return "failover"
	case ClassShortCooldown:
		return "short_cooldown"
	case ClassQuotaCooldown:
		return "quota_cooldown"
	case ClassLongDisable:
		return "long_disable"
	case ClassModelCooldown:
		return "model_cooldown"
	case ClassStop:
		return "stop"
	default:
		return "unknown"
	}
}

// StreamOpenClassification bundles the failover class with the health reason
// string that should be attributed to the failed unit. Rotatable is true when
// the class is not ClassStop (i.e. the candidate loop may rotate to another
// unit). ConnectStage is true when the failure happened before any HTTP
// response — dial refused/timeout, DNS, TLS handshake — meaning the endpoint
// could not be reached at all.
type StreamOpenClassification struct {
	Class        StreamOpenClass
	Reason       string
	StatusCode   int
	Rotatable    bool
	ConnectStage bool
}

// ClassifyStreamOpenError maps a stream-open error to a failover action and
// health reason. It is a pure function with no side effects and no
// dependencies on actor state.
//
// Classification rules:
//
//   - 429                        → ClassShortCooldown (rate_limit)
//   - 403 + quota signal         → ClassQuotaCooldown (quota_exhausted)
//   - 403 (other)                → ClassLongDisable  (authorization_failed)
//   - 401                        → ClassLongDisable  (authentication_failed)
//   - 400, 404 + deprecated signal → ClassModelCooldown (model_deprecated)
//   - 400, 404 (plain)           → ClassStop         (configuration_error)
//   - 5xx                        → ClassFailover     (availability)
//   - network/timeout            → ClassFailover     (availability)
//   - context.Canceled           → ClassStop         (caller cancellation)
//
// ClassFailover and ClassShortCooldown both allow failover to the next
// candidate; they differ in how the failed unit is treated locally.
// ClassLongDisable also allows failover but marks the unit as disabled.
// ClassStop terminates the candidate loop immediately.
func ClassifyStreamOpenError(err error) StreamOpenClassification {
	if err == nil {
		return StreamOpenClassification{Class: ClassStop, Reason: "", Rotatable: false}
	}
	if errors.Is(err, context.Canceled) {
		return StreamOpenClassification{Class: ClassStop, Reason: "", Rotatable: false}
	}

	// Prefer the structured UpstreamError (set by all three protocol adapters
	// after the upstream-error normalization refactor).
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return ClassifyUpstreamError(upstreamErr)
	}

	// Legacy HTTPError (pre-normalization error type).
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return ClassifyHTTPError(httpErr.StatusCode, "", "", httpErr.Body)
	}

	// String-based fallback (error crossed the actor wire and lost its
	// concrete type). ExtractHTTPStatus parses "http N: …" patterns and
	// ExtractErrorCodeType recovers the "[type/code]" pair rendered by
	// (*UpstreamError).Error; the full string doubles as the message for
	// quota-marker matching.
	status := ExtractHTTPStatus(err)
	if status != 0 {
		typ, code := ExtractErrorCodeType(err)
		return ClassifyHTTPError(status, code, typ, err.Error())
	}

	// Idle timeout and network-level transient errors are availability issues.
	// Errors at the connect stage (never got an HTTP response) are marked so
	// the health layer can apply an immediate short cooldown instead of
	// waiting for the availability threshold — an unreachable endpoint is
	// discovered fastest by not re-dialing it on every dispatch.
	if errors.Is(err, ErrStreamIdleTimeout) || isProviderFailure(err) {
		return StreamOpenClassification{Class: ClassFailover, Reason: HealthReasonAvailability, Rotatable: true, ConnectStage: isConnectStageError(err)}
	}

	// Unknown error — attempt failover as a safe default so a single
	// ambiguous failure does not abort an otherwise healthy pool.
	return StreamOpenClassification{Class: ClassFailover, Reason: HealthReasonAvailability, Rotatable: true}
}

// ClassifyUpstreamError maps a structured UpstreamError to a failover
// classification.
func ClassifyUpstreamError(e *UpstreamError) StreamOpenClassification {
	c := ClassifyHTTPError(e.StatusCode, e.Code, e.Type, e.Message)
	c.StatusCode = e.StatusCode
	return c
}

// ClassifyHTTPError maps an HTTP status code and optional structured error
// code/type/message to a failover classification.
func ClassifyHTTPError(statusCode int, code, typ, message string) StreamOpenClassification {
	c := StreamOpenClassification{StatusCode: statusCode, Rotatable: true}
	switch {
	case IsModelDeprecated(statusCode, code, typ, message):
		c.Class = ClassModelCooldown
		c.Reason = HealthReasonModelDeprecated
	case statusCode == 429 || statusCode == 403:
		// Quota exhaustion wins over the bare status: relays deliver
		// account-level exhaustion (token plan / usage window) as 429, and
		// it must land in the LONG quota cooldown so the unit leaves the
		// eligible pool — not the short rate-limit window, where the
		// failover loop re-picks the dead unit until the dispatch budget
		// expires ("context deadline exceeded" on llm.complete).
		if IsQuotaExhausted(statusCode, code, typ, message) {
			c.Class = ClassQuotaCooldown
			c.Reason = HealthReasonQuotaExhausted
		} else if statusCode == 429 {
			c.Class = ClassShortCooldown
			c.Reason = HealthReasonRateLimit
		} else {
			c.Class = ClassLongDisable
			c.Reason = HealthReasonAuthorizationFailed
		}
	case statusCode == 401:
		c.Class = ClassLongDisable
		c.Reason = HealthReasonAuthenticationFailed
	case statusCode == 400 || statusCode == 404:
		c.Class = ClassStop
		c.Reason = HealthReasonConfigurationError
		c.Rotatable = false
	case statusCode >= 500 && statusCode < 600:
		c.Class = ClassFailover
		c.Reason = HealthReasonAvailability
	case statusCode >= 300 && statusCode < 400:
		c.Class = ClassFailover
		c.Reason = HealthReasonAvailability
	case statusCode >= 400 && statusCode < 500:
		c.Class = ClassStop
		c.Reason = HealthReasonConfigurationError
		c.Rotatable = false
	default:
		c.Class = ClassFailover
		c.Reason = HealthReasonAvailability
	}
	return c
}

// isProviderFailure reports whether an error indicates the provider itself is
// unavailable (network, timeout, server error, rate limit) rather than a
// caller-side or configuration issue.
func isProviderFailure(err error) bool {
	if err == nil {
		return false
	}

	// Caller-side cancellation is not a provider health signal.
	if errors.Is(err, context.Canceled) {
		return false
	}

	// HTTP-level errors: only 429 and 5xx reflect provider availability.
	status := ExtractHTTPStatus(err)
	if status != 0 {
		return IsProviderFailureStatus(status)
	}

	// Network-level timeouts and temporary errors are provider-side signals.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	msg := strings.ToLower(err.Error())
	transientStrings := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"broken pipe",
		"unexpected eof",
		"timeout awaiting response headers",
		"tls handshake timeout",
	}
	for _, s := range transientStrings {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// isConnectStageError reports whether the failure occurred while establishing
// the connection — dial refused or timeout, DNS resolution, TLS handshake —
// i.e. before any HTTP response was received. Connect-stage failures justify
// an immediate short cooldown: the endpoint is provably unreachable right
// now, so re-dialing it on the next dispatch only burns the rotation budget.
func isConnectStageError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"connection refused",
		"no such host",
		"dial tcp",
		"tls handshake timeout",
		"timeout awaiting response headers",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
