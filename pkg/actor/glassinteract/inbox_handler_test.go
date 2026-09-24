package glassinteract

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// inboxPlanner is a fake actor.Planner for updater/delivery tests.
type inboxPlanner struct {
	callFunc func(ctx context.Context, target ref.Ref, callID string, payload any) (any, error)
}

func (inboxPlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (p inboxPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	result, err := p.callFunc(ctx, target, callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](result)
}

func (inboxPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

// claimedActorWithInbox returns an actor with an active online glass session
// (gs_aaa / dev-1) and a fresh inbox.
func claimedActorWithInbox(t *testing.T) *Actor {
	t.Helper()
	a, _ := freshTestActor(t)
	res := a.sess.claim(testTime(), "gs_aaa", "dev-1", []gen.GlassCapability{{Name: "native_text"}})
	if res.Rejected || res.Session == nil {
		t.Fatalf("claim failed: %+v", res)
	}
	a.hud.setConnection(hudConnectionOnline)
	return a
}

// inboxCtx returns a FakeCtx wired to a planner and a workspace service ref.
func inboxCtx(planner actor.Planner, wsRef ref.Ref) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	return ctx
}

// coordinatorPlanner builds a planner that answers the updater protocol:
// workspace.coordinator.lookup, agent.status on the coordinator, and the
// explicit coordinator.ingest_glass_event intake. Tests can override agent
// state and record deliveries.
func coordinatorPlanner(
	state string,
	activeTurn string,
	onDeliver func(gen.GlassEventDeliverReq),
) (actor.Planner, ref.Ref) {
	coordID := testutil.GenActorID()
	coordRef := testutil.NewFakeRef(coordID, nil)
	p := inboxPlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "workspace.coordinator_lookup":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: coordID.String()}, nil
		case "agent_status":
			return gen.AgentStatusResp{State: state, ActiveTurnRef: activeTurn, LastTurnCompletedAt: "2026-08-01T11:59:00Z"}, nil
		case coordinatorEventIntake:
			if onDeliver != nil {
				req, _ := payload.(gen.GlassEventDeliverReq)
				onDeliver(req)
			}
			return gen.GlassEventDeliverResp{Received: true}, nil
		}
		return nil, nil
	}}
	return p, coordRef
}

// wireCoordinatorLookup adds LookupID resolution for the coordinator actor id.
func wireCoordinatorLookup(ctx *testutil.FakeCtx, coordRef ref.Ref, coordID id.ActorID) {
	ctx.LookupIDFn = func(got id.ActorID) (ref.Ref, bool) {
		return coordRef, got == coordID
	}
}

// TestEnqueueRequiresOnlineSession verifies the offline fast-ignore path: no
// queue, no persistence, no event.
func TestEnqueueRequiresOnlineSession(t *testing.T) {
	a, _ := freshTestActor(t) // no session claimed
	ctx := inboxCtx(nil, nil)

	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if resp.Accepted || resp.EventID != "" {
		t.Fatalf("offline enqueue resp = %+v, want fast-ignore", resp)
	}
	if !strings.Contains(resp.Reason, "offline") {
		t.Fatalf("offline reason = %q", resp.Reason)
	}
	if snap := a.inbox.snapshot(); snap.Total != 0 {
		t.Fatalf("offline enqueue queued events: %+v", snap)
	}
	if len(ctx.EmittedEvents) != 0 {
		t.Fatalf("offline enqueue emitted events: %v", ctx.EmittedEvents)
	}
}

// TestEnqueueOnlinePersistsAndEmits verifies the online enqueue path: accepted,
// persisted, event emitted, and an immediate updater check runs (no delivery
// when the coordinator is running).
func TestEnqueueOnlinePersistsAndEmits(t *testing.T) {
	a := claimedActorWithInbox(t)
	planner, coordRef := coordinatorPlanner("running", "turn-ref", nil)
	ctx := inboxCtx(planner, nil)
	wireCoordinatorLookup(ctx, coordRef, coordRef.ID())

	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "hello",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !resp.Accepted || resp.EventID == "" {
		t.Fatalf("online enqueue resp = %+v", resp)
	}
	// The immediate updater check saw a running coordinator, so nothing was
	// delivered: the event stays pending.
	ev := a.inbox.get(resp.EventID)
	if ev == nil || ev.State != statePending {
		t.Fatalf("event after running-coordinator enqueue = %+v", ev)
	}
	if len(a.deliveryLog.snapshot()) != 0 {
		t.Fatalf("delivery log not empty despite running coordinator: %+v", a.deliveryLog.snapshot())
	}
	found := false
	for _, e := range ctx.EmittedEvents {
		if e.Kind == eventEnqueued {
			found = true
			payload, ok := e.Payload.(gen.GlassEventEnqueuedEvent)
			if !ok || payload.Event.EventID != resp.EventID {
				t.Fatalf("enqueued event payload = %+v", e.Payload)
			}
		}
	}
	if !found {
		t.Fatal("glass.event.enqueued not emitted")
	}
	// The inbox is persisted through the actor's durable state.
	var d durableState
	if err := a.store.Load(a.actorID, &d); err != nil {
		t.Fatalf("reload durable state: %v", err)
	}
	if d.Inbox == nil || len(d.Inbox.Events) != 1 || d.Inbox.Events[0].EventID != resp.EventID {
		t.Fatalf("persisted inbox = %+v", d.Inbox)
	}
}

