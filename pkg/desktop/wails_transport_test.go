package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type testWindow struct {
	mu     sync.Mutex
	name   string
	events []testEvent
}

type testEvent struct {
	name string
	data any
}

func (w *testWindow) Name() string { return w.name }

func (w *testWindow) DispatchWailsEvent(event *application.CustomEvent) {
	w.mu.Lock()
	w.events = append(w.events, testEvent{name: event.Name, data: event.Data})
	w.mu.Unlock()
}

// snapshot returns a copy of the recorded events under the lock so a test
// goroutine can inspect dispatches without racing the writer goroutine.
func (w *testWindow) snapshot() []testEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]testEvent, len(w.events))
	copy(out, w.events)
	return out
}

// Ensure the test window satisfies the internal wailsWindow interface.
var _ wailsWindow = (*testWindow)(nil)

type testGateway struct {
	serveFn func(ctx context.Context, conn gateway.FrameConn, opts gateway.SessionOptions)
}

func (g *testGateway) ServeSession(ctx context.Context, conn gateway.FrameConn, opts gateway.SessionOptions) {
	if g.serveFn != nil {
		g.serveFn(ctx, conn, opts)
	}
}

// Ensure the test gateway satisfies the internal gatewayServer interface.
var _ gatewayServer = (*testGateway)(nil)

func TestWailsRawFrameConnEnqueueAndRecv(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)
	defer conn.Close()

	wire, err := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeInvoke, CallID: "a.b"})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	frame, err := gateway.UnmarshalWireFrame(wire)
	if err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}

	if !conn.enqueue(frame) {
		t.Fatal("enqueue returned false for open connection")
	}

	got, err := conn.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if got.Type != gateway.FrameTypeInvoke || got.CallID != "a.b" {
		t.Fatalf("Recv returned wrong frame: %+v", got)
	}
}

func TestWailsRawFrameConnRecvOrder(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)
	defer conn.Close()

	for i := 0; i < 5; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeInvoke, CallID: string(rune('a' + i))})
		frame, _ := gateway.UnmarshalWireFrame(wire)
		conn.enqueue(frame)
	}

	for i := 0; i < 5; i++ {
		frame, err := conn.Recv()
		if err != nil {
			t.Fatalf("Recv %d: %v", i, err)
		}
		want := string(rune('a' + i))
		if frame.CallID != want {
			t.Fatalf("frame %d: got CallID %q, want %q", i, frame.CallID, want)
		}
	}
}

func TestWailsRawFrameConnCloseUnblocksRecv(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	done := make(chan struct{})
	go func() {
		_, err := conn.Recv()
		if !errors.Is(err, io.EOF) {
			t.Errorf("Recv expected io.EOF, got %v", err)
		}
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	conn.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Recv did not unblock after Close")
	}
}

func TestWailsRawFrameConnSend(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	wire, err := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "a.b"})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	frame, _ := gateway.UnmarshalWireFrame(wire)

	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Flush the writer goroutine before inspecting events.
	conn.Close()

	data := extractFrameData(t, win.snapshot())
	if len(data) != 1 {
		t.Fatalf("expected 1 frame across events, got %d", len(data))
	}
	decoded, err := base64.StdEncoding.DecodeString(data[0])
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	got, err := gateway.UnmarshalWireFrame(decoded)
	if err != nil {
		t.Fatalf("unmarshal sent frame: %v", err)
	}
	if got.Type != gateway.FrameTypeReply || got.CallID != "a.b" {
		t.Fatalf("Send returned wrong frame: %+v", got)
	}
}

// TestWailsRawFrameConnSendOrder verifies that multiple frames sent
// concurrently are delivered to the window in FIFO order by the writer
// goroutine.
func TestWailsRawFrameConnSendOrder(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	for i := 0; i < 20; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: string(rune('a' + i))})
		frame, _ := gateway.UnmarshalWireFrame(wire)
		if err := conn.Send(frame); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	conn.Close()

	data := extractFrameData(t, win.snapshot())
	if len(data) != 20 {
		t.Fatalf("expected 20 frames across events, got %d", len(data))
	}
	for i, encoded := range data {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("frame %d: decode base64: %v", i, err)
		}
		got, err := gateway.UnmarshalWireFrame(decoded)
		if err != nil {
			t.Fatalf("frame %d: unmarshal: %v", i, err)
		}
		want := string(rune('a' + i))
		if got.CallID != want {
			t.Fatalf("frame %d: got CallID %q, want %q", i, got.CallID, want)
		}
	}

	// Batching must coalesce 20 frames into fewer events than frames.
	if frameEventCount(win.snapshot()) >= 20 {
		t.Fatalf("expected batching, got %d frame events", frameEventCount(win.snapshot()))
	}
}

