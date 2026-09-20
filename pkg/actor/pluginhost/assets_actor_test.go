package pluginhost

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newAssetsTestActor(t *testing.T) (*Actor, *ph.Router) {
	t.Helper()
	router := ph.NewRouter()
	a := &Actor{
		actorID:       "pluginhost-assets-test",
		store:         persist.NewFSPersist(t.TempDir()),
		router:        router,
		ArtifactLoads: map[string]gen.PluginArtifactLoadReq{},
		AssetStores:   map[string]map[string][]byte{},
	}
	return a, router
}

func doPluginGet(t *testing.T, router *ph.Router, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestAssetsPutServesRouteUntilRemoved is the pluginhost-actor acceptance
// test for the plugin assets contract: after pluginhost.assets_put the
// gateway route serves the declared bundle, the bundle is persisted, and
// pluginhost.assets_remove makes the route 404 again.
func TestAssetsPutServesRouteUntilRemoved(t *testing.T) {
	a, router := newAssetsTestActor(t)

	if rec := doPluginGet(t, router, "/plugin/app.demo/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("before put: code = %d, want 404", rec.Code)
	}

	resp, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{
		PluginID: "app.demo",
		Assets: map[string][]byte{
			"index.html": []byte("<html>demo</html>"),
			"app.js":     []byte("console.log(1)"),
		},
	})
	if err != nil {
		t.Fatalf("assets_put: %v", err)
	}
	if resp.Registered != 2 {
		t.Fatalf("registered = %d, want 2", resp.Registered)
	}

	// HTML assets are served with the bridge bootstrap snippet injected before
	// </html> (see pkg/pluginhost serveAsset); the original body must survive.
	if rec := doPluginGet(t, router, "/plugin/app.demo/index.html"); rec.Code != http.StatusOK ||
		!strings.HasPrefix(rec.Body.String(), "<html>demo<script>") ||
		!strings.Contains(rec.Body.String(), "sporemind:ready-for-bootstrap") ||
		!strings.HasSuffix(rec.Body.String(), "</html>") {
		t.Fatalf("after put index.html: code = %d body = %q", rec.Code, rec.Body.String())
	}
	if rec := doPluginGet(t, router, "/plugin/app.demo/app.js"); rec.Code != http.StatusOK {
		t.Fatalf("after put app.js: code = %d", rec.Code)
	}

	// The bundle must survive a restart (persist → load → restore).
	restored := &Actor{actorID: a.actorID, store: a.store, router: ph.NewRouter()}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	restored.restoreAssetRoutes()
	if rec := doPluginGet(t, restored.router, "/plugin/app.demo/index.html"); rec.Code != http.StatusOK {
		t.Fatalf("after restore: code = %d, want 200", rec.Code)
	}

	remResp, err := a.handleAssetsRemove(nil, gen.PluginAssetsRemoveReq{PluginID: "app.demo"})
	if err != nil {
		t.Fatalf("assets_remove: %v", err)
	}
	if remResp.Removed != 1 {
		t.Fatalf("removed = %d, want 1", remResp.Removed)
	}
	if rec := doPluginGet(t, router, "/plugin/app.demo/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("after remove: code = %d, want 404", rec.Code)
	}
}

func TestAssetsPutRequiresPluginID(t *testing.T) {
	a, _ := newAssetsTestActor(t)
	if _, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{Assets: map[string][]byte{"index.html": []byte("x")}}); err == nil {
		t.Fatal("expected error for empty plugin id")
	}
	if _, err := a.handleAssetsRemove(nil, gen.PluginAssetsRemoveReq{}); err == nil {
		t.Fatal("expected error for empty plugin id")
	}
}

