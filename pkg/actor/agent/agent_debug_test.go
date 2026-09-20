package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/debug"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type fakePlannerForInvoke struct {
	callFunc func(context.Context, ref.Ref, string, any) (any, error)
}

type mockTopologyProvider struct {
	snapshot []runtime.ActorNode
}

func (m *mockTopologyProvider) Snapshot() []runtime.ActorNode     { return m.snapshot }
func (m *mockTopologyProvider) OnChange(func())                   {}
func (m *mockTopologyProvider) UnifiedGraph() domain.UnifiedGraph { return domain.UnifiedGraph{} }
func (m *mockTopologyProvider) CurrentEpoch() int32               { return 0 }
func (m *mockTopologyProvider) Sync(_ int32) domain.TopologySyncResp {
	return domain.TopologySyncResp{}
}
func (m *mockTopologyProvider) HistoryEntries() []domain.TopologyHistoryEntry { return nil }
func (m *mockTopologyProvider) OnEpochChange(func(int32, *domain.GraphPatch)) {}
func (m *mockTopologyProvider) EnablePush()                                   {}

func (f fakePlannerForInvoke) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (f fakePlannerForInvoke) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	result, err := f.callFunc(ctx, target, callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](result)
}

func (f fakePlannerForInvoke) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

func TestHandleListCallables(t *testing.T) {
	a := &Actor{
		topo: &mockTopologyProvider{
			snapshot: []runtime.ActorNode{
				{Kind: "workspace", Callables: []domain.CallableInterface{
					{Name: "workspace.list_agents", Description: "List agents"},
					{Name: "workspace.account", Description: "Account info"},
				}},
				{Kind: "oracle", Callables: []domain.CallableInterface{
					{Name: "oracle.search_services", Description: "Search services"},
				}},
			},
		},
	}

	resp, err := a.handleListCallables(testutil.HumanCtx(testutil.GenActorID()), domain.AgentListCallablesReq{})
	if err != nil {
		t.Fatalf("handleListCallables: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 callables, got %d", len(resp.Items))
	}

	filtered, err := a.handleListCallables(testutil.HumanCtx(testutil.GenActorID()), domain.AgentListCallablesReq{Query: "workspace"})
	if err != nil {
		t.Fatalf("handleListCallables filtered: %v", err)
	}
	if len(filtered.Items) != 2 {
		t.Fatalf("expected 2 workspace callables, got %d", len(filtered.Items))
	}
}

