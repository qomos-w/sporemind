package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// coordinatorWearableCtx builds a Coordinator actor with a FakeCtx whose
// glassinteract service lookup resolves to a fake ref and whose planner
// records every glass_interact.speak / glass_interact.render call. The fake
// returns success by default; per-call behavior can be overridden through
// invokeFn (return a non-nil error to simulate a Glass Interact failure).
func coordinatorWearableCtx(t *testing.T, invokeFn func(callID string, payload any) (any, error)) (*testutil.FakeCtx, *Actor) {
	t.Helper()
	if invokeFn == nil {
		invokeFn = func(string, any) (any, error) { return nil, nil }
	}
	glassRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		v, err := invokeFn(callID, payload)
		if err != nil {
			return err
		}
		return v
	})
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "glass_interact" {
			return glassRef, true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				v, err := invokeFn(callID, payload)
				if err != nil {
					return nil, err
				}
				switch callID {
				case glassSpeakCallable:
					req, _ := payload.(gen.GlassSpeakReq)
					return gen.GlassSpeakResp{SessionID: "gs_aaa", Generation: 1, MessageID: req.MessageID, Duplicate: false}, nil
				case glassRenderCallable:
					return gen.GlassRenderResp{SessionID: "gs_aaa", Generation: 1, Applied: true}, nil
				}
				return v, nil
			},
		}
	}
	a := &Actor{
		agentKind:   string(domain.AgentKindCoordinator),
		DisplayName: "Coordinator",
		Session:     domain.Session{},
		RawSession:  domain.RawSession{},
	}
	return ctx, a
}

// captureWearableCalls returns an invokeFn that records speak/render payloads
// and optionally injects errors for a specific callable.
func captureWearableCalls(speaks *[]gen.GlassSpeakReq, renders *[]gen.GlassRenderReq, errCallable, errMsg string) func(string, any) (any, error) {
	return func(callID string, payload any) (any, error) {
		if errCallable != "" && callID == errCallable {
			return nil, fmt.Errorf("%s", errMsg)
		}
		switch v := payload.(type) {
		case gen.GlassSpeakReq:
			if speaks != nil {
				*speaks = append(*speaks, v)
			}
		case gen.GlassRenderReq:
			if renders != nil {
				*renders = append(*renders, v)
			}
		}
		return nil, nil
	}
}

func TestHandleCoordinatorWearableNotify_SpeakOnly(t *testing.T) {
	var speaks []gen.GlassSpeakReq
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(&speaks, nil, "", ""))

	resp, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{Text: "收到"})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !resp.Applied || !resp.Spoken || resp.Displayed {
		t.Fatalf("notify resp = %+v, want applied+spoken only", resp)
	}
	if resp.Duplicate || resp.SessionID != "gs_aaa" || resp.Generation != 1 {
		t.Fatalf("notify resp session fields = %+v", resp)
	}
	if !strings.HasPrefix(resp.MessageID, "wm_") {
		t.Fatalf("generated message id = %q, want wm_ prefix", resp.MessageID)
	}
	if len(speaks) != 1 {
		t.Fatalf("speak calls = %d, want 1", len(speaks))
	}
	if speaks[0].Text != "收到" || speaks[0].MessageID != resp.MessageID {
		t.Fatalf("speak payload = %+v", speaks[0])
	}
}

func TestHandleCoordinatorWearableNotify_RenderOnly(t *testing.T) {
	var renders []gen.GlassRenderReq
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(nil, &renders, "", ""))

	frame := gen.GlassRenderFrame{Text: "天气晴朗", Layout: "top"}
	resp, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{Frame: &frame})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !resp.Applied || resp.Spoken || !resp.Displayed {
		t.Fatalf("notify resp = %+v, want applied+displayed only", resp)
	}
	if len(renders) != 1 {
		t.Fatalf("render calls = %d, want 1", len(renders))
	}
	if renders[0].Frame.Text != "天气晴朗" || renders[0].Frame.Layout != "top" {
		t.Fatalf("render payload = %+v", renders[0])
	}
}

func TestHandleCoordinatorWearableNotify_BothChannels(t *testing.T) {
	var speaks []gen.GlassSpeakReq
	var renders []gen.GlassRenderReq
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(&speaks, &renders, "", ""))

	frame := gen.GlassRenderFrame{Text: "状态", Meta: "ok"}
	resp, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{
		Text:      "请注意",
		MessageID: "wm_fixed",
		Frame:     &frame,
	})
	if err != nil {
		t.Fatalf("notify: %v", err)
	}
	if !resp.Applied || !resp.Spoken || !resp.Displayed {
		t.Fatalf("notify resp = %+v, want both channels", resp)
	}
	if resp.MessageID != "wm_fixed" {
		t.Fatalf("explicit message id = %q, want wm_fixed", resp.MessageID)
	}
	if len(speaks) != 1 || len(renders) != 1 {
		t.Fatalf("speaks=%d renders=%d, want 1 each", len(speaks), len(renders))
	}
	if speaks[0].Text != "请注意" || speaks[0].MessageID != "wm_fixed" {
		t.Fatalf("speak payload = %+v", speaks[0])
	}
}

