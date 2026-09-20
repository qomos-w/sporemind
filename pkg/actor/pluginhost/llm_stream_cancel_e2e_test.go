package pluginhost

// llm_stream_cancel_e2e_test.go pins the full cancellation chain end-to-end:
// the real SDK hello binary's ask_cancel callable starts an InvokeStreamCtx,
// its consumer ctx expires, the SDK emits a 0x09 reverse-cancel frame, the
// host frameSession fires the dispatch's cancellation root (exposed here as
// the fake dispatchStream's dctx.Parent), and the upstream "LLM stream"
// unwinds — the cost-leak fix: no host-side dispatch runs to completion on a
// stream nobody consumes.

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// blockingStreamDispatch is a host-side streaming route that emits one chunk,
// then parks on dctx.Parent until the plugin cancels (0x09) or the test
// times out. observedCancel flips when the cancellation root fires — the
// assertion target of the e2e.
func blockingStreamDispatch(observed *atomic.Bool) HostDispatchStreamFunc {
	return func(dctx DispatchContext, callID string, req []byte, onChunk func([]byte) error) ([]byte, error) {
		if err := onChunk([]byte(`{"kind":"text_delta","data":{"text":"hel"}}`)); err != nil {
			return nil, err
		}
		if dctx.Parent == nil {
			return nil, fmt.Errorf("streaming dispatch received no cancellation parent (0x09 chain broken)")
		}
		<-dctx.Parent.Done()
		observed.Store(true)
		return nil, fmt.Errorf("upstream aborted: %w", dctx.Parent.Err())
	}
}

// TestSubprocessLLMStreamCancelEndToEnd drives the real subprocess stack:
// hello.ask_cancel → InvokeStreamCtx(ctx 500ms) → 0x03 stream req → host
// dispatch parks (no terminal) → plugin ctx expires → 0x09 reverse-cancel →
// host fires the dispatch's cancel → upstream unwinds → plugin returns
// cancelled:true. Without the 0x09 chain the invoke would hang until the
// outer invoke budget (30s) killed it.
func TestSubprocessLLMStreamCancelEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	if testing.Short() {
		t.Skip("e2e builds the real SDK hello binary")
	}
	exe := buildHelloSubprocessForE2E(t)

	var observed atomic.Bool
	terminal := []byte(`{"Text":"hello!"}`)
	unary := func(callID string, req []byte) ([]byte, error) { return terminal, nil }
	loader, host, _, pop := newSubprocessE2ELoader(t, unary, nil)
	pop.dispatchStream = blockingStreamDispatch(&observed)

	loadHello(t, loader, exe)

	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "ask_cancel"))
	if !ok {
		t.Fatal("ask_cancel handler not registered (OnLoad did not run?)")
	}
	req, _ := json.Marshal(map[string]string{"prompt": "cancel me"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	resp, err := h(ctx, req)
	if err != nil {
		t.Fatalf("invoke ask_cancel: %v", err)
	}
	var out struct {
		Cancelled bool   `json:"cancelled"`
		Err       string `json:"err"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal ask_cancel response %q: %v", resp, err)
	}
	if !out.Cancelled {
		t.Fatalf("cancelled = false, want ctx expiry to abort the stream (err %q)", out.Err)
	}
	// The plugin's 500ms consumer ctx bounds the whole round trip; the 0x09
	// propagation must not add visible latency (the legacy behavior would
	// park on the terminal until the outer budget).
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancel propagation took %v; the 0x09 chain did not unwind the host dispatch", elapsed)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !observed.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !observed.Load() {
		t.Fatal("host-side dispatch never observed cancellation (0x09 → dctx.Parent chain broken)")
	}
}
