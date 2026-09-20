package sshmanager

import (
	"bytes"
	"testing"
	"time"
)

// simulateSessionBroadcaster mirrors the wiring done in handleShellOpen: a
// shellBroadcaster is created and assigned as the session output sink. It
// returns the session so callers can exercise the subscribe/close lifecycle
// without a live SSH connection.
func simulateSessionBroadcaster(t *testing.T) *session {
	t.Helper()
	out := newShellBroadcaster()
	return &session{
		id:        "test-session",
		out:       out,
		connected: true,
	}
}

// TestSessionReplayBeforeStreamSubscription reproduces the production race: the
// remote shell emits banner/prompt to the broadcaster (via SSH stdout pump)
// before the frontend subscribes to shell_stream. The late subscriber must
// receive the full initial output in order via the replay buffer.
func TestSessionReplayBeforeStreamSubscription(t *testing.T) {
	sess := simulateSessionBroadcaster(t)
	defer sess.close()

	banner := []byte("Last login: Thu Jan  1 00:00:00 2026\r\nWelcome to Ubuntu!\r\n")
	prompt := []byte("root@prod:~# ")

	// Simulate PTY pump writing before any shell_stream subscriber.
	sess.out.Write(banner)
	sess.out.Write(prompt)

	// Frontend subscribes late.
	ch, replay := sess.out.subscribe()
	defer sess.out.unsubscribe(ch)

	var got bytes.Buffer
	for _, chunk := range replay {
		got.Write(chunk)
	}
	want := append(banner, prompt...)
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("session replay lost initial output: got %q want %q", got.String(), string(want))
	}

	// Live data after subscribe arrives on the channel in order, with no gap.
	sess.out.Write([]byte("echo hi\r\n"))
	live := drain(ch, 200*time.Millisecond)
	got.Write(live)
	if !bytes.Contains(got.Bytes(), []byte("echo hi")) {
		t.Fatal("live data after replay missing from channel")
	}
}

// TestSessionCloseReleasesBroadcaster verifies that session.close() tears down
// the broadcaster: replay buffer released and subscriber channels closed.
func TestSessionCloseReleasesBroadcaster(t *testing.T) {
	sess := simulateSessionBroadcaster(t)
	sess.out.Write([]byte("some banner"))

	ch, _ := sess.out.subscribe()

	sess.close()

	// Subscriber channel must be closed.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("subscriber channel should be closed after session close")
		}
	default:
		t.Fatal("subscriber channel should be closed, not open")
	}

	// Replay buffer released.
	sess.out.mu.Lock()
	replayFreed := sess.out.replay == nil && sess.out.replayLen == 0
	closed := sess.out.closed
	sess.out.mu.Unlock()
	if !replayFreed {
		t.Fatal("replay buffer not released after session close")
	}
	if !closed {
		t.Fatal("broadcaster not marked closed after session close")
	}
}

// TestSessionMultipleStreamViewsEachReplay confirms the reconnect scenario:
// when a frontend disconnects and re-subscribes (or a second view opens), each
// subscriber independently receives the replayed initial output.
func TestSessionMultipleStreamViewsEachReplay(t *testing.T) {
	sess := simulateSessionBroadcaster(t)
	defer sess.close()

	sess.out.Write([]byte("INITIAL-BANNER"))

	for i := 0; i < 2; i++ {
		ch, replay := sess.out.subscribe()
		var got bytes.Buffer
		for _, chunk := range replay {
			got.Write(chunk)
		}
		if got.String() != "INITIAL-BANNER" {
			t.Fatalf("reconnect %d: expected replay of initial banner, got %q", i, got.String())
		}
		sess.out.unsubscribe(ch)
	}
}
