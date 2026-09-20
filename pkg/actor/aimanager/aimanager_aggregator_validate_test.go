package aimanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestValidateAggregatorUnits_ValidNestedConfig(t *testing.T) {
	existing := map[string]domain.AIManagerAggregatorGetResp{
		"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o"},
			{AggregatorID: "B"},
		}},
		"B": {ID: "B", Name: "Agg B", Units: []domain.ManualCallableUnit{
			{ProviderName: "kimi", Model: "moonshot"},
		}},
	}
	units := []domain.ManualCallableUnit{
		{ProviderName: "anthropic", Model: "claude-opus-4-7", Endpoint: "https://anthropic", Protocol: "anthropic", MaxConcurrency: 3},
		{AggregatorID: "A"},
		// The system auto aggregator is a valid reference target even when it
		// is not present in the map (as in this unit test).
		{AggregatorID: autoAggregatorID},
	}
	if err := validateAggregatorUnits("X", units, existing); err != nil {
		t.Fatalf("valid nested config rejected: %v", err)
	}
}

func TestValidateAggregatorUnits_SelfReferenceRejected(t *testing.T) {
	units := []domain.ManualCallableUnit{{AggregatorID: "X"}}
	err := validateAggregatorUnits("X", units, map[string]domain.AIManagerAggregatorGetResp{})
	if err == nil {
		t.Fatal("expected self-reference to be rejected")
	}
	if !strings.Contains(err.Error(), "references itself") {
		t.Fatalf("expected self-reference error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_TwoNodeCycleRejected(t *testing.T) {
	// Existing A → B; configuring B with B → A closes the cycle B → A → B.
	existing := map[string]domain.AIManagerAggregatorGetResp{
		"A": {ID: "A", Units: []domain.ManualCallableUnit{{AggregatorID: "B"}}},
	}
	units := []domain.ManualCallableUnit{{AggregatorID: "A"}}
	err := validateAggregatorUnits("B", units, existing)
	if err == nil {
		t.Fatal("expected two-node cycle to be rejected")
	}
	if !strings.Contains(err.Error(), "B → A → B") {
		t.Fatalf("expected cycle path B → A → B in error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_ThreeNodeCycleRejected(t *testing.T) {
	// Existing A → B → C; configuring C with C → A closes the cycle
	// C → A → B → C.
	existing := map[string]domain.AIManagerAggregatorGetResp{
		"A": {ID: "A", Units: []domain.ManualCallableUnit{{AggregatorID: "B"}}},
		"B": {ID: "B", Units: []domain.ManualCallableUnit{{AggregatorID: "C"}}},
	}
	units := []domain.ManualCallableUnit{{AggregatorID: "A"}}
	err := validateAggregatorUnits("C", units, existing)
	if err == nil {
		t.Fatal("expected three-node cycle to be rejected")
	}
	if !strings.Contains(err.Error(), "C → A → B → C") {
		t.Fatalf("expected cycle path C → A → B → C in error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_UnknownReferenceRejected(t *testing.T) {
	units := []domain.ManualCallableUnit{{AggregatorID: "ghost"}}
	err := validateAggregatorUnits("X", units, map[string]domain.AIManagerAggregatorGetResp{})
	if err == nil {
		t.Fatal("expected unknown reference to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown aggregator") {
		t.Fatalf("expected unknown-aggregator error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_MixedEntryRejected(t *testing.T) {
	existing := map[string]domain.AIManagerAggregatorGetResp{
		"A": {ID: "A", Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}}},
	}
	units := []domain.ManualCallableUnit{{AggregatorID: "A", Model: "gpt-4o", ProviderName: "openai"}}
	err := validateAggregatorUnits("X", units, existing)
	if err == nil {
		t.Fatal("expected mixed unit (aggregatorID + model) to be rejected")
	}
	if !strings.Contains(err.Error(), "cannot mix") {
		t.Fatalf("expected mix error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_EmptyEntryRejected(t *testing.T) {
	units := []domain.ManualCallableUnit{{}}
	err := validateAggregatorUnits("X", units, nil)
	if err == nil {
		t.Fatal("expected empty unit to be rejected")
	}
	if !strings.Contains(err.Error(), "must set either") {
		t.Fatalf("expected missing-fields error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_IncompleteConcreteUnitRejected(t *testing.T) {
	units := []domain.ManualCallableUnit{{Model: "gpt-4o"}}
	err := validateAggregatorUnits("X", units, nil)
	if err == nil {
		t.Fatal("expected concrete unit without ProviderName to be rejected")
	}
	if !strings.Contains(err.Error(), "requires both Model and ProviderName") {
		t.Fatalf("expected incomplete-unit error, got: %v", err)
	}
}

func TestValidateAggregatorUnits_ConcreteUnitsUnaffected(t *testing.T) {
	existing := map[string]domain.AIManagerAggregatorGetResp{
		"A": {ID: "A", Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o", Endpoint: "https://openai", Protocol: "openai", MaxConcurrency: 5},
			{ProviderName: "anthropic", Model: "claude-opus-4-7"},
		}},
	}
	units := []domain.ManualCallableUnit{
		{ProviderName: "kimi", Model: "moonshot", Endpoint: "https://kimi", Protocol: "openai", MaxContextLength: 128000},
	}
	if err := validateAggregatorUnits("X", units, existing); err != nil {
		t.Fatalf("concrete unit config must remain valid: %v", err)
	}
}

// TestHandleAggregatorConfigure_ConcreteUnitsUnaffected guards the regression:
// configuring an aggregator with plain concrete units keeps working.
func TestHandleAggregatorConfigure_ConcreteUnitsUnaffected(t *testing.T) {
	a := &Actor{
		actorID:     "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{},
		aggRefs:     map[string]ref.Ref{},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	_, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID:   "my-agg",
		Name: "My Agg",
		Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o", Endpoint: "https://openai", Protocol: "openai", MaxConcurrency: 5},
		},
	})
	if err != nil {
		t.Fatalf("configure with concrete units failed: %v", err)
	}
	cfg, ok := a.aggregators["my-agg"]
	if !ok {
		t.Fatal("aggregator not stored")
	}
	if len(cfg.Units) != 1 || cfg.Units[0].Model != "gpt-4o" || cfg.Units[0].ProviderName != "openai" {
		t.Fatalf("concrete unit not preserved: %+v", cfg.Units)
	}
}

// TestHandleAggregatorConfigure_ValidNestedReferenceStored verifies a legal
// nested config (concrete unit + aggregator reference) is accepted and the
// reference entry is stored with AggregatorID intact.
func TestHandleAggregatorConfigure_ValidNestedReferenceStored(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}}},
		},
		aggRefs: map[string]ref.Ref{},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	_, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID:   "B",
		Name: "Agg B",
		Units: []domain.ManualCallableUnit{
			{ProviderName: "anthropic", Model: "claude-opus-4-7"},
			{AggregatorID: "A"},
		},
	})
	if err != nil {
		t.Fatalf("valid nested config rejected: %v", err)
	}
	cfg, ok := a.aggregators["B"]
	if !ok {
		t.Fatal("aggregator B not stored")
	}
	if len(cfg.Units) != 2 {
		t.Fatalf("expected 2 units, got %+v", cfg.Units)
	}
	if cfg.Units[1].AggregatorID != "A" {
		t.Fatalf("reference entry not stored with AggregatorID, got %+v", cfg.Units[1])
	}
}

// TestHandleAggregatorConfigure_CycleRejectedNotStored verifies the configure
// handler rejects a cycle and leaves the aggregator map untouched.
func TestHandleAggregatorConfigure_CycleRejectedNotStored(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"A": {ID: "A", Units: []domain.ManualCallableUnit{{AggregatorID: "B"}}},
		},
		aggRefs: map[string]ref.Ref{},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	_, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID:    "B",
		Name:  "Agg B",
		Units: []domain.ManualCallableUnit{{AggregatorID: "A"}},
	})
	if err == nil {
		t.Fatal("expected cycle to be rejected")
	}
	if !strings.Contains(err.Error(), "B → A → B") {
		t.Fatalf("expected cycle path B → A → B in error, got: %v", err)
	}
	if _, ok := a.aggregators["B"]; ok {
		t.Fatal("rejected config must not be stored")
	}
}

