package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/qomos-w/sporemind-plugin-sdk/gen"
)

// ProcessOnLoadCallable is the reserved callable name the host addresses as
// the FIRST invoke-req after spawning the plugin process (spawn -> OnLoad ->
// ready). The subprocess transport intercepts it before registry lookup and
// runs the registered plugin's OnLoad hook, mirroring the FFI PluginOnLoad
// symbol. It must match the host's process_opener (T5).
const ProcessOnLoadCallable = "onLoad"

// processTransport is the plugin side of the subprocess transport: one
// stdin/stdout duplex pipe with correlated framing between the plugin
// process and the host process.
//
// The protocol is full-duplex: run() is the single stdin reader and
// demultiplexes frames — invoke-reqs are dispatched on worker goroutines
// (so a handler may take arbitrarily long without blocking log or
// reverse-resp delivery), and reverse-resps are routed to their pending
// caller by the frame's correlation callID. Host bridge calls
// (ActiveHost().Invoke via processHost) are therefore legal from ANY
// goroutine the plugin owns — inside a handler or from a background worker —
// matching the in-process (FFI) transport's semantics. This is the seam the
// distributed (host-to-host) lane reuses later: the same envelope over a
// different byte stream.
type processTransport struct {
	in  io.Reader
	out io.Writer
	mu  sync.Mutex // serializes out writes (invoke workers + log + reverse-req)

	// reverse correlation state
	pendMu  sync.Mutex
	pending map[string]*reverseWaiter // callID -> waiter (reverse calls)
	nextID  atomic.Int64
	done    chan struct{} // closed when the read loop exits
	doneErr error
}

// reverseWaiter holds the state of one pending reverse call. Unary calls
// (Invoke) only use done; streaming calls (InvokeStream) additionally
// receive intermediate 0x07 chunk frames through an unbounded queue+signal
// until the terminal 0x04 arrives. The read loop delivers chunks BEFORE the
// terminal frame (both are written by the host under the same session write
// lock), so a consumer that drains queued chunks after done fires still
// observes the correct order.
//
// The queue MUST be unbounded (slice + signal, never a bounded channel): the
// consumer may issue nested reverse calls from inside onChunk (e.g. app.emit
// per LLM delta), and a bounded buffer lets the read loop block on delivery
// while the consumer blocks on the nested call's 0x04 — with every goroutine
// parked the Go runtime aborts the whole process ("all goroutines are
// asleep"), which the host observes as a mid-invoke EOF crash. Memory cost is
// bounded by the stream length, the same ceiling as the terminal payload.
type reverseWaiter struct {
	qmu     sync.Mutex
	queue   [][]byte        // intermediate 0x07 payloads; nil signal marks a unary waiter
	signal  chan struct{}   // cap 1; non-blocking wake after each append
	done    chan []byte     // terminal 0x04 payload (or __transport_dead__ marker)
	// abandoned marks a stream whose consumer returned early (onChunk
	// error). The host still sends remaining chunks plus the terminal
	// frame; the read loop skips chunk delivery so the queue cannot grow
	// unboundedly, while the terminal frame (buffered, cap 1) still
	// removes the waiter from the map.
	abandoned atomic.Bool
}

// popQueued atomically takes every buffered chunk in arrival order.
func (w *reverseWaiter) popQueued() [][]byte {
	w.qmu.Lock()
	q := w.queue
	w.queue = nil
	w.qmu.Unlock()
	return q
}

func newProcessTransport(in io.Reader, out io.Writer) *processTransport {
	return &processTransport{
		in:      in,
		out:     out,
		pending: make(map[string]*reverseWaiter),
		done:    make(chan struct{}),
	}
}

// Write implements io.Writer for the transport's stdout end. Every caller
// assembles a complete frame before calling Write, so each Write is one
// whole frame; the mutex keeps concurrent frame writes from interleaving.
func (t *processTransport) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.out.Write(p)
}

func (t *processTransport) writeFrame(typ byte, callID string, payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return writeFrame(t.out, typ, callID, payload)
}

// failAllPending unblocks every reverse-call waiter with the terminal error
// (stdin EOF or a read-loop failure). Callers of ActiveHost().Invoke get a
// clean error instead of hanging while the process tears down.
func (t *processTransport) failAllPending(err error) {
	t.pendMu.Lock()
	pending := t.pending
	t.pending = make(map[string]*reverseWaiter)
	t.pendMu.Unlock()
	for _, w := range pending {
		// Only the terminal channel is notified: stream consumers select on
		// done AND t.done, and any buffered chunks are irrelevant once the
		// marker arrives. Never touch chunks here — a full buffer with no
		// consumer would block the teardown path.
		w.done <- []byte(fmt.Sprintf("__transport_dead__:%v", err))
	}
}

