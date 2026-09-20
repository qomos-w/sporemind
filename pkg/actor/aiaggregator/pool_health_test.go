package aiaggregator

import (
	"testing"
	"time"

	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// ──────────────────────────────────────────────────────────────────────────────
// Aggregator-level pool health: self-reporting + parent-side exclusion
// ──────────────────────────────────────────────────────────────────────────────

// coolUnit puts the given provider::model unit into an immediate rate-limit
// cooldown (60s with the default policy).
func coolUnit(t *testing.T, provider, model string) {
	t.Helper()
	llmclient.RecordFailure(provider, model, &llmclient.UpstreamError{StatusCode: 429})
}

// clearAggHealth clears the aggregator-level registration for id, restoring
// the shared registry to a clean state for other tests.
func clearAggHealth(id string) {
	llmclient.RegisterAggregatorHealth(id, llmclient.AggregatorHealthAvailable)
}

// TestReportOwnPoolHealth_AllUnitsUnavailableRegisters verifies that a child
// aggregator whose whole pool is cooling/disabled registers itself as
// unavailable in the llmclient aggregator registry after a failed selection.
func TestReportOwnPoolHealth_AllUnitsUnavailableRegisters(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-pool") })

	a := &Actor{
		id: "child-pool",
		units: []CallableUnit{
			{ID: "pa::m1", Model: "m1", ProviderName: "pa"},
			{ID: "pb::m2", Model: "m2", ProviderName: "pb"},
		},
		strategy: NewFallbackStrategy(),
	}
	coolUnit(t, "pa", "m1")
	coolUnit(t, "pb", "m2")

	_, err := a.selectUnit(SelectRequest{})
	if err == nil {
		t.Fatal("expected selection to fail with the whole pool cooling")
	}

	snap := llmclient.AggregatorHealthSnapshot()
	e, ok := snap["child-pool"]
	if !ok {
		t.Fatalf("expected child-pool registered unavailable, snapshot: %+v", snap)
	}
	if e.State != llmclient.AggregatorHealthUnavailable {
		t.Errorf("state = %s, want %s", e.State, llmclient.AggregatorHealthUnavailable)
	}
}

// TestReportOwnPoolHealth_EmptyPoolRegisters verifies that a named aggregator
// with no servable units registers itself unavailable — an empty config-loaded
// pool can never open a stream.
func TestReportOwnPoolHealth_EmptyPoolRegisters(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-empty") })

	a := &Actor{id: "child-empty", strategy: NewFallbackStrategy()}
	_, err := a.selectUnit(SelectRequest{})
	if err == nil {
		t.Fatal("expected selection to fail on an empty pool")
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["child-empty"]; !ok {
		t.Fatal("expected child-empty registered unavailable")
	}
}

// TestDispatch_DisabledAggregatorRegistersUnavailable verifies the operator
// policy path: a disabled aggregator must register itself unavailable on the
// fail-fast early return, so parents skip it instead of re-probing at dispatch
// rate. Without this, the disabled flag never reaches the health registry
// because reportOwnPoolHealth only fires inside selectUnit.
func TestDispatch_DisabledAggregatorRegistersUnavailable(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("agg-disabled") })

	deep := domain.ModelUnit{Model: "dm", Provider: "dp"}
	childAID, _ := canonicalAID(t, 11)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "unused"),
	}}
	units := []CallableUnit{
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "agg-disabled", units, "", childAID, planner, dualRegistry(nil))
	a.mu.Lock()
	a.disabled = true
	a.mu.Unlock()

	err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit)
	if err == nil {
		t.Fatal("expected dispatch to fail fast on a disabled aggregator")
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["agg-disabled"]; !ok {
		t.Fatal("expected agg-disabled registered unavailable after fail-fast dispatch")
	}
}

// TestReportOwnPoolHealth_SystemAggregatorNeverReports verifies that the
// system aggregator (which retains on-demand resolution fallback) never
// registers itself in the aggregator registry.
func TestReportOwnPoolHealth_SystemAggregatorNeverReports(t *testing.T) {
	a := &Actor{
		id: systemAggregatorID,
		units: []CallableUnit{
			{ID: "sys-pa::m1", Model: "m1", ProviderName: "sys-pa"},
		},
		strategy: NewFallbackStrategy(),
	}
	coolUnit(t, "sys-pa", "m1")
	a.reportOwnPoolHealth()
	if _, ok := llmclient.AggregatorHealthSnapshot()[systemAggregatorID]; ok {
		t.Errorf("system aggregator must never self-report, snapshot: %+v", llmclient.AggregatorHealthSnapshot())
	}
}

