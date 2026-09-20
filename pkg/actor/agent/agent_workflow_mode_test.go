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

// TestEnsureWorkflowCardMounted_ActiveWorkflowMountsModeAndBundle verifies that
// an active workflow session mounts builtin:mode:workflow plus its
// builtin:bundle:workflow-tools dependency.
func TestEnsureWorkflowCardMounted_ActiveWorkflowMountsModeAndBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureWorkflowCardMounted(ctx) {
		t.Fatal("expected ensureWorkflowCardMounted to make a change")
	}
	for _, want := range []string{"builtin:mode:workflow", "builtin:bundle:workflow-tools"} {
		foundRef, foundMount := false, false
		for _, ref := range a.cardRefs {
			if ref.ID == want && ref.Scope == "user" && !ref.Disabled {
				foundRef = true
			}
		}
		for _, m := range a.ComponentMounts {
			if m.CardID == want && m.Enabled && m.Scope == "user" {
				foundMount = true
			}
		}
		if !foundRef || !foundMount {
			t.Fatalf("expected %s mounted in cardRefs (ref=%t) and ComponentMounts (mount=%t), got refs %+v mounts %+v",
				want, foundRef, foundMount, a.cardRefs, a.ComponentMounts)
		}
	}
}

// TestEnsureWorkflowCardMounted_NoActiveWorkflowDoesNothing verifies that an
// agent without an active workflow session does not get workflow mode.
func TestEnsureWorkflowCardMounted_NoActiveWorkflowDoesNothing(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if a.ensureWorkflowCardMounted(ctx) {
		t.Fatal("expected no change when ActiveWorkflow is nil")
	}

	// A bound goal without an active workflow must not mount workflow mode.
	a.RawSession.Goal = &gen.SessionGoal{Condition: "plain goal", Confirmed: true, BoundTaskCardID: "task-1"}
	if a.ensureWorkflowCardMounted(ctx) {
		t.Fatal("expected no change when goal is bound but no active workflow")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" || ref.ID == "builtin:bundle:workflow-tools" {
			t.Fatalf("expected no workflow cards to be added, got %+v", ref)
		}
	}
}

// TestEnsureWorkflowCardMounted_ReenablesDisabledMode verifies that a disabled
// workflow mode is re-enabled so the active owner keeps the mode active.
func TestEnsureWorkflowCardMounted_ReenablesDisabledMode(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user", Disabled: true},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureWorkflowCardMounted(ctx) {
		t.Fatal("expected ensureWorkflowCardMounted to re-enable the workflow mode")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" && ref.Disabled {
			t.Fatalf("expected builtin:mode:workflow to be re-enabled, got %+v", ref)
		}
	}
}

// TestClearWorkflow_RemovesModeAndBundle covers the full stop lifecycle:
// clearWorkflow removes ActiveWorkflow, the mode, and its bundle.
func TestClearWorkflow_RemovesModeAndBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:goal", Scope: "user"},
			{ID: "builtin:bundle:goal", Scope: "user"},
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
			{ID: "skill:plan-module", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	a.clearWorkflow(ctx)

	if a.RawSession.ActiveWorkflow != nil {
		t.Fatal("expected ActiveWorkflow to be nil after clearWorkflow")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" || ref.ID == "builtin:bundle:workflow-tools" {
			t.Fatalf("expected workflow cards removed from cardRefs after clearWorkflow, got %+v", ref)
		}
	}
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:mode:workflow" || m.CardID == "builtin:bundle:workflow-tools" {
			t.Fatalf("expected workflow cards removed from ComponentMounts after clearWorkflow, got %+v", m)
		}
	}
	// Goal cards must survive clearWorkflow — they are independent.
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" || ref.ID == "builtin:bundle:goal" {
			return
		}
	}
	t.Fatal("expected goal cards to survive clearWorkflow")
}

// TestClearGoal_DoesNotRemoveWorkflowCards verifies that clearing a goal does
// not touch the workflow session or its mounts — the two lifecycles are
// independent under the active-owner model.
func TestClearGoal_DoesNotRemoveWorkflowCards(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal:           &gen.SessionGoal{Condition: "implement auth", Confirmed: true, BoundTaskCardID: "task-1"},
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:goal", Scope: "user"},
			{ID: "builtin:bundle:goal", Scope: "user"},
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	a.clearGoal(ctx)

	if a.RawSession.Goal != nil {
		t.Fatal("expected Goal to be nil after clearGoal")
	}
	if a.RawSession.ActiveWorkflow == nil {
		t.Fatal("expected ActiveWorkflow to survive clearGoal")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" || ref.ID == "builtin:bundle:workflow-tools" {
			return
		}
	}
	t.Fatal("expected workflow cards to survive clearGoal")
}

