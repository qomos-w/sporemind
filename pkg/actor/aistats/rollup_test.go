package aistats

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// testNow is the fixed clock for rollup tests: 2026-09-06 12:00 UTC.
// With default retention (raw 90d / daily 550d):
//
//	raw day cutoff    = 2026-06-08   (days before this are rollable)
//	monthly cutoff    = "2025-03"    (months before this are compressible)
var testNow = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

// rollupBackends runs a test body against both real persist backends: the
// goleveldb backend (PrefixScanner path) and the fs backend (List+Load
// fallback path).
func rollupBackends(t *testing.T, fn func(t *testing.T, backend persist.BackendType)) {
	t.Helper()
	for _, backend := range []persist.BackendType{persist.BackendLevelDB, persist.BackendFS} {
		t.Run(string(backend), func(t *testing.T) {
			fn(t, backend)
		})
	}
}

// setupRollupActor builds a bare Actor over a real persist store, mirroring
// how the existing tests construct actors directly.
func setupRollupActor(t *testing.T, backend persist.BackendType) *Actor {
	t.Helper()
	dir := t.TempDir()
	fsRoot := filepath.Join(dir, "aistats")
	if err := os.MkdirAll(fsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := persist.New(persist.PersistConfig{Backend: backend, DataDir: dir, Prefix: "aistats"})
	if err != nil {
		t.Fatalf("persist.New(%s): %v", backend, err)
	}
	t.Cleanup(func() {
		if c, ok := s.(interface{ Close() error }); ok {
			c.Close()
		}
	})
	var scanner persist.PrefixScanner
	if sc, ok := s.(persist.PrefixScanner); ok {
		scanner = sc
	}
	return &Actor{root: fsRoot, store: s, scanner: scanner, counters: newCounters(), costs: newCostIndex(s)}
}

// mkRollupRecord builds a record on the UTC day "2006-01-02" (10:00 UTC).
func mkRollupRecord(id, day string, latencyMs int64) gen.AIStatsRecord {
	return gen.AIStatsRecord{
		ID:           id,
		WorkspaceID:  "ws-1",
		ProjectID:    "proj-1",
		SessionID:    "sess-1",
		AgentID:      "agent-1",
		Provider:     "openai",
		Model:        "gpt-4o",
		StopReason:   "end_turn",
		LatencyMs:    latencyMs,
		FirstTokenMs: latencyMs / 2,
		CompletedAt:  day + "T10:00:00Z",
		Usage: &gen.UsageData{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
			CostTotal:    0.5,
		},
	}
}

// seedRecords writes records through the real write path (record + month
// marker + checkpoint) like production traffic.
func seedRecords(t *testing.T, a *Actor, records ...gen.AIStatsRecord) {
	t.Helper()
	for _, r := range records {
		if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: r}); err != nil {
			t.Fatalf("handleRecord %s: %v", r.ID, err)
		}
	}
}

// rollupToConvergence runs rollup rounds until a round computes nothing,
// draining the K=7-per-round backlog.
func rollupToConvergence(t *testing.T, a *Actor, now time.Time) AIStatsRollupResp {
	t.Helper()
	var resp AIStatsRollupResp
	for i := 0; i < 1000; i++ {
		r, err := a.rollupOnce(now, AIStatsRollupReq{})
		if err != nil {
			t.Fatalf("rollupOnce round %d: %v", i, err)
		}
		resp = r
		if r.DaysComputed == 0 && r.MonthsComputed == 0 {
			return r
		}
	}
	t.Fatal("rollup did not converge after 1000 rounds")
	return resp
}

func loadRollupForTest(t *testing.T, a *Actor, name string) *rollupDoc {
	t.Helper()
	doc, err := loadRollupDoc(a.store, name)
	if err != nil {
		t.Fatalf("loadRollupDoc %s: %v", name, err)
	}
	return doc
}

