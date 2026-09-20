package pluginhost

// Subprocess-transport e2e tests (task T8, workflow-plan-native-plugin-000181).
//
// These tests exercise the FULL production load path for the subprocess
// (dev) transport — real ArtifactLoader -> real transportOpener (T7
// selection) -> real processOpener (T5) -> real HostBridge (T6) -> the SDK
// hello example built as a standalone executable (T4) — the counterpart of
// TestArtifactLoaderEndToEnd, which covers the same path over the FFI
// (in-process) transport.
//
// The hello plugin is compiled from sporemind-plugin-sdk/examples/hello into
// a synthetic temp module (see buildHelloSubprocessForE2E): the example's
// own go.mod uses relative replaces that only resolve inside the main
// checkout tree, not inside an agent worktree, so the build is staged in a
// temp module with absolute replaces computed at runtime.
//
// Coverage mapped to the task card:
//  1. TestSubprocessHelloEndToEnd — forward invoke (greet) + one reverse call
//     (llm.complete) full round trip, then a clean unload.
//  2. TestSubprocessHelloHotReloadRespawn — kill+respawn cycle: unload kills
//     the old process, load of the (changed) artifact respawns it, and OnLoad
//     re-registers the callables (greet/reverse work again).
//  3. TestSubprocessHelloCrashDetectAndRecover — mid-shutdown EOF is reported
//     as an error (never a panic) and a reload recovers.
//  4. TestSubprocessInvokeTimeoutKillRespawn — invoke timeout kills the
//     process and reports a timeout error; a fresh load respawns cleanly.
//  5. TestSubprocessReverseNestedTimeout — the T5 nested-timeout choice: the
//     reverse call expires on its budget DERIVED FROM the outer invoke budget
//     and fails via the __host_error__ envelope while the process survives.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// buildHelloSubprocessForE2E compiles the SDK hello example into a
// subprocess-mode executable (CGO_ENABLED=0) via the shared staged-module
// helper (pkg/testutil): the example sources are copied into a temp module
// whose go.mod replaces point at the SDK copy next to this test (the
// worktree's sporemind-plugin-sdk, carrying the T3/T4 changes) and the spore
// module resolved via `go list -m`, so the same e2e source builds in any
// checkout layout.
func buildHelloSubprocessForE2E(t *testing.T) string {
	return buildHelloSubprocessVariant(t, nil)
}

