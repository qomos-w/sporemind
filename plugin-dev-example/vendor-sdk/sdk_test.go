package sdk

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unsafe"

	gen "github.com/qomos-w/sporemind-plugin-sdk/gen"
)

// cstr returns an unsafe.Pointer to a null-terminated byte sequence suitable
// for goString. Safe only when the pointer is consumed within the same call
// (goString copies into a Go string immediately).
func cstr(s string) unsafe.Pointer {
	b := append([]byte(s), 0)
	return unsafe.Pointer(&b[0])
}

func TestManifestRoundTrip(t *testing.T) {
	m := Manifest{
		ID:          "com.example.hello",
		Name:        "Hello",
		Version:     "1.0.0",
		Permissions: []string{"llm:call"},
		Entrypoints: []Entrypoint{
			{Kind: "page", ID: "dashboard", Title: "Dashboard", Route: "/"},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != m.ID || got.Version != m.Version {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if len(got.Entrypoints) != 1 || got.Entrypoints[0].Kind != "page" {
		t.Fatalf("entrypoint mismatch: %+v", got.Entrypoints)
	}
}

func TestManifestJSONTagsArePascalCase(t *testing.T) {
	// The manifest JSON must use PascalCase keys matching gen.AppManifest
	// so the host can unmarshal it directly.
	m := Manifest{ID: "app.test", Name: "Test", Version: "1.0.0", Runtime: "native"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for key := range raw {
		if key[0] >= 'a' && key[0] <= 'z' {
			t.Fatalf("manifest JSON key %q is lowercase; must be PascalCase to match gen.AppManifest", key)
		}
	}
}

func TestManifestNormalize(t *testing.T) {
	m := Manifest{ID: "app.test", Name: "Test", Version: "1.0.0"}
	n := m.Normalize()
	if n.Runtime != NativeRuntime {
		t.Fatalf("expected Runtime %q, got %q", NativeRuntime, n.Runtime)
	}
	if n.ProtocolVersion != ProtocolVersion2 {
		t.Fatalf("expected ProtocolVersion %d, got %d", ProtocolVersion2, n.ProtocolVersion)
	}
	if n.Namespace != m.ID {
		t.Fatalf("expected Namespace %q, got %q", m.ID, n.Namespace)
	}
}

func TestToAppManifest(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse"},
		},
		Entrypoints: []Entrypoint{
			{Kind: "command", ID: "test.ping", Title: "Ping", Route: "ping"},
		},
	}
	app, err := m.ToAppManifest()
	if err != nil {
		t.Fatalf("ToAppManifest: %v", err)
	}
	if app.ID != m.ID || app.Name != m.Name || app.Version != m.Version {
		t.Fatalf("identity mismatch: %+v", app)
	}
	if app.Runtime != NativeRuntime {
		t.Fatalf("expected Runtime %q, got %q", NativeRuntime, app.Runtime)
	}
	if app.ProtocolVersion != ProtocolVersion2 {
		t.Fatalf("expected ProtocolVersion %d, got %d", ProtocolVersion2, app.ProtocolVersion)
	}
	if app.Namespace != m.ID {
		t.Fatalf("expected Namespace %q, got %q", m.ID, app.Namespace)
	}
	if len(app.Callables) != 1 || app.Callables[0].ID != "ping" {
		t.Fatalf("callable mismatch: %+v", app.Callables)
	}
	if len(app.Entrypoints) != 1 || app.Entrypoints[0].Kind != "command" {
		t.Fatalf("entrypoint mismatch: %+v", app.Entrypoints)
	}
}

func TestToAppManifestRejectsIncomplete(t *testing.T) {
	cases := []struct {
		name string
		m    Manifest
	}{
		{"missing ID", Manifest{Name: "T", Version: "1"}},
		{"missing Name", Manifest{ID: "a", Version: "1"}},
		{"missing Version", Manifest{ID: "a", Name: "T"}},
		{"wrong runtime", Manifest{ID: "a", Name: "T", Version: "1", Runtime: "spore"}},
		{"incomplete callable", Manifest{ID: "a", Name: "T", Version: "1", Callables: []Callable{{ID: "c"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.m.ToAppManifest(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestManifestJSONCompatibleWithGenAppManifest(t *testing.T) {
	// Serialize a normalized SDK Manifest, then unmarshal into gen.AppManifest.
	// This proves the host can accept the SDK-produced manifest directly.
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse"},
		},
	}.Normalize()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var app gen.AppManifest
	if err := json.Unmarshal(data, &app); err != nil {
		t.Fatalf("unmarshal into gen.AppManifest: %v", err)
	}
	if app.ID != "app.test" || app.Name != "Test" || app.Version != "1.0.0" {
		t.Fatalf("gen.AppManifest identity mismatch: %+v", app)
	}
	if app.Runtime != NativeRuntime || app.ProtocolVersion != ProtocolVersion2 || app.Namespace != "app.test" {
		t.Fatalf("gen.AppManifest contract fields mismatch: %+v", app)
	}
	if len(app.Callables) != 1 || app.Callables[0].ID != "ping" {
		t.Fatalf("gen.AppManifest callable mismatch: %+v", app.Callables)
	}
}

func TestValidateToolName_Valid(t *testing.T) {
	cases := []string{"tool", "my_tool", "my-tool", "tool123", "A_b-C"}
	for _, name := range cases {
		if err := ValidateToolName(name); err != nil {
			t.Errorf("ValidateToolName(%q) = %v, want nil", name, err)
		}
	}
}

func TestValidateToolName_Empty(t *testing.T) {
	if err := ValidateToolName(""); err == nil {
		t.Error("ValidateToolName('') should return error")
	}
}

func TestValidateToolName_InvalidChars(t *testing.T) {
	cases := []string{"my tool", "my.tool", "my/tool", "my@tool"}
	for _, name := range cases {
		if err := ValidateToolName(name); err == nil {
			t.Errorf("ValidateToolName(%q) should return error", name)
		}
	}
}

func TestToAppManifestPassthroughNewFields(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:     "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{
				ID:             "ping",
				RequestSchema:  "PingRequest",
				ResponseSchema: "PingResponse",
				Effect:         "read",
				Service:        "myservice",
				ToolName:       "my-ping-tool",
				Streaming:      true,
			},
		},
	}
	app, err := m.ToAppManifest()
	if err != nil {
		t.Fatalf("ToAppManifest: %v", err)
	}
	c := app.Callables[0]
	if c.Effect != "read" {
		t.Fatalf("Effect mismatch: got %q want %q", c.Effect, "read")
	}
	if c.Service != "myservice" {
		t.Fatalf("Service mismatch: got %q want %q", c.Service, "myservice")
	}
	if c.ToolName != "my-ping-tool" {
		t.Fatalf("ToolName mismatch: got %q want %q", c.ToolName, "my-ping-tool")
	}
	if !c.Streaming {
		t.Fatal("Streaming mismatch: got false want true")
	}
}

func TestToAppManifestRejectsInvalidToolName(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{
				ID:             "bad",
				RequestSchema:  "Req",
				ResponseSchema: "Resp",
				ToolName:       "bad.tool.name",
			},
		},
	}
	if _, err := m.ToAppManifest(); err == nil {
		t.Fatal("expected rejection for invalid ToolName")
	}
}

func TestToAppManifestEmptyToolNameOK(t *testing.T) {
	// Empty ToolName must be accepted — the consumer falls back to
	// dot-folded callable ID (agent toolregistry.go).
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{ID: "ping", RequestSchema: "Req", ResponseSchema: "Resp"},
		},
	}
	if _, err := m.ToAppManifest(); err != nil {
		t.Fatalf("empty ToolName should be accepted: %v", err)
	}
}

