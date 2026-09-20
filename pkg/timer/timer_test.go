package timer

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── test-clock helpers ────────────────────────────────────────────────────────

// fakeClock lets tests drive time manually.
type fakeClock struct {
	now time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (f *fakeClock) Now() time.Time          { return f.now }
func (f *fakeClock) Get10Ms() time.Duration  { return time.Duration(f.now.UnixNano() / int64(time.Millisecond) / 10) }
func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }

// driveWheel advances the wheel by d, calling run every 10 ms.
func driveWheel(tw *timeWheel, clk *fakeClock, d time.Duration) {
	steps := int(d / (10 * time.Millisecond))
	for i := 0; i < steps; i++ {
		clk.Advance(10 * time.Millisecond)
		tw.run(clk.Get10Ms)
	}
}

// waitFor polls cond until it holds or a timeout elapses. Callbacks are
// dispatched on their own goroutine (wheel over-load fix #2), so tests must
// await them instead of assuming a fire is observable the moment a tick ran.
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

// newTestWheel creates a time wheel with a fake clock already attached.
func newTestWheel() (*timeWheel, *fakeClock) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local))
	SetTimeTestHandler(clk)
	tw := newTimeWheel()
	return tw, clk
}

func TestNext(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()

	// One-shot timer reports its fire time before firing.
	oneshot := h.After(50*time.Millisecond, func(TimeNoder) {})
	if got := oneshot.Next(); got != clk.Now().Add(50*time.Millisecond) {
		t.Errorf("one-shot Next = %v, want %v", got, clk.Now().Add(50*time.Millisecond))
	}

	// Interval timer reports the next fire time relative to the wheel now.
	interval := h.Schedule(30*time.Millisecond, func(TimeNoder) {})
	want := clk.Now().Add(30 * time.Millisecond)
	if got := interval.Next(); got != want {
		t.Errorf("interval Next = %v, want %v", got, want)
	}

	// Advance time; the next fire time shifts relative to the wheel now.
	clk.Advance(10 * time.Millisecond)
	want = tw.Now().Add(30 * time.Millisecond)
	if got := interval.Next(); got != want {
		t.Errorf("interval Next after advance = %v, want %v", got, want)
	}

	// Stopped node returns zero time.
	interval.Stop()
	if got := interval.Next(); !got.IsZero() {
		t.Errorf("stopped interval Next = %v, want zero", got)
	}
}

// ── After ─────────────────────────────────────────────────────────────────────

func TestAfter_FiresOnce(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	var fired int32
	h.After(50*time.Millisecond, func(TimeNoder) {
		atomic.AddInt32(&fired, 1)
	})

	// Not fired yet after 40 ms.
	driveWheel(tw, clk, 40*time.Millisecond)
	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Fatalf("timer fired too early: %d", got)
	}

	// Fired exactly once after another 20 ms (callback runs async).
	driveWheel(tw, clk, 20*time.Millisecond)
	waitFor(t, "one-shot fire", func() bool { return atomic.LoadInt32(&fired) == 1 })

	// Does not fire again.
	driveWheel(tw, clk, 100*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("one-shot timer fired again: total=%d", got)
	}
}

// ── Schedule ──────────────────────────────────────────────────────────────────

func TestSchedule_Repeats(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	var count int32
	h.Schedule(30*time.Millisecond, func(TimeNoder) {
		atomic.AddInt32(&count, 1)
	})

	driveWheel(tw, clk, 100*time.Millisecond)
	waitFor(t, "repeat fires >=2", func() bool { return atomic.LoadInt32(&count) >= 2 })
	time.Sleep(5 * time.Millisecond)
	got := atomic.LoadInt32(&count)
	// Expect 2 or 3 firings (time wheel has inherent +1 tick jitter, so
	// effective period is 40 ms; fires at ~40 ms and ~80 ms = 2 times,
	// occasionally 3 depending on initial jiffies alignment).
	if got < 2 || got > 3 {
		t.Errorf("Schedule(30ms) fired %d times in 100ms, want 2-3", got)
	}
}

