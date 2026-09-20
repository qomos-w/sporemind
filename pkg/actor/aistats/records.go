package aistats

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// recordMonth extracts the YYYY-MM month from a record's CompletedAt.
func recordMonth(r gen.AIStatsRecord) string {
	completedAt, _ := time.Parse(time.RFC3339Nano, r.CompletedAt)
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	return completedAt.Format("2006-01")
}

// recordName builds the persist document name for a record:
// records/<YYYY-MM>/<recordID>. This is the name accepted by Save/Load/Delete
// and the prefix used by ScanPrefix("records/<YYYY-MM>/").
func recordName(r gen.AIStatsRecord) string {
	return "records/" + recordMonth(r) + "/" + r.ID
}

// monthMarkerName builds the persist document name for a month marker:
// months/<YYYY-MM>. The marker is an idempotent boolean written alongside
// each record so monthsInRange can enumerate months in O(months) via List
// instead of scanning the full records/ tree.
func monthMarkerName(month string) string {
	return "months/" + month
}

// appendRecord writes a record and its month marker to the persist store in
// a single atomic batch (when the backend implements Saver; otherwise two
// sequential Saves). The checkpoint is NOT included — callers that need
// record+checkpoint atomicity use processRecordLocked which issues a single
// SaveAll with record, month marker, and checkpoint together.
//
// Safe to call from multiple goroutines: records are append-only and named by
// UUID. SaveAll on goleveldb is a single batch write (no appendMu); on fs it
// degrades to sequential atomic file writes (WriteFileAtomic).
func appendRecord(store persist.Persist, r gen.AIStatsRecord) error {
	month := recordMonth(r)
	return persist.SaveAll(store, []persist.Doc{
		{Name: recordName(r), Value: r},
		{Name: monthMarkerName(month), Value: true},
	})
}

// loadRecords scans records matching the given scope. Only the months
// overlapping [since, until] are enumerated. workspaceID, when non-empty,
// restricts to a single workspace (global aggregation otherwise).
//
// Cold path: uses PrefixScanner.ScanPrefix when the backend implements it
// (goleveldb snapshot iterator, no write lock); falls back to List+Load
// (N+1, fs only) when the backend does not. The scan never holds the actor
// lock — callers snapshot the hot cache under RLock and release before
// calling this function.
func loadRecords(store persist.Persist, scanner persist.PrefixScanner, since, until time.Time, scope, scopeID, workspaceID string) ([]gen.AIStatsRecord, error) {
	months, err := monthsInRange(store, since, until)
	if err != nil {
		return nil, err
	}

	var records []gen.AIStatsRecord
	for _, month := range months {
		prefix := "records/" + month + "/"
		var recs []gen.AIStatsRecord
		if scanner != nil {
			recs, err = loadRecordsViaScanner(scanner, prefix)
		} else {
			recs, err = loadRecordsViaList(store, prefix)
		}
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if !recordInTimeRange(r, since, until) {
				continue
			}
			if matchRecord(r, scope, scopeID, workspaceID) {
				records = append(records, r)
			}
		}
	}
	// Records are returned in scan (ascending name) order. Callers are
	// responsible for sorting (see sortRecords) before applying
	// limit/offset or serializing.
	return records, nil
}

// loadRecordsViaScanner reads all records under prefix in a single
// iterator pass using PrefixScanner.ScanPrefix. The iterator is a snapshot
// read — no write lock is held.
func loadRecordsViaScanner(scanner persist.PrefixScanner, prefix string) ([]gen.AIStatsRecord, error) {
	kvs, err := scanner.ScanPrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("aistats: scan records %s: %w", prefix, err)
	}
	records := make([]gen.AIStatsRecord, 0, len(kvs))
	for _, kv := range kvs {
		var r gen.AIStatsRecord
		if err := json.Unmarshal(kv.Value, &r); err != nil {
			return nil, fmt.Errorf("aistats: decode record %s: %w", kv.Name, err)
		}
		records = append(records, r)
	}
	return records, nil
}

// loadRecordsViaList is the fallback for backends that do not implement
// PrefixScanner (fs). It enumerates names via List and loads each record
// individually (N+1 reads). Missing records are skipped (idempotent with
// concurrent deletes).
func loadRecordsViaList(store persist.Persist, prefix string) ([]gen.AIStatsRecord, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return nil, fmt.Errorf("aistats: record listing requires Lister backend")
	}
	names, err := lister.List(prefix)
	if err != nil {
		return nil, fmt.Errorf("aistats: list records %s: %w", prefix, err)
	}
	records := make([]gen.AIStatsRecord, 0, len(names))
	for _, name := range names {
		var r gen.AIStatsRecord
		if err := store.Load(name, &r); err != nil {
			if errors.Is(err, persist.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("aistats: load record %s: %w", name, err)
		}
		records = append(records, r)
	}
	return records, nil
}