func TestToAppManifestBundlesPassthrough(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Callables: []Callable{
			{ID: "ping", RequestSchema: "PingReq", ResponseSchema: "PingResp"},
			{ID: "echo", RequestSchema: "EchoReq", ResponseSchema: "EchoResp", Effect: "read", Streaming: true},
		},
		Bundles: []Bundle{
			{
				Title:       "Network Tools",
				Description: "Basic network utilities",
				Tools: []BundleTool{
					{CallableID: "ping", Effect: "none"},
					{CallableID: "echo", Effect: "read", Service: "myservice", ToolName: "my-echo", Stream: true},
				},
			},
		},
	}
	app, err := m.ToAppManifest()
	if err != nil {
		t.Fatalf("ToAppManifest: %v", err)
	}
	if len(app.Bundles) != 1 {
		t.Fatalf("expected 1 bundle, got %d", len(app.Bundles))
	}
	b := app.Bundles[0]
	if b.Title != "Network Tools" {
		t.Fatalf("bundle Title mismatch: got %q", b.Title)
	}
	if b.Description != "Basic network utilities" {
		t.Fatalf("bundle Description mismatch: got %q", b.Description)
	}
	if len(b.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(b.Tools))
	}
	if b.Tools[0].CallableID != "ping" || b.Tools[0].Effect != "none" {
		t.Fatalf("tool[0] mismatch: %+v", b.Tools[0])
	}
	t1 := b.Tools[1]
	if t1.CallableID != "echo" || t1.Effect != "read" || t1.Service != "myservice" || t1.ToolName != "my-echo" || !t1.Stream {
		t.Fatalf("tool[1] mismatch: %+v", t1)
	}
}

