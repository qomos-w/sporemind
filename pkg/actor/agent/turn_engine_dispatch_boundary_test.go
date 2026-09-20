package agent

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// fakeStreamNode is a minimal plan.Node satisfying the full interface so
// selectDispatchStream's caller can Stop/Ref it. The commit-after-open boundary
// test asserts on attempt counts rather than node identity.
type fakeStreamNode struct {
	id    int
	stops int
}

func (n *fakeStreamNode) Start(context.Context) error { return nil }
func (n *fakeStreamNode) Stop() error                 { n.stops++; return nil }
func (n *fakeStreamNode) Ref() ref.Ref                { return fakeCompactionRef{} }
func (n *fakeStreamNode) State() plan.State           { return plan.StateRunning }
func (n *fakeStreamNode) Recv() (any, error)          { return nil, nil }
func (n *fakeStreamNode) Result() (any, error)        { return nil, nil }
func (n *fakeStreamNode) Done() <-chan struct{}       { ch := make(chan struct{}); close(ch); return ch }
func (n *fakeStreamNode) Target() ref.Ref             { return fakeCompactionRef{} }
func (n *fakeStreamNode) CallID() string              { return "" }
func (n *fakeStreamNode) Payload() any                { return nil }

// openRecord captures one attempt to open a dispatch stream.
type openRecord struct {
	aggRef ref.Ref
	unit   string // Model of the pinned unit, "" if none
	pinned bool   // UnitPinned on the request
}

// fakeStreamOpener returns a canned (succeed-at-index) behavior, recording the
// aggRef + pinned unit of each attempt so tests assert the candidate order and
// unit-pinning. failCount candidates fail with errStreamOpenFailed; the
// failCount-th succeeds (so failCount=len(targets) means all fail).
type fakeStreamOpener struct {
	mu        sync.Mutex
	records   []openRecord
	nodeID    int
	failCount int // number of leading candidates that fail
	failErr   error
}

func (o *fakeStreamOpener) open(_ actor.Context, aggRef ref.Ref, sessReq domain.SendSessionMessageReq) (plan.Node, <-chan plan.RecvResult, context.CancelFunc, *time.Timer, plan.RecvResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	attemptIdx := len(o.records)
	unit := ""
	if sessReq.Unit != nil {
		unit = sessReq.Unit.Model
	}
	o.records = append(o.records, openRecord{aggRef: aggRef, unit: unit, pinned: sessReq.UnitPinned})
	if attemptIdx < o.failCount {
		return nil, nil, nil, nil, plan.RecvResult{}, o.failErr
	}
	o.nodeID++
	n := &fakeStreamNode{id: o.nodeID}
	ch := make(chan plan.RecvResult)
	close(ch)
	return n, ch, func() {}, time.NewTimer(time.Hour), plan.RecvResult{}, nil
}

func newBoundaryEngine() *turnEngine {
	return &turnEngine{logger: fakeCompactionLogger{}}
}

func systemAggRef() ref.Ref { return fakeCompactionRef{} }
func namedAggRef() ref.Ref  { return fakeCompactionRef{} }

// TestAwaitStreamOpen_HandlerErrorIsOpenFailure reproduces the custom-
// aggregator 403 bug: the aggregator's dispatch handler returns the stream-open
// failure ("aiaggregator.dispatch: stream open: http 403 [access_terminated_error/]")
// as the first wire item, not via node.Start. awaitStreamOpen must surface it
// as an open failure so selectDispatchStream falls back to the next slot
// candidate instead of terminating the turn.
func TestAwaitStreamOpen_HandlerErrorIsOpenFailure(t *testing.T) {
	handlerErr := errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 403 [access_terminated_error/]: You've reached your usage limit for this billing cycle")
	recvCh := make(chan plan.RecvResult, 1)
	recvCh <- plan.RecvResult{Err: handlerErr}
	first, err := awaitStreamOpen(recvCh, time.NewTimer(time.Hour), make(chan struct{}))
	if !errors.Is(err, handlerErr) {
		t.Fatalf("expected the handler error, got %v", err)
	}
	if first.Value != nil {
		t.Errorf("no chunk should be returned on open failure, got %+v", first.Value)
	}
}

