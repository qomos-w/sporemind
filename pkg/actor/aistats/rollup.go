package aistats

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// This file implements the rollup engine behind the three-tier retention
// design (设计：aistats 超时降粒度保留方案 §1–§3): raw records older than
// aistats_raw_days are losslessly projected into daily rollup documents;
// daily documents older than aistats_daily_days are losslessly merged into
// permanent monthly documents. The rollup document's own existence is the
// idempotency marker — a single-document Save is atomic on every backend
// (fs: WriteFileAtomic; goleveldb: single Put), so a crash at any step is
// recoverable by re-running the same protocol.
//
// Lane discipline (Owner Lane 禁阻塞 red line): all rollup work runs on the
// dedicated "aistats_rollup" loop, never on the owner lane. The startup
// catch-up and the hourly ticker fire a fire-and-forget self-invoke of the
// AdminOnly aistats.rollup callable, so they execute on the same lane as
// manual invocations.
//
// Lock discipline: each per-day window takes a.mu once and holds it across
// scan → aggregate → save → delete so no record can slip into a day being
// rolled up (processRecord takes the same lock). The window is a single day;
// between days the lock is released so the owner lane interleaves. persist
// calls inside the window are leaf calls — no lock re-entry, no lock cycles.

const (
	// rollupTickInterval is the fixed interval between catch-up rounds. It
	// is a code constant, deliberately not a config surface (设计 §6).
	rollupTickInterval = time.Hour

	// rollupCatchUpPerRound caps how many daily documents one round computes,
	// bounding per-round work; backlog drains round by round (设计 §3). Days
	// whose document already exist are reentry (resume leftover deletion) and
	// do not count against the cap.
	rollupCatchUpPerRound = 7
)

// rollupLatency is the frozen latency snapshot of a rollup period. Daily
// documents carry the full percentile set (computed once from the raw
// records); monthly documents only carry count/sum/max — percentiles cannot
// be merged exactly across days and are declared unavailable at month grain
// (设计 §1).
type rollupLatency struct {
	Count   int64   `json:"count"`
	SumMs   int64   `json:"sumMs"`
	MaxMs   int64   `json:"maxMs"`
	P50     float64 `json:"p50,omitempty"`
	P95     float64 `json:"p95,omitempty"`
	TtftP50 float64 `json:"ttftP50,omitempty"`
}

// rollupDoc is the internal persist document for one rollup period. It is an
// aistats-internal document and intentionally NOT part of the spore schema.
//
//   - Daily documents hold all seven counter views (same shape as the
//     checkpoint: Workspace/WorkspaceMap/Session/Project/Agent/Provider/
//     Model) so L1 stays fully drill-downable.
//   - Monthly documents keep only the four bounded-cardinality views
//     (Workspace/WorkspaceMap/Provider/Model); Session/Project/Agent are
//     nil — the declared dimension downgrade at the permanent layer (设计
//     §1, invariant I5).
type rollupDoc struct {
	Period      string        `json:"period"`
	Granularity string        `json:"granularity"`
	Views       checkpoint    `json:"views"`
	Latency     rollupLatency `json:"latency"`
	Requests    int64         `json:"requests"`
	// StragglerIDs is the exactly-once guard for records that arrived on an
	// already-rolled-up period after its document was computed (backfill
	// imports, clock-corrected replays). The IDs of every raw record being
	// absorbed are persisted BEFORE any deletion starts; once every deletion
	// succeeded the guard is cleared. A crash anywhere in between re-enters
	// with the leftovers marked pending — delete-only, never recounted.
	StragglerIDs []string `json:"straggler_ids,omitempty"`
}

// dailyRollupName returns the persist document name of a daily rollup:
// rollups/daily/<YYYY-MM>/<DD>. Month nesting (rather than a flat
// YYYY-MM-DD) lets the monthly pass reclaim a whole month with one cascade
// Delete("rollups/daily/<YYYY-MM>") (设计 §2).
func dailyRollupName(day string) string {
	return "rollups/daily/" + day[:7] + "/" + day[8:10]
}