// TestReloadWorkflowSession_RestoresModeAndToolsWithoutWorkspace verifies that
// a reloaded session with a persisted ActiveWorkflow repairs the missing
// workflow mode mount and exposes the workflow orchestration tools from the
// embedded bundle asset, with no workspace lookup available.
func TestReloadWorkflowSession_RestoresModeAndToolsWithoutWorkspace(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureWorkflowCardMounted(ctx) {
		t.Fatal("expected ensureWorkflowCardMounted to repair the missing workflow mount")
	}
	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected restoreComponentMountMetadata to update metadata")
	}

	list, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	var modeMount *domain.AgentComponentMount
	for i := range list.Items {
		if list.Items[i].CardID == "builtin:mode:workflow" {
			modeMount = &list.Items[i]
			break
		}
	}
	if modeMount == nil {
		t.Fatalf("workflow mode mount missing from handleComponentList: %+v", list.Items)
	}
	if modeMount.Title != "Workflow Mode" || modeMount.Icon != "waypoints" {
		t.Fatalf("workflow mode mount missing title/icon: %+v", modeMount)
	}
	if modeMount.Visual == nil || modeMount.Visual.Color != "#8b5cf6" {
		t.Fatalf("workflow mode mount missing visual color: %+v", modeMount.Visual)
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	hasSpawnAssign, hasAgentReview, hasListAgents := false, false, false
	hasGoalSubmit, hasGoalCardSubmit := false, false
	for _, tool := range snapshot.Tools {
		switch tool.CallableID {
		case "workspace.agent_spawn_assign":
			hasSpawnAssign = true
		case "workspace.agent_review":
			hasAgentReview = true
		case "workspace.list_agents":
			hasListAgents = true
		case "goal_submit":
			hasGoalSubmit = true
		case "goal_card_submit":
			hasGoalCardSubmit = true
		}
	}
	if !hasSpawnAssign {
		t.Error("component snapshot missing workspace.agent_spawn_assign tool")
	}
	if !hasAgentReview {
		t.Error("component snapshot missing workspace.agent_review tool")
	}
	if !hasListAgents {
		t.Error("component snapshot missing workspace.list_agents tool")
	}
	if hasGoalSubmit || hasGoalCardSubmit {
		t.Errorf("workflow mode must not expose goal tools (goal_submit=%t goal_card_submit=%t)", hasGoalSubmit, hasGoalCardSubmit)
	}
}

// TestUnmountGoalModeDoesNotClearWorkflow verifies that unmounting goal mode on
// an agent with both a goal and an active workflow clears the goal but leaves
// the workflow session and its mounts intact.
func TestUnmountGoalModeDoesNotClearWorkflow(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{})
	a := &Actor{
		RawSession: gen.RawSession{
			Goal:           &gen.SessionGoal{Condition: "implement auth", Confirmed: true, BoundTaskCardID: "task-1"},
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:goal", Scope: "user"},
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:goal"}); err != nil {
		t.Fatalf("handleComponentUnmount: %v", err)
	}
	if a.RawSession.Goal != nil {
		t.Fatal("expected Goal to be nil after unmounting goal mode")
	}
	if a.RawSession.ActiveWorkflow == nil {
		t.Fatal("expected ActiveWorkflow to survive goal unmount")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" || ref.ID == "builtin:bundle:workflow-tools" {
			return
		}
	}
	t.Fatal("expected workflow cards to survive goal unmount")
}

// TestStartNextGoalTurn_WorkflowOwnerIsEventDriven verifies that a workflow map
// owner with a confirmed goal does NOT auto-continue via startNextGoalTurn. The
// owner is event-driven: the project updater wakes it via chat_submit when
// frontier work appears or a worker needs review. Auto-continuing would
// busy-loop the owner, keeping its status permanently "running" so the updater's
// owner-busy check suppresses every notification and workers stall in
// ready_for_review indefinitely.
func TestStartNextGoalTurn_WorkflowOwnerIsEventDriven(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{
				Condition: "ship the workflow",
				Confirmed: true,
				MaxTurns:  200,
				TurnCount: 1,
				Status:    "active",
			},
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	before := len(a.Session.Turns)
	if a.startNextGoalTurn(ctx) {
		t.Fatal("workflow owner must not auto-continue (event-driven); startNextGoalTurn should return false")
	}
	if got := len(a.Session.Turns); got != before {
		t.Fatalf("workflow owner startNextGoalTurn must have no side effects, turns changed %d -> %d", before, got)
	}
}

// TestWorkflowActiveReport checks workflowActive reflects the session state.
func TestWorkflowActiveReport(t *testing.T) {
	a := &Actor{}
	if a.workflowActive() {
		t.Fatal("expected inactive with nil ActiveWorkflow")
	}
	a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: ""}
	if a.workflowActive() {
		t.Fatal("expected inactive with empty MapCardID")
	}
	a.RawSession.ActiveWorkflow.MapCardID = "map-1"
	if !a.workflowActive() {
		t.Fatal("expected active with non-empty MapCardID")
	}
}

// --- workflow_start / workflow_stop handler tests ---

// makeWorkflowCtx creates a test context with a fake project parent that
// responds to wiki_set_map_owner and wiki_set_status, plus a fake workspace
// reporting no live child agents.
func makeWorkflowCtx(t *testing.T, ownerErr, statusErr error) actor.Context {
	t.Helper()
	return makeWorkflowCtxWithAgents(t, ownerErr, statusErr, nil)
}

