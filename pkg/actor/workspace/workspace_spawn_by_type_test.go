package workspace

import (
	"errors"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// testForkSlot is a reusable model slot returned by the fake parent's
// resolve_child_slot; the workspace passes it through to project.spawn_agent as
// the child's Primary slot.
func testForkSlot() domain.ModelSlot {
	return domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: "unit", Unit: &domain.ModelUnit{Model: "child-model", Provider: "openai"}},
	}}
}

// spawnByTypeLookupFn builds a LookupIDFn for agent_spawn_by_type tests:
//
//   - callerAgentID resolves to a live parent that answers resolve_child_slot
//     with slotResp (capturing the request into slotSink when non-nil);
//   - projectID resolves to a project actor that answers project.spawn_agent,
//     capturing the request into spawnSink and returning childActorID, or
//     returning spawnErr when non-nil (failure path).
//
// This replaces the former fork_agent delegation mock: the workspace now spawns
// the child directly via project.spawn_agent and only consults the parent for
// slot resolution.
func spawnByTypeLookupFn(callerAgentID, projectID, childActorID string, slotResp agentactor.ResolveChildSlotResp,
	slotSink *agentactor.ResolveChildSlotReq, spawnSink *domain.ProjectSpawnAgentReq, spawnErr error) func(id.ActorID) (ref.Ref, bool) {
	return func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case callerAgentID:
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "resolve_child_slot" {
					if slotSink != nil {
						if req, ok := payload.(agentactor.ResolveChildSlotReq); ok {
							*slotSink = req
						}
					}
					return slotResp
				}
				return nil
			}), true
		case projectID:
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.spawn_agent" {
					if spawnSink != nil {
						if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
							*spawnSink = req
						}
					}
					if spawnErr != nil {
						return spawnErr
					}
					return domain.ProjectSpawnAgentResp{ActorID: childActorID}
				}
				return nil
			}), true
		}
		return nil, false
	}
}

// TestAgentSpawnByType_ListProjectionMatchesWorkerChain verifies that a fork
// child registered via agent_spawn_by_type flows through the exact same
// sidebar projection chain as a workflow worker: it lands in
// workspace.agentListState items with the parent project ID, and it is
// mounted under the parent's Children projection derived from ParentAgentID.
func TestAgentSpawnByType_ListProjectionMatchesWorkerChain(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, nil, nil)

	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "Explore",
		Prompt:        "p",
		CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnByType: %v", err)
	}

	state := a.buildAgentListState(true)
	var parent, child *gen.AgentListItem
	for i := range state.Items {
		switch state.Items[i].ActorID {
		case callerAgentID:
			parent = &state.Items[i]
		case childActorID:
			child = &state.Items[i]
		}
	}
	if child == nil {
		t.Fatal("fork child missing from agentListState items")
	}
	if child.ProjectID != projectID {
		t.Errorf("child ProjectId = %q, want %q (sidebar per-project filter key)", child.ProjectID, projectID)
	}
	if child.ParentAgentID != callerAgentID {
		t.Errorf("child ParentAgentId = %q, want %q", child.ParentAgentID, callerAgentID)
	}
	if parent == nil {
		t.Fatal("parent missing from agentListState items")
	}
	found := false
	for _, cr := range parent.Children {
		if cr.ActorID == childActorID {
			found = true
			break
		}
	}
	if !found {
		t.Error("fork child not mounted under parent Children projection")
	}
}