// TestRollup_DayTotalsPreserved: rolling up one day preserves the full
// seven-view counter totals (I1) plus the frozen latency snapshot, and the
// raw records + month marker are reclaimed.
func TestRollup_DayTotalsPreserved(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-10"
		records := []gen.AIStatsRecord{
			mkRollupRecord("r1", day, 100),
			mkRollupRecord("r2", day, 300),
			mkRollupRecord("r3", day, 200),
		}
		seedRecords(t, a, records...)

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day})
		if err != nil {
			t.Fatalf("rollup: %v", err)
		}
		if resp.DaysComputed != 1 || resp.RecordsDeleted != 3 {
			t.Fatalf("resp: computed=%d deleted=%d, want 1/3", resp.DaysComputed, resp.RecordsDeleted)
		}

		doc := loadRollupForTest(t, a, dailyRollupName(day))
		if doc == nil {
			t.Fatal("daily rollup doc missing")
		}
		if doc.Period != day || doc.Granularity != "daily" {
			t.Errorf("period/granularity: got %q/%q", doc.Period, doc.Granularity)
		}
		if doc.Requests != 3 {
			t.Errorf("requests: got %d, want 3", doc.Requests)
		}

		// I1: every view equals an independent replay of the raw records.
		want := rebuildCounters(records).snapshot()
		if !reflect.DeepEqual(doc.Views, *want) {
			t.Errorf("views mismatch:\n got %+v\nwant %+v", doc.Views, *want)
		}

		// Frozen latency snapshot, computed independently here.
		var latencies, ttfts []float64
		var sumMs, maxMs int64
		for _, r := range records {
			latencies = append(latencies, float64(r.LatencyMs))
			ttfts = append(ttfts, float64(r.FirstTokenMs))
			sumMs += r.LatencyMs
			if r.LatencyMs > maxMs {
				maxMs = r.LatencyMs
			}
		}
		sort.Float64s(latencies)
		sort.Float64s(ttfts)
		wantLat := rollupLatency{
			Count:   3,
			SumMs:   sumMs,
			MaxMs:   maxMs,
			P50:     percentileFloat(latencies, 50),
			P95:     percentileFloat(latencies, 95),
			TtftP50: percentileFloat(ttfts, 50),
		}
		if doc.Latency != wantLat {
			t.Errorf("latency: got %+v, want %+v", doc.Latency, wantLat)
		}

		// Raw records and month marker reclaimed.
		left, err := a.scanDayRecords(day)
		if err != nil {
			t.Fatalf("rescan day: %v", err)
		}
		if len(left) != 0 {
			t.Errorf("%d raw records left after rollup", len(left))
		}
		var marker bool
		if err := a.store.Load("months/2026-05", &marker); !errors.Is(err, persist.ErrNotExist) {
			t.Errorf("month marker should be cleared, load err=%v", err)
		}
	})
}

// TestRollup_Idempotent: running the same day twice leaves the document
// byte-identical and deletes nothing the second time (I3).
func TestRollup_Idempotent(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-11"
		seedRecords(t, a, mkRollupRecord("r1", day, 100), mkRollupRecord("r2", day, 200))

		if _, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day}); err != nil {
			t.Fatalf("first rollup: %v", err)
		}
		first, err := json.Marshal(loadRollupForTest(t, a, dailyRollupName(day)))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day})
		if err != nil {
			t.Fatalf("second rollup: %v", err)
		}
		if resp.DaysComputed != 0 || resp.DaysExisting != 1 || resp.RecordsDeleted != 0 {
			t.Errorf("second run: computed=%d existing=%d deleted=%d, want 0/1/0",
				resp.DaysComputed, resp.DaysExisting, resp.RecordsDeleted)
		}
		second, err := json.Marshal(loadRollupForTest(t, a, dailyRollupName(day)))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Errorf("doc changed on re-run:\nfirst %s\nsecond %s", first, second)
		}
	})
}

// TestRollup_CrashBeforeDoc: a crash before the doc write leaves raw
// records untouched; the rerun recomputes from scratch and reaches the
// correct final state (I3).
func TestRollup_CrashBeforeDoc(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-12"
		records := []gen.AIStatsRecord{
			mkRollupRecord("r1", day, 100),
			mkRollupRecord("r2", day, 400),
		}
		seedRecords(t, a, records...)

		// Simulate the interrupted pre-save state: raw present, doc absent.
		if doc := loadRollupForTest(t, a, dailyRollupName(day)); doc != nil {
			t.Fatal("doc should not exist yet")
		}
		if left, _ := a.scanDayRecords(day); len(left) != 2 {
			t.Fatalf("raw records should be untouched, got %d", len(left))
		}

		if _, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day}); err != nil {
			t.Fatalf("rerun after crash: %v", err)
		}
		doc := loadRollupForTest(t, a, dailyRollupName(day))
		if doc == nil {
			t.Fatal("doc missing after rerun")
		}
		want := rebuildCounters(records).snapshot()
		if !reflect.DeepEqual(doc.Views, *want) {
			t.Errorf("views mismatch after crash recovery:\n got %+v\nwant %+v", doc.Views, *want)
		}
		if left, _ := a.scanDayRecords(day); len(left) != 0 {
			t.Errorf("%d raw records left after recovery", len(left))
		}
	})
}