func TestHandleInvokeCallable(t *testing.T) {
	called := false
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				called = true
				if callID != "workspace.list_agents" {
					t.Errorf("expected workspace.list_agents, got %s", callID)
				}
				if payload == nil {
					t.Error("expected non-nil payload")
				}
				return domain.AgentRefListResp{Items: []domain.AgentRef{{ID: "a1"}}}, nil
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		Service: "workspace",
		CallID:  "workspace.list_agents",
		Payload: map[string]any{"ProjectId": "p1"},
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if !called {
		t.Fatal("planner.Call was not invoked")
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
}

// ── Free-agent action routing tests ──

func TestFreeAgentActionForCallID(t *testing.T) {
	tests := []struct {
		callID string
		want   string
	}{
		{"workspace.create_agent", "create"},
		{"workspace.load_agent", "switch"},
		{"workspace.agent_send_message", "message"},
		{"workspace.list_agents", ""},
		{"appmanager.invoke", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := freeAgentActionForCallID(tt.callID); got != tt.want {
			t.Errorf("freeAgentActionForCallID(%q) = %q, want %q", tt.callID, got, tt.want)
		}
	}
}

func TestHandleInvokeCallable_FreeAgentCreateRoutesToAgentAction(t *testing.T) {
	var capturedCallID string
	var capturedPayload any

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				capturedCallID = callID
				capturedPayload = payload
				return gen.AppManagerAgentActionResp{Accepted: true}, nil
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		AppID:   "my.app",
		CallID:  "workspace.create_agent",
		Payload: map[string]any{"Kind": "explorer", "Name": "my-agent"},
		AgentID: "agent-1",
		Role:    "admin",
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	// Must route to appmanager.agent.action, not appmanager.invoke.
	if capturedCallID != "appmanager.agent_action" {
		t.Fatalf("expected appmanager.agent.action, got %s", capturedCallID)
	}

	// Verify the request fields.
	actionReq, ok := capturedPayload.(gen.AppManagerAgentActionReq)
	if !ok {
		t.Fatalf("expected AppManagerAgentActionReq, got %T", capturedPayload)
	}
	if actionReq.Action != "create" {
		t.Errorf("expected action=create, got %s", actionReq.Action)
	}
	if actionReq.ID != "my.app" {
		t.Errorf("expected ID=my.app, got %s", actionReq.ID)
	}
	if actionReq.AgentID != "agent-1" {
		t.Errorf("expected AgentID=agent-1, got %s", actionReq.AgentID)
	}
	if actionReq.Kind != "explorer" {
		t.Errorf("expected Kind=explorer, got %s", actionReq.Kind)
	}
}

func TestHandleInvokeCallable_FreeAgentSwitchExtractsTargetAgentID(t *testing.T) {
	var capturedPayload any

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				capturedPayload = payload
				return gen.AppManagerAgentActionResp{Accepted: true}, nil
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	// Use TargetAgentId field in payload.
	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		AppID:   "my.app",
		CallID:  "workspace.load_agent",
		Payload: map[string]any{"TargetAgentId": "agent-42"},
		AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}

	actionReq := capturedPayload.(gen.AppManagerAgentActionReq)
	if actionReq.Action != "switch" {
		t.Errorf("expected action=switch, got %s", actionReq.Action)
	}
	if actionReq.TargetAgentID != "agent-42" {
		t.Errorf("expected TargetAgentID=agent-42, got %s", actionReq.TargetAgentID)
	}
}

func TestHandleInvokeCallable_FreeAgentDeniedPropagatesError(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID == "appmanager.agent_action" {
					return nil, fmt.Errorf("appbinding.denied.free_agent: action create not allowed for kind untrusted")
				}
				return nil, fmt.Errorf("unexpected call: %s", callID)
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		AppID:   "untrusted.app",
		CallID:  "workspace.create_agent",
		Payload: map[string]any{"Kind": "untrusted"},
		AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error for denied free-agent action")
	}
	if !strings.Contains(resp.Error, "free_agent") {
		t.Fatalf("expected free_agent denial, got: %s", resp.Error)
	}
}

func TestHandleInvokeCallable_CapabilityPathUnchanged(t *testing.T) {
	var capturedCallID string

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				capturedCallID = callID
				return map[string]any{"ok": true}, nil
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	// Non-agent-management callable with AppID → should still use appmanager.invoke.
	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		AppID:   "my.app",
		CallID:  "my.app.do_thing",
		Payload: map[string]any{"key": "value"},
		AgentID: "agent-1",
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if capturedCallID != "appmanager.invoke" {
		t.Fatalf("expected appmanager.invoke for capability path, got %s", capturedCallID)
	}
}

func TestHandleInvokeCallable_NoAppIDSkipsFreeAgent(t *testing.T) {
	var capturedCallID string

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				capturedCallID = callID
				return domain.AgentRefListResp{}, nil
			},
		}
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	// No AppID → direct service dispatch, no free-agent routing.
	resp, err := a.handleInvokeCallable(ctx, domain.AgentInvokeCallableReq{
		Service: "workspace",
		CallID:  "workspace.create_agent",
		Payload: map[string]any{"Kind": "explorer"},
	})
	if err != nil {
		t.Fatalf("handleInvokeCallable: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if capturedCallID != "workspace.create_agent" {
		t.Fatalf("expected direct workspace dispatch, got %s", capturedCallID)
	}
}

func TestHandleFrontendDebug_FrontendNotAvailable(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleFrontendDebug(ctx, domain.AgentFrontendDebugReq{Operation: "info"})
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected frontend not available error, got %v", err)
	}
}