func TestToAppManifestBundlesEmptyOmitsField(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
	}
	app, err := m.ToAppManifest()
	if err != nil {
		t.Fatalf("ToAppManifest: %v", err)
	}
	if app.Bundles != nil {
		t.Fatalf("expected nil Bundles for manifest without bundles, got %+v", app.Bundles)
	}
}

func TestToAppManifestBundlesRejectsEmptyTitle(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Bundles: []Bundle{
			{Tools: []BundleTool{{CallableID: "ping"}}},
		},
	}
	if _, err := m.ToAppManifest(); err == nil {
		t.Fatal("expected rejection for bundle with empty title")
	}
}

func TestToAppManifestBundlesRejectsEmptyCallableID(t *testing.T) {
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Bundles: []Bundle{
			{Title: "My Bundle", Tools: []BundleTool{{Effect: "read"}}},
		},
	}
	if _, err := m.ToAppManifest(); err == nil {
		t.Fatal("expected rejection for bundle tool with empty CallableId")
	}
}

func TestToAppManifestBundlesJSONCompatibleWithGenAppManifest(t *testing.T) {
	// Verify SDK-produced manifest JSON with Bundles round-trips into gen.AppManifest.
	m := Manifest{
		ID:      "app.test",
		Name:    "Test",
		Version: "1.0.0",
		Bundles: []Bundle{
			{
				Title: "Tools",
				Tools: []BundleTool{
					{CallableID: "ping", Effect: "none"},
				},
			},
		},
	}.Normalize()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var app gen.AppManifest
	if err := json.Unmarshal(data, &app); err != nil {
		t.Fatalf("unmarshal into gen.AppManifest: %v", err)
	}
	if len(app.Bundles) != 1 || app.Bundles[0].Title != "Tools" {
		t.Fatalf("gen.AppManifest Bundles mismatch: %+v", app.Bundles)
	}
}

func TestDefaultAbi(t *testing.T) {
	abi := DefaultAbi()
	if abi.Encoding != BinaryCodecV1 {
		t.Fatalf("expected Encoding %q, got %q", BinaryCodecV1, abi.Encoding)
	}
	if abi.Isolation != IsolationInProcess {
		t.Fatalf("expected Isolation %q, got %q", IsolationInProcess, abi.Isolation)
	}
	if abi.TrustClass != TrustFirstParty {
		t.Fatalf("expected TrustClass %q, got %q", TrustFirstParty, abi.TrustClass)
	}
	if abi.Signer != DefaultSigner {
		t.Fatalf("expected Signer %q, got %q", DefaultSigner, abi.Signer)
	}
	if abi.ContractVersion != ContractVersion1 {
		t.Fatalf("expected ContractVersion %q, got %q", ContractVersion1, abi.ContractVersion)
	}
	if abi.InvokeSymbol != "PluginInvoke" {
		t.Fatalf("expected InvokeSymbol %q, got %q", "PluginInvoke", abi.InvokeSymbol)
	}
}

func TestCallableDispatch(t *testing.T) {
	RegisterCallable("greet", func(req Request) (Response, error) {
		return Response{Payload: map[string]string{"hello": "world"}}, nil
	})
	defer UnregisterCallable("greet")

	data, err := Dispatch("com.example.hello", "greet", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["hello"] != "world" {
		t.Fatalf("unexpected response: %v", got)
	}
}

func TestCallableDispatchNotFound(t *testing.T) {
	_, err := Dispatch("com.example.hello", "missing", nil)
	if err == nil {
		t.Fatal("expected error for missing callable")
	}
}

func TestFrameEncodeDecodeRoundTrip(t *testing.T) {
	// Verify encode/decode round-trip.
	payload := []byte(`{"hello":"world"}`)
	frame := encodeFrame(payload)

	decoded, err := decodeFrame(frame)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", decoded, payload)
	}

	// Empty payload.
	emptyFrame := encodeFrame(nil)
	got, err := decodeFrame(emptyFrame)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d bytes", len(got))
	}

	// Truncated frame rejected.
	if _, err := decodeFrame(frame[:len(frame)-1]); err == nil {
		t.Fatal("expected truncated frame to be rejected")
	}

	// Length mismatch rejected.
	bad := make([]byte, 8)
	bad[3] = 100 // declares 100 bytes payload
	if _, err := decodeFrame(bad); err == nil {
		t.Fatal("expected length mismatch to be rejected")
	}

	// Too short (below header).
	if _, err := decodeFrame([]byte{0, 0, 0}); err == nil {
		t.Fatal("expected short frame to be rejected")
	}
}

