package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestCanonicalCardRefsExcludeKindConfig(t *testing.T) {
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "builtin:default", Kind: "card-ref", Scope: skillScopeKindConfig},
		{CardID: "skill:default", Kind: "skill", Scope: skillScopeKindConfig},
		{CardID: "builtin:user", Kind: "card-ref", Scope: "user"},
	}}
	a.syncCanonicalCardRefs()
	if len(a.cardRefs) != 1 || a.cardRefs[0].ID != "builtin:user" {
		t.Fatalf("unexpected canonical refs: %+v", a.cardRefs)
	}
}

func TestSyncDefaultCardRefs(t *testing.T) {
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{
		{CardID: "builtin:old", Kind: "card-ref", Scope: skillScopeKindConfig},
		{CardID: "skill:debug", Kind: "skill", Scope: skillScopeKindConfig},
		{CardID: "user:keep", Kind: "card-ref", Scope: "user"},
	}}
	cfg := domain.AgentKindConfig{DefaultCardRefs: []gen.CardRef{{ID: "builtin:new"}}}
	if !a.syncDefaultCardRefs(cfg) {
		t.Fatal("expected default card refs to change mounts")
	}
	if len(a.ComponentMounts) != 3 || a.ComponentMounts[0].CardID != "skill:debug" || a.ComponentMounts[1].CardID != "user:keep" || a.ComponentMounts[2].CardID != "builtin:new" {
		t.Fatalf("unexpected reconciled mounts: %+v", a.ComponentMounts)
	}
	if a.syncDefaultCardRefs(cfg) {
		t.Fatal("identical default refs should be stable")
	}
}

type skillPlannerFunc func(ctx context.Context, target ref.Ref, callID string, payload any) (any, error)

// newSkillTestCtx builds an actor.Context whose fake planner dispatches each registered callID to the provided func.
// Returns the ctx plus a pointer to the call log so tests can assert on RPC traffic.
func newSkillTestCtx(t *testing.T, fn skillPlannerFunc) (*testutil.FakeCtx, *[]string) {
	t.Helper()
	calls := make([]string, 0)
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "project" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(c context.Context, r ref.Ref, callID string, payload any) (any, error) {
				calls = append(calls, callID)
				return fn(c, r, callID, payload)
			},
		}
	}
	return ctx, &calls
}

// manifestPlanner returns a project wiki planner for skill card metadata and raw bodies.
func manifestPlanner(items []domain.SkillManifest, bodies map[string]string) skillPlannerFunc {
	return func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		switch callID {
		case "project.wiki_list_cards":
			cards := make([]domain.MonoCardListItem, 0, len(items))
			for _, item := range items {
				body := bodies[item.ID]
				raw := fmt.Sprintf("---\nname: %s\ntags: [%s]\ndata:\n  description: %q\n  tools: [%s]\n---\n\n%s", item.Name, strings.Join(item.Tags, ", "), item.Description, strings.Join(item.Tools, ", "), body)
				cards = append(cards, domain.MonoCardListItem{ID: "skill:" + item.ID, Tags: append([]string{"component", "skill"}, item.Tags...), Raw: raw})
			}
			return domain.WikiListCardsResp{Cards: cards}, nil
		case "project.wiki_get_card":
			var req domain.WikiGetCardReq
			switch p := payload.(type) {
			case domain.WikiGetCardReq:
				req = p
			case []byte:
				_ = json.Unmarshal(p, &req)
			}
			id := strings.TrimPrefix(req.ID, "skill:")
			for _, item := range items {
				if item.ID != id {
					continue
				}
				body := bodies[id]
				raw := fmt.Sprintf("---\nname: %s\ntags: [%s]\ndescription: %q\ntools: [%s]\n---\n\n%s", item.Name, strings.Join(item.Tags, ", "), item.Description, strings.Join(item.Tools, ", "), body)
				return domain.WikiGetCardResp{ID: req.ID, Raw: raw}, nil
			}
			if body, exists := bodies[id]; exists {
				return domain.WikiGetCardResp{ID: req.ID, Raw: fmt.Sprintf("---\nname: %s\ntags: [component, skill]\n---\n\n%s", id, body)}, nil
			}
			return nil, fmt.Errorf("no card for %q", id)
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	}
}

// findSkillMount returns the mount entry for the given skill cardID, plus whether it was found.
func findSkillMount(a *Actor, cardID string) (domain.AgentComponentMount, bool) {
	for _, m := range a.ComponentMounts {
		if m.CardID == cardID {
			return m, true
		}
	}
	return domain.AgentComponentMount{}, false
}

