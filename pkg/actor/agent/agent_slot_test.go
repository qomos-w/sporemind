package agent

import (
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

func unitRef(model, provider string) *domain.ModelUnit {
	return &domain.ModelUnit{Model: model, Provider: provider}
}

// TestEffectiveCandidates_EmptySlotIsAuto verifies the documented invariant:
// an unconfigured (empty) slot resolves to a single [auto] candidate that the
// agent routes to the system aggregator.
func TestEffectiveCandidates_EmptySlotIsAuto(t *testing.T) {
	got := effectiveCandidates(domain.ModelSlot{})
	if len(got) != 1 || got[0].Kind != modelRefKindAuto {
		t.Fatalf("effectiveCandidates(empty) = %+v, want [{auto}]", got)
	}
}

func TestEffectiveCandidates_PreservesConfiguredOrder(t *testing.T) {
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "a", Provider: "p"}},
		{Kind: modelRefKindAggregator, AggregatorID: "agg-1"},
		{Kind: modelRefKindAuto},
	}}
	got := effectiveCandidates(slot)
	if len(got) != 3 || got[0].Kind != modelRefKindUnit || got[1].Kind != modelRefKindAggregator || got[2].Kind != modelRefKindAuto {
		t.Fatalf("configured order not preserved: %+v", got)
	}
}

func TestIsUnitLockedSlot(t *testing.T) {
	cases := []struct {
		name string
		slot domain.ModelSlot
		want bool
	}{
		{"empty slot is auto-backed, not locked", domain.ModelSlot{}, false},
		{"single unit candidate is locked", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-4o", Provider: "openai"}},
		}}, true},
		{"aggregator candidate is not locked", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "agg-1"},
		}}, false},
		{"auto candidate is not locked", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAuto},
		}}, false},
		{"mixed unit+aggregator chain is not locked (has fallback)", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-4o", Provider: "openai"}},
			{Kind: modelRefKindAggregator, AggregatorID: "agg-1"},
		}}, false},
		// pinnedUnitSlot (frontend composer selection) produces [unit@agg, auto]:
		// the auto tail keeps UnitPinned=false so a unit that lives in a nested
		// child aggregator can reach the parent's nested forwarding path.
		{"unit+auto tail is not locked (soft pin, has fallback)", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-4o", Provider: "openai"}, AggregatorID: "agg-1"},
			{Kind: modelRefKindAuto},
		}}, false},
		{"unit candidate without model is not locked", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{}},
		}}, false},
		{"unit candidate without provider (naked model) is not locked", domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-4o"}},
		}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUnitLockedSlot(c.slot); got != c.want {
				t.Errorf("isUnitLockedSlot = %v, want %v", got, c.want)
			}
		})
	}
}

// slotTestActor is an Actor with a pre-populated aggRefCache so resolveTarget
// never needs to consult the context (no live actor tree required). The system
// aggregator and a named aggregator are both wired with distinguishable refs.
func slotTestActor() *Actor {
	sysAgg := fakeCompactionRef{tag: "system"}
	namedAgg := fakeCompactionRef{tag: "named"}
	return &Actor{
		aggRefCache: map[string]ref.Ref{
			systemAggID: sysAgg,
			"named-agg": namedAgg,
		},
	}
}

// TestCachedAggRef_RefreshThrottle verifies the miss-driven refresh gate:
// the first cache miss records a refresh attempt, and a second miss within
// minAggRefRefreshInterval is throttled (timestamp unchanged) — a refresh
// that succeeds but leaves the id unlisted must not re-invoke
// aimanager.aggregator_list on every dispatch attempt.
func TestCachedAggRef_RefreshThrottle(t *testing.T) {
	a := slotTestActor()
	a.aggRefCache = nil // force misses
	ctx := newFakeCompactionContext()

	if r := a.cachedAggRef(ctx, systemAggID); r != nil {
		t.Fatalf("expected nil ref on miss with nil topo, got %v", r)
	}
	a.aggRefMu.RLock()
	first := a.aggRefRefreshAt
	a.aggRefMu.RUnlock()
	if first.IsZero() {
		t.Fatal("first miss should record a refresh attempt timestamp")
	}

	if r := a.cachedAggRef(ctx, systemAggID); r != nil {
		t.Fatalf("expected nil ref on second miss, got %v", r)
	}
	a.aggRefMu.RLock()
	second := a.aggRefRefreshAt
	a.aggRefMu.RUnlock()
	if !second.Equal(first) {
		t.Fatalf("second miss within interval must be throttled: %v != %v", second, first)
	}
}