// makeWorkflowCtxWithAgents is makeWorkflowCtx with a controllable workspace
// agent list, used to exercise workflow_stop's live-children guard.
func makeWorkflowCtxWithAgents(t *testing.T, ownerErr, statusErr error, agents []domain.AgentRef) actor.Context {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.wiki_set_map_owner":
			if ownerErr != nil {
				return ownerErr
			}
			return domain.WikiSetMapOwnerResp{}
		case "project.workflow_create_worktree":
			return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: "wt-owner"}
		case "project.workflow_stop_merge_worktree":
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			if statusErr != nil {
				return statusErr
			}
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: agents}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	return ctx
}

func TestHandleWorkflowStart_Success(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("handleWorkflowStart: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow active after start")
	}
	if a.RawSession.ActiveWorkflow.WorktreeID != "wt-owner" {
		t.Fatalf("expected session worktree ID wt-owner, got %q", a.RawSession.ActiveWorkflow.WorktreeID)
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode mounted after start")
	}
	if !hasMountedCard(a, "builtin:bundle:workflow-tools") {
		t.Fatal("expected workflow-tools bundle mounted after start")
	}
}

// TestHandleWorkflowStart_NoGitWorktreeStoresEmptyID pins the no-git mode
// activation path (project.workflow_create_worktree returned an empty
// WorktreeID because a.noGitMode() or the root is not a git repo): the empty
// ID is stored into ActiveWorkflow.WorktreeID, workflow mode still mounts, and
// refreshWorktreeStatus clears any stale worktree binding cache from a
// previous git-mode session (no binding exists — no worktree badge, no error).
func TestHandleWorkflowStart_NoGitWorktreeStoresEmptyID(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.wiki_set_map_owner":
			return domain.WikiSetMapOwnerResp{}
		case "project.workflow_create_worktree":
			return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: ""}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				switch callID {
				case "project.worktree_agent_bindings":
					// No-git workflow owner: no worktree binding exists.
					return gen.ProjectWorktreeAgentBindingsResp{Bindings: nil}, nil
				}
				return nil, fmt.Errorf("unexpected planner call %s", callID)
			},
		}
	}
	a := &Actor{
		actorID:        "owner-actor-1",
		worktreeID:     "stale-wt",
		worktreeName:   "stale-name",
		worktreeStatus: "active",
		worktreePath:   "/stale/path",
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
			{ID: "builtin:mode:worktree", Scope: "user"},
			{ID: "builtin:bundle:worktree-tools", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("handleWorkflowStart: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow active after start")
	}
	if a.RawSession.ActiveWorkflow.WorktreeID != "" {
		t.Fatalf("expected empty session WorktreeID in no-git mode, got %q", a.RawSession.ActiveWorkflow.WorktreeID)
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode mounted after start")
	}
	// The stale git-mode binding cache must be cleared: refreshWorktreeStatus
	// found no binding for this agent, so no worktree badge may linger.
	if a.worktreeID != "" || a.worktreeName != "" || a.worktreeStatus != "" || a.worktreePath != "" {
		t.Fatalf("stale worktree cache not cleared: (ID=%q Name=%q Status=%q Path=%q)", a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
	if hasMountedCard(a, "builtin:mode:worktree") {
		t.Fatal("expected worktree mode unmounted after no-binding refresh")
	}
}

// TestHandleWorkflowStop_NoGitWorktreeSkipsMerge pins the no-git stop path:
// when ActiveWorkflow.WorktreeID is empty (no-git activation), workflow_stop
// never invokes the merge callable and marks the map done directly.
func TestHandleWorkflowStop_NoGitWorktreeSkipsMerge(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	mergeCalls := 0
	statusCalls := 0
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.workflow_stop_merge_worktree":
			mergeCalls++
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			statusCalls++
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"}, // WorktreeID empty (no-git)
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if mergeCalls != 0 {
		t.Fatalf("workflow_stop must not invoke the merge callable with an empty WorktreeID, got %d calls", mergeCalls)
	}
	if statusCalls != 1 {
		t.Fatalf("map done stamp must be invoked exactly once, got %d calls", statusCalls)
	}
	if a.workflowActive() {
		t.Fatal("expected workflow inactive after stop")
	}
	if hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode unmounted after stop")
	}
}

func TestHandleWorkflowStart_RejectsEmptyMapID(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{}
	_, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: ""})
	if err == nil || !strings.Contains(err.Error(), "MapCardId is required") {
		t.Fatalf("expected empty MapCardId error, got %v", err)
	}
}

func TestHandleWorkflowStart_RejectsDifferentActiveWorkflow(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	_, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: "map-2"})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected already-active error, got %v", err)
	}
}

