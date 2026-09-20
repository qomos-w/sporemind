package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// grantLookupFn builds a LookupIDFn that stubs the caller agent's
// agent.component_list snapshot (returning mounts) and the target agent's
// messaging/pause/resume callables. invocations records every callID invoked
// on the target so tests can prove (or pin the absence of) forwarding.
// Any other id falls back to lookupOK.
func grantLookupFn(callerID, targetActorID string, mounts []domain.AgentComponentMount, invocations map[string]int) func(id.ActorID) (ref.Ref, bool) {
	return func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case callerID:
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "component_list" {
					return domain.AgentComponentListResp{Items: mounts}
				}
				return nil
			}), true
		case targetActorID:
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				invocations[callID]++
				if callID == "message_read" {
					return domain.AgentMessageReadResp{Items: []domain.AgentMessageReadItem{{Seq: 1, Role: "user", Content: "hi"}}}
				}
				return nil
			}), true
		}
		return lookupOK(aid)
	}
}

// TestConversableGrant_OwnerAgentPassesAllFour verifies that an agent caller
// whose CallerAgentId equals the target's ParentAgentID (the owner) may send,
// read, pause, and resume the target.
func TestConversableGrant_OwnerAgentPassesAllFour(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := genID()
	targetActorID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "Target#0001",
		ActorID:       targetActorID,
		ParentAgentID: ownerID,
		LoadState:     "loaded",
	}}
	invocations := map[string]int{}
	ctx.LookupIDFn = grantLookupFn(ownerID, targetActorID, nil, invocations)

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi", CallerAgentID: ownerID}); err != nil {
		t.Fatalf("owner send: %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001", CallerAgentID: ownerID}); err != nil {
		t.Fatalf("owner read: %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001", CallerAgentID: ownerID}); err != nil {
		t.Fatalf("owner pause: %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001", CallerAgentID: ownerID}); err != nil {
		t.Fatalf("owner resume: %v", err)
	}
	for _, want := range []string{"message_receive", "message_read", "agent_pause", "agent_resume"} {
		if invocations[want] != 1 {
			t.Errorf("expected target %s invoked once, got %d", want, invocations[want])
		}
	}
}

