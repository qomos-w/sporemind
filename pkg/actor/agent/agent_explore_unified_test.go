package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── terminateSelf ──────────────────────────────────────────────────────────

// TestTerminateSelf_WorkspaceSuccess verifies that terminateSelf calls
// workspace.agent_terminate with the child's own ActorID as both
// AgentActorID and CallerAgentID (self-termination authorization).
func TestTerminateSelf_WorkspaceSuccess(t *testing.T) {
	childActorID := testutil.GenActorID()
	var capturedReq domain.WorkspaceAgentTerminateReq
	workspaceCalled := false
	destroyCalled := false

	ctx := testutil.HumanCtx(childActorID)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "workspace.agent_terminate" {
					workspaceCalled = true
					if req, ok := payload.(domain.WorkspaceAgentTerminateReq); ok {
						capturedReq = req
					}
					return domain.WorkspaceAgentTerminateResp{Deleted: true}
				}
				return nil
			}), true
		}
		return nil, false
	}
	ctx.DestroyFn = func(ref.Ref) error {
		destroyCalled = true
		return nil
	}

	a := &Actor{actorID: childActorID.String()}
	a.terminateSelf(ctx)

	if !workspaceCalled {
		t.Fatal("expected workspace.agent_terminate to be called")
	}
	if capturedReq.AgentActorID != childActorID.String() {
		t.Errorf("AgentActorID = %q, want %q", capturedReq.AgentActorID, childActorID.String())
	}
	if capturedReq.CallerAgentID != childActorID.String() {
		t.Errorf("CallerAgentID = %q, want %q (self-termination)", capturedReq.CallerAgentID, childActorID.String())
	}
	// On workspace success, the fallback ctx.Destroy must NOT fire.
	if destroyCalled {
		t.Fatal("ctx.Destroy should not be called when workspace.agent_terminate succeeds")
	}
}

// TestTerminateSelf_FallsBackToDestroy verifies that when the workspace service
// is unavailable, terminateSelf falls back to ctx.Destroy so the child does
// not linger as a zombie.
func TestTerminateSelf_FallsBackToDestroy(t *testing.T) {
	childActorID := testutil.GenActorID()
	destroyCalled := false

	ctx := testutil.HumanCtx(childActorID)
	// No LookupServiceFn → workspace service not found.
	ctx.DestroyFn = func(target ref.Ref) error {
		destroyCalled = true
		if target == nil {
			t.Error("Destroy called with nil target")
		}
		return nil
	}

	a := &Actor{actorID: childActorID.String()}
	a.terminateSelf(ctx)

	if !destroyCalled {
		t.Fatal("expected ctx.Destroy fallback when workspace is unavailable")
	}
}

// TestTerminateSelf_FallsBackOnWorkspaceError verifies that when
// workspace.agent_terminate returns an error, terminateSelf falls back to
// ctx.Destroy.
func TestTerminateSelf_FallsBackOnWorkspaceError(t *testing.T) {
	childActorID := testutil.GenActorID()
	workspaceCalled := false
	destroyCalled := false

	ctx := testutil.HumanCtx(childActorID)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
				if callID == "workspace.agent_terminate" {
					workspaceCalled = true
					return fmt.Errorf("workspace unavailable")
				}
				return nil
			}), true
		}
		return nil, false
	}
	ctx.DestroyFn = func(ref.Ref) error {
		destroyCalled = true
		return nil
	}

	a := &Actor{actorID: childActorID.String()}
	a.terminateSelf(ctx)

	if !workspaceCalled {
		t.Fatal("expected workspace.agent_terminate to be attempted")
	}
	if !destroyCalled {
		t.Fatal("expected ctx.Destroy fallback when workspace.agent_terminate fails")
	}
}

// TestTerminateSelf_EmptyActorIDFallsBackToDestroy verifies that when the
// child's actorID is empty (malformed), terminateSelf skips the workspace call
// and falls through to ctx.Destroy.
func TestTerminateSelf_EmptyActorIDFallsBackToDestroy(t *testing.T) {
	destroyCalled := false

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		// Workspace is available, but actorID is empty so the call should
		// not be attempted.
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
			t.Errorf("unexpected call %q when actorID is empty", callID)
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error {
		destroyCalled = true
		return nil
	}

	a := &Actor{actorID: ""}
	a.terminateSelf(ctx)

	if !destroyCalled {
		t.Fatal("expected ctx.Destroy fallback when actorID is empty")
	}
}

// ── deliverExploreComplete ────────────────────────────────────────────────