// run is the plugin-side main loop and the single owner of the stdin read
// side. io.EOF on stdin means the host closed its write end — the graceful
// unload signal (exit 0); any other read error is fatal and returned to the
// caller for 0x06 + exit 1 handling. On exit every pending reverse-call
// waiter is failed so no plugin goroutine hangs.
func (t *processTransport) run() error {
	err := t.readLoop()
	t.failAllPending(err)
	close(t.done)
	return err
}

func (t *processTransport) readLoop() error {
	for {
		typ, callID, payload, err := readFrame(t.in)
		if err != nil {
			return err
		}
		switch typ {
		case msgInvokeReq:
			t.dispatchInvoke(payload)
		case msgReverseResp:
			if !t.deliverReverse(callID, payload) {
				return fmt.Errorf("reverse-resp for unknown call %q", callID)
			}
		case msgReverseChunk:
			// A chunk for an unknown call (racing the terminal 0x04, or a
			// unary waiter on an old host that never negotiated streaming)
			// is tolerated and skipped — unlike an unknown 0x04 it is not a
			// protocol violation that should kill the transport.
			t.deliverReverseChunk(callID, payload)
		case msgError:
			return fmt.Errorf("host reported fatal error: %s", payload)
		default:
			return fmt.Errorf("unexpected message type 0x%02x", typ)
		}
	}
}

// dispatchInvoke runs one invoke-req on a worker goroutine. The host
// serializes invoke-reqs (single in-flight per process), so workers do not
// overlap in practice; the goroutine boundary still guarantees the read loop
// stays live while a handler runs — logs and reverse-resps keep flowing. A
// panicking handler is recovered into an in-band error response instead of
// killing the process.
func (t *processTransport) dispatchInvoke(payload []byte) {
	go func() {
		resp, err := t.safeHandleInvoke(payload)
		if err != nil {
			errBody, _ := json.Marshal(map[string]string{"error": err.Error()})
			resp = errBody
		}
		if werr := t.writeFrame(msgInvokeResp, "", resp); werr != nil {
			// Writing the response is the only signal a dead pipe gives a
			// worker; stop the whole process so run() can exit and fail the
			// pending reverse waiters too.
			t.abort(werr)
		}
	}()
}

func (t *processTransport) safeHandleInvoke(payload []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return t.handleInvokeReq(payload)
}

// abort unblocks the read loop when a worker observes a dead stdout pipe.
func (t *processTransport) abort(err error) {
	t.pendMu.Lock()
	if t.doneErr == nil {
		t.doneErr = err
	}
	t.pendMu.Unlock()
	// Nothing to cancel the blocking stdin read with (os.Pipe has no
	// deadline); rely on the host closing stdin on process teardown. The
	// failing write already told us the pipe is gone — EOF is imminent.
}

// handleInvokeReq dispatches one invoke-req. The payload is the JSON
// PluginAbiInvokeEnvelope — the same {Callable,Payload,RequestId,SessionId,
// CallSeq} shape as the FFI EncodeInvokeEnvelope wire format. On success the
// returned bytes are the raw JSON Response.Payload (already unwrapped, same
// as FFI DecodeInvokeFrame's return).
func (t *processTransport) handleInvokeReq(payload []byte) ([]byte, error) {
	var env gen.PluginAbiInvokeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode invoke envelope: %w", err)
	}
	if env.Callable == "" {
		return nil, fmt.Errorf("empty callable")
	}
	if env.Callable == ProcessOnLoadCallable {
		return t.handleOnLoad(env.Payload)
	}
	state, err := currentState()
	if err != nil {
		return nil, err
	}
	// emit writes one forward-chunk (0x08) frame per intermediate chunk a
	// streaming handler pushes, before the terminal 0x02 is written by the
	// dispatchInvoke caller. The empty callID matches the forward invoke-resp
	// convention (single in-flight forward call, host-serialized). Unary
	// handlers never call emit.
	emit := func(r Response) error {
		chunk, mErr := json.Marshal(r.Payload)
		if mErr != nil {
			return mErr
		}
		return t.writeFrame(msgForwardChunk, "", chunk)
	}
	return dispatchRequestStream(gen.PluginSdkRequest{
		PluginID:  state.pluginID,
		CallID:    env.Callable,
		Payload:   env.Payload,
		RequestID: env.RequestID,
		SessionID: env.SessionID,
		CallSeq:   env.CallSeq,
	}, emit)
}