// TestAwaitStreamOpen_FirstChunkConfirmsOpen verifies the happy path: the
// aggregator's first item (the resolved-unit chunk emitted right after the
// stream opens) is returned to the caller for normal routing.
func TestAwaitStreamOpen_FirstChunkConfirmsOpen(t *testing.T) {
	chunk := domain.AggregatorChunk{
		Kind:         domain.AggregatorChunkResolvedUnit,
		ResolvedUnit: &domain.ModelUnit{Model: "k3", Provider: "kimi-2"},
	}
	recvCh := make(chan plan.RecvResult, 1)
	recvCh <- plan.RecvResult{Value: chunk}
	first, err := awaitStreamOpen(recvCh, time.NewTimer(time.Hour), make(chan struct{}))
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	got, decErr := decodeChunk(first.Value)
	if decErr != nil {
		t.Fatalf("first chunk must decode: %v", decErr)
	}
	if got.Kind != domain.AggregatorChunkResolvedUnit || got.ResolvedUnit.Provider != "kimi-2" {
		t.Errorf("unexpected first chunk: %+v", got)
	}
}

// TestAwaitStreamOpen_ClosedBeforeOpen verifies a channel that closes before
// any item (abnormal aggregator exit) is an open failure, not a silent success.
func TestAwaitStreamOpen_ClosedBeforeOpen(t *testing.T) {
	recvCh := make(chan plan.RecvResult)
	close(recvCh)
	if _, err := awaitStreamOpen(recvCh, time.NewTimer(time.Hour), make(chan struct{})); err == nil {
		t.Fatal("closed-before-open must be an error")
	}
}

// TestAwaitStreamOpen_EOFBeforeOpen verifies an io.EOF first result (stream
// ended before any content) is an open failure eligible for fallback.
func TestAwaitStreamOpen_EOFBeforeOpen(t *testing.T) {
	recvCh := make(chan plan.RecvResult, 1)
	recvCh <- plan.RecvResult{Err: io.EOF}
	if _, err := awaitStreamOpen(recvCh, time.NewTimer(time.Hour), make(chan struct{})); err == nil {
		t.Fatal("EOF before open must be an error")
	}
}

// TestAwaitStreamOpen_IdleTimeout verifies the idle timer firing before the
// first item surfaces ErrStreamIdleTimeout as an open failure.
func TestAwaitStreamOpen_IdleTimeout(t *testing.T) {
	recvCh := make(chan plan.RecvResult)
	_, err := awaitStreamOpen(recvCh, time.NewTimer(10*time.Millisecond), make(chan struct{}))
	if !errors.Is(err, llmclient.ErrStreamIdleTimeout) {
		t.Fatalf("expected ErrStreamIdleTimeout, got %v", err)
	}
}

// TestAwaitStreamOpen_Cancelled verifies caller cancellation surfaces
// context.Canceled so no fallback is attempted on a dead engine.
func TestAwaitStreamOpen_Cancelled(t *testing.T) {
	recvCh := make(chan plan.RecvResult)
	done := make(chan struct{})
	close(done)
	_, err := awaitStreamOpen(recvCh, time.NewTimer(time.Hour), done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// first candidate opens its stream and is selected; no further candidate is
// attempted (single attempt recorded).
func TestSelectDispatchStream_FirstCandidateOpens(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 0}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1"}},
		{aggRef: namedAggRef()},
	}
	node, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}
	if len(opener.records) != 1 {
		t.Errorf("expected exactly 1 open attempt, got %d (must not over-try after success)", len(opener.records))
	}
}

// TestSelectDispatchStream_FallsBackToNextCandidate verifies the agent-layer
// candidate-chain fallback: when the first candidate fails to open its stream,
// the next candidate is tried, and it succeeds.
func TestSelectDispatchStream_FallsBackToNextCandidate(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 1, failErr: errors.New("connection refused")}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1"}},
		{aggRef: namedAggRef()},
	}
	node, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node from second candidate")
	}
	if len(opener.records) != 2 {
		t.Errorf("expected 2 attempts (first failed, second ok), got %d", len(opener.records))
	}
}

// TestSelectDispatchStream_AllCandidatesFailReturnsLastError verifies that when
// every candidate fails to open, the LAST candidate's error is surfaced (so
// the caller reports the most relevant failure, not the first).
func TestSelectDispatchStream_AllCandidatesFailReturnsLastError(t *testing.T) {
	e := newBoundaryEngine()
	lastErr := errors.New("all aggregators down")
	opener := &fakeStreamOpener{failCount: 3, failErr: lastErr}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1"}},
		{aggRef: namedAggRef()},
		{aggRef: systemAggRef()},
	}
	_, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if !errors.Is(err, lastErr) {
		t.Errorf("expected last candidate error, got %v", err)
	}
	if len(opener.records) != 3 {
		t.Errorf("expected all 3 candidates attempted, got %d", len(opener.records))
	}
}

