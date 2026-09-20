package appmanager

import (
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// pluginEnv is a minimal fake planner environment for the plugin
// load/unload lifecycle: it answers artifact_load, artifact_unload, and
// assets_put, and records the call sequence.
type pluginEnv struct {
	t           *testing.T
	mu          sync.Mutex
	calls       []string
	loadReq     gen.PluginArtifactLoadReq
	assets      gen.PluginAssetsPutReq
	loadAddr    string   // HttpAddr returned by the fake artifact_load
	proxyCalls  []any    // proxy_attach payloads (fire-and-forget Invokes)
	detachCalls []string // proxy_detach plugin ids
}

func (e *pluginEnv) planner() lifecyclePlanner {
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		e.mu.Lock()
		e.calls = append(e.calls, callID)
		e.mu.Unlock()
		switch callID {
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			e.mu.Lock()
			e.loadReq = req
			addr := e.loadAddr
			e.mu.Unlock()
			return gen.PluginArtifactLoadResp{PluginID: req.Manifest.ID, ArtifactHash: req.ArtifactHash, HttpAddr: addr}, nil
		case "pluginhost.artifact_unload":
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		case "pluginhost.assets_put":
			e.mu.Lock()
			e.assets = payload.(gen.PluginAssetsPutReq)
			e.mu.Unlock()
			return gen.PluginAssetsPutResp{}, nil
		default:
			e.t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

func (e *pluginEnv) ctx() *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "pluginhost.proxy_attach":
			e.mu.Lock()
			e.proxyCalls = append(e.proxyCalls, payload)
			e.mu.Unlock()
		case "pluginhost.proxy_detach":
			e.mu.Lock()
			if req, ok := payload.(gen.PluginProxyDetachReq); ok {
				e.detachCalls = append(e.detachCalls, req.PluginID)
			}
			e.mu.Unlock()
		}
		return nil
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	planner := e.planner()
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func (e *pluginEnv) callSeq() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.calls...)
}

func (e *pluginEnv) unloadMissing() bool {
	for _, c := range e.callSeq() {
		if c == "pluginhost.artifact_unload" {
			return false
		}
	}
	return true
}

func newPluginLifecycleActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-plugin-lifecycle-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

func seedPlugin(a *Actor, state string) {
	seedPluginIsolation(a, state, "inprocess")
}

func seedPluginIsolation(a *Actor, state, isolation string) {
	abi := &gen.PluginAbi{Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1", InvokeSymbol: "PluginInvoke", ContractVersion: "1", Isolation: isolation, TrustClass: "first_party", Signer: "sporemind.first-party"}
	manifest := gen.AppManifest{ID: "app.demo", Name: "Demo", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1, Namespace: "demo"}
	a.Apps["app.demo"] = manifest
	a.Records["app.demo"] = appRecord{
		Manifest: manifest, ArtifactPath: "/tmp/plugin-demo.so", ArtifactHash: "aa0000000000001",
		Abi: abi, State: state, Assets: map[string][]byte{"index.html": []byte("<html/>")},
	}
}

func seedSporeApp(a *Actor) {
	manifest := gen.AppManifest{ID: "app.spore", Name: "Spore", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Namespace: "spore"}
	a.Apps["app.spore"] = manifest
	a.Records["app.spore"] = appRecord{Manifest: manifest, State: "running"}
}

// TestPluginUnloadActiveStateRecordUnloadsForReal: pluginhost reports loaded
// artifacts as "active", and the native reload path used to copy that value
// verbatim into appRecord.State. Observed live (round-4 TOTP verification):
// after reload_project the record said "active", plugin_unload then hit the
// idempotent "not running" early-return, returned success, and the app kept
// serving. Both the guard (legacy persisted "active" records) and the
// normalization (no new "active" records) are covered here.
func TestPluginUnloadActiveStateRecordUnloadsForReal(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, stateActive)
	ctx := env.ctx()

	resp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	if resp.Status.State == stateActive {
		t.Fatalf("state = %q: unload silently no-opped on a live record", resp.Status.State)
	}
	if env.unloadMissing() {
		t.Fatal("unload must reach pluginhost.artifact_unload for a live record")
	}

	if got := normalizePluginState(stateActive); got != stateRunning {
		t.Fatalf("normalizePluginState(active) = %q, want running", got)
	}
	if got := normalizePluginState("stopped"); got != "stopped" {
		t.Fatalf("normalizePluginState(stopped) = %q, want stopped", got)
	}
}

