package appmanager

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestNewInstanceSecret(t *testing.T) {
	s1, err := newInstanceSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) != instanceSecretBytes*2 {
		t.Fatalf("secret length = %d, want %d hex chars", len(s1), instanceSecretBytes*2)
	}
	if _, err := hex.DecodeString(s1); err != nil {
		t.Fatalf("secret must be lowercase hex: %v", err)
	}
	s2, err := newInstanceSecret()
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 {
		t.Fatal("two secrets must differ")
	}
}

func TestWithBackendLoadReq(t *testing.T) {
	req, secret, err := (&Actor{}).withBackendLoadReq(gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("secret must be returned")
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatalf("OnLoadConfig must be the backend load config JSON: %v", err)
	}
	if cfg.HTTPAddr != backendHTTPAddr {
		t.Fatalf("httpAddr = %q, want %q", cfg.HTTPAddr, backendHTTPAddr)
	}
	if cfg.SessionSecret != secret {
		t.Fatal("config must carry the returned secret")
	}
}

// TestWithBackendLoadReq_ReusesExistingSecret pins the idempotent-load fix: when
// the app record already carries a session secret (the plugin process is live),
// a re-load must reuse it. Rotating here desyncs the record from the running
// process — the new OnLoadConfig is never delivered on an idempotent load — and
// the gateway proxy then mints tokens the plugin listener rejects (401 on every
// /plugin/{id} request, reported as {"error":"unauthorized"}).
func TestWithBackendLoadReq_ReusesExistingSecret(t *testing.T) {
	a := &Actor{Records: map[string]appRecord{
		"app.x": {SessionSecret: "existingsecret"},
	}}
	req, secret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	if secret != "existingsecret" {
		t.Fatalf("secret = %q, want existing record secret reused", secret)
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.SessionSecret != "existingsecret" {
		t.Fatal("config must carry the existing secret")
	}
}

// TestWithBackendLoadReq_MintsWhenNoRecord pins that a first-time load (no
// record, or record without a committed backend) still mints a fresh secret.
func TestWithBackendLoadReq_MintsWhenNoRecord(t *testing.T) {
	_, secret, err := (&Actor{}).withBackendLoadReq(gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("must mint a fresh secret when the app has no record")
	}
}

// TestWithBackendLoadReqCarriesProjectBinding pins the wiki.read routing
// anchor: the OnLoad config embeds the record's project binding so the
// pluginhost can route project.wiki_* host calls to the bound project. No
// record (zip install, first load) means no binding — fail closed there.
func TestWithBackendLoadReqCarriesProjectBinding(t *testing.T) {
	a := &Actor{}
	req, _, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "" {
		t.Fatalf("unbound record: ProjectID = %q, want empty", cfg.ProjectID)
	}

	a = &Actor{Records: map[string]appRecord{"app.x": {ProjectID: "novelking"}}}
	req, _, err = a.withBackendLoadReq(gen.PluginArtifactLoadReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "novelking" {
		t.Fatalf("bound record: ProjectID = %q, want novelking", cfg.ProjectID)
	}

	// The reload-prepare twin carries the same anchor.
	prep, _, err := a.withBackendPrepareReq(gen.PluginArtifactReloadPrepareReq{Manifest: gen.AppManifest{ID: "app.x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(prep.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.ProjectID != "novelking" {
		t.Fatalf("prepare: ProjectID = %q, want novelking", cfg.ProjectID)
	}
}

// TestRecordProjectBinding pins the fill-first persistence: the binding is
// recorded once and never overwritten by a different project, and an empty
// project id is a no-op.
func TestRecordProjectBinding(t *testing.T) {
	a := &Actor{Records: map[string]appRecord{"app.x": {}}}
	a.recordProjectBinding(nil, "app.x", "novelking")
	if got := a.Records["app.x"].ProjectID; got != "novelking" {
		t.Fatalf("binding after record = %q, want novelking", got)
	}
	// Same value: no-op.
	a.recordProjectBinding(nil, "app.x", "novelking")
	// Different non-empty value: never overwritten.
	a.recordProjectBinding(nil, "app.x", "other")
	if got := a.Records["app.x"].ProjectID; got != "novelking" {
		t.Fatalf("binding after conflicting record = %q, want novelking (fill-first)", got)
	}
	// Empty: no-op.
	a.recordProjectBinding(nil, "app.x", "")
	if got := a.Records["app.x"].ProjectID; got != "novelking" {
		t.Fatalf("binding after empty record = %q, want novelking", got)
	}
}

// TestAppDirFromArtifactPath pins the build-layout derivation feeding
// LoadConfig.StaticDir: the subprocess plugin inherits the host cwd, so the
// SDK static root must be anchored to the app directory explicitly. Unknown
// layouts yield "" (SDK stays fail-closed) rather than a guessed root.
func TestAppDirFromArtifactPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{filepath.ToSlash(filepath.Join("P", "proj", ".sporecode", "build", "plugin-x.exe")), filepath.Join("P", "proj")},
		{filepath.Join("D", "dev", "example", ".sporecode", "build", "plugin-y.exe"), filepath.Join("D", "dev", "example")},
		{filepath.Join("D", "random", "plugin-x.exe"), ""},
		{filepath.Join("D", "proj", ".sporecode", "plugin-x.exe"), ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := appbinding.AppDirFromArtifactPath(c.in); got != c.want {
			t.Errorf("AppDirFromArtifactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// The load config must carry the derived static dir.
	req, _, err := (&Actor{}).withBackendLoadReq(gen.PluginArtifactLoadReq{
		Manifest:     gen.AppManifest{ID: "app.x"},
		ArtifactPath: filepath.Join("D", "proj", ".sporecode", "build", "plugin-x.exe"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.StaticDir != filepath.Join("D", "proj") {
		t.Fatalf("StaticDir = %q, want app dir", cfg.StaticDir)
	}
}

// TestAppDataDirFor pins the app.data grant semantics: the dataDir is only
// handed out when the manifest declares the app.data capability AND the app
// directory resolves (project-registered apps derive it from the artifact
// path). The directory is created eagerly; undeclared or unresolved grants
// stay fail-closed ("").
func TestAppDataDirFor(t *testing.T) {
	// Declared + derivable → granted, created.
	root := t.TempDir()
	artifact := filepath.Join(root, "myapp", ".sporecode", "build", "plugin-a.exe")
	manifest := gen.AppManifest{ID: "app.a", Permissions: []string{appbinding.CapAppData}}
	appDir := appbinding.AppDirFromArtifactPath(artifact)
	dir, err := appDataDirFor(manifest, appDir)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "myapp", ".sporecode", "appdata")
	if dir != want {
		t.Fatalf("dataDir = %q, want %q", dir, want)
	}
	if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
		t.Fatalf("appdata dir must be created: %v", err)
	}

	// Undeclared → "" even with a derivable layout.
	dir, err = appDataDirFor(gen.AppManifest{ID: "app.b"}, appDir)
	if err != nil {
		t.Fatal(err)
	}
	if dir != "" {
		t.Fatalf("undeclared permission must yield empty dataDir, got %q", dir)
	}

	// Declared but unresolved (unknown layout) → fail-closed "".
	dir, err = appDataDirFor(manifest, appbinding.AppDirFromArtifactPath(filepath.Join(root, "tmp", "plugin-a.exe")))
	if err != nil {
		t.Fatal(err)
	}
	if dir != "" {
		t.Fatalf("unresolved app dir must yield empty dataDir, got %q", dir)
	}

	// The load config carries the grant end to end.
	req, _, err := (&Actor{}).withBackendLoadReq(gen.PluginArtifactLoadReq{
		Manifest:     manifest,
		ArtifactPath: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != want {
		t.Fatalf("load config DataDir = %q, want %q", cfg.DataDir, want)
	}
}

// TestResolveBackendAppDir pins the two anchoring rules feeding LoadConfig
// StaticDir/DataDir:
//   - an inventory-owned (zip-installed) artifact anchors to the app's
//     materialized install directory (installedAppDir) — the zip layout has no
//     project tree to derive from;
//   - a project-registered artifact keeps the build-layout derivation;
//   - unknown layouts stay fail-closed ("").
func TestResolveBackendAppDir(t *testing.T) {
	root := t.TempDir()
	config.SetDataDirForTest(root)
	t.Cleanup(config.ResetForTest)

	installedArtifact := filepath.Join(inventoryArtifactsDir(), "app.inst-abc123.exe")
	if got, want := resolveBackendAppDir("app.inst", installedArtifact), installedAppDir("app.inst"); got != want {
		t.Fatalf("installed app dir = %q, want %q", got, want)
	}
	projectArtifact := filepath.Join(root, "proj", ".sporecode", "build", "plugin-a.exe")
	if got, want := resolveBackendAppDir("app.proj", projectArtifact), filepath.Join(root, "proj"); got != want {
		t.Fatalf("project app dir = %q, want %q", got, want)
	}
	if got := resolveBackendAppDir("app.unknown", filepath.Join(root, "tmp", "plugin-a.exe")); got != "" {
		t.Fatalf("unknown layout = %q, want fail-closed \"\"", got)
	}
}

// TestWithBackendLoadReq_InstalledAppStaticDir is the regression for the
// reported "frontend error not found" on a zip-installed plugin: an installed
// native app's artifact lives in the content-addressed inventory, so the app
// dir must be explicitly anchored to the materialized install directory.
// Without this the SDK's static root stayed on its "." default (the host cwd)
// and every /plugin/{id}/ asset request 404'd (and leaked the host cwd).
func TestWithBackendLoadReq_InstalledAppStaticDir(t *testing.T) {
	root := t.TempDir()
	config.SetDataDirForTest(root)
	t.Cleanup(config.ResetForTest)

	artifact := filepath.Join(inventoryArtifactsDir(), "app.inst-abc123.exe")
	req, _, err := (&Actor{}).withBackendLoadReq(gen.PluginArtifactLoadReq{
		Manifest:     gen.AppManifest{ID: "app.inst"},
		ArtifactPath: artifact,
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg backendLoadConfig
	if err := json.Unmarshal(req.OnLoadConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.StaticDir != installedAppDir("app.inst") {
		t.Fatalf("StaticDir = %q, want the materialized install dir %q", cfg.StaticDir, installedAppDir("app.inst"))
	}
}

// TestMaterializeInstalledAssets pins the on-disk projection of an installed
// package's asset bundle (nested paths, stale-file clearing, traversal
// rejection) — the tree the plugin listener serves as its static root.
func TestMaterializeInstalledAssets(t *testing.T) {
	root := t.TempDir()
	config.SetDataDirForTest(root)
	t.Cleanup(config.ResetForTest)

	assets := map[string][]byte{
		"index.html":    []byte("<html></html>"),
		"assets/app.js": []byte("js"),
		"../escape.txt": []byte("nope"),
	}
	if err := materializeInstalledAssets("app.inst", assets); err != nil {
		t.Fatal(err)
	}
	dir := installedAppDir("app.inst")
	if b, err := os.ReadFile(filepath.Join(dir, "index.html")); err != nil || string(b) != "<html></html>" {
		t.Fatalf("index.html = %q, err %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "assets", "app.js")); err != nil || string(b) != "js" {
		t.Fatalf("assets/app.js = %q, err %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Fatal("traversal asset must not be written outside the app dir")
	}

	// A subsequent install with a smaller bundle clears the retired files.
	if err := materializeInstalledAssets("app.inst", map[string][]byte{"index.html": []byte("v2")}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "assets", "app.js")); err == nil {
		t.Fatal("stale asset from the previous bundle must be cleared")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.html")); string(b) != "v2" {
		t.Fatalf("index.html = %q, want v2", b)
	}
}

func TestSetBackendFromLoad(t *testing.T) {
	t.Run("listener confirmed", func(t *testing.T) {
		rec := appRecord{}
		setBackendFromLoad(&rec, "abc123", "127.0.0.1:52345")
		if rec.SessionSecret != "abc123" {
			t.Fatalf("SessionSecret = %q", rec.SessionSecret)
		}
		if rec.BackendPort != 52345 {
			t.Fatalf("BackendPort = %d", rec.BackendPort)
		}
		if rec.BackendUrl != "http://127.0.0.1:52345" {
			t.Fatalf("BackendUrl = %q", rec.BackendUrl)
		}
	})
	t.Run("empty addr clears and drops secret", func(t *testing.T) {
		rec := appRecord{SessionSecret: "old", BackendPort: 1, BackendUrl: "http://127.0.0.1:1"}
		setBackendFromLoad(&rec, "fresh", "")
		if rec.SessionSecret != "" || rec.BackendPort != 0 || rec.BackendUrl != "" {
			t.Fatalf("backend must be cleared, got %+v", rec)
		}
	})
	t.Run("unparsable addr clears", func(t *testing.T) {
		rec := appRecord{SessionSecret: "old", BackendPort: 1, BackendUrl: "http://127.0.0.1:1"}
		setBackendFromLoad(&rec, "fresh", "not-an-addr")
		if rec.SessionSecret != "" || rec.BackendUrl != "" {
			t.Fatalf("backend must be cleared, got %+v", rec)
		}
	})
	t.Run("ipv6 bracket addr", func(t *testing.T) {
		rec := appRecord{}
		setBackendFromLoad(&rec, "s", "[::1]:8080")
		if rec.BackendUrl != "http://[::1]:8080" || rec.BackendPort != 8080 {
			t.Fatalf("ipv6 backend = %+v", rec)
		}
	})
}

func TestMintSessionCookie(t *testing.T) {
	secret, err := newInstanceSecret()
	if err != nil {
		t.Fatal(err)
	}
	const sid = "session-123"
	tok := mintSessionCookie(secret, sid)
	parts := strings.Split(tok, ".")
	if len(parts) != 2 || parts[0] != sid {
		t.Fatalf("cookie must be sessionId.hexMAC, got %q", tok)
	}
	// Verify with the same construction the SDK's validSessionToken uses:
	// hex-decode the secret, HMAC-SHA256 over the session id, constant-time
	// compare.
	key, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(sid))
	want := hex.EncodeToString(mac.Sum(nil))
	if parts[1] != want {
		t.Fatal("cookie MAC does not match independent HMAC computation")
	}
	// A cookie minted for another session id must not verify against this one.
	other := mintSessionCookie(secret, "session-999")
	if other == tok {
		t.Fatal("cookies for different sessions must differ")
	}
	if mintSessionCookie("", sid) != "" {
		t.Fatal("empty secret must mint no cookie")
	}
	if mintSessionCookie(secret, "") != "" {
		t.Fatal("empty session id must mint no cookie")
	}
}

func TestCommitBackend(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.one"}
	var calls []struct {
		callID  string
		payload any
	}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		calls = append(calls, struct {
			callID  string
			payload any
		}{callID, payload})
		return nil
	})
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	a := &Actor{
		actorID: "appmanager",
		Apps:    map[string]gen.AppManifest{manifest.ID: manifest},
		Records: map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateRunning}},
		store:   persist.NewFSPersist(t.TempDir()),
	}
	// The committed secret must be valid hex so the gateway token mints.
	const secret = "736563726574"
	a.commitBackend(ctx, manifest.ID, secret, "127.0.0.1:4001")
	rec := a.Records[manifest.ID]
	if rec.BackendPort != 4001 || rec.BackendUrl != "http://127.0.0.1:4001" || rec.SessionSecret != secret {
		t.Fatalf("backend not committed: %+v", rec)
	}
	// The commit attaches the reverse proxy through pluginhost with the
	// gateway token the SDK side verifies.
	if len(calls) != 1 || calls[0].callID != "pluginhost.proxy_attach" {
		t.Fatalf("expected one proxy_attach tell, got %+v", calls)
	}
	attach, ok := calls[0].payload.(gen.PluginProxyAttachReq)
	if !ok {
		t.Fatalf("attach payload type = %T", calls[0].payload)
	}
	if attach.PluginID != manifest.ID || attach.Addr != "127.0.0.1:4001" {
		t.Fatalf("attach = %+v", attach)
	}
	key, err := hex.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	if want := sdk.MintSessionToken(key, "gateway-proxy"); attach.Token != want {
		t.Fatalf("gateway token = %q, want SDK-minted %q", attach.Token, want)
	}
	// Reload semantics: a re-commit with a fresh listener re-attaches
	// (replace, idempotent on the pluginhost side).
	a.commitBackend(ctx, manifest.ID, secret, "127.0.0.1:4002")
	if len(calls) != 2 || calls[1].callID != "pluginhost.proxy_attach" {
		t.Fatalf("reload must re-attach, got %+v", calls)
	}
	if reattach := calls[1].payload.(gen.PluginProxyAttachReq); reattach.Addr != "127.0.0.1:4002" {
		t.Fatalf("reattach addr = %q", reattach.Addr)
	}
	// An empty report clears the stored backend and detaches the proxy.
	a.commitBackend(ctx, manifest.ID, "73656372", "")
	rec = a.Records[manifest.ID]
	if rec.BackendPort != 0 || rec.BackendUrl != "" || rec.SessionSecret != "" {
		t.Fatalf("backend not cleared: %+v", rec)
	}
	if len(calls) != 3 || calls[2].callID != "pluginhost.proxy_detach" {
		t.Fatalf("clear must detach, got %+v", calls)
	}
	if detach := calls[2].payload.(gen.PluginProxyDetachReq); detach.PluginID != manifest.ID {
		t.Fatalf("detach = %+v", detach)
	}
	// Unknown apps are a no-op (no panic, no record creation, no tells).
	a.commitBackend(ctx, "app.unknown", secret, "127.0.0.1:9")
	if _, ok := a.Records["app.unknown"]; ok {
		t.Fatal("unknown app must not gain a record")
	}
	if len(calls) != 3 {
		t.Fatalf("unknown app must not tell, got %+v", calls)
	}
}

// TestReportProcessStateRunningReattachesGatewayProxy covers the cold-start
// restore path: the plugin respawned on a fresh ephemeral port reports
// "running" with its HttpAddr; appmanager refreshes the (stale) persisted
// backend, keeps the persisted secret, and re-attaches the gateway proxy.
// A repeat report with the same addr does not re-attach.
func TestReportProcessStateRunningReattachesGatewayProxy(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.restore"}
	const secret = "736563726574"
	var calls []struct {
		callID  string
		payload any
	}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		calls = append(calls, struct {
			callID  string
			payload any
		}{callID, payload})
		return nil
	})
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	a := &Actor{
		actorID: "appmanager",
		Apps:    map[string]gen.AppManifest{manifest.ID: manifest},
		Records: map[string]appRecord{manifest.ID: {
			Manifest: manifest, State: stateRunning,
			// Pre-restart backend: same secret, dead port.
			SessionSecret: secret, BackendPort: 4001, BackendUrl: "http://127.0.0.1:4001",
		}},
		store: persist.NewFSPersist(t.TempDir()),
	}

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: manifest.ID, State: stateRunning, HttpAddr: "127.0.0.1:40999",
	}); err != nil {
		t.Fatal(err)
	}
	rec := a.Records[manifest.ID]
	if rec.BackendPort != 40999 || rec.BackendUrl != "http://127.0.0.1:40999" {
		t.Fatalf("backend not refreshed to fresh listener: %+v", rec)
	}
	if rec.SessionSecret != secret {
		t.Fatalf("persisted secret must be kept across restart: %+v", rec)
	}
	if len(calls) != 2 || calls[0].callID != "pluginhost.proxy_attach" || calls[1].callID != "pluginhost.event_deliver" {
		t.Fatalf("expected one proxy_attach + one lifecycle forward, got %+v", calls)
	}
	attach := calls[0].payload.(gen.PluginProxyAttachReq)
	key, _ := hex.DecodeString(secret)
	if attach.Addr != "127.0.0.1:40999" || attach.Token != sdk.MintSessionToken(key, "gateway-proxy") {
		t.Fatalf("attach = %+v", attach)
	}

	// Repeat running report with the same addr is idempotent: no re-attach
	// and no second remount signal.
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: manifest.ID, State: stateRunning, HttpAddr: "127.0.0.1:40999",
	}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("same-addr report must not re-attach or re-emit, got %+v", calls)
	}
}

// TestCommitBackendAttachFailureDoesNotBlock covers the degrade-don't-wedge
// contract: with the pluginhost unreachable the commit still lands, only the
// attach tell is skipped.
func TestCommitBackendAttachFailureDoesNotBlock(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.one"}
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }
	a := &Actor{
		actorID: "appmanager",
		Apps:    map[string]gen.AppManifest{manifest.ID: manifest},
		Records: map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateRunning}},
		store:   persist.NewFSPersist(t.TempDir()),
	}
	a.commitBackend(ctx, manifest.ID, "736563726574", "127.0.0.1:4001")
	rec := a.Records[manifest.ID]
	if rec.BackendUrl != "http://127.0.0.1:4001" {
		t.Fatalf("commit must land despite attach failure: %+v", rec)
	}
}