func TestWailsTransportManagerRawMessageRouting(t *testing.T) {
	win := &testWindow{name: "main"}
	called := make(chan gateway.SessionOptions, 1)
	gw := &testGateway{
		serveFn: func(_ context.Context, conn gateway.FrameConn, opts gateway.SessionOptions) {
			called <- opts
			// Simulate session reading one frame then closing.
			<-conn.(*wailsRawFrameConn).closed
		},
	}
	mgr := &wailsTransportManager{gw: gw, conns: make(map[string]*wailsRawFrameConn)}

	origin := &application.OriginInfo{Origin: "wails://wails"}

	// Connect
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect, Token: "token-123"}), origin)

	select {
	case opts := <-called:
		if opts.Token != "token-123" {
			t.Fatalf("token not passed to session: %q", opts.Token)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeSession was not started")
	}

	// The open event is delivered asynchronously by the writer goroutine.
	waitForEvent(t, win, rawTransportOpenEvent)

	// Send a frame
	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeInvoke, CallID: "x.y"})
	frameB64 := base64.StdEncoding.EncodeToString(wire)
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeFrame, Data: frameB64}), origin)

	// Close
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeClose}), origin)
}

// waitForEvent polls the test window until the named event appears or times out.
func waitForEvent(t *testing.T, win *testWindow, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range win.snapshot() {
			if ev.name == name {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for event %q, got %v", name, win.snapshot())
}

// waitForEventCount polls the test window until the named event appears at
// least want times or times out.
func waitForEventCount(t *testing.T, win *testWindow, name string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		n := 0
		for _, ev := range win.snapshot() {
			if ev.name == name {
				n++
			}
		}
		if n >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d events %q, got %v", want, name, win.snapshot())
}

// TestWailsTransportManagerReplacementSuppressesStaleClose reproduces the dev
// refresh bug: a page reload sends a fresh "connect" for the same window while
// the previous session is still registered. The replaced session's teardown
// must NOT emit a close event — the successor's frontend is mid auth-handshake
// and the stale close rejects its open promise with "connection closed". The
// close event is still emitted when a session ends without a successor.
func TestWailsTransportManagerReplacementSuppressesStaleClose(t *testing.T) {
	win := &testWindow{name: "main"}
	served := make(chan struct{}, 2)
	returned := make(chan struct{}, 2)
	gw := &testGateway{
		serveFn: func(_ context.Context, conn gateway.FrameConn, _ gateway.SessionOptions) {
			served <- struct{}{}
			<-conn.(*wailsRawFrameConn).closed
			returned <- struct{}{}
		},
	}
	mgr := &wailsTransportManager{gw: gw, conns: make(map[string]*wailsRawFrameConn)}

	origin := &application.OriginInfo{Origin: "wails://wails"}

	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect, Token: "t1"}), origin)
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("first ServeSession was not started")
	}
	waitForEvent(t, win, rawTransportOpenEvent)

	// Refresh: a second connect replaces the first session.
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect, Token: "t2"}), origin)
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("second ServeSession was not started")
	}
	waitForEventCount(t, win, rawTransportOpenEvent, 2)

	// Wait until the replaced session's ServeSession has returned, then give
	// its teardown goroutine (Close → removeSession → gated emit) time to
	// wrongly emit a close if the guard were absent.
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("replaced ServeSession did not return")
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, ev := range win.snapshot() {
			if ev.name == rawTransportCloseEvent {
				t.Fatal("replaced session emitted a stale close event")
			}
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Positive control: a session ending without a successor still emits close.
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeClose}), origin)
	waitForEvent(t, win, rawTransportCloseEvent)
}

func TestWailsTransportManagerErrorsWithoutGateway(t *testing.T) {
	win := &testWindow{name: "main"}
	mgr := &wailsTransportManager{gw: nil, conns: make(map[string]*wailsRawFrameConn)}

	origin := &application.OriginInfo{Origin: "wails://wails"}
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect}), origin)

	waitForEvent(t, win, rawTransportErrorEvent)
}

