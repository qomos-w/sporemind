package aistats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// setupTestStore creates a goleveldb-backed persist store in a temp dir for
// testing. Returns (store, scanner, fsRoot) where fsRoot is the legacy
// filesystem path used as the migration source. The store is closed on test
// cleanup.
func setupTestStore(t *testing.T) (store persist.Persist, scanner persist.PrefixScanner, fsRoot string) {
	t.Helper()
	dir := t.TempDir()
	fsRoot = filepath.Join(dir, "aistats")
	if err := os.MkdirAll(fsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s, err := persist.New(persist.PersistConfig{
		Backend: persist.BackendLevelDB,
		DataDir: dir,
		Prefix:  "aistats",
	})
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	store = s
	if sc, ok := s.(persist.PrefixScanner); ok {
		scanner = sc
	}
	t.Cleanup(func() {
		if c, ok := store.(interface{ Close() error }); ok {
			c.Close()
		}
	})
	return store, scanner, fsRoot
}

// writeRecordFS writes a record to the legacy filesystem layout. Used by
// migration tests to seed fs data that migrateFSToDB then copies into the DB.
func writeRecordFS(root string, r gen.AIStatsRecord) error {
	completedAt, _ := time.Parse(time.RFC3339Nano, r.CompletedAt)
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	dir := filepath.Join(root, "records", completedAt.Format("2006-01"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return persist.WriteFileAtomic(filepath.Join(dir, r.ID+".json"), b, 0o644)
}

// monthAnchor returns "YYYY-MM" for the given year/month offset, anchored to
// the 1st so AddDate-style day overflow can never shift the month.
func monthAnchor(year int, m time.Month) string {
	return time.Date(year, m, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
}

func TestAppendAndLoadRecords(t *testing.T) {
	store, scanner, _ := setupTestStore(t)
	r := gen.AIStatsRecord{
		ID:          "rec-1",
		WorkspaceID: "ws-1",
		ProjectID:   "proj-1",
		SessionID:   "sess-1",
		TurnID:      "turn-1",
		RequestID:   "req-1",
		Provider:    "openai",
		Model:       "gpt-4o",
		Usage: &gen.UsageData{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
		CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := appendRecord(store,r); err != nil {
		t.Fatalf("append: %v", err)
	}

	records, err := loadRecords(store, scanner,time.Time{}, time.Time{}, "session", "sess-1", "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ID != "rec-1" {
		t.Errorf("id: got %q, want rec-1", records[0].ID)
	}

	// Different scope should be empty.
	records, err = loadRecords(store, scanner,time.Time{}, time.Time{}, "session", "sess-2", "")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for other session, got %d", len(records))
	}

	// Empty workspace scope id matches all records in this actor.
	records, err = loadRecords(store, scanner,time.Time{}, time.Time{}, "workspace", "", "")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("expected 1 record for empty workspace scope, got %d", len(records))
	}
}

func TestCounters(t *testing.T) {
	c := newCounters()
	c.apply(gen.AIStatsRecord{
		ID:        "r1",
		SessionID: "s1",
		ProjectID: "p1",
		Provider:  "openai",
		Model:     "gpt-4o",
		StopReason: "error",
		LatencyMs:  500,
		Usage: &gen.UsageData{
			InputTokens:  100,
			OutputTokens: 50,
			CostTotal:    0.003,
		},
	})
	c.apply(gen.AIStatsRecord{
		ID:        "r2",
		SessionID: "s1",
		ProjectID: "p1",
		Provider:  "anthropic",
		Model:     "claude-3-5-sonnet",
		LatencyMs:  300,
		Usage: &gen.UsageData{
			InputTokens:  200,
			OutputTokens: 100,
			CostTotal:    0.007,
		},
	})

	if c.workspace.RequestCount != 2 {
		t.Errorf("workspace request count: got %d, want 2", c.workspace.RequestCount)
	}
	if c.workspace.ErrorCount != 1 {
		t.Errorf("workspace error count: got %d, want 1", c.workspace.ErrorCount)
	}
	if c.workspace.LatencySumMs != 800 {
		t.Errorf("workspace latency sum: got %d, want 800", c.workspace.LatencySumMs)
	}
	if c.workspace.InputTokens != 300 {
		t.Errorf("workspace input tokens: got %d, want 300", c.workspace.InputTokens)
	}
	if c.session["s1"].RequestCount != 2 {
		t.Errorf("session request count: got %d, want 2", c.session["s1"].RequestCount)
	}
	if c.provider["openai"].RequestCount != 1 {
		t.Errorf("openai request count: got %d, want 1", c.provider["openai"].RequestCount)
	}
	if c.model["openai/gpt-4o"].RequestCount != 1 {
		t.Errorf("model request count: got %d, want 1", c.model["openai/gpt-4o"].RequestCount)
	}
	if c.workspace.CostTotal != 0.01 {
		t.Errorf("workspace cost: got %f, want 0.01", c.workspace.CostTotal)
	}
}

func TestCheckpointRoundTrip(t *testing.T) {
	store, _, _ := setupTestStore(t)
	c := newCounters()
	c.apply(gen.AIStatsRecord{
		ID:        "r1",
		SessionID: "s1",
		Usage: &gen.UsageData{
			InputTokens:  100,
			OutputTokens: 50,
		},
	})
	if err := saveCheckpoint(store,c); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := loadCheckpoint(store)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.workspace.InputTokens != 100 {
		t.Errorf("input tokens: got %d, want 100", loaded.workspace.InputTokens)
	}
	if loaded.session["s1"].InputTokens != 100 {
		t.Errorf("session input tokens: got %d, want 100", loaded.session["s1"].InputTokens)
	}
}

func TestCostIndex(t *testing.T) {
	store, _, _ := setupTestStore(t)
	ci := newCostIndex(store)
	version, err := ci.configure(gen.AIStatsCostRate{
		Provider:   "openai",
		Model:      "gpt-4o",
		CostInput:  3.0,
		CostOutput: 15.0,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if version != 1 {
		t.Errorf("version: got %d, want 1", version)
	}

	cost := ci.costForUsage("openai", "gpt-4o", llmclient.Usage{InputTokens: 1000, OutputTokens: 200})
	if cost.Total == 0 {
		t.Errorf("expected non-zero cost, got %v", cost.Total)
	}
	want := 1000*3.0/1_000_000 + 200*15.0/1_000_000
	if cost.Total != want {
		t.Errorf("cost: got %f, want %f", cost.Total, want)
	}
}

func TestHandleAggregates(t *testing.T) {
	a := &Actor{counters: newCounters()}
	a.counters.apply(gen.AIStatsRecord{
		ID:        "r1",
		WorkspaceID: "ws-1",
		Provider:  "openai",
		Model:     "gpt-4o",
		Usage:     &gen.UsageData{InputTokens: 100, CostTotal: 0.003},
	})
	a.counters.apply(gen.AIStatsRecord{
		ID:        "r2",
		WorkspaceID: "ws-1",
		Provider:  "anthropic",
		Model:     "claude-3-5-sonnet",
		Usage:     &gen.UsageData{InputTokens: 200, CostTotal: 0.007},
	})
	a.counters.apply(gen.AIStatsRecord{
		ID:        "r3",
		WorkspaceID: "ws-1",
		Provider:  "openai",
		Model:     "gpt-4o",
		Usage:     &gen.UsageData{InputTokens: 50, CostTotal: 0.001},
	})

	resp, err := a.handleAggregates(nil, gen.AIStatsAggregatesReq{})
	if err != nil {
		t.Fatalf("handleAggregates: %v", err)
	}
	if resp.Workspace.RequestCount != 3 {
		t.Errorf("workspace request count: got %d, want 3", resp.Workspace.RequestCount)
	}
	if resp.Workspace.InputTokens != 350 {
		t.Errorf("workspace input tokens: got %d, want 350", resp.Workspace.InputTokens)
	}
	if resp.Workspace.CostTotal != 0.011 {
		t.Errorf("workspace cost: got %f, want 0.011", resp.Workspace.CostTotal)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("models: got %d, want 2", len(resp.Models))
	}
	// Sorted by CostTotal desc: anthropic (0.007) before openai (0.004).
	if resp.Models[0].Provider != "anthropic" || resp.Models[0].Model != "claude-3-5-sonnet" {
		t.Errorf("top model: got %s/%s, want anthropic/claude-3-5-sonnet", resp.Models[0].Provider, resp.Models[0].Model)
	}
	if resp.Models[1].Provider != "openai" || resp.Models[1].Model != "gpt-4o" {
		t.Errorf("second model: got %s/%s, want openai/gpt-4o", resp.Models[1].Provider, resp.Models[1].Model)
	}
	if resp.Models[1].Counters.RequestCount != 2 {
		t.Errorf("openai request count: got %d, want 2", resp.Models[1].Counters.RequestCount)
	}
	if len(resp.Providers) != 2 {
		t.Fatalf("providers: got %d, want 2", len(resp.Providers))
	}
	// Providers sorted by RequestCount desc: openai (2) before anthropic (1).
	if resp.Providers[0].Provider != "openai" || resp.Providers[0].Counters.RequestCount != 2 {
		t.Errorf("top provider: got %s/%d, want openai/2", resp.Providers[0].Provider, resp.Providers[0].Counters.RequestCount)
	}
}

func TestMonthsInRange(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	_ = scanner
	_ = root
	now := time.Now().UTC()

	// Empty store: empty result, no error.
	dirs, err := monthsInRange(store, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("monthsInRange empty: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected no dirs, got %v", dirs)
	}

	oldMonth := monthAnchor(now.Year(), now.Month()-2)
	prevMonth := monthAnchor(now.Year(), now.Month()-1)
	currMonth := monthAnchor(now.Year(), now.Month())
	for _, m := range []string{oldMonth, prevMonth, currMonth} {
		if err := store.Save("months/"+m, true); err != nil {
			t.Fatalf("save month marker %s: %v", m, err)
		}
	}

	// No since: all months returned in chronological order.
	all, err := monthsInRange(store, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("monthsInRange all: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 dirs, got %d: %v", len(all), all)
	}
	if all[0] != oldMonth || all[2] != currMonth {
		t.Errorf("dirs not chronological: %v", all)
	}

	// Since in the middle of prevMonth, until in the middle of currMonth.
	since, _ := time.Parse("2006-01-02", prevMonth+"-15")
	until, _ := time.Parse("2006-01-02", currMonth+"-10")
	filtered, err := monthsInRange(store, since, until)
	if err != nil {
		t.Fatalf("monthsInRange filtered: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 dirs, got %d: %v", len(filtered), filtered)
	}
	if filtered[0] != prevMonth || filtered[1] != currMonth {
		t.Errorf("filtered dirs: got %v, want %s and %s", filtered, prevMonth, currMonth)
	}

	// Since only: extends to the current month.
	sinceOnly, err := monthsInRange(store, since, time.Time{})
	if err != nil {
		t.Fatalf("monthsInRange since only: %v", err)
	}
	if len(sinceOnly) != 2 {
		t.Errorf("expected 2 dirs for since-only, got %d: %v", len(sinceOnly), sinceOnly)
	}
}

func TestPercentileFloat(t *testing.T) {
	if got := percentileFloat(nil, 50); got != 0 {
		t.Errorf("empty: got %v, want 0", got)
	}
	if got := percentileFloat([]float64{5}, 50); got != 5 {
		t.Errorf("single: got %v, want 5", got)
	}
	// Nearest-rank on 1..10: p50 -> index 5 -> 6, p95 -> index 9 -> 10.
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if got := percentileFloat(sorted, 50); got != 6 {
		t.Errorf("p50 of 1..10: got %v, want 6", got)
	}
	if got := percentileFloat(sorted, 95); got != 10 {
		t.Errorf("p95 of 1..10: got %v, want 10", got)
	}
	if got := percentileFloat(sorted, 100); got != 10 {
		t.Errorf("p100 of 1..10: got %v, want 10", got)
	}
	if got := percentileFloat([]float64{10, 20, 30, 40}, 50); got != 30 {
		t.Errorf("p50 of 4 elems: got %v, want 30", got)
	}
	if got := percentileFloat([]float64{10, 20, 30, 40}, 95); got != 40 {
		t.Errorf("p95 of 4 elems: got %v, want 40", got)
	}
}

func TestHandleSeries(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters()}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recs := []gen.AIStatsRecord{
		{
			ID: "r1", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 100, OutputTokens: 50, CostTotal: 0.001},
			LatencyMs:   100, FirstTokenMs: 50,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "r2", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 200, OutputTokens: 60, CostTotal: 0.002},
			LatencyMs:   300, FirstTokenMs: 90,
			CompletedAt: base.Add(30 * time.Minute).Format(time.RFC3339Nano),
		},
		{
			ID: "r5", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 50, OutputTokens: 20, CostTotal: 0.0005},
			LatencyMs:   150, FirstTokenMs: 40,
			CompletedAt: base.Add(45 * time.Minute).Format(time.RFC3339Nano),
		},
		{
			ID: "r3", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			Usage:       &gen.UsageData{InputTokens: 10, OutputTokens: 5, CostTotal: 0.0005},
			LatencyMs:   200, FirstTokenMs: 70,
			CompletedAt: base.Add(2 * time.Hour).Format(time.RFC3339Nano),
		},
		{
			ID: "r4", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			StopReason: "error", ErrorCode: "rate_limit", ErrorMessage: "boom",
			Usage:       &gen.UsageData{InputTokens: 5, OutputTokens: 0},
			LatencyMs:   50,
			CompletedAt: base.Add(2*time.Hour + 30*time.Minute).Format(time.RFC3339Nano),
		},
	}
	for _, r := range recs {
		if err := appendRecord(store,r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "workspace",
		ScopeID:  "",
		Since:    base.Format(time.RFC3339Nano),
		Until:    base.Add(4 * time.Hour).Format(time.RFC3339Nano),
		BucketMs: int64(time.Hour / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("handleSeries: %v", err)
	}

	if len(resp.Buckets) != 2 {
		t.Fatalf("buckets: got %d, want 2", len(resp.Buckets))
	}
	b0 := resp.Buckets[0]
	if b0.Requests != 3 {
		t.Errorf("bucket0 requests: got %d, want 3", b0.Requests)
	}
	if b0.InputTokens != 350 || b0.OutputTokens != 130 {
		t.Errorf("bucket0 tokens: got %d/%d, want 350/130", b0.InputTokens, b0.OutputTokens)
	}
	// Latencies 100,150,300 sorted: p50 index 1 -> 150, p95 index 2 -> 300.
	if b0.LatencyP50 != 150 || b0.LatencyP95 != 300 {
		t.Errorf("bucket0 latency p50/p95: got %v/%v, want 150/300", b0.LatencyP50, b0.LatencyP95)
	}
	if b0.LatencySumMs != 550 {
		t.Errorf("bucket0 latency sum: got %d, want 550", b0.LatencySumMs)
	}
	if b0.TtftP50 != 50 {
		t.Errorf("bucket0 ttft p50: got %v, want 50", b0.TtftP50)
	}

	b1 := resp.Buckets[1]
	if b1.Requests != 2 || b1.Errors != 1 {
		t.Errorf("bucket1 requests/errors: got %d/%d, want 2/1", b1.Requests, b1.Errors)
	}
	if b1.ErrorCodes["rate_limit"] != 1 {
		t.Errorf("bucket1 rate_limit errors: got %d, want 1", b1.ErrorCodes["rate_limit"])
	}
	if b1.InputTokens != 15 || b1.OutputTokens != 5 {
		t.Errorf("bucket1 tokens: got %d/%d, want 15/5", b1.InputTokens, b1.OutputTokens)
	}

	// Model stats sorted by request count desc.
	if len(resp.ModelStats) != 2 {
		t.Fatalf("model stats: got %d, want 2", len(resp.ModelStats))
	}
	if resp.ModelStats[0].Provider != "openai" || resp.ModelStats[0].Requests != 3 {
		t.Errorf("top model stat: got %s/%d, want openai/3", resp.ModelStats[0].Provider, resp.ModelStats[0].Requests)
	}
	if resp.ModelStats[0].OutputTokens != 130 {
		t.Errorf("openai output tokens: got %d, want 130", resp.ModelStats[0].OutputTokens)
	}
	// openai cost: 0.001 + 0.002 + 0.0005 = 0.0035
	if resp.ModelStats[0].CostTotal != 0.0035 {
		t.Errorf("openai cost total: got %f, want 0.0035", resp.ModelStats[0].CostTotal)
	}
	if resp.ModelStats[1].Provider != "anthropic" || resp.ModelStats[1].Requests != 2 {
		t.Errorf("second model stat: got %s/%d, want anthropic/2", resp.ModelStats[1].Provider, resp.ModelStats[1].Requests)
	}
	if resp.ModelStats[1].Errors != 1 {
		t.Errorf("anthropic errors: got %d, want 1", resp.ModelStats[1].Errors)
	}

	// Latencies 50,100,150,200,300: sum 800; p50 index 2 -> 150; p95 index 4 -> 300.
	if resp.OverallLatencySumMs != 800 {
		t.Errorf("overall latency sum: got %d, want 800", resp.OverallLatencySumMs)
	}
	if resp.OverallLatencyP50 != 150 || resp.OverallLatencyP95 != 300 {
		t.Errorf("overall latency p50/p95: got %v/%v, want 150/300", resp.OverallLatencyP50, resp.OverallLatencyP95)
	}
	// Ttfts 40,50,70,90 sorted: p50 index 2 -> 70.
	if resp.OverallTtftP50 != 70 {
		t.Errorf("overall ttft p50: got %v, want 70", resp.OverallTtftP50)
	}
	if resp.OverallOutputTokens != 135 {
		t.Errorf("overall output tokens: got %d, want 135", resp.OverallOutputTokens)
	}

	// Invalid bucket size falls back to daily buckets.
	respDaily, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "workspace",
		ScopeID:  "",
		BucketMs: -1,
	})
	if err != nil {
		t.Fatalf("handleSeries daily: %v", err)
	}
	if len(respDaily.Buckets) != 1 || respDaily.Buckets[0].Requests != 5 {
		t.Errorf("daily buckets: got %d requests, want 1 bucket with 5", respDaily.Buckets[0].Requests)
	}
}

// TestHandleSeriesModelScope verifies that Scope "model" with ScopeId
// "provider/model" restricts buckets, model stats, and overall metrics to a
// single unit — the query the dashboard issues after a unit is selected.
func TestHandleSeriesModelScope(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters()}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recs := []gen.AIStatsRecord{
		{
			ID: "m1", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 100, OutputTokens: 50, CostTotal: 0.001},
			LatencyMs:   100, FirstTokenMs: 50,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "m2", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			Usage:       &gen.UsageData{InputTokens: 10, OutputTokens: 5, CostTotal: 0.0005},
			LatencyMs:   200, FirstTokenMs: 70,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "m3", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			StopReason: "error", ErrorCode: "rate_limit", ErrorMessage: "boom",
			Usage:       &gen.UsageData{InputTokens: 5},
			LatencyMs:   50,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
	}
	for _, r := range recs {
		if err := appendRecord(store,r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	resp, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "model",
		ScopeID:  "anthropic/claude-3-5-sonnet",
		Since:    base.Format(time.RFC3339Nano),
		BucketMs: int64(time.Hour / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("handleSeries: %v", err)
	}

	if len(resp.Buckets) != 1 {
		t.Fatalf("buckets: got %d, want 1", len(resp.Buckets))
	}
	b0 := resp.Buckets[0]
	if b0.Requests != 2 || b0.Errors != 1 {
		t.Errorf("bucket requests/errors: got %d/%d, want 2/1", b0.Requests, b0.Errors)
	}
	if b0.InputTokens != 15 || b0.OutputTokens != 5 {
		t.Errorf("bucket tokens: got %d/%d, want 15/5", b0.InputTokens, b0.OutputTokens)
	}
	if b0.ErrorCodes["rate_limit"] != 1 {
		t.Errorf("bucket rate_limit errors: got %d, want 1", b0.ErrorCodes["rate_limit"])
	}

	if len(resp.ModelStats) != 1 {
		t.Fatalf("model stats: got %d, want 1", len(resp.ModelStats))
	}
	ms := resp.ModelStats[0]
	if ms.Provider != "anthropic" || ms.Model != "claude-3-5-sonnet" {
		t.Errorf("model stat: got %s/%s, want anthropic/claude-3-5-sonnet", ms.Provider, ms.Model)
	}
	if ms.Requests != 2 || ms.Errors != 1 || ms.OutputTokens != 5 {
		t.Errorf("model stat counters: got %d req / %d err / %d out, want 2/1/5", ms.Requests, ms.Errors, ms.OutputTokens)
	}

	// Overall metrics must reflect the scoped records only: latencies 200,50
	// sorted → p50 index 1 → 200; output tokens 5.
	if resp.OverallLatencyP50 != 200 {
		t.Errorf("overall latency p50: got %v, want 200", resp.OverallLatencyP50)
	}
	if resp.OverallOutputTokens != 5 {
		t.Errorf("overall output tokens: got %d, want 5", resp.OverallOutputTokens)
	}

	// Unknown unit yields an empty series, not the global picture.
	empty, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "model",
		ScopeID:  "openai/missing-model",
		Since:    base.Format(time.RFC3339Nano),
		BucketMs: int64(time.Hour / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("handleSeries unknown unit: %v", err)
	}
	if len(empty.Buckets) != 0 || len(empty.ModelStats) != 0 {
		t.Errorf("unknown unit: got %d buckets / %d model stats, want 0/0", len(empty.Buckets), len(empty.ModelStats))
	}
}

// TestFilterRecordsByModel unit-tests the pure ModelFilter record keeper:
// case-insensitive substring match on Provider or Model, empty/whitespace
// filter keeps everything, non-matching records are dropped.
func TestFilterRecordsByModel(t *testing.T) {
	recs := []gen.AIStatsRecord{
		{ID: "a", Provider: "openai", Model: "gpt-4o"},
		{ID: "b", Provider: "anthropic", Model: "claude-3-5-sonnet"},
		{ID: "c", Provider: "deepseek", Model: "deepseek-chat"},
		{ID: "d"}, // provider/model empty
	}

	if got := filterRecordsByModel(recs, ""); len(got) != 4 {
		t.Fatalf("empty filter: got %d records, want 4", len(got))
	}
	if got := filterRecordsByModel(recs, "   "); len(got) != 4 {
		t.Fatalf("whitespace filter: got %d records, want 4", len(got))
	}

	got := filterRecordsByModel(recs, "GPT")
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("model substring: got %+v, want [a]", got)
	}

	got = filterRecordsByModel(recs, "DEEP")
	if len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("provider substring (case-insensitive): got %+v, want [c]", got)
	}

	got = filterRecordsByModel(recs, "k-chat")
	if len(got) != 1 || got[0].ID != "c" {
		t.Fatalf("model tail substring: got %+v, want [c]", got)
	}

	if got := filterRecordsByModel(recs, "gemini"); len(got) != 0 {
		t.Fatalf("non-matching filter: got %+v, want empty", got)
	}
}

// TestHandleSeriesModelFilter verifies ModelFilter restricts series aggregation
// end to end: buckets, model stats and overall metrics only count records whose
// Provider or Model contains the filter as a case-insensitive substring.
func TestHandleSeriesModelFilter(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters()}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recs := []gen.AIStatsRecord{
		{
			ID: "r1", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 100, OutputTokens: 50, CostTotal: 0.001},
			LatencyMs:   100, FirstTokenMs: 50,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "r2", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 200, OutputTokens: 60, CostTotal: 0.002},
			LatencyMs:   300, FirstTokenMs: 90,
			CompletedAt: base.Add(30 * time.Minute).Format(time.RFC3339Nano),
		},
		{
			ID: "r3", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			Usage:       &gen.UsageData{InputTokens: 10, OutputTokens: 5, CostTotal: 0.0005},
			LatencyMs:   200, FirstTokenMs: 70,
			CompletedAt: base.Add(2 * time.Hour).Format(time.RFC3339Nano),
		},
		{
			ID: "r4", WorkspaceID: "ws-1", Provider: "anthropic", Model: "claude-3-5-sonnet",
			StopReason: "error", ErrorCode: "rate_limit", ErrorMessage: "boom",
			Usage:       &gen.UsageData{InputTokens: 5},
			LatencyMs:   50,
			CompletedAt: base.Add(2*time.Hour + 30*time.Minute).Format(time.RFC3339Nano),
		},
	}
	for _, r := range recs {
		if err := appendRecord(store,r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	bucketMs := int64(time.Hour / time.Millisecond)

	// Model substring filter: only the two anthropic records match.
	resp, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:       "workspace",
		Since:       base.Format(time.RFC3339Nano),
		Until:       base.Add(4 * time.Hour).Format(time.RFC3339Nano),
		ModelFilter: "sonnet",
		BucketMs:    bucketMs,
	})
	if err != nil {
		t.Fatalf("handleSeries model filter: %v", err)
	}
	if len(resp.Buckets) != 1 {
		t.Fatalf("filtered buckets: got %d, want 1", len(resp.Buckets))
	}
	b0 := resp.Buckets[0]
	if b0.Requests != 2 || b0.Errors != 1 {
		t.Errorf("filtered bucket req/err: got %d/%d, want 2/1", b0.Requests, b0.Errors)
	}
	if b0.InputTokens != 15 || b0.OutputTokens != 5 {
		t.Errorf("filtered bucket tokens: got %d/%d, want 15/5", b0.InputTokens, b0.OutputTokens)
	}
	if len(resp.ModelStats) != 1 || resp.ModelStats[0].Model != "claude-3-5-sonnet" {
		t.Fatalf("filtered model stats: got %+v, want claude-3-5-sonnet only", resp.ModelStats)
	}
	if resp.OverallOutputTokens != 5 {
		t.Errorf("filtered overall output: got %d, want 5", resp.OverallOutputTokens)
	}
	if resp.OverallLatencySumMs != 250 {
		t.Errorf("filtered overall latency sum: got %d, want 250", resp.OverallLatencySumMs)
	}

	// Provider substring filter, uppercase value exercises case-insensitivity:
	// only the two openai records match.
	resp, err = a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:       "workspace",
		Since:       base.Format(time.RFC3339Nano),
		Until:       base.Add(4 * time.Hour).Format(time.RFC3339Nano),
		ModelFilter: "OPENAI",
		BucketMs:    bucketMs,
	})
	if err != nil {
		t.Fatalf("handleSeries provider filter: %v", err)
	}
	if len(resp.Buckets) != 1 || resp.Buckets[0].Requests != 2 {
		t.Fatalf("provider-filtered buckets: got %+v, want 1 bucket with 2", resp.Buckets)
	}
	if len(resp.ModelStats) != 1 || resp.ModelStats[0].Provider != "openai" {
		t.Fatalf("provider-filtered model stats: got %+v, want openai only", resp.ModelStats)
	}

	// A filter matching no record yields an empty series.
	resp, err = a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:       "workspace",
		Since:       base.Format(time.RFC3339Nano),
		Until:       base.Add(4 * time.Hour).Format(time.RFC3339Nano),
		ModelFilter: "gemini",
		BucketMs:    bucketMs,
	})
	if err != nil {
		t.Fatalf("handleSeries no-match filter: %v", err)
	}
	if len(resp.Buckets) != 0 || len(resp.ModelStats) != 0 {
		t.Errorf("no-match filter: got %d buckets / %d model stats, want 0/0", len(resp.Buckets), len(resp.ModelStats))
	}

	// Empty filter restores the full view: 2 hourly buckets, 4 requests, 2 models.
	resp, err = a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "workspace",
		Since:    base.Format(time.RFC3339Nano),
		Until:    base.Add(4 * time.Hour).Format(time.RFC3339Nano),
		BucketMs: bucketMs,
	})
	if err != nil {
		t.Fatalf("handleSeries empty filter: %v", err)
	}
	if len(resp.Buckets) != 2 || resp.Buckets[0].Requests+resp.Buckets[1].Requests != 4 {
		t.Fatalf("empty filter buckets: got %+v, want 2 buckets with 4 total", resp.Buckets)
	}
	if len(resp.ModelStats) != 2 {
		t.Fatalf("empty filter model stats: got %d, want 2", len(resp.ModelStats))
	}
}