func TestSchedule_WithLoop(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	var count int32
	h.Schedule(20*time.Millisecond, func(TimeNoder) {
		atomic.AddInt32(&count, 1)
	}, WithLoop(3))

	driveWheel(tw, clk, 200*time.Millisecond)
	waitFor(t, "WithLoop(3) fires", func() bool { return atomic.LoadInt32(&count) == 3 })
	time.Sleep(5 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 3 {
		t.Errorf("WithLoop(3) fired %d times, want 3", got)
	}
}

// ── Stop ──────────────────────────────────────────────────────────────────────

func TestStop_PreventsCallback(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	var fired int32
	node := h.After(50*time.Millisecond, func(TimeNoder) {
		atomic.AddInt32(&fired, 1)
	})

	driveWheel(tw, clk, 20*time.Millisecond)
	node.Stop()
	driveWheel(tw, clk, 100*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	if got := atomic.LoadInt32(&fired); got != 0 {
		t.Fatalf("stopped timer fired: %d", got)
	}
}

func TestStop_Idempotent(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	node := h.After(50*time.Millisecond, func(TimeNoder) {})
	// Stopping twice must not panic.
	node.Stop()
	node.Stop()
	driveWheel(tw, clk, 10*time.Millisecond)
}

// ── StopTimer (handler-level) ─────────────────────────────────────────────────

func TestStopTimer_StopsAll(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	var count int32
	cb := func(TimeNoder) { atomic.AddInt32(&count, 1) }
	h.After(30*time.Millisecond, cb)
	h.After(40*time.Millisecond, cb)
	h.After(50*time.Millisecond, cb)

	driveWheel(tw, clk, 20*time.Millisecond)
	h.StopTimer()
	driveWheel(tw, clk, 100*time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	if got := atomic.LoadInt32(&count); got != 0 {
		t.Errorf("StopTimer: %d callbacks fired, want 0", got)
	}
}

// ── Multiple independent handlers ─────────────────────────────────────────────

func TestMultipleHandlers_Independent(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h1 := tw.NewHandler()
	h2 := tw.NewHandler()
	var c1, c2 int32

	h1.Schedule(20*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&c1, 1) })
	h2.Schedule(50*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&c2, 1) })

	driveWheel(tw, clk, 100*time.Millisecond)

	waitFor(t, "h1 fires", func() bool { return atomic.LoadInt32(&c1) >= 3 })
	waitFor(t, "h2 fires", func() bool { return atomic.LoadInt32(&c2) >= 1 })
	if got := atomic.LoadInt32(&c1); got < 3 {
		t.Errorf("h1 (20ms) only fired %d times in 100ms", got)
	}
	if got := atomic.LoadInt32(&c2); got < 1 {
		t.Errorf("h2 (50ms) only fired %d times in 100ms", got)
	}
	h1.DelTimer()
	h2.DelTimer()
}

// ── Cron parse: valid expression does not panic ────────────────────────────────

func TestCron_ValidParse(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()
	_ = clk

	h := tw.NewHandler()
	defer h.DelTimer()

	// Every second, every minute, every hour, day-of-month ignore, every month, every weekday.
	// Should not panic during parse.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("valid cron expression panicked: %v", r)
		}
	}()
	if _, err := h.Cron("*", "*", "*", "?", "*", "*", func(TimeNoder) {}); err != nil {
		t.Fatalf("valid cron expression returned error: %v", err)
	}
}

// ── Cron parse: invalid expression returns an error without crashing ──────────

func TestCron_InvalidReturnsError(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()
	_ = clk

	h := tw.NewHandler()
	defer h.DelTimer()

	// Two '?' is invalid; it must be reported as an error, never a panic, so a
	// bad schedule cannot bring down the scheduler actor.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid cron expression panicked: %v", r)
		}
	}()
	if _, err := h.Cron("*", "*", "*", "?", "*", "?", func(TimeNoder) {}); err == nil {
		t.Fatal("expected error for cron with two '?'")
	}
}

// ── GetCallback / GetDelay / GetInterval ──────────────────────────────────────