// TestConversableGrant_DirectChildCanSendReadButNotPause verifies the
// documented worker→owner notification path: a DIRECT CHILD of the target may
// send/read its messages, but children must NOT pause/resume their parents —
// the forwardPauseResume path carries no direct-child allowance.
func TestConversableGrant_DirectChildCanSendReadButNotPause(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := genID()
	targetActorID := genID()
	childID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Target#0001", ActorID: targetActorID, ParentAgentID: ownerID, LoadState: "loaded"},
		{ID: "Child#0001", ActorID: childID, ParentAgentID: targetActorID, AgentKind: domain.AgentKindWorker},
	}
	invocations := map[string]int{}
	ctx.LookupIDFn = grantLookupFn(childID, targetActorID, nil, invocations)

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "notify", CallerAgentID: childID}); err != nil {
		t.Fatalf("child send: %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err != nil {
		t.Fatalf("child read: %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err == nil {
		t.Fatal("expected child pause to be rejected")
	} else if !strings.Contains(err.Error(), "may not pause/resume its parent") {
		t.Errorf("child pause: unexpected error %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err == nil {
		t.Fatal("expected child resume to be rejected")
	} else if !strings.Contains(err.Error(), "may not pause/resume its parent") {
		t.Errorf("child resume: unexpected error %v", err)
	}
	if invocations["agent_pause"] != 0 || invocations["agent_resume"] != 0 {
		t.Errorf("pause/resume must not reach the target: got %v", invocations)
	}
}

// TestConversableGrant_ChildWithParentChatMountCannotPause verifies that a
// direct child holding the spawn-seeded agent-chat:<parent> mount may send and
// read (the mount exists to surface the messaging tools) but is still denied
// pause/resume: the conversable grant must never hand a child lifecycle
// control over its parent.
func TestConversableGrant_ChildWithParentChatMountCannotPause(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := genID()
	targetActorID := genID()
	childID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Target#0001", ActorID: targetActorID, ParentAgentID: ownerID, LoadState: "loaded"},
		{ID: "Child#0001", ActorID: childID, ParentAgentID: targetActorID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	invocations := map[string]int{}
	ctx.LookupIDFn = grantLookupFn(childID, targetActorID, []domain.AgentComponentMount{{CardID: "agent-chat:" + targetActorID}}, invocations)

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "notify", CallerAgentID: childID}); err != nil {
		t.Fatalf("child send with mount: %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err != nil {
		t.Fatalf("child read with mount: %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err == nil {
		t.Fatal("expected child pause with mount to be rejected")
	} else if !strings.Contains(err.Error(), "may not pause/resume its parent") {
		t.Errorf("child pause with mount: unexpected error %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001", CallerAgentID: childID}); err == nil {
		t.Fatal("expected child resume with mount to be rejected")
	} else if !strings.Contains(err.Error(), "may not pause/resume its parent") {
		t.Errorf("child resume with mount: unexpected error %v", err)
	}
	if invocations["message_receive"] != 1 || invocations["message_read"] != 1 {
		t.Errorf("expected message ops forwarded once, got %v", invocations)
	}
	if invocations["agent_pause"] != 0 || invocations["agent_resume"] != 0 {
		t.Errorf("pause/resume must not reach the target: got %v", invocations)
	}
}

// TestConversableGrant_StrangerNoMountRejected verifies that a live agent
// caller with no relationship to the target and no agent-chat mount is denied
// on all four ops; the send/read denials name the allowed targets.
func TestConversableGrant_StrangerNoMountRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := genID()
	targetActorID := genID()
	strangerID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "Target#0001",
		ActorID:       targetActorID,
		ParentAgentID: ownerID,
		LoadState:     "loaded",
	}}
	invocations := map[string]int{}
	// Stranger resolves to a live actor but holds no mounts (nil component_list).
	ctx.LookupIDFn = grantLookupFn(strangerID, targetActorID, nil, invocations)

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi", CallerAgentID: strangerID}); err == nil {
		t.Fatal("expected stranger send to be rejected")
	} else if !strings.Contains(err.Error(), "not allowed to send/read messages") || !strings.Contains(err.Error(), "agent-chat:") {
		t.Errorf("stranger send: unexpected error %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err == nil {
		t.Fatal("expected stranger read to be rejected")
	} else if !strings.Contains(err.Error(), "not allowed to send/read messages") {
		t.Errorf("stranger read: unexpected error %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err == nil {
		t.Fatal("expected stranger pause to be rejected")
	} else if !strings.Contains(err.Error(), "only the target agent or its direct owner") {
		t.Errorf("stranger pause: unexpected error %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err == nil {
		t.Fatal("expected stranger resume to be rejected")
	} else if !strings.Contains(err.Error(), "only the target agent or its direct owner") {
		t.Errorf("stranger resume: unexpected error %v", err)
	}
	if len(invocations) != 0 {
		t.Errorf("no target callable may be invoked for a denied stranger, got %v", invocations)
	}
}

// TestConversableGrant_StrangerWithMountPasses verifies the explicit,
// revocable grant: a stranger holding an agent-chat:<target.ID> or
// agent-chat:<target.ActorID> mount passes all four ops (the caller actor's
// agent.component_list is stubbed to serve those mounts).
func TestConversableGrant_StrangerWithMountPasses(t *testing.T) {
	targetActorID := genID()
	for _, tc := range []struct {
		name   string
		cardID string
	}{
		{"mount by target ID", "agent-chat:Target#0001"},
		{"mount by target ActorID", "agent-chat:" + targetActorID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ctx := freshActorAnon(t)
			ownerID := genID()
			strangerID := genID()
			a.Agents = []domain.AgentRef{{
				ID:            "Target#0001",
				ActorID:       targetActorID,
				ParentAgentID: ownerID,
				LoadState:     "loaded",
			}}
			invocations := map[string]int{}
			ctx.LookupIDFn = grantLookupFn(strangerID, targetActorID, []domain.AgentComponentMount{{CardID: tc.cardID}}, invocations)

			if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi", CallerAgentID: strangerID}); err != nil {
				t.Fatalf("stranger send (granted): %v", err)
			}
			if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err != nil {
				t.Fatalf("stranger read (granted): %v", err)
			}
			if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err != nil {
				t.Fatalf("stranger pause (granted): %v", err)
			}
			if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001", CallerAgentID: strangerID}); err != nil {
				t.Fatalf("stranger resume (granted): %v", err)
			}
			for _, want := range []string{"message_receive", "message_read", "agent_pause", "agent_resume"} {
				if invocations[want] != 1 {
					t.Errorf("expected target %s invoked once, got %d", want, invocations[want])
				}
			}
		})
	}
}