// TestUpdaterDeliversOneEventWhenCoordinatorIdle verifies the full gate:
// online + coordinator idle + no active call -> exactly one event is delivered
// and marked in_flight.
func TestUpdaterDeliversOneEventWhenCoordinatorIdle(t *testing.T) {
	a := claimedActorWithInbox(t)
	var delivered []gen.GlassEventDeliverReq
	planner, coordRef := coordinatorPlanner("idle", "", func(req gen.GlassEventDeliverReq) {
		delivered = append(delivered, req)
	})
	ctx := inboxCtx(planner, nil)
	wireCoordinatorLookup(ctx, coordRef, coordRef.ID())

	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "build", Kind: "fail", Priority: priorityHigh, Payload: "compile error",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The enqueue kicks an async updater on the glass_updater lane; drive one
	// tick synchronously here (the lane handler is handleUpdaterTick's core).
	a.updaterTick(ctx)
	if len(delivered) != 1 {
		t.Fatalf("deliveries = %d, want 1 (log %+v)", len(delivered), a.deliveryLog.snapshot())
	}
	if delivered[0].EventID != resp.EventID || delivered[0].Payload != "compile error" || delivered[0].Deliveries != 1 {
		t.Fatalf("delivery payload = %+v", delivered[0])
	}
	// The event is now in_flight awaiting the Coordinator receipt.
	ev := a.inbox.get(resp.EventID)
	if ev == nil || ev.State != stateInflight {
		t.Fatalf("event after delivery = %+v, want in_flight", ev)
	}
	if len(a.deliveryLog.snapshot()) != 1 || !a.deliveryLog.snapshot()[0].Success {
		t.Fatalf("delivery log = %+v, want one success", a.deliveryLog.snapshot())
	}
	found := false
	for _, e := range ctx.EmittedEvents {
		if e.Kind == eventDelivered {
			found = true
		}
	}
	if !found {
		t.Fatal("glass.event.delivered not emitted")
	}
}

// TestUpdaterGateBlocksWhenCoordinatorBusy verifies the gate refuses delivery
// while the coordinator is running or has an active call.
func TestUpdaterGateBlocksWhenCoordinatorBusy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      string
		activeTurn string
	}{
		{"running", "running", ""},
		{"active call", "idle", "turn-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := claimedActorWithInbox(t)
			var delivered int
			planner, coordRef := coordinatorPlanner(tc.state, tc.activeTurn, func(gen.GlassEventDeliverReq) {
				delivered++
			})
			ctx := inboxCtx(planner, nil)
			wireCoordinatorLookup(ctx, coordRef, coordRef.ID())

			if _, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
				Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
			}); err != nil {
				t.Fatal(err)
			}
			if delivered != 0 {
				t.Fatalf("delivered %d events while coordinator busy", delivered)
			}
			if snap := a.inbox.snapshot(); snap.Pending != 1 {
				t.Fatalf("inbox snapshot = %+v, want 1 pending", snap)
			}
		})
	}
}

