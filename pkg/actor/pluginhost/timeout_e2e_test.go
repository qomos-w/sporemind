package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/gospore/invoke"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/appdef"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestTimeout60sCallableWithSlowLLMFirstChunk is the T6 end-to-end regression
// pin: an appdef callable declaring timeout: 60s keeps a reverse llm.complete
// call alive when the first aggregator chunk arrives at 35s -- past the old 30s
// host-bridge cap but below the 60s budget.
//
// This test intentionally waits the real 35s once; it is isolated to the
// pluginhost package and exercises the chain from appdef parse -> descriptor
// TimeoutMs -> pluginhost.handleInvoke -> context deadline -> HostBridge
// __DeadlineAt injection -> handleHostBridgeLLMInvoke derived budget -> slow
// aggregator first chunk success.
//
// appmanager forwarding of TimeoutMs is covered by
// pkg/actor/appmanager/native_invoke_test.go; codegen appdef->descriptor is
// covered by pkg/codegen/generate_test.go. This test closes the runtime loop.
func TestTimeout60sCallableWithSlowLLMFirstChunk(t *testing.T) {
	if testing.Short() {
		t.Skip("35s end-to-end regression test skipped in short mode")
	}

	// 1. appdef declares a 60s callable.
	src := `app SlowLLM {
		id: "app.slowllm"
		name: "Slow LLM"
		version: "0.1.0"
		namespace: "slowllm"

		callable slow_complete {
			timeout: 60s
		}
	}`
	ast, diags, err := appdef.ParseFile(src)
	if err != nil {
		t.Fatalf("appdef parse: %v", err)
	}
	if len(diags) > 0 {
		t.Fatalf("appdef diagnostics: %v", diags)
	}
	if len(ast.Callables) != 1 || ast.Callables[0].TimeoutMs != 60_000 {
		t.Fatalf("expected TimeoutMs=60000, got callable=%+v", ast.Callables[0])
	}

	// 2. Wire a pluginhost actor with a 60s-budget callable that calls
	//    llm.complete through the host bridge.
	a := &Actor{}
	if _, err := a.handleRegisterActor(nil, registerActorReq{
		PluginID:  "app.slowllm",
		Namespace: "slowllm",
		CallIDs:   []string{"plugin.app.slowllm.slow_complete"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// 3. The aggregator mock yields its first chunk after 35s -- over the
	//    legacy 30s hostBridgeInvokeTimeout cap.
	stream := &delayedStream{
		delay: 35 * time.Second,
		fakeStream: fakeStream{
			values: []any{
				domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "ok"},
			},
		},
	}
	aggRef := &fakeRef{call: invoke.NewCall(invoke.CallModeStream, stream)}

	// 4. HostBridge mirrors what the SDK would do: inject __DeadlineAt from the
	//    outer invoke context, then route through the stream-catalog dispatch.
	hb := NewHostBridge(
		map[string]struct{}{appbinding.CapLLMInvoke: {}},
		func(callID string, req []byte) ([]byte, error) {
			route, ok := appbinding.LookupStreamRoute(callID)
			if !ok {
				return nil, fmt.Errorf("no stream route for %q", callID)
			}
			return a.handleHostBridgeStream(DispatchContext{}, aggRef, route, callID, req, nil)
		},
		"app.slowllm",
		nil,
	)

	a.RegisterHandler("plugin.app.slowllm.slow_complete", func(ctx context.Context, req []byte) ([]byte, error) {
		dctx := DispatchContext{AgentID: "test-agent"}
		if deadline, ok := ctx.Deadline(); ok {
			dctx.DeadlineAt = deadline.UnixMilli()
		}
		llmReq := []byte(`{"prompt":"hi","model":"gpt-4o","provider":"openai"}`)
		return hb.DispatchContext(dctx, "llm.complete", llmReq)
	})

	// 5. Invoke with the descriptor timeout.
	start := time.Now()
	resp, err := a.handleInvoke(nil, gen.PluginInvokeReq{
		ID:        "app.slowllm",
		Callable:  "slow_complete",
		Payload:   []byte(`{}`),
		TimeoutMs: 60_000,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}

	// 6. The slow first chunk must have been delivered, so elapsed is past 30s
	//    and safely inside the 60s budget (with the standard reverse headroom).
	if elapsed < 35*time.Second {
		t.Fatalf("elapsed = %v, expected at least 35s to prove the slow first chunk ran", elapsed)
	}
	if elapsed > 55*time.Second {
		t.Fatalf("elapsed = %v, expected to finish well before the 60s budget", elapsed)
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Payload, &got); err != nil {
		t.Fatalf("unmarshal resp: %v", err)
	}
	if got["Text"] != "ok" {
		t.Fatalf("Text = %q, want ok", got["Text"])
	}
}