// TestWailsTransportManagerNoSessionWithoutGateway guards the startup-race
// crash: when the frontend "connect" arrives before the gateway is ready,
// startSession must emit an error and must NOT register a session (which would
// reach ServeSession with a nil/typed-nil gateway server and panic).
func TestWailsTransportManagerNoSessionWithoutGateway(t *testing.T) {
	win := &testWindow{name: "main"}
	mgr := &wailsTransportManager{gw: nil, conns: make(map[string]*wailsRawFrameConn)}

	origin := &application.OriginInfo{Origin: "wails://wails"}
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect}), origin)

	mgr.mu.Lock()
	registered := len(mgr.conns)
	mgr.mu.Unlock()
	if registered != 0 {
		t.Fatalf("expected no session registered without gateway, got %d", registered)
	}
}

func TestWailsTransportManagerInvalidEnvelope(t *testing.T) {
	win := &testWindow{name: "main"}
	gw := &testGateway{}
	mgr := &wailsTransportManager{gw: gw, conns: make(map[string]*wailsRawFrameConn)}

	origin := &application.OriginInfo{Origin: "wails://wails"}
	mgr.handleRawMessage(win, "not-json", origin)

	if len(win.snapshot()) != 1 || win.snapshot()[0].name != rawTransportErrorEvent {
		t.Fatalf("expected error event for invalid JSON, got %v", win.snapshot())
	}
}

// extractFrameData flattens every rawTransportFrameEvent batch event into the
// ordered list of base64 frame strings it carries.
func extractFrameData(t *testing.T, events []testEvent) []string {
	t.Helper()
	var out []string
	for _, ev := range events {
		if ev.name != rawTransportFrameEvent {
			continue
		}
		batch, ok := ev.data.(rawBatchPayload)
		if !ok {
			t.Fatalf("expected rawBatchPayload, got %T", ev.data)
		}
		for _, f := range batch.Frames {
			out = append(out, f.Data)
		}
	}
	return out
}

// frameEventCount counts rawTransportFrameEvent dispatches.
func frameEventCount(events []testEvent) int {
	n := 0
	for _, ev := range events {
		if ev.name == rawTransportFrameEvent {
			n++
		}
	}
	return n
}

// maxBatchLen returns the largest number of frames carried in a single frame
// event.
func maxBatchLen(events []testEvent) int {
	max := 0
	for _, ev := range events {
		if ev.name != rawTransportFrameEvent {
			continue
		}
		if b, ok := ev.data.(rawBatchPayload); ok && len(b.Frames) > max {
			max = len(b.Frames)
		}
	}
	return max
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// TestWailsRawFrameConnBatchBySize verifies that a burst reaching the size
// threshold is flushed as a single full batch. A large flush interval removes
// timer interference, and we wait for the size-triggered flush to land before
// closing, so Close's drain path cannot split the batch. The only possible
// flush trigger is therefore reaching maxBatchSize.
func TestWailsRawFrameConnBatchBySize(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConnWith(win, rawTransportMaxBatchSize, time.Second)

	n := rawTransportMaxBatchSize
	frames := make([]*gateway.WireFrame, n)
	for i := 0; i < n; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: fmt.Sprintf("f%d", i)})
		frames[i], _ = gateway.UnmarshalWireFrame(wire)
	}
	for i := 0; i < n; i++ {
		if err := conn.Send(frames[i]); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	// The single size-threshold flush must land before Close to avoid the
	// close-path drain splitting the batch.
	waitForEvent(t, win, rawTransportFrameEvent)
	conn.Close()

	data := extractFrameData(t, win.snapshot())
	if len(data) != n {
		t.Fatalf("expected %d frames, got %d", n, len(data))
	}
	if got := maxBatchLen(win.snapshot()); got != n {
		t.Fatalf("expected a single full batch of %d frames, max batch len %d", n, got)
	}
	for i, encoded := range data {
		decoded, _ := base64.StdEncoding.DecodeString(encoded)
		got, _ := gateway.UnmarshalWireFrame(decoded)
		if want := fmt.Sprintf("f%d", i); got.CallID != want {
			t.Fatalf("frame %d: got %q, want %q", i, got.CallID, want)
		}
	}
}

// TestWailsRawFrameConnBatchFlushOnInterval verifies that sparse frames never
// reaching the size threshold are still flushed by the timer without closing.
func TestWailsRawFrameConnBatchFlushOnInterval(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)
	defer conn.Close()

	for i := 0; i < 3; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "x"})
		frame, _ := gateway.UnmarshalWireFrame(wire)
		if err := conn.Send(frame); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	waitForEvent(t, win, rawTransportFrameEvent)
	data := extractFrameData(t, win.snapshot())
	if len(data) != 3 {
		t.Fatalf("expected 3 frames flushed by timer, got %d", len(data))
	}
}