// TestResolveTarget_UnitKindRoutesToSystemAggregatorWithUnit verifies the
// unit-kind three-state: a unit candidate is served by the system aggregator
// carrying the concrete model.
func TestResolveTarget_UnitKindRoutesToSystemAggregatorWithUnit(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-5", Provider: "openai"}},
	}}
	aggRef, unit, err := a.resolveTarget(ctx, slot)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if aggRef == nil {
		t.Fatal("expected non-nil system aggregator ref for unit-kind candidate")
	}
	if unit.Model != "gpt-5" || unit.Provider != "openai" {
		t.Errorf("unit = %+v, want gpt-5/openai", unit)
	}
}

// TestResolveTarget_UnitKindWithAggregatorIDRoutesToNamedAggregator verifies a
// unit candidate carrying an AggregatorID is served by THAT aggregator, not the
// system aggregator. This is the "pick a model from a named aggregator's pool"
// path: the unit is concrete but routed through the named aggregator.
func TestResolveTarget_UnitKindWithAggregatorIDRoutesToNamedAggregator(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "gpt-5", Provider: "openai"}, AggregatorID: "named-agg"},
	}}
	aggRef, unit, err := a.resolveTarget(ctx, slot)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if aggRef != a.aggRefCache["named-agg"] {
		t.Fatal("expected unit served by named-agg, got a different aggregator ref")
	}
	if aggRef == a.aggRefCache[systemAggID] {
		t.Fatal("unit must NOT be served by the system aggregator when AggregatorID is set")
	}
	if unit.Model != "gpt-5" || unit.Provider != "openai" {
		t.Errorf("unit = %+v, want gpt-5/openai", unit)
	}
}

// TestResolveTarget_AggregatorKindRoutesToNamedAggregator verifies an
// aggregator-kind candidate targets the named aggregator with no pinned unit.
func TestResolveTarget_AggregatorKindRoutesToNamedAggregator(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "named-agg"},
	}}
	aggRef, unit, err := a.resolveTarget(ctx, slot)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if aggRef == nil {
		t.Fatal("expected non-nil named-aggregator ref")
	}
	if unit.Model != "" {
		t.Errorf("aggregator-kind must not pin a unit, got %+v", unit)
	}
}

// TestResolveTarget_AutoKindRoutesToSystemAggregator verifies an auto-kind
// candidate targets the system aggregator with no pinned unit (fast-pick).
func TestResolveTarget_AutoKindRoutesToSystemAggregator(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}
	aggRef, unit, err := a.resolveTarget(ctx, slot)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if aggRef == nil {
		t.Fatal("expected non-nil system aggregator ref for auto candidate")
	}
	if unit.Model != "" {
		t.Errorf("auto candidate must not pin a unit, got %+v", unit)
	}
}

// TestResolveTarget_EmptySlotFallsBackToAuto confirms the default path for an
// unconfigured (no-config) agent: empty slot → [auto] → system aggregator.
func TestResolveTarget_EmptySlotFallsBackToAuto(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	aggRef, unit, err := a.resolveTarget(ctx, domain.ModelSlot{})
	if err != nil {
		t.Fatalf("resolveTarget on empty slot: %v", err)
	}
	if aggRef == nil {
		t.Fatal("expected system aggregator ref for empty slot")
	}
	if unit.Model != "" {
		t.Errorf("empty slot should resolve to aggregator-selected (empty unit), got %+v", unit)
	}
}