// TestPluginhostOnlineReconcilesGatewayProxies pins the deterministic
// cold-start fix: a running report processed while pluginhost was still
// unexposed drops its attach tell; pluginhost's end-of-OnStart online tell
// must then re-attach every running native app with a committed backend.
// Reload-in-flight apps keep their deferral (commitBackend attaches on
// commit), and non-native / non-running / backend-less apps are skipped.
func TestPluginhostOnlineReconcilesGatewayProxies(t *testing.T) {
	const secret = "736563726574"
	var calls []struct {
		callID  string
		payload any
	}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		calls = append(calls, struct {
			callID  string
			payload any
		}{callID, payload})
		return nil
	})
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }

	running := gen.AppManifest{ID: "app.running", Runtime: "native"}
	running2 := gen.AppManifest{ID: "app.running2", Runtime: "native"}
	reloading := gen.AppManifest{ID: "app.reloading", Runtime: "native"}
	stopped := gen.AppManifest{ID: "app.stopped", Runtime: "native"}
	spore := gen.AppManifest{ID: "app.spore", Runtime: "spore"}
	nolistener := gen.AppManifest{ID: "app.nolistener", Runtime: "native"}
	newRecord := func(m gen.AppManifest, state string, backendURL string, port int) appRecord {
		return appRecord{Manifest: m, State: state, SessionSecret: secret, BackendPort: port, BackendUrl: backendURL}
	}
	a := &Actor{
		actorID: "appmanager",
		Apps: map[string]gen.AppManifest{
			running.ID: running, reloading.ID: reloading, stopped.ID: stopped,
			spore.ID: spore, nolistener.ID: nolistener, running2.ID: running2,
		},
		Records: map[string]appRecord{
			running.ID:    newRecord(running, stateRunning, "http://127.0.0.1:4001", 4001),
			running2.ID:   newRecord(running2, stateRunning, "http://127.0.0.1:4002", 4002),
			reloading.ID:  newRecord(reloading, stateRunning, "http://127.0.0.1:4003", 4003),
			stopped.ID:    newRecord(stopped, stateStopped, "http://127.0.0.1:4004", 4004),
			spore.ID:      newRecord(spore, stateRunning, "http://127.0.0.1:4005", 4005),
			nolistener.ID: newRecord(nolistener, stateRunning, "", 0),
		},
		reloadingApps: map[string]bool{reloading.ID: true},
		store:         persist.NewFSPersist(t.TempDir()),
	}

	if err := a.handlePluginhostOnline(ctx, gen.AppManagerPluginhostOnlineReq{}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 {
		t.Fatalf("expected 2 proxy_attach + 2 event_deliver tells, got %+v", calls)
	}
	if got := countCalls(calls2IDs(calls), "pluginhost.proxy_attach"); got != 2 {
		t.Fatalf("proxy calls = %v, want two proxy_attach", calls2IDs(calls))
	}
	if got := countCalls(calls2IDs(calls), "pluginhost.event_deliver"); got != 2 {
		t.Fatalf("proxy calls = %v, want two event_deliver", calls2IDs(calls))
	}
	// Each re-attached app bumps its session generation and emits a reloaded
	// lifecycle event carrying it — the remount signal for panels that loaded
	// while the static assets handler still owned the route.
	reloaded := map[string]int64{}
	for _, ev := range ctx.EmittedEvents {
		le, ok := ev.Payload.(gen.AppLifecycleEvent)
		if !ok {
			t.Fatalf("unexpected %s payload %T", ev.Kind, ev.Payload)
		}
		if le.Kind != "reloaded" || le.ID != running.ID && le.ID != running2.ID {
			t.Fatalf("lifecycle event = %+v, want reloaded for reconciled apps only", le)
		}
		reloaded[le.ID] = le.Generation
	}
	if len(reloaded) != 2 {
		t.Fatalf("reloaded events = %+v, want one per reconciled app", reloaded)
	}
	for id, g := range reloaded {
		if g != 1 {
			t.Fatalf("generation for %s = %d, want bumped to 1", id, g)
		}
		if a.Records[id].Generation != g {
			t.Fatalf("record generation for %s = %d, want %d", id, a.Records[id].Generation, g)
		}
	}
	for _, id := range []string{reloading.ID, stopped.ID, spore.ID, nolistener.ID} {
		if a.Records[id].Generation != 0 {
			t.Fatalf("%s must not be bumped", id)
		}
	}
	attached := map[string]string{}
	for _, c := range calls {
		if c.callID != "pluginhost.proxy_attach" {
			continue
		}
		attach := c.payload.(gen.PluginProxyAttachReq)
		attached[attach.PluginID] = attach.Addr
	}
	if attached[running.ID] != "127.0.0.1:4001" || attached[running2.ID] != "127.0.0.1:4002" {
		t.Fatalf("attach targets = %+v", attached)
	}
	for _, id := range []string{reloading.ID, stopped.ID, spore.ID, nolistener.ID} {
		if _, ok := attached[id]; ok {
			t.Fatalf("%s must not be attached", id)
		}
	}
	// The gateway token is minted from the record secret like every attach.
	for _, c := range calls {
		attach, ok := c.payload.(gen.PluginProxyAttachReq)
		if !ok {
			continue
		}
		key, _ := hex.DecodeString(secret)
		if attach.Token != sdk.MintSessionToken(key, "gateway-proxy") {
			t.Fatalf("attach token for %s not minted from record secret", attach.PluginID)
		}
	}
}

