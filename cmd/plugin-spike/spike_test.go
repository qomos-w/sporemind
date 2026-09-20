package main

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
)

// TestMain re-executes this binary as the plugin process when the env var is
// set; otherwise it runs the tests. The spawned child is therefore a real,
// separate OS process speaking the framing protocol over stdin/stdout pipes.
func TestMain(m *testing.M) {
	if os.Getenv(pluginProcessEnv) == "1" {
		code := 0
		if err := pluginMainLoop(bufio.NewReader(os.Stdin), os.Stdout); err != nil && !errors.Is(err, io.EOF) {
			_ = writeFrame(os.Stdout, msgError, []byte(err.Error()))
			code = 1
		}
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// helloAllowed is the granted capability set for the spike runs (the role of
// EffectiveCapabilities computed by the host SecurityPolicy). app.state is
// deliberately absent so the denial path can be exercised.
func helloAllowed() map[string]struct{} {
	return map[string]struct{}{"llm.invoke": {}, "fs.read": {}}
}

// helloDispatch is the host-side backing service for reverse calls, the role
// NewServiceDispatch plays in production (pkg/actor/pluginhost/host_bridge.go).
func helloDispatch(callID string, req []byte) ([]byte, error) {
	switch callID {
	case "llm.complete":
		var m map[string]any
		if err := json.Unmarshal(req, &m); err != nil {
			return nil, err
		}
		if big, _ := m["big"].(bool); big {
			return json.Marshal(map[string]any{"blob": strings.Repeat("x", 1<<20)})
		}
		return json.Marshal(map[string]any{"result": "LLM replied", "echo_prompt": m["prompt"]})
	case "llm.chat":
		return json.Marshal(map[string]any{"result": "chat ok"})
	case "project.read_file":
		var m struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(req, &m); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"content": "file content of " + m.Path})
	default:
		return nil, fmt.Errorf("unknown reverse call %q", callID)
	}
}

func newTestTransport(t *testing.T, allowed map[string]struct{}, dispatch reverseDispatchFunc) *processTransport {
	t.Helper()
	tr, err := newProcessTransport(allowed, dispatch)
	if err != nil {
		t.Fatalf("spawn plugin process: %v", err)
	}
	t.Cleanup(tr.kill) // safety net: no plugin process outlives a test
	return tr
}

func invoke(t *testing.T, tr *processTransport, callable string, request any, requestID string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	resp, err := tr.invoke(ctx, callable, request, requestID)
	if err != nil {
		t.Fatalf("invoke %s: %v", callable, err)
	}
	return resp
}

// assertOrder checks that events contains want elements in order (ignoring
// unrelated prefix/suffix entries such as "spawned").
func assertOrder(t *testing.T, events []string, want ...string) {
	t.Helper()
	pos := 0
	for _, ev := range events {
		if pos < len(want) && ev == want[pos] {
			pos++
		}
	}
	if pos != len(want) {
		t.Fatalf("event trace %v does not contain %v in order", events, want)
	}
}

// TestForwardRoundTrip is the baseline: one invoke-req, one invoke-resp, no
// reverse traffic. Also verifies the plugin's log output arrives as a 0x05
// log frame before the invoke-resp, and that several sequential invokes plus
// a graceful unload (stdin EOF -> child exit 0) work on one process.
func TestForwardRoundTrip(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	resp := invoke(t, tr, "greet", greetReq{Name: "world"}, "r1")
	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode greet resp %q: %v", resp, err)
	}
	if got.Message != "Hello, world!" {
		t.Fatalf("greet message = %q, want %q", got.Message, "Hello, world!")
	}
	assertOrder(t, tr.events(), "invoke-req greet", "log", "invoke-resp")

	resp = invoke(t, tr, "ping", map[string]any{}, "r2")
	if string(resp) != `{"pong":"ok"}` {
		t.Fatalf("ping resp = %s", resp)
	}

	if err := tr.closeGracefully(); err != nil {
		t.Fatalf("graceful close: %v", err)
	}
}