// monthlyRollupName returns the persist document name of a monthly rollup:
// rollups/monthly/<YYYY-MM>.
func monthlyRollupName(month string) string {
	return "rollups/monthly/" + month
}

// loadRollupDoc loads a rollup document, returning (nil, nil) when absent.
func loadRollupDoc(store persist.Persist, name string) (*rollupDoc, error) {
	var d rollupDoc
	err := store.Load(name, &d)
	if errors.Is(err, persist.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("aistats: load rollup %s: %w", name, err)
	}
	return &d, nil
}

// startOfDayUTC returns the UTC midnight bounding the given instant's day.
func startOfDayUTC(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// daysInMonth returns the number of calendar days in "YYYY-MM".
func daysInMonth(month string) int {
	first, err := time.ParseInLocation("2006-01", month, time.UTC)
	if err != nil {
		return 0
	}
	return int(first.AddDate(0, 1, 0).Sub(first).Hours() / 24)
}

// AIStatsRollupReq is the request for the aistats.rollup maintenance
// callable. Like AIStatsCleanupReq it is intentionally kept out of the
// generated schema — the callable is registered directly in OnStart.
type AIStatsRollupReq struct {
	// DryRun computes and reports what would be rolled up without saving
	// documents or deleting anything.
	DryRun bool `json:"dryRun"`
	// OnlyDay restricts the run to a single UTC day ("2006-01-02"),
	// bypassing the retention cutoff (admin force / manual backfill).
	OnlyDay string `json:"onlyDay,omitempty"`
}

// AIStatsRollupResp reports one rollup round's work.
type AIStatsRollupResp struct {
	DryRun              bool  `json:"dryRun"`
	DaysExamined        int   `json:"daysExamined"`
	DaysComputed        int   `json:"daysComputed"`
	DaysExisting        int   `json:"daysExisting"`
	RecordsDeleted      int64 `json:"recordsDeleted"`
	MonthMarkersCleared int   `json:"monthMarkersCleared"`
	MonthsExamined      int   `json:"monthsExamined"`
	MonthsComputed      int   `json:"monthsComputed"`
	MonthsExisting      int   `json:"monthsExisting"`
	MonthsIncomplete    int   `json:"monthsIncomplete"`
	MonthsNotEligible   int   `json:"monthsNotEligible"`
}

// handleRollup is the AdminOnly aistats.rollup callable. Registered on the
// aistats_rollup loop in OnStart so the scan never occupies the owner lane.
func (a *Actor) handleRollup(_ actor.PureContext, req AIStatsRollupReq) (AIStatsRollupResp, error) {
	if req.OnlyDay != "" {
		if _, err := time.ParseInLocation("2006-01-02", req.OnlyDay, time.UTC); err != nil {
			return AIStatsRollupResp{}, fmt.Errorf("aistats.rollup: onlyDay must be YYYY-MM-DD: %w", err)
		}
	}
	return a.rollupOnce(time.Now().UTC(), req)
}

// rollupOnce runs one catch-up round: the per-day pass followed by the
// per-month pass. now is injected for testability. Each per-day / per-month
// window acquires a.mu itself; this function never holds the lock, so the
// owner lane is only ever blocked for a single-day window.
func (a *Actor) rollupOnce(now time.Time, req AIStatsRollupReq) (AIStatsRollupResp, error) {
	resp := AIStatsRollupResp{DryRun: req.DryRun}

	if req.OnlyDay != "" {
		a.mu.Lock()
		outcome, err := a.rollupDayLocked(now, req.OnlyDay, true, req.DryRun)
		a.mu.Unlock()
		if err != nil {
			return resp, err
		}
		resp.DaysExamined = 1
		resp.accumulateDay(outcome)
		return resp, nil
	}

	// Per-day pass: enumerate candidate days from the union of (a) months
	// holding raw records (markers first, records-tree fallback) and (b)
	// months that already have daily rollup documents. The union matters for
	// crash recovery: a month whose raw records were fully deleted (marker
	// cleared) but whose days were only partially documented (K-cap) must
	// still be enumerated so the remaining empty-day documents get written.
	months, err := a.rollupCandidateMonths()
	if err != nil {
		return resp, err
	}
	cutoff := startOfDayUTC(now).AddDate(0, 0, -config.AistatsRawDays())
	for _, ym := range months {
		if resp.DaysComputed >= rollupCatchUpPerRound {
			break
		}
		first, err := time.ParseInLocation("2006-01", ym, time.UTC)
		if err != nil {
			continue
		}
		monthEnd := first.AddDate(0, 1, -1) // last day of the month
		for d := first; d.Before(cutoff) && !d.After(monthEnd); d = d.AddDate(0, 0, 1) {
			day := d.Format("2006-01-02")
			// A pre-existing document is only a reentry (resume leftover
			// deletion); the cheap marker read is not counted against the
			// K cap so a round always makes progress on undocumented days.
			if existing, err := loadRollupDoc(a.store, dailyRollupName(day)); err != nil {
				return resp, err
			} else if existing != nil {
				a.mu.Lock()
				outcome, err := a.rollupDayLocked(now, day, false, req.DryRun)
				a.mu.Unlock()
				if err != nil {
					return resp, fmt.Errorf("aistats.rollup: day %s: %w", day, err)
				}
				resp.DaysExamined++
				resp.accumulateDay(outcome)
				continue
			}
			if resp.DaysComputed >= rollupCatchUpPerRound {
				break
			}
			a.mu.Lock()
			outcome, err := a.rollupDayLocked(now, day, false, req.DryRun)
			a.mu.Unlock()
			if err != nil {
				return resp, fmt.Errorf("aistats.rollup: day %s: %w", day, err)
			}
			resp.DaysExamined++
			resp.accumulateDay(outcome)
		}
	}

	// Per-month pass: compress complete daily months into permanent monthly
	// documents. Months are enumerated from the rollups/daily tree.
	dailyMonths, err := rollupMonthsWithDailyDocs(a.store)
	if err != nil {
		return resp, err
	}
	for _, ym := range dailyMonths {
		a.mu.Lock()
		mOutcome, err := a.rollupMonthLocked(now, ym, req.DryRun)
		a.mu.Unlock()
		if err != nil {
			return resp, fmt.Errorf("aistats.rollup: month %s: %w", ym, err)
		}
		resp.MonthsExamined++
		switch mOutcome.state {
		case monthComputed:
			resp.MonthsComputed++
		case monthExisting:
			resp.MonthsExisting++
		case monthIncomplete:
			resp.MonthsIncomplete++
		case monthNotEligible:
			resp.MonthsNotEligible++
		}
	}
	return resp, nil
}

func (resp *AIStatsRollupResp) accumulateDay(o dayOutcome) {
	if o.computed {
		resp.DaysComputed++
	} else {
		resp.DaysExisting++
	}
	resp.RecordsDeleted += o.deleted
	if o.markerCleared {
		resp.MonthMarkersCleared++
	}
}

// dayOutcome reports what one per-day window did.
type dayOutcome struct {
	computed      bool // a daily document was (re)computed and saved
	deleted       int64
	markerCleared bool
}

// rollupDayLocked rolls up a single UTC day ("2006-01-02"). Caller must
// hold a.mu for the whole window. force bypasses the retention cutoff
// (aistats.rollup onlyDay mode).
//
// Document selection: a compressed month owns the day's aggregates, so when
// rollups/monthly/<YM> exists, stragglers are absorbed into the monthly
// document instead of creating a dangling daily document the read path's
// L2 segment would never see. Otherwise the day targets
// rollups/daily/<YYYY-MM>/<DD>.
//
// The window is fully reentrant against every crash point:
//
//   - crash before the doc: doc missing → recompute from raw (still present).
//   - crash after the doc save (guard persisted) but before/during raw
//     delete: reentry sees every raw record pending in StragglerIDs →
//     delete-only, never recounted (exactly-once).
//   - crash after all deletes but before the guard clear: reentry finds no
//     raw records and clears the stale guard.
//
// Records that arrive AFTER the document was computed (stragglers: backfill
// imports, clock-corrected replays) are absorbed additively under the same
// StragglerIDs guard — counts must never be dropped (I1).
//
// persist calls are leaf calls under the single a.mu hold — no nesting.
func (a *Actor) rollupDayLocked(now time.Time, day string, force bool, dryRun bool) (dayOutcome, error) {
	var outcome dayOutcome
	d, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		return outcome, fmt.Errorf("aistats.rollup: bad day %q: %w", day, err)
	}
	if !force && !d.Before(startOfDayUTC(now).AddDate(0, 0, -config.AistatsRawDays())) {
		return outcome, nil // inside the raw retention window (I6): never touch
	}

	records, err := a.scanDayRecords(day)
	if err != nil {
		return outcome, err
	}
	// The full month scan backs the guard pruning below: a guard on the
	// (month-wide) monthly document may be owned by another day's window,
	// so staleness must be judged against every raw record of the month,
	// never against this day's slice alone.
	monthRecords, err := a.scanMonthRecords(day[:7])
	if err != nil {
		return outcome, err
	}

	// Resolve the target document: the daily doc, else the compressed
	// monthly doc (dangling-daily prevention), else none.
	targetName := dailyRollupName(day)
	doc, err := loadRollupDoc(a.store, targetName)
	if err != nil {
		return outcome, err
	}
	if doc == nil {
		monthly, err := loadRollupDoc(a.store, monthlyRollupName(day[:7]))
		if err != nil {
			return outcome, err
		}
		if monthly != nil {
			doc = monthly
			targetName = monthlyRollupName(day[:7])
		}
	}

	// windowGuard is the guard set that must be on disk before any deletion
	// starts; remainingGuard is what survives this window's deletions.
	var windowGuard, remainingGuard []string
	var toDelete []gen.AIStatsRecord
	switch {
	case doc == nil:
		// Fresh daily document: aggregate everything, persist the guard for
		// the whole batch BEFORE deleting anything.
		if dryRun {
			outcome.computed = true // would compute
			return outcome, nil
		}
		doc = aggregateDayRollup(day, records)
		doc.StragglerIDs = recordIDs(records)
		if err := a.store.Save(targetName, doc); err != nil {
			return outcome, fmt.Errorf("aistats.rollup: save %s: %w", targetName, err)
		}
		outcome.computed = true
		windowGuard = doc.StragglerIDs
		toDelete = records
	default:
		// Reentry / straggler absorption into an existing daily or monthly
		// document. Pending records are delete-only; fresh records are
		// merged additively and joined into the guard. The guard is pruned
		// against the month's raw records: IDs whose deletion already
		// completed (crash before the clear-save) drop out, IDs still raw
		// anywhere in the month survive.
		fresh, pending := splitStragglers(doc, records)
		if dryRun {
			outcome.computed = len(fresh) > 0 // would absorb
			return outcome, nil
		}
		if len(fresh) > 0 {
			applyStragglersToDoc(doc, fresh)
			outcome.computed = true
		}
		windowGuard = guardAfter(doc.StragglerIDs, fresh, monthRecords)
		if !sameIDSets(windowGuard, doc.StragglerIDs) {
			doc.StragglerIDs = windowGuard
			if err := a.store.Save(targetName, doc); err != nil {
				return outcome, fmt.Errorf("aistats.rollup: save %s: %w", targetName, err)
			}
		}
		toDelete = append(pending, fresh...)
	}

	// Deletion phase: remove the guarded raw records one by one. A failure
	// mid-batch warns and continues — the leftover stays guarded so the next
	// round re-enters delete-only (exactly-once).
	deleted := make(map[string]bool, len(toDelete))
	allDeleted := true
	for i := range toDelete {
		if err := deleteRecord(a.store, toDelete[i]); err != nil {
			slog.Warn("aistats.rollup: delete record failed", "error", err, "id", toDelete[i].ID)
			allDeleted = false
			continue
		}
		deleted[toDelete[i].ID] = true
		outcome.deleted++
	}
	if allDeleted {
		remainingGuard = subtractIDs(windowGuard, deleted)
		if !sameIDSets(remainingGuard, windowGuard) {
			doc.StragglerIDs = remainingGuard
			if err := a.store.Save(targetName, doc); err != nil {
				return outcome, fmt.Errorf("aistats.rollup: save %s: %w", targetName, err)
			}
		}
	}

	// Marker cleanup: once the month has no raw records left, drop its
	// months/<YYYY-MM> marker. monthsInRange falls back to the records tree
	// safely on an empty directory, and rollupCandidateMonths unions in the
	// rollups/daily tree, so the month stays enumerable either way.
	if outcome.deleted > 0 || len(records) == 0 {
		empty, err := monthRecordsEmpty(a.store, day[:7])
		if err != nil {
			return outcome, err
		}
		if empty {
			if err := a.store.Delete(monthMarkerName(day[:7])); err != nil {
				return outcome, fmt.Errorf("aistats.rollup: clear month marker %s: %w", day[:7], err)
			}
			outcome.markerCleared = true
		}
	}
	return outcome, nil
}