func TestHandleWorkflowStart_IdempotentSameMap(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	resp, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("expected idempotent start for same map, got %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
}

func TestHandleWorkflowStart_ProjectUnavailable(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	// No ParentRef set → ctx.Parent() returns nil
	a := &Actor{}
	_, err := a.handleWorkflowStart(ctx, domain.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err == nil || !strings.Contains(err.Error(), "project is unavailable") {
		t.Fatalf("expected project unavailable error, got %v", err)
	}
}

func TestHandleWorkflowStop_Success(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if a.workflowActive() {
		t.Fatal("expected workflow inactive after stop")
	}
	if hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode unmounted after stop")
	}
}

// TestHandleWorkflowStop_FailureStillExitsWorkflowMode pins the anti-deadlock
// contract: when the merge step fails, workflow_stop must still exit workflow
// mode (otherwise the workflow updater keeps waking the owner with
// tree-exhausted notifications that fail identically forever). The failure is
// surfaced in the returned error and the session no longer has ActiveWorkflow.
func TestHandleWorkflowStop_FailureKeepsWorkflowActive(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.workflow_stop_merge_worktree":
			return fmt.Errorf("rebase to main: conflict in file.go")
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1", WorktreeID: "wt-owner"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "workflow still active") {
		t.Fatalf("expected surfaced failure error, got %v", err)
	}
	if !strings.Contains(err.Error(), "rebase to main") {
		t.Fatalf("error must include the underlying cause, got %v", err)
	}
	if !a.workflowActive() {
		t.Fatal("workflow mode must stay active on merge failure so the owner can retry without re-activating")
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode card to remain mounted after failed stop")
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1 in failure response, got %q", resp.MapCardID)
	}
}

// TestHandleWorkflowStop_PassesRequireSynced pins the manual-sync contract:
// workflow_stop must call the project merge callable with RequireSynced=true
// (verify-only, no auto-rebase) so conflict resolution stays in the owner's
// own turn.
func TestHandleWorkflowStop_PassesRequireSynced(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	var mergeReq any
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "project.workflow_stop_merge_worktree":
			mergeReq = payload
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1", WorktreeID: "wt-owner"},
		},
	}
	if _, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{}); err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}
	req, ok := mergeReq.(gen.ProjectWorkflowStopMergeWorktreeReq)
	if !ok {
		t.Fatalf("merge call payload = %T, want ProjectWorkflowStopMergeWorktreeReq", mergeReq)
	}
	if !req.RequireSynced {
		t.Error("workflow_stop must request RequireSynced=true (verify-only merge)")
	}
	if req.WorktreeID != "wt-owner" {
		t.Fatalf("WorktreeID = %q, want wt-owner", req.WorktreeID)
	}
}

// TestHandleWorkflowStop_OutdatedKeepsWorkflowActive verifies the outdated
// rejection path: the worktree lacks the latest main HEAD, so workflow_stop
// fails with rebase guidance, the workflow stays active, and the map is never
// marked done.
func TestHandleWorkflowStop_OutdatedKeepsWorkflowActive(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	var statusCalls int
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.workflow_stop_merge_worktree":
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "outdated"}
		case "project.wiki_set_status":
			statusCalls++
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1", WorktreeID: "wt-owner"},
		},
	}
	_, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "rebase") {
		t.Fatalf("expected outdated error with rebase guidance, got %v", err)
	}
	if !strings.Contains(err.Error(), "workflow still active") {
		t.Fatalf("error must state the workflow stays active, got %v", err)
	}
	if !a.workflowActive() {
		t.Fatal("workflow must stay active after outdated rejection")
	}
	if statusCalls != 0 {
		t.Fatal("map must not be marked done when the worktree is outdated")
	}
}

// --- workflow_stop merge-failure ownerWorktreeId stamp recovery ---

