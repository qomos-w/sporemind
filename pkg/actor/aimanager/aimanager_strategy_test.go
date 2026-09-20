package aimanager

import "testing"

// normalizeStrategy is the single source of truth for the aggregator strategy
// enum on the wire. These tests pin its contract: every accepted alias maps to
// one of the four canonical names, and the default is round_robin.

func TestNormalizeStrategy_CanonicalNames(t *testing.T) {
	cases := map[string]string{
		"round_robin": "round_robin",
		"fallback":    "fallback",
		"smart":       "smart",
		"standard":    "standard",
	}
	for in, want := range cases {
		if got := normalizeStrategy(in); got != want {
			t.Errorf("normalizeStrategy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeStrategy_EmptyAndUnknownDefaultToRoundRobin(t *testing.T) {
	for _, in := range []string{"", "   ", "unknown", "random"} {
		if got := normalizeStrategy(in); got != "round_robin" {
			t.Errorf("normalizeStrategy(%q) = %q, want round_robin (default)", in, got)
		}
	}
}

func TestNormalizeStrategy_RoundRobinAliases(t *testing.T) {
	for _, in := range []string{"round-robin", "round_robin", "roundrobin", "ROUND-ROBIN", " Round_Robin "} {
		if got := normalizeStrategy(in); got != "round_robin" {
			t.Errorf("normalizeStrategy(%q) = %q, want round_robin", in, got)
		}
	}
}

func TestNormalizeStrategy_LatencyAwareAliasesToSmart(t *testing.T) {
	for _, in := range []string{"latency-aware", "latency_aware", "latencyaware", "LATENCY-AWARE"} {
		if got := normalizeStrategy(in); got != "smart" {
			t.Errorf("normalizeStrategy(%q) = %q, want smart", in, got)
		}
	}
}

func TestNormalizeStrategy_LegacyPriorityUsesFallback(t *testing.T) {
	if got := normalizeStrategy("priority"); got != "fallback" {
		t.Errorf("normalizeStrategy(priority) = %q, want fallback", got)
	}
}

func TestNormalizeStrategy_LegacyStickyUsesStandard(t *testing.T) {
	for _, in := range []string{"sticky", "STICKY", " Sticky "} {
		if got := normalizeStrategy(in); got != "standard" {
			t.Errorf("normalizeStrategy(%q) = %q, want standard", in, got)
		}
	}
}

func TestNormalizeStrategy_CaseInsensitive(t *testing.T) {
	cases := map[string]string{
		"FALLBACK": "fallback",
		"Priority": "fallback",
		"SMART":    "smart",
		"STANDARD": "standard",
	}
	for in, want := range cases {
		if got := normalizeStrategy(in); got != want {
			t.Errorf("normalizeStrategy(%q) = %q, want %q", in, got, want)
		}
	}
}
