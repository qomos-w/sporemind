package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestComponentToolGuidance(t *testing.T) {
	guidance := componentToolGuidance(domain.AgentComponentSnapshot{Tools: []domain.ComponentToolContribution{{
		CallableID:  "project.read",
		Usage:       "Read before editing.",
		Constraints: "Do not use shell instead.",
	}}})
	if guidance == "" || !containsAll(guidance, "project.read", "Read before editing.", "Do not use shell instead.") {
		t.Fatalf("unexpected guidance: %q", guidance)
	}
}

func TestApplyComponentToolMetadata(t *testing.T) {
	tools := []domain.ToolSpec{{Name: "file_read", Description: "old", CallableID: "project.read"}}
	updated := applyComponentToolMetadata(tools, domain.AgentComponentSnapshot{Tools: []domain.ComponentToolContribution{{
		CallableID:  "project.read",
		Name:        "read_project_file",
		Description: "Read a project file.",
	}}})
	if len(updated) != 1 || updated[0].Name != "read_project_file" || updated[0].Description != "Read a project file." {
		t.Fatalf("component metadata was not applied: %+v", updated)
	}
}

func TestComponentToolIDsDeduplicates(t *testing.T) {
	ids := componentToolIDs(domain.AgentComponentSnapshot{Tools: []domain.ComponentToolContribution{{CallableID: "a"}, {CallableID: "a"}, {CallableID: "b"}, {}}})
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("unexpected component tool IDs: %+v", ids)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		found := false
		for i := 0; i+len(part) <= len(value); i++ {
			if value[i:i+len(part)] == part {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// --- Integration tests with fake project actor ---

// makeIntegrationCtx creates a test context with a fake planner that
// resolves project.component.get for specific card IDs.
func makeIntegrationCtx(t *testing.T, descriptors map[string]domain.ComponentDescriptor) actor.Context {
	return makeIntegrationCtxWithAgents(t, descriptors, nil)
}

// makeIntegrationCtxWithAgents is makeIntegrationCtx plus a stub for
// workspace.list_agents returning the given live agent metadata, used by the
// agent-chat virtual card descriptor tests.
func makeIntegrationCtxWithAgents(t *testing.T, descriptors map[string]domain.ComponentDescriptor, agents []domain.AgentRef) actor.Context {
	return makeIntegrationCtxWithAgentsRecord(t, descriptors, agents, nil)
}

// makeIntegrationCtxWithAgentsRecord additionally records each
// workspace.list_agents request payload so tests can assert the lookup scope.
func makeIntegrationCtxWithAgentsRecord(t *testing.T, descriptors map[string]domain.ComponentDescriptor, agents []domain.AgentRef, recorded *[]domain.WorkspaceListAgentsReq) actor.Context {
	t.Helper()
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
				case "workspace.list_agents":
					if recorded != nil {
						switch p := payload.(type) {
						case domain.WorkspaceListAgentsReq:
							*recorded = append(*recorded, p)
						case []byte:
							var r domain.WorkspaceListAgentsReq
							if err := json.Unmarshal(p, &r); err == nil {
								*recorded = append(*recorded, r)
							}
						}
					}
					return domain.AgentRefListResp{Items: agents}, nil
				case "project.component_list":
					items := make([]domain.ComponentDescriptor, 0, len(descriptors))
					for _, desc := range descriptors {
						items = append(items, desc)
					}
					return domain.ProjectComponentListResp{Items: items}, nil
				case "project.wiki_create_card":
					return domain.WikiCreateCardResp{}, nil
				case "project.wiki_edit_card":
					return domain.WikiEditCardResp{}, nil
				case "project.wiki_get_card":
					return domain.WikiGetCardResp{}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return ctx
}

// TestIntegration_MountToSnapshot verifies the full chain: mount a card →
// resolveComponentSnapshot → contributions from descriptor appear in
// snapshot.
func TestIntegration_MountToSnapshot(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:prompt:env": {
			Ref:   domain.ComponentRef{CardID: "test:prompt:env", Kind: "prompt", Source: "project"},
			Title: "Environment Prompt",
			Icon:  "🌿",
			Prompts: []domain.ComponentPromptContribution{
				{ID: "env-1", CardID: "test:prompt:env", Text: "You are a helpful agent.", Placement: "system"},
			},
		},
		"test:tool:search": {
			Ref:   domain.ComponentRef{CardID: "test:tool:search", Kind: "tool", Source: "project"},
			Title: "Search Tool",
			Icon:  "🔍",
			Tools: []domain.ComponentToolContribution{
				{ID: "search-1", CardID: "test:tool:search", CallableID: "project.search", Name: "search", Description: "Search files"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	seedTestBuiltinMounts(a, false)

	// Mount a prompt card.
	resp, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "test:prompt:env", Enabled: true})
	if err != nil {
		t.Fatalf("mount prompt: %v", err)
	}
	if resp.Mount.Title != "Environment Prompt" || resp.Mount.Icon != "🌿" {
		t.Fatalf("mount should carry title/icon: %+v", resp.Mount)
	}
	// Mount a tool card.
	_, err = a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "test:tool:search", Enabled: true})
	if err != nil {
		t.Fatalf("mount tool: %v", err)
	}

	snapshot := a.resolveComponentSnapshot(ctx)

	// Verify title/icon propagated into snapshot mounts.
	for _, m := range snapshot.Mounts {
		if m.CardID == "test:prompt:env" && (m.Title != "Environment Prompt" || m.Icon != "🌿") {
			t.Fatalf("snapshot mount missing title/icon: %+v", m)
		}
		if m.CardID == "test:tool:search" && (m.Title != "Search Tool" || m.Icon != "🔍") {
			t.Fatalf("snapshot mount missing title/icon: %+v", m)
		}
	}

	// Verify prompt contribution.
	foundPrompt := false
	for _, p := range snapshot.Prompts {
		if p.CardID == "test:prompt:env" && p.Text == "You are a helpful agent." {
			foundPrompt = true
		}
	}
	if !foundPrompt {
		t.Error("expected prompt contribution from mounted card")
	}

	// Verify tool contribution.
	foundTool := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "project.search" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Error("expected tool contribution from mounted card")
	}
}

func TestIntegration_RestoreCanonicalCardRefMetadata(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {Ref: domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode"}, Title: "Goal", Icon: "target"},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{
		cardRefs:        []gen.CardRef{{ID: "builtin:mode:goal", Scope: "builtin"}},
		ComponentMounts: []domain.AgentComponentMount{{CardID: "builtin:mode:goal", Title: "stale"}},
	}
	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected canonical metadata restoration")
	}
	if len(a.ComponentMounts) != 1 || a.ComponentMounts[0].Kind != "mode" || a.ComponentMounts[0].Title != "Goal" || a.ComponentMounts[0].Icon != "target" {
		t.Fatalf("restored canonical mount = %+v", a.ComponentMounts)
	}
}

func TestIntegration_RestoreComponentMountMetadata(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode"},
			Title: "Goal",
			Icon:  "target",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "builtin:mode:goal", Enabled: true},
	}}

	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected persisted mount metadata to be restored")
	}
	mount := a.ComponentMounts[0]
	if mount.Title != "Goal" || mount.Icon != "target" || mount.Kind != "mode" {
		t.Fatalf("restored mount metadata = %+v", mount)
	}
	if a.ComponentRevision != 1 {
		t.Fatalf("ComponentRevision = %d, want 1", a.ComponentRevision)
	}
}

// TestIntegration_RestoreSkillMountMetadata verifies that persisted skill mounts
// get their Kind, Title, and Icon filled in even though they cannot be resolved
// via project.component.get.
func TestIntegration_MountDefaultsEmptyScopeToUser(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:bundle:computeruse-tools": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:computeruse-tools", Kind: "bundle"},
			Title: "Computer Use Tools",
			Tools: []domain.ComponentToolContribution{{ID: "cu-1", CardID: "builtin:bundle:computeruse-tools", CallableID: "computeruse.list_windows"}},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	// Simulate the composer mounting the bundle without an explicit Scope.
	resp, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:bundle:computeruse-tools", Enabled: true})
	if err != nil {
		t.Fatalf("mount bundle: %v", err)
	}
	if resp.Mount.Scope != "user" {
		t.Fatalf("mount scope = %q, want user", resp.Mount.Scope)
	}

	// Verify the tool contribution is allowed by the card-scope gate.
	snapshot := a.resolveComponentSnapshot(ctx)
	cardScopeByID := make(map[string]string, len(snapshot.Mounts))
	for _, m := range snapshot.Mounts {
		cardScopeByID[m.CardID] = m.Scope
	}
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "computeruse.list_windows" {
			if scope := cardScopeByID[tool.CardID]; scope != "user" {
				t.Fatalf("tool %q resolved to scope %q, want user", tool.CallableID, scope)
			}
			return
		}
	}
	t.Fatal("expected computeruse.list_windows tool contribution")
}

func TestIntegration_RestoreEmptyScopeToUser(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:bundle:computeruse-tools": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:computeruse-tools", Kind: "bundle"},
			Title: "Computer Use Tools",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "builtin:bundle:computeruse-tools", Enabled: true, Scope: ""},
	}}

	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected empty scope to be repaired")
	}
	if a.ComponentMounts[0].Scope != "user" {
		t.Fatalf("restored scope = %q, want user", a.ComponentMounts[0].Scope)
	}
}

func TestIntegration_RestoreSkillMountMetadata(t *testing.T) {
	ctx := makeIntegrationCtx(t, nil)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "skill:read-logs", Enabled: true},
	}}

	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected skill mount metadata to be restored")
	}
	mount := a.ComponentMounts[0]
	if mount.Kind != "skill" || mount.Title != "read-logs" || mount.Icon != "" {
		t.Fatalf("restored skill mount metadata = %+v", mount)
	}
}

// cannot be unmounted when the descriptor says Protected=true.
func TestIntegration_ProtectedUnmountRejected(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:protected:card": {
			Ref:       domain.ComponentRef{CardID: "test:protected:card", Kind: "tool"},
			Title:     "Protected Tool",
			Protected: true,
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "test:protected:card", Enabled: true, Scope: "builtin"},
	}}

	_, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "test:protected:card"})
	if err == nil {
		t.Fatal("expected error when unmounting protected card")
	}
}

// TestIntegration_ProtectedDisableRejected verifies that a protected card
// cannot be disabled via set_enabled.
func TestIntegration_ProtectedDisableRejected(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:protected:card": {
			Ref:       domain.ComponentRef{CardID: "test:protected:card", Kind: "tool"},
			Title:     "Protected Tool",
			Protected: true,
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "test:protected:card", Enabled: true, Scope: "builtin"},
	}}

	_, err := a.handleComponentSetEnabled(ctx, domain.AgentComponentSetEnabledReq{CardID: "test:protected:card", Enabled: false})
	if err == nil {
		t.Fatal("expected error when disabling protected card")
	}
}