// TestSelectDispatchStream_UnitPinnedOntoRequest verifies a unit-kind
// candidate's concrete model is pinned onto the dispatch request, while an
// aggregator/auto candidate (empty unit) leaves the request unchanged.
func TestSelectDispatchStream_UnitPinnedOntoRequest(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 0}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "pinned-model"}},
	}
	baseReq := domain.SendSessionMessageReq{}
	selectDispatchStream(newFakeCompactionContext(), e, targets, baseReq, opener.open)
	if len(opener.records) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(opener.records))
	}
	if opener.records[0].unit != "pinned-model" {
		t.Errorf("unit not pinned: got %q, want pinned-model", opener.records[0].unit)
	}
	// baseReq must not be mutated (selectDispatchStream works on a copy).
	if baseReq.Unit != nil {
		t.Errorf("caller's base request was mutated: Unit=%+v", baseReq.Unit)
	}
}

// TestSelectDispatchStream_AggregatorCandidateLeavesUnitEmpty verifies an
// aggregator/auto candidate does NOT pin a unit onto the request.
func TestSelectDispatchStream_AggregatorCandidateLeavesUnitEmpty(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 0}
	targets := []dispatchTarget{
		{aggRef: namedAggRef()}, // empty unit
	}
	selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if len(opener.records) != 1 || opener.records[0].unit != "" {
		t.Errorf("aggregator candidate must not pin a unit: %+v", opener.records)
	}
}

// TestSelectDispatchStream_UnitLockedSlotSetsUnitPinned verifies the hard-pin
// propagation: when the primary slot is unit-locked (user selected a concrete
// model unit, no aggregator fallback), the pinned unit request carries
// UnitPinned so the aggregator surfaces failures instead of rotating models.
func TestSelectDispatchStream_UnitLockedSlotSetsUnitPinned(t *testing.T) {
	e := newBoundaryEngine()
	e.primarySlot = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m1", Provider: "p1"}},
	}}
	opener := &fakeStreamOpener{failCount: 0}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1", Provider: "p1"}},
	}
	selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if len(opener.records) != 1 {
		t.Fatalf("expected 1 attempt, got %d", len(opener.records))
	}
	if !opener.records[0].pinned {
		t.Errorf("unit-locked slot must set UnitPinned on the dispatch request")
	}
}

// TestSelectDispatchStream_SoftAffinitySlotLeavesUnitUnpinned verifies a
// soft-affinity slot (learned unit + aggregator fallback) does NOT set
// UnitPinned: its unit attempt keeps aggregator failover, and the fallback
// candidate clears both the unit and the flag.
func TestSelectDispatchStream_SoftAffinitySlotLeavesUnitUnpinned(t *testing.T) {
	e := newBoundaryEngine()
	e.primarySlot = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{Kind: modelRefKindAggregator, AggregatorID: "custom"},
	}}
	opener := &fakeStreamOpener{failCount: 1, failErr: errors.New("http 403")}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{aggRef: namedAggRef()},
	}
	if _, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open); err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if len(opener.records) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(opener.records))
	}
	if opener.records[0].pinned {
		t.Errorf("soft-affinity unit attempt must not set UnitPinned (failover stays enabled)")
	}
	if opener.records[1].pinned || opener.records[1].unit != "" {
		t.Errorf("fallback candidate must clear unit and flag: %+v", opener.records[1])
	}
}

// TestSelectDispatchStream_ManualPickUnderAggregatorKeepsFailover verifies a
// unit manually picked from a named aggregator's pool ([unit@agg, auto]):
// it is NOT unit-locked, so the unit attempt runs unpinned and on failure the
// candidate loop falls through to the [auto] target — switching stays
// enabled for anything routed through an aggregator.
func TestSelectDispatchStream_ManualPickUnderAggregatorKeepsFailover(t *testing.T) {
	e := newBoundaryEngine()
	e.primarySlot = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m1", Provider: "p1"}, AggregatorID: "custom"},
		{Kind: modelRefKindAuto},
	}}
	opener := &fakeStreamOpener{failCount: 1, failErr: errors.New("http 403")}
	targets := []dispatchTarget{
		{aggRef: namedAggRef(), unit: domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{aggRef: systemAggRef()}, // [auto] fallback target
	}
	if _, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open); err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if len(opener.records) != 2 {
		t.Fatalf("manual pick under aggregator must fail over, got %d attempts", len(opener.records))
	}
	if opener.records[0].pinned {
		t.Errorf("unit attempt under an aggregator must not set UnitPinned (aggregator may rotate)")
	}
	if opener.records[1].pinned || opener.records[1].unit != "" {
		t.Errorf("fallback target must clear unit and flag: %+v", opener.records[1])
	}
}

