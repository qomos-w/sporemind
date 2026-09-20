package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// goalWorktreeMergeHarness records project.workflow_stop_merge_worktree
// invocations (through the parent ref) and serves
// project.worktree_agent_bindings (through the planner) for the
// goal-completion auto-merge flow in handleTurnComplete.
type goalWorktreeMergeHarness struct {
	mu sync.Mutex

	mergeCalled bool
	mergeReqs   []gen.ProjectWorkflowStopMergeWorktreeReq
	mergeResp   gen.ProjectWorkflowStopMergeWorktreeResp
	mergeErr    error

	bindingsCalled bool
	bindings       []gen.ProjectWorktreeAgentBinding
}

func newGoalWorktreeMergeHarness() *goalWorktreeMergeHarness {
	return &goalWorktreeMergeHarness{
		mergeResp: gen.ProjectWorkflowStopMergeWorktreeResp{Status: "merged"},
	}
}

func (h *goalWorktreeMergeHarness) ctx(t *testing.T) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "project.workflow_stop_merge_worktree":
			h.mu.Lock()
			h.mergeCalled = true
			if req, ok := payload.(gen.ProjectWorkflowStopMergeWorktreeReq); ok {
				h.mergeReqs = append(h.mergeReqs, req)
			}
			resp, err := h.mergeResp, h.mergeErr
			h.mu.Unlock()
			if err != nil {
				return err
			}
			return resp
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				switch callID {
				case "project.worktree_agent_bindings":
					h.mu.Lock()
					h.bindingsCalled = true
					bindings := append([]gen.ProjectWorktreeAgentBinding(nil), h.bindings...)
					h.mu.Unlock()
					return gen.ProjectWorktreeAgentBindingsResp{Bindings: bindings}, nil
				}
				return nil, fmt.Errorf("unexpected call %q", callID)
			},
		}
	}
	return ctx
}

// newTurnCompleteWorktreeActor builds an Actor mid-flight on a single turn
// whose session ends at that turn (no queued user turn), so
// maybeAutoStartTurn in handleTurnComplete is a no-op. The goal is unbound and
// confirmed, so the complete_candidate branch runs without card marking.
func newTurnCompleteWorktreeActor(worktreeID string, workflow *gen.ActiveWorkflow) *Actor {
	a := &Actor{
		actorID:       "agent-wt-merge",
		agentKind:     "worker",
		ActiveTurnRef: "turn-wt-merge",
		worktreeID:    worktreeID,
		aggRefCache: map[string]ref.Ref{
			systemAggID: fakeCompactionRef{tag: "system"},
		},
		Session: domain.Session{
			Turns: []domain.Turn{
				{ID: "user-wt-merge", Role: "user", State: "completed"},
				{ID: "turn-wt-merge", Role: "assistant", State: "running"},
			},
			ActiveHead: 0,
		},
		RawSession: domain.RawSession{
			NextSeq: 1,
			Goal: &gen.SessionGoal{
				Condition: "do the thing",
				Status:    "active",
				Confirmed: true,
				MaxTurns:  10,
				TurnCount: 1,
			},
		},
		steps: []domain.Step{
			{ID: "s1", Role: "assistant", Type: "text", TurnID: "turn-wt-merge", Closed: true, Seq: 1},
		},
		status: turnStatus{
			TurnID:         "turn-wt-merge",
			State:          "running",
			StartStepCount: 0,
		},
		snapshotReady: true,
	}
	if workflow != nil {
		a.RawSession.ActiveWorkflow = workflow
	}
	a.takeSnapshot()
	return a
}

func completeCandidateTurnCompleteReq() domain.AgentTurnCompleteReq {
	return domain.AgentTurnCompleteReq{
		Turn: domain.Turn{
			ID:          "turn-wt-merge",
			Role:        "assistant",
			State:       "completed",
			Timestamp:   "2026-01-01T00:00:00Z",
			StartedAt:   "2026-01-01T00:00:00Z",
			CompletedAt: "2026-01-01T00:00:01Z",
			Assessment: &gen.TurnAssessment{
				Decision: "complete_candidate",
				Reason:   "goal achieved",
			},
		},
	}
}

func setWorktreeBindingCache(a *Actor, id string) {
	a.worktreeID = id
	a.worktreeName = "agent-wt-merge-wt"
	a.worktreeStatus = "active"
	a.worktreePath = "/tmp/wt-merge"
}

func stepText(s domain.Step) string {
	var sb strings.Builder
	for _, b := range s.Content {
		sb.WriteString(b.Text)
	}
	return sb.String()
}

func findSystemStep(a *Actor, contains string) bool {
	for _, s := range a.steps {
		if s.Role == "system" && strings.Contains(stepText(s), contains) {
			return true
		}
	}
	return false
}

