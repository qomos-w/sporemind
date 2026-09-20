package pluginhost

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// cancelableStreamingRecorder is a StreamReverseHandler whose llm.* dispatch
// emits one chunk, then blocks until its DispatchContext's parent ctx (the
// 0x09 cancellation root) fires — the wire-level stand-in for an upstream
// LLM stream that would otherwise run to completion on an abandoned stream.
type cancelableStreamingRecorder struct {
	entered   atomic.Int64
	cancelled atomic.Bool
}

func (r *cancelableStreamingRecorder) Dispatch(callID string, req []byte) ([]byte, error) {
	return []byte(`{}`), nil
}

func (r *cancelableStreamingRecorder) DispatchContextStream(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	if !strings.HasPrefix(callID, "llm.") {
		return r.Dispatch(callID, req)
	}
	r.entered.Add(1)
	if err := onChunk([]byte(`{"kind":"text_delta","text":"hel"}`)); err != nil {
		return nil, err
	}
	if dctx.Parent == nil {
		return nil, fmt.Errorf("streaming dispatch received no cancellation parent")
	}
	<-dctx.Parent.Done()
	r.cancelled.Store(true)
	return nil, fmt.Errorf("upstream aborted: %w", dctx.Parent.Err())
}

// TestFrameSessionReverseCancelAbortsStreamingDispatch pins the 0x09
// reverse-cancel semantics: a plugin-issued cancel frame fires the dispatch's
// cancellation root, the upstream (streaming) dispatch unwinds through its
// DispatchContext.Parent, and the plugin still receives a terminal 0x04
// (the __host_error__ envelope) so the SDK waiter never dangles.
func TestFrameSessionReverseCancelAbortsStreamingDispatch(t *testing.T) {
	rec := &cancelableStreamingRecorder{}
	p := newPipeSessionWithReverse(t, rec)

	// A streaming reverse call with a very long route budget would normally
	// park for the full LLM stream; the 0x09 must cut it short.
	if err := WriteTransportFrame(p.w, MsgReverseReq, "rc1", []byte(`{"callID":"llm.complete","payload":{},"stream":true}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}
	if f := readOneFrame(t, p.r); f == nil || f.Type != MsgReverseChunk {
		t.Fatalf("first reverse chunk never arrived (frame %v)", f)
	}
	if rec.entered.Load() == 0 {
		t.Fatal("dispatch never started before cancel")
	}

	// Plugin cancels: ctx expired or consumer abandoned the stream.
	if err := WriteTransportFrame(p.w, MsgReverseCancel, "rc1", nil); err != nil {
		t.Fatalf("write reverse-cancel: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for rec.cancelled.Load() == false && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rec.cancelled.Load() {
		t.Fatal("upstream dispatch never observed cancellation")
	}

	// The terminal 0x04 still settles the call with a __host_error__ body.
	f := p.readReply(t, 5*time.Second)
	if !strings.Contains(string(f.Payload), "__host_error__") {
		t.Fatalf("terminal payload = %s, want host error envelope", f.Payload)
	}
}

// TestFrameSessionReverseCancelUnknownCallIsDropped pins the advisory nature of
// the 0x09 frame: a cancel for a call that already finished (or never
// existed) must not break the session — later calls keep working.
func TestFrameSessionReverseCancelUnknownCallIsDropped(t *testing.T) {
	p := newPipeSession(t)

	if err := WriteTransportFrame(p.w, MsgReverseCancel, "ghost", nil); err != nil {
		t.Fatalf("write reverse-cancel: %v", err)
	}

	// The session must still answer a normal reverse call afterwards.
	if err := WriteTransportFrame(p.w, MsgReverseReq, "next", []byte(`{"callID":"llm.complete","payload":{}}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}
	f := p.readReply(t, 5*time.Second)
	if f.CallID != "next" {
		t.Fatalf("reply callID = %q, want next", f.CallID)
	}
}
