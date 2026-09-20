package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/message"
	gospore "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/sporemind/pkg/peerclient"
	"github.com/qomos-w/sporemind/pkg/peerserver"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// crossAppTestActor registers one cross-app callable and exposes the service.
type crossAppTestActor struct{}

type echoReq struct {
	Message string `json:"message"`
}

type echoResp struct {
	Message string `json:"message"`
}

func (crossAppTestActor) Type() string                  { return "cross-app-test" }
func (crossAppTestActor) OnInit(actor.Context) error    { return nil }
func (crossAppTestActor) OnStop(actor.Context) error    { return nil }
func (crossAppTestActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("test.echo", func(_ actor.Context, req echoReq) (echoResp, error) {
		return echoResp{Message: "echo:" + req.Message}, nil
	}, actor.CrossApp()); err != nil {
		return err
	}
	return ctx.RegisterDomain("test").Expose()
}

func TestPeerServer_ExposeAndInvoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fragment := protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{"system": 0},
		Schemas: []protocol.StaticSchemaEntry{
			{
				Namespace: "system",
				SchemaID:  200,
				Name:      "echoReq",
				Object: gospore.ObjectDesc{
					Kind: "struct",
					Name: "echoReq",
					Fields: []gospore.FieldDesc{
						{Name: "Message", Type: gospore.TypeDesc{Kind: "scalar", Name: "string"}},
					},
				},
			},
			{
				Namespace: "system",
				SchemaID:  201,
				Name:      "echoResp",
				Object: gospore.ObjectDesc{
					Kind: "struct",
					Name: "echoResp",
					Fields: []gospore.FieldDesc{
						{Name: "Message", Type: gospore.TypeDesc{Kind: "scalar", Name: "string"}},
					},
				},
			},
		},
	}
	protoMgr, err := protocol.NewManager(fragment)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manifestJSON, err := protoMgr.ToManifestImportJSON()
	if err != nil {
		t.Fatalf("manifest import json: %v", err)
	}

	a, err := app.New(
		app.WithNamespace("sporemind"),
		app.WithManifestImport(manifestJSON),
		app.WithRootActor(func() actor.Actor { return crossAppTestActor{} }),
	)
	if err != nil {
		t.Fatalf("app new: %v", err)
	}

	// Run the app in the background.
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	// Wait for the app to be ready (all actors started).
	// The gateway is not used here; we drive the peer server directly.
	time.Sleep(100 * time.Millisecond)

	// Collect and expose cross-app services after actors have started.
	if err := collectCrossAppServices(a, protoMgr); err != nil {
		t.Fatalf("collect cross-app services: %v", err)
	}

	// Wire up peer server on a test HTTP server.
	ps := peerserver.New(a, protoMgr, peerserver.NewTokenAuthenticator(map[string]string{"secret":"client-1"}), "server-1", "v1")
	srv := httptest.NewServer(peerHandler(ps))
	defer srv.Close()

	auth := peerclient.TokenAuth("secret")
	client := peerclient.New(srv.URL, "client-1", "v1", auth, protoMgr)
	if err := client.Handshake(ctx); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	var resp echoResp
	if err := client.InvokeUnary(ctx, "test.echo", echoReq{Message: "hello"}, &resp); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if resp.Message != "echo:hello" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Verify the service is recorded as exposed and includes both request and
	// response schemas.
	if !protoMgr.IsServiceExposedToPeer("client-1", "test") {
		t.Fatal("service not exposed to peer")
	}
	surface, err := protoMgr.ExportForPeer("client-1")
	if err != nil {
		t.Fatalf("export for peer: %v", err)
	}
	if len(surface) != 1 {
		t.Fatalf("expected 1 exposed service, got %d", len(surface))
	}
	if surface[0].Schemas["echoReq"] != 200 {
		t.Fatalf("echoReq schema not exposed: %+v", surface[0].Schemas)
	}
	if surface[0].Schemas["echoResp"] != 201 {
		t.Fatalf("echoResp schema not exposed: %+v", surface[0].Schemas)
	}

	cancel()
	<-done
}