// makeStampRecoveryCtx builds a workflow-stop context whose project parent
// always fails project.workflow_stop_merge_worktree (the merge landed on the
// project side but the call reports failure) and answers the map-card stamp
// query as directed: mapRaw is served for project.wiki_get_card when
// getCardErr is nil; a non-nil getCardErr makes the query itself fail. calls,
// when non-nil, records every project callID in order.
func makeStampRecoveryCtx(t *testing.T, mapRaw string, getCardErr error, calls *[]string) actor.Context {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if calls != nil {
			*calls = append(*calls, callID)
		}
		switch callID {
		case "project.workflow_stop_merge_worktree":
			// Post-merge cleanup failure after the merge already landed.
			return fmt.Errorf("delete manifest: sharing violation")
		case "project.wiki_get_card":
			if getCardErr != nil {
				return getCardErr
			}
			return domain.WikiGetCardResp{ID: "map-1", Raw: mapRaw}
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	return ctx
}

func stampRecoveryActor() *Actor {
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1", WorktreeID: "wt-owner"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	return a
}

func callCount(calls []string, callID string) int {
	n := 0
	for _, c := range calls {
		if c == callID {
			n++
		}
	}
	return n
}

// TestHandleWorkflowStop_MergeErrStampClearedCompletesStop pins the idempotent
// recovery: project.workflow_stop_merge_worktree reports an error but the map
// card's data.ownerWorktreeId stamp is already cleared (the project cleared it
// when the merge landed). workflow_stop must treat the merge as landed and
// complete: consult the stamp, mark the map done, clear workflow mode.
func TestHandleWorkflowStop_MergeErrStampClearedCompletesStop(t *testing.T) {
	mapRaw := "---\nid: map-1\ntype: workflow\ndata:\n  ownerAgentId: owner-actor-1\n---\n\nMap body."
	var calls []string
	ctx := makeStampRecoveryCtx(t, mapRaw, nil, &calls)
	a := stampRecoveryActor()

	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err != nil {
		t.Fatalf("handleWorkflowStop must succeed when the stamp is cleared, got %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if a.workflowActive() {
		t.Fatal("workflow must be inactive after stamp-cleared recovery")
	}
	if hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("workflow mode must be unmounted after stamp-cleared recovery")
	}
	if got := callCount(calls, "project.wiki_get_card"); got != 1 {
		t.Fatalf("recovery must consult the map card stamp exactly once, got %d (calls=%v)", got, calls)
	}
	if got := callCount(calls, "project.wiki_set_status"); got != 1 {
		t.Fatalf("map must be marked done exactly once after recovery, got %d (calls=%v)", got, calls)
	}
}

// TestHandleWorkflowStop_MergeErrStampPresentKeepsWorkflowActive pins the
// conservative branch: a merge error plus a data.ownerWorktreeId stamp that is
// still set means the owner worktree is alive on the project side — the stop
// must fail, keep the workflow active and mounted, and never mark the map
// done.
func TestHandleWorkflowStop_MergeErrStampPresentKeepsWorkflowActive(t *testing.T) {
	mapRaw := "---\nid: map-1\ntype: workflow\ndata:\n  ownerAgentId: owner-actor-1\n  ownerWorktreeId: wt-owner\n---\n\nMap body."
	var calls []string
	ctx := makeStampRecoveryCtx(t, mapRaw, nil, &calls)
	a := stampRecoveryActor()

	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "workflow still active") {
		t.Fatalf("expected surfaced failure keeping workflow active, got %v", err)
	}
	if !strings.Contains(err.Error(), "delete manifest") {
		t.Fatalf("error must include the underlying merge cause, got %v", err)
	}
	if !a.workflowActive() {
		t.Fatal("workflow must stay active while the stamp is still set")
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("workflow mode must stay mounted while the stamp is still set")
	}
	if got := callCount(calls, "project.wiki_get_card"); got != 1 {
		t.Fatalf("recovery must consult the map card stamp exactly once, got %d (calls=%v)", got, calls)
	}
	if got := callCount(calls, "project.wiki_set_status"); got != 0 {
		t.Fatalf("map must not be marked done while the stamp is present, got %d calls (calls=%v)", got, calls)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1 in failure response, got %q", resp.MapCardID)
	}
}

// TestHandleWorkflowStop_MergeErrStampQueryFailsKeepsWorkflowActive pins the
// conservative branch for a broken stamp query: when the map card cannot be
// re-fetched, workflow_stop cannot prove the merge landed and must fail with
// the workflow kept active (retryable).
func TestHandleWorkflowStop_MergeErrStampQueryFailsKeepsWorkflowActive(t *testing.T) {
	var calls []string
	ctx := makeStampRecoveryCtx(t, "", fmt.Errorf("wiki store unavailable"), &calls)
	a := stampRecoveryActor()

	_, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "workflow still active") {
		t.Fatalf("expected conservative failure keeping workflow active, got %v", err)
	}
	if !strings.Contains(err.Error(), "delete manifest") {
		t.Fatalf("error must include the underlying merge cause, got %v", err)
	}
	if !a.workflowActive() {
		t.Fatal("workflow must stay active when the stamp query fails")
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("workflow mode must stay mounted when the stamp query fails")
	}
	if got := callCount(calls, "project.wiki_get_card"); got != 1 {
		t.Fatalf("recovery must attempt the stamp query exactly once, got %d (calls=%v)", got, calls)
	}
	if got := callCount(calls, "project.wiki_set_status"); got != 0 {
		t.Fatalf("map must not be marked done when the stamp query fails, got %d calls (calls=%v)", got, calls)
	}
}

// TestOwnerWorktreeStampFromRaw covers the frontmatter stamp parser: the data
// block key, cleared cards, missing/unterminated frontmatter (parsed=false,
// conservative) and body prose that merely mentions the key.
func TestOwnerWorktreeStampFromRaw(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		stamp  string
		parsed bool
	}{
		{
			name:   "stamped in data block",
			raw:    "---\nid: map-1\ndata:\n  ownerAgentId: a-1\n  ownerWorktreeId: wt-9\n---\n\nMap body.",
			stamp:  "wt-9",
			parsed: true,
		},
		{
			name:   "cleared card keeps sibling keys",
			raw:    "---\nid: map-1\ndata:\n  ownerAgentId: a-1\n---\n\nMap body.",
			stamp:  "",
			parsed: true,
		},
		{
			name:   "quoted value",
			raw:    "---\ndata:\n  ownerWorktreeId: 'wt-q'\n---\n",
			stamp:  "wt-q",
			parsed: true,
		},
		{
			name:   "no frontmatter is not parseable",
			raw:    "plain body",
			stamp:  "",
			parsed: false,
		},
		{
			name:   "unterminated frontmatter is not parseable",
			raw:    "---\ndata:\n  ownerWorktreeId: wt-9\n",
			stamp:  "",
			parsed: false,
		},
		{
			name:   "body mention is not a stamp",
			raw:    "---\nid: map-1\ndata:\n  ownerAgentId: a-1\n---\nprose mentioning ownerWorktreeId: wt-fake",
			stamp:  "",
			parsed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stamp, parsed := ownerWorktreeStampFromRaw(tc.raw)
			if stamp != tc.stamp || parsed != tc.parsed {
				t.Fatalf("ownerWorktreeStampFromRaw() = (%q, %v), want (%q, %v)", stamp, parsed, tc.stamp, tc.parsed)
			}
		})
	}
}

