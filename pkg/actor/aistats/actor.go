package aistats

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Compile-time assertion that Actor implements persist.Persistent, required by
// the actorset's RequirePersistent flag so the runtime preserves the actor's
// canonical ID across restarts.
var _ persist.Persistent = (*Actor)(nil)

// Save persists the aggregate checkpoint to the persist store. Called by the
// runtime on shutdown.
//
// The actor write lock (not RLock) is held for the whole write so the counters
// snapshot serialized by saveCheckpoint is consistent. On goleveldb the
// checkpoint is written as a single Put in the DB; on fs it is an atomic
// file write (WriteFileAtomic: temp file + fsync + rename).
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return saveCheckpoint(a.store, a.counters)
}

// Load restores the aggregate checkpoint from the persist store. Called by the
// runtime on startup; OnInit also calls loadCheckpoint so this is a safe no-op
// when the checkpoint was already loaded.
func (a *Actor) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp, err := loadCheckpoint(a.store)
	if err != nil {
		return err
	}
	if cp != nil {
		a.counters = cp
	}
	return nil
}

// hotRecordsCap bounds the in-memory hot cache. The hot cache holds the most
// recent records written through this actor and serves recent queries without
// a disk scan.
const hotRecordsCap = 5000

// Actor is the durable owner of LLM request telemetry for the whole system.
// It is a single global system actor (canonical service name "aistats") that
// aggregates records from every workspace; per-workspace views are produced by
// filtering on the record's WorkspaceID field rather than by spawning one actor
// per workspace.
type Actor struct {
	actor.Host
	mu             sync.RWMutex
	actorID        string
	root           string                // fs root for migration source + legacy compat
	store          persist.Persist       // persist backend (fs or goleveldb)
	scanner        persist.PrefixScanner // nil when backend lacks PrefixScanner
	counters       *counters
	hotRecords     hotCache
	costs          *costIndex
	cancelEventSub func()
	rollupCtx      actor.Context
	rollupCancel   context.CancelFunc
	rollupDone     chan struct{}
}

// NewActor returns an actor constructor for the global aistats actor.
func NewActor() func() actor.Actor {
	return func() actor.Actor {
		return &Actor{}
	}
}