// splitStragglers partitions records by the document's StragglerIDs guard:
// fresh records have not been counted into the document yet, pending ones
// have (a prior round merged and guarded them but their deletion has not
// completed).
func splitStragglers(doc *rollupDoc, records []gen.AIStatsRecord) (fresh, pending []gen.AIStatsRecord) {
	if len(doc.StragglerIDs) == 0 {
		return records, nil
	}
	guarded := make(map[string]bool, len(doc.StragglerIDs))
	for _, id := range doc.StragglerIDs {
		guarded[id] = true
	}
	for _, r := range records {
		if guarded[r.ID] {
			pending = append(pending, r)
		} else {
			fresh = append(fresh, r)
		}
	}
	return fresh, pending
}

// applyStragglersToDoc merges the aggregation of fresh records additively
// into doc. Daily documents merge all seven views; monthly documents merge
// only their four bounded-cardinality views (session/project/agent stay
// unanswerable at month grain, I5). Latency merges count/sum/max only —
// the percentile snapshot is frozen at first aggregation and cannot be
// rebuilt once the raw samples are deleted (declared degradation: totals
// stay exact, percentiles stay approximate for the straggler tail).
func applyStragglersToDoc(doc *rollupDoc, fresh []gen.AIStatsRecord) {
	c := newCounters()
	for _, r := range fresh {
		c.apply(r)
	}
	agg := c.snapshot()
	doc.Views.Workspace = addCounters(doc.Views.Workspace, agg.Workspace)
	mergeCounterMaps(doc.Views.WorkspaceMap, agg.WorkspaceMap)
	mergeCounterMaps(doc.Views.Provider, agg.Provider)
	mergeCounterMaps(doc.Views.Model, agg.Model)
	if doc.Granularity != "monthly" {
		mergeCounterMaps(doc.Views.Session, agg.Session)
		mergeCounterMaps(doc.Views.Project, agg.Project)
		mergeCounterMaps(doc.Views.Agent, agg.Agent)
	}
	for _, r := range fresh {
		if r.LatencyMs > 0 {
			doc.Latency.Count++
			doc.Latency.SumMs += r.LatencyMs
			if r.LatencyMs > doc.Latency.MaxMs {
				doc.Latency.MaxMs = r.LatencyMs
			}
		}
	}
	doc.Requests += int64(len(fresh))
}

