// Package sporecall provides a built-in SporeApp bundle that exposes the
// "spore 调用能力" — spore script invoking host actor callables — as plain
// app callables, so the script→callable bridge can be exercised (and
// mounted by agents as an app-bundle) without touching the agent tool path.
package sporecall

import (
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Manifest is the sporecall bundle manifest used for registration and tests.
var Manifest = gen.AppManifest{
	ID:              "builtin.sporecall",
	Name:            "SporeCall",
	Namespace:       "sporeapp.builtin.sporecall",
	Version:         "1.0.0",
	Runtime:         "spore",
	ProtocolVersion: 1,
	Permissions:     []string{"spore.invoke"},
	Callables: []gen.AppCallableDescriptor{
		// ping is a diagnostics/health callable — panel-facing only, never an
		// agent tool (Expose "frontend" is skipped by the agent registry).
		{ID: "ping", Expose: "frontend"},
		// ToolName strips the app-<appID>- prefix from the LLM-facing names
		// (same override actor.WithToolName gives host callables like
		// create_task): the tools surface as host_call / mcp_call / app_call.
		{ID: "call", ToolName: "host_call", Description: "Relay a call to any host actor callable by dot id (e.g. appmanager.list), from spore script. Args (named): {\"callId\": \"<dotted callable id>\", \"payload\": {}}."},
		{ID: "call_mcp", ToolName: "mcp_call", Description: "Call an MCP tool on a configured server from spore script. Args (named): {\"serverId\": \"...\", \"tool\": \"...\", \"args\": {}}."},
		{ID: "call_app", ToolName: "app_call", Description: "Invoke a callable on another registered app from spore script. Args (named): {\"appId\": \"...\", \"callable\": \"...\", \"payload\": [positional args or []], \"agentId\": \"...\"}."},
	},
	Bundles: []gen.AppBundle{{
		Title: "SporeCall",
		Description: "从 Spore 脚本中继调用宿主 callable：call(callId, payload) 任意宿主 callable（含 MCP），" +
			"call_app(appId, callable, args, agentId) 调用其他应用。挂载后工具为 app.builtin.sporecall.*。",
		Icon:  "zap",
		Color: "#9333ea",
		Tools: []gen.AppBundleTool{
			{CallableID: "call"},
			{CallableID: "call_mcp"},
			{CallableID: "call_app"},
		},
	}},
	Events:      []gen.AppEventDescriptor{},
	Entrypoints: []gen.AppEntrypoint{{ID: "home", Kind: "view", Title: "SporeCall"}},
	Security: &gen.AppSecurityPolicy{
		MaxInstructions: 1_000_000,
		MaxDurationMs:   15_000,
		MaxHostCalls:    64,
		MaxOutputBytes:  65_536,
	},
}

// Modules contains the spore script source for each module.
var Modules = map[string]string{
	"main": MainModule,
}

// EntryModule is the main module name for the bundle.
const EntryModule = "main"

// MainModule relays host invocations from spore script. Wire payloads are
// positional JSON arrays (decodeRequestPayload): call takes
// ["<callId>", payload]; call_mcp takes [serverId, tool, args]; call_app
// takes [appId, callable, payload, agentId].
const MainModule = `import { invoke, invoke_app } from "host"

export fun ping(): string = "pong"

export fun call(callId: string, payload: any): any {
	var res: any = invoke(callId, payload)
	return res
}

export fun call_mcp(serverId: string, tool: string, args: any): any {
	var res: any = invoke("mcp.call_tool", {"Id": serverId, "Tool": tool, "Arguments": args})
	return res
}

export fun call_app(appId: string, callable: string, payload: any, agentId: string): any {
	var res: any = invoke_app(appId, callable, payload, agentId)
	return res
}`
