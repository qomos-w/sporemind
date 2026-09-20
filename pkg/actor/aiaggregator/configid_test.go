package aiaggregator

import (
	"context"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// These tests pin the production shape of aggregator identity: the spawned
// actor's id is a canonical actor ULID, while parents reference nested
// children by the aimanager config id. Every llmclient aggregator-health
// registration and self-reference guard must key on the config id learned
// from applyResolvedConfig — an actor-id-keyed registration is invisible to
// parents and a disabled child keeps receiving dispatches.

// actorULID mirrors a canonical actor id shape (never equal to a config id).
const actorULID = "01arz3nddek5f5g8ppnqhh3h4m"

func TestApplyResolvedConfig_RegistersDisabledUnderConfigID(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("child-cfg") })

	a := &Actor{id: actorULID, strategy: NewFallbackStrategy()}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:       "child-cfg",
		Name:     "child",
		Disabled: true,
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://openai", Protocol: "openai"},
		},
	})

	snap := llmclient.AggregatorHealthSnapshot()
	e, ok := snap["child-cfg"]
	if !ok {
		t.Fatalf("expected registration under config id, snapshot: %+v", snap)
	}
	if e.State != llmclient.AggregatorHealthUnavailable {
		t.Errorf("state = %s, want %s", e.State, llmclient.AggregatorHealthUnavailable)
	}
	if _, ok := snap[actorULID]; ok {
		t.Error("registration must not leak under the actor id")
	}
}

func TestDispatch_FailFast_RegistersUnderConfigID(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("agg-failfast") })

	a := &Actor{id: actorULID, lifecycleCtx: nil}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:       "agg-failfast",
		Name:     "agg-failfast",
		Disabled: true,
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://openai", Protocol: "openai"},
		},
	})

	err := a.handleDispatch(nil, domain.SendSessionMessageReq{}, nil)
	if err == nil {
		t.Fatal("expected dispatch to fail fast on a disabled aggregator")
	}
	if !strings.Contains(err.Error(), "is disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["agg-failfast"]; !ok {
		t.Fatal("expected fail-fast dispatch to register the config id unavailable")
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()[actorULID]; ok {
		t.Fatal("registration must not leak under the actor id")
	}
}

func TestReportOwnPoolHealth_RegistersUnderConfigID(t *testing.T) {
	t.Cleanup(func() {
		clearAggHealth("child-pool-cfg")
		llmclient.ClearProvider("ppool")
	})

	a := &Actor{id: actorULID}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   "child-pool-cfg",
		Name: "child-pool-cfg",
		Units: []domain.ManualCallableUnit{
			{Model: "pm1", ProviderName: "ppool", Endpoint: "https://ppool", Protocol: "openai"},
		},
	})
	coolUnit(t, "ppool", "pm1")

	if _, err := a.selectUnit(SelectRequest{}); err == nil {
		t.Fatal("expected selection to fail with the whole pool cooling")
	}
	if _, ok := llmclient.AggregatorHealthSnapshot()["child-pool-cfg"]; !ok {
		t.Fatal("expected pool-down registration under the config id")
	}
}

func TestSelectOnDemandUnit_SystemAggregatorByConfigID(t *testing.T) {
	// Production shape: the auto aggregator's actor id is a ULID; it is the
	// system aggregator only by config id ("system") learned from config.
	a := &Actor{
		id:           actorULID,
		strategy:     NewFallbackStrategy(),
		lifecycleCtx: context.Background(),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
				Endpoint: "https://api.example.com",
				Protocol: "openai",
			},
		}},
	}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   systemAggregatorID,
		Name: "Auto",
		Units: []domain.ManualCallableUnit{
			{Model: "m2", ProviderName: "other", Endpoint: "https://other", Protocol: "openai"},
		},
	})

	got, err := a.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-4o"}, false)
	if err != nil {
		t.Fatalf("on-demand resolution must be allowed for the config-id system aggregator: %v", err)
	}
	if got.ProviderName != "openai" || got.Model != "gpt-4o" {
		t.Fatalf("unexpected on-demand unit: %+v", got)
	}
}

func TestSelfReferenceGuards_UseConfigID(t *testing.T) {
	a := &Actor{id: actorULID}
	// Config-time guard: a pool entry referencing this config id is dropped.
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:   "parent-cfg",
		Name: "parent",
		Units: []domain.ManualCallableUnit{
			{AggregatorID: "parent-cfg"},
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://openai", Protocol: "openai"},
		},
	})
	if got := len(a.unitsSnapshotForTest()); got != 1 {
		t.Fatalf("self-referencing entry must be dropped at config load, got %d units", got)
	}

	// Runtime guard: only the config id is treated as self.
	if !a.isSelfAggregatorRef(CallableUnit{ID: "agg:parent-cfg", AggregatorID: "parent-cfg"}) {
		t.Fatal("config-id self reference must be recognized")
	}
	if a.isSelfAggregatorRef(CallableUnit{ID: "agg:" + actorULID, AggregatorID: actorULID}) {
		t.Fatal("actor id must not be treated as self")
	}
}

// unitsSnapshotForTest returns a copy of the current pool under the read lock.
func (a *Actor) unitsSnapshotForTest() []CallableUnit {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]CallableUnit, len(a.units))
	copy(out, a.units)
	return out
}
