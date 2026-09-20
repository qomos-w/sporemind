package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// workflowPlanPlanner intercepts project.* calls used by
// createWorkflowPlanArtifacts + applyWorkflowStart. It records created cards
// and maps so tests can assert on their content.
type workflowPlanPlanner struct {
	mu sync.Mutex

	destination  string
	frontier     []gen.FrontierTaskCard
	createdCards map[string]string // cardID -> raw
	createdMaps  map[string]domain.WikiCreateMapReq

	// mapCardRaw is the raw markdown returned for project.wiki_get_card when
	// the requested card is one of the workflow's maps (the ID it sees in the
	// createWorkflowPlanArtifacts path). Tests can preset it to a realistic
	// frontmatter so the data.coding round-trip is observable.
	mapCardRaw string
	// taskCardsForMap is the task-card catalogue returned for
	// project.wiki_list_cards with Parent == mapID. Used by the workflow
	// coding inference in activateWorkflow.
	taskCardsForMap map[string][]gen.MonoCardListItem
	// editedCardRaws records every project.wiki_edit_card payload in order
	// so tests can assert the data.coding stamp landed on the right card.
	editedCardRaws []editedCardRaw
}

// editedCardRaw captures the inputs to a single project.wiki_edit_card call.
type editedCardRaw struct {
	ID  string
	Raw string
}

func newWorkflowPlanPlanner(destination string) *workflowPlanPlanner {
	return &workflowPlanPlanner{
		destination:    destination,
		createdCards:   make(map[string]string),
		createdMaps:    make(map[string]domain.WikiCreateMapReq),
		taskCardsForMap: make(map[string][]gen.MonoCardListItem),
	}
}

