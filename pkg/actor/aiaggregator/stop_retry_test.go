package aiaggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ════════════════════════════════════════════════════════════════════════════
// Stop retry guard — a stop-class (400/404) stream-open failure rotates to
// another unit; rotation continues while each failure's status differs from
// the first, and terminates only when the first status reappears (the same
// code on two units signals a request-level problem). Cancellation and
// UnitPinned never rotate.
// ════════════════════════════════════════════════════════════════════════════

// stopRegistry builds a Registry with per-scenario failing protocols:
//   - "fail400": stream open fails with a 400 upstream error
//   - "fail404": stream open fails with a 404 upstream error
//   - "cancel":  stream open fails with context.Canceled
//   - "ok":      clean event channel (a Text event then Stop)
func stopRegistry() llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail400",
		Factory: func(_, _ string) llmclient.Client {
			return &errorClient{streamErr: &llmclient.UpstreamError{StatusCode: 400, Message: "invalid request body"}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail404",
		Factory: func(_, _ string) llmclient.Client {
			return &errorClient{streamErr: &llmclient.UpstreamError{StatusCode: 404, Message: "model not found"}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "cancel",
		Factory:  func(_, _ string) llmclient.Client { return &errorClient{streamErr: context.Canceled} },
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

func newStopRetryActor(units ...CallableUnit) *Actor {
	return &Actor{
		id:           "test-agg",
		units:        units,
		strategy:     NewFallbackStrategy(),
		registry:     stopRegistry(),
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
}

func upstreamStatus(t *testing.T, err error) int {
	t.Helper()
	var upstream *llmclient.UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("error should chain an UpstreamError, got: %v", err)
	}
	return upstream.StatusCode
}

// ── guard primitive ──

func TestStopRetryBudget_Consume(t *testing.T) {
	b := &stopRetryBudget{}
	// First stop-class failure records its status and rotates.
	if !b.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 400}, errors.New("http 400")) {
		t.Fatal("first stop-class failure must rotate")
	}
	// Same status repeats → terminate.
	if b.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 400}, errors.New("http 400")) {
		t.Fatal("repeat of the first status must terminate")
	}
	// A different status rotates again.
	if !b.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 404}, errors.New("http 404")) {
		t.Fatal("different stop code must rotate")
	}
	// The first status reappears → terminate.
	if b.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 400}, errors.New("http 400")) {
		t.Fatal("reappearance of the first status must terminate")
	}

	// Cancellation never rotates and does not record a first status.
	c := &stopRetryBudget{}
	if c.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 0}, context.Canceled) {
		t.Fatal("cancellation must never rotate")
	}
	if !c.consume(llmclient.StreamOpenClassification{Class: llmclient.ClassStop, StatusCode: 400}, errors.New("http 400")) {
		t.Fatal("cancellation must not record a first status (next stop must rotate)")
	}
}

// ── dispatch candidate loop ──

// TestStopRetryDispatch_First400RotatesAndSucceeds verifies the first
// stop-class failure (400) records its status and rotates to the next unit,
// which opens the stream successfully.
func TestStopRetryDispatch_First400RotatesAndSucceeds(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("dispatch should succeed via stop rotation, got: %v", err)
	}
	ru := emit.resolvedUnit()
	if ru == nil {
		t.Fatal("expected resolved_unit chunk for the rotated unit")
	}
	if ru.Provider != "b" || ru.Model != "m" {
		t.Errorf("resolved unit = provider=%q model=%q, want provider=b model=m", ru.Provider, ru.Model)
	}
}

// TestStopRetryDispatch_RepeatStatusTerminates verifies a second stop-class
// failure with the same status as the first terminates and surfaces the
// exact failure.
func TestStopRetryDispatch_RepeatStatusTerminates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail400"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected the repeated 400 to terminate dispatch, got success")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the 400 upstream error, got status %d: %v", got, err)
	}
	if ru := emit.resolvedUnit(); ru != nil {
		t.Errorf("no resolved_unit expected (stream never opened), got provider=%q model=%q", ru.Provider, ru.Model)
	}
}

// TestStopRetryDispatch_DifferentStatusRotates verifies a stop-class failure
// whose status differs from the first keeps rotating and can reach a healthy
// unit.
func TestStopRetryDispatch_DifferentStatusRotates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b", "c")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail404"},
		CallableUnit{ID: "c::m", Model: "m", ProviderName: "c", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("different stop codes should keep rotating to the healthy unit, got: %v", err)
	}
	ru := emit.resolvedUnit()
	if ru == nil || ru.Provider != "c" {
		t.Errorf("expected resolved unit provider=c, got %+v", ru)
	}
}

