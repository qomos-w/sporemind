package im

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// Fake HTTP transport (injectable RoundTripper, no real API calls)
// ---------------------------------------------------------------------------

type capturedRequest struct {
	Method string
	URL    string
	Body   string
}

// fakeTransport records every request and delegates responses to handler
// (defaulting to an empty successful response).
type fakeTransport struct {
	mu       sync.Mutex
	requests []capturedRequest
	handler  func(r *http.Request, body string) (*http.Response, error)
}

func (f *fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body := ""
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		if err == nil {
			body = string(b)
		}
	}
	f.mu.Lock()
	f.requests = append(f.requests, capturedRequest{Method: r.Method, URL: r.URL.String(), Body: body})
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return fakeJSONResponse(http.StatusOK, `{"ok":true,"result":[]}`), nil
	}
	return handler(r, body)
}

func (f *fakeTransport) calls() []capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]capturedRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// getUpdatesCalls filters recorded calls to the getUpdates endpoint.
func (f *fakeTransport) getUpdatesCalls() []capturedRequest {
	var out []capturedRequest
	for _, c := range f.calls() {
		if strings.HasSuffix(c.URL, "/getUpdates") {
			out = append(out, c)
		}
	}
	return out
}

// sendMessageCalls filters recorded calls to the sendMessage endpoint.
func (f *fakeTransport) sendMessageCalls() []capturedRequest {
	var out []capturedRequest
	for _, c := range f.calls() {
		if strings.HasSuffix(c.URL, "/sendMessage") {
			out = append(out, c)
		}
	}
	return out
}

// blockUntilDone simulates a long poll: it holds the request open until its
// context is cancelled, then fails like a dropped connection.
func blockUntilDone(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

// fakeJSONResponse builds an *http.Response from a status and JSON body.
func fakeJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// newTestTelegramProvider wires the provider to a fake transport and shrinks
// the retry backoff so tests stay fast.
func newTestTelegramProvider(rt *fakeTransport) *telegramProvider {
	p := newTelegramProvider()
	p.client = &http.Client{Transport: rt}
	p.retryBackoff = 5 * time.Millisecond
	return p
}

// fakeClock is a manual clock for deterministic rate-limit tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// ---------------------------------------------------------------------------
// JSON builders
// ---------------------------------------------------------------------------

func tgTextUpdate(updateID, chatID, fromID int64, username, text string) string {
	textField := ""
	if text != "" {
		textField = fmt.Sprintf(`,"text":%q`, text)
	}
	userField := fmt.Sprintf(`"from":{"id":%d}`, fromID)
	if username != "" {
		userField = fmt.Sprintf(`"from":{"id":%d,"username":%q}`, fromID, username)
	}
	return fmt.Sprintf(`{"update_id":%d,"message":{"message_id":9,"date":1700000000,%s,"chat":{"id":%d,"type":"private"}%s}}`,
		updateID, userField, chatID, textField)
}

func tgUpdatesJSON(bodies ...string) string {
	if len(bodies) == 0 {
		return `{"ok":true,"result":[]}`
	}
	return `{"ok":true,"result":[` + strings.Join(bodies, ",") + `]}`
}

const tgSendOK = `{"ok":true,"result":{"message_id":7,"chat":{"id":1,"type":"private"},"date":1,"text":"ok"}}`

// decodeGetUpdatesBody decodes a recorded getUpdates request body.
func decodeGetUpdatesBody(t *testing.T, body string) telegramGetUpdatesReq {
	t.Helper()
	var req telegramGetUpdatesReq
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode getUpdates body: %v (%q)", err, body)
	}
	return req
}

// decodeSendMessageBody decodes a recorded sendMessage request body.
func decodeSendMessageBody(t *testing.T, body string) telegramSendMessageReq {
	t.Helper()
	var req telegramSendMessageReq
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode sendMessage body: %v (%q)", err, body)
	}
	return req
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func runProviderAsync(p *telegramProvider, acc gen.ImAccount, inbound chan<- InboundMessage) (context.CancelFunc, <-chan error) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx, acc, inbound) }()
	return cancel, done
}