// monthsInRange returns the months that overlap [since, until]. It first
// tries month markers (months/<YYYY-MM>) written alongside each record; if no
// markers exist (legacy data without markers), it falls back to extracting
// month names from the records/ document tree via List.
func monthsInRange(store persist.Persist, since, until time.Time) ([]string, error) {
	months, err := listMonthMarkers(store)
	if err != nil {
		return nil, err
	}

	// Fallback for legacy data without month markers: extract months from
	// record document names (records/<YYYY-MM>/<id>).
	if len(months) == 0 {
		months, err = monthsFromRecords(store)
		if err != nil {
			return nil, err
		}
	}

	if since.IsZero() {
		sort.Strings(months)
		return months, nil
	}
	end := until
	if end.IsZero() {
		end = time.Now().UTC()
	}
	startMonth := since.Format("2006-01")
	endMonth := end.Format("2006-01")
	var filtered []string
	for _, m := range months {
		if m >= startMonth && m <= endMonth {
			filtered = append(filtered, m)
		}
	}
	sort.Strings(filtered)
	return filtered, nil
}

// listMonthMarkers enumerates months/<YYYY-MM> markers from the persist store.
func listMonthMarkers(store persist.Persist) ([]string, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return nil, fmt.Errorf("aistats: month listing requires Lister backend")
	}
	names, err := lister.List("months/")
	if err != nil {
		return nil, fmt.Errorf("aistats: list months: %w", err)
	}
	var months []string
	for _, name := range names {
		month := strings.TrimPrefix(name, "months/")
		if month != name {
			months = append(months, month)
		}
	}
	return months, nil
}

// monthsFromRecords extracts unique month names from record document names
// (records/<YYYY-MM>/<id>) when no month markers exist.
func monthsFromRecords(store persist.Persist) ([]string, error) {
	lister, ok := store.(persist.Lister)
	if !ok {
		return nil, fmt.Errorf("aistats: month extraction requires Lister backend")
	}
	names, err := lister.List("records/")
	if err != nil {
		return nil, fmt.Errorf("aistats: list records for months: %w", err)
	}
	seen := make(map[string]bool)
	var months []string
	for _, name := range names {
		rest := strings.TrimPrefix(name, "records/")
		if rest == name {
			continue
		}
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 || parts[0] == "" {
			continue
		}
		if !seen[parts[0]] {
			seen[parts[0]] = true
			months = append(months, parts[0])
		}
	}
	return months, nil
}

// percentileFloat returns the p-th percentile of a sorted slice. It uses
// nearest-rank semantics; empty input yields 0.
func percentileFloat(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(float64(n) * p / 100)
	if idx >= n {
		idx = n - 1
	}
	return sorted[idx]
}

func matchScope(r gen.AIStatsRecord, scope, scopeID string) bool {
	switch scope {
	case "session":
		return r.SessionID == scopeID
	case "project":
		return r.ProjectID == scopeID
	case "workspace":
		if scopeID == "" {
			return true
		}
		return r.WorkspaceID == scopeID
	case "provider":
		return r.Provider == scopeID
	case "model":
		parts := strings.SplitN(scopeID, "/", 2)
		if len(parts) != 2 {
			return false
		}
		return r.Provider == parts[0] && r.Model == parts[1]
	case "agent":
		return r.AgentID == scopeID
	default:
		return true
	}
}

// matchRecord applies the workspace filter (when set) then the scope filter.
func matchRecord(r gen.AIStatsRecord, scope, scopeID, workspaceID string) bool {
	if workspaceID != "" && r.WorkspaceID != workspaceID {
		return false
	}
	return matchScope(r, scope, scopeID)
}

// recordInTimeRange reports whether the record's CompletedAt falls within
// [since, until]. Records with an unparseable or zero CompletedAt are excluded.
func recordInTimeRange(r gen.AIStatsRecord, since, until time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, r.CompletedAt)
	if err != nil || t.IsZero() {
		return false
	}
	if !since.IsZero() && t.Before(since) {
		return false
	}
	if !until.IsZero() && t.After(until) {
		return false
	}
	return true
}

// sortRecords sorts records by CompletedAt in place. RFC3339Nano strings sort
// lexicographically the same as chronologically. The default order is
// ascending (oldest first); order "desc" returns newest first.
func sortRecords(records []gen.AIStatsRecord, order string) {
	lessAsc := func(i, j int) bool { return records[i].CompletedAt < records[j].CompletedAt }
	lessDesc := func(i, j int) bool { return records[i].CompletedAt > records[j].CompletedAt }
	if order == "desc" {
		sort.Slice(records, lessDesc)
	} else {
		sort.Slice(records, lessAsc)
	}
}

// sliceRecords applies limit/offset paging to records. limit <= 0 means no
// limit; an offset past the end returns an empty, non-nil slice.
func sliceRecords(records []gen.AIStatsRecord, limit, offset int32) []gen.AIStatsRecord {
	start := int(offset)
	if start < 0 {
		start = 0
	}
	if start > len(records) {
		return records[:0]
	}
	end := start + int(limit)
	if end > len(records) || limit <= 0 {
		end = len(records)
	}
	return records[start:end]
}

// deleteRecord removes a single record by computing its month from
// CompletedAt and deleting the persist document. Idempotent: a missing record
// is a no-op (persist.Delete is idempotent).
func deleteRecord(store persist.Persist, r gen.AIStatsRecord) error {
	name := recordName(r)
	if err := store.Delete(name); err != nil {
		return fmt.Errorf("aistats: delete record %s: %w", name, err)
	}
	return nil
}
