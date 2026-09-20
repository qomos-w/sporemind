package sporeapp

import (
	"testing"

	"github.com/qomos-w/gospore/codec"
	sporesch "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/transport"
	"github.com/qomos-w/sporemind/pkg/builtin/demoapp"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// newDemoAppActor builds a SporeApp actor carrying the builtin demo app
// manifest and loads its runtime, mirroring what AppManager.spawnChild does.
func newDemoAppActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{
		Manifest:          demoapp.RegisterReq().Manifest,
		EntryModule:       demoapp.EntryModule,
		Modules:           demoapp.Modules,
		SchemaDescriptors: demoapp.RegisterReq().SchemaDescriptors,
		State:             map[string]any{},
		codec:             codec.NewBinary(), // mirrors OnInit
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	// Wire the per-app schema overlay the way OnInit does, so typed callables
	// resolve their BinaryCodec descriptors. The overlay resolves from the
	// global registry; no ctx-provided fallback reader is needed here.
	registered, err := a.registerAppSchemas()
	if err != nil {
		t.Fatalf("registerAppSchemas: %v", err)
	}
	a.schemas = registered
	t.Cleanup(func() {
		if a.runtime != nil {
			_ = a.runtime.Close()
		}
	})
	return a
}

// encodeTBC encodes value with the BinaryCodec using the descriptor built
// from the gen-registered type, producing the wire form an external client
// would send.
func encodeTBC(t *testing.T, schemaID uint64, value any) []byte {
	t.Helper()
	typ, ok := sporesch.StructTypeByID(schemaID)
	if !ok {
		t.Fatalf("schema ID %d not in global registry", schemaID)
	}
	td, _ := buildDescriptors(typ, "DemoEchoReq", schemaID)
	data, err := codec.NewBinary().Encode(td, value)
	if err != nil {
		t.Fatalf("encode TBC: %v", err)
	}
	if !codec.IsTBCData(data) {
		t.Fatal("expected TBC wire data")
	}
	return data
}

// TestDemoAppEchoBinaryCodecRoundTrip verifies the §8 acceptance path at the
// SporeApp layer: a TBC-encoded struct request is decoded into the concrete
// gen type, executed by the script with typed parameter access, and the
// response is BinaryCodec-encoded back onto the wire.
func TestDemoAppEchoBinaryCodecRoundTrip(t *testing.T) {
	a := newDemoAppActor(t)

	reqPayload := encodeTBC(t, gen.DemoEchoReqSchemaID, gen.DemoEchoReq{Text: "hello", N: 21})

	resp, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{
		ID:       demoapp.Manifest.ID,
		Callable: "echo",
		Payload:  reqPayload,
	})
	if err != nil {
		t.Fatalf("handleInvoke: %v", err)
	}
	if !codec.IsTBCData(resp.Payload) {
		t.Fatalf("expected TBC response, got %q", resp.Payload)
	}

	var out gen.DemoEchoReq
	view := transport.View{Kind: transport.ViewKindFull, Data: resp.Payload}
	typ, ok := sporesch.StructTypeByID(gen.DemoEchoReqSchemaID)
	if !ok {
		t.Fatal("DemoEchoReq not in global registry")
	}
	td, _ := buildDescriptors(typ, "DemoEchoReq", gen.DemoEchoReqSchemaID)
	view.Schema = td
	if err := (&transport.BinaryCodec{}).DecodeInto(view, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Text != "hello" || out.N != 21 {
		t.Fatalf("echo mismatch: got %+v", out)
	}
}

// TestDemoAppJSONPathStillWorks verifies ping/twice keep the positional JSON
// calling convention after the BinaryCodec introduction.
func TestDemoAppJSONPathStillWorks(t *testing.T) {
	a := newDemoAppActor(t)

	resp, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{
		ID:       demoapp.Manifest.ID,
		Callable: "twice",
		Payload:  []byte(`[21]`),
	})
	if err != nil {
		t.Fatalf("handleInvoke twice: %v", err)
	}
	if codec.IsTBCData(resp.Payload) {
		t.Fatal("twice response should stay on the JSON path")
	}
	if string(resp.Payload) != "42" {
		t.Fatalf("twice = %s, want 42", resp.Payload)
	}
}

// TestDemoAppEchoJSONMapDualMode documents the JSON dual-mode behaviour: a
// JSON object argument is wrapped by the VM into the bound DemoEchoReq class,
// the script echoes it, and the response is still BinaryCodec-encoded (the
// response schema resolves). The actor must stay healthy afterwards.
func TestDemoAppEchoJSONMapDualMode(t *testing.T) {
	a := newDemoAppActor(t)

	resp, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{
		ID:       demoapp.Manifest.ID,
		Callable: "echo",
		Payload:  []byte(`[{"Text":"x","N":1}]`),
	})
	if err != nil {
		t.Fatalf("echo JSON map: %v", err)
	}
	if !codec.IsTBCData(resp.Payload) {
		t.Fatalf("expected BinaryCodec-encoded response for schema-declared callable, got %q", resp.Payload)
	}

	// The actor must remain healthy after the typed call.
	pong, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{
		ID:       demoapp.Manifest.ID,
		Callable: "ping",
	})
	if err != nil {
		t.Fatalf("actor unhealthy after typed call: %v", err)
	}
	if string(pong.Payload) != `"pong"` {
		t.Fatalf("ping = %s, want %q", pong.Payload, `"pong"`)
	}
}