// TestRollup_CrashMidDelete: a crash after the doc write but mid-delete
// leaves the doc plus leftover raw records; the rerun takes the doc-exists
// path (no recompute, byte-identical doc) and finishes the deletion (I3).
func TestRollup_CrashMidDelete(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-13"
		records := []gen.AIStatsRecord{
			mkRollupRecord("r1", day, 100),
			mkRollupRecord("r2", day, 200),
			mkRollupRecord("r3", day, 300),
		}
		seedRecords(t, a, records...)

		// Simulate a run that saved the doc (with the StragglerIDs guard) then
		// crashed before deleting: perform the save phase manually and leave
		// all raw records in place.
		doc := aggregateDayRollup(day, records)
		doc.StragglerIDs = recordIDs(records)
		if err := a.store.Save(dailyRollupName(day), doc); err != nil {
			t.Fatalf("simulate crash-state save: %v", err)
		}
		savedViews, savedLatency, savedRequests := doc.Views, doc.Latency, doc.Requests

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day})
		if err != nil {
			t.Fatalf("rerun: %v", err)
		}
		if resp.DaysComputed != 0 || resp.DaysExisting != 1 {
			t.Errorf("doc-exists path should not recount: computed=%d existing=%d", resp.DaysComputed, resp.DaysExisting)
		}
		if resp.RecordsDeleted != 3 {
			t.Errorf("leftover deletion: got %d, want 3", resp.RecordsDeleted)
		}
		if left, _ := a.scanDayRecords(day); len(left) != 0 {
			t.Errorf("%d raw records left after resume", len(left))
		}
		after := loadRollupForTest(t, a, dailyRollupName(day))
		if after == nil {
			t.Fatal("doc missing after resume")
		}
		if !reflect.DeepEqual(after.Views, savedViews) {
			t.Errorf("views were recounted on the guard path:\n got %+v\nwant %+v", after.Views, savedViews)
		}
		if after.Latency != savedLatency || after.Requests != savedRequests {
			t.Errorf("latency/requests changed on the guard path: %+v/%d", after.Latency, after.Requests)
		}
		if after.StragglerIDs != nil {
			t.Errorf("guard must be cleared once all deletions succeed, got %v", after.StragglerIDs)
		}
	})
}

