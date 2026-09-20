package agent

import (
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// turnLifecycleOptions carries the optional fields needed to create a new
// Session.Turns record (when the turn has none yet) and to build the canonical
// lifecycle event. Fields that are only meaningful for a specific transition
// (PauseReason for paused, Error for failed) are validated by
// domain.ApplyTurnLifecycleEvent.
type turnLifecycleOptions struct {
	// Base fields stamped onto a freshly created record. Ignored when the
	// record already exists (its identity is preserved).
	role      string
	seq       int64
	timestamp string
	turnOrder int64
	startedAt string

	// Canonical lifecycle event fields forwarded to the reducer.
	completedAt string
	errorMsg    string
	pauseReason string

	// extra payload merged onto the emitted turn event for legacy subscribers
	// (the frontend reads state/startedAt/completedAt/error/turnOrder from the
	// payload map). Reducer-derived values always win.
	payload map[string]any
}

// applyTurnLifecycle is the single chokepoint through which the agent actor
// mutates a turn record's lifecycle state. It locates (or creates) the turn
// record in a.Session.Turns, lets prepare stamp non-lifecycle fields (usage,
// tasks, file changes, ...), applies the canonical reducer
// domain.ApplyTurnLifecycleEvent (which enforces revision monotonicity, field
// constraints and legal transitions), then persists and emits a compatible
// lifecycle event.
//
// kind must be one of the domain.TurnLifecycle* event kinds (which share their
// string values with the legacy domain.Turn* kinds). The emitted event is a
// legacy domain.TurnEvent carrying the canonical fields in its Payload so
// existing subscribers keep working, while the persisted record carries the
// authoritative Revision/State/PauseReason.
//
// The returned record is the post-event record (a copy); on a reducer rejection
// the pre-event record is returned together with the error and no persistence
// or emit happens.
func (a *Actor) applyTurnLifecycle(ctx actor.Context, kind, turnID string, prepare func(rec *domain.Turn), opts turnLifecycleOptions) (domain.Turn, error) {
	if turnID == "" {
		return domain.Turn{}, fmt.Errorf("agent: apply turn lifecycle: empty turn id")
	}
	expectedState := domain.LifecycleEventExpectedState(kind)
	if expectedState == "" {
		return domain.Turn{}, fmt.Errorf("agent: unknown lifecycle kind %q", kind)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	idx := -1
	var rec domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == turnID {
			idx = i
			rec = a.Session.Turns[i]
			break
		}
	}
	existed := idx >= 0
	appended := false
	if !existed {
		// Brand-new record: leave State empty so the first lifecycle event
		// establishes it. NormalizeTurnForLifecycle would default "" to
		// "completed", which would block a fresh turn.started transition.
		rec = domain.Turn{
			ID:        turnID,
			Role:      opts.role,
			Seq:       opts.seq,
			Timestamp: opts.timestamp,
			TurnOrder: opts.turnOrder,
			StartedAt: opts.startedAt,
		}
		idx = len(a.Session.Turns)
		a.Session.Turns = append(a.Session.Turns, rec)
		appended = true
	} else {
		rec = domain.NormalizeTurnForLifecycle(rec)
	}

	if prepare != nil {
		prepare(&rec)
	}

	turnOrder := rec.TurnOrder
	if turnOrder == 0 {
		turnOrder = opts.turnOrder
	}
	startedAt := rec.StartedAt
	if startedAt == "" {
		startedAt = opts.startedAt
	}
	revision := rec.Revision + 1
	if revision < 1 {
		revision = 1
	}

	event := domain.TurnLifecycleEvent{
		Kind:        kind,
		TurnID:      turnID,
		State:       expectedState,
		Revision:    revision,
		TurnOrder:   turnOrder,
		StartedAt:   startedAt,
		CompletedAt: opts.completedAt,
		Error:       opts.errorMsg,
		PauseReason: opts.pauseReason,
	}
	updated, err := domain.ApplyTurnLifecycleEvent(rec, event, now)
	if err != nil {
		// Roll back a record we created for this call so a rejected transition
		// (e.g. failed without an error, or an illegal revival) does not leave a
		// half-initialized entry in Session.Turns.
		if appended {
			a.Session.Turns = a.Session.Turns[:len(a.Session.Turns)-1]
		}
		ctx.Logger().Warn("agent: turn lifecycle event rejected",
			"kind", kind, "turn", turnID, "error", err,
			"recordState", rec.State, "recordRevision", rec.Revision)
		return rec, err
	}
	a.Session.Turns[idx] = updated

	// Mirror the lifecycle state into the derived status snapshot so the pure
	// session.summary handler and workspace status reflect it immediately.
	a.status.State = updated.State
	switch updated.State {
	case domain.TurnStateRunning:
		a.status.PauseKind = ""
	case domain.TurnStatePaused:
		a.status.PauseKind = opts.pauseReason
	}

	a.takeSnapshot()
	a.saveMailbox(ctx)

	payload := map[string]any{
		"state":     updated.State,
		"revision":  updated.Revision,
		"turnOrder": updated.TurnOrder,
	}
	if updated.StartedAt != "" {
		payload["startedAt"] = updated.StartedAt
	}
	if updated.CompletedAt != "" {
		payload["completedAt"] = updated.CompletedAt
	}
	if updated.Error != "" {
		payload["error"] = updated.Error
	}
	if updated.PauseReason != "" {
		payload["pauseReason"] = updated.PauseReason
	}
	for k, v := range opts.payload {
		if _, ok := payload[k]; !ok {
			payload[k] = v
		}
	}
	_ = a.emitTurnLifecycle(ctx, domain.TurnEvent{
		Kind:    kind,
		TurnID:  turnID,
		Payload: payload,
	})

	return updated, nil
}

// turnRecordState returns the lifecycle state of the given turn record in
// Session.Turns, or "" when no record exists.
func (a *Actor) turnRecordState(turnID string) string {
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == turnID {
			return a.Session.Turns[i].State
		}
	}
	return ""
}