// buildHelloSubprocessVariant is buildHelloSubprocessForE2E with a hook that
// may mutate the staged module sources between tidy and build — used by the
// hot-reload test to produce a genuinely different binary (simulated binary
// change). mutate must not require a re-tidy (only source edits, e.g. a
// version bump).
func buildHelloSubprocessVariant(t *testing.T, mutate func(t testing.TB, modDir string)) string {
	t.Helper()
	sdkDir, ok := testutil.FindSDKDir(t)
	if !ok {
		t.Skip("sporemind-plugin-sdk checkout not found")
	}
	pluginDir := filepath.Join(sdkDir, "examples", "hello")
	if _, err := os.Stat(filepath.Join(pluginDir, "main.go")); err != nil {
		t.Skipf("SDK example not checked out at %s", pluginDir)
	}
	buildDir := t.TempDir()
	modDir := testutil.StageExampleModuleDir(t, pluginDir, buildDir)
	if mutate != nil {
		mutate(t, modDir)
	}
	exe := filepath.Join(buildDir, "plugin-hello"+exeSuffix())
	testutil.GoRun(t, modDir, []string{"CGO_ENABLED=0"}, "build", "-o", exe, ".")
	return exe
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// helloManifest describes the SDK hello plugin with a host capability that
// lets the "reverse" callable's llm.* reverse calls through the gate.
func helloManifest() gen.AppManifest {
	return gen.AppManifest{
		ID:              "com.example.hello",
		Name:            "Hello Plugin",
		Version:         "1.0.0",
		Runtime:         pluginhost.NativeRuntime,
		ProtocolVersion: 2,
		Namespace:       "plugin.com.example.hello",
		Permissions:     []string{appbinding.CapLLMInvoke},
		Callables: []gen.AppCallableDescriptor{
			{ID: "greet", RequestSchema: "GreetRequest", ResponseSchema: "GreetResponse"},
			{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse"},
			{ID: "reverse", RequestSchema: "ReverseRequest", ResponseSchema: "ReverseResponse"},
			// ask (SDK examples/hello) consumes llm.complete via
			// CompleteStream; declared so the streaming e2e can invoke it.
			{ID: "ask", RequestSchema: "AskRequest", ResponseSchema: "AskResponse"},
			// ask_cancel (SDK examples/hello) cancels its InvokeStreamCtx
			// mid-stream; declared so the cancel e2e can invoke it.
			{ID: "ask_cancel", RequestSchema: "AskRequest", ResponseSchema: "AskResponse"},
		},
	}
}

func subprocessAbi() gen.PluginAbi {
	return gen.PluginAbi{
		Name:            pluginhost.BinaryCodecV1,
		Version:         1,
		Encoding:        pluginhost.BinaryCodecV1,
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       pluginhost.IsolationSubprocess,
		TrustClass:      pluginhost.TrustFirstParty,
		Signer:          pluginhost.NativeBuildSigner,
	}
}

// reverseRecorder is the backing dispatch for the subprocess transport's
// capability-gated HostBridge: it records reverse call IDs and can block a
// call to exercise the nested-timeout path.
type reverseRecorder struct {
	mu    sync.Mutex
	calls []string
	block map[string]<-chan struct{}
	// after, when set, runs (e.g. to block) before answering.
	after func(callID string)
}

func (r *reverseRecorder) dispatch(callID string, req []byte) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, callID)
	block := r.block[callID]
	after := r.after
	r.mu.Unlock()
	if block != nil {
		<-block
	}
	if after != nil {
		after(callID)
	}
	return []byte(`{"echo":true}`), nil
}

func (r *reverseRecorder) recorded(callID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c == callID {
			return true
		}
	}
	return false
}

// newSubprocessE2ELoader wires an ArtifactLoader with a dev-mode transport
// opener whose subprocess transport routes reverse calls to dispatch (nil
// falls back to a recorder). env optionally overrides the child environment
// (used to flag a re-exec test binary as the plugin process). It returns the
// loader, the recording host, the log sink recorder, and the *processOpener
// for direct introspection (pid, crash state).
func newSubprocessE2ELoader(t *testing.T, dispatch HostDispatchFunc, env []string) (*pluginhost.ArtifactLoader, *captureHost, *eventRecorder, *processOpener) {
	t.Helper()
	host := &captureHost{}
	loader := pluginhost.NewArtifactLoader(host)
	rec := &eventRecorder{}
	pop := &processOpener{dispatch: dispatch, logger: rec, env: env, settleGrace: 150 * time.Millisecond}
	loader.SetOpener(&transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: pop})
	return loader, host, rec, pop
}

func loadHello(t *testing.T, loader *pluginhost.ArtifactLoader, exe string) {
	t.Helper()
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     helloManifest(),
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("ArtifactLoader.Load: %v", err)
	}
	t.Cleanup(func() {
		_, _ = loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "com.example.hello"})
	})
}

// invokeGreet drives the installed greet handler and returns its message.
func invokeGreet(t *testing.T, host *captureHost, name string) string {
	t.Helper()
	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "greet"))
	if !ok {
		t.Fatalf("handler for greet not registered (OnLoad did not run?)")
	}
	req, _ := json.Marshal(map[string]string{"name": name})
	resp, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("invoke greet: %v", err)
	}
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal greet response %q: %v", resp, err)
	}
	if out.Message != "Hello, "+name+"!" {
		t.Fatalf("greet message = %q, want %q", out.Message, "Hello, "+name+"!")
	}
	return out.Message
}

