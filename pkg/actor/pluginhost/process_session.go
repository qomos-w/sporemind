package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// frameSession is the subprocess session: a permanently-running reader loop
// that demultiplexes the plugin's stdout frames, plus a locked write path for
// everything the host sends. The protocol is full-duplex: every frame carries
// a correlation callID, reverse bridge calls may arrive at any time (not
// only during an invoke dispatch window), and the session dispatches them on
// bounded workers concurrently with the (single-flight) forward invoke path.
//
// Transport neutrality is the point of the type: it speaks io.Reader/io.Writer
// and the frame envelope from process_frame.go, never os/exec. The stdio lane
// is one instantiation; the distributed (host-to-host) lane mounts the same
// session over a gospore Transport stream later, so plugin semantics stay
// identical whether the app runs in-process, in a local subprocess, or on a
// remote host.
//
// Concurrency model:
//   - One reader goroutine (start) owns the Read side exclusively.
//   - All writes go through writeMu (invoke-req from callers, reverse-resp
//     from bounded reverse workers).
//   - Invokes stay single-flight (invokeSem) — the host serializes
//     invoke-reqs per plugin process; the callID machinery is in place so a
//     future multiplexed or remote lane can lift that without touching this
//     type's contract.
type frameSession struct {
	pluginID string
	read     io.Reader
	write    io.Writer

	writeMu sync.Mutex

	// invokeSem is the single-flight invoke token: one slot, held for the whole
	// call (req write through resp) — the forward path carries no per-request
	// correlation, so two in-flight invokes would cross-wire responses. A
	// buffered channel rather than a mutex so a queued caller is admitted the
	// instant the running invoke releases (no poll granularity capping
	// throughput) and can wait on its own budget at the same time.
	invokeSem chan struct{}

	// deliverMu guards the waiter handoff only (never held across a wait),
	// so the reader loop can deliver while a call is parked in its select.
	// invokeCtx is the current in-flight invoke's context: a reverse call
	// arriving while an invoke is in flight inherits that invoke's context
	// for its budget (reverseBudgetContext semantics preserved from v1).
	deliverMu     sync.Mutex
	deliverCh     chan Frame
	deliverChunks chan []byte // forward stream chunks (0x08); non-nil only during callStream
	invokeCtx     context.Context

	reverse      ReverseHandler
	reverseSlots chan struct{} // bounded concurrency for reverse dispatch
	onLog        func([]byte)
	onFatal      func(error)
	// cancelMu guards revCancel: the per-reverse-call cancel funcs registered
	// by serveReverseAsync and removed on completion or eviction. A 0x09
	// reverse-cancel frame from the plugin looks its correlation ID up here.
	cancelMu  sync.Mutex
	revCancel map[string]context.CancelFunc
	// onReverseError surfaces a failed host-side reverse dispatch (bridge
	// routing, eviction, deadline) into the plugin's log ring, where
	// pluginhost.plugin_logs can tail it — the diagnostic path for
	// stream-eviction class failures that the plugin only sees as an opaque
	// __host_error__ terminal.
	onReverseError func(svcCallID string, err error)

	// settleGrace is how long an over-budget admitted invoke keeps its waiter
	// installed (see defaultSettleGrace). Configurable for tests.
	settleGrace time.Duration

	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

// defaultReverseConcurrency bounds how many plugin reverse bridge calls may
// execute at once. Reverse dispatches hit host services (sshmanager, state,
// …); a runaway plugin must not be able to fork-bomb the host side.
const defaultReverseConcurrency = 4

// forwardChunkBuf bounds the host-side buffer for forward stream chunks
// (0x08). Mirrors the SDK's reverse streamChunkBuf: a slow onChunk consumer
// exerts backpressure on the plugin's chunk writer via a full buffer, not a
// drop; a withdrawn call drains it so the readLoop's send unblocks.
const forwardChunkBuf = 8

type frameSessionConfig struct {
	PluginID       string
	Read           io.Reader
	Write          io.Writer
	Reverse        ReverseHandler
	ReverseLimit   int                               // <=0 → defaultReverseConcurrency
	OnLog          func([]byte)                      // optional, 0x05 log frames
	OnFatal        func(error)                       // required: reader EOF / 0x06 / protocol violation
	OnReverseError func(svcCallID string, err error) // optional, host-side dispatch failures
	SettleGrace    time.Duration                     // <=0 → defaultSettleGrace
}

func newFrameSession(cfg frameSessionConfig) *frameSession {
	limit := cfg.ReverseLimit
	if limit <= 0 {
		limit = defaultReverseConcurrency
	}
	s := &frameSession{
		pluginID:       cfg.PluginID,
		read:           cfg.Read,
		write:          cfg.Write,
		reverse:        cfg.Reverse,
		reverseSlots:   make(chan struct{}, limit),
		revCancel:      make(map[string]context.CancelFunc),
		onLog:          cfg.OnLog,
		onFatal:        cfg.OnFatal,
		onReverseError: cfg.OnReverseError,
		settleGrace:    cfg.SettleGrace,
		closed:         make(chan struct{}),
	}
	if s.settleGrace <= 0 {
		s.settleGrace = defaultSettleGrace
	}
	// Seed the single-flight token so the first invoke is admitted immediately.
	s.invokeSem = make(chan struct{}, 1)
	s.invokeSem <- struct{}{}
	return s
}

// start launches the permanent reader loop. Call exactly once after the
// underlying transport is connected and before any call().
func (s *frameSession) start() {
	go s.readLoop()
}

// readLoop is the single owner of the read side. It runs until the stream
// errors (EOF on child exit, or a protocol violation) and then settles the
// session exactly once: every pending invoke waiter gets the error.
func (s *frameSession) readLoop() {
	for {
		frame, err := ReadTransportFrame(s.read)
		if err != nil {
			s.settle(fmt.Errorf("plugin %s: session read: %w", s.pluginID, err))
			return
		}
		switch frame.Type {
		case MsgInvokeResp:
			if !s.deliverInvoke(frame) {
				s.settle(fmt.Errorf("plugin %s: invoke-resp with no invoke in flight", s.pluginID))
				return
			}
		case MsgReverseReq:
			corrID, svcCallID, body, stream, derr := decodeReverseFrame(frame)
			if derr != nil {
				s.replyReverse(corrID, hostErrorEnvelope(derr.Error()))
				continue
			}
			s.serveReverseAsync(corrID, svcCallID, body, stream)
		case MsgForwardChunk:
			s.deliverForwardChunk(frame)
		case MsgReverseCancel:
			s.cancelReverseDispatch(frame.CallID)
		case MsgLog:
			if s.onLog != nil {
				s.onLog(frame.Payload)
			}
		case MsgError:
			s.settle(fmt.Errorf("plugin %s: plugin fatal: %s", s.pluginID, truncateForLog(frame.Payload)))
			return
		default:
			s.settle(fmt.Errorf("plugin %s: unexpected message type 0x%02x", s.pluginID, frame.Type))
			return
		}
	}
}

// decodeReverseFrame extracts the correlation ID and the service callID +
// payload (+ stream negotiation flag) from a reverse-req frame. The frame
// header carries the correlation callID; the JSON body carries the service
// callID and payload ({callID, payload, stream?}). The dispatch uses the
// service callID (capability mapping, host-service routing), the reply uses
// the correlation ID.
func decodeReverseFrame(frame Frame) (corrID, svcCallID string, payload []byte, stream bool, err error) {
	var rev reverseReq
	if uerr := json.Unmarshal(frame.Payload, &rev); uerr != nil {
		return frame.CallID, "", nil, false, fmt.Errorf("decode reverse-req: %w", uerr)
	}
	if rev.CallID == "" {
		return frame.CallID, "", nil, false, fmt.Errorf("reverse-req without service callID")
	}
	return frame.CallID, rev.CallID, []byte(rev.Payload), rev.Stream, nil
}

// deliverInvoke hands the invoke-resp to the waiting call, if any. With
// invokes serialized for their full duration there is exactly one waiter
// between each invoke-req and its response — except a late resp racing a
// timed-out call's withdrawal. That late resp is DROPPED, not a protocol
// violation: frames are length-prefixed so the stream stays in sync, and the
// timed-out caller kills the child anyway.
func (s *frameSession) deliverInvoke(frame Frame) bool {
	s.deliverMu.Lock()
	ch := s.deliverCh
	if ch != nil {
		s.deliverCh = nil
		s.deliverChunks = nil
		s.invokeCtx = nil
	}
	s.deliverMu.Unlock()
	if ch == nil {
		return false
	}
	ch <- frame
	return true
}

// deliverForwardChunk hands one forward-chunk (0x08) to the waiting call
// stream, if any. With invokes single-flight there is at most one stream
// waiter; a chunk racing a withdrawn or unary call (deliverChunks nil) is
// dropped — frames are length-prefixed so the stream stays in sync, and the
// caller kills the child on abort, mirroring the late-resp drop in
// deliverInvoke.
func (s *frameSession) deliverForwardChunk(frame Frame) {
	s.deliverMu.Lock()
	ch := s.deliverChunks
	s.deliverMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- frame.Payload:
	case <-s.closed:
	}
}

