package aistats

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Read-path merge tests (设计 §7 tests 6–9). All tests run against both
// real persist backends (goleveldb PrefixScanner + fs List+Load) via the
// rollupBackends harness, and drive the handlers through the *At variants
// with the fixed testNow clock (2026-09-06; rawFloor 2026-06-08, monthly
// cutoff "2025-03" under the default 90/550 retention).

// straddleSeeds returns five records: three on rollable days before the
// rawFloor (2026-06-08) and two inside the raw window.
func straddleSeeds() []gen.AIStatsRecord {
	return []gen.AIStatsRecord{
		mkRollupRecord("r1", "2026-06-05", 100),
		mkRollupRecord("r2", "2026-06-06", 200),
		mkRollupRecord("r3", "2026-06-07", 300),
		mkRollupRecord("r4", "2026-06-08", 400),
		mkRollupRecord("r5", "2026-06-09", 500),
	}
}

// threeLayerSeeds seeds one record per layer: an L2 month (2024-06, two
// records on one day), three L1 days (2026-05-10..12), and a raw day
// (2026-09-01, two records).
func threeLayerSeeds() []gen.AIStatsRecord {
	return []gen.AIStatsRecord{
		mkRollupRecord("m1", "2024-06-10", 100),
		mkRollupRecord("m2", "2024-06-10", 300),
		mkRollupRecord("d1", "2026-05-10", 100),
		mkRollupRecord("d2", "2026-05-11", 200),
		mkRollupRecord("d3", "2026-05-12", 300),
		mkRollupRecord("w1", "2026-09-01", 400),
		mkRollupRecord("w2", "2026-09-01", 500),
	}
}

func ms(t time.Time) int64 { return t.UnixMilli() }

func mustDay(t *testing.T, day string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		t.Fatalf("parse day %s: %v", day, err)
	}
	return d
}

// TestQuery_NoDoubleCountAcrossBoundary (设计 §7 test 6, invariant I2): a
// query window crossing the rawFloor returns field-identical counters before
// and after the convolution — rollup-owned days move from raw records into
// documents exactly once.
func TestQuery_NoDoubleCountAcrossBoundary(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		seeds := straddleSeeds()
		seedRecords(t, a, seeds...)

		req := gen.AIStatsQueryReq{
			Scope:       "workspace",
			WorkspaceID: "ws-1",
			Since:       "2026-06-01T00:00:00Z",
			Until:       "2026-06-20T00:00:00Z",
		}
		before, err := a.queryAt(testNow, req)
		if err != nil {
			t.Fatalf("query before rollup: %v", err)
		}
		if before.Total != 5 {
			t.Fatalf("pre-rollup total: got %d, want 5", before.Total)
		}

		rollupToConvergence(t, a, testNow)

		// Rolled days must be gone from the raw layer.
		if doc := loadRollupForTest(t, a, dailyRollupName("2026-06-05")); doc == nil {
			t.Fatal("2026-06-05 should be rolled up")
		}
		if left, _ := a.scanDayRecords("2026-06-05"); len(left) != 0 {
			t.Fatalf("2026-06-05 raw records should be reclaimed, got %d", len(left))
		}

		after, err := a.queryAt(testNow, req)
		if err != nil {
			t.Fatalf("query after rollup: %v", err)
		}

		// I2: counters field-identical across the boundary.
		if !reflect.DeepEqual(before.Counters, after.Counters) {
			t.Errorf("counters changed across rollup boundary:\n before %+v\n after  %+v", before.Counters, after.Counters)
		}

		// The raw segment now answers only the un-rolled days: Total counts
		// raw record rows (显式声明语义), records exclude doc-owned days.
		if after.Total != 2 {
			t.Errorf("post-rollup total: got %d, want 2 (raw rows only)", after.Total)
		}
		if len(after.Records) != 2 {
			t.Fatalf("post-rollup records: got %d, want 2", len(after.Records))
		}
		for _, r := range after.Records {
			if r.ID != "r4" && r.ID != "r5" {
				t.Errorf("record %s from a rolled day leaked into the raw segment", r.ID)
			}
		}

		// Sanity: the merge actually re-added the rolled days' contribution
		// (3 requests / 600ms latency) on top of the 2 raw records.
		if after.Counters.RequestCount != 5 || after.Counters.LatencySumMs != 1500 {
			t.Errorf("merged counters: got requests=%d latency=%dms, want 5 / 1500",
				after.Counters.RequestCount, after.Counters.LatencySumMs)
		}
	})
}

