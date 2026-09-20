package llmclient

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestExtractHTTPStatus_FromHTTPError(t *testing.T) {
	err := &HTTPError{StatusCode: 429, Body: "rate limited"}
	if got := ExtractHTTPStatus(err); got != 429 {
		t.Fatalf("expected 429, got %d", got)
	}
}

func TestExtractHTTPStatus_FromWrappedHTTPError(t *testing.T) {
	err := fmt.Errorf("aiaggregator.dispatch: stream open: %w", HTTPError{StatusCode: 502, Body: "bad gateway"})
	if got := ExtractHTTPStatus(err); got != 502 {
		t.Fatalf("expected 502, got %d", got)
	}
}

func TestExtractHTTPStatus_FromSerializedError(t *testing.T) {
	cases := []struct {
		msg  string
		want int
	}{
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 400: bad request", 400},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 429: rate limit", 429},
		{"gospore.handler.error: aiaggregator.dispatch: stream open: http 499: client closed", 499},
		{"upstream returned HTTP 503 for model", 503},
		{"some prefix http 404 trailing text", 404},
		{"no status code here", 0},
		{"http abc: not a status", 0},
		{"http 99: invalid status", 0},
		{"http 1000: invalid status", 0},
	}

	for _, tc := range cases {
		err := errors.New(tc.msg)
		if got := ExtractHTTPStatus(err); got != tc.want {
			t.Errorf("ExtractHTTPStatus(%q) = %d, want %d", tc.msg, got, tc.want)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"header timeout", errors.New("Post: timeout awaiting response headers"), true},
		{"idle timeout", ErrStreamIdleTimeout, false},
		{"serialized idle timeout", errors.New("gospore.handler.error: llmclient: stream idle timeout"), false},
		{"cancel", context.Canceled, false},
		{"serialized cancel", errors.New("context canceled"), false},
		{"server error", errors.New("http 503: unavailable"), true},
		{"client error", errors.New("http 400: bad request"), false},
		{"rate limit (structured)", &HTTPError{StatusCode: 429, Body: "rate limit"}, false},
		{"rate limit (serialized)", errors.New("http 429: too many requests"), false},
		{"rate limit try again (structured)", &HTTPError{StatusCode: 429, Body: "try again later"}, true},
		{"rate limit try again (serialized)", errors.New("http 429: Try Again later"), true},
		// Windows WSAETIMEDOUT (10060) during connect/read: net.Error.Timeout()
		// is not reliably true across Go/Windows versions, so we match by op prefix.
		{"wsarecv connect timeout", errors.New("wsarecv: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond"), true},
		{"wsarecv wrapped by url.Error", errors.New(`Post "https://api.example.com/v1/chat/completions": wsarecv: A connection attempt failed because the connected party did not properly respond after a period of time`), true},
		{"wsarecv wrapped by actor chain", errors.New("aiaggregator.dispatch: stream open: wsarecv: a connection attempt failed"), true},
		{"wsasend timeout", errors.New("wsasend: A connection attempt failed"), true},
		// Sanity: an unrelated "wsa..." substring must not match.
		{"unrelated wsa", errors.New("upstream returned wsa-bad-model"), false},
	}
	for _, tc := range cases {
		if got := IsRetryableError(tc.err); got != tc.want {
			t.Errorf("%s: IsRetryableError() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExtractHTTPStatus_Nil(t *testing.T) {
	if got := ExtractHTTPStatus(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
}

func TestIsProviderFailureStatus(t *testing.T) {
	retryable := []int{429, 500, 502, 503, 504, 599}
	for _, code := range retryable {
		if !IsProviderFailureStatus(code) {
			t.Errorf("expected %d to be provider failure", code)
		}
	}

	notRetryable := []int{0, 400, 401, 403, 404, 408, 499}
	for _, code := range notRetryable {
		if IsProviderFailureStatus(code) {
			t.Errorf("expected %d not to be provider failure", code)
		}
	}
}

func TestClassifyErrorCode(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		stopReason string
		want       string
	}{
		{"nil no stop", nil, "", ""},
		{"nil aborted", nil, StopReasonAborted, ErrorCodeContentFilter},
		{"nil stop", nil, StopReasonStop, ""},
		{"429 rate limit", &HTTPError{StatusCode: 429, Body: "rate limited"}, "", ErrorCodeRateLimit},
		{"429 from string", errors.New("http 429: rate limit"), "", ErrorCodeRateLimit},
		{"401 auth", &HTTPError{StatusCode: 401}, "", ErrorCodeAuth},
		{"403 auth", &HTTPError{StatusCode: 403}, "", ErrorCodeAuth},
		{"408 timeout", &HTTPError{StatusCode: 408}, "", ErrorCodeTimeout},
		{"503 overloaded", &HTTPError{StatusCode: 503}, "", ErrorCodeOverloaded},
		{"529 overloaded", &HTTPError{StatusCode: 529}, "", ErrorCodeOverloaded},
		{"500 server", &HTTPError{StatusCode: 500}, "", ErrorCodeServer},
		{"502 server", &HTTPError{StatusCode: 502}, "", ErrorCodeServer},
		{"400 client", &HTTPError{StatusCode: 400}, "", ErrorCodeClient},
		{"404 client", &HTTPError{StatusCode: 404}, "", ErrorCodeClient},
		{"wrapped 429", fmt.Errorf("dispatch: %w", HTTPError{StatusCode: 429}), "", ErrorCodeRateLimit},
		{"deadline exceeded", context.DeadlineExceeded, "", ErrorCodeTimeout},
		{"canceled", context.Canceled, "", ErrorCodeClient},
		{"idle timeout string", ErrStreamIdleTimeout, "", ErrorCodeTimeout},
		{"generic unknown", errors.New("something broke"), "", ErrorCodeUnknown},
		{"error with aborted stop", errors.New("http 400: filtered"), StopReasonAborted, ErrorCodeClient},
		{"nested dispatch failure", errors.New("nested dispatch to \"child\": child stream closed before open"), "", ErrorCodeDispatch},
		{"no callable unit", errors.New("no callable unit for model \"m\" provider \"p\""), "", ErrorCodeDispatch},
		{"token plan exhausted", errors.New("aiaggregator: all matching units have exhausted token plan"), "", ErrorCodeDispatch},
		{"resolve token failure", errors.New("resolve token for provider \"kimi\": key not found"), "", ErrorCodeDispatch},
		{"connection refused", errors.New("dial tcp 1.2.3.4:443: connection refused"), "", ErrorCodeConnection},
		{"no such host", errors.New("dial tcp: lookup api.example.com: no such host"), "", ErrorCodeConnection},
		{"tls error", errors.New("tls: failed to verify certificate"), "", ErrorCodeConnection},
		{"eof", errors.New("EOF"), "", ErrorCodeConnection},
	}
	for _, tc := range cases {
		if got := ClassifyErrorCode(tc.err, tc.stopReason); got != tc.want {
			t.Errorf("%s: ClassifyErrorCode() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