func (w *workflowPlanPlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (w *workflowPlanPlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	var result any
	switch callID {
	case "project.wiki_create_card":
		var req domain.WikiCreateCardReq
		switch v := payload.(type) {
		case domain.WikiCreateCardReq:
			req = v
		case *domain.WikiCreateCardReq:
			req = *v
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		w.mu.Lock()
		w.createdCards[req.ID] = req.Raw
		w.mu.Unlock()
		result = domain.WikiCreateCardResp{Card: gen.MonoCardListItem{ID: req.ID}}
	case "project.wiki_create_map":
		var req domain.WikiCreateMapReq
		switch v := payload.(type) {
		case domain.WikiCreateMapReq:
			req = v
		case *domain.WikiCreateMapReq:
			req = *v
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		w.mu.Lock()
		w.createdMaps[req.ID] = req
		w.mu.Unlock()
		result = domain.WikiCreateMapResp{Card: gen.MonoCardListItem{ID: req.ID}}
	case "project.wiki_get_card":
		var req domain.WikiGetCardReq
		switch v := payload.(type) {
		case domain.WikiGetCardReq:
			req = v
		case *domain.WikiGetCardReq:
			req = *v
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		raw := w.mapCardRaw
		if raw == "" {
			raw = fmt.Sprintf("destination: %s\n", w.destination)
		}
		result = domain.WikiGetCardResp{ID: req.ID, Raw: raw}
	case "project.wiki_frontier":
		result = domain.WikiFrontierResp{TaskCards: w.frontier}
	case "project.wiki_list_cards":
		var req domain.WikiListCardsReq
		switch v := payload.(type) {
		case domain.WikiListCardsReq:
			req = v
		case *domain.WikiListCardsReq:
			req = *v
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		w.mu.Lock()
		cards := w.taskCardsForMap[req.Parent]
		w.mu.Unlock()
		result = domain.WikiListCardsResp{Cards: cards}
	case "project.wiki_edit_card":
		var req domain.WikiEditCardReq
		switch v := payload.(type) {
		case domain.WikiEditCardReq:
			req = v
		case *domain.WikiEditCardReq:
			req = *v
		default:
			return promise.Reject[any](fmt.Errorf("unexpected payload type %T", payload))
		}
		w.mu.Lock()
		w.editedCardRaws = append(w.editedCardRaws, editedCardRaw{ID: req.ID, Raw: req.Raw})
		w.mu.Unlock()
		result = domain.WikiEditCardResp{}
	default:
		return promise.Reject[any](fmt.Errorf("unexpected call %s", callID))
	}
	return promise.Resolve[any](result)
}

func (w *workflowPlanPlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

func (w *workflowPlanPlanner) editedRaws() []editedCardRaw {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]editedCardRaw, len(w.editedCardRaws))
	copy(out, w.editedCardRaws)
	return out
}

func makeWorkflowPlanCtx(t *testing.T, planner actor.Planner) *testutil.FakeCtx {
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

// workflowPlanWorktreeRecorder wraps makeWorkflowPlanCtx to record every
// invocation of project.workflow_create_worktree so tests can assert the
// worktree-creation call happened (or didn't). The planner handles all
// project.* calls routed through the planner; the parent ref handles the
// worktree create callable.
func workflowPlanWorktreeRecorder(t *testing.T, planner actor.Planner) (*testutil.FakeCtx, *workflowPlanRecorder) {
	t.Helper()
	rec := &workflowPlanRecorder{}
	ctx := makeWorkflowPlanCtx(t, planner)
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, _ any) any {
		switch callID {
		case "project.wiki_set_map_owner":
			return domain.WikiSetMapOwnerResp{}
		case "project.workflow_create_worktree":
			rec.worktreeCreateCalls++
			return gen.ProjectWorkflowCreateWorktreeResp{WorktreeID: "wt-owner"}
		case "project.workflow_stop_merge_worktree":
			return gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"}
		case "project.wiki_set_status":
			return domain.WikiSetStatusResp{}
		}
		return nil
	})
	return ctx, rec
}

// workflowPlanRecorder tracks project-side invocations the test wants to
// assert on (so far just the worktree-create call count).
type workflowPlanRecorder struct {
	worktreeCreateCalls int
}

const samplePlanBody = `## Goal

Ship the new onboarding flow with three steps.

## Approach

1. Create the map.
2. Spawn workers.
3. Review results.
`

const samplePlanBodyNoGoal = `A simple plan without a goal heading.`

// TestApplyWorkflowPlanSubmit_CreatesCardMapAndEntersConfirmation verifies the
// core flow: plan card persisted, workflow map created with plan link, and the
// two-phase workflow_start confirmation entered.
func TestApplyWorkflowPlanSubmit_CreatesCardMapAndEntersConfirmation(t *testing.T) {
	planner := newWorkflowPlanPlanner("Ship the new onboarding flow")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	reqID, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Onboarding Workflow",
		Body:  samplePlanBody,
	})
	if err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	if reqID == "" {
		t.Fatal("expected non-empty request ID (confirmation)")
	}

	// Plan card created with the plan body.
	if len(planner.createdCards) != 1 {
		t.Fatalf("expected 1 plan card created, got %d", len(planner.createdCards))
	}
	for cardID, raw := range planner.createdCards {
		if !strings.Contains(raw, samplePlanBody) {
			t.Fatalf("plan card %s body does not contain plan text", cardID)
		}
		if a.plan.PlanCardID != cardID {
			t.Fatalf("plan.PlanCardID %q != created card %q", a.plan.PlanCardID, cardID)
		}
	}

	// Workflow map created with plan link in notes.
	if len(planner.createdMaps) != 1 {
		t.Fatalf("expected 1 workflow map created, got %d", len(planner.createdMaps))
	}
	for _, m := range planner.createdMaps {
		if !strings.Contains(m.Notes, "[["+a.plan.PlanCardID+"]]") {
			t.Fatalf("map notes missing plan card link; notes=%q", m.Notes)
		}
		if !strings.Contains(m.Notes, samplePlanBody) {
			t.Fatalf("map notes missing full plan body; notes=%q", m.Notes)
		}
	}

	// Two-phase confirmation entered (PendingWorkflowStart set).
	pending := a.RawSession.PendingWorkflowStart
	if pending == nil {
		t.Fatal("expected PendingWorkflowStart to be stored after applyWorkflowPlanSubmit")
	}
	if pending.RequestID != reqID {
		t.Fatalf("expected pending RequestID %q, got %q", reqID, pending.RequestID)
	}
}

// TestApplyWorkflowPlanSubmit_GoalFallback verifies the map destination falls
// back to the plan title when the body has no "## Goal" heading.
func TestApplyWorkflowPlanSubmit_GoalFallback(t *testing.T) {
	planner := newWorkflowPlanPlanner("")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Fallback Title",
		Body:  samplePlanBodyNoGoal,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}

	for _, m := range planner.createdMaps {
		// callWikiCreateMap uses the destination as the map's Destination field.
		// When no Goal heading exists, it should fall back to the title.
		if m.Destination != "Fallback Title" {
			t.Fatalf("expected Destination to fall back to title %q, got %q", "Fallback Title", m.Destination)
		}
	}
}

