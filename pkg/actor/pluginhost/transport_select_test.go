package pluginhost

import (
	"context"
	"os"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
)

func TestSelectTransportDecisionPriority(t *testing.T) {
	fp := gen.PluginAbi{Name: "p", TrustClass: ph.TrustFirstParty}
	tp := gen.PluginAbi{Name: "p", TrustClass: ph.TrustThirdParty}
	fpSub := gen.PluginAbi{Name: "p", TrustClass: ph.TrustFirstParty, Isolation: ph.IsolationSubprocess}
	fpInproc := gen.PluginAbi{Name: "p", TrustClass: ph.TrustFirstParty, Isolation: ph.IsolationInProcess}
	tpInproc := gen.PluginAbi{Name: "p", TrustClass: ph.TrustThirdParty, Isolation: ph.IsolationInProcess}

	cases := []struct {
		name       string
		abi        gen.PluginAbi
		path       string
		devMode    bool
		want       string
		wantErrSub string
	}{
		// Priority 1: third_party forces subprocess regardless of host mode.
		{"third party exe prod", tp, `/opt/plugin/plugin.exe`, false, ph.IsolationSubprocess, ""},
		{"third party exe dev", tp, `/opt/plugin/plugin.exe`, true, ph.IsolationSubprocess, ""},
		{"third party unix exe", tp, `/opt/plugin/plugin`, true, ph.IsolationSubprocess, ""},
		{"third party so dev", tp, `/opt/plugin/plugin.so`, true, "", "requires the subprocess transport"},
		{"third party dll prod", tp, `C:\plugins\plugin.dll`, false, "", "requires the subprocess transport"},
		{"third party dylib dev", tp, `/opt/plugin/plugin.dylib`, true, "", "requires the subprocess transport"},

		// Priority 2: host dev mode + subprocess artifact present.
		{"first party exe dev", fp, `/opt/plugin/plugin.exe`, true, ph.IsolationSubprocess, ""},
		{"first party unix exe dev", fp, `/opt/plugin/plugin`, true, ph.IsolationSubprocess, ""},
		{"first party upper exe dev", fp, `/opt/plugin/Plugin.EXE`, true, ph.IsolationSubprocess, ""},

		// Priority 3: everything else is inprocess — but only for shared
		// libraries; an executable on disk in prod is an explicit error.
		{"first party dll dev", fp, `C:\plugins\plugin.dll`, true, ph.IsolationInProcess, ""},
		{"first party so dev", fp, `/opt/plugin/plugin.so`, true, ph.IsolationInProcess, ""},
		{"first party dylib dev", fp, `/opt/plugin/plugin.dylib`, true, ph.IsolationInProcess, ""},
		{"first party so prod", fp, `/opt/plugin/plugin.so`, false, ph.IsolationInProcess, ""},
		{"first party exe prod", fp, `/opt/plugin/plugin.exe`, false, "", "executable artifact requires dev mode or subprocess isolation"},
		{"first party unix exe prod", fp, `/opt/plugin/plugin`, false, "", "executable artifact requires dev mode or subprocess isolation"},
		{"first party dll prod", fp, `C:\plugins\plugin.dll`, false, ph.IsolationInProcess, ""},

		// Declared isolation is the primary signal and wins on every host.
		// A first-party subprocess plugin loads via subprocess even on prod.
		{"first party subprocess exe prod", fpSub, `/opt/plugin/plugin.exe`, false, ph.IsolationSubprocess, ""},
		{"first party subprocess exe dev", fpSub, `/opt/plugin/plugin.exe`, true, ph.IsolationSubprocess, ""},
		{"first party subprocess unix exe prod", fpSub, `/opt/plugin/plugin`, false, ph.IsolationSubprocess, ""},
		{"first party subprocess shared lib", fpSub, `C:\plugins\plugin.dll`, false, "", "declares subprocess isolation but artifact"},
		{"first party inprocess dll prod", fpInproc, `C:\plugins\plugin.dll`, false, ph.IsolationInProcess, ""},
		{"first party inprocess exe prod", fpInproc, `/opt/plugin/plugin.exe`, false, "", "declares in-process isolation but artifact"},
		// third_party isolation is forced regardless of declaration.
		{"third party inprocess exe", tpInproc, `/opt/plugin/plugin.exe`, false, ph.IsolationSubprocess, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectTransport(tc.abi, tc.path, tc.devMode)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("selectTransport(%s, %s, dev=%v) = %q, %v; want error containing %q", tc.abi.TrustClass, tc.path, tc.devMode, got, err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectTransport(%s, %s, dev=%v): %v", tc.abi.TrustClass, tc.path, tc.devMode, err)
			}
			if got != tc.want {
				t.Fatalf("selectTransport(%s, %s, dev=%v) = %q, want %q", tc.abi.TrustClass, tc.path, tc.devMode, got, tc.want)
			}
		})
	}
}

