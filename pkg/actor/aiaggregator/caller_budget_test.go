package aiaggregator

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ════════════════════════════════════════════════════════════════════════════
// Caller-budget rotation gate — the stream-open candidate loop runs off the
// actor lifecycle context; when the caller's budget dies mid-grind (outer
// invoke deadline exhausted), rotation must stop with the last unit error
// instead of continuing to sweep dead units (observed live: stream-open
// failures logging 7s after the reverse llm call's deadline exceeded).
// ════════════════════════════════════════════════════════════════════════════

// cancelingClient fails the stream open with a rotatable 429 and cancels the
// caller context on the first call — the live failure shape: the outer
// deadline fires while the failover loop is still rotating.
type cancelingClient struct {
	cancel context.CancelFunc
	calls  *atomic.Int32
}

func (c *cancelingClient) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	if c.calls.Add(1) == 1 {
		c.cancel()
	}
	return nil, &llmclient.UpstreamError{StatusCode: 429, Message: "rate limit exceeded"}
}

func TestDispatchStopsRotatingWhenCallerBudgetDies(t *testing.T) {
	resetGlobalHealth(t)
	calls := &atomic.Int32{}
	ctxDone := make(chan struct{})
	cancel := func() { close(ctxDone) }

	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail429Cancel",
		Factory: func(_, _ string) llmclient.Client {
			return &cancelingClient{cancel: cancel, calls: calls}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory: func(_, _ string) llmclient.Client {
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventTextDelta, Text: "ok"},
				{Kind: llmclient.EventStop},
			}}
		},
	})

	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail429Cancel"}, {ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"}},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	ctx := &testPureCtx{done: ctxDone}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("dispatch must fail once the caller budget dies")
	}
	if !strings.Contains(err.Error(), "caller budget exhausted") {
		t.Fatalf("error must name the caller budget: %v", err)
	}
	var upstream *llmclient.UpstreamError
	if !errors.As(err, &upstream) || upstream.StatusCode != 429 {
		t.Fatalf("error must chain the last unit 429, got: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("rotation must stop after the failed unit (1 stream-open attempt), got %d", got)
	}
	// The healthy unit b must never have been dialed: the gate stops rotation,
	// and the "ok" protocol factory has no observable counter — the 429 count
	// above plus the error text carry the assertion.
}
