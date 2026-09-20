package sdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// nextReverseReqRaw waits for the next reverse-req frame and returns its
// correlation callID plus the decoded body.
func nextReverseReqRaw(t *testing.T, frames chan procFrame) (string, map[string]any) {
	t.Helper()
	rr := nextFrame(t, frames)
	if rr.typ != msgReverseReq {
		t.Fatalf("frame type = 0x%02x, want reverse-req", rr.typ)
	}
	var rev map[string]any
	if err := json.Unmarshal(rr.payload, &rev); err != nil {
		t.Fatalf("decode reverse-req %q: %v", rr.payload, err)
	}
	return rr.callID, rev
}

// TestProcessTransportInvokeStreamCtxCancelSendsAbortFrame pins the 0x09
// cancellation wire: a streaming reverse call whose consumer context expires
// mid-stream sends a reverse-cancel frame correlated by the same callID, the
// caller returns promptly with the ctx error, and the transport stays alive.
func TestProcessTransportInvokeStreamCtxCancelSendsAbortFrame(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.proccancel", Name: "ProcCancel", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("cancelme", func(req Request) (Response, error) {
				ch, ok := ctx.Host().(CanceledHost)
				if !ok {
					return Response{}, errNotCanceledHost
				}
				cctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				_, err := ch.InvokeStreamCtx(cctx, "llm.complete", map[string]any{"prompt": "hi"}, func(chunk []byte) error {
					return nil
				})
				if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
					return Response{}, errWantCtx{err}
				}
				return Response{Payload: map[string]any{"cancelled": true, "err": err.Error()}}, nil
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	if f := nextFrame(t, frames); f.typ != msgInvokeResp {
		t.Fatalf("onLoad frame = 0x%02x, want invoke-resp", f.typ)
	}

	sendInvoke(t, stdinW, "cancelme", []byte(`{}`))
	cid, _ := nextReverseReqRaw(t, frames)

	// The host (this test) never answers the reverse call: the plugin's ctx
	// must expire and emit the 0x09 abort frame instead of hanging.
	f := nextFrame(t, frames)
	if f.typ != msgReverseCancel {
		t.Fatalf("frame = 0x%02x, want reverse-cancel", f.typ)
	}
	if f.callID != cid {
		t.Fatalf("cancel callID = %q, want %q", f.callID, cid)
	}

	// The handler surfaces the ctx error to its own caller; the terminal
	// 0x04 from the host (sent below) settles the abandoned waiter.
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"late"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame = 0x%02x, want invoke-resp", ir.typ)
	}
	if !strings.Contains(string(ir.payload), `"cancelled":true`) {
		t.Fatalf("invoke-resp = %s, want cancelled handler result", ir.payload)
	}
}

// TestProcessTransportInvokeStreamCtxOnChunkErrorAborts pins the consumer
// abandoned-stream side: an onChunk error also emits the 0x09 abort frame so
// the host stops the upstream dispatch, while the returned error keeps the
// onChunk error verbatim (the legacy InvokeStream contract).
func TestProcessTransportInvokeStreamCtxOnChunkErrorAborts(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.proccancel2", Name: "ProcCancel2", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("abortme", func(req Request) (Response, error) {
				ch := ctx.Host().(CanceledHost)
				_, err := ch.InvokeStreamCtx(context.Background(), "llm.complete", map[string]any{}, func(chunk []byte) error {
					return errConsumerGaveUp
				})
				if err == nil || err.Error() != errConsumerGaveUp.Error() {
					return Response{}, errWantCtx{err}
				}
				return Response{Payload: map[string]any{"aborted": true}}, nil
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	if f := nextFrame(t, frames); f.typ != msgInvokeResp {
		t.Fatalf("onLoad frame = 0x%02x, want invoke-resp", f.typ)
	}

	sendInvoke(t, stdinW, "abortme", []byte(`{}`))
	cid, _ := nextReverseReqRaw(t, frames)

	if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(`{"kind":"text_delta","data":{"text":"x"}}`)); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	f := nextFrame(t, frames)
	if f.typ != msgReverseCancel {
		t.Fatalf("frame = 0x%02x, want reverse-cancel after onChunk error", f.typ)
	}
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"late"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame = 0x%02x, want invoke-resp", ir.typ)
	}
	if !strings.Contains(string(ir.payload), `"aborted":true`) {
		t.Fatalf("invoke-resp = %s, want aborted handler result", ir.payload)
	}
}

var errNotCanceledHost = &staticErr{"host transport does not implement CanceledHost"}
var errConsumerGaveUp = &staticErr{"consumer gave up"}

type staticErr struct{ msg string }

func (e *staticErr) Error() string { return e.msg }

type errWantCtx struct{ err error }

func (e errWantCtx) Error() string { return "want ctx error, got: " + e.err.Error() }