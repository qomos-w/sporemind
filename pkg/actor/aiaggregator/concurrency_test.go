package aiaggregator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ── ProviderGate tests ──

func TestProviderGate_AcquireRelease(t *testing.T) {
	g := llmclient.NewProviderGate()
	g.Register("p", 3)

	r1, err := g.Acquire(context.Background(), "p")
	if err != nil {
		t.Fatalf("Acquire #1: %v", err)
	}
	r2, err := g.Acquire(context.Background(), "p")
	if err != nil {
		t.Fatalf("Acquire #2: %v", err)
	}
	r3, err := g.Acquire(context.Background(), "p")
	if err != nil {
		t.Fatalf("Acquire #3: %v", err)
	}

	// 4th acquire should block; use a short-timeout context to verify.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = g.Acquire(ctx, "p")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded at capacity, got %v", err)
	}

	// Release one and acquire again — should succeed immediately.
	r1()
	r4, err := g.Acquire(context.Background(), "p")
	if err != nil {
		t.Fatalf("Acquire after release: %v", err)
	}
	r2()
	r3()
	r4()
}

func TestProviderGate_Snapshot(t *testing.T) {
	g := llmclient.NewProviderGate()
	g.Register("p1", 5)
	g.Register("p2", 3)

	r1, _ := g.Acquire(context.Background(), "p1")
	r2, _ := g.Acquire(context.Background(), "p1")
	_, _ = g.Acquire(context.Background(), "p2")

	snap := g.Snapshot()
	if snap["p1"].Inflight != 2 || snap["p1"].Max != 5 {
		t.Fatalf("p1 = %+v, want {Inflight:2 Max:5}", snap["p1"])
	}
	if snap["p2"].Inflight != 1 || snap["p2"].Max != 3 {
		t.Fatalf("p2 = %+v, want {Inflight:1 Max:3}", snap["p2"])
	}

	r1()
	r2()
	snap = g.Snapshot()
	if snap["p1"].Inflight != 0 {
		t.Fatalf("p1 inflight after release = %d, want 0", snap["p1"].Inflight)
	}
}

func TestProviderGate_UnregisteredProviderIsNoOp(t *testing.T) {
	g := llmclient.NewProviderGate()
	rel, err := g.Acquire(context.Background(), "unknown")
	if err != nil {
		t.Fatalf("unregistered provider should not error, got %v", err)
	}
	rel() // should not panic
	snap := g.Snapshot()
	if _, ok := snap["unknown"]; ok {
		t.Fatal("unregistered provider should not appear in snapshot")
	}
}

func TestProviderGate_RegisterZeroRemovesEntry(t *testing.T) {
	g := llmclient.NewProviderGate()
	g.Register("p", 2)
	g.Register("p", 0) // remove
	snap := g.Snapshot()
	if _, ok := snap["p"]; ok {
		t.Fatal("provider should be removed when maxConcurrency=0")
	}
}

func TestProviderGate_ConcurrentAcquireReleaseRaceFree(t *testing.T) {
	g := llmclient.NewProviderGate()
	g.Register("p", 10)
	const goroutines = 50
	const perG = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for gr := 0; gr < goroutines; gr++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				rel, err := g.Acquire(context.Background(), "p")
				if err != nil {
					continue
				}
				rel()
			}
		}()
	}
	wg.Wait()
	snap := g.Snapshot()
	if snap["p"].Inflight != 0 {
		t.Fatalf("inflight after balanced concurrent ops = %d, want 0", snap["p"].Inflight)
	}
}

func TestProviderGate_AcquireContextCancellation(t *testing.T) {
	g := llmclient.NewProviderGate()
	g.Register("p", 1)
	r1, _ := g.Acquire(context.Background(), "p")
	defer r1()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := g.Acquire(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// ── selectUnit: token-plan hard filter (concurrency check removed) ──

func TestSelectUnit_TokenPlanExhaustedSkipsUnit(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "a::m", Model: "m", ProviderName: "a", IsTokenPlan: true, TokenPlanRemainingPct: 0},
		},
		strategy: NewFallbackStrategy(),
	}
	_, err := a.selectUnit(SelectRequest{})
	if !errors.Is(err, errUnitsTokenPlanExhausted) {
		t.Fatalf("expected errUnitsTokenPlanExhausted, got %v", err)
	}
}

func TestSelectUnit_TokenPlanExpiredSkipsUnit(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "a::m", Model: "m", ProviderName: "a",
				IsTokenPlan: true, TokenPlanRemainingPct: 50,
				TokenPlanExpiresAt: "2000-01-01T00:00:00Z"},
		},
		strategy: NewFallbackStrategy(),
	}
	_, err := a.selectUnit(SelectRequest{})
	if !errors.Is(err, errUnitsTokenPlanExhausted) {
		t.Fatalf("expected errUnitsTokenPlanExhausted for expired plan, got %v", err)
	}
}