// TestConversableGrant_EmptyCallerDeveloperRolePasses verifies the
// human/developer path: a developer-role caller with no CallerAgentId (UI
// calls) bypasses authorization on all four ops.
func TestConversableGrant_EmptyCallerDeveloperRolePasses(t *testing.T) {
	a, ctx := freshActor(t) // admin role (developer bypass)
	targetActorID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "Target#0001",
		ActorID:       targetActorID,
		ParentAgentID: genID(),
		LoadState:     "loaded",
	}}
	invocations := map[string]int{}
	ctx.LookupIDFn = grantLookupFn(genID(), targetActorID, nil, invocations)

	// CallerAgentID intentionally empty — the human UI path.
	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi"}); err != nil {
		t.Fatalf("developer send: %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001"}); err != nil {
		t.Fatalf("developer read: %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001"}); err != nil {
		t.Fatalf("developer pause: %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001"}); err != nil {
		t.Fatalf("developer resume: %v", err)
	}
}

// TestConversableGrant_EmptyCallerAnonRejected verifies that an anonymous
// (non-developer) caller with an empty CallerAgentId is rejected on all four
// ops before any mutation — direct Public invocation cannot bypass the turn
// engine's CallerAgentId injection.
func TestConversableGrant_EmptyCallerAnonRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "Target#0001",
		ActorID:       targetActorID,
		ParentAgentID: genID(),
		LoadState:     "loaded",
	}}
	invocations := map[string]int{}
	ctx.LookupIDFn = grantLookupFn(genID(), targetActorID, nil, invocations)

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi"}); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("anon empty-caller send: expected 'not trusted' deny, got %v", err)
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Target#0001"}); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("anon empty-caller read: expected 'not trusted' deny, got %v", err)
	}
	if _, err := a.handleAgentPause(ctx, gen.AgentPauseReq{ToAgentID: "Target#0001"}); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("anon empty-caller pause: expected 'not trusted' deny, got %v", err)
	}
	if _, err := a.handleAgentResume(ctx, gen.AgentResumeReq{ToAgentID: "Target#0001"}); err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("anon empty-caller resume: expected 'not trusted' deny, got %v", err)
	}
	if len(invocations) != 0 {
		t.Errorf("no target callable may be invoked for a denied empty caller, got %v", invocations)
	}
}

// TestConversableGrant_NotLiveCallerDenied verifies the closed-deny on
// resolution failure: a caller id that parses but resolves to no live actor
// is denied, with the error naming the allowed targets.
func TestConversableGrant_NotLiveCallerDenied(t *testing.T) {
	a, ctx := freshActorAnon(t)
	ownerID := genID()
	targetActorID := genID()
	ghostID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "Target#0001",
		ActorID:       targetActorID,
		ParentAgentID: ownerID,
		LoadState:     "loaded",
	}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == targetActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "message_read" {
					return domain.AgentMessageReadResp{}
				}
				return nil
			}), true
		}
		return nil, false // ghost caller does not resolve
	}

	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: "Target#0001", Text: "hi", CallerAgentID: ghostID}); err == nil {
		t.Fatal("expected not-live caller send to be rejected")
	} else if !strings.Contains(err.Error(), "not allowed to send/read messages") || !strings.Contains(err.Error(), "agent-chat:") {
		t.Errorf("not-live send: unexpected error %v", err)
	}
}

// TestConversableGrant_SelfCallerByActorIDPasses pins the appmanager
// plugin-agent default-target path: a non-developer caller whose
// CallerAgentId equals the target's own ActorID (a panel message delivered
// as the agent messaging itself) passes via the self allowance in
// requireConversableGrant, addressing the target by ActorID — and must
// short-circuit before any component_list grant verification (case (c)).
// The turn engine is not on the appmanager planner.Call path, so
// CallerAgentId arrives from the forwarded payload, not an injection.
func TestConversableGrant_SelfCallerByActorIDPasses(t *testing.T) {
	a, ctx := freshActorAnon(t)
	targetActorID := genID()
	a.Agents = []domain.AgentRef{{
		ID:            "PluginAssistant#0001",
		ActorID:       targetActorID,
		ParentAgentID: genID(),
		LoadState:     "loaded",
	}}
	invocations := map[string]int{}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			invocations[callID]++
			if callID == "component_list" {
				return domain.AgentComponentListResp{}
			}
			return nil
		}), true
	}

	// Exactly what fillDefaultPluginAgentTarget forwards: ToAgentId and
	// CallerAgentId both carry the plugin agent's actor id.
	if _, err := a.handleAgentSendMessage(ctx, domain.AgentMessageSendReq{ToAgentID: targetActorID, Text: "hi", CallerAgentID: targetActorID}); err != nil {
		t.Fatalf("self send by ActorID: %v", err)
	}
	if invocations["message_receive"] != 1 {
		t.Errorf("expected message_receive invoked once, got %d", invocations["message_receive"])
	}
	if invocations["component_list"] != 0 {
		t.Errorf("self caller must pass via the self allowance, component_list invoked %d times", invocations["component_list"])
	}
}