// handleOnLoad runs the registered plugin's OnLoad lifecycle hook as the
// first invoke-req of a spawned plugin process. It delegates to
// HandleOnLoad so the loaded-flag transition and panic recovery stay in the
// single lifecycle implementation; the plugin identity is the manifest ID
// because the process transport does not carry the host's artifact ID.
//
// The onLoad invoke payload is the host-pushed per-instance config
// (LoadConfig: HTTP listener address + session cookie secret). It is passed
// through to HandleOnLoad as the config C-string; hosts that send no config
// (nil payload) get the legacy no-config behavior (no HTTP listener, cookie
// auth disabled). A successful load responds {"httpAddr": "<bound>"} when an
// HTTP listener was started (so the host can bootstrap the iframe backendUrl)
// or {} otherwise; failure surfaces as the run loop's {"error":...}.
func (t *processTransport) handleOnLoad(payload json.RawMessage) ([]byte, error) {
	plugin := registered()
	if plugin == nil {
		return nil, fmt.Errorf("plugin is not registered")
	}
	idBytes := append([]byte(plugin.Manifest.ID), 0)
	var cfgPtr unsafe.Pointer
	if len(payload) > 0 {
		cfgBytes := append([]byte(payload), 0)
		cfgPtr = unsafe.Pointer(&cfgBytes[0])
	}
	if status := HandleOnLoad(unsafe.Pointer(&idBytes[0]), cfgPtr); status != 0 {
		return nil, fmt.Errorf("plugin OnLoad failed (status %d)", status)
	}
	if srv := currentHTTPServer(); srv != nil {
		body, err := json.Marshal(map[string]string{"httpAddr": srv.Addr()})
		if err == nil {
			return body, nil
		}
	}
	return []byte("{}"), nil
}

// deliverReverse routes one 0x04 reverse-resp (the terminal frame of a
// reverse call, unary or streaming) to its pending waiter by the frame's
// correlation callID. The waiter leaves the map here: after the terminal
// frame no further frames belong to this call.
func (t *processTransport) deliverReverse(callID string, payload []byte) bool {
	t.pendMu.Lock()
	defer t.pendMu.Unlock()
	if t.doneErr != nil {
		return true // dying; drop
	}
	w, ok := t.pending[callID]
	if !ok {
		return false
	}
	delete(t.pending, callID)
	w.done <- payload
	return true
}

// deliverReverseChunk routes one 0x07 reverse stream chunk to its pending
// stream waiter. Unknown callIDs, unary waiters (nil signal), and abandoned
// streams are skipped without error — intermediate chunks are best-effort
// metadata, and killing the transport here would turn a benign race (chunk
// arriving after the consumer aborted) into a plugin crash.
//
// Delivery NEVER blocks the read loop (see reverseWaiter): the consumer may
// be parked in a nested reverse call issued from onChunk, and a blocking
// send here deadlocks the whole process once the buffer fills.
func (t *processTransport) deliverReverseChunk(callID string, payload []byte) {
	t.pendMu.Lock()
	w, ok := t.pending[callID]
	if !ok || w.signal == nil || w.abandoned.Load() {
		t.pendMu.Unlock()
		return
	}
	t.pendMu.Unlock()
	w.qmu.Lock()
	w.queue = append(w.queue, payload)
	w.qmu.Unlock()
	select {
	case w.signal <- struct{}{}:
	default:
	}
}

// processHost is the IPC-backed Host injected via SetHost by the subprocess
// entry point. Invoke travels as a correlated 0x03 reverse-req frame and
// waits for its 0x04 reverse-resp, mirroring the c-shared hostBridge wire
// semantics: {callID, JSON payload} request, raw-bytes response, and the
// __host_error__ error envelope decoded by the shared bridgeResult.
//
// Invoke may be called from ANY plugin goroutine — a dispatched handler, or
// a background worker started by one. The transport's read loop routes the
// correlated response back; concurrent calls multiplex over distinct
// callIDs.
type processHost struct {
	t *processTransport
}

func newProcessHost(t *processTransport) Host {
	return &processHost{t: t}
}

func (h *processHost) Invoke(callID string, payload any) ([]byte, error) {
	req, err := json.Marshal(map[string]any{"callID": callID, "payload": payload})
	if err != nil {
		return nil, err
	}
	t := h.t
	cid, w, err := t.beginReverse(false)
	if err != nil {
		return nil, err
	}
	if err := t.writeFrame(msgReverseReq, cid, req); err != nil {
		t.cancelReverse(cid)
		return nil, err
	}
	var resp []byte
	select {
	case resp = <-w.done:
	case <-t.done:
		return nil, fmt.Errorf("transport closed before response")
	}
	return terminalReverse(callID, resp)
}

