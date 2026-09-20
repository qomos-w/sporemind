package aiaggregator

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// -- RoundRobinStrategy: Select() rotation, filtering, error paths --

// TestRoundRobin_AutoPickRotatesAcrossAllPoolUnits verifies that an auto-pick
// request (empty unit) rotates across every pool unit regardless of model or
// provider, distributing load evenly.
func TestRoundRobin_AutoPickRotatesAcrossAllPoolUnits(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
		{ID: "c::m", Model: "m", ProviderName: "c"},
	}
	req := domain.ModelUnit{} // auto-pick
	var ids []string
	for i := 0; i < 7; i++ {
		got, err := s.Select(SelectRequest{Unit: req}, units)
		if err != nil {
			t.Fatalf("Select #%d: %v", i, err)
		}
		ids = append(ids, got.ID)
	}
	// Expected rotation: a, b, c, a, b, c, a
	want := []string{"a::m", "b::m", "c::m", "a::m", "b::m", "c::m", "a::m"}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("rotation[%d] = %q, want %q (full seq: %v)", i, ids[i], want[i], ids)
		}
	}
}

// TestRoundRobin_NakedModelRejected verifies that a model without a provider
// (naked-model) is rejected — it must not match across providers. This is the
// core Unit invariant: (model, provider) is the only selection primitive.
func TestRoundRobin_NakedModelRejected(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
	}
	_, err := s.Select(SelectRequest{Unit: domain.ModelUnit{Model: "m"}}, units)
	if err == nil {
		t.Fatal("expected error for naked-model request (model without provider)")
	}
}

func TestRoundRobin_SingleUnitIsPassthrough(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{{ID: "only::m", Model: "m", ProviderName: "only"}}
	for i := 0; i < 3; i++ {
		got, err := s.Select(SelectRequest{Unit: domain.ModelUnit{Model: "m", Provider: "only"}}, units)
		if err != nil {
			t.Fatalf("Select #%d: %v", i, err)
		}
		if got.ID != "only::m" {
			t.Errorf("single-unit Select #%d = %q, want only::m", i, got.ID)
		}
	}
}

// TestRoundRobin_AutoPickRotatesAllMatches verifies that auto-pick rotation
// visits all pool units and only those that are in the pool.
func TestRoundRobin_AutoPickRotatesAllMatches(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m1", Model: "m1", ProviderName: "a"},
		{ID: "b::m2", Model: "m2", ProviderName: "b"},
		{ID: "c::m1", Model: "m1", ProviderName: "c"},
		{ID: "d::m2", Model: "m2", ProviderName: "d"},
	}
	// Auto-pick: rotation should visit all four units in pool order.
	req := domain.ModelUnit{}
	got1, _ := s.Select(SelectRequest{Unit: req}, units)
	got2, _ := s.Select(SelectRequest{Unit: req}, units)
	got3, _ := s.Select(SelectRequest{Unit: req}, units)
	got4, _ := s.Select(SelectRequest{Unit: req}, units)
	got5, _ := s.Select(SelectRequest{Unit: req}, units)
	if got1.ID != "a::m1" || got2.ID != "b::m2" || got3.ID != "c::m1" || got4.ID != "d::m2" {
		t.Errorf("rotation = %q,%q,%q,%q; want a::m1,b::m2,c::m1,d::m2", got1.ID, got2.ID, got3.ID, got4.ID)
	}
	if got5.ID != "a::m1" {
		t.Errorf("rotation wraps: got5 = %q, want a::m1", got5.ID)
	}
}

func TestRoundRobin_ProviderFiltering(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "prov-a"},
		{ID: "b::m", Model: "m", ProviderName: "prov-b"},
	}
	// Request with explicit provider: only the matching unit is selected.
	req := domain.ModelUnit{Model: "m", Provider: "prov-b"}
	got, err := s.Select(SelectRequest{Unit: req}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if got.ID != "b::m" {
		t.Errorf("provider-filtered Select = %q, want b::m", got.ID)
	}
}

func TestRoundRobin_EmptyPoolReturnsError(t *testing.T) {
	s := NewRoundRobinStrategy()
	_, err := s.Select(SelectRequest{Unit: domain.ModelUnit{Model: "m", Provider: "x"}}, nil)
	if err == nil {
		t.Fatal("expected error for empty pool")
	}
}

func TestRoundRobin_NoModelMatchReturnsError(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{{ID: "a::m1", Model: "m1", ProviderName: "a"}}
	_, err := s.Select(SelectRequest{Unit: domain.ModelUnit{Model: "nonexistent", Provider: "a"}}, units)
	if err == nil {
		t.Fatal("expected error when no unit matches requested model")
	}
}

// TestRoundRobin_AutoPickCursorWraps verifies that the auto-pick cursor wraps
// around correctly across repeated calls.
func TestRoundRobin_AutoPickCursorWraps(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m1", Model: "m1", ProviderName: "a"},
		{ID: "b::m1", Model: "m1", ProviderName: "b"},
		{ID: "c::m2", Model: "m2", ProviderName: "c"},
		{ID: "d::m2", Model: "m2", ProviderName: "d"},
	}
	// Auto-pick: cursor advances through all units.
	got1, _ := s.Select(SelectRequest{}, units)
	got2, _ := s.Select(SelectRequest{}, units)
	got3, _ := s.Select(SelectRequest{}, units)
	if got1.ID != "a::m1" || got2.ID != "b::m1" {
		t.Errorf("rotation = %q,%q; want a::m1,b::m1", got1.ID, got2.ID)
	}
	if got3.ID != "c::m2" {
		t.Errorf("third selection = %q; want c::m2 (cursor advanced)", got3.ID)
	}
}

// -- Cooldown closed-loop: select → mark → re-select cross-call --