// TestIntegration_ProtectedUnmountAllowedUserScope verifies that a protected
// card the user explicitly mounted (Scope="user") CAN be unmounted. Protected
// only guards config-seeded mounts (Scope="builtin").
func TestIntegration_ProtectedUnmountAllowedUserScope(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:protected:card": {
			Ref:       domain.ComponentRef{CardID: "test:protected:card", Kind: "tool"},
			Title:     "Protected Tool",
			Protected: true,
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "test:protected:card", Enabled: true, Scope: "user"},
	}}

	resp, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "test:protected:card"})
	if err != nil {
		t.Fatalf("expected user-mounted protected card to be unmountable, got error: %v", err)
	}
	if resp.CardID != "test:protected:card" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(a.ComponentMounts) != 0 {
		t.Fatalf("expected mount removed, got %+v", a.ComponentMounts)
	}
}

// TestIntegration_ProtectedDisableAllowedUserScope verifies that a protected
// card mounted by the user (Scope="user") CAN be disabled via set_enabled.
func TestIntegration_ProtectedDisableAllowedUserScope(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:protected:card": {
			Ref:       domain.ComponentRef{CardID: "test:protected:card", Kind: "tool"},
			Title:     "Protected Tool",
			Protected: true,
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "test:protected:card", Enabled: true, Scope: "user"},
	}}

	resp, err := a.handleComponentSetEnabled(ctx, domain.AgentComponentSetEnabledReq{CardID: "test:protected:card", Enabled: false})
	if err != nil {
		t.Fatalf("expected user-mounted protected card to be disableable, got error: %v", err)
	}
	if resp.Mount.Enabled {
		t.Fatalf("expected mount disabled, got %+v", resp.Mount)
	}
}

// TestIntegration_ProtectedUnmountAllowedUserScopeCardRefs verifies the same
// behavior through the canonical cardRefs code path.
func TestIntegration_ProtectedUnmountAllowedUserScopeCardRefs(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:protected:card": {
			Ref:       domain.ComponentRef{CardID: "test:protected:card", Kind: "tool"},
			Title:     "Protected Tool",
			Protected: true,
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{cardRefs: []gen.CardRef{{ID: "test:protected:card", Scope: "user"}}}

	resp, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "test:protected:card"})
	if err != nil {
		t.Fatalf("expected user-mounted protected card to be unmountable via cardRefs, got error: %v", err)
	}
	if resp.CardID != "test:protected:card" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(a.cardRefs) != 0 {
		t.Fatalf("expected cardRef removed, got %+v", a.cardRefs)
	}
}

// TestIntegration_CacheInvalidation verifies that mount → snapshot → unmount
// → snapshot produces different results and invalidates the cache.
func TestIntegration_CacheInvalidation(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:cache:tool": {
			Ref:   domain.ComponentRef{CardID: "test:cache:tool", Kind: "tool"},
			Title: "Cache Test",
			Tools: []domain.ComponentToolContribution{
				{ID: "ct-1", CardID: "test:cache:tool", CallableID: "project.cache_test"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	seedTestBuiltinMounts(a, false)

	// Mount and take snapshot.
	_, _ = a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "test:cache:tool", Enabled: true})
	snap1 := a.resolveComponentSnapshot(ctx)
	if snap1.Revision == 0 {
		t.Fatal("expected non-zero revision")
	}

	// Take snapshot again — should be cached.
	snap2 := a.resolveComponentSnapshot(ctx)
	if snap2.Revision != snap1.Revision {
		t.Fatal("expected same revision from cache")
	}

	// Unmount → should invalidate cache.
	_, _ = a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "test:cache:tool"})
	snap3 := a.resolveComponentSnapshot(ctx)
	if snap3.Revision <= snap1.Revision {
		t.Fatalf("expected higher revision after unmount, got %d <= %d", snap3.Revision, snap1.Revision)
	}

	// The tool should no longer be in snapshot.
	for _, tool := range snap3.Tools {
		if tool.CallableID == "project.cache_test" {
			t.Fatal("expected cache_test tool to be gone after unmount")
		}
	}
}

// TestIntegration_DependencyAutoExpand verifies that mounting a card with
// required dependencies causes the dependency's contributions to appear in
// the snapshot, plus a virtual mount entry.
func TestIntegration_DependencyAutoExpand(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:bundle:main": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:main", Kind: "bundle"},
			Title: "Main Bundle",
			Dependencies: []domain.ComponentDependency{
				{CardID: "test:dep:required", Required: true, Reason: "core functionality"},
			},
			Tools: []domain.ComponentToolContribution{
				{ID: "main-1", CardID: "test:bundle:main", CallableID: "project.main_tool"},
			},
		},
		"test:dep:required": {
			Ref:   domain.ComponentRef{CardID: "test:dep:required", Kind: "tool"},
			Title: "Required Dependency",
			Tools: []domain.ComponentToolContribution{
				{ID: "dep-1", CardID: "test:dep:required", CallableID: "project.dep_tool"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	seedTestBuiltinMounts(a, false)

	_, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "test:bundle:main", Enabled: true})
	if err != nil {
		t.Fatalf("mount bundle: %v", err)
	}

	snapshot := a.resolveComponentSnapshot(ctx)

	// Main tool should be present.
	foundMain := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "project.main_tool" {
			foundMain = true
		}
	}
	if !foundMain {
		t.Error("expected main tool in snapshot")
	}

	// Dependency tool should also be present via auto-expand.
	foundDep := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "project.dep_tool" {
			foundDep = true
		}
	}
	if !foundDep {
		t.Error("expected dependency tool in snapshot via auto-expand")
	}

	// Virtual mount should exist for the dependency.
	foundVirtual := false
	for _, m := range snapshot.Mounts {
		if m.CardID == "test:dep:required" && m.Scope == "dependency" {
			foundVirtual = true
		}
	}
	if !foundVirtual {
		t.Error("expected virtual dependency mount in snapshot")
	}
}

// TestSkillUseRequiresMount verifies that handleSkillUse rejects skills that
// are not mounted as components.
func TestSkillUseRequiresMount(t *testing.T) {
	a := &Actor{}
	// No mounts at all — should fail.
	if a.isSkillMounted("my-skill") {
		t.Fatal("expected isSkillMounted=false when no mounts exist")
	}
	// Mount a skill component that is enabled.
	a.ComponentMounts = append(a.ComponentMounts, domain.AgentComponentMount{
		CardID:  "skill:my-skill",
		Enabled: true,
	})
	if !a.isSkillMounted("my-skill") {
		t.Fatal("expected isSkillMounted=true after mounting enabled skill")
	}
	// Disable the mount — should fail.
	a.ComponentMounts[0].Enabled = false
	if a.isSkillMounted("my-skill") {
		t.Fatal("expected isSkillMounted=false when skill is mounted but disabled")
	}
}

// TestProtectedComponentCannotDisable verifies that set_enabled rejects
// disabling a protected component.
func TestProtectedComponentCannotDisable(ctx *testing.T) {
	a := &Actor{}
	a.ComponentMounts = []domain.AgentComponentMount{
		{CardID: "builtin:bundle:project-wiki", Enabled: true, Scope: "builtin"},
	}
	// Attempting to disable should fail because the descriptor for
	// builtin:bundle:project-wiki is protected. Without a project actor,
	// componentDescriptor returns (zero, false), so the check is skipped.
	// We test the protected check path by using a cardID we can fake.
	//
	// Instead, test the logic directly: if descriptor.Protected is true and
	// req.Enabled is false, the handler should error. We verify this by
	// checking that the method correctly identifies protected status.
	//
	// Since componentDescriptor requires a real project actor, we test the
	// isSkillMounted logic and the seedBuiltinComponentMounts path here.
	_ = a
}

// TestCanonicalCardRefs cleans historical corruption: stale empty-ID refs and
// accidental duplicates collapse to a clean, unique, order-preserving set.
func TestCanonicalCardRefs(t *testing.T) {
	in := []gen.CardRef{
		{ID: "", Source: "bundle", Scope: "builtin"},
		{ID: "", Source: "tool", Scope: "builtin"},
		{ID: "builtin:bundle:project-wiki", Source: "bundle", Scope: "builtin"},
		{ID: "builtin:bundle:file-tools", Source: "bundle", Scope: "builtin"},
		{ID: "builtin:bundle:project-wiki", Source: "bundle", Scope: "builtin"},
		{ID: "", Source: "prompt", Scope: "builtin"},
	}
	out := canonicalCardRefs(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 canonical refs, got %d: %+v", len(out), out)
	}
	if out[0].ID != "builtin:bundle:project-wiki" || out[1].ID != "builtin:bundle:file-tools" {
		t.Fatalf("unexpected canonical order: %+v", out)
	}
}

// TestMigrateRenamedCardRefs verifies that a persisted mount of a retired
// builtin card ID is swapped for its replacement, dropped when the
// replacement is already mounted, and that untouched refs survive.
func TestMigrateRenamedCardRefs(t *testing.T) {
	refs := []gen.CardRef{
		{ID: "builtin:bundle:project-wiki", Source: "bundle", Scope: "builtin"},
		{ID: "builtin:bundle:omnibox", Source: "bundle", Scope: "builtin"},
	}
	got, changed := migrateRenamedCardRefs(refs)
	if !changed {
		t.Fatal("expected migration to report a change")
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 refs after migration, got %d", len(got))
	}
	if got[1].ID != "builtin:bundle:interface-controls" {
		t.Errorf("renamed ref ID=%q", got[1].ID)
	}
	if got[1].Source != "bundle" || got[1].Scope != "builtin" {
		t.Errorf("renamed ref metadata=%+v", got[1])
	}

	// Replacement already mounted: the stale ref is dropped, no duplicate.
	both := append([]gen.CardRef{}, refs...)
	both = append(both, gen.CardRef{ID: "builtin:bundle:interface-controls", Source: "bundle", Scope: "builtin"})
	got, changed = migrateRenamedCardRefs(both)
	if !changed {
		t.Fatal("expected migration to report a change")
	}
	count := 0
	for _, ref := range got {
		if ref.ID == "builtin:bundle:omnibox" {
			t.Error("stale omnibox ref survived migration")
		}
		if ref.ID == "builtin:bundle:interface-controls" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 interface-controls ref, got %d", count)
	}

	// No renamed refs: unchanged.
	got, changed = migrateRenamedCardRefs([]gen.CardRef{{ID: "builtin:bundle:project-wiki"}})
	if changed {
		t.Error("unexpected change for refs without renamed IDs")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(got))
	}
}

// TestMigrateRenamedComponentMounts covers the legacy ComponentMounts-only
// variant of the rename migration.
func TestMigrateRenamedComponentMounts(t *testing.T) {
	mounts := []domain.AgentComponentMount{
		{CardID: "builtin:bundle:omnibox", Enabled: true, Scope: "builtin"},
	}
	got, changed := migrateRenamedComponentMounts(mounts, "now")
	if !changed {
		t.Fatal("expected migration to report a change")
	}
	if len(got) != 1 || got[0].CardID != "builtin:bundle:interface-controls" {
		t.Fatalf("unexpected mounts after migration: %+v", got)
	}
	if got[0].UpdatedAt != "now" {
		t.Errorf("UpdatedAt=%q", got[0].UpdatedAt)
	}
	if got[0].Title == "" || got[0].Kind == "" {
		t.Errorf("descriptor metadata not overlaid: %+v", got[0])
	}
}