func TestIsSharedLibraryArtifact(t *testing.T) {
	for _, lib := range []string{"plugin.dll", "plugin.so", "plugin.dylib", "plugin.DLL", "a/b/plugin.So", `/C:/x/plugin.dll`} {
		if !isSharedLibraryArtifact(lib) {
			t.Errorf("isSharedLibraryArtifact(%q) = false, want true", lib)
		}
	}
	for _, exe := range []string{"plugin", "plugin.exe", "plugin.bin", "plugin.EXE", "a/b/plugin", ""} {
		if isSharedLibraryArtifact(exe) {
			t.Errorf("isSharedLibraryArtifact(%q) = true, want false", exe)
		}
	}
}

// recordingOpener is a fake ArtifactOpener that records which transport got
// the load.
type recordingOpener struct {
	transport string
	opened    int
}

func (r *recordingOpener) Open(abi gen.PluginAbi, artifactPath, entrySymbol, pluginID string, allowed map[string]struct{}, onLoadConfig []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), ph.InvokeStreamFunc, func() error, string, error) {
	r.opened++
	return func(context.Context, string, []byte) ([]byte, error) { return []byte(r.transport), nil }, nil, func() error { return nil }, "", nil
}

func TestTransportOpenerRoutesToSelectedTransport(t *testing.T) {
	ffi := &recordingOpener{transport: "ffi"}
	proc := &recordingOpener{transport: "proc"}
	op := &transportOpener{inprocess: ffi, subprocess: proc}

	cases := []struct {
		name       string
		abi        gen.PluginAbi
		path       string
		devMode    bool
		wantOpened *recordingOpener
		wantErr    bool
	}{
		{name: "first party shared lib", abi: gen.PluginAbi{TrustClass: ph.TrustFirstParty}, path: "plugin.dll", wantOpened: ffi},
		{name: "first party exe dev", abi: gen.PluginAbi{TrustClass: ph.TrustFirstParty}, path: "plugin.exe", devMode: true, wantOpened: proc},
		{name: "third party exe prod", abi: gen.PluginAbi{TrustClass: ph.TrustThirdParty}, path: "plugin.exe", wantOpened: proc},
		{name: "third party shared lib", abi: gen.PluginAbi{TrustClass: ph.TrustThirdParty}, path: "plugin.so", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ffi.opened, proc.opened = 0, 0
			op.devMode = tc.devMode
			_, _, _, _, err := op.Open(tc.abi, tc.path, "", "p", nil, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected selection error")
				}
				if ffi.opened != 0 || proc.opened != 0 {
					t.Fatalf("openers must not be reached on error: ffi=%d proc=%d", ffi.opened, proc.opened)
				}
				return
			}
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if tc.wantOpened == ffi {
				if ffi.opened != 1 || proc.opened != 0 {
					t.Fatalf("want FFI open, got ffi=%d proc=%d", ffi.opened, proc.opened)
				}
			} else {
				if proc.opened != 1 || ffi.opened != 0 {
					t.Fatalf("want subprocess open, got ffi=%d proc=%d", ffi.opened, proc.opened)
				}
			}
		})
	}
}