// call writes one invoke-req and blocks until its invoke-resp, the caller's
// context expires, or the session settles fatally. Invokes are single-flight
// for the FULL call duration (invokeSem held until the response arrives): the
// forward wire shape carries no correlation, so two concurrent invokes would
// cross-wire their responses.
//
// Timeout policy (including killing the child process) stays with the caller
// — the session only fails the wait.
func (s *frameSession) call(ctx context.Context, payload []byte) ([]byte, error) {
	if err := s.acquireInvoke(ctx); err != nil {
		return nil, err
	}
	defer s.releaseInvoke()
	if err := ctx.Err(); err != nil {
		// Lost the race between admission and installing the waiter: report a
		// queued failure, never a bare ctx error — the opener treats a
		// non-queued ctx error as a hung plugin and kills the process.
		return nil, fmt.Errorf("%w (budget expired at admission): %w", errInvokeQueued, err)
	}
	ch := make(chan Frame, 1)
	s.deliverMu.Lock()
	s.deliverCh = ch
	s.invokeCtx = ctx
	s.deliverMu.Unlock()
	s.writeMu.Lock()
	err := WriteTransportFrame(s.write, MsgInvokeReq, "", payload)
	s.writeMu.Unlock()
	if err != nil {
		s.clearWaiter()
		return nil, fmt.Errorf("write invoke-req: %w", err)
	}

	select {
	case frame := <-ch:
		return frame.Payload, nil
	case <-ctx.Done():
		// Overran budget while admitted. Do NOT withdraw the waiter: the
		// plugin still owes exactly one invoke-resp, and releasing the token
		// now would let the next invoke cross-wire with (or fatally choke on)
		// this one's late response. Park for the settle grace instead.
		return nil, s.settleOverrun(ch, nil, fmt.Errorf("%w: %w", errInvokeOverran, ctx.Err()))
	case <-s.closed:
		s.clearWaiter()
		return nil, fmt.Errorf("session closed: %w", s.closeErr)
	}
}