func TestPluginUnloadRunningPlugin(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "running")
	ctx := env.ctx()

	resp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	// In-process fixture: unload degrades to unload-pending (the c-shared
	// library cannot be safely unmapped while the host lives).
	if resp.Status.State != "unload_pending" {
		t.Fatalf("state = %q, want unload_pending", resp.Status.State)
	}
	if a.Records["app.demo"].State != "unload_pending" {
		t.Fatalf("record state = %q, want unload_pending", a.Records["app.demo"].State)
	}
	if a.Records["app.demo"].Error == "" {
		t.Fatal("expected restart-required notice in record error")
	}
	if got := env.callSeq(); len(got) != 1 || got[0] != "pluginhost.artifact_unload" {
		t.Fatalf("planner calls = %v, want only artifact_unload", got)
	}
	var found bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "app_lifecycle" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected app_lifecycle event emission")
	}
}

func TestPluginUnloadSubprocessPluginStopsForReal(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPluginIsolation(a, "running", "subprocess")
	ctx := env.ctx()

	resp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	// Subprocess transport: the plugin process is killed for real, the app
	// becomes a plain stopped record that may load again immediately.
	if resp.Status.State != "stopped" {
		t.Fatalf("state = %q, want stopped", resp.Status.State)
	}
	if a.Records["app.demo"].Error != "" {
		t.Fatalf("record error = %q, want empty", a.Records["app.demo"].Error)
	}
}

func TestPluginLoadStagesRestartPendingWhileUnloadPending(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "unload_pending")

	// A2: an in-process plugin whose unload degraded to unload_pending
	// cannot be revived while the host lives — plugin_load stages the load
	// as restart_pending (the recorded artifact activates on restart)
	// instead of refusing with an error.
	resp, err := a.handlePluginLoad(env.ctx(), gen.AppManagerPluginLoadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("plugin_load while unload-pending: %v", err)
	}
	if resp.Status.State != "restart_pending" {
		t.Fatalf("state = %q, want restart_pending", resp.Status.State)
	}
	if got := a.Records["app.demo"]; got.State != "restart_pending" {
		t.Fatalf("record state = %q, want restart_pending", got.State)
	}
	if calls := env.callSeq(); len(calls) != 0 {
		// The artifact must NOT be re-loaded into the stale in-process
		// mapping (no pluginhost.artifact_load call).
		t.Fatalf("plugin_load while unload-pending should not call pluginhost, got %v", calls)
	}
}

func TestPluginLoadSubprocessWhileUnloadPendingRefuses(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPluginIsolation(a, "unload_pending", "subprocess")
	if _, err := a.handlePluginLoad(env.ctx(), gen.AppManagerPluginLoadReq{ID: "app.demo"}); err == nil {
		t.Fatal("expected plugin_load refusal while unload-pending for subprocess")
	}
}

// TestPluginLoadAttachesBackendProxy pins the revive-path gateway attach: a
// plugin_load whose respawned subprocess confirms its HTTP listener must
// re-attach pluginhost's reverse proxy (with a freshly minted gateway token)
// — without it the revived app answers 405/404 on /plugin/{id}/invoke|events
// until a reload. Mirrors the cold-start restore commit path.
func TestPluginLoadAttachesBackendProxy(t *testing.T) {
	env := &pluginEnv{t: t}
	env.loadAddr = "127.0.0.1:49199"
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "stopped")
	ctx := env.ctx()

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"}); err != nil {
		t.Fatalf("load: %v", err)
	}
	env.mu.Lock()
	attaches := env.proxyCalls
	env.mu.Unlock()
	if len(attaches) != 1 {
		t.Fatalf("proxy attach calls = %v, want exactly one", attaches)
	}
	req, ok := attaches[0].(gen.PluginProxyAttachReq)
	if !ok {
		t.Fatalf("attach payload = %T, want PluginProxyAttachReq", attaches[0])
	}
	if req.PluginID != "app.demo" || req.Addr != "127.0.0.1:49199" || req.Token == "" {
		t.Fatalf("attach req = %+v, want app.demo @ 127.0.0.1:49199 with token", req)
	}
	if got := a.Records["app.demo"].BackendUrl; got != "http://127.0.0.1:49199" {
		t.Fatalf("record backend url = %q, want adopted listener", got)
	}
}

