package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHarshScenario_OnlyBundleUseMounted walks the exact path the bundle-use
// bundle is designed for, in the harshest configuration: a minimal custom kind
// ("researcher") whose ONLY mount is builtin:bundle:bundle-use, receiving a
// task that requires another bundle (builtin:bundle:browser-tools).
//
// Step-by-step it proves the discover → mount → use chain now works:
//
//	Turn N   : retrieval surface is present with usable metadata (the
//	           agent-local component_* callables materialize with real
//	           descriptions and input schemas via the enrichment fallback).
//	Mid-turn : after mounting browser-tools, the turn engine's pending
//	           tools-refresh mechanism re-resolves the tool surface at the
//	           safe judgment window; the browser tool is visible AND
//	           resolvable for the remaining dispatches of the same turn.
//	Turn N+1 : tools are recomputed at turn start and include
//	           open_global_browser with full metadata.
//
// The topology callables map mirrors what the runtime manifest export really
// yields for agent-local handler-only registrations: the entries are present
// (the manifest enumerates runtime registrations) but carry empty descriptions
// and no params — metadata shells. The enrichment fallback must upgrade them
// into usable tools; without it the LLM sees description-less, schema-less
// shells it cannot call correctly.
func TestHarshScenario_OnlyBundleUseMounted(t *testing.T) {
	const kind = "researcher"

	// Real agent shape: the kind's only default bundle arrives as a cardRef
	// (and its mirrored ComponentMounts, as after OnStart sync).
	a := &Actor{
		agentKind: kind,
		cardRefs:  []gen.CardRef{{ID: "builtin:bundle:bundle-use", Scope: "builtin"}},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// Realistic topology snapshot for a project-child agent: exposed project
	// services carry metadata; agent-local handler-only registrations
	// (component_*, open_global_browser, ...) appear as metadata shells —
	// empty description, no params — exactly as the runtime manifest export
	// emits registrations that lack WithDescription/WithParams.
	a.topo = &mockTopologyProvider{
		snapshot: []runtime.ActorNode{
			{Kind: "agent", Callables: []domain.CallableInterface{
				{Name: "component_mount"},
				{Name: "component_unmount"},
				{Name: "component_set_enabled"},
				{Name: "component_list"},
				{Name: "component_snapshot"},
				{Name: "open_global_browser"},
			}},
			{Kind: "project", Callables: []domain.CallableInterface{
				{Name: "project.component_list", Description: "list components"},
				{Name: "project.component_get", Description: "get component"},
				{Name: "project.read", Description: "read file"},
			}},
			{Kind: "mcpmanager", Callables: []domain.CallableInterface{
				// Metadata shells exactly as the runtime manifest export emits
				// handler-only registrations: empty description, no params, but
				// with the ServiceName the cell recorded from the exposed "mcp"
				// domain, plus the declared effect for the mutation.
				{Name: "mcp.list_servers", ServiceName: "mcp"},
				{Name: "mcp.add_server", ServiceName: "mcp", EffectKind: string(domain.EffectReversible)},
				{Name: "mcp.reconnect", ServiceName: "mcp"},
			}},
		},
	}
	cfg := domain.AgentKindConfig{Kind: kind}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// ── Turn N: task arrives ("screenshot page X") ──────────────────────────
	toolsN := a.recomputeTurnToolSurface(ctx, cfg, "test-model")
	byID := map[string]domain.ToolSpec{}
	for _, tl := range toolsN {
		byID[tl.CallableID] = tl
	}
	// Retrieval surface is present for every bundle-use tool…
	for _, want := range []string{"component_list", "component_snapshot", "component_mount", "component_unmount", "component_set_enabled", "project.component_list", "project.component_get", "mcp.list_servers", "mcp.add_server", "mcp.reconnect"} {
		spec, ok := byID[want]
		if !ok {
			t.Fatalf("turn N: bundle-use tool %q missing from tool surface: %+v", want, toolsN)
		}
		// …and materialized with usable metadata, not description-less shells.
		if strings.TrimSpace(spec.Description) == "" || strings.TrimSpace(spec.Description) == want {
			t.Fatalf("turn N: bundle-use tool %q has no real description: %q", want, spec.Description)
		}
	}
	mountSpec := byID["component_mount"]
	if !strings.Contains(mountSpec.InputSchema, "CardId") {
		t.Fatalf("turn N: component_mount input schema lacks CardId: %s", mountSpec.InputSchema)
	}
	// The MCP management pair materializes with routing + schema intact:
	// list routes to the "mcp" service, add carries the Config object and
	// the reversible effect declared at registration.
	listSpec := byID["mcp.list_servers"]
	if listSpec.ServiceName != "mcp" {
		t.Fatalf("turn N: mcp.list_servers ServiceName = %q, want mcp", listSpec.ServiceName)
	}
	addSpec := byID["mcp.add_server"]
	if addSpec.ServiceName != "mcp" {
		t.Fatalf("turn N: mcp.add_server ServiceName = %q, want mcp", addSpec.ServiceName)
	}
	if !strings.Contains(addSpec.InputSchema, "Config") {
		t.Fatalf("turn N: mcp.add_server input schema lacks Config: %s", addSpec.InputSchema)
	}
	if addSpec.EffectKind != string(domain.EffectReversible) {
		t.Fatalf("turn N: mcp.add_server EffectKind = %q, want reversible", addSpec.EffectKind)
	}
	reconnSpec := byID["mcp.reconnect"]
	if reconnSpec.ServiceName != "mcp" {
		t.Fatalf("turn N: mcp.reconnect ServiceName = %q, want mcp", reconnSpec.ServiceName)
	}
	if !strings.Contains(reconnSpec.InputSchema, "Id") {
		t.Fatalf("turn N: mcp.reconnect input schema lacks Id: %s", reconnSpec.InputSchema)
	}
	// No browser capability exists yet — correct per premise: the shell
	// callable is in the topology map, but browser-tools is not mounted, so
	// the contribution gate (componentSnapshotToolIDs) keeps it out.
	if _, ok := byID["open_global_browser"]; ok {
		t.Fatal("turn N: browser tool present before mounting")
	}

	// ── Mid-turn: the agent retrieves the catalog and mounts browser-tools ──
	// (handleComponentMount is the real handler; builtin:bundle:browser-tools
	// resolves from the embedded builtin assets, no fake descriptors needed.)
	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:bundle:browser-tools", Enabled: true}); err != nil {
		t.Fatalf("mount browser-tools: %v", err)
	}

	// The turn engine frozen at turn start would never see the new tool.
	// The fix: component mutations stage pendingToolsRefresh, and the engine
	// consumes it at the safe judgment window via consumePendingToolsChange —
	// the same wiring startTurnWithName installs on the real engine.
	e := &turnEngine{
		tools: toolsN,
		startReq: domain.TurnStartReq{Input: domain.TurnInput{
			Unit: &domain.ModelUnit{Model: "test-model"},
		}},
		consumePendingToolsChange: func(model string) []domain.ToolSpec {
			if !a.pendingToolsRefresh.Swap(false) {
				return nil
			}
			return a.recomputeTurnToolSurface(ctx, cfg, model)
		},
	}
	e.applyPendingToolsChange()

	_, byName := e.buildToolIndex()
	var browser domain.ToolSpec
	var found bool
	for _, alias := range []string{"open_global_browser", "open-global-browser"} {
		spec, ok := byName[alias]
		if !ok {
			continue
		}
		browser = spec
		found = true
	}
	if !found {
		names := make([]string, 0, len(byName))
		for name := range byName {
			names = append(names, name)
		}
		t.Fatalf("mid-turn: browser tool not resolvable by tool index after refresh; index aliases: %v", names)
	}
	if strings.TrimSpace(browser.Description) == "" || strings.TrimSpace(browser.Description) == "open_global_browser" {
		t.Fatalf("mid-turn: browser tool has no real description: %q", browser.Description)
	}
	if !strings.Contains(browser.InputSchema, "Url") {
		t.Fatalf("mid-turn: browser tool input schema lacks Url: %s", browser.InputSchema)
	}

	// ── Turn N+1: tools are recomputed from the (now mounted) snapshot ──────
	snapshot := a.resolveComponentSnapshot(ctx)
	contributed := false
	for _, m := range snapshot.Mounts {
		if m.CardID == "builtin:bundle:browser-tools" && !m.Enabled {
			t.Fatal("browser-tools should be enabled")
		}
	}
	for _, tc := range snapshot.Tools {
		if tc.CallableID == "open_global_browser" {
			contributed = true
		}
	}
	if !contributed {
		t.Fatal("snapshot must carry the browser tool contribution (mount succeeded)")
	}
	// And the contribution materializes: the resolved surface carries the
	// browser tool with metadata, for this non-coder kind, without any
	// coder-only autonomous injection.
	toolsN1 := a.recomputeTurnToolSurface(ctx, cfg, "test-model")
	browserN1, ok := func() (domain.ToolSpec, bool) {
		for _, tl := range toolsN1 {
			if tl.CallableID == "open_global_browser" {
				return tl, true
			}
		}
		return domain.ToolSpec{}, false
	}()
	if !ok {
		t.Fatalf("turn N+1: open_global_browser did not materialize: %+v", toolsN1)
	}
	if strings.TrimSpace(browserN1.Description) == "" || strings.TrimSpace(browserN1.Description) == "open_global_browser" {
		t.Fatalf("turn N+1: open_global_browser materialized as a metadata shell: %q", browserN1.Description)
	}
	if !strings.Contains(browserN1.InputSchema, "Url") {
		t.Fatalf("turn N+1: open_global_browser input schema lacks Url: %s", browserN1.InputSchema)
	}
	// The refreshed mid-turn surface and the next turn's surface agree
	// (modulo ask_user, which only the engine layer appends).
	engineTools := make([]domain.ToolSpec, 0, len(e.tools))
	for _, tl := range e.tools {
		if tl.CallableID == "ask_user" {
			continue
		}
		engineTools = append(engineTools, tl)
	}
	if len(toolsN1) != len(engineTools) {
		t.Fatalf("turn N+1 surface (%d tools) diverges from refreshed mid-turn surface (%d tools)", len(toolsN1), len(engineTools))
	}
}

