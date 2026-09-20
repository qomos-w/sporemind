package domain

import "testing"

// TestStreamIdleTimeoutInvariants guards the two-phase idle design: the
// first-token wait (StreamIdleTimeout / PlanGoalStreamIdleTimeout) must exceed
// the inter-chunk window (StreamChunkIdleTimeout), and the inter-chunk window
// must stay under ChildAgentIdleTimeout so a stalled forked child is surfaced
// by the dispatch watchdog before the parent's liveness check could misfire.
func TestStreamIdleTimeoutInvariants(t *testing.T) {
	if StreamChunkIdleTimeout <= 0 {
		t.Fatalf("StreamChunkIdleTimeout must be positive, got %v", StreamChunkIdleTimeout)
	}
	if StreamChunkIdleTimeout >= StreamIdleTimeout {
		t.Errorf("StreamChunkIdleTimeout (%v) must be shorter than first-token StreamIdleTimeout (%v)",
			StreamChunkIdleTimeout, StreamIdleTimeout)
	}
	if StreamChunkIdleTimeout >= PlanGoalStreamIdleTimeout {
		t.Errorf("StreamChunkIdleTimeout (%v) must be shorter than PlanGoalStreamIdleTimeout (%v)",
			StreamChunkIdleTimeout, PlanGoalStreamIdleTimeout)
	}
	if StreamChunkIdleTimeout >= ChildAgentIdleTimeout {
		t.Errorf("StreamChunkIdleTimeout (%v) must be shorter than ChildAgentIdleTimeout (%v); "+
			"otherwise a stalled stream can stay silent past the parent liveness check",
			StreamChunkIdleTimeout, ChildAgentIdleTimeout)
	}
}
