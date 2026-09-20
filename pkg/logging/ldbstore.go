package logging

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// Key layout inside the "logs" persist prefix (goleveldb DB directory
// <DataDir>/leveldb/logs/). Entries are keyed log/e/<YYYY-MM-DD>/<NNNNNNNN>
// so lexicographic scan order equals chronological order. Day markers
// log/m/<YYYY-MM-DD> support age-based cleanup. The migrated flag
// log/migrated is written last so a partial migration restarts idempotently.
const (
	ldbEntryPrefix = "log/e/"
	ldbDayPrefix   = "log/m/"
	ldbMigratedKey = "log/migrated"
)

// LevelDBLogStore persists structured backend logs to an embedded goleveldb
// database accessed exclusively through the pkg/persist registry. All DB
// access goes through persist.Persist — Saver for atomic batch writes,
// PrefixScanner for reverse-ordered reads, Lister for day enumeration in
// cleanup — never directly opening a leveldb.DB.
//
// Write path: Ring.Append spawns a goroutine that calls Write; the store
// batches entries under a mutex and flushes via persist.SaveAll (a single
// leveldb batch) when the flush threshold, interval, or day rollover fires.
// This keeps the high-frequency Append path off the caller's lane — the
// only synchronous work is an in-memory slice append.
//
// Read path: Tail and QueryBefore use PrefixScanner snapshot iterators that
// hold no write lock, so reads never block writers (deadlock invariant 2).
//
// Shutdown order: Close sets a closed flag (stop writes), flushes pending,
// then releases the DB handle via the refcounted closer (invariant 6).
type LevelDBLogStore struct {
	db persist.Persist

	mu             sync.Mutex
	pending        []persist.Doc
	currentDate    string
	currentSeq     int
	flushInterval  time.Duration
	flushThreshold int
	lastFlush      time.Time
	closed         bool
	closeOnce      sync.Once
	jsonlDir       string // one-time migration source: <dataDir>/logs/backend/
}

// NewLevelDBLogStore opens a goleveldb-backed log store. The DB is opened
// through persist.New with prefix "logs", so the registry manages the
// refcounted handle. If legacy JSONL files exist under
// <dataDir>/logs/backend/, they are migrated once (idempotent, source files
// not deleted so rollback is a config change).
func NewLevelDBLogStore(dataDir string) (*LevelDBLogStore, error) {
	pc := persist.PersistConfig{
		Backend: persist.BackendLevelDB,
		DataDir: dataDir,
		Prefix:  "logs",
	}
	db, err := persist.New(pc)
	if err != nil {
		return nil, fmt.Errorf("logging: open leveldb: %w", err)
	}

	s := &LevelDBLogStore{
		db:             db,
		flushInterval:  defaultFlushInterval,
		flushThreshold: defaultFlushThreshold,
		lastFlush:      time.Now(),
		jsonlDir:       filepath.Join(dataDir, "logs", "backend"),
	}

	if err := s.migrate(); err != nil {
		// Best-effort: log to stderr but don't fail startup. The JSONL
		// files remain on disk; the user can retry by restarting.
		fmt.Fprintf(os.Stderr, "logging: leveldb migration warning: %v\n", err)
	}

	return s, nil
}

// Write appends a log entry to the batch buffer. The entry is keyed
// log/e/<date>/<seq> where seq is an in-memory per-day counter initialised
// from the last persisted sequence on day rollover. A flush is triggered
// when the pending batch reaches the threshold, the interval elapses, or
// the day rolls over.
func (s *LevelDBLogStore) Write(e gateway.LogEntry) error {
	date := dateOf(e.Timestamp)
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("logging: store closed")
	}

	// Day rollover: flush, then initialise the sequence for the new day
	// from the last persisted entry (invariant: no key collision).
	if date != s.currentDate {
		if err := s.flushLocked(); err != nil {
			return err
		}
		s.currentDate = date
		s.currentSeq = s.lastSeqForDateLocked(date)
	}

	s.currentSeq++
	key := fmt.Sprintf("%s%s/%08d", ldbEntryPrefix, date, s.currentSeq)
	s.pending = append(s.pending, persist.Doc{Name: key, Value: e})

	if len(s.pending) >= s.flushThreshold || time.Since(s.lastFlush) >= s.flushInterval {
		return s.flushLocked()
	}
	return nil
}

// Flush writes any buffered entries to the DB. Safe to call concurrently
// with Write.
func (s *LevelDBLogStore) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushLocked()
}

// flushLocked flushes the pending batch via persist.SaveAll (a single
// atomic leveldb batch). It also writes day markers for every date in the
// batch so cleanup can enumerate days. Caller must hold s.mu.
func (s *LevelDBLogStore) flushLocked() error {
	if len(s.pending) == 0 {
		s.lastFlush = time.Now()
		return nil
	}

	// Collect dates for day markers (idempotent — overwriting is fine).
	dates := make(map[string]bool, 1)
	for _, d := range s.pending {
		// Key format: log/e/<date>/<seq>
		if parts := strings.SplitN(d.Name, "/", 4); len(parts) >= 4 {
			dates[parts[2]] = true
		}
	}

	docs := make([]persist.Doc, 0, len(s.pending)+len(dates))
	docs = append(docs, s.pending...)
	for date := range dates {
		docs = append(docs, persist.Doc{
			Name:  ldbDayPrefix + date,
			Value: map[string]any{},
		})
	}

	if err := persist.SaveAll(s.db, docs); err != nil {
		return fmt.Errorf("logging: flush: %w", err)
	}

	s.pending = s.pending[:0]
	s.lastFlush = time.Now()
	return nil
}