// TestWailsRawFrameConnBatchFlushBeforeControl verifies that a control event
// (close) flushes any pending frames first, preserving frame-before-close order.
func TestWailsRawFrameConnBatchFlushBeforeControl(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	conn.emitOpen()
	waitForEvent(t, win, rawTransportOpenEvent)

	for i := 0; i < 3; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "c"})
		frame, _ := gateway.UnmarshalWireFrame(wire)
		if err := conn.Send(frame); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	// Teardown order mirrors startSession: bounded flush of pending frames
	// first, then the out-of-band close notification.
	conn.Close()
	conn.emitCloseDirect("")

	frameIdx, closeIdx := -1, -1
	for i, ev := range win.snapshot() {
		switch ev.name {
		case rawTransportFrameEvent:
			if frameIdx == -1 {
				frameIdx = i
			}
		case rawTransportCloseEvent:
			if closeIdx == -1 {
				closeIdx = i
			}
		}
	}
	if frameIdx < 0 {
		t.Fatalf("frame event missing, got %v", win.snapshot())
	}
	if closeIdx < 0 {
		t.Fatalf("close event missing, got %v", win.snapshot())
	}
	if frameIdx > closeIdx {
		t.Fatalf("frame event %d dispatched after close event %d", frameIdx, closeIdx)
	}
	data := extractFrameData(t, win.snapshot())
	if len(data) != 3 {
		t.Fatalf("expected 3 frames, got %d", len(data))
	}
}

// TestWailsRawFrameConnBatchNoLossOnClose verifies that frames sent immediately
// before close are not dropped on the drain path.
func TestWailsRawFrameConnBatchNoLossOnClose(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	for i := 0; i < rawTransportMaxBatchSize*2+7; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: fmt.Sprintf("k%d", i)})
		frame, _ := gateway.UnmarshalWireFrame(wire)
		if err := conn.Send(frame); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	conn.Close()

	data := extractFrameData(t, win.snapshot())
	want := rawTransportMaxBatchSize*2 + 7
	if len(data) != want {
		t.Fatalf("expected %d frames, got %d (frames lost on close)", want, len(data))
	}
	for i, encoded := range data {
		decoded, _ := base64.StdEncoding.DecodeString(encoded)
		got, _ := gateway.UnmarshalWireFrame(decoded)
		if w := fmt.Sprintf("k%d", i); got.CallID != w {
			t.Fatalf("frame %d: got %q, want %q", i, got.CallID, w)
		}
	}
}

// TestWailsRawFrameConnSupersededDropsPendingFrames pins the replacement gate:
// once a conn is superseded, its buffered frames must never dispatch to the
// window — the successor session shares the frame event name and stale frames
// would interleave with its transId sequence (frontend close 4001).
func TestWailsRawFrameConnSupersededDropsPendingFrames(t *testing.T) {
	win := &testWindow{name: "main"}
	// Huge batch + flush interval keeps frames buffered in the writeLoop,
	// so nothing dispatches before the supersession lands.
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:  1024,
		flushInterval: time.Hour,
	})

	for i := 0; i < 5; i++ {
		if err := conn.Send(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	conn.markSuperseded()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for _, ev := range win.snapshot() {
		if ev.name == rawTransportFrameEvent {
			t.Fatalf("frame event dispatched after supersession: %+v", ev)
		}
	}
	// The dying session must fail fast instead of queueing more frames.
	if err := conn.Send(&gateway.WireFrame{Type: gateway.FrameTypeReply}); !errors.Is(err, io.EOF) {
		t.Fatalf("Send after supersession: got %v, want io.EOF", err)
	}
}

// TestWailsTransportManagerReplacementDropsStaleFrames covers the manager-level
// path: a reconnect (second connect on the same window) must stop the old
// session's frame dispatch synchronously, before the successor starts sending.
func TestWailsTransportManagerReplacementDropsStaleFrames(t *testing.T) {
	win := &testWindow{name: "main"}
	served := make(chan struct{}, 2)
	returned := make(chan struct{}, 1)
	gw := &testGateway{
		serveFn: func(_ context.Context, conn gateway.FrameConn, opts gateway.SessionOptions) {
			c := conn.(*wailsRawFrameConn)
			served <- struct{}{}
			if opts.Token != "t1" {
				<-c.closed
				return
			}
			for i := 0; ; i++ {
				if err := c.Send(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: fmt.Sprintf("stale%d", i)}); err != nil {
					returned <- struct{}{}
					return
				}
				time.Sleep(time.Millisecond)
			}
		},
	}
	mgr := &wailsTransportManager{gw: gw, conns: make(map[string]*wailsRawFrameConn)}
	origin := &application.OriginInfo{Origin: "wails://wails"}

	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect, Token: "t1"}), origin)
	select {
	case <-served:
	case <-time.After(time.Second):
		t.Fatal("first ServeSession was not started")
	}
	waitForEvent(t, win, rawTransportFrameEvent)

	// markSuperseded + Close run synchronously inside this connect, so no
	// old-session frame may dispatch from here on.
	mgr.handleRawMessage(win, mustJSON(rawEnvelope{Type: rawEnvelopeConnect, Token: "t2"}), origin)
	before := len(extractFrameData(t, win.snapshot()))
	waitForEventCount(t, win, rawTransportOpenEvent, 2)

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := len(extractFrameData(t, win.snapshot())); got != before {
			t.Fatalf("stale frames dispatched after replacement: %d -> %d", before, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("replaced session pump did not exit via superseded Send")
	}
}