// TestPluginLoadDetachesProxyWithoutListener pins the clearing half: when the
// respawned instance runs no HTTP listener, the stale proxy route from the
// previous instance must be dropped, not left pointing at a dead port.
func TestPluginLoadDetachesProxyWithoutListener(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "stopped")
	seeded := a.Records["app.demo"]
	seeded.BackendUrl = "http://127.0.0.1:49198"
	seeded.SessionSecret = "old-secret"
	a.Records["app.demo"] = seeded
	ctx := env.ctx()

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"}); err != nil {
		t.Fatalf("load: %v", err)
	}
	env.mu.Lock()
	detaches := env.detachCalls
	env.mu.Unlock()
	if len(detaches) != 1 {
		t.Fatalf("proxy detach calls = %v, want exactly one", detaches)
	}
	if got := a.Records["app.demo"].BackendUrl; got != "" {
		t.Fatalf("record backend url = %q, want cleared", got)
	}
}

func TestPluginLoadStoppedPlugin(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "stopped")
	ctx := env.ctx()

	resp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}
	seq := env.callSeq()
	want := []string{"pluginhost.artifact_load", "pluginhost.assets_put"}
	if len(seq) != len(want) {
		t.Fatalf("planner calls = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("planner calls = %v, want %v", seq, want)
		}
	}
	env.mu.Lock()
	loadReq := env.loadReq
	assetsReq := env.assets
	env.mu.Unlock()
	if loadReq.ArtifactPath != "/tmp/plugin-demo.so" || loadReq.ArtifactHash != "aa0000000000001" {
		t.Fatalf("load req = %+v", loadReq)
	}
	if loadReq.Abi.InvokeSymbol != "PluginInvoke" {
		t.Fatalf("load abi = %+v", loadReq.Abi)
	}
	if assetsReq.PluginID != "app.demo" || len(assetsReq.Assets) != 1 {
		t.Fatalf("assets req = %+v", assetsReq)
	}
	if got := a.children["app.demo"]; got != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route after load", got)
	}
	if got := a.Records["app.demo"].ActorID; got != pluginhostServiceName {
		t.Fatalf("record ActorID = %q, want pluginhost route after load", got)
	}
}

// TestPluginLoadSelfHealsMissingChildrenSentinel pins the wedged-record
// recovery: a plugin whose children entry was lost (restore-time
// verification failed after a host restart) must become invocable again
// after plugin_load — the load restores the pluginhost routing sentinel
// exactly like the reload self-heal.
func TestPluginLoadSelfHealsMissingChildrenSentinel(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "failed")
	delete(a.children, "app.demo")
	ctx := env.ctx()

	resp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}
	if got := a.children["app.demo"]; got != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route self-healed", got)
	}
	if got := a.Records["app.demo"].ActorID; got != pluginhostServiceName {
		t.Fatalf("record ActorID = %q, want pluginhost route self-healed", got)
	}
}

// TestPluginLoadLiveRecordSelfHealsMissingChildren covers the early-return
// path: the record already says running but the routing sentinel is gone, so
// every invoke would still be rejected as not running.
func TestPluginLoadLiveRecordSelfHealsMissingChildren(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "running")
	delete(a.children, "app.demo")
	ctx := env.ctx()

	resp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}
	if got := a.children["app.demo"]; got != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route self-healed", got)
	}
	if seq := env.callSeq(); len(seq) != 0 {
		t.Fatalf("live record must not reload the artifact, calls = %v", seq)
	}
}

func TestPluginLoadUnloadIdempotent(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedPlugin(a, "running")
	ctx := env.ctx()

	resp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("load while running: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}
	if calls := env.callSeq(); len(calls) != 0 {
		t.Fatalf("load while running should be a no-op, got calls %v", calls)
	}

	seedPlugin(a, "stopped")
	unloadResp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.demo"})
	if err != nil {
		t.Fatalf("unload while stopped: %v", err)
	}
	if calls := env.callSeq(); len(calls) != 0 {
		t.Fatalf("unload while stopped should be a no-op, got calls %v", calls)
	}
	if unloadResp.Status.State != "stopped" {
		t.Fatalf("state = %q, want stopped", unloadResp.Status.State)
	}
}

func TestPluginLifecycleRejectsSporeAndUnknown(t *testing.T) {
	env := &pluginEnv{t: t}
	a := newPluginLifecycleActor(t)
	seedSporeApp(a)
	ctx := env.ctx()

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.spore"}); err == nil {
		t.Fatal("expected error loading spore app")
	}
	if _, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.spore"}); err == nil {
		t.Fatal("expected error unloading spore app")
	}
	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.missing"}); err == nil {
		t.Fatal("expected error for unknown app")
	}
}