func TestSyncSkillMounts_AddsMatchingSkill(t *testing.T) {
	manifest := []domain.SkillManifest{
		{ID: "read-logs", Tags: []string{"debug"}},
		{ID: "grill-me", Tags: []string{"design"}},
	}
	ctx, calls := newSkillTestCtx(t, manifestPlanner(manifest, nil))
	a := &Actor{} // actorID empty → saveMailbox no-ops via key==""

	changed, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{
		Kind:     "agent",
		SkillIDs: []string{"debug"},
	})
	if err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first sync")
	}
	mount, ok := findSkillMount(a, "skill:read-logs")
	if !ok {
		t.Fatal("expected skill:read-logs to be mounted")
	}
	if !mount.Enabled {
		t.Error("expected mount to be Enabled")
	}
	if mount.Scope != skillScopeKindConfig {
		t.Errorf("expected Scope=%q, got %q", skillScopeKindConfig, mount.Scope)
	}
	if mount.Kind != "skill" {
		t.Errorf("expected Kind=skill, got %q", mount.Kind)
	}
	if mount.Title != "read-logs" {
		t.Errorf("expected Title=read-logs, got %q", mount.Title)
	}
	if mount.Icon != "" {
		t.Errorf("expected Icon to be empty, got %q", mount.Icon)
	}
	if _, ok := findSkillMount(a, "skill:grill-me"); !ok {
		t.Error("skill:grill-me should be mounted from the project card store")
	}
	if len(*calls) != 1 || (*calls)[0] != "project.wiki_list_cards" {
		t.Errorf("expected only wiki_list_cards call (no body fetches), got %v", *calls)
	}
}

func TestSyncSkillMounts_IdempotentAcrossAgentKindConfig(t *testing.T) {
	manifest := []domain.SkillManifest{
		{ID: "read-logs", Tags: []string{"debug"}},
		{ID: "grill-me", Tags: []string{"design"}},
	}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, nil))
	a := &Actor{}

	if _, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"debug"}}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if _, ok := findSkillMount(a, "skill:read-logs"); !ok {
		t.Fatal("expected read-logs mounted after first sync")
	}

	changed, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"design"}})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if changed {
		t.Fatal("expected no change when only workspace SkillIDs change")
	}
	if _, ok := findSkillMount(a, "skill:read-logs"); !ok {
		t.Error("expected skill:read-logs to remain mounted")
	}
	if _, ok := findSkillMount(a, "skill:grill-me"); !ok {
		t.Error("expected skill:grill-me to remain mounted")
	}
}

func TestSyncSkillMounts_Idempotent(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, nil))
	a := &Actor{}
	cfg := domain.AgentKindConfig{SkillIDs: []string{"debug"}}

	if _, err := a.syncSkillMounts(ctx, cfg); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	rev := a.ComponentRevision

	changed, err := a.syncSkillMounts(ctx, cfg)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if changed {
		t.Errorf("expected changed=false on idempotent re-sync; revision %d → %d", rev, a.ComponentRevision)
	}
	if a.ComponentRevision != rev {
		t.Errorf("revision should not change on idempotent sync: %d → %d", rev, a.ComponentRevision)
	}
	if n := countManagedSkillMounts(a); n != 1 {
		t.Errorf("expected exactly 1 managed skill mount, got %d", n)
	}
}

func TestSyncSkillMounts_PreservesUserMounts(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, nil))
	a := &Actor{
		// Pre-existing user-mounted skill that is NOT in the manifest and NOT in the
		// whitelist. syncSkillMounts must not touch it.
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "skill:user-picked", Enabled: true, Scope: "user"},
			// Also a non-skill mount to ensure we don't accidentally filter it.
			{CardID: "builtin:mode:goal", Enabled: true, Scope: "builtin"},
		},
	}

	changed, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"debug"}})
	if err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true (read-logs was added)")
	}
	if _, ok := findSkillMount(a, "skill:user-picked"); !ok {
		t.Error("user-mounted skill:user-picked must be preserved")
	}
	if m, ok := findSkillMount(a, "builtin:mode:goal"); !ok || !m.Enabled {
		t.Error("builtin:mode:goal must be preserved")
	}
	if _, ok := findSkillMount(a, "skill:read-logs"); !ok {
		t.Error("skill:read-logs should be added")
	}
}

// TestSyncSkillMounts_EnablesHandleSkillUse confirms the end-to-end contract: after
// syncSkillMounts runs for a skill that matches the whitelist, handleSkillUse no longer
// rejects with "not mounted" and returns the skill body.
func TestSyncSkillMounts_EnablesHandleSkillUse(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, map[string]string{
		"read-logs": "skill body for read-logs",
	}))
	a := &Actor{}

	if _, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"debug"}}); err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}

	// Before sync: handleSkillUse would reject with "not mounted". After sync: must succeed.
	resp, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "read-logs"})
	if err != nil {
		t.Fatalf("handleSkillUse after sync: %v", err)
	}
	if !strings.Contains(resp.Body, "read-logs") {
		t.Errorf("expected body to mention skill id, got %q", resp.Body)
	}
}

