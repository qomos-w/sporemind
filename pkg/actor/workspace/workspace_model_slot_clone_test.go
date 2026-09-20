package workspace

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestCloneModelSlotPtr_DeepCopiesUnitAndCandidates pins actor #12: the
// workspace persists cloned ModelSlots on AgentRefs, so the clone must own its
// Candidates backing array AND the ModelRef.Unit pointer — a shallow Unit copy
// would let a transient request mutate persisted selection state.
func TestCloneModelSlotPtr_DeepCopiesUnitAndCandidates(t *testing.T) {
	src := &gen.ModelSlot{Candidates: []gen.ModelRef{
		{Kind: "unit", Unit: &gen.ModelUnit{Model: "m1", Provider: "p1", ThinkLevel: "high"}},
		{Kind: "auto"},
	}}
	clone := cloneModelSlotPtr(src)
	if clone == nil || len(clone.Candidates) != len(src.Candidates) {
		t.Fatalf("clone = %+v, want %d candidates", clone, len(src.Candidates))
	}
	// Independent Candidates backing array.
	if &clone.Candidates[0] == &src.Candidates[0] {
		t.Fatal("clone shares the Candidates backing array with the source")
	}
	// Independent ModelRef.Unit pointer.
	if clone.Candidates[0].Unit == src.Candidates[0].Unit {
		t.Fatal("clone shares the ModelRef.Unit pointer with the source")
	}

	// Mutating the source must not leak into the clone.
	src.Candidates[0].Kind = "aggregator"
	src.Candidates[0].Unit.Model = "source-mutated"
	src.Candidates[0].Unit.Provider = "source-mutated"
	if clone.Candidates[0].Kind != "unit" {
		t.Fatalf("clone Kind = %q, want unit (source mutation leaked)", clone.Candidates[0].Kind)
	}
	if clone.Candidates[0].Unit.Model != "m1" || clone.Candidates[0].Unit.Provider != "p1" {
		t.Fatalf("clone Unit = %+v, want m1/p1 (source mutation leaked)", clone.Candidates[0].Unit)
	}

	// Mutating the clone must not leak into the source.
	clone.Candidates[0].Unit.ThinkLevel = "low"
	clone.Candidates[1].Kind = "unit"
	if src.Candidates[0].Unit.ThinkLevel != "high" {
		t.Fatalf("source ThinkLevel = %q, want high (clone mutation leaked)", src.Candidates[0].Unit.ThinkLevel)
	}
	if src.Candidates[1].Kind != "auto" {
		t.Fatalf("source candidate[1] Kind = %q, want auto (clone mutation leaked)", src.Candidates[1].Kind)
	}

	// Nil stays nil.
	if cloneModelSlotPtr(nil) != nil {
		t.Fatal("cloneModelSlotPtr(nil) must be nil")
	}
}