// settleOverrun parks a failed-but-admitted invoke until the plugin's late
// terminal invoke-resp arrives (consumed and discarded — the caller already
// failed) or the settle grace elapses. Late settle returns cause with the wire
// still single-flight clean (the token releases normally via the caller's
// defer). Grace expiry means the plugin is genuinely unresponsive: the session
// settles fatally and the standard wedge kill reaps the process. chunks, when
// non-nil, is a stream's chunk buffer: the read side drops new chunks while
// the loop drains buffered ones so the readLoop never blocks on a full buffer.
func (s *frameSession) settleOverrun(ch chan Frame, chunks chan []byte, cause error) error {
	if chunks != nil {
		s.deliverMu.Lock()
		s.deliverChunks = nil // read-side drops new chunks; buffered drained below
		s.deliverMu.Unlock()
	}
	grace := time.NewTimer(s.settleGrace)
	defer grace.Stop()
	for {
		select {
		case <-ch:
			// Late but present: the plugin answered. Consume the terminal so
			// the wire stays single-flight clean.
			if chunks != nil {
				s.drainChunks(chunks)
			}
			s.clearWaiter()
			return cause
		case <-chunks:
			// Discard buffered chunks while parked (nil channel never fires).
		case <-grace.C:
			// Unresponsive beyond budget+grace: fatal desync. onFatal records
			// the exit and reaps the process.
			s.settle(fmt.Errorf("plugin %s: admitted invoke produced no response within the settle grace (desync)", s.pluginID))
			s.clearWaiter()
			if chunks != nil {
				s.drainChunks(chunks)
			}
			return cause
		case <-s.closed:
			s.clearWaiter()
			if chunks != nil {
				s.drainChunks(chunks)
			}
			return cause
		}
	}
}