func countManagedSkillMounts(a *Actor) int {
	n := 0
	for _, m := range a.ComponentMounts {
		if m.Scope == skillScopeKindConfig && isSkillCardID(m.CardID) {
			n++
		}
	}
	return n
}

// TestFetchFilteredSkillManifest_IncludesWorkspaceBuiltinSkills verifies that
// builtin skills seeded into the workspace card store (not the project store)
// are discovered and included in the manifest with metadata parsed from the
// list response (not from full card body fetches).
func TestFetchFilteredSkillManifest_IncludesWorkspaceBuiltinSkills(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	workspaceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project":
			return projectRef, true
		case "workspace":
			return workspaceRef, true
		default:
			return nil, false
		}
	}

	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, r ref.Ref, callID string, payload any) (any, error) {
				switch callID {
				case "project.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{
						{ID: "skill:read-logs", Data: map[string]any{"description": "read log files"}},
					}}, nil
				case "workspace.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{
						{ID: "skill:skill-creator", Data: map[string]any{"description": "create new skills"}},
					}}, nil
				default:
					return nil, fmt.Errorf("unexpected call %s", callID)
				}
			},
		}
	}

	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills (1 project + 1 workspace), got %d: %+v", len(items), items)
	}

	byID := make(map[string]domain.SkillManifest, len(items))
	for _, m := range items {
		byID[m.ID] = m
	}

	if m, ok := byID["read-logs"]; !ok {
		t.Error("expected project skill read-logs in manifest")
	} else {
		if m.Name != "read-logs" {
			t.Errorf("read-logs name: got %q, want %q", m.Name, "read-logs")
		}
		if m.Description != "read log files" {
			t.Errorf("read-logs description: got %q, want %q", m.Description, "read log files")
		}
	}

	if m, ok := byID["skill-creator"]; !ok {
		t.Error("expected workspace skill skill-creator in manifest")
	} else {
		if m.Name != "skill-creator" {
			t.Errorf("skill-creator name: got %q, want %q", m.Name, "skill-creator")
		}
		if m.Description != "create new skills" {
			t.Errorf("skill-creator description: got %q, want %q", m.Description, "create new skills")
		}
	}
}

// TestHandleSkillInject_WorkspaceFallbackSkill verifies that handleSkillInject
// can resolve a workspace-only skill (not present in the project store) via the
// workspace card store fallback, enabling agent_skill_use for builtin skills.
func TestHandleSkillInject_WorkspaceFallbackSkill(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	workspaceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project":
			return projectRef, true
		case "workspace":
			return workspaceRef, true
		default:
			return nil, false
		}
	}

	skillBody := "---\ntitle: Skill Creator\ndescription: \"create skills\"\n---\n\nCreate a new skill."

	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, r ref.Ref, callID string, payload any) (any, error) {
				switch callID {
				case "project.wiki_get_card":
					// Project store does NOT have this skill.
					return domain.WikiGetCardResp{}, fmt.Errorf("not found")
				case "workspace.wiki_get_card":
					var req domain.WikiGetCardReq
					switch p := payload.(type) {
					case domain.WikiGetCardReq:
						req = p
					case []byte:
						_ = json.Unmarshal(p, &req)
					}
					if req.ID == "skill:skill-creator" {
						return domain.WikiGetCardResp{ID: req.ID, Raw: skillBody}, nil
					}
					return nil, fmt.Errorf("not found")
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	a := &Actor{}
	resp, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: "skill-creator"})
	if err != nil {
		t.Fatalf("handleSkillInject for workspace skill: %v", err)
	}
	if !strings.Contains(resp.Body, "Create a new skill") {
		t.Errorf("expected skill body, got %q", resp.Body)
	}
	if resp.SkillID != "skill-creator" {
		t.Errorf("expected SkillID=skill-creator, got %q", resp.SkillID)
	}
}

// TestFetchFilteredSkillManifest_WorkspaceBuiltinShadowsProjectResidue
// verifies that a builtin skill present in BOTH the project store (a stale
// residue) and the workspace (canonical) resolves to the workspace copy,
// because the workspace is collected first.
func TestFetchFilteredSkillManifest_WorkspaceBuiltinShadowsProjectResidue(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	workspaceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project":
			return projectRef, true
		case "workspace":
			return workspaceRef, true
		default:
			return nil, false
		}
	}

	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				switch callID {
				case "project.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{
						{ID: "skill:skill-creator", Data: map[string]any{"description": "stale project residue"}},
					}}, nil
				case "workspace.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: []domain.MonoCardListItem{
						{ID: "skill:skill-creator", Data: map[string]any{"description": "canonical workspace"}},
					}}, nil
				default:
					return nil, fmt.Errorf("unexpected call %s", callID)
				}
			},
		}
	}

	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 deduped skill, got %d: %+v", len(items), items)
	}
	if items[0].ID != "skill-creator" {
		t.Fatalf("expected skill-creator, got %q", items[0].ID)
	}
	if items[0].Description != "canonical workspace" {
		t.Errorf("builtin skill must resolve to workspace canonical, got description %q", items[0].Description)
	}
}

