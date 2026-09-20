package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// workflowStartPlanner dispatches project.wiki_get_card and project.wiki_frontier
// calls used to derive the two-phase workflow_start confirmation goal.
type workflowStartPlanner struct {
	destination string
	frontier    []gen.FrontierTaskCard
	frontierErr error
}

func (w *workflowStartPlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (w *workflowStartPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	var result any
	switch callID {
	case "project.wiki_get_card":
		var req domain.WikiGetCardReq
		switch v := payload.(type) {
		case domain.WikiGetCardReq:
			req = v
		case *domain.WikiGetCardReq:
			req = *v
		case []byte:
			if err := unmarshalJSON(v, &req); err != nil {
				return promise.Reject[any](err)
			}
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		raw := fmt.Sprintf("destination: %s\n", w.destination)
		result = domain.WikiGetCardResp{ID: req.ID, Raw: raw}
	case "project.wiki_frontier":
		if w.frontierErr != nil {
			return promise.Reject[any](w.frontierErr)
		}
		result = domain.WikiFrontierResp{TaskCards: w.frontier}
	default:
		return promise.Reject[any](fmt.Errorf("unexpected call %s", callID))
	}
	return promise.Resolve[any](result)
}

func (w *workflowStartPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

func makeWorkflowStartCtx(t *testing.T, planner actor.Planner) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.wiki_set_map_owner":
			return domain.WikiSetMapOwnerResp{}
		case "project.workflow_create_worktree":
			return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: "wt-owner"}
		case "project.workflow_stop_merge_worktree":
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func TestApplyWorkflowStart_DerivesGoalAndStoresPending(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{
		destination: "Ship the new onboarding flow",
		frontier: []gen.FrontierTaskCard{
			{ID: "task-1", Title: "Design sign-up page", Status: "doing"},
			{ID: "task-2", Title: "Wire API", Status: "todo"},
		},
	})
	a := &Actor{}

	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-onboarding"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	if reqID == "" {
		t.Fatal("expected non-empty request ID")
	}
	if a.workflowActive() {
		t.Fatal("workflow should not be active until confirmed")
	}
	pending := a.RawSession.PendingWorkflowStart
	if pending == nil {
		t.Fatal("expected PendingWorkflowStart to be stored")
	}
	if pending.MapCardID != "map-onboarding" {
		t.Fatalf("expected MapCardID map-onboarding, got %q", pending.MapCardID)
	}
	if pending.RequestID != reqID {
		t.Fatalf("expected RequestID %q, got %q", reqID, pending.RequestID)
	}
	if pending.Destination != "Ship the new onboarding flow" {
		t.Fatalf("expected Destination %q, got %q", "Ship the new onboarding flow", pending.Destination)
	}
	if !strings.Contains(pending.InterpretedGoal, "Ship the new onboarding flow") {
		t.Fatalf("expected InterpretedGoal to contain destination, got %q", pending.InterpretedGoal)
	}
	if !strings.Contains(pending.InterpretedGoal, "task-1") || !strings.Contains(pending.InterpretedGoal, "doing") {
		t.Fatalf("expected InterpretedGoal to contain frontier, got %q", pending.InterpretedGoal)
	}
	if !strings.Contains(pending.FrontierSummary, "task-2 (todo)") {
		t.Fatalf("expected FrontierSummary to list tasks, got %q", pending.FrontierSummary)
	}
	if !a.workflowStartPending || a.workflowStartRequestID != reqID {
		t.Fatal("expected workflowStartPending flags to be set")
	}

	status, err := a.handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if status.PendingWorkflowStart == nil || status.PendingWorkflowStart.MapCardID != "map-onboarding" {
		t.Fatal("expected status to expose PendingWorkflowStart")
	}
}

func TestApplyWorkflowStart_IdempotentSameMap(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	reqID1, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	reqID2, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart second: %v", err)
	}
	if reqID1 != reqID2 {
		t.Fatalf("expected idempotent request ID, got %q then %q", reqID1, reqID2)
	}
}

func TestApplyWorkflowStart_RejectsDifferentPendingMap(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	if _, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"}); err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	_, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-2"})
	if err == nil || !strings.Contains(err.Error(), "already pending") {
		t.Fatalf("expected already-pending error, got %v", err)
	}
}

