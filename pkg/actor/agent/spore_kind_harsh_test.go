package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestSporeKind_MinimalSurfaceAndSelfExtension answers "can a spore-kind
// agent (only sporecall + bundle-use) complete tasks independently?" at the
// capability-closure level, deterministically:
//
//	Turn N   : the surface is exactly the bootstrap pair — 3 bridge tools
//	           (workspace.host_call / mcp.call_tool / appmanager.invoke) +
//	           10 bundle-use tools — and NOTHING else from the project side
//	           (no project.read / project.write), proving "only" holds.
//	Mid-turn : the task needs file capability → the agent mounts
//	           builtin:bundle:file-tools; the pendingToolsRefresh mechanism
//	           makes project.read & co. visible to the same turn.
//	Turn N+1 : the acquired tools persist; the bridge tools remain.
//
// The loop "minimal start → task-driven acquisition → execute" is the
// complete capability story of the kind; the LLM driving it is turn-engine
// machinery shared by every kind (dreamer already proves zero-tool turns
// run), so it is not re-proven here.
func TestSporeKind_MinimalSurfaceAndSelfExtension(t *testing.T) {
	a := &Actor{
		agentKind: domain.AgentKindSpore,
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:sporecall", Scope: "builtin"},
			{ID: "builtin:bundle:bundle-use", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	a.topo = &mockTopologyProvider{
		snapshot: []runtime.ActorNode{
			{Kind: "agent", Callables: []domain.CallableInterface{
				{Name: "component_mount"},
				{Name: "component_unmount"},
				{Name: "component_set_enabled"},
				{Name: "component_list"},
				{Name: "component_snapshot"},
			}},
			{Kind: "workspace", Callables: []domain.CallableInterface{
				{Name: "workspace.host_call", ServiceName: "workspace", Description: "Invoke any host actor callable by dotted ID."},
			}},
			{Kind: "mcpmanager", Callables: []domain.CallableInterface{
				{Name: "mcp.list_servers", ServiceName: "mcp"},
				{Name: "mcp.add_server", ServiceName: "mcp", EffectKind: string(domain.EffectReversible)},
				{Name: "mcp.reconnect", ServiceName: "mcp"},
				{Name: "mcp.call_tool", ServiceName: "mcp", Description: "Invoke a tool on a configured MCP server."},
			}},
			{Kind: "appmanager", Callables: []domain.CallableInterface{
				{Name: "appmanager.invoke", ServiceName: "appmanager", Description: "Invoke a registered app's callable."},
			}},
			{Kind: "project", Callables: []domain.CallableInterface{
				{Name: "project.component_list", Description: "list components"},
				{Name: "project.component_get", Description: "get component"},
				{Name: "project.read", Description: "read file"},
				{Name: "project.write", Description: "write file"},
			}},
		},
	}
	cfg := domain.AgentKindConfig{Kind: domain.AgentKindSpore}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	// ── Turn N: exactly the bootstrap surface ──────────────────────────────
	toolsN := a.recomputeTurnToolSurface(ctx, cfg, "test-model")
	byID := map[string]domain.ToolSpec{}
	for _, tl := range toolsN {
		byID[tl.CallableID] = tl
	}
	for _, want := range []string{
		"workspace.host_call", "mcp.call_tool", "appmanager.invoke",
		"component_list", "component_snapshot", "component_mount",
		"component_unmount", "component_set_enabled",
		"project.component_list", "project.component_get",
		"mcp.list_servers", "mcp.add_server", "mcp.reconnect",
	} {
		if _, ok := byID[want]; !ok {
			t.Fatalf("turn N: bootstrap tool %q missing: %+v", want, toolsN)
		}
	}
	bridge := byID["workspace.host_call"]
	if strings.TrimSpace(bridge.Description) == "" {
		t.Fatalf("turn N: workspace.host_call has no description")
	}
	if bridge.ServiceName != "workspace" {
		t.Fatalf("turn N: workspace.host_call ServiceName = %q", bridge.ServiceName)
	}
	// "Only" holds: no work capability is present before self-extension.
	for _, forbidden := range []string{"project.read", "project.write"} {
		if _, ok := byID[forbidden]; ok {
			t.Fatalf("turn N: %q present before any self-extension — surface is not minimal", forbidden)
		}
	}

	// ── Mid-turn: task needs file capability → acquire it ─────────────────
	if _, err := a.handleComponentMount(ctx, domain.AgentComponentMountReq{CardID: "builtin:bundle:file-tools", Enabled: true}); err != nil {
		t.Fatalf("mount file-tools: %v", err)
	}
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
	if _, ok := byName["project_read"]; !ok {
		if _, ok2 := byName["project.read"]; !ok2 {
			names := make([]string, 0, len(byName))
			for n := range byName {
				names = append(names, n)
			}
			t.Fatalf("mid-turn: project.read not visible after acquiring file-tools; index: %v", names)
		}
	}

	// ── Turn N+1: acquired capability persists, bridge remains ────────────
	toolsN1 := a.recomputeTurnToolSurface(ctx, cfg, "test-model")
	var readN1, hostCallN1 bool
	for _, tl := range toolsN1 {
		if tl.CallableID == "project.read" {
			readN1 = true
		}
		if tl.CallableID == "workspace.host_call" {
			hostCallN1 = true
		}
	}
	if !readN1 {
		t.Fatalf("turn N+1: project.read did not persist: %+v", toolsN1)
	}
	if !hostCallN1 {
		t.Fatal("turn N+1: workspace.host_call must remain after self-extension")
	}
}