// TestUpdaterDeliversOnePerTick verifies the updater delivers at most one event
// per evaluation even when several are queued.
func TestUpdaterDeliversOnePerTick(t *testing.T) {
	a := claimedActorWithInbox(t)
	var delivered []gen.GlassEventDeliverReq
	planner, coordRef := coordinatorPlanner("", "", func(req gen.GlassEventDeliverReq) {
		delivered = append(delivered, req)
	})
	ctx := inboxCtx(planner, nil)
	wireCoordinatorLookup(ctx, coordRef, coordRef.ID())

	for i := 0; i < 3; i++ {
		if _, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
			Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The enqueues kick async updater checks; drive one tick synchronously —
	// the first delivery leaves one in_flight, so later ticks cannot deliver.
	a.updaterTick(ctx)
	if len(delivered) != 1 {
		t.Fatalf("deliveries = %d, want exactly 1 (in_flight blocks)", len(delivered))
	}
	snap := a.inbox.snapshot()
	if snap.Pending != 2 || snap.InFlight != 1 {
		t.Fatalf("snapshot = %+v, want 2 pending + 1 in_flight", snap)
	}
}

// TestDeliveryFailureRequeues verifies a failed intake call requeues the event
// with a bounded retry and records the failure.
func TestDeliveryFailureRequeues(t *testing.T) {
	a := claimedActorWithInbox(t)
	coordID := testutil.GenActorID()
	coordRef := testutil.NewFakeRef(coordID, nil)
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	planner := inboxPlanner{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "workspace.coordinator_lookup":
			return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "小明", ActorID: coordID.String()}, nil
		case "agent_status":
			return gen.AgentStatusResp{State: "idle"}, nil
		case coordinatorEventIntake:
			return nil, context.DeadlineExceeded
		}
		return nil, nil
	}}
	ctx := inboxCtx(planner, wsRef)
	ctx.LookupIDFn = func(got id.ActorID) (ref.Ref, bool) { return coordRef, got == coordID }

	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Drive the async kick's tick synchronously: this delivery fails.
	a.updaterTick(ctx)
	ev := a.inbox.get(resp.EventID)
	if ev == nil || ev.State != statePending || ev.Retries != 1 {
		t.Fatalf("event after failed delivery = %+v, want pending with 1 retry", ev)
	}
	entries := a.deliveryLog.snapshot()
	if len(entries) != 1 || entries[0].Success || !strings.Contains(entries[0].Detail, coordinatorEventIntake) {
		t.Fatalf("delivery log = %+v", entries)
	}
}

// TestEventCompleteOutcomesViaHandler verifies the Coordinator receipt handler
// for handled (terminal), defer (pending), and failed (pending + retry).
func TestEventCompleteOutcomesViaHandler(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome string
		want    inboxState
	}{
		{"handled", outcomeHandled, stateCompleted},
		{"ignored", outcomeIgnored, stateIgnored},
		{"defer", outcomeDefer, statePending},
		{"failed", outcomeFailed, statePending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := claimedActorWithInbox(t)
			ctx := inboxCtx(nil, nil)
			resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
				Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
			})
			if err != nil {
				t.Fatal(err)
			}
			// Put it in_flight directly (the gate is not needed for the
			// receipt path).
			if err := a.inbox.beginDelivery(resp.EventID, a.now()); err != nil {
				t.Fatal(err)
			}
			cr, err := a.handleEventComplete(ctx, gen.GlassEventCompleteReq{EventID: resp.EventID, Outcome: tc.outcome})
			if err != nil {
				t.Fatalf("complete(%s): %v", tc.outcome, err)
			}
			if cr.State != string(tc.want) {
				t.Fatalf("complete state = %q, want %q", cr.State, tc.want)
			}
			ev := a.inbox.get(resp.EventID)
			if ev == nil || ev.State != tc.want {
				t.Fatalf("event after %s = %+v", tc.outcome, ev)
			}
			if tc.outcome == outcomeFailed && ev.Retries != 1 {
				t.Fatalf("failed outcome retries = %d, want 1", ev.Retries)
			}
			found := false
			for _, e := range ctx.EmittedEvents {
				if e.Kind == eventCompleted {
					found = true
				}
			}
			if !found {
				t.Fatal("glass.event.completed not emitted")
			}
		})
	}
}

// TestFormalOfflineClearsInbox verifies the queue is dropped on formal offline.
func TestFormalOfflineClearsInbox(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := inboxCtx(nil, nil)
	if _, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	}); err != nil {
		t.Fatal(err)
	}
	// Advance the clock past the reconnect grace and run the formal-offline
	// evaluator; it must clear the persisted inbox.
	a.now = func() time.Time { return testTime().Add(ReconnectGrace) }
	if err := a.handleCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if snap := a.inbox.snapshot(); snap.Total != 0 {
		t.Fatalf("inbox after formal offline = %+v, want empty", snap)
	}
	var d durableState
	if err := a.store.Load(a.actorID, &d); err != nil {
		t.Fatal(err)
	}
	if d.Inbox == nil || len(d.Inbox.Events) != 0 {
		t.Fatalf("persisted inbox after offline = %+v", d.Inbox)
	}
}