// waitRunExit waits for Run to return; it fails the test on timeout.
func waitRunExit(t *testing.T, done <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		t.Fatal("Run did not exit in time")
		return nil
	}
}

// recvInbound receives one message with a timeout, reporting whether it got
// one.
func recvInbound(t *testing.T, ch <-chan InboundMessage, timeout time.Duration) (InboundMessage, bool) {
	t.Helper()
	select {
	case m := <-ch:
		return m, true
	case <-time.After(timeout):
		return InboundMessage{}, false
	}
}

// assertNoInbound fails the test if any message arrives within the timeout.
func assertNoInbound(t *testing.T, ch <-chan InboundMessage, timeout time.Duration) {
	t.Helper()
	if m, ok := recvInbound(t, ch, timeout); ok {
		t.Fatalf("unexpected inbound message: %+v", m)
	}
}

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// Registration / interface surface
// ---------------------------------------------------------------------------

func TestTelegramProviderRegistered(t *testing.T) {
	p, ok := LookupProvider(telegramProviderID)
	if !ok {
		t.Fatal("telegram provider not registered")
	}
	if p.ID() != telegramProviderID || p.Name() != telegramProviderName {
		t.Fatalf("unexpected identity: id=%q name=%q", p.ID(), p.Name())
	}
}

func TestTelegramAllowedGate(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		from  string
		want  bool
	}{
		{"nil allowlist denies everyone", nil, "alice", false},
		{"empty allowlist denies everyone", []string{}, "alice", false},
		{"listed user admitted", []string{"alice"}, "alice", true},
		{"unlisted user denied", []string{"alice"}, "bob", false},
		{"empty sender denied", []string{"alice"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := telegramAllowed(tc.allow, tc.from); got != tc.want {
				t.Errorf("telegramAllowed(%v, %q) = %v, want %v", tc.allow, tc.from, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Probe (getMe)
// ---------------------------------------------------------------------------

func TestTelegramProbeGetMe(t *testing.T) {
	rt := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
		return fakeJSONResponse(http.StatusOK, `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"b","username":"spore_bot"}}`), nil
	}}
	p := newTestTelegramProvider(rt)

	acc := gen.ImAccount{ID: "ia_1", Token: "TOKEN123", APIBase: "https://tg.example.com"}
	if err := p.Probe(context.Background(), acc); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if got := p.BotUsername(); got != "spore_bot" {
		t.Fatalf("BotUsername = %q, want spore_bot", got)
	}
	calls := rt.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 request, got %d", len(calls))
	}
	if calls[0].Method != http.MethodGet {
		t.Errorf("expected GET, got %s", calls[0].Method)
	}
	if calls[0].URL != "https://tg.example.com/botTOKEN123/getMe" {
		t.Errorf("unexpected URL: %s", calls[0].URL)
	}
	if calls[0].Body != "" {
		t.Errorf("GET getMe should have no body, got %q", calls[0].Body)
	}

	// Default API base is used when the account omits APIBase.
	rt2 := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
		return fakeJSONResponse(http.StatusOK, `{"ok":true,"result":{"id":1,"username":"x"}}`), nil
	}}
	p2 := newTestTelegramProvider(rt2)
	if err := p2.Probe(context.Background(), gen.ImAccount{Token: "T"}); err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if got := rt2.calls()[0].URL; got != "https://api.telegram.org/botT/getMe" {
		t.Errorf("unexpected default-base URL: %s", got)
	}
}

