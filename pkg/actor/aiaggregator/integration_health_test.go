package aiaggregator

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ──────────────────────────────────────────────────────────────────────────────
// Integration verification: process-global health consistency, failover
// regression, status protocol projection, and the on-demand path.
//
// These tests exercise the READ side (selectUnit/selectUnitExcluding/status)
// and the WRITE side (applyStreamOpenFailure → llmclient.RecordFailure) across
// separate Actor instances to prove the cooldown state is process-global —
// the core invariant of the llmclient health refactor. The 1.5× backoff
// sequence itself (30s→45s→67.5s→…→15m cap, 429/Retry-After, quota, disabled
// persistence) is asserted in pkg/llmclient/health_test.go; the disabled
// cross-restart persistence bridge is asserted in
// pkg/actor/aimanager/aimanager_health_test.go.
// ──────────────────────────────────────────────────────────────────────────────

// resetGlobalHealth replaces the process-wide DefaultProviderHealth with a
// fresh instance (preserving the current policy) so each integration test
// starts from a clean slate, mirroring the var-swap test-isolation pattern
// used by the llmclient and aimanager packages. No test in this package uses
// t.Parallel, so the swap is race-free.
func resetGlobalHealth(t *testing.T) {
	t.Helper()
	prev := llmclient.DefaultProviderHealth.Policy()
	llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()
	llmclient.DefaultProviderHealth.SetCooldownPolicy(prev)
	t.Cleanup(func() {
		llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()
		llmclient.DefaultProviderHealth.SetCooldownPolicy(prev)
	})
}

// TestGlobalConsistency_TwoAggregatorsShareCooldown is the acceptance test for
// item 2: two aggregators selecting the same provider::model unit; one
// triggers a cooldown and the other must see it immediately — no per-actor
// mirror, no eventual propagation. The write goes through the dispatch failure
// path (applyStreamOpenFailure) on aggregator A; the read goes through
// selectUnit on a completely separate aggregator B.
func TestGlobalConsistency_TwoAggregatorsShareCooldown(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("g-agg-a") })
	t.Cleanup(func() { clearAggHealth("g-agg-b") })

	// Shrink cooldowns so expiry is testable within the test.
	orig := llmclient.DefaultProviderHealth.Policy()
	llmclient.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: 1,
		BaseCooldown:          30 * time.Millisecond,
		BackoffFactor:         1.5,
		MaxCooldown:           time.Second,
		RateLimitFloor:        50 * time.Millisecond,
		RateLimitCeiling:      time.Second,
		QuotaCooldown:         100 * time.Millisecond,
	})
	t.Cleanup(func() { llmclient.SetCooldownPolicy(orig) })

	shared := CallableUnit{ID: "shared::m", Model: "m", ProviderName: "shared"}
	alt := CallableUnit{ID: "alt::m2", Model: "m2", ProviderName: "alt"}
	aggA := &Actor{id: "g-agg-a", lifecycleCtx: context.Background(), units: []CallableUnit{shared}, strategy: NewFallbackStrategy(), aimanagerRef: &fakeRef{}}
	aggB := &Actor{id: "g-agg-b", units: []CallableUnit{shared, alt}, strategy: NewFallbackStrategy()}

	// Aggregator A hits a 429 on the shared unit via the dispatch write path.
	err429 := &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}
	aggA.applyStreamOpenFailure(&shared, err429, llmclient.ClassifyStreamOpenError(err429))

	// Aggregator B — a separate actor, no shared state besides the process
	// health layer — must refuse the cooled unit immediately.
	if chosen, err := aggB.selectUnit(SelectRequest{Unit: domain.ModelUnit{Provider: "shared", Model: "m"}}); err == nil {
		t.Fatalf("aggregator B must not select the cooled unit, got %s", chosen.ID)
	}

	// Auto-selection falls through to the healthy alternative.
	chosen, err := aggB.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("aggregator B auto-select: %v", err)
	}
	if chosen.ID != "alt::m2" {
		t.Fatalf("aggregator B auto-select = %q, want alt::m2 (cooled shared unit skipped)", chosen.ID)
	}

	// Once the cooldown expires, the unit is selectable again.
	time.Sleep(120 * time.Millisecond)
	chosen, err = aggB.selectUnit(SelectRequest{Unit: domain.ModelUnit{Provider: "shared", Model: "m"}})
	if err != nil {
		t.Fatalf("aggregator B select after cooldown expiry: %v", err)
	}
	if chosen.ID != "shared::m" {
		t.Fatalf("aggregator B select after expiry = %q, want shared::m", chosen.ID)
	}
}