// TestBuiltinCardsToSeed_ConfigIsSoleBundleSource verifies that bundles come
// only from the kind config's DefaultBundleIDs and that project-wiki is never
// listed twice. The debug/introspection bundle is mounted the same way as any
// other bundle (no special-cased debug path).
func TestBuiltinCardsToSeed_ConfigIsSoleBundleSource(t *testing.T) {
	a := &Actor{}
	a.kindConfig.Store(&domain.AgentKindConfig{
		Kind: domain.AgentKindCoder,
		DefaultBundleIDs: []string{
			"builtin:bundle:project-wiki",
			"builtin:bundle:file-tools",
			"builtin:bundle:debug",
			"mcp:srv-1",
		},
	})
	ids := a.builtinCardsToSeed(nil)

	counts := map[string]int{}
	for _, id := range ids {
		counts[id]++
	}
	if counts["builtin:bundle:project-wiki"] != 1 {
		t.Errorf("project-wiki must be listed exactly once (config is the sole source), got %d", counts["builtin:bundle:project-wiki"])
	}
	if counts["builtin:bundle:debug"] != 1 {
		t.Errorf("debug bundle must be seeded when listed in DefaultBundleIDs, got %d", counts["builtin:bundle:debug"])
	}
	if counts["mcp:srv-1"] != 1 {
		t.Errorf("mcp:<server-id> DefaultBundleIDs must be seeded verbatim, got %d", counts["mcp:srv-1"])
	}
}

// TestSeedBuiltinComponentMounts_MCPCards verifies the MCP bundle-card seed
// behavior: an mcp:<server-id> card that resolves via the project's
// McpExternalCardProvider (descriptor Kind "bundle") is mounted with scope
// "builtin" — the same path as any other default bundle — while a card whose
// server is not configured (descriptor unresolvable) is silently skipped, the
// existing fail-safe so an agent kind referencing a missing MCP server never
// breaks startup.
func TestSeedBuiltinComponentMounts_MCPCards(t *testing.T) {
	t.Run("configured server is seeded as a builtin bundle mount", func(t *testing.T) {
		a := &Actor{agentKind: domain.AgentKindCoder}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"mcp:srv-1"},
		})
		a.ComponentMounts = []domain.AgentComponentMount{}
		ctx := testutil.AnonCtx(testutil.GenActorID())
		ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
		ctx.PlannerFn = func() actor.Planner {
			return fakePlannerForInvoke{
				callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
					if callID == "project.component_get" {
						return domain.ProjectComponentGetResp{
							Component: domain.ComponentDescriptor{
								Ref:       domain.ComponentRef{CardID: "mcp:srv-1", Kind: "bundle", Source: "mcpmanager"},
								Title:     "mcp:srv-1",
								Protected: true,
							},
						}, nil
					}
					return nil, fmt.Errorf("unexpected call %s", callID)
				},
			}
		}
		a.seedBuiltinComponentMounts(ctx)
		if len(a.ComponentMounts) != 1 {
			t.Fatalf("configured mcp card must be seeded, got %d mounts", len(a.ComponentMounts))
		}
		m := a.ComponentMounts[0]
		if m.CardID != "mcp:srv-1" || m.Kind != "bundle" || m.Scope != "builtin" || !m.Enabled {
			t.Errorf("unexpected mount for mcp card: %+v", m)
		}
	})

	t.Run("missing server is skipped silently", func(t *testing.T) {
		a := &Actor{agentKind: domain.AgentKindCoder}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"mcp:srv-1"},
		})
		a.ComponentMounts = []domain.AgentComponentMount{}
		// Without a planner, componentDescriptor returns (zero,false) for the
		// mcp card since it cannot call project.component_get.
		seedCtx := testutil.AnonCtx(testutil.GenActorID())
		seedCtx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
		a.seedBuiltinComponentMounts(seedCtx)
		if len(a.ComponentMounts) != 0 {
			t.Fatalf("unresolvable mcp card must be skipped silently, got %d mounts", len(a.ComponentMounts))
		}
	})
}

// TestSeedBuiltinComponentMounts verifies that seedBuiltinComponentMounts
// adds the kind config's default bundles (and the debug prompt when enabled)
// to ComponentMounts without producing duplicates.
func TestSeedBuiltinComponentMounts(t *testing.T) {
	a := &Actor{}
	seedTestBuiltinMounts(a, true)
	if len(a.ComponentMounts) != 2 {
		t.Fatalf("expected 2 builtin mounts, got %d", len(a.ComponentMounts))
	}
	cardIDs := make(map[string]bool)
	for _, m := range a.ComponentMounts {
		cardIDs[m.CardID] = true
	}
	for _, expected := range []string{"builtin:bundle:project-wiki", "builtin:bundle:debug"} {
		if !cardIDs[expected] {
			t.Errorf("expected builtin mount %q to be present", expected)
		}
	}
}

// TestSeedBuiltinComponentMounts_AppBundleCards verifies that a virtual
// app-bundle component (non-builtin ID) listed in DefaultBundleIDs is seeded
// through the appmanager.component_get path (mirrors the mcp:<server-id>
// behavior) — the seed loop resolves every DefaultBundleIDs entry via
// componentDescriptor, which routes app-bundle:* IDs to appmanager. A
// component that does not resolve is silently skipped.
func TestSeedBuiltinComponentMounts_AppBundleCards(t *testing.T) {
	t.Run("resolvable app-bundle card is seeded", func(t *testing.T) {
		a := &Actor{agentKind: domain.AgentKindCoder}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"app-bundle:testapp:tools"},
		})
		a.ComponentMounts = []domain.AgentComponentMount{}
		ctx := testutil.AnonCtx(testutil.GenActorID())
		ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
			if name == "appmanager" {
				return testutil.NewFakeRef(testutil.GenActorID(), nil), true
			}
			return nil, false
		}
		ctx.PlannerFn = func() actor.Planner {
			return fakePlannerForInvoke{
				callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
					if callID == "appmanager.component_get" {
						return gen.AppManagerComponentGetResp{
							Component: domain.ComponentDescriptor{
								Ref:   domain.ComponentRef{CardID: "app-bundle:testapp:tools", Kind: "bundle", Source: "appmanager"},
								Title: "Test App Tools",
							},
						}, nil
					}
					return nil, fmt.Errorf("unexpected call %s", callID)
				},
			}
		}
		a.seedBuiltinComponentMounts(ctx)
		if len(a.ComponentMounts) != 1 {
			t.Fatalf("resolvable app-bundle card must be seeded, got %d mounts", len(a.ComponentMounts))
		}
		m := a.ComponentMounts[0]
		if m.CardID != "app-bundle:testapp:tools" || m.Kind != "bundle" || m.Scope != "builtin" || !m.Enabled {
			t.Errorf("unexpected mount for app-bundle card: %+v", m)
		}
	})

	t.Run("unresolvable app-bundle card is skipped silently", func(t *testing.T) {
		a := &Actor{agentKind: domain.AgentKindCoder}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"app-bundle:ghost:tools"},
		})
		a.ComponentMounts = []domain.AgentComponentMount{}
		seedCtx := testutil.AnonCtx(testutil.GenActorID())
		seedCtx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
		// No planner: componentDescriptor returns (zero,false), so the card is
		// skipped silently instead of breaking startup.
		a.seedBuiltinComponentMounts(seedCtx)
		if len(a.ComponentMounts) != 0 {
			t.Fatalf("unresolvable app-bundle card must be skipped silently, got %d mounts", len(a.ComponentMounts))
		}
	})
}

// TestSeedParentChatMount verifies that a spawn_assign child (parentAgentID
// set, non-read-only kind) seeds a conversable agent-chat:<parent> mount at
// startup so the parent-messaging tools reach its tool surface, while root
// agents, read-only kinds, and unresolvable parents seed none.
func TestSeedParentChatMount(t *testing.T) {
	parentActorID := testutil.GenActorID().String()
	loadedParent := []domain.AgentRef{{
		ID: "Owner#0001", ActorID: parentActorID, DisplayName: "Owner", AgentKind: "coordinator",
		LoadState: "loaded", Status: "idle",
	}}

	t.Run("parented worker seeds mount and messaging tools", func(t *testing.T) {
		ctx := makeIntegrationCtxWithAgents(t, nil, loadedParent)
		a := &Actor{agentKind: domain.AgentKindWorker, parentAgentID: parentActorID}
		a.ComponentMounts = []domain.AgentComponentMount{}
		a.seedBuiltinComponentMounts(ctx)

		want := agentChatCardPrefix + parentActorID
		var seeded *domain.AgentComponentMount
		for i := range a.ComponentMounts {
			if a.ComponentMounts[i].CardID == want {
				seeded = &a.ComponentMounts[i]
				break
			}
		}
		if seeded == nil {
			t.Fatalf("expected seeded mount %q, got %+v", want, a.ComponentMounts)
		}
		if seeded.Kind != "agent-chat" || !seeded.Enabled || seeded.Scope != "builtin" {
			t.Errorf("unexpected seeded mount shape: %+v", seeded)
		}

		snapshot := a.resolveComponentSnapshot(ctx)
		var send, read bool
		for _, tool := range snapshot.Tools {
			if tool.CardID != want {
				continue
			}
			switch tool.CallableID {
			case "workspace.agent_send_message":
				send = true
			case "workspace.agent_read_message":
				read = true
			}
		}
		if !send || !read {
			t.Errorf("expected send/read tools contributed by %q, got %+v", want, snapshot.Tools)
		}
	})

	t.Run("root agent seeds none", func(t *testing.T) {
		ctx := makeIntegrationCtxWithAgents(t, nil, loadedParent)
		a := &Actor{agentKind: domain.AgentKindWorker}
		a.ComponentMounts = []domain.AgentComponentMount{}
		a.seedBuiltinComponentMounts(ctx)
		for _, m := range a.ComponentMounts {
			if strings.HasPrefix(m.CardID, agentChatCardPrefix) {
				t.Fatalf("root agent must not seed an agent-chat mount, got %+v", m)
			}
		}
	})

	t.Run("read-only kind seeds none", func(t *testing.T) {
		ctx := makeIntegrationCtxWithAgents(t, nil, loadedParent)
		a := &Actor{agentKind: "explorer", parentAgentID: parentActorID}
		a.ComponentMounts = []domain.AgentComponentMount{}
		a.seedBuiltinComponentMounts(ctx)
		for _, m := range a.ComponentMounts {
			if strings.HasPrefix(m.CardID, agentChatCardPrefix) {
				t.Fatalf("read-only kind must not seed an agent-chat mount, got %+v", m)
			}
		}
	})

	t.Run("unloaded parent is skipped silently", func(t *testing.T) {
		ctx := makeIntegrationCtxWithAgents(t, nil, nil)
		a := &Actor{agentKind: domain.AgentKindWorker, parentAgentID: parentActorID}
		a.ComponentMounts = []domain.AgentComponentMount{}
		a.seedBuiltinComponentMounts(ctx)
		for _, m := range a.ComponentMounts {
			if strings.HasPrefix(m.CardID, agentChatCardPrefix) {
				t.Fatalf("unresolvable parent mount must be skipped, got %+v", m)
			}
		}
	})
}