func TestApplyWorkflowStart_RejectsWhenActiveWorkflowDiffers(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{RawSession: gen.RawSession{ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-active"}}}
	_, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-new"})
	if err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("expected already-active error, got %v", err)
	}
}

func TestResolveWorkflowStart_Approve_ActivatesWorkflow(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{
		cardRefs: []gen.CardRef{{ID: "builtin:bundle:project-wiki", Scope: "builtin"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}

	decision, gotReqID, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected approve decision, got %q", decision)
	}
	if gotReqID != reqID {
		t.Fatalf("expected requestID %q, got %q", reqID, gotReqID)
	}
	if !a.workflowActive() || a.RawSession.ActiveWorkflow.MapCardID != "map-1" {
		t.Fatal("expected workflow active after approval")
	}
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected pending workflow start cleared after approval")
	}
	if a.workflowStartPending {
		t.Fatal("expected workflowStartPending flag cleared")
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode mounted after activation")
	}
}

func TestResolveWorkflowStart_Reject_LeavesWorkflowInactive(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	if _, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"}); err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}

	decision, _, feedback := a.resolveWorkflowStart(ctx, `{"decision":"reject","feedback":"wrong goal"}`)
	if decision != "reject" {
		t.Fatalf("expected reject decision, got %q", decision)
	}
	if !strings.Contains(feedback, "wrong goal") {
		t.Fatalf("expected feedback, got %q", feedback)
	}
	if a.workflowActive() {
		t.Fatal("workflow should remain inactive after rejection")
	}
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected pending workflow start cleared after rejection")
	}
}

type errorParentRef struct {
	id.ActorID
	err error
}

func (r errorParentRef) ID() id.ActorID          { return r.ActorID }
func (r errorParentRef) Service() (string, bool) { return "", false }
func (r errorParentRef) Invoke(_ context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(r.err))
}

func TestResolveWorkflowStart_Approve_ActivationFailureBecomesReject(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	ctx2 := testutil.AnonCtx(testutil.GenActorID())
	ctx2.ParentRef = errorParentRef{ActorID: testutil.GenActorID(), err: fmt.Errorf("owner refused")}
	ctx2.LookupServiceFn = ctx.LookupServiceFn
	ctx2.PlannerFn = ctx.PlannerFn

	a := &Actor{}
	if _, err := a.applyWorkflowStart(ctx2, gen.AgentWorkflowStartReq{MapCardID: "map-1"}); err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	if err := a.activateWorkflow(ctx2, "map-1", false); err == nil {
		t.Fatal("expected activateWorkflow to fail in this test setup")
	}
	decision, _, feedback := a.resolveWorkflowStart(ctx2, `{"decision":"approve"}`)
	if decision != "reject" {
		t.Fatalf("expected reject after activation failure, got %q", decision)
	}
	if !strings.Contains(feedback, "owner refused") {
		t.Fatalf("expected activation failure feedback, got %q", feedback)
	}
	if a.workflowActive() {
		t.Fatal("workflow should not be active after failed activation")
	}
}

func TestEmitWorkflowStartEvent_EmitsGoalSubmitInteraction(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	turnID := "turn-1"
	a.emitWorkflowStartEvent(ctx, turnID, reqID)

	if a.pendingInteraction == nil {
		t.Fatal("expected pendingInteraction recorded")
	}
	if a.pendingInteraction.Type != "workflow_start" {
		t.Fatalf("expected pendingInteraction type workflow_start, got %q", a.pendingInteraction.Type)
	}
	if a.pendingInteraction.RequestID != reqID {
		t.Fatalf("expected pendingInteraction requestID %q, got %q", reqID, a.pendingInteraction.RequestID)
	}
	found := false
	for _, step := range a.steps {
		if step.TurnID == turnID && step.Type == "goal_submit" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected goal_submit interaction step in a.steps")
	}
}

func TestExpireWorkflowStart_ClearsPending(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	a.expireWorkflowStart(ctx, reqID)
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected pending workflow start cleared")
	}
	if a.workflowStartPending {
		t.Fatal("expected workflowStartPending flag cleared")
	}
}

