// Coordinator-only high-level wearable interaction tools (Stage 4).
//
// The Coordinator LLM sees exactly two wearable-facing tools —
// coordinator.wearable.notify and coordinator.wearable.call — exposed through
// the Coordinator-only builtin:bundle:coordinator-wearable bundle. Their
// handlers resolve the glassinteract service and call only the existing
// high-level internal Glass Interact callables glass_interact.speak /
// glass_interact.render; the raw glass_interact render/speak/debug/internal
// low-level callables are never exposed to the LLM.
//
// notify fires high-level speech (speak) and/or display (render). call
// delivers an interaction prompt through the same high-level surface and
// records a bounded pending interaction response on the Coordinator
// (runtime-only, never persisted) for later correlation. There is no raw
// cross-session interaction channel; when no active online glass session
// exists the underlying speak/render call fails and the error is propagated
// verbatim with the tool name.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	coordinatorWearableNotifyCallable = "coordinator_wearable_notify"
	coordinatorWearableCallCallable   = "coordinator_wearable_call"

	// coordinatorGlassReplyIntake is the internal callable Glass Interact
	// delivers a confirmed interaction report to. It must match the
	// glassinteract-side constant in pkg/actor/glassinteract/interact.go.
	coordinatorGlassReplyIntake = "coordinator_ingest_glass_reply"

	// glassSpeakCallable and glassRenderCallable are the only high-level
	// Glass Interact output callables the wearable tools may invoke.
	glassSpeakCallable  = "glass_interact.speak"
	glassRenderCallable = "glass_interact.render"

	// coordinatorWearableInteractionsMax bounds the Coordinator-only pending
	// interaction record (runtime-only, never persisted).
	coordinatorWearableInteractionsMax = 32
)

// coordWearableInteraction is one pending interaction record kept by the
// Coordinator so a later wearable reply can be correlated with the prompt.
// ElementIDs are the stable scene element ids of the rendered prompt frame
// (glass.scene schema: "stable id for diffing + interaction routing"); a
// confirmed interaction report on one of these ids resolves the interaction.
type coordWearableInteraction struct {
	InteractionID string
	Prompt        string
	MessageID     string
	ElementIDs    []string
}

// handleCoordinatorWearableNotify fires high-level speech and/or display for
// the active glass session. Text is delivered through glass_interact.speak
// (at-most-once by MessageId); a non-empty Frame is rendered through
// glass_interact.render (latest-only lastFrame). A generated MessageId is
// returned when the caller omitted one.
func (a *Actor) handleCoordinatorWearableNotify(ctx actor.PureContext, req gen.CoordinatorWearableNotifyReq) (gen.CoordinatorWearableNotifyResp, error) {
	if strings.TrimSpace(req.Text) == "" && frameEmpty(req.Frame) {
		return gen.CoordinatorWearableNotifyResp{}, fmt.Errorf("%s: at least one of text or a display frame is required", coordinatorWearableNotifyCallable)
	}
	planner, glassRef, err := a.resolveGlassInteract(ctx)
	if err != nil {
		return gen.CoordinatorWearableNotifyResp{}, fmt.Errorf("%s: %w", coordinatorWearableNotifyCallable, err)
	}
	resp := gen.CoordinatorWearableNotifyResp{
		MessageID: strings.TrimSpace(req.MessageID),
	}
	if resp.MessageID == "" {
		resp.MessageID = newWearableID("wm_")
	}
	var firstErr error
	if strings.TrimSpace(req.Text) != "" {
		speakResp, err := callGlassSpeak(ctx, planner, glassRef, gen.GlassSpeakReq{
			Text:       req.Text,
			MessageID:  resp.MessageID,
			SessionID:  req.SessionID,
			Generation: req.Generation,
		})
		if err != nil {
			firstErr = fmt.Errorf("%s: speak: %w", coordinatorWearableNotifyCallable, err)
			resp.Error = firstErr.Error()
		} else {
			resp.Spoken = true
			resp.Duplicate = speakResp.Duplicate
			resp.SessionID = speakResp.SessionID
			resp.Generation = speakResp.Generation
		}
	}
	if req.Frame != nil && !frameEmpty(req.Frame) {
		renderResp, err := callGlassRender(ctx, planner, glassRef, gen.GlassRenderReq{
			Frame:      *req.Frame,
			SessionID:  req.SessionID,
			Generation: req.Generation,
		})
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: render: %w", coordinatorWearableNotifyCallable, err)
			}
			if resp.Error == "" {
				resp.Error = firstErr.Error()
			}
		} else {
			resp.Displayed = true
			if resp.SessionID == "" {
				resp.SessionID = renderResp.SessionID
			}
			if resp.Generation == 0 {
				resp.Generation = renderResp.Generation
			}
		}
	}
	resp.Applied = resp.Spoken || resp.Displayed
	return resp, firstErr
}

