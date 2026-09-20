package xmath

import (
	"math"
	"testing"
)

// ── SRandom: never returns 1.0 ────────────────────────────────────────────────

func TestDiceSRandom_NeverOne(t *testing.T) {
	d := NewDice()
	for seed := uint64(0); seed < 200_000; seed++ {
		d.SetSeed(seed)
		v := d.SRandom()
		if v >= 1.0 {
			t.Fatalf("Dice.SRandom() returned %v (>= 1.0) at seed=%d", v, seed)
		}
		if v < 0 {
			t.Fatalf("Dice.SRandom() returned negative %v at seed=%d", v, seed)
		}
	}
}

// ── DiceShuffle: all elements present, proper distribution ───────────────────

func TestDiceShuffle_ContainsAll(t *testing.T) {
	d := NewDice(42)
	src := []int{1, 2, 3, 4, 5}
	got := DiceShuffle(src, d)

	if len(got) != len(src) {
		t.Fatalf("DiceShuffle changed length: got %d, want %d", len(got), len(src))
	}
	counts := map[int]int{}
	for _, v := range got {
		counts[v]++
	}
	for _, v := range src {
		if counts[v] != 1 {
			t.Fatalf("element %d appears %d times after shuffle", v, counts[v])
		}
	}
}

func TestDiceShuffle_DoesNotMutateSource(t *testing.T) {
	d := NewDice(1)
	src := []int{10, 20, 30, 40, 50}
	orig := make([]int, len(src))
	copy(orig, src)
	DiceShuffle(src, d)
	for i, v := range src {
		if v != orig[i] {
			t.Fatalf("DiceShuffle mutated source at index %d: got %d, want %d", i, v, orig[i])
		}
	}
}

func TestDiceShuffle_Distribution(t *testing.T) {
	// With N=3, each of the 6 permutations should appear roughly equally.
	d := NewDice()
	counts := map[[3]int]int{}
	n := 60_000
	for i := 0; i < n; i++ {
		d.SetSeed(uint64(i))
		got := DiceShuffle([]int{1, 2, 3}, d)
		counts[[3]int{got[0], got[1], got[2]}]++
	}
	if len(counts) != 6 {
		t.Fatalf("expected 6 distinct permutations, got %d", len(counts))
	}
	expected := n / 6
	for perm, cnt := range counts {
		ratio := math.Abs(float64(cnt-expected)) / float64(expected)
		if ratio > 0.15 { // allow ±15% deviation
			t.Errorf("permutation %v appeared %d times (expected ~%d, ratio=%.2f)", perm, cnt, expected, ratio)
		}
	}
}

// ── DicePickOneWeightSelect: does NOT mutate caller ────────────────────────────

func TestDicePickOneWeightSelect_NoMutation(t *testing.T) {
	d := NewDice(7)
	list := []WeightAbleFloat{1, 2, 3, 4}
	orig := make([]WeightAbleFloat, len(list))
	copy(orig, list)

	_, _, remain := DicePickOneWeightSelect(list, d)

	// original slice must be unchanged
	for i, v := range list {
		if v != orig[i] {
			t.Fatalf("DicePickOneWeightSelect mutated input at index %d", i)
		}
	}
	// remain must be shorter by one
	if len(remain) != len(list)-1 {
		t.Fatalf("remain len=%d, want %d", len(remain), len(list)-1)
	}
}

// ── Dice.Polar: returns ±1 with equal probability ────────────────────────────

func TestDicePolar(t *testing.T) {
	d := NewDice()
	pos, neg := 0, 0
	n := 10_000
	for i := 0; i < n; i++ {
		d.SetSeed(uint64(i * 997))
		v := d.Polar()
		if v == 1.0 {
			pos++
		} else if v == -1.0 {
			neg++
		} else {
			t.Fatalf("Dice.Polar() returned unexpected value %v", v)
		}
	}
	ratio := math.Abs(float64(pos-neg)) / float64(n)
	if ratio > 0.05 {
		t.Errorf("Dice.Polar() skewed: pos=%d neg=%d ratio=%.3f", pos, neg, ratio)
	}
}

// ── DiceGetOne: empty slice returns zero value ─────────────────────────────────

func TestDiceGetOne_Empty(t *testing.T) {
	d := NewDice(0)
	v := DiceGetOne[int](nil, d)
	if v != 0 {
		t.Fatalf("DiceGetOne(nil) should return 0, got %d", v)
	}
}
