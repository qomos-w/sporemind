package sdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// processTransport tests drive the plugin-side framing loop over io.Pipe
// pairs — the in-process stand-in for the real stdin/stdout duplex pipes of
// a spawned plugin process. They exercise the exact frame protocol the host
// side (T5/T6) consumes: invoke-req/resp, interleaved reverse-req/resp,
// direct 0x05 log frames, {"error":...} and __host_error__ envelopes, and
// EOF graceful unload.

type procFrame struct {
	typ     byte
	callID  string
	payload []byte
	err     error
}

// startProcTest sets up a transport pair, wires the process-mode host/log
// writer like RunProcess does, and starts the plugin dispatch loop plus a
// goroutine that continuously drains the plugin's stdout. It returns the
// test-side stdin writer, a channel of stdout frames, and the run-loop
// completion channel.
func startProcTest(t *testing.T) (io.WriteCloser, chan procFrame, chan error) {
	t.Helper()
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	tr := newProcessTransport(stdinR, stdoutW)
	SetHost(newProcessHost(tr))
	SetProcessLogWriter(tr)

	runDone := make(chan error, 1)
	go func() { runDone <- tr.run() }()
	t.Cleanup(func() {
		// Closing the plugin's stdin is the graceful unload; closing
		// stdoutR unblocks any pending stdout write via error.
		_ = stdinW.Close()
		_ = stdoutR.Close()
		select {
		case <-runDone:
			// run() terminated; keep the pipes closed and proceed.
		case <-time.After(500 * time.Millisecond):
			// runDone may already have been drained by the test body
			// (GracefulEOF / UnexpectedType). The pipe closes above
			// guarantee the loop terminates promptly.
		}
		// Unload the lifecycle state so package-level state (activeState,
		// plugin-scoped callables) is clean for other tests.
		if p := registered(); p != nil {
			_ = HandleOnUnload(cstr(p.Manifest.ID))
		}
		SetProcessLogWriter(nil)
		SetHost(nil)
	})

	frames := make(chan procFrame, 16)
	go func() {
		for {
			typ, callID, payload, err := readFrame(stdoutR)
			frames <- procFrame{typ, callID, payload, err}
			if err != nil {
				return
			}
		}
	}()
	return stdinW, frames, runDone
}

// nextFrame reads the next stdout frame with a timeout (deadlock guard).
func nextFrame(t *testing.T, frames chan procFrame) procFrame {
	t.Helper()
	select {
	case f := <-frames:
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for stdout frame (possible deadlock)")
		return procFrame{}
	}
}

// sendInvoke writes one invoke-req frame (PluginAbiInvokeEnvelope JSON) to
// the plugin's stdin.
func sendInvoke(t *testing.T, stdinW io.Writer, callable string, payload []byte) {
	t.Helper()
	env, err := json.Marshal(map[string]any{"Callable": callable, "Payload": payload})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := writeFrame(stdinW, msgInvokeReq, "", env); err != nil {
		t.Fatalf("write invoke-req: %v", err)
	}
}