func TestQueryOrderAndTotal(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters()}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, id := range []string{"r1", "r2", "r3"} {
		r := gen.AIStatsRecord{
			ID:          id,
			WorkspaceID: "ws-1",
			Provider:    "openai",
			Model:       "gpt-4o",
			CompletedAt: base.Add(time.Duration(i+1) * time.Hour).Format(time.RFC3339Nano),
		}
		if err := appendRecord(store,r); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	// Default ascending with limit.
	resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Limit: 2,
	})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("total: got %d, want 3", resp.Total)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("records: got %d, want 2", len(resp.Records))
	}
	if resp.Records[0].ID != "r1" || resp.Records[1].ID != "r2" {
		t.Errorf("asc order: got %s,%s, want r1,r2", resp.Records[0].ID, resp.Records[1].ID)
	}

	// Descending with limit: newest first.
	resp, err = a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Limit: 2, Order: "desc",
	})
	if err != nil {
		t.Fatalf("handleQuery desc: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("total desc: got %d, want 3", resp.Total)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("records desc: got %d, want 2", len(resp.Records))
	}
	if resp.Records[0].ID != "r3" || resp.Records[1].ID != "r2" {
		t.Errorf("desc order: got %s,%s, want r3,r2", resp.Records[0].ID, resp.Records[1].ID)
	}

	// Offset combined with descending.
	resp, err = a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Limit: 1, Offset: 1, Order: "desc",
	})
	if err != nil {
		t.Fatalf("handleQuery offset: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("total offset: got %d, want 3", resp.Total)
	}
	if len(resp.Records) != 1 || resp.Records[0].ID != "r2" {
		t.Errorf("offset desc: got %v, want r2", resp.Records)
	}
}