// TestResolveTargets_FallbackOrderPreserved verifies the candidate chain is
// resolved in declared order and all resolvable candidates are returned.
func TestResolveTargets_FallbackOrderPreserved(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "preferred", Provider: "p"}},
		{Kind: modelRefKindAggregator, AggregatorID: "named-agg"},
		{Kind: modelRefKindAuto},
	}}
	targets := a.resolveTargets(ctx, slot)
	if len(targets) != 3 {
		t.Fatalf("expected 3 resolvable targets, got %d", len(targets))
	}
	if targets[0].unit.Model != "preferred" || targets[0].unit.Provider != "p" {
		t.Errorf("first target should carry the preferred unit, got %+v", targets[0].unit)
	}
	if targets[1].unit.Model != "" {
		t.Errorf("second target (named aggregator) must not pin a unit, got %+v", targets[1].unit)
	}
	if targets[2].unit.Model != "" {
		t.Errorf("third target (auto) must not pin a unit, got %+v", targets[2].unit)
	}
}

// TestResolveTargets_SkipsUnresolvableCandidate verifies that a candidate
// whose aggregator is not in the cache is skipped, but the remaining chain is
// still resolved in order (graceful degradation).
func TestResolveTargets_SkipsUnresolvableCandidate(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "missing-agg"}, // not in cache
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fallback", Provider: "p"}},
		{Kind: modelRefKindAuto},
	}}
	targets := a.resolveTargets(ctx, slot)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets (missing agg skipped), got %d", len(targets))
	}
	if targets[0].unit.Model != "fallback" || targets[0].unit.Provider != "p" {
		t.Errorf("first resolvable target should be the unit, got %+v", targets[0].unit)
	}
}

// TestResolveTargets_UnitCandidateWithoutModelSkipped verifies a malformed
// unit-kind candidate (nil unit, empty model, or empty provider) is skipped
// rather than producing an empty-unit dispatch target.
func TestResolveTargets_UnitCandidateWithoutModelSkipped(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit},                                      // nil unit
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{}},           // empty model
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "x"}}, // empty provider (naked model)
		{Kind: modelRefKindAuto},
	}}
	targets := a.resolveTargets(ctx, slot)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (only auto valid), got %d", len(targets))
	}
}

// TestResolveTarget_AllCandidatesUnresolvableErrors verifies the error path
// when no candidate in the chain can be resolved.
func TestResolveTarget_AllCandidatesUnresolvableErrors(t *testing.T) {
	// Empty cache → system aggregator not resolvable, named aggregator absent.
	a := &Actor{}
	ctx := newFakeCompactionContext()
	_, _, err := a.resolveTarget(ctx, domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAuto},
	}})
	if err == nil {
		t.Fatal("expected error when no candidate resolves")
	}
}

// TestResolveTarget_AggregatorKindEmptyIdDefaultsToSystem verifies an
// aggregator-kind candidate with no AggregatorID defaults to the system
// aggregator (mirrors resolveCandidate's empty-id handling).
func TestResolveTarget_AggregatorKindEmptyIdDefaultsToSystem(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator}, // empty AggregatorID
	}}
	aggRef, _, err := a.resolveTarget(ctx, slot)
	if err != nil {
		t.Fatalf("resolveTarget: %v", err)
	}
	if aggRef == nil {
		t.Fatal("expected system aggregator ref for empty-id aggregator candidate")
	}
}

// TestSlotFirstUnit helpers
func TestSlotFirstUnit(t *testing.T) {
	// Unit candidate first (with provider).
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m1", Provider: "p1"}},
		{Kind: modelRefKindAuto},
	}}
	if u := slotFirstUnit(slot); u.Model != "m1" || u.Provider != "p1" {
		t.Errorf("slotFirstUnit = %+v, want m1/p1", u)
	}
	// Unit after an aggregator candidate: still finds it.
	slot2 := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "x"},
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m2", Provider: "p2"}},
	}}
	if u := slotFirstUnit(slot2); u.Model != "m2" || u.Provider != "p2" {
		t.Errorf("slotFirstUnit = %+v, want m2/p2", u)
	}
	// Unit without provider (naked model) is skipped.
	slot3 := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "m3"}},
		{Kind: modelRefKindAuto},
	}}
	if u := slotFirstUnit(slot3); u.Model != "" {
		t.Errorf("slotFirstUnit with naked model should return zero, got %+v", u)
	}
	// No unit candidate → zero.
	if u := slotFirstUnit(domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}); u.Model != "" {
		t.Errorf("slotFirstUnit = %q, want empty", u.Model)
	}
}

