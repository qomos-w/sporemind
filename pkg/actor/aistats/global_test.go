package aistats

import (
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestGlobalActor_MultiWorkspaceAggregation verifies the global actor aggregates
// records from multiple workspaces and that per-workspace filtering returns a
// single workspace's data while an empty WorkspaceID returns the cross-workspace
// aggregate.
func TestGlobalActor_MultiWorkspaceAggregation(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	mk := func(id, ws, provider, model string, out int64, err bool) gen.AIStatsRecord {
		stop := "end_turn"
		if err {
			stop = "error"
		}
		return gen.AIStatsRecord{
			ID:          id,
			WorkspaceID: ws,
			Provider:    provider,
			Model:       model,
			CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
			StopReason:  stop,
			Usage: &gen.UsageData{
				OutputTokens: out,
				TotalTokens:  out,
			},
		}
	}

	records := []gen.AIStatsRecord{
		mk("a1", "ws-A", "openai", "gpt-4o", 100, false),
		mk("a2", "ws-A", "openai", "gpt-4o", 200, true),
		mk("b1", "ws-B", "anthropic", "claude", 50, false),
	}
	for _, r := range records {
		if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: r}); err != nil {
			t.Fatalf("handleRecord %s: %v", r.ID, err)
		}
	}

	// Global query (empty WorkspaceID) sees all three records.
	resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace"})
	if err != nil {
		t.Fatalf("global query: %v", err)
	}
	if resp.Total != 3 {
		t.Fatalf("global total: got %d, want 3", resp.Total)
	}
	// Cross-workspace counters sum all output tokens.
	wantOut := int64(100 + 200 + 50)
	if resp.Counters.OutputTokens != wantOut {
		t.Fatalf("global output tokens: got %d, want %d", resp.Counters.OutputTokens, wantOut)
	}

	// Per-workspace query (ws-A) sees only ws-A's two records.
	respA, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace", WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("ws-A query: %v", err)
	}
	if respA.Total != 2 {
		t.Fatalf("ws-A total: got %d, want 2", respA.Total)
	}
	if respA.Counters.OutputTokens != 300 {
		t.Fatalf("ws-A output tokens: got %d, want 300", respA.Counters.OutputTokens)
	}

	// Per-workspace query (ws-B) sees only ws-B's record.
	respB, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace", WorkspaceID: "ws-B"})
	if err != nil {
		t.Fatalf("ws-B query: %v", err)
	}
	if respB.Total != 1 {
		t.Fatalf("ws-B total: got %d, want 1", respB.Total)
	}
	if respB.Counters.OutputTokens != 50 {
		t.Fatalf("ws-B output tokens: got %d, want 50", respB.Counters.OutputTokens)
	}

	// Global aggregates (empty WorkspaceID) cross-workspace model totals.
	agg, err := a.handleAggregates(nil, gen.AIStatsAggregatesReq{})
	if err != nil {
		t.Fatalf("global aggregates: %v", err)
	}
	if len(agg.Models) != 2 {
		t.Fatalf("global models: got %d, want 2", len(agg.Models))
	}

	// Per-workspace aggregates (ws-A) only ws-A models.
	aggA, err := a.handleAggregates(nil, gen.AIStatsAggregatesReq{WorkspaceID: "ws-A"})
	if err != nil {
		t.Fatalf("ws-A aggregates: %v", err)
	}
	if len(aggA.Models) != 1 || aggA.Models[0].Provider != "openai" {
		t.Fatalf("ws-A models: got %+v, want 1 openai", aggA.Models)
	}
}

// TestGlobalActor_WorkspaceMapTotals verifies the in-memory workspace map tracks
// per-workspace totals (used by scope=workspace counters).
func TestGlobalActor_WorkspaceMapTotals(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: gen.AIStatsRecord{
		ID: "x1", WorkspaceID: "ws-1", Provider: "p", Model: "m", CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Usage: &gen.UsageData{OutputTokens: 7, TotalTokens: 7},
	}}); err != nil {
		t.Fatalf("handleRecord: %v", err)
	}
	if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: gen.AIStatsRecord{
		ID: "x2", WorkspaceID: "ws-2", Provider: "p", Model: "m", CompletedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Usage: &gen.UsageData{OutputTokens: 3, TotalTokens: 3},
	}}); err != nil {
		t.Fatalf("handleRecord: %v", err)
	}

	if got := a.counters.workspaceTotals("").OutputTokens; got != 10 {
		t.Fatalf("global workspace totals: got %d, want 10", got)
	}
	if got := a.counters.workspaceTotals("ws-1").OutputTokens; got != 7 {
		t.Fatalf("ws-1 totals: got %d, want 7", got)
	}
	if got := a.counters.workspaceTotals("ws-2").OutputTokens; got != 3 {
		t.Fatalf("ws-2 totals: got %d, want 3", got)
	}
}