// TestQuery_WorkspaceScopeOldData (设计 §7 test 7): per-workspace counters
// for data older than the daily window (served from a permanent monthly
// document) equal the manual replay, and other workspaces' records in the
// same documents are not attributed.
func TestQuery_WorkspaceScopeOldData(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)

		oldWS := mkRollupRecord("o1", "2024-06-10", 100)
		oldWS.Usage.CostTotal = 1.5
		oldWS2 := mkRollupRecord("o2", "2024-06-11", 300)
		oldWS2.Usage.CostTotal = 2.5
		otherWS := mkRollupRecord("o3", "2024-06-10", 200)
		otherWS.WorkspaceID = "ws-other"
		seeds := []gen.AIStatsRecord{oldWS, oldWS2, otherWS}
		seedRecords(t, a, seeds...)

		rollupToConvergence(t, a, testNow)

		// 2024-06 is older than the monthly cutoff ("2025-03"): it must
		// have compressed into the permanent monthly document.
		if doc := loadRollupForTest(t, a, monthlyRollupName("2024-06")); doc == nil {
			t.Fatal("2024-06 should be compressed to a monthly document")
		}
		if doc := loadRollupForTest(t, a, dailyRollupName("2024-06-10")); doc != nil {
			t.Fatal("daily docs of 2024-06 should be reclaimed after compression")
		}

		resp, err := a.queryAt(testNow, gen.AIStatsQueryReq{
			Scope:       "workspace",
			WorkspaceID: "ws-1",
			Since:       "2024-06-01T00:00:00Z",
			Until:       "2024-06-30T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("query old workspace data: %v", err)
		}

		// Manual replay of exactly the two ws-1 records.
		var want gen.AIStatsCounters
		for _, r := range seeds[:2] {
			want = addCounters(want, recordToCounters(r))
		}
		if !reflect.DeepEqual(resp.Counters, want) {
			t.Errorf("old per-workspace counters:\n got %+v\nwant %+v", resp.Counters, want)
		}
		if resp.Total != 0 {
			t.Errorf("total: got %d, want 0 (monthly layer has no per-request rows)", resp.Total)
		}

		// The other workspace answers its own slice of the same document.
		other, err := a.queryAt(testNow, gen.AIStatsQueryReq{
			Scope:       "workspace",
			WorkspaceID: "ws-other",
			Since:       "2024-06-01T00:00:00Z",
			Until:       "2024-06-30T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("query ws-other: %v", err)
		}
		if other.Counters.RequestCount != 1 || other.Counters.LatencySumMs != 200 {
			t.Errorf("ws-other counters: got %+v, want requests=1 latency=200ms", other.Counters)
		}
	})
}

