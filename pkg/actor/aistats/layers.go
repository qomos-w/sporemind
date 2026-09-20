package aistats

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// This file implements the merged read path across the three retention
// layers (设计：aistats 超时降粒度保留方案 §4–§5): a query/series/export
// window is served by the raw record layer where records still exist and by
// rollup documents where the convolution has already reclaimed them.
//
// Read-source criterion (§5, the anti-leak rule): the read source for a UTC
// day is decided PER DAY by rollup-document existence, not by a whole-range
// time cutoff — a day whose daily document exists reads L1 even if raw
// records are still present (the crash window between doc write and raw
// deletion), and a day inside a month whose monthly document exists reads
// L2. A day with NO rollup document falls back to raw records even when the
// day is already outside the N-day window and still sitting in the
// convolution backlog. Consequence: raw records whose day/month is
// doc-owned are excluded from every raw result (excludeRollupOwnedRecords),
// so any given day's statistics are read exactly once (I2).
//
// Lock discipline: rollup documents are immutable after write (straggler
// absorption rewrites them only under a.mu during a rollup window), so the
// read path loads them WITHOUT holding a.mu. Enumeration goes through the
// Lister / PrefixScanner snapshot interfaces — never a locked scan.

// layerSegment is one contiguous span of a query window with a single read
// strategy. coarse segments are answered from rollup documents (L1/L2) plus
// raw fallback for days the convolution has not reached yet; raw segments
// keep the existing hot-cache / loadRecords behavior.
type layerSegment struct {
	coarse bool
	start  time.Time
	end    time.Time
}

// layerPlan is the layer segmentation of [since, until) per 设计 §4.
type layerPlan struct {
	rawFloor   time.Time // startOfDayUTC(now) - AistatsRawDays(): L0 lower bound
	dailyFloor time.Time // startOfMonthUTC(now) - AistatsDailyDays(): L1 lower bound
	segments   []layerSegment
}

// planLayers splits [since, until) at rawFloor into a coarse segment and a
// raw segment. until zero means "now". Note the floors only PLAN which
// documents to enumerate — the actual read source is still decided per day
// by rollup-document existence (§5), so dailyFloor is advisory: a month
// older than dailyFloor whose daily documents are incomplete keeps
// contributing L1 day buckets, and a month younger than dailyFloor that
// somehow holds a monthly document is read at L2.
func planLayers(since, until time.Time, now time.Time) layerPlan {
	rawFloor := startOfDayUTC(now).AddDate(0, 0, -config.AistatsRawDays())
	dailyFloor := startOfMonthUTC(now).AddDate(0, 0, -config.AistatsDailyDays())

	end := until
	if end.IsZero() {
		end = now
	}
	var segs []layerSegment
	if since.Before(rawFloor) {
		segs = append(segs, layerSegment{coarse: true, start: since, end: minTime(end, rawFloor)})
	}
	if end.After(rawFloor) {
		segs = append(segs, layerSegment{coarse: false, start: maxTime(since, rawFloor), end: end})
	}
	return layerPlan{rawFloor: rawFloor, dailyFloor: dailyFloor, segments: segs}
}

func startOfMonthUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// rollupRead is the planned rollup contribution for one query window: the
// in-range daily/monthly documents plus the day/month ownership sets used
// to exclude doc-owned raw records.
type rollupRead struct {
	daily  []*rollupDoc // daily docs overlapping the window, chronological
	monthly []*rollupDoc // monthly docs overlapping the window, chronological

	ownedDaily  map[string]bool // "2006-01-02" with a daily document
	ownedMonths map[string]bool // "2006-01" with a monthly document
}