// TestHandleAgentSpawnByType_Success verifies the happy path: the workspace
// validates the request, resolves the live caller, calls resolve_child_slot on
// the parent for the model slot, invokes project.spawn_agent directly with the
// ParentAgentID/ParentTurnID/ParentStepID/ToolUseID payload, and registers the
// resulting child as a fork agent in workspace.Agents.
func TestHandleAgentSpawnByType_Success(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	var capturedSpawn domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, &capturedSpawn, nil)

	resp, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "Explore the codebase",
		Prompt:        "Find all entry points",
		MaxIterations: 3,
		ParentTurnID:  "turn-1",
		ParentStepID:  "step-1",
		ToolUseID:     "toolu-1",
		CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnByType: %v", err)
	}
	if resp.ChildActorID != childActorID {
		t.Errorf("expected ChildActorId %q, got %q", childActorID, resp.ChildActorID)
	}
	if resp.DisplayName == "" {
		t.Error("expected non-empty DisplayName")
	}

	// Verify the workspace delegated the actual spawn directly to project.spawn_agent.
	if capturedSpawn.AgentKind != "explorer" {
		t.Errorf("expected spawn AgentKind %q, got %q", "explorer", capturedSpawn.AgentKind)
	}
	if !strings.HasPrefix(capturedSpawn.SpawnName, "explorer-") || !strings.HasSuffix(capturedSpawn.SpawnName, "-toolu-1") {
		t.Errorf("expected SpawnName explorer-*-toolu-1, got %q", capturedSpawn.SpawnName)
	}
	if capturedSpawn.ParentAgentID != callerAgentID {
		t.Errorf("expected ParentAgentID %q, got %q", callerAgentID, capturedSpawn.ParentAgentID)
	}
	if capturedSpawn.ChildConfig == nil {
		t.Fatal("expected ChildConfig to be set")
	}
	if capturedSpawn.ChildConfig.ParentActorID != callerAgentID {
		t.Errorf("expected ChildConfig.ParentActorID %q, got %q", callerAgentID, capturedSpawn.ChildConfig.ParentActorID)
	}
	if capturedSpawn.ChildConfig.ParentTurnID != "turn-1" {
		t.Errorf("expected ParentTurnID %q, got %q", "turn-1", capturedSpawn.ChildConfig.ParentTurnID)
	}
	if capturedSpawn.ChildConfig.ParentStepID != "step-1" {
		t.Errorf("expected ParentStepID %q, got %q", "step-1", capturedSpawn.ChildConfig.ParentStepID)
	}
	if capturedSpawn.ChildConfig.ParentToolUseID != "toolu-1" {
		t.Errorf("expected ParentToolUseID %q, got %q", "toolu-1", capturedSpawn.ChildConfig.ParentToolUseID)
	}
	if capturedSpawn.ChildConfig.Task != "Explore the codebase" {
		t.Errorf("expected Task %q, got %q", "Explore the codebase", capturedSpawn.ChildConfig.Task)
	}
	if capturedSpawn.ChildConfig.Prompt != "Find all entry points" {
		t.Errorf("expected Prompt %q, got %q", "Find all entry points", capturedSpawn.ChildConfig.Prompt)
	}
	if capturedSpawn.ChildConfig.MaxIterations != 3 {
		t.Errorf("expected MaxIterations 3, got %d", capturedSpawn.ChildConfig.MaxIterations)
	}

	// Verify the child was registered as a persistent fork workspace agent.
	if len(a.Agents) != 2 {
		t.Fatalf("expected 2 agents in workspace.Agents (parent + child), got %d", len(a.Agents))
	}
	child := a.Agents[1]
	if child.ActorID != childActorID {
		t.Errorf("expected child ActorID %q, got %q", childActorID, child.ActorID)
	}
	if child.LifecycleScope != "fork" {
		t.Errorf("expected LifecycleScope %q, got %q", "fork", child.LifecycleScope)
	}
	if child.ParentAgentID != callerAgentID {
		t.Errorf("expected ParentAgentID %q, got %q", callerAgentID, child.ParentAgentID)
	}
	if child.ProjectID != projectID {
		t.Errorf("expected child to inherit parent ProjectID %q, got %q", projectID, child.ProjectID)
	}
	if child.AgentKind != "explorer" {
		t.Errorf("expected AgentKind %q, got %q", "explorer", child.AgentKind)
	}
}

// TestHandleAgentSpawnByType_NonWorkflowAgentForks verifies that a plain
// non-workflow agent (no ActiveWorkflowMapCardId) can still fork. This is the
// core migration requirement: fork_explore/fork_general/fork_review must
// remain available to any live agent, not just workflow orchestrators.
// TestHandleAgentSpawnByType_DevAppProjectAttachesNativeDevBundle verifies the
// workspace wiring for the auto-mounted plugin-dev bundle: when the
// parent's project mount is a dev-app (plugin) project, the spawn request
// must carry builtin:bundle:plugin-dev in ExtraBundleIDs so the child
// inherits the dev callables; regular projects must not.
func TestHandleAgentSpawnByType_DevAppProjectAttachesNativeDevBundle(t *testing.T) {
	run := func(t *testing.T, appKind string) domain.ProjectSpawnAgentReq {
		a, ctx := freshActorAnon(t)
		callerAgentID := genID()
		projectID := genID()
		childActorID := genID()

		a.Agents = []domain.AgentRef{
			{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
		}
		a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID, AppKind: appKind}}

		var capturedSpawn domain.ProjectSpawnAgentReq
		ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, &capturedSpawn, nil)

		_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
			AgentKind:     "general",
			Description:   "Help build the app",
			Prompt:        "Implement handlers",
			MaxIterations: 3,
			ParentTurnID:  "turn-1",
			ToolUseID:     "toolu-1",
			CallerAgentID: callerAgentID,
		})
		if err != nil {
			t.Fatalf("handleAgentSpawnByType: %v", err)
		}
		return capturedSpawn
	}

	t.Run("dev-app mount attaches the bundle", func(t *testing.T) {
		spawn := run(t, devAppDirName)
		if len(spawn.ExtraBundleIDs) != 1 || spawn.ExtraBundleIDs[0] != agentkit.PluginDevBundleID {
			t.Fatalf("expected ExtraBundleIDs [%s], got %v", agentkit.PluginDevBundleID, spawn.ExtraBundleIDs)
		}
	})

	t.Run("regular and sporeapp mounts attach nothing", func(t *testing.T) {
		for _, kind := range []string{"", sporeAppDirName, appDirName} {
			spawn := run(t, kind)
			if len(spawn.ExtraBundleIDs) != 0 {
				t.Errorf("appKind %q: expected no ExtraBundleIDs, got %v", kind, spawn.ExtraBundleIDs)
			}
		}
	})
}

