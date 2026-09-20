package panicprobe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// capturingRef records every Invoke as a serialized payload, then signals a
// channel from Close so tests can deterministically wait for the
// fire-and-forget report goroutine.
type capturingRef struct {
	mu       sync.Mutex
	calls    [][]byte
	closeCh  chan struct{}
	actorID  id.ActorID
}

func newCapturingRef() *capturingRef {
	return &capturingRef{
		closeCh: make(chan struct{}, 16),
		actorID: testutil.GenActorID(),
	}
}

func (c *capturingRef) ID() id.ActorID        { return c.actorID }
func (*capturingRef) Service() (string, bool) { return "oracle", true }
func (c *capturingRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	if callID != "oracle.report_diagnostic" {
		return nil
	}
	var raw []byte
	switch v := payload.(type) {
	case []byte:
		raw = v
	case json.RawMessage:
		raw = v
	default:
		raw, _ = json.Marshal(payload)
	}
	c.mu.Lock()
	c.calls = append(c.calls, raw)
	c.mu.Unlock()
	stream := &fakeStream{}
	call := invoke.NewCall(invoke.CallModeUnary, stream)
	// Signal on close — Guard calls Close() once the Tell-mode invoke returns.
	go func() {
		_ = call.Close()
		c.closeCh <- struct{}{}
	}()
	return call
}

// fakeStream is the minimum invoke.Stream that returns a single nil value.
type fakeStream struct{ closed bool }

func (s *fakeStream) Recv() (any, error)        { return nil, nil }
func (s *fakeStream) RecvRaw() ([]byte, error)  { return nil, nil }
func (s *fakeStream) Close() error              { s.closed = true; return nil }

var _ ref.Ref = (*capturingRef)(nil)

func (c *capturingRef) waitForCall(t *testing.T) []byte {
	t.Helper()
	select {
	case <-c.closeCh:
	case <-time.After(2 * time.Second):
		t.Fatalf("oracle.report_diagnostic never arrived")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		t.Fatalf("no captured calls")
	}
	return c.calls[len(c.calls)-1]
}

// TestGuard_Panic_ReturnsErrorAndReports tests that a panicking fn causes
// Guard to: (a) return a non-nil error whose message contains "panic" and
// the source prefix; (b) fire one oracle.report_diagnostic call with
// Severity="error", Source=source, and a RawData block containing the stack.
func TestGuard_Panic_ReturnsErrorAndReports(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	type req struct {
		Field string `json:"field"`
	}
	type zeroResp struct{}

	resp, err := Guard(ctx, "workspace.test_panic", req{Field: "hello"}, func() (zeroResp, error) {
		panic("boom")
	})

	if err == nil {
		t.Fatalf("expected error from Guard on panic, got nil")
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Errorf("error %q should contain 'panic'", err.Error())
	}
	if !strings.Contains(err.Error(), "workspace.test_panic") {
		t.Errorf("error %q should contain the source prefix", err.Error())
	}
	if resp != (zeroResp{}) {
		t.Errorf("expected zero-value resp on panic, got %+v", resp)
	}

	raw := oracleRef.waitForCall(t)
	var diag domain.OracleReportDiagnosticReq
	if err := json.Unmarshal(raw, &diag); err != nil {
		t.Fatalf("unmarshal diagnostic: %v", err)
	}
	if diag.Severity != "error" {
		t.Errorf("Severity = %q, want error", diag.Severity)
	}
	if diag.Source != "workspace.test_panic" {
		t.Errorf("Source = %q, want workspace.test_panic", diag.Source)
	}
	if !strings.Contains(diag.Message, "boom") {
		t.Errorf("Message %q should contain panic value 'boom'", diag.Message)
	}
	if !strings.Contains(diag.RawData, "--- stack ---") {
		t.Errorf("RawData should contain stack section, got: %s", diag.RawData)
	}
	if !strings.Contains(diag.RawData, "--- request ---") {
		t.Errorf("RawData should contain request section, got: %s", diag.RawData)
	}
	if !strings.Contains(diag.RawData, `"field":"hello"`) {
		t.Errorf("RawData should contain serialized request, got: %s", diag.RawData)
	}
}

// TestGuard_NormalError_PropagatesWithoutReport tests that a fn returning an
// error normally (no panic) propagates the error verbatim AND does NOT fire
// a diagnostic — that path is the caller's responsibility.
func TestGuard_NormalError_PropagatesWithoutReport(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	boom := errors.New("business error")
	_, err := Guard[any, struct{}](ctx, "workspace.test_normal_err", nil, func() (struct{}, error) {
		return struct{}{}, boom
	})

	if !errors.Is(err, boom) {
		t.Errorf("expected err to be the business error, got %v", err)
	}
	select {
	case <-oracleRef.closeCh:
		t.Errorf("diagnostic was sent for a non-panic error path — should not happen")
	case <-time.After(50 * time.Millisecond):
		// Good: no diagnostic fired.
	}
}

// TestGuard_Success_Propagates tests that a fn returning normally has its
// response propagated verbatim.
func TestGuard_Success_Propagates(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	type resp struct{ Value int }
	got, err := Guard[any, resp](ctx, "workspace.test_ok", nil, func() (resp, error) {
		return resp{Value: 42}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != 42 {
		t.Errorf("resp.Value = %d, want 42", got.Value)
	}
	select {
	case <-oracleRef.closeCh:
		t.Errorf("diagnostic was sent for a success path")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestGuard_PanicWhenOracleMissing_StillReturnsError tests that a missing
// oracle service does not break the panic-to-error contract.
func TestGuard_PanicWhenOracleMissing_StillReturnsError(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }

	_, err := Guard[any, struct{}](ctx, "workspace.test_no_oracle", nil, func() (struct{}, error) {
		panic("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected error containing 'panic', got %v", err)
	}
}

// TestGuard_PanicWithNilReq tests that a nil req does not crash the report
// path.
func TestGuard_PanicWithNilReq(t *testing.T) {
	oracleRef := newCapturingRef()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "oracle" {
			return oracleRef, true
		}
		return nil, false
	}

	_, err := Guard[any, struct{}](ctx, "workspace.test_nil_req", nil, func() (struct{}, error) {
		panic("nil-req boom")
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	raw := oracleRef.waitForCall(t)
	if !strings.Contains(string(raw), "nil-req boom") {
		t.Errorf("diagnostic payload missing panic value: %s", raw)
	}
}
