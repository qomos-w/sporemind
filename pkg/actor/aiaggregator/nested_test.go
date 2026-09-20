package aiaggregator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gosporeactor "github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ──────────────────────────────────────────────────────────────────────────────
// Test infrastructure: fake planner + plan node + actor-context stub
// ──────────────────────────────────────────────────────────────────────────────

// nestedCtx extends testPureCtx with a Planner so handleDispatch can route
// nested aggregator entries through a scripted plan node.
type nestedCtx struct {
	testPureCtx
	planner gosporeactor.Planner
}

func (c *nestedCtx) Planner() gosporeactor.Planner { return c.planner }

// stubActorCtx provides the two actor.Context methods the nested-dispatch
// path touches: LookupID (to resolve the child aggregator ref) and Destroy
// (to clean up the plan node). Everything else is nil-satisfied.
type stubActorCtx struct {
	gosporeactor.Context
	byID map[id.ActorID]ref.Ref
}

func (c *stubActorCtx) LookupID(aid id.ActorID) (ref.Ref, bool) {
	r, ok := c.byID[aid]
	return r, ok
}

func (c *stubActorCtx) Destroy(target ref.Ref) error { return nil }

// staticRef is a ref.Ref with a fixed ActorID; its Invoke is unused by the
// fake planner (the node never dispatches a real Invoke).
type staticRef struct{ aid id.ActorID }

func (r *staticRef) ID() id.ActorID          { return r.aid }
func (r *staticRef) Service() (string, bool) { return "", false }
func (r *staticRef) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(io.EOF))
}

// fakePlanNode implements plan.Node by replaying a scripted sequence of
// RecvResults, then io.EOF.
type fakePlanNode struct {
	ref    ref.Ref
	script []plan.RecvResult
	idx    int
	done   chan struct{}
	once   sync.Once
}

func (n *fakePlanNode) Ref() ref.Ref                { return n.ref }
func (n *fakePlanNode) State() plan.State           { return plan.StateRunning }
func (n *fakePlanNode) Start(context.Context) error { return nil }
func (n *fakePlanNode) Stop() error {
	n.once.Do(func() { close(n.done) })
	return nil
}
func (n *fakePlanNode) Recv() (any, error) {
	if n.idx >= len(n.script) {
		return nil, io.EOF
	}
	r := n.script[n.idx]
	n.idx++
	return r.Value, r.Err
}
func (n *fakePlanNode) Result() (any, error)  { return nil, plan.ErrStreamingResultUnavailable }
func (n *fakePlanNode) Done() <-chan struct{} { return n.done }
func (n *fakePlanNode) Target() ref.Ref       { return n.ref }
func (n *fakePlanNode) CallID() string        { return "aiaggregator.dispatch" }
func (n *fakePlanNode) Payload() any          { return nil }

// fakePlanner implements actor.Planner. Plan returns a fake node carrying
// the scripted RecvResults keyed by the target ref's ActorID string. Call
// and Stream are unused and return nil.
type fakePlanner struct {
	byTarget map[string][]plan.RecvResult
}

func (p *fakePlanner) Plan(target ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	script, ok := p.byTarget[target.ID().String()]
	if !ok {
		script = []plan.RecvResult{{Err: errors.New("fake planner: unplanned target")}}
	}
	return &fakePlanNode{ref: target, script: script, done: make(chan struct{})}, nil
}

func (p *fakePlanner) Call(context.Context, ref.Ref, string, any) *promise.Promise[any] { return nil }

func (p *fakePlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

// chunkList builds a child-side chunk stream (resolved_unit → text → stop).
func chunkList(deep domain.ModelUnit, text string) []plan.RecvResult {
	return []plan.RecvResult{
		{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkResolvedUnit, ResolvedUnit: &deep}},
		{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: text}},
		{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkStop}},
	}
}