func (a *Actor) OnInit(ctx actor.Context) error {
	a.actorID = ctx.Self().ID().String()
	a.root = filepath.Join(config.ActorDataDir(), "aistats")

	// Create the persist store from the scoped backend. config.PersistConfig
	// fills DataDir/Endpoint/etc.; we override Backend with the scoped value
	// so aistats uses goleveldb when backend_aistats is set, without
	// affecting the global backend.
	pc := config.PersistConfig("aistats")
	pc.Backend = persist.BackendType(config.ScopedBackend("aistats"))
	store, err := persist.New(pc)
	if err != nil {
		return fmt.Errorf("aistats: persist init: %w", err)
	}
	a.store = store
	if s, ok := store.(persist.PrefixScanner); ok {
		a.scanner = s
	}

	a.counters = newCounters()
	a.costs = newCostIndex(a.store)

	// Best-effort migration of legacy per-workspace data into the single
	// global filesystem namespace. Once migrated the per-workspace
	// subdirectories are empty; this is a no-op when there is nothing to
	// migrate. This runs on the raw filesystem before the persist store is
	// used, consolidating per-workspace dirs into the global root.
	if moved, failed, err := migrateLegacyWorkspaces(a.root); err != nil {
		ctx.Logger().Warn("aistats: legacy migration failed", "error", err)
	} else if moved > 0 {
		ctx.Logger().Info("aistats: migrated legacy per-workspace records", "moved", moved, "failed", failed)
	}

	// Migrate from the legacy filesystem layout into the persist store
	// (goleveldb). Idempotent — a "migrated" marker gates re-entry. On fs
	// backend this is effectively a no-op (data is already in the right
	// format). On goleveldb backend this copies all fs records, checkpoint,
	// and cost rates into the DB.
	if moved, err := migrateFSToDB(a.root, a.store); err != nil {
		ctx.Logger().Warn("aistats: fs→db migration failed", "error", err)
	} else if moved > 0 {
		ctx.Logger().Info("aistats: migrated records to persist store", "moved", moved)
	}

	if err := a.costs.load(); err != nil {
		ctx.Logger().Error("aistats: load cost index failed", "error", err)
	}

	// Load checkpoint from the persist store. Cold records stay in the DB
	// and are never bulk-scanned at startup.
	cp, err := loadCheckpoint(a.store)
	if err != nil {
		ctx.Logger().Error("aistats: load checkpoint failed", "error", err)
	} else if cp != nil {
		a.counters = cp
	}

	// Hot/cold separation: do not scan the full record history at startup.
	// Rebuilding counters from every historical record can hang startup on
	// large datasets. Cold records remain queryable from the DB via the query
	// and series cold paths; the in-memory aggregates and the hot cache cover
	// records written after startup. Aggregates may be incomplete until new
	// records arrive (or a checkpoint from a previous run is loaded).
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("aistats: starting global actor", "id", a.actorID)

	if err := ctx.Register("aistats.record", a.handleRecord, actor.Internal()); err != nil {
		return fmt.Errorf("aistats: register record: %w", err)
	}
	if err := ctx.Register("aistats.query", a.handleQuery, actor.Public()); err != nil {
		return fmt.Errorf("aistats: register query: %w", err)
	}
	if err := ctx.Register("aistats.aggregates", a.handleAggregates, actor.Public()); err != nil {
		return fmt.Errorf("aistats: register aggregates: %w", err)
	}
	if err := ctx.Register("aistats.series", a.handleSeries, actor.Public()); err != nil {
		return fmt.Errorf("aistats: register series: %w", err)
	}
	if err := ctx.Register("aistats.cost_configure", a.handleCostConfigure, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aistats: register cost.configure: %w", err)
	}
	if err := ctx.Register("aistats.cost_list", a.handleCostList, actor.Public()); err != nil {
		return fmt.Errorf("aistats: register cost.list: %w", err)
	}
	if err := ctx.Register("aistats.export", a.handleExport, actor.Public()); err != nil {
		return fmt.Errorf("aistats: register export: %w", err)
	}
	if err := ctx.Register("aistats.backfill", a.handleBackfill, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aistats: register backfill: %w", err)
	}
	if err := ctx.Register("aistats.cleanup", a.handleCleanup, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aistats: register cleanup: %w", err)
	}
	// Rollup lane: per-day / per-month retention convolution scans cold
	// records for seconds at a time and must never occupy the owner lane
	// (Owner Lane 禁阻塞 red line). The startup catch-up and the 1h ticker
	// both reach it as fire-and-forget self-invokes of aistats.rollup.
	if err := ctx.RegisterLoop("aistats_rollup", actor.ModeStateful); err != nil {
		return fmt.Errorf("aistats: register rollup loop: %w", err)
	}
	if err := ctx.Register("aistats.rollup", a.handleRollup, actor.AdminOnly(), actor.WithLoop("aistats_rollup"), actor.WithDescription("Run one aistats retention rollup round: roll raw records older than aistats_raw_days into daily documents, and complete daily months older than aistats_daily_days into permanent monthly documents. dryRun reports without writing or deleting; onlyDay forces a single YYYY-MM-DD day.")); err != nil {
		return fmt.Errorf("aistats: register rollup: %w", err)
	}
	if err := ctx.RegisterDomain("aistats").Expose(); err != nil {
		return fmt.Errorf("aistats: expose service: %w", err)
	}

	// Subscribe to aistats.record events emitted by aggregators. The event
	// path replaces the invoke path for aggregator→aistats record flow:
	// no PendingTable slot, no owner-loop serialization, drop-oldest
	// backpressure instead of caller goroutines piling up.
	cancelSub, err := ctx.SubscribeEventKind("aistats.record", func(env actor.EventEnvelope) {
		record, ok := env.Payload.(gen.AIStatsRecord)
		if !ok {
			return
		}
		a.processRecord(record)
	})
	if err != nil {
		return fmt.Errorf("aistats: subscribe aistats.record: %w", err)
	}
	a.cancelEventSub = cancelSub

	// Background rollup catch-up (startup once + 1h ticker), driven as
	// self-invokes on the aistats_rollup loop.
	a.startRollupLoop(ctx)
	return nil
}

func (a *Actor) OnStop(_ actor.Context) error {
	if a.cancelEventSub != nil {
		a.cancelEventSub()
	}
	// Stop the rollup ticker before touching the store: a rollup window
	// mid-flight must not outlive the persist store close below.
	a.stopRollupLoop()
	a.mu.Lock()
	saveErr := a.saveLocked()
	a.mu.Unlock()
	// Close the persist store after releasing the actor lock (red line #4:
	// no leveldb Open/Close under actor lock). LevelDBPersist.Close releases
	// the refcount; FSPersist has no Close (no-op).
	if c, ok := a.store.(interface{ Close() error }); ok {
		_ = c.Close()
	}
	return saveErr
}

