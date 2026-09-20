package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestPromptComponentIDs(t *testing.T) {
	profile := domain.PromptRef{Kind: "profile", Key: "project.coder"}
	fragment := domain.PromptRef{Kind: "fragment", Key: "builtin/project-graph-prompts"}
	if got := promptComponentID(profile); got != "prompt:profile:project.coder" {
		t.Fatalf("profile component id = %q", got)
	}
	if got := promptComponentID(fragment); got != "prompt:fragment:builtin-project-graph-prompts" {
		t.Fatalf("fragment component id = %q", got)
	}
	if got := promptContributionID(profile, "role"); got != "prompt:profile:project.coder:role:project.coder" {
		t.Fatalf("profile contribution id = %q", got)
	}
	// When the frontend stores the full card ID as the Key (it does this for
	// fragment cards whose frontmatter lacks a promptKey), promptComponentID
	// must use it verbatim regardless of Kind — sanitizePromptKey would mangle
	// ":" into "-" and yield a non-existent card ID.
	fullID := domain.PromptRef{Kind: "instructions", Key: "prompt:fragment:builtin-example"}
	if got := promptComponentID(fullID); got != "prompt:fragment:builtin-example" {
		t.Fatalf("full-id fragment component id = %q, want prompt:fragment:builtin-example", got)
	}
	fullProfile := domain.PromptRef{Kind: "", Key: "prompt:profile:project.coder"}
	if got := promptComponentID(fullProfile); got != "prompt:profile:project.coder" {
		t.Fatalf("full-id profile component id = %q, want prompt:profile:project.coder", got)
	}
}

func TestFragmentPlacement(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "system"},
		{"system", "system"},
		{"after_role", "system"},
		{"leading", "leading"},
		{"trailing", "trailing"},
		{"bogus", "system"},
	}
	for _, c := range cases {
		if got := fragmentPlacement(c.in); got != c.want {
			t.Fatalf("fragmentPlacement(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecodePromptResult_TypedStruct(t *testing.T) {
	result := domain.PromptResolvedRef{Content: "role content", Source: "builtin"}
	var target domain.PromptResolvedRef
	if !decodePromptResult(result, &target) {
		t.Fatal("decodePromptResult failed for typed struct")
	}
	if target.Content != "role content" || target.Source != "builtin" {
		t.Fatalf("unexpected decoded value: %+v", target)
	}
}

func TestDecodePromptResult_TypedStructPointer(t *testing.T) {
	result := &domain.PromptResolvedRef{Content: "pointer content", Source: "override"}
	var target domain.PromptResolvedRef
	if !decodePromptResult(result, &target) {
		t.Fatal("decodePromptResult failed for typed struct pointer")
	}
	if target.Content != "pointer content" || target.Source != "override" {
		t.Fatalf("unexpected decoded value: %+v", target)
	}
}

func TestDecodePromptResult_JSONBytes(t *testing.T) {
	result := []byte(`{"Content":"from json","Source":"builtin"}`)
	var target domain.PromptResolvedRef
	if !decodePromptResult(result, &target) {
		t.Fatal("decodePromptResult failed for JSON bytes")
	}
	if target.Content != "from json" || target.Source != "builtin" {
		t.Fatalf("unexpected decoded value: %+v", target)
	}
}

func TestDecodePromptResult_Map(t *testing.T) {
	result := map[string]interface{}{"Content": "from map", "Source": "builtin"}
	var target domain.PromptResolvedRef
	if !decodePromptResult(result, &target) {
		t.Fatal("decodePromptResult failed for map")
	}
	if target.Content != "from map" || target.Source != "builtin" {
		t.Fatalf("unexpected decoded value: %+v", target)
	}
}

func TestDecodePromptResult_Nil(t *testing.T) {
	var target domain.PromptResolvedRef
	if decodePromptResult(nil, &target) {
		t.Fatal("decodePromptResult should fail for nil result")
	}
	if decodePromptResult(domain.PromptResolvedRef{}, nil) {
		t.Fatal("decodePromptResult should fail for nil target")
	}
}

func TestConfiguredPromptComponentsFiltered_ResolvesRole(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "workspace.wiki_get_card" {
					return domain.WikiGetCardResp{ID: "prompt:profile:project.coder", Raw: "---\ntitle: Coder\ntags: [component, prompt, profile]\n---\n\nYou are a coder."}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:          "coder",
		RolePromptRef: domain.PromptRef{Kind: "profile", Key: "project.coder"},
	}
	prompts := a.configuredPromptComponentsFiltered(ctx, cfg, nil)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d: %+v", len(prompts), prompts)
	}
	if prompts[0].Text != "You are a coder." {
		t.Fatalf("unexpected role text: %q", prompts[0].Text)
	}
	if prompts[0].Priority != -1000 {
		t.Fatalf("expected role priority -1000, got %d", prompts[0].Priority)
	}
	if prompts[0].Placement != "role" {
		t.Fatalf("unexpected placement: %q", prompts[0].Placement)
	}
}

func TestConfiguredPromptComponentsFiltered_Placements(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID != "workspace.wiki_get_card" {
					return nil, fmt.Errorf("unexpected call %s", callID)
				}
				var req domain.WikiGetCardReq
				if err := json.Unmarshal(payload.([]byte), &req); err != nil {
					return nil, err
				}
				switch req.ID {
				case "prompt:profile:role":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: Role\ntags: [component, prompt, profile]\n---\n\nrole"}, nil
				case "prompt:fragment:lead":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: Lead\ntags: [component, prompt, fragment]\n---\n\nlead"}, nil
				case "prompt:fragment:trail":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: Trail\ntags: [component, prompt, fragment]\n---\n\ntrail"}, nil
				case "prompt:fragment:default":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: Default\ntags: [component, prompt, fragment]\n---\n\ndefault"}, nil
				default:
					return domain.WikiGetCardResp{}, nil
				}
			},
		}
	}

	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:          "coder",
		RolePromptRef: domain.PromptRef{Kind: "profile", Key: "role"},
		SystemFragmentRefs: []domain.PromptRef{
			{Kind: "fragment", Key: "lead", Placement: "leading"},
			{Kind: "fragment", Key: "default"},
			{Kind: "fragment", Key: "trail", Placement: "trailing"},
		},
	}
	prompts := a.configuredPromptComponentsFiltered(ctx, cfg, nil)
	if len(prompts) != 4 {
		t.Fatalf("expected 4 prompts, got %d: %+v", len(prompts), prompts)
	}
	want := []struct {
		placement, text string
	}{
		{"role", "role"},
		{"leading", "lead"},
		{"system", "default"},
		{"trailing", "trail"},
	}
	for i, w := range want {
		if prompts[i].Placement != w.placement {
			t.Fatalf("prompts[%d].Placement = %q, want %q", i, prompts[i].Placement, w.placement)
		}
		if prompts[i].Text != w.text {
			t.Fatalf("prompts[%d].Text = %q, want %q", i, prompts[i].Text, w.text)
		}
	}
}