// TestReportOwnPoolHealth_RecoveryClears verifies that once any unit in the
// pool becomes available again, a previously registered unavailable state is
// cleared and selection succeeds (cooldown expiry path).
func TestReportOwnPoolHealth_RecoveryClears(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-recover") })

	// Shrink the rate-limit cooldown so the unit recovers within the test.
	origPolicy := llmclient.DefaultProviderHealth.Policy()
	t.Cleanup(func() { llmclient.SetCooldownPolicy(origPolicy) })
	llmclient.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: 1,
		BaseCooldown:          time.Millisecond,
		BackoffFactor:         1.5,
		MaxCooldown:           time.Second,
		RateLimitFloor:        100 * time.Millisecond,
		RateLimitCeiling:      time.Second,
		QuotaCooldown:         time.Millisecond,
	})

	a := &Actor{
		id: "child-recover",
		units: []CallableUnit{
			{ID: "rc::m", Model: "m", ProviderName: "rc"},
		},
		strategy: NewFallbackStrategy(),
	}
	coolUnit(t, "rc", "m")

	// Pool fully down → selection fails → registers unavailable.
	_, err := a.selectUnit(SelectRequest{})
	if err == nil {
		t.Fatal("expected selection to fail while the unit is cooling")
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["child-recover"]; !ok {
		t.Fatal("expected child-recover registered unavailable while pool down")
	}

	// Cooldown expires → the unit is selectable again → registration cleared.
	time.Sleep(250 * time.Millisecond)
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selection after recovery: %v", err)
	}
	if chosen.ID != "rc::m" {
		t.Errorf("chosen = %q, want rc::m", chosen.ID)
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["child-recover"]; ok {
		t.Error("expected registration cleared after pool recovery")
	}
}

// TestSelectUnit_SkipsUnavailableAggregatorRef verifies that the parent
// selection hard-skips an aggregator-ref entry whose child pool is registered
// unavailable, falling through to the next candidate without dispatching.
func TestSelectUnit_SkipsUnavailableAggregatorRef(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-skip") })
	llmclient.RegisterAggregatorHealth("child-skip", llmclient.AggregatorHealthUnavailable)

	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child-skip", AggregatorID: "child-skip"},
			{ID: "a::m", Model: "m", ProviderName: "a"},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if chosen.ID != "a::m" {
		t.Errorf("chosen = %q, want a::m (unavailable child must be skipped)", chosen.ID)
	}
	// The parent's selection must not have touched the child registration:
	// no dispatch was sent, so the snapshot still reports it unavailable.
	snap := llmclient.AggregatorHealthSnapshot()
	if e, ok := snap["child-skip"]; !ok || e.State != llmclient.AggregatorHealthUnavailable {
		t.Errorf("child-skip registration must remain unavailable after parent selection, snapshot: %+v", snap)
	}
}

// TestSelectUnitExcluding_SkipsUnavailableAggregatorRef verifies the failover
// selector also skips an unavailable child ref and returns the next candidate.
func TestSelectUnitExcluding_SkipsUnavailableAggregatorRef(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-skip2") })
	llmclient.RegisterAggregatorHealth("child-skip2", llmclient.AggregatorHealthUnavailable)

	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child-skip2", AggregatorID: "child-skip2"},
			{ID: "b::m", Model: "m", ProviderName: "b"},
		},
		strategy: NewFallbackStrategy(),
	}
	chosen, err := a.selectUnitExcluding(SelectRequest{}, map[string]bool{})
	if err != nil {
		t.Fatalf("selectUnitExcluding: %v", err)
	}
	if chosen.ID != "b::m" {
		t.Errorf("chosen = %q, want b::m (unavailable child must be skipped in failover too)", chosen.ID)
	}
}

// TestSelectUnit_UnavailableChildOnlyFailsFast verifies that a parent whose
// only candidate is an unavailable child ref fails fast — no nested dispatch
// is attempted.
func TestSelectUnit_UnavailableChildOnlyFailsFast(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-only") })
	llmclient.RegisterAggregatorHealth("child-only", llmclient.AggregatorHealthUnavailable)

	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child-only", AggregatorID: "child-only"},
		},
		strategy: NewFallbackStrategy(),
	}
	_, err := a.selectUnit(SelectRequest{})
	if err == nil {
		t.Fatal("expected selection to fail when the only candidate is an unavailable child")
	}
}

