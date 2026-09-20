package pluginhost

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sessionReverseRecorder adapts eventRecorder to the ReverseHandler interface.
type sessionReverseRecorder struct{ rec *eventRecorder }

func (r sessionReverseRecorder) Dispatch(callID string, req []byte) ([]byte, error) {
	return r.rec.dispatch(callID, req)
}

// streamingReverseRecorder is a ReverseHandler that also implements
// StreamReverseHandler: llm.* calls deliver two ordered chunks then the
// terminal value; non-llm calls degrade to unary dispatch.
type streamingReverseRecorder struct{ rec *eventRecorder }

func (r streamingReverseRecorder) Dispatch(callID string, req []byte) ([]byte, error) {
	return r.rec.dispatch(callID, req)
}

func (r streamingReverseRecorder) DispatchContextStream(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	if !strings.HasPrefix(callID, "llm.") {
		return r.rec.dispatch(callID, req)
	}
	if err := onChunk([]byte(`{"kind":"text_delta","text":"hello, "}`)); err != nil {
		return nil, err
	}
	if err := onChunk([]byte(`{"kind":"text_delta","text":"world"}`)); err != nil {
		return nil, err
	}
	return []byte(`{"Text":"hello, world"}`), nil
}

// pipeSession wires a frameSession to a raw duplex pair: the test writes
// plugin-side frames into w and reads host->plugin frames from r2.
type pipeSession struct {
	s   *frameSession
	w   io.Writer // test -> session.read (plugin stdout)
	r   io.Reader // session.write -> test (plugin stdin)
	rec *eventRecorder
}

func newPipeSession(t *testing.T) *pipeSession {
	return newPipeSessionWithReverse(t, nil)
}

// newPipeSessionWithReverse builds a pipeSession with an optional injected
// reverse handler (nil = the default eventRecorder-backed one).
func newPipeSessionWithReverse(t *testing.T, reverse ReverseHandler) *pipeSession {
	t.Helper()
	pluginToHostR, pluginToHostW := io.Pipe()
	hostToPluginR, hostToPluginW := io.Pipe()
	rec := &eventRecorder{}
	if reverse == nil {
		reverse = sessionReverseRecorder{rec}
	}
	s := newFrameSession(frameSessionConfig{
		PluginID: "test.session",
		Read:     pluginToHostR,
		Write:    hostToPluginW,
		Reverse:  reverse,
		OnLog:    rec.logRaw,
		OnFatal:  func(error) {},
	})
	s.start()
	t.Cleanup(func() {
		_ = pluginToHostW.Close()
		_ = hostToPluginR.Close()
		s.close()
	})
	return &pipeSession{s: s, w: pluginToHostW, r: hostToPluginR, rec: rec}
}

// readReply skips host->plugin invoke-req echo frames (a test's own session
// call writes 0x01 synchronously) and returns the next reverse-resp.
func (p *pipeSession) readReply(t *testing.T, timeout time.Duration) Frame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("timeout waiting for reverse-resp")
		}
		f := p.readFrameTimeout(t, remain)
		if f.Type == MsgInvokeReq {
			continue // the test's own in-flight call racing the reader
		}
		if f.Type != MsgReverseResp {
			t.Fatalf("frame = {type:0x%02x callID:%q}, want reverse-resp", f.Type, f.CallID)
		}
		return f
	}
}

func (p *pipeSession) readFrameTimeout(t *testing.T, timeout time.Duration) Frame {
	t.Helper()
	type res struct {
		f   Frame
		err error
	}
	ch := make(chan res, 1)
	go func() {
		f, err := ReadTransportFrame(p.r)
		ch <- res{f, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("read host->plugin frame: %v", r.err)
		}
		return r.f
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for host->plugin frame")
		return Frame{}
	}
}

func (r *eventRecorder) logRaw(payload []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "log: "+truncateForLog(payload))
}

