package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestSporeKind_PureBridgeSurface pins the spore kind's contract: the
// LLM-visible tool surface is exactly the sporeeval bundle — the agent-local
// eval plus the workspace discovery/relay callables — nothing else.
//
// Capability closure is reach, not surface (workspace_hostcall.go relays any
// service callable by dotted ID, target-side gates intact): file editing,
// wiki persistence and catalog discovery are direct host_call targets, so no
// dedicated tools are required to complete tasks.
func TestSporeKind_PureBridgeSurface(t *testing.T) {
	a := &Actor{
		agentKind: domain.AgentKindSpore,
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:sporeeval", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// The topology deliberately contains everything the agent could ever
	// want: work callables (project.*), the component surface bundle-use
	// used to expose, MCP management. The assertion is that NONE of it
	// becomes a tool — only the bundle surfaces materialize.
	a.topo = &mockTopologyProvider{
		snapshot: []runtime.ActorNode{
			{Kind: "agent", Callables: []domain.CallableInterface{
				{Name: "eval", Description: "Evaluate an ad-hoc spore script."},
				{Name: "component_mount"},
				{Name: "component_unmount"},
				{Name: "component_set_enabled"},
				{Name: "component_list"},
				{Name: "component_snapshot"},
			}},
			{Kind: "workspace", Callables: []domain.CallableInterface{
				{Name: "workspace.host_call", ServiceName: "workspace", Description: "Invoke any host actor callable by dotted ID."},
				{Name: "workspace.spore_syntax", ServiceName: "workspace", Description: "Spore language syntax reference."},
				{Name: "workspace.search_callables", ServiceName: "workspace", Description: "Search the live callable catalog."},
			}},
			{Kind: "mcpmanager", Callables: []domain.CallableInterface{
				{Name: "mcp.list_servers", ServiceName: "mcp"},
				{Name: "mcp.call_tool", ServiceName: "mcp", Description: "Invoke a tool on a configured MCP server."},
			}},
			{Kind: "appmanager", Callables: []domain.CallableInterface{
				{Name: "appmanager.invoke", ServiceName: "appmanager", Description: "Invoke a registered app's callable."},
			}},
			{Kind: "project", Callables: []domain.CallableInterface{
				{Name: "project.component_list", Description: "list components"},
				{Name: "project.read", Description: "read file"},
				{Name: "project.write", Description: "write file"},
			}},
		},
	}
	cfg := domain.AgentKindConfig{Kind: domain.AgentKindSpore}
	ctx := testutil.AnonCtx(testutil.GenActorID())

	tools := a.recomputeTurnToolSurface(ctx, cfg, "test-model")
	// The surface is the six sporeeval tools plus the hard-injected
	// infrastructure every kind gets (skill_use, forced image-recognition)
	// — nothing else.
	allowed := map[string]bool{
		"eval":                        true,
		"workspace.spore_syntax":      true,
		"workspace.search_callables":  true,
		"workspace.host_call":         true,
		"mcp.call_tool":               true,
		"appmanager.invoke":           true,
		"skill_use":                   true,
		"image_recognize":             true,
	}
	for _, tl := range tools {
		if !allowed[tl.CallableID] {
			t.Fatalf("tool %q leaked into the spore surface", tl.CallableID)
		}
	}
	byID := map[string]domain.ToolSpec{}
	for _, tl := range tools {
		byID[tl.CallableID] = tl
	}
	for _, want := range []string{
		"eval", "workspace.spore_syntax", "workspace.search_callables",
		"workspace.host_call", "mcp.call_tool", "appmanager.invoke",
	} {
		tool, ok := byID[want]
		if !ok {
			t.Fatalf("sporeeval tool %q missing", want)
		}
		if tool.Description == "" {
			t.Fatalf("sporeeval tool %q has no description", want)
		}
	}
	if bridge := byID["workspace.host_call"]; bridge.ServiceName != "workspace" {
		t.Fatalf("workspace.host_call ServiceName = %q, want workspace", bridge.ServiceName)
	}

	// The removed layers must not leak in: no bundle-use tools, no work
	// tools — even though the topology offers them all.
	for _, forbidden := range []string{
		"component_mount", "component_snapshot", "component_list",
		"project.component_list", "mcp.list_servers",
		"project.read", "project.write",
	} {
		if _, ok := byID[forbidden]; ok {
			t.Fatalf("%q leaked into the spore surface", forbidden)
		}
	}
}
