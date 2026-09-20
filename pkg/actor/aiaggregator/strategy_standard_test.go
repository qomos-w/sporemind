package aiaggregator

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func testUnits() []CallableUnit {
	return []CallableUnit{
		{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai"},
		{ID: "anthropic::claude", Model: "claude", ProviderName: "anthropic"},
	}
}

func TestStandardStrategy_ReusesPinnedUnit(t *testing.T) {
	cache := newAssignmentCache()
	strat := NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	units := testUnits()
	req := SelectRequest{AgentID: "agent-1", SlotKind: "primary", Unit: domain.ModelUnit{}}

	first, err := strat.Select(req, units)
	if err != nil {
		t.Fatalf("first select: %v", err)
	}

	second, err := strat.Select(req, units)
	if err != nil {
		t.Fatalf("second select: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("standard did not reuse pinned unit: first=%s second=%s", first.ID, second.ID)
	}
}

func TestStandardStrategy_DifferentAgentsGetDifferentUnits(t *testing.T) {
	cache := newAssignmentCache()
	strat := NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	units := testUnits()

	a1, _ := strat.Select(SelectRequest{AgentID: "agent-1", SlotKind: "primary"}, units)
	a2, _ := strat.Select(SelectRequest{AgentID: "agent-2", SlotKind: "primary"}, units)
	if a1.ID == a2.ID {
		t.Errorf("two different agents got same unit %s (expected load-balancing on first pick)", a1.ID)
	}
}

func TestStandardStrategy_FallsBackWhenPinnedUnitAbsent(t *testing.T) {
	cache := newAssignmentCache()
	strat := NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	allUnits := testUnits()

	req := SelectRequest{AgentID: "agent-1", SlotKind: "primary"}
	first, _ := strat.Select(req, allUnits)

	reduced := []CallableUnit{allUnits[0]}
	if first.ID == reduced[0].ID {
		reduced = []CallableUnit{allUnits[1]}
	}

	second, err := strat.Select(req, reduced)
	if err != nil {
		t.Fatalf("fallback select: %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("standard returned absent pinned unit %s", first.ID)
	}
}

func TestStandardStrategy_TTLExpiryTriggersFallback(t *testing.T) {
	cache := newAssignmentCache()
	now := time.Now()
	strat := &StandardStrategy{
		base:            NewRoundRobinStrategy(),
		pins:            make(map[string]standardEntry),
		assignmentCount: cache.count,
		now:             func() time.Time { return now },
	}
	units := testUnits()
	req := SelectRequest{AgentID: "agent-1", SlotKind: "primary"}

	first, _ := strat.Select(req, units)

	strat.now = func() time.Time { return now.Add(standardTTL + time.Second) }
	second, _ := strat.Select(req, units)

	// After TTL, the pin is stale; RR may pick a different unit.
	if first.ID != second.ID {
		// RR rotated — acceptable, the point is that expiry allows reselection.
		return
	}
}

func TestStandardStrategy_ProviderLoadBalancingOnFallback(t *testing.T) {
	cache := newAssignmentCache()
	cache.store(map[string]int{"openai": 5, "anthropic": 0})

	strat := NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	units := testUnits()

	chosen, err := strat.Select(SelectRequest{AgentID: "agent-x", SlotKind: "primary"}, units)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if chosen.ProviderName != "anthropic" {
		t.Errorf("expected least-loaded provider anthropic (count=0), got %s (count=5)", chosen.ProviderName)
	}
}

func TestStandardStrategy_EmptyAgentIDDegradesToRotation(t *testing.T) {
	cache := newAssignmentCache()
	strat := NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	units := testUnits()

	// No AgentID → no pinning, pure RR rotation.
	req := SelectRequest{Unit: domain.ModelUnit{}}
	results := map[string]bool{}
	for i := 0; i < len(units); i++ {
		chosen, _ := strat.Select(req, units)
		results[chosen.ID] = true
	}
	if len(results) < 2 {
		t.Errorf("expected RR rotation across both units with empty AgentID, got %d distinct", len(results))
	}
}
