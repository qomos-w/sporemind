package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestInjectForkChildContextReviewerBackfill(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		kind       string
		goal       string
		wantPrompt string // reviewer: review text carried in Prompt; others: LLM prompt
	}{
		{
			name:       "reviewer omitted ReviewText backfills goal",
			input:      `{"Description":"review","Prompt":""}`,
			kind:       domain.AgentKindReviewer,
			goal:       "achieve 80% coverage",
			wantPrompt: "achieve 80% coverage",
		},
		{
			name:       "reviewer explicit ReviewText not overwritten",
			input:      `{"Description":"review","Prompt":"","ReviewText":"check the auth module"}`,
			kind:       domain.AgentKindReviewer,
			goal:       "session goal ignored",
			wantPrompt: "check the auth module",
		},
		{
			name:       "non-reviewer kind does not receive goal",
			input:      `{"Description":"explore","Prompt":"find usage"}`,
			kind:       "explorer",
			goal:       "session goal ignored",
			wantPrompt: "find usage",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := injectForkChildContext(tt.input, "step-1", "turn-1", "tool-1", tt.kind, tt.goal)
			var req domain.WorkspaceAgentSpawnByTypeReq
			if err := json.Unmarshal([]byte(out), &req); err != nil {
				t.Fatalf("output not valid JSON: %v", err)
			}
			if req.Prompt != tt.wantPrompt {
				t.Fatalf("Prompt = %q, want %q", req.Prompt, tt.wantPrompt)
			}
			if req.ParentStepID != "step-1" {
				t.Fatalf("ParentStepID = %q, want step-1", req.ParentStepID)
			}
			if req.ParentTurnID != "turn-1" {
				t.Fatalf("ParentTurnID = %q, want turn-1", req.ParentTurnID)
			}
			if req.ToolUseID != "tool-1" {
				t.Fatalf("ToolUseID = %q, want tool-1", req.ToolUseID)
			}
			if req.AgentKind != tt.kind {
				t.Fatalf("AgentKind = %q, want %q", req.AgentKind, tt.kind)
			}
		})
	}
}

func TestInjectForkChildContextDescriptionBackfill(t *testing.T) {
	// fork_review's LLM schema has no Description field; injectForkChildContext
	// must backfill it so workspace.agent_spawn_by_type validation passes.
	tests := []struct {
		name     string
		input    string
		kind     string
		goal     string
		wantDesc string
	}{
		{
			name:     "reviewer ReviewText becomes description",
			input:    `{"ReviewText":"check the auth module"}`,
			kind:     domain.AgentKindReviewer,
			goal:     "session goal",
			wantDesc: "check the auth module",
		},
		{
			name:     "reviewer goal backfill also feeds description",
			input:    `{}`,
			kind:     domain.AgentKindReviewer,
			goal:     "achieve 80% coverage",
			wantDesc: "achieve 80% coverage",
		},
		{
			name:     "reviewer empty everything falls back to kind",
			input:    `{}`,
			kind:     domain.AgentKindReviewer,
			goal:     "",
			wantDesc: domain.AgentKindReviewer,
		},
		{
			name:     "explicit description preserved",
			input:    `{"Description":"my review","ReviewText":"check x"}`,
			kind:     domain.AgentKindReviewer,
			goal:     "goal",
			wantDesc: "my review",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := injectForkChildContext(tt.input, "step-1", "turn-1", "tool-1", tt.kind, tt.goal)
			var req domain.WorkspaceAgentSpawnByTypeReq
			if err := json.Unmarshal([]byte(out), &req); err != nil {
				t.Fatalf("output not valid JSON: %v", err)
			}
			if req.Description != tt.wantDesc {
				t.Fatalf("Description = %q, want %q", req.Description, tt.wantDesc)
			}
		})
	}
}