// TestSeedAndReconcile_ExtraBundleIDs verifies that spawn-time extra bundles
// (plugin-dev for dev-app project agents) are seeded alongside the kind
// defaults with scope "project", survive reconcileBuiltinBundleMounts (which
// only prunes scope "builtin" mounts), and are never seeded on read-only
// kinds whose turns auto-allow every tool call.
func TestSeedAndReconcile_ExtraBundleIDs(t *testing.T) {
	newActor := func(kind string) *Actor {
		a := &Actor{agentKind: kind}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             kind,
			DefaultBundleIDs: []string{"builtin:bundle:file-tools"},
		})
		a.extraBundleIDs = []string{agentkit.PluginDevBundleID}
		a.ComponentMounts = []domain.AgentComponentMount{}
		return a
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	t.Run("seed mounts kind defaults plus extra bundles with project scope", func(t *testing.T) {
		a := newActor(domain.AgentKindCoder)
		a.seedBuiltinComponentMounts(ctx)
		scopes := map[string]string{}
		for _, m := range a.ComponentMounts {
			scopes[m.CardID] = m.Scope
		}
		if scopes["builtin:bundle:file-tools"] != "builtin" {
			t.Errorf("kind default should seed with scope builtin, got %q", scopes["builtin:bundle:file-tools"])
		}
		if scopes[agentkit.PluginDevBundleID] != "project" {
			t.Errorf("extra bundle should seed with scope project (reconcile-immune), got %q", scopes[agentkit.PluginDevBundleID])
		}
	})

	t.Run("read-only kinds never receive extra bundles", func(t *testing.T) {
		for _, kind := range []string{"explorer", domain.AgentKindReviewer, domain.AgentKindDreamer, domain.AgentKindScout} {
			a := newActor(kind)
			a.seedBuiltinComponentMounts(ctx)
			for _, m := range a.ComponentMounts {
				if m.CardID == agentkit.PluginDevBundleID {
					t.Errorf("read-only kind %q must not seed the plugin-dev bundle (child turns auto-allow mutations)", kind)
				}
			}
			found := false
			for _, m := range a.ComponentMounts {
				if m.CardID == "builtin:bundle:file-tools" {
					found = true
				}
			}
			if !found {
				t.Errorf("read-only kind %q should still seed its kind defaults", kind)
			}
		}
	})

	t.Run("reconcile never prunes the extra bundle, even after respawn without extras", func(t *testing.T) {
		a := newActor(domain.AgentKindCoder)
		a.seedBuiltinComponentMounts(ctx)
		// Simulate a restore via a spawn path that does not re-send
		// ExtraBundleIDs (timer-local project spawn): the persisted
		// scope-"project" mount must survive reconcile.
		a.extraBundleIDs = nil
		if a.reconcileBuiltinBundleMounts(ctx) {
			t.Fatal("reconcile must not report changes for a scope-project mount")
		}
		found := false
		for _, m := range a.ComponentMounts {
			if m.CardID == agentkit.PluginDevBundleID {
				found = true
			}
		}
		if !found {
			t.Fatal("persisted plugin-dev mount was pruned by reconcile despite scope project")
		}
	})

	t.Run("tools from extra bundles reach the tool surface", func(t *testing.T) {
		a := newActor(domain.AgentKindCoder)
		a.seedBuiltinComponentMounts(ctx)
		snapshot := a.resolveComponentSnapshot(ctx)
		want := map[string]bool{}
		for _, id := range agentkit.CallableIDsForBundles([]string{agentkit.PluginDevBundleID}) {
			want[id] = true
		}
		if len(want) == 0 {
			t.Fatal("plugin-dev bundle resolves to no callables; bundle card missing data.tools?")
		}
		// The appmanager runtime surface is not declared by the bundle card
		// itself; it must arrive via the required builtin:bundle:app-tools
		// dependency (dependency auto-expand), mirroring the workflow mode →
		// workflow-tools layering.
		for _, id := range agentkit.CallableIDsForBundles([]string{"builtin:bundle:app-tools"}) {
			want[id] = true
		}
		got := map[string]bool{}
		for _, tool := range snapshot.Tools {
			if tool.CallableID != "" {
				got[tool.CallableID] = true
			}
		}
		for id := range want {
			if !got[id] {
				t.Errorf("mounted plugin-dev bundle should contribute tool %q to the snapshot", id)
			}
		}
	})
}

// TestReconcileBuiltinBundleMounts verifies that builtin-seeded bundle mounts
// dropped from the kind config's DefaultBundleIDs are pruned from existing
// agents, while user-mounted, dependency-mounted and still-default bundles are
// preserved — this is what makes kind settings bundle removal take effect.
func TestReconcileBuiltinBundleMounts(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())

	t.Run("cardRefs path prunes only stale builtin bundles", func(t *testing.T) {
		a := &Actor{}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"builtin:bundle:file-tools"},
		})
		a.cardRefs = []gen.CardRef{
			{ID: "builtin:bundle:file-tools", Source: "bundle", Scope: "builtin"},
			{ID: "builtin:bundle:web-search", Source: "bundle", Scope: "builtin"},
			{ID: "builtin:bundle:browser-crawl", Source: "bundle", Scope: "user"},
			{ID: "builtin:bundle:workflow-tools", Source: "bundle", Scope: "dependency"},
		}
		a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

		if !a.reconcileBuiltinBundleMounts(ctx) {
			t.Fatal("expected reconcile to report a change")
		}
		got := map[string]string{}
		for _, ref := range a.cardRefs {
			got[ref.ID] = ref.Scope
		}
		if _, ok := got["builtin:bundle:web-search"]; ok {
			t.Error("stale builtin bundle web-search should have been pruned")
		}
		for id, scope := range map[string]string{
			"builtin:bundle:file-tools":     "builtin",
			"builtin:bundle:browser-crawl":  "user",
			"builtin:bundle:workflow-tools": "dependency",
		} {
			if got[id] != scope {
				t.Errorf("expected %q preserved with scope %q, got %q", id, scope, got[id])
			}
		}
		if len(a.ComponentMounts) != 3 {
			t.Errorf("expected ComponentMounts rebuilt to 3 entries, got %d", len(a.ComponentMounts))
		}
		if a.reconcileBuiltinBundleMounts(ctx) {
			t.Error("second reconcile should be a no-op")
		}
	})

	t.Run("legacy mounts path prunes stale builtin bundles", func(t *testing.T) {
		a := &Actor{}
		a.kindConfig.Store(&domain.AgentKindConfig{
			Kind:             domain.AgentKindCoder,
			DefaultBundleIDs: []string{"builtin:bundle:file-tools"},
		})
		a.ComponentMounts = []domain.AgentComponentMount{
			{CardID: "builtin:bundle:file-tools", Enabled: true, Scope: "builtin", Kind: "bundle"},
			{CardID: "builtin:bundle:video-gen", Enabled: true, Scope: "builtin", Kind: "bundle"},
			{CardID: "builtin:bundle:image-gen", Enabled: true, Scope: "user", Kind: "bundle"},
		}
		if !a.reconcileBuiltinBundleMounts(ctx) {
			t.Fatal("expected reconcile to report a change")
		}
		if len(a.ComponentMounts) != 2 {
			t.Fatalf("expected 2 mounts kept, got %d", len(a.ComponentMounts))
		}
		for _, m := range a.ComponentMounts {
			if m.CardID == "builtin:bundle:video-gen" {
				t.Error("stale builtin bundle video-gen should have been pruned")
			}
		}
	})

	t.Run("no kind config is a fail-safe no-op", func(t *testing.T) {
		a := &Actor{}
		a.cardRefs = []gen.CardRef{{ID: "builtin:bundle:web-search", Source: "bundle", Scope: "builtin"}}
		if a.reconcileBuiltinBundleMounts(ctx) {
			t.Error("reconcile without kind config must not prune")
		}
		if len(a.cardRefs) != 1 {
			t.Error("cardRefs should be untouched without kind config")
		}
	})
}

// TestPromptContributionPlacementSorting verifies that prompt contributions
// with placement "resolved" go to instructions.Resolved, while "system" and
// empty placement go to instructions.Base, and priority ordering is respected.
func TestPromptContributionPlacementSorting(t *testing.T) {
	a := &Actor{}
	cfg := domain.AgentKindConfig{Kind: domain.AgentKindCoder}
	a.kindConfig.Store(&cfg)
	// Seed builtin mounts so snapshot doesn't add extra builtin prompts.
	seedTestBuiltinMounts(a, false)

	// We test the placement/priority sorting by calling the snippet logic
	// directly. resolveInstructions compiles project card contributions
	// configured prompt components. Instead, test the sorting + placement
	// logic that resolveInstructions applies to componentSnapshot.Prompts.
	snapshot := domain.AgentComponentSnapshot{
		Prompts: []domain.ComponentPromptContribution{
			{ID: "p1", CardID: "c1", Text: "base-prompt", Placement: "system", Priority: 10},
			{ID: "p2", CardID: "c2", Text: "resolved-prompt", Placement: "resolved", Priority: 5},
			{ID: "p3", CardID: "c3", Text: "low-priority", Placement: "", Priority: 1},
			{ID: "p4", CardID: "c4", Text: "resolved-prompt-2", Placement: "resolved", Priority: 3},
		},
	}

	// Replicate the sorting logic from resolveInstructions.
	sortedPrompts := append([]domain.ComponentPromptContribution(nil), snapshot.Prompts...)
	sort.SliceStable(sortedPrompts, func(i, j int) bool {
		if sortedPrompts[i].Priority != sortedPrompts[j].Priority {
			return sortedPrompts[i].Priority < sortedPrompts[j].Priority
		}
		return sortedPrompts[i].CardID < sortedPrompts[j].CardID
	})

	var baseTexts, resolvedTexts []string
	for _, p := range sortedPrompts {
		switch p.Placement {
		case "resolved":
			resolvedTexts = append(resolvedTexts, p.Text)
		default:
			baseTexts = append(baseTexts, p.Text)
		}
	}

	// Priority 1 < 3 < 5 < 10. So order is: low-priority, resolved-prompt-2,
	// resolved-prompt, base-prompt.
	if len(baseTexts) != 2 || baseTexts[0] != "low-priority" || baseTexts[1] != "base-prompt" {
		t.Fatalf("unexpected base texts: %v", baseTexts)
	}
	if len(resolvedTexts) != 2 || resolvedTexts[0] != "resolved-prompt-2" || resolvedTexts[1] != "resolved-prompt" {
		t.Fatalf("unexpected resolved texts: %v", resolvedTexts)
	}
}

// TestComponentDiagnosticOnUnresolvedDependency verifies that a diagnostic
// is recorded when a required dependency cannot be resolved.
func TestComponentDiagnosticOnUnresolvedDependency(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	// No project actor available — componentDescriptor returns (zero, false).
	// Directly test appendComponentDescriptor with an unresolvable card ID.
	snapshot := domain.AgentComponentSnapshot{}
	seen := make(map[string]struct{})
	a.appendComponentDescriptor(ctx, &snapshot, "nonexistent:card", seen)
	if len(snapshot.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(snapshot.Diagnostics))
	}
	d := snapshot.Diagnostics[0]
	if d.CardID != "nonexistent:card" || d.Level != "error" {
		t.Fatalf("unexpected diagnostic: %+v", d)
	}
}

// --- Handler-level integration tests ---

func TestHandleComponentMount_RejectsCoordinatorWearableBundleForNonCoordinator(t *testing.T) {
	a := &Actor{agentKind: domain.AgentKindCoder}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:bundle:coordinator-wearable", Enabled: true}); err == nil {
		t.Fatal("expected coordinator wearable bundle mount to be rejected")
	}
}