func TestNode_Accessors(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()
	_ = clk

	h := tw.NewHandler()
	cb := func(TimeNoder) {}
	node := h.Schedule(100*time.Millisecond, cb, WithDelay(200*time.Millisecond))

	if node.GetInterval() != 100*time.Millisecond {
		t.Errorf("GetInterval=%v, want 100ms", node.GetInterval())
	}
	if node.GetDelay() != 200*time.Millisecond {
		t.Errorf("GetDelay=%v, want 200ms", node.GetDelay())
	}
	if node.GetCallback() == nil {
		t.Error("GetCallback should not be nil")
	}
}

// ── wheel stop/reinsert semantics (#1) ────────────────────────────────────────

func TestSchedule_StopInsideCallbackCancelsNextFire(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()

	var count int32
	stopped := make(chan struct{})
	h.Schedule(20*time.Millisecond, func(n TimeNoder) {
		if atomic.AddInt32(&count, 1) == 1 {
			n.Stop()
			close(stopped)
		}
	})

	driveWheel(tw, clk, 30*time.Millisecond)
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("first fire never ran")
	}

	driveWheel(tw, clk, 200*time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("Stop() inside callback did not cancel next fire: fired %d times, want 1", got)
	}
}

// ── wheel over-load (#2) ──────────────────────────────────────────────────────

func TestWheel_SlowCallbackDoesNotStallOthers(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()

	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	var second int32
	h.After(10*time.Millisecond, func(TimeNoder) {
		close(started)
		<-release
	})
	h.After(50*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&second, 1) })

	driveWheel(tw, clk, 20*time.Millisecond)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow callback never started")
	}

	// The first callback is still blocked; the wheel must keep firing the rest.
	driveWheel(tw, clk, 60*time.Millisecond)
	waitFor(t, "timer behind a slow callback", func() bool { return atomic.LoadInt32(&second) == 1 })
}

// ── curTimePoint concurrency (#3) ─────────────────────────────────────────────

func TestWheel_ConcurrentNowDoesNotRace(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()
	h.Schedule(20*time.Millisecond, func(TimeNoder) {})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = tw.Now()
			}
		}
	}()

	driveWheel(tw, clk, 100*time.Millisecond)
	close(stop)
	wg.Wait()
}

// ── past / sub-tick delays (#4, #5) ───────────────────────────────────────────

func TestAt_PastTimeFiresSoonNotDaysLater(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()
	var fired int32
	h.At(clk.Now().Add(-time.Hour), func(TimeNoder) { atomic.AddInt32(&fired, 1) })

	driveWheel(tw, clk, 30*time.Millisecond)
	waitFor(t, "past At to fire on the next tick", func() bool { return atomic.LoadInt32(&fired) == 1 })
}

func TestAfter_SubTenMsFiresWithinOneTick(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()
	var fired int32
	h.After(5*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&fired, 1) })

	driveWheel(tw, clk, 30*time.Millisecond)
	waitFor(t, "sub-10ms After", func() bool { return atomic.LoadInt32(&fired) == 1 })
}

func TestSchedule_WithLoopOneFiresOnce(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()
	var count int32
	h.Schedule(20*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&count, 1) }, WithLoop(1))

	driveWheel(tw, clk, 200*time.Millisecond)
	waitFor(t, "one-shot schedule", func() bool { return atomic.LoadInt32(&count) == 1 })
	time.Sleep(5 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("WithLoop(1) fired %d times, want 1", got)
	}
}

// ── clock jumps (#6) ──────────────────────────────────────────────────────────

func TestRun_BackwardClockJumpDoesNotRebase(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()
	tw.run(clk.Get10Ms)
	jiffies := tw.jiffies
	clock := tw.curTimePoint.Load()

	clk.now = clk.now.Add(-time.Hour)
	tw.run(clk.Get10Ms)

	if tw.jiffies != jiffies {
		t.Errorf("backward jump advanced jiffies from %d to %d", jiffies, tw.jiffies)
	}
	if got := tw.curTimePoint.Load(); got != clock {
		t.Errorf("backward jump rebased curTimePoint from %d to %d", clock, got)
	}
}

func TestRun_ForwardClockJumpIsClamped(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()
	tw.run(clk.Get10Ms)
	before := tw.jiffies

	clk.now = clk.now.Add(time.Hour)
	tw.run(clk.Get10Ms)

	advanced := tw.jiffies - before
	if advanced == 0 {
		t.Fatal("forward jump did not advance the wheel")
	}
	if advanced > 100 {
		t.Errorf("forward jump replayed %d ticks in one run, want <= 100", advanced)
	}
}