// TestDeliverExploreComplete_DeliversAndTerminates verifies the happy path:
// the result is delivered to the parent via planner.Call("explore_complete")
// and terminateSelf is called (deferred) to remove the child from the
// workspace.
func TestDeliverExploreComplete_DeliversAndTerminates(t *testing.T) {
	childActorID := testutil.GenActorID()
	parentRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	terminateCalled := false

	var capturedCallID string
	var capturedPayload exploreCompleteReq

	ctx := testutil.HumanCtx(childActorID)
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				capturedCallID = callID
				if p, ok := payload.(exploreCompleteReq); ok {
					capturedPayload = p
				}
				return nil, nil
			},
		}
	}
	// workspace service so terminateSelf doesn't just destroy (it would still
	// work, but we want to verify the path is attempted, not that it fails).
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "workspace.agent_terminate" {
					terminateCalled = true
					return domain.WorkspaceAgentTerminateResp{Deleted: true}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{
		actorID: childActorID.String(),
		child: childState{
			Mode:      true,
			AgentRef:  parentRef,
			TurnRef:   "turn-1",
			StepRef:   "step-1",
			ToolUseID: "toolu-1",
		},
	}

	a.deliverExploreComplete(ctx, domain.ForkResult{
		Summary:     "Found 3 files",
		SearchCount: 5,
		ReadCount:   2,
	})

	if capturedCallID != "explore_complete" {
		t.Errorf("expected callID %q, got %q", "explore_complete", capturedCallID)
	}
	if capturedPayload.ParentTurnID != "turn-1" {
		t.Errorf("ParentTurnID = %q, want turn-1", capturedPayload.ParentTurnID)
	}
	if capturedPayload.ParentStepID != "step-1" {
		t.Errorf("ParentStepID = %q, want step-1", capturedPayload.ParentStepID)
	}
	if capturedPayload.ToolUseID != "toolu-1" {
		t.Errorf("ToolUseID = %q, want toolu-1", capturedPayload.ToolUseID)
	}
	if capturedPayload.Result.Summary != "Found 3 files" {
		t.Errorf("Result.Summary = %q, want 'Found 3 files'", capturedPayload.Result.Summary)
	}
	if !terminateCalled {
		t.Fatal("expected terminateSelf to call workspace.agent_terminate")
	}
}

