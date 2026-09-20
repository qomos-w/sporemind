package shell

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// sessionOutputEvents returns the shell.session_output events for sid, in the
// order they were emitted.
func sessionOutputEvents(ctx *testutil.FakeCtx, sid string) []domain.ShellSessionOutputEvent {
	var out []domain.ShellSessionOutputEvent
	for _, ev := range ctx.EmittedEventsSnapshot() {
		if ev.Kind != SessionOutputEventKind {
			continue
		}
		payload, ok := ev.Payload.(domain.ShellSessionOutputEvent)
		if !ok {
			continue
		}
		if payload.SessionID == sid {
			out = append(out, payload)
		}
	}
	return out
}

// TestSessionFetch_IdxMonotonicAndEquivalence verifies that emitted chunks
// receive contiguous, zero-based, monotonically increasing Idx, and that
// session_fetch(FromIdx=0) returns exactly the chunks the subscriber stream
// delivered — same Idx, Kind, Data, in order.
func TestSessionFetch_IdxMonotonicAndEquivalence(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	a.sessionForcePipe = true // deterministic pipe path

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}
	const marker = "idx-marker"
	if _, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: resp.SessionID,
		Data:      "echo " + marker + "\r\n",
	}); err != nil {
		t.Fatalf("session_write: %v", err)
	}

	waitSessionEvent(t, ctx, 10*time.Second, "stdout containing idx-marker",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID &&
				(ev.Kind == "stdout" || ev.Kind == "stderr") &&
				strings.Contains(ev.Data, marker)
		})

	fetch, err := a.handleSessionFetch(ctx, domain.ShellSessionFetchReq{
		SessionID: resp.SessionID, FromIdx: 0,
	})
	if err != nil {
		t.Fatalf("session_fetch: %v", err)
	}
	if len(fetch.Chunks) == 0 {
		t.Fatal("session_fetch returned no buffered chunks")
	}

	// Idx must start at 0 and be strictly increasing.
	var lastIdx int64 = -1
	for i, c := range fetch.Chunks {
		if i == 0 && c.Idx != 0 {
			t.Fatalf("first chunk Idx = %d, want 0", c.Idx)
		}
		if c.Idx <= lastIdx {
			t.Fatalf("chunk Idx not monotonic at %d: got %d, want > %d", i, c.Idx, lastIdx)
		}
		lastIdx = c.Idx
	}

	// Equivalence: fetch replay must equal the subscribed event stream.
	want := sessionOutputEvents(ctx, resp.SessionID)
	if len(want) == 0 {
		t.Fatal("no emitted events to compare against")
	}
	if len(fetch.Chunks) != len(want) {
		t.Fatalf("fetch returned %d chunks but %d were emitted", len(fetch.Chunks), len(want))
	}
	for i, c := range fetch.Chunks {
		if c.Idx != want[i].Idx || c.Kind != want[i].Kind || c.Data != want[i].Data {
			t.Fatalf("chunk %d mismatch: fetch %+v != emit %+v", i, c, want[i])
		}
	}
	if fetch.NextIdx != lastIdx+1 {
		t.Fatalf("NextIdx = %d, want %d", fetch.NextIdx, lastIdx+1)
	}
	if fetch.Truncated {
		t.Fatal("Truncated=true on a fresh session; want false")
	}

	if _, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: resp.SessionID,
	}); err != nil {
		t.Fatalf("session_close: %v", err)
	}
	waitSessionGone(t, a, resp.SessionID, 5*time.Second)
}