// ── Cron error/timezone/second-boundary (#7, #8, #11) ─────────────────────────

func TestCron_OutOfRangeReturnsError(t *testing.T) {
	tw, _ := newTestWheel()
	defer tw.Stop()
	h := tw.NewHandler()
	defer h.DelTimer()

	if _, err := h.Cron("0", "0", "99", "*", "*", "?", func(TimeNoder) {}); err == nil {
		t.Fatal("expected error for out-of-range hour")
	}
}

func TestCron_TimezoneApplied(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	SetTimeTestHandler(clk)
	tw := newTimeWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()

	loc := time.FixedZone("TST", 3*3600)
	node, err := h.Cron("0", "0", "12", "*", "*", "?", func(TimeNoder) {}, WithLocation(loc))
	if err != nil {
		t.Fatal(err)
	}
	// 12:00 TST == 09:00 UTC; without the location the same expression would
	// land on 12:00 in the server's time.Local.
	want := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if got := node.Next(); !got.Equal(want) {
		t.Errorf("cron timezone Next = %v, want %v", got, want)
	}
}

func TestCron_StarSecondFiresAtBoundary(t *testing.T) {
	// newTestWheel starts the fake clock exactly on a second boundary, so a
	// "*" second schedule must fire on the next tick rather than skipping to
	// :01 (a permanent 1s offset).
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()
	var fired int32
	if _, err := h.Cron("*", "*", "*", "?", "*", "*", func(TimeNoder) {
		atomic.AddInt32(&fired, 1)
	}); err != nil {
		t.Fatal(err)
	}

	driveWheel(tw, clk, 30*time.Millisecond)
	waitFor(t, "cron * to fire at :00", func() bool { return atomic.LoadInt32(&fired) >= 1 })
}

// ── Stop idempotency / slot integrity (#9) ────────────────────────────────────

func TestStop_IdempotentPreservesSiblingInSlot(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	defer h.DelTimer()

	var a, b int32
	na := h.After(30*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&a, 1) })
	h.After(30*time.Millisecond, func(TimeNoder) { atomic.AddInt32(&b, 1) })

	// Both timers share the same slot. A second Stop must not decrement the slot
	// length again, which would make moveAndExec treat the non-empty slot as
	// empty and skip the sibling.
	na.Stop()
	na.Stop()

	driveWheel(tw, clk, 60*time.Millisecond)
	waitFor(t, "sibling timer in the same slot", func() bool { return atomic.LoadInt32(&b) == 1 })
	if got := atomic.LoadInt32(&a); got != 0 {
		t.Errorf("stopped timer fired %d times, want 0", got)
	}
}

// ── DelTimer / event channel close race (#10) ─────────────────────────────────

func TestDelTimer_NoSendOnClosedRace(t *testing.T) {
	tw, clk := newTestWheel()
	defer tw.Stop()

	h := tw.NewHandler()
	h.EventChan() // materialize the event channel
	h.Schedule(10*time.Millisecond, func(TimeNoder) {})

	done := make(chan struct{})
	go func() {
		driveWheel(tw, clk, 100*time.Millisecond)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	h.DelTimer()
	<-done
}

// ── weekday typo / anchoring (#12, #13) ───────────────────────────────────────

func TestParseWeekWords_THU(t *testing.T) {
	wd, err := parseWeekWords("THU")
	if err != nil {
		t.Fatal(err)
	}
	if wd != time.Thursday {
		t.Errorf("THU parsed as %v, want Thursday", wd)
	}
	if _, err := parseWeekWords("THD"); err == nil {
		t.Error("THD must not be accepted as a weekday abbreviation")
	}
}

func TestIgnoreCheckIsAnchored(t *testing.T) {
	if !ignoreCheck("?") {
		t.Error("bare ? should be recognized as the ignore token")
	}
	if ignoreCheck("?extra") || ignoreCheck("x?") {
		t.Error("ignoreCheck must be anchored: a string merely containing ? is not the ignore token")
	}
}