// countingErrorClient is an llmclient.Client whose Stream always fails with
// streamErr, counting every open attempt.
type countingErrorClient struct {
	attempts  *atomic.Int32
	streamErr error
}

func (c *countingErrorClient) Stream(_ context.Context, _ llmclient.Request) (llmclient.Stream, error) {
	c.attempts.Add(1)
	return nil, c.streamErr
}

// TestDispatchFailover_NoRetryWithinDispatch_CooldownAcrossDispatch is the
// failover regression (item 3): within one dispatch the failed unit is not
// retried (triedIDs) — the loop rotates to the next candidate exactly once —
// and across dispatches the global cooldown excludes the failed unit from the
// very first attempt.
func TestDispatchFailover_NoRetryWithinDispatch_CooldownAcrossDispatch(t *testing.T) {
	resetGlobalHealth(t)
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	var attemptsA atomic.Int32
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail429",
		Factory: func(_, _ string) llmclient.Client {
			return &countingErrorClient{attempts: &attemptsA, streamErr: &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory:  func(_, _ string) llmclient.Client { return &okClient{} },
	})
	regValue := *reg

	a := &Actor{
		id: "test-agg",
		units: []CallableUnit{
			{ID: "a::m1", Model: "m1", ProviderName: "a", Protocol: "fail429"},
			{ID: "b::m2", Model: "m2", ProviderName: "b", Protocol: "ok"},
		},
		strategy:     NewFallbackStrategy(),
		registry:     regValue,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	ctx := &testPureCtx{done: make(chan struct{})}

	// First dispatch: a::m1 fails with 429, the loop rotates to b::m2 exactly
	// once (triedIDs prevents a retry of a::m1), and the stream opens.
	emit := &collectingEmitter{done: make(chan struct{})}
	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	ru := emit.resolvedUnit()
	if ru == nil || ru.Provider != "b" || ru.Model != "m2" {
		t.Fatalf("first dispatch resolved unit = %+v, want b/m2", ru)
	}
	if got := attemptsA.Load(); got != 1 {
		t.Fatalf("a::m1 attempted %d times within the dispatch, want exactly 1 (triedIDs)", got)
	}

	// Second dispatch (fresh candidate loop): a::m1 is now globally cooled by
	// the 429, so it is excluded before the first attempt — b::m2 is picked
	// directly and a::m1 is never touched.
	emit2 := &collectingEmitter{done: make(chan struct{})}
	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit2); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	ru2 := emit2.resolvedUnit()
	if ru2 == nil || ru2.Provider != "b" || ru2.Model != "m2" {
		t.Fatalf("second dispatch resolved unit = %+v, want b/m2", ru2)
	}
	if got := attemptsA.Load(); got != 1 {
		t.Fatalf("a::m1 attempted %d times across dispatches, want still 1 (global cooldown excluded it)", got)
	}
}

// TestSelectUnitExcluding_TriedIDsExcluded verifies the per-dispatch exclusion
// set directly: a unit whose ID is in excludeIDs is never returned, while an
// untried unit is.
func TestSelectUnitExcluding_TriedIDsExcluded(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("tried-agg") })

	a := &Actor{
		id: "tried-agg",
		units: []CallableUnit{
			{ID: "x::m1", Model: "m1", ProviderName: "x"},
			{ID: "y::m2", Model: "m2", ProviderName: "y"},
		},
		strategy: NewFallbackStrategy(),
	}

	chosen, err := a.selectUnitExcluding(SelectRequest{}, map[string]bool{"x::m1": true})
	if err != nil {
		t.Fatalf("selectUnitExcluding: %v", err)
	}
	if chosen.ID != "y::m2" {
		t.Fatalf("chosen = %q, want y::m2 (x::m1 excluded as tried)", chosen.ID)
	}

	// Excluding the only candidate leaves no match.
	if _, err := a.selectUnitExcluding(SelectRequest{}, map[string]bool{"x::m1": true, "y::m2": true}); err == nil {
		t.Fatal("expected no-match when every candidate is tried")
	}

	// Nil exclude map behaves like an empty map.
	chosen, err = a.selectUnitExcluding(SelectRequest{}, nil)
	if err != nil {
		t.Fatalf("selectUnitExcluding with nil map: %v", err)
	}
	if chosen.ID != "x::m1" {
		t.Fatalf("chosen = %q, want x::m1 (pool order)", chosen.ID)
	}
}