// TestRollup_MonthlyEqualsDailySum: a fully rolled-up month compresses into
// a monthly document whose every field equals the sum of the month's daily
// documents (I4); the daily tree is then cascade-reclaimed and the monthly
// document carries only the four bounded-cardinality views (I5).
func TestRollup_MonthlyEqualsDailySum(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		const month = "2024-01" // older than the 550-day daily floor
		records := []gen.AIStatsRecord{
			mkRollupRecord("r1", "2024-01-05", 100),
			mkRollupRecord("r2", "2024-01-05", 300),
			mkRollupRecord("r3", "2024-01-10", 200),
			mkRollupRecord("r4", "2024-01-31", 500),
		}
		seedRecords(t, a, records...)

		rollupToConvergence(t, a, testNow)

		doc := loadRollupForTest(t, a, monthlyRollupName(month))
		if doc == nil {
			t.Fatal("monthly rollup doc missing")
		}
		if doc.Period != month || doc.Granularity != "monthly" {
			t.Errorf("period/granularity: got %q/%q", doc.Period, doc.Granularity)
		}
		if doc.Requests != 4 {
			t.Errorf("requests: got %d, want 4", doc.Requests)
		}

		// I4: monthly views == independent replay of the raw records.
		want := rebuildCounters(records).snapshot()
		if !reflect.DeepEqual(doc.Views.Workspace, want.Workspace) {
			t.Errorf("workspace mismatch:\n got %+v\nwant %+v", doc.Views.Workspace, want.Workspace)
		}
		if !reflect.DeepEqual(doc.Views.WorkspaceMap, want.WorkspaceMap) {
			t.Errorf("workspaceMap mismatch:\n got %+v\nwant %+v", doc.Views.WorkspaceMap, want.WorkspaceMap)
		}
		if !reflect.DeepEqual(doc.Views.Provider, want.Provider) {
			t.Errorf("provider mismatch:\n got %+v\nwant %+v", doc.Views.Provider, want.Provider)
		}
		if !reflect.DeepEqual(doc.Views.Model, want.Model) {
			t.Errorf("model mismatch:\n got %+v\nwant %+v", doc.Views.Model, want.Model)
		}

		// I5: the permanent layer drops the high-cardinality views.
		if doc.Views.Session != nil || doc.Views.Project != nil || doc.Views.Agent != nil {
			t.Errorf("monthly doc must omit Session/Project/Agent views, got %+v", doc.Views)
		}

		// Latency merges count/sum/max only; percentiles are declared
		// unavailable at month grain.
		wantLat := rollupLatency{Count: 4, SumMs: 1100, MaxMs: 500}
		if doc.Latency != wantLat {
			t.Errorf("latency: got %+v, want %+v", doc.Latency, wantLat)
		}

		// The whole daily month is cascade-reclaimed.
		dailyMonths, err := rollupMonthsWithDailyDocs(a.store)
		if err != nil {
			t.Fatalf("list daily months: %v", err)
		}
		if len(dailyMonths) != 0 {
			t.Errorf("daily tree should be empty after compression, got %v", dailyMonths)
		}
		if doc := loadRollupForTest(t, a, dailyRollupName("2024-01-05")); doc != nil {
			t.Error("daily doc should be gone after cascade delete")
		}

		// Raw records and marker reclaimed.
		if left, _ := a.scanDayRecords("2024-01-10"); len(left) != 0 {
			t.Errorf("%d raw records left", len(left))
		}
		var marker bool
		if err := a.store.Load("months/2024-01", &marker); !errors.Is(err, persist.ErrNotExist) {
			t.Errorf("month marker should be cleared, load err=%v", err)
		}
	})
}

// TestRollup_SkipsIncompleteMonth: a month missing daily documents is not
// compressed; once the gap is filled the next round compresses it (设计 §3
// completeness precondition).
func TestRollup_SkipsIncompleteMonth(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		const month = "2024-02" // 29 days, leap year
		records := []gen.AIStatsRecord{
			mkRollupRecord("r1", "2024-02-10", 100),
			mkRollupRecord("r2", "2024-02-20", 200),
		}

		// Build the exact post-day-pass state for every day except the 15th,
		// with the 10th carrying the seeded records' aggregation.
		for day := 1; day <= 29; day++ {
			if day == 15 {
				continue
			}
			ds := month + fmt.Sprintf("-%02d", day)
			var recs []gen.AIStatsRecord
			if day == 10 {
				recs = records
			}
			if err := a.store.Save(dailyRollupName(ds), aggregateDayRollup(ds, recs)); err != nil {
				t.Fatalf("seed daily doc %s: %v", ds, err)
			}
		}

		a.mu.Lock()
		outcome, err := a.rollupMonthLocked(testNow, month, false)
		a.mu.Unlock()
		if err != nil {
			t.Fatalf("rollupMonthLocked: %v", err)
		}
		if outcome.state != monthIncomplete {
			t.Fatalf("incomplete month state = %v, want monthIncomplete", outcome.state)
		}
		if doc := loadRollupForTest(t, a, monthlyRollupName(month)); doc != nil {
			t.Fatal("monthly doc must not exist for an incomplete month")
		}
		if left, _ := rollupDayDocCount(a.store, month); left != 28 {
			t.Errorf("incomplete month must keep its daily docs, got %d", left)
		}

		// Fill the gap with an empty-day document (the day had no records)
		// and verify the next round compresses the month.
		gapDay := "2024-02-15"
		if err := a.store.Save(dailyRollupName(gapDay), aggregateDayRollup(gapDay, nil)); err != nil {
			t.Fatalf("fill gap: %v", err)
		}
		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{})
		if err != nil {
			t.Fatalf("rollupOnce after gap filled: %v", err)
		}
		if resp.MonthsComputed != 1 {
			t.Fatalf("months computed = %d, want 1 (resp %+v)", resp.MonthsComputed, resp)
		}
		doc := loadRollupForTest(t, a, monthlyRollupName(month))
		if doc == nil {
			t.Fatal("monthly doc missing after completion")
		}
		// I4: the monthly totals equal the seeded records' totals.
		want := rebuildCounters(records).snapshot()
		if !reflect.DeepEqual(doc.Views.Workspace, want.Workspace) {
			t.Errorf("workspace mismatch:\n got %+v\nwant %+v", doc.Views.Workspace, want.Workspace)
		}
		if doc.Requests != 2 {
			t.Errorf("requests: got %d, want 2", doc.Requests)
		}
		if left, _ := rollupDayDocCount(a.store, month); left != 0 {
			t.Errorf("daily tree should be cascaded away, got %d docs", left)
		}
	})
}