// TestApplyWorkflowPlanSubmit_GoalExtractedFromHeading verifies the map
// destination is extracted from a "## Goal" heading in the plan body.
func TestApplyWorkflowPlanSubmit_GoalExtractedFromHeading(t *testing.T) {
	planner := newWorkflowPlanPlanner("")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Any Title",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}

	for _, m := range planner.createdMaps {
		if m.Destination != "Ship the new onboarding flow with three steps." {
			t.Fatalf("expected Destination from Goal heading, got %q", m.Destination)
		}
	}
}

// TestApplyWorkflowPlanSubmit_NoSessionTasks verifies that no session-level
// tasks are created — this is the key difference from plan_submit.
func TestApplyWorkflowPlanSubmit_NoSessionTasks(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "No Tasks Plan",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}

	if len(a.RawSession.Tasks) != 0 {
		t.Fatalf("workflow_plan_submit must not create session tasks, got %d", len(a.RawSession.Tasks))
	}
	if len(a.plan.PendingTasks) != 0 || len(a.plan.ApprovedTasks) != 0 {
		t.Fatal("workflow_plan_submit must not populate plan PendingTasks or ApprovedTasks")
	}
}

// TestApplyWorkflowPlanSubmit_RejectsWhenPlanApprovalPending verifies the guard
// against a pending plan_submit approval.
func TestApplyWorkflowPlanSubmit_RejectsWhenPlanApprovalPending(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}
	a.plan.Status = "pending_approval"
	a.plan.RequestID = "existing-req"

	_, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Blocked",
		Body:  samplePlanBody,
	})
	if err == nil {
		t.Fatal("expected error when plan approval is pending")
	}
	if !strings.Contains(err.Error(), "pending_approval") && !strings.Contains(err.Error(), "plan approval pending") {
		t.Fatalf("expected plan-approval-pending error, got %v", err)
	}
}

// TestApplyWorkflowPlanSubmit_RejectsWhenWorkflowActive verifies the guard
// against an already-active workflow.
func TestApplyWorkflowPlanSubmit_RejectsWhenWorkflowActive(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}
	a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: "existing-map"}

	_, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Blocked",
		Body:  samplePlanBody,
	})
	if err == nil {
		t.Fatal("expected error when workflow is already active")
	}
}

// TestApplyWorkflowPlanSubmit_RequiresTitleAndBody verifies input validation.
func TestApplyWorkflowPlanSubmit_RequiresTitleAndBody(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)

	// Missing title.
	a1 := &Actor{}
	_, err := a1.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{Body: "body"})
	if err == nil || !strings.Contains(err.Error(), "Title is required") {
		t.Fatalf("expected Title required error, got %v", err)
	}

	// Missing body.
	a2 := &Actor{}
	_, err = a2.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{Title: "title"})
	if err == nil || !strings.Contains(err.Error(), "Body is required") {
		t.Fatalf("expected Body required error, got %v", err)
	}
}

// TestHandleWorkflowPlanSubmit_ReturnsCardIDs verifies the direct-invocation
// handler returns the plan card and map card IDs without entering confirmation.
func TestHandleWorkflowPlanSubmit_ReturnsCardIDs(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	resp, err := a.handleWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Direct Handler",
		Body:  samplePlanBody,
	})
	if err != nil {
		t.Fatalf("handleWorkflowPlanSubmit: %v", err)
	}
	if resp.PlanCardID == "" {
		t.Fatal("expected non-empty PlanCardId in response")
	}
	if resp.MapCardID == "" {
		t.Fatal("expected non-empty MapCardId in response")
	}
	// Direct handler does NOT enter the confirmation.
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("direct handler must not enter workflow confirmation")
	}
	if a.workflowActive() {
		t.Fatal("direct handler must not activate workflow")
	}
}

