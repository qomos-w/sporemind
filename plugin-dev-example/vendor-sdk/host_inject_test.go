package sdk

import (
	"testing"
)

// fakeInjectHost implements Host for SetHost injection tests. It records the
// callIDs it serves so tests can assert the injected host actually receives
// reverse invokes (the process-mode IPC-backed host path).
type fakeInjectHost struct {
	callIDs []string
}

func (h *fakeInjectHost) Invoke(callID string, payload any) ([]byte, error) {
	h.callIDs = append(h.callIDs, callID)
	return []byte(`{}`), nil
}
// InvokeStream records the callID and delivers the full response as one
// chunk (the same shape the FFI fallback uses); tests only assert the call
// reached the injected host, not real streaming.
func (h *fakeInjectHost) InvokeStream(callID string, payload any, onChunk func([]byte) error) ([]byte, error) {
	h.callIDs = append(h.callIDs, callID)
	resp := []byte(`{}`)
	if onChunk != nil {
		_ = onChunk(resp)
	}
	return resp, nil
}

// TestActiveHostPrefersInjectedHost guards the SetHost precedence contract:
// an injected host wins over the default newHostBridge even when no plugin
// state exists, and SetHost(nil) restores the default bridge.
func TestActiveHostPrefersInjectedHost(t *testing.T) {
	// This test runs with an empty plugin lifecycle state; a stale state from
	// another test would change the fallback, so the whole scenario is
	// self-contained (SetHost + assertions + SetHost(nil)).
	fake := &fakeInjectHost{}
	SetHost(fake)
	defer SetHost(nil)

	if got := ActiveHost(); got != fake {
		t.Fatalf("ActiveHost = %T(%p), want injected fake host %T(%p)", got, got, fake, fake)
	}

	SetHost(nil)
	if got := ActiveHost(); got == nil {
		t.Fatal("ActiveHost returned nil after SetHost(nil)")
	} else if _, ok := got.(*defaultHost); !ok {
		t.Fatalf("ActiveHost = %T, want *defaultHost after SetHost(nil)", got)
	}
}

// TestEnsureStateUsesInjectedHost guards that ensureState (plugin.go) creates
// the lifecycle state with the injected host instead of the hardcoded
// newHostBridge, so ctx.Host() and every reverse invoke in process mode
// resolve to the IPC-backed implementation.
func TestEnsureStateUsesInjectedHost(t *testing.T) {
	fake := &fakeInjectHost{}
	SetHost(fake)
	defer SetHost(nil)

	const pluginID = "app.inject.host"
	Register(&Plugin{
		Manifest: Manifest{ID: pluginID, Name: "Inject Host", Version: "1.0.0"},
	})
	defer unregisterPluginCallables(pluginID)

	id := cstr(pluginID)
	if got := HandleOnLoad(id, nil); got != 0 {
		t.Fatalf("HandleOnLoad = %d, want 0", got)
	}
	defer HandleOnUnload(id)

	state, err := currentState()
	if err != nil {
		t.Fatalf("currentState: %v", err)
	}
	if state.host != fake {
		t.Fatalf("state.host = %T(%p), want injected fake host %T(%p)", state.host, state.host, fake, fake)
	}
	if got := state.ctx.Host(); got != fake {
		t.Fatalf("ctx.Host() = %T(%p), want injected fake host", got, got)
	}
	if got := ActiveHost(); got != fake {
		t.Fatalf("ActiveHost = %T(%p), want injected fake host", got, got)
	}

	// A reverse invoke from the injected host's primitive must reach the
	// injected host (project.write_file content stays a JSON string per the
	// wire contract; the fake only records the call).
	if _, err := state.host.Invoke("project.write_file", map[string]any{"path": "test.txt", "content": "content"}); err != nil {
		t.Fatalf("Invoke via injected host: %v", err)
	}
	if len(fake.callIDs) != 1 || fake.callIDs[0] != "project.write_file" {
		t.Fatalf("injected host callIDs = %v, want [project.write_file]", fake.callIDs)
	}
}

// TestSetHostNilRestoresDefaultHostBridge guards the c-shared default: with
// no injection and no active state, ActiveHost falls back to newHostBridge.
func TestSetHostNilRestoresDefaultHostBridge(t *testing.T) {
	SetHost(nil)
	got := ActiveHost()
	if got == nil {
		t.Fatal("ActiveHost returned nil")
	}
	if _, ok := got.(*defaultHost); !ok {
		t.Fatalf("ActiveHost = %T, want *defaultHost", got)
	}
	// The default host without an FFI bridge must fail cleanly instead of
	// dereferencing a nil function pointer.
	invoke, err := got.Invoke("state.get", map[string]string{"key": "k"})
	if invoke != nil || err == nil {
		t.Fatalf("default host without bridge: invoke=%q err=%v, want nil payload with error", invoke, err)
	}
}