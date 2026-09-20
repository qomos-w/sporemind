package llmclient

import "math"

// CostRate is the price sheet for one provider/model combination. Rates are in
// US dollars per 1,000,000 tokens. The zero value means "unknown / free".
type CostRate struct {
	Input      float64 // non-cache input tokens
	Output     float64 // output tokens
	CacheRead  float64 // cache read/hit tokens
	CacheWrite float64 // cache write/miss tokens; when zero, 1h cache writes
	// fall back to 2*Input for Anthropic-style pricing
	Tiers []CostTier
}

// CostTier applies to the entire request once the input token threshold is
// exceeded. The highest matching tier wins, matching pi's behavior.
type CostTier struct {
	InputTokensAbove int64 // threshold in tokens
	Input            float64
	Output           float64
	CacheRead        float64
	CacheWrite       float64
}

// Cost is the computed dollar amount for a single request, split by category.
type Cost struct {
	Input      float64
	Output     float64
	CacheRead  float64
	CacheWrite float64
	Total      float64
}

// Add returns the sum of two costs.
func (c Cost) Add(o Cost) Cost {
	return Cost{
		Input:      c.Input + o.Input,
		Output:     c.Output + o.Output,
		CacheRead:  c.CacheRead + o.CacheRead,
		CacheWrite: c.CacheWrite + o.CacheWrite,
		Total:      c.Total + o.Total,
	}
}

// EffectiveRate picks the matching tier for the given input token volume. If no
// tier matches, the base rate is returned.
func (r CostRate) EffectiveRate(inputTokens int64) CostRate {
	if len(r.Tiers) == 0 {
		return r
	}
	matched := -1
	for i, tier := range r.Tiers {
		if inputTokens > tier.InputTokensAbove && tier.InputTokensAbove > int64(matched) {
			matched = i
		}
	}
	if matched < 0 {
		return r
	}
	t := r.Tiers[matched]
	if t.Input == 0 {
		t.Input = r.Input
	}
	if t.Output == 0 {
		t.Output = r.Output
	}
	if t.CacheRead == 0 {
		t.CacheRead = r.CacheRead
	}
	if t.CacheWrite == 0 {
		t.CacheWrite = r.CacheWrite
	}
	return CostRate{
		Input:      t.Input,
		Output:     t.Output,
		CacheRead:  t.CacheRead,
		CacheWrite: t.CacheWrite,
	}
}

// CalculateCost returns the cost for a given usage and rate. Unknown rates
// (<=0) leave the corresponding cost component at zero.
func CalculateCost(usage Usage, rate CostRate) Cost {
	effective := rate.EffectiveRate(int64(usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens))

	input := float64(usage.InputTokens) * perMillion(effective.Input)
	output := float64(usage.OutputTokens) * perMillion(effective.Output)
	cacheRead := float64(usage.CacheReadInputTokens) * perMillion(effective.CacheRead)

	cacheWriteRate := effective.CacheWrite
	if cacheWriteRate <= 0 && effective.Input > 0 {
		// Anthropic-style default: 1h cache writes are billed at 2x the base
		// input rate when no explicit cache-write rate is configured.
		cacheWriteRate = effective.Input * 2
	}
	cacheWrite := float64(usage.CacheCreationInputTokens) * perMillion(cacheWriteRate)

	total := input + output + cacheRead + cacheWrite
	return Cost{
		Input:      round6(input),
		Output:     round6(output),
		CacheRead:  round6(cacheRead),
		CacheWrite: round6(cacheWrite),
		Total:      round6(total),
	}
}

func perMillion(rate float64) float64 { return rate / 1_000_000 }

func round6(v float64) float64 {
	if v == 0 {
		return 0
	}
	return math.Round(v*1_000_000) / 1_000_000
}

// CacheHitRate computes the share of prompt tokens that were served from cache.
// It mirrors pi's footer metric: cacheRead / (input + cacheRead + cacheWrite).
// Returns 0 when the denominator is zero.
func CacheHitRate(inputTokens, cacheWriteTokens, cacheReadTokens int) float64 {
	denom := inputTokens + cacheWriteTokens + cacheReadTokens
	if denom <= 0 {
		return 0
	}
	return float64(cacheReadTokens) / float64(denom)
}