func mergeCounterMaps(dst, src map[string]gen.AIStatsCounters) {
	for k, v := range src {
		dst[k] = addCounters(dst[k], v)
	}
}

func recordIDs(records []gen.AIStatsRecord) []string {
	if len(records) == 0 {
		return nil
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return ids
}

// scanMonthRecords returns every raw record under records/<YYYY-MM>/.
// Uses the PrefixScanner snapshot when the backend has one, else List+Load.
func (a *Actor) scanMonthRecords(month string) ([]gen.AIStatsRecord, error) {
	prefix := "records/" + month + "/"
	if a.scanner != nil {
		return loadRecordsViaScanner(a.scanner, prefix)
	}
	return loadRecordsViaList(a.store, prefix)
}

// scanDayRecords returns the raw records whose CompletedAt falls on the UTC
// day "2006-01-02" (a filtered view of the month scan).
func (a *Actor) scanDayRecords(day string) ([]gen.AIStatsRecord, error) {
	month, err := a.scanMonthRecords(day[:7])
	if err != nil {
		return nil, err
	}
	dayStart, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		return nil, err
	}
	dayEnd := dayStart.AddDate(0, 0, 1)
	var out []gen.AIStatsRecord
	for _, r := range month {
		t, err := time.Parse(time.RFC3339Nano, r.CompletedAt)
		if err != nil || t.IsZero() {
			continue
		}
		if !t.Before(dayStart) && t.Before(dayEnd) {
			out = append(out, r)
		}
	}
	return out, nil
}

