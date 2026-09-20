package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestHandleChatSubmit_ModeSlashGoal(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:          domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title:        "Goal Mode",
			Dependencies: []domain.ComponentDependency{{CardID: "builtin:bundle:goal", Required: true}},
		},
		"builtin:bundle:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:goal", Kind: "bundle", Source: "builtin"},
			Title: "Goal Tools",
		},
	})
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/goal refactor authentication"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if !a.cardRefEnabled("builtin:mode:goal") {
		t.Fatal("goal mode was not mounted")
	}
	if a.RawSession.Goal == nil || a.RawSession.Goal.Condition != "refactor authentication" {
		t.Fatalf("goal condition = %+v, want stripped slash-mode argument", a.RawSession.Goal)
	}
	if len(a.Session.Turns) != 1 || a.Session.Turns[0].UserInput != "refactor authentication" {
		t.Fatalf("user turns = %+v, want one turn with stripped text", a.Session.Turns)
	}
}

// TestHandleChatSubmit_GoalInterceptWithActiveTurnRef verifies that the goal
// condition is intercepted even when ActiveTurnRef is set (a running or paused
// turn exists). Before the fix the goal interception block was placed after
// the active-turn inject/queue paths, so the message was queued into the
// running turn and goal mode never activated.
func TestHandleChatSubmit_GoalInterceptWithActiveTurnRef(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:          domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title:        "Goal Mode",
			Dependencies: []domain.ComponentDependency{{CardID: "builtin:bundle:goal", Required: true}},
		},
		"builtin:bundle:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:goal", Kind: "bundle", Source: "builtin"},
			Title: "Goal Tools",
		},
	})
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	// Mount goal mode first.
	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/goal"}); err != nil {
		t.Fatalf("mount goal mode: %v", err)
	}
	if !a.cardRefEnabled("builtin:mode:goal") {
		t.Fatal("goal mode was not mounted")
	}

	// Simulate an active turn ref (paused turn, no live engine).
	a.setActiveTurnRef("turn-stale")
	a.Session.Turns = append(a.Session.Turns, domain.Turn{
		ID:    "turn-stale",
		Role:  "assistant",
		State: "paused",
	})

	// Now submit the goal condition while the stale ref is set.
	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "refactor the auth module"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}

	if a.RawSession.Goal == nil || a.RawSession.Goal.Condition != "refactor the auth module" {
		t.Fatalf("goal = %+v, want condition %q", a.RawSession.Goal, "refactor the auth module")
	}
}

func TestHandleChatSubmit_ModeSlashWorkflow_TagsUserStep(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:mode:workflow": {
			Ref:          domain.ComponentRef{CardID: "builtin:mode:workflow", Kind: "mode", Source: "builtin"},
			Title:        "Workflow Mode",
			Dependencies: []domain.ComponentDependency{{CardID: "builtin:bundle:workflow-tools", Required: true}},
		},
		"builtin:bundle:workflow-tools": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:workflow-tools", Kind: "bundle", Source: "builtin"},
			Title: "Workflow Tools",
		},
	})
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/workflow orchestrate the migration"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if !a.cardRefEnabled("builtin:mode:workflow") {
		t.Fatal("workflow mode was not mounted")
	}
	if a.workflowActive() {
		t.Fatal("expected no active workflow after mount")
	}
	if len(a.Session.Turns) != 1 || a.Session.Turns[0].UserInput != "orchestrate the migration" {
		t.Fatalf("user turns = %+v, want one turn with stripped text", a.Session.Turns)
	}
	if n := len(a.steps); n == 0 || a.steps[n-1].Meta != "workflow_submit" {
		t.Fatalf("expected last user step Meta=workflow_submit, got steps %+v", a.steps)
	}
}

func TestBuiltinModeSlashInput(t *testing.T) {
	tests := []struct {
		input      string
		wantCardID string
		wantArgs   string
		wantMatch  bool
	}{
		{input: "/goal refactor authentication", wantCardID: "builtin:mode:goal", wantArgs: "refactor authentication", wantMatch: true},
		{input: "/memory remember this", wantCardID: "builtin:mode:memory", wantArgs: "remember this", wantMatch: true},
		{input: "/worktree isolate this task", wantCardID: "builtin:mode:worktree", wantArgs: "isolate this task", wantMatch: true},
		{input: "/workflow follow this plan", wantCardID: "builtin:mode:workflow", wantArgs: "follow this plan", wantMatch: true},
		{input: "/goal", wantCardID: "builtin:mode:goal", wantMatch: true},
		{input: "/unknown text", wantArgs: "", wantMatch: false},
		{input: "ordinary message", wantArgs: "", wantMatch: false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cardID, args, matched, err := builtinModeSlashInput(tt.input)
			if err != nil {
				t.Fatalf("builtinModeSlashInput(%q): %v", tt.input, err)
			}
			if cardID != tt.wantCardID || args != tt.wantArgs || matched != tt.wantMatch {
				t.Errorf("builtinModeSlashInput(%q) = (%q, %q, %t), want (%q, %q, %t)", tt.input, cardID, args, matched, tt.wantCardID, tt.wantArgs, tt.wantMatch)
			}
		})
	}
}
