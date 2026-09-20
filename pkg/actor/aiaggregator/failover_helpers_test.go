package aiaggregator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	gosporeactor "github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// noopLogger implements actor.Logger and discards all output.
type noopLogger struct{}

func (noopLogger) Debug(string, ...any) {}
func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

// testPureCtx is a minimal actor.PureContext stub. handleDispatch only calls
// Logger() and Done() on the context; the nil-embedded PureContext satisfies the
// interface for any unneeded methods.
type testPureCtx struct {
	gosporeactor.PureContext
	done chan struct{}
}

func (c *testPureCtx) Logger() gosporeactor.Logger { return noopLogger{} }
func (c *testPureCtx) Done() <-chan struct{}       { return c.done }

// collectingEmitter is a no-op actor.Emitter that records ResolvedUnit chunks.
type collectingEmitter struct {
	done   chan struct{}
	mu     sync.Mutex
	chunks []domain.AggregatorChunk
}

func (e *collectingEmitter) Send(chunk any) error {
	if c, ok := chunk.(domain.AggregatorChunk); ok {
		e.mu.Lock()
		e.chunks = append(e.chunks, c)
		e.mu.Unlock()
	}
	return nil
}
func (e *collectingEmitter) Done() <-chan struct{} { return e.done }

func (e *collectingEmitter) resolvedUnit() *domain.ModelUnit {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.chunks {
		if e.chunks[i].Kind == domain.AggregatorChunkResolvedUnit {
			return e.chunks[i].ResolvedUnit
		}
	}
	return nil
}

// errorClient is an llmclient.Client whose Stream always returns streamErr.
type errorClient struct{ streamErr error }

func (c *errorClient) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	return nil, c.streamErr
}

// TestUnitFailureSuffix verifies dispatch failure errors carry the serving
// unit identity (provider/model/endpoint) so retry diagnostics can attribute
// a stalled stream to a concrete endpoint. Nested aggregator refs name the
// child aggregator instead — the child's own error carries the deep unit.
func TestUnitFailureSuffix(t *testing.T) {
	if got := unitFailureSuffix(nil); got != "" {
		t.Fatalf("nil unit suffix = %q, want empty", got)
	}
	got := unitFailureSuffix(&CallableUnit{
		ProviderName: "kimi-2",
		Model:        "k3",
		Endpoint:     "https://api.example.com/v1",
	})
	want := " [provider=kimi-2 model=k3 endpoint=https://api.example.com/v1]"
	if got != want {
		t.Fatalf("direct unit suffix = %q, want %q", got, want)
	}
	got = unitFailureSuffix(&CallableUnit{AggregatorID: "agg-child"})
	want = " [aggregator=agg-child]"
	if got != want {
		t.Fatalf("nested unit suffix = %q, want %q", got, want)
	}

	// The suffix must not break sentinel-based classification downstream.
	wrapped := fmt.Errorf("aiaggregator.dispatch: stream%s: %w", unitFailureSuffix(&CallableUnit{
		ProviderName: "p", Model: "m", Endpoint: "https://e",
	}), llmclient.ErrStreamIdleTimeout)
	if !errors.Is(wrapped, llmclient.ErrStreamIdleTimeout) {
		t.Fatalf("wrapped error must keep the sentinel chained, got %v", wrapped)
	}
	if msg := wrapped.Error(); !strings.Contains(msg, "stream idle timeout") {
		t.Fatalf("wrapped error must keep the sentinel text, got %q", msg)
	}
}

// swapGate temporarily replaces llmclient.DefaultProviderGate with a fresh gate
// registered for the given providers, returning a restore function.
func swapGate(t *testing.T, providers ...string) func() {
	t.Helper()
	gate := llmclient.NewProviderGate()
	for _, p := range providers {
		gate.Register(p, 2)
	}
	orig := llmclient.DefaultProviderGate
	llmclient.DefaultProviderGate = gate
	return func() { llmclient.DefaultProviderGate = orig }
}

// tokenFakeRef returns a fakeRef that resolves provider tokens (empty auth).
func tokenFakeRef() *fakeRef {
	return &fakeRef{results: map[string]any{
		"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
	}}
}

// fakeRef is a minimal ref.Ref implementation for tests.
type fakeRef struct {
	results map[string]any
	err     error
}

func (r *fakeRef) ID() id.ActorID          { return id.ActorID{} }
func (r *fakeRef) Service() (string, bool) { return "", false }
func (r *fakeRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	if r.err != nil {
		return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(r.err))
	}
	v, ok := r.results[callID]
	if !ok {
		// Fall back to payload string key for legacy tests that keyed by
		// the callable name rather than the full callID.
		if s, ok := payload.(string); ok {
			v, ok = r.results[s]
		}
	}
	if !ok {
		return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(fmt.Errorf("fakeRef: no result for %s", callID)))
	}
	return invoke.NewCall(invoke.CallModeUnary, &fakeResultStream{value: v})
}

// fakeResultStream returns a single value then EOF.
type fakeResultStream struct {
	value any
	once  sync.Once
}

func (s *fakeResultStream) Recv() (any, error) {
	var v any
	s.once.Do(func() { v = s.value })
	if v != nil {
		return v, nil
	}
	return nil, io.EOF
}
func (s *fakeResultStream) RecvRaw() ([]byte, error) { return nil, io.EOF }
func (s *fakeResultStream) Close() error             { return nil }
func (s *fakeResultStream) Cancel() error            { return nil }

// dualRegistry builds a Registry with two protocols:
//   - "fail": Stream returns failErr
//   - "ok":   Stream returns a clean event channel (single Stop event)
func dualRegistry(failErr error) llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail",
		Factory:  func(_, _ string) llmclient.Client { return &errorClient{streamErr: failErr} },
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory:  func(_, _ string) llmclient.Client { return &okClient{} },
	})
	return *reg
}

// okClientWithEvents replays a fixed list of events then closes.
type okClientWithEvents struct {
	events []llmclient.Event
}

func (c *okClientWithEvents) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	ch := make(chan llmclient.Event, len(c.events))
	for _, e := range c.events {
		ch <- e
	}
	close(ch)
	return &okStream{ch: ch}, nil
}

type okClient struct{}

func (c *okClient) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	ch := make(chan llmclient.Event, 1)
	close(ch)
	return &okStream{ch: ch}, nil
}

type okStream struct{ ch <-chan llmclient.Event }

func (s *okStream) Events() <-chan llmclient.Event        { return s.ch }
func (s *okStream) Close() error                          { return nil }
func (s *okStream) Telemetry() llmclient.RequestTelemetry { return llmclient.RequestTelemetry{} }

// oneShotStream returns an llmclient.Stream that yields a single text chunk and
// then a Stop event.
func oneShotStream(text string) llmclient.Stream {
	ch := make(chan llmclient.Event, 2)
	ch <- llmclient.Event{Kind: llmclient.EventTextDelta, Text: text}
	ch <- llmclient.Event{Kind: llmclient.EventStop}
	return &okStream{ch: ch}
}