// guardAfter computes the guard set for a window: the existing guard ∪ the
// fresh records' IDs, pruned to IDs that are still present as raw records
// of the month. Pruning implements both self-healing behaviors: a guard ID
// whose deletion already completed (crash before the clear-save) drops out,
// while a guard ID still raw anywhere in the month — including one owned by
// a different day's window on the shared monthly document — survives.
func guardAfter(guard []string, fresh []gen.AIStatsRecord, monthRecords []gen.AIStatsRecord) []string {
	if len(guard) == 0 && len(fresh) == 0 {
		return nil
	}
	present := make(map[string]bool, len(monthRecords))
	for _, r := range monthRecords {
		present[r.ID] = true
	}
	seen := make(map[string]bool, len(guard)+len(fresh))
	var out []string
	add := func(id string) {
		if present[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range guard {
		add(id)
	}
	for _, r := range fresh {
		add(r.ID)
	}
	return out
}

// sameIDSets reports whether two ID slices hold the same set (order and
// duplicates ignored; nil and empty are equal).
func sameIDSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	for _, id := range b {
		if !set[id] {
			return false
		}
	}
	return true
}

// subtractIDs removes the given IDs from ids, preserving order.
func subtractIDs(ids []string, removed map[string]bool) []string {
	var out []string
	for _, id := range ids {
		if !removed[id] {
			out = append(out, id)
		}
	}
	return out
}

// aggregateDayRollup projects one day's raw records into a daily rollupDoc:
// the seven counter views via the same counters.apply used by the write
// path (so rollup counters are bit-identical to checkpoint semantics), plus
// the frozen latency snapshot.
func aggregateDayRollup(day string, records []gen.AIStatsRecord) *rollupDoc {
	c := newCounters()
	var latencies, ttfts []float64
	var sumMs, maxMs int64
	for _, r := range records {
		c.apply(r)
		if r.LatencyMs > 0 {
			latencies = append(latencies, float64(r.LatencyMs))
			sumMs += r.LatencyMs
			if r.LatencyMs > maxMs {
				maxMs = r.LatencyMs
			}
		}
		if r.FirstTokenMs > 0 {
			ttfts = append(ttfts, float64(r.FirstTokenMs))
		}
	}
	sort.Float64s(latencies)
	sort.Float64s(ttfts)
	cp := c.snapshot()
	return &rollupDoc{
		Period:      day,
		Granularity: "daily",
		Views:       *cp,
		Latency: rollupLatency{
			Count:   int64(len(latencies)),
			SumMs:   sumMs,
			MaxMs:   maxMs,
			P50:     percentileFloat(latencies, 50),
			P95:     percentileFloat(latencies, 95),
			TtftP50: percentileFloat(ttfts, 50),
		},
		Requests: cp.Workspace.RequestCount,
	}
}

// monthRecordsEmpty reports whether records/<YYYY-MM>/ holds no documents.
// Returns false (conservatively keeping the marker) when the backend cannot
// list.
func monthRecordsEmpty(store persist.Persist, ym string) (bool, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return false, nil
	}
	names, err := lister.List("records/" + ym + "/")
	if err != nil {
		return false, fmt.Errorf("aistats.rollup: list records/%s: %w", ym, err)
	}
	return len(names) == 0, nil
}