// TestGlobalActor_ConcurrentHandlers exercises the stateless (PureContext)
// handler surface the way the runtime does: record/query/export run on forked
// goroutines with no mailbox serialization, concurrently with the event-path
// processRecord and runtime Save. Run under -race this pins the lock
// discipline (single a.mu critical sections, lock-free disk scans) and the
// atomic record write (a scanner never observes a partially written file).
func TestGlobalActor_ConcurrentHandlers(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	const writerGoros = 8      // invoke path (handleRecord)
	const eventGoros = 4       // event path (processRecord)
	const perGoro = 25
	total := (writerGoros + eventGoros) * perGoro

	base := time.Now().UTC().Add(-time.Hour)
	rec := func(i int) gen.AIStatsRecord {
		return gen.AIStatsRecord{
			ID:          fmt.Sprintf("r%04d", i),
			WorkspaceID: "ws-" + strconv.Itoa(i%4),
			Provider:    "openai",
			Model:       "gpt-4o",
			CompletedAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			Usage:       &gen.UsageData{OutputTokens: 1, TotalTokens: 1},
		}
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, total+64)

	for g := 0; g < writerGoros; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < perGoro; i++ {
				idx := g*perGoro + i
				if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: rec(idx)}); err != nil {
					errCh <- fmt.Errorf("handleRecord %d: %w", idx, err)
				}
			}
		}(g)
	}
	for g := 0; g < eventGoros; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < perGoro; i++ {
				idx := writerGoros*perGoro + g*perGoro + i
				a.processRecord(rec(idx))
			}
		}(g)
	}

	// Readers race the writers until they finish. Results are
	// nondeterministic mid-flight; only errors fail the test. A reader that
	// hits an error stops so error sends stay bounded.
	stop := make(chan struct{})
	reader := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := fn(); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	reader(func() error {
		_, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace"})
		return err
	})
	reader(func() error {
		_, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace", WorkspaceID: "ws-1"})
		return err
	})
	reader(func() error {
		_, err := a.handleExport(nil, gen.AIStatsExportReq{Format: "jsonl"})
		return err
	})
	reader(func() error {
		return a.Save()
	})

	close(start)
	// wg tracks both writers and readers, so wait for writers by polling
	// until all records are queryable, then stop the readers.
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace"})
			if err == nil && resp.Total == int64(total) {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		close(stop)
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// Final consistency: every record persisted exactly once and applied to
	// the in-memory counters exactly once.
	resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace"})
	if err != nil {
		t.Fatalf("final query: %v", err)
	}
	if resp.Total != int64(total) {
		t.Fatalf("final total: got %d, want %d", resp.Total, total)
	}
	if resp.Counters.RequestCount != int64(total) {
		t.Fatalf("final counters: got %d, want %d", resp.Counters.RequestCount, total)
	}
}