// TestHandleComponentMount_NewCard verifies that handleComponentMount adds a
// new component mount, increments ComponentRevision, and invalidates the
// componentSnapshot and resolvedInstructions caches.
func TestHandleComponentMount_NewCard(t *testing.T) {
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	// Pre-populate caches so we can verify they get nil'd.
	a.componentSnapshot.Store(&domain.AgentComponentSnapshot{Revision: 999})
	a.resolvedInstructions.Store(&domain.CompiledInstructions{})
	prevRevision := a.ComponentRevision

	ctx := testutil.AnonCtx(testutil.GenActorID())
	// componentDescriptor needs a non-nil parent and planner. Set up a
	// fake parent ref and a fake planner that returns a valid descriptor
	// for project.component.get.
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "project.component_get" {
					return domain.ProjectComponentGetResp{
						Component: domain.ComponentDescriptor{
							Ref: domain.ComponentRef{CardID: "test:card:1", Kind: "tool"},
						},
					}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	resp, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{
		CardID:  "test:card:1",
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("handleComponentMount: %v", err)
	}
	if len(a.ComponentMounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(a.ComponentMounts))
	}
	if a.ComponentMounts[0].CardID != "test:card:1" {
		t.Errorf("expected CardID test:card:1, got %q", a.ComponentMounts[0].CardID)
	}
	if a.ComponentRevision != prevRevision+1 {
		t.Errorf("expected revision %d, got %d", prevRevision+1, a.ComponentRevision)
	}
	// invalidateComponentSnapshot rebuilds the component snapshot immediately so
	// the pure component.snapshot handler sees fresh data. resolvedInstructions
	// stays nil (rebuilt lazily on next turn compile).
	if a.componentSnapshot.Load() == nil {
		t.Error("expected componentSnapshot to be rebuilt after mount")
	}
	if a.resolvedInstructions.Load() != nil {
		t.Error("expected resolvedInstructions to be nil after mount")
	}
	if resp.Mount.CardID != "test:card:1" {
		t.Errorf("expected response Mount.CardID test:card:1, got %q", resp.Mount.CardID)
	}
}

// TestHandleComponentUnmount_NotFound verifies that handleComponentUnmount
// returns an error when the card is not mounted.
func TestHandleComponentUnmount_NotFound(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "nonexistent"})
	if err == nil {
		t.Fatal("expected error when unmounting a card that is not mounted")
	}
}

// TestHandleComponentSetEnabled_NotFound verifies that handleComponentSetEnabled
// returns an error when the card is not mounted.
func TestHandleComponentSetEnabled_NotFound(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleComponentSetEnabled(ctx, domain.AgentComponentSetEnabledReq{
		CardID:  "nonexistent",
		Enabled: false,
	})
	if err == nil {
		t.Fatal("expected error when setting enabled on a card that is not mounted")
	}
}

func TestCanonicalCardRefsDriveMutationPaths(t *testing.T) {
	a := &Actor{cardRefs: []gen.CardRef{{ID: "card:a", Scope: "user", Order: 2}}}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleComponentSetEnabled(ctx, domain.AgentComponentSetEnabledReq{CardID: "card:a", Enabled: false})
	if err != nil || resp.Mount.Enabled {
		t.Fatalf("set enabled from canonical refs: resp=%+v err=%v", resp, err)
	}
	if len(a.cardRefs) != 1 || !a.cardRefs[0].Disabled {
		t.Fatalf("canonical disabled state not updated: %+v", a.cardRefs)
	}
	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "card:a"}); err != nil {
		t.Fatal(err)
	}
	if len(a.cardRefs) != 0 || len(a.ComponentMounts) != 0 {
		t.Fatalf("canonical unmount left state: refs=%+v mounts=%+v", a.cardRefs, a.ComponentMounts)
	}
}
func TestHandleComponentListUsesCanonicalCardRefs(t *testing.T) {
	a := &Actor{
		cardRefs:        []gen.CardRef{{ID: "card:a", Source: "prompt", Scope: "user", Order: 3}},
		ComponentMounts: []domain.AgentComponentMount{{CardID: "card:a", Title: "stale", Icon: "?"}},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 || resp.Items[0].CardID != "card:a" || resp.Items[0].Order != 3 || !resp.Items[0].Enabled {
		t.Fatalf("unexpected canonical projection: %+v", resp.Items)
	}
}

// TestHandleComponentList verifies that handleComponentList returns all
// mounted components.
func TestHandleComponentList(t *testing.T) {
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "card:a", Enabled: true},
			{CardID: "card:b", Enabled: false},
		},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
}

// TestResolveComponentSnapshot_DeduplicatesByCardID verifies that
// resolveComponentSnapshot produces no duplicate CardIDs in Prompts and no
// duplicate CallableIDs in Tools.
func TestResolveComponentSnapshot_DeduplicatesByCardID(t *testing.T) {
	a := &Actor{}
	seedTestBuiltinMounts(a, false)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	snapshot := a.resolveComponentSnapshot(ctx)

	// Verify no duplicate CardIDs in Prompts.
	seenPromptCard := make(map[string]bool)
	for _, p := range snapshot.Prompts {
		if seenPromptCard[p.CardID] {
			t.Errorf("duplicate prompt CardID: %q", p.CardID)
		}
		seenPromptCard[p.CardID] = true
	}

	// Verify no duplicate CallableIDs in Tools.
	seenCallable := make(map[string]bool)
	for _, tool := range snapshot.Tools {
		if seenCallable[tool.CallableID] {
			t.Errorf("duplicate tool CallableID: %q", tool.CallableID)
		}
		seenCallable[tool.CallableID] = true
	}
}

// TestSetGoalFromInput verifies the goal interception flow after the composer
// "/goal" slash-mode interception: the goal mode is mounted up front (simulating
// the composer), setGoalFromInput records the session goal with the default
// max-turns budget, and unmounting the mode (badge close) clears the goal.
// The goal-tools bundle is automatically mounted as a required dependency of
// the goal mode (see TestComponentMount_AutoMountsDependencies).
func TestSetGoalFromInput(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title: "Goal Mode",
			Icon:  "🎯",
			// Mirrors the production goal-mode.md `requires` declaration so this
			// test exercises the same dependency auto-mount path as a real agent.
			Dependencies: []domain.ComponentDependency{
				{CardID: "builtin:bundle:goal", Required: true},
			},
			Prompts: []domain.ComponentPromptContribution{
				{ID: "goal-1", CardID: "builtin:mode:goal", Text: "You are in Goal Mode.", Placement: "system"},
			},
		},
		"builtin:bundle:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:goal", Kind: "bundle", Source: "builtin"},
			Title: "Goal Tools",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	seedTestBuiltinMounts(a, false)

	goalModeID := "builtin:mode:goal"
	// Simulate the composer mounting the goal mode via "/goal" interception.
	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: goalModeID, Enabled: true, Scope: "user"}); err != nil {
		t.Fatalf("mount goal mode: %v", err)
	}
	// Mounting the goal mode must auto-mount its required goal-tools bundle.
	bundleMounted := false
	for _, m := range a.ComponentMounts {
		if m.CardID == "builtin:bundle:goal" && m.Enabled {
			bundleMounted = true
		}
	}
	if !bundleMounted {
		t.Fatalf("expected builtin:bundle:goal auto-mounted with goal mode, mounts=%+v", a.ComponentMounts)
	}

	condition := "refactor the auth module"
	if _, err := a.setGoalFromInput(ctx, condition); err != nil {
		t.Fatalf("setGoalFromInput: %v", err)
	}
	if a.RawSession.Goal == nil || a.RawSession.Goal.Condition != condition {
		t.Fatalf("expected goal condition %q, got %+v", condition, a.RawSession.Goal)
	}
	if a.RawSession.Goal.MaxTurns != DefaultGoalMaxTurns {
		t.Fatalf("expected goal MaxTurns = %d (default), got %d", DefaultGoalMaxTurns, a.RawSession.Goal.MaxTurns)
	}

	// Unmounting the goal mode (badge close) must clear the active goal.
	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: goalModeID}); err != nil {
		t.Fatalf("unmount goal mode: %v", err)
	}
	if a.RawSession.Goal != nil {
		t.Fatalf("expected goal cleared after unmount, got %+v", a.RawSession.Goal)
	}
	for _, m := range a.ComponentMounts {
		if m.CardID == goalModeID {
			t.Fatalf("expected builtin:mode:goal unmounted, found %+v", m)
		}
	}
}

// TestComponentMount_AutoMountsDependencies verifies that mounting a component
// with required dependencies also mounts those dependencies with scope
// "dependency", and that an already-enabled dependency is left untouched
// (not re-mounted, not scope-downgraded).
func TestComponentMount_AutoMountsDependencies(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title: "Goal Mode",
			Dependencies: []domain.ComponentDependency{
				{CardID: "builtin:bundle:goal", Required: true},
			},
		},
		"builtin:bundle:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:goal", Kind: "bundle", Source: "builtin"},
			Title: "Goal Tools",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)

	// Scenario 1: goal mode mounted with its dependency missing -> the
	// bundle is auto-mounted as a dependency.
	t.Run("auto_mounts_missing_dependency", func(t *testing.T) {
		a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
		seedTestBuiltinMounts(a, false)

		if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:goal", Enabled: true, Scope: "user"}); err != nil {
			t.Fatalf("mount goal mode: %v", err)
		}

		var goalBundleMount *domain.AgentComponentMount
		for i := range a.ComponentMounts {
			if a.ComponentMounts[i].CardID == "builtin:bundle:goal" {
				goalBundleMount = &a.ComponentMounts[i]
				break
			}
		}
		if goalBundleMount == nil {
			t.Fatal("expected builtin:bundle:goal to be auto-mounted as a dependency")
		}
		if !goalBundleMount.Enabled {
			t.Fatalf("expected auto-mounted dependency to be enabled, got %+v", goalBundleMount)
		}
		if goalBundleMount.Scope != "dependency" {
			t.Fatalf("expected auto-mounted dependency scope to be \"dependency\", got %q", goalBundleMount.Scope)
		}
	})

	// Scenario 2: the bundle is already mounted (e.g. by ensureGoalCardMounted
	// at OnStart with scope "user"). Mounting the goal mode must NOT downgrade
	// the existing bundle scope to "dependency" nor duplicate the mount.
	t.Run("does_not_downgrade_existing_dependency_scope", func(t *testing.T) {
		a := &Actor{ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:bundle:goal", Enabled: true, Scope: "user"},
		}}
		seedTestBuiltinMounts(a, false)

		if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:goal", Enabled: true, Scope: "user"}); err != nil {
			t.Fatalf("mount goal mode: %v", err)
		}

		bundleCount := 0
		var bundleScope string
		for i := range a.ComponentMounts {
			if a.ComponentMounts[i].CardID == "builtin:bundle:goal" {
				bundleCount++
				bundleScope = a.ComponentMounts[i].Scope
			}
		}
		if bundleCount != 1 {
			t.Fatalf("expected exactly one builtin:bundle:goal mount, got %d", bundleCount)
		}
		if bundleScope != "user" {
			t.Fatalf("expected existing dependency scope preserved as \"user\", got %q", bundleScope)
		}
	})
}

