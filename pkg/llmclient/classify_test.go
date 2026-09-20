package llmclient

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClassifyStreamOpenError_Matrix(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantClass  StreamOpenClass
		wantReason string
		wantRot    bool
	}{
		// nil / cancellation
		{"nil", nil, ClassStop, "", false},
		{"context.Canceled", context.Canceled, ClassStop, "", false},

		// UpstreamError matrix
		{"UE 429", &UpstreamError{StatusCode: 429}, ClassShortCooldown, HealthReasonRateLimit, true},
		{"UE 429+RetryAfter", &UpstreamError{StatusCode: 429, RetryAfter: 30 * time.Second}, ClassShortCooldown, HealthReasonRateLimit, true},
		{"UE 403+quota", &UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"}, ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"UE 403+access_terminated", &UpstreamError{StatusCode: 403, Type: "access_terminated_error", Message: "You've reached your usage limit for this billing cycle."}, ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"UE 403+usage_limit_msg", &UpstreamError{StatusCode: 403, Message: "usage limit reached for this plan"}, ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"UE 403+other_code", &UpstreamError{StatusCode: 403, Code: "permission_denied"}, ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"UE 403_empty", &UpstreamError{StatusCode: 403}, ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"UE 401", &UpstreamError{StatusCode: 401}, ClassLongDisable, HealthReasonAuthenticationFailed, true},
		{"UE 400", &UpstreamError{StatusCode: 400}, ClassStop, HealthReasonConfigurationError, false},
		{"UE 400+bad_request_msg", &UpstreamError{StatusCode: 400, Message: "invalid request: missing messages"}, ClassStop, HealthReasonConfigurationError, false},
		{"UE 400+deprecated_msg", &UpstreamError{StatusCode: 400, Message: "Error from provider (Console Go): Upstream request failed: [404] This model has been deprecated. It is recommended to migrate to xiaomi/mimo-v2.5"}, ClassModelCooldown, HealthReasonModelDeprecated, true},
		{"UE 404+deprecated_msg", &UpstreamError{StatusCode: 404, Message: "This model has been deprecated and is no longer available"}, ClassModelCooldown, HealthReasonModelDeprecated, true},
		{"UE 400+deprecated_code", &UpstreamError{StatusCode: 400, Code: "model_deprecated"}, ClassModelCooldown, HealthReasonModelDeprecated, true},
		{"UE 404+deprecated_type", &UpstreamError{StatusCode: 404, Type: "Model_Deprecated"}, ClassModelCooldown, HealthReasonModelDeprecated, true},
		{"UE 429+deprecated_msg_ignored", &UpstreamError{StatusCode: 429, Message: "This model has been deprecated"}, ClassShortCooldown, HealthReasonRateLimit, true},
		{"UE 404", &UpstreamError{StatusCode: 404}, ClassStop, HealthReasonConfigurationError, false},
		{"UE 500", &UpstreamError{StatusCode: 500}, ClassFailover, HealthReasonAvailability, true},
		{"UE 502", &UpstreamError{StatusCode: 502}, ClassFailover, HealthReasonAvailability, true},
		{"UE 503", &UpstreamError{StatusCode: 503}, ClassFailover, HealthReasonAvailability, true},
		{"UE 599", &UpstreamError{StatusCode: 599}, ClassFailover, HealthReasonAvailability, true},
		{"UE 302_redirect", &UpstreamError{StatusCode: 302}, ClassFailover, HealthReasonAvailability, true},
		{"UE 301_redirect", &UpstreamError{StatusCode: 301}, ClassFailover, HealthReasonAvailability, true},
		{"UE 418_other4xx", &UpstreamError{StatusCode: 418}, ClassStop, HealthReasonConfigurationError, false},
		{"UE 451_other4xx", &UpstreamError{StatusCode: 451}, ClassStop, HealthReasonConfigurationError, false},
		{"UE 409_other4xx", &UpstreamError{StatusCode: 409}, ClassStop, HealthReasonConfigurationError, false},

		// Legacy HTTPError
		{"HE 429", &HTTPError{StatusCode: 429}, ClassShortCooldown, HealthReasonRateLimit, true},
		{"HE 403", &HTTPError{StatusCode: 403}, ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"HE 500", &HTTPError{StatusCode: 500}, ClassFailover, HealthReasonAvailability, true},
		{"HE 400", &HTTPError{StatusCode: 400}, ClassStop, HealthReasonConfigurationError, false},
		{"HE 401", &HTTPError{StatusCode: 401}, ClassLongDisable, HealthReasonAuthenticationFailed, true},

		// String fallback (actor-wire serialized errors)
		{"str 429", errors.New("http 429: rate limit"), ClassShortCooldown, HealthReasonRateLimit, true},
		{"str 503", errors.New("http 503: service unavailable"), ClassFailover, HealthReasonAvailability, true},
		{"str 401", errors.New("http 401: unauthorized"), ClassLongDisable, HealthReasonAuthenticationFailed, true},
		{"str 403", errors.New("http 403: forbidden"), ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"str 403_kimi_wire", errors.New("http 403 [access_terminated_error/]: You've reached your usage limit for this billing cycle. Your quota will be refreshed in the next cycle."), ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"str 404", errors.New("http 404: not found"), ClassStop, HealthReasonConfigurationError, false},
		// Relay-wrapped upstream 404 "model has been deprecated" arrives as an
		// outer 400 [server_error]; it must cool the unit instead of stopping
		// the candidate loop.
		{"str 400_relay_deprecated", errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 400 [server_error/]: Error from provider (Console Go): Upstream request failed: [404] This model has been deprecated. It is recommended to migrate to xiaomi/mimo-v2.5"), ClassModelCooldown, HealthReasonModelDeprecated, true},

		// Idle timeout
		{"idle_timeout", ErrStreamIdleTimeout, ClassFailover, HealthReasonAvailability, true},

		// Network errors (provider availability)
		{"connection_refused", errors.New("connection refused"), ClassFailover, HealthReasonAvailability, true},
		{"timeout_headers", errors.New("timeout awaiting response headers"), ClassFailover, HealthReasonAvailability, true},
		{"no_such_host", errors.New("dial tcp: no such host"), ClassFailover, HealthReasonAvailability, true},
		// HTTP/2 keepalive-detected dead pooled connection (LB silently
		// dropped it; next stream-open wrote into a black hole). Must be
		// retryable and rotatable, never cool the unit.
		{"h2_client_connection_lost", errors.New("http2: client connection lost"), ClassFailover, HealthReasonAvailability, true},

		// Unknown error → safe-default failover
		{"unknown", errors.New("mystery boom"), ClassFailover, HealthReasonAvailability, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyStreamOpenError(tt.err)
			if got.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", got.Class, tt.wantClass)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Rotatable != tt.wantRot {
				t.Errorf("rotatable = %v, want %v", got.Rotatable, tt.wantRot)
			}
		})
	}
}