func TestRecoverPendingInteraction_WorkflowStart(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{
		Session: domain.Session{Turns: []domain.Turn{
			{ID: "turn-1", Role: "assistant", State: "running"},
		}},
		RawSession: gen.RawSession{
			PendingWorkflowStart: &gen.PendingWorkflowStart{
				MapCardID: "map-1",
				RequestID: "req-1",
			},
		},
	}
	a.pendingInteraction = &pendingInteractionState{
		TurnID:    "turn-1",
		StepID:    "step-1",
		RequestID: "req-1",
		Type:      "workflow_start",
		Task:      map[string]any{"condition": "Goal"},
	}
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("expected recovery to canonicalize the interaction turn as paused/interaction")
	}
	if !a.workflowStartPending || a.workflowStartRequestID != "req-1" {
		t.Fatal("expected workflowStartPending flags restored")
	}
	if a.status.State != "paused" || a.status.PauseKind != "interaction" {
		t.Fatalf("status = %q/%q, want paused/interaction", a.status.State, a.status.PauseKind)
	}
	for _, turn := range a.Session.Turns {
		if turn.ID == "turn-1" && (turn.State != "paused" || turn.PauseReason != "interaction") {
			t.Fatalf("turn-1 not canonicalized to paused/interaction: %+v", turn)
		}
	}
}

func TestRecoverPendingInteraction_WorkflowStartStaleClears(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	a.pendingInteraction = &pendingInteractionState{
		RequestID: "req-1",
		Type:      "workflow_start",
	}
	if !a.recoverPendingInteraction(ctx) {
		t.Fatal("expected stale workflow_start interaction to be cleared")
	}
	if a.pendingInteraction != nil {
		t.Fatal("expected pendingInteraction cleared")
	}
}