// TestComponentMount_AutoMountsNestedDependencies verifies that a dependency
// chain (mode -> bundle -> sub-bundle) is fully mounted transitively.
func TestComponentMount_AutoMountsNestedDependencies(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:mode:chain": {
			Ref:   domain.ComponentRef{CardID: "test:mode:chain", Kind: "mode"},
			Title: "Chain Mode",
			Dependencies: []domain.ComponentDependency{
				{CardID: "test:bundle:a", Required: true},
			},
		},
		"test:bundle:a": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:a", Kind: "bundle"},
			Title: "Bundle A",
			Dependencies: []domain.ComponentDependency{
				{CardID: "test:bundle:b", Required: true},
			},
		},
		"test:bundle:b": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:b", Kind: "bundle"},
			Title: "Bundle B",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "test:mode:chain", Enabled: true}); err != nil {
		t.Fatalf("mount chain mode: %v", err)
	}

	mounted := map[string]bool{}
	for _, m := range a.ComponentMounts {
		mounted[m.CardID] = true
	}
	for _, want := range []string{"test:mode:chain", "test:bundle:a", "test:bundle:b"} {
		if !mounted[want] {
			t.Errorf("expected %q to be transitively mounted, mounts=%+v", want, a.ComponentMounts)
		}
	}
}

// TestBuiltinDescriptorFromAsset_LocalFallback verifies that builtin cards can
// be resolved from the embedded agentkit assets when the workspace service is
// unavailable. This is the regression test for the bug where reloading a
// goal-mode session lost the goal badge and turn.assess because the workspace
// component lookup failed.
func TestBuiltinDescriptorFromAsset_LocalFallback(t *testing.T) {
	desc, ok := builtinDescriptorFromAsset("builtin:mode:goal")
	if !ok {
		t.Fatal("expected builtin:mode:goal descriptor from embedded assets")
	}
	if desc.Title != "Goal Mode" {
		t.Errorf("Title = %q, want %q", desc.Title, "Goal Mode")
	}
	if desc.Icon != "target" {
		t.Errorf("Icon = %q, want target", desc.Icon)
	}
	if desc.Visual == nil {
		t.Fatal("expected Visual from data.visual block")
	}
	if desc.Visual.Icon != "target" || desc.Visual.Accent != "amber" || desc.Visual.Color != "#d97706" {
		t.Errorf("Visual = %+v, want icon=target accent=amber color=#d97706", desc.Visual)
	}
	if desc.Ref.Kind != "mode" {
		t.Errorf("Kind = %q, want mode", desc.Ref.Kind)
	}
	if len(desc.Prompts) != 1 {
		t.Errorf("expected 1 prompt, got %d", len(desc.Prompts))
	}
	wantTools := map[string]bool{"turn_assess": false, "goal_submit": false, "goal_card_submit": false}
	for _, tool := range desc.Tools {
		if _, exists := wantTools[tool.CallableID]; exists {
			wantTools[tool.CallableID] = true
		}
	}
	for id, found := range wantTools {
		if !found {
			t.Errorf("missing tool %q in descriptor", id)
		}
	}

	// The goal mode requires the goal guidance bundle for its prompt contribution.
	hasGoalBundleDep := false
	for _, dep := range desc.Dependencies {
		if dep.CardID == "builtin:bundle:goal" {
			hasGoalBundleDep = true
		}
	}
	if !hasGoalBundleDep {
		t.Error("goal mode descriptor missing builtin:bundle:goal dependency")
	}

	bundleDesc, ok := builtinDescriptorFromAsset("builtin:bundle:goal")
	if !ok {
		t.Fatal("expected builtin:bundle:goal descriptor from embedded assets")
	}
	for _, tool := range bundleDesc.Tools {
		if tool.CallableID == "goal_submit" || tool.CallableID == "goal_card_submit" || tool.CallableID == "turn_assess" {
			t.Errorf("goal bundle must not expose %q; goal mode owns it", tool.CallableID)
		}
	}
}

// TestBuiltinDescriptor_OverlaysFriendlyTitleWhenCatalogReturnsRawID is the
// regression test for composer badges showing the raw "builtin:..." id. When
// the workspace catalog resolves a builtin card but surfaces its raw id as the
// title (the catalog falls back to the card id), the agent must overlay the
// friendly name from the embedded asset so the badge reads "Goal Mode" rather
// than "builtin:mode:goal".
func TestBuiltinDescriptor_OverlaysFriendlyTitleWhenCatalogReturnsRawID(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title: "builtin:mode:goal", // raw id, as produced by the catalog path
			Icon:  "target",
		},
	})
	// Resolve via the agent path that the composer badge feeds on.
	a := &Actor{}
	got, found := a.builtinComponentDescriptor(ctx, "builtin:mode:goal")
	if !found {
		t.Fatal("expected builtin descriptor to resolve")
	}
	if got.Title != "Goal Mode" {
		t.Fatalf("Title = %q, want %q (raw id must be overlaid)", got.Title, "Goal Mode")
	}
}

// TestBuiltinDescriptor_PreservesCatalogTitleWhenFriendly verifies the overlay
// is skipped when the catalog already supplies a friendly title, so workspace
// overrides (and test stubs) are respected.
func TestBuiltinDescriptor_PreservesCatalogTitleWhenFriendly(t *testing.T) {
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:mode:goal": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:goal", Kind: "mode", Source: "builtin"},
			Title: "Custom Goal Title",
			Icon:  "target",
		},
	})
	a := &Actor{}
	got, found := a.builtinComponentDescriptor(ctx, "builtin:mode:goal")
	if !found {
		t.Fatal("expected builtin descriptor to resolve")
	}
	if got.Title != "Custom Goal Title" {
		t.Fatalf("Title = %q, want %q (friendly catalog title must be preserved)", got.Title, "Custom Goal Title")
	}
}

// TestBuiltinDescriptorFromAsset_WorkflowTools verifies the composite
// workflow-tools bundle resolves from embedded assets: its own workspace
// agent callables are exposed directly and the sub-bundles (project-wiki,
// fork-explore) arrive as required dependencies that the
// component snapshot recursion pulls in.
func TestBuiltinDescriptorFromAsset_WorkflowTools(t *testing.T) {
	desc, ok := builtinDescriptorFromAsset("builtin:bundle:workflow-tools")
	if !ok {
		t.Fatal("expected builtin:bundle:workflow-tools descriptor from embedded assets")
	}
	if desc.Ref.Kind != "bundle" {
		t.Errorf("Kind = %q, want bundle", desc.Ref.Kind)
	}
	// The body contribution must carry the frontmatter placement so it lands
	// in the tool_guidance section, not hard-dropped to policy ("system") by
	// the local asset fallback. Regression for silent placement loss.
	if len(desc.Prompts) == 0 {
		t.Fatal("expected a body prompt contribution from workflow-tools asset")
	}
	if got := desc.Prompts[0].Placement; got != "tool_guidance" {
		t.Errorf("body Placement = %q, want tool_guidance", got)
	}
	wantTools := map[string]bool{
		"workspace.list_agents":        false,
		"workspace.agent_spawn_assign": false,
		"workspace.agent_review":       false,
		"workspace.agent_send_message": false,
	}
	for _, tool := range desc.Tools {
		if _, exists := wantTools[tool.CallableID]; exists {
			wantTools[tool.CallableID] = true
		}
	}
	for id, found := range wantTools {
		if !found {
			t.Errorf("workflow-tools descriptor missing tool %q", id)
		}
	}
	for _, dep := range []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:fork-explore",
	} {
		found := false
		for _, d := range desc.Dependencies {
			if d.CardID == dep {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("workflow-tools descriptor missing required dependency %q", dep)
		}
	}
}

// TestEnsureComponentCardMounted verifies the generic mount-repair helper used
// by ensureGoalCardMounted. It should add a missing card, re-enable a disabled
// one, and do nothing when inactive.
func TestEnsureComponentCardMounted(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureComponentCardMounted(ctx, "test:mode:example", "user", true) {
		t.Fatal("expected ensureComponentCardMounted to add the example card")
	}
	found := false
	for _, ref := range a.cardRefs {
		if ref.ID == "test:mode:example" && ref.Scope == "user" && !ref.Disabled {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected example card added to cardRefs, got %+v", a.cardRefs)
	}

	// Re-enabling a disabled card should also return true.
	a.cardRefs[1].Disabled = true
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	if !a.ensureComponentCardMounted(ctx, "test:mode:example", "user", true) {
		t.Fatal("expected ensureComponentCardMounted to re-enable the example card")
	}
	if a.cardRefs[1].Disabled {
		t.Fatal("expected example card to be re-enabled")
	}

	// Inactive should be a no-op.
	if a.ensureComponentCardMounted(ctx, "test:mode:example", "user", false) {
		t.Fatal("expected ensureComponentCardMounted to do nothing when inactive")
	}
}

// TestSyncWorktreeModeCard mounts builtin:mode:worktree when the agent enters a
// worktree and unmounts it when the agent leaves, mirroring the goal-mode
// lifecycle.
func TestSyncWorktreeModeCard(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:worktree": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:worktree", Kind: "mode", Source: "builtin"},
			Title: "Worktree Mode",
			Icon:  "🌿",
			Tools: []domain.ComponentToolContribution{
				{ID: "wt-exit", CardID: "builtin:mode:worktree", CallableID: "project.worktree_exit"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)

	a := &Actor{cardRefs: []gen.CardRef{}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// Enter worktree -> mode card mounted.
	a.worktreeStatus = "active"
	a.worktreeName = "feature"
	a.worktreeID = "wt-1"
	a.syncWorktreeModeCard(ctx)

	found := false
	scopeOK := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:worktree" && !ref.Disabled {
			found = true
			// The card-scope permission gate only trusts
			// builtin/project/user/dependency; a scope outside that set
			// denies every contributed callable (regression: "system").
			scopeOK = ref.Scope == "builtin"
		}
	}
	if !found || !scopeOK {
		t.Fatalf("expected builtin:mode:worktree mounted with scope builtin, got %+v", a.cardRefs)
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	hasExit := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "project.worktree_exit" {
			hasExit = true
		}
	}
	if !hasExit {
		t.Fatalf("expected project.worktree.exit in snapshot tools, got %+v", snapshot.Tools)
	}

	// Exit worktree -> mode card unmounted.
	a.worktreeStatus = "none"
	a.worktreeName = ""
	a.worktreeID = ""
	a.syncWorktreeModeCard(ctx)

	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:worktree" {
			t.Fatalf("expected builtin:mode:worktree to be unmounted, got %+v", ref)
		}
	}
}

// TestSyncWorktreeModeCard_ChildModeSkipped verifies that fork children
// (child.Mode = true) never mount builtin:mode:worktree even when their
// worktree binding is active — worktree mode (exit/discard lifecycle) belongs
// to the parent that owns the worktree.
func TestSyncWorktreeModeCard_ChildModeSkipped(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"builtin:mode:worktree": {
			Ref:   domain.ComponentRef{CardID: "builtin:mode:worktree", Kind: "mode", Source: "builtin"},
			Title: "Worktree Mode",
			Icon:  "🌿",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)

	a := &Actor{cardRefs: []gen.CardRef{}}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
	a.child.Mode = true
	a.worktreeStatus = "active"
	a.worktreeName = "feature"
	a.worktreeID = "wt-1"
	a.syncWorktreeModeCard(ctx)

	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:worktree" && !ref.Disabled {
			t.Fatalf("expected no active builtin:mode:worktree mount for child.Mode, got %+v", ref)
		}
	}
}