// TestSlotFirstUnitResolved_UnitKind returns the unit directly for unit-kind
// candidates, matching slotFirstUnit.
func TestSlotFirstUnitResolved_UnitKind(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "direct-model", Provider: "openai"}},
	}}
	u := a.slotFirstUnitResolved(ctx, slot)
	if u.Model != "direct-model" || u.Provider != "openai" {
		t.Fatalf("slotFirstUnitResolved = %+v, want direct-model/openai", u)
	}
}

// TestSlotFirstUnitResolved_AggregatorKind queries the aggregator for the
// first healthy pooled unit when the slot is aggregator-backed.
func TestSlotFirstUnitResolved_AggregatorKind(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	ctx.planner = &fakeStatusPlanner{status: gen.AIAggregatorStatusResp{
		Units: []gen.AICallableUnitView{
			{Model: "pooled-1", ProviderName: "anthropic"},
			{Model: "pooled-2", ProviderName: "openai"},
		},
	}}
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: systemAggID},
	}}
	u := a.slotFirstUnitResolved(ctx, slot)
	if u.Model != "pooled-1" || u.Provider != "anthropic" {
		t.Fatalf("slotFirstUnitResolved = %+v, want pooled-1/anthropic", u)
	}
}

// TestSlotFirstUnitResolved_AutoKind queries the system aggregator for [auto]
// slots (empty slot defaults to [auto]).
func TestSlotFirstUnitResolved_AutoKind(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	ctx.planner = &fakeStatusPlanner{status: gen.AIAggregatorStatusResp{
		Units: []gen.AICallableUnitView{
			{Model: "auto-model", ProviderName: "openai"},
		},
	}}
	u := a.slotFirstUnitResolved(ctx, domain.ModelSlot{})
	if u.Model != "auto-model" || u.Provider != "openai" {
		t.Fatalf("slotFirstUnitResolved = %+v, want auto-model/openai", u)
	}
}

// TestSlotFirstUnitResolved_SkipsDisabledAndCooling verifies that disabled and
// actively cooling-down units are skipped in favor of the next healthy one.
func TestSlotFirstUnitResolved_SkipsDisabledAndCooling(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	ctx.planner = &fakeStatusPlanner{status: gen.AIAggregatorStatusResp{
		Units: []gen.AICallableUnitView{
			{Model: "disabled-model", ProviderName: "p1", HealthState: "disabled"},
			{Model: "cooling-model", ProviderName: "p2", HealthState: "cooling_down", CooldownUntil: time.Now().Unix() + 3600},
			{Model: "healthy-model", ProviderName: "p3"},
		},
	}}
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}
	u := a.slotFirstUnitResolved(ctx, slot)
	if u.Model != "healthy-model" || u.Provider != "p3" {
		t.Fatalf("slotFirstUnitResolved = %+v, want healthy-model/p3", u)
	}
}

// TestSlotFirstUnitResolved_AggregatorFailureReturnsZero verifies graceful
// degradation when the aggregator call fails (planner returns nil).
func TestSlotFirstUnitResolved_AggregatorFailureReturnsZero(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext() // no planner set
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}
	u := a.slotFirstUnitResolved(ctx, slot)
	if u.Model != "" {
		t.Fatalf("slotFirstUnitResolved = %+v, want empty on aggregator failure", u)
	}
}

// TestSlotFirstUnitResolved_UnitKindPrecedesAggregator verifies that a
// unit-kind candidate earlier in the chain is returned before trying any
// aggregator candidate.
func TestSlotFirstUnitResolved_UnitKindPrecedesAggregator(t *testing.T) {
	a := slotTestActor()
	ctx := newFakeCompactionContext()
	ctx.planner = &fakeStatusPlanner{status: gen.AIAggregatorStatusResp{
		Units: []gen.AICallableUnitView{
			{Model: "pooled", ProviderName: "p"},
		},
	}}
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "pinned", Provider: "openai"}},
		{Kind: modelRefKindAuto},
	}}
	u := a.slotFirstUnitResolved(ctx, slot)
	if u.Model != "pinned" || u.Provider != "openai" {
		t.Fatalf("slotFirstUnitResolved = %+v, want pinned/openai", u)
	}
}