// TestSubprocessHelloEndToEnd is task item (1): subprocess-mode forward
// invoke + one reverse call round trip through the full production stack
// (ArtifactLoader -> transportOpener -> processOpener -> HostBridge -> hello
// plugin), followed by a clean unload.
func TestSubprocessHelloEndToEnd(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	exe := buildHelloSubprocessForE2E(t)
	rev := &reverseRecorder{}
	loader, host, _, _ := newSubprocessE2ELoader(t, rev.dispatch, nil)

	loadHello(t, loader, exe)

	// Forward invoke: greet.
	invokeGreet(t, host, "e2e")

	// Reverse call round trip: the hello "reverse" callable issues a
	// plugin->host bridge call (0x03/0x04 frame pair) to llm.complete; the
	// capability gate allows it (manifest Permission llm.invoke) and the
	// backing dispatch answers {"echo":true}. The invoke-resp payload is the
	// dispatch result verbatim.
	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "reverse"))
	if !ok {
		t.Fatalf("handler for reverse not registered")
	}
	req, _ := json.Marshal(map[string]any{"callID": "llm.complete", "payload": map[string]any{"prompt": "hi"}})
	resp, err := h(context.Background(), req)
	if err != nil {
		t.Fatalf("invoke reverse: %v", err)
	}
	if string(resp) != `{"echo":true}` {
		t.Fatalf("reverse resp = %s, want the host dispatch result verbatim", resp)
	}
	if !rev.recorded("llm.complete") {
		t.Fatal("reverse dispatch never saw llm.complete")
	}

	// Clean unload: kills the process, reports no error.
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "com.example.hello"}); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if _, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "greet")); ok {
		t.Fatal("greet handler still registered after unload")
	}
}

// TestSubprocessHelloHotReloadRespawn is task item (2): a kill+respawn cycle
// simulating a binary change. Unload kills the old process; loading a fresh
// artifact path respawns it and OnLoad (the first invoke-req after spawn)
// re-registers the callables so greet and reverse work again.
func TestSubprocessHelloHotReloadRespawn(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	exe := buildHelloSubprocessForE2E(t)
	// Simulated binary change: rebuild from a staged copy whose manifest
	// version was bumped, so the second artifact is genuinely different
	// (distinct content hash, same behavior).
	changed := buildHelloSubprocessVariant(t, func(t testing.TB, modDir string) {
		p := filepath.Join(modDir, "main.go")
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read staged plugin source: %v", err)
		}
		data = bytes.Replace(data, []byte(`Version:     "1.0.0"`), []byte(`Version:     "2.0.0"`), 1)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatalf("write modified plugin source: %v", err)
		}
	})
	hashFile := func(p string) string {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read artifact %s: %v", p, err)
		}
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	if hashFile(exe) == hashFile(changed) {
		t.Fatal("simulated binary change produced an identical artifact; reload would be vacuous")
	}

	rev := &reverseRecorder{}
	loader, host, rec, _ := newSubprocessE2ELoader(t, rev.dispatch, nil)

	loadHello(t, loader, exe)
	invokeGreet(t, host, "first")

	// Unload = kill the old process (clean, no error).
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "com.example.hello"}); err != nil {
		t.Fatalf("unload before reload: %v", err)
	}

	// Load the "changed" artifact: respawn + OnLoad re-registration.
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: changed,
		Manifest:     helloManifest(),
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("reload: %v", err)
	}
	invokeGreet(t, host, "second")
	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "reverse"))
	if !ok {
		t.Fatal("reverse handler not re-registered after respawn (OnLoad did not run?)")
	}
	req, _ := json.Marshal(map[string]any{"callID": "llm.complete", "payload": map[string]any{}})
	if _, err := h(context.Background(), req); err != nil {
		t.Fatalf("reverse after respawn: %v", err)
	}
	if !rev.recorded("llm.complete") {
		t.Fatal("reverse dispatch never saw llm.complete after the respawn round trip")
	}

	// The cycle really spawned two processes and unloaded one.
	spawns, unloads := 0, 0
	for _, e := range rec.list() {
		switch e {
		case "log: pluginhost: subprocess spawned":
			spawns++
		case "log: pluginhost: subprocess terminated by unload":
			unloads++
		}
	}
	if spawns != 2 {
		t.Fatalf("spawn diagnostics = %d, want 2 (kill+respawn)", spawns)
	}
	if unloads != 1 {
		t.Fatalf("unload diagnostics = %d, want 1", unloads)
	}
}