// TestHandleStatus_ProjectsHealthFields is the aggregator status protocol
// regression (item 4): the response joins the global health snapshot into
// AICallableUnitView fields (HealthState/HealthReason/RecoveryMode/
// CooldownUntil/ConsecutiveFailures), surfaces non-pool (on-demand) unhealthy
// pairs, and normalizes an expired cooldown back to healthy.
func TestHandleStatus_ProjectsHealthFields(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("status-agg") })

	a := &Actor{
		id: "status-agg",
		units: []CallableUnit{
			{ID: "cool::m1", Model: "m1", ProviderName: "cool"},
			{ID: "dis::m2", Model: "m2", ProviderName: "dis"},
			{ID: "ok::m3", Model: "m3", ProviderName: "ok"},
		},
		strategy: NewFallbackStrategy(),
	}

	// Default policy: rate-limit floor 60s, so the cooling unit stays cooling
	// for the whole test.
	llmclient.RecordFailure("cool", "m1", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 90 * time.Second})
	llmclient.RecordFailure("dis", "m2", &llmclient.UpstreamError{StatusCode: 401})
	// 3 availability failures on a pool unit: below threshold → healthy, but
	// the counter is projected.
	llmclient.RecordFailure("ok", "m3", &llmclient.UpstreamError{StatusCode: 503})
	llmclient.RecordFailure("ok", "m3", &llmclient.UpstreamError{StatusCode: 503})
	llmclient.RecordFailure("ok", "m3", &llmclient.UpstreamError{StatusCode: 503})
	// Non-pool pair cooled → surfaced as an on-demand view.
	llmclient.RecordFailure("ghost", "g-model", &llmclient.UpstreamError{StatusCode: 429})

	resp, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if resp.ID != "status-agg" || resp.UnitCount != 3 {
		t.Fatalf("resp.ID=%q UnitCount=%d, want status-agg/3", resp.ID, resp.UnitCount)
	}

	byID := make(map[string]domain.AICallableUnitView, len(resp.Units))
	for _, u := range resp.Units {
		byID[u.ID] = u
	}

	cool, ok := byID["cool::m1"]
	if !ok {
		t.Fatal("cool::m1 missing from status units")
	}
	if cool.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("cool::m1 HealthState = %q, want cooling_down", cool.HealthState)
	}
	if cool.HealthReason != llmclient.HealthReasonRateLimit {
		t.Errorf("cool::m1 HealthReason = %q, want rate_limit", cool.HealthReason)
	}
	if cool.RecoveryMode != "cooldown" {
		t.Errorf("cool::m1 RecoveryMode = %q, want cooldown", cool.RecoveryMode)
	}
	if cool.CooldownUntil <= time.Now().Unix() {
		t.Errorf("cool::m1 CooldownUntil = %d, want future deadline", cool.CooldownUntil)
	}
	if cool.CooldownReason != llmclient.HealthReasonRateLimit {
		t.Errorf("cool::m1 CooldownReason = %q, want rate_limit", cool.CooldownReason)
	}

	dis, ok := byID["dis::m2"]
	if !ok {
		t.Fatal("dis::m2 missing from status units")
	}
	if dis.HealthState != llmclient.HealthStateDisabled {
		t.Errorf("dis::m2 HealthState = %q, want disabled", dis.HealthState)
	}
	if dis.HealthReason != llmclient.HealthReasonAuthenticationFailed {
		t.Errorf("dis::m2 HealthReason = %q, want authentication_failed", dis.HealthReason)
	}
	if dis.RecoveryMode != "manual_or_balance_refresh" {
		t.Errorf("dis::m2 RecoveryMode = %q, want manual_or_balance_refresh", dis.RecoveryMode)
	}
	if dis.CooldownUntil != 0 {
		t.Errorf("dis::m2 CooldownUntil = %d, want 0 (disabled has no deadline)", dis.CooldownUntil)
	}

	okU, ok := byID["ok::m3"]
	if !ok {
		t.Fatal("ok::m3 missing from status units")
	}
	if okU.HealthState != llmclient.HealthStateHealthy {
		t.Errorf("ok::m3 HealthState = %q, want %q (pre-refactor projection also returned healthy)", okU.HealthState, llmclient.HealthStateHealthy)
	}
	if okU.ConsecutiveFailures != 3 {
		t.Errorf("ok::m3 ConsecutiveFailures = %d, want 3", okU.ConsecutiveFailures)
	}

	// The on-demand view: a cooled pair that is not in the pool must appear
	// with OnDemand=true so operators can see why a unit-kind ref is blocked.
	od, ok := byID["ghost::g-model"]
	if !ok {
		t.Fatal("on-demand cooled pair ghost::g-model missing from status units")
	}
	if !od.OnDemand {
		t.Error("ghost::g-model must be flagged OnDemand")
	}
	if od.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("ghost::g-model HealthState = %q, want cooling_down", od.HealthState)
	}
}