func TestTelegramProbeErrors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"http unauthorized", http.StatusUnauthorized, `{"ok":false,"error_code":401,"description":"Unauthorized"}`, "401"},
		{"api error ok false", http.StatusOK, `{"ok":false,"error_code":400,"description":"Bad Request: bad token"}`, "400"},
		{"malformed json", http.StatusOK, `not-json`, "parse response"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
				return fakeJSONResponse(tc.status, tc.body), nil
			}}
			p := newTestTelegramProvider(rt)
			err := p.Probe(context.Background(), gen.ImAccount{Token: "T"})
			if err == nil {
				t.Fatal("expected probe error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}

	// Empty token fails before any request.
	rt := &fakeTransport{}
	p := newTestTelegramProvider(rt)
	if err := p.Probe(context.Background(), gen.ImAccount{}); err == nil {
		t.Fatal("expected error for empty token")
	}
	if len(rt.calls()) != 0 {
		t.Errorf("no request should be issued for an empty token")
	}
}

// ---------------------------------------------------------------------------
// Send (sendMessage)
// ---------------------------------------------------------------------------

func TestTelegramSendRequestShape(t *testing.T) {
	rt := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{Token: "TOKEN", APIBase: "https://tg.example.com"}

	if err := p.Send(context.Background(), acc, "42", "hello <world>"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	calls := rt.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 request, got %d", len(calls))
	}
	c := calls[0]
	if c.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", c.Method)
	}
	if c.URL != "https://tg.example.com/botTOKEN/sendMessage" {
		t.Errorf("unexpected URL: %s", c.URL)
	}
	req := decodeSendMessageBody(t, c.Body)
	if req.ChatID != "42" || req.Text != "hello <world>" {
		t.Errorf("unexpected sendMessage body: %+v", req)
	}
	if !req.DisableWebPagePreview {
		t.Errorf("web preview should be disabled by default: %+v", req)
	}
}

