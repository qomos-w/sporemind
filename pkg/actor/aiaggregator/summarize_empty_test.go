package aiaggregator

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ════════════════════════════════════════════════════════════════════════════
// Empty-completion guard — a summarize stream that finishes without a single
// text delta (reasoning-only output, stop_reason "length") must not surface as
// a silent empty success: it earns exactly one rotation to another pool unit,
// then a descriptive error carrying the stop reason.
// ════════════════════════════════════════════════════════════════════════════

// emptyTextRegistry builds a Registry with two protocols:
//   - "empty": reasoning deltas then Stop("length") — zero text deltas
//   - "ok":    a normal text delta then Stop("stop")
//
// opens counts how many "empty" streams were opened.
func emptyTextRegistry(opens *int32) llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "empty",
		Factory: func(_, _ string) llmclient.Client {
			atomic.AddInt32(opens, 1)
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventReasoningDelta, Text: "thinking about the summary"},
				{Kind: llmclient.EventStop, StopReason: llmclient.StopReasonLength},
			}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory: func(_, _ string) llmclient.Client {
			return &okClientWithEvents{events: []llmclient.Event{
				{Kind: llmclient.EventTextDelta, Text: "ok-summary"},
				{Kind: llmclient.EventStop, StopReason: llmclient.StopReasonStop},
			}}
		},
	})
	return *reg
}

func newEmptyTextActor(reg llmclient.Registry, units ...CallableUnit) *Actor {
	return &Actor{
		id:           "test-agg",
		units:        units,
		strategy:     NewFallbackStrategy(),
		registry:     reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
}

func emptyUnit(provider string) CallableUnit {
	return CallableUnit{ID: provider + "::m", Model: "m", ProviderName: provider, Protocol: "empty"}
}

// TestSummarizeEmptyCompletion_RotatesToHealthyUnit verifies a reasoning-only
// completion rotates once and returns the healthy unit's text.
func TestSummarizeEmptyCompletion_RotatesToHealthyUnit(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	var opens int32
	a := newEmptyTextActor(emptyTextRegistry(&opens),
		emptyUnit("a"),
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)

	resp, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err != nil {
		t.Fatalf("empty completion should rotate to the healthy unit, got: %v", err)
	}
	if resp.Text != "ok-summary" {
		t.Errorf("summary text = %q, want %q", resp.Text, "ok-summary")
	}
	if n := atomic.LoadInt32(&opens); n != 1 {
		t.Errorf("empty-unit stream opens = %d, want 1", n)
	}
}

// TestSummarizeEmptyCompletion_SingleUnitErrorsWithStopReason verifies that
// when no alternative unit exists the error names the stop reason instead of
// returning an empty success.
func TestSummarizeEmptyCompletion_SingleUnitErrorsWithStopReason(t *testing.T) {
	restoreGate := swapGate(t, "a")
	defer restoreGate()

	var opens int32
	a := newEmptyTextActor(emptyTextRegistry(&opens), emptyUnit("a"))

	_, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err == nil {
		t.Fatal("expected an error when the only unit completes without text")
	}
	if !strings.Contains(err.Error(), llmclient.StopReasonLength) {
		t.Errorf("error should carry stop_reason %q, got: %v", llmclient.StopReasonLength, err)
	}
}

// TestSummarizeEmptyCompletion_RotationIsBounded verifies the empty-completion
// rotation budget: with three empty units only two streams are opened (initial
// attempt + one rotation) before the error surfaces.
func TestSummarizeEmptyCompletion_RotationIsBounded(t *testing.T) {
	restoreGate := swapGate(t, "a", "b", "c")
	defer restoreGate()

	var opens int32
	a := newEmptyTextActor(emptyTextRegistry(&opens),
		emptyUnit("a"),
		emptyUnit("b"),
		emptyUnit("c"),
	)

	_, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err == nil {
		t.Fatal("expected an error when every unit completes without text")
	}
	if n := atomic.LoadInt32(&opens); n != 2 {
		t.Errorf("empty-unit stream opens = %d, want 2 (initial + one rotation)", n)
	}
	if !strings.Contains(err.Error(), llmclient.StopReasonLength) {
		t.Errorf("error should carry stop_reason %q, got: %v", llmclient.StopReasonLength, err)
	}
}