func TestSelectUnit_TokenPlanHealthyRoutesToSpare(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "a::m", Model: "m", ProviderName: "a", IsTokenPlan: true, TokenPlanRemainingPct: 0},
			{ID: "b::m", Model: "m", ProviderName: "b", IsTokenPlan: true, TokenPlanRemainingPct: 80},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if chosen.ID != "b::m" {
		t.Fatalf("expected b::m (a::m exhausted), got %q", chosen.ID)
	}
}

func TestSelectUnit_TokenPlanNotPlanIgnored(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "a::m", Model: "m", ProviderName: "a", IsTokenPlan: false, TokenPlanRemainingPct: 0},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if chosen.ID != "a::m" {
		t.Fatalf("expected a::m (non-plan unit not filtered), got %q", chosen.ID)
	}
}

func TestSelectUnit_UnlimitedConcurrencyNeverRejected(t *testing.T) {
	a := &Actor{
		units: []CallableUnit{
			{ID: "a::m", Model: "m", MaxConcurrency: 0},
		},
		strategy: NewRoundRobinStrategy(),
	}
	for i := 0; i < 5; i++ {
		if _, err := a.selectUnit(SelectRequest{}); err != nil {
			t.Fatalf("selectUnit #%d: %v (unlimited unit must never be rejected)", i, err)
		}
	}
}

// ── handler-level: ProviderGate integration ──

type fakeClient struct {
	events []llmclient.Event
}

func (c *fakeClient) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	ch := make(chan llmclient.Event, len(c.events))
	for _, e := range c.events {
		ch <- e
	}
	close(ch)
	return &fakeStream{ch: ch}, nil
}

type fakeStream struct {
	ch  <-chan llmclient.Event
	tel llmclient.RequestTelemetry
}

func (s *fakeStream) Events() <-chan llmclient.Event        { return s.ch }
func (s *fakeStream) Close() error                          { return nil }
func (s *fakeStream) Telemetry() llmclient.RequestTelemetry { return s.tel }

func newFakeRegistry(events []llmclient.Event) llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fake",
		Factory: func(endpoint, authToken string) llmclient.Client {
			return &fakeClient{events: events}
		},
	})
	return *reg
}

func TestHandleProbeTokens_GateReleaseOnSuccess(t *testing.T) {
	gate := llmclient.NewProviderGate()
	gate.Register("a", 1)
	a := &Actor{
		id:       "test-agg",
		units:    []CallableUnit{{ID: "a::m", Model: "m", Protocol: "fake", ProviderName: "a"}},
		strategy: NewFallbackStrategy(),
		registry: newFakeRegistry([]llmclient.Event{{Kind: llmclient.EventUsage, Usage: &llmclient.Usage{InputTokens: 42}}}),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}
	// Temporarily swap the default gate for the test.
	orig := llmclient.DefaultProviderGate
	llmclient.DefaultProviderGate = gate
	defer func() { llmclient.DefaultProviderGate = orig }()

	resp, err := a.handleProbeTokens(nil, domain.SendSessionMessageReq{})
	if err != nil {
		t.Fatalf("handleProbeTokens: %v", err)
	}
	if resp.Usage.InputTokens != 42 {
		t.Errorf("usage input tokens = %d, want 42", resp.Usage.InputTokens)
	}
	// Gate should be fully released after the handler returns.
	snap := gate.Snapshot()
	if snap["a"].Inflight != 0 {
		t.Fatalf("gate inflight after success = %d, want 0", snap["a"].Inflight)
	}
}

func TestHandleProbeTokens_GateReleaseOnError(t *testing.T) {
	gate := llmclient.NewProviderGate()
	gate.Register("a", 1)
	a := &Actor{
		id:       "test-agg",
		units:    []CallableUnit{{ID: "a::m", Model: "m", Protocol: "fake", ProviderName: "a"}},
		strategy: NewFallbackStrategy(),
		registry: newFakeRegistry([]llmclient.Event{{Kind: llmclient.EventError, Err: errors.New("boom")}}),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
		}},
		lifecycleCtx: context.Background(),
	}
	orig := llmclient.DefaultProviderGate
	llmclient.DefaultProviderGate = gate
	defer func() { llmclient.DefaultProviderGate = orig }()

	if _, err := a.handleProbeTokens(nil, domain.SendSessionMessageReq{}); err == nil {
		t.Fatal("expected error from error-event stream")
	}
	snap := gate.Snapshot()
	if snap["a"].Inflight != 0 {
		t.Fatalf("gate inflight after error = %d, want 0", snap["a"].Inflight)
	}
}
