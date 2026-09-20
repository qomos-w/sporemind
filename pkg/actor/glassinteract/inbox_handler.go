// Actor-level Stage 5 handlers and the session-gated updater: enqueue,
// complete, inbox state, the 5-second updater tick, the Coordinator delivery
// attempt, and the bounded delivery log.
package glassinteract

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Debug timeline kinds used by the inbox and updater.
const (
	debugKindEventEnqueued    = "event_enqueued"
	debugKindEventDelivered   = "event_delivered"
	debugKindEventDeliverFail = "event_delivery_failed"
	debugKindEventComplete    = "event_completed"
	debugKindEventInboxLost   = "event_inbox_cleared"
)

// handleEventEnqueue is the explicit internal enqueue callable used by event
// sources. While no glass session is online the event is fast-ignored: nothing
// is queued or persisted.
func (a *Actor) handleEventEnqueue(ctx actor.Context, req gen.GlassEventEnqueueReq) (gen.GlassEventEnqueueResp, error) {
	if _, _, online := a.sess.activeIdentity(); !online {
		return gen.GlassEventEnqueueResp{
			Accepted: false,
			Reason:   "glass is offline; event not queued",
		}, nil
	}
	now := a.now()
	resp, err := a.inbox.enqueue(now, req)
	if err != nil {
		return resp, fmt.Errorf("%s: %w", callableEventEnqueue, err)
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist event enqueue", "err", err)
	}
	if ev := a.inbox.get(resp.EventID); ev != nil {
		a.debug.record(debugKindEventEnqueued, resp.EventID+"/"+ev.Source, now)
		if emitErr := ctx.EmitEvent(eventEnqueued, gen.GlassEventEnqueuedEvent{Event: ev.toGen()}); emitErr != nil {
			ctx.Logger().Error("glassinteract: emit event.enqueued", "err", emitErr)
		}
	}
	// Immediate updater check: a freshly enqueued event may be deliverable now.
	a.updaterTick(ctx)
	return resp, nil
}

// handleEventComplete is the Coordinator-internal receipt callable. handled
// and ignored are terminal; defer and failed return the event to pending.
func (a *Actor) handleEventComplete(ctx actor.Context, req gen.GlassEventCompleteReq) (gen.GlassEventCompleteResp, error) {
	if strings.TrimSpace(req.EventID) == "" {
		return gen.GlassEventCompleteResp{}, fmt.Errorf("%s: event id is required", callableEventComplete)
	}
	now := a.now()
	state, err := a.inbox.complete(req.EventID, req.Outcome, req.Reason, now)
	if err != nil {
		return gen.GlassEventCompleteResp{}, fmt.Errorf("%s: %w", callableEventComplete, err)
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist event completion", "err", err)
	}
	if ev := a.inbox.get(req.EventID); ev != nil {
		a.debug.record(debugKindEventComplete, req.EventID+"/"+req.Outcome, now)
		if emitErr := ctx.EmitEvent(eventCompleted, gen.GlassEventCompletedEvent{Event: ev.toGen()}); emitErr != nil {
			ctx.Logger().Error("glassinteract: emit event.completed", "err", emitErr)
		}
	}
	// A defer/failed event is eligible again; check immediately.
	a.updaterTick(ctx)
	return gen.GlassEventCompleteResp{EventID: req.EventID, State: string(state)}, nil
}

// handleInboxState returns the secret-free inbox projection for debug and
// observability tooling.
func (a *Actor) handleInboxState(_ actor.PureContext, _ gen.GlassInboxReq) (gen.GlassInboxResp, error) {
	return gen.GlassInboxResp{Snapshot: a.inbox.snapshot()}, nil
}

// handleUpdaterTick is the scheduled session-gated updater. It runs on the
// dedicated glass_updater lane so its coordinator round-trip never occupies
// the owner lane. While a session is online it keeps a 5-second cadence; when
// offline the timer is not re-armed (a future claim re-arms it). The delayed
// self-call inherits the handler's glass_updater lane route.
func (a *Actor) handleUpdaterTick(ctx actor.Context) error {
	a.updaterArmed.Store(false)
	a.updaterTick(ctx)
	if _, _, online := a.sess.activeIdentity(); online {
		a.scheduleUpdater(ctx)
	}
	return nil
}

// scheduleUpdater arms the periodic updater tick at most once. It is a no-op
// when a tick is already outstanding. The check-and-set is a CompareAndSwap
// because the scheduled tick (glass_updater lane) and a session claim (owner
// lane) can now call it concurrently; a plain Load-then-Store would arm two
// timer chains.
func (a *Actor) scheduleUpdater(ctx actor.PureContext) {
	if !a.updaterArmed.CompareAndSwap(false, true) {
		return
	}
	if err := ctx.After(updaterInterval, callableUpdaterTick, nil); err != nil {
		a.updaterArmed.Store(false)
		ctx.Logger().Error("glassinteract: schedule updater tick failed", "err", err)
	}
}

