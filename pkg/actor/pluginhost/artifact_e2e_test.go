package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestArtifactLoaderEndToEnd exercises the full production load path for a
// native plugin: real ArtifactLoader → real transportOpener (T7 selection) →
// real processOpener (subprocess transport) → real OnLoad lifecycle hook →
// handler registration → framed invoke. This is the integration counterpart to
// pkg/pluginloader/e2e_test.go, which tests the lower-level pluginloader.Open
// directly; the subprocess-transport variant of this path is covered in
// subprocess_e2e_test.go with reverse-call round trips.
//
// It verifies that ArtifactLoader.Load:
//   - opens the compiled subprocess executable via the process transport,
//   - invokes OnLoad so the plugin registers its callables,
//   - installs namespaced handlers that route PluginInvokeReq to the plugin,
//   - returns frames whose payloads match the plugin's declared contract.
//
// Skipped on js/wasm and when the SDK example source is not checked out.
func TestArtifactLoaderEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("native plugins not supported on this platform")
	}
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	pluginDir := filepath.Join(sdkDir, "examples", "hello")
	if _, err := os.Stat(filepath.Join(pluginDir, "main.go")); err != nil {
		t.Skipf("SDK example not checked out at %s", pluginDir)
	}

	libPath := buildHelloPluginForArtifactTest(t)

	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	// transportOpener routes the executable artifact to the subprocess
	// transport (dev mode); the in-process purego opener is unused here.
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: &processOpener{}})

	manifest := gen.AppManifest{
		ID:              "com.example.hello",
		Name:            "Hello Plugin",
		Version:         "1.0.0",
		Runtime:         pluginhost.NativeRuntime,
		ProtocolVersion: 2,
		Namespace:       "plugin.com.example.hello",
		Callables: []gen.AppCallableDescriptor{
			{ID: "greet", RequestSchema: "greetReq", ResponseSchema: "greetResp"},
			{ID: "ping", RequestSchema: "pingReq", ResponseSchema: "pingResp"},
		},
	}
	abi := gen.PluginAbi{
		Name:            pluginhost.BinaryCodecV1,
		Version:         1,
		Encoding:        pluginhost.BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       pluginhost.IsolationSubprocess,
		TrustClass:      pluginhost.TrustFirstParty,
		Signer:          "sporemind.first-party",
	}

	_, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: libPath,
		Manifest:     manifest,
		Abi:          abi,
	})
	if err != nil {
		t.Fatalf("ArtifactLoader.Load: %v", err)
	}

	// greet: the handler should have been installed under the namespaced route.
	greetRoute := pluginhost.PluginCallID("com.example.hello", "greet")
	h, ok := host.handler(greetRoute)
	if !ok {
		t.Fatalf("handler for %q not registered", greetRoute)
	}
	resp, err := h(context.Background(), []byte(`{"name":"World"}`))
	if err != nil {
		t.Fatalf("invoke greet: %v", err)
	}
	var greet struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp, &greet); err != nil {
		t.Fatalf("unmarshal greet response %q: %v", resp, err)
	}
	if greet.Message != "Hello, World!" {
		t.Fatalf("greet message = %q, want %q", greet.Message, "Hello, World!")
	}

	// ping
	pingRoute := pluginhost.PluginCallID("com.example.hello", "ping")
	hp, ok := host.handler(pingRoute)
	if !ok {
		t.Fatalf("handler for %q not registered", pingRoute)
	}
	resp, err = hp(context.Background(), []byte(`{}`))
	if err != nil {
		t.Fatalf("invoke ping: %v", err)
	}
	var ping struct {
		Pong string `json:"pong"`
	}
	if err := json.Unmarshal(resp, &ping); err != nil {
		t.Fatalf("unmarshal ping response %q: %v", resp, err)
	}
	if ping.Pong != "ok" {
		t.Fatalf("ping pong = %q, want %q", ping.Pong, "ok")
	}
}

// captureHost is a minimal pluginhost.ArtifactHost that records registered
// handlers so the test can drive them directly.
type captureHost struct {
	handlers map[string]pluginhost.HandlerFunc
}

func (h *captureHost) RegisterHandler(callID string, fn pluginhost.HandlerFunc) {
	if h.handlers == nil {
		h.handlers = make(map[string]pluginhost.HandlerFunc)
	}
	h.handlers[callID] = fn
}

func (h *captureHost) RegisterHandlerStream(callID string, fn pluginhost.HandlerStreamFunc) {
	// Streaming capture is exercised in the dedicated stream e2e tests; the
	// artifact-path capture host ignores stream registrations.
	_ = callID
	_ = fn
}

func (h *captureHost) UnregisterHandler(callID string) {
	delete(h.handlers, callID)
}

func (h *captureHost) RegisterDescriptor(pluginhost.PluginDescriptorData) {}
func (h *captureHost) RemoveDescriptor(string)                            {}
func (h *captureHost) RegisterCloser(string, func() error)                {}
func (h *captureHost) RemoveCloser(string)                                {}
func (h *captureHost) ReplaceArtifact(oldCallIDs []string, handlers map[string]pluginhost.HandlerFunc, _ map[string]pluginhost.HandlerStreamFunc, _ pluginhost.PluginDescriptorData, oldCloser, _ func() error) error {
	if oldCloser != nil {
		if err := oldCloser(); err != nil {
			return err
		}
	}
	for _, callID := range oldCallIDs {
		delete(h.handlers, callID)
	}
	for callID, handler := range handlers {
		h.handlers[callID] = handler
	}
	return nil
}

func (h *captureHost) handler(callID string) (pluginhost.HandlerFunc, bool) {
	fn, ok := h.handlers[callID]
	return fn, ok
}

// buildHelloPluginForArtifactTest compiles the SDK example into a process-
// scoped temp directory (the loaded artifact stays alive until process exit:
// Windows keeps a running executable locked, so an auto-cleaning t.TempDir
// would fail its post-test removal). The example sources are staged into a
// temp module whose go.mod replaces resolve absolutely (pkg/testutil), so the
// build works in any checkout layout — the checked-in relative replaces only
// resolve in the main checkout tree. The artifact is a subprocess-mode
// executable (CGO_ENABLED=0, no -buildmode).
func buildHelloPluginForArtifactTest(t *testing.T) string {
	t.Helper()
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	pluginDir := filepath.Join(sdkDir, "examples", "hello")
	tmpDir, err := os.MkdirTemp("", "sporemind-artifact-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	outPath := filepath.Join(tmpDir, "plugin-hello"+exeSuffix())
	modDir := testutil.StageExampleModuleDir(t, pluginDir, tmpDir)
	testutil.GoRun(t, modDir, []string{"CGO_ENABLED=0"}, "build", "-o", outPath, ".")
	return outPath
}