func TestFrameSessionBackgroundReverseDispatch(t *testing.T) {
	p := newPipeSession(t)

	// Invoke in flight (the session's caller is a test goroutine).
	invokeDone := make(chan []byte, 1)
	go func() {
		resp, err := p.s.call(context.Background(), []byte(`{"Callable":"greet"}`))
		if err != nil {
			t.Errorf("call: %v", err)
		}
		invokeDone <- resp
	}()

	// A correlated reverse-req "from a background goroutine" — possibly
	// while the invoke is in flight.
	if err := WriteTransportFrame(p.w, MsgReverseReq, "bg1", []byte(`{"callID":"llm.complete","payload":{}}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}

	// The session must answer with a correlated reverse-resp.
	f := p.readReply(t, 5*time.Second)
	if f.Type != MsgReverseResp || f.CallID != "bg1" {
		t.Fatalf("frame = {type:0x%02x callID:%q}, want correlated reverse-resp bg1", f.Type, f.CallID)
	}
	if !strings.Contains(string(f.Payload), `"ok":true`) {
		t.Fatalf("reverse-resp payload = %s, want dispatch result", f.Payload)
	}

	// Deliver the invoke response afterwards; the invoke completes.
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"message":"hi"}`)); err != nil {
		t.Fatalf("write invoke-resp: %v", err)
	}
	select {
	case resp := <-invokeDone:
		if string(resp) != `{"message":"hi"}` {
			t.Fatalf("invoke resp = %s", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invoke never completed")
	}
}

// TestFrameSessionQueuedInvokeFailsFastOnBudget pins the invokeMu starvation
// fix: a second invoke queued behind a long-running one (single-flight forward
// wire) must fail at admission once its own budget expires, instead of
// acquiring the lock with a burnt deadline and surfacing the failure from
// inside its first reverse call as a misleading "reverse call <id>: context
// deadline exceeded". Reproduces the novel-app report where novel_debug_llm
// failed near turn end after a long novel_chapter_generate held the session.
func TestFrameSessionQueuedInvokeFailsFastOnBudget(t *testing.T) {
	p := newPipeSession(t)

	// First invoke runs (holds invokeMu); we never answer its 0x02 yet.
	firstDone := make(chan error, 1)
	go func() {
		_, err := p.s.call(context.Background(), []byte(`{"Callable":"long"}`))
		firstDone <- err
	}()
	if f := readOneFrame(t, p.r); f == nil || f.Type != MsgInvokeReq {
		t.Fatalf("first invoke-req never reached the plugin pipe")
	}

	// Second invoke queues behind it with a short budget.
	ctx, cancel := context.WithTimeout(context.Background(), 50*invokeMuPoll)
	defer cancel()
	start := time.Now()
	_, err := p.s.call(ctx, []byte(`{"Callable":"queued"}`))
	if err == nil {
		t.Fatal("queued invoke must fail once its budget expires")
	}
	if !strings.Contains(err.Error(), "queued behind a running invoke") {
		t.Fatalf("error must name the starvation cause, got: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error must wrap context.DeadlineExceeded, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("queued invoke failed far too late (%v); must fail within ~budget", elapsed)
	}

	// Releasing the first invoke must leave the session usable: a fresh
	// invoke acquires the lock and completes normally.
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write invoke-resp: %v", err)
	}
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first invoke never completed")
	}
	afterDone := make(chan []byte, 1)
	go func() {
		resp, err := p.s.call(context.Background(), []byte(`{"Callable":"after"}`))
		if err != nil {
			afterDone <- nil
			return
		}
		afterDone <- resp
	}()
	if f := readOneFrame(t, p.r); f == nil {
		t.Fatal("post-starvation invoke never wrote its request")
	}
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"after":true}`)); err != nil {
		t.Fatalf("write after-resp: %v", err)
	}
	select {
	case resp := <-afterDone:
		if string(resp) != `{"after":true}` {
			t.Fatalf("post-starvation invoke resp = %s", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-starvation invoke never completed")
	}
}

// TestFrameSessionExpiredCtxNeverAdmitted pins the admission invariant behind
// the novelking "killed on invoke timeout" crash: a caller whose budget is
// already spent must fail as *queued* (non-fatal) even when the single-flight
// token is free, and must hand the token back. Before the fix the token was a
// mutex taken before the ctx was re-checked, so a queued invoke that won the
// lock exactly as its deadline fired returned a bare context error — which the
// opener classifies as a hung plugin and answers by killing the process. Under
// the step-event fan-out a 30s backlog made that race routine.
func TestFrameSessionExpiredCtxNeverAdmitted(t *testing.T) {
	p := newPipeSession(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // budget already spent at admission

	err := p.s.acquireInvoke(ctx)
	if err == nil {
		t.Fatal("expired context must not be admitted")
	}
	if !errors.Is(err, errInvokeQueued) {
		t.Fatalf("expired-admission error must be classified queued, got: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error must still wrap the ctx cause, got: %v", err)
	}

	// The token must be handed back: a fresh invoke with a live context works.
	good, goodCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer goodCancel()
	done := make(chan error, 1)
	go func() {
		_, err := p.s.call(good, []byte(`{"Callable":"after"}`))
		done <- err
	}()
	if f := readOneFrame(t, p.r); f == nil || f.Type != MsgInvokeReq {
		t.Fatal("invoke after expired admission never wrote its request")
	}
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("write invoke-resp: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("post-admission invoke failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-admission invoke never completed")
	}
}

// TestReverseCallNotBoundToMetalessEventInvoke pins the __event__ fan-out fix:
// a reverse call issued while a META-LESS framed invoke (an event delivery —
// handleEventDeliver builds its ctx without WithInvokeMeta) is in flight must
// run on the standalone budget, never inherit that invoke's context. The event
// invoke returns (and its caller cancels its ctx) within milliseconds, so the
// old inheritance canceled unrelated HTTP-data-path reverse calls whenever any
// agent was streaming — every panel data load failed with
// reverse call "state.get": context canceled.
func TestReverseCallNotBoundToMetalessEventInvoke(t *testing.T) {
	release := make(chan struct{})
	blocking := reverseFunc(func(callID string, req []byte) ([]byte, error) {
		<-release // hold the dispatch until the event invoke has returned+released
		return []byte(`{"ok":true}`), nil
	})
	p := newPipeSessionWithReverse(t, blocking)

	// Meta-less invoke in flight, mirroring handleEventDeliver's shape.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	callDone := make(chan error, 1)
	go func() {
		_, err := p.s.call(ctx, []byte(`{"Callable":"__event__:step"}`))
		callDone <- err
	}()
	if f := readOneFrame(t, p.r); f == nil || f.Type != MsgInvokeReq {
		t.Fatal("invoke-req never reached the plugin pipe")
	}

	// Reverse call arrives while the event invoke is still in flight …
	if err := WriteTransportFrame(p.w, MsgReverseReq, "corr1", []byte(`{"callID":"state.get","payload":"e30="}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}
	// … then the event invoke completes and its caller releases the ctx.
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{}`)); err != nil {
		t.Fatalf("write invoke-resp: %v", err)
	}
	select {
	case <-callDone:
	case <-time.After(5 * time.Second):
		t.Fatal("event invoke never completed")
	}
	cancel()
	time.Sleep(50 * time.Millisecond) // let the cancellation (if inherited) land
	close(release)

	reply := p.readReply(t, 5*time.Second)
	if strings.Contains(string(reply.Payload), "context canceled") {
		t.Fatalf("reverse call was bound to the completed event invoke's ctx: %s", reply.Payload)
	}
	if !strings.Contains(string(reply.Payload), `"ok":true`) {
		t.Fatalf("reverse reply = %s, want the handler's success payload", reply.Payload)
	}
}

// reverseFunc adapts a plain function to the ReverseHandler interface.
type reverseFunc func(callID string, req []byte) ([]byte, error)

func (f reverseFunc) Dispatch(callID string, req []byte) ([]byte, error) {
	return f(callID, req)
}

// readOneFrame reads exactly one transport frame with a timeout, tolerating
// the io.Pipe's lack of deadlines.
func readOneFrame(t *testing.T, r io.Reader) *Frame {
	t.Helper()
	type frameResult struct {
		f  Frame
		ok bool
	}
	ch := make(chan frameResult, 1)
	go func() {
		f, err := ReadTransportFrame(r)
		ch <- frameResult{f, err == nil}
	}()
	select {
	case res := <-ch:
		if res.ok {
			return &res.f
		}
		return nil
	case <-time.After(5 * time.Second):
		return nil
	}
}

// TestFrameSessionReverseErrorSurfacedToHostCallback pins the issue-2
// diagnostic path: a failed host-side reverse dispatch (eviction, deadline,
// routing) must reach the OnReverseError callback so it lands in the plugin's
// log ring, not only travel to the plugin as an opaque __host_error__.
func TestFrameSessionReverseErrorSurfacedToHostCallback(t *testing.T) {
	failing := reverseFunc(func(callID string, req []byte) ([]byte, error) {
		return nil, errors.New("llm.complete stream: invoke: caller stalled (buffer full), stream evicted")
	})
	pluginToHostR, pluginToHostW := io.Pipe()
	hostToPluginR, hostToPluginW := io.Pipe()
	var got atomic.Value
	s := newFrameSession(frameSessionConfig{
		PluginID: "test.revfail",
		Read:     pluginToHostR,
		Write:    hostToPluginW,
		Reverse:  failing,
		OnLog:    func([]byte) {},
		OnFatal:  func(error) {},
		OnReverseError: func(svcCallID string, err error) {
			got.Store(svcCallID + "|" + err.Error())
		},
	})
	s.start()
	t.Cleanup(func() {
		_ = pluginToHostW.Close()
		_ = hostToPluginR.Close()
		s.close()
	})

	if err := WriteTransportFrame(pluginToHostW, MsgReverseReq, "r1", []byte(`{"callID":"llm.complete","payload":{}}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := got.Load().(string); ok {
			if !strings.Contains(v, "llm.complete|") || !strings.Contains(v, "stream evicted") {
				t.Fatalf("surfaced error = %q", v)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("reverse dispatch failure was never surfaced to OnReverseError")
}

// TestFrameSessionRejectsPlainFrames pins the v2-only wire: a frame without
// the callID flag is a protocol violation — readLoop fails the session (which
// fails any pending invoke) instead of guessing. The raw bytes are written
// directly because WriteTransportFrame always sets the flag.
func TestFrameSessionRejectsPlainFrames(t *testing.T) {
	p := newPipeSession(t)

	go func() {
		_, _ = p.s.call(context.Background(), []byte(`{}`))
	}()

	// A v1-shaped frame: [4-byte BE length][type without 0x80][payload].
	payload := []byte(`{"callID":"llm.complete","payload":{"x":1}}`)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(len(payload)))
	buf.WriteByte(MsgReverseReq) // 0x03, no callID flag
	buf.Write(payload)
	if _, err := p.w.Write(buf.Bytes()); err != nil {
		t.Fatalf("write plain frame: %v", err)
	}

	select {
	case <-p.s.settled():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not fail on plain frame")
	}
}