func (a *Actor) saveLocked() error {
	return saveCheckpoint(a.store, a.counters)
}

func (a *Actor) handleRecord(ctx actor.PureContext, req gen.AIStatsRecordReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.processRecordLocked(ctx, req.Record)
}

// processRecord is the event-path entry: it receives a record from the
// aistats.record event subscription and applies the same normalization
// and persistence as the invoke path. Runs on the subscription drain
// goroutine (no actor.PureContext).
func (a *Actor) processRecord(r gen.AIStatsRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.processRecordLocked(nil, r); err != nil {
		// Event path has no caller to return the error to; log only.
		slog.Debug("aistats: process event record failed", "error", err, "id", r.ID)
	}
}

// processRecordLocked normalizes, prices, persists, and applies a record.
// Callers must hold a.mu. ctx may be nil (event path) — logging degrades
// to slog.
func (a *Actor) processRecordLocked(ctx actor.PureContext, r gen.AIStatsRecord) error {
	if r.ID == "" {
		return fmt.Errorf("aistats.record: record id is required")
	}
	if r.WorkspaceID == "" {
		return fmt.Errorf("aistats.record: record workspaceID is required")
	}
	if r.CompletedAt == "" {
		r.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}

	// Compute cost if usage is present but cost is not.
	if r.Usage != nil && r.Usage.CostTotal == 0 {
		usage := llmclient.Usage{
			InputTokens:              int(r.Usage.InputTokens),
			OutputTokens:             int(r.Usage.OutputTokens),
			TotalTokens:              int(r.Usage.TotalTokens),
			CacheCreationInputTokens: int(r.Usage.CacheCreationInputTokens),
			CacheReadInputTokens:     int(r.Usage.CacheReadInputTokens),
			ReasoningTokens:          int(r.Usage.ReasoningTokens),
		}
		c := a.costs.costForUsage(r.Provider, r.Model, usage)
		r.Usage.CostInput = c.Input
		r.Usage.CostOutput = c.Output
		r.Usage.CostCacheRead = c.CacheRead
		r.Usage.CostCacheWrite = c.CacheWrite
		r.Usage.CostTotal = c.Total
	}

	// SaveAll: record + month marker + checkpoint in a single atomic batch.
	// On goleveldb this is one leveldb.Write call (all three land together
	// or none); on fs it degrades to three sequential atomic file writes.
	// The checkpoint snapshot is taken AFTER applying r to a.counters so
	// the persisted checkpoint includes the new record — if SaveAll
	// succeeds, record and checkpoint are consistent. If it fails, neither
	// the record nor the updated checkpoint is written (same failure
	// semantics as the old appendRecord + saveLocked pair, but with stronger
	// atomicity when the backend implements Saver).
	a.counters.apply(r)
	month := recordMonth(r)
	if err := persist.SaveAll(a.store, []persist.Doc{
		{Name: "records/" + month + "/" + r.ID, Value: r},
		{Name: "months/" + month, Value: true},
		{Name: "checkpoint", Value: a.counters.snapshot()},
	}); err != nil {
		if ctx != nil {
			ctx.Logger().Error("aistats: saveAll record failed", "error", err, "id", r.ID)
		} else {
			slog.Error("aistats: saveAll record failed", "error", err, "id", r.ID)
		}
		return err
	}

	// Keep the most recent records in memory so recent queries avoid a DB
	// scan. r is used, not req.Record, so the cached copy carries the
	// normalized WorkspaceID, CompletedAt and cost fields that were written.
	a.hotRecords.push(r)
	return nil
}

func (a *Actor) handleQuery(ctx actor.PureContext, req gen.AIStatsQueryReq) (gen.AIStatsQueryResp, error) {
	return a.queryAt(time.Now().UTC(), req)
}

