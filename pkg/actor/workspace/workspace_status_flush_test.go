package workspace

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// Status updates are the highest-frequency ownerLoop entry. Their persist
// writes must be coalesced into a single delayed flush instead of one full
// Save per push.
func TestHandleAgentStatusUpdateCoalescesPersist(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder"}}

	var flushScheduled atomic.Int32
	ctx.AfterFn = func(_ time.Duration, callID string, _ any) error {
		if callID == "workspace.internal_flush_state" {
			flushScheduled.Add(1)
		}
		return nil
	}

	for i := 0; i < 3; i++ {
		if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
			AgentActorID: actorID,
			State:        "running",
			LastActivity: "tick",
		}); err != nil {
			t.Fatalf("status update %d: %v", i, err)
		}
	}
	if got := flushScheduled.Load(); got != 1 {
		t.Fatalf("flush scheduled %d times after 3 updates, want 1 (coalesced)", got)
	}
	if !a.stateFlushPending.Load() {
		t.Fatal("stateFlushPending = false, want true while flush is pending")
	}

	// The flush performs the actual persist and clears the pending flag.
	if _, err := a.handleStateFlush(ctx, stateFlushReq{}); err != nil {
		t.Fatalf("handleStateFlush: %v", err)
	}
	if a.stateFlushPending.Load() {
		t.Fatal("stateFlushPending still true after flush")
	}
	var savedReg []domain.AgentRef
	if err := a.store.Load(a.agentRegistryName(), &savedReg); err != nil {
		t.Fatalf("load agent registry card: %v", err)
	}
	if len(savedReg) != 1 || savedReg[0].ActorID != actorID {
		t.Fatalf("agent registry card: got %+v, want 1 agent with ActorID=%s", savedReg, actorID)
	}

	// A new update after the flush schedules a fresh flush.
	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: actorID,
		State:        "idle",
	}); err != nil {
		t.Fatalf("status update after flush: %v", err)
	}
	if got := flushScheduled.Load(); got != 2 {
		t.Fatalf("flush scheduled %d times total, want 2 after post-flush update", got)
	}
}

func TestHandleAgentStatusUpdateSkipsUnchangedListBroadcast(t *testing.T) {
	a, ctx := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder"}}

	req := gen.WorkspaceAgentStatusUpdateReq{AgentActorID: actorID, State: "running", LastActivity: "tick"}
	if _, err := a.handleAgentStatusUpdate(ctx, req); err != nil {
		t.Fatalf("first status update: %v", err)
	}
	if _, err := a.handleAgentStatusUpdate(ctx, req); err != nil {
		t.Fatalf("duplicate status update: %v", err)
	}
	agentListEvents := func() int {
		count := 0
		for _, event := range ctx.EmittedEvents {
			if event.Kind == "agent_list_state" {
				count++
			}
		}
		return count
	}
	if got := agentListEvents(); got != 1 {
		t.Fatalf("agent_list_state events after duplicate update = %d, want 1", got)
	}

	req.LastActivity = "next-tick"
	if _, err := a.handleAgentStatusUpdate(ctx, req); err != nil {
		t.Fatalf("changed status update: %v", err)
	}
	if got := agentListEvents(); got != 2 {
		t.Fatalf("agent_list_state events after visible change = %d, want 2", got)
	}
}

func TestBuildAgentListStateReadsRuntimeUnderLock(t *testing.T) {
	a, _ := freshActor(t)
	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder"}}
	a.agentRuntime = map[string]gen.AgentRuntimeState{actorID: {State: "running"}}

	const iterations = 1_000
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			a.agentsMu.Lock()
			a.agentRuntime[actorID] = gen.AgentRuntimeState{State: "running"}
			a.agentsMu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < iterations; i++ {
			state := a.buildAgentListState(false)
			if len(state.Items) != 1 {
				t.Errorf("items = %d, want 1", len(state.Items))
				return
			}
			if state.Items[0].Runtime != nil && state.Items[0].Runtime.State != "running" {
				t.Errorf("runtime state = %q, want running", state.Items[0].Runtime.State)
				return
			}
		}
	}()
	close(start)
	wg.Wait()
}

// handleAgentListState is PureContext (concurrent goroutine) and must serve
// the last snapshot built on the ownerLoop, never live mutable fields.
func TestHandleAgentListStateServesSnapshot(t *testing.T) {
	a, ctx := freshActor(t)

	// Before any build, the handler returns an empty full state.
	state, err := a.handleAgentListState(ctx)
	if err != nil {
		t.Fatalf("handleAgentListState before build: %v", err)
	}
	if len(state.Items) != 0 {
		t.Fatalf("items before build = %d, want 0", len(state.Items))
	}

	actorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{{ID: "W#1", ActorID: actorID, AgentKind: "coder", DisplayName: "alpha"}}
	if _, err := a.handleAgentStatusUpdate(ctx, gen.WorkspaceAgentStatusUpdateReq{
		AgentActorID: actorID,
		State:        "running",
	}); err != nil {
		t.Fatalf("status update: %v", err)
	}

	state, err = a.handleAgentListState(ctx)
	if err != nil {
		t.Fatalf("handleAgentListState: %v", err)
	}
	if len(state.Items) != 1 || state.Items[0].ActorID != actorID {
		t.Fatalf("snapshot items = %+v, want one item with actorID %q", state.Items, actorID)
	}
	if state.Version == 0 {
		t.Fatal("snapshot version = 0, want a built snapshot after status update")
	}
	if state.Items[0].Runtime == nil || state.Items[0].Runtime.State != "running" {
		t.Fatalf("snapshot runtime = %+v, want state running", state.Items[0].Runtime)
	}
}
