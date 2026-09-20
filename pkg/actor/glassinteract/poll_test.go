package glassinteract

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// blockingAgentListPlanner returns a fake planner whose workspace.agent_list_state
// call blocks until release is closed, simulating a slow/busy workspace owner
// loop or a stalled RPC.
func blockingAgentListPlanner(release chan struct{}) fakePlanner {
	return fakePlanner{callFunc: func(ctx context.Context, _ ref.Ref, callID string, _ any) (any, error) {
		if callID != "workspace.agent_list_state" {
			return nil, fmt.Errorf("unexpected call %s", callID)
		}
		select {
		case <-release:
			return gen.WorkspaceAgentListState{
				Full:  true,
				Items: []gen.AgentListItem{{ActorID: "ag-1", DisplayName: "Agent One", AgentKind: "worker"}},
			}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
}

// TestAgentPollTickReturnsWhileWorkspacePending proves the owner-lane handler
// returns immediately even though the workspace fetch is still in flight: the
// Await moved onto a background goroutine and the result is delivered back as
// a fire-and-forget self-invoke to glass_interact.agent_poll_apply.
func TestAgentPollTickReturnsWhileWorkspacePending(t *testing.T) {
	a, ctx := freshTestActor(t)

	release := make(chan struct{})
	var applyMu sync.Mutex
	appliedCallID := ""
	var appliedState gen.WorkspaceAgentListState

	ctx.SelfRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		applyMu.Lock()
		defer applyMu.Unlock()
		appliedCallID = callID
		if s, ok := payload.(gen.WorkspaceAgentListState); ok {
			appliedState = s
		}
		return nil
	})
	ctx.PlannerFn = func() actor.Planner { return blockingAgentListPlanner(release) }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "workspace"
	}
	// Record the re-arm so we can assert the tick chain survives the fetch.
	var rearmMu sync.Mutex
	rearmed := false
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == callableAgentPollTick {
			rearmMu.Lock()
			rearmed = true
			rearmMu.Unlock()
		}
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- a.handleAgentPollTick(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("agent_poll_tick: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent_poll_tick blocked the owner lane on the workspace fetch")
	}

	rearmMu.Lock()
	rearmedVal := rearmed
	rearmMu.Unlock()
	if !rearmedVal {
		t.Fatal("agent_poll_tick did not self-reschedule")
	}

	// Release the workspace fetch; the background goroutine must self-invoke
	// the apply callable with the fetched state.
	close(release)
	deadline := time.After(2 * time.Second)
	for {
		applyMu.Lock()
		callOK := appliedCallID == callableAgentPollApply
		stateOK := appliedState.Full && len(appliedState.Items) == 1 && appliedState.Items[0].ActorID == "ag-1"
		applyMu.Unlock()
		if callOK && stateOK {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("agent_poll_apply was not dispatched with the fetched state; callID=%q state=%+v", appliedCallID, appliedState)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestAgentPollApplyUpdatesMonitorAndRendersIdleFrame verifies the owner-lane
// continuation applies the fetched state to the agent monitor and re-renders
// the idle frame.
func TestAgentPollApplyUpdatesMonitorAndRendersIdleFrame(t *testing.T) {
	a, ctx := freshTestActor(t)
	// Enter idle mode so the apply re-renders the idle frame.
	a.setDisplayFrame(gen.GlassRenderFrame{}, frameModeIdle)

	state := gen.WorkspaceAgentListState{
		Full: true,
		Items: []gen.AgentListItem{{
			ActorID:     "ag-1",
			DisplayName: "Agent One",
			AgentKind:   "worker",
			Runtime:     &gen.AgentRuntimeState{State: "running"},
		}},
	}
	if err := a.handleAgentPollApply(ctx, state); err != nil {
		t.Fatalf("agent_poll_apply: %v", err)
	}

	if got := a.agentMon.activeCount(); got != 1 {
		t.Fatalf("expected 1 active agent after apply, got %d", got)
	}
	if a.getDisplayMode() != frameModeIdle {
		t.Fatalf("expected idle frame mode, got %q", a.getDisplayMode())
	}
	foundRender := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == eventRender {
			foundRender = true
			break
		}
	}
	if !foundRender {
		t.Fatal("expected a glass.render event after applying the poll result")
	}
}