func TestHotCacheQuery(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	// Records written through handleRecord populate the hot cache.
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	for i, id := range []string{"h1", "h2", "h3"} {
		r := gen.AIStatsRecord{
			ID:          id,
			WorkspaceID: "ws-1",
			Provider:    "openai",
			Model:       "gpt-4o",
			CompletedAt: base.Add(time.Duration(i+1) * time.Minute).Format(time.RFC3339Nano),
		}
		if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: r}); err != nil {
			t.Fatalf("handleRecord: %v", err)
		}
	}
	if a.hotRecords.recordCount() != 3 {
		t.Fatalf("hot cache: got %d records, want 3", a.hotRecords.recordCount())
	}

	// A since covering the whole hot cache must be served from memory.
	since := base.Add(time.Minute)
	if !canServeFromHot(a.hotRecords.snapshot(), since, time.Time{}) {
		t.Fatal("expected query to be servable from the hot cache")
	}
	resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Since: since.Format(time.RFC3339Nano),
		Order: "desc",
	})
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if resp.Total != 3 {
		t.Errorf("total: got %d, want 3", resp.Total)
	}
	if len(resp.Records) != 3 || resp.Records[0].ID != "h3" || resp.Records[2].ID != "h1" {
		t.Errorf("hot desc query: got %v, want [h3 h2 h1]", resp.Records)
	}
	if resp.Counters.RequestCount != 3 {
		t.Errorf("counters: got %d, want 3", resp.Counters.RequestCount)
	}

	// A since inside the hot window only returns the newer records.
	resp, err = a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Since: base.Add(2 * time.Minute).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("handleQuery partial: %v", err)
	}
	if resp.Total != 2 || len(resp.Records) != 2 ||
		resp.Records[0].ID != "h2" || resp.Records[1].ID != "h3" {
		t.Errorf("partial hot query: total=%d records=%v, want 2 [h2 h3]", resp.Total, resp.Records)
	}

	// A since older than the oldest hot record falls back to disk and still works.
	resp, err = a.handleQuery(nil, gen.AIStatsQueryReq{
		Scope: "workspace", ScopeID: "",
		Since: base.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("handleQuery cold: %v", err)
	}
	if resp.Total != 3 || len(resp.Records) != 3 ||
		resp.Records[0].ID != "h1" || resp.Records[2].ID != "h3" {
		t.Errorf("cold fallback query: total=%d records=%v, want 3 [h1 h2 h3]", resp.Total, resp.Records)
	}
}

