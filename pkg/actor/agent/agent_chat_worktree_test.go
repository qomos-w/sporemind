package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func worktreeModeDescriptors() map[string]domain.ComponentDescriptor {
	return map[string]domain.ComponentDescriptor{
		"builtin:mode:worktree": {
			Ref:          domain.ComponentRef{CardID: "builtin:mode:worktree", Kind: "mode", Source: "builtin"},
			Title:        "Worktree Mode",
			Dependencies: []domain.ComponentDependency{{CardID: "builtin:bundle:worktree", Required: true}},
		},
		"builtin:bundle:worktree": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:worktree", Kind: "bundle", Source: "builtin"},
			Title: "Worktree Tools",
		},
	}
}

// makeWorktreeModeCtx builds a test context whose planner counts
// project.worktree_enter invocations, records worktree_exit, and reports an
// optional binding back through project.worktree_agent_bindings.
func makeWorktreeModeCtx(t *testing.T, descriptors map[string]domain.ComponentDescriptor, enterErr error, binding *gen.ProjectWorktreeAgentBinding) (*testutil.FakeCtx, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	entered := &atomic.Int32{}
	exited := &atomic.Bool{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				switch callID {
				case "project.component_get", "workspace.component_get":
					var req domain.ProjectComponentGetReq
					switch p := payload.(type) {
					case domain.ProjectComponentGetReq:
						req = p
					case []byte:
						_ = json.Unmarshal(p, &req)
					}
					if desc, ok := descriptors[req.CardID]; ok {
						return domain.ProjectComponentGetResp{Component: desc}, nil
					}
					return nil, fmt.Errorf("component %q not found", req.CardID)
				case "project.wiki_create_card":
					return domain.WikiCreateCardResp{}, nil
				case "project.wiki_edit_card":
					return domain.WikiEditCardResp{}, nil
				case "project.wiki_get_card":
					return domain.WikiGetCardResp{}, nil
				case "project.worktree_enter":
					entered.Add(1)
					if enterErr != nil {
						return nil, enterErr
					}
					return gen.ProjectWorktreeEnterResp{}, nil
				case "project.worktree_exit":
					exited.Store(true)
					return gen.ProjectWorktreeExitResp{}, nil
				case "project.worktree_agent_bindings":
					resp := gen.ProjectWorktreeAgentBindingsResp{Bindings: []gen.ProjectWorktreeAgentBinding{}}
					if binding != nil {
						resp.Bindings = append(resp.Bindings, *binding)
					}
					return resp, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return ctx, entered, exited
}

// TestHandleChatSubmit_SlashWorktreeEntersBinding pins the unified entry:
// /worktree must bind an isolated worktree (project.worktree_enter) before
// the mode card is mounted, and must not start a turn when no args follow.
func TestHandleChatSubmit_SlashWorktreeEntersBinding(t *testing.T) {
	binding := &gen.ProjectWorktreeAgentBinding{
		AgentActorID: "agent-1", WorktreeID: "wt-1", Name: "agent-1-wt", Status: "active", WorktreePath: "/tmp/wt-1",
	}
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, binding)
	a := &Actor{actorID: "agent-1", ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/worktree"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if entered.Load() != 1 {
		t.Fatalf("project.worktree_enter invocations = %d, want exactly 1 (enter gate + explicit mount must not double-invoke)", entered.Load())
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("worktree mode card was not mounted")
	}
	if a.worktreeID != "wt-1" || a.worktreeName != "agent-1-wt" || a.worktreeStatus != "active" || a.worktreePath != "/tmp/wt-1" {
		t.Fatalf("binding cache = (ID=%q, Name=%q, Status=%q, Path=%q), want (wt-1, agent-1-wt, active, /tmp/wt-1)",
			a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
	if n := len(a.Session.Turns); n != 0 {
		t.Fatalf("turns = %d, want 0 for bare /worktree", n)
	}
}

// TestHandleChatSubmit_SlashWorktreeWithArgsKeepsTurn: /worktree <text>
// enters isolation AND starts a turn carrying the stripped text.
func TestHandleChatSubmit_SlashWorktreeWithArgsKeepsTurn(t *testing.T) {
	binding := &gen.ProjectWorktreeAgentBinding{
		AgentActorID: "agent-1", WorktreeID: "wt-1", WorktreePath: "/tmp/wt-1", Status: "active",
	}
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, binding)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/worktree isolate this task"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if entered.Load() == 0 {
		t.Fatal("project.worktree_enter was not invoked by /worktree")
	}
	if len(a.Session.Turns) != 1 || a.Session.Turns[0].UserInput != "isolate this task" {
		t.Fatalf("user turns = %+v, want one turn with stripped text", a.Session.Turns)
	}
}

// TestHandleChatSubmit_SlashWorktreeEnterFails_NoMount: when
// project.worktree_enter fails, the mode card must NOT be mounted — card
// presence always implies an active binding — and no turn may be started.
func TestHandleChatSubmit_SlashWorktreeEnterFails_NoMount(t *testing.T) {
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), fmt.Errorf("git worktree add failed"), nil)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/worktree"}); err == nil {
		t.Fatal("expected error when worktree_enter fails")
	}
	if entered.Load() == 0 {
		t.Fatal("project.worktree_enter was not invoked")
	}
	if a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("mode card mounted despite failed worktree_enter")
	}
	if n := len(a.Session.Turns); n != 0 {
		t.Fatalf("turns = %d, want 0 when worktree_enter fails", n)
	}
}

