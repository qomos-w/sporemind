package runtime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/events"
	"github.com/qomos-w/gospore/resource"

	"github.com/qomos-w/sporemind/pkg/actor/aimanager"
	"github.com/qomos-w/sporemind/pkg/actor/filesystem"
	"github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/actor/workspace"
	"github.com/qomos-w/sporemind/pkg/config"
	phrouter "github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

func defaultChildren() []runtime.ChildSpec {
	return []runtime.ChildSpec{
		{Name: "workspace", Factory: func() actor.Actor { return &workspace.Actor{} }},
		{Name: "filesystem", Factory: func() actor.Actor { return &filesystem.Actor{} }},
		{Name: "aimanager", Factory: func() actor.Actor { return &aimanager.Actor{} }},
	}
}

func TestNew_FullAppStartup(t *testing.T) {
	a, err := runtime.New(runtime.Config{NoGateway: true, Children: defaultChildren()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sub, err := a.Events().Subscribe(a.Self().ID(), 0,
		[]actor.WatchKind{actor.WatchStarted})
	if err != nil {
		t.Fatalf("Events.Subscribe: %v", err)
	}
	defer sub.Close()

	type recvResult struct {
		rec events.Record
		err error
	}
	recvCh := make(chan recvResult, 1)
	go func() {
		rec, err := sub.Recv()
		recvCh <- recvResult{rec: rec, err: err}
	}()

	done := make(chan error, 1)
	go func() {
		done <- a.Run(ctx)
	}()

	select {
	case r := <-recvCh:
		if r.err != nil {
			t.Fatalf("Events.Recv: %v", r.err)
		}
		t.Logf("root started: kind=%v", r.rec.Kind)
	case <-time.After(5 * time.Second):
		t.Fatal("app startup timed out")
	}

	// Wait until all cells have started (or stopped) before checking services;
	// a fixed sleep was flaky under load.
	if err := a.WaitForAllCellsStart(5 * time.Second); err != nil {
		t.Logf("WaitForAllCellsStart: %v", err)
	}

	for _, svc := range []string{"workspace", "aimanager", "filesystem"} {
		if _, ok := a.LookupService(svc); !ok {
			t.Errorf("expected %s service", svc)
		}
	}

	cancel()
	<-done
}

func TestWorkspaceActorStarts(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{Children: defaultChildren()})
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	time.Sleep(500 * time.Millisecond)

	app := h.App()

	fsRef, ok := app.LookupService("filesystem")
	if !ok {
		t.Fatal("filesystem actor not found")
	}
	call := fsRef.Invoke(context.Background(), "filesystem.list", ".")
	if call == nil {
		t.Fatal("filesystem.list invoke returned nil")
	}
	defer call.Close()
	_, err = call.RecvRaw()
	if err != nil {
		t.Logf("filesystem.list result: %v", err)
	}

	wsRef, ok := app.LookupService("workspace")
	if !ok {
		t.Fatal("workspace actor not found")
	}
	call2 := wsRef.Invoke(context.Background(), "workspace.list_project", nil)
	if call2 == nil {
		t.Fatal("workspace.list_project invoke returned nil")
	}
	defer call2.Close()
	raw, err := call2.RecvRaw()
	t.Logf("workspace.list_project raw=%s err=%v", raw, err)

	call3 := wsRef.Invoke(context.Background(), "workspace.account", nil)
	if call3 == nil {
		t.Fatal("workspace.account invoke returned nil")
	}
	defer call3.Close()
	raw3, err3 := call3.RecvRaw()
	t.Logf("workspace.account raw=%s err=%v", raw3, err3)
}

func TestPluginRouter_RegisteredAsResource(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children:    defaultChildren(),
	})
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	// The app boots in the background; wait on the actual condition (the
	// plugin router resource registering) rather than a fixed sleep.
	// Cancelling mid-boot is safe: gospore 5f95dd8 makes Run and recursive
	// Spawn return promptly on ctx cancellation during init.
	app := h.App()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, ok := resource.Get[*phrouter.Router](app.Resources(), phrouter.RouterKey); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("plugin router not registered within 10s")
		}
		time.Sleep(25 * time.Millisecond)
	}

	// The plugin router should be registered as a resource.
	router, ok := resource.Get[*phrouter.Router](app.Resources(), phrouter.RouterKey)
	if !ok {
		t.Fatal("plugin router not found in resources")
	}

	// Register a test plugin handler and exercise it directly.
	router.Register("com.example.test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plugin:" + r.URL.Path))
	}))

	req := httptest.NewRequest(http.MethodGet, "/plugin/com.example.test/index.html", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if got, want := string(body), "plugin:/index.html"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	// Unregister and verify 404.
	router.Unregister("com.example.test")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status after unregister = %d, want 404", rec.Code)
	}

	// Wait for the persistent workspace child to finish OnInit before the
	// deferred teardown cancels the root ctx. An abort mid-OnInit leaves the
	// workspace actor's persist writes in flight when the test framework
	// removes the TempDir data dir, and Windows RemoveAll fails on the
	// non-empty directory (deterministic since gospore 3d3fdc0 made
	// workspace init marginally slower).
	wsDeadline := time.Now().Add(10 * time.Second)
	for {
		wsRef, ok := app.LookupService("workspace")
		if ok {
			if c := wsRef.Invoke(context.Background(), "workspace.list_project", nil); c != nil {
				if _, err := c.RecvRaw(); err == nil {
					_ = c.Close()
					break
				}
				_ = c.Close()
			}
		}
		if time.Now().After(wsDeadline) {
			t.Fatal("workspace did not answer within 10s")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestPluginHostActor_Invoke(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	phActor := &pluginhost.Actor{}
	phActor.RegisterHandler("plugin.com.example.hello.hello.search", func(_ context.Context, req []byte) ([]byte, error) {
		return []byte(`{"results":["a","b"]}`), nil
	})

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{Name: "pluginhost", Factory: func() actor.Actor { return phActor }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()

	select {
	case <-time.After(500 * time.Millisecond):
	}

	app := h.App()
	ref, ok := app.LookupService("pluginhost")
	if !ok {
		t.Fatal("pluginhost service not found")
	}

	// Register the plugin descriptor through a callable.
	call := ref.Invoke(context.Background(), "pluginhost.register_actor", map[string]any{
		"PluginID":  "com.example.hello",
		"Name":      "Hello Plugin",
		"Version":   "1.0.0",
		"Namespace": "plugin.com.example.hello",
		"CallIDs":   []string{"plugin.com.example.hello.hello.search"},
	})
	if call == nil {
		t.Fatal("register_actor invoke returned nil")
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		t.Fatalf("register_actor failed: %v", err)
	}
	t.Logf("register_actor raw=%s", raw)

	// Invoke the plugin callable through the dispatcher. The wire contract is
	// gen.PluginInvokeReq{ID, Callable, Payload}: the host namespaces them into
	// the canonical plugin.<id>.<callable> route.
	call2 := ref.Invoke(context.Background(), "pluginhost.invoke", map[string]any{
		"Id":       "com.example.hello",
		"Callable": "hello.search",
		"Payload":  []byte(`{"q":"x"}`),
	})
	if call2 == nil {
		t.Fatal("invoke returned nil")
	}
	defer call2.Close()
	raw2, err := call2.RecvRaw()
	if err != nil {
		t.Fatalf("invoke failed: %v", err)
	}

	// RecvRaw may return a JSON string wrapper; unwrap if necessary.
	body := string(raw2)
	if len(body) >= 2 && body[0] == '"' && body[len(body)-1] == '"' {
		var unquoted string
		if err := json.Unmarshal(raw2, &unquoted); err == nil {
			body = unquoted
		}
	}

	var res struct {
		Payload string `json:"Payload"`
	}
	if err := json.Unmarshal([]byte(body), &res); err == nil && res.Payload != "" {
		body = res.Payload
	}

	// The plugin host transports []byte payloads as base64; decode before
	// comparing against the handler output.
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode invoke payload %q: %v", body, err)
	}

	wantSub := `"results":["a","b"]`
	if !strings.Contains(string(decoded), wantSub) {
		t.Fatalf("invoke result = %q, want to contain %s", decoded, wantSub)
	}
}