func TestInjectForkChildContextPlanEvidence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		kind  string
		want  []string
	}{
		{
			name:  "reviewer PlanEvidence carried through",
			input: `{"ReviewText":"check x","PlanEvidence":["plan-1","plan-2"]}`,
			kind:  domain.AgentKindReviewer,
			want:  []string{"plan-1", "plan-2"},
		},
		{
			name:  "reviewer without PlanEvidence stays nil",
			input: `{"ReviewText":"check x"}`,
			kind:  domain.AgentKindReviewer,
			want:  nil,
		},
		{
			name:  "non-reviewer ignores PlanEvidence",
			input: `{"Description":"d","Prompt":"p","PlanEvidence":["plan-1"]}`,
			kind:  "explorer",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := injectForkChildContext(tt.input, "step-1", "turn-1", "tool-1", tt.kind, "")
			var req domain.WorkspaceAgentSpawnByTypeReq
			if err := json.Unmarshal([]byte(out), &req); err != nil {
				t.Fatalf("output not valid JSON: %v", err)
			}
			if !reflect.DeepEqual(req.PlanEvidence, tt.want) {
				t.Fatalf("PlanEvidence = %v, want %v", req.PlanEvidence, tt.want)
			}
		})
	}
}

func TestInjectForkChildContextMalformedJSON(t *testing.T) {
	// Best-effort string injection must still succeed on malformed input.
	out := injectForkChildContext("not-json", "step-1", "turn-1", "tool-1", domain.AgentKindReviewer, "goal")
	var req domain.WorkspaceAgentSpawnByTypeReq
	if err := json.Unmarshal([]byte(out), &req); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if req.ParentStepID != "step-1" {
		t.Fatalf("ParentStepID = %q, want step-1", req.ParentStepID)
	}
	if req.ParentTurnID != "turn-1" {
		t.Fatalf("ParentTurnID = %q, want turn-1", req.ParentTurnID)
	}
	if req.AgentKind != domain.AgentKindReviewer {
		t.Fatalf("AgentKind = %q, want reviewer", req.AgentKind)
	}
}

func TestResolveGoalConditionNilSafe(t *testing.T) {
	e := &turnEngine{} // onGoalCondition nil
	if got := e.resolveGoalCondition(); got != "" {
		t.Fatalf("expected empty string when callback nil, got %q", got)
	}
	called := false
	e.onGoalCondition = func() string {
		called = true
		return "active goal"
	}
	if got := e.resolveGoalCondition(); got != "active goal" {
		t.Fatalf("expected active goal, got %q", got)
	}
	if !called {
		t.Fatal("expected callback to be invoked")
	}
}

// TestTrackForkChildren_RegistersChildActorID verifies that after a successful
// workspace.agent_spawn_by_type call, trackForkChildren parses the returned
// ChildActorID and invokes onChildTrack so the parent agent can track the child
// for cancellation, liveness checks, and explore_complete routing.
func TestTrackForkChildren_RegistersChildActorID(t *testing.T) {
	var trackedToolUseID, trackedChildID string
	e := &turnEngine{
		onChildTrack: func(_ actor.Context, toolUseID, childActorID string) {
			trackedToolUseID = toolUseID
			trackedChildID = childActorID
		},
	}
	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{ID: "toolu-123", CallableID: "workspace.agent_spawn_by_type", LLMName: "fork_explore"},
		},
	}
	resp := domain.WorkspaceAgentSpawnByTypeResp{ChildActorID: "child-actor-1", DisplayName: "Explorer"}
	respJSON, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal resp: %v", err)
	}
	results := []toolExecutionResult{
		{call: batch.calls[0], out: string(respJSON), isErr: false},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e.trackForkChildren(ctx, batch, results)
	if trackedToolUseID != "toolu-123" {
		t.Errorf("tracked ToolUseID = %q, want toolu-123", trackedToolUseID)
	}
	if trackedChildID != "child-actor-1" {
		t.Errorf("tracked ChildActorID = %q, want child-actor-1", trackedChildID)
	}
	if len(e.pendingChildren) != 1 {
		t.Fatalf("pendingChildren len = %d, want 1", len(e.pendingChildren))
	}
	if e.pendingChildren[0].ToolUseID != "toolu-123" {
		t.Errorf("pendingChild.ToolUseID = %q, want toolu-123", e.pendingChildren[0].ToolUseID)
	}
}