// TestEncodeDecodeWireBase64Pooled verifies the pooled base64 helpers round-trip
// correctly, reuse buffers across calls of differing sizes, and surface errors.
func TestEncodeDecodeWireBase64Pooled(t *testing.T) {
	original := []byte("hello world \x00\x01\x02 binary")

	encoded := encodeWireBase64(original)
	if encoded != base64.StdEncoding.EncodeToString(original) {
		t.Fatalf("encode mismatch: %q", encoded)
	}
	var got []byte
	if err := decodeWireBase64(encoded, func(b []byte) error {
		got = append(got, b...)
		return nil
	}); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("decode mismatch: %q", got)
	}

	// Reuse the pool with a much larger payload to confirm a recycled buffer
	// (initially small) grows and decodes correctly.
	big := make([]byte, 5000)
	for i := range big {
		big[i] = byte(i)
	}
	enc2 := encodeWireBase64(big)
	if enc2 != base64.StdEncoding.EncodeToString(big) {
		t.Fatal("large encode mismatch")
	}
	var got2 []byte
	if err := decodeWireBase64(enc2, func(b []byte) error {
		got2 = append(got2, b...)
		return nil
	}); err != nil {
		t.Fatalf("large decode: %v", err)
	}
	if !bytes.Equal(got2, big) {
		t.Fatal("large decode mismatch")
	}

	if err := decodeWireBase64("!!!not-base64!!!", func([]byte) error { return nil }); err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

// TestWailsRawFrameConnSignalCloseDoesNotWait verifies that signalClose
// returns immediately without waiting for the writeLoop goroutine to drain,
// unlike Close which blocks on wg.Wait. This is the property that prevents the
// Wails main-thread deadlock: DispatchWailsEvent -> ExecJS -> InvokeSync needs
// the main thread, but the main thread is the caller of the close path and
// would block in wg.Wait waiting for a goroutine that needs the main thread.
func TestWailsRawFrameConnSignalCloseDoesNotWait(t *testing.T) {
	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnWith(win, rawTransportMaxBatchSize, 50*time.Millisecond)

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k1"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// signalClose must return immediately even though the writeLoop is
	// blocked trying to dispatch to the window. Use a timer to prove it.
	done := make(chan struct{})
	go func() {
		conn.signalClose()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("signalClose blocked waiting for writeLoop (would deadlock on main thread)")
	}
	// Unblock the writeLoop so the goroutine exits and the test doesn't leak.
	win.unblock()
	conn.Close()
}