// TestHandleTurnComplete_CompleteCandidate_AutoMergesWorktree pins the happy
// path: a confirmed goal completing inside an isolated worktree invokes
// project.workflow_stop_merge_worktree with the bound WorktreeID, then
// refreshes the binding cache (the merged worktree is gone server-side, so the
// cache is cleared) without rolling back the goal completion.
func TestHandleTurnComplete_CompleteCandidate_AutoMergesWorktree(t *testing.T) {
	h := newGoalWorktreeMergeHarness()
	ctx := h.ctx(t)
	a := newTurnCompleteWorktreeActor("wt-1", nil)
	setWorktreeBindingCache(a, "wt-1")

	if err := a.handleTurnComplete(ctx, completeCandidateTurnCompleteReq()); err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if !h.mergeCalled {
		t.Fatal("project.workflow_stop_merge_worktree was not invoked on goal completion")
	}
	if len(h.mergeReqs) != 1 || h.mergeReqs[0].WorktreeID != "wt-1" {
		t.Fatalf("merge requests = %+v, want exactly one with WorktreeID=wt-1", h.mergeReqs)
	}
	if !h.bindingsCalled {
		t.Fatal("binding status was not refreshed after a successful merge")
	}
	if a.worktreeID != "" || a.worktreeName != "" || a.worktreeStatus != "" || a.worktreePath != "" {
		t.Fatalf("binding cache not cleared after merge: (ID=%q Name=%q Status=%q Path=%q)",
			a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("goal must still be cleared after merge: %+v", a.RawSession.Goal)
	}
	if findSystemStep(a, "Worktree auto-merge failed") {
		t.Fatal("successful merge must not emit a failure step")
	}
}

// TestHandleTurnComplete_CompleteCandidate_NoWorktree_SkipsMerge verifies the
// first guard: an agent without a worktree binding never invokes the merge.
func TestHandleTurnComplete_CompleteCandidate_NoWorktree_SkipsMerge(t *testing.T) {
	h := newGoalWorktreeMergeHarness()
	ctx := h.ctx(t)
	a := newTurnCompleteWorktreeActor("", nil)

	if err := a.handleTurnComplete(ctx, completeCandidateTurnCompleteReq()); err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if h.mergeCalled {
		t.Fatal("merge must not be invoked when the agent has no worktree")
	}
	if h.bindingsCalled {
		t.Fatal("binding status must not be refreshed without a worktree")
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("goal must still be cleared without a worktree: %+v", a.RawSession.Goal)
	}
}

// TestHandleTurnComplete_CompleteCandidate_WorkflowActive_SkipsMerge verifies
// the second guard: a worktree owned by an active workflow map is merged by
// workflow_stop, never by goal completion, so the binding is left untouched.
func TestHandleTurnComplete_CompleteCandidate_WorkflowActive_SkipsMerge(t *testing.T) {
	h := newGoalWorktreeMergeHarness()
	ctx := h.ctx(t)
	a := newTurnCompleteWorktreeActor("wt-owned", &gen.ActiveWorkflow{MapCardID: "map-1"})
	setWorktreeBindingCache(a, "wt-owned")

	if err := a.handleTurnComplete(ctx, completeCandidateTurnCompleteReq()); err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if h.mergeCalled {
		t.Fatal("merge must not be invoked while a workflow is active")
	}
	if a.worktreeID != "wt-owned" || a.worktreeStatus != "active" {
		t.Fatalf("workflow-owned binding must be untouched, got (ID=%q Status=%q)", a.worktreeID, a.worktreeStatus)
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("goal must still be cleared when a workflow is active: %+v", a.RawSession.Goal)
	}
}

// TestHandleTurnComplete_CompleteCandidate_MergeConflict_KeepsBinding verifies
// the conflict path: the goal completion is NOT rolled back, the worktree
// binding cache is retained (no refresh), and a system step instructs the user
// to resolve the merge manually.
func TestHandleTurnComplete_CompleteCandidate_MergeConflict_KeepsBinding(t *testing.T) {
	h := newGoalWorktreeMergeHarness()
	h.mergeResp = gen.ProjectWorkflowStopMergeWorktreeResp{Status: "conflict", ConflictFiles: []string{"a.go"}}
	ctx := h.ctx(t)
	a := newTurnCompleteWorktreeActor("wt-1", nil)
	setWorktreeBindingCache(a, "wt-1")

	if err := a.handleTurnComplete(ctx, completeCandidateTurnCompleteReq()); err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if !h.mergeCalled {
		t.Fatal("merge must be attempted even when it will conflict")
	}
	if h.bindingsCalled {
		t.Fatal("binding status must not be refreshed after a failed merge")
	}
	if a.worktreeID != "wt-1" || a.worktreeName != "agent-wt-merge-wt" || a.worktreeStatus != "active" || a.worktreePath != "/tmp/wt-merge" {
		t.Fatalf("binding cache must be retained on conflict, got (ID=%q Name=%q Status=%q Path=%q)",
			a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("goal completion must not be rolled back on conflict: %+v", a.RawSession.Goal)
	}
	if !findSystemStep(a, "Worktree auto-merge failed") {
		t.Fatal("expected a system step announcing the failed auto-merge")
	}
	if !findSystemStep(a, "resolve the merge manually") {
		t.Fatal("expected the system step to instruct manual resolution")
	}
}

// TestHandleTurnComplete_CompleteCandidate_MergeCallError_KeepsBinding covers
// the transport/error branch of the merge call: same retention semantics as
// the conflict path.
func TestHandleTurnComplete_CompleteCandidate_MergeCallError_KeepsBinding(t *testing.T) {
	h := newGoalWorktreeMergeHarness()
	h.mergeErr = fmt.Errorf("git rebase aborted")
	ctx := h.ctx(t)
	a := newTurnCompleteWorktreeActor("wt-1", nil)
	setWorktreeBindingCache(a, "wt-1")

	if err := a.handleTurnComplete(ctx, completeCandidateTurnCompleteReq()); err != nil {
		t.Fatalf("handleTurnComplete error: %v", err)
	}

	if !h.mergeCalled {
		t.Fatal("merge must be attempted even when it will fail")
	}
	if h.bindingsCalled {
		t.Fatal("binding status must not be refreshed after a failed merge")
	}
	if a.worktreeID != "wt-1" || a.worktreeStatus != "active" {
		t.Fatalf("binding cache must be retained on error, got (ID=%q Status=%q)", a.worktreeID, a.worktreeStatus)
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("goal completion must not be rolled back on error: %+v", a.RawSession.Goal)
	}
	if !findSystemStep(a, "Worktree auto-merge failed") || !findSystemStep(a, "git rebase aborted") {
		t.Fatalf("expected system step with the merge error, steps=%d", len(a.steps))
	}
}