// registerProcPlugin registers a fresh plugin definition with the callables
// used by the transport scenarios, then loads it through the onLoad invoke
// (OnLoad is the first invoke-req after spawn, per the D2 contract).
func registerProcPlugin(t *testing.T, stdinW io.Writer, frames chan procFrame) {
	t.Helper()
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.proctest", Name: "ProcTest", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("greet", func(req Request) (Response, error) {
				var p struct {
					Name string `json:"name"`
				}
				_ = json.Unmarshal(req.Payload, &p)
				ctx.Log(LogLevelInfo, "greet called for %s", p.Name)
				return Response{Payload: map[string]string{"message": "Hello, " + p.Name + "!"}}, nil
			})
			ctx.RegisterCallable("reverse", func(req Request) (Response, error) {
				var p struct {
					CallID  string          `json:"callID"`
					Payload json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal(req.Payload, &p); err != nil {
					return Response{}, fmt.Errorf("reverse: %w", err)
				}
				out, err := ctx.Host().Invoke(p.CallID, json.RawMessage(p.Payload))
				if err != nil {
					return Response{}, err
				}
				return Response{Payload: json.RawMessage(out)}, nil
			})
			ctx.RegisterCallable("boom", func(req Request) (Response, error) {
				return Response{}, fmt.Errorf("boom: %s", string(req.Payload))
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	f := nextFrame(t, frames)
	if f.typ != msgInvokeResp {
		t.Fatalf("onLoad resp type = 0x%02x, want invoke-resp", f.typ)
	}
	if string(f.payload) != "{}" {
		t.Fatalf("onLoad resp payload = %s, want {}", f.payload)
	}
}

func TestProcessTransportOnLoadAndForward(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerProcPlugin(t, stdinW, frames)

	sendInvoke(t, stdinW, "greet", []byte(`{"name":"world"}`))
	// The handler's ctx.Log must arrive as a direct 0x05 log frame before
	// the invoke-resp (decision D1: direct frames, no ring/drain in process
	// mode).
	lf := nextFrame(t, frames)
	if lf.typ != msgLog {
		t.Fatalf("first frame after greet = 0x%02x, want 0x05 log", lf.typ)
	}
	var entry LogEntry
	if err := json.Unmarshal(lf.payload, &entry); err != nil {
		t.Fatalf("decode log entry %q: %v", lf.payload, err)
	}
	if entry.Level != LogLevelInfo || entry.Message != "greet called for world" {
		t.Fatalf("log entry = %+v", entry)
	}
	rf := nextFrame(t, frames)
	if rf.typ != msgInvokeResp {
		t.Fatalf("greet resp type = 0x%02x, want invoke-resp", rf.typ)
	}
	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rf.payload, &got); err != nil {
		t.Fatalf("decode greet resp %q: %v", rf.payload, err)
	}
	if got.Message != "Hello, world!" {
		t.Fatalf("greet message = %q, want Hello, world!", got.Message)
	}
}

func TestProcessTransportReverseCall(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerProcPlugin(t, stdinW, frames)

	// The "reverse" handler calls ctx.Host().Invoke -> 0x03 reverse-req,
	// then blocks until the 0x04 reverse-resp arrives -> 0x02 invoke-resp.
	sendInvoke(t, stdinW, "reverse", []byte(`{"callID":"llm.complete","payload":{"prompt":"hi"}}`))

	rr := nextFrame(t, frames)
	if rr.typ != msgReverseReq {
		t.Fatalf("frame type = 0x%02x, want reverse-req", rr.typ)
	}
	if rr.callID == "" {
		t.Fatalf("reverse-req frame carries no correlation callID")
	}
	var rev struct {
		CallID  string          `json:"callID"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(rr.payload, &rev); err != nil {
		t.Fatalf("decode reverse-req %q: %v", rr.payload, err)
	}
	if rev.CallID != "llm.complete" {
		t.Fatalf("reverse callID = %q, want llm.complete", rev.CallID)
	}
	if rr.callID == "" {
		t.Fatalf("reverse-req frame carries no correlation callID")
	}

	// Host answers the reverse call on the same correlation.
	if err := writeFrame(stdinW, msgReverseResp, rr.callID, []byte(`{"result":"ok"}`)); err != nil {
		t.Fatalf("write reverse-resp: %v", err)
	}
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want 0x02 invoke-resp", ir.typ)
	}
	if string(ir.payload) != `{"result":"ok"}` {
		t.Fatalf("invoke-resp payload = %s, want {\"result\":\"ok\"}", ir.payload)
	}
}

// TestProcessTransportBackgroundReverseCall is the full-duplex acceptance
// case: a plugin handler starts a background goroutine that calls
// ActiveHost().Invoke AFTER the handler returned — i.e. the reverse-req is
// issued while NO invoke is in flight, then a new invoke arrives and is
// dispatched while the reverse call is still pending, then the correlated
// reverse-resp resolves the background waiter.
func TestProcessTransportBackgroundReverseCall(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)

	bgDone := make(chan string, 1)
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.procbg", Name: "ProcBG", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("spawnbg", func(req Request) (Response, error) {
				go func() {
					out, err := ActiveHost().Invoke("state.get", map[string]any{"key": "k"})
					if err != nil {
						bgDone <- "err:" + err.Error()
						return
					}
					bgDone <- "ok:" + string(out)
				}()
				return Response{Payload: map[string]string{"started": "true"}}, nil
			})
			ctx.RegisterCallable("bgresult", func(req Request) (Response, error) {
				select {
				case r := <-bgDone:
					return Response{Payload: map[string]string{"result": r}}, nil
				case <-time.After(5 * time.Second):
					return Response{}, fmt.Errorf("background result timeout")
				}
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	if f := nextFrame(t, frames); f.typ != msgInvokeResp {
		t.Fatalf("onLoad resp type = 0x%02x, want invoke-resp", f.typ)
	}

	// 1. Handler spawns the background caller and returns immediately.
	sendInvoke(t, stdinW, "spawnbg", []byte(`{}`))
	if f := nextFrame(t, frames); f.typ != msgInvokeResp {
		t.Fatalf("spawnbg resp type = 0x%02x, want invoke-resp", f.typ)
	}

	// 2. The background goroutine's reverse-req arrives BETWEEN invokes.
	rr := nextFrame(t, frames)
	if rr.typ != msgReverseReq || rr.callID == "" {
		t.Fatalf("background frame = {typ:0x%02x callID:%q}, want correlated reverse-req", rr.typ, rr.callID)
	}

	// 3. A NEW invoke is dispatched while the reverse call is pending —
	// the transport must stay fully operational.
	sendInvoke(t, stdinW, "bgresult", []byte(`{}`))

	// 4. Host answers the background reverse call on its correlation.
	if err := writeFrame(stdinW, msgReverseResp, rr.callID, []byte(`{"value":"v1"}`)); err != nil {
		t.Fatalf("write reverse-resp: %v", err)
	}

	// 5. bgresult now observes the background waiter's result.
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("bgresult resp type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode bgresult resp %q: %v", ir.payload, err)
	}
	if got.Result != `ok:{"value":"v1"}` {
		t.Fatalf("background result = %q, want ok:{\"value\":\"v1\"}", got.Result)
	}
}

func TestProcessTransportReverseHostError(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerProcPlugin(t, stdinW, frames)

	sendInvoke(t, stdinW, "reverse", []byte(`{"callID":"state.get","payload":{"key":"k"}}`))
	rr := nextFrame(t, frames)
	if rr.typ != msgReverseReq {
		t.Fatalf("frame type = 0x%02x, want reverse-req", rr.typ)
	}
	if rr.callID == "" {
		t.Fatalf("reverse-req frame carries no correlation callID")
	}

	// Host denies the reverse call via the __host_error__ envelope
	// (bridgeResult decode path), on the same correlation.
	if err := writeFrame(stdinW, msgReverseResp, rr.callID, []byte(`{"__host_error__":"capability not granted: state.get"}`)); err != nil {
		t.Fatalf("write reverse-resp: %v", err)
	}
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if !strings.Contains(got.Error, "capability not granted") {
		t.Fatalf("error = %q, want capability denial surfaced", got.Error)
	}
}

func TestProcessTransportHandlerErrorEnvelope(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerProcPlugin(t, stdinW, frames)

	sendInvoke(t, stdinW, "boom", []byte(`kaboom`))
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if !strings.Contains(got.Error, "boom: kaboom") {
		t.Fatalf("error = %q, want boom: kaboom", got.Error)
	}
}

func TestProcessTransportInvokeBeforeOnLoad(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{Manifest: Manifest{ID: "com.example.proctest", Name: "P", Version: "1"}})

	// No onLoad invoke yet: a regular invoke must fail with a clean
	// {"error":...} (plugin not loaded), not a protocol crash.
	sendInvoke(t, stdinW, "greet", []byte(`{}`))
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if got.Error == "" {
		t.Fatalf("expected error for invoke before OnLoad, got %s", ir.payload)
	}
}

func TestProcessTransportGracefulEOF(t *testing.T) {
	stdinW, _, runDone := startProcTest(t)
	Register(&Plugin{Manifest: Manifest{ID: "com.example.proctest", Name: "P", Version: "1"}})

	// Host closes its write end of stdin: clean EOF = graceful unload; the
	// run loop must exit with io.EOF (RunProcess maps that to exit 0).
	_ = stdinW.Close()
	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("run err = %v, want io.EOF (graceful unload)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit on stdin EOF")
	}
}

// TestProcessTransportUnexpectedType is no longer applicable in v2: every
// frame type has bit 7 set. A bare 0x77 would be caught as "missing callID
// flag" by readFrame, not as "unexpected message type". The test now sends
// a frame with a valid callID flag but a type the dispatch loop doesn't
// handle, which is the real protocol violation.
func TestProcessTransportUnexpectedType(t *testing.T) {
	stdinW, frames, runDone := startProcTest(t)
	Register(&Plugin{Manifest: Manifest{ID: "com.example.proctest", Name: "P", Version: "1"}})

	// A frame with a valid callID flag but an unknown type (0x77|0x80=0xf7)
	// must abort the loop (fatal; RunProcess maps it to 0x06 + exit 1).
	if err := writeFrame(stdinW, 0x77, "", []byte(`x`)); err != nil {
		t.Fatalf("write bad frame: %v", err)
	}
	select {
	case err := <-runDone:
		if err == nil {
			t.Fatal("run err = nil, want protocol violation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not exit after protocol violation")
	}
	select {
	case f := <-frames:
		if f.err == nil && f.typ != msgError {
			t.Fatalf("unexpected frame after violation: 0x%02x %q", f.typ, f.payload)
		}
	case <-time.After(1 * time.Second):
	}
}

func TestProcessTransportMalformedEnvelope(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{Manifest: Manifest{ID: "com.example.proctest", Name: "P", Version: "1"}})

	// invoke-req payload that is not a PluginAbiInvokeEnvelope.
	if err := writeFrame(stdinW, msgInvokeReq, "", []byte(`not-json`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if !strings.Contains(got.Error, "decode invoke envelope") {
		t.Fatalf("error = %q, want decode invoke envelope", got.Error)
	}
}

func TestFrameCodecRoundTrip(t *testing.T) {
	for _, typ := range []byte{msgInvokeReq, msgInvokeResp, msgReverseReq, msgReverseResp, msgLog, msgError, msgReverseChunk, msgForwardChunk} {
		payload := []byte(`{"hello":"world"}`)
		var buf strings.Builder
		if err := writeFrame(&frameBuffer{b: &buf}, typ, "cid1", payload); err != nil {
			t.Fatalf("write 0x%02x: %v", typ, err)
		}
		gotTyp, gotCallID, gotPayload, err := readFrame(strings.NewReader(buf.String()))
		if err != nil {
			t.Fatalf("read 0x%02x: %v", typ, err)
		}
		if gotTyp != typ || string(gotPayload) != string(payload) || gotCallID != "cid1" {
			t.Fatalf("round trip 0x%02x: typ=%02x callID=%q payload=%q", typ, gotTyp, gotCallID, gotPayload)
		}
	}
}

type frameBuffer struct{ b *strings.Builder }

func (fb *frameBuffer) Write(p []byte) (int, error) { return fb.b.Write(p) }

// --- reverse streaming (0x07) ---

// registerStreamPlugin registers a plugin whose "stream" callable performs a
// streaming reverse call via Host().InvokeStream. The handler reports the
// observed chunk payloads (joined with "|") and the terminal value through
// the invoke response so tests can assert both, in order.
func registerStreamPlugin(t *testing.T, stdinW io.Writer, frames chan procFrame, abortAfter int) {
	t.Helper()
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.procstream", Name: "ProcStream", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("stream", func(req Request) (Response, error) {
				out, err := ctx.Host().InvokeStream("llm.complete", map[string]any{"prompt": "hi"}, func(chunk []byte) error {
					abortAfter--
					if abortAfter == 0 {
						return fmt.Errorf("consumer closed early")
					}
					return nil
				})
				if err != nil {
					return Response{Payload: map[string]string{"error": err.Error()}}, nil
				}
				return Response{Payload: map[string]string{"out": string(out)}}, nil
			})
			ctx.RegisterCallable("chunks", func(req Request) (Response, error) {
				var got []string
				out, err := ctx.Host().InvokeStream("llm.chat", map[string]any{"prompt": "hi"}, func(chunk []byte) error {
					got = append(got, string(chunk))
					return nil
				})
				if err != nil {
					return Response{}, err
				}
				return Response{Payload: map[string]any{"chunks": got, "final": string(out)}}, nil
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	f := nextFrame(t, frames)
	if f.typ != msgInvokeResp || string(f.payload) != "{}" {
		t.Fatalf("onLoad resp = 0x%02x %s, want invoke-resp {}", f.typ, f.payload)
	}
}

// nextReverseReq reads the next frame and asserts it is a 0x03 reverse-req,
// returning its correlation callID and decoded payload.
func nextReverseReq(t *testing.T, frames chan procFrame) (string, map[string]any) {
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

// TestProcessTransportReverseStreamCall drives the happy path: reverse-req
// with {"stream":true}, two 0x07 chunks, then the terminal 0x04. The handler
// must observe chunks in arrival order and still receive the terminal value.
func TestProcessTransportReverseStreamCall(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerStreamPlugin(t, stdinW, frames, -1)

	sendInvoke(t, stdinW, "chunks", []byte(`{}`))
	cid, rev := nextReverseReq(t, frames)
	if rev["stream"] != true {
		t.Fatalf("reverse-req payload = %v, want stream:true", rev)
	}
	if rev["callID"] != "llm.chat" {
		t.Fatalf("reverse callID = %v, want llm.chat", rev["callID"])
	}

	if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(`{"kind":"text","text":"Hello"}`)); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(`{"kind":"text","text":" world"}`)); err != nil {
		t.Fatalf("write chunk 2: %v", err)
	}
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"Hello world"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}

	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Chunks []string `json:"chunks"`
		Final  string   `json:"final"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if len(got.Chunks) != 2 || got.Chunks[0] != `{"kind":"text","text":"Hello"}` || got.Chunks[1] != `{"kind":"text","text":" world"}` {
		t.Fatalf("chunks = %q, want the two chunk payloads in order", got.Chunks)
	}
	if got.Final != `{"Text":"Hello world"}` {
		t.Fatalf("final = %q, want terminal payload", got.Final)
	}
}

// TestProcessTransportReverseStreamNestedReverseNoDeadlock is the regression
// test for the bounded-chunks deadlock: the consumer issues a NESTED unary
// reverse call (app.emit) from inside onChunk — the emit-per-delta streaming
// pattern — while the host pumps more chunks than the old fixed buffer held.
// With the old cap-8 channel the read loop parked on delivery while the
// consumer parked on the nested 0x04, the runtime aborted the process ("all
// goroutines are asleep"), and the host saw a mid-invoke EOF. The queue-based
// delivery must keep both sides running to completion.
func TestProcessTransportReverseStreamNestedReverseNoDeadlock(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.procnested", Name: "ProcNested", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("nested", func(req Request) (Response, error) {
				emitted := 0
				out, err := ctx.Host().InvokeStream("llm.complete", map[string]any{"prompt": "hi"}, func(chunk []byte) error {
					emitted++
					if _, err := ctx.Host().Invoke("app.emit", map[string]any{"delta": string(chunk)}); err != nil {
						return err
					}
					return nil
				})
				if err != nil {
					return Response{}, err
				}
				return Response{Payload: map[string]any{"emitted": emitted, "final": string(out)}}, nil
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	if f := nextFrame(t, frames); f.typ != msgInvokeResp || string(f.payload) != "{}" {
		t.Fatalf("onLoad resp = 0x%02x %s, want invoke-resp {}", f.typ, f.payload)
	}

	sendInvoke(t, stdinW, "nested", []byte(`{}`))
	cid, rev := nextReverseReq(t, frames)
	if rev["callID"] != "llm.complete" || rev["stream"] != true {
		t.Fatalf("reverse-req = %v, want llm.complete stream:true", rev)
	}

	// 12 chunks in a burst — far beyond the old 8-slot buffer — before any
	// nested emit reverse-resp is served.
	for i := 0; i < 12; i++ {
		chunk := fmt.Sprintf(`{"kind":"text_delta","data":{"text":"d%d"}}`, i)
		if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(chunk)); err != nil {
			t.Fatalf("write chunk %d: %v", i, err)
		}
	}

	// Serve the 12 nested app.emit reverse calls, then the stream terminal.
	for i := 0; i < 12; i++ {
		ecid, erev := nextReverseReq(t, frames)
		if erev["callID"] != "app.emit" {
			t.Fatalf("nested reverse callID = %v, want app.emit", erev["callID"])
		}
		if err := writeFrame(stdinW, msgReverseResp, ecid, []byte(`{}`)); err != nil {
			t.Fatalf("write emit resp %d: %v", i, err)
		}
	}
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"done"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}

	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Emitted int    `json:"emitted"`
		Final   string `json:"final"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if got.Emitted != 12 || got.Final != `{"Text":"done"}` {
		t.Fatalf("emitted=%d final=%q, want 12 chunks and terminal passthrough", got.Emitted, got.Final)
	}
}

// TestProcessTransportReverseStreamOldHostDegradation simulates a host that
// predates the stream flag: the reverse-req carries stream:true but the host
// answers with the terminal 0x04 only. The call must succeed with zero
// intermediate chunks.
func TestProcessTransportReverseStreamOldHostDegradation(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerStreamPlugin(t, stdinW, frames, -1)

	sendInvoke(t, stdinW, "chunks", []byte(`{}`))
	cid, rev := nextReverseReq(t, frames)
	if rev["stream"] != true {
		t.Fatalf("reverse-req payload = %v, want stream:true", rev)
	}
	// Old host: ignores the flag, answers unary.
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"agg"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}

	ir := nextFrame(t, frames)
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Chunks []string `json:"chunks"`
		Final  string   `json:"final"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if len(got.Chunks) != 0 {
		t.Fatalf("chunks = %q, want none (old host)", got.Chunks)
	}
	if got.Final != `{"Text":"agg"}` {
		t.Fatalf("final = %q, want terminal payload", got.Final)
	}
}

