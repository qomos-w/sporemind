package workspace

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// rawStreamRef is a ref.Ref whose Invoke answers with raw JSON bytes (the
// sporebridge wire), recording the call for assertions.
type rawStreamRef struct {
	actorID id.ActorID

	mu       sync.Mutex
	callID   string
	payload  []byte
	headers  []map[string]string
	response []byte
}

func (r *rawStreamRef) ID() id.ActorID            { return r.actorID }
func (r *rawStreamRef) Service() (string, bool)   { return "", false }
func (r *rawStreamRef) recorded() (string, []byte, []map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callID, r.payload, r.headers
}

func (r *rawStreamRef) Invoke(_ context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	r.mu.Lock()
	r.callID = callID
	body, _ := payload.([]byte)
	r.payload = body
	r.headers = headers
	resp := r.response
	r.mu.Unlock()
	return invoke.NewCall(invoke.CallModeUnary, &rawOnceStream{body: resp})
}

type rawOnceStream struct {
	body     []byte
	consumed bool
}

func (s *rawOnceStream) Recv() (any, error) { return nil, io.EOF }
func (s *rawOnceStream) RecvRaw() ([]byte, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	if s.body == nil {
		return nil, io.EOF
	}
	return s.body, nil
}
func (s *rawOnceStream) Close() error { return nil }

func TestHandleHostCallRelaysToService(t *testing.T) {
	target := &rawStreamRef{
		actorID:  testutil.GenActorID(),
		response: []byte(`{"pong":true}`),
	}
	ctx := testutil.SystemCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "demo" {
			return nil, false
		}
		return target, true
	}

	resp, err := (&Actor{}).handleHostCall(ctx, domain.WorkspaceHostCallReq{
		CallID:  "demo.ping",
		Payload: map[string]any{"q": "hi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result == nil || resp.Result["pong"] != true {
		t.Fatalf("result = %+v, want {pong:true}", resp.Result)
	}
	callID, body, headers := target.recorded()
	if callID != "demo.ping" {
		t.Fatalf("callID = %q, want demo.ping", callID)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(body, &forwarded); err != nil {
		t.Fatalf("forwarded payload = %q: %v", body, err)
	}
	if forwarded["q"] != "hi" {
		t.Fatalf("forwarded payload = %v", forwarded)
	}
	if len(headers) == 0 || headers[0]["gospore.caller_role"] != "system" {
		t.Fatalf("caller role headers = %v, want system", headers)
	}
}

func TestHandleHostCallDeniesAnonymous(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.Identity_ = id.Identity{Role: "anonymous"}
	if _, err := (&Actor{}).handleHostCall(ctx, domain.WorkspaceHostCallReq{CallID: "demo.ping"}); err == nil {
		t.Fatal("anonymous role must be denied")
	}
}

func TestHandleHostCallRequiresCallID(t *testing.T) {
	ctx := testutil.SystemCtx(testutil.GenActorID())
	_, err := (&Actor{}).handleHostCall(ctx, domain.WorkspaceHostCallReq{})
	if err == nil || !strings.Contains(err.Error(), "callId is required") {
		t.Fatalf("err = %v, want callId required", err)
	}
}

func TestHandleHostCallUnresolvableServiceFails(t *testing.T) {
	ctx := testutil.SystemCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }
	// Self() fallback hits a nil-invoke ref; the relay must surface an error.
	if _, err := (&Actor{}).handleHostCall(ctx, domain.WorkspaceHostCallReq{CallID: "nosuch.ping"}); err == nil {
		t.Fatal("unresolvable service must surface an error")
	}
}
