package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
)

// testPluginEnv makes the test binary re-execute itself as the plugin
// process (checked in TestMain). This mirrors the T1 spike's re-exec trick:
// the child spawned by the opener under test is a real, separate OS process
// speaking the duplex framing protocol from process_frame.go.
const testPluginEnv = "PLUGINHOST_PROCESS_OPENER_TEST_PLUGIN"

// testPluginOnLoadFailEnv makes the re-exec plugin's OnLoad hook report an
// {"error": ...} body, exercising the runOnLoad failure classification.
const testPluginOnLoadFailEnv = "PLUGINHOST_PROCESS_OPENER_TEST_ONLOAD_FAIL"

// testPluginStderrNoiseEnv makes the re-exec plugin write two lines to
// stderr before serving, exercising the host's stderr pump.
const testPluginStderrNoiseEnv = "PLUGINHOST_PROCESS_OPENER_TEST_STDERR_NOISE"

// testPluginHTTPAddrEnv makes the re-exec plugin's OnLoad hook answer with
// {"httpAddr": <value>}, exercising the host's runOnLoad address parsing.
const testPluginHTTPAddrEnv = "PLUGINHOST_PROCESS_OPENER_TEST_HTTP_ADDR"

// lastOnLoadPayload captures the payload the re-exec plugin received on its
// OnLoad invoke, so tests can assert the host pushed the per-instance config.
var lastOnLoadPayload []byte

func TestMain(m *testing.M) {
	if os.Getenv(testPluginEnv) == "1" {
		os.Exit(runTestPlugin())
	}
	os.Exit(m.Run())
}

// bgReverseCh carries a v2 correlated reverse-resp from the main read loop
// to a background goroutine's pending reverse call (full-duplex test hook).
var bgReverseCh chan []byte

// bgDone carries the background reverse call's outcome to the bgresult
// callable.
var bgDone chan string

// runTestPlugin is the stand-in for the SDK process entry (T4 process_main.go
// mirror): read 0x01 invoke-reqs from stdin, dispatch, write 0x02 responses
// to stdout, emit 0x05 log frames, and answer synchronous 0x03/0x04 reverse
// calls on the same duplex pipe. v2 correlated reverse-resps (0x84) destined
// for a background goroutine are demuxed by the main loop onto bgReverseCh.
func runTestPlugin() int {
	if os.Getenv(testPluginStderrNoiseEnv) == "1" {
		fmt.Fprintf(os.Stderr, "stderr noise line 1\nstderr noise line 2\n")
	}
	bgReverseCh = make(chan []byte, 1)
	bgDone = make(chan string, 1)
	in := bufio.NewReader(os.Stdin)
	out := os.Stdout
	for {
		frame, err := ReadTransportFrame(in)
		if err != nil {
			if err == io.EOF {
				return 0 // graceful unload via stdin EOF
			}
			return 1
		}
		switch frame.Type {
		case MsgInvokeReq:
			// bgresult runs on a WORKER goroutine (like the real SDK's
			// dispatchInvoke): it blocks waiting for the background
			// reverse call, so the main loop must stay free to demux the
			// correlated 0x84 onto bgReverseCh meanwhile.
			var probe gen.PluginAbiInvokeEnvelope
			if json.Unmarshal(frame.Payload, &probe) == nil && probe.Callable == "bgresult" {
				go func(payload []byte) {
					resp, err := testPluginDispatch(payload, in, out)
					if err != nil {
						errBody, _ := json.Marshal(map[string]string{"error": err.Error()})
						resp = errBody
					}
					if werr := WriteTransportFrame(out, MsgInvokeResp, "", resp); werr != nil {
						os.Exit(3)
					}
				}(frame.Payload)
				continue
			}
			resp, err := testPluginDispatch(frame.Payload, in, out)
			if err != nil {
				errBody, _ := json.Marshal(map[string]string{"error": err.Error()})
				if werr := WriteTransportFrame(out, MsgInvokeResp, "", errBody); werr != nil {
					return 1
				}
				continue
			}
			if werr := WriteTransportFrame(out, MsgInvokeResp, "", resp); werr != nil {
				return 1
			}
		case MsgReverseResp:
			// Correlated reply to a background reverse call; the sync
			// reverse helper (testPluginReverse) consumes its own replies
			// while the main loop is blocked inside dispatch.
			bgReverseCh <- frame.Payload
		case MsgError:
			return 1
		default:
			return 2
		}
	}
}