// handleCoordinatorWearableCall delivers an interaction prompt to the active
// glass session through the high-level speak surface (and an optional rendered
// frame), then records a bounded pending interaction on the Coordinator so the
// eventual reply can be correlated. It returns the pending interaction
// response with a generated InteractionId. When no session is online the
// underlying speak failure is propagated as a clear error.
func (a *Actor) handleCoordinatorWearableCall(ctx actor.PureContext, req gen.CoordinatorWearableCallReq) (gen.CoordinatorWearableCallResp, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return gen.CoordinatorWearableCallResp{}, fmt.Errorf("%s: prompt is required", coordinatorWearableCallCallable)
	}
	planner, glassRef, err := a.resolveGlassInteract(ctx)
	if err != nil {
		return gen.CoordinatorWearableCallResp{}, fmt.Errorf("%s: %w", coordinatorWearableCallCallable, err)
	}
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		messageID = newWearableID("wm_")
	}
	speakResp, err := callGlassSpeak(ctx, planner, glassRef, gen.GlassSpeakReq{
		Text:       req.Prompt,
		MessageID:  messageID,
		SessionID:  req.SessionID,
		Generation: req.Generation,
	})
	if err != nil {
		return gen.CoordinatorWearableCallResp{}, fmt.Errorf("%s: no online glass session to interact with: %w", coordinatorWearableCallCallable, err)
	}
	if req.Frame != nil && !frameEmpty(req.Frame) {
		if _, err := callGlassRender(ctx, planner, glassRef, gen.GlassRenderReq{
			Frame:      *req.Frame,
			SessionID:  req.SessionID,
			Generation: req.Generation,
		}); err != nil {
			return gen.CoordinatorWearableCallResp{}, fmt.Errorf("%s: render prompt frame: %w", coordinatorWearableCallCallable, err)
		}
	}
	interactionID := a.recordWearableInteraction(req.Prompt, messageID, req.Frame)
	return gen.CoordinatorWearableCallResp{
		InteractionID: interactionID,
		SessionID:     speakResp.SessionID,
		Generation:    speakResp.Generation,
		MessageID:     messageID,
		Pending:       true,
		Prompt:        req.Prompt,
	}, nil
}

// resolveGlassInteract returns the planner and the glassinteract service ref,
// or a clear error when either is unavailable.
func (a *Actor) resolveGlassInteract(ctx actor.PureContext) (actor.Planner, ref.Ref, error) {
	planner := ctx.Planner()
	if planner == nil {
		return nil, nil, fmt.Errorf("planner unavailable")
	}
	glassRef, ok := ctx.LookupService("glass_interact")
	if !ok {
		return nil, nil, fmt.Errorf("glassinteract service unavailable")
	}
	return planner, glassRef, nil
}

// callGlassSpeak invokes the internal high-level glass_interact.speak callable
// and decodes the typed response.
func callGlassSpeak(ctx actor.PureContext, planner actor.Planner, glassRef ref.Ref, req gen.GlassSpeakReq) (gen.GlassSpeakResp, error) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, glassRef, glassSpeakCallable, req).Await()
	if err != nil {
		return gen.GlassSpeakResp{}, err
	}
	return decodeGlassResp[gen.GlassSpeakResp](result, glassSpeakCallable)
}