// hangingPlanNode simulates a child aggregator whose stream neither opens nor
// fails — Recv blocks until Stop. Used to verify the parent's first-chunk wait
// is bounded by the (parent-derived) child ctx instead of hanging forever.
type hangingPlanNode struct {
	ref  ref.Ref
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func newHangingPlanNode(target ref.Ref) *hangingPlanNode {
	return &hangingPlanNode{ref: target, stop: make(chan struct{}), done: make(chan struct{})}
}

func (n *hangingPlanNode) Ref() ref.Ref                { return n.ref }
func (n *hangingPlanNode) State() plan.State           { return plan.StateRunning }
func (n *hangingPlanNode) Start(context.Context) error { return nil }
func (n *hangingPlanNode) Stop() error {
	n.once.Do(func() { close(n.stop) })
	return nil
}
func (n *hangingPlanNode) Recv() (any, error) {
	<-n.stop
	return nil, io.EOF
}
func (n *hangingPlanNode) Result() (any, error)  { return nil, plan.ErrStreamingResultUnavailable }
func (n *hangingPlanNode) Done() <-chan struct{} { return n.done }
func (n *hangingPlanNode) Target() ref.Ref       { return n.ref }
func (n *hangingPlanNode) CallID() string        { return "aiaggregator.dispatch" }
func (n *hangingPlanNode) Payload() any          { return nil }

// hangingPlanner always returns the same pre-built hanging node.
type hangingPlanner struct{ node plan.Node }

func (p *hangingPlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return p.node, nil
}
func (p *hangingPlanner) Call(context.Context, ref.Ref, string, any) *promise.Promise[any] {
	return nil
}
func (p *hangingPlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

// aggListRef builds a fakeRef whose aimanager.aggregator_list call returns a
// descriptor for the given config ID → canonical actor ID, and whose
// provider_resolve_token returns an empty auth token (for the direct path).
func aggListRef(childCfgID, childActorIDStr string) *fakeRef {
	return &fakeRef{results: map[string]any{
		"aimanager.aggregator_list": domain.AggregatorDescriptorListResp{
			Items: []domain.AggregatorDescriptor{{ID: childCfgID, ActorID: childActorIDStr}},
		},
		"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{},
	}}
}

// TestResolveChildAggregatorRef_TTLCache verifies the child-ref resolution
// cache: repeated resolutions of a listed (positive) or unlisted (negative)
// config id must not re-invoke aimanager.aggregator_list within the TTL —
// the aggregator_list storm amplifier.
func TestResolveChildAggregatorRef_TTLCache(t *testing.T) {
	childAID, _ := canonicalAID(t, 10)
	aimgr := &countingAggListRef{fakeRef: fakeRef{results: map[string]any{
		"aimanager.aggregator_list": domain.AggregatorDescriptorListResp{
			Items: []domain.AggregatorDescriptor{{ID: "child", ActorID: childAID.String()}},
		},
	}}}
	a := &Actor{
		id:           "parent",
		aimanagerRef: aimgr,
		lifecycleCtx: context.Background(),
		actorCtx:     &stubActorCtx{byID: map[id.ActorID]ref.Ref{childAID: &staticRef{aid: childAID}}},
	}

	for i := 0; i < 5; i++ {
		r, err := a.resolveChildAggregatorRef("child")
		if err != nil || r == nil {
			t.Fatalf("resolve %d: err=%v ref=%v", i, err, r)
		}
	}
	if n := atomic.LoadInt32(&aimgr.listCalls); n != 1 {
		t.Errorf("aggregator_list invoked %d times for repeated positive resolve, want 1", n)
	}

	// Negative entry: an unlisted id must not hammer the list either.
	for i := 0; i < 5; i++ {
		if _, err := a.resolveChildAggregatorRef("ghost"); err == nil {
			t.Fatal("ghost id should not resolve")
		}
	}
	if n := atomic.LoadInt32(&aimgr.listCalls); n != 2 {
		t.Errorf("aggregator_list invoked %d times after ghost probes, want 2", n)
	}
}

// countingAggListRef wraps fakeRef and counts aimanager.aggregator_list
// invocations.
type countingAggListRef struct {
	fakeRef
	listCalls int32
}

func (r *countingAggListRef) Invoke(ctx context.Context, callID string, payload any, hdrs ...map[string]string) *invoke.Call {
	if callID == "aimanager.aggregator_list" {
		atomic.AddInt32(&r.listCalls, 1)
	}
	return r.fakeRef.Invoke(ctx, callID, payload, hdrs...)
}
func canonicalAID(t *testing.T, slot uint16) (id.ActorID, string) {
	t.Helper()
	cid, err := identity.NewCanonicalID(uint64(time.Now().UnixMilli()), slot, 1, 1)
	if err != nil {
		t.Fatalf("NewCanonicalID: %v", err)
	}
	aid := id.From(cid)
	return aid, aid.String()
}

// nestedActor builds a parent aggregator whose pool has the given units and
// whose aimanager/actor context resolve the child aggregator ref.
func nestedActor(t *testing.T, parentID string, units []CallableUnit, childCfgID string, childAID id.ActorID, planner gosporeactor.Planner, reg llmclient.Registry) (*Actor, *nestedCtx, *collectingEmitter) {
	t.Helper()
	childRef := &staticRef{aid: childAID}
	actx := &stubActorCtx{byID: map[id.ActorID]ref.Ref{childAID: childRef}}
	ctx := &nestedCtx{
		testPureCtx: testPureCtx{done: make(chan struct{})},
		planner:     planner,
	}
	a := &Actor{
		id:           parentID,
		units:        units,
		strategy:     NewFallbackStrategy(),
		registry:     reg,
		aimanagerRef: aggListRef(childCfgID, childAID.String()),
		lifecycleCtx: context.Background(),
		actorCtx:     actx,
	}
	emit := &collectingEmitter{done: make(chan struct{})}
	return a, ctx, emit
}

// ──────────────────────────────────────────────────────────────────────────────
// 1. matchUnits — aggregator entries participate only in auto-pick
// ──────────────────────────────────────────────────────────────────────────────

func TestMatchUnits_AggregatorEntryAutoPick(t *testing.T) {
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "a::m", Model: "m", ProviderName: "a"},
	}
	got := matchUnits(domain.ModelUnit{}, units)
	if len(got) != 2 {
		t.Fatalf("auto-pick should include the aggregator entry, got %d: %+v", len(got), got)
	}
}