// TestGetCardFromStore_BuiltinPrefersWorkspace verifies that reading a builtin
// skill body prefers the workspace (canonical) copy even when a stale residue
// exists in the project store. A project-level (non-builtin) skill still
// resolves against the project store first.
func TestGetCardFromStore_BuiltinPrefersWorkspace(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	workspaceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project":
			return projectRef, true
		case "workspace":
			return workspaceRef, true
		default:
			return nil, false
		}
	}

	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				var req domain.WikiGetCardReq
				switch p := payload.(type) {
				case domain.WikiGetCardReq:
					req = p
				case []byte:
					_ = json.Unmarshal(p, &req)
				}
				switch callID {
				case "project.wiki_get_card":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "stale project body"}, nil
				case "workspace.wiki_get_card":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "canonical workspace body"}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	a := &Actor{}

	// Builtin skill: workspace canonical wins over the project residue.
	resp, err := a.getCardFromStore(ctx, "skill:skill-creator")
	if err != nil {
		t.Fatalf("getCardFromStore builtin: %v", err)
	}
	if resp.Raw != "canonical workspace body" {
		t.Errorf("builtin skill must return workspace body, got %q", resp.Raw)
	}
}

// TestHandleSkillMount_WritesComponentMount verifies that the agent.skill.mount
// callable (handleSkillInject) not only returns the skill body but also writes
// the skill into ComponentMounts so that UI and later handleSkillUse calls see
// a consistent mount state.
func TestHandleSkillMount_WritesComponentMount(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, calls := newSkillTestCtx(t, manifestPlanner(manifest, map[string]string{
		"read-logs": "skill body for read-logs",
	}))
	a := &Actor{}

	resp, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: "read-logs"})
	if err != nil {
		t.Fatalf("handleSkillInject: %v", err)
	}
	if !strings.Contains(resp.Body, "read-logs") {
		t.Errorf("expected body to contain skill id, got %q", resp.Body)
	}

	mount, ok := findSkillMount(a, "skill:read-logs")
	if !ok {
		t.Fatal("expected skill:read-logs to be written to ComponentMounts")
	}
	if mount.MountID == "" {
		t.Error("expected mount to have a MountID")
	}
	if resp.MountID != mount.MountID {
		t.Errorf("resp.MountID %q should match stored mount %q", resp.MountID, mount.MountID)
	}
	if mount.Scope != "user" {
		t.Errorf("expected Scope=user for explicit mount, got %q", mount.Scope)
	}
	if mount.Kind != "skill" {
		t.Errorf("expected Kind=skill, got %q", mount.Kind)
	}
	if mount.Title != "read-logs" {
		t.Errorf("expected Title=read-logs, got %q", mount.Title)
	}
	if mount.Icon != "" {
		t.Errorf("expected Icon to be empty for skill mount, got %q", mount.Icon)
	}
	if !mount.Enabled {
		t.Error("expected mount to be Enabled")
	}

	// Second call must be idempotent and not add a duplicate mount.
	prevCount := len(a.ComponentMounts)
	_, err = a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: "read-logs"})
	if err != nil {
		t.Fatalf("second handleSkillInject: %v", err)
	}
	if len(a.ComponentMounts) != prevCount {
		t.Errorf("expected idempotent mount, got %d mounts, want %d", len(a.ComponentMounts), prevCount)
	}

	// get_body should have been called twice; manifest.list is not needed here.
	if len(*calls) != 2 {
		t.Errorf("expected 2 get_body calls, got %v", *calls)
	}
}

// TestSynthesizeSkillMount_WritesComponentMount verifies that the slash command
// path (/read-logs) writes the skill into ComponentMounts even though it does
// not go through handleSkillUse.
func TestSynthesizeSkillMount_WritesComponentMount(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, map[string]string{
		"read-logs": "skill body for read-logs",
	}))
	a := &Actor{}

	turnName := "turn-1"
	userTurnID := "turn-user-1"
	stepID, err := a.synthesizeSkillMount(ctx, turnName, userTurnID, "read-logs", "")
	if err != nil {
		t.Fatalf("synthesizeSkillMount: %v", err)
	}
	if stepID == "" {
		t.Fatal("expected non-empty stepID")
	}
	if got := a.steps[len(a.steps)-1].TurnID; got != turnName {
		t.Fatalf("expected slash-skill step TurnID=%q, got %q", turnName, got)
	}

	if _, ok := findSkillMount(a, "skill:read-logs"); !ok {
		t.Fatal("expected slash skill mount to be written to ComponentMounts")
	}
	mount, _ := findSkillMount(a, "skill:read-logs")
	if mount.Title != "read-logs" {
		t.Errorf("expected Title=read-logs, got %q", mount.Title)
	}
	if mount.Icon != "" {
		t.Errorf("expected Icon to be empty, got %q", mount.Icon)
	}
}