// rollupDayDocCount counts the daily documents under one month.
func rollupDayDocCount(store persist.Persist, ym string) (int, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return 0, fmt.Errorf("Lister required")
	}
	names, err := lister.List("rollups/daily/" + ym + "/")
	if err != nil {
		return 0, err
	}
	return len(names), nil
}

// TestRollup_Boundary: days at or inside the raw retention floor (cutoff
// day and today) are never rolled up or deleted; only strictly-older days
// are touched (I6).
func TestRollup_Boundary(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		// Default raw retention is 90 days: cutoff = 2026-06-08.
		const cutoffDay = "2026-06-08"
		oldRec := mkRollupRecord("old", "2026-06-07", 100)
		floorRec := mkRollupRecord("floor", cutoffDay, 200)
		todayRec := mkRollupRecord("today", "2026-09-06", 300)
		seedRecords(t, a, oldRec, floorRec, todayRec)

		rollupToConvergence(t, a, testNow)

		// The day before the cutoff was rolled up: doc exists, raw gone.
		if doc := loadRollupForTest(t, a, dailyRollupName("2026-06-07")); doc == nil {
			t.Error("2026-06-07 should have a daily rollup doc")
		}
		if left, _ := a.scanDayRecords("2026-06-07"); len(left) != 0 {
			t.Errorf("2026-06-07 raw records should be deleted, got %d", len(left))
		}

		// The cutoff-day record and today's record are untouched.
		if doc := loadRollupForTest(t, a, dailyRollupName(cutoffDay)); doc != nil {
			t.Error("cutoff-day record must not be rolled up")
		}
		if left, _ := a.scanDayRecords(cutoffDay); len(left) != 1 || left[0].ID != "floor" {
			t.Errorf("cutoff-day record must survive, got %+v", left)
		}
		if left, _ := a.scanDayRecords("2026-09-06"); len(left) != 1 || left[0].ID != "today" {
			t.Errorf("today's record must survive, got %+v", left)
		}

		// The month still holds raw records, so its marker must survive too.
		var marker bool
		if err := a.store.Load("months/2026-06", &marker); err != nil {
			t.Errorf("months/2026-06 marker should survive while raw records remain: %v", err)
		}
	})
}

