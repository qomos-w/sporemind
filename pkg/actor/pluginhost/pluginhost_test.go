package pluginhost

import (
	"context"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestActor_RegisterAndListPlugins(t *testing.T) {
	a := &Actor{}

	res, err := a.handleRegisterActor(nil, registerActorReq{
		PluginID:  "com.example.hello",
		Name:      "Hello Plugin",
		Version:   "1.0.0",
		Namespace: "plugin.com.example.hello",
		CallIDs:   []string{"plugin.com.example.hello.hello.search"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if res.Registered != 1 {
		t.Fatalf("registered = %d, want 1", res.Registered)
	}

	plugins, err := a.handleListPlugins(nil, listPluginsReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(plugins.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(plugins.Plugins))
	}
	if plugins.Plugins[0].ID != "com.example.hello" {
		t.Fatalf("plugin id = %q", plugins.Plugins[0].ID)
	}
}

func TestActor_InvokeRegisteredHandler(t *testing.T) {
	a := &Actor{}
	_, err := a.handleRegisterActor(nil, registerActorReq{PluginID: "com.example.hello", CallIDs: []string{"plugin.com.example.hello.hello.search"}})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	a.RegisterHandler("plugin.com.example.hello.hello.search", func(ctx context.Context, req []byte) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	})

	res, err := a.handleInvoke(nil, gen.PluginInvokeReq{
		ID:       "com.example.hello",
		Callable: "hello.search",
		Payload:  []byte(`{"q":"x"}`),
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if string(res.Payload) != "ok" {
		t.Fatalf("payload = %q, want ok", string(res.Payload))
	}
	if _, err := a.handleInvoke(nil, gen.PluginInvokeReq{ID: "com.example.hello", Callable: "hello.other"}); err == nil {
		t.Fatal("expected undeclared callable error")
	}
}

func TestActor_InvokeUnknownHandler(t *testing.T) {
	a := &Actor{}

	_, err := a.handleInvoke(nil, gen.PluginInvokeReq{ID: "missing", Callable: "method"})
	if err == nil {
		t.Fatal("expected error for unknown callable")
	}
}

func TestActor_UnregisterActor(t *testing.T) {
	a := &Actor{}

	_, _ = a.handleRegisterActor(nil, registerActorReq{
		PluginID: "com.example.hello",
		CallIDs:  []string{"plugin.com.example.hello.hello.search"},
	})
	a.RegisterHandler("plugin.com.example.hello.hello.search", func(ctx context.Context, req []byte) ([]byte, error) {
		return nil, nil
	})

	res, err := a.handleUnregisterActor(nil, unregisterActorReq{PluginID: "com.example.hello"})
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if res.Removed != 1 {
		t.Fatalf("removed = %d, want 1", res.Removed)
	}

	plugins, _ := a.handleListPlugins(nil, listPluginsReq{})
	if len(plugins.Plugins) == 0 || plugins.Plugins[0].Status != "unloaded" {
		t.Fatalf("status = %+v, want unloaded", plugins.Plugins)
	}
}

func TestActor_UnregisterCallsLibraryCloser(t *testing.T) {
	a := &Actor{}
	a.closers = make(map[string]func() error)

	_, _ = a.handleRegisterActor(nil, registerActorReq{
		PluginID: "com.example.native",
		CallIDs:  []string{"plugin.com.example.native.compute"},
	})

	closed := false
	a.RegisterCloser("com.example.native", func() error {
		closed = true
		return nil
	})

	_, err := a.handleUnregisterActor(nil, unregisterActorReq{PluginID: "com.example.native"})
	if err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if !closed {
		t.Fatal("library closer was not called on unregister")
	}
}

func TestActor_InvokePanicRecovery(t *testing.T) {
	a := &Actor{}
	_, _ = a.handleRegisterActor(nil, registerActorReq{
		PluginID: "com.example.crashy",
		CallIDs:  []string{"plugin.com.example.crashy.boom"},
	})
	a.RegisterHandler("plugin.com.example.crashy.boom", func(ctx context.Context, req []byte) ([]byte, error) {
		panic("intentional crash")
	})

	res, err := a.handleInvoke(nil, gen.PluginInvokeReq{
		ID:       "com.example.crashy",
		Callable: "boom",
		Payload:  []byte("{}"),
	})
	// Panic must be recovered (not crash the test) and surfaced as a Go error
	// so the AppManager audit records the failure.
	if err == nil {
		t.Fatal("expected go error for panicked plugin")
	}
	if res.Payload != nil {
		t.Fatalf("expected empty payload on failure, got %q", string(res.Payload))
	}

	// Plugin should be marked as errored
	plugins, _ := a.handleListPlugins(nil, listPluginsReq{})
	if len(plugins.Plugins) == 0 || plugins.Plugins[0].Status != "error" {
		t.Fatalf("expected status 'error', got %+v", plugins.Plugins)
	}
}

func TestActorRegisterWithoutValidatedABIIsNotTrusted(t *testing.T) {
	a := &Actor{}
	_, _ = a.handleRegisterActor(nil, registerActorReq{PluginID: "com.example.unverified"})

	plugins, _ := a.handleListPlugins(nil, listPluginsReq{})
	if len(plugins.Plugins) == 0 || plugins.Plugins[0].Trusted {
		t.Fatal("plugin without validated first-party ABI must not be trusted")
	}
}

func TestActor_InvokeUsesForwardedTimeoutMs(t *testing.T) {
	a := &Actor{}
	_, err := a.handleRegisterActor(nil, registerActorReq{PluginID: "com.example.slow", CallIDs: []string{"plugin.com.example.slow.compute"}})
	if err != nil {
		t.Fatal(err)
	}

	a.RegisterHandler("plugin.com.example.slow.compute", func(ctx context.Context, req []byte) ([]byte, error) {
		select {
		case <-time.After(5 * time.Second):
			return []byte("too late"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})

	start := time.Now()
	_, err = a.handleInvoke(nil, gen.PluginInvokeReq{
		ID:        "com.example.slow",
		Callable:  "compute",
		Payload:   []byte(`{}`),
		TimeoutMs: 100,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout was not enforced; elapsed = %v", elapsed)
	}
}

func TestActor_InvokeFallsBackToDefaultTimeoutWhenZero(t *testing.T) {
	a := &Actor{}
	_, err := a.handleRegisterActor(nil, registerActorReq{PluginID: "com.example.normal", CallIDs: []string{"plugin.com.example.normal.compute"}})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	a.RegisterHandler("plugin.com.example.normal.compute", func(ctx context.Context, req []byte) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	})

	res, err := a.handleInvoke(nil, gen.PluginInvokeReq{
		ID:       "com.example.normal",
		Callable: "compute",
		Payload:  []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if string(res.Payload) != "ok" {
		t.Fatalf("payload = %q, want ok", string(res.Payload))
	}
}
