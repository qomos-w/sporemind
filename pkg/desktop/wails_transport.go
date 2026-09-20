package desktop

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Wails raw transport event names used by the frontend @qomos/gospore-client
// WailsRawFrameConnection.
const (
	rawTransportFrameEvent = "sporemind:raw:frame"
	rawTransportCloseEvent = "sporemind:raw:close"
	rawTransportErrorEvent = "sporemind:raw:error"
	rawTransportOpenEvent  = "sporemind:raw:open"
)

type rawEnvelopeType string

const (
	rawEnvelopeConnect rawEnvelopeType = "connect"
	rawEnvelopeFrame   rawEnvelopeType = "frame"
	rawEnvelopeClose   rawEnvelopeType = "close"
)

// rawEnvelope is the JSON envelope sent by the frontend over System.invoke.
type rawEnvelope struct {
	Type  rawEnvelopeType `json:"type"`
	Token string          `json:"token,omitempty"`
	Data  string          `json:"data,omitempty"` // base64-encoded binary wire frame
}

// Batched outbound transport tuning. Frame events are accumulated into a single
// DispatchWailsEvent carrying a frames[] array, trading a small flush latency
// for collapsing N main-thread InvokeSync round-trips into one. A size threshold
// forces a flush when many frames arrive at once (e.g. LLM streaming); the time
// window bounds the latency of sparse frames.
const (
	rawTransportMaxBatchSize  = 64
	rawTransportFlushInterval = 2 * time.Millisecond
)

// A single DispatchWailsEvent runs ExecJS → InvokeSync, blocking until the
// Wails main thread executes the JS. While the renderer is suspended (a
// hidden/minimized/occluded WebView2 window suspends its renderer — see
// app.go) this block outlasts any short budget, but it is not a wedge: once
// the renderer resumes the queued dispatches complete and the buffered frames
// flush in order. So a stall past dispatchTimeout is only logged; the session
// stays open and lossless because frames spill into the overflow queue (below)
// instead of back-pressuring the gateway. Only a stall that outlasts
// stallForceClose — minutes of zero progress, i.e. a dead renderer rather than
// a paused one — force-closes the transport so the gateway session tears down
// and the frontend reconnects (gospore event rings replay the gap via
// sinceSeqNo).
//
// A minimised window is the deliberate exception: its renderer stays suspended
// for as long as the user keeps it minimised — normal usage that can far
// outlast the horizon — so window minimise/hide/restore events gate the
// force-close (see subscribeRendererSuspension) and the overflow budget alone
// bounds buffered memory while suspended.
const (
	rawTransportDispatchTimeout = 15 * time.Second
	rawTransportStallForceClose = 10 * time.Minute
)

// rawTransportOverflowBudget bounds the overflow queue that absorbs frames
// while the renderer cannot accept dispatches. Send never blocks on a full
// outCh — it spills here instead — so a suspended renderer stalls the stream
// without back-pressuring the gateway session (the workspace-freeze chain) and
// without dropping a frame. The buffered frames flush in order once the
// renderer resumes. Exceeding the budget force-closes the transport; the
// frontend reconnects and resubscribes, and gospore event rings replay the
// gap, so recovery is lossless for ring-backed (message) streams.
const rawTransportOverflowBudget = 32 << 20 // 32 MiB

// Close waits at most this long for writeLoop to flush pending frames. When
// writeLoop is wedged inside a stalled dispatch, abandoning the drain is
// preferable to holding the gateway teardown (sess.send → conn.Close) hostage:
// an unbounded wait would leak the whole session worker pool and keep the
// session's subscriptions alive.
const rawTransportCloseDrainTimeout = 5 * time.Second

// rawFramePayload is the single-frame shape inside a batch. A named struct
// avoids the per-frame map allocation of map[string]string{"data": ...}.
type rawFramePayload struct {
	Data string `json:"data"` // base64-encoded binary wire frame
}

// rawBatchPayload is the batch envelope: one event carrying N base64 frames,
// processed in array order by the frontend.
type rawBatchPayload struct {
	Frames []rawFramePayload `json:"frames"`
}

// outbound discriminates batched frame payloads from control events
// (open/close/error) that must be delivered immediately. A control event
// flushes any pending frames first so order is preserved across the boundary.
type outbound struct {
	frame   string                   // base64-encoded wire frame; used when control == nil
	control *application.CustomEvent // non-nil for immediate control events
}

// encodeWireBase64 base64-encodes data into a pooled scratch buffer and returns
// the encoded string. Only the string escapes the pool; the buffer is reused.
func encodeWireBase64(data []byte) string {
	n := base64.StdEncoding.EncodedLen(len(data))
	bp := b64BufPool.Get().(*[]byte)
	if cap(*bp) < n {
		*bp = make([]byte, n)
	}
	b := (*bp)[:n]
	base64.StdEncoding.Encode(b, data)
	s := string(b) // copy out; buffer is reused below
	*bp = b[:0]
	b64BufPool.Put(bp)
	return s
}