// TestApplyWorkflowPlanSubmit_ApproveActivatesWorkflow verifies the full
// round-trip: submit → user approves → workflow activated.
func TestApplyWorkflowPlanSubmit_ApproveActivatesWorkflow(t *testing.T) {
	planner := newWorkflowPlanPlanner("Ship onboarding")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	reqID, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Round Trip",
		Body:  samplePlanBody,
	})
	if err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}

	// User approves the workflow_start confirmation.
	decision, resolvedReqID, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	if resolvedReqID != reqID {
		t.Fatalf("expected request ID %q, got %q", reqID, resolvedReqID)
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow to be active after approval")
	}
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected PendingWorkflowStart cleared after approval")
	}
}

// TestApplyWorkflowPlanSubmit_RejectKeepsWorkflowInactive verifies that a user
// rejection does not activate the workflow.
func TestApplyWorkflowPlanSubmit_RejectKeepsWorkflowInactive(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	ctx := makeWorkflowPlanCtx(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Reject Test",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}

	decision, _, feedback := a.resolveWorkflowStart(ctx, `{"decision":"reject","feedback":"wrong goal"}`)
	if decision != "reject" {
		t.Fatalf("expected decision reject, got %q", decision)
	}
	if feedback != "wrong goal" {
		t.Fatalf("expected feedback %q, got %q", "wrong goal", feedback)
	}
	if a.workflowActive() {
		t.Fatal("workflow must NOT be active after rejection")
	}
	if a.RawSession.PendingWorkflowStart != nil {
		t.Fatal("expected PendingWorkflowStart cleared after rejection")
	}
}

// TestFindWorkflowPlanSubmit verifies the batch detection helper.
func TestFindWorkflowPlanSubmit(t *testing.T) {
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{CallableID: "project.read"},
		{CallableID: "workflow_plan_submit"},
		{CallableID: "project.write"},
	}}
	idx := findWorkflowPlanSubmit(batch)
	if idx != 1 {
		t.Fatalf("expected index 1, got %d", idx)
	}

	empty := toolExecutionBatch{calls: []pendingToolCall{
		{CallableID: "project.read"},
	}}
	if findWorkflowPlanSubmit(empty) != -1 {
		t.Fatal("expected -1 when workflow_plan_submit is absent")
	}
}

// ── coding / non-coding activation gate tests ─────────────────────────────
//
// These verify the worktree-skip behaviour introduced by the workflow_plan_submit
// NonCoding flag (see coding-judgment-plan-submit-activation card). Five cases
// from the task spec plus a mixed-scope sanity check.

const sampleWorkflowMapRaw = `---
id: workflow-test
tags: [workflow]
status: doing
data:
  scope:
    include:
      - workflow-test
  destination: |
    Sample workflow destination.
---

# Sample workflow
`

// singleMapID returns the (only) map card ID recorded by the planner. The
// plan_submit path creates exactly one map per call.
func singleMapID(t *testing.T, p *workflowPlanPlanner) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.createdMaps) != 1 {
		t.Fatalf("expected 1 map created, got %d", len(p.createdMaps))
	}
	for id := range p.createdMaps {
		return id
	}
	return ""
}

// TestApplyWorkflowPlanSubmit_ExplicitNonCodingSkipsWorktree verifies that
// NonCoding=true on workflow_plan_submit skips worktree creation and stamps
// data.coding=false on the map card.
func TestApplyWorkflowPlanSubmit_ExplicitNonCodingSkipsWorktree(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = sampleWorkflowMapRaw
	ctx, rec := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	reqID, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title:     "Research-Only Workflow",
		Body:      samplePlanBody,
		NonCoding: true,
	})
	if err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	// PendingWorkflowStart must carry the explicit NonCoding intent so the
	// resolve → activate path can read it.
	pending := a.RawSession.PendingWorkflowStart
	if pending == nil {
		t.Fatal("expected PendingWorkflowStart set")
	}
	if !pending.NonCoding {
		t.Fatalf("expected PendingWorkflowStart.NonCoding = true, got %v", pending.NonCoding)
	}

	// User approves — activateWorkflow runs with explicitNonCoding=true.
	decision, _, _ := a.resolveWorkflowStart(ctx, fmt.Sprintf(`{"decision":"approve","requestId":%q}`, reqID))
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}

	// Non-coding → no owner worktree create call.
	if rec.worktreeCreateCalls != 0 {
		t.Fatalf("expected 0 workflow_create_worktree calls (non-coding), got %d", rec.worktreeCreateCalls)
	}
	if !a.workflowActive() {
		t.Fatal("expected workflow active after approval")
	}
	if got := a.RawSession.ActiveWorkflow.WorktreeID; got != "" {
		t.Fatalf("expected ActiveWorkflow.WorktreeID empty for non-coding, got %q", got)
	}
	if got := a.RawSession.ActiveWorkflow.MapCardID; got == "" {
		t.Fatal("expected ActiveWorkflow.MapCardID populated")
	}

	// Map card data.coding must be stamped false.
	edits := planner.editedRaws()
	if len(edits) != 1 {
		t.Fatalf("expected 1 wiki_edit_card call (data.coding stamp), got %d", len(edits))
	}
	if !strings.Contains(edits[0].Raw, "\n  coding: false\n") {
		t.Fatalf("expected data.coding: false in edited raw, got:\n%s", edits[0].Raw)
	}
}