func TestMatchUnits_AggregatorExcludedWhenModelPinned(t *testing.T) {
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "a::m", Model: "m", ProviderName: "a"},
	}
	got := matchUnits(domain.ModelUnit{Model: "m", Provider: "a"}, units)
	if len(got) != 1 || got[0].ID != "a::m" {
		t.Fatalf("pinned model must exclude aggregator entry, got %+v", got)
	}
}

func TestMatchUnits_AggregatorExcludedWhenProviderPinned(t *testing.T) {
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "a::m", Model: "m", ProviderName: "a"},
	}
	got := matchUnits(domain.ModelUnit{Provider: "a"}, units)
	if len(got) != 1 || got[0].ID != "a::m" {
		t.Fatalf("pinned provider must exclude aggregator entry, got %+v", got)
	}
}

func TestMatchUnits_NakedModelRejectsAggregator(t *testing.T) {
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "a::m", Model: "m", ProviderName: "a"},
	}
	got := matchUnits(domain.ModelUnit{Model: "m"}, units)
	if len(got) != 0 {
		t.Fatalf("naked-model must reject all (incl. aggregator), got %+v", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 2. selectUnit / selectUnitExcluding — self-aggregator ref is skipped
// ──────────────────────────────────────────────────────────────────────────────

func TestSelectUnit_SkipsSelfAggregatorRef(t *testing.T) {
	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:parent", AggregatorID: "parent"},
			{ID: "agg:child", AggregatorID: "child"},
			{ID: "a::m", Model: "m", ProviderName: "a"},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if chosen.ID == "agg:parent" {
		t.Fatalf("self-referencing aggregator entry must not be selected")
	}
}

func TestSelectUnitExcluding_SkipsSelfAggregatorRef(t *testing.T) {
	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:parent", AggregatorID: "parent"},
			{ID: "agg:child", AggregatorID: "child"},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnitExcluding(SelectRequest{}, map[string]bool{})
	if err != nil {
		t.Fatalf("selectUnitExcluding: %v", err)
	}
	if chosen.ID != "agg:child" {
		t.Errorf("got %q, want agg:child (self ref must be skipped)", chosen.ID)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 3. applyResolvedConfig — aggregator entry passthrough, ID synthesis, self-skip
// ──────────────────────────────────────────────────────────────────────────────

func TestApplyResolvedConfig_AggregatorPassthroughAndID(t *testing.T) {
	a := &Actor{id: "parent"}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		Units: []domain.ManualCallableUnit{
			{AggregatorID: "child"},
			{ProviderName: "openai", Model: "gpt-5"},
		},
	})
	if len(a.units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(a.units))
	}
	if a.units[0].ID != "agg:child" || a.units[0].AggregatorID != "child" {
		t.Errorf("aggregator entry: ID=%q AggregatorID=%q, want agg:child/child", a.units[0].ID, a.units[0].AggregatorID)
	}
	if a.units[1].ID != "openai::gpt-5" {
		t.Errorf("direct entry ID=%q, want openai::gpt-5", a.units[1].ID)
	}
}

func TestApplyResolvedConfig_SkipsSelfAggregatorRef(t *testing.T) {
	a := &Actor{id: "parent"}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		Units: []domain.ManualCallableUnit{
			{AggregatorID: "parent"},
			{ProviderName: "openai", Model: "gpt-5"},
		},
	})
	if len(a.units) != 1 {
		t.Fatalf("self-referencing aggregator entry must be skipped, got %d units", len(a.units))
	}
	if a.units[0].ID != "openai::gpt-5" {
		t.Errorf("expected only the direct unit, got %q", a.units[0].ID)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 4. handleDispatch nested — success commits parent, no rotation, deep unit
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_SuccessNoRotation(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 7)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "hello world"),
	}}
	// Pool: aggregator ref first, then a direct "ok" unit that must NOT be
	// attempted once the nested stream opens.
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	// Exactly one resolved_unit chunk, and it is the deep unit.
	ru := emit.resolvedUnit()
	if ru == nil {
		t.Fatal("expected a resolved_unit chunk")
	}
	if ru.Model != deep.Model || ru.Provider != deep.Provider {
		t.Errorf("resolved unit = %s/%s, want %s/%s", ru.Provider, ru.Model, deep.Provider, deep.Model)
	}
	emit.mu.Lock()
	count := 0
	for _, c := range emit.chunks {
		if c.Kind == domain.AggregatorChunkResolvedUnit {
			count++
		}
	}
	emit.mu.Unlock()
	if count != 1 {
		t.Errorf("expected exactly one resolved_unit chunk, got %d", count)
	}

	// The parent did NOT rotate to the direct unit: the only text chunk
	// relayed is the child's "hello world".
	if !containsText(emit, "hello world") {
		t.Errorf("expected relayed text 'hello world', chunks: %+v", emit.chunks)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 5. handleDispatch nested — child pool exhausted → parent rotates
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_ChildExhaustedRotatesParent(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	childAID, _ := canonicalAID(t, 8)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		// Child stream returns an open failure (its pool exhausted).
		childAID.String(): {{Err: errors.New("child pool exhausted: connection refused")}},
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("parent should rotate to the direct unit and succeed, got: %v", err)
	}

	// The parent emitted its OWN resolved_unit for the direct unit.
	ru := emit.resolvedUnit()
	if ru == nil {
		t.Fatal("expected a resolved_unit chunk for the direct unit")
	}
	if ru.Provider != "b" || ru.Model != "m" {
		t.Errorf("resolved unit = %s/%s, want b/m", ru.Provider, ru.Model)
	}

	// The aggregator ref entry must NOT receive cooldown/disabled feedback.
	// With health state moved to llmclient, no local fields are written for
	// aggregator ref units.
}