// decodeWireBase64 base64-decodes s into a pooled scratch buffer and invokes fn
// with the decoded bytes. The buffer is reused once fn returns, so fn must copy
// any data it needs to keep (gateway.UnmarshalWireFrame already copies).
func decodeWireBase64(s string, fn func([]byte) error) error {
	n := base64.StdEncoding.DecodedLen(len(s))
	bp := b64BufPool.Get().(*[]byte)
	if cap(*bp) < n {
		*bp = make([]byte, n)
	}
	b := (*bp)[:n]
	m, err := base64.StdEncoding.Decode(b, []byte(s))
	if err != nil {
		*bp = b[:0]
		b64BufPool.Put(bp)
		return err
	}
	fnErr := fn(b[:m])
	*bp = b[:0]
	b64BufPool.Put(bp)
	return fnErr
}

// wailsWindow is the minimal subset of application.Window used by the raw
// transport. DispatchWailsEvent is used directly (synchronous, main-thread
// FIFO) instead of EmitEvent, which spawns a goroutine per dispatch and
// destroys frame ordering.
type wailsWindow interface {
	Name() string
	DispatchWailsEvent(event *application.CustomEvent)
}

// windowEventSubscriber is the optional surface of application.Window used to
// track renderer suspension. Real Wails windows implement it; test fakes may
// not, in which case no suspension gating happens.
type windowEventSubscriber interface {
	OnWindowEvent(eventType events.WindowEventType, callback func(event *application.WindowEvent)) func()
}

// subscribeRendererSuspension mirrors the main window's minimise/hide pairing
// (see app.go registerMainWindowEvents): a minimised/hidden WebView2 window
// suspends its renderer, so dispatch stalls on it are expected rather than
// fatal and the stall watchdog defers force-close while suspended. The events
// are processed before suspension engages (minimise precedes any dispatch that
// could park on it), so the flag reliably precedes the stall. Returns a cancel
// that the session teardown calls so reconnects don't leak listeners.
func subscribeRendererSuspension(window wailsWindow, conn *wailsRawFrameConn) (cancel func()) {
	sub, ok := window.(windowEventSubscriber)
	if !ok {
		return func() {}
	}
	cancels := make([]func(), 0, 4)
	for _, s := range []struct {
		event   events.WindowEventType
		suspend bool
	}{
		{events.Common.WindowMinimise, true},
		{events.Common.WindowHide, true},
		{events.Common.WindowUnMinimise, false},
		{events.Common.WindowRestore, false},
	} {
		suspend := s.suspend
		cancels = append(cancels, sub.OnWindowEvent(s.event, func(*application.WindowEvent) {
			conn.suspended.Store(suspend)
		}))
	}
	return func() {
		for _, c := range cancels {
			c()
		}
	}
}

// wailsRawFrameConn implements gateway.FrameConn over Wails raw messages and
// window-scoped events. It guarantees that the gospore session sees inbound
// frames in the exact order the frontend emitted them by reading them from a
// single Go channel consumed by ServeSession.
//
// Outbound frames are serialized through a single writer goroutine (writeLoop)
// that drains outCh and calls DispatchWailsEvent synchronously. This is
// critical: EmitEvent spawns a goroutine per dispatch, which reorders frames
// and trips the client's transId-gap detector.
type wailsRawFrameConn struct {
	window wailsWindow
	recvCh chan *gateway.WireFrame
	outCh  chan outbound
	closed chan struct{}
	// writeDone is closed by writeLoop on exit. Close waits on it with a
	// bounded drain instead of a WaitGroup: a WaitGroup cannot time out, and
	// a writeLoop wedged in InvokeSync must not block Close forever.
	writeDone chan struct{}

	// ofMu guards the overflow queue. Ordering invariant: everything in
	// outCh predates everything in overflow. An enqueue spills to overflow
	// only when outCh is full, and stays on overflow until drained, so while
	// overflow is non-empty no new item can enter outCh. Consumers must
	// therefore drain outCh to empty before taking any overflow item.
	ofMu          sync.Mutex
	overflow      []outbound
	overflowBytes int

	// closeReason, set by the force-close paths (stall watchdog, overflow
	// budget) under ofMu, records why the transport tore itself down. The
	// teardown reads it to log a warn-level line (get_system_logs) and to
	// report a diagnostic (get_problems) — without it a force-close is a
	// bare info log and the visible symptom is only a mysterious reconnect.
	closeReason string

	// overflowWarnedAt tracks the last watermark threshold crossed so the
	// observability log fires once per threshold crossing, not per frame.
	overflowWarnedAt float64

	// overflowPeakBytes is the high-water mark of the current overflow
	// episode; it doubles as the "had overflow" flag for the drain log.
	overflowPeakBytes int

	// Batch tuning and budgets. Defaults come from the package constants;
	// overridable per connection via wailsRawConnTuning (tests use a large
	// flushInterval to test the size threshold deterministically without
	// timer interference).
	maxBatchSize    int
	flushInterval   time.Duration
	dispatchTimeout time.Duration
	stallForceClose time.Duration
	closeDrainWait  time.Duration
	overflowBudget  int

	closeOnce sync.Once

	// suspended records that the owning window is minimised/hidden, so its
	// WebView2 renderer is (or about to be) suspended and dispatch stalls
	// are expected, not fatal. Maintained from window events (see
	// subscribeRendererSuspension); read by dispatchGuard's stall watchdog.
	suspended atomic.Bool

	// superseded marks a conn whose session must not dispatch anything more
	// to the window: it was replaced (frontend reconnect / page reload) or
	// the frontend explicitly closed it. The successor session shares the
	// window's frame event name, so stale frames flushed by the old
	// writeLoop interleave with the successor's transId sequence and force
	// the frontend to close 4001 ("transId gap") — the dominant observed
	// cause of the desktop disconnect/reconnect loop. Set under ofMu so the
	// enqueueOut check-and-enqueue stays atomic; read lock-free.
	superseded atomic.Bool
}