func TestHandleTurnAnswer_ResolvesWorkflowStartAfterRestart(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{
		cardRefs: []gen.CardRef{{ID: "builtin:bundle:project-wiki", Scope: "builtin"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	// Simulate restart: active turn ref is gone, but pending interaction remains.
	a.ActiveTurnRef = ""
	a.pendingInteraction = &pendingInteractionState{
		TurnID:    "turn-1",
		StepID:    "step-1",
		RequestID: reqID,
		Type:      "workflow_start",
	}

	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{RequestID: reqID, AnswersJSON: `{"decision":"approve"}`}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if !a.workflowActive() || a.RawSession.ActiveWorkflow.MapCardID != "map-1" {
		t.Fatal("expected workflow active after restart-path approval")
	}
	if a.pendingInteraction != nil {
		t.Fatal("expected pendingInteraction cleared")
	}
}

func TestHandleTurnAnswer_RejectWorkflowStartAfterRestart(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{}
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	a.ActiveTurnRef = ""
	a.pendingInteraction = &pendingInteractionState{
		RequestID: reqID,
		Type:      "workflow_start",
	}
	if err := a.handleTurnAnswer(ctx, domain.TurnAnswerReq{RequestID: reqID, AnswersJSON: `{"decision":"reject"}`}); err != nil {
		t.Fatalf("handleTurnAnswer: %v", err)
	}
	if a.workflowActive() {
		t.Fatal("workflow should remain inactive after restart-path rejection")
	}
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected pending workflow start cleared")
	}
}

// TestWorkflowStart_RoundTripSurvivesRestart reproduces the reported bug: when
// the program is closed while the agent is blocked on the workflow_start
// confirmation, a restart must restore the confirmation state. It models the
// real in-flight scenario where the blocked assistant turn has NOT yet been
// appended to Session.Turns (that only happens at turn completion) and runs the
// full OnStart recovery sequence, including recoverOrphanTurnSteps.
func TestWorkflowStart_RoundTripSurvivesRestart(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())

	actorID := "019f30e43afd00000000000000000003"
	actorDir := filepath.Join(config.ActorDataDir(), "agent", actorID)
	if err := os.MkdirAll(filepath.Join(actorDir, "turns"), 0755); err != nil {
		t.Fatalf("mkdir turns: %v", err)
	}

	src := &Actor{actorID: actorID, snapshotReady: true}
	// Real in-flight state: only the user turn is in Session.Turns. The blocked
	// assistant turn "t2" is tracked in memory (ActiveTurnRef) and not persisted
	// as a Session.Turns entry until the turn completes.
	src.Session = domain.Session{Turns: []domain.Turn{
		{ID: "t1", Role: "user", Seq: 1, State: "completed"},
	}}
	src.RawSession.PendingWorkflowStart = &gen.PendingWorkflowStart{
		MapCardID:       "map-1",
		RequestID:       "r1",
		Destination:     "Goal",
		InterpretedGoal: "Start workflow map map-1",
	}
	src.steps = []domain.Step{
		{ID: "s1", TurnID: "t1", Role: "user", Type: "text", Seq: 1, Closed: true,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "orchestrate the migration"}}},
		{ID: "s2", TurnID: "t2", Role: "assistant", Type: "goal_submit", Seq: 2,
			Closed: false, ContentStatus: "running", InteractionStatus: "pending",
			RequestID: "r1",
			Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: `{"condition":"Goal"}`}}},
	}
	src.pendingInteraction = &pendingInteractionState{
		TurnID:    "t2",
		StepID:    "s2",
		RequestID: "r1",
		Type:      "workflow_start",
		Task:      map[string]any{"condition": "Goal"},
	}
	src.status = turnStatus{TurnID: "t2", State: "running"}
	src.takeSnapshot()
	src.saveMailbox(nil)
	if err := src.saveTurns(); err != nil {
		t.Fatalf("saveTurns: %v", err)
	}

	// Full OnStart recovery sequence (agent.go OnStart ordering).
	reloaded := &Actor{actorID: actorID, snapshotReady: true}
	reloaded.loadMailbox(nil)
	reloaded.rebuildSteps()
	reloaded.closeOpenStepsForTerminalTurns()
	reloaded.recoverOrphanTurnSteps()
	reloaded.recoverTurnStatus()
	if reloaded.recoverPendingInteraction(nil) {
		reloaded.saveMailbox(nil)
	}
	reloaded.takeSnapshot()

	if !reloaded.workflowStartPending {
		t.Errorf("workflowStartPending = false, want true (confirmation lost on restart)")
	}
	if reloaded.workflowStartRequestID != "r1" {
		t.Errorf("workflowStartRequestID = %q, want r1", reloaded.workflowStartRequestID)
	}
	if reloaded.status.State != "paused" {
		t.Errorf("status.State = %q, want paused (interaction wait)", reloaded.status.State)
	}
	if reloaded.status.PauseKind != "interaction" {
		t.Errorf("status.PauseKind = %q, want interaction", reloaded.status.PauseKind)
	}
	if reloaded.RawSession.PendingWorkflowStart == nil {
		t.Errorf("PendingWorkflowStart = nil, want preserved across restart")
	}
	if reloaded.pendingInteraction == nil {
		t.Errorf("pendingInteraction = nil, want preserved (drives the answer path)")
	}
	var step *domain.Step
	for i := range reloaded.steps {
		if reloaded.steps[i].ID == "s2" {
			step = &reloaded.steps[i]
			break
		}
	}
	if step == nil {
		t.Fatalf("interaction step s2 missing after reload")
	}
	if step.Closed {
		t.Errorf("interaction step s2 closed; want open (still pending)")
	}

	// The frontend reconstructs the confirmation UI from the steps returned by
	// session.summary. Verify the pending workflow_start step is actually
	// returned (not dropped by the MaxTurns window or the recentTurnIDs filter).
	summary, err := reloaded.handleSessionSummaryPure(nil, domain.AgentSessionSummaryReq{})
	if err != nil {
		t.Fatalf("session.summary after reload: %v", err)
	}
	var summaryStep *domain.Step
	for i := range summary.Steps {
		if summary.Steps[i].ID == "s2" {
			summaryStep = &summary.Steps[i]
			break
		}
	}
	if summaryStep == nil {
		var ids []string
		for _, s := range summary.Steps {
			ids = append(ids, s.ID)
		}
		t.Fatalf("pending workflow_start step s2 missing from session.summary; got steps=%v", ids)
	}
	if summaryStep.InteractionStatus != "pending" {
		t.Errorf("summary step s2 InteractionStatus = %q, want pending", summaryStep.InteractionStatus)
	}
	if summary.AgentState != "paused" {
		t.Errorf("summary AgentState = %q, want paused", summary.AgentState)
	}
}

// TestHandleTurnResume_RejectsWithPendingWorkflowStart verifies that a user
// cannot bypass a pending workflow_start confirmation by clicking "Resume" on
// the paused turn after a restart. The turn must stay blocked until the user
// answers the interaction (approve / reject).
func TestHandleTurnResume_RejectsWithPendingWorkflowStart(t *testing.T) {
	ctx := makeWorkflowStartCtx(t, &workflowStartPlanner{destination: "Goal"})
	a := &Actor{
		ActiveTurnRef: "turn-blocked",
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "turn-blocked", Role: "assistant", State: "paused"},
			},
		},
	}
	reqID, err := a.applyWorkflowStart(ctx, gen.AgentWorkflowStartReq{MapCardID: "map-1"})
	if err != nil {
		t.Fatalf("applyWorkflowStart: %v", err)
	}
	a.pendingInteraction = &pendingInteractionState{
		TurnID:    "turn-blocked",
		RequestID: reqID,
		Type:      "workflow_start",
	}

	err = a.handleTurnResume(ctx)
	if err == nil {
		t.Fatal("expected handleTurnResume to reject when a pending workflow_start interaction exists")
	}
	if a.workflowActive() {
		t.Fatal("workflow must not activate via resume")
	}
	if a.RawSession.PendingWorkflowStart == nil {
		t.Fatal("pending workflow start must survive the rejected resume")
	}
}