// TestHarshScenario_ToolDiagnostics proves the failure-signal half of the
// chain: a bundle contribution that cannot materialize (no callable
// registration anywhere) and one that is registered AdminOnly must both be
// reported in snapshot.Diagnostics instead of disappearing silently.
func TestHarshScenario_ToolDiagnostics(t *testing.T) {
	descriptors := map[string]domain.ComponentDescriptor{
		"test:bundle:ghost": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:ghost", Kind: "bundle", Source: "project"},
			Title: "Ghost Bundle",
			Tools: []domain.ComponentToolContribution{
				{ID: "ghost-1", CardID: "test:bundle:ghost", CallableID: "project.nonexistent"},
			},
		},
		"test:bundle:admin": {
			Ref:   domain.ComponentRef{CardID: "test:bundle:admin", Kind: "bundle", Source: "project"},
			Title: "Admin Bundle",
			Tools: []domain.ComponentToolContribution{
				{ID: "admin-1", CardID: "test:bundle:admin", CallableID: "sshmanager.host_list"},
			},
		},
	}
	ctx := makeIntegrationCtx(t, descriptors)
	a := &Actor{ComponentMounts: []domain.AgentComponentMount{}}
	a.topo = &mockTopologyProvider{
		snapshot: []runtime.ActorNode{
			{Kind: "sshmanager", Callables: []domain.CallableInterface{
				{Name: "sshmanager.host_list", Description: "list hosts", Permission: "admin"},
			}},
		},
	}

	for _, cardID := range []string{"test:bundle:ghost", "test:bundle:admin"} {
		if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: cardID, Enabled: true}); err != nil {
			t.Fatalf("mount %s: %v", cardID, err)
		}
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	var ghostErr, adminWarn bool
	for _, d := range snapshot.Diagnostics {
		if d.Reason == "callable_not_found" && d.CardID == "test:bundle:ghost" && strings.Contains(d.Message, "project.nonexistent") {
			ghostErr = true
		}
		if d.Reason == "admin_visibility" && d.CardID == "test:bundle:admin" && strings.Contains(d.Message, "sshmanager.host_list") {
			adminWarn = true
		}
	}
	if !ghostErr {
		t.Fatalf("snapshot.Diagnostics must carry a callable_not_found error for test:bundle:ghost/project.nonexistent: %+v", snapshot.Diagnostics)
	}
	if !adminWarn {
		t.Fatalf("snapshot.Diagnostics must carry an admin_visibility warning for test:bundle:admin/sshmanager.host_list: %+v", snapshot.Diagnostics)
	}

	// The ghost contribution must not materialize into the tool surface.
	cfg := domain.AgentKindConfig{Kind: "researcher"}
	tools := a.resolveTools(ctx, cfg, a.componentCallableUniverse())
	for _, tl := range tools {
		if tl.CallableID == "project.nonexistent" {
			t.Fatal("ghost contribution materialized into the tool surface")
		}
	}
	// The admin-scoped contribution does materialize — AdminOnly is frontend
	// visibility, not a runtime call wall — which is exactly why the warning
	// diagnostic matters for transparency.
	var adminMaterialized bool
	for _, tl := range tools {
		if tl.CallableID == "sshmanager.host_list" {
			adminMaterialized = true
		}
	}
	if !adminMaterialized {
		t.Fatal("admin-registered contribution should still materialize (runtime visibility is not a call wall)")
	}
}