func TestSynthesizeSkillMount_WarnsOnDuplicateUse(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, map[string]string{
		"read-logs": "skill body for read-logs",
	}))
	a := &Actor{}

	if _, err := a.synthesizeSkillMount(ctx, "turn-1", "turn-user-1", "read-logs", ""); err != nil {
		t.Fatalf("synthesizeSkillMount: %v", err)
	}

	resp, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "read-logs"})
	if err != nil {
		t.Fatalf("duplicate handleSkillUse: %v", err)
	}
	if !strings.Contains(resp.Warning, "already been used") {
		t.Errorf("expected duplicate warning, got %q", resp.Warning)
	}
	if resp.Body != "skill body for read-logs" {
		t.Errorf("expected duplicate use to reinject body, got %q", resp.Body)
	}
}

func TestHandleSkillUse_WarnsOnDuplicate(t *testing.T) {
	manifest := []domain.SkillManifest{{ID: "read-logs", Tags: []string{"debug"}}}
	ctx, _ := newSkillTestCtx(t, manifestPlanner(manifest, map[string]string{
		"read-logs": "skill body for read-logs",
	}))
	a := &Actor{}

	if _, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{SkillIDs: []string{"debug"}}); err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}

	// First use succeeds.
	if _, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "read-logs"}); err != nil {
		t.Fatalf("first handleSkillUse: %v", err)
	}

	resp, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "read-logs"})
	if err != nil {
		t.Fatalf("duplicate handleSkillUse: %v", err)
	}
	if !strings.Contains(resp.Warning, "already been used") {
		t.Errorf("expected duplicate warning, got %q", resp.Warning)
	}
	if resp.Body != "skill body for read-logs" {
		t.Errorf("expected duplicate use to reinject body, got %q", resp.Body)
	}
}

// TestHandleSkillInject_RejectsProjectMismatch verifies that a skill whose body
// declares a different owning ProjectID than the agent's parent project cannot
// be injected and is not written to ComponentMounts.
func TestHandleSkillInject_RejectsProjectMismatch(t *testing.T) {
	ctx, _ := newSkillTestCtx(t, func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
		if callID != "project.wiki_get_card" {
			return nil, fmt.Errorf("unexpected call %s", callID)
		}
		var req domain.WikiGetCardReq
		switch p := payload.(type) {
		case domain.WikiGetCardReq:
			req = p
		case []byte:
			_ = json.Unmarshal(p, &req)
		}
		return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: project-skill\nprojectId: project-other\n---\n\nskill body for project-skill"}, nil
	})
	// Agent's parent project differs from the skill's owning project.
	parentID := testutil.GenActorID()
	ctx.ParentRef = testutil.NewFakeRef(parentID, nil)

	a := &Actor{}
	_, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: "project-skill"})
	if err == nil {
		t.Fatal("expected project mismatch error")
	}
	if !strings.Contains(err.Error(), "scoped to project") {
		t.Errorf("expected 'scoped to project' error, got %q", err.Error())
	}
	if _, ok := findSkillMount(a, "skill:project-skill"); ok {
		t.Error("mismatched skill should not be written to ComponentMounts")
	}
}

// TestResolveComponentSnapshot_DoesNotInjectSkillBody verifies that mounted
// skills do NOT inject their full body into the component snapshot prompts.
// Skill bodies are loaded on-demand via agent_skill_use, not eagerly.
func TestResolveComponentSnapshot_DoesNotInjectSkillBody(t *testing.T) {
	ctx, _ := newSkillTestCtx(t, manifestPlanner(nil, map[string]string{
		"plan-module": "You are in structured planning mode.",
	}))
	a := &Actor{}

	if _, err := a.handleSkillInject(ctx, domain.AgentSkillMountReq{SkillID: "plan-module"}); err != nil {
		t.Fatalf("handleSkillInject: %v", err)
	}

	snapshot := a.resolveComponentSnapshot(ctx)

	for _, p := range snapshot.Prompts {
		if p.CardID == "skill:plan-module" {
			t.Fatalf("skill body should NOT be in snapshot.Prompts, got %q", p.Text)
		}
	}
}

// ---------------------------------------------------------------------------
// Skill override resolution: project-internal > system-internal > project-external
// ---------------------------------------------------------------------------