// calls2IDs flattens the (callID, payload) invoke log so countCalls can be
// reused across tests that capture richer call records.
func calls2IDs(calls []struct {
	callID  string
	payload any
}) []string {
	ids := make([]string, len(calls))
	for i, c := range calls {
		ids[i] = c.callID
	}
	return ids
}

func TestSessionCreateDualTrackBackend(t *testing.T) {
	a, appID := newSessionTestActor(t)
	// Track 1: no backend configured (legacy / in-process / spore) — the
	// response carries no backend fields.
	created, err := a.handleSessionCreate(&testutil.FakeCtx{}, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if created.BackendURL != "" || created.CookieToken != "" {
		t.Fatalf("no-backend session must not carry backend fields: %+v", created)
	}

	// Track 2: a live SDK HTTP listener — the response hands the iframe the
	// origin plus a cookie token the subprocess can verify.
	rec := a.Records[appID]
	rec.BackendPort = 43121
	rec.BackendUrl = "http://127.0.0.1:43121"
	rec.SessionSecret = "736563726574"
	a.Records[appID] = rec
	created2, err := a.handleSessionCreate(&testutil.FakeCtx{}, gen.AppSessionCreateReq{AppID: appID, ViewID: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if created2.BackendURL != "http://127.0.0.1:43121" {
		t.Fatalf("BackendURL = %q", created2.BackendURL)
	}
	if !strings.HasPrefix(created2.CookieToken, created2.SessionID+".") {
		t.Fatalf("CookieToken must embed the session id: %q", created2.CookieToken)
	}
	// The cookie verifies against the record secret exactly like the SDK
	// would (HMAC over the session id).
	key, _ := hex.DecodeString(rec.SessionSecret)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(created2.SessionID))
	if created2.CookieToken != created2.SessionID+"."+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("CookieToken does not match HMAC(secret, sessionId)")
	}
}

func TestStatusFromRecordCarriesBackend(t *testing.T) {
	a, _ := newSessionTestActor(t)
	rec := a.Records["app.one"]
	rec.BackendPort = 4000
	rec.BackendUrl = "http://127.0.0.1:4000"
	status := a.statusFromRecord(a.Apps["app.one"], rec)
	if status.BackendPort != 4000 || status.BackendURL != "http://127.0.0.1:4000" {
		t.Fatalf("status backend = %d %q", status.BackendPort, status.BackendURL)
	}
}

func TestReportProcessStateClearsBackend(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.native"}
	a := &Actor{
		actorID: "appmanager",
		Apps:    map[string]gen.AppManifest{manifest.ID: manifest},
		Records: map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateRunning, BackendPort: 5000, BackendUrl: "http://127.0.0.1:5000", SessionSecret: "sec"}},
		store:   persist.NewFSPersist(t.TempDir()),
	}
	var detachCalls []string
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "pluginhost.proxy_detach" {
			detachCalls = append(detachCalls, payload.(gen.PluginProxyDetachReq).PluginID)
		}
		return nil
	})
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	// A crash kills the process and its listener: the backend must clear
	// and the gateway proxy must detach.
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{PluginID: manifest.ID, State: stateCrashed, CrashCause: "boom"}); err != nil {
		t.Fatal(err)
	}
	rec := a.Records[manifest.ID]
	if rec.BackendPort != 0 || rec.BackendUrl != "" || rec.SessionSecret != "" {
		t.Fatalf("backend must clear on crash: %+v", rec)
	}
	if len(detachCalls) != 1 || detachCalls[0] != manifest.ID {
		t.Fatalf("crash must detach the proxy, got %v", detachCalls)
	}
	// A plain stop clears and detaches too.
	a.Records[manifest.ID] = appRecord{Manifest: manifest, State: stateRunning, BackendPort: 5001, BackendUrl: "http://127.0.0.1:5001", SessionSecret: "sec"}
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{PluginID: manifest.ID, State: stateStopped}); err != nil {
		t.Fatal(err)
	}
	if rec := a.Records[manifest.ID]; rec.BackendUrl != "" {
		t.Fatalf("backend must clear on stop: %+v", rec)
	}
	if len(detachCalls) != 2 {
		t.Fatalf("stop must detach the proxy, got %v", detachCalls)
	}
	// The spawn-time "running" report keeps a previously committed backend
	// and sends no detach.
	a.Records[manifest.ID] = appRecord{Manifest: manifest, State: stateRunning, BackendPort: 5002, BackendUrl: "http://127.0.0.1:5002", SessionSecret: "sec"}
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{PluginID: manifest.ID, State: stateRunning}); err != nil {
		t.Fatal(err)
	}
	if rec := a.Records[manifest.ID]; rec.BackendUrl != "http://127.0.0.1:5002" {
		t.Fatalf("running report must keep backend: %+v", rec)
	}
	if len(detachCalls) != 2 {
		t.Fatalf("running report must not detach, got %v", detachCalls)
	}
}