func newWailsRawFrameConn(window wailsWindow) *wailsRawFrameConn {
	return newWailsRawFrameConnTuned(window, wailsRawConnTuning{
		maxBatchSize:  rawTransportMaxBatchSize,
		flushInterval: rawTransportFlushInterval,
	})
}

// newWailsRawFrameConnWith allows callers (tests) to override the batch size and
// flush interval. writeLoop is started after config is applied.
func newWailsRawFrameConnWith(window wailsWindow, maxBatchSize int, flushInterval time.Duration) *wailsRawFrameConn {
	return newWailsRawFrameConnTuned(window, wailsRawConnTuning{
		maxBatchSize:  maxBatchSize,
		flushInterval: flushInterval,
	})
}

// wailsRawConnTuning carries per-connection overrides; a zero field falls back
// to its package default, so tests need set only the knobs they exercise.
type wailsRawConnTuning struct {
	maxBatchSize    int
	flushInterval   time.Duration
	dispatchTimeout time.Duration
	stallForceClose time.Duration
	closeDrainWait  time.Duration
	overflowBudget  int
}

// newWailsRawFrameConnTuned applies a full tuning set. Zero fields resolve to
// the package defaults.
func newWailsRawFrameConnTuned(window wailsWindow, t wailsRawConnTuning) *wailsRawFrameConn {
	if t.maxBatchSize <= 0 {
		t.maxBatchSize = rawTransportMaxBatchSize
	}
	if t.flushInterval <= 0 {
		t.flushInterval = rawTransportFlushInterval
	}
	if t.dispatchTimeout <= 0 {
		t.dispatchTimeout = rawTransportDispatchTimeout
	}
	if t.stallForceClose <= 0 {
		t.stallForceClose = rawTransportStallForceClose
	}
	if t.closeDrainWait <= 0 {
		t.closeDrainWait = rawTransportCloseDrainTimeout
	}
	if t.overflowBudget <= 0 {
		t.overflowBudget = rawTransportOverflowBudget
	}
	c := &wailsRawFrameConn{
		window:          window,
		recvCh:          make(chan *gateway.WireFrame, 64),
		outCh:           make(chan outbound, 64),
		closed:          make(chan struct{}),
		writeDone:       make(chan struct{}),
		maxBatchSize:    t.maxBatchSize,
		flushInterval:   t.flushInterval,
		dispatchTimeout: t.dispatchTimeout,
		stallForceClose: t.stallForceClose,
		closeDrainWait:  t.closeDrainWait,
		overflowBudget:  t.overflowBudget,
	}
	go c.writeLoop()
	return c
}

// writeLoop is the single writer goroutine. It batches frame events and
// dispatches them as a single frames[] event once maxBatchSize frames accrue or
// the flush interval elapses, collapsing N main-thread InvokeSync round-trips
// into one. Control events (open/close/error) flush any pending frames first,
// then are delivered immediately, preserving order across the boundary.
//
// Order guarantees:
//   - Within a batch, frames are processed in array order by the frontend.
//   - Across batches, FIFO is preserved because only this goroutine calls
//     DispatchWailsEvent, and outCh/overflow are consumed oldest-first
//     (everything in outCh predates everything in overflow — see ofMu).
//
// When closed fires, it flushes the in-flight batch and drains both queues so
// no frame is lost.
func (c *wailsRawFrameConn) writeLoop() {
	defer close(c.writeDone)

	var batch []string
	timer := time.NewTimer(c.flushInterval)
	defer timer.Stop()
	timerArmed := false

	flush := func() {
		if len(batch) == 0 {
			timerArmed = false
			return
		}
		c.dispatchBatch(batch)
		batch = batch[:0]
		timerArmed = false
	}

	handle := func(item outbound) {
		if item.control != nil {
			// Control events flush pending frames first to preserve order,
			// then are delivered immediately.
			flush()
			c.dispatchControl(item.control)
			return
		}
		batch = append(batch, item.frame)
		if len(batch) >= c.maxBatchSize {
			flush()
		} else if !timerArmed {
			timer.Stop()
			timer.Reset(c.flushInterval)
			timerArmed = true
		}
	}

	for {
		// Only arm the timer channel when frames are pending, so an idle
		// connection never wakes the goroutine.
		var timerC <-chan time.Time
		if timerArmed && len(batch) > 0 {
			timerC = timer.C
		}

		// Fast path while items are queued. outCh contents strictly predate
		// overflow contents (see ofMu), so outCh must be consumed to empty
		// before any overflow item. A full outCh is always receivable, and a
		// non-empty overflow is always poppable, so neither path can stall.
		select {
		case item := <-c.outCh:
			handle(item)
			continue
		default:
		}
		if item, ok := c.popOverflow(); ok {
			handle(item)
			continue
		}

		select {
		case item := <-c.outCh:
			handle(item)
		case <-timerC:
			flush()
		case <-c.closed:
			flush()
			c.drainOut(&batch)
			return
		}
	}
}