func TestTelegramSendWebPreviewOptional(t *testing.T) {
	rt := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	p.disableWebPreview = false
	if err := p.Send(context.Background(), gen.ImAccount{Token: "T"}, "1", "hi"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	req := decodeSendMessageBody(t, rt.calls()[0].Body)
	if req.DisableWebPagePreview {
		t.Errorf("disable_web_page_preview should be omitted when turned off")
	}
}

func TestTelegramSendValidationAndErrors(t *testing.T) {
	rt := &fakeTransport{}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{Token: "T"}

	if err := p.Send(context.Background(), acc, "", "hi"); err == nil {
		t.Fatal("expected error for empty chat id")
	}
	if err := p.Send(context.Background(), acc, "1", ""); err == nil {
		t.Fatal("expected error for empty text")
	}
	if err := p.Send(context.Background(), gen.ImAccount{}, "1", "hi"); err == nil {
		t.Fatal("expected error for empty token")
	}
	if len(rt.calls()) != 0 {
		t.Errorf("validation errors should not issue requests")
	}

	rt2 := &fakeTransport{handler: func(_ *http.Request, _ string) (*http.Response, error) {
		return fakeJSONResponse(http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"chat not found"}`), nil
	}}
	p2 := newTestTelegramProvider(rt2)
	if err := p2.Send(context.Background(), acc, "9", "hi"); err == nil {
		t.Fatal("expected send error")
	} else if !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Run: long-polling lifecycle
// ---------------------------------------------------------------------------

func TestTelegramRunLifecycleStopDuringLongPoll(t *testing.T) {
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		return blockUntilDone(r)
	}}
	p := newTestTelegramProvider(rt)
	cancel, done := runProviderAsync(p, gen.ImAccount{Token: "T"}, make(chan InboundMessage, 4))

	// Wait until the loop is actually polling.
	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 1 })

	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
	before := len(rt.getUpdatesCalls())
	time.Sleep(100 * time.Millisecond)
	if after := len(rt.getUpdatesCalls()); after != before {
		t.Errorf("loop kept polling after stop: %d -> %d", before, after)
	}
}

func TestTelegramRunLifecycleStopWhileForwarding(t *testing.T) {
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(100, 42, 111, "alice", "hi"))), nil
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	// Unbuffered channel with no reader: Run blocks in the forward send.
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"alice"}}
	cancel, done := runProviderAsync(p, acc, make(chan InboundMessage))

	// Give the loop time to receive the update and block on the send.
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run exited before cancel: %v", err)
	default:
	}

	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunTransientPollErrorRetries(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) <= 2 {
				return fakeJSONResponse(http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"boom"}`), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	cancel, done := runProviderAsync(p, gen.ImAccount{Token: "T"}, make(chan InboundMessage, 4))

	// Transient errors must not terminate the loop: it should keep retrying.
	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 3 })
	select {
	case err := <-done:
		t.Fatalf("Run exited on transient poll error: %v", err)
	default:
	}

	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

// ---------------------------------------------------------------------------
// Run: forwarding + offset cursor
// ---------------------------------------------------------------------------

func TestTelegramRunForwardsTextMessageAndAdvancesOffset(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(100, 42, 111, "alice", "hi there"))), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"alice"}}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	msg, ok := recvInbound(t, inbound, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for inbound message")
	}
	want := InboundMessage{AccountID: "ia_1", ChatID: "42", FromUser: "alice", Text: "hi there"}
	if msg != want {
		t.Fatalf("inbound = %+v, want %+v", msg, want)
	}

	// The next poll carries the offset cursor (last update_id + 1) and the
	// long-poll timeout; the first poll omits the offset.
	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 2 })
	first := decodeGetUpdatesBody(t, rt.getUpdatesCalls()[0].Body)
	second := decodeGetUpdatesBody(t, rt.getUpdatesCalls()[1].Body)
	if first.Offset != 0 || first.Timeout != 50 {
		t.Errorf("first poll body = %+v, want offset 0 timeout 50", first)
	}
	if second.Offset != 101 || second.Timeout != 50 {
		t.Errorf("second poll body = %+v, want offset 101 timeout 50", second)
	}

	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunFromUserFallsBackToNumericID(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				// No username: FromUser falls back to the numeric user id.
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(100, 7, 999, "", "yo"))), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"999"}}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	msg, ok := recvInbound(t, inbound, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for inbound message")
	}
	if msg.FromUser != "999" || msg.ChatID != "7" {
		t.Fatalf("inbound = %+v, want FromUser 999 ChatID 7", msg)
	}
	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunSkipsNonTextUpdates(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				// A message without text and an update without a message:
				// both acknowledged, neither forwarded.
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(
					tgTextUpdate(200, 5, 1, "alice", ""),
					`{"update_id":201}`,
				)), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"alice"}}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 2 })
	assertNoInbound(t, inbound, 100*time.Millisecond)
	// Both updates are acknowledged: the cursor advances past the last one.
	second := decodeGetUpdatesBody(t, rt.getUpdatesCalls()[1].Body)
	if second.Offset != 202 {
		t.Errorf("second poll offset = %d, want 202 (non-text updates acknowledged)", second.Offset)
	}
	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunDefaultDenyEmptyAllowlist(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(300, 55, 111, "alice", "hello"))), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	// No AllowUsers: everyone is denied and answered with the fixed notice.
	acc := gen.ImAccount{ID: "ia_1", Token: "T"}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	waitFor(t, 2*time.Second, func() bool { return len(rt.sendMessageCalls()) >= 1 })
	assertNoInbound(t, inbound, 100*time.Millisecond)

	sends := rt.sendMessageCalls()
	if len(sends) != 1 {
		t.Fatalf("expected exactly 1 rejection notice, got %d", len(sends))
	}
	req := decodeSendMessageBody(t, sends[0].Body)
	if req.ChatID != "55" || req.Text != telegramRejectNotice {
		t.Errorf("rejection notice = %+v, want chat 55 with the fixed text", req)
	}

	// The denied update is still acknowledged.
	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 2 })
	second := decodeGetUpdatesBody(t, rt.getUpdatesCalls()[1].Body)
	if second.Offset != 301 {
		t.Errorf("second poll offset = %d, want 301", second.Offset)
	}
	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunRejectsUnlistedUserSendsNotice(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				// Two different chats so the per-chat rate limit does not
				// interfere: bob is rejected, alice is forwarded.
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(
					tgTextUpdate(400, 10, 222, "bob", "let me in"),
					tgTextUpdate(401, 11, 111, "alice", "hi"),
				)), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"alice"}}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	msg, ok := recvInbound(t, inbound, 2*time.Second)
	if !ok {
		t.Fatal("timed out waiting for alice's message")
	}
	if msg.FromUser != "alice" || msg.ChatID != "11" || msg.Text != "hi" {
		t.Fatalf("inbound = %+v", msg)
	}
	assertNoInbound(t, inbound, 100*time.Millisecond)

	sends := rt.sendMessageCalls()
	if len(sends) != 1 {
		t.Fatalf("expected exactly 1 rejection notice, got %d", len(sends))
	}
	req := decodeSendMessageBody(t, sends[0].Body)
	if req.ChatID != "10" || req.Text != telegramRejectNotice {
		t.Errorf("rejection notice = %+v, want chat 10 with the fixed text", req)
	}
	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunRateLimitPerChat(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1700000000, 0)}
	var polls int32
	// One message per poll; the clock advances between polls so delivery
	// times are deterministic: A at t0, B at t0+0.5s (dropped by the 1/s
	// bucket), C at t0+1.6s (refilled), D at t0+1.6s from another chat
	// (independent bucket).
	deltas := []time.Duration{0, 500 * time.Millisecond, 1100 * time.Millisecond, 0}
	msgs := []struct {
		id, chat int64
	}{
		{500, 1}, {501, 1}, {502, 1}, {503, 2},
	}
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			n := int(atomic.AddInt32(&polls, 1))
			if n <= len(deltas) {
				clock.Advance(deltas[n-1])
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(msgs[n-1].id, msgs[n-1].chat, 1, "u1", fmt.Sprintf("m%d", n)))), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusOK, tgSendOK), nil
	}}
	p := newTestTelegramProvider(rt)
	p.now = clock.Now
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"u1"}}
	inbound := make(chan InboundMessage, 4)
	cancel, done := runProviderAsync(p, acc, inbound)

	waitFor(t, 2*time.Second, func() bool { return len(rt.getUpdatesCalls()) >= 4 })
	time.Sleep(50 * time.Millisecond)

	var got []string
	for {
		if m, ok := recvInbound(t, inbound, 100*time.Millisecond); ok {
			got = append(got, m.Text)
		} else {
			break
		}
	}
	want := []string{"m1", "m3", "m4"}
	if len(got) != len(want) {
		t.Fatalf("forwarded = %v, want %v (m2 must be rate-limited)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("forwarded[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if sends := rt.sendMessageCalls(); len(sends) != 0 {
		t.Errorf("rate limiting must not send rejection notices, got %d sendMessage calls", len(sends))
	}
	cancel()
	if err := waitRunExit(t, done, 2*time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestTelegramRunRejectNoticeFailureStopsLoop(t *testing.T) {
	var polls int32
	rt := &fakeTransport{handler: func(r *http.Request, _ string) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getUpdates") {
			if atomic.AddInt32(&polls, 1) == 1 {
				return fakeJSONResponse(http.StatusOK, tgUpdatesJSON(tgTextUpdate(600, 1, 222, "bob", "hi"))), nil
			}
			return blockUntilDone(r)
		}
		return fakeJSONResponse(http.StatusInternalServerError, `{"ok":false,"error_code":500,"description":"send broken"}`), nil
	}}
	p := newTestTelegramProvider(rt)
	acc := gen.ImAccount{ID: "ia_1", Token: "T", AllowUsers: []string{"alice"}}
	cancel, done := runProviderAsync(p, acc, make(chan InboundMessage, 4))
	defer cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "reject notice") {
			t.Fatalf("Run error = %v, want a reject-notice failure", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after the reject notice failed")
	}
}
