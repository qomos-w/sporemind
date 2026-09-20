package pluginhost

// llm_stream_e2e_test.go drives the full subprocess stack (real SDK hello
// binary → 0x03 stream:req → frameSession → HostBridge → dispatchStream →
// 0x07 chunks → SDK stream waiter → onChunk) for the native-llm-stream S4
// acceptance: mock aggregator streaming blocks forwarded as 0x07 frames,
// SDK side onChunk receives ordered intermediate chunks + terminal
// aggregation correct; and the bidirectional compatibility mirror — a
// new-plugin (stream:true) against an old-host (no dispatchStream) degrades
// to zero chunks + intact terminal.

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// fakeStreamDispatch is the host-side streaming llm.complete route: it
// delivers scripted wire chunks (the generic envelope {kind,data} shape) then
// the terminal aggregated response. Non-llm callIDs degrade to unary.
func fakeStreamDispatch(chunks [][]byte, terminal []byte) HostDispatchStreamFunc {
	return func(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
		for _, c := range chunks {
			if err := onChunk(c); err != nil {
				return nil, err
			}
		}
		return terminal, nil
	}
}

// TestSubprocessLLMStreamEndToEnd pins S4 acceptance #4 end-to-end: the real
// SDK hello binary's "ask" callable calls LLM().CompleteStream; the host's
// dispatchStream delivers scripted 0x07 chunks; the SDK stream waiter
// forwards them to onChunk (deltas counted), and the terminal 0x04 carries
// the aggregated response.
func TestSubprocessLLMStreamEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	if testing.Short() {
		t.Skip("e2e builds the real SDK hello binary")
	}
	exe := buildHelloSubprocessForE2E(t)

	chunks := [][]byte{
		[]byte(`{"kind":"text_delta","data":{"text":"hel"}}`),
		[]byte(`{"kind":"text_delta","data":{"text":"lo"}}`),
		[]byte(`{"kind":"text_delta","data":{"text":"!"}}`),
		[]byte(`{"kind":"usage","data":{"InputTokens":2,"OutputTokens":3}}`),
	}
	terminal := []byte(`{"Text":"hello!"}`)

	// Unary route (for non-llm callables, never reached here) returns the
	// terminal too so any stray reverse call does not error.
	unary := func(callID string, req []byte) ([]byte, error) { return terminal, nil }
	loader, host, _, pop := newSubprocessE2ELoader(t, unary, nil)
	pop.dispatchStream = fakeStreamDispatch(chunks, terminal)

	loadHello(t, loader, exe)

	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "ask"))
	if !ok {
		t.Fatal("ask handler not registered (OnLoad did not run?)")
	}
	req, _ := json.Marshal(map[string]string{"prompt": "stream me"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := h(ctx, req)
	if err != nil {
		t.Fatalf("invoke ask: %v", err)
	}
	var out struct {
		Answer struct {
			Text string `json:"Text"`
		} `json:"answer"`
		Deltas int `json:"deltas"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal ask response %q: %v", resp, err)
	}
	if out.Deltas != 3 {
		t.Errorf("deltas = %d, want 3 (only text_delta chunks count)", out.Deltas)
	}
	if out.Answer.Text != "hello!" {
		t.Errorf("answer.Text = %q, want %q", out.Answer.Text, "hello!")
	}
}

// TestSubprocessLLMStreamOldHostDegrades pins the new-plugin / old-host
// compatibility: a host WITHOUT a dispatchStream (the pre-streaming host
// contract) still answers a stream:true reverse-req with its terminal 0x04 —
// the SDK's InvokeStream receives zero chunks and the aggregated value, no
// error. This is the wire-level mirror of the old-plugin / new-host path
// covered by TestFrameSessionReverseStreamWithoutFlagStaysUnary.
func TestSubprocessLLMStreamOldHostDegrades(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	if testing.Short() {
		t.Skip("e2e builds the real SDK hello binary")
	}
	exe := buildHelloSubprocessForE2E(t)

	// No dispatchStream: the HostBridge falls back to unary DispatchContext
	// for llm.* (DispatchContextStream returns b.DispatchContext when
	// dispatchStream == nil). One terminal response, zero chunks.
	terminal := []byte(`{"Text":"hello!"}`)
	unary := func(callID string, req []byte) ([]byte, error) { return terminal, nil }
	loader, host, _, _ := newSubprocessE2ELoader(t, unary, nil)

	loadHello(t, loader, exe)

	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "ask"))
	if !ok {
		t.Fatal("ask handler not registered")
	}
	req, _ := json.Marshal(map[string]string{"prompt": "degrade me"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := h(ctx, req)
	if err != nil {
		t.Fatalf("invoke ask (old host): %v", err)
	}
	var out struct {
		Answer struct {
			Text string `json:"Text"`
		} `json:"answer"`
		Deltas int `json:"deltas"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal ask response %q: %v", resp, err)
	}
	if out.Deltas != 0 {
		t.Errorf("deltas = %d, want 0 on old host (no streaming, no chunks)", out.Deltas)
	}
	if out.Answer.Text != "hello!" {
		t.Errorf("answer.Text = %q, want intact terminal %q", out.Answer.Text, "hello!")
	}
}
