package aiaggregator

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
)

func statsProviderWith(stats map[aistatsquery.Key]aistatsquery.Stats) func([]aistatsquery.Key) map[aistatsquery.Key]aistatsquery.Stats {
	return func(keys []aistatsquery.Key) map[aistatsquery.Key]aistatsquery.Stats {
		out := make(map[aistatsquery.Key]aistatsquery.Stats, len(keys))
		for _, k := range keys {
			if s, ok := stats[k]; ok {
				out[k] = s
			} else {
				p, m := k.Split()
				out[k] = aistatsquery.Stats{Provider: p, Model: m}
			}
		}
		return out
	}
}

// TestSmartStrategy_HighLatencyRanksLower verifies that a unit with higher
// average latency is ranked after a unit with lower latency.
func TestSmartStrategy_HighLatencyRanksLower(t *testing.T) {
	stats := map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("fast", "m"): {Requests: 10, AvgLatencyMs: 100, ErrorRate: 0},
		aistatsquery.MakeKey("slow", "m"): {Requests: 10, AvgLatencyMs: 2000, ErrorRate: 0},
	}
	s := &SmartStrategy{
		StatsProvider:      statsProviderWith(stats),
		ErrorRateThreshold: 0.5,
		WLatency:           1.0,
		WError:             1.0,
		WCost:              1.0,
		WTokenRate:         1.0,
		WCongest:           1.0,
		Now:                func() time.Time { return time.Now() },
	}
	units := []CallableUnit{
		{ID: "slow::m", Model: "m", ProviderName: "slow"},
		{ID: "fast::m", Model: "m", ProviderName: "fast"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "fast::m" {
		t.Fatalf("expected fast::m (lower latency), got %q", chosen.ID)
	}
}

// TestSmartStrategy_HighErrorExcluded verifies that a unit whose error rate
// exceeds the threshold is excluded and the healthy unit is selected.
func TestSmartStrategy_HighErrorExcluded(t *testing.T) {
	stats := map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("bad", "m"):  {Requests: 10, ErrorRate: 0.8, AvgLatencyMs: 50},
		aistatsquery.MakeKey("good", "m"): {Requests: 10, ErrorRate: 0.0, AvgLatencyMs: 100},
	}
	s := &SmartStrategy{
		StatsProvider:      statsProviderWith(stats),
		ErrorRateThreshold: 0.5,
		WLatency:           1.0,
		WError:             1.0,
		WCost:              1.0,
		WTokenRate:         1.0,
		WCongest:           1.0,
		Now:                func() time.Time { return time.Now() },
	}
	units := []CallableUnit{
		{ID: "bad::m", Model: "m", ProviderName: "bad"},
		{ID: "good::m", Model: "m", ProviderName: "good"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "good::m" {
		t.Fatalf("expected good::m (bad excluded by error rate), got %q", chosen.ID)
	}
}

// TestSmartStrategy_TokenPlanFastConsumerPenalized verifies that a token-plan
// unit burning its quota too fast is penalized relative to a slow consumer.
func TestSmartStrategy_TokenPlanFastConsumerPenalized(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	expiry := now.Add(1 * time.Hour) // 3600000 ms remaining

	stats := map[aistatsquery.Key]aistatsquery.Stats{
		// fast: 100 requests in a 1-hour window → high actual rate
		aistatsquery.MakeKey("fast", "m"): {Requests: 100, AvgLatencyMs: 100, ErrorRate: 0},
		// slow: 5 requests in a 1-hour window → low actual rate
		aistatsquery.MakeKey("slow", "m"): {Requests: 5, AvgLatencyMs: 100, ErrorRate: 0},
	}
	s := &SmartStrategy{
		StatsProvider:      statsProviderWith(stats),
		ErrorRateThreshold: 0.5,
		WLatency:           1.0,
		WError:             1.0,
		WCost:              1.0,
		WTokenRate:         1.0,
		WCongest:           1.0,
		Now:                func() time.Time { return now },
	}
	units := []CallableUnit{
		{ID: "fast::m", Model: "m", ProviderName: "fast",
			IsTokenPlan: true, TokenPlanRemainingPct: 50,
			TokenPlanExpiresAt: expiry.Format(time.RFC3339),
			TokenPlanWindowMs:  3600000},
		{ID: "slow::m", Model: "m", ProviderName: "slow",
			IsTokenPlan: true, TokenPlanRemainingPct: 50,
			TokenPlanExpiresAt: expiry.Format(time.RFC3339),
			TokenPlanWindowMs:  3600000},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "slow::m" {
		t.Fatalf("expected slow::m (fast penalized for burning quota), got %q", chosen.ID)
	}
}

// TestSmartStrategy_NoStatsFallsBackToPoolOrder verifies that when the stats
// provider is nil (no telemetry), the strategy falls back to pool order.
func TestSmartStrategy_NoStatsFallsBackToPoolOrder(t *testing.T) {
	s := &SmartStrategy{
		StatsProvider:      nil,
		ErrorRateThreshold: 0.5,
		WLatency:           1.0,
		WError:             1.0,
		WCost:              1.0,
		WTokenRate:         1.0,
		WCongest:           1.0,
		Now:                func() time.Time { return time.Now() },
	}
	units := []CallableUnit{
		{ID: "first::m", Model: "m", ProviderName: "first"},
		{ID: "second::m", Model: "m", ProviderName: "second"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "first::m" {
		t.Fatalf("expected first::m (pool order fallback), got %q", chosen.ID)
	}
}

// TestSmartStrategy_AllExceededErrorThresholdFallsBack verifies that when all
// units exceed the error threshold, the strategy falls back to the first match
// rather than failing (telemetry is advisory).
func TestSmartStrategy_AllExceededErrorThresholdFallsBack(t *testing.T) {
	stats := map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("a", "m"): {Requests: 10, ErrorRate: 0.9},
		aistatsquery.MakeKey("b", "m"): {Requests: 10, ErrorRate: 0.8},
	}
	s := &SmartStrategy{
		StatsProvider:      statsProviderWith(stats),
		ErrorRateThreshold: 0.5,
		WLatency:           1.0,
		WError:             1.0,
		WCost:              1.0,
		WTokenRate:         1.0,
		WCongest:           1.0,
		Now:                func() time.Time { return time.Now() },
	}
	units := []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a"},
		{ID: "b::m", Model: "m", ProviderName: "b"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen == nil {
		t.Fatal("expected fallback to first match, got nil")
	}
}

// TestSmartStrategy_SingleCandidatePassThrough verifies the fast path.
func TestSmartStrategy_SingleCandidatePassThrough(t *testing.T) {
	s := NewSmartStrategy(nil, nil)
	units := []CallableUnit{
		{ID: "only::m", Model: "m", ProviderName: "only"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "only::m" {
		t.Fatalf("expected only::m, got %q", chosen.ID)
	}
}

// TestSmartStrategy_CongestedProviderDeprioritized verifies that a unit on a
// heavily loaded provider (high inflight/max) ranks lower than a unit on a
// less loaded provider, even when their latency is identical.
func TestSmartStrategy_CongestedProviderDeprioritized(t *testing.T) {
	stats := map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("busy", "m"): {Requests: 10, AvgLatencyMs: 100, ErrorRate: 0},
		aistatsquery.MakeKey("idle", "m"): {Requests: 10, AvgLatencyMs: 100, ErrorRate: 0},
	}
	usage := map[string]llmclient.ProviderUsage{
		"busy": {Inflight: 9, Max: 10}, // 90% loaded
		"idle": {Inflight: 1, Max: 10}, // 10% loaded
	}
	s := &SmartStrategy{
		StatsProvider:         statsProviderWith(stats),
		ProviderUsageProvider: func(name string) llmclient.ProviderUsage { return usage[name] },
		ErrorRateThreshold:    0.5,
		WLatency:              1.0,
		WError:                1.0,
		WCost:                 1.0,
		WTokenRate:            1.0,
		WCongest:              1.0,
		Now:                   func() time.Time { return time.Now() },
	}
	units := []CallableUnit{
		{ID: "busy::m", Model: "m", ProviderName: "busy"},
		{ID: "idle::m", Model: "m", ProviderName: "idle"},
	}
	chosen, err := s.Select(SelectRequest{}, units)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if chosen.ID != "idle::m" {
		t.Fatalf("expected idle::m (congested provider deprioritized), got %q", chosen.ID)
	}
}