// TestWailsRawFrameConnDispatchStallBuffersWithoutLoss reproduces the reported
// symptom: a suspended renderer (minimized/occluded WebView2 window) blocks
// DispatchWailsEvent indefinitely. Before the overflow queue the 15s
// dispatch-stall guard force-closed the session, and frames produced between
// teardown and reconnect were lost for live streams — "drops messages,
// recovers on interaction". Now the guard only logs within its force-close
// horizon; frames spill from the bounded outCh into the byte-budgeted
// overflow queue and Send keeps succeeding. Once the renderer resumes
// (unblock) the buffered frames flush in FIFO order with zero loss.
func TestWailsRawFrameConnDispatchStallBuffersWithoutLoss(t *testing.T) {
	win := newBlockingWindow("main")
	// Large flush interval drives batching by the size threshold, so a
	// stalled dispatch carries a full batch and the overflow path is
	// exercised once outCh fills.
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   time.Second,
		dispatchTimeout: 40 * time.Millisecond,
		stallForceClose: time.Minute, // well beyond the test; the guard must only log
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	const n = 300
	frames := make([]*gateway.WireFrame, n)
	for i := 0; i < n; i++ {
		wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: fmt.Sprintf("k%d", i)})
		frames[i], _ = gateway.UnmarshalWireFrame(wire)
	}

	// Send everything while the renderer is suspended. No Send may fail: the
	// overflow queue must absorb everything between outCh filling and the
	// eventual resume.
	for i := 0; i < n; i++ {
		if err := conn.Send(frames[i]); err != nil {
			t.Fatalf("Send %d during stall: %v (overflow should buffer, not drop)", i, err)
		}
	}

	// "Interaction" — the renderer resumes. All buffered frames flush.
	win.unblock()
	conn.Close()

	data := extractFrameData(t, win.snapshot())
	if len(data) != n {
		t.Fatalf("expected %d frames delivered after resume, got %d (frames lost during stall)", n, len(data))
	}
	for i, encoded := range data {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("frame %d: decode base64: %v", i, err)
		}
		got, err := gateway.UnmarshalWireFrame(decoded)
		if err != nil {
			t.Fatalf("frame %d: unmarshal: %v", i, err)
		}
		if want := fmt.Sprintf("k%d", i); got.CallID != want {
			t.Fatalf("frame %d: got CallID %q, want %q (ordering broken)", i, got.CallID, want)
		}
	}
}

// TestWailsRawFrameConnLongStallForceCloses ensures that a stall lasting the
// force-close horizon (zero dispatch progress for minutes, i.e. a renderer
// that is dead rather than merely paused) tears the session down: Send starts
// failing with io.EOF and the frontend reconnects. A transient suspension
// stays within the horizon and must NOT force-close (covered by the lossless
// test above).
func TestWailsRawFrameConnLongStallForceCloses(t *testing.T) {
	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   rawTransportFlushInterval,
		dispatchTimeout: 40 * time.Millisecond,
		stallForceClose: 150 * time.Millisecond,
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k1"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send before stall: %v", err)
	}

	// The stalled dispatch (writeLoop blocked in DispatchWailsEvent) is held
	// past stallForceClose, so the guard force-closes the transport. Once
	// closed, new Sends must report EOF.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.Send(frame); err == io.EOF {
			if reason := conn.closeReasonOf(); !strings.Contains(reason, "dispatch stalled") {
				t.Fatalf("closeReason = %q, want it to record the stalled dispatch", reason)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Send never returned io.EOF after long-stall horizon; session not force-closed")
}

// TestWailsRawFrameConnOverflowWatermarkLogging verifies the observability
// ramp: crossing 50% of the overflow budget logs a watermark warning, and a
// fully-drained overflow logs the episode peak. This is what makes a
// renderer-stall visible in get_system_logs before the terminal force-close
// line — the debug bundle can reconstruct the ramp-up.
func TestWailsRawFrameConnOverflowWatermarkLogging(t *testing.T) {
	var buf syncBuffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   time.Second,
		dispatchTimeout: time.Hour, // no stall-guard noise; only watermark logs
		stallForceClose: time.Hour,
		overflowBudget:  1000,
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	// The outCh/batch/overflow split is timing-dependent, so pump until the
	// 50% watermark fires instead of computing an exact send count. The
	// watermark is guaranteed to precede the budget force-close (overflow
	// grows monotonically while the renderer is stalled), so hitting EOF
	// first is a failure.
	deadline := time.Now().Add(2 * time.Second)
	for i := 0; ; i++ {
		if err := conn.Send(frame); err == io.EOF {
			t.Fatalf("budget force-closed before the 50%% watermark fired, got: %q", buf.String())
		}
		if strings.Contains(buf.String(), "overflow queue at 50%") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("50%% watermark never fired, got: %q", buf.String())
		}
	}
	if !strings.Contains(buf.String(), "overflow queue at 50%") {
		t.Fatalf("50%% watermark log missing, got: %q", buf.String())
	}

	// Renderer resumes: the overflow drains and the episode peak is logged.
	win.unblock()
	conn.Close()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), "overflow queue drained") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("drain log missing, got: %q", buf.String())
}