// TestProcessTransportReverseStreamAbort covers the consumer-abort path: the
// onChunk callback returns an error after the first chunk. InvokeStream must
// surface that error, later 0x07 chunks must be skipped without killing the
// read loop, and the terminal 0x04 must still clean up the waiter — proven
// by the transport serving a subsequent invoke.
func TestProcessTransportReverseStreamAbort(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	registerStreamPlugin(t, stdinW, frames, 1) // abort after 1st chunk

	sendInvoke(t, stdinW, "stream", []byte(`{}`))
	cid, _ := nextReverseReq(t, frames)

	if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(`{"kind":"text","text":"one"}`)); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	// Give the handler a moment to observe the chunk and abort before the
	// later frames arrive (deterministic: the terminal 0x04 below removes
	// the waiter either way).
	if err := writeFrame(stdinW, msgReverseChunk, cid, []byte(`{"kind":"text","text":"two"}`)); err != nil {
		t.Fatalf("write chunk 2: %v", err)
	}
	if err := writeFrame(stdinW, msgReverseResp, cid, []byte(`{"Text":"never-consumed"}`)); err != nil {
		t.Fatalf("write terminal: %v", err)
	}

	ir := nextFrame(t, frames)
	// The consumer-side abort now emits the 0x09 reverse-cancel frame before
	// the handler's invoke-resp: the host is asked to abort its upstream
	// dispatch (cost-leak fix). Tolerate it in arrival order.
	if ir.typ == msgReverseCancel {
		if ir.callID != cid {
			t.Fatalf("reverse-cancel callID = %q, want %q", ir.callID, cid)
		}
		ir = nextFrame(t, frames)
	}
	if ir.typ != msgInvokeResp {
		t.Fatalf("frame type = 0x%02x, want invoke-resp", ir.typ)
	}
	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(ir.payload, &got); err != nil {
		t.Fatalf("decode invoke-resp %q: %v", ir.payload, err)
	}
	if !strings.Contains(got.Error, "consumer closed early") {
		t.Fatalf("error = %q, want consumer closed early", got.Error)
	}

	// The transport must still be alive: dispatch a fresh plain invoke.
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.procstream", Name: "ProcStream", Version: "1.0.0"},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	lf := nextFrame(t, frames)
	if lf.typ != msgInvokeResp {
		t.Fatalf("post-abort frame = 0x%02x, want invoke-resp (transport died?)", lf.typ)
	}
}

