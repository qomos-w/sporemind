package llmclient

import (
	"math"
	"testing"
)

func TestCalculateCost(t *testing.T) {
	// Anthropic-style: input 3, output 15, cache read 0.375, cache write 3.75
	// per 1M tokens. 1h cache write defaults to 2x input = 6.
	rate := CostRate{
		Input:      3.0,
		Output:     15.0,
		CacheRead:  0.375,
		CacheWrite: 3.75,
	}
	usage := Usage{
		InputTokens:              1000,
		OutputTokens:             200,
		CacheReadInputTokens:     500,
		CacheCreationInputTokens: 100,
	}
	cost := CalculateCost(usage, rate)
	if cost.Input != 0.003 {
		t.Errorf("input cost: got %v, want 0.003", cost.Input)
	}
	if cost.Output != 0.003 {
		t.Errorf("output cost: got %v, want 0.003", cost.Output)
	}
	if cost.CacheRead != 0.000188 {
		t.Errorf("cache read cost: got %v, want 0.000188", cost.CacheRead)
	}
	if cost.CacheWrite != 0.000375 {
		t.Errorf("cache write cost: got %v, want 0.000375", cost.CacheWrite)
	}
	wantTotal := 0.003 + 0.003 + 0.000188 + 0.000375
	if math.Abs(cost.Total-wantTotal) > 1e-9 {
		t.Errorf("total cost: got %v, want %v", cost.Total, wantTotal)
	}
}

func TestCalculateCost_DefaultCacheWrite(t *testing.T) {
	// When CacheWrite is unset, 1h cache writes fall back to 2x input.
	rate := CostRate{Input: 3.0, Output: 15.0, CacheRead: 0.375}
	usage := Usage{CacheCreationInputTokens: 100}
	cost := CalculateCost(usage, rate)
	want := 100 * (3.0 * 2) / 1_000_000
	if math.Abs(cost.CacheWrite-want) > 1e-9 {
		t.Errorf("cache write cost: got %v, want %v", cost.CacheWrite, want)
	}
}

func TestCalculateCost_Tier(t *testing.T) {
	rate := CostRate{
		Input:  3.0,
		Output: 15.0,
		Tiers: []CostTier{
			{InputTokensAbove: 1000, Input: 2.0, Output: 10.0},
		},
	}
	// 1001 input tokens exceeds the 1000 threshold, so the tier applies.
	usage := Usage{InputTokens: 1001, OutputTokens: 100}
	cost := CalculateCost(usage, rate)
	wantInput := 1001 * 2.0 / 1_000_000
	wantOutput := 100 * 10.0 / 1_000_000
	if math.Abs(cost.Input-wantInput) > 1e-9 {
		t.Errorf("input cost: got %v, want %v", cost.Input, wantInput)
	}
	if math.Abs(cost.Output-wantOutput) > 1e-9 {
		t.Errorf("output cost: got %v, want %v", cost.Output, wantOutput)
	}
}

func TestCalculateCost_ZeroRate(t *testing.T) {
	usage := Usage{InputTokens: 1000, OutputTokens: 200}
	cost := CalculateCost(usage, CostRate{})
	if cost.Total != 0 {
		t.Errorf("expected zero cost, got %v", cost.Total)
	}
}