// clearWaiter withdraws the current waiter channels so late frames are
// dropped instead of delivered to a dead call.
func (s *frameSession) clearWaiter() {
	s.deliverMu.Lock()
	s.deliverCh = nil
	s.deliverChunks = nil
	s.invokeCtx = nil
	s.deliverMu.Unlock()
}

// errInvokeQueued reports single-flight admission failure: the invoke waited
// on the single-flight token behind a running invoke until its own budget
// expired. The plugin process itself is healthy — the opener must fail the
// invoke WITHOUT killing the subprocess (the kill path is reserved for a
// plugin that genuinely exceeded its declared budget).
var errInvokeQueued = errors.New("invoke queued behind a running invoke")

// errInvokeOverran reports an ADMITTED invoke whose caller budget expired and
// which then consumed the plugin's late terminal response within the settle
// grace: the process is healthy and the forward wire is still single-flight
// clean — callers must fail this invoke without killing the process.
var errInvokeOverran = errors.New("invoke overran caller budget (settled late)")

// defaultSettleGrace bounds how long an over-budget (or consumer-aborted)
// admitted invoke keeps its waiter installed to consume the plugin's late
// terminal response. The forward wire carries no per-request correlation, so
// withdrawing the waiter while the plugin still owes a response lets the next
// admitted invoke cross-wire with — or fatally choke on — that response (the
// novelking crash-loop under multi-agent step floods). A plugin that misses
// budget + grace is genuinely wedged: the session settles fatally and the
// standard wedge kill applies.
const defaultSettleGrace = 5 * time.Second