// TestNestedDispatch_ChildExhaustedRegistersUnavailable verifies the
// failure-is-signal rule: a nested open failure wrapping errPoolExhausted
// registers the child aggregator unavailable in the process-wide registry
// (default TTL), so the parent's future selections hard-skip the dead child
// instead of re-probing it at dispatch rate. Rotation to the sibling still
// succeeds within the same dispatch.
func TestNestedDispatch_ChildExhaustedRegistersUnavailable(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()
	t.Cleanup(func() { clearAggHealth("child") })

	childAID, _ := canonicalAID(t, 8)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): {{Err: fmt.Errorf("aiaggregator.dispatch: stream open: %w: %w", errors.New("connection refused"), errPoolExhausted)}},
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("parent should rotate to the direct unit and succeed, got: %v", err)
	}

	if llmclient.IsAggregatorAvailable("child", time.Now()) {
		t.Error("exhausted child must be registered unavailable after nested open failure")
	}
	snap := llmclient.AggregatorHealthSnapshot()
	if e, ok := snap["child"]; !ok || e.State != llmclient.AggregatorHealthUnavailable || e.Remaining <= 0 {
		t.Errorf("registry entry = %+v (ok=%v), want unavailable with positive remaining", e, ok)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 6a. handleDispatch nested — zero-relay child cut → parent rotates to sibling
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_ZeroRelayCutRotatesParent(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 9)
	// Child opens (resolved_unit) then cuts before any content — the deep unit
	// is cooled by the child; the parent must rotate to its direct sibling.
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): {
			{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkResolvedUnit, ResolvedUnit: &deep}},
			{Err: llmclient.ErrStreamClosed},
		},
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("parent should rotate to the direct unit and succeed, got: %v", err)
	}

	// Both resolved_unit chunks were emitted: the child's deep marker first,
	// then the parent's own marker for the direct sibling (last-wins for the
	// caller). No deep content reached the caller.
	emit.mu.Lock()
	var resolved []domain.ModelUnit
	for _, c := range emit.chunks {
		if c.Kind == domain.AggregatorChunkResolvedUnit {
			resolved = append(resolved, *c.ResolvedUnit)
		}
	}
	emit.mu.Unlock()
	if len(resolved) != 2 {
		t.Fatalf("expected deep + direct resolved_unit chunks, got %d", len(resolved))
	}
	last := resolved[len(resolved)-1]
	if last.Provider != "b" || last.Model != "m" {
		t.Errorf("final resolved unit = %s/%s, want b/m", last.Provider, last.Model)
	}
	if containsText(emit, "hello world") {
		t.Errorf("deep content must not be relayed after a zero-relay cut")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 6b. handleDispatch nested — content already relayed → mid-stream cut stands
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_MidStreamErrorAfterContentNoRotation(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 10)
	midErr := llmclient.ErrStreamClosed
	// Child opens, emits one text chunk, then cuts. Content has already been
	// relayed to the caller, so rotation would duplicate output — the error
	// must surface without rotating to the direct sibling.
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): {
			{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkResolvedUnit, ResolvedUnit: &deep}},
			{Value: domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: "hello"}},
			{Err: midErr},
		},
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected a mid-stream error to surface")
	}
	if !errors.Is(err, midErr) {
		t.Errorf("error should carry the mid-stream cause, got: %v", err)
	}

	// The parent did NOT rotate: only the child's deep resolved_unit chunk and
	// its single text chunk were relayed.
	emit.mu.Lock()
	resolvedCount := 0
	for _, c := range emit.chunks {
		if c.Kind == domain.AggregatorChunkResolvedUnit {
			resolvedCount++
		}
	}
	emit.mu.Unlock()
	if resolvedCount != 1 {
		t.Errorf("expected exactly one resolved_unit chunk (the deep one), got %d", resolvedCount)
	}
	if !containsText(emit, "hello") {
		t.Errorf("the relayed content chunk should remain, chunks: %+v", emit.chunks)
	}
}