// TestHandleAggregatorConfigure_CycleRejectedResolveUnchanged is the cross-layer
// smoke for scenario 7: configuring A→B is accepted, then configuring B→A is
// rejected as a cycle. After the rejection, resolve for A still returns its
// original config (concrete + ref→B with AggregatorID intact), while B's
// existing config remains unchanged — the rejected path did not corrupt state.
func TestHandleAggregatorConfigure_CycleRejectedResolveUnchanged(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			// B pre-exists with a concrete unit so A→B is a valid reference.
			"B": {ID: "B", Name: "Agg B", Units: []domain.ManualCallableUnit{
				{ProviderName: "kimi", Model: "moonshot"},
			}},
		},
		aggRefs:         map[string]ref.Ref{},
		persistedHealth: map[string]persistedHealthEntry{},
	}
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// 1. Configure A with [concrete unit, ref→B]. Valid: B exists, no cycle.
	if _, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID:   "A",
		Name: "Agg A",
		Units: []domain.ManualCallableUnit{
			{ProviderName: "openai", Model: "gpt-4o"},
			{AggregatorID: "B"},
		},
	}); err != nil {
		t.Fatalf("configure A with valid nested reference: %v", err)
	}

	// 2. Attempt to configure B with [ref→A]. This closes the cycle B→A→B.
	_, err := a.handleAggregatorConfigure(ctx, domain.AIManagerAggregatorConfigureReq{
		ID:   "B",
		Name: "Agg B",
		Units: []domain.ManualCallableUnit{
			{AggregatorID: "A"},
		},
	})
	if err == nil {
		t.Fatal("expected cycle B→A→B to be rejected")
	}
	if !strings.Contains(err.Error(), "B → A → B") {
		t.Fatalf("expected cycle path B → A → B in error, got: %v", err)
	}

	// 3. B's stored config is unchanged (still the original concrete unit).
	bCfg, ok := a.aggregators["B"]
	if !ok {
		t.Fatal("B must remain stored with its original config after rejection")
	}
	if len(bCfg.Units) != 1 || bCfg.Units[0].Model != "moonshot" {
		t.Fatalf("B config corrupted by rejected configure, got %+v", bCfg.Units)
	}

	// 4. Resolve A → the resolve response carries the original config
	// (concrete + ref→B with AggregatorID intact). This is the exact payload
	// applyResolvedConfig would receive.
	resp, err := a.handleAggregatorResolve(nil, domain.AIManagerAggregatorResolveReq{ID: "A"})
	if err != nil {
		t.Fatalf("resolve A after rejection: %v", err)
	}
	if len(resp.Units) != 2 {
		t.Fatalf("expected 2 units in resolve, got %+v", resp.Units)
	}
	if resp.Units[0].Model != "gpt-4o" || resp.Units[0].ProviderName != "openai" {
		t.Fatalf("concrete unit not preserved, got %+v", resp.Units[0])
	}
	if resp.Units[1].AggregatorID != "B" {
		t.Fatalf("AggregatorID not preserved through resolve, got %+v", resp.Units[1])
	}
	if resp.Units[1].Model != "" || resp.Units[1].ProviderName != "" {
		t.Fatalf("reference entry must stay a pure reference, got %+v", resp.Units[1])
	}
}

// TestHandleAggregatorResolve_PreservesAggregatorID verifies step 4: resolve
// responses carry the AggregatorID of reference entries through to the
// aggregator actor (applyResolvedConfig input) unchanged.
func TestHandleAggregatorResolve_PreservesAggregatorID(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}}},
			"B": {ID: "B", Name: "Agg B", Units: []domain.ManualCallableUnit{
				{ProviderName: "anthropic", Model: "claude-opus-4-7"},
				{AggregatorID: "A"},
			}},
		},
		persistedHealth: map[string]persistedHealthEntry{},
	}

	resp, err := a.handleAggregatorResolve(nil, domain.AIManagerAggregatorResolveReq{ID: "B"})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(resp.Units) != 2 {
		t.Fatalf("expected 2 units, got %+v", resp.Units)
	}
	refUnit := resp.Units[1]
	if refUnit.AggregatorID != "A" {
		t.Fatalf("AggregatorID not preserved through resolve, got %+v", refUnit)
	}
	if refUnit.Model != "" || refUnit.ProviderName != "" {
		t.Fatalf("reference entry must stay a pure reference, got %+v", refUnit)
	}
}