// TestHandleStatus_ExpiredCooldownProjectsHealthy verifies the status
// projection normalizes a cooldown whose deadline has passed back to healthy
// (and drops the on-demand surface of an expired pair).
func TestHandleStatus_ExpiredCooldownProjectsHealthy(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("expire-agg") })

	orig := llmclient.DefaultProviderHealth.Policy()
	llmclient.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: 1,
		BaseCooldown:          time.Millisecond,
		BackoffFactor:         1.5,
		MaxCooldown:           time.Second,
		RateLimitFloor:        30 * time.Millisecond,
		RateLimitCeiling:      time.Second,
		QuotaCooldown:         time.Millisecond,
	})
	t.Cleanup(func() { llmclient.SetCooldownPolicy(orig) })

	a := &Actor{
		id: "expire-agg",
		units: []CallableUnit{
			{ID: "e::m", Model: "m", ProviderName: "e"},
		},
		strategy: NewFallbackStrategy(),
	}
	llmclient.RecordFailure("e", "m", &llmclient.UpstreamError{StatusCode: 429})
	time.Sleep(80 * time.Millisecond)

	resp, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if len(resp.Units) != 1 {
		t.Fatalf("len(units) = %d, want 1 (expired pair must not surface as on-demand)", len(resp.Units))
	}
	u := resp.Units[0]
	if u.HealthState != llmclient.HealthStateHealthy {
		t.Errorf("expired cooldown must project healthy, got HealthState=%q", u.HealthState)
	}
	if u.CooldownUntil != 0 {
		t.Errorf("expired cooldown must project no deadline, got %d", u.CooldownUntil)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// On-demand path (item 8a coverage gap): the old ondemand_test.go was deleted
// wholesale; resolveOnDemandUnit/selectOnDemandUnit now have no direct test.
// The construction/guard/fallback behavior below is the minimal equivalent of
// the deleted TestResolveOnDemandUnit_* and TestSelectUnit_SystemAggregator*
// scenarios.
// ──────────────────────────────────────────────────────────────────────────────

// TestOnDemandResolve_ConstructionAndFallback covers on-demand unit
// construction from the aimanager resolve response, the provider/model guards,
// not-found handling, the system-aggregator selectUnit fallback, and the
// pool-match-preference order.
func TestOnDemandResolve_ConstructionAndFallback(t *testing.T) {
	resetGlobalHealth(t)

	ref := &fakeRef{results: map[string]any{
		"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
			Endpoint:         "https://api.example.com",
			Protocol:         "openai",
			Modality:         "chat",
			MaxConcurrency:   4,
			MaxContextLength: 128000,
		},
	}}
	sys := &Actor{id: systemAggregatorID, lifecycleCtx: context.Background(), aimanagerRef: ref}

	cu, err := sys.resolveOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-5"})
	if err != nil {
		t.Fatalf("resolveOnDemandUnit: %v", err)
	}
	if cu.ID != "openai::gpt-5" || cu.Endpoint != "https://api.example.com" || cu.Protocol != "openai" {
		t.Errorf("constructed unit = %+v, want openai::gpt-5 / example endpoint / openai protocol", cu)
	}
	if !cu.OnDemand {
		t.Error("expected OnDemand=true (pool must not be polluted)")
	}
	if cu.MaxConcurrency != 4 || cu.MaxContextLength != 128000 {
		t.Errorf("MaxConcurrency=%d MaxContextLength=%d, want 4/128000", cu.MaxConcurrency, cu.MaxContextLength)
	}
	inferred, err := sys.resolveOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "deepseek-r1"})
	if err != nil {
		t.Fatalf("resolveOnDemandUnit reasoning model: %v", err)
	}
	if !inferred.IsReasoning {
		t.Error("reasoning model must be marked IsReasoning")
	}

	// Guards: both provider and model are required.
	if _, err := sys.resolveOnDemandUnit(domain.ModelUnit{Model: "gpt-5"}); err == nil {
		t.Fatal("expected error when provider is missing")
	}
	if _, err := sys.resolveOnDemandUnit(domain.ModelUnit{Provider: "openai"}); err == nil {
		t.Fatal("expected error when model is missing")
	}

	// Not found: empty resolve response errors.
	notFound := &fakeRef{results: map[string]any{
		"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{},
	}}
	sysNF := &Actor{id: systemAggregatorID, lifecycleCtx: context.Background(), aimanagerRef: notFound}
	if _, err := sysNF.resolveOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "ghost"}); err == nil {
		t.Fatal("expected error when model not found by aimanager")
	}

	// System aggregator selectUnit falls back to on-demand for a pair absent
	// from the pool.
	sysSel := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: ref,
		strategy:     NewRoundRobinStrategy(),
		units:        []CallableUnit{{ID: "anthropic::claude", Model: "claude", ProviderName: "anthropic"}},
	}
	got, err := sysSel.selectUnit(SelectRequest{Unit: domain.ModelUnit{Provider: "openai", Model: "gpt-5"}})
	if err != nil {
		t.Fatalf("selectUnit on-demand fallback: %v", err)
	}
	if got.ID != "openai::gpt-5" || !got.OnDemand {
		t.Errorf("selected %+v, want on-demand openai::gpt-5", got)
	}

	// Pool match is preferred over on-demand resolution: with the pair in the
	// pool, resolve_model is never reached (fakeRef has no entry → error).
	sysPool := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: &fakeRef{},
		strategy:     NewFallbackStrategy(),
		units:        []CallableUnit{{ID: "openai::gpt-5", Model: "gpt-5", ProviderName: "openai"}},
	}
	got, err = sysPool.selectUnit(SelectRequest{Unit: domain.ModelUnit{Provider: "openai", Model: "gpt-5"}})
	if err != nil {
		t.Fatalf("selectUnit pool match: %v", err)
	}
	if got.ID != "openai::gpt-5" || got.OnDemand {
		t.Errorf("selected %+v, want pool unit openai::gpt-5 (not on-demand)", got)
	}
}