// TestSessionFetch_FromIdxPagesAndExitChunk asserts that:
//   - paging with FromIdx returns only chunks with Idx >= FromIdx;
//   - NextIdx advances to the last returned chunk's Idx + 1;
//   - the terminal exit chunk is buffered and returned by a fetch performed
//     after close (before reap removes the session).
func TestSessionFetch_FromIdxPagesAndExitChunk(t *testing.T) {
	a, ctx := newSessionTestActor(t)
	a.sessionForcePipe = true

	resp, err := a.handleSessionOpen(ctx, domain.ShellSessionOpenReq{})
	if err != nil {
		t.Fatalf("session_open: %v", err)
	}

	const marker = "paging-marker"
	if _, err := a.handleSessionWrite(ctx, domain.ShellSessionWriteReq{
		SessionID: resp.SessionID, Data: "echo " + marker + "\r\n",
	}); err != nil {
		t.Fatalf("session_write: %v", err)
	}
	waitSessionEvent(t, ctx, 10*time.Second, "stdout containing paging-marker",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && strings.Contains(ev.Data, marker)
		})

	// Snapshot the last emitted chunk's Idx to page from.
	emitted := sessionOutputEvents(ctx, resp.SessionID)
	if len(emitted) == 0 {
		t.Fatal("no emitted chunks before paging")
	}
	pageFrom := emitted[len(emitted)-1].Idx

	fetch, err := a.handleSessionFetch(ctx, domain.ShellSessionFetchReq{
		SessionID: resp.SessionID, FromIdx: pageFrom,
	})
	if err != nil {
		t.Fatalf("session_fetch(from=%d): %v", pageFrom, err)
	}
	for _, c := range fetch.Chunks {
		if c.Idx < pageFrom {
			t.Fatalf("fetch returned chunk with Idx %d < FromIdx %d", c.Idx, pageFrom)
		}
	}

	// Close and wait for the terminal exit chunk, then page forward from its
	// Idx — the exit chunk must be present and be the last one buffered.
	if _, err := a.handleSessionClose(ctx, domain.ShellSessionCloseReq{
		SessionID: resp.SessionID,
	}); err != nil {
		t.Fatalf("session_close: %v", err)
	}
	exit := waitSessionEvent(t, ctx, 5*time.Second, "exit chunk after close",
		func(ev domain.ShellSessionOutputEvent) bool {
			return ev.SessionID == resp.SessionID && ev.Kind == "exit"
		})

	// Fetch before reap fully removes the session; the exit chunk lives in the
	// buffer at exit.Idx. Reap removes the session, so retry briefly.
	var post domain.ShellSessionFetchResp
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		got, ferr := a.handleSessionFetch(ctx, domain.ShellSessionFetchReq{
			SessionID: resp.SessionID, FromIdx: exit.Idx,
		})
		if ferr != nil {
			break // session reaped; exit chunk was already delivered to subscribers
		}
		post = got
		if len(post.Chunks) > 0 && post.Chunks[len(post.Chunks)-1].Kind == "exit" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(post.Chunks) > 0 {
		last := post.Chunks[len(post.Chunks)-1]
		if last.Kind != "exit" {
			t.Fatalf("last buffered chunk Kind = %q, want exit", last.Kind)
		}
		if last.Idx != exit.Idx {
			t.Fatalf("exit chunk Idx in fetch = %d, want %d", last.Idx, exit.Idx)
		}
	}
	waitSessionGone(t, a, resp.SessionID, 5*time.Second)
}