// TestResolveChildSlot verifies the slot-aware child resolver honors
// aggregator/auto candidates instead of collapsing to a pinned unit, respects
// the per-kind priority order, falls back across slots, honors an explicit
// request-unit override, and errors when nothing resolves.
func TestResolveChildSlot(t *testing.T) {
	ctx := newFakeCompactionContext()

	// newChildActor builds an Actor with system + named aggregator refs cached
	// (so resolveTargets never needs the live actor tree) and applies the slot
	// configuration fn.
	newChildActor := func(set func(a *Actor)) *Actor {
		a := slotTestActor()
		set(a)
		return a
	}

	aggSlot := func(id string) domain.ModelSlot {
		return domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAggregator, AggregatorID: id}}}
	}
	unitSlot := func(model string) domain.ModelSlot {
		return domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: model, Provider: "p"}},
		}}
	}

	t.Run("explorer adopts aggregator fast slot instead of collapsing to primary unit", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.fast = aggSlot("named-agg")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, "explorer", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if len(slot.Candidates) == 0 || slot.Candidates[0].Kind != modelRefKindAggregator {
			t.Fatalf("expected the raw aggregator fast slot (not collapsed to a primary unit), got %+v", slot)
		}
	})

	t.Run("explorer auto empty fast slot resolves via system aggregator", func(t *testing.T) {
		// An empty slot is [auto]; resolveChildSlot must return it verbatim
		// (it still resolves to the system aggregator) rather than skipping it.
		a := newChildActor(func(a *Actor) {
			a.fast = domain.ModelSlot{}
		})
		slot, err := a.resolveChildSlot(ctx, "explorer", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if !slotIsEmpty(slot) {
			t.Fatalf("expected the empty [auto] slot to be returned, got %+v", slot)
		}
		if len(a.resolveTargets(ctx, slot)) == 0 {
			t.Fatal("empty [auto] slot must resolve to the system aggregator")
		}
	})

	t.Run("explorer falls back to primary when fast aggregator unresolvable", func(t *testing.T) {
		// "missing-agg" is not in the cache → resolveTargets(fast)=0 → degrade.
		a := newChildActor(func(a *Actor) {
			a.fast = aggSlot("missing-agg")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, "explorer", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "primary-model" {
			t.Fatalf("expected fallback to primary unit, got %+v", got)
		}
	})

	t.Run("request unit override ignores configured slots", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.fast = unitSlot("fast-model")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, "explorer", &domain.ModelUnit{Model: "override", Provider: "p"})
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "override" {
			t.Fatalf("expected reqUnit override, got %+v", got)
		}
	})

	t.Run("reviewer priority review then fast then primary", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.review = unitSlot("review-model")
			a.fast = unitSlot("fast-model")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, domain.AgentKindReviewer, nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "review-model" {
			t.Fatalf("expected review slot to win, got %+v", got)
		}
	})

	t.Run("default priority execution then primary then fast", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.execution = unitSlot("exec-model")
			a.primary = unitSlot("primary-model")
			a.fast = unitSlot("fast-model")
		})
		slot, err := a.resolveChildSlot(ctx, "general", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "exec-model" {
			t.Fatalf("expected execution slot to win, got %+v", got)
		}
	})

	t.Run("dreamer priority fast then primary like explorer", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.fast = unitSlot("fast-model")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, domain.AgentKindDreamer, nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "fast-model" {
			t.Fatalf("expected dreamer to prefer fast slot, got %+v", slot)
		}
	})

	t.Run("scout priority fast then primary like explorer/dreamer", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.fast = unitSlot("fast-model")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, "scout", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "fast-model" {
			t.Fatalf("expected scout to prefer fast slot, got %+v", slot)
		}
	})

	t.Run("scout falls back to primary when fast slot does not resolve", func(t *testing.T) {
		a := newChildActor(func(a *Actor) {
			a.fast = aggSlot("missing-agg")
			a.primary = unitSlot("primary-model")
		})
		slot, err := a.resolveChildSlot(ctx, "scout", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if got := slotFirstUnit(slot); got.Model != "primary-model" {
			t.Fatalf("expected scout to fall back to primary slot, got %+v", slot)
		}
	})

	t.Run("errors when no slot resolves", func(t *testing.T) {
		// Empty cache + no topo: nothing resolves, including [auto].
		a := &Actor{}
		if _, err := a.resolveChildSlot(ctx, "explorer", nil); err == nil {
			t.Fatal("expected error when no slot resolves")
		}
	})
}