func TestFrameSessionSettleFailsPendingInvoke(t *testing.T) {
	p := newPipeSession(t)
	// Drain the host->plugin side so the call's synchronous invoke-req write
	// (io.Pipe is unbuffered) does not block it before its select.
	go func() { _, _ = io.Copy(io.Discard, p.r) }()
	done := make(chan error, 1)
	go func() {
		_, err := p.s.call(context.Background(), []byte(`{}`))
		done <- err
	}()
	// Plugin dies (EOF on the plugin->host stream).
	if err := closerOf(p.w); err != nil {
		t.Fatalf("close plugin stream: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pending invoke must fail on session death")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pending invoke hung after session death")
	}
}

func closerOf(w io.Writer) error {
	type closeable interface{ Close() error }
	if c, ok := w.(closeable); ok {
		return c.Close()
	}
	return nil
}

// TestFrameSessionReverseStreamChunks pins the S2 wire contract: a
// reverse-req with stream:true is answered by ordered 0x07 chunk frames on
// the same correlation ID, followed by the terminal 0x04. Chunk frames must
// be fully written before the terminal frame (structural ordering).
func TestFrameSessionReverseStreamChunks(t *testing.T) {
	p := newPipeSessionWithReverse(t, streamingReverseRecorder{rec: &eventRecorder{}})

	if err := WriteTransportFrame(p.w, MsgReverseReq, "s1", []byte(`{"callID":"llm.complete","payload":{},"stream":true}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}

	var chunks []string
	for {
		f := p.readFrameTimeout(t, 5*time.Second)
		if f.Type == MsgInvokeReq {
			continue
		}
		if f.Type == MsgReverseChunk {
			if f.CallID != "s1" {
				t.Fatalf("chunk frame callID = %q, want correlated s1", f.CallID)
			}
			chunks = append(chunks, string(f.Payload))
			continue
		}
		if f.Type != MsgReverseResp {
			t.Fatalf("frame type = 0x%02x, want reverse-resp", f.Type)
		}
		if f.CallID != "s1" {
			t.Fatalf("terminal frame callID = %q, want s1", f.CallID)
		}
		break
	}
	if len(chunks) != 2 {
		t.Fatalf("got %d chunk frames, want 2 (got %v)", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0], "hello, ") || !strings.Contains(chunks[1], "world") {
		t.Fatalf("chunks = %v, want ordered deltas", chunks)
	}
}

// TestFrameSessionForwardStreamChunks pins the forward streaming wire: a
// callStream waits for the plugin to push 0x08 forward-chunk frames followed
// by the terminal 0x02 invoke-resp; onChunk fires per chunk in wire order and
// the call returns the terminal payload.
func TestFrameSessionForwardStreamChunks(t *testing.T) {
	p := newPipeSession(t)

	var chunks []string
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		_, err := p.s.callStream(context.Background(), []byte(`{"Callable":"novel"}`), func(c []byte) error {
			chunks = append(chunks, string(c))
			return nil
		})
		if err != nil {
			t.Errorf("callStream: %v", err)
		}
	}()

	// Read & discard the host->plugin 0x01 invoke-req (io.Pipe is synchronous).
	_ = p.readFrameTimeout(t, 5*time.Second)

	// Plugin pushes two forward chunks then the terminal invoke-resp.
	if err := WriteTransportFrame(p.w, MsgForwardChunk, "", []byte(`{"delta":"Once"}`)); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	if err := WriteTransportFrame(p.w, MsgForwardChunk, "", []byte(`{"delta":" upon"}`)); err != nil {
		t.Fatalf("write chunk 2: %v", err)
	}
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"chapter":"Once upon a forest"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}

	select {
	case <-streamDone:
	case <-time.After(5 * time.Second):
		t.Fatal("callStream never completed")
	}
	if len(chunks) != 2 || !strings.Contains(chunks[0], "Once") || !strings.Contains(chunks[1], "upon") {
		t.Fatalf("chunks = %v, want 2 ordered deltas [Once, upon]", chunks)
	}
}

// TestFrameSessionForwardStreamOnChunkError pins the abort path: an onChunk
// error parks the waiter for the settle grace (no-poison rule — the plugin
// still owes a terminal invoke-resp); once the terminal arrives it is
// consumed, callStream returns the onChunk error, and the session stays
// alive with the wire still single-flight clean.
func TestFrameSessionForwardStreamOnChunkError(t *testing.T) {
	p := newPipeSession(t)

	errDone := make(chan error, 1)
	go func() {
		_, err := p.s.callStream(context.Background(), []byte(`{"Callable":"novel"}`), func(c []byte) error {
			return fmt.Errorf("stop")
		})
		errDone <- err
	}()

	// Drain the 0x01 invoke-req.
	_ = p.readFrameTimeout(t, 5*time.Second)

	// First chunk triggers the abort.
	if err := WriteTransportFrame(p.w, MsgForwardChunk, "", []byte(`{"delta":"x"}`)); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	// The plugin's terminal arrives after the consumer aborted: it must be
	// consumed by the parked waiter (not treated as an unmatched response).
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"done":true}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}
	select {
	case err := <-errDone:
		if err == nil || !strings.Contains(err.Error(), "stop") {
			t.Fatalf("callStream err = %v, want stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callStream never returned after onChunk error")
	}
	// The session survived: a subsequent unary call round-trips cleanly.
	go func() {
		_, _ = p.s.call(context.Background(), []byte(`{"Callable":"ping"}`))
	}()
	_ = p.readFrameTimeout(t, 5*time.Second) // drain the fresh 0x01
	if err := WriteTransportFrame(p.w, MsgInvokeResp, "", []byte(`{"pong":true}`)); err != nil {
		t.Fatalf("write pong: %v", err)
	}
}

// TestFrameSessionReverseStreamWithoutHandlerDegrades pins the degradation
// contract for a handler that does NOT implement StreamReverseHandler: a
// stream:true reverse-req still gets its terminal 0x04 (and no chunks) —
// this is the wire-level mirror of the new-plugin/old-host path.
func TestFrameSessionReverseStreamWithoutHandlerDegrades(t *testing.T) {
	p := newPipeSession(t) // default recorder: Dispatch only, no streaming

	if err := WriteTransportFrame(p.w, MsgReverseReq, "s2", []byte(`{"callID":"llm.complete","payload":{},"stream":true}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}

	for {
		f := p.readFrameTimeout(t, 5*time.Second)
		if f.Type == MsgInvokeReq {
			continue
		}
		if f.Type == MsgReverseChunk {
			t.Fatalf("unexpected chunk frame from non-streaming handler: %s", f.Payload)
		}
		if f.Type == MsgReverseResp {
			if f.CallID != "s2" {
				t.Fatalf("terminal frame callID = %q, want s2", f.CallID)
			}
			return
		}
		t.Fatalf("frame type = 0x%02x, want reverse-resp", f.Type)
	}
}

// TestFrameSessionReverseStreamWithoutFlagStaysUnary pins backward
// compatibility: reverse-reqs WITHOUT the stream flag never produce chunk
// frames, even when the handler can stream (the old-plugin contract).
func TestFrameSessionReverseStreamWithoutFlagStaysUnary(t *testing.T) {
	p := newPipeSessionWithReverse(t, streamingReverseRecorder{rec: &eventRecorder{}})

	if err := WriteTransportFrame(p.w, MsgReverseReq, "s3", []byte(`{"callID":"llm.complete","payload":{}}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}

	for {
		f := p.readFrameTimeout(t, 5*time.Second)
		if f.Type == MsgInvokeReq {
			continue
		}
		if f.Type == MsgReverseChunk {
			t.Fatalf("unexpected chunk frame for streamless reverse-req: %s", f.Payload)
		}
		if f.Type == MsgReverseResp {
			return
		}
		t.Fatalf("frame type = 0x%02x, want reverse-resp", f.Type)
	}
}

// TestFrameSessionReverseStreamConsumerGone pins the abort path: when the
// session dies while a streaming dispatch is mid-flight, the next onChunk
// observes the closed session and returns an error, the dispatch aborts, and
// nothing hangs.
func TestFrameSessionReverseStreamConsumerGone(t *testing.T) {
	handler := &blockingStreamReverse{
		sessionClosed:       make(chan struct{}),
		firstChunkDelivered: make(chan struct{}),
		dispatchDone:        make(chan struct{}),
	}
	p := newPipeSessionWithReverse(t, handler)

	// Drain the host->plugin side: io.Pipe writes are synchronous, so the
	// chunk frame write needs a reader or it blocks forever.
	go func() { _, _ = io.Copy(io.Discard, p.r) }()

	if err := WriteTransportFrame(p.w, MsgReverseReq, "s4", []byte(`{"callID":"llm.complete","payload":{},"stream":true}`)); err != nil {
		t.Fatalf("write reverse-req: %v", err)
	}

	// After the first chunk is delivered, kill the session, then let the
	// handler try to deliver the second chunk.
	<-handler.firstChunkDelivered
	p.s.close()
	close(handler.sessionClosed)

	select {
	case <-handler.dispatchDone:
		if handler.dispatchErr == nil || !strings.Contains(handler.dispatchErr.Error(), "session closed") {
			t.Fatalf("dispatch err = %v, want session-closed abort after close", handler.dispatchErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch hung after session death")
	}
}

// blockingStreamReverse delivers one chunk, waits for the session to close,
// then delivers a second chunk — which must fail because the session's write
// side is gone.
type blockingStreamReverse struct {
	reverseSkeleton
	sessionClosed       chan struct{}
	firstChunkDelivered chan struct{}
	dispatchDone        chan struct{}
	dispatchErr         error
	once                sync.Once
}

type reverseSkeleton struct{}

func (s *reverseSkeleton) Dispatch(callID string, req []byte) ([]byte, error) {
	return []byte(`{}`), nil
}

type panickingReverse struct{}

func (p *panickingReverse) Dispatch(callID string, req []byte) ([]byte, error) {
	panic("capability handler exploded")
}

// A panic inside a reverse-dispatch handler must be recovered on the dispatch
// goroutine and returned to the caller as an error — not escape and kill the
// host process (2026-09-09 crash loop: browser.cookies_export panic took the
// whole desktop app down, three times, with zero trace).
func TestDispatchWithinBudgetPanicRecovered(t *testing.T) {
	resp, err := dispatchWithinBudget(context.Background(), &panickingReverse{}, DispatchContext{}, "browser.cookies_export", []byte(`{}`), nil)
	if err == nil || !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), "browser.cookies_export") {
		t.Fatalf("dispatchWithinBudget err = %v, want a panicked error naming the callID", err)
	}
	if resp != nil {
		t.Errorf("resp = %q, want nil on panic", resp)
	}
}

func (h *blockingStreamReverse) DispatchContextStream(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
	defer close(h.dispatchDone)
	if err := onChunk([]byte(`{"kind":"text_delta","text":"a"}`)); err != nil {
		h.dispatchErr = fmt.Errorf("first chunk: %w", err)
		return nil, h.dispatchErr
	}
	h.once.Do(func() { close(h.firstChunkDelivered) })
	<-h.sessionClosed
	if err := onChunk([]byte(`{"kind":"text_delta","text":"b"}`)); err != nil {
		h.dispatchErr = fmt.Errorf("post-close chunk must fail: %w", err)
		return nil, h.dispatchErr
	}
	h.dispatchErr = fmt.Errorf("post-close chunk unexpectedly delivered")
	return []byte(`{"Text":"ab"}`), nil
}

// --- no-poison settle grace (admitted invokes that overrun their budget) ---

// TestAdmittedInvokeSettlesLateWithoutPoison pins the no-poison rule: an
// admitted invoke whose caller budget expires must keep its waiter installed
// until the plugin's late response is consumed, then fail as errInvokeOverran
// — the next invoke must receive ITS OWN response (no cross-wire) and the
// session must stay alive. Pre-fix behavior: clearWaiter + token release
// while the plugin still owed a response → the late resp cross-wired the next
// waiter or fatally settled the session (the novelking crash-loop under
// multi-agent step floods).
func TestAdmittedInvokeSettlesLateWithoutPoison(t *testing.T) {
	pluginToHostR, pluginToHostW := io.Pipe()
	hostToPluginR, hostToPluginW := io.Pipe()
	fatal := make(chan error, 1)
	s := newFrameSession(frameSessionConfig{
		PluginID:    "test.overrun",
		Read:        pluginToHostR,
		Write:       hostToPluginW,
		Reverse:     sessionReverseRecorder{&eventRecorder{}},
		OnFatal:     func(err error) { fatal <- err },
		SettleGrace: 500 * time.Millisecond,
	})
	s.start()
	t.Cleanup(func() {
		_ = pluginToHostW.Close()
		_ = hostToPluginR.Close()
		s.close()
	})

	// Plugin side: answer invoke #0 after the caller's budget expires,
	// invoke #1 immediately.
	go func() {
		for i := 0; i < 2; i++ {
			f, err := ReadTransportFrame(hostToPluginR)
			if err != nil || f.Type != MsgInvokeReq {
				return
			}
			if i == 0 {
				time.Sleep(150 * time.Millisecond)
			}
			_ = WriteTransportFrame(pluginToHostW, MsgInvokeResp, "", []byte(fmt.Sprintf(`{"seq":%d}`, i)))
		}
	}()

	ctx1, cancel1 := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel1()
	_, err := s.call(ctx1, []byte(`{"Callable":"probe"}`))
	if !errors.Is(err, errInvokeOverran) {
		t.Fatalf("overrun err = %v, want errInvokeOverran", err)
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("err = %v, want the underlying deadline cause", err)
	}

	// The session survived and the wire stayed clean: the next invoke gets
	// its own response, not the abandoned invoke's.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	resp, err := s.call(ctx2, []byte(`{"Callable":"probe"}`))
	if err != nil {
		t.Fatalf("post-overrun call: %v", err)
	}
	if string(resp) != `{"seq":1}` {
		t.Fatalf("post-overrun resp = %s, want {\"seq\":1} (no cross-wire)", resp)
	}
	select {
	case err := <-fatal:
		t.Fatalf("session settled fatally: %v", err)
	default:
	}
}

// TestAdmittedInvokeDesyncKills pins the far side of the policy: a plugin
// that produces no response within budget + settle grace is genuinely wedged —
// the session settles fatally (the standard wedge kill reaps the process) and
// subsequent calls fail fast instead of hanging.
func TestAdmittedInvokeDesyncKills(t *testing.T) {
	pluginToHostR, pluginToHostW := io.Pipe()
	hostToPluginR, hostToPluginW := io.Pipe()
	fatal := make(chan error, 1)
	s := newFrameSession(frameSessionConfig{
		PluginID:    "test.desync",
		Read:        pluginToHostR,
		Write:       hostToPluginW,
		Reverse:     sessionReverseRecorder{&eventRecorder{}},
		OnFatal:     func(err error) { fatal <- err },
		SettleGrace: 100 * time.Millisecond,
	})
	s.start()
	t.Cleanup(func() {
		_ = pluginToHostW.Close()
		_ = hostToPluginR.Close()
		s.close()
	})

	// Plugin reads requests forever but never answers.
	go func() {
		for {
			if _, err := ReadTransportFrame(hostToPluginR); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := s.call(ctx, []byte(`{"Callable":"stall"}`))
	if !errors.Is(err, errInvokeOverran) || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("desync call err = %v, want overran + deadline cause", err)
	}
	select {
	case err := <-fatal:
		if !strings.Contains(err.Error(), "settle grace") {
			t.Fatalf("fatal cause = %v, want the desync message", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session never settled after the desync grace")
	}
	// A subsequent call fails fast against the settled session.
	if _, err := s.call(context.Background(), []byte(`{}`)); err == nil {
		t.Fatal("post-desync call must fail")
	}
}

// TestPushBoundedEvictsOldest pins the bounded event queue's drop-oldest
// policy: a full queue evicts the oldest pending event to admit the newest
// (recency beats completeness for streaming surfaces).
func TestPushBoundedEvictsOldest(t *testing.T) {
	q := make(chan hostEventDispatch, 2)
	pushBounded(q, hostEventDispatch{payload: "a"})
	pushBounded(q, hostEventDispatch{payload: "b"})
	pushBounded(q, hostEventDispatch{payload: "c"})
	if got := <-q; got.payload != "b" {
		t.Fatalf("oldest surviving = %q, want b (a evicted)", got.payload)
	}
	if got := <-q; got.payload != "c" {
		t.Fatalf("newest = %q, want c", got.payload)
	}
}