func TestHandleWorkflowStop_NoActiveWorkflow(t *testing.T) {
	ctx := makeWorkflowCtx(t, nil, nil)
	a := &Actor{}
	_, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "no active workflow") {
		t.Fatalf("expected no active workflow error, got %v", err)
	}
}

// TestHandleWorkflowStop_MergesBoundOwnerWorktree verifies that workflow_stop
// invokes project.workflow_stop_merge_worktree before clearing the workflow
// when the session tracks a workflow owner worktree, and that the call happens
// before the map is marked done.
func TestHandleWorkflowStop_MergesBoundOwnerWorktree(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	var calls []string
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		calls = append(calls, callID)
		switch callID {
		case "project.workflow_stop_merge_worktree":
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		if callID == "workspace.list_agents" {
			return domain.AgentRefListResp{Items: nil}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1", WorktreeID: "wt-owner"},
		},
	}
	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if a.workflowActive() {
		t.Fatal("expected workflow inactive after stop")
	}
	// The merge must be invoked and must precede the done-status stamp.
	mergeAt, doneAt := -1, -1
	for i, id := range calls {
		if id == "project.workflow_stop_merge_worktree" {
			mergeAt = i
		}
		if id == "project.wiki_set_status" {
			doneAt = i
		}
	}
	if mergeAt < 0 {
		t.Fatalf("workflow_stop must call project.workflow_stop_merge_worktree, calls=%v", calls)
	}
	if doneAt < 0 || mergeAt > doneAt {
		t.Fatalf("merge must run before map done stamp, calls=%v", calls)
	}
}

// TestHandleWorkflowStop_BlockedByLiveChildren verifies that workflow_stop
// fails while live workflow child agents spawned by this owner still exist.
func TestHandleWorkflowStop_BlockedByLiveChildren(t *testing.T) {
	const ownerID = "owner-actor-1"
	child := domain.AgentRef{
		ID:             "Worker#0001",
		ActorID:        "worker-actor-1",
		DisplayName:    "Worker",
		ParentAgentID:  ownerID,
		LifecycleScope: "workflow",
	}
	ctx := makeWorkflowCtxWithAgents(t, nil, nil, []domain.AgentRef{child})
	a := &Actor{
		actorID: ownerID,
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	_, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err == nil || !strings.Contains(err.Error(), "child agent") {
		t.Fatalf("expected live-children error, got %v", err)
	}
	for _, want := range []string{"Worker#0001", `"Worker"`, "worker-actor-1", "workspace.agent_terminate", `CallerAgentId="` + ownerID + `"`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q (child id/name/actorId + terminate instruction), got %v", want, err)
		}
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow to remain active when stop is blocked")
	}
}

// TestHandleWorkflowStop_AllowsDeletingChildren verifies that children already
// tombstoned for deletion do not block workflow_stop.
func TestHandleWorkflowStop_AllowsDeletingChildren(t *testing.T) {
	const ownerID = "owner-actor-1"
	ctx := makeWorkflowCtxWithAgents(t, nil, nil, []domain.AgentRef{
		{
			ID:             "Worker#0001",
			ActorID:        "worker-actor-1",
			DisplayName:    "Worker",
			ParentAgentID:  ownerID,
			LifecycleScope: "workflow",
			DeletionStatus: "deleting",
		},
		// Unrelated agents (different parent) never block.
		{
			ID:            "Other#0001",
			ActorID:       "other-actor-1",
			ParentAgentID: "someone-else",
		},
	})
	a := &Actor{
		actorID: ownerID,
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
	}
	resp, err := a.handleWorkflowStop(ctx, domain.AgentWorkflowStopReq{})
	if err != nil {
		t.Fatalf("handleWorkflowStop: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Fatalf("expected MapCardID map-1, got %q", resp.MapCardID)
	}
	if a.workflowActive() {
		t.Fatal("expected workflow inactive after stop")
	}
}

// TestEnsureGoalCardMounted_ActiveGoalMountsModeAndBundle verifies that a goal
// session mounts builtin:mode:goal plus its builtin:bundle:goal dependency.
func TestEnsureGoalCardMounted_ActiveGoalMountsModeAndBundle(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "implement feature", MaxTurns: 20},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to make a change")
	}
	for _, want := range []string{"builtin:mode:goal", "builtin:bundle:goal"} {
		foundRef, foundMount := false, false
		for _, ref := range a.cardRefs {
			if ref.ID == want && ref.Scope == "user" && !ref.Disabled {
				foundRef = true
			}
		}
		for _, m := range a.ComponentMounts {
			if m.CardID == want && m.Enabled && m.Scope == "user" {
				foundMount = true
			}
		}
		if !foundRef || !foundMount {
			t.Fatalf("expected %s mounted in cardRefs (ref=%t) and ComponentMounts (mount=%t)", want, foundRef, foundMount)
		}
	}
}