// dispatchGuard bounds a main-thread dispatch. DispatchWailsEvent ->
// ExecJS -> InvokeSync blocks until the Wails main thread executes the JS.
// A suspended renderer (minimized/hidden WebView2 window) blocks it well
// past dispatchTimeout without being wedged — frames keep spilling into the
// overflow queue and flush in order once the renderer resumes — so a stall
// within dispatchTimeout..stallForceClose is only logged. Past
// stallForceClose (zero dispatch progress for minutes, i.e. a renderer that
// is dead rather than paused) the transport force-closes itself: Send starts
// failing with io.EOF, the session tears down, and the frontend reconnects
// once its renderer recovers.
//
// A minimised window is the exception: its renderer stays suspended for as
// long as the user keeps it minimised, which is normal usage and can far
// outlast stallForceClose. While suspended is set (window events, see
// subscribeRendererSuspension) the force-close is deferred and re-checked
// each horizon; the overflow budget still bounds buffered memory. Probing
// IsMinimised from here instead is not viable: while the main thread is
// parked inside a suspended dispatch, an InvokeSyncWithResult probe parks
// behind it and cannot answer until the renderer resumes anyway.
func (c *wailsRawFrameConn) dispatchGuard(fn func()) {
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
			return
		case <-time.After(c.dispatchTimeout):
			log.Printf("[WailsRaw] dispatch stalled for %v on window %q (suspended renderer?); buffering frames", c.dispatchTimeout, c.window.Name())
		}
		for wait := c.stallForceClose - c.dispatchTimeout; ; wait = c.stallForceClose {
			select {
			case <-done:
				return
			case <-time.After(wait):
			}
			if !c.suspended.Load() {
				log.Printf("[WailsRaw] dispatch stalled for %v with zero progress; force-closing session", c.stallForceClose)
				c.setCloseReason(fmt.Sprintf("dispatch stalled %v with zero progress (dead renderer)", c.stallForceClose))
				c.signalClose()
				return
			}
			log.Printf("[WailsRaw] dispatch stalled %v on window %q while renderer suspended; deferring force-close", c.stallForceClose, c.window.Name())
		}
	}()
	fn()
	close(done)
}

// markSuperseded flags the conn as replaced or client-closed: its writeLoop
// stops dispatching frames to the window and Send fails fast with io.EOF so
// the gateway session tears down promptly. Must be called before Close in the
// manager paths where a successor session on the same window may follow.
func (c *wailsRawFrameConn) markSuperseded() {
	c.ofMu.Lock()
	c.superseded.Store(true)
	dropped := len(c.overflow)
	c.ofMu.Unlock()
	if dropped > 0 {
		log.Printf("[WailsRaw] conn superseded; dropping %d buffered overflow frames", dropped)
	}
}

// dispatchControl delivers a control event under the dispatch stall guard.
func (c *wailsRawFrameConn) dispatchControl(event *application.CustomEvent) {
	if c.superseded.Load() {
		return
	}
	c.dispatchGuard(func() {
		if c.superseded.Load() {
			return
		}
		c.window.DispatchWailsEvent(event)
	})
}

// dispatchBatch emits a single frames[] event carrying the accumulated base64
// frames. Named structs replace the former per-frame map allocation.
func (c *wailsRawFrameConn) dispatchBatch(frames []string) {
	if c.superseded.Load() {
		return
	}
	payload := rawBatchPayload{Frames: make([]rawFramePayload, len(frames))}
	for i := range frames {
		payload.Frames[i].Data = frames[i]
	}
	c.dispatchGuard(func() {
		if c.superseded.Load() {
			return
		}
		c.window.DispatchWailsEvent(&application.CustomEvent{
			Name: rawTransportFrameEvent,
			Data: payload,
		})
	})
}

// drainOut flushes any remaining outbound items after close. outCh is drained
// to empty first, then the overflow queue (ordering invariant, see ofMu).
// Frames are accumulated into batch (flushing on size threshold), and control
// events flush pending frames first. No frame is dropped on the close path.
func (c *wailsRawFrameConn) drainOut(batch *[]string) {
	handle := func(item outbound) {
		if item.control != nil {
			if len(*batch) > 0 {
				c.dispatchBatch(*batch)
				*batch = (*batch)[:0]
			}
			c.dispatchControl(item.control)
			return
		}
		*batch = append(*batch, item.frame)
		if len(*batch) >= c.maxBatchSize {
			c.dispatchBatch(*batch)
			*batch = (*batch)[:0]
		}
	}
	for {
		select {
		case item := <-c.outCh:
			handle(item)
		default:
			item, ok := c.popOverflow()
			if !ok {
				if len(*batch) > 0 {
					c.dispatchBatch(*batch)
					*batch = (*batch)[:0]
				}
				return
			}
			handle(item)
		}
	}
}

