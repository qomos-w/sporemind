package llmclient

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsImageInputUnsupported_Matrix(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		code       string
		typ        string
		message    string
		want       bool
	}{
		{"observed openai-relay 400", 400, "", "invalid_request_error", "this model does not support image input", true},
		{"wrapped 400 with prefix noise", 400, "", "", "Error from provider: this model does not support image input", true},
		{"images plural", 400, "", "", "images are not supported with this model", true},
		{"multimodal", 404, "", "", "This model does not support multimodal input", true},
		{"chinese marker", 400, "", "", "该模型不支持图片输入", true},
		{"chinese relay content.type", 400, "", "", "messages.content.type 参数非法，取值范围 ['text']", true},
		{"chinese 非图像模型", 400, "", "", "非图像模型接管图像模型", true},
		{"plain 400 no marker", 400, "", "invalid_request_error", "invalid request body", false},
		{"marker but wrong status", 429, "", "", "this model does not support image input", false},
		{"marker but 500", 500, "", "", "this model does not support image input", false},
		{"empty message", 400, "", "", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsImageInputUnsupported(tt.statusCode, tt.code, tt.typ, tt.message); got != tt.want {
				t.Errorf("IsImageInputUnsupported(%d, %q, %q, %q) = %v, want %v",
					tt.statusCode, tt.code, tt.typ, tt.message, got, tt.want)
			}
		})
	}
}

