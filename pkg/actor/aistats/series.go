package aistats

import (
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

type bucketAccum struct {
	bucket     gen.AIStatsBucket
	latency    []float64
	ttft       []float64
	latencySum int64
}

type modelAccum struct {
	stat       gen.AIStatsModelStat
	latency    []float64
	ttft       []float64
	latencySum int64
}

func (a *Actor) handleSeries(_ actor.PureContext, req gen.AIStatsSeriesReq) (gen.AIStatsSeriesResp, error) {
	return a.seriesAt(time.Now().UTC(), req)
}

// seriesAt is handleSeries with an injected clock.
//
// Merged read path (设计 §4–§5): the raw segment keeps the per-record
// bucketing untouched; on top of it the in-range rollup documents contribute
// day-grain (L1) and month-grain (L2) buckets flagged rollup=true with the
// grain declared. Day buckets carry the document's frozen percentile
// snapshot; month buckets have no percentiles (0, declared unavailable at
// month grain). ModelFilter and scope/workspace filters gate document
// inclusion by dimension; bucket counters stay document-level (declared
// approximation — a rollup document cannot be re-split per record).
func (a *Actor) seriesAt(now time.Time, req gen.AIStatsSeriesReq) (gen.AIStatsSeriesResp, error) {
	since, _ := time.Parse(time.RFC3339Nano, req.Since)
	until, _ := time.Parse(time.RFC3339Nano, req.Until)

	// Serve recent ranges from the hot cache; older ranges fall back to disk.
	a.mu.RLock()
	hot := a.hotRecords.snapshot()
	a.mu.RUnlock()

	rr, err := a.planRollupRead(since, until, now)
	if err != nil {
		return gen.AIStatsSeriesResp{}, err
	}

	var records []gen.AIStatsRecord
	if canServeFromHot(hot, since, until) {
		records = filterHotRecords(hot, since, until, req.Scope, req.ScopeID, req.WorkspaceID)
	} else {
		var err error
		records, err = loadRecords(a.store, a.scanner, since, until, req.Scope, req.ScopeID, req.WorkspaceID)
		if err != nil {
			return gen.AIStatsSeriesResp{}, err
		}
	}
	records = excludeRollupOwnedRecords(records, rr)

	// Apply the ModelFilter before aggregation so every downstream view
	// (buckets, model stats, overall metrics) reflects only matched records.
	records = filterRecordsByModel(records, req.ModelFilter)

	resp, err := a.aggregateSeries(records, req.BucketMs)
	if err != nil {
		return gen.AIStatsSeriesResp{}, err
	}

	// Merge the rollup buckets. A rollup bucket colliding with a raw bucket
	// (only possible when bucketMs > 24h spans the rawFloor boundary, or
	// after a forced rollup of an in-window day) folds its counters into the
	// raw bucket: raw samples win the percentiles, sums stay exact.
	rollupBuckets := rr.buildRollupBuckets(req.Scope, req.ScopeID, req.WorkspaceID, req.ModelFilter, effectiveBucketMs(req.BucketMs))
	for i := range rollupBuckets {
		rb := rollupBuckets[i]
		if raw := findBucket(resp.Buckets, rb.Start); raw != nil {
			raw.Requests += rb.Requests
			raw.Errors += rb.Errors
			raw.InputTokens += rb.InputTokens
			raw.OutputTokens += rb.OutputTokens
			raw.CacheRead += rb.CacheRead
			raw.CacheWrite += rb.CacheWrite
			raw.ReasoningTokens += rb.ReasoningTokens
			raw.Cost += rb.Cost
			raw.LatencySumMs += rb.LatencySumMs
			continue
		}
		resp.Buckets = append(resp.Buckets, rb)
	}
	sort.Slice(resp.Buckets, func(i, j int) bool {
		return resp.Buckets[i].Start < resp.Buckets[j].Start
	})
	return resp, nil
}

// effectiveBucketMs normalizes the requested bucket width the same way
// aggregateSeries does, so the rollup bucket keys use identical semantics.
func effectiveBucketMs(bucketMs int64) int64 {
	if bucketMs <= 0 {
		return int64(24 * time.Hour / time.Millisecond)
	}
	return bucketMs
}

func findBucket(buckets []gen.AIStatsBucket, start int64) *gen.AIStatsBucket {
	for i := range buckets {
		if buckets[i].Start == start {
			return &buckets[i]
		}
	}
	return nil
}

// filterRecordsByModel keeps records whose Provider or Model contains filter
// as a case-insensitive substring. An empty (or whitespace-only) filter keeps
// every record; the returned slice aliases the input in that case.
func filterRecordsByModel(records []gen.AIStatsRecord, filter string) []gen.AIStatsRecord {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return records
	}
	kept := make([]gen.AIStatsRecord, 0, len(records))
	for _, r := range records {
		if strings.Contains(strings.ToLower(r.Provider), filter) ||
			strings.Contains(strings.ToLower(r.Model), filter) {
			kept = append(kept, r)
		}
	}
	return kept
}