// Recv blocks until the next inbound wire frame arrives. It is called by the
// single ServeSession goroutine, providing the serial consumer the user asked
// for.
func (c *wailsRawFrameConn) Recv() (*gateway.WireFrame, error) {
	select {
	case f := <-c.recvCh:
		return f, nil
	case <-c.closed:
		return nil, io.EOF
	}
}

// Send marshals a wire frame and enqueues it on outCh for batched dispatch. The
// single writer goroutine (writeLoop) coalesces frames into a frames[] event,
// preserving the TransID order established by the gospore session's sendMu.
func (c *wailsRawFrameConn) Send(wire *gateway.WireFrame) error {
	data, err := gateway.MarshalWireFrame(wire)
	if err != nil {
		return err
	}
	encoded := encodeWireBase64(data)
	return c.enqueueOut(outbound{frame: encoded})
}

// enqueueOut pushes an outbound item toward the writer goroutine. It never
// blocks: while writeLoop is stalled (suspended renderer holding InvokeSync),
// items spill from the bounded outCh into the byte-budgeted overflow queue and
// Send keeps succeeding, so the gateway session is never back-pressured (the
// workspace-freeze chain) and no frame is dropped — the buffered frames flush
// in order once the renderer resumes. Once the budget is exceeded the
// transport force-closes itself and io.EOF propagates like any send failure.
func (c *wailsRawFrameConn) enqueueOut(item outbound) error {
	c.ofMu.Lock()
	defer c.ofMu.Unlock()
	if c.isClosedLocked() || c.superseded.Load() {
		return io.EOF
	}
	// Fast path while no overflow exists. Sending under ofMu is safe: the
	// send is non-blocking, and writeLoop never takes ofMu to receive.
	if len(c.overflow) == 0 {
		select {
		case c.outCh <- item:
			return nil
		default:
		}
	}
	if c.overflowBytes+len(item.frame) > c.overflowBudget {
		log.Printf("[WailsRaw] overflow budget %d bytes exceeded; force-closing session", c.overflowBudget)
		c.closeReason = fmt.Sprintf("overflow budget %d bytes exceeded after %d queued frames", c.overflowBudget, len(c.overflow))
		c.signalClose()
		return io.EOF
	}
	c.overflow = append(c.overflow, item)
	c.overflowBytes += len(item.frame)
	if c.overflowBytes > c.overflowPeakBytes {
		c.overflowPeakBytes = c.overflowBytes
	}
	c.maybeWarnOverflow()
	return nil
}

// maybeWarnOverflow logs the overflow watermark crossings that precede a
// budget force-close. The caller holds ofMu. Reporting at 50% and 90% gives
// the debug bundle (get_system_logs) the ramp-up trace of a suspended
// renderer accumulating frames, instead of a single terminal line.
func (c *wailsRawFrameConn) maybeWarnOverflow() {
	ratio := float64(c.overflowBytes) / float64(c.overflowBudget)
	for _, threshold := range []float64{0.5, 0.9} {
		if ratio >= threshold && c.overflowWarnedAt < threshold {
			c.overflowWarnedAt = threshold
			log.Printf("[WailsRaw] overflow queue at %d%% of %d-byte budget (%d frames queued); renderer not draining (suspended or stalled)", int(threshold*100), c.overflowBudget, len(c.overflow))
			return
		}
	}
}

// setCloseReason records why the transport is being force-closed (first
// writer wins; a normal Close leaves it empty). It guards ofMu internally so
// the watchdog goroutine can call it safely.
func (c *wailsRawFrameConn) setCloseReason(reason string) {
	c.ofMu.Lock()
	if c.closeReason == "" {
		c.closeReason = reason
	}
	c.ofMu.Unlock()
}

// closeReasonOf returns the recorded force-close reason, if any.
func (c *wailsRawFrameConn) closeReasonOf() string {
	c.ofMu.Lock()
	defer c.ofMu.Unlock()
	return c.closeReason
}

// popOverflow removes the head of the overflow queue. Callers must only
// consume overflow items after outCh is drained to empty (ordering invariant,
// see ofMu).
func (c *wailsRawFrameConn) popOverflow() (outbound, bool) {
	c.ofMu.Lock()
	defer c.ofMu.Unlock()
	if len(c.overflow) == 0 {
		return outbound{}, false
	}
	item := c.overflow[0]
	c.overflow[0] = outbound{}
	c.overflow = c.overflow[1:]
	c.overflowBytes -= len(item.frame)
	if len(c.overflow) == 0 {
		c.overflow = nil // release the slice backing array
		// A fully-drained overflow means the renderer resumed (or the stall
		// broke). Logging the episode's peak preserves the recovered-stall
		// trace that would otherwise vanish without a force-close.
		if c.overflowPeakBytes > 0 {
			log.Printf("[WailsRaw] overflow queue drained after stall episode: peaked at %d bytes of %d-byte budget", c.overflowPeakBytes, c.overflowBudget)
			c.overflowPeakBytes = 0
			c.overflowWarnedAt = 0
		}
	}
	return item, true
}