// TestSelectUnit_CooldownClosedLoopSwitchesToNextUnit is omitted; health state
// now lives in llmclient and is covered by llmclient health tests.
func _TestSelectUnit_CooldownClosedLoopSwitchesToNextUnit(t *testing.T) {}

// TestSelectUnit_AllUnitsCooledDownStillSelectable is omitted; health state now
// lives in llmclient.
func _TestSelectUnit_AllUnitsCooledDownStillSelectable(t *testing.T) {}

// TestSelectUnit_RoundRobinResumesAfterCooldownExpiry is omitted; health state
// now lives in llmclient.
func _TestSelectUnit_RoundRobinResumesAfterCooldownExpiry(t *testing.T) {}

// -- RoundRobinStrategy: per-agent assignment pinning --

// TestRoundRobin_AgentKeepsAssignedUnitWhileEligible verifies that an agent
// keeps the unit it was assigned as long as that unit stays in the eligible
// pool — repeated dispatches never rotate a healthy assigned unit away.
func TestRoundRobin_AgentKeepsAssignedUnitWhileEligible(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
		{ID: "c::m", Model: "m", ProviderName: "c"},
	}
	req := SelectRequest{AgentID: "ag-1", SlotKind: "primary", Unit: domain.ModelUnit{}}
	first, err := s.Select(req, units)
	if err != nil {
		t.Fatalf("first select: %v", err)
	}
	if first.ID != "a::m" {
		t.Fatalf("first assignment = %q, want a::m", first.ID)
	}
	for i := 0; i < 5; i++ {
		got, err := s.Select(req, units)
		if err != nil {
			t.Fatalf("select #%d: %v", i, err)
		}
		if got.ID != first.ID {
			t.Fatalf("select #%d = %q, want pinned %q", i+2, got.ID, first.ID)
		}
	}
}

// TestRoundRobin_AgentAdvancesWhenAssignedDropsOut verifies the failover
// semantics: when the assigned unit leaves the eligible pool, the next unit
// after it in pool order is assigned, and the agent then stays on the new
// unit even after the old one recovers.
func TestRoundRobin_AgentAdvancesWhenAssignedDropsOut(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
		{ID: "c::m", Model: "m", ProviderName: "c"},
	}
	req := SelectRequest{AgentID: "ag-1", SlotKind: "primary", Unit: domain.ModelUnit{}}

	first, _ := s.Select(req, units) // a::m
	if first.ID != "a::m" {
		t.Fatalf("first assignment = %q, want a::m", first.ID)
	}

	// a::m cools down → eligible pool is [b::m, c::m].
	eligible := units[1:]
	second, err := s.Select(req, eligible)
	if err != nil {
		t.Fatalf("reassign select: %v", err)
	}
	if second.ID != "b::m" {
		t.Errorf("reassignment = %q, want b::m (next after a::m in pool order)", second.ID)
	}

	// a::m recovers → the agent keeps b::m.
	third, err := s.Select(req, units)
	if err != nil {
		t.Fatalf("post-recovery select: %v", err)
	}
	if third.ID != "b::m" {
		t.Errorf("after a::m recovers, agent should keep %q, got %q", "b::m", third.ID)
	}
}

// TestRoundRobin_ReassignmentTakesNextEligibleNotFirstEligible verifies that
// reassignment scans downward from the agent's last index instead of falling
// back to pool position 0: an agent pinned on b::m with b cooled must move to
// c::m (next down), not wrap straight back to a::m.
func TestRoundRobin_ReassignmentTakesNextEligibleNotFirstEligible(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
		{ID: "c::m", Model: "m", ProviderName: "c"},
	}
	req := SelectRequest{AgentID: "ag-1", SlotKind: "primary", Unit: domain.ModelUnit{}}

	// Advance the shared cursor so ag-1's first assignment is b::m.
	s.Select(SelectRequest{AgentID: "ag-0", SlotKind: "primary", Unit: domain.ModelUnit{}}, units)
	first, _ := s.Select(req, units)
	if first.ID != "b::m" {
		t.Fatalf("first assignment = %q, want b::m", first.ID)
	}

	// b::m cools down → eligible pool is [a::m, c::m]. Scanning down from
	// b::m's index must pick c::m, not a::m.
	eligible := []CallableUnit{units[0], units[2]}
	got, err := s.Select(req, eligible)
	if err != nil {
		t.Fatalf("reassign select: %v", err)
	}
	if got.ID != "c::m" {
		t.Errorf("reassignment = %q, want c::m (next down from b::m), not a::m", got.ID)
	}
}

// TestRoundRobin_DifferentAgentsSpreadOnFirstAssignment verifies first
// assignments draw from the shared rotation cursor so agents distribute
// across the pool.
func TestRoundRobin_DifferentAgentsSpreadOnFirstAssignment(t *testing.T) {
	s := NewRoundRobinStrategy()
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
		{ID: "c::m", Model: "m", ProviderName: "c"},
	}
	seen := map[string]bool{}
	for _, agent := range []string{"ag-1", "ag-2", "ag-3"} {
		got, err := s.Select(SelectRequest{AgentID: agent, SlotKind: "primary", Unit: domain.ModelUnit{}}, units)
		if err != nil {
			t.Fatalf("select %s: %v", agent, err)
		}
		seen[got.ID] = true
	}
	if len(seen) != 3 {
		t.Errorf("expected 3 distinct units across 3 agents, got %d (%v)", len(seen), seen)
	}
}

// TestRoundRobin_AgentClosedLoopCooldownSwitch is omitted; health state now
// lives in llmclient and is covered by llmclient health tests.
func _TestRoundRobin_AgentClosedLoopCooldownSwitch(t *testing.T) {}