func TestPromoteRouteUnit(t *testing.T) {
	newUnit := domain.ModelUnit{Model: "m-new", Provider: "p"}

	t.Run("head already resolved unit is a no-op", func(t *testing.T) {
		slot := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: unitRef("m-new", "p"), AggregatorID: "named"},
			{Kind: modelRefKindAuto},
		}}
		got, changed := promoteRouteUnit(slot, newUnit)
		if changed {
			t.Error("promote of the head unit must be a no-op")
		}
		if got.Candidates[0].AggregatorID != "named" {
			t.Errorf("no-op must preserve the original candidate, got %+v", got.Candidates)
		}
	})

	t.Run("aggregator-backed slot is a no-op", func(t *testing.T) {
		slot := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "my-agg"},
		}}
		got, changed := promoteRouteUnit(slot, newUnit)
		if changed {
			t.Errorf("promote on aggregator slot must be a no-op, got %+v", got.Candidates)
		}
	})

	t.Run("auto/empty slot is a no-op", func(t *testing.T) {
		got, changed := promoteRouteUnit(domain.ModelSlot{}, newUnit)
		if changed {
			t.Errorf("promote on empty/auto slot must be a no-op, got %+v", got.Candidates)
		}
		autoSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: modelRefKindAuto}}}
		got, changed = promoteRouteUnit(autoSlot, newUnit)
		if changed {
			t.Errorf("promote on [auto] slot must be a no-op, got %+v", got.Candidates)
		}
	})

	t.Run("existing candidate is hoisted preserving AggregatorID", func(t *testing.T) {
		slot := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p")},
			{Kind: modelRefKindUnit, Unit: unitRef("m-new", "p"), AggregatorID: "named"},
			{Kind: modelRefKindAuto},
		}}
		got, changed := promoteRouteUnit(slot, newUnit)
		if !changed {
			t.Fatal("hoisting must change the chain")
		}
		if got.Candidates[0].Unit.Model != "m-new" || got.Candidates[0].AggregatorID != "named" {
			t.Errorf("hoisted candidate must keep its serving aggregator, got %+v", got.Candidates[0])
		}
		if len(got.Candidates) != 3 || got.Candidates[2].Kind != modelRefKindAuto {
			t.Errorf("dedup + tail expected, got %+v", got.Candidates)
		}
	})

	t.Run("unseen unit is a no-op (only hoisting existing candidates)", func(t *testing.T) {
		slot := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p")},
			{Kind: modelRefKindUnit, Unit: unitRef("m-b", "p")},
			{Kind: modelRefKindUnit, Unit: unitRef("m-c", "p")},
			{Kind: modelRefKindAuto},
		}}
		got, changed := promoteRouteUnit(slot, newUnit)
		if changed {
			t.Errorf("promote of an unseen unit must be a no-op, got %+v", got.Candidates)
		}
	})

	t.Run("unit-locked chain with unseen unit is a no-op", func(t *testing.T) {
		slot := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: unitRef("m-x", "p")},
		}}
		got, changed := promoteRouteUnit(slot, newUnit)
		if changed {
			t.Errorf("promote of unseen unit on unit-locked chain must be a no-op, got %+v", got.Candidates)
		}
	})

	t.Run("invalid unit is a no-op", func(t *testing.T) {
		if _, changed := promoteRouteUnit(domain.ModelSlot{}, domain.ModelUnit{Model: "m"}); changed {
			t.Error("unit without provider must be rejected")
		}
	})
}