func TestHandleAgentSpawnByType_NonWorkflowAgentForks(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, nil, nil)

	resp, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "general",
		Description:   "quick sub-task",
		Prompt:        "do something",
		CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("non-workflow agent fork must succeed, got: %v", err)
	}
	if resp.ChildActorID != childActorID {
		t.Errorf("expected ChildActorId %q, got %q", childActorID, resp.ChildActorID)
	}
}

// TestHandleAgentSpawnByType_ValidationErrors verifies each required field is
// validated before any cross-actor call.
func TestHandleAgentSpawnByType_ValidationErrors(t *testing.T) {
	a, ctx := freshActorAnon(t)
	cases := []struct {
		name string
		req  domain.WorkspaceAgentSpawnByTypeReq
	}{
		{"missing AgentKind", domain.WorkspaceAgentSpawnByTypeReq{
			Description: "task", CallerAgentID: "ignored"}},
		{"invalid AgentKind", domain.WorkspaceAgentSpawnByTypeReq{
			AgentKind: "bogus-kind", Description: "task", CallerAgentID: "ignored"}},
		{"empty Description", domain.WorkspaceAgentSpawnByTypeReq{
			AgentKind: "explorer", Description: "   ", CallerAgentID: "ignored"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.handleAgentSpawnByType(ctx, tc.req)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestHandleAgentSpawnByType_NoCallerAgentID verifies that an anonymous
// caller without CallerAgentId is rejected by requireLiveAgent before any
// cross-actor call. The turn engine always injects a non-empty CallerAgentId,
// so an empty value means a direct non-agent invocation bypassing the engine.
func TestHandleAgentSpawnByType_NoCallerAgentID(t *testing.T) {
	a, ctx := freshActorAnon(t)
	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:   "explorer",
		Description: "task",
	})
	if err == nil {
		t.Fatal("expected error for missing CallerAgentId, got nil")
	}
	if !strings.Contains(err.Error(), "CallerAgentId") {
		t.Fatalf("expected CallerAgentId error, got %v", err)
	}
}

// TestHandleAgentSpawnByType_ForgedCallerAgentIDRejected verifies that a
// forged CallerAgentId (a string that looks like an ID but does not resolve
// to a live actor) is rejected by requireLiveAgent. The turn engine
// overwrites CallerAgentId with the authoritative value, so a value that
// fails to resolve indicates either a stale or fabricated request.
func TestHandleAgentSpawnByType_ForgedCallerAgentIDRejected(t *testing.T) {
	a, ctx := freshActorAnon(t)
	forgedID := genID()

	// LookupIDFn returns false for all IDs — the forged ID resolves to nothing.
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }

	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "task",
		CallerAgentID: forgedID,
	})
	if err == nil {
		t.Fatal("expected error for forged/unresolvable CallerAgentId, got nil")
	}
	if !strings.Contains(err.Error(), "not live") {
		t.Fatalf("expected not-live error, got %v", err)
	}
}

// TestHandleAgentSpawnByType_InvalidCallerAgentID verifies that a malformed
// CallerAgentId (not a valid canonical ID) is rejected.
func TestHandleAgentSpawnByType_InvalidCallerAgentID(t *testing.T) {
	a, ctx := freshActorAnon(t)
	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "task",
		CallerAgentID: "not-a-valid-id",
	})
	if err == nil {
		t.Fatal("expected error for invalid CallerAgentId, got nil")
	}
	if !strings.Contains(err.Error(), "invalid CallerAgentId") {
		t.Fatalf("expected invalid CallerAgentId error, got %v", err)
	}
}