// TestSubprocessHelloCrashDetectAndRecover is task item (3): a mid-shutdown
// EOF (process killed externally while the loader holds it) surfaces as an
// error on the next invoke — never a panic — and a normal unload + reload
// recovers the plugin.
func TestSubprocessHelloCrashDetectAndRecover(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	exe := buildHelloSubprocessForE2E(t)
	loader, host, _, pop := newSubprocessE2ELoader(t, nil, nil)
	// transportOpener clones a fresh processOpener per load; observeClone
	// captures the live spawn so the crash simulation below can kill it.
	var live *processOpener
	pop.observeClone = func(o *processOpener) { live = o }
	loadHello(t, loader, exe)
	invokeGreet(t, host, "alive")

	// Simulate a crash: kill the child process out from under the loader.
	if live == nil {
		t.Fatal("no live processOpener clone captured via observeClone")
	}
	live.killMu.Lock()
	proc := live.cmd.Process
	live.killMu.Unlock()
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}

	// The next invoke sees the pipe EOF before any invoke-resp: reported as
	// an error with the mid-invoke classification, not a panic.
	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "greet"))
	if !ok {
		t.Fatal("greet handler missing")
	}
	_, err := h(context.Background(), []byte(`{"name":"post-crash"}`))
	if err == nil {
		t.Fatal("expected an error for the crashed plugin, got nil")
	}
	if !strings.Contains(err.Error(), "mid-invoke") {
		t.Fatalf("err = %v, want mid-invoke classification", err)
	}

	// Normal unload after the crash is clean (the process was already reaped).
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "com.example.hello"}); err != nil {
		t.Fatalf("unload after crash: %v", err)
	}

	// Reload respawns: OnLoad re-registers and invoke works again.
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     helloManifest(),
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("reload after crash: %v", err)
	}
	invokeGreet(t, host, "recovered")
}