// planRollupRead enumerates and loads the rollup documents overlapping
// [since, until). now is injected for testability. Returns an empty plan
// when the backend cannot list (graceful degradation to the pure raw path).
func (a *Actor) planRollupRead(since, until time.Time, now time.Time) (*rollupRead, error) {
	rr := &rollupRead{
		ownedDaily:  map[string]bool{},
		ownedMonths: map[string]bool{},
	}
	lister, ok := a.store.(persist.Lister)
	if !ok {
		return rr, nil
	}
	end := until
	if end.IsZero() {
		end = now
	}
	startDay := time.Time{}
	if !since.IsZero() {
		startDay = startOfDayUTC(since)
	}

	dailyNames, err := lister.List("rollups/daily/")
	if err != nil {
		return nil, fmt.Errorf("aistats: list daily rollups: %w", err)
	}
	for _, name := range dailyNames {
		rest := strings.TrimPrefix(name, "rollups/daily/")
		if len(rest) != len("YYYY-MM/DD") || rest[7] != '/' {
			continue
		}
		day := rest[:7] + "-" + rest[8:10]
		dayStart, err := time.ParseInLocation("2006-01-02", day, time.UTC)
		if err != nil {
			continue
		}
		rr.ownedDaily[day] = true
		// All-or-nothing at day grain: the document covers the whole UTC
		// day, so a window overlapping the day at all takes the rollup.
		if !startDay.IsZero() && dayStart.Before(startDay) {
			continue
		}
		if !dayStart.Before(end) {
			continue
		}
		doc, err := loadRollupDoc(a.store, name)
		if err != nil {
			return nil, err
		}
		if doc != nil {
			rr.daily = append(rr.daily, doc)
		}
	}
	sort.Slice(rr.daily, func(i, j int) bool { return rr.daily[i].Period < rr.daily[j].Period })

	monthlyNames, err := lister.List("rollups/monthly/")
	if err != nil {
		return nil, fmt.Errorf("aistats: list monthly rollups: %w", err)
	}
	for _, name := range monthlyNames {
		month := strings.TrimPrefix(name, "rollups/monthly/")
		if len(month) != len("YYYY-MM") {
			continue
		}
		monthStart, err := time.ParseInLocation("2006-01", month, time.UTC)
		if err != nil {
			continue
		}
		monthEnd := monthStart.AddDate(0, 1, 0)
		rr.ownedMonths[month] = true
		if !monthStart.Before(end) {
			continue
		}
		if !startDay.IsZero() && !monthEnd.After(startDay) {
			continue
		}
		doc, err := loadRollupDoc(a.store, name)
		if err != nil {
			return nil, err
		}
		if doc != nil {
			rr.monthly = append(rr.monthly, doc)
		}
	}
	sort.Slice(rr.monthly, func(i, j int) bool { return rr.monthly[i].Period < rr.monthly[j].Period })
	return rr, nil
}

// excludeRollupOwnedRecords drops raw records whose UTC day (or month) is
// owned by a rollup document — the per-day read-source criterion (§5).
// Applied uniformly to hot-cache and disk results so a day is never counted
// through both layers.
func excludeRollupOwnedRecords(records []gen.AIStatsRecord, rr *rollupRead) []gen.AIStatsRecord {
	if len(rr.ownedDaily) == 0 && len(rr.ownedMonths) == 0 {
		return records
	}
	kept := records[:0]
	for _, r := range records {
		t, err := time.Parse(time.RFC3339Nano, r.CompletedAt)
		if err != nil || t.IsZero() {
			kept = append(kept, r)
			continue
		}
		t = t.UTC()
		day := t.Format("2006-01-02")
		if rr.ownedDaily[day] || rr.ownedMonths[t.Format("2006-01")] {
			continue
		}
		kept = append(kept, r)
	}
	return kept
}

// rollupCountersMatch extracts the counters matching a query's dimension
// filters from one rollup document (设计 §4 "匹配 scope/workspace 的 rollup
// Views 计数器"). Dimension maps are not cross-filterable (a session view
// is not per-workspace), so when both scope and workspace are set the scope
// dimension wins and the workspace constraint is approximated away —
// declared degradation, exact only along a single dimension. Monthly
// documents carry no Session/Project/Agent views, so a session/project/
// agent scope matches nothing there (I5: the L2 segment contributes 0).
func rollupCountersMatch(doc *rollupDoc, scope, scopeID, workspaceID string) gen.AIStatsCounters {
	if scopeID != "" {
		switch scope {
		case "session":
			return doc.Views.Session[scopeID]
		case "project":
			return doc.Views.Project[scopeID]
		case "agent":
			return doc.Views.Agent[scopeID]
		case "workspace":
			return doc.Views.WorkspaceMap[scopeID]
		case "provider":
			return doc.Views.Provider[scopeID]
		case "model":
			return doc.Views.Model[scopeID]
		}
	}
	if workspaceID != "" {
		return doc.Views.WorkspaceMap[workspaceID]
	}
	return doc.Views.Workspace
}