// TestPeerServer_InvokeRaw verifies binary (TBC) request/response assembly.
func TestPeerServer_InvokeRaw(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fragment := protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{"system": 0},
		Schemas: []protocol.StaticSchemaEntry{
			{
				Namespace: "system",
				SchemaID:  200,
				Name:      "echoReq",
				Object: gospore.ObjectDesc{
					Kind: "struct",
					Name: "echoReq",
					Fields: []gospore.FieldDesc{
						{Name: "Message", Type: gospore.TypeDesc{Kind: "scalar", Name: "string"}},
					},
				},
			},
			{
				Namespace: "system",
				SchemaID:  201,
				Name:      "echoResp",
				Object: gospore.ObjectDesc{
					Kind: "struct",
					Name: "echoResp",
					Fields: []gospore.FieldDesc{
						{Name: "Message", Type: gospore.TypeDesc{Kind: "scalar", Name: "string"}},
					},
				},
			},
		},
	}
	protoMgr, err := protocol.NewManager(fragment)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	manifestJSON, err := protoMgr.ToManifestImportJSON()
	if err != nil {
		t.Fatalf("manifest import json: %v", err)
	}

	a, err := app.New(
		app.WithNamespace("sporemind"),
		app.WithManifestImport(manifestJSON),
		app.WithRootActor(func() actor.Actor { return crossAppTestActor{} }),
	)
	if err != nil {
		t.Fatalf("app new: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(100 * time.Millisecond)

	if err := collectCrossAppServices(a, protoMgr); err != nil {
		t.Fatalf("collect cross-app services: %v", err)
	}

	ps := peerserver.New(a, protoMgr, peerserver.NewTokenAuthenticator(map[string]string{"secret":"client-1"}), "server-1", "v1")
	srv := httptest.NewServer(peerHandler(ps))
	defer srv.Close()

	auth := peerclient.TokenAuth("secret")
	client := peerclient.New(srv.URL, "client-1", "v1", auth, protoMgr)
	if err := client.Handshake(ctx); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	// Assemble the binary request packet:
	// 1. Look up the request TypeDesc by schema ID.
	// 2. Encode the Go value with EncodingBinary via the app's codec.
	// 3. Send raw bytes via InvokeRaw.
	// 4. Decode the binary response bytes with the response TypeDesc.
	reqDesc, ok := a.Schemas().LookupSchema(200)
	if !ok {
		t.Fatal("request schema not found")
	}
	respDesc, ok := a.Schemas().LookupSchema(201)
	if !ok {
		t.Fatal("response schema not found")
	}
	enc, ok := a.Codec().(codec.EncodingAwareCodec)
	if !ok {
		t.Fatal("codec does not support EncodeAs")
	}
	reqBytes, err := enc.EncodeAs(reqDesc, echoReq{Message: "binary"}, message.EncodingBinary)
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	respBytes, err := client.InvokeRaw(ctx, "test.echo", reqBytes)
	if err != nil {
		t.Fatalf("invoke raw: %v", err)
	}
	dec, ok := a.Codec().(codec.EncodingAwareCodec)
	if !ok {
		t.Fatal("codec does not support Decode")
	}
	val, err := dec.Decode(respDesc, respBytes)
	if err != nil {
		t.Fatalf("decode response: %v", err)
	}
	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("unexpected response type %T", val)
	}
	if m["message"] != "echo:binary" {
		t.Fatalf("unexpected response: %+v", m)
	}

	cancel()
	<-done
}

// peerHandler returns an http.Handler with the peer routes registered.
func peerHandler(ps *peerserver.Server) http.Handler {
	mux := http.NewServeMux()
	ps.RegisterRoutes(mux)
	return mux
}

// TestCollectCrossAppServices_SkipsNonCrossApp verifies that only callables
// marked with CrossApp() are exposed.
func TestCollectCrossAppServices_SkipsNonCrossApp(t *testing.T) {
	fragment := protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{"system": 0},
		Schemas:          nil,
	}
	protoMgr, err := protocol.NewManager(fragment)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	a, err := app.New(
		app.WithNamespace("sporemind"),
		app.WithRootActor(func() actor.Actor {
			return &mixedExposureActor{}
		}),
	)
	if err != nil {
		t.Fatalf("app new: %v", err)
	}

	if err := collectCrossAppServices(a, protoMgr); err != nil {
		t.Fatalf("collect: %v", err)
	}

	if protoMgr.IsServiceExposedToPeer("any", "test") {
		t.Fatal("service should not be exposed when no callables are CrossApp")
	}
}

type mixedExposureActor struct{}

func (mixedExposureActor) Type() string               { return "mixed" }
func (mixedExposureActor) OnInit(actor.Context) error { return nil }
func (mixedExposureActor) OnStop(actor.Context) error { return nil }
func (mixedExposureActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("test.internal", func(actor.Context) error { return nil }); err != nil {
		return err
	}
	return ctx.RegisterDomain("test").Expose()
}

// Verify the manifest import JSON shape produced by the protocol manager
// matches what gospore expects.
func TestProtocolManager_ManifestImportJSON(t *testing.T) {
	fragment := protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{"system": 0},
		Schemas: []protocol.StaticSchemaEntry{
			{
				Namespace: "system",
				SchemaID:  200,
				Name:      "EchoReq",
				Object: gospore.ObjectDesc{
					Kind: "struct",
					Name: "EchoReq",
					Fields: []gospore.FieldDesc{
						{Name: "Message", Type: gospore.TypeDesc{Kind: "scalar", Name: "string"}},
					},
				},
			},
		},
	}
	m, err := protocol.NewManager(fragment)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	b, err := m.ToManifestImportJSON()
	if err != nil {
		t.Fatalf("manifest json: %v", err)
	}
	var manifest gospore.Manifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(manifest.Schemas) != 1 {
		t.Fatalf("expected 1 schema, got %d", len(manifest.Schemas))
	}
	if manifest.Schemas[0].SchemaID != 200 {
		t.Fatalf("expected schema id 200, got %d", manifest.Schemas[0].SchemaID)
	}
}