// manifestCard builds an internal "skill:<name>" list item carrying an explicit
// source and description so the manifest resolver can classify it.
func manifestCard(name, source, desc string) domain.MonoCardListItem {
	return domain.MonoCardListItem{
		ID:     "skill:" + name,
		Source: source,
		Data:   map[string]any{"description": desc, "source": source},
	}
}

// extCard builds an external "ext-skill:<source>:<name>" list item.
func extCard(source, name, desc string) domain.MonoCardListItem {
	return domain.MonoCardListItem{
		ID:     "ext-skill:" + source + ":" + name,
		Source: source,
		Data:   map[string]any{"description": desc, "source": source},
	}
}

// overrideCtx builds a context whose fake planner serves explicit project and
// workspace card lists for wiki_list_all_cards, plus a bodies map for
// wiki_get_card (used by the mount/use path). Both services are resolvable.
func overrideCtx(t *testing.T, projectCards, workspaceCards []domain.MonoCardListItem, bodies map[string]string) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "project", "workspace":
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		default:
			return nil, false
		}
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				switch callID {
				case "project.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: projectCards}, nil
				case "workspace.wiki_list_cards":
					return domain.WikiListCardsResp{Cards: workspaceCards}, nil
				case "project.wiki_get_card", "workspace.wiki_get_card":
					var req domain.WikiGetCardReq
					switch p := payload.(type) {
					case domain.WikiGetCardReq:
						req = p
					case []byte:
						_ = json.Unmarshal(p, &req)
					}
					if body, ok := bodies[req.ID]; ok {
						return domain.WikiGetCardResp{ID: req.ID, Raw: body}, nil
					}
					return nil, fmt.Errorf("no card for %q", req.ID)
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return ctx
}

func manifestByID(items []domain.SkillManifest) map[string]domain.SkillManifest {
	m := make(map[string]domain.SkillManifest, len(items))
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

// M1: an external skill not shadowed by any same-named internal skill appears in
// the manifest with its full ext-skill: ID.
func TestManifest_IncludesExternalSkill(t *testing.T) {
	ctx := overrideCtx(t, []domain.MonoCardListItem{extCard("claude", "foo", "external foo")}, nil, nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	byID := manifestByID(items)
	m, ok := byID["ext-skill:claude:foo"]
	if !ok {
		t.Fatalf("expected ext-skill:claude:foo in manifest, got %+v", items)
	}
	if m.Name != "foo" {
		t.Errorf("Name = %q, want foo", m.Name)
	}
	if m.Description != "external foo" {
		t.Errorf("Description = %q, want external foo", m.Description)
	}
}

// M2: a project-internal skill shadows a same-named external skill.
func TestManifest_ProjectOverridesExternal(t *testing.T) {
	ctx := overrideCtx(t, []domain.MonoCardListItem{
		manifestCard("foo", "project", "project foo"),
		extCard("claude", "foo", "external foo"),
	}, nil, nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 || items[0].ID != "foo" {
		t.Fatalf("expected single internal foo, got %+v", items)
	}
	if items[0].Description != "project foo" {
		t.Errorf("expected project description, got %q", items[0].Description)
	}
}

// M3: a system-internal (builtin) skill shadows a same-named external skill when
// there is no project-internal override.
func TestManifest_SystemOverridesExternal(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{extCard("claude", "foo", "external foo")},
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "system foo")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 || items[0].ID != "foo" {
		t.Fatalf("expected single system foo, got %+v", items)
	}
	if items[0].Description != "system foo" {
		t.Errorf("expected system description, got %q", items[0].Description)
	}
}

// M4: a project-authored (user) skill overrides a same-named system builtin.
func TestManifest_ProjectUserOverridesSystem(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{manifestCard("foo", "project", "project foo")},
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "system foo")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 || items[0].ID != "foo" {
		t.Fatalf("expected single foo, got %+v", items)
	}
	if items[0].Description != "project foo" {
		t.Errorf("expected project description (project > system), got %q", items[0].Description)
	}
}

// M5: a builtin residue in the project store does not override the workspace
// canonical builtin (both system class → first-seen workspace wins).
func TestManifest_BuiltinResidueDoesNotOverrideCanonical(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "residue foo")},
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "canonical foo")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single foo, got %+v", items)
	}
	if items[0].Description != "canonical foo" {
		t.Errorf("expected workspace canonical (system), got %q", items[0].Description)
	}
}

// M5b: a project card that explicitly carries source=user overrides the
// workspace canonical builtin even though its name collides with a real
// builtin (plan-module). This is the project-internal > system-internal
// guarantee for explicit overrides; the name-based isBuiltinSkill heuristic
// must NOT demote an explicitly user-sourced card.
func TestManifest_ProjectUserOverridesSystemBuiltinName(t *testing.T) {
	if !isBuiltinSkill("plan-module") {
		t.Skip("plan-module is not a builtin skill in this build; adjust fixture")
	}
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{manifestCard("plan-module", "user", "project override")},
		[]domain.MonoCardListItem{manifestCard("plan-module", "builtin", "system canonical")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single plan-module, got %+v", items)
	}
	if items[0].Description != "project override" {
		t.Errorf("expected project user override to win, got %q", items[0].Description)
	}
}