// isClosedLocked reports whether the connection is closed; the caller is
// expected to hold ofMu so the closed-check and the subsequent enqueue
// decision are atomic with respect to the drain path.
func (c *wailsRawFrameConn) isClosedLocked() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

// Close terminates the transport. It waits at most closeDrainWait for writeLoop
// to flush pending frames; when writeLoop is wedged inside a stalled dispatch
// the drain is abandoned so the gateway teardown (sess.send → conn.Close on
// send failure) is not held hostage. The wedged writeLoop goroutine itself is
// left to exit whenever the renderer recovers.
func (c *wailsRawFrameConn) Close() error {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
	select {
	case <-c.writeDone:
	case <-time.After(c.closeDrainWait):
		log.Printf("[WailsRaw] close drain abandoned: writeLoop stalled > %v", c.closeDrainWait)
	}
	return nil
}

// signalClose closes the connection without waiting for the writeLoop to
// drain. It is safe to call from the Wails main thread, where waiting for the
// writeLoop would deadlock: the writeLoop's close-path flush calls
// DispatchWailsEvent -> ExecJS -> InvokeSync, which posts to the main thread
// and blocks until it runs — but the main thread is the caller and is blocked
// in Close. signalClose breaks that cycle by letting the writeLoop finish
// asynchronously after the main thread releases.
func (c *wailsRawFrameConn) signalClose() {
	c.closeOnce.Do(func() {
		close(c.closed)
	})
}

func (c *wailsRawFrameConn) enqueue(frame *gateway.WireFrame) bool {
	select {
	case c.recvCh <- frame:
		return true
	case <-c.closed:
		return false
	}
}

func (c *wailsRawFrameConn) emitOpen() {
	_ = c.enqueueOut(outbound{control: &application.CustomEvent{Name: rawTransportOpenEvent, Data: ""}})
}

// emitCloseDirect delivers the close event out-of-band, after the writeLoop
// has (bounded) flushed buffered frames. enqueueOut drops control events once
// the transport is closed — exactly the force-close paths (stall watchdog,
// overflow budget) where the frontend most needs to learn its session died —
// which previously left it stuck "open" until the next heartbeat tripped
// "no active session". Dispatching directly still funnels through the main
// thread like every other dispatch: a wedged or suspended renderer receives
// it on recovery, immediately triggering the frontend reconnect and the
// sinceSeqNo replay instead of a silent dead session.
//
// reason explains the teardown ("" for an ordinary close); the frontend logs
// it alongside its reconnect trace so the debug bundle's console-log source
// shows what killed the session.
func (c *wailsRawFrameConn) emitCloseDirect(reason string) {
	data := any("")
	if reason != "" {
		data = reason
	}
	c.dispatchGuard(func() {
		c.window.DispatchWailsEvent(&application.CustomEvent{Name: rawTransportCloseEvent, Data: data})
	})
}

func (c *wailsRawFrameConn) emitError(message string) {
	_ = c.enqueueOut(outbound{control: &application.CustomEvent{Name: rawTransportErrorEvent, Data: message}})
}

// gatewayServer is the minimal surface of *gateway.Server needed by the raw
// transport manager. It is satisfied by the real gateway server and by test
// doubles.
type gatewayServer interface {
	ServeSession(ctx context.Context, conn gateway.FrameConn, opts gateway.SessionOptions)
}

// wailsTransportManager owns one raw transport session per window. It is a
// Wails bridge service, not an actor, so its mutable state is isolated to the
// desktop service layer and never shared across actors.
type wailsTransportManager struct {
	handle  *runtime.Handle
	gw      gatewayServer // directly injected (tests); nil → resolve from handle
	mu      sync.Mutex
	conns   map[string]*wailsRawFrameConn
	pending map[string]struct{} // windows with an in-flight async startSession
}

func newWailsTransportManager(handle *runtime.Handle) *wailsTransportManager {
	return &wailsTransportManager{
		handle:  handle,
		conns:   make(map[string]*wailsRawFrameConn),
		pending: make(map[string]struct{}),
	}
}

// gateway resolves the gateway server lazily. Bootstrap starts app.Run in a
// goroutine, so GatewayServer may be nil at construction time and only become
// available once Run reaches gateway initialisation. By resolving on each
// message we avoid the construction-time race.
//
// The concrete nil pointer must be collapsed to a nil interface: a typed-nil
// (*gateway.Server)(nil) wrapped in the gatewayServer interface is non-nil to
// the == nil check and would later panic inside ServeSession (nil receiver).
func (m *wailsTransportManager) gateway() gatewayServer {
	if m.gw != nil {
		return m.gw
	}
	if m.handle == nil || m.handle.App() == nil {
		return nil
	}
	srv := m.handle.App().GatewayServer()
	if srv == nil {
		return nil
	}
	return srv
}