// TestSelectDispatchStream_UnitLockedStopsAtFailedUnit verifies a unit-locked
// slot (all-unit chain) surfaces the first unit's failure instead of trying
// the next unit candidate — switching models on a non-aggregator selection
// is forbidden.
func TestSelectDispatchStream_UnitLockedStopsAtFailedUnit(t *testing.T) {
	e := newBoundaryEngine()
	e.primarySlot = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m2", Provider: "p2"}},
	}}
	opener := &fakeStreamOpener{failCount: 2, failErr: errors.New("http 403")}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m2", Provider: "p2"}},
	}
	_, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if err == nil {
		t.Fatal("unit-locked failure must surface, not fall through")
	}
	if len(opener.records) != 1 {
		t.Fatalf("unit-locked slot must stop at the failed unit, got %d attempts", len(opener.records))
	}
	if !opener.records[0].pinned {
		t.Errorf("unit-locked slot must set UnitPinned so the aggregator surfaces the failure")
	}
}

// TestSelectDispatchStream_FallbackClearsBaseRequestUnit reproduces the
// soft-affinity 403 bug: buildDispatchRequest copies TurnInput.Unit (the slot's
// first unit-kind candidate) onto the base request, so when the pinned unit's
// stream open fails (e.g. 403 quota), the aggregator/auto fallback candidate
// must NOT inherit that pin — otherwise every fallback target leads with the
// same dead unit instead of letting the pool auto-pick a healthy one.
func TestSelectDispatchStream_FallbackClearsBaseRequestUnit(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 1, failErr: errors.New("http 403 quota exhausted")}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "k3", Provider: "kimi-1"}},
		{aggRef: systemAggRef()}, // auto fallback — must free-pick
	}
	baseReq := domain.SendSessionMessageReq{
		Unit: &domain.ModelUnit{Model: "k3", Provider: "kimi-1"},
	}
	_, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, baseReq, opener.open)
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if len(opener.records) != 2 {
		t.Fatalf("expected 2 attempts, got %d", len(opener.records))
	}
	if opener.records[0].unit != "k3" {
		t.Errorf("first attempt must pin the unit candidate, got %q", opener.records[0].unit)
	}
	if opener.records[1].unit != "" {
		t.Errorf("fallback attempt must clear the pinned unit, got %q", opener.records[1].unit)
	}
}

// TestSelectDispatchStream_EmptyTargetsInvokesNoOpener verifies the iterator
// contract: with no resolved targets, no opener is invoked and the result is
// empty (no node, no error). The "no model target available" guard lives in
// runDispatch, which checks len(targets)==0 before calling this helper.
func TestSelectDispatchStream_EmptyTargetsInvokesNoOpener(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 0}
	node, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, nil, domain.SendSessionMessageReq{}, opener.open)
	if node != nil {
		t.Errorf("empty targets should yield nil node, got %v", node)
	}
	if err != nil {
		t.Errorf("empty targets should yield nil error (caller guards len==0), got %v", err)
	}
	if len(opener.records) != 0 {
		t.Errorf("expected zero attempts for empty targets, got %d", len(opener.records))
	}
}

// TestSelectDispatchStream_CommitsToOpenedStream verifies the boundary
// invariant: once a stream opens, no later candidate is tried. Concretely,
// with failCount=1 (first fails, second opens), exactly 2 attempts occur and
// the third candidate is never attempted even though it exists.
func TestSelectDispatchStream_CommitsToOpenedStream(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 1, failErr: errors.New("503")}
	targets := []dispatchTarget{
		{aggRef: systemAggRef(), unit: domain.ModelUnit{Model: "m1"}},
		{aggRef: namedAggRef()},  // opens here
		{aggRef: systemAggRef()}, // must never be attempted
	}
	_, _, _, _, _, err := selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if err != nil {
		t.Fatalf("expected commit success, got %v", err)
	}
	if len(opener.records) != 2 {
		t.Errorf("expected exactly 2 attempts (committed after open), got %d — post-open rollback is forbidden", len(opener.records))
	}
}

// TestSelectDispatchStream_CandidateOrderPreserved verifies the candidates are
// attempted in the exact order returned by resolveTargets (declared chain
// order), so the preferred aggregator is tried before fallbacks.
func TestSelectDispatchStream_CandidateOrderPreserved(t *testing.T) {
	e := newBoundaryEngine()
	opener := &fakeStreamOpener{failCount: 3, failErr: errors.New("fail")}
	first := namedAggRef()
	second := systemAggRef()
	targets := []dispatchTarget{
		{aggRef: first},
		{aggRef: second},
		{aggRef: first},
	}
	selectDispatchStream(newFakeCompactionContext(), e, targets, domain.SendSessionMessageReq{}, opener.open)
	if len(opener.records) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(opener.records))
	}
	if opener.records[0].aggRef != first || opener.records[1].aggRef != second || opener.records[2].aggRef != first {
		t.Errorf("candidate order not preserved: %+v", opener.records)
	}
}