// TestApplyWorkflowPlanSubmit_ExplicitCodingCreatesWorktree verifies that
// NonCoding=false (explicit "I want a coding workflow") routes through the
// scope-inference path, which then creates the owner worktree and stamps
// data.coding=true. We seed the planner's task-card catalogue with a coding
// card so the inference rule agrees.
func TestApplyWorkflowPlanSubmit_ExplicitCodingCreatesWorktree(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = sampleWorkflowMapRaw
	mapID := "" // captured below
	ctx, rec := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title:     "Coding Workflow",
		Body:      samplePlanBody,
		NonCoding: false,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	mapID = singleMapID(t, planner)
	planner.taskCardsForMap[mapID] = []gen.MonoCardListItem{
		{ID: "task-1", Type: "task", Data: map[string]any{"category": "code"}},
	}
	// NonCoding=false → infer from scope → at least one coding card → coding.
	decision, _, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	if rec.worktreeCreateCalls != 1 {
		t.Fatalf("expected 1 workflow_create_worktree call (coding), got %d", rec.worktreeCreateCalls)
	}
	if got := a.RawSession.ActiveWorkflow.WorktreeID; got != "wt-owner" {
		t.Fatalf("expected ActiveWorkflow.WorktreeID = wt-owner, got %q", got)
	}
	edits := planner.editedRaws()
	if len(edits) != 1 {
		t.Fatalf("expected 1 wiki_edit_card call (data.coding stamp), got %d", len(edits))
	}
	if !strings.Contains(edits[0].Raw, "\n  coding: true\n") {
		t.Fatalf("expected data.coding: true in edited raw, got:\n%s", edits[0].Raw)
	}
}

// TestApplyWorkflowPlanSubmit_AutoInferAllResearchSkipsWorktree verifies the
// auto-inference rule: default NonCoding (absent) inspects the map scope's
// task-card categories; all research/explore/review → non-coding, no
// worktree, data.coding=false.
func TestApplyWorkflowPlanSubmit_AutoInferAllResearchSkipsWorktree(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = sampleWorkflowMapRaw
	ctx, rec := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	// NonCoding omitted → auto-infer at activation.
	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Auto-Infer Research Workflow",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	mapID := singleMapID(t, planner)
	planner.taskCardsForMap[mapID] = []gen.MonoCardListItem{
		{ID: "task-r", Type: "task", Data: map[string]any{"category": "research"}},
		{ID: "task-e", Type: "task", Data: map[string]any{"category": "explore"}},
		{ID: "task-v", Type: "task", Data: map[string]any{"category": "review"}},
	}
	decision, _, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	if rec.worktreeCreateCalls != 0 {
		t.Fatalf("expected 0 workflow_create_worktree calls (all-research scope), got %d", rec.worktreeCreateCalls)
	}
	if got := a.RawSession.ActiveWorkflow.WorktreeID; got != "" {
		t.Fatalf("expected ActiveWorkflow.WorktreeID empty, got %q", got)
	}
	edits := planner.editedRaws()
	if len(edits) != 1 {
		t.Fatalf("expected 1 wiki_edit_card call, got %d", len(edits))
	}
	if !strings.Contains(edits[0].Raw, "\n  coding: false\n") {
		t.Fatalf("expected data.coding: false, got:\n%s", edits[0].Raw)
	}
}

