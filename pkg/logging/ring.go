package logging

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/qomos-w/gospore/gateway"
)

// Ring is a thread-safe ring buffer of structured log entries.
// It implements gateway.LogProvider for direct wiring into the gateway /logs endpoint.
type Ring struct {
	mu       sync.RWMutex
	entries  []gateway.LogEntry
	max      int
	persist  LogStore
	streamer *LogStreamer
}

// NewRing creates a ring buffer that keeps at most max entries.
func NewRing(max int) *Ring {
	return &Ring{
		entries: make([]gateway.LogEntry, 0, max),
		max:     max,
	}
}

// Append adds an entry to the ring buffer and, if configured, persists it.
func (r *Ring) Append(e gateway.LogEntry) {
	r.mu.Lock()
	r.entries = append(r.entries, e)
	if len(r.entries) > r.max {
		r.entries = r.entries[len(r.entries)-r.max:]
	}
	persist := r.persist
	streamer := r.streamer
	r.mu.Unlock()
	// Persist in a background goroutine so disk I/O never blocks the caller.
	// This is critical: the hook runs on the actor's goroutine, and blocking
	// I/O here would stall the entire actor (including child agent turns).
	if persist != nil {
		go func() { _ = persist.Write(e) }()
	}
	// Forward to the live streamer. Push is a single non-blocking channel send
	// (drop-oldest under backpressure), so a log producer can never stall here
	// even when the caller holds another lock (e.g. the EventBus write lock
	// during subscribe). The streamer batches and flushes on its own consumer
	// goroutine — the handler never runs on the producer's stack.
	if streamer != nil {
		streamer.Push(e, SourceBackend)
	}
}

// SetStreamer wires a batched live-log streamer so every appended entry is
// forwarded with source "backend". May be set before or after the ring is
// used; nil detaches.
func (r *Ring) SetStreamer(s *LogStreamer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamer = s
}

// SetPersist wires a log store so every appended entry is written to disk.
func (r *Ring) SetPersist(s LogStore) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persist = s
}

// Restore replaces the in-memory entries with the provided chronological list.
// It is used at startup to hydrate the ring from persisted history.
func (r *Ring) Restore(entries []gateway.LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = entries
	if len(r.entries) > r.max {
		r.entries = r.entries[len(r.entries)-r.max:]
	}
}

// All returns a copy of all entries in chronological order.
func (r *Ring) All() []gateway.LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]gateway.LogEntry, len(r.entries))
	copy(out, r.entries)
	return out
}

// QueryLogs implements gateway.LogProvider.
func (r *Ring) QueryLogs(opts gateway.LogQuery) []gateway.LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	limit := opts.Limit
	if limit <= 0 {
		limit = 200
	}

	var result []gateway.LogEntry
	for i := len(r.entries) - 1; i >= 0; i-- {
		e := r.entries[i]
		if opts.Level != "" && !strings.EqualFold(e.Level, opts.Level) {
			continue
		}
		if opts.Caller != "" && !strings.Contains(e.Caller, opts.Caller) {
			continue
		}
		result = append(result, e)
		if len(result) >= limit {
			break
		}
	}
	slices.Reverse(result)
	return result
}

// Tail returns the last n persisted log entries, falling back to the
// in-memory ring when no file store is configured.
func (r *Ring) Tail(n int) ([]gateway.LogEntry, error) {
	r.mu.RLock()
	persist := r.persist
	r.mu.RUnlock()

	if persist != nil {
		return persist.Tail(n)
	}

	all := r.All()
	if n <= 0 || n >= len(all) {
		return all, nil
	}
	return all[len(all)-n:], nil
}

// QueryBefore returns up to limit entries with Timestamp strictly before the
// given RFC3339Nano value, in chronological order. It delegates to the file
// store when available; otherwise it filters the in-memory ring.
func (r *Ring) QueryBefore(before string, limit int) ([]gateway.LogEntry, error) {
	r.mu.RLock()
	persist := r.persist
	r.mu.RUnlock()

	if persist != nil {
		return persist.QueryBefore(before, limit)
	}

	if limit <= 0 {
		limit = 200
	}
	all := r.All()
	var result []gateway.LogEntry
	for i := len(all) - 1; i >= 0 && len(result) < limit; i-- {
		if all[i].Timestamp < before {
			result = append(result, all[i])
		}
	}
	slices.Reverse(result)
	return result, nil
}

// fieldsMatch checks that all key-value pairs in want exist in fields.
func fieldsMatch(fields map[string]any, want map[string]string) bool {
	for k, v := range want {
		fv, ok := fields[k]
		if !ok || fmt.Sprint(fv) != v {
			return false
		}
	}
	return true
}