func TestIsImageInputUnsupportedErr_Forms(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"structured upstream", &UpstreamError{StatusCode: 400, Type: "invalid_request_error", Message: "this model does not support image input"}, true},
		{"structured other 400", &UpstreamError{StatusCode: 400, Message: "invalid request body"}, false},
		// Actor-wire string form: the concrete type is lost but the
		// "http N [type/code]: message" rendering survives.
		{"string form", errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 400 [invalid_request_error/]: this model does not support image input"), true},
		{"string form other 400", errors.New("http 400 [invalid_request_error/]: max_tokens is too large"), false},
		{"string form relay content.type", errors.New("http 400 [/1210]: messages.content.type 参数非法，取值范围 ['text']"), true},
		{"string form no status", errors.New("this model does not support image input"), false},
		{"plain error", errors.New("mystery boom"), false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsImageInputUnsupportedErr(tt.err); got != tt.want {
				t.Errorf("IsImageInputUnsupportedErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestParseProviderErrorBody_OpenAI(t *testing.T) {
	cases := []struct {
		name            string
		body            string
		wantCode        string
		wantType        string
		wantMessage     string
		wantMessageOnly bool
	}{
		{
			name:        "full error",
			body:        `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`,
			wantCode:    "rate_limit_exceeded",
			wantType:    "rate_limit_error",
			wantMessage: "Rate limit exceeded",
		},
		{
			name:        "insufficient quota",
			body:        `{"error":{"message":"You exceeded your current quota","type":"insufficient_quota","code":"insufficient_quota"}}`,
			wantCode:    "insufficient_quota",
			wantType:    "insufficient_quota",
			wantMessage: "You exceeded your current quota",
		},
		{
			name:        "null code",
			body:        `{"error":{"message":"Invalid request","type":"invalid_request_error","code":null}}`,
			wantCode:    "",
			wantType:    "invalid_request_error",
			wantMessage: "Invalid request",
		},
		{
			name:        "missing code and type",
			body:        `{"error":{"message":"Unknown error"}}`,
			wantCode:    "",
			wantType:    "",
			wantMessage: "Unknown error",
		},
		{
			name:            "non-json body",
			body:            "Internal Server Error",
			wantCode:        "",
			wantType:        "",
			wantMessage:     "Internal Server Error",
			wantMessageOnly: true,
		},
		{
			name:            "empty body",
			body:            "",
			wantCode:        "",
			wantType:        "",
			wantMessage:     "",
			wantMessageOnly: true,
		},
		{
			name:        "empty json object",
			body:        `{}`,
			wantCode:    "",
			wantType:    "",
			wantMessage: "",
		},
		{
			name:        "whitespace trimmed",
			body:        `  {"error":{"message":"  trim me  "}}  `,
			wantCode:    "",
			wantType:    "",
			wantMessage: "trim me",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, typ, msg := parseProviderErrorBody([]byte(tc.body))
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if typ != tc.wantType {
				t.Errorf("type = %q, want %q", typ, tc.wantType)
			}
			if msg != tc.wantMessage {
				t.Errorf("message = %q, want %q", msg, tc.wantMessage)
			}
		})
	}
}

func TestParseProviderErrorBody_Anthropic(t *testing.T) {
	body := `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	code, typ, msg := parseProviderErrorBody([]byte(body))
	if code != "" {
		t.Errorf("anthropic code should be empty, got %q", code)
	}
	if typ != "overloaded_error" {
		t.Errorf("anthropic type = %q, want %q", typ, "overloaded_error")
	}
	if msg != "Overloaded" {
		t.Errorf("anthropic message = %q, want %q", msg, "Overloaded")
	}
}

func TestParseProviderErrorBody_Gemini(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantType    string
		wantMessage string
	}{
		{
			name:        "rate limit",
			body:        `{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED"}}`,
			wantType:    "RESOURCE_EXHAUSTED",
			wantMessage: "Resource has been exhausted",
		},
		{
			name:        "permission denied",
			body:        `{"error":{"code":403,"message":"Permission denied","status":"PERMISSION_DENIED"}}`,
			wantType:    "PERMISSION_DENIED",
			wantMessage: "Permission denied",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, typ, msg := parseProviderErrorBody([]byte(tc.body))
			if code != "" {
				t.Errorf("gemini code should be empty (numeric skipped), got %q", code)
			}
			if typ != tc.wantType {
				t.Errorf("type = %q, want %q", typ, tc.wantType)
			}
			if msg != tc.wantMessage {
				t.Errorf("message = %q, want %q", msg, tc.wantMessage)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"delta seconds", "120", 120 * time.Second},
		{"zero", "0", 0},
		{"negative delta", "-5", 0},
		{"whitespace", "  60  ", 60 * time.Second},
		{"empty", "", 0},
		{"invalid", "soon", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header)
			if got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	// Future date: should return a positive duration approximately equal to
	// the offset from now.
	future := time.Now().Add(time.Hour).UTC()
	header := future.Format(time.RFC1123)
	got := parseRetryAfter(header)
	if got <= 0 {
		t.Fatalf("expected positive duration for future date, got %v", got)
	}
	// Allow a few seconds of clock skew.
	diff := got - time.Hour
	if diff < 0 {
		diff = -diff
	}
	if diff > 5*time.Second {
		t.Errorf("duration %v too far from 1h", got)
	}

	// Past date: should return zero, not negative.
	past := time.Now().Add(-time.Hour).UTC()
	gotPast := parseRetryAfter(past.Format(time.RFC1123))
	if gotPast != 0 {
		t.Errorf("past date should yield 0, got %v", gotPast)
	}
}

func TestUpstreamError_Error(t *testing.T) {
	cases := []struct {
		name string
		err  *UpstreamError
		want string
	}{
		{
			name: "with message",
			err:  &UpstreamError{StatusCode: 429, Message: "Rate limited"},
			want: "http 429: Rate limited",
		},
		{
			name: "with code and type",
			err:  &UpstreamError{StatusCode: 429, Code: "rate_limit_exceeded", Type: "rate_limit_error", Message: "Rate limited"},
			want: "http 429 [rate_limit_error/rate_limit_exceeded]: Rate limited",
		},
		{
			name: "no message uses status text",
			err:  &UpstreamError{StatusCode: 500},
			want: "http 500: Internal Server Error",
		},
		{
			name: "unknown status with no message",
			err:  &UpstreamError{StatusCode: 999},
			want: "http 999: upstream error",
		},
		{
			name: "type only",
			err:  &UpstreamError{StatusCode: 503, Type: "overloaded_error"},
			want: "http 503 [overloaded_error/]: Service Unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Errorf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUpstreamError_ErrorsAs(t *testing.T) {
	inner := &UpstreamError{
		StatusCode: 429,
		Code:       "rate_limit_exceeded",
		Type:       "rate_limit_error",
		Message:    "Rate limited",
		RetryAfter: 60 * time.Second,
	}
	wrapped := fmt.Errorf("stream open: %w", inner)

	var got *UpstreamError
	if !errors.As(wrapped, &got) {
		t.Fatal("errors.As did not find *UpstreamError")
	}
	if got.StatusCode != 429 || got.Code != "rate_limit_exceeded" || got.Type != "rate_limit_error" || got.RetryAfter != 60*time.Second {
		t.Errorf("extracted UpstreamError mismatch: %+v", got)
	}
}

func TestExtractHTTPStatus_UpstreamError(t *testing.T) {
	direct := &UpstreamError{StatusCode: 429, Message: "Rate limited"}
	if got := ExtractHTTPStatus(direct); got != 429 {
		t.Errorf("direct UpstreamError: got %d, want 429", got)
	}

	wrapped := fmt.Errorf("dispatch: %w", &UpstreamError{StatusCode: 503})
	if got := ExtractHTTPStatus(wrapped); got != 503 {
		t.Errorf("wrapped UpstreamError: got %d, want 503", got)
	}
}

func TestExtractHTTPStatus_FallsBackToStringAfterUpstream(t *testing.T) {
	// If an UpstreamError is serialized to a string and re-wrapped, the
	// string fallback should still extract the status.
	serialized := errors.New("upstream failed: http 418: I'm a teapot")
	if got := ExtractHTTPStatus(serialized); got != 418 {
		t.Errorf("string fallback: got %d, want 418", got)
	}
}

func TestUpstreamErrorFromResponse(t *testing.T) {
	body := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
	rec := httptest.NewRecorder()
	rec.Header().Set("Retry-After", "90")
	rec.WriteHeader(http.StatusTooManyRequests)
	rec.WriteString(body)

	resp := rec.Result()
	err := UpstreamErrorFromResponse(resp, []byte(body))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", err.StatusCode)
	}
	if err.Code != "rate_limit_exceeded" {
		t.Errorf("Code = %q, want %q", err.Code, "rate_limit_exceeded")
	}
	if err.Type != "rate_limit_error" {
		t.Errorf("Type = %q, want %q", err.Type, "rate_limit_error")
	}
	if err.Message != "Rate limit exceeded" {
		t.Errorf("Message = %q, want %q", err.Message, "Rate limit exceeded")
	}
	if err.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %v, want %v", err.RetryAfter, 90*time.Second)
	}
}

func TestUpstreamErrorFromResponse_NoRetryAfter(t *testing.T) {
	body := `{"error":{"message":"Bad request"}}`
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusBadRequest)
	rec.WriteString(body)

	resp := rec.Result()
	err := UpstreamErrorFromResponse(resp, []byte(body))
	if err.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0", err.RetryAfter)
	}
	if err.Code != "" || err.Type != "" {
		t.Errorf("unexpected structured fields: %+v", err)
	}
}

func TestOpenAIClientReturnsUpstreamError(t *testing.T) {
	body := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(body))
	}))
	defer server.Close()

	c := NewOpenAIClient(server.URL, "k")
	c.HTTPClient = server.Client()

	_, err := c.Stream(t.Context(), Request{Model: "m", UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("expected *UpstreamError, got %T: %v", err, err)
	}
	if upstreamErr.StatusCode != 429 {
		t.Errorf("StatusCode = %d, want 429", upstreamErr.StatusCode)
	}
	if upstreamErr.Code != "rate_limit_exceeded" {
		t.Errorf("Code = %q, want %q", upstreamErr.Code, "rate_limit_exceeded")
	}
	if upstreamErr.RetryAfter != 120*time.Second {
		t.Errorf("RetryAfter = %v, want %v", upstreamErr.RetryAfter, 120*time.Second)
	}
}