// gatewayReady returns the readiness channel from the runtime handle, or nil
// when no gateway is configured. Kept separate so startSession can wait on it
// without holding the session lock.
func (m *wailsTransportManager) gatewayReady() <-chan struct{} {
	if m.handle == nil {
		return nil
	}
	return m.handle.GatewayReady()
}

// RawMessageHandler returns the Wails v3 RawMessageHandler closure that feeds
// frontend raw messages into the gospore gateway via a strictly-ordered queue.
func (m *wailsTransportManager) RawMessageHandler() func(window application.Window, message string, originInfo *application.OriginInfo) {
	return func(window application.Window, message string, originInfo *application.OriginInfo) {
		m.handleRawMessage(window, message, originInfo)
	}
}

func (m *wailsTransportManager) handleRawMessage(window wailsWindow, message string, _ *application.OriginInfo) {
	// Temporary instrumentation for Trace-20260724T030404 slow-refresh investigation.
	handleStart := time.Now()
	defer func() {
		if elapsed := time.Since(handleStart); elapsed > 100*time.Millisecond {
			log.Printf("[WailsRaw] handleRawMessage total took %v", elapsed)
		}
	}()

	var env rawEnvelope
	if err := json.Unmarshal([]byte(message), &env); err != nil {
		m.emitError(window, "invalid envelope")
		return
	}

	// WailsRaw frame type is not logged — it's too noisy (multiple per second).

	switch env.Type {
	case rawEnvelopeConnect:
		m.startSession(window, env.Token)
	case rawEnvelopeFrame:
		m.handleFrame(window, env.Data)
	case rawEnvelopeClose:
		m.closeSession(window)
	default:
		m.emitError(window, "unknown envelope type")
	}
}

func (m *wailsTransportManager) startSession(window wailsWindow, token string) {
	// Temporary instrumentation for Trace-20260724T030404 slow-refresh investigation.
	start := time.Now()
	log.Printf("[WailsRaw] startSession begin window=%s", window.Name())
	defer func() {
		log.Printf("[WailsRaw] startSession end window=%s elapsed=%v", window.Name(), time.Since(start))
	}()

	name := window.Name()

	m.mu.Lock()
	if m.pending == nil {
		m.pending = make(map[string]struct{})
	}
	if old, ok := m.conns[name]; ok {
		// The successor shares the window's frame event name; stale frames
		// from the old writeLoop would interleave with the successor's
		// transId sequence and force a 4001 close on the frontend.
		old.markSuperseded()
		old.Close()
		delete(m.conns, name)
	}
	if _, inFlight := m.pending[name]; inFlight {
		m.mu.Unlock()
		log.Printf("[WailsRaw] startSession already pending window=%s", name)
		return
	}
	m.pending[name] = struct{}{}
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.pending, name)
			m.mu.Unlock()
		}()

		if !m.waitGatewayReady() {
			m.emitError(window, "gateway not available")
			log.Printf("[WailsRaw] startSession gateway not ready window=%s", name)
			return
		}

		m.mu.Lock()
		if _, exists := m.conns[name]; exists {
			m.mu.Unlock()
			log.Printf("[WailsRaw] startSession raced window=%s", name)
			return
		}

		conn := newWailsRawFrameConn(window)
		cancelSuspensionEvents := subscribeRendererSuspension(window, conn)
		m.conns[name] = conn

		gw := m.gateway()
		m.mu.Unlock()

		go func() {
			gw.ServeSession(context.Background(), conn, gateway.SessionOptions{
				Token: token,
			})
			// Order matters: Close lets the writeLoop flush buffered frames
			// (bounded), removeSession stops inbound frames, then the close
			// event is dispatched out-of-band so it survives even the
			// force-close paths that made enqueueOut a no-op.
			conn.Close()
			wasCurrent := m.removeSession(name, conn)
			cancelSuspensionEvents()
			reason := conn.closeReasonOf()
			if reason != "" {
				log.Printf("[WailsRaw] session window=%s force-closed: %s", name, reason)
				m.reportTransportDiagnostic(name, reason)
			}
			// A replaced session (page refresh, client reconnect) must not
			// emit a close event: the window already belongs to the successor
			// session, and the stale close aborts the successor's in-flight
			// auth handshake with "connection closed".
			if wasCurrent {
				conn.emitCloseDirect(reason)
			}
		}()

		conn.emitOpen()
		log.Printf("[WailsRaw] startSession emitted open window=%s", name)
	}()
}

// waitGatewayReady blocks until the gateway server is constructed and ready,
// or returns false on timeout. It returns immediately when a gateway is
// injected directly (tests) or already available.
//
// The gateway server (and its readiness channel) is built lazily during
// app.Run — only after the actor tree finishes starting. An early frontend
// "connect" can therefore arrive before the channel even exists, so we cannot
// rely on a single channel fetch; we poll for the channel to appear, then wait
// for it to close (gateway listening).
func (m *wailsTransportManager) waitGatewayReady() bool {
	// Temporary instrumentation for Trace-20260724T030404 slow-refresh investigation.
	start := time.Now()
	ready := m.waitGatewayReadyInstrumented()
	log.Printf("[WailsRaw] waitGatewayReady elapsed=%v ready=%v", time.Since(start), ready)
	return ready
}

