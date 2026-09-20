package xmath

import (
	"math"
	"testing"
)

type WeightAbleFloat float64

func (this WeightAbleFloat) Weight() float64 {
	return float64(this)
}

func TestPickOneWeightSelect(t *testing.T) {
	arr := []WeightAbleFloat{1, 12, 3, 4, 5, 6, 0}
	idx, w, remain := PickOneWeightSelect(arr)
	t.Logf("idx=%d, w=%v, remain=%v", idx, w, remain)

	arr = []WeightAbleFloat{1, 2, 3, 4, 5, 6}
	var results []WeightAbleFloat
	results, remain = PickSomeWeightSelect(arr, 3)
	t.Logf("results=%v, remain=%v", results, remain)
}

func TestWeightSelect(t *testing.T) {
	arr := []WeightAbleFloat{1, 12, 3, 4, 5, 0, 6}
	picked := map[WeightAbleFloat]int{}
	picked1 := map[WeightAbleFloat]int{}
	var seed uint64 = 8

	for i := 0; i < 100; i++ {
		_, v := WeightSelect(arr, seed+uint64(i))
		picked[v]++
	}
	for k, v := range picked {
		t.Logf("WeightSelect: k=%v, count=%d", k, v)
	}

	for i := 0; i < 100; i++ {
		_, v := WeightSelect1(arr, seed+uint64(i))
		picked1[v]++
	}
	for k, v := range picked1 {
		t.Logf("WeightSelect1: k=%v, count=%d", k, v)
	}
}

// ── SRandom: never returns 1.0 ────────────────────────────────────────────────

func TestSRandom_NeverOne(t *testing.T) {
	for seed := uint64(0); seed < 200_000; seed++ {
		v := SRandom(seed)
		if v >= 1.0 {
			t.Fatalf("SRandom(%d) = %v, want < 1.0", seed, v)
		}
		if v < 0 {
			t.Fatalf("SRandom(%d) = %v, want >= 0", seed, v)
		}
	}
}

// ── PickOne ───────────────────────────────────────────────────────────────────

func TestPickOne_Empty(t *testing.T) {
	v, remain := PickOne[int](nil)
	if v != 0 || remain != nil {
		t.Fatal("PickOne(nil) should return zero value and nil")
	}
}

func TestPickOne_Single(t *testing.T) {
	v, remain := PickOne([]int{42})
	if v != 42 {
		t.Fatalf("PickOne([42]) returned %d, want 42", v)
	}
	if len(remain) != 0 {
		t.Fatalf("PickOne([42]) remain should be empty, got %v", remain)
	}
}

func TestPickOne_AllElements(t *testing.T) {
	// Over many seeds, all elements should be picked at least once.
	picked := map[int]int{}
	src := []int{10, 20, 30, 40, 50}
	for seed := uint64(0); seed < 5000; seed++ {
		v, _ := PickOne(src, seed)
		picked[v]++
	}
	for _, v := range src {
		if picked[v] == 0 {
			t.Errorf("element %d was never picked", v)
		}
	}
}

// ── GetOne ────────────────────────────────────────────────────────────────────

func TestGetOne_Empty(t *testing.T) {
	v := GetOne[int](nil)
	if v != 0 {
		t.Fatalf("GetOne(nil) should return 0, got %d", v)
	}
}

// ── Shuffle ───────────────────────────────────────────────────────────────────

func TestShuffle_ContainsAll(t *testing.T) {
	src := []int{1, 2, 3, 4, 5}
	got := Shuffle(src, 123)
	if len(got) != len(src) {
		t.Fatalf("Shuffle changed length: %d → %d", len(src), len(got))
	}
	counts := map[int]int{}
	for _, v := range got {
		counts[v]++
	}
	for _, v := range src {
		if counts[v] != 1 {
			t.Fatalf("element %d appears %d times after Shuffle", v, counts[v])
		}
	}
}

func TestShuffle_Distribution(t *testing.T) {
	counts := map[[3]int]int{}
	n := 60_000
	for i := 0; i < n; i++ {
		got := Shuffle([]int{1, 2, 3}, uint64(i))
		counts[[3]int{got[0], got[1], got[2]}]++
	}
	if len(counts) != 6 {
		t.Fatalf("expected 6 permutations, got %d", len(counts))
	}
	expected := n / 6
	for perm, cnt := range counts {
		ratio := math.Abs(float64(cnt-expected)) / float64(expected)
		if ratio > 0.15 {
			t.Errorf("perm %v: count=%d expected~%d ratio=%.2f", perm, cnt, expected, ratio)
		}
	}
}

// ── Polar ─────────────────────────────────────────────────────────────────────

func TestPolar(t *testing.T) {
	pos, neg := 0, 0
	for i := uint64(0); i < 10_000; i++ {
		v := Polar(i * 997)
		if v == 1.0 {
			pos++
		} else if v == -1.0 {
			neg++
		} else {
			t.Fatalf("Polar returned unexpected %v", v)
		}
	}
	ratio := math.Abs(float64(pos-neg)) / 10_000.0
	if ratio > 0.05 {
		t.Errorf("Polar skewed: pos=%d neg=%d", pos, neg)
	}
}

// ── WeightSelect1 (test helper, kept for comparison) ─────────────────────────

func WeightSelect1[T WeightAble](weightList []T, seed ...uint64) (int, T) {
	if len(weightList) == 0 {
		panic("weight list must > 0")
	}
	sum := 0.0
	for i := 0; i < len(weightList); i++ {
		sum += weightList[i].Weight()
	}

	if sum == 0 {
		return 0, weightList[0]
	}

	r := SRandom(seed...) * sum

	for i := 0; i < len(weightList); i++ {
		weight := weightList[i].Weight()
		if weight == 0 {
			continue
		}
		if weight >= r {
			return i, weightList[i]
		}
		r -= weight
	}

	// 安全返回
	for i := len(weightList) - 1; i >= 0; i-- {
		if weightList[i].Weight() > 0 {
			return i, weightList[i]
		}
	}
	return 0, weightList[0]
}