// TestSeries_RollupBuckets (设计 §7 test 8): the series endpoint merges
// three bucket layers — one month bucket (grain "month", no percentiles),
// one day bucket per daily document (grain "day", frozen percentiles), and
// the untouched raw buckets (grain "raw") — flagged via Rollup/Grain.
func TestSeries_RollupBuckets(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		seedRecords(t, a, threeLayerSeeds()...)
		rollupToConvergence(t, a, testNow)

		resp, err := a.seriesAt(testNow, gen.AIStatsSeriesReq{
			Scope:    "workspace",
			Since:    "2024-06-01T00:00:00Z",
			Until:    "2026-09-03T00:00:00Z",
			BucketMs: int64(24 * time.Hour / time.Millisecond),
		})
		if err != nil {
			t.Fatalf("series: %v", err)
		}

		if len(resp.Buckets) != 5 {
			t.Fatalf("buckets: got %d, want 5 (1 month + 3 days + 1 raw)\n%v", len(resp.Buckets), resp.Buckets)
		}

		monthStart := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
		type want struct {
			start    int64
			requests int64
			rollup   bool
			grain    string
			p50      float64
		}
		wants := []want{
			{ms(monthStart), 2, true, "month", 0},
			{ms(mustDay(t, "2026-05-10")), 1, true, "day", 100},
			{ms(mustDay(t, "2026-05-11")), 1, true, "day", 200},
			{ms(mustDay(t, "2026-05-12")), 1, true, "day", 300},
			{ms(mustDay(t, "2026-09-01")), 2, false, "raw", 500},
		}
		for i, w := range wants {
			b := resp.Buckets[i]
			if b.Start != w.start {
				t.Errorf("bucket[%d].Start: got %d, want %d", i, b.Start, w.start)
			}
			if b.Requests != w.requests {
				t.Errorf("bucket[%d].Requests: got %d, want %d", i, b.Requests, w.requests)
			}
			if b.Rollup != w.rollup || b.Grain != w.grain {
				t.Errorf("bucket[%d] flags: got rollup=%v grain=%q, want rollup=%v grain=%q",
					i, b.Rollup, b.Grain, w.rollup, w.grain)
			}
			if b.LatencyP50 != w.p50 {
				t.Errorf("bucket[%d].LatencyP50: got %v, want %v (frozen snapshot / unavailable)",
					i, b.LatencyP50, w.p50)
			}
		}

		// The month bucket's latency sum merges exactly from the two daily
		// records (100+300) even though its percentiles are unavailable.
		if b := resp.Buckets[0]; b.LatencySumMs != 400 {
			t.Errorf("month bucket LatencySumMs: got %d, want 400", b.LatencySumMs)
		}

		// ModelFilter gates rollup documents by provider/model dimension:
		// no seed uses provider "claude", so every rollup bucket drops out.
		filtered, err := a.seriesAt(testNow, gen.AIStatsSeriesReq{
			Scope:       "workspace",
			Since:       "2024-06-01T00:00:00Z",
			Until:       "2026-09-03T00:00:00Z",
			BucketMs:    int64(24 * time.Hour / time.Millisecond),
			ModelFilter: "claude",
		})
		if err != nil {
			t.Fatalf("series with ModelFilter: %v", err)
		}
		for _, b := range filtered.Buckets {
			if b.Rollup {
				t.Errorf("rollup bucket %d survived a non-matching ModelFilter", b.Start)
			}
		}
	})
}

// TestExport_RollupLines (设计 §7 test 9): the export endpoint emits
// aggregate lines for the coarse segment — jsonl {"rollup":true,...} rows
// and csv rows headed rollup,<period>,<granularity> — while raw rows keep
// their exact per-request formats.
func TestExport_RollupLines(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		seedRecords(t, a, threeLayerSeeds()...)
		rollupToConvergence(t, a, testNow)

		// --- jsonl: 1 monthly + 3 daily rollup lines + 2 raw record lines.
		jsonl, err := a.exportAt(testNow, gen.AIStatsExportReq{
			Scope: "workspace",
			Since: "2024-06-01T00:00:00Z",
			Until: "2026-09-03T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("jsonl export: %v", err)
		}
		lines := strings.Split(strings.TrimRight(jsonl.Data, "\n"), "\n")
		if len(lines) != 6 {
			t.Fatalf("jsonl lines: got %d, want 6\n%s", len(lines), jsonl.Data)
		}
		var monthly map[string]any
		dailies := map[string]bool{}
		for _, line := range lines[:4] {
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("rollup line is not a JSON object: %v", err)
			}
			if m["rollup"] != true {
				t.Errorf("coarse-segment line missing rollup marker: %s", line)
			}
			switch m["granularity"] {
			case "monthly":
				monthly = m
			case "daily":
				dailies[m["period"].(string)] = true
			default:
				t.Errorf("unknown granularity in rollup line: %s", line)
			}
		}
		if monthly == nil || monthly["period"] != "2024-06" {
			t.Errorf("monthly rollup line wrong: %v", monthly)
		}
		for _, day := range []string{"2026-05-10", "2026-05-11", "2026-05-12"} {
			if !dailies[day] {
				t.Errorf("daily rollup line for %s missing: %v", day, dailies)
			}
		}
		// The last two lines are the raw per-request records, format
		// unchanged.
		for _, line := range lines[4:] {
			var rec gen.AIStatsRecord
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Errorf("raw line should unmarshal into AIStatsRecord: %v\nline: %s", err, line)
			}
		}

		// --- csv: header + 4 rollup rows + 2 raw rows.
		csv, err := a.exportAt(testNow, gen.AIStatsExportReq{
			Scope:  "workspace",
			Since:  "2024-06-01T00:00:00Z",
			Until:  "2026-09-03T00:00:00Z",
			Format: "csv",
		})
		if err != nil {
			t.Fatalf("csv export: %v", err)
		}
		clines := strings.Split(strings.TrimRight(csv.Data, "\n"), "\n")
		if len(clines) != 7 {
			t.Fatalf("csv lines: got %d, want 7\n%s", len(clines), csv.Data)
		}
		if !strings.HasPrefix(clines[0], "id,workspaceId,") {
			t.Errorf("csv header changed: %s", clines[0])
		}
		if !strings.HasPrefix(clines[1], "rollup,2024-06,monthly,") {
			t.Errorf("monthly rollup row: %s", clines[1])
		}
		if !strings.HasPrefix(clines[2], "rollup,2026-05-10,daily,") {
			t.Errorf("daily rollup row: %s", clines[2])
		}
		for _, row := range clines[5:] {
			if strings.HasPrefix(row, "rollup,") {
				t.Errorf("raw row must stay per-request: %q", row)
			}
		}

		// --- filtered export: a workspace with no old data gets no rollup
		// lines (dimension matching, I5).
		none, err := a.exportAt(testNow, gen.AIStatsExportReq{
			Scope:       "workspace",
			WorkspaceID: "ws-absent",
			Since:       "2024-06-01T00:00:00Z",
			Until:       "2026-09-03T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("filtered jsonl export: %v", err)
		}
		if strings.Contains(none.Data, "rollup") {
			t.Errorf("filtered export should contain no rollup lines, got:\n%s", none.Data)
		}
	})
}