// rollupCandidateMonths enumerates the months that may contain rollable
// days: the union of months holding raw records (monthsInRange: markers
// first, records-tree fallback) and months that already have daily rollup
// documents. Sorted chronologically; dictionary order == time order.
func (a *Actor) rollupCandidateMonths() ([]string, error) {
	seen := map[string]bool{}
	var months []string
	raw, err := monthsInRange(a.store, time.Time{}, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("aistats.rollup: list raw months: %w", err)
	}
	for _, ym := range raw {
		if !seen[ym] {
			seen[ym] = true
			months = append(months, ym)
		}
	}
	daily, err := rollupMonthsWithDailyDocs(a.store)
	if err != nil {
		return nil, err
	}
	for _, ym := range daily {
		if !seen[ym] {
			seen[ym] = true
			months = append(months, ym)
		}
	}
	sort.Strings(months)
	return months, nil
}

// rollupMonthsWithDailyDocs extracts unique "YYYY-MM" months from the
// rollups/daily/<YYYY-MM>/ document tree.
func rollupMonthsWithDailyDocs(store persist.Persist) ([]string, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return nil, fmt.Errorf("aistats.rollup: daily-doc enumeration requires Lister backend")
	}
	names, err := lister.List("rollups/daily/")
	if err != nil {
		return nil, fmt.Errorf("aistats.rollup: list daily rollups: %w", err)
	}
	seen := map[string]bool{}
	var months []string
	for _, name := range names {
		rest := name[len("rollups/daily/"):]
		if len(rest) < len("YYYY-MM/x") || rest[7] != '/' {
			continue
		}
		ym := rest[:7]
		if !seen[ym] {
			seen[ym] = true
			months = append(months, ym)
		}
	}
	sort.Strings(months)
	return months, nil
}