func TestAnthropicClientReturnsUpstreamError(t *testing.T) {
	body := `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(body))
	}))
	defer server.Close()

	c := NewAnthropicClient(server.URL, "k")
	c.HTTPClient = server.Client()

	_, err := c.Stream(t.Context(), Request{Model: "m", UserText: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	var upstreamErr *UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("expected *UpstreamError, got %T: %v", err, err)
	}
	if upstreamErr.StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", upstreamErr.StatusCode)
	}
	if upstreamErr.Type != "overloaded_error" {
		t.Errorf("Type = %q, want %q", upstreamErr.Type, "overloaded_error")
	}
	if upstreamErr.Code != "" {
		t.Errorf("Anthropic Code should be empty, got %q", upstreamErr.Code)
	}
}

func TestIsQuotaExhausted(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		code    string
		typ     string
		message string
		want    bool
	}{
		{"openai code", 403, "insufficient_user_quota", "", "", true},
		{"kimi type", 403, "", "access_terminated_error", "", true},
		{"case insensitive code", 403, "Insufficient_Quota", "", "", true},
		{"usage limit message", 403, "", "", "You've reached your usage limit for this billing cycle", true},
		{"quota message", 403, "", "", "Your quota will be refreshed in the next cycle", true},
		{"chinese message", 403, "", "", "当前账户额度已用完", true},
		{"non-403 code ignored", 401, "insufficient_user_quota", "", "", false},
		{"plain 403", 403, "", "", "forbidden", false},
		{"403 permission", 403, "permission_denied", "", "access denied", false},
		// Providers also deliver account-level exhaustion as 429 (token-plan
		// / usage-window limits): a quota signal on 429 must classify as
		// quota (long cooldown), not ride the short rate-limit window.
		{"429 quota message", 429, "", "", "quota exceeded", true},
		{"429 usage-window message", 429, "", "", "已达到 5 小时的使用上限", true},
		{"429 plain rate limit", 429, "", "", "rate limit exceeded, retry later", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsQuotaExhausted(tc.status, tc.code, tc.typ, tc.message); got != tc.want {
				t.Errorf("IsQuotaExhausted(%d, %q, %q, %q) = %v, want %v",
					tc.status, tc.code, tc.typ, tc.message, got, tc.want)
			}
		})
	}
}

func TestIsModelDeprecated(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		code    string
		typ     string
		message string
		want    bool
	}{
		{"direct 404 message", 404, "", "", "This model has been deprecated. It is recommended to migrate to xiaomi/mimo-v2.5", true},
		{"relay-wrapped 400 message", 400, "", "server_error", "Error from provider (Console Go): Upstream request failed: [404] This model has been deprecated", true},
		{"structured code", 400, "model_deprecated", "", "ignored", true},
		{"case-insensitive type", 404, "", "Model_Deprecated", "", true},
		{"plain 400", 400, "", "", "invalid request: missing messages", false},
		{"plain 404", 404, "", "", "not found", false},
		{"non-400/404 status ignored", 429, "", "", "This model has been deprecated", false},
		{"500 status ignored", 500, "model_deprecated", "", "", false},
		{"unrelated deprecated mention", 400, "", "", "the stream_options parameter is deprecated", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsModelDeprecated(tc.status, tc.code, tc.typ, tc.message); got != tc.want {
				t.Errorf("IsModelDeprecated(%d, %q, %q, %q) = %v, want %v",
					tc.status, tc.code, tc.typ, tc.message, got, tc.want)
			}
		})
	}
}

func TestExtractErrorCodeType(t *testing.T) {
	// Structured error takes precedence.
	typ, code := ExtractErrorCodeType(&UpstreamError{StatusCode: 403, Type: "access_terminated_error"})
	if typ != "access_terminated_error" || code != "" {
		t.Errorf("structured: got (%q, %q)", typ, code)
	}
	// Wire-serialized string fallback.
	wireErr := errors.New("http 403 [access_terminated_error/]: usage limit reached")
	typ, code = ExtractErrorCodeType(wireErr)
	if typ != "access_terminated_error" || code != "" {
		t.Errorf("wire: got (%q, %q)", typ, code)
	}
	wireBoth := errors.New("http 429 [rate_limit_error/rate_limit_exceeded]: slow down")
	typ, code = ExtractErrorCodeType(wireBoth)
	if typ != "rate_limit_error" || code != "rate_limit_exceeded" {
		t.Errorf("wire both: got (%q, %q)", typ, code)
	}
	// No pattern.
	typ, code = ExtractErrorCodeType(errors.New("http 403: forbidden"))
	if typ != "" || code != "" {
		t.Errorf("plain: got (%q, %q), want empty", typ, code)
	}
	if typ, code = ExtractErrorCodeType(nil); typ != "" || code != "" {
		t.Errorf("nil: got (%q, %q), want empty", typ, code)
	}
}