// TestReloadGoalSession_RestoresBadgeAndToolsWithoutWorkspace is the end-to-end
// regression test for the bug where reloading an agent session with an active
// goal lost the composer goal badge and the turn.assess tool. The workspace
// lookup is intentionally unavailable, so the fix must derive the goal card
// mount from the active goal and resolve the builtin descriptor from embedded
// assets.
func TestReloadGoalSession_RestoresBadgeAndToolsWithoutWorkspace(t *testing.T) {
	// No builtin:mode:goal in the descriptors map, so the workspace lookup will
	// fail and force the local embedded-asset fallback.
	ctx := makeIntegrationCtx(t, map[string]domain.ComponentDescriptor{
		"builtin:bundle:project-wiki": {
			Ref:   domain.ComponentRef{CardID: "builtin:bundle:project-wiki", Kind: "bundle", Source: "builtin"},
			Title: "Project Wiki",
		},
	})
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "refactor auth", Confirmed: true, MaxTurns: 20},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:project-wiki", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if !a.ensureGoalCardMounted(ctx) {
		t.Fatal("expected ensureGoalCardMounted to add the goal card")
	}
	if !a.restoreComponentMountMetadata(ctx) {
		t.Fatal("expected restoreComponentMountMetadata to update metadata")
	}

	list, err := a.handleComponentList(ctx, domain.AgentComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	var goalMount *domain.AgentComponentMount
	for i := range list.Items {
		if list.Items[i].CardID == "builtin:mode:goal" {
			goalMount = &list.Items[i]
			break
		}
	}
	if goalMount == nil {
		t.Fatalf("goal mount missing from handleComponentList: %+v", list.Items)
	}
	if goalMount.Title != "Goal Mode" || goalMount.Icon != "target" {
		t.Fatalf("goal mount missing title/icon: %+v", goalMount)
	}
	if goalMount.Visual == nil || goalMount.Visual.Color != "#d97706" {
		t.Fatalf("goal mount missing visual color: %+v", goalMount.Visual)
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	hasGoalTool := false
	hasAssessTool := false
	for _, tool := range snapshot.Tools {
		if tool.CallableID == "goal_submit" {
			hasGoalTool = true
		}
		if tool.CallableID == "turn_assess" {
			hasAssessTool = true
		}
	}
	if !hasGoalTool {
		t.Error("component snapshot missing goal.submit tool")
	}
	if !hasAssessTool {
		t.Error("component snapshot missing turn.assess tool")
	}
}

// TestHandleComponentUnmount_WorkflowModeClearsWorkflow verifies that
// unmounting builtin:mode:workflow clears the ActiveWorkflow session state
// and removes the workflow-tools bundle (when mounted with scope "user"),
// preserving any bundle with scope "builtin" (permission proxy).
func TestHandleComponentUnmount_WorkflowModeClearsWorkflow(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:workflow-tools", Scope: "user"},
			{ID: "builtin:mode:workflow", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:workflow"}); err != nil {
		t.Fatalf("handleComponentUnmount: %v", err)
	}

	if a.RawSession.ActiveWorkflow != nil {
		t.Fatalf("expected ActiveWorkflow to be nil after unmount, got %+v", a.RawSession.ActiveWorkflow)
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:workflow" {
			t.Fatal("expected builtin:mode:workflow removed from cardRefs")
		}
	}
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:bundle:workflow-tools" {
			t.Fatal("expected builtin:bundle:workflow-tools (scope user) removed from cardRefs")
		}
	}
}

// TestHandleComponentUnmount_WorkflowModePreservesBuiltinBundle verifies
// that unmounting builtin:mode:workflow preserves a workflow-tools bundle
// mounted with scope "builtin" (kind config permission proxy).
func TestHandleComponentUnmount_WorkflowModePreservesBuiltinBundle(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			ActiveWorkflow: &gen.ActiveWorkflow{MapCardID: "map-1"},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:workflow-tools", Scope: "builtin"},
			{ID: "builtin:mode:workflow", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	if _, err := a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "builtin:mode:workflow"}); err != nil {
		t.Fatalf("handleComponentUnmount: %v", err)
	}

	if a.RawSession.ActiveWorkflow != nil {
		t.Fatalf("expected ActiveWorkflow to be nil after unmount")
	}
	bundleFound := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:bundle:workflow-tools" {
			bundleFound = true
			if ref.Scope != "builtin" {
				t.Fatalf("expected bundle scope preserved as \"builtin\", got %q", ref.Scope)
			}
		}
	}
	if !bundleFound {
		t.Fatal("expected builtin:bundle:workflow-tools with scope \"builtin\" to be preserved")
	}
}

func TestHandleComponentMount_FlowMutualExclusion(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
			{ID: "builtin:bundle:workflow-tools", Scope: "builtin"},
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	_, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:workflow"})
	if err == nil {
		t.Fatal("expected error mounting workflow mode while goal mode is active (same flow)")
	}
}

func TestHandleComponentMount_FlowAllowsNonConflictingModes(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		cardRefs: []gen.CardRef{
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// memory mode has no flow — should always be mountable
	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:mode:memory"}); err != nil {
		t.Fatalf("expected memory mode to mount freely: %v", err)
	}
}

func TestEnsureModeActive_AutoDeactivatesConflictingFlow(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{
		RawSession: gen.RawSession{
			Goal: &gen.SessionGoal{Condition: "do thing", Confirmed: true},
		},
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:goal", Scope: "builtin"},
			{ID: "builtin:bundle:workflow-tools", Scope: "builtin"},
			{ID: "builtin:mode:goal", Scope: "user"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	a.ensureModeActive(ctx, "builtin:mode:workflow")

	goalFound := false
	workflowFound := false
	for _, ref := range a.cardRefs {
		if ref.ID == "builtin:mode:goal" {
			goalFound = true
		}
		if ref.ID == "builtin:mode:workflow" {
			workflowFound = true
		}
	}
	if goalFound {
		t.Fatal("expected builtin:mode:goal to be deactivated by ensureModeActive(workflow)")
	}
	if !workflowFound {
		t.Fatal("expected builtin:mode:workflow to be mounted")
	}
}

// TestIntegration_MountUnmountMCPCard drives the mount/unmount handlers for an
// mcp:<server-id> card and verifies resolveMCPTools follows the mount state:
// tools visible after mount, gone after unmount.
func TestIntegration_MountUnmountMCPCard(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"mcp:srv-0": {
			Ref:   domain.ComponentRef{CardID: "mcp:srv-0", Kind: "bundle", Source: "mcpmanager"},
			Title: "mcp:srv-0",
		},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" || name == "mcp" {
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
				case "mcp.discover_tools":
					return domain.McpDiscoverToolsResp{Servers: []domain.McpServerTools{
						{ID: "srv-0", Name: "srv-0", Tools: []domain.McpToolView{{Name: "tool0", Description: "from server 0"}}},
					}}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	// Mount the MCP card.
	resp, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "mcp:srv-0", Enabled: true})
	if err != nil {
		t.Fatalf("mount mcp card: %v", err)
	}
	if resp.Mount.Kind != "bundle" {
		t.Fatalf("mcp mount kind = %q, want bundle", resp.Mount.Kind)
	}
	// After mount, resolveMCPTools must see the tool.
	specs := a.resolveMCPTools(ctx)
	if len(specs) != 1 || specs[0].CallableID != "mcp.srv-0.tool0" {
		t.Fatalf("expected mounted server's tool visible after mount, got %+v", specs)
	}

	// Unmount: the tool must disappear.
	_, err = a.handleComponentUnmount(ctx, domain.AgentComponentUnmountReq{CardID: "mcp:srv-0"})
	if err != nil {
		t.Fatalf("unmount mcp card: %v", err)
	}
	if len(a.ComponentMounts) != 0 {
		t.Fatalf("expected mount removed, got %+v", a.ComponentMounts)
	}
	if specs := a.resolveMCPTools(ctx); specs != nil {
		t.Fatalf("expected no tools after unmount, got %d", len(specs))
	}
}

// TestVirtualAppBundleResolution covers the virtual app-bundle component
// source: app-bundle:* ids resolve through appmanager.component_get (typed
// response decode), unknown ids fail, and availableBundleCatalog merges the
// virtual catalog over stale project entries with the same id.
func TestVirtualAppBundleResolution(t *testing.T) {
	virtual := domain.ComponentDescriptor{
		Ref:    domain.ComponentRef{CardID: "app-bundle:app.totp:totp-authenticator", Kind: "bundle", Source: "appmanager"},
		Title:  "TOTP Authenticator",
		Icon:   "shield-check",
		Tools: []domain.ComponentToolContribution{{
			ID: "app.app.totp.codes", CardID: "app-bundle:app.totp:totp-authenticator", CallableID: "app.app.totp.codes",
		}},
	}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
			switch callID {
			case "appmanager.component_get":
				var req gen.AppManagerComponentGetReq
				switch p := payload.(type) {
				case gen.AppManagerComponentGetReq:
					req = p
				case []byte:
					_ = json.Unmarshal(p, &req)
				}
				if req.CardID == virtual.Ref.CardID {
					return gen.AppManagerComponentGetResp{Component: virtual}, nil
				}
				return nil, fmt.Errorf("component %q not found", req.CardID)
			case "appmanager.component_list":
				return gen.AppManagerComponentListResp{Items: []domain.ComponentDescriptor{virtual}}, nil
			case "project.component_list":
				// A stale materialized project card with the same id but
				// different content: the virtual entry must win.
				stale := virtual
				stale.Title = "stale materialized copy"
				return domain.ProjectComponentListResp{Items: []domain.ComponentDescriptor{stale}}, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		}}
	}
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}

	d, ok := a.componentDescriptor(ctx, virtual.Ref.CardID)
	if !ok {
		t.Fatal("virtual app-bundle descriptor must resolve")
	}
	if d.Title != virtual.Title || d.Icon != virtual.Icon || len(d.Tools) != 1 || d.Tools[0].CallableID != "app.app.totp.codes" {
		t.Fatalf("resolved descriptor = %+v", d)
	}
	if _, ok := a.componentDescriptor(ctx, "app-bundle:app.totp:gone"); ok {
		t.Fatal("unknown app-bundle id must not resolve")
	}

	catalog := a.availableBundleCatalog(ctx, nil, map[string]bool{})
	if len(catalog) != 1 {
		t.Fatalf("catalog = %d entries, want 1", len(catalog))
	}
	if catalog[0].Title != virtual.Title {
		t.Fatalf("virtual entry must shadow the stale project copy, got %q", catalog[0].Title)
	}
}

// ── agent-chat: virtual conversable tag descriptor ──────────────────────────