// callGlassRender invokes the internal high-level glass_interact.render
// callable and decodes the typed response.
func callGlassRender(ctx actor.PureContext, planner actor.Planner, glassRef ref.Ref, req gen.GlassRenderReq) (gen.GlassRenderResp, error) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, glassRef, glassRenderCallable, req).Await()
	if err != nil {
		return gen.GlassRenderResp{}, err
	}
	return decodeGlassResp[gen.GlassRenderResp](result, glassRenderCallable)
}

// decodeGlassResp decodes a planner call result into the typed response,
// accepting the raw generated struct, JSON bytes, or a JSON object map.
func decodeGlassResp[T any](result any, callable string) (T, error) {
	var zero T
	switch v := result.(type) {
	case T:
		return v, nil
	case []byte:
		if err := json.Unmarshal(v, &zero); err != nil {
			return zero, err
		}
		return zero, nil
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		if err := json.Unmarshal(b, &zero); err != nil {
			return zero, err
		}
		return zero, nil
	default:
		return zero, fmt.Errorf("%s returned unexpected type %T", callable, result)
	}
}

// recordWearableInteraction appends one pending interaction to the bounded
// Coordinator-only runtime record and returns its generated id. The element
// ids of the rendered prompt frame are recorded so a later confirmed
// interaction report can be correlated back to this interaction.
func (a *Actor) recordWearableInteraction(prompt, messageID string, frame *gen.GlassRenderFrame) string {
	var elementIDs []string
	if frame != nil && frame.Scene != nil {
		for _, el := range frame.Scene.Elements {
			if id := strings.TrimSpace(el.ID); id != "" {
				elementIDs = append(elementIDs, id)
			}
		}
	}
	a.wearableInteractionsMu.Lock()
	defer a.wearableInteractionsMu.Unlock()
	id := newWearableID("wi_")
	a.wearableInteractions = append(a.wearableInteractions, coordWearableInteraction{
		InteractionID: id,
		Prompt:        prompt,
		MessageID:     messageID,
		ElementIDs:    elementIDs,
	})
	if len(a.wearableInteractions) > coordinatorWearableInteractionsMax {
		a.wearableInteractions = a.wearableInteractions[len(a.wearableInteractions)-coordinatorWearableInteractionsMax:]
	}
	return id
}

// resolveWearableInteractionReply matches a confirmed interaction report
// against the pending interactions by element id, removes the matched record,
// and returns the reply turn text (empty when nothing matched).
func (a *Actor) resolveWearableInteractionReply(ev gen.GlassInteractionEvent) string {
	a.wearableInteractionsMu.Lock()
	defer a.wearableInteractionsMu.Unlock()
	for i, wi := range a.wearableInteractions {
		for _, id := range wi.ElementIDs {
			if id != ev.ElementID {
				continue
			}
			a.wearableInteractions = append(a.wearableInteractions[:i], a.wearableInteractions[i+1:]...)
			reply := strings.TrimSpace(ev.Value)
			if reply == "" {
				reply = ev.Action
			}
			return fmt.Sprintf("眼镜交互回复（%s，提示：%s）：%s", wi.InteractionID, wi.Prompt, reply)
		}
	}
	return ""
}

// wearableInteractionsSnapshot returns a copy of the bounded pending
// interaction record (used by tests).
func (a *Actor) wearableInteractionsSnapshot() []coordWearableInteraction {
	a.wearableInteractionsMu.Lock()
	defer a.wearableInteractionsMu.Unlock()
	out := make([]coordWearableInteraction, len(a.wearableInteractions))
	copy(out, a.wearableInteractions)
	return out
}

// frameEmpty reports whether a render frame carries no displayable content.
func frameEmpty(f *gen.GlassRenderFrame) bool {
	if f == nil {
		return true
	}
	sceneEmpty := f.Scene == nil || len(f.Scene.Elements) == 0
	return strings.TrimSpace(f.Text) == "" &&
		strings.TrimSpace(f.ImageURL) == "" &&
		strings.TrimSpace(f.Layout) == "" &&
		strings.TrimSpace(f.Meta) == "" &&
		sceneEmpty
}

// newWearableID returns a cryptographically random short id with the given
// prefix, falling back to a time-based suffix when the entropy source fails.
func newWearableID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	return prefix + hex.EncodeToString(b)
}