// aggregateSeries computes bucket and per-model statistics from a set of
// records. It is shared by the hot (in-memory) and cold (disk) series paths.
func (a *Actor) aggregateSeries(records []gen.AIStatsRecord, bucketMs int64) (gen.AIStatsSeriesResp, error) {
	if bucketMs <= 0 {
		bucketMs = int64(24 * time.Hour / time.Millisecond)
	}

	bucketMap := make(map[int64]*bucketAccum)
	modelMap := make(map[string]*modelAccum)
	var overallLatency, overallTtft []float64
	var overallLatencySum int64
	var overallOutput int64

	for _, r := range records {
		completedAt, _ := time.Parse(time.RFC3339Nano, r.CompletedAt)
		if completedAt.IsZero() {
			continue
		}
		ts := completedAt.UnixMilli()
		bucketKey := (ts / bucketMs) * bucketMs

		ba := bucketMap[bucketKey]
		if ba == nil {
		ba = &bucketAccum{
			bucket: gen.AIStatsBucket{
				Start:      bucketKey,
				End:        bucketKey + bucketMs,
				ErrorCodes: make(map[string]int64),
				Grain:      "raw",
			},
		}
			bucketMap[bucketKey] = ba
		}
		ba.bucket.Requests++

		isErr := r.StopReason == "error" || r.StopReason == "aborted" || r.ErrorMessage != ""
		if isErr {
			ba.bucket.Errors++
			code := r.ErrorCode
			if code == "" {
				code = "unknown"
			}
			ba.bucket.ErrorCodes[code]++
		}
		if r.Usage != nil {
			u := r.Usage
			ba.bucket.InputTokens += u.InputTokens
			ba.bucket.OutputTokens += u.OutputTokens
			ba.bucket.CacheRead += u.CacheReadInputTokens
			ba.bucket.CacheWrite += u.CacheCreationInputTokens
			ba.bucket.ReasoningTokens += u.ReasoningTokens
			ba.bucket.Cost += u.CostTotal
		}
		if r.LatencyMs > 0 {
			ba.latency = append(ba.latency, float64(r.LatencyMs))
			ba.latencySum += r.LatencyMs
		}
		if r.FirstTokenMs > 0 {
			ba.ttft = append(ba.ttft, float64(r.FirstTokenMs))
		}

		if r.Provider != "" && r.Model != "" {
			mk := r.Provider + "/" + r.Model
			ma := modelMap[mk]
			if ma == nil {
				ma = &modelAccum{
					stat: gen.AIStatsModelStat{
						Provider: r.Provider,
						Model:    r.Model,
					},
				}
				modelMap[mk] = ma
			}
			ma.stat.Requests++
			if isErr {
				ma.stat.Errors++
			}
			if r.Usage != nil {
				ma.stat.InputTokens += r.Usage.InputTokens
				ma.stat.OutputTokens += r.Usage.OutputTokens
				ma.stat.CacheRead += r.Usage.CacheReadInputTokens
				ma.stat.CacheWrite += r.Usage.CacheCreationInputTokens
				ma.stat.CostTotal += r.Usage.CostTotal
				ma.stat.CostInput += r.Usage.CostInput
				ma.stat.CostOutput += r.Usage.CostOutput
				ma.stat.CostCacheRead += r.Usage.CostCacheRead
				ma.stat.CostCacheWrite += r.Usage.CostCacheWrite
			}
			if r.LatencyMs > 0 {
				ma.latency = append(ma.latency, float64(r.LatencyMs))
				ma.latencySum += r.LatencyMs
			}
			if r.FirstTokenMs > 0 {
				ma.ttft = append(ma.ttft, float64(r.FirstTokenMs))
			}
		}

		if r.LatencyMs > 0 {
			overallLatency = append(overallLatency, float64(r.LatencyMs))
			overallLatencySum += r.LatencyMs
		}
		if r.FirstTokenMs > 0 {
			overallTtft = append(overallTtft, float64(r.FirstTokenMs))
		}
		if r.Usage != nil {
			overallOutput += r.Usage.OutputTokens
		}
	}

	var buckets []gen.AIStatsBucket
	for _, ba := range bucketMap {
		sort.Float64s(ba.latency)
		sort.Float64s(ba.ttft)
		ba.bucket.LatencyP50 = percentileFloat(ba.latency, 50)
		ba.bucket.LatencyP95 = percentileFloat(ba.latency, 95)
		ba.bucket.TtftP50 = percentileFloat(ba.ttft, 50)
		ba.bucket.LatencySumMs = ba.latencySum
		buckets = append(buckets, ba.bucket)
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Start < buckets[j].Start
	})

	var modelStats []gen.AIStatsModelStat
	for _, ma := range modelMap {
		sort.Float64s(ma.latency)
		sort.Float64s(ma.ttft)
		ma.stat.LatencyP95 = percentileFloat(ma.latency, 95)
		ma.stat.TtftP50 = percentileFloat(ma.ttft, 50)
		ma.stat.LatencySumMs = ma.latencySum
		modelStats = append(modelStats, ma.stat)
	}
	sort.Slice(modelStats, func(i, j int) bool {
		return modelStats[i].Requests > modelStats[j].Requests
	})

	sort.Float64s(overallLatency)
	sort.Float64s(overallTtft)

	return gen.AIStatsSeriesResp{
		Buckets:             buckets,
		ModelStats:          modelStats,
		OverallLatencyP50:   percentileFloat(overallLatency, 50),
		OverallLatencyP95:   percentileFloat(overallLatency, 95),
		OverallTtftP50:      percentileFloat(overallTtft, 50),
		OverallLatencySumMs: overallLatencySum,
		OverallOutputTokens: overallOutput,
	}, nil
}