// TestOnDemandFailure_RecordsGlobalHealth pins the changed on-demand semantic
// (item 8a): the old implementation kept on-demand cooldowns in a per-actor
// map that never blocked dispatch and was invisible to other aggregators; the
// new implementation records on-demand failures into the process-global health
// layer, so the same provider::model pair becomes unavailable everywhere — the
// pool of any other aggregator included.
func TestOnDemandFailure_RecordsGlobalHealth(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("od-agg") })

	od := &CallableUnit{ID: "openai::gpt-5", Model: "gpt-5", ProviderName: "openai", OnDemand: true}
	err429 := &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}
	agg := &Actor{id: "od-agg", lifecycleCtx: context.Background(), aimanagerRef: &fakeRef{}, strategy: NewFallbackStrategy()}
	agg.applyStreamOpenFailure(od, err429, llmclient.ClassifyStreamOpenError(err429))

	// The pair is recorded in the global snapshot even though it was never in
	// any pool.
	snap := llmclient.HealthSnapshot()
	e, ok := snap["openai::gpt-5"]
	if !ok {
		t.Fatal("on-demand failure must be recorded in the global health snapshot")
	}
	if e.State != llmclient.HealthStateCoolingDown || e.Reason != llmclient.HealthReasonRateLimit {
		t.Fatalf("snapshot entry = %+v, want cooling_down/rate_limit", e)
	}

	// The same pair in another aggregator's pool is now unavailable.
	other := &Actor{
		id:       "other-agg",
		units:    []CallableUnit{{ID: "openai::gpt-5", Model: "gpt-5", ProviderName: "openai"}},
		strategy: NewFallbackStrategy(),
	}
	if chosen, err := other.selectUnit(SelectRequest{Unit: domain.ModelUnit{Provider: "openai", Model: "gpt-5"}}); err == nil {
		t.Fatalf("on-demand failure must cool the pair for pool selection too, got %s", chosen.ID)
	}
}