func TestClassifyHTTPError_Matrix(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		code       string
		typ        string
		message    string
		wantClass  StreamOpenClass
		wantReason string
		wantRot    bool
	}{
		{"429", 429, "", "", "", ClassShortCooldown, HealthReasonRateLimit, true},
		{"403_quota", 403, "insufficient_user_quota", "", "", ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"403_access_terminated", 403, "", "access_terminated_error", "", ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"403_quota_msg", 403, "", "", "Your quota will be refreshed in the next cycle", ClassQuotaCooldown, HealthReasonQuotaExhausted, true},
		{"403_empty", 403, "", "", "", ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"403_other", 403, "some_other", "", "", ClassLongDisable, HealthReasonAuthorizationFailed, true},
		{"401", 401, "", "", "", ClassLongDisable, HealthReasonAuthenticationFailed, true},
		{"400", 400, "", "", "", ClassStop, HealthReasonConfigurationError, false},
		{"404", 404, "", "", "", ClassStop, HealthReasonConfigurationError, false},
		{"500", 500, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"502", 502, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"503", 503, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"599", 599, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"302_redirect", 302, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"301_redirect", 301, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"418_other4xx", 418, "", "", "", ClassStop, HealthReasonConfigurationError, false},
		{"451_other4xx", 451, "", "", "", ClassStop, HealthReasonConfigurationError, false},
		{"409_other4xx", 409, "", "", "", ClassStop, HealthReasonConfigurationError, false},
		{"200_default", 200, "", "", "", ClassFailover, HealthReasonAvailability, true},
		{"100_default", 100, "", "", "", ClassFailover, HealthReasonAvailability, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyHTTPError(tt.status, tt.code, tt.typ, tt.message)
			if got.Class != tt.wantClass {
				t.Errorf("class = %s, want %s", got.Class, tt.wantClass)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, tt.wantReason)
			}
			if got.Rotatable != tt.wantRot {
				t.Errorf("rotatable = %v, want %v", got.Rotatable, tt.wantRot)
			}
		})
	}
}

func TestClassifyUpstreamError_StatusCode(t *testing.T) {
	ue := &UpstreamError{StatusCode: 503, Code: "overloaded", Type: "overloaded_error", Message: "overloaded"}
	got := ClassifyUpstreamError(ue)
	if got.StatusCode != 503 {
		t.Errorf("statusCode = %d, want 503", got.StatusCode)
	}
	if got.Class != ClassFailover {
		t.Errorf("class = %s, want %s", got.Class, ClassFailover)
	}
}

func TestStreamOpenClass_String(t *testing.T) {
	if ClassFailover.String() != "failover" {
		t.Errorf("ClassFailover.String() = %q", ClassFailover.String())
	}
	if ClassStop.String() != "stop" {
		t.Errorf("ClassStop.String() = %q", ClassStop.String())
	}
}

func TestClassifyStreamOpenError_NilAndCanceledRotatableFalse(t *testing.T) {
	if got := ClassifyStreamOpenError(nil); got.Rotatable {
		t.Error("nil error should not be rotatable")
	}
	if got := ClassifyStreamOpenError(context.Canceled); got.Rotatable {
		t.Error("context.Canceled should not be rotatable")
	}
}

func TestClassifyStreamOpenError_StopClassNotRotatable(t *testing.T) {
	got := ClassifyStreamOpenError(&UpstreamError{StatusCode: 400})
	if got.Rotatable {
		t.Error("400 should not be rotatable")
	}
}
