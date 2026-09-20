package glassinteract

import (
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func mustEnqueue(t *testing.T, m *inboxManager, now time.Time, src, kind, prio, payload, dedupe string) string {
	t.Helper()
	resp, err := m.enqueue(now, gen.GlassEventEnqueueReq{
		Source: src, Kind: kind, Priority: prio, Payload: payload, DedupeKey: dedupe,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !resp.Accepted || resp.EventID == "" {
		t.Fatalf("enqueue resp = %+v", resp)
	}
	return resp.EventID
}

// TestInboxPriorityThenCreationOrder verifies queue order is priority first,
// then creation time.
func TestInboxPriorityThenCreationOrder(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	// Enqueue in interleaved order: normal, low, high, high-later.
	mustEnqueue(t, m, now, "turn", "start", priorityNormal, "n1", "")
	mustEnqueue(t, m, now.Add(time.Second), "diagnostic", "warn", priorityLow, "l1", "")
	idHigh := mustEnqueue(t, m, now.Add(2*time.Second), "build", "fail", priorityHigh, "h1", "")
	idHigh2 := mustEnqueue(t, m, now.Add(3*time.Second), "build", "fail", priorityHigh, "h2", "")
	idNormal := mustEnqueue(t, m, now.Add(4*time.Second), "turn", "end", priorityNormal, "n2", "")
	_ = idHigh2

	// next() must return high before normal before low; high preserves creation order.
	got := []string{}
	for {
		ev := m.next(now.Add(10 * time.Second))
		if ev == nil {
			break
		}
		got = append(got, ev.EventID)
		if err := m.beginDelivery(ev.EventID, now.Add(10*time.Second)); err != nil {
			t.Fatalf("beginDelivery(%s): %v", ev.EventID, err)
		}
		if _, err := m.complete(ev.EventID, outcomeHandled, "", now.Add(10*time.Second)); err != nil {
			t.Fatalf("complete(%s): %v", ev.EventID, err)
		}
	}
	// High (ge_3, ge_4 in creation order), then normal (ge_1, ge_5), then low.
	want := []string{idHigh, idHigh2, "ge_1", idNormal, "ge_2"}
	if len(got) != len(want) {
		t.Fatalf("delivery order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery order[%d] = %s, want %s (full %v)", i, got[i], want[i], got)
		}
	}
}

// TestInboxDedupeMergeRefresh verifies the same dedupe key merges and refreshes
// the existing non-terminal event instead of queuing a second one.
func TestInboxDedupeMergeRefresh(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "payload-1", "dedupe:turn:42")

	// Same dedupe key at a later time: merge + refresh (same id, new payload).
	resp, err := m.enqueue(now.Add(5*time.Minute), gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityHigh, Payload: "payload-2", DedupeKey: "dedupe:turn:42",
	})
	if err != nil {
		t.Fatalf("dedupe enqueue: %v", err)
	}
	if !resp.Accepted || resp.EventID != id {
		t.Fatalf("dedupe resp = %+v, want same id %s", resp, id)
	}
	ev := m.get(id)
	if ev == nil {
		t.Fatal("merged event missing")
	}
	if ev.Payload != "payload-2" {
		t.Fatalf("merged payload = %q, want payload-2", ev.Payload)
	}
	if ev.CreatedAt != now.Add(5*time.Minute) {
		t.Fatalf("merged CreatedAt = %v, want refreshed", ev.CreatedAt)
	}
	wantTTL := now.Add(5 * time.Minute).Add(ttlHigh) // priority updated to high on merge
	if !ev.TTLAt.Equal(wantTTL) {
		t.Fatalf("merged TTLAt = %v, want %v", ev.TTLAt, wantTTL)
	}

	// A terminal event with the same dedupe key no longer merges.
	if err := m.beginDelivery(id, now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.complete(id, outcomeHandled, "", now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	resp2, err := m.enqueue(now.Add(7*time.Minute), gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "payload-3", DedupeKey: "dedupe:turn:42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp2.Accepted || resp2.EventID == id {
		t.Fatalf("post-terminal dedupe resp = %+v, want a new event id", resp2)
	}
}

// TestInboxTTLPerPriority verifies the fixed TTL bands: high 30m, normal 10m,
// low 2m.
func TestInboxTTLPerPriority(t *testing.T) {
	now := testTime()
	for prio, want := range map[string]time.Duration{
		priorityHigh:   ttlHigh,
		priorityNormal: ttlNormal,
		priorityLow:    ttlLow,
	} {
		m := newInboxManager()
		mustEnqueue(t, m, now, "src", "kind", prio, "p", "")
		snap := m.snapshot()
		if len(snap.Events) != 1 {
			t.Fatalf("%s: events = %d", prio, len(snap.Events))
		}
		created, _ := time.Parse(time.RFC3339, snap.Events[0].CreatedAt)
		ttlAt, _ := time.Parse(time.RFC3339, snap.Events[0].TTLAt)
		if !ttlAt.Equal(created.Add(want)) {
			t.Fatalf("%s TTL = %v, want %v", prio, ttlAt.Sub(created), want)
		}
	}
}

// TestInboxExpiry verifies overdue events expire and are excluded from next().
func TestInboxExpiry(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "diagnostic", "low", priorityLow, "p", "")

	// Before expiry the event is deliverable.
	if ev := m.next(now.Add(time.Minute)); ev == nil || ev.EventID != id {
		t.Fatalf("next before expiry = %+v", ev)
	}
	// After TTL the housekeep expires it and next() returns nothing.
	expired, _ := m.housekeep(now.Add(ttlLow + time.Second))
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	if ev := m.next(now.Add(ttlLow + time.Second)); ev != nil {
		t.Fatalf("next after expiry = %+v", ev)
	}
	snap := m.snapshot()
	if snap.Expired != 1 || snap.Total != 1 {
		t.Fatalf("snapshot after expiry = %+v", snap)
	}
}

// TestInboxInflightRetryWindow verifies an in_flight event with no receipt for
// 2 minutes returns to pending with an incremented retry.
func TestInboxInflightRetryWindow(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")

	if err := m.beginDelivery(id, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if !m.hasInflight() {
		t.Fatal("hasInflight expected true after beginDelivery")
	}
	// A high-priority event behind an in_flight one must not be delivered.
	mustEnqueue(t, m, now.Add(2*time.Second), "build", "fail", priorityHigh, "p2", "")
	if ev := m.next(now.Add(2 * time.Second)); ev != nil {
		t.Fatalf("high priority preempted in_flight: %+v", ev)
	}

	// Inside the 2-minute window nothing changes.
	if exp, _ := m.housekeep(now.Add(90 * time.Second)); exp != 0 {
		t.Fatalf("housekeep inside window expired %d", exp)
	}
	// At exactly 2 minutes the event returns to pending with retries++.
	expired, requeued := m.housekeep(now.Add(time.Second).Add(inflightRetryWindow))
	if expired != 0 || requeued != 1 {
		t.Fatalf("housekeep = expired %d requeued %d, want 0/1", expired, requeued)
	}
	ev := m.get(id)
	if ev == nil || ev.State != statePending || ev.Retries != 1 {
		t.Fatalf("requeued event = %+v, want pending with 1 retry", ev)
	}
	if m.hasInflight() {
		t.Fatal("hasInflight still true after requeue")
	}
}

// TestInboxCompleteOutcomes verifies the four Coordinator receipts.
func TestInboxCompleteOutcomes(t *testing.T) {
	now := testTime()

	newInflight := func(t *testing.T, m *inboxManager, id string) {
		t.Helper()
		if err := m.beginDelivery(id, now); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("handled terminal", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
		newInflight(t, m, id)
		state, err := m.complete(id, outcomeHandled, "", now)
		if err != nil || state != stateCompleted {
			t.Fatalf("complete = %s, %v", state, err)
		}
		if ev := m.get(id); ev == nil || !ev.State.terminal() {
			t.Fatalf("event after handled = %+v", ev)
		}
	})

	t.Run("ignored terminal", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "diagnostic", "warn", priorityLow, "p", "")
		newInflight(t, m, id)
		state, err := m.complete(id, outcomeIgnored, "no user", now)
		if err != nil || state != stateIgnored {
			t.Fatalf("complete = %s, %v", state, err)
		}
	})

	t.Run("defer requeues within TTL", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
		newInflight(t, m, id)
		state, err := m.complete(id, outcomeDefer, "", now)
		if err != nil || state != statePending {
			t.Fatalf("complete = %s, %v", state, err)
		}
		ev := m.get(id)
		if ev == nil || ev.State != statePending {
			t.Fatalf("deferred event = %+v", ev)
		}
		wantRetry := now.Add(deferWindow)
		if !ev.RetryAt.Equal(wantRetry) {
			t.Fatalf("defer retry = %v, want %v", ev.RetryAt, wantRetry)
		}
		// Not eligible until the defer window elapses.
		if next := m.next(now.Add(deferWindow - time.Second)); next != nil {
			t.Fatalf("deferred event eligible early: %+v", next)
		}
		if next := m.next(now.Add(deferWindow)); next == nil || next.EventID != id {
			t.Fatalf("deferred event not eligible after window: %+v", next)
		}
	})

	t.Run("defer cannot exceed TTL", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "diagnostic", "low", priorityLow, "p", "")
		newInflight(t, m, id)
		// Defer at a time very close to the low-priority TTL (2m).
		late := now.Add(ttlLow - time.Second)
		state, err := m.complete(id, outcomeDefer, "", late)
		if err != nil || state != statePending {
			t.Fatalf("complete = %s, %v", state, err)
		}
		ev := m.get(id)
		if ev == nil {
			t.Fatal("event missing")
		}
		if ev.RetryAt.After(ev.TTLAt) {
			t.Fatalf("defer retry %v exceeds TTL %v", ev.RetryAt, ev.TTLAt)
		}
		// One second past the original TTL the event is reaped as expired.
		expired, _ := m.housekeep(now.Add(ttlLow))
		if expired != 1 {
			t.Fatalf("expired = %d, want 1", expired)
		}
	})

	t.Run("failed requeues with retry", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "build", "fail", priorityHigh, "p", "")
		newInflight(t, m, id)
		state, err := m.complete(id, outcomeFailed, "llm error", now)
		if err != nil || state != statePending {
			t.Fatalf("complete = %s, %v", state, err)
		}
		ev := m.get(id)
		if ev == nil || ev.State != statePending || ev.Retries != 1 {
			t.Fatalf("failed event = %+v", ev)
		}
		if !ev.RetryAt.Equal(now.Add(retryBackoff)) {
			t.Fatalf("failed retry = %v, want %v", ev.RetryAt, now.Add(retryBackoff))
		}
		if next := m.next(now.Add(retryBackoff - time.Second)); next != nil {
			t.Fatalf("failed event eligible early: %+v", next)
		}
		if next := m.next(now.Add(retryBackoff)); next == nil || next.EventID != id {
			t.Fatalf("failed event not eligible after backoff: %+v", next)
		}
	})

	t.Run("receipt only for in_flight", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
		if _, err := m.complete(id, outcomeHandled, "", now); err == nil {
			t.Fatal("complete on pending event accepted, want rejection")
		}
		if _, err := m.complete("ge_unknown", outcomeHandled, "", now); err == nil {
			t.Fatal("complete on unknown event accepted, want rejection")
		}
		if _, err := m.complete(id, "sideways", "", now); err == nil {
			t.Fatal("complete with unknown outcome accepted, want rejection")
		}
	})

	t.Run("terminal receipt idempotent", func(t *testing.T) {
		m := newInboxManager()
		id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
		if err := m.beginDelivery(id, now); err != nil {
			t.Fatal(err)
		}
		if _, err := m.complete(id, outcomeHandled, "", now); err != nil {
			t.Fatal(err)
		}
		if _, err := m.complete(id, outcomeHandled, "", now); err != nil {
			t.Fatalf("duplicate handled receipt rejected: %v", err)
		}
		if _, err := m.complete(id, outcomeIgnored, "", now); err == nil {
			t.Fatal("ignored receipt accepted on completed event, want rejection")
		}
	})
}