func TestHotCacheSeries(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour)
	recs := []gen.AIStatsRecord{
		{
			ID: "h1", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 100, OutputTokens: 50},
			LatencyMs:   100, FirstTokenMs: 40,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "h2", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 200, OutputTokens: 60},
			LatencyMs:   300, FirstTokenMs: 90,
			CompletedAt: base.Add(30 * time.Minute).Format(time.RFC3339Nano),
		},
		{
			ID: "h3", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 50, OutputTokens: 20},
			LatencyMs:   150, FirstTokenMs: 50,
			CompletedAt: base.Add(45 * time.Minute).Format(time.RFC3339Nano),
		},
	}
	for _, r := range recs {
		if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: r}); err != nil {
			t.Fatalf("handleRecord: %v", err)
		}
	}

	// A since inside the hot window must be aggregated from memory.
	since := base.Add(10 * time.Minute)
	if !canServeFromHot(a.hotRecords.snapshot(), since, time.Time{}) {
		t.Fatal("expected series to be servable from the hot cache")
	}
	resp, err := a.handleSeries(nil, gen.AIStatsSeriesReq{
		Scope:    "workspace",
		ScopeID:  "",
		Since:    since.Format(time.RFC3339Nano),
		BucketMs: int64(time.Hour / time.Millisecond),
	})
	if err != nil {
		t.Fatalf("handleSeries: %v", err)
	}
	if len(resp.Buckets) != 1 {
		t.Fatalf("buckets: got %d, want 1", len(resp.Buckets))
	}
	b0 := resp.Buckets[0]
	if b0.Requests != 2 {
		t.Errorf("bucket requests: got %d, want 2", b0.Requests)
	}
	if b0.InputTokens != 250 || b0.OutputTokens != 80 {
		t.Errorf("bucket tokens: got %d/%d, want 250/80", b0.InputTokens, b0.OutputTokens)
	}
	if b0.LatencySumMs != 450 {
		t.Errorf("bucket latency sum: got %d, want 450", b0.LatencySumMs)
	}
	if len(resp.ModelStats) != 1 || resp.ModelStats[0].Requests != 2 || resp.ModelStats[0].OutputTokens != 80 {
		t.Errorf("model stats: got %+v, want openai with 2 requests / 80 output tokens", resp.ModelStats)
	}
}