type monthState int

const (
	monthComputed monthState = iota
	monthExisting
	monthIncomplete
	monthNotEligible
)

type monthOutcome struct {
	state monthState
}

// rollupMonthLocked compresses one month of daily rollup documents into the
// permanent monthly document. Caller must hold a.mu.
//
// Completeness precondition (设计 §3): the month compresses only when its
// daily-document count equals the month's calendar day count — i.e. every
// day of the month has been rolled up and the sum never needs raw records
// that no longer exist. An incomplete month is skipped and re-examined next
// round.
func (a *Actor) rollupMonthLocked(now time.Time, ym string, dryRun bool) (monthOutcome, error) {
	var outcome monthOutcome

	// Retention: months at or newer than the daily floor stay at L1.
	cutoff := startOfDayUTC(now).AddDate(0, 0, -config.AistatsDailyDays())
	if ym >= cutoff.Format("2006-01") {
		outcome.state = monthNotEligible
		return outcome, nil
	}

	if existing, err := loadRollupDoc(a.store, monthlyRollupName(ym)); err != nil {
		return outcome, err
	} else if existing != nil {
		outcome.state = monthExisting
		return outcome, nil
	}

	lister, ok := a.store.(persist.Lister)
	if !ok {
		return outcome, fmt.Errorf("aistats.rollup: month %s compression requires Lister backend", ym)
	}
	names, err := lister.List("rollups/daily/" + ym + "/")
	if err != nil {
		return outcome, fmt.Errorf("aistats.rollup: list daily docs %s: %w", ym, err)
	}
	if len(names) != daysInMonth(ym) {
		outcome.state = monthIncomplete
		return outcome, nil
	}

	if dryRun {
		outcome.state = monthComputed // would compute
		return outcome, nil
	}

	docs := make([]*rollupDoc, 0, len(names))
	for _, name := range names {
		doc, err := loadRollupDoc(a.store, name)
		if err != nil {
			return outcome, err
		}
		if doc == nil {
			outcome.state = monthIncomplete
			return outcome, nil
		}
		docs = append(docs, doc)
	}
	if err := a.store.Save(monthlyRollupName(ym), mergeDailyRollups(ym, docs)); err != nil {
		return outcome, fmt.Errorf("aistats.rollup: save monthly %s: %w", ym, err)
	}

	// Reclaim the whole daily month with one cascade delete (persist Delete
	// subtree semantics, identical on fs and goleveldb).
	if err := a.store.Delete("rollups/daily/" + ym); err != nil {
		return outcome, fmt.Errorf("aistats.rollup: cascade daily %s: %w", ym, err)
	}
	outcome.state = monthComputed
	return outcome, nil
}

