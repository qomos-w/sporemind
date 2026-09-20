// Coordinator-side intake for the Stage 5 online Glass event inbox.
//
// Glass Interact's session-gated updater delivers at most one inbox event at a
// time through the compact explicit internal callable
// coordinator.ingest_glass_event (there is no generic actor-to-actor event
// bus). The Coordinator validates the delivery, records it in a narrow
// Coordinator-only runtime record, acks the receipt, and completes the outcome
// back through glass_interact.event.complete so the inbox can advance to a
// terminal state.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Coordinator intake + completion protocol (Stage 5 part B). These callables
// are Coordinator-only and deliberately not part of a generic agent bus.
func (a *Actor) handleCoordinatorGlassTranscript(ctx actor.Context, transcript gen.GlassTranscript) error {
	if strings.TrimSpace(transcript.Text) == "" {
		return fmt.Errorf("coordinator.ingest_glass_transcript: final transcript text is required")
	}
	if transcript.Error != "" {
		return fmt.Errorf("coordinator.ingest_glass_transcript: transcript has STT error")
	}
	return a.startCoordinatorInputTurn(ctx, domain.TurnInput{Text: transcript.Text, Meta: "via=glass"})
}

func (a *Actor) startCoordinatorInputTurn(ctx actor.Context, turnInput domain.TurnInput) error {
	_, _, _ = a.createUserTurn(ctx, turnInput)
	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)
	a.status.State = "running"
	a.notifyWorkspaceStatus(ctx)
	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: turnInput,
		TurnName:  turnName,
	}); err != nil {
		return fmt.Errorf("schedule start_turn: %w", err)
	}
	return nil
}

const (
	// coordinatorGlassEventIntake is the compact explicit internal callable the
	// Glass Interact updater delivers one inbox event to. It must match the
	// glassinteract-side coordinatorEventIntake constant.
	coordinatorGlassEventIntake = "coordinator_ingest_glass_event"

	// glassEventCompleteCallable is the Glass Interact receipt callable the
	// Coordinator fires after processing a delivered event.
	glassEventCompleteCallable = "glass_interact.event_complete"

	// coordinatorGlassEventOutcomeHandled is the completion outcome for a
	// successfully ingested inbox event.
	coordinatorGlassEventOutcomeHandled = "handled"

	// coordinatorGlassEventsMax bounds the Coordinator-only runtime record of
	// received inbox events.
	coordinatorGlassEventsMax = 64

	coordinatorLifecycleNotify          = "coordinator_lifecycle_notify"
	coordinatorGlassEventOutcomeIgnored = "ignored"
)

// handleCoordinatorGlassEvent is the Coordinator intake for the online inbox.
// It validates the delivery, records it in the narrow Coordinator-only runtime
// record, acks the receipt, and completes the outcome asynchronously so the
// glassinteract actor — which is blocked inside deliverEvent awaiting this
// response — never deadlocks on a synchronous callback.
func (a *Actor) handleCoordinatorGlassEvent(ctx actor.Context, req gen.GlassEventDeliverReq) (gen.GlassEventDeliverResp, error) {
	if strings.TrimSpace(req.EventID) == "" || strings.TrimSpace(req.Source) == "" || strings.TrimSpace(req.Kind) == "" {
		return gen.GlassEventDeliverResp{}, fmt.Errorf("%s: event id, source and kind are required", coordinatorGlassEventIntake)
	}
	a.recordCoordinatorGlassEvent(req)
	outcome, notifyText := coordinatorGlassEventPolicy(req)
	ctx.Logger().Info("coordinator: glass event ingested", "event", req.EventID, "source", req.Source, "kind", req.Kind, "outcome", outcome)
	if notifyText != "" {
		if err := ctx.After(0, coordinatorLifecycleNotify, gen.CoordinatorWearableNotifyReq{Text: notifyText, MessageID: "lifecycle_" + req.EventID}); err != nil {
			return gen.GlassEventDeliverResp{}, fmt.Errorf("%s: schedule lifecycle notify: %w", coordinatorGlassEventIntake, err)
		}
	}
	a.completeCoordinatorGlassEvent(ctx, req.EventID, outcome)
	return gen.GlassEventDeliverResp{EventID: req.EventID, Received: true}, nil
}

func coordinatorGlassEventPolicy(req gen.GlassEventDeliverReq) (outcome, notifyText string) {
	if req.Source != "agent" {
		return coordinatorGlassEventOutcomeHandled, ""
	}
	switch req.Kind {
	case "agent.started", "agent.completed":
		return coordinatorGlassEventOutcomeIgnored, ""
	case "agent.failed", "agent.blocked":
		var payload struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(req.Payload), &payload); err != nil {
			return "failed", ""
		}
		title := strings.TrimSpace(payload.Title)
		if title == "" {
			title = "Agent"
		}
		if req.Kind == "agent.failed" {
			return coordinatorGlassEventOutcomeHandled, title + " 执行失败"
		}
		return coordinatorGlassEventOutcomeHandled, title + " 等待你的处理"
	default:
		return coordinatorGlassEventOutcomeIgnored, ""
	}
}

func (a *Actor) handleCoordinatorLifecycleNotify(ctx actor.Context, req gen.CoordinatorWearableNotifyReq) error {
	_, err := a.handleCoordinatorWearableNotify(ctx, req)
	return err
}

// recordCoordinatorGlassEvent appends one delivered event to the
// Coordinator-only runtime record (bounded, never persisted).
func (a *Actor) recordCoordinatorGlassEvent(req gen.GlassEventDeliverReq) {
	a.coordGlassEventsMu.Lock()
	defer a.coordGlassEventsMu.Unlock()
	a.coordGlassEvents = append(a.coordGlassEvents, req)
	if len(a.coordGlassEvents) > coordinatorGlassEventsMax {
		a.coordGlassEvents = a.coordGlassEvents[len(a.coordGlassEvents)-coordinatorGlassEventsMax:]
	}
}

// coordinatorGlassEventsSnapshot returns a copy of the Coordinator-only inbox
// record (used by tests and the debug surface).
func (a *Actor) coordinatorGlassEventsSnapshot() []gen.GlassEventDeliverReq {
	a.coordGlassEventsMu.Lock()
	defer a.coordGlassEventsMu.Unlock()
	out := make([]gen.GlassEventDeliverReq, len(a.coordGlassEvents))
	copy(out, a.coordGlassEvents)
	return out
}

// completeCoordinatorGlassEvent fires glass_interact.event.complete with the
// given outcome. The call is intentionally NOT awaited on the Coordinator
// owner loop: Glass Interact is blocked inside deliverEvent awaiting our
// intake response, and its complete handler synchronously consults
// agent.status, so a synchronous callback would deadlock both owner loops. The
// promise executor runs off-loop; errors are observed through the callback.
func (a *Actor) completeCoordinatorGlassEvent(ctx actor.Context, eventID, outcome string) {
	glassRef, ok := ctx.LookupService("glass_interact")
	if !ok {
		ctx.Logger().Warn("coordinator: glassinteract service unavailable; event left in_flight", "event", eventID)
		return
	}
	planner := ctx.Planner()
	if planner == nil {
		ctx.Logger().Warn("coordinator: planner unavailable; event left in_flight", "event", eventID)
		return
	}
	logger := ctx.Logger()
	planner.Call(ctx.Lifecycle(), glassRef, glassEventCompleteCallable, gen.GlassEventCompleteReq{
		EventID: eventID,
		Outcome: outcome,
	}).AsCallback(func(_ any, err error) {
		if err != nil {
			logger.Warn("coordinator: glass event completion failed", "event", eventID, "err", err)
		}
	})
}