// TestStrategyResolution_UsesConfiguredNames covers the strategy-name
// resolution removed with the old strategy_test.go. It keeps the wire aliases
// and the smart/standard paths from silently degrading to round-robin.
func TestStrategyResolution_UsesConfiguredNames(t *testing.T) {
	a := &Actor{}
	tests := []struct {
		name string
		want any
	}{
		{name: "fallback", want: &FallbackStrategy{}},
		{name: "priority", want: &FallbackStrategy{}},
		{name: "round-robin", want: &RoundRobinStrategy{}},
		{name: "round_robin", want: &RoundRobinStrategy{}},
		{name: "standard", want: &StandardStrategy{}},
		{name: "smart", want: &SmartStrategy{}},
		{name: "unknown", want: &RoundRobinStrategy{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := a.resolveStrategy(tc.name)
			switch tc.want.(type) {
			case *FallbackStrategy:
				if _, ok := got.(*FallbackStrategy); !ok {
					t.Fatalf("resolveStrategy(%q) = %T, want *FallbackStrategy", tc.name, got)
				}
			case *RoundRobinStrategy:
				if _, ok := got.(*RoundRobinStrategy); !ok {
					t.Fatalf("resolveStrategy(%q) = %T, want *RoundRobinStrategy", tc.name, got)
				}
			case *StandardStrategy:
				if _, ok := got.(*StandardStrategy); !ok {
					t.Fatalf("resolveStrategy(%q) = %T, want *StandardStrategy", tc.name, got)
				}
			case *SmartStrategy:
				if _, ok := got.(*SmartStrategy); !ok {
					t.Fatalf("resolveStrategy(%q) = %T, want *SmartStrategy", tc.name, got)
				}
			default:
				t.Fatalf("unsupported expected strategy type %T", tc.want)
			}
		})
	}
}