// TestRollup_StragglerMerge: records that arrive on an already-rolled-up
// day (backfill imports, clock-corrected replays) are absorbed additively
// into the daily document — every view equals the full record set's replay
// (I1), latency totals merge additively, and the frozen percentile
// snapshot stays frozen (declared degradation).
func TestRollup_StragglerMerge(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-10"
		first := []gen.AIStatsRecord{
			mkRollupRecord("r1", day, 100),
			mkRollupRecord("r2", day, 300),
		}
		seedRecords(t, a, first...)
		if _, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day}); err != nil {
			t.Fatalf("first rollup: %v", err)
		}
		frozen := loadRollupForTest(t, a, dailyRollupName(day)).Latency

		// Backfill two more records on the same, already rolled-up day —
		// one on a second workspace/provider/model to exercise every view.
		s1 := mkRollupRecord("s1", day, 500)
		s1.WorkspaceID = "ws-2"
		s1.SessionID = "sess-2"
		s1.Provider = "anthropic"
		s1.Model = "claude-3"
		s2 := mkRollupRecord("s2", day, 50)
		stragglers := []gen.AIStatsRecord{s1, s2}
		seedRecords(t, a, stragglers...)

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day})
		if err != nil {
			t.Fatalf("straggler rollup: %v", err)
		}
		if resp.DaysComputed != 1 || resp.RecordsDeleted != 2 {
			t.Fatalf("resp: computed=%d deleted=%d, want 1/2", resp.DaysComputed, resp.RecordsDeleted)
		}

		doc := loadRollupForTest(t, a, dailyRollupName(day))
		if doc == nil {
			t.Fatal("daily doc missing")
		}
		// Every view (all seven) equals the full record set replayed.
		all := append(append([]gen.AIStatsRecord{}, first...), stragglers...)
		want := rebuildCounters(all).snapshot()
		if !reflect.DeepEqual(doc.Views, *want) {
			t.Errorf("views mismatch after straggler merge:\n got %+v\nwant %+v", doc.Views, *want)
		}
		if doc.Requests != 4 {
			t.Errorf("requests: got %d, want 4", doc.Requests)
		}

		// Latency: additive count/sum/max, frozen percentiles.
		if doc.Latency.Count != 4 || doc.Latency.SumMs != 950 || doc.Latency.MaxMs != 500 {
			t.Errorf("latency totals: got %+v", doc.Latency)
		}
		if doc.Latency.P50 != frozen.P50 || doc.Latency.P95 != frozen.P95 || doc.Latency.TtftP50 != frozen.TtftP50 {
			t.Errorf("percentiles must stay frozen: got %+v, had %+v", doc.Latency, frozen)
		}
		if doc.StragglerIDs != nil {
			t.Errorf("guard must be cleared, got %v", doc.StragglerIDs)
		}
		if left, _ := a.scanDayRecords(day); len(left) != 0 {
			t.Errorf("%d raw records left", len(left))
		}
	})
}

// TestRollup_StragglerCrashReentry: a crash after the straggler merge was
// persisted (guard written) but before the raw records were deleted — the
// rerun counts the stragglers exactly once (delete-only) and clears the
// guard.
func TestRollup_StragglerCrashReentry(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-11"
		first := []gen.AIStatsRecord{
			mkRollupRecord("r1", day, 100),
			mkRollupRecord("r2", day, 200),
		}
		seedRecords(t, a, first...)
		if _, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day}); err != nil {
			t.Fatalf("first rollup: %v", err)
		}

		stragglers := []gen.AIStatsRecord{
			mkRollupRecord("s1", day, 700),
			mkRollupRecord("s2", day, 800),
		}
		seedRecords(t, a, stragglers...)

		// Simulate the crashed run: merge + guard persisted, records left.
		doc := loadRollupForTest(t, a, dailyRollupName(day))
		applyStragglersToDoc(doc, stragglers)
		doc.StragglerIDs = recordIDs(stragglers)
		if err := a.store.Save(dailyRollupName(day), doc); err != nil {
			t.Fatalf("simulate crash-state save: %v", err)
		}
		wantViews, wantLatency, wantRequests := doc.Views, doc.Latency, doc.Requests

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day})
		if err != nil {
			t.Fatalf("rerun: %v", err)
		}
		if resp.DaysComputed != 0 || resp.DaysExisting != 1 || resp.RecordsDeleted != 2 {
			t.Errorf("rerun resp: computed=%d existing=%d deleted=%d, want 0/1/2",
				resp.DaysComputed, resp.DaysExisting, resp.RecordsDeleted)
		}
		after := loadRollupForTest(t, a, dailyRollupName(day))
		if !reflect.DeepEqual(after.Views, wantViews) {
			t.Errorf("stragglers recounted on reentry:\n got %+v\nwant %+v", after.Views, wantViews)
		}
		if after.Latency != wantLatency || after.Requests != wantRequests {
			t.Errorf("latency/requests changed on reentry: %+v/%d", after.Latency, after.Requests)
		}
		if after.StragglerIDs != nil {
			t.Errorf("guard must be cleared, got %v", after.StragglerIDs)
		}
		if left, _ := a.scanDayRecords(day); len(left) != 0 {
			t.Errorf("%d raw records left", len(left))
		}
	})
}