// TestNestedDispatch_MixedPoolSuccessDoesNotCallDirectUnit covers the mixed
// parent pool success branch: once the nested stream opens, the direct sibling
// is not attempted.
func TestNestedDispatch_MixedPoolSuccessDoesNotCallDirectUnit(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 10)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "nested success"),
	}}
	direct := &countingClient{client: &fakeClient{events: []llmclient.Event{{Kind: llmclient.EventStop}}}}
	a, ctx, emit := nestedActor(t, "parent", []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}, "child", childAID, planner, countingOKRegistry(direct))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}
	if calls := direct.Calls(); calls != 0 {
		t.Fatalf("direct sibling must not be called after nested success, got %d calls", calls)
	}
	ru := emit.resolvedUnit()
	if ru == nil || ru.Model != deep.Model || ru.Provider != deep.Provider {
		t.Fatalf("resolved unit = %+v, want deep unit %s/%s", ru, deep.Provider, deep.Model)
	}
	if !containsText(emit, "nested success") {
		t.Fatalf("expected nested content to be relayed")
	}
}

// TestNestedDispatch_MixedPoolExhaustionCallsDirectUnit covers the mixed parent
// pool exhaustion branch: a child open failure rotates to the direct sibling.
func TestNestedDispatch_MixedPoolExhaustionCallsDirectUnit(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	childAID, _ := canonicalAID(t, 11)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): {{Err: errors.New("child pool exhausted")}},
	}}
	direct := &countingClient{client: &fakeClient{events: []llmclient.Event{{Kind: llmclient.EventStop}}}}
	a, ctx, emit := nestedActor(t, "parent", []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}, "child", childAID, planner, countingOKRegistry(direct))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}
	if calls := direct.Calls(); calls != 1 {
		t.Fatalf("direct sibling must be called once after nested exhaustion, got %d calls", calls)
	}
	ru := emit.resolvedUnit()
	if ru == nil || ru.Model != "m" || ru.Provider != "b" {
		t.Fatalf("resolved unit = %+v, want direct unit b/m", ru)
	}
}