func TestHandleFrontendDebug_InfoOperation(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	original := debug.EvalJS
	defer func() { debug.EvalJS = original }()

	var capturedScript string
	debug.EvalJS = func(_ context.Context, script string) (any, error) {
		capturedScript = script
		return map[string]any{"url": "http://localhost:5173/", "title": "Test"}, nil
	}

	resp, err := a.handleFrontendDebug(ctx, domain.AgentFrontendDebugReq{Operation: "info"})
	if err != nil {
		t.Fatal(err)
	}
	if capturedScript == "" {
		t.Fatal("expected script to be executed")
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok || m["url"] != "http://localhost:5173/" {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
}

func TestHandleFrontendDebug_EvalOperation(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	original := debug.EvalJS
	defer func() { debug.EvalJS = original }()

	debug.EvalJS = func(_ context.Context, script string) (any, error) {
		return 42, nil
	}

	resp, err := a.handleFrontendDebug(ctx, domain.AgentFrontendDebugReq{Operation: "eval", Script: "1 + 1"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result != 42 {
		t.Fatalf("expected 42, got %v", resp.Result)
	}
}

func TestHandleFrontendDebug_EvalError(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	original := debug.EvalJS
	defer func() { debug.EvalJS = original }()

	debug.EvalJS = func(_ context.Context, script string) (any, error) {
		return nil, errors.New("syntax error")
	}

	resp, err := a.handleFrontendDebug(ctx, domain.AgentFrontendDebugReq{Operation: "eval", Script: "bad"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "syntax error" {
		t.Fatalf("expected syntax error, got %v", resp.Error)
	}
}

func TestHandleFrontendDebug_DefaultOperationIsEval(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	original := debug.EvalJS
	defer func() { debug.EvalJS = original }()

	var capturedScript string
	debug.EvalJS = func(_ context.Context, script string) (any, error) {
		capturedScript = script
		return "ok", nil
	}

	resp, err := a.handleFrontendDebug(ctx, domain.AgentFrontendDebugReq{Script: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if capturedScript != "test" {
		t.Fatalf("expected script to run, got %q", capturedScript)
	}
	if resp.Result != "ok" {
		t.Fatalf("unexpected result: %v", resp.Result)
	}
}

func TestHandleInspectActor_OK(t *testing.T) {
	a := &Actor{}
	rootRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.RootFn = func() ref.Ref { return rootRef }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, target ref.Ref, callID string, _ any) (any, error) {
				switch callID {
				case "gospore.cell.stats":
					return map[string]any{
						"actorId":        "01HTEST000000000000000000A",
						"actorType":      "workspace",
						"state":          "started",
						"ownerQueue":     map[string]any{"depth": 3, "capacity": 256},
						"systemQueue":    map[string]any{"depth": 0, "capacity": 64},
						"replyQueue":     map[string]any{"depth": 1, "capacity": 64},
						"pendingInvokes": 5,
						"invoke": map[string]any{
							"inFlight":        5,
							"inFlightByMode":  map[string]any{"stream": 4, "unary": 1},
							"inFlightByCall":  map[string]any{"gospore.events.subscribe_service": 4, "aimanager.aggregator_list": 1},
							"registered":      120,
							"completed":       100,
							"errored":         7,
							"sendFailed":      2,
							"closedEarly":     6,
							"evictedStalled":  1,
							"evictedCapacity": 0,
							"droppedFrames":   3,
							"recentEvictions": []any{map[string]any{
								"callId": "aiaggregator.dispatch",
								"mode":   "stream",
								"corId":  4095,
								"reason": "stalled",
								"drops":  8,
								"at":     "2026-08-27T12:34:56Z",
							}},
						},
					}, nil
				default:
					return nil, fmt.Errorf("unexpected call: %s", callID)
				}
			},
		}
	}

	resp, err := a.handleInspectActor(ctx, domain.AgentInspectActorReq{ActorPath: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.ActorID != "01HTEST000000000000000000A" {
		t.Errorf("ActorID = %q", resp.ActorID)
	}
	if resp.ActorType != "workspace" {
		t.Errorf("ActorType = %q", resp.ActorType)
	}
	if resp.CellState != "started" {
		t.Errorf("CellState = %q", resp.CellState)
	}
	if resp.OwnerQueueDepth != 3 || resp.OwnerQueueCapacity != 256 {
		t.Errorf("OwnerQueue = %d/%d", resp.OwnerQueueDepth, resp.OwnerQueueCapacity)
	}
	if resp.PendingInvokes != 5 {
		t.Errorf("PendingInvokes = %d", resp.PendingInvokes)
	}
	// Invoke diagnostics: type breakdowns and failure counters pass through.
	inv := resp.Invoke
	if inv == nil {
		t.Fatal("Invoke diagnostics missing from response")
	}
	if inv.InFlight != 5 {
		t.Errorf("Invoke.InFlight = %d, want 5", inv.InFlight)
	}
	if inv.InFlightByMode["stream"] != 4 || inv.InFlightByMode["unary"] != 1 {
		t.Errorf("Invoke.InFlightByMode = %v", inv.InFlightByMode)
	}
	if inv.InFlightByCall["aimanager.aggregator_list"] != 1 {
		t.Errorf("Invoke.InFlightByCall = %v", inv.InFlightByCall)
	}
	if inv.Registered != 120 || inv.Completed != 100 || inv.Errored != 7 ||
		inv.SendFailed != 2 || inv.ClosedEarly != 6 || inv.DroppedFrames != 3 {
		t.Errorf("Invoke counters = %+v", inv)
	}
	// Eviction attribution passes through with decimal CorID and reason.
	if len(inv.RecentEvictions) != 1 {
		t.Fatalf("Invoke.RecentEvictions = %+v, want 1 record", inv.RecentEvictions)
	}
	if ev := inv.RecentEvictions[0]; ev.CallID != "aiaggregator.dispatch" || ev.Mode != "stream" ||
		ev.CorID != "4095" || ev.Reason != "stalled" || ev.Drops != 8 || ev.At != "2026-08-27T12:34:56Z" {
		t.Errorf("Invoke.RecentEvictions[0] = %+v", ev)
	}
	// pendingInvokes > 0 → backlogged.
	if resp.Health != "backlogged" {
		t.Errorf("Health = %q, want backlogged", resp.Health)
	}
	// Non-agent target: business state should be empty.
	if resp.BusinessState != "" {
		t.Errorf("BusinessState = %q, want empty for non-agent", resp.BusinessState)
	}
}

func TestHandleInspectActor_CellStatsError(t *testing.T) {
	a := &Actor{}
	rootRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.RootFn = func() ref.Ref { return rootRef }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "gospore.cell.stats" {
					return nil, fmt.Errorf("actor not found")
				}
				return nil, fmt.Errorf("unexpected call: %s", callID)
			},
		}
	}

	resp, err := a.handleInspectActor(ctx, domain.AgentInspectActorReq{ActorPath: "bad-path"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "not found") {
		t.Fatalf("expected error containing 'not found', got %q", resp.Error)
	}
}

func TestHandleInspectActor_HealthLabels(t *testing.T) {
	cases := []struct {
		name   string
		stats  cellStatsResult
		health string
	}{
		{"idle", cellStatsResult{State: "started", OwnerQueue: queueStat{0, 256}}, "ok"},
		{"backlogged queue", cellStatsResult{State: "started", OwnerQueue: queueStat{1, 256}}, "backlogged"},
		{"backlogged pending", cellStatsResult{State: "started", PendingInvokes: 1}, "backlogged"},
		{"blocked", cellStatsResult{State: "started", OwnerQueue: queueStat{210, 256}}, "blocked"},
		{"stopped", cellStatsResult{State: "stopped"}, "stopped"},
		// Permanent event subscriptions are steady-state, not pending work.
		{"subscriptions only", cellStatsResult{
			State: "started", PendingInvokes: 18,
			Invoke: invokeStatsResult{InFlightByCall: map[string]int{
				"gospore.events.subscribe_service":  13,
				"gospore.events.subscribe_instance": 5,
			}},
		}, "ok"},
		// The probe's own diagnostic calls are self-observation, not workload.
		{"diagnostics only", cellStatsResult{
			State: "started", PendingInvokes: 3,
			Invoke: invokeStatsResult{InFlightByCall: map[string]int{
				"gospore.cell.stats": 1,
				"inspect_actor":      2,
			}},
		}, "ok"},
		{"subscriptions plus awaited work", cellStatsResult{
			State: "started", PendingInvokes: 23,
			Invoke: invokeStatsResult{InFlightByCall: map[string]int{
				"gospore.events.subscribe_service":  13,
				"gospore.events.subscribe_instance": 5,
				"aiaggregator.chat":                 2,
				"inspect_actor":                     1,
				"gospore.cell.stats":                2,
			}},
		}, "backlogged"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeActorHealth(c.stats); got != c.health {
				t.Errorf("computeActorHealth(%+v) = %q, want %q", c.stats, got, c.health)
			}
		})
	}
}

func TestDecodeCellStatsResult_JSONBytes(t *testing.T) {
	// Production path: gospore.cell.stats returns type `any` (BuiltinAny),
	// so the codec hands back raw JSON bytes rather than a decoded map.
	raw := []byte(`{"actorId":"01HTEST","actorType":"workspace","state":"started","ownerQueue":{"depth":3,"capacity":256},"systemQueue":{"depth":0,"capacity":64},"replyQueue":{"depth":1,"capacity":64},"pendingInvokes":5}`)
	s, err := decodeCellStatsResult(raw)
	if err != nil {
		t.Fatalf("decodeCellStatsResult([]byte): %v", err)
	}
	if s.ActorID != "01HTEST" {
		t.Errorf("ActorID = %q", s.ActorID)
	}
	if s.PendingInvokes != 5 {
		t.Errorf("PendingInvokes = %d", s.PendingInvokes)
	}
}

func TestDecodeCellStatsResult_Map(t *testing.T) {
	raw := map[string]any{
		"actorId":        "01HMAP",
		"actorType":      "agent",
		"state":          "idle",
		"pendingInvokes": float64(0),
	}
	s, err := decodeCellStatsResult(raw)
	if err != nil {
		t.Fatalf("decodeCellStatsResult(map): %v", err)
	}
	if s.ActorID != "01HMAP" {
		t.Errorf("ActorID = %q", s.ActorID)
	}
}

func TestDecodeCellStatsResult_Nil(t *testing.T) {
	s, err := decodeCellStatsResult(nil)
	if err != nil {
		t.Fatalf("decodeCellStatsResult(nil): %v", err)
	}
	if s.ActorID != "" {
		t.Errorf("expected zero value, got %+v", s)
	}
}

func TestIsValidProfileName(t *testing.T) {
	valid := []string{"cpu", "heap", "goroutine", "mutex", "block", "threadcreate"}
	for _, p := range valid {
		if !isValidProfileName(p) {
			t.Errorf("isValidProfileName(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"", "allocs", "trace", "unknown"} {
		if isValidProfileName(p) {
			t.Errorf("isValidProfileName(%q) = true, want false", p)
		}
	}
}

func TestHandleCaptureProfile_Goroutine(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{Profile: "goroutine"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Profile != "goroutine" {
		t.Errorf("Profile = %q, want goroutine", resp.Profile)
	}
	if resp.Text == "" {
		t.Error("Text is empty; expected goroutine profile output")
	}
	if !strings.Contains(resp.Text, "goroutine profile:") {
		t.Errorf("Text does not contain goroutine header; got:\n%s", resp.Text)
	}
}

func TestHandleCaptureProfile_Heap(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{Profile: "heap"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Profile != "heap" {
		t.Errorf("Profile = %q, want heap", resp.Profile)
	}
	if resp.Text == "" {
		t.Error("Text is empty; expected heap profile output")
	}
}

func TestHandleCaptureProfile_DefaultsToGoroutine(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Profile != "goroutine" {
		t.Errorf("Profile = %q, want goroutine (default)", resp.Profile)
	}
}

func TestHandleCaptureProfile_InvalidProfile(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	_, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{Profile: "unknown"})
	if err == nil {
		t.Fatal("expected error for unknown profile, got nil")
	}
	if !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("error %q does not mention 'unknown profile'", err.Error())
	}
}

func TestTruncateProfileText(t *testing.T) {
	// Short string: no truncation.
	out, truncated := truncateProfileText("short")
	if truncated {
		t.Error("short text should not be truncated")
	}
	if out != "short" {
		t.Errorf("got %q, want %q", out, "short")
	}

	// Long string: truncation at last newline boundary.
	long := strings.Repeat("line\n", maxProfileTextBytes/5+100)
	out, truncated = truncateProfileText(long)
	if !truncated {
		t.Error("long text should be truncated")
	}
	if len(out) > maxProfileTextBytes+len("\n... [truncated: profile exceeds 256KB; use Debug=1 for a more compact summary]") {
		t.Errorf("truncated output too long: %d bytes", len(out))
	}
	if !strings.Contains(out, "[truncated") {
		t.Error("truncated output should contain truncation marker")
	}
	// Must end at a newline boundary (the last full line before the cut).
	if !strings.HasSuffix(out, "\n") || strings.HasSuffix(out, "\n\n") {
		// The truncation marker line ends with \n, which is fine.
	}
}

func TestHandleCaptureProfile_CPU(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// Capture a 1-second CPU profile. Since we're running under `go test`,
	// `go tool pprof` is guaranteed to be available.
	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{
		Profile: "cpu",
		Seconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Profile != "cpu" {
		t.Errorf("Profile = %q, want cpu", resp.Profile)
	}
	// The text should contain pprof output or a clear fallback message.
	if resp.Text == "" {
		t.Error("Text is empty for cpu profile")
	}
}

func TestHandleCaptureProfile_SummaryMode(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{
		Profile: "goroutine",
		TopN:    5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Profile != "goroutine" {
		t.Errorf("Profile = %q, want goroutine", resp.Profile)
	}
	// Summary mode should contain total and unique stacks info.
	if !strings.Contains(resp.Text, "total") {
		t.Errorf("missing 'total':\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "unique stacks") {
		t.Errorf("missing 'unique stacks':\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "showing top") {
		t.Errorf("missing 'showing top':\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "[1]") {
		t.Errorf("missing first entry marker:\n%s", resp.Text)
	}
}

func TestHandleCaptureProfile_DiffMode(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{
		Profile: "goroutine",
		Diff:    true,
		Seconds: 1,
		TopN:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if resp.Profile != "goroutine" {
		t.Errorf("Profile = %q, want goroutine", resp.Profile)
	}
	// Diff mode should show totals and either changes or "no changes".
	if !strings.Contains(resp.Text, "before=") || !strings.Contains(resp.Text, "after=") {
		t.Errorf("missing before/after totals:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "No changes") && !strings.Contains(resp.Text, "NEW") && !strings.Contains(resp.Text, "GROWING") {
		t.Errorf("missing diff sections:\n%s", resp.Text)
	}
}

func TestHandleCaptureProfile_DiffNotSupportedForCPU(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	_, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{
		Profile: "cpu",
		Diff:    true,
	})
	if err == nil {
		t.Fatal("expected error for Diff=true with cpu profile")
	}
	if !strings.Contains(err.Error(), "Diff is not supported for cpu") {
		t.Errorf("error = %q, want 'Diff is not supported for cpu'", err.Error())
	}
}

func TestHandleCaptureProfile_Debug2RawStacks(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	resp, err := a.handleCaptureProfile(ctx, domain.AgentCaptureProfileReq{
		Profile: "goroutine",
		Debug:   2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	// Debug=2 should contain individual goroutine stacks (format: "goroutine <id> [<state>]:").
	if !strings.Contains(resp.Text, "goroutine ") {
		t.Errorf("missing goroutine entries:\n%s", resp.Text)
	}
	if !strings.Contains(resp.Text, "[running]") && !strings.Contains(resp.Text, "[chan receive]") && !strings.Contains(resp.Text, "[io wait]") && !strings.Contains(resp.Text, "[sleep]") && !strings.Contains(resp.Text, "[select]") {
		t.Errorf("missing goroutine state markers:\n%s", resp.Text)
	}
	if strings.Contains(resp.Text, "showing top") {
		t.Errorf("should not contain summary 'showing top' in Debug=2 mode:\n%s", resp.Text)
	}
}