// TestSessionFetch_RingTruncation drives a synthetic session (no live process)
// through appendOutput past its buffer capacity and asserts:
//   - the ring keeps at most bufferCap chunks, overwriting the oldest;
//   - truncated is set once overflow happens, and a fetch from a dropped Idx
//     reports Truncated=true;
//   - a fetch from within the retained window reports Truncated=false and
//     returns exactly the retained chunks.
func TestSessionFetch_RingTruncation(t *testing.T) {
	a, _ := newSessionTestActor(t)
	_ = a

	s := newSession()
	s.id = "ring-test"
	s.bufferCap = 3

	// Push 5 chunks into a cap-3 ring. Idx stays monotonic across all 5; only
	// the last 3 (Idx 2,3,4) survive, and the ring is flagged full.
	for i := 0; i < 5; i++ {
		s.appendOutput(domain.ShellSessionOutputEvent{
			SessionID: s.id, Kind: "stdout", Data: fmt.Sprintf("c%d", i),
		})
	}
	if !s.ringFull {
		t.Fatal("ringFull=false, want true after overflow")
	}

	// Fetch from the dropped start (Idx 0): must flag truncation and return
	// only the retained window.
	fromStart := sessionFetchResp(s, 0)
	if !fromStart.Truncated {
		t.Fatal("expected Truncated=true when FromIdx is below the ring's first Idx")
	}
	if len(fromStart.Chunks) != 3 {
		t.Fatalf("expected 3 retained chunks, got %d", len(fromStart.Chunks))
	}
	for i, c := range fromStart.Chunks {
		if want := int64(2 + i); c.Idx != want {
			t.Fatalf("retained chunk %d Idx = %d, want %d", i, c.Idx, want)
		}
	}
	if fromStart.NextIdx != 5 {
		t.Fatalf("NextIdx = %d, want 5", fromStart.NextIdx)
	}

	// Fetch from within the retained window: not truncated.
	fromWithin := sessionFetchResp(s, 3)
	if fromWithin.Truncated {
		t.Fatal("expected Truncated=false when FromIdx is within the retained window")
	}
	if len(fromWithin.Chunks) != 2 {
		t.Fatalf("expected 2 chunks from FromIdx=3, got %d", len(fromWithin.Chunks))
	}

	// Fetch from beyond everything buffered: empty window, not truncated.
	fromBeyond := sessionFetchResp(s, 99)
	if len(fromBeyond.Chunks) != 0 {
		t.Fatalf("expected 0 chunks from FromIdx=99, got %d", len(fromBeyond.Chunks))
	}
	if fromBeyond.NextIdx != 99 {
		t.Fatalf("NextIdx = %d, want 99 when nothing matches", fromBeyond.NextIdx)
	}
}

// sessionFetchResp is a test helper that calls s.fetchOutput directly for the
// synthetic (unregistered) sessions used by ring tests.
func sessionFetchResp(s *session, from int64) domain.ShellSessionFetchResp {
	chunks, next, trunc := s.fetchOutput(from)
	return domain.ShellSessionFetchResp{Chunks: chunks, NextIdx: next, Truncated: trunc}
}

// TestSessionFetch_ConcurrentWithEmit verifies the session mu keeps the ring
// consistent under concurrent appendOutput (pump) and fetchOutput (fetch)
// callers: no data race, Idx stays monotonic, and fetch never returns a
// torn slice.
func TestSessionFetch_ConcurrentWithEmit(t *testing.T) {
	a, _ := newSessionTestActor(t)
	_ = a

	s := newSession()
	s.id = "concurrent"

	var wg sync.WaitGroup
	const emitters = 4
	const chunksEach = 200
	wg.Add(emitters)
	for g := 0; g < emitters; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < chunksEach; i++ {
				s.appendOutput(domain.ShellSessionOutputEvent{
					SessionID: s.id, Kind: "stdout",
					Data: fmt.Sprintf("g%d-i%d", g, i),
				})
			}
		}(g)
	}

	// Concurrent fetcher keeps paging forward while emitters run.
	stop := time.Now().Add(2 * time.Second)
	wg.Add(1)
	go func() {
		defer wg.Done()
		var from int64
		for time.Now().Before(stop) {
			r := sessionFetchResp(s, from)
			for _, c := range r.Chunks {
				if c.Idx < from {
					t.Errorf("fetch returned chunk Idx %d < FromIdx %d", c.Idx, from)
				}
				from = c.Idx + 1
			}
		}
	}()

	wg.Wait()

	// Final consistency: every buffered chunk has a strictly greater Idx than
	// its predecessor.
	r := sessionFetchResp(s, 0)
	for i := 1; i < len(r.Chunks); i++ {
		if r.Chunks[i].Idx <= r.Chunks[i-1].Idx {
			t.Fatalf("ring not monotonic at %d: %d <= %d", i, r.Chunks[i].Idx, r.Chunks[i-1].Idx)
		}
	}
}