// TestDeliverExploreComplete_NoPlannerStillTerminates verifies that when the
// planner is nil (unavailable), deliverExploreComplete still calls
// terminateSelf via the defer — the child must not linger as a zombie.
func TestDeliverExploreComplete_NoPlannerStillTerminates(t *testing.T) {
	childActorID := testutil.GenActorID()
	parentRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	terminateCalled := false

	ctx := testutil.HumanCtx(childActorID)
	// No PlannerFn → ctx.Planner() returns nil.
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
				if callID == "workspace.agent_terminate" {
					terminateCalled = true
					return domain.WorkspaceAgentTerminateResp{Deleted: true}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{
		actorID: childActorID.String(),
		child: childState{
			Mode:     true,
			AgentRef: parentRef,
		},
	}

	a.deliverExploreComplete(ctx, domain.ForkResult{Summary: "test"})

	if !terminateCalled {
		t.Fatal("expected terminateSelf even when planner is nil")
	}
}

// TestDeliverExploreComplete_NoAgentRefStillTerminates verifies that when
// child.AgentRef is nil (child not properly initialized), deliverExploreComplete
// still calls terminateSelf via the defer.
func TestDeliverExploreComplete_NoAgentRefStillTerminates(t *testing.T) {
	childActorID := testutil.GenActorID()
	terminateCalled := false

	ctx := testutil.HumanCtx(childActorID)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
				if callID == "workspace.agent_terminate" {
					terminateCalled = true
					return domain.WorkspaceAgentTerminateResp{Deleted: true}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{
		actorID: childActorID.String(),
		child: childState{
			Mode:     true,
			AgentRef: nil, // no parent ref set
		},
	}

	a.deliverExploreComplete(ctx, domain.ForkResult{Summary: "test"})

	if !terminateCalled {
		t.Fatal("expected terminateSelf even when AgentRef is nil")
	}
}

// ── handleExploreComplete (result return path) ───────────────────────────

// TestHandleExploreComplete_RemovesChildFromActiveChildren verifies that
// handleExploreComplete removes the child from activeChildren keyed by
// ToolUseID, regardless of whether the parent turn is still active.
func TestHandleExploreComplete_RemovesChildFromActiveChildren(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	childRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a := &Actor{
		activeChildren: map[string]ref.Ref{"toolu-1": childRef},
	}

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ToolUseID: "toolu-1",
		Result: domain.ForkResult{
			Summary: "done",
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	a.activeChildrenMu.Lock()
	_, stillActive := a.activeChildren["toolu-1"]
	a.activeChildrenMu.Unlock()
	if stillActive {
		t.Fatal("expected child removed from activeChildren after explore_complete")
	}
}

// TestHandleExploreComplete_PersistsResultToRawSession verifies that
// handleExploreComplete upserts the result into RawSession.ExploreResults
// keyed by TurnID+StepID.
func TestHandleExploreComplete_PersistsResultToRawSession(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		ActiveTurnRef: "turn-1",
	}

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ParentTurnID: "turn-1",
		ParentStepID: "step-1",
		ToolUseID:    "toolu-1",
		Result: domain.ForkResult{
			Summary:     "Exploration findings",
			SearchCount: 3,
			ReadCount:   7,
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	if len(a.RawSession.ExploreResults) != 1 {
		t.Fatalf("expected 1 ExploreResult, got %d", len(a.RawSession.ExploreResults))
	}
	er := a.RawSession.ExploreResults[0]
	if er.TurnID != "turn-1" {
		t.Errorf("TurnID = %q, want turn-1", er.TurnID)
	}
	if er.StepID != "step-1" {
		t.Errorf("StepID = %q, want step-1", er.StepID)
	}
	if er.Summary != "Exploration findings" {
		t.Errorf("Summary = %q, want 'Exploration findings'", er.Summary)
	}
	if er.SearchCount != 3 {
		t.Errorf("SearchCount = %d, want 3", er.SearchCount)
	}
	if er.ReadCount != 7 {
		t.Errorf("ReadCount = %d, want 7", er.ReadCount)
	}
}

// TestHandleExploreComplete_UpsertsExistingResult verifies that a second
// explore_complete for the same TurnID+StepID updates the existing entry
// instead of appending a duplicate.
func TestHandleExploreComplete_UpsertsExistingResult(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		ActiveTurnRef: "turn-1",
		RawSession: domain.RawSession{
			ExploreResults: []domain.ExploreResult{
				{
					TurnID:  "turn-1",
					StepID:  "step-1",
					Summary: "old summary",
				},
			},
		},
	}

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ParentTurnID: "turn-1",
		ParentStepID: "step-1",
		ToolUseID:    "toolu-1",
		Result: domain.ForkResult{
			Summary: "updated summary",
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	if len(a.RawSession.ExploreResults) != 1 {
		t.Fatalf("expected 1 ExploreResult (upsert), got %d", len(a.RawSession.ExploreResults))
	}
	if a.RawSession.ExploreResults[0].Summary != "updated summary" {
		t.Errorf("Summary = %q, want 'updated summary'", a.RawSession.ExploreResults[0].Summary)
	}
}

// TestHandleExploreComplete_EmptySummaryGetsDefault verifies that when the
// result summary is empty, a default message is used.
func TestHandleExploreComplete_EmptySummaryGetsDefault(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{ActiveTurnRef: "turn-1"}

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ParentTurnID: "turn-1",
		ParentStepID: "step-1",
		ToolUseID:    "toolu-1",
		Result: domain.ForkResult{
			Summary: "", // empty
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	if len(a.RawSession.ExploreResults) != 1 {
		t.Fatalf("expected 1 ExploreResult, got %d", len(a.RawSession.ExploreResults))
	}
	if a.RawSession.ExploreResults[0].Summary != "Exploration completed with no findings." {
		t.Errorf("Summary = %q, want default", a.RawSession.ExploreResults[0].Summary)
	}
}

// TestHandleExploreComplete_DeliversToActiveTurnEngine verifies that when the
// parent turn is still active (ActiveTurnRef matches ParentTurnID), the result
// is delivered to the inline turnEngine via childDoneCh.
func TestHandleExploreComplete_DeliversToActiveTurnEngine(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		childDoneCh: make(chan childResult, 1),
	}
	a := &Actor{ActiveTurnRef: "turn-1"}
	a.turnEngineStore(eng)

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ParentTurnID: "turn-1",
		ParentStepID: "step-1",
		ToolUseID:    "toolu-1",
		Result: domain.ForkResult{
			Summary: "active turn result",
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	select {
	case cr := <-eng.childDoneCh:
		if cr.ToolUseID != "toolu-1" {
			t.Errorf("childDoneCh ToolUseID = %q, want toolu-1", cr.ToolUseID)
		}
		if cr.Result.Summary != "active turn result" {
			t.Errorf("childDoneCh Summary = %q, want 'active turn result'", cr.Result.Summary)
		}
	default:
		t.Fatal("expected result in childDoneCh")
	}
}

// TestHandleExploreComplete_StaleTurnPersistsOnly verifies that when the parent
// turn has moved on (ActiveTurnRef does not match ParentTurnID), the result is
// persisted to RawSession but NOT delivered to the turn engine (no
// childDoneCh send).
func TestHandleExploreComplete_StaleTurnPersistsOnly(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	eng := &turnEngine{
		childDoneCh: make(chan childResult, 1),
	}
	a := &Actor{ActiveTurnRef: "new-turn"} // different from ParentTurnID
	a.turnEngineStore(eng)

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ParentTurnID: "old-turn",
		ParentStepID: "step-1",
		ToolUseID:    "toolu-1",
		Result: domain.ForkResult{
			Summary: "stale result",
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	// Result should be persisted.
	if len(a.RawSession.ExploreResults) != 1 {
		t.Fatalf("expected 1 ExploreResult persisted, got %d", len(a.RawSession.ExploreResults))
	}
	if a.RawSession.ExploreResults[0].Summary != "stale result" {
		t.Errorf("Summary = %q, want 'stale result'", a.RawSession.ExploreResults[0].Summary)
	}

	// childDoneCh should be empty (no delivery to turn engine).
	select {
	case <-eng.childDoneCh:
		t.Fatal("expected no result in childDoneCh for stale turn")
	default:
	}
}

// TestHandleExploreComplete_MemorySleepSpecialCase verifies that when
// ToolUseID is "memory-sleep", handleExploreComplete calls finishMemorySleep
// instead of persisting to ExploreResults.
func TestHandleExploreComplete_MemorySleepSpecialCase(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	a := &Actor{
		ActiveTurnRef:   "turn-1",
		memorySleeping:  true,
		memorySleepTurn: "turn-1",
	}

	err := a.handleExploreComplete(ctx, exploreCompleteReq{
		ToolUseID: "memory-sleep",
		Result: domain.ForkResult{
			Summary: "Memory consolidation done",
		},
	})
	if err != nil {
		t.Fatalf("handleExploreComplete: %v", err)
	}

	// memorySleeping should be cleared by finishMemorySleep.
	if a.memorySleeping {
		t.Error("expected memorySleeping to be false after memory-sleep completion")
	}
	// ExploreResults should NOT be populated for memory-sleep.
	if len(a.RawSession.ExploreResults) != 0 {
		t.Errorf("expected 0 ExploreResults for memory-sleep, got %d", len(a.RawSession.ExploreResults))
	}
}

// ── spawnChild error paths ────────────────────────────────────────────────

// TestSpawnChild_NoProjectService verifies that spawnChild returns an error
// when the project service is not available.
func TestSpawnChild_NoProjectService(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// No LookupServiceFn → project service not found.

	a := &Actor{}
	_, err := a.spawnChild(ctx, childSpawnOpts{
		Kind:        "general",
		Description: "task",
		Prompt:      "do work",
		Slot:        slotFromUnit(domain.ModelUnit{Model: "m", Provider: "p"}),
		ToolUseID:   "toolu-1",
		NamePrefix:  "general",
	})
	if err == nil {
		t.Fatal("expected error when project service is unavailable")
	}
}

// TestSpawnChild_EmptyActorIDInResponse verifies that spawnChild returns an
// error when project.spawn_agent returns an empty ActorID.
func TestSpawnChild_EmptyActorIDInResponse(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
				if callID == "project.spawn_agent" {
					return domain.ProjectSpawnAgentResp{ActorID: ""} // empty
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{}
	_, err := a.spawnChild(ctx, childSpawnOpts{
		Kind:        "general",
		Description: "task",
		Prompt:      "do work",
		Slot:        slotFromUnit(domain.ModelUnit{Model: "m", Provider: "p"}),
		ToolUseID:   "toolu-1",
		NamePrefix:  "general",
	})
	if err == nil {
		t.Fatal("expected error when spawn returns empty ActorID")
	}
}

// TestSpawnChild_PassesChildConfigToSpawnAgent verifies that spawnChild
// correctly threads the child context (ParentActorID, ParentTurnID,
// ParentStepID, ParentToolUseID, Task, Prompt, MaxIterations, HotContext)
// through ChildSpawnConfig to project.spawn_agent.
func TestSpawnChild_PassesChildConfigToSpawnAgent(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	parentActorID := "parent-actor-123"
	childActorID := testutil.GenActorID().String()

	var capturedReq domain.ProjectSpawnAgentReq
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
						capturedReq = req
					}
					return domain.ProjectSpawnAgentResp{ActorID: childActorID}
				}
				return nil
			}), true
		}
		return nil, false
	}

	a := &Actor{
		actorID:     parentActorID,
		workspaceID: "ws-1",
	}
	_, err := a.spawnChild(ctx, childSpawnOpts{
		Kind:          "explorer",
		Description:   "explore the codebase",
		Prompt:        "find entry points",
		Slot:          slotFromUnit(domain.ModelUnit{Model: "m", Provider: "p"}),
		MaxIterations: 5,
		ParentStepID:  "step-1",
		ParentTurnID:  "turn-1",
		ToolUseID:     "toolu-1",
		NamePrefix:    "explore",
	})
	if err != nil {
		t.Fatalf("spawnChild: %v", err)
	}

	if capturedReq.ChildConfig == nil {
		t.Fatal("expected ChildConfig to be set in spawn request")
	}
	cc := capturedReq.ChildConfig
	if cc.ParentActorID != parentActorID {
		t.Errorf("ParentActorID = %q, want %q", cc.ParentActorID, parentActorID)
	}
	if cc.ParentTurnID != "turn-1" {
		t.Errorf("ParentTurnID = %q, want turn-1", cc.ParentTurnID)
	}
	if cc.ParentStepID != "step-1" {
		t.Errorf("ParentStepID = %q, want step-1", cc.ParentStepID)
	}
	if cc.ParentToolUseID != "toolu-1" {
		t.Errorf("ParentToolUseID = %q, want toolu-1", cc.ParentToolUseID)
	}
	if cc.Task != "explore the codebase" {
		t.Errorf("Task = %q, want 'explore the codebase'", cc.Task)
	}
	if cc.MaxIterations != 5 {
		t.Errorf("MaxIterations = %d, want 5", cc.MaxIterations)
	}

	// Verify the child is tracked in activeChildren.
	a.activeChildrenMu.Lock()
	_, tracked := a.activeChildren["toolu-1"]
	a.activeChildrenMu.Unlock()
	if !tracked {
		t.Fatal("expected child tracked in activeChildren")
	}
}