// M5c: a project card with a builtin name but NO explicit source metadata is
// treated as a legacy builtin residue (system class), so the workspace
// canonical still wins — the name-based heuristic fallback remains intact.
func TestManifest_BuiltinNameMissingSourceTreatedAsResidue(t *testing.T) {
	if !isBuiltinSkill("plan-module") {
		t.Skip("plan-module is not a builtin skill in this build; adjust fixture")
	}
	residue := manifestCard("plan-module", "", "residue no-source")
	residue.Source = ""
	delete(residue.Data, "source")
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{residue},
		[]domain.MonoCardListItem{manifestCard("plan-module", "builtin", "system canonical")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected single plan-module, got %+v", items)
	}
	if items[0].Description != "system canonical" {
		t.Errorf("expected workspace canonical for missing-source residue, got %q", items[0].Description)
	}
}

// M6: skills with distinct names are all preserved (internal + external).
func TestManifest_DistinctNamesAllKept(t *testing.T) {
	ctx := overrideCtx(t, []domain.MonoCardListItem{
		manifestCard("a", "project", "project a"),
		extCard("claude", "b", "external b"),
	}, nil, nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	byID := manifestByID(items)
	if _, ok := byID["a"]; !ok {
		t.Error("expected internal skill a")
	}
	if _, ok := byID["ext-skill:claude:b"]; !ok {
		t.Error("expected external skill ext-skill:claude:b")
	}
}

// The skill manifest summary (LLM-facing prompt) surfaces unshadowed external
// skills with their ext-skill: ID and description.
func TestSkillManifestSummary_IncludesExternalSkill(t *testing.T) {
	ctx := overrideCtx(t, []domain.MonoCardListItem{extCard("claude", "foo", "external foo")}, nil, nil)
	a := &Actor{}
	summary := a.skillManifestSummary(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if !strings.Contains(summary, "ext-skill:claude:foo") {
		t.Errorf("expected summary to mention external skill id, got:\n%s", summary)
	}
	if !strings.Contains(summary, "external foo") {
		t.Errorf("expected summary to include description, got:\n%s", summary)
	}
}

// U1: using an external skill ID that is not mounted returns "not mounted".
func TestHandleSkillUse_ExternalNotMounted(t *testing.T) {
	ctx := overrideCtx(t, nil, nil, map[string]string{
		"ext-skill:claude:foo": "---\nname: foo\n---\n\nbody\n",
	})
	a := &Actor{}
	_, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "ext-skill:claude:foo"})
	if err == nil {
		t.Fatal("expected 'not mounted' error for unmounted external skill")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("expected 'not mounted' error, got %q", err.Error())
	}
}

// U2: syncSkillMounts auto-mounts an external skill, then handleSkillUse returns
// its body from the external provider and ComponentMounts records the card.
func TestSyncSkillMounts_AndUse_ExternalSkill(t *testing.T) {
	body := "---\nname: foo\ndescription: external foo\n---\n\nExternal skill body.\n"
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{extCard("claude", "foo", "external foo")},
		nil,
		map[string]string{"ext-skill:claude:foo": body})
	a := &Actor{}

	changed, err := a.syncSkillMounts(ctx, domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("syncSkillMounts: %v", err)
	}
	if !changed {
		t.Fatal("expected syncSkillMounts to add the external skill mount")
	}
	mount, ok := findSkillMount(a, "ext-skill:claude:foo")
	if !ok {
		t.Fatal("expected ext-skill:claude:foo mount after sync")
	}
	if mount.Scope != skillScopeKindConfig {
		t.Errorf("Scope = %q, want %q", mount.Scope, skillScopeKindConfig)
	}
	if mount.Title != "foo" {
		t.Errorf("Title = %q, want foo", mount.Title)
	}
	if mount.Kind != "skill" {
		t.Errorf("Kind = %q, want skill", mount.Kind)
	}

	resp, err := a.handleSkillUse(ctx, domain.AgentSkillUseReq{SkillID: "ext-skill:claude:foo"})
	if err != nil {
		t.Fatalf("handleSkillUse external: %v", err)
	}
	if !strings.Contains(resp.Body, "External skill body") {
		t.Errorf("expected external body, got %q", resp.Body)
	}
	if resp.SkillID != "ext-skill:claude:foo" {
		t.Errorf("SkillID = %q, want ext-skill:claude:foo", resp.SkillID)
	}

	// External mount must be reconciled away when no longer in the manifest.
	ctx2 := overrideCtx(t, nil, nil, nil)
	if changed2, err := a.syncSkillMounts(ctx2, domain.AgentKindConfig{}); err != nil {
		t.Fatalf("second sync: %v", err)
	} else if !changed2 {
		t.Fatal("expected managed external mount to be removed on re-sync")
	}
	if _, ok := findSkillMount(a, "ext-skill:claude:foo"); ok {
		t.Error("external mount should be removed once no longer in the manifest")
	}
}

