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
// eval family (eval with host.invoke reach, eval_syntax, eval_callables) —
// nothing else. Host reach lives INSIDE eval scripts: the runtime binds
// host.invoke via sporebridge with the agent's identity, so the removed
// relay tools (workspace.host_call / mcp.call_tool / appmanager.invoke)
// carried no capability eval does not.
func TestSporeKind_PureBridgeSurface(t *testing.T) {
	a := &Actor{
		agentKind: domain.AgentKindSpore,
		cardRefs: []gen.CardRef{
			{ID: "builtin:bundle:sporeeval", Scope: "builtin"},
		},
	}
	a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)

	// The topology deliberately contains everything the agent could ever
	// want: the former relay callables, work callables (project.*), the
	// component surface bundle-use used to expose. The assertion is that
	// NONE of it becomes a tool — only the bundle surfaces materialize.
	a.topo = &mockTopologyProvider{
		snapshot: []runtime.ActorNode{
			{Kind: "agent", Callables: []domain.CallableInterface{
				{Name: "eval", Description: "Evaluate an ad-hoc spore script."},
				{Name: "eval_syntax", Description: "Spore language syntax reference."},
				{Name: "eval_callables", Description: "Search the callable catalog."},
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
	// The surface is the three sporeeval tools plus the hard-injected
	// infrastructure every kind gets (skill_use, forced image-recognition)
	// — nothing else.
	allowed := map[string]bool{
		"eval":            true,
		"eval_syntax":     true,
		"eval_callables":  true,
		"skill_use":       true,
		"image_recognize": true,
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
	for _, want := range []string{"eval", "eval_syntax", "eval_callables"} {
		tool, ok := byID[want]
		if !ok {
			t.Fatalf("sporeeval tool %q missing", want)
		}
		if tool.Description == "" {
			t.Fatalf("sporeeval tool %q has no description", want)
		}
	}

	// The removed layers must not leak in: no relay tools, no bundle-use
	// tools, no work tools — even though the topology offers them all
	// (relays remain reachable only via host.invoke inside eval scripts).
	for _, forbidden := range []string{
		"workspace.host_call", "workspace.spore_syntax", "workspace.search_callables",
		"mcp.call_tool", "appmanager.invoke",
		"component_mount", "component_snapshot", "component_list",
		"project.component_list", "mcp.list_servers",
		"project.read", "project.write",
	} {
		if _, ok := byID[forbidden]; ok {
			t.Fatalf("%q leaked into the spore surface", forbidden)
		}
	}
}
