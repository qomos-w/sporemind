package pluginloader

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestPluginEndToEnd is the regression baseline for native plugin development.
// It exercises the full compile → load → invoke chain with the SDK example
// plugin (sporemind-plugin-sdk/examples/hello), proving the ABI framing,
// OnLoad callable registration, and invoke routing are all intact.
//
// The test uses the process-wide shared library handle (sharedExampleLibrary)
// because a Go c-shared runtime can only be initialised once per process.
// OnLoad is therefore also invoked exactly once via onLoadOnce.
//
// Skipped when:
//   - the platform cannot load shared libraries (js/wasm)
//   - the SDK example source is not checked out
func TestPluginEndToEnd(t *testing.T) {
	skipIfNoSharedLibSupport(t)

	lib, _ := sharedExampleLibrary(t)
	const pluginID = "com.example.hello"
	onLoadOnce(t, lib, pluginID)

	// Invoke "greet" with {"name":"World"} and expect {"message":"Hello, World!"}.
	greetResp := invokeCallable(t, lib, "greet", json.RawMessage(`{"name":"World"}`))
	var greet struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(greetResp, &greet); err != nil {
		t.Fatalf("unmarshal greet response %q: %v", greetResp, err)
	}
	if greet.Message != "Hello, World!" {
		t.Fatalf("greet message = %q, want %q", greet.Message, "Hello, World!")
	}

	// Invoke "ping" and expect {"pong":"ok"}.
	pingResp := invokeCallable(t, lib, "ping", json.RawMessage(`{}`))
	var ping struct {
		Pong string `json:"pong"`
	}
	if err := json.Unmarshal(pingResp, &ping); err != nil {
		t.Fatalf("unmarshal ping response %q: %v", pingResp, err)
	}
	if ping.Pong != "ok" {
		t.Fatalf("ping pong = %q, want %q", ping.Pong, "ok")
	}
}

// TestPluginInvoke_RetryOnBufferTooSmall verifies the dynamic retry path.
// It invokes "greet" with a deliberately tiny response capacity (1 byte).
// The SDK detects the buffer is too small, returns StatusBufferTooSmall with
// the needed size, and lib.Invoke retries transparently with the right
// buffer — the response must be identical to a normal invocation.
func TestPluginInvoke_RetryOnBufferTooSmall(t *testing.T) {
	skipIfNoSharedLibSupport(t)

	lib, _ := sharedExampleLibrary(t)
	const pluginID = "com.example.hello"
	onLoadOnce(t, lib, pluginID)

	env := gen.PluginAbiInvokeEnvelope{
		Callable: "greet",
		Payload:  json.RawMessage(`{"name":"Retry"}`),
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	frame := encodeFrameForTest(body)

	// capacity=1 forces the first attempt to fail with StatusBufferTooSmall;
	// the host retries with the needed size.
	resp, err := lib.Invoke(NativeInvokeSymbol, frame, 1)
	if err != nil {
		t.Fatalf("invoke with tiny capacity (retry expected): %v", err)
	}
	decoded, err := decodeFrameForTest(resp)
	if err != nil {
		t.Fatalf("decode response frame: %v", err)
	}
	var greet struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(decoded, &greet); err != nil {
		t.Fatalf("unmarshal greet response %q: %v", decoded, err)
	}
	if greet.Message != "Hello, Retry!" {
		t.Fatalf("greet message = %q, want %q", greet.Message, "Hello, Retry!")
	}
}

// onLoadOnce ensures PluginOnLoad is called exactly once for the shared
// example library. The SDK's OnLoad is idempotent (returns 0 if already
// loaded), but calling it repeatedly is wasteful and we want deterministic
// ordering.
var (
	onLoadOnceFlag sync.Once
	onLoadOnceErr  error
)

func onLoadOnce(t *testing.T, lib *Library, pluginID string) {
	t.Helper()
	onLoadOnceFlag.Do(func() {
		onLoadOnceErr = callOnLoadForTest(lib, pluginID)
	})
	if onLoadOnceErr != nil {
		t.Fatalf("OnLoad: %v", onLoadOnceErr)
	}
}

// invokeCallable wraps a callable dispatch over the length-prefixed ABI,
// encoding the {callable, payload} envelope and decoding the response frame.
func invokeCallable(t *testing.T, lib *Library, callable string, payload json.RawMessage) json.RawMessage {
	t.Helper()
	env := gen.PluginAbiInvokeEnvelope{
		Callable: callable,
		Payload:  payload,
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	frame := encodeFrameForTest(body)
	resp, err := lib.Invoke(NativeInvokeSymbol, frame, 1<<20+len(frame))
	if err != nil {
		t.Fatalf("invoke %q: %v", callable, err)
	}
	decoded, err := decodeFrameForTest(resp)
	if err != nil {
		t.Fatalf("decode response frame: %v", err)
	}
	return json.RawMessage(decoded)
}

// callOnLoadForTest mirrors pkg/actor/pluginhost.callOnLoad without the
// import cycle.
func callOnLoadForTest(lib *Library, pluginID string) error {
	var fn func(pluginIDPtr, configPtr *byte) int32
	if err := lib.Symbol("PluginOnLoad", &fn); err != nil {
		return nil
	}
	idBytes := append([]byte(pluginID), 0)
	if status := fn(&idBytes[0], nil); status != 0 {
		return errStatus(status)
	}
	return nil
}

// ── frame helpers (mirror pkg/pluginhost ABI without the import cycle) ──

const testAbiHeaderSize = 4

func encodeFrameForTest(payload []byte) []byte {
	frame := make([]byte, testAbiHeaderSize+len(payload))
	frame[0] = byte(len(payload) >> 24)
	frame[1] = byte(len(payload) >> 16)
	frame[2] = byte(len(payload) >> 8)
	frame[3] = byte(len(payload))
	copy(frame[testAbiHeaderSize:], payload)
	return frame
}

func decodeFrameForTest(frame []byte) ([]byte, error) {
	if len(frame) < testAbiHeaderSize {
		return nil, errStatus(1)
	}
	declared := int(frame[0])<<24 | int(frame[1])<<16 | int(frame[2])<<8 | int(frame[3])
	if declared != len(frame)-testAbiHeaderSize {
		return nil, errStatus(2)
	}
	return frame[testAbiHeaderSize:], nil
}

type statusError int32

func (s statusError) Error() string { return "plugin status" }
func errStatus(s int32) error      { return statusError(s) }
