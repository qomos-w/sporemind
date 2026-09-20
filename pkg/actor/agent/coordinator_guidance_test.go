package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// memPersist is an in-memory persist.Persist for guidance guard tests.
type memPersist struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemPersist() *memPersist { return &memPersist{data: map[string][]byte{}} }

func (m *memPersist) Load(name string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.data[name]
	if !ok {
		return persist.ErrNotExist
	}
	return json.Unmarshal(data, v)
}

func (m *memPersist) Save(name string, v any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.data[name] = data
	return nil
}

func (m *memPersist) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, name)
	return nil
}

func newGuidanceTestActor(store persist.Persist) *Actor {
	return &Actor{
		agentKind:     domain.AgentKindCoordinator,
		guidanceStore: store,
	}
}

func humanGuidanceCtx() *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Identity_.Subject = "user-1"
	return ctx
}

func TestHintStateNormalization(t *testing.T) {
	cases := []struct {
		name  string
		entry *domain.GuidanceCapabilityEntry
		want  string
	}{
		{name: "nil", entry: nil, want: guidanceHintStateIdle},
		{name: "empty string", entry: &domain.GuidanceCapabilityEntry{}, want: guidanceHintStateIdle},
		{name: "idle", entry: &domain.GuidanceCapabilityEntry{HintState: "idle"}, want: guidanceHintStateIdle},
		{name: "triggered", entry: &domain.GuidanceCapabilityEntry{HintState: "triggered"}, want: guidanceHintStateTriggered},
		{name: "dismissed", entry: &domain.GuidanceCapabilityEntry{HintState: "dismissed"}, want: guidanceHintStateDismissed},
		{name: "unknown value", entry: &domain.GuidanceCapabilityEntry{HintState: "bogus"}, want: guidanceHintStateIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hintState(tc.entry); got != tc.want {
				t.Fatalf("hintState = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClaimCoordinatorHint_OneShotPerAccount(t *testing.T) {
	store := newMemPersist()
	ctx := humanGuidanceCtx()
	a := newGuidanceTestActor(store)

	claimed, err := a.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall)
	if err != nil {
		t.Fatalf("first claim error: %v", err)
	}
	if !claimed {
		t.Fatal("first claim = false, want true")
	}

	// A second claim must be a no-op for the same account.
	claimed, err = a.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall)
	if err != nil {
		t.Fatalf("second claim error: %v", err)
	}
	if claimed {
		t.Fatal("second claim = true, want false")
	}

	// The persisted profile must carry the triggered hint entry.
	state, err := a.loadGuidance()
	if err != nil {
		t.Fatalf("loadGuidance error: %v", err)
	}
	profile, ok := state.Profiles["user-1"]
	if !ok {
		t.Fatal("profile for user-1 missing after claim")
	}
	found := false
	for _, entry := range profile.Capabilities {
		if entry.Capability == coordinatorHintFirstToolCall {
			found = true
			if entry.HintState != guidanceHintStateTriggered {
				t.Fatalf("hint state = %q, want %q", entry.HintState, guidanceHintStateTriggered)
			}
		}
	}
	if !found {
		t.Fatal("hint capability entry missing from profile")
	}

	// A fresh actor over the same store must also see triggered (persistence).
	a2 := newGuidanceTestActor(store)
	claimed, err = a2.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall)
	if err != nil {
		t.Fatalf("fresh actor claim error: %v", err)
	}
	if claimed {
		t.Fatal("fresh actor claim = true, want false (persisted guard)")
	}
}

func TestClaimCoordinatorHint_OtherCapabilityIndependent(t *testing.T) {
	store := newMemPersist()
	ctx := humanGuidanceCtx()
	a := newGuidanceTestActor(store)

	if claimed, err := a.claimCoordinatorHint(ctx, "hint.other"); err != nil || !claimed {
		t.Fatalf("claim hint.other = (%v, %v), want (true, nil)", claimed, err)
	}
	if claimed, err := a.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall); err != nil || !claimed {
		t.Fatalf("claim first-tool-call after other = (%v, %v), want (true, nil)", claimed, err)
	}
}