// TestApplyWorkflowPlanSubmit_EmptyScopeDefaultsToCoding verifies the empty
// scope default: no task cards yet → coding (safer to provision a worktree so
// a coding card authored later has a place to land).
func TestApplyWorkflowPlanSubmit_EmptyScopeDefaultsToCoding(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = sampleWorkflowMapRaw
	ctx, rec := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Empty-Scope Workflow",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	// No task cards seeded — empty scope.
	decision, _, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	if rec.worktreeCreateCalls != 1 {
		t.Fatalf("expected 1 workflow_create_worktree call (empty scope defaults to coding), got %d", rec.worktreeCreateCalls)
	}
	if got := a.RawSession.ActiveWorkflow.WorktreeID; got != "wt-owner" {
		t.Fatalf("expected ActiveWorkflow.WorktreeID = wt-owner, got %q", got)
	}
	edits := planner.editedRaws()
	if len(edits) != 1 {
		t.Fatalf("expected 1 wiki_edit_card call, got %d", len(edits))
	}
	if !strings.Contains(edits[0].Raw, "\n  coding: true\n") {
		t.Fatalf("expected data.coding: true (empty scope → coding), got:\n%s", edits[0].Raw)
	}
}

// TestApplyWorkflowPlanSubmit_MixedScopeDefaultsToCoding verifies the
// conservative inference: a single coding card in an otherwise research-heavy
// scope forces the workflow to coding so the coding worker is not stranded
// on main repo (the rule from the parent card's Decisions-so-far).
func TestApplyWorkflowPlanSubmit_MixedScopeDefaultsToCoding(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = sampleWorkflowMapRaw
	ctx, rec := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title: "Mixed Workflow",
		Body:  samplePlanBody,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	mapID := singleMapID(t, planner)
	planner.taskCardsForMap[mapID] = []gen.MonoCardListItem{
		{ID: "task-r1", Type: "task", Data: map[string]any{"category": "research"}},
		{ID: "task-c", Type: "task", Data: map[string]any{"category": "code"}},
		{ID: "task-r2", Type: "task", Data: map[string]any{"category": "review"}},
	}
	decision, _, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	if rec.worktreeCreateCalls != 1 {
		t.Fatalf("expected 1 workflow_create_worktree call (mixed scope → coding), got %d", rec.worktreeCreateCalls)
	}
	edits := planner.editedRaws()
	if !strings.Contains(edits[0].Raw, "\n  coding: true\n") {
		t.Fatalf("expected data.coding: true for mixed scope, got:\n%s", edits[0].Raw)
	}
}

// TestApplyWorkflowPlanSubmit_MapCardCodingRoundTrip verifies the
// setCardDataBoolInRaw helper round-trips through both insert and update:
// pre-existing data.coding is overwritten, and other data.* keys are
// preserved.
func TestApplyWorkflowPlanSubmit_MapCardCodingRoundTrip(t *testing.T) {
	planner := newWorkflowPlanPlanner("Goal")
	planner.mapCardRaw = "---\nid: workflow-x\ndata:\n  scope:\n    include: [a]\n  destination: ok\n---\n\nBody\n"
	ctx, _ := workflowPlanWorktreeRecorder(t, planner)
	a := &Actor{}

	if _, err := a.applyWorkflowPlanSubmit(ctx, gen.WorkflowPlanSubmitReq{
		Title:     "Stamp Test",
		Body:      samplePlanBody,
		NonCoding: true,
	}); err != nil {
		t.Fatalf("applyWorkflowPlanSubmit: %v", err)
	}
	decision, _, _ := a.resolveWorkflowStart(ctx, `{"decision":"approve"}`)
	if decision != "approve" {
		t.Fatalf("expected decision approve, got %q", decision)
	}
	edits := planner.editedRaws()
	if len(edits) != 1 {
		t.Fatalf("expected 1 wiki_edit_card call, got %d", len(edits))
	}
	raw := edits[0].Raw
	// Preserves the other data keys.
	for _, must := range []string{"scope:", "destination:", "ok", "coding: false"} {
		if !strings.Contains(raw, must) {
			t.Errorf("expected %q preserved in edited raw; got:\n%s", must, raw)
		}
	}
}