// updaterTick runs one updater evaluation: housekeeping, the session/coordinator
// gate, and at most one delivery. It is safe to call from handlers (the
// enqueue/complete/claim immediate checks, which run inline on the owner lane)
// and from the scheduled tick on the glass_updater lane. The inbox, delivery
// log, and debug timeline it mutates are mutex-guarded, so the two lanes do not
// race; the in_flight gate keeps delivery single-flight across lanes.
func (a *Actor) updaterTick(ctx actor.Context) {
	now := a.now()
	a.inbox.housekeep(now)

	if _, _, online := a.sess.activeIdentity(); !online {
		return
	}
	// One delivery at a time; a high-priority event never preempts in_flight.
	if a.inbox.hasInflight() {
		return
	}
	gate, ok := a.coordinatorGate(ctx)
	if !ok {
		return
	}
	if gate.Running || gate.ActiveCall {
		return
	}
	ev := a.inbox.next(now)
	if ev == nil {
		return
	}
	if err := a.inbox.beginDelivery(ev.EventID, now); err != nil {
		return
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist event delivery", "err", err)
	}
	a.deliverEvent(ctx, ev)
}

// deliverEvent hands one event to the Coordinator through the explicit
// internal intake callable coordinator.ingest_glass_event. Every attempt is
// recorded on the bounded delivery log; on failure the event returns to
// pending with a bounded retry and no further delivery happens this tick.
func (a *Actor) deliverEvent(ctx actor.Context, ev *inboxEvent) {
	now := a.now()
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		a.recordDeliveryFailure(ctx, ev, now, "workspace service unavailable")
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		a.recordDeliveryFailure(ctx, ev, now, "planner unavailable")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()

	result, err := planner.Call(callCtx, wsRef, "workspace.coordinator_lookup", nil).Await()
	if err != nil {
		a.recordDeliveryFailure(ctx, ev, now, "coordinator lookup: "+err.Error())
		return
	}
	lookup, ok := decodeCoordinatorLookup(result)
	if !ok || !lookup.Found || lookup.ActorID == "" {
		a.recordDeliveryFailure(ctx, ev, now, "coordinator unavailable")
		return
	}
	coordRef, ok := lookupCoordinatorRef(ctx, lookup.ActorID)
	if !ok {
		a.recordDeliveryFailure(ctx, ev, now, "coordinator actor not resolvable")
		return
	}
	req := gen.GlassEventDeliverReq{
		EventID:    ev.EventID,
		Source:     ev.Source,
		Kind:       ev.Kind,
		Priority:   ev.Priority,
		Payload:    ev.Payload,
		Deliveries: int64(ev.Retries + 1),
	}
	result, err = planner.Call(callCtx, coordRef, coordinatorEventIntake, req).Await()
	if err != nil {
		a.recordDeliveryFailure(ctx, ev, now, coordinatorEventIntake+": "+err.Error())
		return
	}
	if resp, ok := decodeEventDeliverResp(result); !ok || !resp.Received {
		a.recordDeliveryFailure(ctx, ev, now, coordinatorEventIntake+": receipt not acknowledged")
		return
	}
	a.deliveryLog.record(true, ev.EventID, "", now)
	a.debug.record(debugKindEventDelivered, ev.EventID, now)
	// Re-read the live event so the emitted projection reflects the in_flight
	// state (ev is a pre-delivery clone). The scheduled tick now runs on the
	// glass_updater lane, so an owner-lane clear (formal offline / session
	// replacement) can drop the event between beginDelivery and here; there is
	// then nothing to project.
	live := a.inbox.get(ev.EventID)
	if live == nil {
		return
	}
	if emitErr := ctx.EmitEvent(eventDelivered, gen.GlassEventDeliveredEvent{Event: live.toGen()}); emitErr != nil {
		ctx.Logger().Error("glassinteract: emit event.delivered", "err", emitErr)
	}
}

// decodeEventDeliverResp decodes a coordinator.ingest_glass_event result that
// may arrive as the typed struct, raw JSON bytes, or a generic map.
func decodeEventDeliverResp(result any) (gen.GlassEventDeliverResp, bool) {
	switch v := result.(type) {
	case gen.GlassEventDeliverResp:
		return v, true
	case []byte:
		var resp gen.GlassEventDeliverResp
		if err := json.Unmarshal(v, &resp); err != nil {
			return gen.GlassEventDeliverResp{}, false
		}
		return resp, true
	case map[string]interface{}:
		b, err := json.Marshal(v)
		if err != nil {
			return gen.GlassEventDeliverResp{}, false
		}
		var resp gen.GlassEventDeliverResp
		if err := json.Unmarshal(b, &resp); err != nil {
			return gen.GlassEventDeliverResp{}, false
		}
		return resp, true
	default:
		return gen.GlassEventDeliverResp{}, false
	}
}

// recordDeliveryFailure requeues the event (bounded retry) and records the
// failed attempt on the delivery log and debug timeline.
func (a *Actor) recordDeliveryFailure(ctx actor.Context, ev *inboxEvent, now time.Time, detail string) {
	a.deliveryLog.record(false, ev.EventID, detail, now)
	a.debug.record(debugKindEventDeliverFail, ev.EventID+": "+detail, now)
	if err := a.inbox.requeue(ev.EventID, now); err != nil {
		ctx.Logger().Error("glassinteract: requeue failed delivery", "event", ev.EventID, "err", err)
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist requeued event", "err", err)
	}
}

// clearInbox drops the queue on formal offline and on new-session replacement.
func (a *Actor) clearInbox(ctx actor.PureContext, reason string) {
	a.inbox.clear()
	a.debug.record(debugKindEventInboxLost, reason, a.now())
	if err := a.Save(); err != nil {
		ctx.Logger().Error("glassinteract: persist inbox clear", "err", err)
	}
}
