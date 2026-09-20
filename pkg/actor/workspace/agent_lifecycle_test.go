package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestLifecycleEventForAgent(t *testing.T) {
	agent := domain.AgentRef{ActorID: "agent-1", AgentKind: "coder", Title: "Fix login"}
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		previous gen.AgentRuntimeState
		current  gen.AgentRuntimeState
		kind     string
		priority string
	}{
		{"started", gen.AgentRuntimeState{}, gen.AgentRuntimeState{State: "running"}, "agent.started", "low"},
		{"completed", gen.AgentRuntimeState{State: "running"}, gen.AgentRuntimeState{State: "idle"}, "agent.completed", "normal"},
		{"failed", gen.AgentRuntimeState{State: "running"}, gen.AgentRuntimeState{State: "failed"}, "agent.failed", "high"},
		{"blocked", gen.AgentRuntimeState{State: "running"}, gen.AgentRuntimeState{State: "paused"}, "agent.blocked", "normal"},
		{"approval blocked", gen.AgentRuntimeState{State: "running"}, gen.AgentRuntimeState{State: "running", ApprovalPending: true}, "agent.blocked", "normal"},
		{"unchanged", gen.AgentRuntimeState{State: "running"}, gen.AgentRuntimeState{State: "running"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := lifecycleEventForAgent(tc.previous, tc.current, agent, now)
			if ok != (tc.kind != "") {
				t.Fatalf("ok = %v, want %v", ok, tc.kind != "")
			}
			if !ok {
				return
			}
			if event.Kind != tc.kind || event.Priority != tc.priority {
				t.Fatalf("event = %+v, want kind=%q priority=%q", event, tc.kind, tc.priority)
			}
			if event.DedupeKey != "agent-1:"+tc.kind {
				t.Errorf("dedupe = %q", event.DedupeKey)
			}
		})
	}
}

func TestHandleAgentStatusUpdateEnqueuesLifecycleEvent(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ActorID: actorID, AgentKind: "coder", Title: "Fix login"}}
	planner := &fakeClonePlanner{}
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "glass_interact" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), nil), true
	}

	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{AgentActorID: actorID, State: "running"}); err != nil {
		t.Fatal(err)
	}
	waitForCloneCalls(t, planner, func(calls []forkPlannerCall) bool { return len(calls) == 1 })
	planner.mu.Lock()
	call := planner.calls[0]
	planner.mu.Unlock()
	if call.callID != "glass_interact.event_enqueue" {
		t.Fatalf("call id = %q", call.callID)
	}
	req, ok := call.payload.(gen.GlassEventEnqueueReq)
	if !ok {
		t.Fatalf("payload type = %T", call.payload)
	}
	if req.Source != "agent" || req.Kind != "agent.started" || req.Priority != "low" || req.DedupeKey != actorID+":agent.started" {
		t.Fatalf("enqueue req = %+v", req)
	}
}

func TestHandleAgentStatusUpdateWithoutGlassDoesNotBlock(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ActorID: actorID, AgentKind: "coder", Title: "Fix login"}}
	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{AgentActorID: actorID, State: "running"}); err != nil {
		t.Fatalf("status update without glass: %v", err)
	}
}

func TestLifecycleEventPayloadIsSafe(t *testing.T) {
	agent := domain.AgentRef{ActorID: "agent-1", AgentKind: "coder", DisplayName: "Coder"}
	rawError := " token=secret\n" + strings.Repeat("x", 260)
	event, ok := lifecycleEventForAgent(
		gen.AgentRuntimeState{State: "running"},
		gen.AgentRuntimeState{State: "failed", CurrentTaskSummary: " test ", Error: rawError},
		agent,
		time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	)
	if !ok {
		t.Fatal("expected failed lifecycle event")
	}
	var payload agentLifecyclePayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Title != "Coder" || payload.TaskSummary != "test" {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Error != "Agent reported an error; inspect diagnostics for details." || strings.Contains(payload.Error, "secret") {
		t.Errorf("error summary = %q", payload.Error)
	}
}