// Tail returns the last n entries in chronological order. It flushes
// pending writes first, then scans via PrefixScanner.ScanPrefixReverse
// (newest-first snapshot, no write lock held).
func (s *LevelDBLogStore) Tail(n int) ([]gateway.LogEntry, error) {
	if n <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	_ = s.flushLocked()
	s.mu.Unlock()

	scanner, ok := s.db.(persist.PrefixScanner)
	if !ok {
		return nil, fmt.Errorf("logging: backend does not support PrefixScanner")
	}

	kvs, err := scanner.ScanPrefixReverse(ldbEntryPrefix, n)
	if err != nil {
		return nil, fmt.Errorf("logging: tail scan: %w", err)
	}

	// kvs are in descending order (newest first); reverse to chronological.
	result := make([]gateway.LogEntry, 0, len(kvs))
	for i := len(kvs) - 1; i >= 0; i-- {
		var e gateway.LogEntry
		if err := json.Unmarshal(kvs[i].Value, &e); err != nil {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}

// QueryBefore returns up to limit entries with Timestamp strictly before
// the given RFC3339Nano value, in chronological order. It scans day by day
// from newest to oldest (via Lister + PrefixScanner) so each scan is
// bounded to a single day's entries, not the entire DB.
func (s *LevelDBLogStore) QueryBefore(before string, limit int) ([]gateway.LogEntry, error) {
	if limit <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	_ = s.flushLocked()
	s.mu.Unlock()

	scanner, ok := s.db.(persist.PrefixScanner)
	if !ok {
		return nil, fmt.Errorf("logging: backend does not support PrefixScanner")
	}

	beforeDate := dateOf(before)
	if beforeDate == "" {
		beforeDate = time.Now().UTC().Format("2006-01-02")
	}

	// Enumerate days with entries, sorted newest-first.
	lister, hasLister := s.db.(persist.Lister)
	var days []string
	if hasLister {
		names, err := lister.List(ldbDayPrefix)
		if err != nil {
			return nil, fmt.Errorf("logging: list days: %w", err)
		}
		for _, name := range names {
			days = append(days, strings.TrimPrefix(name, ldbDayPrefix))
		}
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
	} else {
		// Fallback: scan all entries in reverse and filter (less efficient
		// but correct when Lister is unavailable).
		return s.queryBeforeFallback(scanner, before, limit)
	}

	var result []gateway.LogEntry
	remaining := limit

loop:
	for _, date := range days {
		if date > beforeDate {
			continue // skip days after the target
		}

		dayPrefix := ldbEntryPrefix + date + "/"
		if date == beforeDate {
			// Same day: scan all entries, filter by exact timestamp.
			kvs, err := scanner.ScanPrefixReverse(dayPrefix, 0)
			if err != nil {
				continue
			}
			for _, kv := range kvs {
				var e gateway.LogEntry
				if err := json.Unmarshal(kv.Value, &e); err != nil {
					continue
				}
				if e.Timestamp < before {
					result = append(result, e)
					remaining--
					if remaining <= 0 {
						break loop
					}
				}
			}
		} else {
			// Previous day: every entry qualifies. Scan only what we need
			// (ScanPrefixReverse stops at the limit internally).
			kvs, err := scanner.ScanPrefixReverse(dayPrefix, remaining)
			if err != nil {
				continue
			}
			for _, kv := range kvs {
				var e gateway.LogEntry
				if err := json.Unmarshal(kv.Value, &e); err != nil {
					continue
				}
				result = append(result, e)
				remaining--
				if remaining <= 0 {
					break loop
				}
			}
		}
	}

	// result is in descending order (newest first); reverse to chronological.
	reverse(result)
	return result, nil
}

// queryBeforeFallback scans all entries in reverse and filters by timestamp.
// Used when the backend does not implement Lister.
func (s *LevelDBLogStore) queryBeforeFallback(scanner persist.PrefixScanner, before string, limit int) ([]gateway.LogEntry, error) {
	kvs, err := scanner.ScanPrefixReverse(ldbEntryPrefix, 0)
	if err != nil {
		return nil, fmt.Errorf("logging: query before scan: %w", err)
	}

	var result []gateway.LogEntry
	for _, kv := range kvs {
		var e gateway.LogEntry
		if err := json.Unmarshal(kv.Value, &e); err != nil {
			continue
		}
		if e.Timestamp < before {
			result = append(result, e)
			if len(result) >= limit {
				break
			}
		}
	}

	reverse(result)
	return result, nil
}

// Cleanup removes log entries older than age. It enumerates day markers via
// Lister and deletes each old day's entries + marker via Delete (which
// cascades into the log/e/<date>/ sub-namespace). Best-effort: errors per
// day are skipped.
func (s *LevelDBLogStore) Cleanup(age time.Duration) error {
	cutoffDate := time.Now().Add(-age).UTC().Format("2006-01-02")

	lister, ok := s.db.(persist.Lister)
	if !ok {
		return nil // nothing to do without listing
	}

	days, err := lister.List(ldbDayPrefix)
	if err != nil {
		return fmt.Errorf("logging: list days for cleanup: %w", err)
	}

	for _, name := range days {
		date := strings.TrimPrefix(name, ldbDayPrefix)
		if date >= cutoffDate {
			continue
		}
		// Delete cascades: Delete("log/e/<date>") reclaims the
		// log/e/<date>/ sub-namespace (all entries for that day).
		_ = s.db.Delete(ldbEntryPrefix + date)
		_ = s.db.Delete(ldbDayPrefix + date)
	}
	return nil
}

// Close implements the shutdown order: stop writes → flush → release.
// The closed flag rejects further Write calls; flushLocked drains the
// pending batch; the DB handle is released via the refcounted closer.
func (s *LevelDBLogStore) Close() error {
	var flushErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		flushErr = s.flushLocked()
		s.mu.Unlock()

		// Release the refcounted DB handle.
		if closer, ok := s.db.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
	return flushErr
}

// lastSeqForDateLocked scans the DB for the last entry of the given date and
// returns its sequence number. Used on day rollover to continue numbering
// without colliding with persisted entries. Caller must hold s.mu.
func (s *LevelDBLogStore) lastSeqForDateLocked(date string) int {
	scanner, ok := s.db.(persist.PrefixScanner)
	if !ok {
		return 0
	}
	prefix := ldbEntryPrefix + date + "/"
	kvs, err := scanner.ScanPrefixReverse(prefix, 1)
	if err != nil || len(kvs) == 0 {
		return 0
	}
	// Key format: log/e/<date>/<seq>
	parts := strings.Split(kvs[0].Name, "/")
	if len(parts) < 4 {
		return 0
	}
	seq, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0
	}
	return seq
}

// migrate performs a one-time idempotent import of legacy JSONL files from
// <dataDir>/logs/backend/ into the leveldb DB. It writes the log/migrated
// flag last so a partial migration restarts cleanly. Source files are not
// deleted (rollback = change config back to fs).
func (s *LevelDBLogStore) migrate() error {
	var migrated bool
	if err := s.db.Load(ldbMigratedKey, &migrated); err == nil && migrated {
		return nil // already migrated
	} else if err != nil && !errors.Is(err, persist.ErrNotExist) {
		return fmt.Errorf("logging: check migrated flag: %w", err)
	}

	if _, err := os.Stat(s.jsonlDir); err != nil {
		if os.IsNotExist(err) {
			return s.db.Save(ldbMigratedKey, true) // no JSONL to migrate
		}
		return fmt.Errorf("logging: stat jsonl dir: %w", err)
	}

	entries, err := os.ReadDir(s.jsonlDir)
	if err != nil {
		return fmt.Errorf("logging: read jsonl dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			files = append(files, filepath.Join(s.jsonlDir, e.Name()))
		}
	}
	sort.Strings(files)

	seqByDate := make(map[string]int)
	for _, file := range files {
		if err := s.migrateFile(file, seqByDate); err != nil {
			return fmt.Errorf("logging: migrate %s: %w", filepath.Base(file), err)
		}
	}

	// Write day markers for migrated dates.
	for date := range seqByDate {
		if err := s.db.Save(ldbDayPrefix+date, map[string]any{}); err != nil {
			return fmt.Errorf("logging: write day marker %s: %w", date, err)
		}
	}

	// Write migrated flag last (idempotent restart on crash).
	return s.db.Save(ldbMigratedKey, true)
}

// migrateFile reads a JSONL file and imports entries in batches of
// flushThreshold. Entries are keyed log/e/<date>/<seq> where seq is a
// per-day counter that continues across files (files are sorted
// chronologically).
func (s *LevelDBLogStore) migrateFile(path string, seqByDate map[string]int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var batch []persist.Doc
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e gateway.LogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed entries
		}

		date := dateOf(e.Timestamp)
		if date == "" {
			date = time.Now().UTC().Format("2006-01-02")
		}

		seqByDate[date]++
		key := fmt.Sprintf("%s%s/%08d", ldbEntryPrefix, date, seqByDate[date])
		batch = append(batch, persist.Doc{Name: key, Value: e})

		if len(batch) >= s.flushThreshold {
			if err := persist.SaveAll(s.db, batch); err != nil {
				return fmt.Errorf("flush batch: %w", err)
			}
			batch = batch[:0]
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	if len(batch) > 0 {
		if err := persist.SaveAll(s.db, batch); err != nil {
			return fmt.Errorf("flush remaining: %w", err)
		}
	}

	return nil
}