// InvokeStream is the streaming variant of Invoke for host callables that
// support chunked delivery (llm.chat / llm.complete). The request frame
// carries {"stream": true}; hosts that understand it deliver intermediate
// 0x07 chunk frames (passed to onChunk in arrival order) before the terminal
// 0x04 response. Hosts that do not understand it ignore the flag and answer
// with the terminal frame only — zero intermediate chunks, terminal value
// still correct — so the same code works against old and new hosts.
//
// Returning an error from onChunk aborts the stream: InvokeStream returns
// that error immediately, the waiter is marked abandoned (the host's
// terminal frame still cleans it up), no further chunks are delivered, and a
// 0x09 reverse-cancel frame asks the host to abort its upstream dispatch.
func (h *processHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	return h.InvokeStreamCtx(context.Background(), callID, payload, onChunk)
}

// InvokeStreamCtx is the cancellation-aware core (sdk.CanceledHost): the
// legacy InvokeStream delegates with a background context. On ctx expiry the
// waiter is abandoned and a 0x09 reverse-cancel frame tells the host to stop
// the upstream dispatch — the running LLM stream stops instead of billing to
// completion on a stream nobody consumes.
func (h *processHost) InvokeStreamCtx(ctx context.Context, callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if onChunk == nil {
		return h.Invoke(callID, payload)
	}
	req, err := json.Marshal(map[string]any{"callID": callID, "payload": payload, "stream": true})
	if err != nil {
		return nil, err
	}
	t := h.t
	cid, w, err := t.beginReverse(true)
	if err != nil {
		return nil, err
	}
	if err := t.writeFrame(msgReverseReq, cid, req); err != nil {
		t.cancelReverse(cid)
		return nil, err
	}

	var final []byte
	for final == nil {
		select {
		case <-w.signal:
			for _, c := range w.popQueued() {
				if err := onChunk(c); err != nil {
					w.abandoned.Store(true)
					t.abortReverse(cid)
					return nil, err
				}
			}
		case resp := <-w.done:
			final = resp
		case <-t.done:
			return nil, fmt.Errorf("transport closed before response")
		case <-ctx.Done():
			w.abandoned.Store(true)
			t.abortReverse(cid)
			return nil, fmt.Errorf("%s: %w", callID, ctx.Err())
		}
	}
	// The host writes chunks strictly before the terminal frame and the read
	// loop queues them in wire order, so anything still queued when done
	// fired is the tail of the stream — drain it before returning so the
	// consumer never loses ordering.
	for _, c := range w.popQueued() {
		if err := onChunk(c); err != nil {
			return nil, err
		}
	}
	return terminalReverse(callID, final)
}

// abortReverse sends the 0x09 reverse-cancel frame for one in-flight reverse
// call. Best-effort by design: a dead pipe is already tearing the transport
// down (nothing left to abort on the host side), and an old host that does
// not know the frame type still settles the call via its terminal 0x04 —
// the plugin just returns early, the legacy abandoned-stream behavior.
func (t *processTransport) abortReverse(cid string) {
	_ = t.writeFrame(msgReverseCancel, cid, nil)
}

// beginReverse registers a waiter for one reverse call under a fresh
// correlation callID and returns both. The cid and the map insert happen
// under one lock so the read loop can never observe a frame for a callID
// that is not yet registered.
//
// Waiter lifecycle: normally the terminal 0x04 frame (deliverReverse) or the
// transport teardown (failAllPending) removes it from the map. A stream
// whose consumer returns early (onChunk error) instead marks the waiter
// abandoned — it stays registered so the inevitable terminal frame still
// clears it, while chunk delivery is skipped.
func (t *processTransport) beginReverse(stream bool) (string, *reverseWaiter, error) {
	w := &reverseWaiter{done: make(chan []byte, 1)}
	if stream {
		w.signal = make(chan struct{}, 1)
	}
	cid := fmt.Sprintf("r%d", t.nextID.Add(1))
	t.pendMu.Lock()
	defer t.pendMu.Unlock()
	if t.doneErr != nil {
		return "", nil, fmt.Errorf("transport closed: %w", t.doneErr)
	}
	t.pending[cid] = w
	return cid, w, nil
}

// cancelReverse removes a waiter whose frame could not be written (dead
// pipe) so later frames for it do not leak.
func (t *processTransport) cancelReverse(cid string) {
	t.pendMu.Lock()
	delete(t.pending, cid)
	t.pendMu.Unlock()
}

// terminalReverse validates the terminal payload shared by Invoke and
// InvokeStream: transport-dead markers and __host_error__ envelopes become
// Go errors; anything else passes through as the raw response bytes (host
// pluginhost decode semantics).
func terminalReverse(callID string, resp []byte) ([]byte, error) {
	if raw, dead := cutPrefix(string(resp), "__transport_dead__:"); dead {
		return nil, fmt.Errorf("host bridge transport closed: %s", raw)
	}
	if _, err := bridgeResult(callID, resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