// rollupDocMatchesModelFilter reports whether any provider/model dimension
// key of the document matches the case-insensitive substring filter. Bucket
// counters stay document-level (a rollup doc cannot be split per model) —
// declared approximation of the raw path's record-level filter (设计 §4).
func rollupDocMatchesModelFilter(doc *rollupDoc, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	for key := range doc.Views.Model {
		if strings.Contains(strings.ToLower(key), filter) {
			return true
		}
	}
	for provider := range doc.Views.Provider {
		if strings.Contains(strings.ToLower(provider), filter) {
			return true
		}
	}
	return false
}

// matchedRollupCounters sums the dimension-matched counters of every
// in-range rollup document — the coarse-segment contribution merged into
// aistats.query responses.
func (rr *rollupRead) matchedRollupCounters(scope, scopeID, workspaceID string) gen.AIStatsCounters {
	var out gen.AIStatsCounters
	for _, doc := range rr.daily {
		out = addCounters(out, rollupCountersMatch(doc, scope, scopeID, workspaceID))
	}
	for _, doc := range rr.monthly {
		out = addCounters(out, rollupCountersMatch(doc, scope, scopeID, workspaceID))
	}
	return out
}

// ---------------------------------------------------------------------------
// Series buckets from rollup documents
// ---------------------------------------------------------------------------

// rollupBucketAccum accumulates one output bucket built from rollup
// documents. Unlike raw buckets there are no per-record samples: percentiles
// come from the daily document's frozen snapshot and can neither be
// recomputed nor merged exactly across documents.
type rollupBucketAccum struct {
	bucket    gen.AIStatsBucket
	sources   int           // how many rollup documents merged into the bucket
	lat       rollupLatency // frozen snapshot of the single contributing doc
	grain     string        // "day" | "month"
	dayMillis int64         // granularity floor of the bucket content
}

// dayGrainMillis is the declared granularity floor of a daily rollup
// document: 24h. Buckets requested finer than this still get one bucket per
// day (设计 §4: 粒度下限).
const dayGrainMillis = int64(24 * time.Hour / time.Millisecond)

// bucketKeyFor returns the output-bucket key for a rollup period starting
// at periodStart (UTC). When the requested bucketMs is coarser than the
// rollup grain, consecutive periods merge into one bucket by floor-dividing;
// when finer, each period keeps its own bucket (granularity floor).
func bucketKeyFor(periodStart time.Time, bucketMs, grainMillis int64) int64 {
	startMs := periodStart.UnixMilli()
	if bucketMs >= grainMillis {
		return (startMs / bucketMs) * bucketMs
	}
	return startMs
}

// newRollupBucketAccum opens an accumulator for one rollup period.
func newRollupBucketAccum(periodStart time.Time, grainMillis int64, matched gen.AIStatsCounters, lat rollupLatency, grain string, bucketMs int64) *rollupBucketAccum {
	key := bucketKeyFor(periodStart, bucketMs, grainMillis)
	b := gen.AIStatsBucket{
		Start:           key,
		End:             key + maxInt64(bucketMs, grainMillis),
		Requests:        matched.RequestCount,
		Errors:          matched.ErrorCount,
		InputTokens:     matched.InputTokens,
		OutputTokens:    matched.OutputTokens,
		CacheRead:       matched.CacheReadInputTokens,
		CacheWrite:      matched.CacheCreationInputTokens,
		ReasoningTokens: matched.ReasoningTokens,
		Cost:            matched.CostTotal,
		LatencySumMs:    lat.SumMs,
		LatencyP50:      lat.P50,
		LatencyP95:      lat.P95,
		TtftP50:         lat.TtftP50,
		ErrorCodes:      map[string]int64{},
		Rollup:          true,
		Grain:           grain,
	}
	return &rollupBucketAccum{bucket: b, sources: 1, lat: lat, grain: grain, dayMillis: grainMillis}
}