func TestHandleCoordinatorWearableNotify_Validation(t *testing.T) {
	ctx, a := coordinatorWearableCtx(t, nil)
	if _, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{}); err == nil {
		t.Fatal("notify with no text and no frame succeeded")
	}
}

func TestHandleCoordinatorWearableNotify_NoService(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(context.Context, ref.Ref, string, any) (any, error) { return nil, nil }}
	}
	a := &Actor{agentKind: string(domain.AgentKindCoordinator)}

	_, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "glassinteract service unavailable") {
		t.Fatalf("notify no-service err = %v", err)
	}
}

func TestHandleCoordinatorWearableNotify_NoSession(t *testing.T) {
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(nil, nil, glassSpeakCallable, "glass_interact.speak: no active online glass session"))

	resp, err := a.handleCoordinatorWearableNotify(ctx, gen.CoordinatorWearableNotifyReq{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "no active online glass session") {
		t.Fatalf("notify no-session err = %v", err)
	}
	if resp.Error == "" || resp.Applied || resp.Spoken {
		t.Fatalf("notify no-session resp = %+v", resp)
	}
}

func TestHandleCoordinatorWearableCall_CreatesPendingInteraction(t *testing.T) {
	var speaks []gen.GlassSpeakReq
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(&speaks, nil, "", ""))

	resp, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{Prompt: "需要你确认是否继续"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !resp.Pending || !strings.HasPrefix(resp.InteractionID, "wi_") {
		t.Fatalf("call resp = %+v, want pending interaction", resp)
	}
	if resp.SessionID != "gs_aaa" || resp.Generation != 1 || resp.Prompt != "需要你确认是否继续" {
		t.Fatalf("call resp fields = %+v", resp)
	}
	if len(speaks) != 1 || speaks[0].Text != "需要你确认是否继续" {
		t.Fatalf("call speak payload = %+v", speaks)
	}
	if !strings.HasPrefix(speaks[0].MessageID, "wm_") || speaks[0].MessageID != resp.MessageID {
		t.Fatalf("call generated message id = %q (resp %q)", speaks[0].MessageID, resp.MessageID)
	}
	records := a.wearableInteractionsSnapshot()
	if len(records) != 1 || records[0].InteractionID != resp.InteractionID || records[0].Prompt != "需要你确认是否继续" {
		t.Fatalf("pending records = %+v", records)
	}
}

func TestHandleCoordinatorWearableCall_RecordBounded(t *testing.T) {
	ctx, a := coordinatorWearableCtx(t, nil)
	for i := 0; i < coordinatorWearableInteractionsMax+10; i++ {
		if _, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{Prompt: "p"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	records := a.wearableInteractionsSnapshot()
	if len(records) != coordinatorWearableInteractionsMax {
		t.Fatalf("pending records len = %d, want bounded %d", len(records), coordinatorWearableInteractionsMax)
	}
	if records[0].Prompt != "p" {
		t.Fatalf("oldest retained record = %+v, want tail kept", records[0])
	}
}

func TestHandleCoordinatorWearableCall_Validation(t *testing.T) {
	ctx, a := coordinatorWearableCtx(t, nil)
	if _, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{}); err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("call empty prompt err = %v", err)
	}
}

func TestHandleCoordinatorWearableCall_NoService(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(context.Context, ref.Ref, string, any) (any, error) { return nil, nil }}
	}
	a := &Actor{agentKind: string(domain.AgentKindCoordinator)}

	_, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "glassinteract service unavailable") {
		t.Fatalf("call no-service err = %v", err)
	}
}

func TestHandleCoordinatorWearableCall_NoSession(t *testing.T) {
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(nil, nil, glassSpeakCallable, "glass_interact.speak: no active online glass session"))

	_, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "no online glass session to interact with") {
		t.Fatalf("call no-session err = %v", err)
	}
}

func TestHandleCoordinatorWearableCall_RendersPromptFrame(t *testing.T) {
	var speaks []gen.GlassSpeakReq
	var renders []gen.GlassRenderReq
	ctx, a := coordinatorWearableCtx(t, captureWearableCalls(&speaks, &renders, "", ""))

	frame := gen.GlassRenderFrame{Text: "请选择", Layout: "full"}
	resp, err := a.handleCoordinatorWearableCall(ctx, gen.CoordinatorWearableCallReq{Prompt: "请选择", Frame: &frame})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !resp.Pending || len(renders) != 1 || renders[0].Frame.Text != "请选择" {
		t.Fatalf("call resp=%+v renders=%+v", resp, renders)
	}
	if len(speaks) != 1 || speaks[0].Text != "请选择" {
		t.Fatalf("call speak payload = %+v", speaks)
	}
}