func TestCleanupAggregatorGhostRecords(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	now := time.Now().UTC()
	base := now.Truncate(time.Hour).Add(-2 * time.Hour)

	// Seed ghost records with Provider="aggregator" and real records with a
	// mix of providers/models. We intentionally use distinct months to make
	// deleteRecord walk more than one directory.
	ghosts := []gen.AIStatsRecord{
		{
			ID: "agg-1", WorkspaceID: "ws-1", ProjectID: "p-1", SessionID: "s-1",
			RequestID: "r-1", Provider: "aggregator", Model: "aggregator",
			Usage:       &gen.UsageData{InputTokens: 10, OutputTokens: 5, CostTotal: 0.0001},
			LatencyMs:   10,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "agg-2", WorkspaceID: "ws-1", ProjectID: "p-1", SessionID: "s-1",
			RequestID: "r-2", Provider: "aggregator", Model: "aggregator",
			Usage:       &gen.UsageData{InputTokens: 20, OutputTokens: 10, CostTotal: 0.0002},
			LatencyMs:   20,
			CompletedAt: base.Add(1 * time.Hour).Format(time.RFC3339Nano),
		},
		{
			ID: "agg-3", WorkspaceID: "ws-1", ProjectID: "p-2", SessionID: "s-2",
			RequestID: "r-3", Provider: "aggregator", Model: "aggregator",
			Usage:       &gen.UsageData{InputTokens: 30, OutputTokens: 15, CostTotal: 0.0003},
			LatencyMs:   30,
			CompletedAt: base.Add(2 * time.Hour).Format(time.RFC3339Nano),
		},
	}
	real := []gen.AIStatsRecord{
		{
			ID: "real-1", WorkspaceID: "ws-1", ProjectID: "p-1", SessionID: "s-1",
			RequestID: "rr-1", Provider: "openai", Model: "gpt-4o",
			Usage:       &gen.UsageData{InputTokens: 100, OutputTokens: 50, CostTotal: 0.003},
			LatencyMs:   100,
			CompletedAt: base.Format(time.RFC3339Nano),
		},
		{
			ID: "real-2", WorkspaceID: "ws-1", ProjectID: "p-1", SessionID: "s-1",
			RequestID: "rr-2", Provider: "anthropic", Model: "claude-sonnet",
			Usage:       &gen.UsageData{InputTokens: 200, OutputTokens: 100, CostTotal: 0.007},
			LatencyMs:   200,
			CompletedAt: base.Add(1 * time.Hour).Format(time.RFC3339Nano),
		},
	}

	for _, r := range append(ghosts, real...) {
		if err := appendRecord(store,r); err != nil {
			t.Fatalf("append %s: %v", r.ID, err)
		}
		// Simulate pre-cleanup counters that include the ghost records.
		a.counters.apply(r)
	}

	if a.counters.provider["aggregator"].RequestCount != int64(len(ghosts)) {
		t.Fatalf("pre-cleanup aggregator counter: got %d, want %d", a.counters.provider["aggregator"].RequestCount, len(ghosts))
	}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleCleanup(ctx, AIStatsCleanupReq{})
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if resp.Found != int64(len(ghosts)) {
		t.Errorf("found: got %d, want %d", resp.Found, len(ghosts))
	}
	if resp.Deleted != int64(len(ghosts)) {
		t.Errorf("deleted: got %d, want %d", resp.Deleted, len(ghosts))
	}
	if resp.Remaining != 0 {
		t.Errorf("remaining: got %d, want 0", resp.Remaining)
	}

	// Ghost files should be gone, real files should remain.
	remaining, err := loadRecords(store, scanner,time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("loadRecords after cleanup: %v", err)
	}
	if len(remaining) != len(real) {
		t.Fatalf("records remaining: got %d, want %d", len(remaining), len(real))
	}
	for _, r := range remaining {
		if r.Provider == "aggregator" {
			t.Errorf("ghost record still on disk: %s", r.ID)
		}
	}

	// Counters should be rebuilt from real records only.
	if a.counters.workspace.RequestCount != int64(len(real)) {
		t.Errorf("workspace request count: got %d, want %d", a.counters.workspace.RequestCount, len(real))
	}
	if _, ok := a.counters.provider["aggregator"]; ok {
		t.Errorf("aggregator provider counter still present after cleanup")
	}
	if a.counters.provider["openai"].RequestCount != 1 {
		t.Errorf("openai provider count: got %d, want 1", a.counters.provider["openai"].RequestCount)
	}
	if a.counters.provider["anthropic"].RequestCount != 1 {
		t.Errorf("anthropic provider count: got %d, want 1", a.counters.provider["anthropic"].RequestCount)
	}
	if a.counters.model["openai/gpt-4o"].RequestCount != 1 {
		t.Errorf("openai/gpt-4o model count: got %d, want 1", a.counters.model["openai/gpt-4o"].RequestCount)
	}
	if a.counters.model["anthropic/claude-sonnet"].RequestCount != 1 {
		t.Errorf("anthropic/claude-sonnet model count: got %d, want 1", a.counters.model["anthropic/claude-sonnet"].RequestCount)
	}

	// Checkpoint should be refreshed and free of aggregator counters.
	cp, err := loadCheckpoint(store)
	if err != nil {
		t.Fatalf("loadCheckpoint: %v", err)
	}
	if cp.workspace.RequestCount != int64(len(real)) {
		t.Errorf("checkpoint workspace request count: got %d, want %d", cp.workspace.RequestCount, len(real))
	}
	if _, ok := cp.provider["aggregator"]; ok {
		t.Errorf("checkpoint still contains aggregator provider counter")
	}
	if cp.provider["openai"].InputTokens != 100 {
		t.Errorf("checkpoint openai input tokens: got %d, want 100", cp.provider["openai"].InputTokens)
	}
	if cp.provider["anthropic"].InputTokens != 200 {
		t.Errorf("checkpoint anthropic input tokens: got %d, want 200", cp.provider["anthropic"].InputTokens)
	}

	// Idempotence: a second cleanup should find nothing and leave state unchanged.
	resp2, err := a.handleCleanup(ctx, AIStatsCleanupReq{})
	if err != nil {
		t.Fatalf("cleanup second call: %v", err)
	}
	if resp2.Found != 0 || resp2.Deleted != 0 || resp2.Remaining != 0 {
		t.Errorf("cleanup not idempotent: %+v", resp2)
	}
	remaining2, _ := loadRecords(store, scanner,time.Time{}, time.Time{}, "", "", "")
	if len(remaining2) != len(real) {
		t.Errorf("records changed after second cleanup: got %d", len(remaining2))
	}
	if cp2, err := loadCheckpoint(store); err != nil {
		t.Fatalf("loadCheckpoint second: %v", err)
	} else if cp2.workspace.RequestCount != int64(len(real)) {
		t.Errorf("checkpoint changed after second cleanup: got %d", cp2.workspace.RequestCount)
	}
}

