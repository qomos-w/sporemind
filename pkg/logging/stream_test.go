package logging

import (
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

// guardedBatch is a concurrency-safe collector of flushed batches for tests.
// Flushes happen on the LogStreamer consumer goroutine, so every read after
// Close (or after a waitForBatchCount) is race-free.
type guardedBatch struct {
	mu      sync.Mutex
	batches [][]StreamEntry
}

func (g *guardedBatch) handler() StreamHandler {
	return func(b []StreamEntry) {
		cp := make([]StreamEntry, len(b))
		copy(cp, b)
		g.mu.Lock()
		g.batches = append(g.batches, cp)
		g.mu.Unlock()
	}
}

func (g *guardedBatch) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.batches)
}

func (g *guardedBatch) all() []StreamEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []StreamEntry
	for _, b := range g.batches {
		out = append(out, b...)
	}
	return out
}

func waitForBatchCount(t *testing.T, get func() int, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if get() >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expected at least %d batches, got %d", want, get())
}

func logEntry(msg string) gateway.LogEntry {
	return gateway.LogEntry{Message: msg, Timestamp: time.Now().UTC().Format(time.RFC3339)}
}

// TestLogStreamerFlushesOnBatchSize: a full batch flushes before the window
// fires. Flush is asynchronous (consumer goroutine), so we poll for it.
func TestLogStreamerFlushesOnBatchSize(t *testing.T) {
	streamer := NewLogStreamer(4, time.Hour) // hour window — won't fire
	defer streamer.Close()
	var g guardedBatch
	streamer.SetHandler(g.handler())
	for i := 0; i < 4; i++ {
		streamer.Push(logEntry("m"), SourceBackend)
	}
	waitForBatchCount(t, g.count, 1, time.Second)
	all := g.all()
	if len(all) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(all))
	}
	for i, e := range all {
		if e.Seq != int64(i+1) {
			t.Fatalf("seq %d: expected %d, got %d", i, i+1, e.Seq)
		}
	}
}

// TestLogStreamerFlushesOnWindow: the time window flushes a partial batch.
func TestLogStreamerFlushesOnWindow(t *testing.T) {
	streamer := NewLogStreamer(100, 50*time.Millisecond) // large batch, small window
	defer streamer.Close()
	var g guardedBatch
	streamer.SetHandler(g.handler())
	streamer.Push(logEntry("a"), SourceBackend)
	streamer.Push(logEntry("b"), SourceBackend)
	waitForBatchCount(t, g.count, 1, time.Second)
	all := g.all()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
}

// TestLogStreamerMultipleBatches: full batches flush as they fill; the
// pending tail flushes on Close.
func TestLogStreamerMultipleBatches(t *testing.T) {
	streamer := NewLogStreamer(3, time.Hour)
	defer streamer.Close()
	var g guardedBatch
	streamer.SetHandler(g.handler())
	for i := 0; i < 7; i++ {
		streamer.Push(logEntry("m"), SourceBackend)
	}
	// 7 entries, batch 3 → two full batches flush asynchronously.
	waitForBatchCount(t, g.count, 2, time.Second)
	streamer.Close() // flushes the pending tail (1 entry)
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(g.batches))
	}
	if len(g.batches[2]) != 1 {
		t.Fatalf("expected last batch size 1, got %d", len(g.batches[2]))
	}
	if g.batches[2][0].Seq != 7 {
		t.Fatalf("expected last seq 7, got %d", g.batches[2][0].Seq)
	}
}

// TestLogStreamerSourceAndSeq: source is preserved and sequence is monotonic.
func TestLogStreamerSourceAndSeq(t *testing.T) {
	streamer := NewLogStreamer(2, time.Hour)
	defer streamer.Close()
	var g guardedBatch
	streamer.SetHandler(g.handler())
	streamer.Push(logEntry("x"), SourceBackend)
	streamer.Push(logEntry("y"), SourceConsole)
	streamer.Close()
	all := g.all()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if all[0].Source != SourceBackend || all[1].Source != SourceConsole {
		t.Fatalf("source mismatch: %q %q", all[0].Source, all[1].Source)
	}
	if all[0].Seq != 1 || all[1].Seq != 2 {
		t.Fatalf("seq mismatch: %d %d", all[0].Seq, all[1].Seq)
	}
}

// TestLogStreamerConcurrentPush exercises concurrent producers (the ring and
// the console store both forward through Push). Every pushed entry must be
// delivered exactly once with monotonic sequence numbers. The channel buffer
// (DefaultStreamChannelCap) exceeds producers*perProd, so drop-oldest does
// not trigger under this load; the contract is best-effort delivery, never
// duplicates.
func TestLogStreamerConcurrentPush(t *testing.T) {
	const producers, perProd = 8, 500
	streamer := NewLogStreamer(13, 20*time.Millisecond)
	defer streamer.Close()
	var g guardedBatch
	streamer.SetHandler(g.handler())
	var wg sync.WaitGroup
	wg.Add(producers)
	for p := 0; p < producers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perProd; i++ {
				streamer.Push(logEntry("c"), SourceBackend)
			}
		}()
	}
	wg.Wait()
	streamer.Close()
	delivered := g.all()
	if got := len(delivered); got != producers*perProd {
		t.Fatalf("expected %d delivered, got %d (drops=%d)", producers*perProd, got, streamer.Drops())
	}
	seen := make(map[int64]bool, len(delivered))
	for _, e := range delivered {
		if e.Seq < 1 || e.Seq > int64(producers*perProd) {
			t.Fatalf("seq out of range: %d", e.Seq)
		}
		if seen[e.Seq] {
			t.Fatalf("duplicate seq: %d", e.Seq)
		}
		seen[e.Seq] = true
	}
}