// TestWearableReplyLoop closes the coordinator_wearable_call contract: the
// pending record carries the prompt frame's scene element ids, a confirmed
// interaction report on one of those ids resolves the record exactly once, and
// unmatched reports leave the pending set untouched.
func TestWearableReplyLoop(t *testing.T) {
	a := &Actor{}
	frame := gen.GlassRenderFrame{Scene: &gen.GlassScene{
		Elements: []gen.GlassSceneElement{{ID: "opt0"}, {ID: "opt1"}},
	}}
	id := a.recordWearableInteraction("是否继续？", "wm_1", &frame)
	records := a.wearableInteractionsSnapshot()
	if len(records) != 1 || len(records[0].ElementIDs) != 2 || records[0].ElementIDs[0] != "opt0" {
		t.Fatalf("pending record = %+v, want element ids opt0/opt1", records[0])
	}

	reply := a.resolveWearableInteractionReply(gen.GlassInteractionEvent{ElementID: "opt1", Action: "select", Value: "继续"})
	if !strings.Contains(reply, id) || !strings.Contains(reply, "是否继续？") || !strings.Contains(reply, "继续") {
		t.Fatalf("reply text = %q, want interaction id + prompt + value", reply)
	}
	if got := a.wearableInteractionsSnapshot(); len(got) != 0 {
		t.Fatalf("resolved interaction not removed: %+v", got)
	}
	// The pending record is single-use: a replayed report matches nothing.
	if again := a.resolveWearableInteractionReply(gen.GlassInteractionEvent{ElementID: "opt1", Action: "select"}); again != "" {
		t.Fatalf("replayed reply resolved again: %q", again)
	}
}

func TestHandleCoordinatorGlassReply_UnmatchedIsInert(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// No pending interactions: an unmatched report is not an error.
	if err := a.handleCoordinatorGlassReply(ctx, gen.GlassInteractionEvent{ElementID: "opt0", Action: "select"}); err != nil {
		t.Fatalf("unmatched reply err = %v", err)
	}
	// Element id is required.
	if err := a.handleCoordinatorGlassReply(ctx, gen.GlassInteractionEvent{Action: "select"}); err == nil {
		t.Fatal("reply without element id accepted")
	}
}

// TestCoordinatorWearableToolSpecs verifies the LLM-facing tool assembly: the
// Coordinator bundle's allowlist resolves to exactly the two high-level tools,
// named coordinator_wearable_notify / coordinator_wearable_call (flat callables
// without dots get their name unchanged), and the low-level glass_interact
// callables are never part of the tool surface.
func TestCoordinatorWearableToolSpecs(t *testing.T) {
	callables := map[string]domain.CallableInterface{
		"coordinator_wearable_notify": {
			Name: "coordinator_wearable_notify", Kind: "unary", Permission: "internal",
			Params: []domain.CallableParam{{Name: "Text", Type: "string"}},
		},
		"coordinator_wearable_call": {
			Name: "coordinator_wearable_call", Kind: "unary", Permission: "internal",
			Params: []domain.CallableParam{{Name: "Prompt", Type: "string", Required: true}},
		},
		"glass_interact.render":      {Name: "glass_interact.render", Kind: "unary", Permission: "internal"},
		"glass_interact.speak":       {Name: "glass_interact.speak", Kind: "unary", Permission: "internal"},
		"glass_interact.debug_state": {Name: "glass_interact.debug_state", Kind: "unary", Permission: "public"},
	}
	// The Coordinator-only bundle declares exactly these two callables.
	allowed := []string{"coordinator_wearable_notify", "coordinator_wearable_call"}
	specs := ToolSpecsFromCallables(callables, allowed)
	if len(specs) != 2 {
		t.Fatalf("tool specs = %d (%+v), want exactly 2", len(specs), specs)
	}
	byName := map[string]domain.ToolSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	for _, want := range []string{"coordinator_wearable_notify", "coordinator_wearable_call"} {
		s, ok := byName[want]
		if !ok {
			t.Fatalf("tool %q missing from %v", want, byName)
		}
		if s.CallableID != want {
			t.Fatalf("tool %q callable id = %q, want %q", want, s.CallableID, want)
		}
		// Agent-local flat callables (single-segment callID, no matching
		// domain) derive no ServiceName; routing falls back to ctx.Self().
		if s.ServiceName != "" {
			t.Fatalf("tool %q service = %q, want empty (agent-local flat callable)", want, s.ServiceName)
		}
	}
	if !strings.Contains(byName["coordinator_wearable_call"].InputSchema, "Prompt") {
		t.Fatalf("call tool schema = %s, want Prompt property", byName["coordinator_wearable_call"].InputSchema)
	}
}