// queryAt is handleQuery with an injected clock. Counters are resolved
// after records are loaded: a per-workspace query recomputes from the
// filtered records so the totals match exactly, while a global query uses
// the fast in-memory aggregate.
//
// Merged read path (设计 §4–§5): raw records whose day is owned by a rollup
// document are excluded (per-day read-source criterion), and the
// per-workspace counters additionally merge the dimension-matched counters
// of every in-range rollup document — old data outside the raw window stays
// answerable and no day is counted twice (I2). Total counts raw record rows
// only: coarse segments have no per-request rows (显式声明语义).
func (a *Actor) queryAt(now time.Time, req gen.AIStatsQueryReq) (gen.AIStatsQueryResp, error) {
	// Snapshot the hot cache quickly, then release the lock so a slow disk scan
	// never blocks handleRecord's write lock.
	a.mu.RLock()
	hot := a.hotRecords.snapshot()
	a.mu.RUnlock()

	since, _ := time.Parse(time.RFC3339Nano, req.Since)
	until, _ := time.Parse(time.RFC3339Nano, req.Until)

	rr, err := a.planRollupRead(since, until, now)
	if err != nil {
		return gen.AIStatsQueryResp{}, err
	}

	var records []gen.AIStatsRecord

	if canServeFromHot(hot, since, until) {
		records = filterHotRecords(hot, since, until, req.Scope, req.ScopeID, req.WorkspaceID)
	} else {
		var err error
		records, err = loadRecords(a.store, a.scanner, since, until, req.Scope, req.ScopeID, req.WorkspaceID)
		if err != nil {
			return gen.AIStatsQueryResp{}, err
		}
	}
	records = excludeRollupOwnedRecords(records, rr)
	// Total counts raw record rows only (显式声明语义): coarse segments
	// have no per-request rows, and doc-owned raw records are excluded.
	total := int64(len(records))

	var counters gen.AIStatsCounters
	if req.WorkspaceID != "" {
		// Per-workspace: recompute from the matching records for accuracy
		// across every scope (the in-memory model/provider maps are
		// cross-workspace and cannot answer a single-workspace total),
		// plus the rollup documents' dimension-matched counters so data
		// older than the raw window stays answerable (设计 §4).
		counters = addCounters(sumCounters(records), rr.matchedRollupCounters(req.Scope, req.ScopeID, req.WorkspaceID))
	} else {
		a.mu.RLock()
		counters = a.counters.query(req.Scope, req.ScopeID)
		a.mu.RUnlock()
	}

	sortRecords(records, req.Order)
	records = sliceRecords(records, req.Limit, req.Offset)

	return gen.AIStatsQueryResp{
		Records:  records,
		Counters: counters,
		Total:    total,
	}, nil
}

// sumCounters aggregates the per-record counters for a slice of records.
func sumCounters(records []gen.AIStatsRecord) gen.AIStatsCounters {
	var c gen.AIStatsCounters
	for _, r := range records {
		c = addCounters(c, recordToCounters(r))
	}
	return c
}

// canServeFromHot reports whether a query/series range is fully covered by the
// hot cache. The hot cache only holds the most recent records, so a range with
// no since bound, a future until, or a since older than the oldest hot record
// must fall back to disk. chunks is a hotCache snapshot (oldest-first); the
// oldest record is the first record of the first chunk.
func canServeFromHot(chunks [][]gen.AIStatsRecord, since, until time.Time) bool {
	if len(chunks) == 0 {
		return false
	}
	if !until.IsZero() {
		// Future queries are unlikely; just use disk for simplicity.
		return false
	}
	oldest, err := time.Parse(time.RFC3339Nano, chunks[0][0].CompletedAt)
	if err != nil || oldest.IsZero() {
		return false
	}
	// Hot cache can serve if the requested range starts at or after the oldest hot record.
	return !since.IsZero() && !since.Before(oldest)
}