// ── general-kind slot priority (shared with workflow workers) ──────────────
// Workflow workers (workspace.agent_spawn_assign) inherit their model slot
// from the parent agent through the same resolve_child_slot callable as fork
// children, so this priority is the single source of truth for both paths.

// TestResolveChildSlot_GeneralKindPriorityParityWithWorkflowWorker verifies
// that resolveChildSlot for the general kind (the branch workflow workers
// also hit) uses execution → primary → fast priority. This is the
// cross-cutting invariant that the unified spawn path enforces.
func TestResolveChildSlot_GeneralKindPriorityParityWithWorkflowWorker(t *testing.T) {
	ctx := newFakeCompactionContext()

	t.Run("execution wins over primary", func(t *testing.T) {
		a := slotTestActor()
		a.execution = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "exec-model", Provider: "p"}},
		}}
		a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "p"}},
		}}

		slot, err := a.resolveChildSlot(ctx, "general", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if u := slotFirstUnit(slot); u.Model != "exec-model" {
			t.Errorf("expected exec-model (execution priority), got %q", u.Model)
		}
	})

	t.Run("primary fallback when no execution", func(t *testing.T) {
		a := slotTestActor()
		// Execution slot has an unresolvable aggregator (not in cache), so
		// resolveTargets(execution) returns 0 and the resolver falls back
		// to primary.
		a.execution = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "missing-agg"},
		}}
		a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "p"}},
		}}

		slot, err := a.resolveChildSlot(ctx, "general", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if u := slotFirstUnit(slot); u.Model != "primary-model" {
			t.Errorf("expected primary-model (fallback), got %q", u.Model)
		}
	})

	t.Run("fast fallback when no execution or primary", func(t *testing.T) {
		a := slotTestActor()
		// Execution and primary have unresolvable aggregators, so the
		// resolver falls back to fast.
		a.execution = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "missing-agg"},
		}}
		a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindAggregator, AggregatorID: "also-missing"},
		}}
		a.fast = domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "fast-model", Provider: "p"}},
		}}

		slot, err := a.resolveChildSlot(ctx, "general", nil)
		if err != nil {
			t.Fatalf("resolveChildSlot: %v", err)
		}
		if u := slotFirstUnit(slot); u.Model != "fast-model" {
			t.Errorf("expected fast-model (last fallback), got %q", u.Model)
		}
	})
}