// TestNewTransportOpenerWiresBothTransports ensures the actor-side factory
// builds the dual-transport opener with both concrete transports wired to
// the same dispatch wires (per-plugin capability gate parity across
// transports), and that the selector defaults to the in-process transport
// for the existing c-shared artifact world.
func TestNewTransportOpenerWiresBothTransports(t *testing.T) {
	rec := &eventRecorder{}
	op := newTransportOpener(rec.dispatch, nil, rec.dispatch, rec, false)
	if op.inprocess == nil || op.subprocess == nil {
		t.Fatal("newTransportOpener must wire both transports")
	}
	// prod host + shared library -> FFI opener path (no error, no spawn).
	_, _, _, _, err := op.Open(gen.PluginAbi{Name: "p", TrustClass: ph.TrustFirstParty, Isolation: ph.IsolationInProcess}, "plugin.so", "", "p", nil, nil)
	if err == nil {
		t.Fatal("expected pluginloader open failure for nonexistent shared library, proving the FFI path (not the spawn path) was taken")
	}
	if !strings.Contains(err.Error(), "open plugin") {
		t.Fatalf("unexpected error from FFI path: %v", err)
	}
}

// TestTransportOpenerLoadsTwoSubprocessPlugins pins the multi-plugin fix:
// every subprocess load gets its own processOpener clone, so two plugins can
// coexist. Before the fix the shared singleton processOpener rejected the
// second load with "already spawned" (reproduced live loading a second native
// app while another subprocess app was running).
func TestTransportOpenerLoadsTwoSubprocessPlugins(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	rec := &eventRecorder{}
	proto := &processOpener{
		dispatch: rec.dispatch,
		logger:   rec,
		env:      append(os.Environ(), testPluginEnv+"=1"),
	}
	to := &transportOpener{devMode: true, inprocess: loaderOpener{}, subprocess: proto}
	abi := gen.PluginAbi{Name: "test.plugin", Isolation: ph.IsolationSubprocess}

	invokeA, _, closerA, _, err := to.Open(abi, exe, "", "test.plugin.a", nil, nil)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	defer func() { _ = closerA() }()
	invokeB, _, closerB, _, err := to.Open(abi, exe, "", "test.plugin.b", nil, nil)
	if err != nil {
		t.Fatalf("second load (want coexistence, got singleton rejection): %v", err)
	}
	defer func() { _ = closerB() }()

	for _, tc := range []struct {
		name   string
		invoke func(ctx context.Context, callable string, request []byte) ([]byte, error)
	}{{"a", invokeA}, {"b", invokeB}} {
		resp, err := tc.invoke(context.Background(), "ping", []byte(`{"n":1}`))
		if err != nil {
			t.Fatalf("invoke ping on plugin %s: %v", tc.name, err)
		}
		if string(resp) != `{"pong":"ok"}` {
			t.Fatalf("plugin %s ping resp = %s", tc.name, resp)
		}
	}
}

// TestTransportOpenerCloneCopiesOnLoadConfig pins the per-load clone contract:
// the prototype's config-carrying fields (here onLoadConfig) must carry into
// the fresh per-load processOpener, so a respawned prototype keeps its
// bootstrap payload.
func TestTransportOpenerCloneCopiesOnLoadConfig(t *testing.T) {
	var cloned *processOpener
	proto := &processOpener{
		onLoadConfig: []byte(`{"httpAddr":"127.0.0.1:0"}`),
		observeClone: func(o *processOpener) { cloned = o },
	}
	op := &transportOpener{devMode: true, inprocess: &recordingOpener{transport: "ffi"}, subprocess: proto}
	// The artifact does not exist; the load fails at spawn. The clone (and
	// the observeClone hook) runs before the spawn, so the assertion holds.
	_, _, _, _, _ = op.Open(gen.PluginAbi{Name: "p", Isolation: ph.IsolationSubprocess}, "missing.exe", "", "p", nil, nil)
	if cloned == nil {
		t.Fatal("expected a per-load processOpener clone")
	}
	if string(cloned.onLoadConfig) != string(proto.onLoadConfig) {
		t.Fatalf("clone onLoadConfig = %q, want proto's %q", cloned.onLoadConfig, proto.onLoadConfig)
	}
}