// TestRollup_StragglerIntoCompressedMonth: a straggler arriving for a month
// that was already compressed is absorbed into the monthly document (guard
// semantics), never creating a dangling daily document; the monthly totals
// equal the full record set.
func TestRollup_StragglerIntoCompressedMonth(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		const month = "2024-01"
		first := []gen.AIStatsRecord{
			mkRollupRecord("r1", "2024-01-05", 100),
			mkRollupRecord("r2", "2024-01-05", 300),
			mkRollupRecord("r3", "2024-01-31", 500),
		}
		seedRecords(t, a, first...)
		rollupToConvergence(t, a, testNow)
		base := loadRollupForTest(t, a, monthlyRollupName(month))
		if base == nil {
			t.Fatal("monthly doc missing before backfill")
		}

		s1 := mkRollupRecord("s1", "2024-01-20", 400)
		s1.WorkspaceID = "ws-2"
		s1.Provider = "anthropic"
		s1.Model = "claude-3"
		stragglers := []gen.AIStatsRecord{s1, mkRollupRecord("s2", "2024-01-20", 600)}
		seedRecords(t, a, stragglers...)

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{})
		if err != nil {
			t.Fatalf("rollup after backfill: %v", err)
		}
		if resp.DaysComputed != 1 {
			t.Errorf("days computed = %d, want 1 (resp %+v)", resp.DaysComputed, resp)
		}

		doc := loadRollupForTest(t, a, monthlyRollupName(month))
		all := append(append([]gen.AIStatsRecord{}, first...), stragglers...)
		want := rebuildCounters(all).snapshot()
		if !reflect.DeepEqual(doc.Views.Workspace, want.Workspace) {
			t.Errorf("workspace mismatch:\n got %+v\nwant %+v", doc.Views.Workspace, want.Workspace)
		}
		if !reflect.DeepEqual(doc.Views.WorkspaceMap, want.WorkspaceMap) {
			t.Errorf("workspaceMap mismatch:\n got %+v\nwant %+v", doc.Views.WorkspaceMap, want.WorkspaceMap)
		}
		if !reflect.DeepEqual(doc.Views.Provider, want.Provider) {
			t.Errorf("provider mismatch:\n got %+v\nwant %+v", doc.Views.Provider, want.Provider)
		}
		if !reflect.DeepEqual(doc.Views.Model, want.Model) {
			t.Errorf("model mismatch:\n got %+v\nwant %+v", doc.Views.Model, want.Model)
		}
		if doc.Requests != 5 {
			t.Errorf("requests: got %d, want 5", doc.Requests)
		}
		// Latency totals additively extended from the compressed base.
		if doc.Latency.Count != base.Latency.Count+2 || doc.Latency.SumMs != base.Latency.SumMs+1000 || doc.Latency.MaxMs != 600 {
			t.Errorf("latency: got %+v, base %+v", doc.Latency, base.Latency)
		}
		if doc.Views.Session != nil || doc.Views.Project != nil || doc.Views.Agent != nil {
			t.Errorf("monthly doc must stay four-view, got %+v", doc.Views)
		}

		// No dangling daily document was created for the straggler day.
		if doc := loadRollupForTest(t, a, dailyRollupName("2024-01-20")); doc != nil {
			t.Error("straggler absorption must not create a daily document")
		}
		if left, _ := a.scanDayRecords("2024-01-20"); len(left) != 0 {
			t.Errorf("%d raw records left", len(left))
		}
		var marker bool
		if err := a.store.Load("months/2024-01", &marker); !errors.Is(err, persist.ErrNotExist) {
			t.Errorf("month marker should be re-cleared, load err=%v", err)
		}
	})
}

