package demoapp

import (
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Manifest is the demo app manifest used for registration and acceptance tests.
var Manifest = gen.AppManifest{
	ID:              "builtin.demo",
	Name:            "Demo",
	Namespace:       "sporeapp.builtin.demo",
	Version:         "1.0.0",
	Runtime:         "spore",
	ProtocolVersion: 1,
	Schemas: []gen.AppSchemaRef{
		{Name: "DemoEchoReq", Hash: "demo-echo-req-v1", SchemaID: int64(gen.DemoEchoReqSchemaID)},
	},
	Callables: []gen.AppCallableDescriptor{
		{ID: "ping"},
		{ID: "twice"},
		{
			// echo round-trips a typed struct: the BinaryCodec path decodes
			// the request into gen.DemoEchoReq and the script returns it
			// unchanged, so request and response share one schema.
			ID:             "echo",
			RequestSchema:  "DemoEchoReq",
			ResponseSchema: "DemoEchoReq",
		},
	},
	Events:      []gen.AppEventDescriptor{{ID: "pong"}},
	Entrypoints: []gen.AppEntrypoint{{ID: "home", Kind: "view", Title: "Demo Home"}, {ID: "status", Kind: "panel", Title: "Demo Status"}},
	AgentBinding: &gen.AppAgentBinding{
		Surface: &gen.AgentSurfaceBinding{
			AgentID:    "demo-agent",
			Entrypoint: "home",
		},
		Capability: &gen.AgentCapabilityBinding{
			Callables: []string{"ping", "twice", "echo"},
		},
	},
}

// Modules contains the spore script source for each module.
var Modules = map[string]string{
	"main": MainModule,
}

// MainModule is the spore script source for the demo app's entry module.
// It exports three callables: ping (no-arg, returns greeting), twice
// (takes an int, returns it doubled), and echo (round-trips a typed
// DemoEchoReq struct for BinaryCodec acceptance tests).
const MainModule = `import DemoEchoReq from "app"
export fun ping(): string = "pong"
export fun twice(n: int): int = n * 2
export fun echo(req: DemoEchoReq): DemoEchoReq = req`

// EntryModule is the main module name for the demo app.
const EntryModule = "main"