// TestStopRetryDispatch_FirstStatusReappearsTerminates verifies that after
// rotating through a different stop code, reappearance of the first status
// terminates.
func TestStopRetryDispatch_FirstStatusReappearsTerminates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b", "c")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail404"},
		CallableUnit{ID: "c::m", Model: "m", ProviderName: "c", Protocol: "fail400"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected reappearance of the first 400 to terminate dispatch")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the terminating 400, got status %d: %v", got, err)
	}
	if ru := emit.resolvedUnit(); ru != nil {
		t.Errorf("no resolved_unit expected, got provider=%q model=%q", ru.Provider, ru.Model)
	}
}

// TestStopRetryDispatch_CanceledNeverRotates verifies caller cancellation
// terminates immediately: the healthy second unit is never attempted.
func TestStopRetryDispatch_CanceledNeverRotates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cancel"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected cancellation to terminate dispatch immediately")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should chain context.Canceled, got: %v", err)
	}
	if ru := emit.resolvedUnit(); ru != nil {
		t.Errorf("no resolved_unit expected (no rotation on cancellation), got provider=%q model=%q", ru.Provider, ru.Model)
	}
}

// TestStopRetryDispatch_UnitPinnedNeverRotates verifies the UnitPinned
// contract still fails loudly on a stop-class error: the hard user selection
// must not rotate even with the stop guard available.
func TestStopRetryDispatch_UnitPinnedNeverRotates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
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
		t.Fatal("UnitPinned dispatch must surface the 400 instead of rotating")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the original 400 upstream error, got status %d: %v", got, err)
	}
	if ru := emit.resolvedUnit(); ru != nil {
		t.Errorf("no resolved_unit expected (UnitPinned never rotates), got provider=%q model=%q", ru.Provider, ru.Model)
	}
}

// ── summarize candidate loop ──

// TestStopRetrySummarize_RepeatStatusTerminates verifies the guard in the
// summarize loop: two consecutive 400s terminate and surface the exact
// failure. (The rotation-success case is covered by
// TestSummarizeFailover_StopClass400OneShotRotation.)
func TestStopRetrySummarize_RepeatStatusTerminates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail400"},
	)

	_, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err == nil {
		t.Fatal("expected the repeated 400 to terminate summarize, got success")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the 400 upstream error, got status %d: %v", got, err)
	}
}

// TestStopRetrySummarize_DifferentStatusRotates verifies a different stop
// code keeps rotating and can reach a healthy unit in the summarize loop.
func TestStopRetrySummarize_DifferentStatusRotates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b", "c")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail404"},
		CallableUnit{ID: "c::m", Model: "m", ProviderName: "c", Protocol: "ok"},
	)

	resp, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err != nil {
		t.Fatalf("different stop codes should keep rotating to the healthy unit, got: %v", err)
	}
	if resp.Text != "ok-summary" {
		t.Errorf("summary text = %q, want %q (must come from the rotated unit)", resp.Text, "ok-summary")
	}
}

// TestStopRetrySummarize_FirstStatusReappearsTerminates verifies that after
// rotating through a different stop code, reappearance of the first status
// terminates the summarize loop.
func TestStopRetrySummarize_FirstStatusReappearsTerminates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b", "c")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "fail400"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "fail404"},
		CallableUnit{ID: "c::m", Model: "m", ProviderName: "c", Protocol: "fail400"},
	)

	_, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err == nil {
		t.Fatal("expected reappearance of the first 400 to terminate summarize")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the terminating 400, got status %d: %v", got, err)
	}
}

// TestStopRetrySummarize_CanceledNeverRotates verifies caller cancellation
// terminates summarize immediately.
func TestStopRetrySummarize_CanceledNeverRotates(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	a := newStopRetryActor(
		CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "cancel"},
		CallableUnit{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	)

	_, err := a.handleSummarize(&testPureCtx{done: make(chan struct{})}, domain.SendSessionMessageReq{})
	if err == nil {
		t.Fatal("expected cancellation to terminate summarize immediately")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should chain context.Canceled, got: %v", err)
	}
}