// TestHandleChatSubmit_SlashWorktreeAlreadyBound_ReusesBinding: an already
// bound agent issuing /worktree again re-enters its existing worktree
// idempotently — binding cache stays identical, the mode card stays mounted,
// and no new turn is started.
func TestHandleChatSubmit_SlashWorktreeAlreadyBound_ReusesBinding(t *testing.T) {
	binding := &gen.ProjectWorktreeAgentBinding{
		AgentActorID: "agent-1", WorktreeID: "wt-1", Name: "agent-1-wt", Status: "active", WorktreePath: "/tmp/wt-1",
	}
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, binding)
	a := &Actor{
		actorID:        "agent-1",
		worktreeID:     "wt-1",
		worktreeName:   "agent-1-wt",
		worktreeStatus: "active",
		worktreePath:   "/tmp/wt-1",
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:mode:worktree", Enabled: true, Scope: "user"},
		},
	}

	if _, err := a.handleChatSubmit(ctx, domain.AgentChatSubmitReq{Text: "/worktree"}); err != nil {
		t.Fatalf("handleChatSubmit: %v", err)
	}
	if entered.Load() != 1 {
		t.Fatalf("project.worktree_enter invocations = %d, want exactly 1 for idempotent re-entry", entered.Load())
	}
	if a.worktreeID != "wt-1" || a.worktreeName != "agent-1-wt" || a.worktreeStatus != "active" || a.worktreePath != "/tmp/wt-1" {
		t.Fatalf("binding cache = (ID=%q, Name=%q, Status=%q, Path=%q), want unchanged (wt-1, agent-1-wt, active, /tmp/wt-1)",
			a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("mode card must stay mounted on idempotent re-entry")
	}
	if n := len(a.Session.Turns); n != 0 {
		t.Fatalf("turns = %d, want 0 for bare /worktree", n)
	}
}

// TestHandleComponentUnmount_WorktreeRequiresConfirm pins the guarded
// unmount: without Confirm the unmount is rejected with a destructive-action
// error so the UI can surface a warning dialog; with Confirm the agent exits
// its worktree (discard) before the card is removed.
func TestHandleComponentUnmount_WorktreeRequiresConfirm(t *testing.T) {
	binding := &gen.ProjectWorktreeAgentBinding{
		AgentActorID: "agent-1", WorktreeID: "wt-1", Name: "agent-1-ab12", Status: "active", WorktreePath: "/tmp/wt-1",
	}
	ctx, _, exited := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, binding)
	a := &Actor{actorID: "agent-1", ComponentMounts: []domain.AgentComponentMount{}}

	// Mount the mode card; the mount gate enters the worktree and refreshes
	// the binding cache the way production does (no manual field presetting).
	if _, merr := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:worktree", Enabled: true, Scope: "user"}); merr != nil {
		t.Fatalf("precondition mount: %v", merr)
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("precondition: mode card must be enabled")
	}
	if a.worktreeStatus != "active" {
		t.Fatalf("precondition: binding cache must be populated by the mount gate, got status %q", a.worktreeStatus)
	}

	if _, uerr := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:worktree"}); uerr == nil {
		t.Fatal("unmount without Confirm must be rejected")
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("card must stay mounted after rejected unmount")
	}
	if exited.Load() {
		t.Fatal("unconfirmed unmount must not discard the worktree")
	}

	if _, uerr := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:worktree", Confirm: true}); uerr != nil {
		t.Fatalf("confirmed unmount: %v", uerr)
	}
	if a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("card must be unmounted after confirmed discard")
	}
	if a.worktreeID != "" || a.worktreeStatus != "" {
		t.Fatalf("binding cache must be cleared, got (%q, %q)", a.worktreeID, a.worktreeStatus)
	}
	if !exited.Load() {
		t.Fatal("confirmed unmount must discard the worktree via worktree_exit")
	}
}