// TestHandleAgentSpawnByType_ProjectSpawnFailure verifies that when
// project.spawn_agent returns an error, it is propagated to the caller.
func TestHandleAgentSpawnByType_ProjectSpawnFailure(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	spawnErr := errors.New("project.spawn_agent: actor name collision")
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, "", agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, nil, nil, spawnErr)

	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "task",
		CallerAgentID: callerAgentID,
	})
	if err == nil {
		t.Fatal("expected project.spawn_agent error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "project.spawn_agent failed") {
		t.Fatalf("expected project.spawn_agent failed error, got %v", err)
	}
}

// TestHandleAgentSpawnByType_PropagatesModelUnit verifies that an explicit
// model unit override is threaded through to the parent agent's resolve_child_slot
// call.
func TestHandleAgentSpawnByType_PropagatesModelUnit(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	var capturedSlot agentactor.ResolveChildSlotReq
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, agentactor.ResolveChildSlotResp{Slot: testForkSlot()}, &capturedSlot, nil, nil)

	unit := &gen.ModelUnit{Model: "claude-sonnet", Provider: "anthropic"}
	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "general",
		Description:   "task",
		CallerAgentID: callerAgentID,
		Unit:          unit,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnByType: %v", err)
	}
	if capturedSlot.Unit == nil || capturedSlot.Unit.Model != "claude-sonnet" || capturedSlot.Unit.Provider != "anthropic" {
		t.Fatalf("expected unit claude-sonnet/anthropic, got %+v", capturedSlot.Unit)
	}
}

// TestHandleAgentSpawnByType_NonPrimarySlotsPassthrough verifies that the
// parent's non-primary slots (fast/execution/review/summary) returned by
// resolve_child_slot are copied into project.spawn_agent, while empty ([auto])
// slots are passed as nil so the fork child keeps the default [auto]. The
// primary slot (Slot) continues to flow through the unchanged path as Primary.
func TestHandleAgentSpawnByType_NonPrimarySlotsPassthrough(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	slotResp := agentactor.ResolveChildSlotResp{
		Slot: testForkSlot(),
		Fast: domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: "unit", Unit: &domain.ModelUnit{Model: "fast-model", Provider: "openai"}},
		}},
		// Execution stays empty ([auto]): must be passed through as nil.
		Review: domain.ModelSlot{Candidates: []domain.ModelRef{
			{Kind: "unit", Unit: &domain.ModelUnit{Model: "review-model", Provider: "openai"}},
		}},
		// Summary stays empty ([auto]): must be passed through as nil.
	}

	var capturedSpawn domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = spawnByTypeLookupFn(callerAgentID, projectID, childActorID, slotResp, nil, &capturedSpawn, nil)

	if _, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "explorer",
		Description:   "task",
		CallerAgentID: callerAgentID,
	}); err != nil {
		t.Fatalf("handleAgentSpawnByType: %v", err)
	}

	// Primary (resolved primary-purpose slot) keeps flowing through unchanged.
	if capturedSpawn.Primary == nil || len(capturedSpawn.Primary.Candidates) == 0 {
		t.Fatalf("Primary slot must be forwarded, got %+v", capturedSpawn.Primary)
	}
	if got := capturedSpawn.Primary.Candidates[0].Unit.Model; got != "child-model" {
		t.Errorf("Primary = %q, want child-model", got)
	}

	// Configured non-primary slots are passed through verbatim.
	if capturedSpawn.Fast == nil {
		t.Fatal("Fast slot must be passed through to project.spawn_agent, got nil")
	}
	if got := capturedSpawn.Fast.Candidates[0].Unit.Model; got != "fast-model" {
		t.Errorf("Fast = %q, want fast-model", got)
	}
	if capturedSpawn.Review == nil {
		t.Fatal("Review slot must be passed through to project.spawn_agent, got nil")
	}
	if got := capturedSpawn.Review.Candidates[0].Unit.Model; got != "review-model" {
		t.Errorf("Review = %q, want review-model", got)
	}

	// Empty ([auto]) slots are passed as nil so the child keeps default [auto].
	if capturedSpawn.Execution != nil {
		t.Errorf("Execution = %+v, want nil (empty [auto] slot)", capturedSpawn.Execution)
	}
	if capturedSpawn.Summary != nil {
		t.Errorf("Summary = %+v, want nil (empty [auto] slot)", capturedSpawn.Summary)
	}

	// The child AgentRef registered in workspace.Agents must carry Primary and
	// non-primary slots so the frontend composer shows the resolved model
	// instead of "Auto".
	child, found := a.findAgentByActorOrID(childActorID)
	if !found {
		t.Fatal("child agent not found in workspace.Agents")
	}
	if child.Primary == nil || len(child.Primary.Candidates) == 0 {
		t.Fatalf("child AgentRef Primary must be populated, got nil")
	}
	if got := child.Primary.Candidates[0].Unit.Model; got != "child-model" {
		t.Errorf("child AgentRef Primary = %q, want child-model", got)
	}
	if child.Fast == nil || child.Fast.Candidates[0].Unit.Model != "fast-model" {
		t.Errorf("child AgentRef Fast = %+v, want fast-model", child.Fast)
	}
	if child.Review == nil || child.Review.Candidates[0].Unit.Model != "review-model" {
		t.Errorf("child AgentRef Review = %+v, want review-model", child.Review)
	}
}