// --- forward streaming (0x08) ---

// TestProcessTransportForwardStreamChunks pins the forward streaming wire: a
// HandlerStream callable invoked via 0x01 must emit its chunks as ordered 0x08
// forward-chunk frames followed by exactly one terminal 0x02 invoke-resp.
func TestProcessTransportForwardStreamChunks(t *testing.T) {
	stdinW, frames, _ := startProcTest(t)
	Register(&Plugin{
		Manifest: Manifest{ID: "com.example.procstream", Name: "ProcStream", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallableStream("novel", func(req Request, emit func(Response) error) (Response, error) {
				var p struct {
					Topic string `json:"topic"`
				}
				_ = json.Unmarshal(req.Payload, &p)
				for _, piece := range []string{"Once", " upon", " a", " " + p.Topic} {
					if err := emit(Response{Payload: map[string]string{"delta": piece}}); err != nil {
						return Response{}, err
					}
				}
				return Response{Payload: map[string]string{"chapter": "Once upon a " + p.Topic, "done": "true"}}, nil
			})
			return nil
		},
	})
	sendInvoke(t, stdinW, ProcessOnLoadCallable, nil)
	if f := nextFrame(t, frames); f.typ != msgInvokeResp {
		t.Fatalf("onLoad resp type = 0x%02x, want invoke-resp", f.typ)
	}

	sendInvoke(t, stdinW, "novel", []byte(`{"topic":"forest"}`))
	var deltas []string
	for {
		f := nextFrame(t, frames)
		if f.typ == msgForwardChunk {
			var c struct {
				Delta string `json:"delta"`
			}
			if err := json.Unmarshal(f.payload, &c); err != nil {
				t.Fatalf("decode chunk %q: %v", f.payload, err)
			}
			deltas = append(deltas, c.Delta)
			continue
		}
		if f.typ != msgInvokeResp {
			t.Fatalf("frame after chunks = 0x%02x, want invoke-resp", f.typ)
		}
		var term struct {
			Chapter string `json:"chapter"`
			Done    string `json:"done"`
		}
		if err := json.Unmarshal(f.payload, &term); err != nil {
			t.Fatalf("decode terminal %q: %v", f.payload, err)
		}
		if term.Chapter != "Once upon a forest" || term.Done != "true" {
			t.Fatalf("terminal = %+v", term)
		}
		break
	}
	if strings.Join(deltas, "") != "Once upon a forest" {
		t.Fatalf("deltas joined = %q, want %q", strings.Join(deltas, ""), "Once upon a forest")
	}
}

// TestDispatchStreamingCallableDegradesToUnary pins the FFI/in-process
// fallback: a HandlerStream callable dispatched through the unary Dispatch
// entry (no forward-chunk wire) runs with a no-op emit and returns just the
// terminal payload — zero chunks, no error.
func TestDispatchStreamingCallableDegradesToUnary(t *testing.T) {
	RegisterCallableStream("genstream", func(req Request, emit func(Response) error) (Response, error) {
		if err := emit(Response{Payload: map[string]string{"delta": "dropped"}}); err != nil {
			return Response{}, err
		}
		return Response{Payload: map[string]string{"full": "value"}}, nil
	})
	defer UnregisterCallable("genstream")

	data, err := Dispatch("", "genstream", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["full"] != "value" {
		t.Fatalf("unexpected response: %v", got)
	}
}
