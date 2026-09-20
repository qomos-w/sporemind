package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestThinkingUnitKey_IgnoresThinkLevel verifies the core invariant of the
// explicit thinkingUnitKey: the registry is keyed by model+provider identity
// alone, so a unit's own default think level never participates in override
// equality. Two units that differ only in ThinkLevel must resolve to the same
// override slot.
func TestThinkingUnitKey_IgnoresThinkLevel(t *testing.T) {
	a := &Actor{}
	a.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{
		Mode: "effort", Effort: "high",
	})

	reg := a.loadThinkingRegistry()
	lvl, ok := reg[thinkingUnitKey{Model: "gpt-5", Provider: "openai"}]
	if !ok {
		t.Fatal("expected override keyed by model+provider to be found")
	}
	if lvl.Effort != "high" {
		t.Fatalf("Effort = %q, want high", lvl.Effort)
	}

	// A lookup must not be affected by a ThinkLevel on the queried unit; the key
	// struct has no such field, so this is structural — but guard the contract.
	a.status.Unit = gen.ModelUnit{Model: "gpt-5", Provider: "openai", ThinkLevel: "medium"}
	if got := a.activeThinkLevelString(nil); got != "effort" {
		t.Fatalf("activeThinkLevelString = %q, want effort (registry wins over unit.ThinkLevel)", got)
	}
}

// TestThinkingUnitKey_DifferentUnitDifferentKey ensures distinct model+provider
// pairs map to distinct registry entries (no aliasing).
func TestThinkingUnitKey_DifferentUnitDifferentKey(t *testing.T) {
	a := &Actor{}
	a.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "effort", Effort: "high"})
	a.storeThinkingLevel(thinkingUnitKey{Model: "claude", Provider: "anthropic"}, gen.ThinkingLevel{Mode: "budget", Budget: 8000})
	reg := a.loadThinkingRegistry()
	if len(reg) != 2 {
		t.Fatalf("expected 2 registry entries, got %d", len(reg))
	}
	if reg[thinkingUnitKey{Model: "gpt-5", Provider: "openai"}].Effort != "high" {
		t.Error("openai/gpt-5 override missing or wrong")
	}
	if reg[thinkingUnitKey{Model: "claude", Provider: "anthropic"}].Budget != 8000 {
		t.Error("anthropic/claude override missing or wrong")
	}
}

// TestResolveThinking_ThreeTierPrecedence covers the per-turn reasoning-effort
// precedence: explicit registry effort > registry budget > unit ThinkLevel
// default, and that an explicit registry "none" suppresses the unit default.
func TestResolveThinking_ThreeTierPrecedence(t *testing.T) {
	mk := func(thinkLevel string) domain.TurnInput {
		return domain.TurnInput{
			Unit: &gen.ModelUnit{Model: "gpt-5", Provider: "openai", ThinkLevel: thinkLevel},
		}
	}

	// No registry, unit has ThinkLevel default → default applies.
	a := &Actor{}
	b, e := a.resolveThinking(mk("medium"))
	if b != 0 || e != "medium" {
		t.Fatalf("unit default: (%d,%q), want (0,medium)", b, e)
	}

	// No registry, unit ThinkLevel="none" → no default.
	b, e = a.resolveThinking(mk("none"))
	if b != 0 || e != "" {
		t.Fatalf("unit none: (%d,%q), want (0,\"\")", b, e)
	}

	// No registry, unit ThinkLevel empty → nothing.
	b, e = a.resolveThinking(mk(""))
	if b != 0 || e != "" {
		t.Fatalf("unit empty: (%d,%q), want (0,\"\")", b, e)
	}

	// Registry effort override wins over unit ThinkLevel default.
	a2 := &Actor{}
	a2.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "effort", Effort: "xhigh"})
	b, e = a2.resolveThinking(mk("high"))
	if b != 0 || e != "xhigh" {
		t.Fatalf("registry effort: (%d,%q), want (0,xhigh)", b, e)
	}

	// "max" and "ultra" effort values flow through identically.
	aMax := &Actor{}
	aMax.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "effort", Effort: "max"})
	b, e = aMax.resolveThinking(mk("high"))
	if b != 0 || e != "max" {
		t.Fatalf("registry max: (%d,%q), want (0,max)", b, e)
	}
	aUltra := &Actor{}
	aUltra.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "effort", Effort: "ultra"})
	b, e = aUltra.resolveThinking(mk("high"))
	if b != 0 || e != "ultra" {
		t.Fatalf("registry ultra: (%d,%q), want (0,ultra)", b, e)
	}

	// Registry budget override.
	a3 := &Actor{}
	a3.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "budget", Budget: 16000})
	b, e = a3.resolveThinking(mk("high"))
	if b != 16000 || e != "" {
		t.Fatalf("registry budget: (%d,%q), want (16000,\"\")", b, e)
	}

	// Explicit registry "none" suppresses the unit ThinkLevel default — must NOT
	// fall back to "high".
	a4 := &Actor{}
	a4.storeThinkingLevel(thinkingUnitKey{Model: "gpt-5", Provider: "openai"}, gen.ThinkingLevel{Mode: "none"})
	b, e = a4.resolveThinking(mk("high"))
	if b != 0 || e != "" {
		t.Fatalf("registry none: (%d,%q), want (0,\"\") — explicit none must override unit default", b, e)
	}

	// No unit → nothing regardless of ThinkLevel.
	b, e = (&Actor{}).resolveThinking(domain.TurnInput{})
	if b != 0 || e != "" {
		t.Fatalf("nil unit: (%d,%q), want (0,\"\")", b, e)
	}
}
