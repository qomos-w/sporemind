package aiaggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ════════════════════════════════════════════════════════════════════════════
// Zero-output mid-stream cut — ErrStreamClosed delivered before any content
// chunk must fail over to the next unit (recording the failure) instead of
// aborting the turn. Once content was relayed, or the unit is pinned, the
// error stands. A pool where every unit cuts exhausts rotation and surfaces
// the error.
// ════════════════════════════════════════════════════════════════════════════

func streamCutRegistry() llmclient.Registry {
	reg := llmclient.NewRegistry()
	// "cut": stream opens, then closes without any content (zero-output cut).
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "cut",
		Factory: func(_, _ string) llmclient.Client {
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventError, Err: llmclient.ErrStreamClosed},
			}}
		},
	})
	// "cut-partial": one content chunk, then the connection drops.
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "cut-partial",
		Factory: func(_, _ string) llmclient.Client {
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventTextDelta, Text: "partial"},
				{Kind: llmclient.EventError, Err: llmclient.ErrStreamClosed},
			}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory: func(_, _ string) llmclient.Client {
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventTextDelta, Text: "ok-summary"},
				{Kind: llmclient.EventStop},
			}}
		},
	})
	return *reg
}

func newStreamCutActor(units ...CallableUnit) *Actor {
	return &Actor{
		id:           "test-agg",
		units:        units,
		strategy:     NewFallbackStrategy(),
		registry:     streamCutRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
}

func lastResolvedUnit(e *collectingEmitter) *domain.ModelUnit {
	e.mu.Lock()
	defer e.mu.Unlock()
	var last *domain.ModelUnit
	for i := range e.chunks {
		if e.chunks[i].Kind == domain.AggregatorChunkResolvedUnit {
			last = e.chunks[i].ResolvedUnit
		}
	}
	return last
}

func hasTextChunk(e *collectingEmitter, text string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.chunks {
		if e.chunks[i].Kind == domain.AggregatorChunkText && e.chunks[i].Text == text {
			return true
		}
	}
	return false
}

// TestStreamCutDispatch_ZeroOutputRotatesAndSucceeds verifies a zero-output
// ErrStreamClosed rotates to the next unit and the turn still completes; the
// rotated unit's resolved-unit marker updates the executing model.
func TestStreamCutDispatch_ZeroOutputRotatesAndSucceeds(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStreamCutActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cut"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("zero-output cut should rotate and succeed, got: %v", err)
	}
	if ru := lastResolvedUnit(emit); ru == nil || ru.Provider != "b" {
		t.Errorf("last resolved unit should be the rotated unit b, got %+v", ru)
	}
	if !hasTextChunk(emit, "ok-summary") {
		t.Error("expected content from the rotated unit b")
	}
}

// TestStreamCutDispatch_PartialOutputKeepsError verifies a cut after content
// was relayed must NOT rotate (the new stream would duplicate output) and
// surfaces the original error.
func TestStreamCutDispatch_PartialOutputKeepsError(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStreamCutActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cut-partial"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("partial-output cut must not rotate; expected the error to stand")
	}
	if !errors.Is(err, llmclient.ErrStreamClosed) {
		t.Errorf("error should chain ErrStreamClosed, got: %v", err)
	}
	if hasTextChunk(emit, "ok-summary") {
		t.Error("unit b must not have been dispatched after partial output")
	}
}

// TestStreamCutDispatch_UnitPinnedKeepsError verifies the UnitPinned contract:
// a hard user selection surfaces the cut instead of rotating to unit b.
func TestStreamCutDispatch_UnitPinnedKeepsError(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStreamCutActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cut"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	req := domain.SendSessionMessageReq{
		Unit:       &domain.ModelUnit{Model: "m", Provider: "a"},
		UnitPinned: true,
	}
	err := a.handleDispatch(ctx, req, emit)
	if err == nil {
		t.Fatal("UnitPinned dispatch must surface the cut instead of rotating")
	}
	if !errors.Is(err, llmclient.ErrStreamClosed) {
		t.Errorf("error should chain ErrStreamClosed, got: %v", err)
	}
	if hasTextChunk(emit, "ok-summary") {
		t.Error("unit b must not have been dispatched under UnitPinned")
	}
}

// TestStreamCutDispatch_AllUnitsCutExhaustsRotation verifies that when every
// pool unit cuts with zero output, rotation exhausts and the exact error
// surfaces instead of looping.
func TestStreamCutDispatch_AllUnitsCutExhaustsRotation(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStreamCutActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cut"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "cut"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected rotation to exhaust and surface the cut error")
	}
	if !errors.Is(err, llmclient.ErrStreamClosed) {
		t.Errorf("error should chain ErrStreamClosed, got: %v", err)
	}
}