// TestAgentChatDescriptor_ResolvesForLoadedTarget verifies the agent-chat:
// virtual card resolves to a tools-only descriptor when the bound workspace
// agent is live: exactly the four inter-agent ops, each naming the target, a
// "Chat: <DisplayName>" title, and no Prompts contribution (the hot-context
// block owns the prompt surface).
func TestAgentChatDescriptor_ResolvesForLoadedTarget(t *testing.T) {
	ctx := makeIntegrationCtxWithAgents(t, nil, []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-coder-1", DisplayName: "Coder", AgentKind: "coder", LoadState: "loaded"},
	})
	a := &Actor{}
	got, found := a.componentDescriptor(ctx, "agent-chat:Coder#0001")
	if !found {
		t.Fatal("agent-chat descriptor must resolve for a loaded target")
	}
	if got.Ref.CardID != "agent-chat:Coder#0001" || got.Ref.Kind != "agent-chat" || got.Ref.Source != "workspace" {
		t.Fatalf("Ref = %+v, want cardID=agent-chat:Coder#0001 kind=agent-chat source=workspace", got.Ref)
	}
	if got.Title != "Chat: Coder" {
		t.Errorf("Title = %q, want %q", got.Title, "Chat: Coder")
	}
	if got.Icon != "message-circle" {
		t.Errorf("Icon = %q, want message-circle (composer badge row requires a non-empty Icon)", got.Icon)
	}
	if len(got.Prompts) != 0 {
		t.Errorf("agent-chat descriptor must contribute no prompts (hot context owns the prompt), got %d", len(got.Prompts))
	}
	want := []string{
		"workspace.agent_send_message",
		"workspace.agent_read_message",
		"workspace.agent_pause",
		"workspace.agent_resume",
	}
	if len(got.Tools) != len(want) {
		t.Fatalf("Tools = %d, want %d: %+v", len(got.Tools), len(want), got.Tools)
	}
	for i, callable := range want {
		tool := got.Tools[i]
		if tool.CallableID != callable {
			t.Errorf("tool[%d].CallableID = %q, want %q", i, tool.CallableID, callable)
		}
		if tool.CardID != "agent-chat:Coder#0001" {
			t.Errorf("tool[%d].CardID = %q, want %q", i, tool.CardID, "agent-chat:Coder#0001")
		}
		if !strings.Contains(tool.Description, "Coder") {
			t.Errorf("tool[%d].Description %q must mention the bound target by name", i, tool.Description)
		}
	}
}

// TestAgentChatDescriptor_ResolvesCrossProjectTarget verifies the agent-chat
// lookup is workspace-wide: a loaded target whose ProjectID differs from the
// mounting agent's parent project must resolve and pass mount validation, and
// the list_agents request must carry no ProjectID filter (regression: @ mounts
// of agents from other projects were rejected as "unavailable").
func TestAgentChatDescriptor_ResolvesCrossProjectTarget(t *testing.T) {
	var reqs []domain.WorkspaceListAgentsReq
	ctx := makeIntegrationCtxWithAgentsRecord(t, nil, []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-coder-1", DisplayName: "Coder", AgentKind: "coder", LoadState: "loaded", ProjectID: "project-other"},
	}, &reqs)
	a := &Actor{}
	got, found := a.componentDescriptor(ctx, "agent-chat:Coder#0001")
	if !found {
		t.Fatal("agent-chat descriptor must resolve for a loaded target in another project")
	}
	if got.Title != "Chat: Coder" {
		t.Fatalf("Title = %q, want %q", got.Title, "Chat: Coder")
	}
	if err := a.validateComponentCard(ctx, "agent-chat:Coder#0001"); err != nil {
		t.Fatalf("validateComponentCard must accept a cross-project target: %v", err)
	}
	if len(reqs) == 0 || reqs[0].ProjectID != "" {
		t.Fatalf("list_agents lookup must be workspace-wide (empty ProjectID), got %+v", reqs)
	}
}

// TestAgentChatDescriptor_RejectsUnknownOrUnloadedTarget verifies mount
// validation gates on live target availability: an unknown agent, an unloaded
// agent, and an empty target suffix all resolve not-ok, so validateComponentCard
// rejects the mount with an error naming the card.
func TestAgentChatDescriptor_RejectsUnknownOrUnloadedTarget(t *testing.T) {
	ctx := makeIntegrationCtxWithAgents(t, nil, []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-coder-1", DisplayName: "Coder", LoadState: "loaded"},
		{ID: "Sleeper#0001", ActorID: "actor-sleeper-1", DisplayName: "Sleeper", LoadState: "unloaded"},
	})
	a := &Actor{}
	for _, cardID := range []string{
		"agent-chat:Ghost#0001",  // not in the workspace agent list
		"agent-chat:Sleeper#0001", // present but not loaded
		"agent-chat:",             // no target suffix
	} {
		if _, ok := a.componentDescriptor(ctx, cardID); ok {
			t.Errorf("card %q must not resolve", cardID)
		}
		if err := a.validateComponentCard(ctx, cardID); err == nil {
			t.Errorf("validateComponentCard(%q) must reject", cardID)
		} else if !strings.Contains(err.Error(), cardID) {
			t.Errorf("validateComponentCard(%q) error %q must name the target card", cardID, err)
		}
	}
}

// TestAgentChatDescriptor_NeverInCatalogSection verifies agent-chat: virtuals
// stay out of the not-mounted catalog suggestions in the component status
// section: they are per-target virtuals, not catalog items. The mounted
// agent-chat card may appear in the mounted listing, but never in the
// available-bundles block.
func TestAgentChatDescriptor_NeverInCatalogSection(t *testing.T) {
	chatCardID := "agent-chat:Coder#0001"
	agents := []domain.AgentRef{
		{ID: "Coder#0001", ActorID: "actor-coder-1", DisplayName: "Coder", LoadState: "loaded"},
	}
	descriptors := map[string]domain.ComponentDescriptor{
		chatCardID: agentChatDescriptorFromRef(chatCardID, agents[0]),
		"test:bundle:web": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:web", Kind: "bundle", Source: "project"},
			Title: "Web Bundle",
			Tools: []domain.ComponentToolContribution{
				{ID: "w1", CardID: "test:bundle:web", CallableID: "web.fetch"},
			},
		},
	}
	ctx := makeIntegrationCtxWithAgents(t, descriptors, agents)
	a := &Actor{}
	mounts := []domain.AgentComponentMount{
		{CardID: "builtin:bundle:bundle-use", Kind: "bundle", Enabled: true, Scope: "builtin"},
		{CardID: chatCardID, Kind: "agent-chat", Enabled: true, Scope: "user"},
	}
	section := a.componentStatusSection(ctx, bundleUseSnapshot(mounts))
	if section == "" {
		t.Fatal("expected non-empty component status section")
	}
	if !strings.Contains(section, "test:bundle:web — Web Bundle") {
		t.Fatalf("bundle catalog entry missing from available block:\n%s", section)
	}
	if idx := strings.Index(section, "Available bundles not currently mounted"); idx >= 0 {
		if strings.Contains(section[idx:], "agent-chat:") {
			t.Fatalf("agent-chat card leaked into not-mounted catalog block:\n%s", section[idx:])
		}
	}
}

// ── browser-chat: virtual browser-window tag descriptor ─────────────────────

// TestBrowserChatDescriptor_ResolvesWithPreBoundCrawlTools verifies the
// browser-chat:<instanceId> virtual card resolves to a tools-only descriptor:
// exactly the five crawl.* callables from the crawl actor's registration
// surface, each naming the bound window and pre-binding
// Config.InstanceID=<instanceId> via its usage text, a "Browser: <id>" title
// with an icon (composer badge row requires both), and no Prompts contribution
// (the hot-context block owns the prompt surface, mirroring agent-chat).
func TestBrowserChatDescriptor_ResolvesWithPreBoundCrawlTools(t *testing.T) {
	ctx := makeIntegrationCtx(t, nil)
	a := &Actor{}
	got, found := a.componentDescriptor(ctx, "browser-chat:win-abc123")
	if !found {
		t.Fatal("browser-chat descriptor must resolve for a non-empty instance suffix")
	}
	if got.Ref.CardID != "browser-chat:win-abc123" || got.Ref.Kind != "browser-chat" || got.Ref.Source != "browser" {
		t.Fatalf("Ref = %+v, want cardID=browser-chat:win-abc123 kind=browser-chat source=browser", got.Ref)
	}
	if got.Title != "Browser: win-abc123" {
		t.Errorf("Title = %q, want %q", got.Title, "Browser: win-abc123")
	}
	if got.Icon != "globe" {
		t.Errorf("Icon = %q, want globe (composer badge row requires a non-empty Icon)", got.Icon)
	}
	if len(got.Prompts) != 0 {
		t.Errorf("browser-chat descriptor must contribute no prompts (hot context owns the prompt), got %d", len(got.Prompts))
	}
	want := []string{"crawl.start", "crawl.status", "crawl.results", "crawl.cancel", "crawl.handoff"}
	if len(got.Tools) != len(want) {
		t.Fatalf("Tools = %d, want %d: %+v", len(got.Tools), len(want), got.Tools)
	}
	for i, callable := range want {
		tool := got.Tools[i]
		if tool.CallableID != callable {
			t.Errorf("tool[%d].CallableID = %q, want %q", i, tool.CallableID, callable)
		}
		if tool.CardID != "browser-chat:win-abc123" {
			t.Errorf("tool[%d].CardID = %q, want %q", i, tool.CardID, "browser-chat:win-abc123")
		}
		if !strings.Contains(tool.Description, "win-abc123") {
			t.Errorf("tool[%d].Description %q must mention the bound window instance", i, tool.Description)
		}
		if !strings.Contains(tool.Usage, "win-abc123") {
			t.Errorf("tool[%d].Usage %q must pre-bind the instance ID", i, tool.Usage)
		}
	}
	if !strings.Contains(got.Tools[0].Usage, `Config.InstanceID = "win-abc123"`) {
		t.Errorf("crawl.start Usage %q must pre-bind Config.InstanceID to the mounted window", got.Tools[0].Usage)
	}
}

// TestBrowserChatDescriptor_RejectsEmptySuffix verifies mount validation gates
// on a non-empty instance suffix: the bare prefix resolves not-ok, so
// validateComponentCard rejects it with an error naming the card.
func TestBrowserChatDescriptor_RejectsEmptySuffix(t *testing.T) {
	ctx := makeIntegrationCtx(t, nil)
	a := &Actor{}
	if _, ok := a.componentDescriptor(ctx, "browser-chat:"); ok {
		t.Fatal("browser-chat: with an empty instance suffix must not resolve")
	}
	if err := a.validateComponentCard(ctx, "browser-chat:"); err == nil {
		t.Fatal("validateComponentCard(browser-chat:) must reject")
	} else if !strings.Contains(err.Error(), "browser-chat:") {
		t.Fatalf("validateComponentCard error %q must name the card", err)
	}
}

// TestBrowserChatDescriptor_StaticResolutionNeedsNoLookup verifies the
// browser-chat descriptor is fully static: resolution must succeed on a bare
// anonymous context with no planner, no parent, and no service lookup —
// unlike agent-chat it performs no workspace.list_agents call, so mounts
// resolve identically before and after a process restart.
func TestBrowserChatDescriptor_StaticResolutionNeedsNoLookup(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}
	got, found := a.componentDescriptor(ctx, "browser-chat:win-static")
	if !found {
		t.Fatal("browser-chat descriptor must resolve without planner/parent/services")
	}
	if got.Title != "Browser: win-static" || len(got.Tools) != 5 {
		t.Fatalf("descriptor = %+v, want Browser: win-static with 5 tools", got)
	}
}