// TestCleanup_RebuildIncludesRollups (设计 §7 测试 10) constructs the mixed
// retention state — one month compressed into a permanent monthly document,
// one month still at daily documents, recent data still raw — and verifies
// that a cleanup rebuild reproduces the full manual sum instead of losing
// the convolved history. It also pins the declared dimension downgrade: the
// monthly document's Session/Project/Agent views are nil, so the rebuilt
// session/project/agent counters contain nearline data only.
//
// Run on both real persist backends via rollupBackends (card 5
// "全链路验收" requires dual-backend verification).
func TestCleanup_RebuildIncludesRollups(t *testing.T) {
	rollupBackends(t, func(t *testing.T, backend persist.BackendType) {
		a := setupRollupActor(t, backend)

		oldMonth := "2025-01"  // older than the monthly cutoff: compresses to L2
		oldDay := "2025-01-10" // within oldMonth
		l1Day := "2026-05-10"  // past the raw cutoff, inside the daily window: stays L1
		rawDay := "2026-09-01" // inside the raw window: stays a raw record

		oldRecs := []gen.AIStatsRecord{
			mkRollupRecord("old-1", oldDay, 100),
			mkRollupRecord("old-2", oldDay, 300),
		}
		l1Recs := []gen.AIStatsRecord{
			mkRollupRecord("l1-1", l1Day, 150),
			mkRollupRecord("l1-2", l1Day, 250),
		}
		rawRecs := []gen.AIStatsRecord{
			mkRollupRecord("raw-1", rawDay, 200),
		}
		all := append(append(append([]gen.AIStatsRecord{}, oldRecs...), l1Recs...), rawRecs...)
		seedRecords(t, a, all...)

		// Convolve: oldMonth compresses into a monthly document, l1Day stays a
		// daily document, rawDay keeps its raw record.
		rollupToConvergence(t, a, testNow)
		if doc := loadRollupForTest(t, a, monthlyRollupName(oldMonth)); doc == nil {
			t.Fatal("monthly rollup doc missing after convergence")
		}
		if doc := loadRollupForTest(t, a, dailyRollupName(l1Day)); doc == nil {
			t.Fatal("daily rollup doc missing after convergence")
		}
		if left, _ := a.scanDayRecords(oldDay); len(left) != 0 {
			t.Fatalf("raw records of %s not reclaimed: %d left", oldDay, len(left))
		}
		if left, _ := a.scanDayRecords(rawDay); len(left) != len(rawRecs) {
			t.Fatalf("raw records of %s disturbed: got %d, want %d", rawDay, len(left), len(rawRecs))
		}

		// Simulate a stale checkpoint: wipe the counters. A raw-only rebuild
		// would answer 1 request; the correct rebuild answers all 5.
		a.counters = newCounters()

		ctx := testutil.AnonCtx(testutil.GenActorID())
		resp, err := a.handleCleanup(ctx, AIStatsCleanupReq{})
		if err != nil {
			t.Fatalf("cleanup: %v", err)
		}
		if resp.Found != 0 || resp.Deleted != 0 {
			t.Fatalf("unexpected ghosts: %+v", resp)
		}

		got := a.counters.snapshot()

		// The four bounded-cardinality views carry full history (the monthly
		// document keeps Workspace/WorkspaceMap/Provider/Model).
		wantAll := rebuildCounters(all).snapshot()
		if !reflect.DeepEqual(got.Workspace, wantAll.Workspace) {
			t.Errorf("workspace counters mismatch:\n got %+v\nwant %+v", got.Workspace, wantAll.Workspace)
		}
		for _, field := range []struct {
			name string
			got  map[string]gen.AIStatsCounters
			want map[string]gen.AIStatsCounters
		}{
			{"workspaceMap", got.WorkspaceMap, wantAll.WorkspaceMap},
			{"provider", got.Provider, wantAll.Provider},
			{"model", got.Model, wantAll.Model},
		} {
			if !reflect.DeepEqual(field.got, field.want) {
				t.Errorf("%s counters mismatch:\n got %+v\nwant %+v", field.name, field.got, field.want)
			}
		}

		// Session/Project/Agent are dropped by the monthly compression (I5), so
		// these views hold nearline (daily + raw) data only.
		wantNearline := rebuildCounters(append(append([]gen.AIStatsRecord{}, l1Recs...), rawRecs...)).snapshot()
		for _, field := range []struct {
			name string
			got  map[string]gen.AIStatsCounters
			want map[string]gen.AIStatsCounters
		}{
			{"session", got.Session, wantNearline.Session},
			{"project", got.Project, wantNearline.Project},
			{"agent", got.Agent, wantNearline.Agent},
		} {
			if !reflect.DeepEqual(field.got, field.want) {
				t.Errorf("%s counters mismatch:\n got %+v\nwant %+v", field.name, field.got, field.want)
			}
		}

		// The persisted checkpoint must reflect the same rebuild.
		cp, err := loadCheckpoint(a.store)
		if err != nil {
			t.Fatalf("loadCheckpoint: %v", err)
		}
		if cpSnap := cp.snapshot(); !reflect.DeepEqual(cpSnap, got) {
			t.Errorf("checkpoint mismatch after rebuild:\n got %+v\nwant %+v", cpSnap, got)
		}

		// Idempotence: a second cleanup rebuilds the same counters.
		if _, err := a.handleCleanup(ctx, AIStatsCleanupReq{}); err != nil {
			t.Fatalf("cleanup second call: %v", err)
		}
		if again := a.counters.snapshot(); !reflect.DeepEqual(again, got) {
			t.Errorf("counters changed after second cleanup:\n got %+v\nwant %+v", again, got)
		}
	})
}

func TestMain(m *testing.M) {
	config.SetDataDirForTest(".")
	os.Exit(m.Run())
}