// TestConcurrentProcessAndCleanup exercises the write path (processRecord,
// SaveAll batch) concurrently with handleCleanup (delete + rebuild under the
// same write lock) on the goleveldb backend. Run under -race this verifies
// no data race between the SaveAll batch path and the cleanup rebuild path.
func TestConcurrentProcessAndCleanup(t *testing.T) {
	store, scanner, root := setupTestStore(t)
	a := &Actor{root: root, store: store, scanner: scanner, counters: newCounters(), costs: newCostIndex(store)}

	const writers = 4
	const perWriter = 20
	total := writers * perWriter

	base := time.Now().UTC().Add(-time.Hour)
	rec := func(i int) gen.AIStatsRecord {
		return gen.AIStatsRecord{
			ID:          fmt.Sprintf("c%04d", i),
			WorkspaceID: "ws-" + fmt.Sprintf("%d", i%2),
			Provider:    "openai",
			Model:       "gpt-4o",
			CompletedAt: base.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			Usage:       &gen.UsageData{OutputTokens: 1, TotalTokens: 1},
		}
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, total+64)

	// Writers.
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for i := 0; i < perWriter; i++ {
				idx := g*perWriter + i
				if err := a.handleRecord(nil, gen.AIStatsRecordReq{Record: rec(idx)}); err != nil {
					errCh <- fmt.Errorf("handleRecord %d: %w", idx, err)
				}
			}
		}(g)
	}

	// Cleanup runner: runs cleanup repeatedly during the write storm.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10; i++ {
			if _, err := a.handleCleanup(nil, AIStatsCleanupReq{DryRun: false}); err != nil {
				errCh <- fmt.Errorf("handleCleanup: %w", err)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// All records must be present and counters consistent.
	resp, err := a.handleQuery(nil, gen.AIStatsQueryReq{Scope: "workspace"})
	if err != nil {
		t.Fatalf("final query: %v", err)
	}
	if resp.Total != int64(total) {
		t.Fatalf("final total: got %d, want %d", resp.Total, total)
	}
	if resp.Counters.RequestCount != int64(total) {
		t.Fatalf("final counters: got %d, want %d", resp.Counters.RequestCount, total)
	}

	// Cleanup with no ghosts is a no-op.
	clean, err := a.handleCleanup(nil, AIStatsCleanupReq{DryRun: false})
	if err != nil {
		t.Fatalf("post-write cleanup: %v", err)
	}
	if clean.Found != 0 {
		t.Fatalf("post-write cleanup found ghosts: %d", clean.Found)
	}
}
// TestMigrateLegacyWorkspaces verifies per-workspace record files and
// checkpoints are folded into the single global namespace on startup, then
// copied into the persist store by migrateFSToDB.
func TestMigrateLegacyWorkspaces(t *testing.T) {
	globalRoot := filepath.Join(t.TempDir(), "aistats")

	// Simulate legacy layout: <parent>/ws-legacy/records/2026-01/<id>.json
	legacyRoot := filepath.Join(filepath.Dir(globalRoot), "ws-legacy")
	if err := writeRecordFS(legacyRoot, gen.AIStatsRecord{
		ID: "old-1", WorkspaceID: "ws-legacy", Provider: "p", Model: "m",
		CompletedAt: "2026-01-15T10:00:00Z",
		Usage:       &gen.UsageData{OutputTokens: 5, TotalTokens: 5},
	}); err != nil {
		t.Fatalf("writeRecordFS legacy: %v", err)
	}
	// Legacy checkpoint.
	if err := writeCheckpointFS(legacyRoot, &checkpoint{
		Workspace: gen.AIStatsCounters{RequestCount: 1, OutputTokens: 5},
		WorkspaceMap: map[string]gen.AIStatsCounters{
			"ws-legacy": {RequestCount: 1, OutputTokens: 5},
		},
		Session:  map[string]gen.AIStatsCounters{},
		Project:  map[string]gen.AIStatsCounters{},
		Provider: map[string]gen.AIStatsCounters{},
		Model:    map[string]gen.AIStatsCounters{},
	}); err != nil {
		t.Fatalf("writeCheckpointFS legacy: %v", err)
	}

	moved, failed, err := migrateLegacyWorkspaces(globalRoot)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if failed != 0 {
		t.Fatalf("unexpected failed dirs: %d", failed)
	}
	if moved != 1 {
		t.Fatalf("moved: got %d, want 1", moved)
	}

	// The migrated fs data is then copied into the goleveldb store.
	store, scanner, _ := setupTestStore(t)
	if _, err := migrateFSToDB(globalRoot, store); err != nil {
		t.Fatalf("migrateFSToDB: %v", err)
	}

	// The migrated record is now queryable from the store.
	recs, err := loadRecords(store, scanner, time.Time{}, time.Time{}, "workspace", "", "")
	if err != nil {
		t.Fatalf("loadRecords store: %v", err)
	}
	if len(recs) != 1 || recs[0].ID != "old-1" {
		t.Fatalf("migrated records: got %+v", recs)
	}

	// The merged checkpoint carries the legacy aggregate.
	cp, err := loadCheckpoint(store)
	if err != nil {
		t.Fatalf("loadCheckpoint store: %v", err)
	}
	if cp.workspace.OutputTokens != 5 {
		t.Fatalf("merged checkpoint output: got %d, want 5", cp.workspace.OutputTokens)
	}
	if cp.workspaceMap["ws-legacy"].OutputTokens != 5 {
		t.Fatalf("merged checkpoint workspace map: got %+v", cp.workspaceMap)
	}
}