// acquireInvoke takes the single-flight invoke token or fails once ctx expires
// while queued. Invokes are single-flight for the full call duration (no
// per-request correlation on the forward wire), so a second invoke queued
// behind a long-running one would otherwise wait while its own appdef-declared
// budget burns; once admitted it would fail from inside its first reverse call
// with a misleading "reverse call <id>: context deadline exceeded". Failing at
// admission with an explicit queue-exhausted error makes the starvation
// diagnosable instead of disguised.
//
// The token is a channel, so admission is handed off the instant the running
// invoke releases (a mutex + timer poll capped this at one admission per poll
// interval, which a high-rate best-effort event fan-out outran). A caller
// whose budget expires during the handoff gives the token back and is reported
// as queued — never admitted with a dead context, which the opener would
// misclassify as a hung process and kill.
func (s *frameSession) acquireInvoke(ctx context.Context) error {
	select {
	case <-s.invokeSem:
		if err := ctx.Err(); err != nil {
			s.releaseInvoke()
			return fmt.Errorf("%w (budget expired at admission): %w", errInvokeQueued, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w; budget exhausted while waiting: %w", errInvokeQueued, ctx.Err())
	case <-s.closed:
		return fmt.Errorf("session closed: %w", s.closeErr)
	}
}

// releaseInvoke returns the single-flight token. Every successful
// acquireInvoke is paired with exactly one releaseInvoke.
func (s *frameSession) releaseInvoke() {
	s.invokeSem <- struct{}{}
}

// drainChunks empties a withdrawn call stream's chunk buffer so a readLoop
// send blocked on a full buffer unblocks instead of stalling the session.
// The captured channel becomes garbage after withdrawal (a fresh call
// installs a new one), so drained entries need no further handling.
func (s *frameSession) drainChunks(ch chan []byte) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// callStream is the streaming variant of call: the plugin may push any number
// of 0x08 forward-chunk frames before its terminal 0x02. onChunk fires per
// chunk payload in wire order; the returned bytes are the terminal payload.
// An onChunk error or context expiry parks the waiter for the settle grace to
// consume the terminal (no-poison rule, see settleOverrun) instead of
// withdrawing it while the plugin still owes frames.
func (s *frameSession) callStream(ctx context.Context, payload []byte, onChunk func([]byte) error) ([]byte, error) {
	if err := s.acquireInvoke(ctx); err != nil {
		return nil, err
	}
	defer s.releaseInvoke()
	if err := ctx.Err(); err != nil {
		// Lost the race between admission and installing the waiter: report a
		// queued failure, never a bare ctx error — the opener treats a
		// non-queued ctx error as a hung plugin and kills the process.
		return nil, fmt.Errorf("%w (budget expired at admission): %w", errInvokeQueued, err)
	}
	ch := make(chan Frame, 1)
	chunks := make(chan []byte, forwardChunkBuf)
	s.deliverMu.Lock()
	s.deliverCh = ch
	s.deliverChunks = chunks
	s.invokeCtx = ctx
	s.deliverMu.Unlock()
	s.writeMu.Lock()
	err := WriteTransportFrame(s.write, MsgInvokeReq, "", payload)
	s.writeMu.Unlock()
	if err != nil {
		s.clearWaiter()
		return nil, fmt.Errorf("write invoke-req: %w", err)
	}

	for {
		select {
		case chunk := <-chunks:
			if onChunk != nil {
				if cerr := onChunk(chunk); cerr != nil {
					// Consumer aborted mid-stream: the plugin still owes a
					// terminal invoke-resp — park for it (no-poison rule).
					return nil, s.settleOverrun(ch, chunks, cerr)
				}
			}
		case frame := <-ch:
			// Drain chunks delivered before the terminal but not yet consumed
			// (the readLoop delivers in wire order; the terminal select may
			// win the race over buffered chunks).
			for {
				select {
				case chunk := <-chunks:
					if onChunk != nil {
						if cerr := onChunk(chunk); cerr != nil {
							return nil, cerr
						}
					}
				default:
					return frame.Payload, nil
				}
			}
		case <-ctx.Done():
			// Overran budget while admitted — same no-poison rule as call.
			return nil, s.settleOverrun(ch, chunks, fmt.Errorf("%w: %w", errInvokeOverran, ctx.Err()))
		case <-s.closed:
			s.clearWaiter()
			return nil, fmt.Errorf("session closed: %w", s.closeErr)
		}
	}
}

// serveReverseAsync dispatches one reverse-req on a bounded worker: the
// SERVICE callID goes to the reverse handler (capability gate + host
// routing), the CORRELATION ID rides the reply. Failures are always answered
// with a __host_error__ envelope — never a dropped pipe.
//
// stream selects chunked delivery: intermediate chunks are written as 0x07
// frames on the same correlation ID before the terminal 0x04. Ordering is
// structural: onChunk fires synchronously inside the dispatch goroutine
// (chunk frames leave through writeMu), and the terminal frame is written by
// this goroutine only after the dispatch returns — every chunk frame is
// fully written before the terminal frame's write starts.
func (s *frameSession) serveReverseAsync(corrID, svcCallID string, payload []byte, stream bool) {
	select {
	case s.reverseSlots <- struct{}{}:
	case <-s.closed:
		return
	}
	go func() {
		defer func() {
			<-s.reverseSlots
			s.unregisterReverseCancel(corrID)
		}()
		// A reverse call racing its own session's death must not hang the
		// worker. Reverse calls arriving while an invoke is in flight
		// inherit that invoke's context (v1 nested-budget semantics);
		// background reverse calls (v2 children only) run on the standalone
		// budget.
		ctx := context.Background()
		var rctx DispatchContext
		s.deliverMu.Lock()
		if s.invokeCtx != nil {
			if meta, ok := pluginhost.InvokeMetaFrom(s.invokeCtx); ok {
				// Inherit ONLY identity-carrying invokes (agent/session-facing
				// plugin.invoke). Best-effort event deliveries (the __event__
				// fan-out) carry no InvokeMeta and complete in milliseconds;
				// inheriting one bound an unrelated HTTP-data-path reverse call
				// to a ctx that dies the moment that event invoke returns —
				// surfaced as reverse call "state.get": context canceled on
				// every panel data load while any agent was streaming.
				ctx = s.invokeCtx
				// Caller identity rides the same invoke context so reverse calls
				// issued mid-invoke (the common case: handler calls llm.complete)
				// are attributed to the originating agent.
				rctx = DispatchContext{AgentID: meta.AgentID, WorkspaceID: meta.WorkspaceID}
				if deadline, ok := s.invokeCtx.Deadline(); ok {
					rctx.DeadlineAt = deadline.UnixMilli()
				}
			}
		}
		s.deliverMu.Unlock()
		dctx, cancel := reverseBudgetContext(ctx, svcCallID)
		defer cancel()
		// The cancel func doubles as the 0x09 abort target: a reverse-cancel
		// frame from the plugin fires it so the dispatch (and its upstream
		// LLM stream, via the DispatchContext parent) unwinds instead of
		// running to completion on a stream nobody consumes.
		s.registerReverseCancel(corrID, cancel)
		// rctx.Parent threads the same cancellation into the streaming
		// forwarder: the forwarder's streamBudgetContext derives its ctx
		// from this parent, so the upstream Invoke/Next loop unwinds on
		// 0x09 and the reverse slot frees without waiting for the LLM
		// stream to finish on its own.
		rctx.Parent = dctx

		var onChunk func([]byte) error
		if stream {
			onChunk = func(chunk []byte) error {
				return s.replyReverseChunk(corrID, chunk)
			}
		}
		resp, err := dispatchWithinBudget(dctx, s.reverse, rctx, svcCallID, payload, onChunk)
		if err != nil {
			if s.onReverseError != nil {
				s.onReverseError(svcCallID, err)
			}
			s.replyReverse(corrID, hostErrorEnvelope(err.Error()))
			return
		}
		if len(resp) == 0 {
			resp = []byte("{}")
		}
		s.replyReverse(corrID, resp)
	}()
}

// replyReverse writes the correlated 0x04 reverse-resp.
func (s *frameSession) replyReverse(callID string, payload []byte) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return
	default:
	}
	_ = WriteTransportFrame(s.write, MsgReverseResp, callID, payload)
}