// TestSelectUnit_ComposesHealthAndTokenPlanFilters proves that health
// availability and token-plan exhaustion are both applied before strategy
// selection; a healthy, non-exhausted unit wins over either filtered unit.
func TestSelectUnit_ComposesHealthAndTokenPlanFilters(t *testing.T) {
	resetGlobalHealth(t)

	llmclient.RecordFailure("cool", "m", &llmclient.UpstreamError{StatusCode: 429})
	a := &Actor{
		units: []CallableUnit{
			{ID: "cool::m", Model: "m", ProviderName: "cool"},
			{ID: "exhausted::m", Model: "m", ProviderName: "exhausted", IsTokenPlan: true, TokenPlanRemainingPct: 0},
			{ID: "healthy::m", Model: "m", ProviderName: "healthy", IsTokenPlan: true, TokenPlanRemainingPct: 80},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("composed filtering selectUnit: %v", err)
	}
	if chosen.ID != "healthy::m" {
		t.Fatalf("selected %q, want healthy::m after health/token-plan filters", chosen.ID)
	}
}

// TestHandleStatus_AggregatorRefEntry_ProjectsRegistryHealth verifies that
// status handles pool entries with AggregatorID != "" via the aggregator-level
// health registry (IsAggregatorAvailable) instead of the unit snapshot join.
// Three sub-cases:
//   - available child (never registered) → healthy + AggregatorID populated;
//   - unavailable child (registered unavailable) → disabled + cooldown deadline;
//   - a direct unit in the same pool is unaffected by the ref entry's health.
func TestHandleStatus_AggregatorRefEntry_ProjectsRegistryHealth(t *testing.T) {
	resetGlobalHealth(t)
	t.Cleanup(func() { clearAggHealth("agg-up") })
	t.Cleanup(func() { clearAggHealth("agg-down") })

	a := &Actor{
		id: "status-parent",
		units: []CallableUnit{
			{ID: "agg:agg-up", AggregatorID: "agg-up"},
			{ID: "agg:agg-down", AggregatorID: "agg-down"},
			{ID: "concrete::m", Model: "m", ProviderName: "concrete"},
		},
		strategy: NewFallbackStrategy(),
	}

	// Register agg-down as unavailable; agg-up is left unregistered (available).
	llmclient.RegisterAggregatorHealth("agg-down", llmclient.AggregatorHealthUnavailable)
	// Make the concrete unit cooling so we prove it's read from the unit snapshot.
	llmclient.RecordFailure("concrete", "m", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 90 * time.Second})

	resp, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if resp.UnitCount != 3 {
		t.Fatalf("UnitCount = %d, want 3", resp.UnitCount)
	}

	byID := make(map[string]domain.AICallableUnitView, len(resp.Units))
	for _, u := range resp.Units {
		byID[u.ID] = u
	}

	// Available aggregator-ref: healthy, AggregatorID populated.
	up, ok := byID["agg:agg-up"]
	if !ok {
		t.Fatal("agg:agg-up missing from status units")
	}
	if up.AggregatorID != "agg-up" {
		t.Errorf("agg:agg-up AggregatorID = %q, want agg-up", up.AggregatorID)
	}
	if up.HealthState != llmclient.HealthStateHealthy {
		t.Errorf("agg:agg-up HealthState = %q, want %q", up.HealthState, llmclient.HealthStateHealthy)
	}
	if up.RecoveryMode != "" {
		t.Errorf("agg:agg-up RecoveryMode = %q, want empty", up.RecoveryMode)
	}
	if up.CooldownUntil != 0 {
		t.Errorf("agg:agg-up CooldownUntil = %d, want 0", up.CooldownUntil)
	}

	// Unavailable aggregator-ref: disabled, cooldown deadline, recovery=cooldown.
	down, ok := byID["agg:agg-down"]
	if !ok {
		t.Fatal("agg:agg-down missing from status units")
	}
	if down.AggregatorID != "agg-down" {
		t.Errorf("agg:agg-down AggregatorID = %q, want agg-down", down.AggregatorID)
	}
	if down.HealthState != llmclient.HealthStateDisabled {
		t.Errorf("agg:agg-down HealthState = %q, want %q", down.HealthState, llmclient.HealthStateDisabled)
	}
	if down.RecoveryMode != "cooldown" {
		t.Errorf("agg:agg-down RecoveryMode = %q, want cooldown", down.RecoveryMode)
	}
	if down.CooldownUntil <= time.Now().Unix() {
		t.Errorf("agg:agg-down CooldownUntil = %d, want future deadline", down.CooldownUntil)
	}

	// The concrete unit must still read its health from the unit snapshot.
	cu, ok := byID["concrete::m"]
	if !ok {
		t.Fatal("concrete::m missing from status units")
	}
	if cu.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("concrete::m HealthState = %q, want %q", cu.HealthState, llmclient.HealthStateCoolingDown)
	}
	if cu.AggregatorID != "" {
		t.Errorf("concrete::m AggregatorID = %q, want empty", cu.AggregatorID)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// On-demand cooldown exclusion (item 3 fix coverage): after an on-demand unit
// fails and enters cooling_down, selectOnDemandUnit must return noMatchError
// instead of re-resolving the same cooling-down pair. The fix at actor.go:753
// added a unitAvailable check inside selectOnDemandUnit; before the fix, the
// method would construct the CallableUnit via resolveOnDemandUnit and return
// it even though the pair was globally cooled, causing doomed re-dispatch.
// ──────────────────────────────────────────────────────────────────────────────

// TestSelectOnDemandUnit_CoolingDownReturnsNoMatch verifies that
// selectOnDemandUnit refuses to return a unit that is currently in
// cooling_down. The test simulates the on-demand failure path
// (applyStreamOpenFailure → RecordFailure), then calls selectOnDemandUnit
// for the same pair and asserts a no-match error rather than the cooled unit.
func TestSelectOnDemandUnit_CoolingDownReturnsNoMatch(t *testing.T) {
	resetGlobalHealth(t)

	ref := &fakeRef{results: map[string]any{
		"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
			Endpoint:         "https://api.example.com",
			Protocol:         "openai",
			Modality:         "chat",
			MaxConcurrency:   4,
			MaxContextLength: 128000,
		},
	}}
	sys := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: ref,
	}

	// Verify the pair is resolvable when healthy.
	unit, err := sys.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-5"}, false)
	if err != nil {
		t.Fatalf("healthy selectOnDemandUnit: %v", err)
	}
	if unit.ID != "openai::gpt-5" {
		t.Fatalf("healthy unit ID = %q, want openai::gpt-5", unit.ID)
	}

	// Simulate an on-demand failure: applyStreamOpenFailure records the 429
	// to the global health layer. Default policy RateLimitFloor=60s ensures
	// the unit stays cooling_down for the entire test.
	od := &CallableUnit{ID: "openai::gpt-5", Model: "gpt-5", ProviderName: "openai", OnDemand: true}
	err429 := &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}
	sys.applyStreamOpenFailure(od, err429, llmclient.ClassifyStreamOpenError(err429))

	// Confirm the pair is cooling_down in the global snapshot.
	snap := llmclient.HealthSnapshot()
	s, ok := snap["openai::gpt-5"]
	if !ok {
		t.Fatal("on-demand failure not recorded in global health snapshot")
	}
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("state = %q, want cooling_down", s.State)
	}

	// selectOnDemandUnit must now refuse the cooled pair, returning
	// noMatchError — not re-resolving and returning the cooling-down unit.
	_, err = sys.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-5"}, false)
	if err == nil {
		t.Fatal("selectOnDemandUnit must return an error for a cooling-down on-demand pair, got nil")
	}
	// The error should be a no-match error, not a panic or a different type.
	if !strings.Contains(err.Error(), "no callable unit") {
		t.Fatalf("error = %q, want a no-match error containing 'no callable unit'", err.Error())
	}
}

