package domain

import (
	"fmt"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Canonical turn lifecycle states.
const (
	TurnStateRunning   = "running"
	TurnStatePaused    = "paused"
	TurnStateCompleted = "completed"
	TurnStateFailed    = "failed"
	TurnStateCancelled = "cancelled"
	TurnStateAbandoned = "abandoned"
	TurnStateWaiting   = "waiting"

	// TurnStateLegacyResumed is the historical terminal-like state emitted by
	// older engines. It is normalized to TurnStateCompleted on read and must never
	// be written by new code.
	TurnStateLegacyResumed = "resumed"
)

// Canonical pause reasons for TurnStatePaused.
const (
	PauseReasonUser         = "user"
	PauseReasonInteraction  = "interaction"
	PauseReasonPermission   = "permission"
	PauseReasonPlanApproval = "plan_approval"
	PauseReasonGoalInput    = "goal_input"
	PauseReasonRecovery     = "recovery"
)

// Canonical turn lifecycle event kinds emitted on the turn topic.
const (
	TurnLifecycleStarted   = "turn.started"
	TurnLifecyclePaused    = "turn.paused"
	TurnLifecycleResumed   = "turn.resumed"
	TurnLifecycleCompleted = "turn.completed"
	TurnLifecycleFailed    = "turn.failed"
	TurnLifecycleCancelled = "turn.cancelled"
	TurnLifecycleAbandoned = "turn.abandoned"
	TurnLifecycleWaiting  = "turn.waiting"
)

// TurnLifecycleEvent is the canonical lifecycle event payload for turn state
// transitions. It is the public contract for the turn topic and supersedes the
// ad-hoc Payload map used by TurnEvent for lifecycle kinds.
type TurnLifecycleEvent = gen.TurnLifecycleEvent

// IsTerminalTurnState reports whether the canonical state is terminal.
func IsTerminalTurnState(state string) bool {
	switch state {
	case TurnStateCompleted, TurnStateFailed, TurnStateCancelled, TurnStateAbandoned:
		return true
	}
	return false
}

// IsValidTurnState reports whether the state is a known canonical state.
// Historical "resumed" is not a valid write state and is rejected here.
func IsValidTurnState(state string) bool {
	_, ok := validTurnStates[state]
	return ok
}

// IsValidPauseReason reports whether reason is a known pause reason.
func IsValidPauseReason(reason string) bool {
	_, ok := validPauseReasons[reason]
	return ok
}

// NormalizeTurnState migrates historical states to the canonical vocabulary.
// The legacy "resumed" state is read-only and maps to completed.
func NormalizeTurnState(state string) string {
	if state == TurnStateLegacyResumed {
		return TurnStateCompleted
	}
	return state
}

// CanTransitionTurnState reports whether a lifecycle transition is legal.
// Same-state transitions are always allowed. Terminal states cannot transition
// to any other state (including other terminal states), which prevents
// revival of failed/cancelled/abandoned turns and accidental resurrection of
// completed turns.
func CanTransitionTurnState(from, to string) bool {
	from = NormalizeTurnState(from)
	if from == to {
		return true
	}
	if IsTerminalTurnState(from) {
		return false
	}
	return IsValidTurnState(to)
}

// LifecycleEventExpectedState returns the canonical state implied by a lifecycle
// event kind. It returns an empty string for unknown kinds.
func LifecycleEventExpectedState(kind string) string {
	return lifecycleStateByKind[kind]
}

// ApplyTurnLifecycleEvent applies a single canonical lifecycle event to a turn
// record and returns the updated record. It is a pure function: it does not
// mutate the input record, emit events, or touch persistence. The caller
// (actor) is responsible for persisting the result and executing side effects.
//
// Validation rules:
//   - event.TurnID, if non-empty, must match record.ID.
//   - event.State must be a valid canonical state and must match the event Kind.
//   - The transition from record.State to event.State must be legal.
//   - event.Revision must be >= record.Revision; stale revisions are rejected.
//   - Paused events must carry a valid PauseReason.
//   - Failed events must carry a non-empty Error.
//   - Terminal events clear PauseReason and ResumeDescriptor.
//
// now is an RFC3339 timestamp used to fill CompletedAt for terminal transitions
// when the event does not provide one.
func ApplyTurnLifecycleEvent(record gen.Turn, event gen.TurnLifecycleEvent, now string) (gen.Turn, error) {
	if event.TurnID != "" && event.TurnID != record.ID {
		return record, fmt.Errorf("turn id mismatch: record %q, event %q", record.ID, event.TurnID)
	}

	expectedState := LifecycleEventExpectedState(event.Kind)
	if expectedState != "" && event.State != expectedState {
		return record, fmt.Errorf("event kind %q expects state %q, got %q", event.Kind, expectedState, event.State)
	}

	nextState := NormalizeTurnState(event.State)
	if !IsValidTurnState(nextState) {
		return record, fmt.Errorf("unknown turn state %q", event.State)
	}

	currentState := NormalizeTurnState(record.State)
	if !CanTransitionTurnState(currentState, nextState) {
		return record, fmt.Errorf("illegal transition from %q to %q", record.State, event.State)
	}

	if event.Revision < record.Revision {
		return record, fmt.Errorf("stale event revision %d < record revision %d", event.Revision, record.Revision)
	}

	out := record
	out.State = nextState
	if event.Revision > out.Revision {
		out.Revision = event.Revision
	}
	if event.TurnOrder > out.TurnOrder {
		out.TurnOrder = event.TurnOrder
	}
	if out.StartedAt == "" && event.StartedAt != "" {
		out.StartedAt = event.StartedAt
	}

	switch nextState {
	case TurnStateRunning:
		out.Error = ""
		out.PauseReason = ""
		out.ResumeDescriptor = ""
	case TurnStatePaused:
		if event.PauseReason == "" {
			return record, fmt.Errorf("paused transition requires PauseReason")
		}
		if !IsValidPauseReason(event.PauseReason) {
			return record, fmt.Errorf("unknown PauseReason %q", event.PauseReason)
		}
		out.PauseReason = event.PauseReason
		out.Error = ""
	case TurnStateWaiting:
		out.Error = ""
		out.PauseReason = ""
		out.ResumeDescriptor = ""
		out.CompletedAt = firstNonEmpty(event.CompletedAt, now)
	case TurnStateCompleted:
		out.Error = ""
		out.PauseReason = ""
		out.ResumeDescriptor = ""
		out.CompletedAt = firstNonEmpty(event.CompletedAt, now)
	case TurnStateFailed:
		if event.Error == "" {
			return record, fmt.Errorf("failed transition requires Error")
		}
		out.Error = event.Error
		out.PauseReason = ""
		out.ResumeDescriptor = ""
		out.CompletedAt = firstNonEmpty(event.CompletedAt, now)
	case TurnStateCancelled:
		out.Error = ""
		out.PauseReason = ""
		out.ResumeDescriptor = ""
		out.Cancelled = true
		out.CompletedAt = firstNonEmpty(event.CompletedAt, now)
	case TurnStateAbandoned:
		out.Error = event.Error
		out.PauseReason = ""
		out.ResumeDescriptor = ""
		out.CompletedAt = firstNonEmpty(event.CompletedAt, now)
	}

	return out, nil
}

// NormalizeTurnForLifecycle migrates a legacy turn record to the canonical
// lifecycle vocabulary. It should be called when loading old persisted sessions.
// It does not assign a revision if the record already has one; revision=0 is
// treated as "pre-revision" and will be assigned by the first lifecycle event.
func NormalizeTurnForLifecycle(record gen.Turn) gen.Turn {
	out := record
	out.State = NormalizeTurnState(out.State)
	if out.State == "" {
		out.State = TurnStateCompleted
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
