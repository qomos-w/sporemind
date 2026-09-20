package tokenest

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name  string
		input rune
		want  CharClass
	}{
		{"uppercase A", 'A', ClassLatin},
		{"lowercase z", 'z', ClassLatin},
		{"CJK han", '中', ClassCJK},
		{"hiragana", 'あ', ClassCJK},
		{"katakana", 'カ', ClassCJK},
		{"hangul", '한', ClassCJK},
		{"digit", '5', ClassOther},
		{"space", ' ', ClassOther},
		{"punctuation", ',', ClassOther},
		{"symbol", '@', ClassOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.input); got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCountChars(t *testing.T) {
	cc := CountChars("Hello世界123")
	if cc.Latin != 5 {
		t.Errorf("Latin = %d, want 5", cc.Latin)
	}
	if cc.CJK != 2 {
		t.Errorf("CJK = %d, want 2", cc.CJK)
	}
	if cc.Other != 3 {
		t.Errorf("Other = %d, want 3", cc.Other)
	}
}

func TestCalibrationEstimateTokens(t *testing.T) {
	cal := NewCalibration()

	// tiktoken-backed estimate: must be a positive integer and equal the
	// package-level estimate when no calibration samples exist.
	tokens := cal.EstimateTokens("Hello世界123")
	if tokens <= 0 {
		t.Errorf("EstimateTokens = %d, want > 0", tokens)
	}
	if EstimateTokens("Hello世界123") != tokens {
		t.Error("cal.EstimateTokens should match package EstimateTokens with no samples")
	}

	// nil receiver falls back to package-level EstimateTokens.
	var nilCal *Calibration
	if nilCal.EstimateTokens("Hello") != EstimateTokens("Hello") {
		t.Error("nil cal.EstimateTokens should match package EstimateTokens")
	}
}

func TestCalibrationUpdate(t *testing.T) {
	cal := NewCalibration()

	estimated := 4
	actual := 6
	cal.Update(CharCounts{Latin: 5, CJK: 2}, estimated, actual)

	if cal.Count != 1 {
		t.Errorf("Count = %d, want 1", cal.Count)
	}
	// Per-class coefficients are no longer adjusted by Update (tiktoken handles
	// class differences); they stay at their defaults so persisted snapshots
	// remain interpretable.
	if cal.LatinCoef != 0.25 || cal.CJKCoef != 1.5 || cal.OtherCoef != 0.25 {
		t.Errorf("coefs changed: Latin=%f CJK=%f Other=%f", cal.LatinCoef, cal.CJKCoef, cal.OtherCoef)
	}
	if cal.CumEstimatedTokens != int64(estimated) {
		t.Errorf("CumEstimatedTokens = %d, want %d", cal.CumEstimatedTokens, estimated)
	}
	if cal.CumActualTokens != int64(actual) {
		t.Errorf("CumActualTokens = %d, want %d", cal.CumActualTokens, actual)
	}

	// After a real-sample update, EstimateTokens applies the cumulative scale
	// (actual/estimated = 1.5) to the tiktoken base.
	base := EstimateTokens("Hello世界123")
	want := int(float64(base) * 1.5)
	if got := cal.EstimateTokens("Hello世界123"); got != want {
		t.Errorf("scaled estimate = %d, want %d (base=%d)", got, want, base)
	}
}

func TestCalibrationSnapshot(t *testing.T) {
	cal := NewCalibration()
	cal.LatinCoef = 0.3
	cal.CJKCoef = 1.8
	cal.OtherCoef = 0.2
	cal.Count = 42
	cal.CumEstimatedTokens = 1000
	cal.CumActualTokens = 1500

	snap := cal.Snapshot()
	if snap.LatinCoef != 0.3 || snap.CJKCoef != 1.8 || snap.OtherCoef != 0.2 || snap.Count != 42 {
		t.Errorf("Snapshot() mismatch: %+v", snap)
	}
	if snap.CumEstimatedTokens != 1000 || snap.CumActualTokens != 1500 {
		t.Errorf("Snapshot() cumulative mismatch: %+v", snap)
	}

	// Load into a new calibration.
	cal2 := NewCalibration()
	cal2.Load(snap)
	if cal2.LatinCoef != 0.3 || cal2.CJKCoef != 1.8 || cal2.OtherCoef != 0.2 || cal2.Count != 42 {
		t.Errorf("after Load: Latin=%f CJK=%f Other=%f Count=%d",
			cal2.LatinCoef, cal2.CJKCoef, cal2.OtherCoef, cal2.Count)
	}
	if cal2.CumEstimatedTokens != 1000 || cal2.CumActualTokens != 1500 {
		t.Errorf("after Load cumulative mismatch: est=%d act=%d",
			cal2.CumEstimatedTokens, cal2.CumActualTokens)
	}

	// Loading zero-count snapshot should be a no-op.
	cal3 := NewCalibration()
	cal3.Load(CalibrationSnapshot{Count: 0})
	if cal3.LatinCoef != 0.25 {
		t.Errorf("zero-count Load should not overwrite, got Latin=%f", cal3.LatinCoef)
	}

	// Loading out-of-bounds coefficients should clamp them.
	cal4 := NewCalibration()
	cal4.Load(CalibrationSnapshot{
		LatinCoef:          100,
		CJKCoef:            0.01,
		OtherCoef:          -5,
		Count:              1,
		CumEstimatedTokens: 10,
		CumActualTokens:    20,
	})
	if cal4.LatinCoef != maxLatinCoef {
		t.Errorf("LatinCoef clamp failed, got %f want %f", cal4.LatinCoef, maxLatinCoef)
	}
	if cal4.CJKCoef != minCJKCoef {
		t.Errorf("CJKCoef clamp failed, got %f want %f", cal4.CJKCoef, minCJKCoef)
	}
	if cal4.OtherCoef != minOtherCoef {
		t.Errorf("OtherCoef clamp failed, got %f want %f", cal4.OtherCoef, minOtherCoef)
	}
}

func TestCalibrationUpdateNoOp(t *testing.T) {
	cal := NewCalibration()
	orig := cal.Snapshot()

	// Zero estimated should not update.
	cal.Update(CharCounts{Latin: 10}, 0, 100)
	if cal.Snapshot() != orig {
		t.Error("Update with estimated=0 should be no-op")
	}

	// Zero actual should not update.
	cal.Update(CharCounts{Latin: 10}, 100, 0)
	if cal.Snapshot() != orig {
		t.Error("Update with actual=0 should be no-op")
	}
}

func TestCalibrationScaleClamping(t *testing.T) {
	cal := NewCalibration()

	// actual=10000, estimated=1000 → ratio 10×, clamped to maxRatio in the
	// estimate even though the raw cumulative ratio is unbounded.
	cal.Update(CharCounts{Latin: 1000}, 1000, 10000)
	if cal.CumEstimatedTokens != 1000 || cal.CumActualTokens != 10000 {
		t.Errorf("cumulative tokens not tracked: %+v", cal.Snapshot())
	}

	base := EstimateTokens("hello world")
	scaled := cal.EstimateTokens("hello world")
	want := int(float64(base) * maxRatio)
	if scaled != want {
		t.Errorf("scaled estimate = %d, want %d (clamped to maxRatio)", scaled, want)
	}

	// Repeated extreme updates keep the scale clamped, not diverging.
	for i := 0; i < 10; i++ {
		cal.Update(CharCounts{Latin: 1000}, 1000, 10000)
	}
	if got := cal.EstimateTokens("hello world"); got != want {
		t.Errorf("scaled estimate diverged: %d, want %d", got, want)
	}
}

func TestCalibrationCumulativeConvergence(t *testing.T) {
	cal := NewCalibration()

	// Simulate many dispatches of 100 'a' chars where the true token cost is 30.
	// tiktoken gives a stable base for "aaaa..."; the scale converges toward
	// actual/base. After convergence, the scaled estimate should approach actual.
	chars := CharCounts{Latin: 100}
	text := strings.Repeat("a", chars.Latin)
	actual := 30
	for i := 0; i < 50; i++ {
		estimated := cal.EstimateTokens(text)
		cal.Update(chars, estimated, actual)
	}

	if cal.CumActualTokens != int64(50*actual) {
		t.Errorf("CumActualTokens = %d, want %d", cal.CumActualTokens, 50*actual)
	}
	// Final scaled estimate should be within maxRatio of actual (convergence).
	final := cal.EstimateTokens(text)
	if final < int(float64(actual)/maxRatio) || final > int(float64(actual)*maxRatio) {
		t.Errorf("converged estimate = %d, want near %d", final, actual)
	}
}