// Pure helpers for ext-skill card ID handling.
func TestIsSkillCardID(t *testing.T) {
	cases := map[string]bool{
		"skill:foo":            true,
		"ext-skill:claude:foo": true,
		"prompt:bar":           false,
		"builtin:baz":          false,
		"":                     false,
	}
	for id, want := range cases {
		if got := isSkillCardID(id); got != want {
			t.Errorf("isSkillCardID(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestSkillCardIDFor_ExternalPassthrough(t *testing.T) {
	if got := skillCardIDFor("read-logs"); got != "skill:read-logs" {
		t.Errorf("skillCardIDFor(read-logs) = %q, want skill:read-logs", got)
	}
	if got := skillCardIDFor("ext-skill:claude:foo"); got != "ext-skill:claude:foo" {
		t.Errorf("skillCardIDFor(ext-skill:claude:foo) = %q, want ext-skill:claude:foo", got)
	}
}

func TestExtSkillNameFromID(t *testing.T) {
	if name, ok := extSkillNameFromID("ext-skill:claude:foo"); !ok || name != "foo" {
		t.Errorf("extSkillNameFromID(ext-skill:claude:foo) = (%q,%v), want (foo,true)", name, ok)
	}
	if _, ok := extSkillNameFromID("skill:foo"); ok {
		t.Error("expected ok=false for non-ext skill ID")
	}
	if _, ok := extSkillNameFromID("ext-skill:bad"); ok {
		t.Error("expected ok=false for malformed ext skill ID")
	}
}

// M7 (end-to-end three-layer override): all three layers carry the SAME skill
// name simultaneously — project-internal (source=project), system-internal
// (workspace builtin) and project-external (ext-skill:claude:foo). The manifest
// must resolve to exactly one winner, the project-internal card. This exercises
// the complete priority chain project > system > external in a single pass,
// closing the pairwise gap between M2 (project>external), M3 (system>external)
// and M4 (project>system).
func TestManifest_ThreeLayerSameName_ProjectWins(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{
			manifestCard("foo", "project", "project foo"),
			extCard("claude", "foo", "external foo"),
		},
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "system foo")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected a single winner across all three layers, got %d: %+v", len(items), items)
	}
	if items[0].ID != "foo" || items[0].Description != "project foo" {
		t.Errorf("expected project-internal foo to win (project > system > external), got %+v", items[0])
	}
}

// M8 (end-to-end two-layer fallback): with project-internal absent, the
// system-internal (workspace builtin) must still override the same-named
// project-external card, leaving a single winner. Together with M7 this proves
// the chain degrades correctly when the highest layer is missing.
func TestManifest_ThreeLayerSameName_SystemWinsWhenProjectAbsent(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{extCard("claude", "foo", "external foo")},
		[]domain.MonoCardListItem{manifestCard("foo", "builtin", "system foo")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	if len(items) != 1 || items[0].ID != "foo" || items[0].Description != "system foo" {
		t.Errorf("expected system-internal foo to win when project absent, got %+v", items)
	}
}

// M9 (end-to-end different names across all three layers): every layer
// contributes a distinct name, so all three must survive resolution in a single
// manifest pass (no false dedup of non-colliding names).
func TestManifest_ThreeLayerDistinctNames_AllKept(t *testing.T) {
	ctx := overrideCtx(t,
		[]domain.MonoCardListItem{
			manifestCard("alpha", "project", "project alpha"),
			extCard("claude", "gamma", "external gamma"),
		},
		[]domain.MonoCardListItem{manifestCard("beta", "builtin", "system beta")},
		nil)
	a := &Actor{}
	items, err := a.fetchFilteredSkillManifest(ctx, ctx.Planner(), domain.AgentKindConfig{})
	if err != nil {
		t.Fatalf("fetchFilteredSkillManifest: %v", err)
	}
	byID := manifestByID(items)
	if len(items) != 3 {
		t.Fatalf("expected all three distinct skills kept, got %d: %+v", len(items), items)
	}
	for id, want := range map[string]string{
		"alpha":                  "project alpha",
		"beta":                   "system beta",
		"ext-skill:claude:gamma": "external gamma",
	} {
		m, ok := byID[id]
		if !ok {
			t.Errorf("expected %q in manifest", id)
		} else if m.Description != want {
			t.Errorf("%q description = %q, want %q", id, m.Description, want)
		}
	}
}