// TestSeries_FineBucketsKeepDayGrain: bucketMs below 24h cannot subdivide a
// daily rollup document — one bucket per day (粒度下限, 设计 §4).
func TestSeries_FineBucketsKeepDayGrain(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		seedRecords(t, a, threeLayerSeeds()...)
		rollupToConvergence(t, a, testNow)

		resp, err := a.seriesAt(testNow, gen.AIStatsSeriesReq{
			Scope:    "workspace",
			Since:    "2026-05-01T00:00:00Z",
			Until:    "2026-05-31T00:00:00Z",
			BucketMs: int64(time.Hour / time.Millisecond),
		})
		if err != nil {
			t.Fatalf("series: %v", err)
		}
		if len(resp.Buckets) != 3 {
			t.Fatalf("fine-grain buckets: got %d, want 3 day buckets\n%v", len(resp.Buckets), resp.Buckets)
		}
		for _, b := range resp.Buckets {
			if b.Grain != "day" || !b.Rollup {
				t.Errorf("bucket %d: got grain=%q rollup=%v, want day/true", b.Start, b.Grain, b.Rollup)
			}
		}
	})
}

// TestQuery_BacklogDayFallsBackToRaw: days outside the raw window whose
// rollup documents do not exist yet (convolution backlog — no rollup round
// has run at all here) are still answered from raw records. The read source
// is the per-day document existence, never a whole-range time cutoff
// (设计 §5 — the card's key anti-leak point).
func TestQuery_BacklogDayFallsBackToRaw(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		// 2026-06-01/02 are outside the raw window (cutoff 2026-06-08) but
		// no rollup has ever run: no documents exist.
		seedRecords(t, a,
			mkRollupRecord("b1", "2026-06-01", 100),
			mkRollupRecord("b2", "2026-06-02", 200),
		)

		resp, err := a.queryAt(testNow, gen.AIStatsQueryReq{
			Scope:       "workspace",
			WorkspaceID: "ws-1",
			Since:       "2026-06-01T00:00:00Z",
			Until:       "2026-06-03T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if resp.Counters.RequestCount != 2 || resp.Counters.LatencySumMs != 300 {
			t.Errorf("backlog counters: got %+v, want requests=2 latency=300ms", resp.Counters)
		}
		if resp.Total != 2 || len(resp.Records) != 2 {
			t.Errorf("backlog rows: total=%d records=%d, want 2/2", resp.Total, len(resp.Records))
		}
	})
}