// TestRollup_GuardCarriedThroughCompression: when a month compresses while
// a straggler's deletion is still pending (guard set on a daily doc, raw
// record alive), the guard must carry into the monthly document — the
// leftover is then deleted exactly once, never re-counted as fresh.
func TestRollup_GuardCarriedThroughCompression(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		const month = "2024-01"
		base := mkRollupRecord("r1", "2024-01-05", 100)
		straggler := mkRollupRecord("s1", "2024-01-05", 300)
		// The straggler is still raw on disk (its deletion never completed).
		seedRecords(t, a, straggler)

		// Complete daily-document month: day 05 carries r1+s1 counts with
		// the pending-deletion guard on s1.
		for day := 1; day <= 31; day++ {
			ds := month + fmt.Sprintf("-%02d", day)
			var recs []gen.AIStatsRecord
			if day == 5 {
				recs = []gen.AIStatsRecord{base, straggler}
			}
			doc := aggregateDayRollup(ds, recs)
			if day == 5 {
				doc.StragglerIDs = recordIDs([]gen.AIStatsRecord{straggler})
			}
			if err := a.store.Save(dailyRollupName(ds), doc); err != nil {
				t.Fatalf("seed daily doc %s: %v", ds, err)
			}
		}

		a.mu.Lock()
		outcome, err := a.rollupMonthLocked(testNow, month, false)
		a.mu.Unlock()
		if err != nil {
			t.Fatalf("compress: %v", err)
		}
		if outcome.state != monthComputed {
			t.Fatalf("compress state = %v, want monthComputed", outcome.state)
		}
		monthly := loadRollupForTest(t, a, monthlyRollupName(month))
		if len(monthly.StragglerIDs) != 1 || monthly.StragglerIDs[0] != "s1" {
			t.Fatalf("guard must carry into the monthly doc, got %v", monthly.StragglerIDs)
		}
		wantWorkspace := monthly.Views.Workspace
		wantRequests := monthly.Requests

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{})
		if err != nil {
			t.Fatalf("round after compression: %v", err)
		}
		if resp.RecordsDeleted != 1 {
			t.Errorf("records deleted = %d, want 1 (resp %+v)", resp.RecordsDeleted, resp)
		}
		after := loadRollupForTest(t, a, monthlyRollupName(month))
		if !reflect.DeepEqual(after.Views.Workspace, wantWorkspace) {
			t.Errorf("straggler re-counted after compression:\n got %+v\nwant %+v", after.Views.Workspace, wantWorkspace)
		}
		if after.Requests != wantRequests {
			t.Errorf("requests changed: got %d, want %d", after.Requests, wantRequests)
		}
		if after.StragglerIDs != nil {
			t.Errorf("guard must be cleared, got %v", after.StragglerIDs)
		}
		if left, _ := a.scanDayRecords("2024-01-05"); len(left) != 0 {
			t.Errorf("%d raw records left", len(left))
		}
	})
}

// TestRollup_DryRun: dryRun reports what would happen without writing
// documents, deleting records, or clearing markers.
func TestRollup_DryRun(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		day := "2026-05-14"
		seedRecords(t, a, mkRollupRecord("r1", day, 100))

		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{OnlyDay: day, DryRun: true})
		if err != nil {
			t.Fatalf("dryRun: %v", err)
		}
		if !resp.DryRun || resp.DaysComputed != 1 || resp.RecordsDeleted != 0 {
			t.Errorf("dryRun resp: %+v", resp)
		}
		if doc := loadRollupForTest(t, a, dailyRollupName(day)); doc != nil {
			t.Error("dryRun must not write the doc")
		}
		if left, _ := a.scanDayRecords(day); len(left) != 1 {
			t.Errorf("dryRun must not delete records, got %d left", len(left))
		}
		var marker bool
		if err := a.store.Load("months/2026-05", &marker); err != nil {
			t.Errorf("dryRun must not clear the month marker: %v", err)
		}
	})
}

// TestRollup_KCapBoundsRound: a single round examines at most K candidate
// days even when the backlog is larger, and the backlog drains over
// successive rounds.
func TestRollup_KCapBoundsRound(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)
		// Three records on three consecutive eligible days in 2026-05.
		seedRecords(t, a,
			mkRollupRecord("r1", "2026-05-20", 100),
			mkRollupRecord("r2", "2026-05-21", 100),
			mkRollupRecord("r3", "2026-05-22", 100),
		)
		resp, err := a.rollupOnce(testNow, AIStatsRollupReq{})
		if err != nil {
			t.Fatalf("round 1: %v", err)
		}
		if resp.DaysComputed > rollupCatchUpPerRound {
			t.Errorf("round computed %d days, cap is %d", resp.DaysComputed, rollupCatchUpPerRound)
		}
		// Drain the backlog: 2026-05 has 20 eligible days (01..20 before the
		// 2026-06-08 cutoff); each round processes at most K.
		rollupToConvergence(t, a, testNow)
		if doc := loadRollupForTest(t, a, dailyRollupName("2026-05-20")); doc == nil {
			t.Error("2026-05-20 should eventually be rolled up")
		}
		if doc := loadRollupForTest(t, a, dailyRollupName("2026-05-22")); doc == nil {
			t.Error("2026-05-22 should eventually be rolled up")
		}
	})
}