// TestEnsureGoalModeMounted_NoGoalDoesNothing verifies that an agent without an
// active goal session does not get goal mode or goal bundle.
func TestEnsureGoalModeMounted_NoGoalDoesNothing(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected no change when no active goal")
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" || ref.ID == "builtin:bundle:goal" {
			t.Fatalf("expected no goal cards to be added, got %+v", ref)
		}
	}
}

// TestReloadGoalSession_RestoresModeAndTools verifies that a reloaded session
// with a persisted goal repairs the missing goal mode mount and exposes the
// goal tools from the embedded card assets, with no workspace lookup available.
func TestReloadGoalSession_RestoresModeAndTools(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "implement feature", Confirmed: true, MaxTurns: 20},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to repair the missing goal mount")
	}
	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected restoreComponentMountMetadata to update metadata")
	}

	list, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	var modeMount *domain.AgentComponentMount
	for i := range list.Items {
		if list.Items[i].CardID == "builtin:mode:goal" {
			modeMount = &list.Items[i]
			break
		}
	}
	if modeMount == nil {
		t.Fatalf("goal mode mount missing from handleComponentList: %+v", list.Items)
	}
	if modeMount.Title != "Goal Mode" || modeMount.Icon != "target" {
		t.Fatalf("goal mode mount missing title/icon: %+v", modeMount)
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	hasGoalSubmit, hasGoalCardSubmit, hasTurnAssess := false, false, false
	for _, tool := range snapshot.Tools {
		switch tool.CallableID {
		case "goal_submit":
			hasGoalSubmit = true
		case "goal_card_submit":
			hasGoalCardSubmit = true
		case "turn_assess":
			hasTurnAssess = true
		}
	}
	if !hasGoalSubmit {
		t.Error("component snapshot missing goal_submit tool")
	}
	if !hasGoalCardSubmit {
		t.Error("component snapshot missing goal_card_submit tool")
	}
	if !hasTurnAssess {
		t.Error("component snapshot missing turn_assess tool")
	}
}

// TestGoalTools_ExcludedFromNonGoalMode verifies that goal tools are NOT in the
// component snapshot when goal mode is not mounted. This guards the core
// requirement: normal chat and workflow mode LLM tool schemas must not expose
// goal_submit or goal_card_submit.
func TestGoalTools_ExcludedFromNonGoalMode(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	snapshot := a.resolveComponentSnapshot(ctx)
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "goal_submit" {
			t.Error("goal_submit must not appear in component snapshot without goal mode mounted")
		}
		if tool.CallableID == "goal_card_submit" {
			t.Error("goal_card_submit must not appear in component snapshot without goal mode mounted")
		}
		if tool.CallableID == "turn_assess" {
			t.Error("turn_assess must not appear in component snapshot without goal mode mounted")
		}
	}
}

// TestGoalModeCard_DescriptorDeclaresGoalTools verifies that the embedded
// builtin:mode:goal card descriptor declares goal_submit, goal_card_submit, and
// turn_assess in its tools list.
func TestGoalModeCard_DescriptorDeclaresGoalTools(t *testing.T) {
	descriptor, ok := builtinDescriptorFromAsset("builtin:mode:goal")
	if !ok {
		t.Fatal("builtin:mode:goal descriptor not found")
	}
	got := map[string]bool{}
	for _, tool := range descriptor.Tools {
		got[tool.CallableID] = true
	}
	for _, want := range []string{"turn_assess", "goal_submit", "goal_card_submit"} {
		if !got[want] {
			t.Errorf("builtin:mode:goal descriptor missing tool %q", want)
		}
	}
}

// TestBuildWorkflowBlock_ModeMountedNoActiveWorkflow verifies that when the
// workflow mode is mounted but no workflow is active, buildWorkflowBlock
// returns a guidance block (mirroring the unconfirmed goal block) telling the
// LLM to establish the workflow via workflow_start.
func TestBuildWorkflowBlock_ModeMountedNoActiveWorkflow(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	block := a.buildWorkflowBlock(ctx)
	if block == nil {
		t.Fatal("expected non-nil guidance block when workflow mode mounted and no active workflow")
	}
	if !strings.HasPrefix(block.Text, "## Active Workflow") {
		t.Fatalf("expected block to start with '## Active Workflow', got %q", block.Text)
	}
	if !strings.Contains(block.Text, "workflow_start") {
		t.Fatalf("expected guidance block to mention workflow_start, got %q", block.Text)
	}
	if !strings.Contains(block.Text, "No workflow started yet") {
		t.Fatalf("expected block to state no workflow started, got %q", block.Text)
	}
}

