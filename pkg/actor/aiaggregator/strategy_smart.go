package aiaggregator

import (
	"sort"
	"time"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
)

// SmartStrategy is a load-aware selection strategy. After the hard filters
// (token-plan) in selectUnit, it reads cached aistats telemetry and per-provider
// concurrency usage for each surviving candidate, excludes units whose error
// rate exceeds a threshold, and ranks the rest by a weighted score:
//
//	score = w_latency * normalizedLatency
//	      + w_error   * errorRate
//	      + w_cost    * normalizedCost
//	      + w_token   * tokenRatePenalty
//	      + w_congest * providerCongestion
//
// Lower score = better. The provider congestion factor deprioritizes units
// whose provider is near its MaxConcurrency limit (the global gate will queue
// them, so preferring a less loaded provider reduces wait time). All data
// comes from the local async cache — the strategy never blocks.
//
// SmartStrategy is NOT the default — it must be explicitly configured via the
// aggregator's strategy field. The default remains round-robin to preserve
// existing behavior.
type SmartStrategy struct {
	// StatsProvider returns a snapshot of cached per-unit telemetry. It
	// never blocks on an actor call — the cache is populated by a background
	// refresher. May return an empty map when no telemetry is available yet.
	StatsProvider func(keys []aistatsquery.Key) map[aistatsquery.Key]aistatsquery.Stats

	// ProviderUsageProvider returns the cached per-provider concurrency
	// snapshot. Never blocks — reads from the local cache populated by the
	// background refresher.
	ProviderUsageProvider func(providerName string) llmclient.ProviderUsage

	// ErrorRateThreshold is the maximum tolerated error rate in [0,1]. Units
	// above this are excluded. Default 0.5 if zero.
	ErrorRateThreshold float64

	// Weights for the scoring function. All default to equal weight (1.0) when
	// zero.
	WLatency   float64
	WError     float64
	WCost      float64
	WTokenRate float64
	WCongest   float64

	// Now is the clock function for expiry computation. Defaults to time.Now
	// when nil.
	Now func() time.Time
}

// NewSmartStrategy returns a SmartStrategy with the given stats and provider
// usage providers, and default weights.
func NewSmartStrategy(
	statsProvider func(keys []aistatsquery.Key) map[aistatsquery.Key]aistatsquery.Stats,
	usageProvider func(providerName string) llmclient.ProviderUsage,
) *SmartStrategy {
	return &SmartStrategy{
		StatsProvider:         statsProvider,
		ProviderUsageProvider: usageProvider,
		ErrorRateThreshold:    0.5,
		WLatency:              1.0,
		WError:                1.0,
		WCost:                 1.0,
		WTokenRate:            1.0,
		WCongest:              1.0,
		Now:                   time.Now,
	}
}

func (s *SmartStrategy) Select(req SelectRequest, units []CallableUnit) (*CallableUnit, error) {
	matched := matchUnits(req.Unit, units)
	if len(matched) == 0 {
		return nil, noMatchError(req.Unit)
	}
	if len(matched) == 1 {
		return &matched[0], nil
	}

	now := s.now()
	threshold := s.ErrorRateThreshold
	if threshold <= 0 {
		threshold = 0.5
	}

	// Fetch stats from the local cache snapshot (never blocks).
	var stats map[aistatsquery.Key]aistatsquery.Stats
	if s.StatsProvider != nil {
		keys := make([]aistatsquery.Key, 0, len(matched))
		for _, u := range matched {
			keys = append(keys, aistatsquery.MakeKey(u.ProviderName, u.Model))
		}
		stats = s.StatsProvider(keys)
	}

	// Collect scored candidates, excluding high-error units.
	type scored struct {
		idx   int
		score float64
	}
	candidates := make([]scored, 0, len(matched))
	for i, u := range matched {
		st := stats[aistatsquery.MakeKey(u.ProviderName, u.Model)]
		if st.Requests > 0 && st.ErrorRate > threshold {
			continue
		}
		candidates = append(candidates, scored{idx: i, score: s.scoreUnit(u, st, now)})
	}
	if len(candidates) == 0 {
		// All units exceeded the error threshold; fall back to the first match
		// rather than failing the dispatch (telemetry is advisory, not a hard
		// gate — a cold unit with no history should not block the request).
		return &matched[0], nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return candidates[i].idx < candidates[j].idx
	})
	return &matched[candidates[0].idx], nil
}

// scoreUnit computes a lower-is-better score. Dimensions:
//   - latency: avg latency, normalized by the max avg among candidates (caller
//     normalizes externally; here we use the raw value so relative ordering
//     is preserved within one Select call).
//   - error: error rate [0,1].
//   - cost: cost per request (CostTotal / Requests).
//   - token rate: penalty for consuming a token plan too fast relative to the
//     target rate. Only applies to token-plan units with a valid expiry window.
//   - congestion: provider inflight / Max ratio [0,1+]. Higher = more loaded,
//     so the strategy prefers providers with spare capacity (the gate queues
//     at saturation, so routing to a less loaded provider reduces wait time).
func (s *SmartStrategy) scoreUnit(u CallableUnit, st aistatsquery.Stats, now time.Time) float64 {
	wLat := s.normWeight(s.WLatency)
	wErr := s.normWeight(s.WError)
	wCost := s.normWeight(s.WCost)
	wTok := s.normWeight(s.WTokenRate)
	wCon := s.normWeight(s.WCongest)

	latency := st.AvgLatencyMs
	errRate := st.ErrorRate
	costPerReq := 0.0
	if st.Requests > 0 {
		costPerReq = st.CostTotal / float64(st.Requests)
	}

	tokenPenalty := 0.0
	if u.IsTokenPlan && u.TokenPlanWindowMs > 0 && u.TokenPlanExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, u.TokenPlanExpiresAt); err == nil && exp.After(now) {
			remainingMs := exp.Sub(now).Milliseconds()
			if remainingMs > 0 {
				targetRate := float64(u.TokenPlanRemainingPct) / float64(remainingMs)
				if st.Requests > 0 && u.TokenPlanWindowMs > 0 {
					actualRate := float64(st.Requests) / float64(u.TokenPlanWindowMs)
					if targetRate > 0 {
						ratio := actualRate / targetRate
						if ratio > 1 {
							tokenPenalty = ratio - 1
						} else {
							tokenPenalty = -(1 - ratio) * 0.5
						}
					}
				}
			}
		}
	}

	congestion := 0.0
	if s.ProviderUsageProvider != nil {
		usage := s.ProviderUsageProvider(u.ProviderName)
		if usage.Max > 0 {
			congestion = float64(usage.Inflight) / float64(usage.Max)
		}
	}

	return wLat*latency + wErr*errRate*1000 + wCost*costPerReq + wTok*tokenPenalty*100 + wCon*congestion*100
}

func (s *SmartStrategy) normWeight(w float64) float64 {
	if w == 0 {
		return 1.0
	}
	return w
}

func (s *SmartStrategy) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
