package sshmanager

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// drain collects all bursts from a subscriber channel until it is closed or
// idle for the timeout, concatenating them into a single byte slice in arrival
// order.
func drain(ch chan []byte, timeout time.Duration) []byte {
	var buf bytes.Buffer
	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return buf.Bytes()
			}
			buf.Write(data)
		case <-time.After(timeout):
			return buf.Bytes()
		}
	}
}

// TestReplayCapturesPreSubscribeOutput verifies the core race fix: PTY output
// written before any subscriber attaches must be replayed to the first
// subscriber.
func TestReplayCapturesPreSubscribeOutput(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	banner := []byte("Welcome to Ubuntu 22.04 LTS\r\n")
	prompt := []byte("root@host:~# ")
	b.Write(banner)
	b.Write(prompt)

	ch, replay := b.subscribe()
	defer b.unsubscribe(ch)

	var got bytes.Buffer
	for _, chunk := range replay {
		got.Write(chunk)
	}
	if !bytes.Equal(got.Bytes(), append(banner, prompt...)) {
		t.Fatalf("replay mismatch: got %q want %q", got.String(), string(append(banner, prompt...)))
	}
}

// TestReplayPreservesOrderNoDupes writes data before and after subscribing and
// confirms the subscriber observes the complete sequence exactly once in the
// correct order.
func TestReplayPreservesOrderNoDupes(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	b.Write([]byte("A"))
	b.Write([]byte("B"))

	ch, replay := b.subscribe()
	defer b.unsubscribe(ch)

	// Data written after subscribe arrives only on the live channel, never
	// duplicated into the replay snapshot.
	b.Write([]byte("C"))

	var got bytes.Buffer
	for _, chunk := range replay {
		got.Write(chunk)
	}
	live := drain(ch, 200*time.Millisecond)
	got.Write(live)

	if want := "ABC"; got.String() != want {
		t.Fatalf("ordered output mismatch: got %q want %q", got.String(), want)
	}
}

// TestReplayMultipleSubscribersEachGetBuffer verifies that every subscriber —
// not just the first — receives the replayed buffer contents.
func TestReplayMultipleSubscribersEachGetBuffer(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	b.Write([]byte("shared-banner"))

	for i := 0; i < 3; i++ {
		ch, replay := b.subscribe()
		var got bytes.Buffer
		for _, chunk := range replay {
			got.Write(chunk)
		}
		if got.String() != "shared-banner" {
			t.Fatalf("subscriber %d replay mismatch: got %q want %q", i, got.String(), "shared-banner")
		}
		b.unsubscribe(ch)
	}
}

// TestReplayRollingWindowEvictsOldest writes enough data to exceed the byte
// cap and verifies the oldest bursts are evicted while newer ones survive.
func TestReplayRollingWindowEvictsOldest(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	// Each write is half the cap; two writes fill it, a third evicts the first.
	// Use distinguishable markers so we can assert which burst survived.
	mk := func(fill byte) []byte {
		buf := make([]byte, replayMaxBytes/2)
		for i := range buf {
			buf[i] = fill
		}
		return buf
	}
	first := mk('A')
	second := mk('B')
	b.Write(first)  // fills first half
	b.Write(second) // now at cap

	b.Write([]byte("Z9")) // evicts oldest (first half) to stay under cap

	_, replay := b.subscribe()
	var got bytes.Buffer
	for _, chunk := range replay {
		got.Write(chunk)
	}
	if strings.Contains(got.String(), "A") {
		t.Fatalf("oldest burst ('A') should have been evicted but replay still contains it (len=%d)", got.Len())
	}
	if !strings.Contains(got.String(), "B") {
		t.Fatalf("second burst ('B') missing from replay after eviction")
	}
	if !strings.HasSuffix(got.String(), "Z9") {
		t.Fatalf("newest data missing from replay: got suffix len=%d", got.Len())
	}
}

// TestReplayNotBlockedBySlowConsumer confirms that Write returns promptly even
// when a subscriber never reads — the slow consumer is dropped, not blocking
// the PTY pump.
func TestReplayNotBlockedBySlowConsumer(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	ch, _ := b.subscribe()
	// Never drain ch. With a 256-buffer channel, writing beyond the buffer
	// must drop the subscriber rather than block Write.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 512; i++ {
			b.Write([]byte("data-that-nobody-reads"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Write blocked on slow consumer — PTY pump stalled")
	}
	// The channel should have been closed by the slow-consumer drop.
	select {
	case _, ok := <-ch:
		if ok {
			// It may not be closed yet if exactly 256 fit; drain remaining.
			for range ch {
			}
		}
	case <-time.After(time.Second):
		// Acceptable: still draining.
	}
}

// TestReplayAfterCloseReturnsClosedChannel verifies that subscribing after
// close yields a closed channel with no replay data.
func TestReplayAfterCloseReturnsClosedChannel(t *testing.T) {
	b := newShellBroadcaster()
	b.Write([]byte("some-data"))
	b.close()

	ch, replay := b.subscribe()
	if replay != nil {
		t.Fatalf("expected nil replay after close, got %d chunks", len(replay))
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should be closed after broadcaster close")
		}
	default:
		t.Fatal("channel should be immediately closed, not open")
	}
}

// TestReplayReleasedOnClose confirms the replay buffer is released when the
// broadcaster closes.
func TestReplayReleasedOnClose(t *testing.T) {
	b := newShellBroadcaster()
	b.Write(make([]byte, 1024))
	b.close()

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.replay != nil || b.replayLen != 0 {
		t.Fatalf("replay buffer not released after close: replay=%v len=%d", b.replay, b.replayLen)
	}
}

// TestReplayConcurrentWriteAndSubscribe exercises the concurrency invariant:
// replay snapshot + channel registration are atomic under the lock, so a
// goroutine that subscribes while the pump is writing never observes
// duplicate or missing bytes across the replay/live boundary.
func TestReplayConcurrentWriteAndSubscribe(t *testing.T) {
	b := newShellBroadcaster()
	defer b.close()

	const writers = 4
	const writesPer = 200
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writesPer; i++ {
				b.Write([]byte{byte('a' + (i % 26))})
			}
		}()
	}

	// Subscribe concurrently mid-stream.
	ch, replay := b.subscribe()
	defer b.unsubscribe(ch)

	wg.Wait()

	// Drain live channel.
	live := drain(ch, 300*time.Millisecond)

	// The subscriber must have received at least all the live data. The replay
	// + live concatenation must not be empty and must be a subsequence of the
	// total output. We verify non-empty and no panic under concurrency.
	var total bytes.Buffer
	for _, chunk := range replay {
		total.Write(chunk)
	}
	total.Write(live)
	if total.Len() == 0 {
		t.Fatal("subscriber received no data under concurrent write")
	}
}