// TestReconnectWithinGraceRetainsInbox verifies a 30s blip inside the grace
// window keeps the queue intact.
func TestReconnectWithinGraceRetainsInbox(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := inboxCtx(nil, nil)
	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Same-session claim inside the grace window: reconnected, no clear.
	a.now = func() time.Time { return testTime().Add(10 * time.Second) }
	gctx := glassCtx(testutil.GenActorID(), "gs_aaa")
	if _, err := a.handleClaim(gctx, gen.GlassSessionClaimReq{SessionID: "gs_aaa", DeviceID: "dev-1"}); err != nil {
		t.Fatal(err)
	}
	if ev := a.inbox.get(resp.EventID); ev == nil {
		t.Fatal("inbox was cleared on in-grace reconnect")
	}
}

// TestSessionReplacementClearsInbox verifies a new session replaces the old and
// clears its queue.
func TestSessionReplacementClearsInbox(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := inboxCtx(nil, nil)
	if _, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	}); err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return testTime().Add(time.Second) }
	gctx := glassCtx(testutil.GenActorID(), "gs_new")
	if _, err := a.handleClaim(gctx, gen.GlassSessionClaimReq{SessionID: "gs_new", DeviceID: "dev-2"}); err != nil {
		t.Fatal(err)
	}
	if snap := a.inbox.snapshot(); snap.Total != 0 {
		t.Fatalf("inbox after replacement = %+v, want empty", snap)
	}
}

// TestInboxStateCallable verifies the internal inbox.state callable returns the
// secret-free projection.
func TestInboxStateCallable(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := inboxCtx(nil, nil)
	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "diagnostic", Kind: "warn", Priority: priorityLow, Payload: "mem",
	})
	if err != nil {
		t.Fatal(err)
	}
	stateResp, err := a.handleInboxState(nil, gen.GlassInboxReq{})
	if err != nil {
		t.Fatal(err)
	}
	snap := stateResp.Snapshot
	if snap.Total != 1 || snap.Pending != 1 || len(snap.Events) != 1 {
		t.Fatalf("inbox.state = %+v", snap)
	}
	if snap.Events[0].EventID != resp.EventID || snap.Events[0].Payload != "mem" {
		t.Fatalf("inbox.state event = %+v", snap.Events[0])
	}
}

// TestDebugStateIncludesInboxHudDeliveries verifies the debug projection gains
// the inbox snapshot, HUD whitelist and delivery log.
func TestDebugStateIncludesInboxHudDeliveries(t *testing.T) {
	a := claimedActorWithInbox(t)
	planner, coordRef := coordinatorPlanner("idle", "", nil)
	ctx := inboxCtx(planner, nil)
	wireCoordinatorLookup(ctx, coordRef, coordRef.ID())

	if _, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	}); err != nil {
		t.Fatal(err)
	}
	// Drive the async kick's tick synchronously so the event is in_flight.
	a.updaterTick(ctx)
	// debug_state is developer-gated; call it with a developer identity.
	devCtx := testutil.AnonCtx(testutil.GenActorID())
	devCtx.Identity_ = id.Identity{Role: "developer"}
	debugResp, err := a.handleDebugState(devCtx, gen.GlassDebugReq{})
	if err != nil {
		t.Fatal(err)
	}
	st := debugResp.State
	if st.Inbox == nil || st.Inbox.Total != 1 || st.Inbox.InFlight != 1 {
		t.Fatalf("debug inbox = %+v", st.Inbox)
	}
	if st.Hud == nil {
		t.Fatal("debug hud missing")
	}
	if st.Hud.Connection != "online" || st.Hud.AgentRunning || st.Hud.AgentState != "idle" {
		t.Fatalf("debug hud = %+v", st.Hud)
	}
	if len(st.Deliveries) != 1 {
		t.Fatalf("debug deliveries = %+v, want 1", st.Deliveries)
	}
}

// TestActorPersistInboxRoundtrip verifies Save/Load restores queued events and
// that a restored in_flight event past its receipt window requeues.
func TestActorPersistInboxRoundtrip(t *testing.T) {
	a := claimedActorWithInbox(t)
	ctx := inboxCtx(nil, nil)
	resp, err := a.handleEventEnqueue(ctx, gen.GlassEventEnqueueReq{
		Source: "turn", Kind: "start", Priority: priorityNormal, Payload: "p",
	})
	if err != nil {
		t.Fatal(err)
	}

	// New actor with the same store + clock simulates a process restart.
	a2 := &Actor{
		store: a.store,
		key:   a.key,
		jwt:   a.jwt,
		now:   a.now,
	}
	ctx2 := testutil.HumanCtx(testutil.GenActorID())
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if ev := a2.inbox.get(resp.EventID); ev == nil || ev.Payload != "p" || ev.State != statePending {
		t.Fatalf("restored event = %+v", ev)
	}
}
