package agent

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleTurnResume_RecoveryPausedWaitingToWaiting verifies that when a
// workflow-owner agent restarts with a waiting turn, OnStart recovery converts
// it to paused/recovery. A subsequent turn_resume restores the turn to
// waiting and cascades turn_resume to all direct child agents discovered via
// workspace persistent topology (not the in-memory activeChildren map).
func TestHandleTurnResume_RecoveryPausedWaitingToWaiting(t *testing.T) {
	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	var mu sync.Mutex
	var resumedChildIDs []string

	childRef := testutil.NewFakeRef(childAID, func(callID string, payload any) any {
		if callID == "turn_resume" {
			mu.Lock()
			resumedChildIDs = append(resumedChildIDs, childAID.String())
			mu.Unlock()
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "workspace.list_agents" {
			var req domain.WorkspaceListAgentsReq
			switch v := payload.(type) {
			case domain.WorkspaceListAgentsReq:
				req = v
			case *domain.WorkspaceListAgentsReq:
				req = *v
			case []byte:
				_ = json.Unmarshal(v, &req)
			}
			if req.ParentAgentID != selfID.String() {
				t.Errorf("workspace.list_agents ParentAgentID = %q, want %q", req.ParentAgentID, selfID.String())
			}
			if req.ProjectID != projectID.String() {
				t.Errorf("workspace.list_agents ProjectID = %q, want %q", req.ProjectID, projectID.String())
			}
			return gen.AgentRefListResp{Items: []gen.AgentRef{{ActorID: childAID.String()}}}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true}, // skip file-system saveMailbox side effects in tests
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2, CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonRecovery
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	if err := a.handleTurnResume(ctx); err != nil {
		t.Fatalf("handleTurnResume returned error: %v", err)
	}

	if a.status.State != domain.TurnStateWaiting {
		t.Fatalf("status.State = %q, want %q", a.status.State, domain.TurnStateWaiting)
	}
	if a.status.PauseKind != "" {
		t.Fatalf("status.PauseKind = %q, want empty", a.status.PauseKind)
	}
	if a.status.TurnID != "t2" {
		t.Fatalf("status.TurnID = %q, want t2", a.status.TurnID)
	}
	if a.ActiveTurnRef != "t2" {
		t.Fatalf("ActiveTurnRef = %q, want t2", a.ActiveTurnRef)
	}
	turn := a.Session.Turns[1]
	if turn.State != domain.TurnStateWaiting {
		t.Fatalf("turn state = %q, want %q", turn.State, domain.TurnStateWaiting)
	}
	if turn.PauseReason != "" {
		t.Fatalf("turn PauseReason = %q, want empty", turn.PauseReason)
	}

	mu.Lock()
	for i := 0; i < 200 && len(resumedChildIDs) == 0; i++ {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	if len(resumedChildIDs) != 1 || resumedChildIDs[0] != childAID.String() {
		t.Fatalf("resumed children = %v, want [%s]", resumedChildIDs, childAID.String())
	}
	mu.Unlock()
}

// TestHandleTurnResume_RecoveryPausedRunningKeepsExistingPath verifies that a
// recovery-paused turn WITHOUT a CompletedAt (a running orphan) does NOT take the
// waiting-resume path; it falls through to the existing crash-recovery
// resume-to-running path. Children are still lazy-loaded and resumed by the
// unconditional workflow-owner cascade (dispatched for any resume).
func TestHandleTurnResume_RecoveryPausedRunningKeepsExistingPath(t *testing.T) {
	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	var mu sync.Mutex
	resumed := false
	childRef := testutil.NewFakeRef(childAID, func(callID string, payload any) any {
		if callID == "turn_resume" {
			mu.Lock()
			resumed = true
			mu.Unlock()
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "workspace.list_agents" {
			return gen.AgentRefListResp{Items: []gen.AgentRef{{ActorID: childAID.String()}}}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2, PauseReason: "recovery"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonRecovery
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	// The turn lacks CompletedAt, so the waiting path should NOT match.
	// handleTurnResume falls through to the existing crash-recovery path (the
	// fake context has no After scheduler, so it errors after the cascade
	// dispatch). The owner must NOT have been restored to waiting.
	_ = a.handleTurnResume(ctx)

	if a.status.State == domain.TurnStateWaiting {
		t.Fatal("owner must not be restored to waiting for a running-orphan turn")
	}

	// The unconditional workflow-owner cascade still resumes children.
	mu.Lock()
	for i := 0; i < 200 && !resumed; i++ {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	if !resumed {
		t.Fatal("child agent should be resumed by the workflow-owner cascade")
	}
	mu.Unlock()
}

// TestHandleTurnResume_NonWorkflowRecoveryPausedWaitingDoesNotResumeChildren
// verifies that the waiting-resume path requires workflowActive; otherwise the
// new path is skipped.
func TestHandleTurnResume_NonWorkflowRecoveryPausedWaitingDoesNotResumeChildren(t *testing.T) {
	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	resumed := false
	childRef := testutil.NewFakeRef(childAID, func(callID string, payload any) any {
		if callID == "turn_resume" {
			resumed = true
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "workspace.list_agents" {
			return gen.AgentRefListResp{Items: []gen.AgentRef{{ActorID: childAID.String()}}}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2, CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		// No ActiveWorkflow -> workflowActive() false.
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonRecovery
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	_ = a.handleTurnResume(ctx)

	if resumed {
		t.Fatal("child agent should not be resumed when not workflow-active")
	}
}

// TestHandleTurnResume_MidRunPausedWorkflowOwnerAlsoResumesChildren verifies
// that a workflow owner paused from a RUNNING turn (no CompletedAt — the
// crash-recovery path) still lazy-loads and resumes its children. The child
// cascade is dispatched for ANY workflow-active owner resume, not just the
// paused-from-waiting scenario.
func TestHandleTurnResume_MidRunPausedWorkflowOwnerAlsoResumesChildren(t *testing.T) {
	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	var mu sync.Mutex
	var ops []string

	childRef := testutil.NewFakeRef(childAID, func(callID string, payload any) any {
		if callID == "turn_resume" {
			mu.Lock()
			ops = append(ops, "turn_resume")
			mu.Unlock()
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workspace.list_agents":
			return gen.AgentRefListResp{Items: []gen.AgentRef{{
				ID:        "Worker#0001",
				ActorID:   childAID.String(),
				LoadState: "unloaded",
			}}}
		case "workspace.load_agent":
			mu.Lock()
			ops = append(ops, "load_agent")
			mu.Unlock()
			return gen.AgentRef{ID: "Worker#0001", ActorID: childAID.String(), LoadState: "loaded"}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				// Paused mid-run: NO CompletedAt — the crash-recovery path.
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonUser
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	// No engine: falls through to crash-recovery resume in place. ctx.After
	// is unavailable in the fake context; the error is expected AFTER the
	// child cascade has been dispatched.
	_ = a.handleTurnResume(ctx)

	mu.Lock()
	for i := 0; i < 200 && len(ops) < 2; i++ {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	want := []string{"load_agent", "turn_resume"}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v", ops, want)
		}
	}
	mu.Unlock()
}

// TestHandleTurnResume_UserPausedWaitingLazyLoadsChildrenFirst verifies the
// workflow_pause_all resume path: an owner paused from waiting with
// PauseKind "user" is resumed by lazy-loading each unloaded child
// (workspace.load_agent), resuming the children, and only then restoring the
// owner itself to waiting.
func TestHandleTurnResume_UserPausedWaitingLazyLoadsChildrenFirst(t *testing.T) {
	selfID := testutil.GenActorID()
	projectID := testutil.GenActorID()
	childAID := testutil.GenActorID()

	var mu sync.Mutex
	var ops []string // ordered op log: load_agent, turn_resume

	childRef := testutil.NewFakeRef(childAID, func(callID string, payload any) any {
		if callID == "turn_resume" {
			mu.Lock()
			ops = append(ops, "turn_resume")
			mu.Unlock()
		}
		return nil
	})

	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workspace.list_agents":
			return gen.AgentRefListResp{Items: []gen.AgentRef{{
				ID:        "Worker#0001",
				ActorID:   childAID.String(),
				LoadState: "unloaded",
			}}}
		case "workspace.load_agent":
			var req gen.WorkspaceLoadAgentReq
			switch v := payload.(type) {
			case gen.WorkspaceLoadAgentReq:
				req = v
			case []byte:
				_ = json.Unmarshal(v, &req)
			}
			if req.AgentID != "Worker#0001" {
				t.Errorf("workspace.load_agent AgentID = %q, want Worker#0001", req.AgentID)
			}
			mu.Lock()
			ops = append(ops, "load_agent")
			mu.Unlock()
			return gen.AgentRef{ID: "Worker#0001", ActorID: childAID.String(), LoadState: "loaded"}
		}
		return nil
	})

	ctx := testutil.HumanCtx(selfID)
	ctx.ParentRef = testutil.NewFakeRef(projectID, nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == childAID.String() {
			return childRef, true
		}
		return nil, false
	}

	a := &Actor{
		actorID: selfID.String(),
		child:   childState{Mode: true},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "t1", Role: "user", State: "completed"},
				{ID: "t2", Role: "assistant", State: "paused", TurnOrder: 2, CompletedAt: "2025-01-01T00:00:00Z"},
			},
		},
		RawSession: domain.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	a.ActiveTurnRef = "t2"
	a.status.State = domain.TurnStatePaused
	a.status.PauseKind = domain.PauseReasonUser
	a.status.TurnID = "t2"
	a.setActiveTurnOrder(2)

	if err := a.handleTurnResume(ctx); err != nil {
		t.Fatalf("handleTurnResume returned error: %v", err)
	}

	if a.status.State != domain.TurnStateWaiting || a.status.PauseKind != "" {
		t.Fatalf("owner status = %+v, want waiting with empty PauseKind", a.status)
	}
	if turn := a.Session.Turns[1]; turn.State != domain.TurnStateWaiting {
		t.Fatalf("turn state = %q, want %q", turn.State, domain.TurnStateWaiting)
	}

	mu.Lock()
	for i := 0; i < 200 && len(ops) < 2; i++ {
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
	}
	want := []string{"load_agent", "turn_resume"}
	if len(ops) != len(want) {
		t.Fatalf("ops = %v, want %v", ops, want)
	}
	for i := range want {
		if ops[i] != want[i] {
			t.Fatalf("ops = %v, want %v (children before owner restore)", ops, want)
		}
	}
	mu.Unlock()
}