// TestMigrateFSToDB_Idempotent verifies migrateFSToDB can be re-run safely:
// running it twice produces exactly one copy of each record, no duplicates,
// and a second run is a no-op after the migrated marker is present.
func TestMigrateFSToDB_Idempotent(t *testing.T) {
	globalRoot := filepath.Join(t.TempDir(), "aistats")

	recs := []gen.AIStatsRecord{
		{ID: "m1", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			CompletedAt: "2026-02-01T10:00:00Z",
			Usage:       &gen.UsageData{OutputTokens: 10, TotalTokens: 10}},
		{ID: "m2", WorkspaceID: "ws-1", Provider: "openai", Model: "gpt-4o",
			CompletedAt: "2026-02-02T10:00:00Z",
			Usage:       &gen.UsageData{OutputTokens: 20, TotalTokens: 20}},
		{ID: "m3", WorkspaceID: "ws-2", Provider: "anthropic", Model: "claude",
			CompletedAt: "2026-03-01T10:00:00Z",
			Usage:       &gen.UsageData{OutputTokens: 30, TotalTokens: 30}},
	}
	for _, r := range recs {
		if err := writeRecordFS(globalRoot, r); err != nil {
			t.Fatalf("writeRecordFS %s: %v", r.ID, err)
		}
	}
	if err := writeCheckpointFS(globalRoot, &checkpoint{
		Workspace: gen.AIStatsCounters{RequestCount: 3, OutputTokens: 60},
		WorkspaceMap: map[string]gen.AIStatsCounters{
			"ws-1": {RequestCount: 2, OutputTokens: 30},
			"ws-2": {RequestCount: 1, OutputTokens: 30},
		},
		Session:  map[string]gen.AIStatsCounters{},
		Project:  map[string]gen.AIStatsCounters{},
		Provider: map[string]gen.AIStatsCounters{},
		Model:    map[string]gen.AIStatsCounters{},
	}); err != nil {
		t.Fatalf("writeCheckpointFS: %v", err)
	}

	store, scanner, _ := setupTestStore(t)

	// First migration copies everything.
	if _, err := migrateFSToDB(globalRoot, store); err != nil {
		t.Fatalf("first migrateFSToDB: %v", err)
	}
	recs1, err := loadRecords(store, scanner, time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("loadRecords after first migrate: %v", err)
	}
	if len(recs1) != 3 {
		t.Fatalf("after first migrate: got %d records, want 3", len(recs1))
	}

	// Second migration is a re-entrant no-op: same record count, no dupes.
	if _, err := migrateFSToDB(globalRoot, store); err != nil {
		t.Fatalf("second migrateFSToDB: %v", err)
	}
	recs2, err := loadRecords(store, scanner, time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("loadRecords after second migrate: %v", err)
	}
	if len(recs2) != 3 {
		t.Fatalf("after second migrate: got %d records, want 3 (idempotent)", len(recs2))
	}
	seen := map[string]bool{}
	for _, r := range recs2 {
		if seen[r.ID] {
			t.Fatalf("duplicate record %s after second migrate", r.ID)
		}
		seen[r.ID] = true
	}

	// New records added after migration are NOT picked up: the migrated
	// marker makes subsequent runs a no-op.
	if err := writeRecordFS(globalRoot, gen.AIStatsRecord{
		ID: "m4", WorkspaceID: "ws-2", Provider: "anthropic", Model: "claude",
		CompletedAt: "2026-04-01T10:00:00Z",
		Usage:       &gen.UsageData{OutputTokens: 40, TotalTokens: 40},
	}); err != nil {
		t.Fatalf("writeRecordFS m4: %v", err)
	}
	if _, err := migrateFSToDB(globalRoot, store); err != nil {
		t.Fatalf("third migrateFSToDB: %v", err)
	}
	recs3, err := loadRecords(store, scanner, time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("loadRecords after third migrate: %v", err)
	}
	if len(recs3) != 3 {
		t.Fatalf("after third migrate: got %d records, want 3 (no-op after marker)", len(recs3))
	}

	// Simulate an interrupted first run: remove the marker, re-run.
	// The migration wipes the partial store state and re-copies everything
	// from the fs source (which now includes m4).
	if err := store.Delete("migrated"); err != nil {
		t.Fatalf("delete migrated marker: %v", err)
	}
	if _, err := migrateFSToDB(globalRoot, store); err != nil {
		t.Fatalf("fourth migrateFSToDB: %v", err)
	}
	recs4, err := loadRecords(store, scanner, time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		t.Fatalf("loadRecords after fourth migrate: %v", err)
	}
	if len(recs4) != 4 {
		t.Fatalf("after re-run without marker: got %d records, want 4", len(recs4))
	}
	seen4 := map[string]bool{}
	for _, r := range recs4 {
		if seen4[r.ID] {
			t.Fatalf("duplicate record %s after re-run", r.ID)
		}
		seen4[r.ID] = true
	}
}