// TestHandleResolveChildSlot_ReturnsNonPrimarySlots verifies that
// handleResolveChildSlot (the internal resolve_child_slot callable body) copies
// the parent's fast/execution/review/summary runtime slots into the response,
// leaving empty ([auto], slotIsEmpty) slots zero-valued so callers can drop
// them and keep the child on the default [auto].
func TestHandleResolveChildSlot_ReturnsNonPrimarySlots(t *testing.T) {
	ctx := newFakeCompactionContext()
	a := slotTestActor()
	a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "p"}},
	}}
	a.execution = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "exec-model", Provider: "p"}},
	}}
	a.review = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "review-model", Provider: "p"}},
	}}
	// fast and summary stay empty ([auto]): must be returned zero-valued.

	resp, err := a.handleResolveChildSlot(ctx, ResolveChildSlotReq{ChildKind: "general"})
	if err != nil {
		t.Fatalf("handleResolveChildSlot: %v", err)
	}
	// Primary-purpose resolution unchanged: general kind prefers execution.
	if u := slotFirstUnit(resp.Slot); u.Model != "exec-model" {
		t.Errorf("Slot = %q, want exec-model (execution priority for general kind)", u.Model)
	}
	if u := slotFirstUnit(resp.Execution); u.Model != "exec-model" {
		t.Errorf("Execution = %q, want exec-model", u.Model)
	}
	if u := slotFirstUnit(resp.Review); u.Model != "review-model" {
		t.Errorf("Review = %q, want review-model", u.Model)
	}
	for name, slot := range map[string]domain.ModelSlot{
		"Fast":    resp.Fast,
		"Summary": resp.Summary,
	} {
		if !slotIsEmpty(slot) {
			t.Errorf("%s = %+v, want zero value (empty [auto] slot not returned)", name, slot)
		}
	}
}

