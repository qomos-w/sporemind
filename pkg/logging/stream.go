package logging

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

const (
	// DefaultStreamBatchSize is the entry count at which a pending batch is
	// flushed even if the time window has not elapsed.
	DefaultStreamBatchSize = 64
	// DefaultStreamWindow is the maximum time the first entry of a batch may
	// wait for coalescing before the batch is flushed.
	DefaultStreamWindow = 100 * time.Millisecond
	// DefaultStreamChannelCap is the capacity of the producer→consumer
	// channel. Sized to absorb log bursts (e.g. a web-client reconnect
	// subscribe storm that logs dozens of "eventbus: Subscribe" lines) without
	// blocking producers. Under sustained backpressure that exceeds this cap,
	// excess live entries are dropped (drop-oldest); persisted history in the
	// ring/file store is unaffected — only live subscribers miss a tail.
	DefaultStreamChannelCap = 8192
)

// SourceBackend and SourceConsole identify the origin stream of a log entry
// in the live log event; they match the Source values accepted by
// workspace.logs_query.
const (
	SourceBackend = "backend"
	SourceConsole = "console"
)

// StreamEntry is one log entry delivered to a live stream subscriber. It
// pairs the underlying gateway log entry with its origin source and a
// batcher-assigned monotonic sequence number used for in-session
// ordering/dedup. Backward paging still uses Timestamp via
// workspace.logs_query Before/NextBefore.
type StreamEntry struct {
	gateway.LogEntry
	Source string
	Seq    int64
}

// StreamHandler receives a flushed batch of entries. It is invoked ONLY on
// the LogStreamer's single consumer goroutine — never on a producer's stack.
// Implementations must be cheap and non-blocking (typically an event-bus
// publish or channel send).
type StreamHandler func(batch []StreamEntry)

// LogStreamer forwards log entries from many producers to a single consumer
// goroutine that batches and flushes them to a handler.
//
// Producers call Push, which performs one non-blocking channel send
// (drop-oldest when the channel is saturated). Push therefore never blocks
// and never invokes the handler on the producer's goroutine. This is
// load-bearing: a log entry is often produced while the caller holds an
// unrelated lock (notably the EventBus write lock during subscribe), and the
// handler re-enters that same lock (HasEventSubscribers →
// SubscriberCountByInstance → RLock, and EmitEvent → Publish). Running the
// handler synchronously on the producer's stack would re-enter the RWMutex
// while its write lock is held and deadlock the bus against itself. Routing
// every entry through the consumer goroutine makes that impossible: the
// handler always runs on a goroutine that holds no producer lock.
type LogStreamer struct {
	ch       chan StreamEntry
	flushCh  chan struct{}
	maxBatch int
	window   time.Duration
	seq      atomic.Int64
	dropped  atomic.Int64

	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once

	handlerMu sync.RWMutex
	handler   StreamHandler
}

// NewLogStreamer creates a batcher that flushes when a pending batch reaches
// maxBatch entries or after window elapses. The consumer goroutine is started
// here; Close stops it (draining and flushing the tail). Non-positive values
// fall back to DefaultStreamBatchSize / DefaultStreamWindow.
func NewLogStreamer(maxBatch int, window time.Duration) *LogStreamer {
	if maxBatch <= 0 {
		maxBatch = DefaultStreamBatchSize
	}
	if window <= 0 {
		window = DefaultStreamWindow
	}
	s := &LogStreamer{
		ch:       make(chan StreamEntry, DefaultStreamChannelCap),
		flushCh:  make(chan struct{}, 1),
		maxBatch: maxBatch,
		window:   window,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go s.run()
	return s
}

// SetHandler installs the batch delivery handler. It may be replaced at any
// time; a flush in flight uses the handler observed when it drained.
func (s *LogStreamer) SetHandler(h StreamHandler) {
	s.handlerMu.Lock()
	s.handler = h
	s.handlerMu.Unlock()
}

func (s *LogStreamer) handlerSnapshot() StreamHandler {
	s.handlerMu.RLock()
	defer s.handlerMu.RUnlock()
	return s.handler
}

// Push appends an entry to the live stream, assigning it the next monotonic
// sequence number. It is non-blocking: if the internal channel is saturated
// under sustained backpressure, the entry is dropped from the live stream
// only (persisted history in the ring/file store is unaffected).
func (s *LogStreamer) Push(e gateway.LogEntry, source string) {
	seq := s.seq.Add(1)
	select {
	case s.ch <- StreamEntry{LogEntry: e, Source: source, Seq: seq}:
	default:
		s.dropped.Add(1)
	}
}

// Drops returns the count of live entries dropped under backpressure.
func (s *LogStreamer) Drops() int64 { return s.dropped.Load() }

// run is the single consumer goroutine owning batching and handler delivery.
// It exits when the channel is closed or stop is signalled, draining and
// flushing any buffered tail first.
func (s *LogStreamer) run() {
	defer close(s.stopped)
	batch := make([]StreamEntry, 0, s.maxBatch)
	var timer *time.Timer

	flush := func() {
		if len(batch) == 0 {
			return
		}
		out := batch
		batch = make([]StreamEntry, 0, s.maxBatch)
		if h := s.handlerSnapshot(); h != nil {
			h(out)
		}
	}
	stopTimer := func() {
		if timer != nil {
			timer.Stop()
			timer = nil
		}
	}
	armTimer := func() {
		if timer == nil {
			timer = time.AfterFunc(s.window, func() {
				select {
				case s.flushCh <- struct{}{}:
				default:
				}
			})
		}
	}

	for {
		select {
		case e, ok := <-s.ch:
			if !ok {
				stopTimer()
				flush()
				return
			}
			batch = append(batch, e)
			if len(batch) >= s.maxBatch {
				stopTimer()
				flush()
			} else {
				armTimer()
			}
		case <-s.flushCh:
			stopTimer()
			flush()
		case <-s.stop:
			stopTimer()
			// Drain whatever producers already buffered, flushing at
			// maxBatch, then flush the tail and exit.
			draining := true
			for draining {
				select {
				case e := <-s.ch:
					batch = append(batch, e)
					if len(batch) >= s.maxBatch {
						flush()
					}
				default:
					draining = false
				}
			}
			flush()
			return
		}
	}
}

// Close signals the consumer to stop, draining and flushing the pending tail.
// Producers may still Push afterwards; those entries are simply dropped (the
// channel is not closed, so sends never panic). Callers wanting a hard stop
// should SetHandler(nil) first.
func (s *LogStreamer) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.stopped
}