// mergeRollupBucketAccum folds another rollup period into an existing
// accumulator. Counters add exactly; the frozen percentiles cannot merge —
// once a second source lands they are zeroed (declared degradation, 设计
// §4). LatencySumMs still adds because the sum is exact.
func mergeRollupBucketAccum(acc *rollupBucketAccum, matched gen.AIStatsCounters, lat rollupLatency) {
	acc.bucket.Requests += matched.RequestCount
	acc.bucket.Errors += matched.ErrorCount
	acc.bucket.InputTokens += matched.InputTokens
	acc.bucket.OutputTokens += matched.OutputTokens
	acc.bucket.CacheRead += matched.CacheReadInputTokens
	acc.bucket.CacheWrite += matched.CacheCreationInputTokens
	acc.bucket.ReasoningTokens += matched.ReasoningTokens
	acc.bucket.Cost += matched.CostTotal
	acc.bucket.LatencySumMs += lat.SumMs
	acc.sources++
	acc.bucket.LatencyP50 = 0
	acc.bucket.LatencyP95 = 0
	acc.bucket.TtftP50 = 0
}

// buildRollupBuckets converts the in-range rollup documents into output
// buckets. Documents whose dimension match is empty are skipped (they hold
// no records for this query — e.g. an L2 month under a session scope, I5).
// ModelFilter gates inclusion on the provider/model dimension.
func (rr *rollupRead) buildRollupBuckets(scope, scopeID, workspaceID, modelFilter string, bucketMs int64) []gen.AIStatsBucket {
	accs := map[int64]*rollupBucketAccum{}
	var keys []int64

	put := func(key int64, fresh func() *rollupBucketAccum, merge func(*rollupBucketAccum)) {
		acc := accs[key]
		if acc == nil {
			acc = fresh()
			accs[key] = acc
			keys = append(keys, key)
		} else {
			merge(acc)
		}
	}

	for _, doc := range rr.daily {
		if !rollupDocMatchesModelFilter(doc, modelFilter) {
			continue
		}
		matched := rollupCountersMatch(doc, scope, scopeID, workspaceID)
		if matched.RequestCount == 0 {
			continue
		}
		dayStart, err := time.ParseInLocation("2006-01-02", doc.Period, time.UTC)
		if err != nil {
			continue
		}
		key := bucketKeyFor(dayStart, bucketMs, dayGrainMillis)
		put(key,
			func() *rollupBucketAccum {
				return newRollupBucketAccum(dayStart, dayGrainMillis, matched, doc.Latency, "day", bucketMs)
			},
			func(acc *rollupBucketAccum) { mergeRollupBucketAccum(acc, matched, doc.Latency) })
	}

	for _, doc := range rr.monthly {
		if !rollupDocMatchesModelFilter(doc, modelFilter) {
			continue
		}
		matched := rollupCountersMatch(doc, scope, scopeID, workspaceID)
		if matched.RequestCount == 0 {
			continue
		}
		monthStart, err := time.ParseInLocation("2006-01", doc.Period, time.UTC)
		if err != nil {
			continue
		}
		grainMillis := monthStart.AddDate(0, 1, 0).Sub(monthStart).Milliseconds()
		key := bucketKeyFor(monthStart, bucketMs, grainMillis)
		put(key,
			func() *rollupBucketAccum {
				return newRollupBucketAccum(monthStart, grainMillis, matched, doc.Latency, "month", bucketMs)
			},
			func(acc *rollupBucketAccum) { mergeRollupBucketAccum(acc, matched, doc.Latency) })
	}

	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]gen.AIStatsBucket, 0, len(keys))
	for _, key := range keys {
		out = append(out, accs[key].bucket)
	}
	return out
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