// countingClient records stream attempts while delegating to a test client.
type countingClient struct {
	client llmclient.Client
	mu     sync.Mutex
	calls  int
}

func (c *countingClient) Stream(ctx context.Context, req llmclient.Request) (llmclient.Stream, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.client.Stream(ctx, req)
}

func (c *countingClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func countingOKRegistry(client *countingClient) llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory:  func(_, _ string) llmclient.Client { return client },
	})
	return *reg
}

// capturingPlanner wraps a fakePlanner and records the last payload passed
// to Plan, so tests can assert that req.Unit is forwarded to the child.
type capturingPlanner struct {
	*fakePlanner
	lastPayload any
}

func (p *capturingPlanner) Plan(target ref.Ref, method string, payload any, opts ...plan.Option) (plan.Node, error) {
	p.lastPayload = payload
	return p.fakePlanner.Plan(target, method, payload, opts...)
}

// ──────────────────────────────────────────────────────────────────────────────
// 7. handleDispatch nested — pinned unit is forwarded to child aggregator
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_ForwardsPinnedUnitToChild(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 12)
	fake := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "pinned forwarded"),
	}}
	planner := &capturingPlanner{fakePlanner: fake}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	pinnedUnit := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	req := domain.SendSessionMessageReq{Unit: &pinnedUnit}
	if err := a.handleDispatch(ctx, req, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	// The child must have received the pinned unit in its dispatch request.
	payload, ok := planner.lastPayload.(domain.SendSessionMessageReq)
	if !ok {
		t.Fatalf("expected SendSessionMessageReq payload, got %T", planner.lastPayload)
	}
	if payload.Unit == nil {
		t.Fatal("child dispatch request must carry the forwarded pinned unit")
	}
	if payload.Unit.Model != pinnedUnit.Model || payload.Unit.Provider != pinnedUnit.Provider {
		t.Errorf("forwarded unit = %s/%s, want %s/%s",
			payload.Unit.Provider, payload.Unit.Model, pinnedUnit.Provider, pinnedUnit.Model)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 8. handleDispatch nested — nil unit is still nil when forwarded (auto-pick)
// ──────────────────────────────────────────────────────────────────────────────

func TestNestedDispatch_NilUnitStaysNilForAutoPick(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()

	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 13)
	fake := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "auto pick"),
	}}
	planner := &capturingPlanner{fakePlanner: fake}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	// No Unit set — auto-pick. The child should also receive nil Unit.
	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	payload, ok := planner.lastPayload.(domain.SendSessionMessageReq)
	if !ok {
		t.Fatalf("expected SendSessionMessageReq payload, got %T", planner.lastPayload)
	}
	if payload.Unit != nil {
		t.Errorf("auto-pick dispatch should forward nil Unit, got %+v", payload.Unit)
	}
}

// ─── helpers ───