func TestPlanGoalText(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want string
	}{
		{name: "goal section", plan: "# Plan\n\n## Goal\n\nShip it.\n\n## Approach\n\nDo it.", want: "Ship it."},
		{name: "case insensitive", plan: "# GOAL\n\nUse the title.", want: "Use the title."},
		{name: "missing", plan: "# Plan\n\nNo goal.", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planGoalText(tt.plan); got != tt.want {
				t.Fatalf("planGoalText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// planWorkflowPlanner handles project.wiki_create_map for the
// plan-to-workflow conversion test.
type planWorkflowPlanner struct {
	mapID        string
	createMapReq domain.WikiCreateMapReq
}

func (p *planWorkflowPlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (p *planWorkflowPlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	switch callID {
	case "project.wiki_create_map":
		req, ok := payload.(domain.WikiCreateMapReq)
		if !ok {
			return promise.Reject[any](fmt.Errorf("unexpected map request type %T", payload))
		}
		p.createMapReq = req
		return promise.Resolve[any](domain.WikiCreateMapResp{
			Card: gen.MonoCardListItem{ID: p.mapID},
		})
	default:
		return promise.Reject[any](fmt.Errorf("unexpected call %s", callID))
	}
}

func (p *planWorkflowPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

func TestResolvePlanApproval_StartWorkflow_CreatesMapAndActivates(t *testing.T) {
	planner := &planWorkflowPlanner{mapID: "workflow-map-1"}
	ctx := makeWorkflowStartCtx(t, planner)
	a := &Actor{
		actorID:   "agent-1",
		agentKind: "coder",
		plan: planState{
			Status:       "pending_approval",
			RequestID:    "req-1",
			Title:        "Add Retry Logic",
			PlanCardID:   "plan-card-1",
			Plan:         "## Goal\n\nAdd retry logic to the HTTP client.",
			PendingTasks: []gen.PlanTaskRef{{ID: "t1", Subject: "step 1", Status: "pending"}},
		},
		cardRefs: []gen.CardRef{{ID: "builtin:bundle:project-wiki", Scope: "builtin"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	answer := `{"decision":"start_workflow"}`
	decision, tasks, requestID, _ := a.resolvePlanApproval(ctx, answer)

	if decision != "start_workflow" {
		t.Fatalf("decision = %q, want start_workflow", decision)
	}
	if requestID != "req-1" {
		t.Fatalf("requestID = %q, want req-1", requestID)
	}
	if a.plan.Status != "approved" {
		t.Errorf("plan.Status = %q, want approved", a.plan.Status)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks = %+v, want none (start_workflow must not create session tasks)", tasks)
	}
	if len(a.plan.ApprovedTasks) != 0 {
		t.Errorf("ApprovedTasks = %+v, want none", a.plan.ApprovedTasks)
	}
	if len(a.plan.PendingTasks) != 1 || a.plan.PendingTasks[0].ID != "t1" {
		t.Errorf("PendingTasks = %+v, want preserved task t1 for task-card creation", a.plan.PendingTasks)
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow to be active after start_workflow")
	}
	if a.RawSession.ActiveWorkflow.MapCardID != "workflow-map-1" {
		t.Fatalf("ActiveWorkflow.MapCardID = %q, want workflow-map-1", a.RawSession.ActiveWorkflow.MapCardID)
	}
	if !hasMountedCard(a, "builtin:mode:workflow") {
		t.Fatal("expected workflow mode mounted after activation")
	}
	if planner.createMapReq.Destination != "Add retry logic to the HTTP client." {
		t.Fatalf("map destination = %q, want extracted Goal", planner.createMapReq.Destination)
	}
	if !strings.Contains(planner.createMapReq.Notes, "## Goal") {
		t.Fatal("map notes must preserve the plan body")
	}
	if !strings.Contains(planner.createMapReq.Notes, "[[plan-card-1]]") {
		t.Fatalf("map notes = %q, want plan WikiWord link", planner.createMapReq.Notes)
	}
}

// unmarshalJSON is a small helper to avoid importing encoding/json in the test
// for the planner payload switch (already imported in the package).
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