func TestDispatchFirstToolCallHint_NonCoordinatorSkips(t *testing.T) {
	ctx := humanGuidanceCtx()
	calls := 0
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "interfacemanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "interfacemanager.control" {
					calls++
				}
				return domain.InterfaceManagerControlResp{Accepted: true}, nil
			},
		}
	}
	a := newGuidanceTestActor(newMemPersist())
	a.agentKind = domain.AgentKindCoder
	a.dispatchFirstToolCallHint(ctx, domain.StepEvent{
		Kind:  "block.appended",
		Block: &domain.ContentBlock{Type: domain.ContentBlockToolResult},
	})
	if calls != 0 {
		t.Fatalf("non-coordinator dispatched %d control calls, want 0", calls)
	}
}

func TestDispatchFirstToolCallHint_OneShotAndEventGuarded(t *testing.T) {
	ctx := humanGuidanceCtx()
	store := newMemPersist()
	var controlCalls []domain.InterfaceManagerControlReq
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "interfacemanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID == "interfacemanager.control" {
					if req, ok := payload.(domain.InterfaceManagerControlReq); ok {
						controlCalls = append(controlCalls, req)
					}
				}
				return domain.InterfaceManagerControlResp{Accepted: true}, nil
			},
		}
	}
	a := newGuidanceTestActor(store)

	toolResult := domain.StepEvent{
		Kind:   "block.appended",
		TurnID: "turn-1",
		Block:  &domain.ContentBlock{Type: domain.ContentBlockToolResult, ToolUseID: "t1", Text: "ok"},
	}
	textBlock := domain.StepEvent{
		Kind:   "block.appended",
		TurnID: "turn-1",
		Block:  &domain.ContentBlock{Type: domain.ContentBlockText, Text: "hello"},
	}

	// Non-tool_result blocks never dispatch.
	a.dispatchFirstToolCallHint(ctx, textBlock)
	if len(controlCalls) != 0 {
		t.Fatalf("text block dispatched %d control calls, want 0", len(controlCalls))
	}

	// First tool_result dispatches exactly one show_guide.
	a.dispatchFirstToolCallHint(ctx, toolResult)
	if len(controlCalls) != 1 {
		t.Fatalf("first tool_result dispatched %d control calls, want 1", len(controlCalls))
	}
	if controlCalls[0].Action != "show_guide" || len(controlCalls[0].Steps) != 1 {
		t.Fatalf("control req = %+v, want single-step show_guide", controlCalls[0])
	}

	// Subsequent tool_results (same or later session) never dispatch again.
	a.dispatchFirstToolCallHint(ctx, toolResult)
	if len(controlCalls) != 1 {
		t.Fatalf("second tool_result dispatched %d control calls, want 1 (one-shot)", len(controlCalls))
	}
}

func TestDispatchFirstToolCallHint_InterfaceManagerDownDoesNotPanic(t *testing.T) {
	ctx := humanGuidanceCtx()
	// interfacemanager not resolvable: dispatch must degrade silently.
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	a := newGuidanceTestActor(newMemPersist())
	a.dispatchFirstToolCallHint(ctx, domain.StepEvent{
		Kind:  "block.appended",
		Block: &domain.ContentBlock{Type: domain.ContentBlockToolResult},
	})
	// Reaching here without panic is the assertion; the claim was still
	// persisted as triggered so the hint never retries.
	claimed, err := a.claimCoordinatorHint(ctx, coordinatorHintFirstToolCall)
	if err != nil {
		t.Fatalf("claim after degraded dispatch error: %v", err)
	}
	if claimed {
		t.Fatal("claim after degraded dispatch = true, want false (guard claimed regardless)")
	}
}

func TestCoordinatorGuidanceDecision(t *testing.T) {
	profile := &domain.GuidanceCapabilityProfile{Capabilities: []domain.GuidanceCapabilityEntry{{Capability: "workspace", Familiarity: 1}}}
	if got := coordinatorGuidanceDecision("Where is the project view?", profile); got != "help" {
		t.Fatalf("decision = %q, want help", got)
	}
	if got := coordinatorGuidanceDecision("build it", nil); got != "onboarding" {
		t.Fatalf("decision = %q, want onboarding", got)
	}
	if got := coordinatorGuidanceDecision("thanks", profile); got != "answer" {
		t.Fatalf("decision = %q, want answer", got)
	}
}