func containsText(e *collectingEmitter, want string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, c := range e.chunks {
		if c.Kind == domain.AggregatorChunkText && c.Text == want {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

// ──────────────────────────────────────────────────────────────────────────────
// 9. tryDispatchNestedAggregator — first-chunk wait is bounded by parent ctx
// ──────────────────────────────────────────────────────────────────────────────

// A child that neither opens nor fails must not block the parent forever.
// The parent's dispatch ctx (passed as parentCtx) must propagate into the
// child stream ctx so cancellation — caller cancel or the parent's own idle
// timeout — unblocks the first-chunk wait and runs the cleanup/release path.
func TestNestedDispatch_FirstChunkWaitUnblocksOnParentCancel(t *testing.T) {
	childAID, _ := canonicalAID(t, 14)
	childRef := &staticRef{aid: childAID}
	hangNode := newHangingPlanNode(childRef)
	planner := &hangingPlanner{node: hangNode}
	units := []CallableUnit{{ID: "agg:child", AggregatorID: "child"}}
	a, ctx, _ := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type res struct {
		err error
	}
	resCh := make(chan res, 1)
	go func() {
		_, _, err := a.tryDispatchNestedAggregator(ctx, parentCtx, units[0], domain.SendSessionMessageReq{})
		resCh <- res{err: err}
	}()

	// Cancel the parent dispatch ctx; the first-chunk wait must return.
	cancel()

	select {
	case r := <-resCh:
		if r.err == nil {
			t.Fatal("expected an error from cancelled nested dispatch, got nil")
		}
		if !contains(r.err.Error(), "child stream open wait") {
			t.Errorf("expected a child-stream-open-wait error, got: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tryDispatchNestedAggregator blocked past parent cancel — first-chunk wait is not ctx-guarded")
	}

	// The cleanup path (fail→release→node.Stop) must have torn down the child.
	select {
	case <-hangNode.stop:
	case <-time.After(1 * time.Second):
		t.Fatal("child plan node was not stopped after the cancelled dispatch")
	}
}

// A child that neither opens nor fails must not consume the parent's full
// idle budget: the dedicated open deadline fires first, the wait returns a
// deadline-exceeded error, and the child is registered short-term unavailable
// so subsequent parent dispatches skip it instead of re-probing immediately.
func TestNestedDispatch_OpenWaitBoundedByDedicatedDeadline(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child") })

	origTimeout := nestedAggregatorOpenTimeout
	nestedAggregatorOpenTimeout = 50 * time.Millisecond
	t.Cleanup(func() { nestedAggregatorOpenTimeout = origTimeout })

	childAID, _ := canonicalAID(t, 15)
	childRef := &staticRef{aid: childAID}
	hangNode := newHangingPlanNode(childRef)
	planner := &hangingPlanner{node: hangNode}
	units := []CallableUnit{{ID: "agg:child", AggregatorID: "child"}}
	a, ctx, _ := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	parentCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type res struct {
		err error
	}
	resCh := make(chan res, 1)
	start := time.Now()
	go func() {
		_, _, err := a.tryDispatchNestedAggregator(ctx, parentCtx, units[0], domain.SendSessionMessageReq{})
		resCh <- res{err: err}
	}()

	select {
	case r := <-resCh:
		if r.err == nil {
			t.Fatal("expected open-wait timeout error, got nil")
		}
		if !contains(r.err.Error(), "context deadline exceeded") {
			t.Errorf("expected deadline-exceeded, got: %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tryDispatchNestedAggregator blocked past the dedicated open deadline")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("open wait took %v, dedicated deadline did not bound it", elapsed)
	}

	// Deadline expiry (not caller cancel) must register the child unavailable
	// for the default TTL so the parent's next dispatch hard-skips it.
	snap := llmclient.AggregatorHealthSnapshot()
	e, ok := snap["child"]
	if !ok || e.State != llmclient.AggregatorHealthUnavailable {
		t.Fatalf("expected child registered unavailable after open deadline, snapshot: %+v", snap)
	}
	if !e.UnavailableUntil.After(time.Now()) {
		t.Fatalf("UnavailableUntil = %v, want in the future", e.UnavailableUntil)
	}

	// The cleanup path must have torn down the child plan node.
	select {
	case <-hangNode.stop:
	case <-time.After(1 * time.Second):
		t.Fatal("child plan node was not stopped after the timed-out dispatch")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 8. handleIntent nested — refs-only pool still infers a title
// ──────────────────────────────────────────────────────────────────────────────

// TestIntent_NestedRefOnlyPoolProducesTitle reproduces the production
// failure mode end-to-end at the aggregator layer: the fast slot's aggregator
// pool contains ONLY nested-aggregator refs. Before the failover refactor,
// selectIntentUnit excluded refs and handleIntent failed with "no callable
// units" — the agent then fell back to the system aggregator, whose own pool
// was quota-limited, and the session stayed untitled. Now the ref is a
// candidate: the child streams the marker-wrapped title, the parent extracts
// it, and the aggregator itself answers.
func TestIntent_NestedRefOnlyPoolProducesTitle(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child") })

	deep := domain.ModelUnit{Model: "deep-fast", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 11)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "【INTENT】Fix login bug【/INTENT】"),
	}}
	units := []CallableUnit{
		{ID: "agg:child", AggregatorID: "child"},
	}
	a, ctx, _ := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(nil))

	req := domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "fix the login bug"}},
		}},
	}
	got, err := a.handleIntent(ctx, req)
	if err != nil {
		t.Fatalf("handleIntent on refs-only pool: %v", err)
	}
	if got.Text != "Fix login bug" {
		t.Errorf("intent text = %q, want %q", got.Text, "Fix login bug")
	}
}