// TestBuildWorkflowBlock_NoModeMountedNoActiveWorkflow verifies that when the
// workflow mode is not mounted and no workflow is active, buildWorkflowBlock
// returns nil (no guidance block).
func TestBuildWorkflowBlock_NoModeMountedNoActiveWorkflow(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if block := a.buildWorkflowBlock(ctx); block != nil {
		t.Fatalf("expected nil block when workflow mode not mounted, got %#v", block)
	}
}

// TestBuildWorkflowBlock_DisabledModeNoActiveWorkflow verifies that a disabled
// workflow mode does not produce the guidance block.
func TestBuildWorkflowBlock_DisabledModeNoActiveWorkflow(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user", Disabled: true},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if block := a.buildWorkflowBlock(ctx); block != nil {
		t.Fatalf("expected nil block when workflow mode is disabled, got %#v", block)
	}
}

// TestResolveHotContext_IncludesWorkflowGuidanceBlock verifies that
// resolveHotContext surfaces the workflow guidance block when the mode is
// mounted but no workflow is active, in the same hot-context position as the
// active goal block.
func TestResolveHotContext_IncludesWorkflowGuidanceBlock(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:workflow", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	blocks := a.resolveHotContext(ctx)
	var found bool
	for _, b := range blocks {
		if strings.HasPrefix(b.Text, "## Active Workflow") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected workflow guidance block in hot context, got %d blocks: %v", len(blocks), blocks)
	}
}

// TestCompileMessages_WorkflowSubmitPrefix verifies that a user step tagged with
// Meta "workflow_submit" gets the workflow-establish prefix prepended to its
// text when no workflow is active, mirroring the goal_submit prefix.
func TestCompileMessages_WorkflowSubmitPrefix(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{
			ID:     "u1",
			Role:   "user",
			Type:   "text",
			TurnID: "turn-1",
			Meta:   "workflow_submit",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "orchestrate the migration"},
			},
		},
	}
	a := newCompileTestActor(turns, nil, steps)
	// Workflow mode mounted but no active workflow — the prefix must apply.
	a.cardRefs = []gen.CardRef{{ID: "builtin:mode:workflow", Scope: "user"}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	msgs := a.compileMessages(true)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected user message, got %s", msgs[0].Role)
	}
	if len(msgs[0].Content) < 1 || msgs[0].Content[0].Type != domain.ContentBlockText {
		t.Fatalf("expected text block, got %+v", msgs[0].Content)
	}
	text := msgs[0].Content[0].Text
	if !strings.HasPrefix(text, "[This message states the workflow intent.") {
		t.Fatalf("expected workflow-start prefix, got %q", text)
	}
	if !strings.Contains(text, "orchestrate the migration") {
		t.Fatalf("expected original text preserved after prefix, got %q", text)
	}
}

// TestCompileMessages_WorkflowSubmitPrefixForUserInjectBeforeWorkflowStart verifies
// that an injected user message is prefixed before workflow start confirmation.
func TestCompileMessages_WorkflowSubmitPrefixForUserInjectBeforeWorkflowStart(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{
			ID:     "u1",
			Role:   "assistant",
			Type:   "user_inject",
			TurnID: "turn-1",
			Meta:   "user",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "continue the migration"},
			},
		},
	}
	a := newCompileTestActor(turns, nil, steps)
	a.cardRefs = []gen.CardRef{{ID: "builtin:mode:workflow", Scope: "user"}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	msgs := a.compileMessages(true)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Fatalf("expected injected message to be remapped to user, got %s", msgs[0].Role)
	}
	text := msgs[0].Content[0].Text
	if !strings.HasPrefix(text, "[This message states the workflow intent.") {
		t.Fatalf("expected workflow-start prefix, got %q", text)
	}
	if !strings.Contains(text, "continue the migration") {
		t.Fatalf("expected original text preserved after prefix, got %q", text)
	}
}

// TestCompileMessages_WorkflowSubmitPrefixNotAppliedWhenActive verifies that the
// workflow-submit prefix is NOT applied once a workflow is active, even if the
// step still carries the workflow_submit meta tag.
func TestCompileMessages_WorkflowSubmitPrefixNotAppliedWhenActive(t *testing.T) {
	turns := []domain.Turn{{ID: "turn-1"}}
	steps := []domain.Step{
		{
			ID:     "u1",
			Role:   "user",
			Type:   "text",
			TurnID: "turn-1",
			Meta:   "workflow_submit",
			Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "orchestrate the migration"},
			},
		},
	}
	a := newCompileTestActor(turns, nil, steps)
	a.cardRefs = []gen.CardRef{{ID: "builtin:mode:workflow", Scope: "user"}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: "map-1"}

	msgs := a.compileMessages(true)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(msgs), msgs)
	}
	text := msgs[0].Content[0].Text
	if strings.HasPrefix(text, "[This message states the workflow intent.") {
		t.Fatalf("prefix must not be applied when a workflow is active, got %q", text)
	}
}