func filterHotRecords(chunks [][]gen.AIStatsRecord, since, until time.Time, scope, scopeID, workspaceID string) []gen.AIStatsRecord {
	size := 0
	for _, chunk := range chunks {
		size += len(chunk)
	}
	filtered := make([]gen.AIStatsRecord, 0, size)
	for _, chunk := range chunks {
		for _, r := range chunk {
			if !matchRecord(r, scope, scopeID, workspaceID) {
				continue
			}
			if !recordInTimeRange(r, since, until) {
				continue
			}
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func (a *Actor) handleAggregates(_ actor.PureContext, req gen.AIStatsAggregatesReq) (gen.AIStatsAggregatesResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if req.WorkspaceID != "" {
		return a.perWorkspaceAggregatesLocked(req.WorkspaceID), nil
	}

	resp := gen.AIStatsAggregatesResp{Workspace: a.counters.workspace}
	for k, v := range a.counters.model {
		parts := strings.SplitN(k, "/", 2)
		if len(parts) != 2 {
			continue
		}
		resp.Models = append(resp.Models, gen.AIStatsModelAggregate{
			Provider: parts[0],
			Model:    parts[1],
			Counters: v,
		})
	}
	for k, v := range a.counters.provider {
		resp.Providers = append(resp.Providers, gen.AIStatsProviderAggregate{
			Provider: k,
			Counters: v,
		})
	}
	resp.Models, resp.Providers = orderAggregates(resp.Models, resp.Providers)
	return resp, nil
}

// perWorkspaceAggregatesLocked builds a single-workspace breakdown from the hot
// cache. The hot cache holds the most recent records, which is what a workspace
// dashboard renders; the global in-memory model/provider maps are
// cross-workspace and cannot answer a single-workspace view. Caller must hold
// a.mu (at least RLock).
func (a *Actor) perWorkspaceAggregatesLocked(workspaceID string) gen.AIStatsAggregatesResp {
	modelAgg := make(map[string]gen.AIStatsCounters)
	providerAgg := make(map[string]gen.AIStatsCounters)
	var ws gen.AIStatsCounters
	for _, chunk := range a.hotRecords.chunks {
		for _, r := range chunk {
			if r.WorkspaceID != workspaceID {
				continue
			}
			delta := recordToCounters(r)
			ws = addCounters(ws, delta)
			if r.Provider != "" {
				providerAgg[r.Provider] = addCounters(providerAgg[r.Provider], delta)
			}
			if r.Provider != "" && r.Model != "" {
				key := r.Provider + "/" + r.Model
				modelAgg[key] = addCounters(modelAgg[key], delta)
			}
		}
	}
	resp := gen.AIStatsAggregatesResp{Workspace: ws}
	for k, v := range modelAgg {
		parts := strings.SplitN(k, "/", 2)
		if len(parts) != 2 {
			continue
		}
		resp.Models = append(resp.Models, gen.AIStatsModelAggregate{Provider: parts[0], Model: parts[1], Counters: v})
	}
	for k, v := range providerAgg {
		resp.Providers = append(resp.Providers, gen.AIStatsProviderAggregate{Provider: k, Counters: v})
	}
	resp.Models, resp.Providers = orderAggregates(resp.Models, resp.Providers)
	return resp
}

// orderAggregates sorts model and provider aggregates by cost then request count
// and returns the sorted slices.
func orderAggregates(models []gen.AIStatsModelAggregate, providers []gen.AIStatsProviderAggregate) ([]gen.AIStatsModelAggregate, []gen.AIStatsProviderAggregate) {
	sort.Slice(models, func(i, j int) bool {
		if models[i].Counters.CostTotal != models[j].Counters.CostTotal {
			return models[i].Counters.CostTotal > models[j].Counters.CostTotal
		}
		return models[i].Counters.RequestCount > models[j].Counters.RequestCount
	})
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Counters.RequestCount > providers[j].Counters.RequestCount
	})
	return models, providers
}

func (a *Actor) handleCostConfigure(_ actor.PureContext, req gen.AIStatsCostConfigureReq) (gen.AIStatsCostConfigureResp, error) {
	version, err := a.costs.configure(req.Rate)
	if err != nil {
		return gen.AIStatsCostConfigureResp{}, err
	}
	return gen.AIStatsCostConfigureResp{Version: version}, nil
}

func (a *Actor) handleCostList(_ actor.PureContext, req gen.AIStatsCostListReq) (gen.AIStatsCostListResp, error) {
	return gen.AIStatsCostListResp{Rates: a.costs.list(req.Provider, req.Model)}, nil
}

func (a *Actor) handleExport(_ actor.PureContext, req gen.AIStatsExportReq) (gen.AIStatsExportResp, error) {
	return a.exportAt(time.Now().UTC(), req)
}

// exportAt is handleExport with an injected clock.
//
// Semantic change (设计 §4, 显式声明): the coarse segment no longer exports
// per-request rows — the rollup documents ARE the only surviving form of
// that data. jsonl emits one {"rollup":true,...} line per in-range rollup
// document; csv emits one `rollup,<period>,<granularity>,<counters...>` row.
// Callers distinguish the two line kinds by the rollup marker. The raw
// segment keeps the exact current formats (per-request rows, byte for byte).
// Ordering stays time-ascending overall: rollup lines first (they cover the
// oldest span), then raw record rows.
func (a *Actor) exportAt(now time.Time, req gen.AIStatsExportReq) (gen.AIStatsExportResp, error) {
	since, _ := time.Parse(time.RFC3339Nano, req.Since)
	until, _ := time.Parse(time.RFC3339Nano, req.Until)

	rr, err := a.planRollupRead(since, until, now)
	if err != nil {
		return gen.AIStatsExportResp{}, err
	}

	records, err := loadRecords(a.store, a.scanner, since, until, req.Scope, req.ScopeID, req.WorkspaceID)
	if err != nil {
		return gen.AIStatsExportResp{}, err
	}
	records = excludeRollupOwnedRecords(records, rr)
	sortRecords(records, "asc") // loadRecords no longer sorts; exports stay ascending.

	// In-range rollup documents with records matching this export's
	// dimensions. Empty documents (Requests == 0 — the rollup pass writes
	// a document for every rollable day) carry no data and are skipped.
	// Filtered exports additionally require a non-zero dimension match
	// (I5: an L2 month contributes nothing to a session-scope export).
	filtered := req.WorkspaceID != "" || req.ScopeID != ""
	var rollups []*rollupDoc
	for _, doc := range append(append([]*rollupDoc{}, rr.daily...), rr.monthly...) {
		if doc.Requests == 0 {
			continue
		}
		if filtered && rollupCountersMatch(doc, req.Scope, req.ScopeID, req.WorkspaceID).RequestCount == 0 {
			continue
		}
		rollups = append(rollups, doc)
	}
	// Chronological overall: L1 day docs and L2 month docs interleave by
	// period string (字典序 == 时间序 for both shapes).
	sort.Slice(rollups, func(i, j int) bool { return rollups[i].Period < rollups[j].Period })

	format := strings.ToLower(req.Format)
	if format == "" {
		format = "jsonl"
	}
	var sb strings.Builder
	switch format {
	case "jsonl":
		for _, doc := range rollups {
			line := map[string]any{
				"rollup":      true,
				"period":      doc.Period,
				"granularity": doc.Granularity, // "daily" | "monthly"
				"views":       doc.Views,
				"latency":     doc.Latency,
			}
			b, err := json.Marshal(line)
			if err != nil {
				return gen.AIStatsExportResp{}, fmt.Errorf("aistats: marshal export rollup: %w", err)
			}
			sb.Write(b)
			sb.WriteString("\n")
		}
		for _, r := range records {
			b, err := json.Marshal(r)
			if err != nil {
				return gen.AIStatsExportResp{}, fmt.Errorf("aistats: marshal export record: %w", err)
			}
			sb.Write(b)
			sb.WriteString("\n")
		}
	case "csv":
		sb.WriteString("id,workspaceId,projectId,agentId,sessionId,turnId,requestId,provider,model,responseModel,responseId,inputTokens,outputTokens,totalTokens,cacheCreationInputTokens,cacheReadInputTokens,reasoningTokens,costTotal,latencyMs,firstTokenMs,stopReason,errorMessage,startedAt,completedAt\n")
		for _, doc := range rollups {
			matched := rollupCountersMatch(doc, req.Scope, req.ScopeID, req.WorkspaceID)
			// Rollup rows reuse none of the per-request columns; the
			// counter column order is declared here (设计 §4): requests,
			// errors, latencySumMs, input, output, total tokens,
			// cacheCreation, cacheRead, reasoning, then cost fields.
			fmt.Fprintf(&sb, "rollup,%s,%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%f,%f,%f,%f,%f\n",
				doc.Period, doc.Granularity,
				matched.RequestCount, matched.ErrorCount, matched.LatencySumMs,
				matched.InputTokens, matched.OutputTokens, matched.TotalTokens,
				matched.CacheCreationInputTokens, matched.CacheReadInputTokens, matched.ReasoningTokens,
				matched.CostInput, matched.CostOutput, matched.CostCacheRead, matched.CostCacheWrite, matched.CostTotal)
		}
		for _, r := range records {
			u := r.Usage
			if u == nil {
				u = &gen.UsageData{}
			}
			fmt.Fprintf(&sb, "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%d,%d,%d,%d,%d,%d,%f,%d,%d,%s,%s,%s,%s\n",
				r.ID, r.WorkspaceID, r.ProjectID, r.AgentID, r.SessionID, r.TurnID, r.RequestID,
				r.Provider, r.Model, r.ResponseModel, r.ResponseID,
				u.InputTokens, u.OutputTokens, u.TotalTokens,
				u.CacheCreationInputTokens, u.CacheReadInputTokens, u.ReasoningTokens,
				u.CostTotal, r.LatencyMs, r.FirstTokenMs, r.StopReason, r.ErrorMessage,
				r.StartedAt, r.CompletedAt)
		}
	default:
		return gen.AIStatsExportResp{}, fmt.Errorf("aistats: unsupported export format %q", req.Format)
	}
	return gen.AIStatsExportResp{Data: sb.String()}, nil
}

func (a *Actor) handleBackfill(ctx actor.PureContext, req gen.AIStatsBackfillReq) (gen.AIStatsBackfillResp, error) {
	// Backfill is implemented in the agent layer; aistats just provides the
	// storage surface. For now, return zero and let the caller scan history.
	ctx.Logger().Info("aistats: backfill requested", "workspace", req.WorkspaceID)
	return gen.AIStatsBackfillResp{RecordCount: 0}, nil
}

// cleanupBatchSize caps the number of ghost records removed in a single
// aistats.cleanup invocation. Each invocation holds the actor write lock for the
// (scan + delete + rebuild + save) window, so keeping the batch small protects
// the owner lane from blocking on very large cleanups. Callers can poll the
// callable when Remaining > 0.
const cleanupBatchSize = 100

// AIStatsCleanupReq is the request for the aistats.cleanup maintenance
// callable. It is intentionally kept out of the generated schema to minimize
// surface area; the callable is registered directly in OnStart.
type AIStatsCleanupReq struct {
	DryRun bool `json:"dryRun"`
}

// AIStatsCleanupResp reports how many aggregator ghost records were found,
// deleted in this batch, and left for subsequent calls.
type AIStatsCleanupResp struct {
	Found     int64 `json:"found"`
	Deleted   int64 `json:"deleted"`
	Remaining int64 `json:"remaining"`
}

// handleCleanup removes stale Provider="aggregator" records created by the
// nested-aggregator stats attribution bug. It runs entirely under the actor
// write lock so deletions and counter rebuilds are atomic relative to
// processRecord. The operation is idempotent: a second call on an already-clean
// state rebuilds counters from raw records plus every rollup document (so
// convolved history is never dropped) and saves the checkpoint.
func (a *Actor) handleCleanup(ctx actor.PureContext, req AIStatsCleanupReq) (AIStatsCleanupResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Load all records under the write lock. This scan is the only way to get a
	// consistent view of records in the DB while processRecord is blocked, which
	// keeps delete + rebuild + save in the same lock window.
	records, err := loadRecords(a.store, a.scanner, time.Time{}, time.Time{}, "", "", "")
	if err != nil {
		return AIStatsCleanupResp{}, fmt.Errorf("aistats.cleanup: load records: %w", err)
	}

	var ghosts []gen.AIStatsRecord
	var real []gen.AIStatsRecord
	for _, r := range records {
		if r.Provider == "aggregator" {
			ghosts = append(ghosts, r)
		} else {
			real = append(real, r)
		}
	}

	resp := AIStatsCleanupResp{Found: int64(len(ghosts))}
	if len(ghosts) == 0 {
		// Even when no ghosts remain, rebuild counters from disk to clear any
		// residual aggregator state that may have been persisted in the
		// checkpoint before the bug was fixed. The rebuild must replay not
		// only the raw records but every rollup document as well, otherwise
		// history already convolved away from the raw layer would be lost
		// (设计 §4 aistats.cleanup 交互).
		if !req.DryRun {
			rebuilt, err := a.rebuildCountersWithRollups(real)
			if err != nil {
				return AIStatsCleanupResp{}, err
			}
			a.counters = rebuilt
			a.hotRecords = hotCache{}
			if err := a.saveLocked(); err != nil {
				return AIStatsCleanupResp{}, fmt.Errorf("aistats.cleanup: save checkpoint: %w", err)
			}
		}
		return resp, nil
	}

	if req.DryRun {
		return resp, nil
	}

	limit := cleanupBatchSize
	if len(ghosts) < limit {
		limit = len(ghosts)
	}
	for i := 0; i < limit; i++ {
		if err := deleteRecord(a.store, ghosts[i]); err != nil {
			// Log and continue; a missing record is acceptable. Failed
			// deletions are counted as not deleted and will be retried.
			ctx.Logger().Warn("aistats.cleanup: delete record failed", "error", err, "id", ghosts[i].ID)
			continue
		}
		resp.Deleted++
	}

	// Rebuild counters from the real records still in the DB plus every
	// rollup document. Any ghosts not deleted in this batch are still
	// present, but they are excluded from the rebuilt counters.
	rebuilt, err := a.rebuildCountersWithRollups(real)
	if err != nil {
		return AIStatsCleanupResp{}, err
	}
	a.counters = rebuilt
	a.hotRecords = hotCache{}
	if err := a.saveLocked(); err != nil {
		return AIStatsCleanupResp{}, fmt.Errorf("aistats.cleanup: save checkpoint: %w", err)
	}

	resp.Remaining = int64(len(ghosts)) - resp.Deleted
	return resp, nil
}

// rebuildCountersWithRollups reconstructs the in-memory counters by replaying
// the given raw records plus every rollup document (daily + monthly). The
// rollup layer holds the only surviving copy of history whose raw records
// were already convolved away, so a raw-only rebuild would silently drop it
// (设计 §4 aistats.cleanup 交互). Correctness rests on the §5 mutual
// exclusion invariant — a given UTC day contributes either through its raw
// records or through its rollup document, never both — so plain replay
// addition cannot double-count.
//
// The one transient exception is the per-day protocol's crash window: a
// document whose guarded raw records (StragglerIDs) are not deleted yet is
// counted both in the document's views and in the raw scan. Guarded IDs are
// therefore skipped during the raw replay; their counts already live in the
// document views. Monthly documents carry only the four bounded-cardinality
// views (Session/Project/Agent are nil) — the declared permanent-layer
// dimension downgrade (I5) — so the rebuilt session/project/agent views
// contain nearline data only, consistent with the query semantics.
//
// Caller must hold a.mu (the enumeration and replay are one consistency
// window relative to processRecord and the rollup lane).
func (a *Actor) rebuildCountersWithRollups(real []gen.AIStatsRecord) (*counters, error) {
	docs, guarded, err := a.loadAllRollupDocs()
	if err != nil {
		return nil, fmt.Errorf("aistats.cleanup: %w", err)
	}
	c := newCounters()
	for _, r := range real {
		if guarded[r.ID] {
			continue // counts already inside a rollup document's views
		}
		c.apply(r)
	}
	for _, doc := range docs {
		c.addCheckpointViews(&doc.Views)
	}
	return c, nil
}

// loadAllRollupDocs enumerates and loads every rollup document — monthly
// documents plus all daily documents under rollups/daily/<YYYY-MM>/. It also
// returns the union of every document's StragglerIDs guard: raw records still
// on disk whose counts are already merged into a document's views.
func (a *Actor) loadAllRollupDocs() ([]*rollupDoc, map[string]bool, error) {
	lister, ok := a.store.(persist.Lister)
	if !ok {
		return nil, nil, fmt.Errorf("rollup replay requires Lister backend")
	}
	var names []string
	monthly, err := lister.List("rollups/monthly/")
	if err != nil {
		return nil, nil, fmt.Errorf("list monthly rollups: %w", err)
	}
	names = append(names, monthly...)
	dailyMonths, err := rollupMonthsWithDailyDocs(a.store)
	if err != nil {
		return nil, nil, err
	}
	for _, ym := range dailyMonths {
		daily, err := lister.List("rollups/daily/" + ym + "/")
		if err != nil {
			return nil, nil, fmt.Errorf("list daily rollups %s: %w", ym, err)
		}
		names = append(names, daily...)
	}
	docs := make([]*rollupDoc, 0, len(names))
	guarded := make(map[string]bool)
	for _, name := range names {
		doc, err := loadRollupDoc(a.store, name)
		if err != nil {
			return nil, nil, err
		}
		if doc == nil {
			continue
		}
		docs = append(docs, doc)
		for _, id := range doc.StragglerIDs {
			guarded[id] = true
		}
	}
	return docs, guarded, nil
}

// addCheckpointViews merges a frozen rollup view set into c, view by view.
// Nil maps (the monthly document's dropped Session/Project/Agent views)
// contribute nothing.
func (c *counters) addCheckpointViews(cp *checkpoint) {
	c.workspace = addCounters(c.workspace, cp.Workspace)
	for k, v := range cp.WorkspaceMap {
		c.workspaceMap[k] = addCounters(c.workspaceMap[k], v)
	}
	for k, v := range cp.Session {
		c.session[k] = addCounters(c.session[k], v)
	}
	for k, v := range cp.Project {
		c.project[k] = addCounters(c.project[k], v)
	}
	for k, v := range cp.Agent {
		c.agent[k] = addCounters(c.agent[k], v)
	}
	for k, v := range cp.Provider {
		c.provider[k] = addCounters(c.provider[k], v)
	}
	for k, v := range cp.Model {
		c.model[k] = addCounters(c.model[k], v)
	}
}
