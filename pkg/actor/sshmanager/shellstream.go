package sshmanager

import "sync"

// replayMaxBytes caps the total retained recent PTY output so that late
// subscribers (frontend shell_stream) can replay banner/prompt data that
// arrived before they subscribed. The buffer is a rolling window: once the
// cap is exceeded the oldest bursts are evicted.
const replayMaxBytes = 64 * 1024 // 64 KiB

// shellBroadcaster fans the raw PTY byte stream out to streaming subscribers
// (sshmanager.shell_stream handlers). Terminal rendering happens in the
// frontend xterm.js; the backend no longer emulates a terminal.
//
// In addition to live fan-out the broadcaster retains a bounded replay buffer
// of recent output. Each new subscriber receives the buffered bytes in their
// original order before live data, closing the race in which a banner or
// prompt is emitted by the remote shell before the frontend subscribes.
//
// Write is called from the SSH session's stdout/stderr pump goroutines and
// must never block: a subscriber that falls behind is dropped (its channel
// closed) rather than allowed to stall PTY reads.
type shellBroadcaster struct {
	mu     sync.Mutex
	subs   map[chan []byte]struct{}
	closed bool

	// replay is a rolling ring of recent output bursts, capped by total bytes
	// (replayMaxBytes). It captures output that arrived before any subscriber
	// attached so a late shell_stream can replay it in order.
	replay    [][]byte
	replayLen int // current total bytes across replay bursts
}

func newShellBroadcaster() *shellBroadcaster {
	return &shellBroadcaster{subs: make(map[chan []byte]struct{})}
}

// Write implements io.Writer. SSH stdout and stderr share one instance.
func (b *shellBroadcaster) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return len(p), nil
	}
	b.appendReplay(p)
	for ch := range b.subs {
		cp := append([]byte(nil), p...)
		select {
		case ch <- cp:
		default:
			delete(b.subs, ch)
			close(ch)
		}
	}
	return len(p), nil
}

// appendReplay stores a copy of p in the rolling replay buffer, evicting the
// oldest bursts until the total retained bytes is within replayMaxBytes. The
// caller must hold b.mu.
func (b *shellBroadcaster) appendReplay(p []byte) {
	if len(p) == 0 {
		return
	}
	cp := append([]byte(nil), p...)
	b.replay = append(b.replay, cp)
	b.replayLen += len(cp)
	for b.replayLen > replayMaxBytes && len(b.replay) > 0 {
		evicted := b.replay[0]
		b.replay[0] = nil // help GC
		b.replay = b.replay[1:]
		b.replayLen -= len(evicted)
	}
}

// snapshotReplay returns a shallow copy of the current replay burst slice in
// original order. The individual byte slices are safe to share because every
// appendReplay entry is an independent copy. The caller must hold b.mu.
func (b *shellBroadcaster) snapshotReplay() [][]byte {
	if len(b.replay) == 0 {
		return nil
	}
	out := make([][]byte, len(b.replay))
	copy(out, b.replay)
	return out
}

// subscribe returns a channel receiving raw PTY output bursts and a snapshot
// of the replay buffer (in original byte order) that the caller must deliver
// to its consumer before reading live data from the channel.
//
// Ordering guarantee: the replay snapshot and the channel registration happen
// atomically under b.mu, so every byte present in the snapshot was written
// before the channel existed, and every byte arriving on the channel was
// written after. Emitting the snapshot first therefore preserves global PTY
// byte order with no duplicates.
//
// The channel is closed when the session closes or the subscriber is dropped
// for slowness.
func (b *shellBroadcaster) subscribe() (chan []byte, [][]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan []byte)
		close(ch)
		return ch, nil
	}
	ch := make(chan []byte, 256)
	replay := b.snapshotReplay()
	b.subs[ch] = struct{}{}
	return ch, replay
}

// unsubscribe detaches ch without closing it; only Write (slow-consumer drop)
// and close terminate subscriber channels.
func (b *shellBroadcaster) unsubscribe(ch chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, ch)
}

func (b *shellBroadcaster) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for ch := range b.subs {
		delete(b.subs, ch)
		close(ch)
	}
	// Release the replay buffer now that the session is gone.
	b.replay = nil
	b.replayLen = 0
}