// applyTurnLifecycleEventToRecord applies a single canonical lifecycle event to the
// turn record in a.Session.Turns, creating the record if it does not exist. It is
// the reducer-only path used by recovery code: it does not emit events, persist,
// take snapshots, or update a.status. The caller is responsible for those side
// effects.
//
// For a newly created record the caller must supply a non-zero TurnOrder and a
// StartedAt in the event; for an existing record these are backfilled from the
// persisted record when zero. Revision must be >= the record's current revision
// (typically current+1 or 1 for a fresh record). If the reducer rejects the
// event, any record appended by this helper is rolled back and the pre-event
// record is returned together with the error.
func (a *Actor) applyTurnLifecycleEventToRecord(turnID string, event domain.TurnLifecycleEvent) (domain.Turn, bool, error) {
	if turnID == "" {
		return domain.Turn{}, false, fmt.Errorf("agent: apply turn lifecycle: empty turn id")
	}
	expectedState := domain.LifecycleEventExpectedState(event.Kind)
	if expectedState == "" {
		return domain.Turn{}, false, fmt.Errorf("agent: unknown lifecycle kind %q", event.Kind)
	}
	if event.State != expectedState {
		return domain.Turn{}, false, fmt.Errorf("agent: event kind %q expects state %q, got %q", event.Kind, expectedState, event.State)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	idx := -1
	var rec domain.Turn
	for i := range a.Session.Turns {
		if a.Session.Turns[i].ID == turnID {
			idx = i
			rec = a.Session.Turns[i]
			break
		}
	}
	existed := idx >= 0
	appended := false
	if !existed {
		// Brand-new record: leave State empty so the first lifecycle event
		// establishes it. The caller must provide TurnOrder and StartedAt.
		rec = domain.Turn{
			ID:        turnID,
			Role:      "assistant",
			TurnOrder: event.TurnOrder,
			StartedAt: event.StartedAt,
			Timestamp: now,
		}
		idx = len(a.Session.Turns)
		a.Session.Turns = append(a.Session.Turns, rec)
		appended = true
	} else {
		rec = domain.NormalizeTurnForLifecycle(rec)
		if event.TurnOrder == 0 && rec.TurnOrder > 0 {
			event.TurnOrder = rec.TurnOrder
		}
		if event.StartedAt == "" && rec.StartedAt != "" {
			event.StartedAt = rec.StartedAt
		}
	}

	updated, err := domain.ApplyTurnLifecycleEvent(rec, event, now)
	if err != nil {
		if appended {
			a.Session.Turns = a.Session.Turns[:len(a.Session.Turns)-1]
		}
		return rec, existed, err
	}
	a.Session.Turns[idx] = updated
	return updated, existed, nil
}