// TestIntent_ConcreteUnitFailsOverToNestedRef locks the rotation semantics:
// when a concrete unit cannot open its stream, handleIntent rotates to the
// nested-aggregator ref candidate, whose child produces the title.
func TestIntent_ConcreteUnitFailsOverToNestedRef(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child") })

	deep := domain.ModelUnit{Model: "deep-fast", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 12)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "【INTENT】Rotate works【/INTENT】"),
	}}
	units := []CallableUnit{
		{ID: "f::down", Model: "down", ProviderName: "f", Protocol: "fail"},
		{ID: "agg:child", AggregatorID: "child"},
	}
	a, ctx, _ := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(&llmclient.UpstreamError{StatusCode: 500, Message: "provider exploded"}))

	req := domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "verify rotation"}},
		}},
	}
	got, err := a.handleIntent(ctx, req)
	if err != nil {
		t.Fatalf("handleIntent after concrete-unit failure: %v", err)
	}
	if got.Text != "Rotate works" {
		t.Errorf("intent text = %q, want %q", got.Text, "Rotate works")
	}
}

// TestIntent_PinnedUnitOpenFailureFallsBackToPool locks the soft-pin
// semantics: a pinned unit (agent fast slot) that resolves but fails to open
// its stream rotates into the pool candidates instead of returning the open
// error — matching handleSummarize's soft pin. The pinned unit is not
// retried (deduplicated by ID).
func TestIntent_PinnedUnitOpenFailureFallsBackToPool(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child") })

	deep := domain.ModelUnit{Model: "deep-fast", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 13)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "【INTENT】Pin rotates【/INTENT】"),
	}}
	units := []CallableUnit{
		{ID: "f::pinned", Model: "pinned", ProviderName: "f", Protocol: "fail"},
		{ID: "agg:child", AggregatorID: "child"},
	}
	a, ctx, _ := nestedActor(t, "parent", units, "child", childAID, planner, dualRegistry(&llmclient.UpstreamError{StatusCode: 500, Message: "pinned unit exploded"}))

	req := domain.SendSessionMessageReq{
		Unit: &domain.ModelUnit{Model: "pinned", Provider: "f"},
		Messages: []domain.ChatMessage{{
			Role:    "user",
			Content: []domain.ContentBlock{{Type: "text", Text: "soft pin check"}},
		}},
	}
	got, err := a.handleIntent(ctx, req)
	if err != nil {
		t.Fatalf("handleIntent with failing pinned unit: %v", err)
	}
	if got.Text != "Pin rotates" {
		t.Errorf("intent text = %q, want %q", got.Text, "Pin rotates")
	}
}

// TestIntent_CandidateOrdering locks the candidate ordering produced by
// selectIntentCandidates: non-reasoning concrete units first, then reasoning
// ones, then aggregator refs.
func TestIntent_CandidateOrdering(t *testing.T) {
	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child", AggregatorID: "child"},
			{ID: "r::deep", Model: "deep", ProviderName: "r", IsReasoning: true},
			{ID: "p::chat", Model: "chat", ProviderName: "p"},
		},
	}
	cands, err := a.selectIntentCandidates()
	if err != nil {
		t.Fatalf("selectIntentCandidates: %v", err)
	}
	want := []string{"p::chat", "r::deep", "agg:child"}
	if len(cands) != len(want) {
		t.Fatalf("candidates = %+v, want order %v", cands, want)
	}
	for i, id := range want {
		if cands[i].ID != id {
			t.Errorf("candidate[%d] = %q, want %q (full order: %+v)", i, cands[i].ID, id, cands)
		}
	}
}
