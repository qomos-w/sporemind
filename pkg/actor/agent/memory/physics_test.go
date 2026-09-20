package memory

import (
	"math"
	"testing"
)

func TestDecayNode(t *testing.T) {
	t.Run("not awake no decay", func(t *testing.T) {
		if got := DecayNode(100, 0.1, 0, 0, false); got != 100 {
			t.Fatalf("not awake: got %f, want 100", got)
		}
	})
	t.Run("awake decay", func(t *testing.T) {
		got := DecayNode(100, 0.1, 0, 0, true)
		want := 100.0 - 100.0*0.1 // 90
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("awake decay: got %f, want %f", got, want)
		}
	})
	t.Run("ignores base energy (no clamp)", func(t *testing.T) {
		got := DecayNode(100, 0.5, 60, 0, true)
		// loss=50, result=50; baseEnergy=60 is ignored
		if got != 50 {
			t.Fatalf("baseEnergy ignored: got %f, want 50", got)
		}
	})
	t.Run("clamps to min energy", func(t *testing.T) {
		got := DecayNode(100, 0.5, 0, 70, true)
		if got != 70 {
			t.Fatalf("min clamp: got %f, want 70", got)
		}
	})
	t.Run("rate > 1 clipped to weight", func(t *testing.T) {
		got := DecayNode(50, 2.0, 0, 0, true)
		if got != 0 {
			t.Fatalf("rate=2 on weight=50: got %f, want 0", got)
		}
	})
}

func TestAntagonistDrain(t *testing.T) {
	tests := []struct {
		winnerAct, loserW float64
		want              float64
	}{
		{10.0, 100.0, 10.0}, // capped at winner activity
		{10.0, 5.0, 5.0},    // capped at loser weight
		{10.0, 0.0, 0.0},    // zero loser
		{100.0, 10.0, 10.0}, // capped by loser
	}
	for _, tc := range tests {
		if got := AntagonistDrain(tc.winnerAct, tc.loserW); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("AntagonistDrain(%f,%f)=%f, want %f", tc.winnerAct, tc.loserW, got, tc.want)
		}
	}
}

func TestShouldEvaporate(t *testing.T) {
	tests := []struct {
		weight, deathLine float64
		want              bool
	}{
		{0.5, 1.0, true},
		{1.0, 1.0, false}, // equal is not below
		{10.0, 1.0, false},
		{-1.0, 1.0, true},
	}
	for _, tc := range tests {
		if got := ShouldEvaporate(tc.weight, tc.deathLine); got != tc.want {
			t.Errorf("ShouldEvaporate(%f,%f)=%v, want %v", tc.weight, tc.deathLine, got, tc.want)
		}
	}
}

func TestLayerPressure(t *testing.T) {
	if got := LayerPressure(12, 12, 24); got != 0 {
		t.Fatalf("at target = %v, want 0", got)
	}
	if got := LayerPressure(18, 12, 24); got != 0.5 {
		t.Fatalf("mid range = %v, want 0.5", got)
	}
	if got := LayerPressure(24, 12, 24); got != 1 {
		t.Fatalf("at high = %v, want 1", got)
	}
}

func TestRecallScore(t *testing.T) {
	tickDecayK := 0.1

	t.Run("zero tick delta", func(t *testing.T) {
		n := &Node{Energy: 100, LastAccessTick: 5}
		if got := RecallScore(n, 5, tickDecayK); got != 100.0 {
			t.Fatalf("zero delta: got %f, want 100.0", got)
		}
	})
	t.Run("10 ticks ago", func(t *testing.T) {
		n := &Node{Energy: 100, LastAccessTick: 0}
		want := 100.0 / (1.0 + 10.0*tickDecayK) // 100 / 2 = 50
		if got := RecallScore(n, 10, tickDecayK); math.Abs(got-want) > 1e-9 {
			t.Fatalf("10 ticks: got %f, want %f", got, want)
		}
	})
	t.Run("future tick clamped", func(t *testing.T) {
		n := &Node{Energy: 50, LastAccessTick: 10}
		if got := RecallScore(n, 5, tickDecayK); got != 50.0 {
			t.Fatalf("future tick clamped: got %f, want 50.0", got)
		}
	})
}