// TestHandleResolveChildSlot_PrimaryFallbackRegression pins the handler-level
// primary-purpose priority regression (general kind: execution → primary →
// fast): with the parent's execution slot unresolvable, the resolved Slot
// falls back to the parent's primary model, while non-primary slots are still
// copied verbatim and empty ([auto]) slots stay zero-valued. Note an empty
// execution slot is [auto] and resolves on its own — it is not a fallback
// trigger, so the regression uses an unresolvable aggregator candidate.
func TestHandleResolveChildSlot_PrimaryFallbackRegression(t *testing.T) {
	ctx := newFakeCompactionContext()
	a := slotTestActor()
	// Execution holds an aggregator missing from the cache: resolveTargets
	// yields nothing, so the resolver falls back to primary.
	a.execution = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindAggregator, AggregatorID: "missing-agg"},
	}}
	a.primary = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "primary-model", Provider: "p"}},
	}}
	a.review = domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: modelRefKindUnit, Unit: &domain.ModelUnit{Model: "review-model", Provider: "p"}},
	}}
	// fast and summary stay empty ([auto]).

	resp, err := a.handleResolveChildSlot(ctx, ResolveChildSlotReq{ChildKind: "general"})
	if err != nil {
		t.Fatalf("handleResolveChildSlot: %v", err)
	}
	if u := slotFirstUnit(resp.Slot); u.Model != "primary-model" {
		t.Errorf("Slot = %q, want primary-model (primary fallback when execution is unresolvable)", u.Model)
	}
	// The configured (though unresolvable) execution slot is still copied
	// verbatim for the child to re-resolve on its own.
	if got := len(resp.Execution.Candidates); got != 1 || resp.Execution.Candidates[0].AggregatorID != "missing-agg" {
		t.Errorf("Execution = %+v, want the configured missing-agg slot copied verbatim", resp.Execution)
	}
	if u := slotFirstUnit(resp.Review); u.Model != "review-model" {
		t.Errorf("Review = %q, want review-model", u.Model)
	}
	for name, slot := range map[string]domain.ModelSlot{
		"Fast":    resp.Fast,
		"Summary": resp.Summary,
	} {
		if !slotIsEmpty(slot) {
			t.Errorf("%s = %+v, want zero value (empty [auto] slot not returned)", name, slot)
		}
	}
}