func TestConfiguredPromptComponentsFiltered_DeduplicatesCardReads(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	calls := 0
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "workspace.wiki_get_card" {
					calls++
					return domain.WikiGetCardResp{ID: "prompt:fragment:f1", Raw: "---\ntitle: f1\ntags: [component, prompt, fragment]\n---\n\nfragment content"}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:               "coder",
		SystemFragmentRefs: []domain.PromptRef{{Kind: "fragment", Key: "f1"}, {Kind: "fragment", Key: "f1"}},
	}
	prompts := a.configuredPromptComponentsFiltered(ctx, cfg, nil)
	if len(prompts) != 2 {
		t.Fatalf("expected duplicate references to preserve two contributions, got %d: %+v", len(prompts), prompts)
	}
	if calls != 1 {
		t.Fatalf("expected one card read for duplicate reference, got %d", calls)
	}
}
func TestConfiguredPromptComponentsFiltered_ResolvesFragments(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "workspace.wiki_get_card" {
					return domain.WikiGetCardResp{ID: "prompt:fragment:f1", Raw: "---\ntitle: f1\ntags: [component, prompt, fragment]\npriority: 10\n---\n\nfragment content"}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:               "coder",
		SystemFragmentRefs: []domain.PromptRef{{Kind: "fragment", Key: "f1"}},
	}
	prompts := a.configuredPromptComponentsFiltered(ctx, cfg, nil)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d: %+v", len(prompts), prompts)
	}
	if prompts[0].Text != "fragment content" {
		t.Fatalf("unexpected fragment text: %q", prompts[0].Text)
	}
	if prompts[0].Priority != 10 {
		t.Fatalf("unexpected priority: %d", prompts[0].Priority)
	}
}

// TestConfiguredPromptComponentsFiltered_UIKindVocabulary reproduces the
// agent-config fragment injection bug: the settings UI stores SystemFragmentRefs
// with the fragment card's semantic kind (instructions/system/git/…, default
// "instructions") and a bare promptKey, while the card itself lives at
// prompt:fragment:<key>. Refs whose Kind is not the literal "fragment" must
// still resolve through the fragment card prefix instead of being silently
// dropped after a prompt:profile:<key> miss.
func TestConfiguredPromptComponentsFiltered_UIKindVocabulary(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	var requestedIDs []string
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID != "workspace.wiki_get_card" {
					return nil, fmt.Errorf("unexpected call %s", callID)
				}
				var req domain.WikiGetCardReq
				if err := json.Unmarshal(payload.([]byte), &req); err != nil {
					return nil, err
				}
				requestedIDs = append(requestedIDs, req.ID)
				switch req.ID {
				case "prompt:fragment:my-style":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: my-style\ntags: [component, prompt, fragment]\n---\n\nalways answer in Chinese"}, nil
				case "prompt:fragment:git-rules":
					return domain.WikiGetCardResp{ID: req.ID, Raw: "---\ntitle: git-rules\ntags: [component, prompt, fragment]\n---\n\nconventional commits only"}, nil
				default:
					return domain.WikiGetCardResp{}, nil
				}
			},
		}
	}

	a := &Actor{}
	cfg := domain.AgentKindConfig{
		Kind:          "coder",
		RolePromptRef: domain.PromptRef{Kind: "profile", Key: "project.coder"},
		SystemFragmentRefs: []domain.PromptRef{
			{Kind: "instructions", Key: "my-style"},
			{Kind: "git", Key: "git-rules"},
		},
	}
	prompts := a.configuredPromptComponentsFiltered(ctx, cfg, nil)
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d (requested %v): %+v", len(prompts), requestedIDs, prompts)
	}
	if prompts[0].Text != "always answer in Chinese" || prompts[1].Text != "conventional commits only" {
		t.Fatalf("unexpected fragment texts: %q, %q", prompts[0].Text, prompts[1].Text)
	}
	for _, id := range requestedIDs {
		if id == "prompt:profile:my-style" || id == "prompt:profile:git-rules" {
			t.Fatalf("fragment resolved through profile prefix: %v", requestedIDs)
		}
	}
}