// testPluginReverse is the plugin-side synchronous reverse bridge call: write
// a 0x03 reverse-req and block reading until the matching 0x04 reverse-resp.
// A __host_error__ envelope is converted into a Go error (mirroring sdk
// bridgeResult).
func testPluginReverse(in *bufio.Reader, out io.Writer, callID string, payload any) ([]byte, error) {
	req, err := json.Marshal(map[string]any{"callID": callID, "payload": payload})
	if err != nil {
		return nil, err
	}
	if err := WriteTransportFrame(out, MsgReverseReq, callID, req); err != nil {
		return nil, err
	}
	for {
		f, err := ReadTransportFrame(in)
		if err != nil {
			return nil, err
		}
		switch f.Type {
		case MsgReverseResp:
			var env struct {
				HostError string `json:"__host_error__"`
			}
			if json.Unmarshal(f.Payload, &env) == nil && env.HostError != "" {
				return nil, fmt.Errorf("host invoke %s: %s", callID, env.HostError)
			}
			return f.Payload, nil
		case MsgError:
			return nil, fmt.Errorf("host reported fatal error: %s", f.Payload)
		default:
			return nil, fmt.Errorf("unexpected message 0x%02x while awaiting reverse-resp", f.Type)
		}
	}
}

func testPluginDispatch(payload []byte, in *bufio.Reader, out io.Writer) ([]byte, error) {
	var env gen.PluginAbiInvokeEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("decode invoke envelope: %w", err)
	}
	switch env.Callable {
	case "ping":
		_ = WriteTransportFrame(out, MsgLog, "", []byte("ping called"))
		return json.Marshal(map[string]string{"pong": "ok"})
	case "onLoad":
		// Reserved lifecycle hook (SDK ProcessOnLoadCallable): the host runs
		// OnLoad as the first invoke-req after spawn. Expects an empty
		// success body ("{}"); an {"error": ...} body would fail Open. The
		// payload is the host-pushed per-instance config; when the HTTP-addr
		// env is set the hook answers like the SDK does after auto-starting
		// its listener from that config.
		lastOnLoadPayload = append([]byte(nil), env.Payload...)
		if os.Getenv(testPluginOnLoadFailEnv) == "1" {
			return json.Marshal(map[string]string{"error": "onLoad boom"})
		}
		if addr := os.Getenv(testPluginHTTPAddrEnv); addr != "" {
			return json.Marshal(map[string]string{"httpAddr": addr})
		}
		return []byte("{}"), nil
	case "onloadpayload":
		// Echo the OnLoad payload back so the host side can assert the
		// config delivery.
		body, _ := json.Marshal(map[string]string{"payload": string(lastOnLoadPayload)})
		return body, nil
	case "crash":
		// Mid-invoke process death: emit a log frame, then exit hard so the
		// host's next frame read hits EOF (crash detection path).
		_ = WriteTransportFrame(out, MsgLog, "", []byte("dying now"))
		os.Exit(7)
		return nil, nil
	case "stall":
		time.Sleep(60 * time.Second)
		return json.Marshal(map[string]string{"stalled": "never"})
	case "greet":
		var req struct {
			Name    string          `json:"name"`
			Reverse json.RawMessage `json:"reverse,omitempty"`
		}
		if err := json.Unmarshal(env.Payload, &req); err != nil {
			return nil, err
		}
		_ = WriteTransportFrame(out, MsgLog, "", []byte("greet called for "+req.Name))
		resp := map[string]any{"message": "Hello, " + req.Name + "!"}
		if len(req.Reverse) > 0 {
			var rev struct {
				CallID  string          `json:"callID"`
				Payload json.RawMessage `json:"payload"`
			}
			if err := json.Unmarshal(req.Reverse, &rev); err != nil {
				return nil, err
			}
			revResp, err := testPluginReverse(in, out, rev.CallID, rev.Payload)
			if err != nil {
				return nil, fmt.Errorf("greet reverse %s: %w", rev.CallID, err)
			}
			resp["reverse_result"] = json.RawMessage(revResp)
		}
		return json.Marshal(resp)
	case "slow_reverse":
		revResp, err := testPluginReverse(in, out, "llm.slow", map[string]any{"model": "slow"})
		if err != nil {
			return nil, fmt.Errorf("slow_reverse: %w", err)
		}
		return json.Marshal(map[string]any{"reverse_result": json.RawMessage(revResp)})
	case "spawnbg":
		// Full-duplex hook: a background goroutine issues a v2 CORRELATED
		// reverse call (callID in the frame header) AFTER this handler
		// returned, i.e. with no invoke in flight on the host side.
		go func() {
			req, err := json.Marshal(map[string]any{"callID": "llm.complete", "payload": map[string]any{"prompt": "bg"}})
			if err != nil {
				bgDone <- "err:" + err.Error()
				return
			}
			if err := WriteTransportFrame(out, MsgReverseReq, "bg1", req); err != nil {
				bgDone <- "err:" + err.Error()
				return
			}
			select {
			case p := <-bgReverseCh:
				bgDone <- "ok:" + string(p)
			case <-time.After(8 * time.Second):
				bgDone <- "err:timeout waiting for correlated reverse-resp"
			}
		}()
		return json.Marshal(map[string]string{"started": "true"})
	case "bgresult":
		select {
		case r := <-bgDone:
			return json.Marshal(map[string]string{"result": r})
		case <-time.After(8 * time.Second):
			return nil, fmt.Errorf("bg result timeout")
		}
	default:
		return nil, fmt.Errorf("callable %s not found", env.Callable)
	}
}