// syncBuffer is a concurrency-safe log sink for tests.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestWailsRawFrameConnOverflowBudgetForceCloses verifies the safety valve:
// once the overflow queue exceeds its byte budget (renderer suspended for too
// long under heavy streaming), the transport force-closes so the frontend
// reconnects and resubscribes (gospore event rings replay via sinceSeqNo).
func TestWailsRawFrameConnOverflowBudgetForceCloses(t *testing.T) {
	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   time.Second,
		dispatchTimeout: 10 * time.Minute, // only the budget, not the stall guard, should trigger
		stallForceClose: time.Hour,
		overflowBudget:  300,
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	// Pump frames while the renderer is suspended until the overflow budget
	// is exceeded and the transport force-closes.
	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	deadline := time.Now().Add(2 * time.Second)
	sent := 0
	for time.Now().Before(deadline) {
		if err := conn.Send(frame); err == io.EOF {
			// Force-close observed; a subsequent Send must stay EOF.
			if err := conn.Send(frame); err != io.EOF {
				t.Fatalf("Send after force-close returned %v, want io.EOF", err)
			}
			if reason := conn.closeReasonOf(); !strings.Contains(reason, "overflow budget") {
				t.Fatalf("closeReason = %q, want it to record the overflow budget", reason)
			}
			return
		}
		sent++
	}
	t.Fatalf("overflow budget never exceeded after %d sends; force-close did not trigger", sent)
}

// TestWailsRawFrameConnCloseBoundedAgainstWedgedWriteLoop ensures Close does
// not block forever when writeLoop is wedged inside a stalled dispatch. The
// gateway teardown calls conn.Close after a send failure; an unbounded wait
// would hold the session teardown (and its worker pool) hostage. Close must
// return within its drain budget even though writeLoop cannot finish.
func TestWailsRawFrameConnCloseBoundedAgainstWedgedWriteLoop(t *testing.T) {
	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   rawTransportFlushInterval,
		dispatchTimeout: time.Hour,
		closeDrainWait:  120 * time.Millisecond,
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k1"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Give the flush timer a beat to drive writeLoop into the blocking dispatch.
	time.Sleep(60 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		conn.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked waiting for a wedged writeLoop (drain budget not honored)")
	}
}

// blockingWindow is a testWindow whose DispatchWailsEvent blocks until
// unblock is called, simulating InvokeSync waiting for the main thread.
type blockingWindow struct {
	testWindow
	unblockCh chan struct{}
}

func newBlockingWindow(name string) *blockingWindow {
	return &blockingWindow{
		testWindow: testWindow{name: name},
		unblockCh:  make(chan struct{}),
	}
}

func (w *blockingWindow) unblock() {
	select {
	case <-w.unblockCh:
	default:
		close(w.unblockCh)
	}
}

func (w *blockingWindow) DispatchWailsEvent(event *application.CustomEvent) {
	<-w.unblockCh
	w.testWindow.DispatchWailsEvent(event)
}

// TestWailsRawFrameConnCloseEventDeliveredAfterForceClose guards the
// regression where the close notification was enqueued through enqueueOut,
// which drops control events once the transport is closed. Exactly the
// force-close paths (stall watchdog, overflow budget) silenced the close
// event, leaving the frontend stuck on a dead session until its next
// heartbeat tripped "no active session". The out-of-band dispatch must reach
// the window even after a force-close, with buffered frames flushed first.
func TestWailsRawFrameConnCloseEventDeliveredAfterForceClose(t *testing.T) {
	win := &testWindow{name: "main"}
	conn := newWailsRawFrameConn(win)

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k1"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Force-close exactly like the stall watchdog, then run the session
	// teardown sequence: writeLoop still flushes the buffered frame.
	conn.setCloseReason("dispatch stalled with zero progress (dead renderer)")
	conn.signalClose()
	conn.Close()
	<-conn.writeDone
	conn.emitCloseDirect(conn.closeReasonOf())

	events := win.snapshot()
	if len(events) == 0 {
		t.Fatal("no events dispatched")
	}
	last := events[len(events)-1]
	if last.name != rawTransportCloseEvent {
		t.Fatalf("last event = %q, want %q (close event dropped after force-close)", last.name, rawTransportCloseEvent)
	}
	if got, ok := last.data.(string); !ok || got != "dispatch stalled with zero progress (dead renderer)" {
		t.Fatalf("close event data = %v (%T), want force-close reason string", last.data, last.data)
	}
	if got := extractFrameData(t, events); len(got) != 1 {
		t.Fatalf("expected buffered frame flushed before close, got %d frames", len(got))
	}
}