// TestReverseCallRoundTrip is the gating gate: while the host is waiting for
// invoke-resp it receives an interleaved reverse-req, dispatches it through
// the capability gate, writes reverse-resp, and then gets the invoke-resp.
// A deadlock in the synchronous duplex framing would hang here.
func TestReverseCallRoundTrip(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	resp := invoke(t, tr, "greet", greetReq{
		Name:    "world",
		Reverse: &reverseReq{CallID: "llm.complete", Payload: json.RawMessage(`{"prompt":"hi"}`)},
	}, "r1")

	var got struct {
		Message       string `json:"message"`
		ReverseResult struct {
			Result     string `json:"result"`
			EchoPrompt string `json:"echo_prompt"`
		} `json:"reverse_result"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode greet resp %q: %v", resp, err)
	}
	if got.Message != "Hello, world!" {
		t.Fatalf("greet message = %q", got.Message)
	}
	if got.ReverseResult.Result != "LLM replied" || got.ReverseResult.EchoPrompt != "hi" {
		t.Fatalf("reverse_result = %+v, want LLM replied/hi", got.ReverseResult)
	}

	// Full interleave order: invoke-req -> log -> reverse-req -> reverse-resp
	// -> invoke-resp.
	assertOrder(t, tr.events(),
		"invoke-req greet", "log",
		"reverse-req llm.complete", "reverse-resp",
		"invoke-resp",
	)
}

// TestReverseCallCapabilityDenied verifies the per-plugin capability gate on
// the IPC path: a reverse call whose capability is not granted gets a
// {"__host_error__": ...} reverse-resp, the plugin surfaces it as a Go error,
// and the invoke fails with that error inside the invoke-resp envelope.
func TestReverseCallCapabilityDenied(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch) // app.state NOT granted

	resp := invoke(t, tr, "greet", greetReq{
		Name:    "x",
		Reverse: &reverseReq{CallID: "state.get", Payload: json.RawMessage(`{"key":"k"}`)},
	}, "r1")

	var got struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode error resp %q: %v", resp, err)
	}
	if !strings.Contains(got.Error, "capability not granted") {
		t.Fatalf("error = %q, want capability denial", got.Error)
	}
	assertOrder(t, tr.events(), "invoke-req greet", "reverse-req state.get", "reverse-deny", "invoke-resp")

	// An unknown callID is denied too (callIDToCapability maps it to "").
	resp = invoke(t, tr, "greet", greetReq{
		Name:    "x",
		Reverse: &reverseReq{CallID: "hax.evil", Payload: json.RawMessage(`{}`)},
	}, "r2")
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode error resp %q: %v", resp, err)
	}
	if !strings.Contains(got.Error, "capability not granted") {
		t.Fatalf("error = %q, want capability denial", got.Error)
	}
}

// TestLargePayloadsNoDeadlock pushes frames far beyond the default pipe
// buffer (64 KiB on Linux, 4 KiB on Windows) in both directions, forward and
// reverse. If the synchronous framing had a write/write deadlock, this test
// would hang until the per-invoke timeout kills the plugin and fails.
func TestLargePayloadsNoDeadlock(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	// invoke-req and invoke-resp both ~1 MiB: echo round trip.
	big := strings.Repeat("a", 1<<20)
	resp := invoke(t, tr, "echo", map[string]any{"data": big}, "r1")
	var got struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode echo resp: %v", err)
	}
	if got.Data != big {
		t.Fatalf("1 MiB echo corrupted: got %d bytes, want %d", len(got.Data), len(big))
	}

	// reverse-resp and invoke-resp both ~1 MiB: reverse call whose dispatch
	// returns a large blob, embedded into the greet response.
	resp = invoke(t, tr, "greet", greetReq{
		Name:    "big",
		Reverse: &reverseReq{CallID: "llm.complete", Payload: json.RawMessage(`{"prompt":"big","big":true}`)},
	}, "r2")
	var got2 struct {
		ReverseResult struct {
			Blob string `json:"blob"`
		} `json:"reverse_result"`
	}
	if err := json.Unmarshal(resp, &got2); err != nil {
		t.Fatalf("decode greet resp: %v", err)
	}
	if len(got2.ReverseResult.Blob) != 1<<20 {
		t.Fatalf("reverse blob = %d bytes, want %d", len(got2.ReverseResult.Blob), 1<<20)
	}
}

// TestConcurrentInvokesSerialized runs many invokes (each with a reverse
// call) from concurrent host goroutines. The transport serializes them with
// the per-process invoke mutex — the design's "初版串行化" — so no request-id
// multiplexing is needed in the frame header. Cross-talk (wrong response for
// the caller) or a hang would indicate the serialization is insufficient.
func TestConcurrentInvokesSerialized(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	const workers = 8
	const perWorker = 10
	var wg sync.WaitGroup
	errCh := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				name := fmt.Sprintf("w%d-%d", w, i)
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				resp, err := tr.invoke(ctx, "greet", greetReq{
					Name:    name,
					Reverse: &reverseReq{CallID: "llm.complete", Payload: json.RawMessage(`{"prompt":"` + name + `"}`)},
				}, name)
				cancel()
				if err != nil {
					errCh <- fmt.Errorf("%s: %w", name, err)
					return
				}
				var got struct {
					Message       string `json:"message"`
					ReverseResult struct {
						EchoPrompt string `json:"echo_prompt"`
					} `json:"reverse_result"`
				}
				if err := json.Unmarshal(resp, &got); err != nil {
					errCh <- fmt.Errorf("%s: decode: %w", name, err)
					return
				}
				if got.Message != "Hello, "+name+"!" || got.ReverseResult.EchoPrompt != name {
					errCh <- fmt.Errorf("%s: cross-talk: message=%q echo=%q (serialization violated?)", name, got.Message, got.ReverseResult.EchoPrompt)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent invoke error: %v", err)
	}

	if err := tr.closeGracefully(); err != nil {
		t.Fatalf("graceful close after concurrent invokes: %v", err)
	}
}

// TestPluginCrashDetectedMidInvoke verifies crash isolation: killing the
// plugin process while an invoke is in flight makes the host's blocked read
// return immediately (pipe EOF) instead of hanging forever, and the invoke
// surfaces as an error — the EOF-on-kill detection the design requires.
func TestPluginCrashDetectedMidInvoke(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resCh := make(chan error, 1)
	go func() {
		_, err := tr.invoke(ctx, "stall", map[string]any{}, "r1")
		resCh <- err
	}()

	time.Sleep(300 * time.Millisecond) // let the invoke-req land and stall
	tr.kill()                          // simulated plugin crash

	select {
	case err := <-resCh:
		if err == nil {
			t.Fatalf("invoke on killed plugin unexpectedly succeeded")
		}
		t.Logf("crash surfaced as: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("invoke did not unblock after plugin death (deadlock in EOF handling)")
	}
}

// TestInvokeTimeoutKillsPlugin verifies the "超时即 kill+respawn" path: when
// the plugin does not answer within the invoke budget, the host kills the
// process (unblocking the read via pipe EOF) and returns a timeout error with
// no leaked child.
func TestInvokeTimeoutKillsPlugin(t *testing.T) {
	tr := newTestTransport(t, helloAllowed(), helloDispatch)

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	start := time.Now()
	_, err := tr.invoke(ctx, "stall", map[string]any{}, "r1")
	cancel()
	if err == nil {
		t.Fatalf("stall invoke should time out")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") || !strings.Contains(err.Error(), "plugin killed") {
		t.Fatalf("timeout error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout path too slow: %v", elapsed)
	}

	// The killed process must be reaped; a fresh spawn must work afterwards
	// (kill+respawn feasibility).
	tr2 := newTestTransport(t, helloAllowed(), helloDispatch)
	resp := invoke(t, tr2, "ping", map[string]any{}, "r1")
	if string(resp) != `{"pong":"ok"}` {
		t.Fatalf("respawn ping resp = %s", resp)
	}
}