// eventRecorder implements pluginLogSink and feeds the reverse dispatch so
// tests can assert the exact interleaved event order. State reports are
// kept in a separate slice so log-event order assertions stay unaffected.
type eventRecorder struct {
	mu     sync.Mutex
	events []string
	states []stateReport
	onLog  func(level int, msg string)
	onCall func(callID string)
	gen    int64
}

type stateReport struct {
	state string
	crash string
}

func (r *eventRecorder) LogPluginEntry(pluginID string, generation int, level int, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "log: "+msg)
	if r.onLog != nil {
		r.onLog(level, msg)
	}
}

func (r *eventRecorder) ReportProcessState(pluginID, state, crash, httpAddr string, gen int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, stateReport{state: state, crash: crash})
}

func (r *eventRecorder) NextProcessGeneration(pluginID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	return r.gen
}

func (r *eventRecorder) stateReports() []stateReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stateReport(nil), r.states...)
}

func (r *eventRecorder) dispatch(callID string, req []byte) ([]byte, error) {
	r.mu.Lock()
	r.events = append(r.events, "reverse: "+callID)
	r.mu.Unlock()
	if r.onCall != nil {
		r.onCall(callID)
	}
	return []byte(`{"ok":true}`), nil
}

func (r *eventRecorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// newTestProcessOpener spawns the test binary as a plugin via re-exec and
// returns the opener, its invoke func, and its closer. allowed controls the
// capability gate of the default HostBridge reverse handler.
func newTestProcessOpener(t *testing.T, allowed map[string]struct{}, rec *eventRecorder) (*processOpener, func(ctx context.Context, callable string, request []byte) ([]byte, error), func() error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	if rec == nil {
		rec = &eventRecorder{}
	}
	op := &processOpener{
		dispatch: rec.dispatch,
		logger:   rec,
		env:      append(os.Environ(), testPluginEnv+"=1"),
		// Shrink the settle grace: only post-budget-expiry behavior reads it,
		// so the timeout-kill tests reach the wedge verdict in ~450ms instead
		// of production's 5s.
		settleGrace: 150 * time.Millisecond,
	}
	invoke, _, closer, _, err := op.Open(gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}, exe, "", "test.plugin", allowed, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = closer() })
	return op, invoke, closer
}

func TestProcessOpenerForwardRoundTrip(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, closer := newTestProcessOpener(t, map[string]struct{}{"llm.invoke": {}}, rec)

	// Multiple sequential invokes on one process; a 0x05 log frame precedes
	// the invoke-resp and is forwarded to the log sink.
	for i := 0; i < 3; i++ {
		resp, err := invoke(context.Background(), "ping", []byte(`{"n":1}`))
		if err != nil {
			t.Fatalf("invoke ping (round %d): %v", i, err)
		}
		if string(resp) != `{"pong":"ok"}` {
			t.Fatalf("ping resp = %s", resp)
		}
	}

	// Closer kills the process and is idempotent.
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	if err := closer(); err != nil {
		t.Fatalf("second closer: %v", err)
	}

	logs := rec.list()
	found := false
	for _, e := range logs {
		if e == "log: ping called" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("log frame was not forwarded; events = %v", logs)
	}
}