// TestPoolHealth_TTLExpiryReenablesChildSelection verifies the TTL behaviour:
// while a child is registered unavailable the parent skips it; once the TTL
// expires the child is selectable again without any explicit clear.
func TestPoolHealth_TTLExpiryReenablesChildSelection(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-ttl") })
	origTTL := llmclient.DefaultAggregatorHealth
	llmclient.DefaultAggregatorHealth = llmclient.NewAggregatorHealthRegistry()
	t.Cleanup(func() { llmclient.DefaultAggregatorHealth = origTTL })
	llmclient.SetAggregatorUnavailableTTL(30 * time.Millisecond)
	llmclient.RegisterAggregatorHealth("child-ttl", llmclient.AggregatorHealthUnavailable)

	a := &Actor{
		id: "parent",
		units: []CallableUnit{
			{ID: "agg:child-ttl", AggregatorID: "child-ttl"},
			{ID: "a::m", Model: "m", ProviderName: "a"},
		},
		strategy: NewFallbackStrategy(),
	}

	chosen, err := a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit before TTL expiry: %v", err)
	}
	if chosen.ID != "a::m" {
		t.Errorf("chosen = %q, want a::m (child skipped while registered)", chosen.ID)
	}

	time.Sleep(80 * time.Millisecond) // TTL expired — auto-invalidated

	chosen, err = a.selectUnit(SelectRequest{})
	if err != nil {
		t.Fatalf("selectUnit after TTL expiry: %v", err)
	}
	if chosen.ID != "agg:child-ttl" {
		t.Errorf("chosen = %q, want agg:child-ttl (child re-eligible after TTL expiry)", chosen.ID)
	}
}

// TestOwnPoolUnavailable_MixedPoolRegression covers the nested + concrete
// mixed-pool regression: an unavailable grandchild ref plus a healthy concrete
// unit keeps the pool available; the same grandchild ref alone makes it
// unavailable.
func TestOwnPoolUnavailable_MixedPoolRegression(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("grandchild") })
	llmclient.RegisterAggregatorHealth("grandchild", llmclient.AggregatorHealthUnavailable)

	// Mixed: grandchild ref unavailable + concrete unit healthy → pool usable.
	mixed := &Actor{
		id: "middle",
		units: []CallableUnit{
			{ID: "agg:grandchild", AggregatorID: "grandchild"},
			{ID: "a::m", Model: "m", ProviderName: "a"},
		},
		strategy: NewFallbackStrategy(),
	}
	if mixed.ownPoolUnavailable(time.Now()) {
		t.Error("mixed pool with a healthy concrete unit must be available")
	}

	// Ref only: grandchild unavailable → pool fully unavailable.
	refOnly := &Actor{
		id: "middle2",
		units: []CallableUnit{
			{ID: "agg:grandchild", AggregatorID: "grandchild"},
		},
		strategy: NewFallbackStrategy(),
	}
	if !refOnly.ownPoolUnavailable(time.Now()) {
		t.Error("ref-only pool with unavailable grandchild must be unavailable")
	}

	// All concrete units cooling + no fallback → pool unavailable.
	cooled := &Actor{
		id: "middle3",
		units: []CallableUnit{
			{ID: "c1::m1", Model: "m1", ProviderName: "c1"},
			{ID: "c2::m2", Model: "m2", ProviderName: "c2"},
		},
		strategy: NewFallbackStrategy(),
	}
	coolUnit(t, "c1", "m1")
	coolUnit(t, "c2", "m2")
	if !cooled.ownPoolUnavailable(time.Now()) {
		t.Error("all-cooling pool must be unavailable")
	}
}

// TestOwnPoolUnavailable_HealthyUnitKeepsPoolAvailable verifies that a single
// healthy unit (even alongside cooled ones) keeps the pool available.
func TestOwnPoolUnavailable_HealthyUnitKeepsPoolAvailable(t *testing.T) {
	a := &Actor{
		id: "child-mixed",
		units: []CallableUnit{
			{ID: "d1::m1", Model: "m1", ProviderName: "d1"},
			{ID: "d2::m2", Model: "m2", ProviderName: "d2"},
		},
		strategy: NewFallbackStrategy(),
	}
	coolUnit(t, "d1", "m1")
	if a.ownPoolUnavailable(time.Now()) {
		t.Error("pool with one healthy unit must be available")
	}
}