// TestInboxRequeueOnDeliveryFailure verifies a failed delivery returns to
// pending with an incremented retry and a backoff.
func TestInboxRequeueOnDeliveryFailure(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
	if err := m.beginDelivery(id, now); err != nil {
		t.Fatal(err)
	}
	if err := m.requeue(id, now); err != nil {
		t.Fatal(err)
	}
	ev := m.get(id)
	if ev == nil || ev.State != statePending || ev.Retries != 1 {
		t.Fatalf("requeued event = %+v", ev)
	}
	if !ev.RetryAt.Equal(now.Add(retryBackoff)) {
		t.Fatalf("requeue retry = %v, want %v", ev.RetryAt, now.Add(retryBackoff))
	}
}

// TestInboxClear verifies the queue is dropped wholesale.
func TestInboxClear(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p", "")
	if err := m.beginDelivery(id, now); err != nil {
		t.Fatal(err)
	}
	m.clear()
	snap := m.snapshot()
	if snap.Total != 0 || len(snap.Events) != 0 {
		t.Fatalf("snapshot after clear = %+v", snap)
	}
	// Id counter is preserved so ids stay unique after a new session.
	resp, err := m.enqueue(now, gen.GlassEventEnqueueReq{Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.EventID == id {
		t.Fatalf("event id reused after clear: %s", resp.EventID)
	}
}

// TestInboxValidation verifies enqueue input rules.
func TestInboxValidation(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	req := gen.GlassEventEnqueueReq{Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p"}
	for name, mutate := range map[string]func(*gen.GlassEventEnqueueReq){
		"empty source":  func(r *gen.GlassEventEnqueueReq) { r.Source = "" },
		"empty kind":    func(r *gen.GlassEventEnqueueReq) { r.Kind = "" },
		"empty payload": func(r *gen.GlassEventEnqueueReq) { r.Payload = "" },
		"bad priority":  func(r *gen.GlassEventEnqueueReq) { r.Priority = "urgent" },
	} {
		t.Run(name, func(t *testing.T) {
			r := req
			mutate(&r)
			if _, err := m.enqueue(now, r); err == nil {
				t.Errorf("enqueue(%s) accepted invalid request", name)
			}
		})
	}
	// Empty priority defaults to normal.
	resp, err := m.enqueue(now, gen.GlassEventEnqueueReq{Source: "turn", Kind: "start", Payload: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if ev := m.get(resp.EventID); ev == nil || ev.Priority != priorityNormal {
		t.Fatalf("default priority = %+v", ev)
	}
}

// TestInboxPersistRoundtrip verifies the inbox survives export/import exactly.
func TestInboxPersistRoundtrip(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	id := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "payload-a", "")
	mustEnqueue(t, m, now.Add(time.Second), "diagnostic", "warn", priorityLow, "payload-b", "")
	if err := m.beginDelivery(id, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	m2 := newInboxManager()
	m2.importState(m.export())

	snap := m2.snapshot()
	if snap.Total != 2 || snap.Pending != 1 || snap.InFlight != 1 {
		t.Fatalf("restored snapshot = %+v", snap)
	}
	ev := m2.get(id)
	if ev == nil || ev.State != stateInflight || ev.Payload != "payload-a" {
		t.Fatalf("restored inflight event = %+v", ev)
	}
	// A restored in_flight event past its receipt window requeues on housekeep.
	// The low-priority second event (TTL 2m) expires at the same moment.
	expired, requeued := m2.housekeep(now.Add(2 * time.Second).Add(inflightRetryWindow))
	if requeued != 1 {
		t.Fatalf("restored housekeep = expired %d requeued %d, want requeued 1", expired, requeued)
	}
	if ev := m2.get(id); ev == nil || ev.State != statePending || ev.Retries != 1 {
		t.Fatalf("restored requeued event = %+v", ev)
	}
}

// TestInboxSnapshotCounts verifies the snapshot accounting.
func TestInboxSnapshotCounts(t *testing.T) {
	m := newInboxManager()
	now := testTime()
	a := mustEnqueue(t, m, now, "turn", "start", priorityNormal, "p1", "")
	b := mustEnqueue(t, m, now.Add(time.Second), "build", "fail", priorityHigh, "p2", "")
	// Deliver and ignore the normal event, then take the high one in_flight.
	if err := m.beginDelivery(a, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.complete(a, outcomeIgnored, "", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := m.beginDelivery(b, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	snap := m.snapshot()
	if snap.Total != 2 || snap.Pending != 0 || snap.InFlight != 1 || snap.Ignored != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Snapshot events are ordered by priority then creation.
	if len(snap.Events) != 2 || snap.Events[0].EventID != b {
		t.Fatalf("snapshot order = %v, want %s first", snap.Events, b)
	}
}
