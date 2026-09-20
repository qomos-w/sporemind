package glassinteract

import (
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const callableDebugSimulate = "glass_interact.debug_simulate"

// handleDebugSimulate is a debug-only public surface that drives the same actor
// state and emits the same events as the real Coordinator → MentraOS flow, but
// without requiring an active glass session. It lets the web debug panel render
// prefab frames, trigger speak, enqueue events, and simulate telemetry/HUD.
func (a *Actor) handleDebugSimulate(ctx actor.PureContext, req gen.GlassDebugSimulateReq) (gen.GlassDebugSimulateResp, error) {
	now := a.now()
	cmd := strings.ToLower(strings.TrimSpace(req.Command))
	switch cmd {
	case "render_idle":
		frame := IdleFrame(a.now(), a.agentMon, a.hud.batteryBar(), a.hud.connectionGlyph())
		a.setDisplayFrame(frame, frameModeIdle)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		emitDebugRender(ctx, frame, now)
		return resp("idle frame rendered"), nil

	case "render_notification":
		frame := NotificationFrame(req.Body, req.Source, req.TimeAgo)
		a.setDisplayFrame(frame, frameModeNotification)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		emitDebugRender(ctx, frame, now)
		return resp(fmt.Sprintf("notification rendered: %s", req.Body)), nil

	case "render_askuser":
		frame := AskUserFrame(req.Context, req.Title, req.Options, int(req.SelectedIdx))
		a.setDisplayFrame(frame, frameModeAskUser)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		emitDebugRender(ctx, frame, now)
		return resp(fmt.Sprintf("askuser rendered: %d options", len(req.Options))), nil

	case "render_transcript":
		remaining := 30
		if req.SelectedIdx != 0 {
			remaining = int(req.SelectedIdx)
		}
		frame := TranscriptFrame(req.Body, remaining)
		a.setDisplayFrame(frame, frameModeTranscript)
		a.debug.record(debugKindRender, renderDetail(frame), now)
		emitDebugRender(ctx, frame, now)
		return resp("transcript frame rendered"), nil

	case "speak":
		text := strings.TrimSpace(req.Body)
		if text == "" {
			a.debug.record(debugKindError, "simulate speak: empty text", now)
			return gen.GlassDebugSimulateResp{Applied: false}, fmt.Errorf("simulate speak: text is required")
		}
		msgID := req.Title
		if msgID == "" {
			msgID = fmt.Sprintf("debug-%d", now.UnixNano())
		}
		dup := a.speak.suppress(now, msgID)
		kind := debugKindSpeak
		if dup {
			kind = debugKindSpeakDup
		}
		a.debug.record(kind, msgID, now)
		emitDebugSpeak(ctx, text, msgID, dup, now)
		return resp(fmt.Sprintf("speak %s (dup=%v)", msgID, dup)), nil

	case "enqueue":
		summary := req.Title
		if summary == "" {
			summary = "debug event"
		}
		priority := req.Priority
		if priority == "" {
			priority = "normal"
		}
		enqResp, err := a.inbox.enqueue(now, gen.GlassEventEnqueueReq{
			Source:   "debug",
			Kind:     "simulate",
			Priority: priority,
			Payload:  summary,
		})
		if err != nil {
			a.debug.record(debugKindError, "simulate enqueue: "+err.Error(), now)
			return gen.GlassDebugSimulateResp{Applied: false}, fmt.Errorf("simulate enqueue: %w", err)
		}
		if enqResp.Accepted {
			emitDebugEventEnqueued(ctx, enqResp.EventID, summary, priority, now)
		}
		return resp(fmt.Sprintf("enqueue accepted=%v id=%s reason=%s", enqResp.Accepted, enqResp.EventID, enqResp.Reason)), nil

	case "clear":
		frame := gen.GlassRenderFrame{Text: ""}
		a.setDisplayFrame(frame, frameModeIdle)
		a.debug.record(debugKindRender, "clear", now)
		emitDebugRender(ctx, frame, now)
		return resp("display cleared"), nil

	case "battery":
		level := int(req.BatteryLevel)
		charging := false
		if a.hud.setBattery(level, charging, true) {
			a.emitHudUpdate(ctx)
		}
		return resp(fmt.Sprintf("battery simulated level=%d", level)), nil

	case "agent_running":
		name := req.Source
		if name == "" {
			name = "debug"
		}
		if a.hud.setAgent(gen.AgentStatusResp{State: agentStateRunning, DisplayName: name}) {
			a.emitHudUpdate(ctx)
		}
		return resp(fmt.Sprintf("agent running: %s", name)), nil

	case "agent_idle":
		name := req.Source
		if name == "" {
			name = "debug"
		}
		if a.hud.setAgent(gen.AgentStatusResp{State: "idle", DisplayName: name}) {
			a.emitHudUpdate(ctx)
		}
		return resp(fmt.Sprintf("agent idle: %s", name)), nil

	case "button_tap":
		emitDebugInteraction(ctx, "tap", req.Title, now)
		return resp("button tap emitted"), nil

	case "button_hold":
		emitDebugInteraction(ctx, "hold", req.Title, now)
		return resp("button hold emitted"), nil

	case "swipe_up":
		emitDebugInteraction(ctx, "swipe_up", req.Title, now)
		return resp("swipe up emitted"), nil

	case "swipe_down":
		emitDebugInteraction(ctx, "swipe_down", req.Title, now)
		return resp("swipe down emitted"), nil

	default:
		a.debug.record(debugKindError, "simulate: unknown command "+cmd, now)
		return gen.GlassDebugSimulateResp{Applied: false}, fmt.Errorf("unknown simulate command %q", req.Command)
	}
}

func resp(detail string) gen.GlassDebugSimulateResp {
	return gen.GlassDebugSimulateResp{Applied: true, Detail: detail}
}

func emitDebugRender(ctx actor.PureContext, frame gen.GlassRenderFrame, now time.Time) {
	_ = ctx.EmitEvent(eventRender, gen.GlassRenderEvent{
		SessionID:  "debug",
		Generation: 0,
		Frame:      frame,
		Timestamp:  formatTime(now),
	})
}

func emitDebugSpeak(ctx actor.PureContext, text, msgID string, dup bool, now time.Time) {
	_ = ctx.EmitEvent(eventSpeak, gen.GlassSpeakEvent{
		SessionID:  "debug",
		Generation: 0,
		MessageID:  msgID,
		Text:       text,
		Duplicate:  dup,
		Timestamp:  formatTime(now),
	})
}

func emitDebugEventEnqueued(ctx actor.PureContext, eventID, summary, priority string, now time.Time) {
	_ = ctx.EmitEvent("glass.event.enqueued", gen.GlassEventEnqueuedEvent{
		Event: gen.GlassEvent{
			EventID:   eventID,
			Source:    "debug",
			Kind:      "simulate",
			Priority:  priority,
			Payload:   summary,
			State:     "pending",
			CreatedAt: formatTime(now),
		},
	})
}

func emitDebugInteraction(ctx actor.PureContext, action, elementID string, now time.Time) {
	_ = ctx.EmitEvent(eventInteraction, gen.GlassInteractionEvent{
		SessionID:  "debug",
		Generation: 0,
		Tick:       0,
		ElementID:  elementID,
		Action:     action,
		Timestamp:  formatTime(now),
	})
}