// replyReverseChunk writes one correlated 0x07 reverse stream chunk. It
// shares writeMu with replyReverse so chunk frames serialize strictly before
// the terminal 0x04 of the same call (see serveReverseAsync). A closed
// session reports an error so the dispatch-side onChunk chain aborts instead
// of spinning on a dead pipe.
func (s *frameSession) replyReverseChunk(callID string, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.closed:
		return fmt.Errorf("plugin %s: session closed before chunk delivered", s.pluginID)
	default:
	}
	if err := WriteTransportFrame(s.write, MsgReverseChunk, callID, payload); err != nil {
		return fmt.Errorf("plugin %s: write reverse chunk: %w", s.pluginID, err)
	}
	return nil
}

// registerReverseCancel records the cancel func of one in-flight reverse
// dispatch under its correlation ID so a 0x09 reverse-cancel frame can abort
// it. Callers fire the dctx-derived cancel; dispatches that never registered
// (already finished, never started) are silently ignored.
func (s *frameSession) registerReverseCancel(corrID string, cancel context.CancelFunc) {
	s.cancelMu.Lock()
	s.revCancel[corrID] = cancel
	s.cancelMu.Unlock()
}

// unregisterReverseCancel removes a finished dispatch's registration.
func (s *frameSession) unregisterReverseCancel(corrID string) {
	s.cancelMu.Lock()
	delete(s.revCancel, corrID)
	s.cancelMu.Unlock()
}

// cancelReverseDispatch fires the cancel func of the reverse dispatch named
// by corrID. Unknown IDs (already-terminated calls, forged/raced frames)
// are dropped without error: the frame is advisory, and a late cancel for a
// dead call has nothing to abort.
func (s *frameSession) cancelReverseDispatch(corrID string) {
	s.cancelMu.Lock()
	cancel, ok := s.revCancel[corrID]
	if ok {
		delete(s.revCancel, corrID)
	}
	s.cancelMu.Unlock()
	if ok {
		cancel()
	}
}

func hostErrorEnvelope(msg string) []byte {
	body, _ := json.Marshal(map[string]string{"__host_error__": msg})
	return body
}

// settled exposes the terminal channel for tests: it closes exactly once
// when the session settles (fatal or host-close).
func (s *frameSession) settled() <-chan struct{} { return s.closed }

// settle records the fatal condition, wakes every waiter and stops accepting
// work. Idempotent.
func (s *frameSession) settle(err error) {
	s.closeOnce.Do(func() {
		s.closeErr = err
		close(s.closed)
		if s.onFatal != nil {
			s.onFatal(err)
		}
	})
}

// errSessionClosedByHost marks a host-initiated session close (unload). It
// is not a crash: the opener's OnFatal callback skips recordExit for it so a
// subsequent invoke reports the normal "not running (unloaded)" condition
// instead of a crash cause.
var errSessionClosedByHost = fmt.Errorf("session closed by host")

// close terminates the session (reader observes EOF on its own once the
// process dies; close only guarantees no further waits hang).
func (s *frameSession) close() {
	s.settleQuiet(fmt.Errorf("plugin %s: %w", s.pluginID, errSessionClosedByHost))
}

// settleQuiet records the condition without notifying OnFatal.
func (s *frameSession) settleQuiet(err error) {
	s.closeOnce.Do(func() {
		s.closeErr = err
		close(s.closed)
	})
}