func TestAssetsRemoveUnknownPluginIsIdempotent(t *testing.T) {
	a, router := newAssetsTestActor(t)
	resp, err := a.handleAssetsRemove(nil, gen.PluginAssetsRemoveReq{PluginID: "never.registered"})
	if err != nil {
		t.Fatalf("remove unknown: %v", err)
	}
	if resp.Removed != 0 {
		t.Fatalf("removed = %d, want 0", resp.Removed)
	}
	if rec := doPluginGet(t, router, "/plugin/never.registered/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestUnregisterActorDropsAssetRoute(t *testing.T) {
	a, router := newAssetsTestActor(t)

	_, _ = a.handleRegisterActor(nil, registerActorReq{PluginID: "app.guest", CallIDs: []string{"plugin.app.guest.run"}})
	if _, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{PluginID: "app.guest", Assets: map[string][]byte{"index.html": []byte("guest")}}); err != nil {
		t.Fatalf("assets_put: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.guest/index.html"); rec.Code != http.StatusOK {
		t.Fatalf("after put: code = %d", rec.Code)
	}

	if _, err := a.handleUnregisterActor(nil, unregisterActorReq{PluginID: "app.guest"}); err != nil {
		t.Fatalf("unregister_actor: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/app.guest/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("after unregister_actor: code = %d, want 404", rec.Code)
	}
}

// TestArtifactUnloadDropsAssetsE2E proves the teardown half of the contract
// through the production actor handler with a real c-shared artifact:
// artifact_load + assets_put serve the bundle, and artifact_unload drops both
// the library and the /plugin/{id}/ route. Skipped when the Go toolchain or
// the SDK example is unavailable, and on Windows: FreeLibrary of a Go
// c-shared DLL followed by any fsync (the persist.Save inside the unload
// handler) triggers a hard access violation (0xc0000005) in this host's
// scheduler — a pre-existing platform hazard of the loader's dlclose path,
// not of the assets wiring. The map/router teardown itself is covered on
// Windows by TestAssetsPutServesRouteUntilRemoved and
// TestUnregisterActorDropsAssetRoute.
func TestArtifactUnloadDropsAssetsE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fsync-after-FreeLibrary access violation on Windows; covered on Linux CI")
	}
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("shared libraries not supported on this platform")
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

	a, router := newAssetsTestActor(t)
	a.loader = ph.NewArtifactLoader(a)
	a.loader.SetOpener(loaderOpener{})
	ctx := testutil.HumanCtx(testutil.GenActorID())

	manifest := gen.AppManifest{
		ID: "com.example.hello", Name: "Hello Plugin", Version: "1.0.0",
		Runtime: ph.NativeRuntime, ProtocolVersion: 2, Namespace: "plugin.com.example.hello",
		Callables: []gen.AppCallableDescriptor{
			{ID: "greet", RequestSchema: "greetReq", ResponseSchema: "greetResp"},
		},
	}
	abi := gen.PluginAbi{
		Name: ph.BinaryCodecV1, Version: 1, Encoding: ph.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: ph.IsolationInProcess, TrustClass: ph.TrustFirstParty, Signer: "sporemind.first-party",
	}
	if _, err := a.handleArtifactLoad(ctx, gen.PluginArtifactLoadReq{ArtifactPath: libPath, Manifest: manifest, Abi: abi}); err != nil {
		t.Fatalf("artifact_load: %v", err)
	}
	if _, err := a.handleAssetsPut(nil, gen.PluginAssetsPutReq{
		PluginID: manifest.ID,
		Assets:   map[string][]byte{"index.html": []byte("<html>hello</html>")},
	}); err != nil {
		t.Fatalf("assets_put: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/com.example.hello/index.html"); rec.Code != http.StatusOK {
		t.Fatalf("after load+put: code = %d, want 200", rec.Code)
	}

	if _, err := a.handleArtifactUnload(ctx, gen.PluginArtifactUnloadReq{PluginID: manifest.ID}); err != nil {
		t.Fatalf("artifact_unload: %v", err)
	}
	if rec := doPluginGet(t, router, "/plugin/com.example.hello/index.html"); rec.Code != http.StatusNotFound {
		t.Fatalf("after unload: code = %d, want 404", rec.Code)
	}

	// The bundle must also be gone from persisted state.
	restored := &Actor{actorID: a.actorID, store: a.store}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(restored.AssetStores) != 0 {
		t.Fatalf("persisted asset stores = %v, want empty", restored.AssetStores)
	}
	if len(restored.ArtifactLoads) != 0 {
		t.Fatalf("persisted artifact loads = %v, want empty", restored.ArtifactLoads)
	}
}