func TestDemoteRouteUnit(t *testing.T) {
	slot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p"), AggregatorID: "named"},
		{Kind: modelRefKindUnit, Unit: unitRef("m-b", "p")},
		{Kind: modelRefKindAuto},
	}}

	t.Run("failed head unit moves behind remaining units", func(t *testing.T) {
		got, changed := demoteRouteUnit(slot, domain.ModelUnit{Model: "m-a", Provider: "p"})
		if !changed {
			t.Fatal("demote of the head unit must change the chain")
		}
		if got.Candidates[0].Unit.Model != "m-b" || got.Candidates[1].Unit.Model != "m-a" || got.Candidates[1].AggregatorID != "named" {
			t.Errorf("demoted unit must keep its aggregator id and sit last, got %+v", got.Candidates)
		}
		if got.Candidates[2].Kind != modelRefKindAuto {
			t.Errorf("auto tail must be preserved, got %+v", got.Candidates)
		}
	})

	t.Run("already-last unit is a no-op", func(t *testing.T) {
		if _, changed := demoteRouteUnit(slot, domain.ModelUnit{Model: "m-b", Provider: "p"}); changed {
			t.Error("demote of the last unit candidate must be a no-op")
		}
	})

	t.Run("unknown unit is a no-op", func(t *testing.T) {
		if _, changed := demoteRouteUnit(slot, domain.ModelUnit{Model: "m-z", Provider: "p"}); changed {
			t.Error("demote of an unrecorded unit must be a no-op")
		}
	})

	t.Run("sole unit before tail stays in place", func(t *testing.T) {
		solo := domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p")},
			{Kind: modelRefKindAuto},
		}}
		if _, changed := demoteRouteUnit(solo, domain.ModelUnit{Model: "m-a", Provider: "p"}); changed {
			t.Error("sole unit has nowhere to move; must be a no-op")
		}
	})
}

func TestSlotFromUnit_IsHardLocked(t *testing.T) {
	got := slotFromUnit(domain.ModelUnit{Model: "m", Provider: "p"})
	if len(got.Candidates) != 1 || got.Candidates[0].Kind != modelRefKindUnit {
		t.Errorf("slotFromUnit must produce [unit] (hard-locked), got %+v", got.Candidates)
	}
	if !isUnitLockedSlot(got) {
		t.Error("slotFromUnit must produce a unit-locked slot")
	}
	if got := slotFromUnit(domain.ModelUnit{}); len(got.Candidates) != 0 {
		t.Error("empty unit must yield empty slot")
	}
}

func TestRecordRouteUnit_ArmsReportFlag(t *testing.T) {
	a := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p")},
		{Kind: modelRefKindUnit, Unit: unitRef("m-b", "p")},
	}}}
	ctx := newFakeCompactionContext()
	// Promote m-b to the head — it's an existing candidate, so it gets hoisted.
	if !a.recordRouteUnit(ctx, domain.ModelUnit{Model: "m-b", Provider: "p"}) {
		t.Fatal("hoisting an existing unit must change the chain")
	}
	if !a.routeReportPending.Load() {
		t.Error("routeReportPending must be armed after a promote")
	}
	if slotFirstUnit(a.primary).Model != "m-b" {
		t.Errorf("primary head must be m-b, got %+v", a.primary)
	}
}

func TestRecordRouteUnit_NoOpDoesNotArm(t *testing.T) {
	a := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: unitRef("m", "p")},
	}}}
	ctx := newFakeCompactionContext()
	a.routeReportPending.Store(false)
	if a.recordRouteUnit(ctx, domain.ModelUnit{Model: "m", Provider: "p"}) {
		t.Error("promote of the head unit must be a no-op")
	}
	if a.routeReportPending.Load() {
		t.Error("no-op promote must not arm the report flag")
	}
}

func TestDemoteFailedRouteUnit_ArmsReportFlag(t *testing.T) {
	a := &Actor{primary: domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: unitRef("m-a", "p")},
		{Kind: modelRefKindUnit, Unit: unitRef("m-b", "p")},
	}}}
	ctx := newFakeCompactionContext()
	if !a.demoteFailedRouteUnit(ctx, domain.ModelUnit{Model: "m-a", Provider: "p"}) {
		t.Fatal("demote of the head unit must change the chain")
	}
	if !a.routeReportPending.Load() {
		t.Error("routeReportPending must be armed after a demote")
	}
	if slotFirstUnit(a.primary).Model != "m-b" {
		t.Errorf("primary head must now be m-b, got %+v", a.primary)
	}
}