func TestProcessOpenerInvokeAfterUnload(t *testing.T) {
	_, invoke, closer := newTestProcessOpener(t, nil, nil)
	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	_, err := invoke(context.Background(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("invoke after unload: err = %v, want 'not running'", err)
	}
}

// TestProcessOpenerQueuedInvokeDoesNotKillProcess pins the fix for the
// novel-app regression: a single-flight starved invoke (queued behind a
// running one until its own budget expired) must fail WITHOUT killing the
// subprocess. Before the fix every starved invoke tore the plugin down and
// left it dead until plugin_load, so even sub-second state-only callables
// (novel_list) failed for the rest of the turn.
func TestProcessOpenerQueuedInvokeDoesNotKillProcess(t *testing.T) {
	op, invoke, closer := newTestProcessOpener(t, nil, nil)

	// A long-running invoke holds the single-flight session (the stall
	// handler sleeps 60s; the test never waits for it — closer() reaps).
	stallDone := make(chan error, 1)
	go func() {
		_, err := invoke(context.Background(), "stall", nil)
		stallDone <- err
	}()
	time.Sleep(300 * time.Millisecond) // let the stall invoke be admitted

	// A second invoke with a short budget queues, then fails at admission.
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, err := invoke(ctx, "ping", nil)
	if err == nil {
		t.Fatal("starved invoke must fail once its budget expires")
	}
	if !errors.Is(err, errInvokeQueued) {
		t.Fatalf("starved invoke err = %v, want errInvokeQueued", err)
	}
	if strings.Contains(err.Error(), "plugin killed") {
		t.Fatalf("starved invoke must not kill the plugin: %v", err)
	}

	// The subprocess must still be alive — the kill path is reserved for a
	// plugin that genuinely exceeded its own declared budget.
	op.killMu.Lock()
	exited := op.exited
	op.killMu.Unlock()
	if exited {
		t.Fatal("starved invoke killed the subprocess; queue starvation must fail the invoke only")
	}

	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	_ = stallDone
}

// TestProcessOpenerStderrForwardedToLog: child stderr is captured via a host
// pipe (not inherited from the host's stderr handle) and forwarded to the log
// sink as error-level entries, while the frame protocol stays unaffected.
func TestProcessOpenerStderrForwardedToLog(t *testing.T) {
	rec := &eventRecorder{}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	op := &processOpener{
		dispatch: rec.dispatch,
		logger:   rec,
		env:      append(os.Environ(), testPluginEnv+"=1", testPluginStderrNoiseEnv+"=1"),
	}
	invoke, _, closer, _, err := op.Open(gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}, exe, "", "test.plugin", nil, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = closer() }()

	// The frame protocol is unaffected by the stderr noise.
	resp, err := invoke(context.Background(), "ping", []byte(`{}`))
	if err != nil {
		t.Fatalf("invoke ping: %v", err)
	}
	if string(resp) != `{"pong":"ok"}` {
		t.Fatalf("ping resp = %s", resp)
	}

	// The stderr pump runs concurrently; poll briefly for both lines.
	want := []string{
		"log: plugin stderr: stderr noise line 1",
		"log: plugin stderr: stderr noise line 2",
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if containsAllEvents(rec.list(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stderr lines not forwarded; events = %v", rec.list())
}

func containsAllEvents(events, want []string) bool {
	for _, w := range want {
		found := false
		for _, e := range events {
			if e == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestProcessOpenerReverseRoundTrip(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, _ := newTestProcessOpener(t, map[string]struct{}{"llm.invoke": {}}, rec)

	req := map[string]any{
		"name":    "Ada",
		"reverse": map[string]any{"callID": "llm.complete", "payload": map[string]any{"prompt": "hi"}},
	}
	reqBytes, _ := json.Marshal(req)
	resp, err := invoke(context.Background(), "greet", reqBytes)
	if err != nil {
		t.Fatalf("invoke greet: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode greet resp %s: %v", resp, err)
	}
	if out["message"] != "Hello, Ada!" {
		t.Fatalf("message = %v", out["message"])
	}
	revResult, ok := out["reverse_result"].(map[string]any)
	if !ok || revResult["ok"] != true {
		t.Fatalf("reverse_result = %v, want {\"ok\":true}", out["reverse_result"])
	}

	// Exact interleave order: the spawn diagnostic is the first entry, then
	// the 0x05 log arrives before the reverse-req, the reverse dispatch runs,
	// and only then the invoke completes.
	events := rec.list()
	wantTail := []string{"log: greet called for Ada", "reverse: llm.complete"}
	if len(events) < 2 {
		t.Fatalf("events = %v, want tail %v", events, wantTail)
	}
	if events[0] != "log: pluginhost: subprocess spawned" {
		t.Fatalf("events[0] = %q, want spawn diagnostic", events[0])
	}
	if events[len(events)-2] != wantTail[0] || events[len(events)-1] != wantTail[1] {
		t.Fatalf("event tail = %v, want %v", events[len(events)-2:], wantTail)
	}
}

func TestProcessOpenerReverseCapabilityDenied(t *testing.T) {
	// No capabilities granted: HostBridge.Dispatch denies every reverse call.
	rec := &eventRecorder{}
	_, invoke, _ := newTestProcessOpener(t, map[string]struct{}{}, rec)

	req, _ := json.Marshal(map[string]any{
		"name":    "Denied",
		"reverse": map[string]any{"callID": "llm.complete", "payload": map[string]any{}},
	})
	resp, err := invoke(context.Background(), "greet", req)
	if err != nil {
		t.Fatalf("invoke greet: %v", err)
	}
	// The plugin converted the __host_error__ envelope into a Go error and
	// its invoke failed, so the invoke-resp carries {"error": ...}.
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode resp %s: %v", resp, err)
	}
	errMsg, _ := out["error"].(string)
	if !strings.Contains(errMsg, "capability not granted") {
		t.Fatalf("resp = %s, want capability-denied error inside invoke-resp", resp)
	}
}

func TestProcessOpenerBackgroundReverseFullDuplex(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, closer := newTestProcessOpener(t, map[string]struct{}{"llm.invoke": {}}, rec)
	defer func() { _ = closer() }()

	// 1. The handler spawns a background reverse caller and returns.
	resp, err := invoke(context.Background(), "spawnbg", nil)
	if err != nil {
		t.Fatalf("invoke spawnbg: %v", err)
	}
	if !strings.Contains(string(resp), "started") {
		t.Fatalf("spawnbg resp = %s", resp)
	}

	// 2. The background reverse call lands with NO invoke in flight; the
	// session's permanent reader must dispatch it and reply with a
	// correlated 0x04. bgresult then proves the child received it.
	resp2, err := invoke(context.Background(), "bgresult", nil)
	if err != nil {
		t.Fatalf("invoke bgresult: %v", err)
	}
	var out struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(resp2, &out); err != nil {
		t.Fatalf("decode bgresult resp %s: %v", resp2, err)
	}
	if !strings.HasPrefix(out.Result, "ok:") {
		t.Fatalf("background reverse result = %q, want ok:{\"ok\":true}; events = %v", out.Result, rec.list())
	}
	if !strings.Contains(out.Result, `"ok":true`) {
		t.Fatalf("background reverse result = %q, want host dispatch payload", out.Result)
	}

	events := rec.list()
	found := false
	for _, e := range events {
		if e == "reverse: llm.complete" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %v, want reverse dispatch recorded", events)
	}
}

func TestProcessOpenerCrashMidInvoke(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, closer := newTestProcessOpener(t, nil, rec)

	_, err := invoke(context.Background(), "crash", nil)
	if err == nil {
		t.Fatal("expected error for crashed plugin, got nil")
	}
	// Reported as an error, not a panic, and clearly crash-classified.
	if !strings.Contains(err.Error(), "mid-invoke") {
		t.Fatalf("err = %v, want mid-invoke classification", err)
	}

	// The crash is recorded: a subsequent invoke reports the same condition.
	_, err = invoke(context.Background(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "process exited") {
		t.Fatalf("invoke after crash: err = %v, want process exited", err)
	}

	// Closer is safe after a crash (process already reaped).
	if err := closer(); err != nil {
		t.Fatalf("closer after crash: %v", err)
	}

	// The pre-crash log frame still flowed to the sink.
	found := false
	for _, e := range rec.list() {
		if e == "log: dying now" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("pre-crash log frame was not forwarded")
	}
}

func TestProcessOpenerTimeoutKillsProcess(t *testing.T) {
	_, invoke, closer := newTestProcessOpener(t, nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := invoke(ctx, "stall", nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("invoke stall: err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v, plugin was not killed", elapsed)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer after timeout: %v", err)
	}
}

// TestProcessOpenerRespawnAfterTimeout verifies that a fresh opener spawns a
// new process after the previous one was killed on an invoke timeout.
func TestProcessOpenerRespawnAfterTimeout(t *testing.T) {
	_, invoke, _ := newTestProcessOpener(t, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	_, err := invoke(ctx, "stall", nil)
	cancel()
	if err == nil {
		t.Fatal("expected timeout error")
	}

	// A fresh opener spawns a new process: timeout kill leaves no residue.
	rec := &eventRecorder{}
	_, invoke2, _ := newTestProcessOpener(t, map[string]struct{}{}, rec)
	resp, err := invoke2(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("invoke ping on fresh opener: %v", err)
	}
	if string(resp) != `{"pong":"ok"}` {
		t.Fatalf("resp = %s", resp)
	}
}

// TestProcessOpenerReverseBudgetDerivedFromOuter is the nested-timeout fix:
// the reverse dispatch is bounded by the REMAINING outer invoke budget
// (minus unwind headroom), so it fails cleanly via the __host_error__ envelope
// and the process is NOT killed mid-call — a following invoke still works.
func TestProcessOpenerReverseBudgetDerivedFromOuter(t *testing.T) {
	done := make(chan struct{})
	defer close(done)
	rec := &eventRecorder{}
	rec.onCall = func(callID string) {
		if callID != "llm.slow" {
			return
		}
		// Simulate a slow backing service: block far beyond both the outer
		// budget (1.5s) and the old 30s reverse timeout would allow.
		select {
		case <-done:
		case <-time.After(60 * time.Second):
		}
	}

	_, invoke, _ := newTestProcessOpener(t, map[string]struct{}{"llm.invoke": {}}, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	// Mirror the production path: handleInvoke wraps the timeout ctx with
	// WithInvokeMeta before the opener sees it — reverse budget inheritance
	// (serveReverseAsync) keys on that meta, so a bare ctx would not derive.
	metaCtx := ph.WithInvokeMeta(ctx, ph.InvokeMeta{RequestID: "req-slow"})

	start := time.Now()
	resp, err := invoke(metaCtx, "slow_reverse", nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("invoke slow_reverse: %v", err)
	}
	// The plugin surfaced the derived-budget failure inside the invoke-resp.
	var out map[string]any
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("decode resp %s: %v", resp, err)
	}
	errMsg, _ := out["error"].(string)
	if !strings.Contains(errMsg, "deadline exceeded") {
		t.Fatalf("resp = %s, want derived-budget error, not an alive reverse", resp)
	}
	// Bounded by the outer budget (+headroom), never by the dispatch's own
	// long sleep.
	if elapsed > 1400*time.Millisecond {
		t.Fatalf("invoke took %v; reverse budget was not derived from the outer budget", elapsed)
	}

	// The process is still alive and answering: the plugin was not killed
	// while its reverse call was in flight.
	resp, err = invoke(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("invoke ping after budget failure: %v", err)
	}
	if string(resp) != `{"pong":"ok"}` {
		t.Fatalf("resp = %s", resp)
	}
}

// TestReverseBudgetContext covers the nested-timeout fix's pure logic: with
// an outer deadline the reverse budget is the remaining budget minus unwind
// headroom (so an invoke including nested reverse calls never exceeds the
// outer timeout); without a deadline it falls back to hostBridgeInvokeTimeout.
func TestReverseBudgetContext(t *testing.T) {
	// No outer deadline -> standalone reverse cap (config.get has no route
	// budget, so the generic hostBridgeInvokeTimeout applies).
	ctx, cancel := reverseBudgetContext(context.Background(), "config.get")
	defer cancel()
	if d, ok := ctx.Deadline(); !ok {
		t.Fatal("expected a deadline from the fallback budget")
	} else if remaining := time.Until(d); remaining < hostBridgeInvokeTimeout-100*time.Millisecond || remaining > hostBridgeInvokeTimeout {
		t.Fatalf("fallback budget = %v, want ~hostBridgeInvokeTimeout", remaining)
	}

	// Outer deadline -> derived from remaining budget minus headroom.
	outer, _ := context.WithDeadline(context.Background(), time.Now().Add(5*time.Second))
	derived, cancel := reverseBudgetContext(outer, "llm.complete")
	defer cancel()
	d, ok := derived.Deadline()
	if !ok {
		t.Fatal("expected a derived deadline")
	}
	remaining := time.Until(d)
	want := 5*time.Second - reverseHeadroom
	if remaining < want-100*time.Millisecond || remaining > want {
		t.Fatalf("derived budget = %v, want ~%v", remaining, want)
	}

	// An already-expired outer deadline yields an immediately-fired budget.
	expired, _ := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	derived2, cancel2 := reverseBudgetContext(expired, "llm.complete")
	defer cancel2()
	if _, ok := derived2.Deadline(); !ok {
		t.Fatal("expected an expired derived deadline")
	}
	if err := derived2.Err(); err == nil {
		t.Fatal("expected derived budget to be already expired")
	}
}

// TestProcessOpenerConcurrentInvokesSerialized exercises the 初版串行化
// decision: concurrent host goroutines queue on invokeMu without cross-wiring
// responses (each invoke carries its own reverse call).
func TestProcessOpenerConcurrentInvokesSerialized(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, _ := newTestProcessOpener(t, map[string]struct{}{"llm.invoke": {}}, rec)

	const goroutines = 6
	const rounds = 5
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*rounds)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				req, _ := json.Marshal(map[string]any{
					"name":    fmt.Sprintf("G%dR%d", g, r),
					"reverse": map[string]any{"callID": "llm.complete", "payload": map[string]any{"g": g, "r": r}},
				})
				resp, err := invoke(context.Background(), "greet", req)
				if err != nil {
					errs <- fmt.Errorf("g%d r%d: %w", g, r, err)
					continue
				}
				var out map[string]any
				if err := json.Unmarshal(resp, &out); err != nil {
					errs <- fmt.Errorf("g%d r%d: decode: %v", g, r, err)
					continue
				}
				if out["message"] != fmt.Sprintf("Hello, G%dR%d!", g, r) {
					errs <- fmt.Errorf("g%d r%d: cross-wired response %v", g, r, out["message"])
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestProcessOpenerOnLoadFailureFailsOpen covers the OnLoad-as-first-
// invoke-req contract (subprocess mirror of the FFI callOnLoad): a plugin
// whose OnLoad reports {"error": ...} in-band must fail Open (the process is
// killed), and a subsequent Open on a fresh process must succeed — the
// failed lifecycle leaves no residue.
func TestProcessOpenerOnLoadFailureFailsOpen(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	abi := gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}

	op := &processOpener{
		dispatch: (&eventRecorder{}).dispatch,
		env:      append(os.Environ(), testPluginEnv+"=1", testPluginOnLoadFailEnv+"=1"),
	}
	_, _, _, _, err = op.Open(abi, exe, "", "test.plugin", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "OnLoad") || !strings.Contains(err.Error(), "onLoad boom") {
		t.Fatalf("Open with failing OnLoad = %v, want the OnLoad error surfaced", err)
	}

	// Fresh process, no failure env: OnLoad succeeds and invokes work.
	op2 := &processOpener{
		dispatch: (&eventRecorder{}).dispatch,
		env:      append(os.Environ(), testPluginEnv+"=1"),
	}
	invoke, _, closer, _, err := op2.Open(abi, exe, "", "test.plugin", nil, nil)
	if err != nil {
		t.Fatalf("Open after failed OnLoad: %v", err)
	}
	defer func() { _ = closer() }()
	resp, err := invoke(context.Background(), "ping", nil)
	if err != nil || string(resp) != `{"pong":"ok"}` {
		t.Fatalf("invoke ping after failed-then-clean OnLoad: resp=%s err=%v", resp, err)
	}
}

// lastStateReport returns the most recent state report, failing the test
// when none arrived yet.
func lastStateReport(t *testing.T, rec *eventRecorder) stateReport {
	t.Helper()
	states := rec.stateReports()
	if len(states) == 0 {
		t.Fatal("no process state reports recorded")
	}
	return states[len(states)-1]
}

// TestProcessOpenerSpawnReportsRunning pins the spawn transition point:
// Open must report running through the ReportProcessState seam, and the
// closer must report stopped — both independent of log message strings.
func TestProcessOpenerSpawnReportsRunning(t *testing.T) {
	rec := &eventRecorder{}
	_, _, closer := newTestProcessOpener(t, nil, rec)

	if s := lastStateReport(t, rec); s.state != "running" || s.crash != "" {
		t.Fatalf("spawn state report = %+v, want running without crash", s)
	}

	if err := closer(); err != nil {
		t.Fatalf("closer: %v", err)
	}
	if s := lastStateReport(t, rec); s.state != "stopped" || s.crash != "" {
		t.Fatalf("unload state report = %+v, want stopped without crash", s)
	}
}

// TestProcessOpenerAbnormalExitReportsCrashed pins the recordExit transition
// point: an abnormal exit (session EOF, protocol violation) reports crashed
// with the cause attached.
func TestProcessOpenerAbnormalExitReportsCrashed(t *testing.T) {
	rec := &eventRecorder{}
	op, _, closer := newTestProcessOpener(t, nil, rec)

	op.recordExit(fmt.Errorf("session read: EOF"))

	if s := lastStateReport(t, rec); s.state != "crashed" || !strings.Contains(s.crash, "EOF") {
		t.Fatalf("crash state report = %+v, want crashed with EOF cause", s)
	}
	// recordExit already reaped: close is a no-op and must not overwrite
	// the crashed verdict with stopped.
	if err := closer(); err != nil {
		t.Fatalf("closer after crash: %v", err)
	}
	if s := lastStateReport(t, rec); s.state != "crashed" {
		t.Fatalf("state report after close = %+v, want crashed preserved", s)
	}
}

// TestProcessOpenerTimeoutKillReportsCrashedWithCause pins the invoke-timeout
// kill transition: the timeout kill funnels through recordExit with a
// "killed on invoke timeout" cause, so the state report must carry it (the
// path that previously logged nothing and left the verdict stale).
func TestProcessOpenerTimeoutKillReportsCrashedWithCause(t *testing.T) {
	rec := &eventRecorder{}
	_, invoke, closer := newTestProcessOpener(t, nil, rec)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := invoke(ctx, "stall", nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("invoke stall: err = %v, want deadline exceeded", err)
	}

	if s := lastStateReport(t, rec); s.state != "crashed" || !strings.Contains(s.crash, "settle grace") {
		t.Fatalf("timeout-kill state report = %+v, want crashed with desync (settle grace) cause", s)
	}
	if err := closer(); err != nil {
		t.Fatalf("closer after timeout kill: %v", err)
	}
}

// TestParseOnLoadHTTPAddr covers the OnLoad response address extraction:
// the SDK answers {"httpAddr": "<bound>"} after auto-starting its listener
// from the pushed config, {} when no listener started, and {"error": ...}
// never reaches the parser (runOnLoad classifies it first).
func TestParseOnLoadHTTPAddr(t *testing.T) {
	cases := []struct {
		name string
		resp string
		want string
	}{
		{"sdk listener report", `{"httpAddr":"127.0.0.1:52345"}`, "127.0.0.1:52345"},
		{"empty success body", `{}`, ""},
		{"no config legacy", ``, ""},
		{"malformed", `not json`, ""},
		{"wrong type", `{"httpAddr":42}`, ""},
		{"unrelated fields", `{"other":"x"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseOnLoadHTTPAddr([]byte(tc.resp)); got != tc.want {
				t.Fatalf("parseOnLoadHTTPAddr(%q) = %q, want %q", tc.resp, got, tc.want)
			}
		})
	}
}

// TestProcessOpenerOnLoadConfigDeliveryAndHTTPAddrReport is the T4 contract
// test over the real re-exec child process: the OnLoad invoke must carry the
// per-instance config payload verbatim, and a listener report in the OnLoad
// response must surface as Open's httpAddr return and the opener's HTTPAddr.
func TestProcessOpenerOnLoadConfigDeliveryAndHTTPAddrReport(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	abi := gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}
	cfg := []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"deadbeef"}`)
	op := &processOpener{
		dispatch: (&eventRecorder{}).dispatch,
		env:      append(os.Environ(), testPluginEnv+"=1", testPluginHTTPAddrEnv+"=127.0.0.1:54321"),
	}
	invoke, _, closer, httpAddr, err := op.Open(abi, exe, "", "test.plugin", nil, cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = closer() }()
	if httpAddr != "127.0.0.1:54321" {
		t.Fatalf("Open httpAddr = %q, want the OnLoad-reported address", httpAddr)
	}
	if got := op.HTTPAddr(); got != "127.0.0.1:54321" {
		t.Fatalf("HTTPAddr() = %q, want the OnLoad-reported address", got)
	}
	// The config must have been delivered as the OnLoad invoke payload.
	resp, err := invoke(context.Background(), "onloadpayload", nil)
	if err != nil {
		t.Fatalf("invoke onloadpayload: %v", err)
	}
	var echo struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(resp, &echo); err != nil {
		t.Fatalf("decode onloadpayload resp: %v", err)
	}
	if echo.Payload != string(cfg) {
		t.Fatalf("OnLoad payload = %q, want the pushed config %q", echo.Payload, cfg)
	}
}

// TestProcessOpenerOnLoadWithoutConfigReportsNoAddr pins the backward-
// compatible path: no config pushed → OnLoad payload empty, no address
// reported, Open succeeds.
func TestProcessOpenerOnLoadWithoutConfigReportsNoAddr(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	abi := gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}
	op := &processOpener{
		dispatch: (&eventRecorder{}).dispatch,
		env:      append(os.Environ(), testPluginEnv+"=1"),
	}
	_, _, closer, httpAddr, err := op.Open(abi, exe, "", "test.plugin", nil, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = closer() }()
	if httpAddr != "" {
		t.Fatalf("Open httpAddr = %q, want empty for a config-less load", httpAddr)
	}
	if got := op.HTTPAddr(); got != "" {
		t.Fatalf("HTTPAddr() = %q, want empty for a config-less load", got)
	}
}