// TestNestedDispatch_ParentSkipsUnavailableChild is the acceptance test: a
// parent with a mixed pool (aggregator ref + concrete unit) must NOT dispatch
// to a child registered unavailable — it goes straight to the concrete unit
// even though the child's planner is scripted to succeed. The availability
// snapshot asserts the child registration is untouched (no invalid dispatch).
func TestNestedDispatch_ParentSkipsUnavailableChild(t *testing.T) {
	restoreGate := swapGate(t, "b")
	defer restoreGate()
	t.Cleanup(func() { clearAggHealth("child-healthy-script") })
	llmclient.RegisterAggregatorHealth("child-healthy-script", llmclient.AggregatorHealthUnavailable)

	// If the parent wrongly dispatched to the child, the planner would succeed
	// and the deep unit would be resolved; the assertion below proves the
	// child was never reached.
	deep := domain.ModelUnit{Model: "deep-model", Provider: "childprov"}
	childAID, _ := canonicalAID(t, 11)
	planner := &fakePlanner{byTarget: map[string][]plan.RecvResult{
		childAID.String(): chunkList(deep, "child would have answered"),
	}}
	units := []CallableUnit{
		{ID: "agg:child-healthy-script", AggregatorID: "child-healthy-script"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "ok"},
	}
	a, ctx, emit := nestedActor(t, "parent", units, "child-healthy-script", childAID, planner, dualRegistry(nil))

	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("handleDispatch: %v", err)
	}

	ru := emit.resolvedUnit()
	if ru == nil {
		t.Fatal("expected a resolved_unit chunk")
	}
	if ru.Provider != "b" || ru.Model != "m" {
		t.Errorf("resolved unit = %s/%s, want b/m (unavailable child must be skipped)", ru.Provider, ru.Model)
	}
	if containsText(emit, "child would have answered") {
		t.Error("child must not have been dispatched")
	}
	// No invalid dispatch was sent: the registration is still present and the
	// parent did not clear or extend anything about the child.
	snap := llmclient.AggregatorHealthSnapshot()
	if e, ok := snap["child-healthy-script"]; !ok || e.State != llmclient.AggregatorHealthUnavailable {
		t.Errorf("child registration must remain unavailable, snapshot: %+v", snap)
	}
}

// TestReportOwnPoolHealth_DeadlineTracksPoolCooldown pins the horizon rule: a
// child whose whole pool is cooling for minutes must register an
// unavailable-until deadline matching that cooldown, not the flat 30s TTL.
// With a flat TTL the entry expires while the pool is still cooling, so
// parents re-probe the aggregator every 30s and every probe re-fails.
func TestReportOwnPoolHealth_DeadlineTracksPoolCooldown(t *testing.T) {
	t.Cleanup(func() {
		clearAggHealth("child-horizon")
		llmclient.ClearProvider("hz")
	})

	a := &Actor{
		id: "child-horizon",
		units: []CallableUnit{
			{ID: "hz::m1", Model: "m1", ProviderName: "hz"},
		},
		strategy: NewFallbackStrategy(),
	}
	retryAfter := 10 * time.Minute
	llmclient.RecordFailure("hz", "m1", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: retryAfter})

	if _, err := a.selectUnit(SelectRequest{}); err == nil {
		t.Fatal("expected selection to fail with the whole pool cooling")
	}

	snap := llmclient.AggregatorHealthSnapshot()
	e, ok := snap["child-horizon"]
	if !ok {
		t.Fatalf("expected child-horizon registered unavailable, snapshot: %+v", snap)
	}
	if !e.UnavailableUntil.After(time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL)) {
		t.Fatalf("UnavailableUntil = %v, want beyond the 30s TTL — must track the pool cooldown", e.UnavailableUntil)
	}
	if !e.UnavailableUntil.After(time.Now().Add(5 * time.Minute)) {
		t.Fatalf("UnavailableUntil = %v, want ≈10min out (the unit's Retry-After cooldown)", e.UnavailableUntil)
	}
}

// TestReportOwnPoolHealth_UntimedUnavailabilityFloorsAtTTL verifies the floor:
// a pool with no timed recovery estimate (disabled-by-auth units carry no
// deadline) still registers with at least the default TTL, keeping parents
// from re-probing at dispatch rate.
func TestReportOwnPoolHealth_UntimedUnavailabilityFloorsAtTTL(t *testing.T) {
	t.Cleanup(func() {
		clearAggHealth("child-floor")
		llmclient.ClearProvider("fl")
	})

	a := &Actor{
		id: "child-floor",
		units: []CallableUnit{
			{ID: "fl::m1", Model: "m1", ProviderName: "fl"},
		},
		strategy: NewFallbackStrategy(),
	}
	// Auth failure → disabled state, no cooldown deadline.
	llmclient.RecordFailure("fl", "m1", &llmclient.UpstreamError{StatusCode: 401})

	if _, err := a.selectUnit(SelectRequest{}); err == nil {
		t.Fatal("expected selection to fail with the pool disabled")
	}

	snap := llmclient.AggregatorHealthSnapshot()
	e, ok := snap["child-floor"]
	if !ok || e.State != llmclient.AggregatorHealthUnavailable {
		t.Fatalf("expected child-floor registered unavailable, snapshot: %+v", snap)
	}
	if !e.UnavailableUntil.After(time.Now().Add(20 * time.Second)) {
		t.Fatalf("UnavailableUntil = %v, want at least the 30s default TTL", e.UnavailableUntil)
	}
}