func (m *wailsTransportManager) waitGatewayReadyInstrumented() bool {
	if m.gateway() != nil {
		return true
	}
	// No handle and no injected gateway means no gateway will ever appear —
	// bail out immediately rather than polling for the full deadline.
	if m.handle == nil {
		return false
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if ready := m.gatewayReady(); ready != nil {
			select {
			case <-ready:
			case <-deadline.C:
			}
			return m.gateway() != nil
		}
		select {
		case <-deadline.C:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (m *wailsTransportManager) handleFrame(window wailsWindow, data string) {
	m.mu.Lock()
	conn, ok := m.conns[window.Name()]
	m.mu.Unlock()
	if !ok {
		m.emitError(window, "no active session")
		return
	}

	var wireErr error
	if err := decodeWireBase64(data, func(payload []byte) error {
		wire, err := gateway.UnmarshalWireFrame(payload)
		if err != nil {
			wireErr = err
			return err
		}
		conn.enqueue(wire)
		return nil
	}); err != nil {
		m.emitError(window, "invalid base64 frame")
		return
	}
	if wireErr != nil {
		m.emitError(window, "invalid wire frame")
		return
	}
}

func (m *wailsTransportManager) closeSession(window wailsWindow) {
	m.mu.Lock()
	conn, ok := m.conns[window.Name()]
	m.mu.Unlock()
	if ok {
		// The frontend detaches its handlers right after this close; any
		// frames still flushed by the drain would land on the successor's
		// handler after a reconnect and poison its transId sequence.
		conn.markSuperseded()
		conn.Close()
	}
}

func (m *wailsTransportManager) closeAll() {
	m.mu.Lock()
	conns := make([]*wailsRawFrameConn, 0, len(m.conns))
	for _, c := range m.conns {
		conns = append(conns, c)
	}
	m.conns = make(map[string]*wailsRawFrameConn)
	m.mu.Unlock()

	for _, c := range conns {
		c.Close()
	}
}

// signalCloseAll closes all sessions without waiting for their writeLoops to
// drain. Use from the Wails main thread (Shutdown/BeforeClose), where
// waiting would deadlock on DispatchWailsEvent -> InvokeSync.
func (m *wailsTransportManager) signalCloseAll() {
	m.mu.Lock()
	conns := make([]*wailsRawFrameConn, 0, len(m.conns))
	for _, c := range m.conns {
		conns = append(conns, c)
	}
	m.conns = make(map[string]*wailsRawFrameConn)
	m.mu.Unlock()

	for _, c := range conns {
		c.signalClose()
	}
}

// reportTransportDiagnostic forwards a transport force-close incident to the
// oracle actor (oracle.report_diagnostic) so it surfaces in the debug bundle's
// get_problems instead of only the log ring. Fire-and-forget Tell: the caller
// is the session teardown goroutine and must never block on the actor tree —
// failures here are swallowed (the force-close reason is already logged).
func (m *wailsTransportManager) reportTransportDiagnostic(windowName, reason string) {
	if m.handle == nil || m.handle.App() == nil {
		return
	}
	oracleRef, ok := m.handle.App().LookupService("oracle")
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := oracleRef.Invoke(ctx, "oracle.report_diagnostic", domain.OracleReportDiagnosticReq{
		Severity:   "warning",
		Source:     "desktop.wails_transport",
		Message:    fmt.Sprintf("wails raw transport force-closed (window %q): %s — frontend reconnects and replays via sinceSeqNo", windowName, reason),
		CallableID: "desktop.wails_transport",
	}, nil)
	call.Close()
}

// removeSession unregisters conn only when it is still the window's current
// session. It reports false when the session was replaced (page refresh,
// client reconnect) — the caller uses that to suppress the stale close event.
func (m *wailsTransportManager) removeSession(name string, conn *wailsRawFrameConn) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conns[name] == conn {
		delete(m.conns, name)
		return true
	}
	return false
}

func (m *wailsTransportManager) emitError(window wailsWindow, message string) {
	window.DispatchWailsEvent(&application.CustomEvent{Name: rawTransportErrorEvent, Data: message})
}

// Ensure the manager is always safe to call, even when disabled.
func (m *wailsTransportManager) closeSessionByName(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	conn, ok := m.conns[name]
	m.mu.Unlock()
	if ok {
		conn.markSuperseded()
		conn.Close()
	}
}

// signalCloseSessionByName closes a session without waiting for its writeLoop
// to drain. Use from the Wails main thread, where waiting would deadlock.
func (m *wailsTransportManager) signalCloseSessionByName(name string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	conn, ok := m.conns[name]
	m.mu.Unlock()
	if ok {
		conn.signalClose()
	}
}