func TestCoordinatorGuidancePromptStable(t *testing.T) {
	profile := &domain.GuidanceCapabilityProfile{Capabilities: []domain.GuidanceCapabilityEntry{
		{Capability: "z", Familiarity: 2, Confidence: 0.5},
		{Capability: "a", Familiarity: 0, Confidence: 0.2},
	}}
	got := coordinatorGuidancePrompt(profile)
	if len(got) == 0 || got[0] != '<' {
		t.Fatalf("prompt = %q", got)
	}
	if len(got) < 80 || got[0:len("<user_guidance_profile>\na:")] != "<user_guidance_profile>\na:" {
		t.Fatalf("prompt ordering = %q", got)
	}
}

func TestApplyUsageSignal_NewEntry(t *testing.T) {
	got := applyUsageSignal(nil, "guide_completed", "", "2026-01-01T00:00:00Z")
	if got.Familiarity != 1 {
		t.Fatalf("familiarity = %d, want 1", got.Familiarity)
	}
	if got.Confidence != guidanceBehavioralConfidence {
		t.Fatalf("confidence = %v, want %v", got.Confidence, guidanceBehavioralConfidence)
	}
	if got.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("updatedAt = %q", got.UpdatedAt)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence len = %d, want 1", len(got.Evidence))
	}
}

func TestApplyUsageSignal_Increments(t *testing.T) {
	existing := &domain.GuidanceCapabilityEntry{
		Capability:  "git",
		Familiarity: 3,
		Confidence:  guidanceBehavioralConfidence,
		Evidence:    []string{"guide_completed:2026-01-01T00:00:00Z"},
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}
	got := applyUsageSignal(existing, "action_success", "", "2026-01-02T00:00:00Z")
	if got.Familiarity != 4 {
		t.Fatalf("familiarity = %d, want 4", got.Familiarity)
	}
	if len(got.Evidence) != 2 {
		t.Fatalf("evidence len = %d, want 2", len(got.Evidence))
	}
}

func TestApplyUsageSignal_CapsAtMax(t *testing.T) {
	existing := &domain.GuidanceCapabilityEntry{
		Capability:  "workspace",
		Familiarity: 5,
		Confidence:  guidanceBehavioralConfidence,
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}
	got := applyUsageSignal(existing, "action_success", "", "2026-01-02T00:00:00Z")
	if got.Familiarity != 5 {
		t.Fatalf("familiarity = %d, want 5 (capped)", got.Familiarity)
	}
}

func TestApplyUsageSignal_PreservesUserDeclared(t *testing.T) {
	existing := &domain.GuidanceCapabilityEntry{
		Capability:  "git",
		Familiarity: 5,
		Confidence:  0.95,
		Evidence:    []string{"user_declared"},
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}
	got := applyUsageSignal(existing, "action_success", "behavioral", "2026-01-02T00:00:00Z")
	if got.Familiarity != 5 {
		t.Fatalf("familiarity = %d, want 5 unchanged", got.Familiarity)
	}
	if got.Confidence != 0.95 {
		t.Fatalf("confidence = %v, want 0.95 (user-declared)", got.Confidence)
	}
	if len(got.Evidence) != 1 {
		t.Fatalf("evidence len = %d, want 1 (unchanged)", len(got.Evidence))
	}
}

func TestApplyUsageSignal_OverridesLowConfidence(t *testing.T) {
	existing := &domain.GuidanceCapabilityEntry{
		Capability:  "testing",
		Familiarity: 2,
		Confidence:  0.6,
		Evidence:    []string{"old"},
		UpdatedAt:   "2026-01-01T00:00:00Z",
	}
	got := applyUsageSignal(existing, "guide_completed", "", "2026-01-02T00:00:00Z")
	if got.Familiarity != 3 {
		t.Fatalf("familiarity = %d, want 3", got.Familiarity)
	}
	if got.Confidence != guidanceBehavioralConfidence {
		t.Fatalf("confidence = %v, want %v (downgraded to behavioral)", got.Confidence, guidanceBehavioralConfidence)
	}
}
