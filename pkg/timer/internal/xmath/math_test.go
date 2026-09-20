package xmath

import (
	"math"
	"testing"
)

// ── Abs ───────────────────────────────────────────────────────────────────────

func TestAbs(t *testing.T) {
	if Abs(-5) != 5 {
		t.Fatal("Abs(-5) should be 5")
	}
	if Abs(5) != 5 {
		t.Fatal("Abs(5) should be 5")
	}
	if Abs(0) != 0 {
		t.Fatal("Abs(0) should be 0")
	}
	if Abs(-3.14) != 3.14 {
		t.Fatal("Abs(-3.14) should be 3.14")
	}
}

// ── Sum ───────────────────────────────────────────────────────────────────────

func TestSum(t *testing.T) {
	if Sum([]int{1, 2, 3, 4}) != 10 {
		t.Fatal("Sum([1,2,3,4]) should be 10")
	}
	if Sum([]float64{0.1, 0.2}) < 0.29 {
		t.Fatal("Sum([0.1,0.2]) should be ~0.3")
	}
	if Sum([]int{}) != 0 {
		t.Fatal("Sum([]) should be 0")
	}
}

// ── MaxArr / MinArr ───────────────────────────────────────────────────────────

func TestMaxArr(t *testing.T) {
	if MaxArr([]int{3, 1, 4, 1, 5, 9}) != 9 {
		t.Fatal("MaxArr should return 9")
	}
	if MaxArr([]float64{-1.0, -2.0}) != -1.0 {
		t.Fatal("MaxArr of negatives should return -1.0")
	}
}

func TestMinArr(t *testing.T) {
	if MinArr([]int{3, 1, 4, 1, 5, 9}) != 1 {
		t.Fatal("MinArr should return 1")
	}
	if MinArr([]float64{-1.0, -2.0}) != -2.0 {
		t.Fatal("MinArr of negatives should return -2.0")
	}
}

// ── Range ─────────────────────────────────────────────────────────────────────

func TestRange(t *testing.T) {
	r := Range(0, 5)
	if len(r) != 5 {
		t.Fatalf("Range(0,5) len=%d, want 5", len(r))
	}
	for i, v := range r {
		if v != i {
			t.Fatalf("Range(0,5)[%d]=%d, want %d", i, v, i)
		}
	}

	// start > end: same range [min, max)
	r2 := Range(5, 0)
	if len(r2) != 5 {
		t.Fatalf("Range(5,0) len=%d, want 5", len(r2))
	}
	for i, v := range r2 {
		if v != i {
			t.Fatalf("Range(5,0)[%d]=%d, want %d", i, v, i)
		}
	}

	// equal: empty
	if len(Range(3, 3)) != 0 {
		t.Fatal("Range(3,3) should be empty")
	}
}

// ── Clamp ─────────────────────────────────────────────────────────────────────

func TestClamp(t *testing.T) {
	if Clamp(5, 0, 10) != 5 {
		t.Fatal("Clamp(5,0,10) should be 5")
	}
	if Clamp(-1, 0, 10) != 0 {
		t.Fatal("Clamp(-1,0,10) should be 0")
	}
	if Clamp(15, 0, 10) != 10 {
		t.Fatal("Clamp(15,0,10) should be 10")
	}
	if Clamp(0.5, 0.0, 1.0) != 0.5 {
		t.Fatal("Clamp(0.5,0,1) should be 0.5")
	}
}

// ── Lerp ──────────────────────────────────────────────────────────────────────

func TestLerp(t *testing.T) {
	if Lerp[int](0.0, 0, 100) != 0 {
		t.Fatal("Lerp(0,0,100) should be 0")
	}
	if Lerp[int](1.0, 0, 100) != 100 {
		t.Fatal("Lerp(1,0,100) should be 100")
	}
	if Lerp[int](0.5, 0, 100) != 50 {
		t.Fatal("Lerp(0.5,0,100) should be 50")
	}
	got := Lerp[float64](0.5, 10.0, 20.0)
	if math.Abs(got-15.0) > 1e-9 {
		t.Fatalf("Lerp(0.5,10,20)=%v, want 15", got)
	}
}

// ── Step / Sign ───────────────────────────────────────────────────────────────

func TestStep(t *testing.T) {
	if Step(5, 3) != 0 {
		t.Fatal("Step(edge=5, x=3) should be 0")
	}
	if Step(5, 5) != 1 {
		t.Fatal("Step(edge=5, x=5) should be 1")
	}
	if Step(5, 7) != 1 {
		t.Fatal("Step(edge=5, x=7) should be 1")
	}
}

func TestSign(t *testing.T) {
	if Sign(10) != 1 {
		t.Fatal("Sign(10) should be 1")
	}
	if Sign(-10) != -1 {
		t.Fatal("Sign(-10) should be -1")
	}
	if Sign(0) != 0 {
		t.Fatal("Sign(0) should be 0")
	}
}

// ── Ternary ───────────────────────────────────────────────────────────────────

func TestTernary(t *testing.T) {
	if Ternary(true, "yes", "no") != "yes" {
		t.Fatal("Ternary(true,...) should return first")
	}
	if Ternary(false, "yes", "no") != "no" {
		t.Fatal("Ternary(false,...) should return second")
	}
}

// ── NumberMax / NumberMin ─────────────────────────────────────────────────────

func TestNumberMaxMin(t *testing.T) {
	if NumberMax[int]() != math.MaxInt {
		t.Fatal("NumberMax[int] should be MaxInt")
	}
	if NumberMin[int]() != math.MinInt {
		t.Fatal("NumberMin[int] should be MinInt")
	}
	if NumberMax[uint8]() != math.MaxUint8 {
		t.Fatal("NumberMax[uint8] should be 255")
	}
	if NumberMin[uint8]() != 0 {
		t.Fatal("NumberMin[uint8] should be 0")
	}
	if NumberMax[float64]() != math.MaxFloat64 {
		t.Fatal("NumberMax[float64] should be MaxFloat64")
	}
}