// TestSubprocessInvokeTimeoutKillRespawn is task item (4): an invoke that
// outlives its caller context is killed (pipe EOF) and reported as a timeout
// error; the plugin is reaped (no leak), unload is clean, and a fresh load
// respawns a healthy process. The re-exec test binary (process_opener_test.go
// runTestPlugin) provides the "stall" callable the hello example lacks.
func TestSubprocessInvokeTimeoutKillRespawn(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	loader, host, _, _ := newSubprocessE2ELoader(t, nil, append(os.Environ(), testPluginEnv+"=1"))
	manifest := gen.AppManifest{
		ID: "test.plugin", Name: "Test Plugin", Version: "1.0.0",
		Runtime: pluginhost.NativeRuntime, ProtocolVersion: 2,
		Namespace: "plugin.test.plugin",
		Callables: []gen.AppCallableDescriptor{
			{ID: "stall", RequestSchema: "stallReq", ResponseSchema: "stallResp"},
			{ID: "ping", RequestSchema: "pingReq", ResponseSchema: "pingResp"},
		},
	}
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     manifest,
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() {
		_, _ = loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.plugin"})
	})

	stall := pluginhost.PluginCallID("test.plugin", "stall")
	h, ok := host.handler(stall)
	if !ok {
		t.Fatalf("handler for %q not registered", stall)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = h(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") || !strings.Contains(err.Error(), "plugin killed") {
		t.Fatalf("invoke stall: err = %v, want timeout error with kill notice", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v, plugin was not killed and reaped", elapsed)
	}
	// The killed process stays recorded as exited.
	_, err = h(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "process exited") {
		t.Fatalf("invoke after timeout kill: err = %v, want process exited", err)
	}

	// Unload is clean after the timeout kill...
	if _, err := loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.plugin"}); err != nil {
		t.Fatalf("unload after timeout: %v", err)
	}
	// ...and a fresh load respawns a working process (OnLoad re-registration).
	if _, err := loader.Load(context.Background(), gen.PluginArtifactLoadReq{
		ArtifactPath: exe,
		Manifest:     manifest,
		Abi:          subprocessAbi(),
	}); err != nil {
		t.Fatalf("reload after timeout: %v", err)
	}
	hp, ok := host.handler(pluginhost.PluginCallID("test.plugin", "ping"))
	if !ok {
		t.Fatal("ping handler not re-registered after respawn")
	}
	resp, err := hp(context.Background(), nil)
	if err != nil || string(resp) != `{"pong":"ok"}` {
		t.Fatalf("ping after respawn: resp=%s err=%v", resp, err)
	}
}

// TestSubprocessReverseNestedTimeout is task item (4) under the T5 choice:
// a slow reverse call nested inside an invoke expires on its budget DERIVED
// FROM the remaining outer invoke budget (deadline minus unwind headroom),
// fails through the __host_error__ envelope so the plugin unwinds with an
// error inside the invoke-resp, and the process is NOT killed mid-call — a
// following invoke still works.
func TestSubprocessReverseNestedTimeout(t *testing.T) {
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("subprocess transport not supported on this platform")
	}
	exe := buildHelloSubprocessForE2E(t)
	slowDone := make(chan struct{})
	defer close(slowDone) // releases the stuck dispatch goroutine at test end
	dispatch := func(callID string, req []byte) ([]byte, error) {
		if callID == "llm.slow" {
			<-slowDone // blocks well past every budget
		}
		return []byte(`{"ok":true}`), nil
	}
	loader, host, _, _ := newSubprocessE2ELoader(t, dispatch, nil)
	loadHello(t, loader, exe)

	h, ok := host.handler(pluginhost.PluginCallID("com.example.hello", "reverse"))
	if !ok {
		t.Fatal("reverse handler not registered")
	}
	outer := 1500 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), outer)
	defer cancel()
	// Mirror the production invoke path: pluginhost.invoke attaches InvokeMeta
	// before the opener sees the ctx — serveReverseAsync keys reverse-budget
	// inheritance on that meta (a bare ctx must NOT derive, so that __event__
	// fan-out invokes cannot bind unrelated HTTP reverse calls).
	metaCtx := pluginhost.WithInvokeMeta(ctx, pluginhost.InvokeMeta{RequestID: "e2e-slow"})
	start := time.Now()
	resp, err := h(metaCtx, []byte(`{"callID":"llm.slow","payload":{}}`))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("invoke reverse(llm.slow): %v (process must survive via derived reverse budget)", err)
	}
	// The reverse failed on its own derived budget (outer minus headroom), so
	// the invoke returns well within the outer deadline with the plugin's
	// error inside the invoke-resp.
	if elapsed >= outer {
		t.Fatalf("invoke took %v with an outer deadline of %v — the derived reverse budget did not fire before the outer kill", elapsed, outer)
	}
	if !strings.Contains(string(resp), "error") || !strings.Contains(string(resp), "deadline exceeded") {
		t.Fatalf("reverse resp = %s, want the plugin's derived-budget error inside invoke-resp", resp)
	}

	// The process survived (the delta is the whole point of the T5 choice).
	invokeGreet(t, host, "still-alive")
}