// ── Panic recovery tests ────────────────────────────────────────────────────

func TestHandleOnLoadRecoversPanic(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "app.panic.load", Name: "Panic Load", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			panic("boom on load")
		},
	})
	defer unregisterPluginCallables("app.panic.load")

	id := cstr("app.panic.load")
	if got := HandleOnLoad(id, nil); got != -1 {
		t.Fatalf("expected -1, got %d", got)
	}
	// The plugin must not be marked as loaded after a panic.
	if _, err := currentState(); err == nil {
		t.Fatal("expected plugin not to be active after OnLoad panic")
	}
	HandleOnUnload(id)
}

func TestHandleOnUnloadRecoversPanic(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "app.panic.unload", Name: "Panic Unload", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("echo", func(req Request) (Response, error) {
				return Response{Payload: map[string]string{"ok": "true"}}, nil
			})
			return nil
		},
		OnUnload: func(ctx Context) error {
			panic("boom on unload")
		},
	})

	id := cstr("app.panic.unload")
	if got := HandleOnLoad(id, nil); got != 0 {
		t.Fatalf("load failed: %d", got)
	}
	if got := HandleOnUnload(id); got != -1 {
		t.Fatalf("expected -1, got %d", got)
	}
	// Callable must still be unregistered despite the panic.
	_, err := Dispatch("app.panic.unload", "echo", nil)
	if err == nil {
		t.Fatal("expected callable to be unregistered after OnUnload panic")
	}
}

func TestHandleOnConfigChangeRecoversPanic(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "app.panic.config", Name: "Panic Config", Version: "1.0.0"},
		OnLoad:   func(ctx Context) error { return nil },
		OnConfigChange: func(ctx Context, config json.RawMessage) error {
			panic("boom on config change")
		},
	})
	defer unregisterPluginCallables("app.panic.config")

	id := cstr("app.panic.config")
	if got := HandleOnLoad(id, nil); got != 0 {
		t.Fatalf("load failed: %d", got)
	}
	cfg := cstr(`{"x":1}`)
	if got := HandleOnConfigChange(id, cfg); got != -1 {
		t.Fatalf("expected -1, got %d", got)
	}
	HandleOnUnload(id)
}

func TestDispatchRecoversPanic(t *testing.T) {
	Register(&Plugin{
		Manifest: Manifest{ID: "app.panic.callable", Name: "Panic Callable", Version: "1.0.0"},
		OnLoad: func(ctx Context) error {
			ctx.RegisterCallable("panic", func(req Request) (Response, error) {
				panic("boom in callable")
			})
			return nil
		},
	})
	defer unregisterPluginCallables("app.panic.callable")

	id := cstr("app.panic.callable")
	if got := HandleOnLoad(id, nil); got != 0 {
		t.Fatalf("load failed: %d", got)
	}

	_, err := Dispatch("app.panic.callable", "panic", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error from panicking callable")
	}
	if !strings.Contains(err.Error(), "boom in callable") {
		t.Fatalf("expected panic message in error, got: %v", err)
	}
	HandleOnUnload(id)
}

func TestPanicErrorFormats(t *testing.T) {
	// Verify panicError produces readable messages for each panic type.
	e := panicError(fmt.Errorf("typed error"))
	if e == nil || !strings.Contains(e.Error(), "typed error") {
		t.Fatalf("expected typed error message, got %v", e)
	}
	e = panicError("string panic")
	if e == nil || !strings.Contains(e.Error(), "string panic") {
		t.Fatalf("expected string panic message, got %v", e)
	}
	e = panicError(42)
	if e == nil || !strings.Contains(e.Error(), "42") {
		t.Fatalf("expected numeric panic message, got %v", e)
	}
	e = panicError(nil)
	if e != nil {
		t.Fatalf("expected nil for nil panic, got %v", e)
	}
}