// TestWailsRawFrameConnStallDefersForceCloseWhileSuspended: a minimised
// window suspends its renderer for arbitrarily long — normal usage that the
// fixed stallForceClose horizon used to kill. While suspended the watchdog
// must defer force-close; once restored, a still-wedged renderer is killed
// at the next horizon.
func TestWailsRawFrameConnStallDefersForceCloseWhileSuspended(t *testing.T) {
	win := newBlockingWindow("main")
	conn := newWailsRawFrameConnTuned(win, wailsRawConnTuning{
		maxBatchSize:    rawTransportMaxBatchSize,
		flushInterval:   rawTransportFlushInterval,
		dispatchTimeout: 40 * time.Millisecond,
		stallForceClose: 150 * time.Millisecond,
	})
	defer func() {
		win.unblock()
		<-conn.writeDone
	}()

	// The window is minimised before the first stalled dispatch.
	conn.suspended.Store(true)

	wire, _ := gateway.MarshalWireFrame(&gateway.WireFrame{Type: gateway.FrameTypeReply, CallID: "k1"})
	frame, _ := gateway.UnmarshalWireFrame(wire)
	if err := conn.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Several stall horizons pass while suspended: no force-close.
	deadline := time.Now().Add(600 * time.Millisecond)
	for time.Now().Before(deadline) {
		if err := conn.Send(frame); err == io.EOF {
			t.Fatal("force-closed while renderer suspended (minimised window)")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Window restored: the still-wedged renderer is killed at the next horizon.
	conn.suspended.Store(false)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := conn.Send(frame); err == io.EOF {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("force-close never fired after restore; watchdog leaked the session")
}

// eventTestWindow is a testWindow that records OnWindowEvent subscriptions so
// tests can synthesise window minimise/restore events.
type eventTestWindow struct {
	testWindow
	hooksMu sync.Mutex
	hooks   map[events.WindowEventType]func(*application.WindowEvent)
}

func (w *eventTestWindow) OnWindowEvent(eventType events.WindowEventType, callback func(*application.WindowEvent)) func() {
	w.hooksMu.Lock()
	if w.hooks == nil {
		w.hooks = make(map[events.WindowEventType]func(*application.WindowEvent))
	}
	w.hooks[eventType] = callback
	w.hooksMu.Unlock()
	return func() {
		w.hooksMu.Lock()
		delete(w.hooks, eventType)
		w.hooksMu.Unlock()
	}
}

func (w *eventTestWindow) fire(eventType events.WindowEventType) {
	w.hooksMu.Lock()
	cb := w.hooks[eventType]
	w.hooksMu.Unlock()
	if cb != nil {
		cb(&application.WindowEvent{})
	}
}

func (w *eventTestWindow) hookCount() int {
	w.hooksMu.Lock()
	defer w.hooksMu.Unlock()
	return len(w.hooks)
}

// TestWailsTransportManagerWiresRendererSuspension verifies startSession
// bridges window minimise/hide/restore events into the connection's
// suspension flag (the gate for the stall watchdog's force-close) and
// cancels the subscriptions when the session tears down.
func TestWailsTransportManagerWiresRendererSuspension(t *testing.T) {
	win := &eventTestWindow{testWindow: testWindow{name: "main"}}
	release := make(chan struct{})
	gw := &testGateway{
		serveFn: func(context.Context, gateway.FrameConn, gateway.SessionOptions) {
			<-release
		},
	}
	mgr := newWailsTransportManager(nil)
	mgr.gw = gw

	mgr.startSession(win, "")

	var conn *wailsRawFrameConn
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mgr.mu.Lock()
		c, ok := mgr.conns["main"]
		mgr.mu.Unlock()
		if ok {
			conn = c
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if conn == nil {
		t.Fatal("session never started")
	}

	win.fire(events.Common.WindowMinimise)
	if !conn.suspended.Load() {
		t.Fatal("minimise did not mark the renderer suspended")
	}
	win.fire(events.Common.WindowHide)
	if !conn.suspended.Load() {
		t.Fatal("hide did not keep the renderer suspended")
	}
	win.fire(events.Common.WindowRestore)
	if conn.suspended.Load() {
		t.Fatal("restore did not clear renderer suspension")
	}

	// Session teardown must cancel the window-event subscriptions so
	// reconnects don't leak listeners.
	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if win.hookCount() == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("suspension subscriptions not cancelled on session teardown")
}