// TestSelectOnDemandUnit_DisabledReturnsNoMatch verifies the same exclusion for
// a disabled (401 auth failure) on-demand unit.
func TestSelectOnDemandUnit_DisabledReturnsNoMatch(t *testing.T) {
	resetGlobalHealth(t)

	ref := &fakeRef{results: map[string]any{
		"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
			Endpoint: "https://api.example.com",
			Protocol: "openai",
			Modality: "chat",
		},
	}}
	sys := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: ref,
	}

	// Healthy resolution succeeds.
	if _, err := sys.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-5"}, false); err != nil {
		t.Fatalf("healthy selectOnDemandUnit: %v", err)
	}

	// 401 → disabled.
	od := &CallableUnit{ID: "openai::gpt-5", Model: "gpt-5", ProviderName: "openai", OnDemand: true}
	err401 := &llmclient.UpstreamError{StatusCode: 401, Message: "invalid api key"}
	sys.applyStreamOpenFailure(od, err401, llmclient.ClassifyStreamOpenError(err401))

	snap := llmclient.HealthSnapshot()
	s := snap["openai::gpt-5"]
	if s.State != llmclient.HealthStateDisabled {
		t.Fatalf("state = %q, want disabled", s.State)
	}

	// Disabled on-demand pair must be refused.
	_, err := sys.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-5"}, false)
	if err == nil {
		t.Fatal("selectOnDemandUnit must return an error for a disabled on-demand pair, got nil")
	}
	if !strings.Contains(err.Error(), "no callable unit") {
		t.Fatalf("error = %q, want a no-match error containing 'no callable unit'", err.Error())
	}
}
