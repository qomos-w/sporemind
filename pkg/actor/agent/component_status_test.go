package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// bundleUseSnapshot builds a snapshot shaped like an agent that has the
// bundle-use capability live (self-management tools contributed).
func bundleUseSnapshot(mounts []domain.AgentComponentMount) domain.AgentComponentSnapshot {
	return domain.AgentComponentSnapshot{
		Mounts: mounts,
		Tools: []domain.ComponentToolContribution{
			{ID: "t1", CardID: "builtin:bundle:bundle-use", CallableID: "component_mount"},
			{ID: "t2", CardID: "builtin:bundle:bundle-use", CallableID: "component_list"},
			{ID: "t3", CardID: "builtin:bundle:bundle-use", CallableID: "component_snapshot"},
		},
	}
}

// TestComponentStatusSection_GatedOff verifies the section is absent when the
// bundle-use capability is not live: no self-management tools contributed, or
// no mounts at all.
func TestComponentStatusSection_GatedOff(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{}

	// No component_mount/component_list contributions → no section.
	snapshot := domain.AgentComponentSnapshot{
		Mounts: []domain.AgentComponentMount{{CardID: "x", Enabled: true, Scope: "user"}},
		Tools:  []domain.ComponentToolContribution{{CallableID: "project.read"}},
	}
	if got := a.componentStatusSection(ctx, snapshot); got != "" {
		t.Fatalf("expected empty section without bundle-use tools, got:\n%s", got)
	}

	// Bundle-use live but no mounts → no section.
	if got := a.componentStatusSection(ctx, bundleUseSnapshot(nil)); got != "" {
		t.Fatalf("expected empty section without mounts, got:\n%s", got)
	}
}

// TestComponentStatusSection_Render verifies the full render: mounted lines
// with LOCKED markers (kind-config and protected builtin), the disabled
// marker, and the available-bundle catalog minus mounted entries.
func TestComponentStatusSection_Render(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:bundle:web": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:web", Kind: "bundle", Source: "project"},
			Title: "Web Bundle",
			Tools: []domain.ComponentToolContribution{
				{ID: "w1", CardID: "test:bundle:web", CallableID: "web.fetch"},
				{ID: "w2", CardID: "test:bundle:web", CallableID: "web.search"},
			},
		},
		// Non-bundle catalog entries must not appear in the available list.
		"test:prompt:env": {
			Ref:   domain.ComponentRef{CardID: "test:prompt:env", Kind: "prompt", Source: "project"},
			Title: "Environment Prompt",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{}

	mounts := []domain.AgentComponentMount{
		// bundle-use itself: builtin scope, protected in embedded assets → LOCKED.
		{CardID: "builtin:bundle:bundle-use", Kind: "bundle", Enabled: true, Scope: "builtin"},
		// Config-file mount → LOCKED with kind-config reason.
		{CardID: "test:bundle:ssh", Kind: "bundle", Enabled: true, Scope: skillScopeKindConfig},
		// User mount, disabled → silenced marker, no LOCKED.
		{CardID: "test:bundle:browser-tools", Kind: "bundle", Enabled: false, Scope: "user"},
		// Builtin scope but not a protected embedded asset → no LOCKED.
		{CardID: "test:bundle:plain-builtin", Kind: "bundle", Enabled: true, Scope: "builtin"},
	}
	section := a.componentStatusSection(ctx, bundleUseSnapshot(mounts))
	if section == "" {
		t.Fatal("expected non-empty component status section")
	}

	// Locked markers.
	if !strings.Contains(section, "builtin:bundle:bundle-use [bundle] scope=builtin — LOCKED (protected builtin; cannot be unmounted)") {
		t.Fatalf("missing protected-builtin LOCKED marker:\n%s", section)
	}
	if !strings.Contains(section, "test:bundle:ssh [bundle] scope=kind-config — LOCKED (kind-config: mounted via the agent kind config file; cannot be unmounted at runtime)") {
		t.Fatalf("missing kind-config LOCKED marker:\n%s", section)
	}
	// Disabled marker.
	if !strings.Contains(section, "test:bundle:browser-tools [bundle] scope=user — disabled (silenced") {
		t.Fatalf("missing disabled marker:\n%s", section)
	}
	// Non-protected builtin-scope mount is not locked.
	if strings.Contains(section, "test:bundle:plain-builtin [bundle] scope=builtin — LOCKED") {
		t.Fatalf("non-protected builtin-scope mount must not be marked LOCKED:\n%s", section)
	}
	// Available catalog: only kind=bundle, mounted entries excluded.
	if !strings.Contains(section, "test:bundle:web — Web Bundle (web.fetch, web.search)") {
		t.Fatalf("missing available bundle entry with tool summary:\n%s", section)
	}
	if strings.Contains(section, "test:prompt:env") {
		t.Fatalf("non-bundle catalog entry leaked into available list:\n%s", section)
	}
	for _, mounted := range []string{"builtin:bundle:bundle-use —", "test:bundle:ssh —", "test:bundle:browser-tools —", "test:bundle:plain-builtin —"} {
		// Mounted ids appear once as mount lines ("- id [kind]"), and must not
		// reappear as available entries. Available lines use "- id —".
		if strings.Count(section, "- "+mounted) > 1 {
			t.Fatalf("mounted id %q appears more than once (leaked into available list):\n%s", mounted, section)
		}
	}
}