// TestHandleAgentSpawnByType_ReviewerPlanEvidence verifies that a reviewer
// spawn with PlanEvidence card IDs loads each plan card via
// project.wiki_get_card and embeds the raw bodies (plus explicit unavailable
// markers for failures) into the child's review prompt.
func TestHandleAgentSpawnByType_ReviewerPlanEvidence(t *testing.T) {
	a, ctx := freshActorAnon(t)
	callerAgentID := genID()
	projectID := genID()
	childActorID := genID()

	a.Agents = []domain.AgentRef{
		{ID: callerAgentID, ActorID: callerAgentID, ProjectID: projectID, DisplayName: "Parent", AgentKind: "coder"},
	}
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	var capturedSpawn domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case callerAgentID:
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "resolve_child_slot" {
					return agentactor.ResolveChildSlotResp{Slot: testForkSlot()}
				}
				return nil
			}), true
		case projectID:
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "project.spawn_agent":
					if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
						capturedSpawn = req
					}
					return domain.ProjectSpawnAgentResp{ActorID: childActorID}
				case "project.wiki_get_card":
					req, _ := payload.(domain.WikiGetCardReq)
					if req.ID == "plan-1" {
						return domain.WikiGetCardResp{ID: "plan-1", Raw: "plan body one"}
					}
					return errors.New("card not found")
				}
				return nil
			}), true
		}
		return nil, false
	}

	_, err := a.handleAgentSpawnByType(ctx, domain.WorkspaceAgentSpawnByTypeReq{
		AgentKind:     "reviewer",
		Description:   "review",
		Prompt:        "check the auth module",
		PlanEvidence:  []string{"plan-1", "plan-missing"},
		CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnByType: %v", err)
	}
	if capturedSpawn.ChildConfig == nil {
		t.Fatal("expected ChildConfig to be set")
	}
	prompt := capturedSpawn.ChildConfig.Prompt
	if !strings.Contains(prompt, "check the auth module") {
		t.Errorf("review text missing from prompt: %q", prompt)
	}
	if !strings.Contains(prompt, "plan body one") {
		t.Errorf("loaded plan card body missing from prompt: %q", prompt)
	}
	if !strings.Contains(prompt, `[plan card "plan-missing" unavailable`) {
		t.Errorf("unavailable marker for missing plan card missing from prompt: %q", prompt)
	}
}

// TestWorkspaceSpawnByType_SchemaRegistration verifies the generated schema
// types exist and are registered with correct IDs.
func TestWorkspaceSpawnByType_SchemaRegistration(t *testing.T) {
	if gen.WorkspaceAgentSpawnByTypeReqSchemaID != 3880 {
		t.Errorf("expected 3880, got %d", gen.WorkspaceAgentSpawnByTypeReqSchemaID)
	}
	if gen.WorkspaceAgentSpawnByTypeRespSchemaID != 3881 {
		t.Errorf("expected 3881, got %d", gen.WorkspaceAgentSpawnByTypeRespSchemaID)
	}
}

// TestWorkspaceSpawnByType_DomainAlias verifies the domain type aliases
// resolve to the generated types.
func TestWorkspaceSpawnByType_DomainAlias(t *testing.T) {
	var req domain.WorkspaceAgentSpawnByTypeReq
	req.AgentKind = "explorer"
	req.Description = "test"
	req.CallerAgentID = "agent-1"
	if req.AgentKind != "explorer" {
		t.Error("field assignment failed")
	}
	var resp domain.WorkspaceAgentSpawnByTypeResp
	resp.ChildActorID = "child-1"
	resp.DisplayName = "Explorer"
	if resp.ChildActorID != "child-1" {
		t.Error("field assignment failed")
	}
}