// mergeDailyRollups sums a complete month of daily documents into the
// monthly document. Only the four bounded-cardinality views survive
// (Workspace/WorkspaceMap/Provider/Model); Session/Project/Agent are
// dropped — the declared permanent-layer dimension downgrade (I5). Latency
// merges count/sum/max only; percentiles cannot merge exactly and stay 0.
func mergeDailyRollups(month string, docs []*rollupDoc) *rollupDoc {
	out := &rollupDoc{
		Period:      month,
		Granularity: "monthly",
		Views: checkpoint{
			WorkspaceMap: map[string]gen.AIStatsCounters{},
			Provider:     map[string]gen.AIStatsCounters{},
			Model:        map[string]gen.AIStatsCounters{},
		},
	}
	for _, d := range docs {
		out.Views.Workspace = addCounters(out.Views.Workspace, d.Views.Workspace)
		for k, v := range d.Views.WorkspaceMap {
			out.Views.WorkspaceMap[k] = addCounters(out.Views.WorkspaceMap[k], v)
		}
		for k, v := range d.Views.Provider {
			out.Views.Provider[k] = addCounters(out.Views.Provider[k], v)
		}
		for k, v := range d.Views.Model {
			out.Views.Model[k] = addCounters(out.Views.Model[k], v)
		}
		out.Latency.Count += d.Latency.Count
		out.Latency.SumMs += d.Latency.SumMs
		if d.Latency.MaxMs > out.Latency.MaxMs {
			out.Latency.MaxMs = d.Latency.MaxMs
		}
		out.Requests += d.Requests
		// Carry pending deletion guards forward: a guarded record's counts
		// are already inside the daily (now monthly) views, so once the
		// daily tree is reclaimed the guard must live on the monthly doc —
		// otherwise the leftover would be re-counted as fresh.
		out.StragglerIDs = append(out.StragglerIDs, d.StragglerIDs...)
	}
	return out
}

// ---------------------------------------------------------------------------
// Rollup lane + ticker
// ---------------------------------------------------------------------------

// startRollupLoop launches the background catch-up driver: one startup
// catch-up followed by a fixed 1h ticker. Both fire a fire-and-forget
// self-invoke of aistats.rollup so the actual scan runs on the
// aistats_rollup lane, never on the owner lane.
func (a *Actor) startRollupLoop(ctx actor.Context) {
	a.rollupCtx = ctx
	bg, cancel := context.WithCancel(context.Background())
	a.rollupCancel = cancel
	a.rollupDone = make(chan struct{})
	go a.rollupLoop(bg)
}

// stopRollupLoop halts the ticker goroutine and waits for it to return. Must
// run before the persist store is closed (a goroutine blocked in a rollup
// window must not outlive the store).
func (a *Actor) stopRollupLoop() {
	if a.rollupCancel != nil {
		a.rollupCancel()
		a.rollupCancel = nil
	}
	if a.rollupDone != nil {
		<-a.rollupDone
		a.rollupDone = nil
	}
}

func (a *Actor) rollupLoop(bg context.Context) {
	defer close(a.rollupDone)
	a.triggerRollup() // startup catch-up (一次)
	ticker := time.NewTicker(rollupTickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-bg.Done():
			return
		case <-ticker.C:
			a.triggerRollup()
		}
	}
}

// triggerRollup fire-and-forget self-invokes aistats.rollup on the
// aistats_rollup loop (agent_run pattern). Visibility is export-time only;
// the actor's own self-invoke is always permitted.
func (a *Actor) triggerRollup() {
	ctx := a.rollupCtx
	if ctx == nil {
		return
	}
	self := ctx.Self()
	if self == nil {
		return
	}
	call := self.Invoke(ctx.Lifecycle(), "aistats.rollup", AIStatsRollupReq{})
	if call != nil {
		_ = call.Close()
	}
}