// TestComponentStatusSection_FallbackCatalog verifies that when no
// parent/planner is reachable, the embedded builtin bundles serve as the
// available catalog.
func TestComponentStatusSection_FallbackCatalog(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID()) // no parent/planner
	a := &Actor{}
	mounts := []domain.AgentComponentMount{
		{CardID: "builtin:bundle:bundle-use", Kind: "bundle", Enabled: true, Scope: "builtin"},
	}
	section := a.componentStatusSection(ctx, bundleUseSnapshot(mounts))
	if section == "" {
		t.Fatal("expected non-empty section in fallback mode")
	}
	if !strings.Contains(section, "Available bundles not currently mounted") {
		t.Fatalf("expected available-bundles block in fallback mode:\n%s", section)
	}
	// bundle-use itself is mounted and protected; some other builtin bundle
	// must be offered as available (the embedded set ships several).
	found := false
	for _, id := range []string{"builtin:bundle:browser-tools", "builtin:bundle:file-tools", "builtin:bundle:computeruse-tools"} {
		if strings.Contains(section, id) && !strings.Contains(section, id+" — LOCKED") {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback catalog did not offer any builtin bundle:\n%s", section)
	}
}

// TestTurnSynthCards_ComponentStatusCard verifies the section is wired into
// turnSynthCards as the agent:component-status card with section
// component_status.
func TestTurnSynthCards_ComponentStatusCard(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:bundle:web": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:web", Kind: "bundle", Source: "project"},
			Title: "Web Bundle",
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{
		ComponentMounts: []domain.AgentComponentMount{
			{CardID: "builtin:bundle:bundle-use", Kind: "bundle", Enabled: true, Scope: "builtin"},
		},
	}
	snapshot := a.resolveComponentSnapshot(ctx)
	// Ensure the bundle-use capability is live in the snapshot (mount
	// contributes the self-management tools).
	snapshot.Tools = append(snapshot.Tools, bundleUseSnapshot(nil).Tools...)

	cards := a.turnSynthCards(ctx, domain.AgentKindConfig{}, snapshot)
	var found bool
	for _, c := range cards {
		if c.ID == "agent:component-status" {
			found = true
			if len(c.PromptContributions) != 1 || c.PromptContributions[0].Section != "component_status" {
				t.Fatalf("unexpected prompt contribution on component-status card: %+v", c.PromptContributions)
			}
			if !strings.Contains(c.PromptContributions[0].Text, "Currently mounted components") {
				t.Fatalf("component-status card body unexpected: %q", c.PromptContributions[0].Text)
			}
		}
	}
	if !found {
		t.Fatalf("agent:component-status card missing from synth cards: %+v", cards)
	}
}