// TestHandleComponentUnmount_WorktreeUnbound_NoConfirmNeeded verifies that
// unmounting builtin:mode:worktree when the agent has no worktree binding
// proceeds without Confirm and without calling project.worktree_exit — the
// card is simply removed.
func TestHandleComponentUnmount_WorktreeUnbound_NoConfirmNeeded(t *testing.T) {
	ctx, _, exited := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, nil)
	a := &Actor{actorID: "agent-1", ComponentMounts: []domain.AgentComponentMount{}}

	// Mount the mode card. worktree_enter succeeds but the bindings query
	// reports nothing bound (worktreeStatus stays ""), which is the mocked
	// equivalent of an agent with no usable worktree.
	if _, merr := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:worktree", Enabled: true, Scope: "user"}); merr != nil {
		t.Fatalf("precondition mount: %v", merr)
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("precondition: mode card must be enabled")
	}

	// Unmount without Confirm — should succeed because no binding exists.
	if _, uerr := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:worktree"}); uerr != nil {
		t.Fatalf("unmount without binding must not require Confirm: %v", uerr)
	}
	if a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("card must be unmounted when no binding exists")
	}
	if exited.Load() {
		t.Fatal("unbound unmount must not invoke project.worktree_exit")
	}
	if a.worktreeID != "" || a.worktreeStatus != "" {
		t.Fatalf("binding cache must stay empty, got (%q, %q)", a.worktreeID, a.worktreeStatus)
	}
}

// TestHandleComponentMount_WorktreeEntersBinding pins the unified mount gate:
// agent.component.mount (the UI's mode panel / omnibox entry point) must bind
// the worktree BEFORE mounting the card. Before this gate a UI-mounted card
// carried no binding, and the turn-start refreshWorktreeStatus unmounted it on
// the next message — the vanishing worktree badge.
func TestHandleComponentMount_WorktreeEntersBinding(t *testing.T) {
	binding := &gen.ProjectWorktreeAgentBinding{
		AgentActorID: "agent-1", WorktreeID: "wt-1", Name: "agent-1-wt", Status: "active", WorktreePath: "/tmp/wt-1",
	}
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), nil, binding)
	a := &Actor{actorID: "agent-1", ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:worktree", Enabled: true, Scope: "user"}); err != nil {
		t.Fatalf("handleComponentMount: %v", err)
	}
	if entered.Load() != 1 {
		t.Fatalf("project.worktree_enter invocations = %d, want 1", entered.Load())
	}
	if !a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("worktree mode card was not mounted")
	}
	if a.worktreeID != "wt-1" || a.worktreeName != "agent-1-wt" || a.worktreeStatus != "active" || a.worktreePath != "/tmp/wt-1" {
		t.Fatalf("binding cache = (ID=%q, Name=%q, Status=%q, Path=%q), want (wt-1, agent-1-wt, active, /tmp/wt-1)",
			a.worktreeID, a.worktreeName, a.worktreeStatus, a.worktreePath)
	}
}

// TestHandleComponentMount_WorktreeEnterFails_NoMount: when the mount gate's
// project.worktree_enter fails, the mount must fail and the card must NOT be
// mounted — card presence always implies an active binding.
func TestHandleComponentMount_WorktreeEnterFails_NoMount(t *testing.T) {
	ctx, entered, _ := makeWorktreeModeCtx(t, worktreeModeDescriptors(), fmt.Errorf("git worktree add failed"), nil)
	a := &Actor{actorID: "agent-1", ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:worktree", Enabled: true, Scope: "user"}); err == nil {
		t.Fatal("expected mount error when worktree_enter fails")
	}
	if entered.Load() == 0 {
		t.Fatal("project.worktree_enter was not invoked")
	}
	if a.cardRefEnabled("builtin:mode:worktree") {
		t.Fatal("mode card mounted despite failed worktree_enter")
	}
}
