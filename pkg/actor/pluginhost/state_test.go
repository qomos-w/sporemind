package pluginhost

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// hermeticAppStateActor returns an actor whose app-state store lives in a
// temp dir, so state tests never touch the real ActorDataDir.
func hermeticAppStateActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{}
	a.appStateStore = persist.NewFSPersist(filepath.Join(t.TempDir(), "appstate"))
	return a
}

func TestStateHandlerSetGetDelete(t *testing.T) {
	a := hermeticAppStateActor(t)

	_, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "captcha", Key: "counter", Value: []byte("42")})
	if err != nil {
		t.Fatalf("state.set: %v", err)
	}

	resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "captcha", Key: "counter"})
	if err != nil {
		t.Fatalf("state.get: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if string(resp.Value) != "42" {
		t.Fatalf("expected value 42, got %q", resp.Value)
	}

	_, err = a.handleStateDelete(nil, gen.PluginStateDeleteReq{Plugin: "captcha", Key: "counter"})
	if err != nil {
		t.Fatalf("state.delete: %v", err)
	}

	resp, err = a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "captcha", Key: "counter"})
	if err != nil {
		t.Fatalf("state.get after delete: %v", err)
	}
	if resp.Found {
		t.Fatal("expected Found=false after delete")
	}
}

func TestStateHandlerPerPluginIsolation(t *testing.T) {
	a := hermeticAppStateActor(t)

	_, _ = a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "pluginA", Key: "key1", Value: []byte("valueA")})
	_, _ = a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "pluginB", Key: "key1", Value: []byte("valueB")})

	resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "pluginA", Key: "key1"})
	if err != nil {
		t.Fatalf("pluginA state.get: %v", err)
	}
	if string(resp.Value) != "valueA" {
		t.Fatalf("pluginA expected valueA, got %q", resp.Value)
	}

	resp, err = a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "pluginB", Key: "key1"})
	if err != nil {
		t.Fatalf("pluginB state.get: %v", err)
	}
	if string(resp.Value) != "valueB" {
		t.Fatalf("pluginB expected valueB, got %q", resp.Value)
	}

	_, _ = a.handleStateDelete(nil, gen.PluginStateDeleteReq{Plugin: "pluginB", Key: "key1"})
	resp, err = a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "pluginA", Key: "key1"})
	if err != nil {
		t.Fatalf("pluginA state.get after pluginB delete: %v", err)
	}
	if !resp.Found {
		t.Fatal("pluginA key must survive pluginB delete")
	}
}

func TestStateHandlerRequiresPluginAndKey(t *testing.T) {
	a := hermeticAppStateActor(t)

	_, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "", Key: "key"})
	if err == nil || !strings.Contains(err.Error(), "requires Plugin and Key") {
		t.Fatalf("expected error for missing Plugin, got %v", err)
	}

	_, err = a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "captcha", Key: "", Value: []byte("val")})
	if err == nil || !strings.Contains(err.Error(), "requires Plugin and Key") {
		t.Fatalf("expected error for missing Key, got %v", err)
	}
}

func TestHostBridgeStateDispatchInjectsPluginID(t *testing.T) {
	var gotCallID string
	var gotReq []byte
	storeDispatch := func(callID string, req []byte) ([]byte, error) {
		gotCallID = callID
		gotReq = req
		return []byte(`{}`), nil
	}
	b := NewHostBridge(
		map[string]struct{}{"app.state": {}},
		nil,
		"captcha-plugin",
		storeDispatch,
	)

	_, err := b.Dispatch("state.get", []byte(`{"key":"counter"}`))
	if err != nil {
		t.Fatalf("Dispatch state.get: %v", err)
	}
	if gotCallID != "state.get" {
		t.Fatalf("expected callID state.get, got %q", gotCallID)
	}
	var m map[string]any
	if err := json.Unmarshal(gotReq, &m); err != nil {
		t.Fatalf("unmarshal injected req: %v", err)
	}
	if m["Plugin"] != "captcha-plugin" {
		t.Fatalf("expected Plugin=captcha-plugin, got %v", m["Plugin"])
	}
	if m["key"] != "counter" {
		t.Fatalf("expected key=counter, got %v", m["key"])
	}
}

func TestHostBridgeStateDeniedWithoutCapability(t *testing.T) {
	b := NewHostBridge(nil, nil, "captcha", func(string, []byte) ([]byte, error) {
		t.Fatal("storeDispatch must not be called when capability not granted")
		return nil, nil
	})
	_, err := b.Dispatch("state.get", []byte(`{"key":"k"}`))
	if err == nil {
		t.Fatal("expected error for ungranted app.state capability")
	}
}

func TestHostBridgeStateRequiresStoreDispatch(t *testing.T) {
	b := NewHostBridge(
		map[string]struct{}{"app.state": {}},
		nil,
		"captcha",
		nil,
	)
	_, err := b.Dispatch("state.get", []byte(`{"key":"k"}`))
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("expected 'not configured' error, got %v", err)
	}
}

func TestHostBridgeInjectPluginIDOverwritesSDKValue(t *testing.T) {
	var gotReq []byte
	storeDispatch := func(callID string, req []byte) ([]byte, error) {
		gotReq = req
		return []byte(`{}`), nil
	}
	b := NewHostBridge(
		map[string]struct{}{"app.state": {}},
		nil,
		"real-plugin",
		storeDispatch,
	)
	_, err := b.Dispatch("state.set", []byte(`{"Plugin":"spoofed","key":"k","value":"v"}`))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	var m map[string]any
	json.Unmarshal(gotReq, &m)
	if m["Plugin"] != "real-plugin" {
		t.Fatalf("expected injected Plugin=real-plugin (overwriting SDK spoof), got %v", m["Plugin"])
	}
}

// TestStateSurvivesZombieArtifactUnload is a defense-in-depth regression for
// BP13: explicit artifact_unload of a restore-failed zombie must purge the
// persisted load record and descriptor, but it must never touch the appstate
// store. App-private state is keyed only by plugin ID and survives any record
// churn (failed, unloaded, re-loaded) as long as the plugin ID is stable.
func TestStateSurvivesZombieArtifactUnload(t *testing.T) {
	a := restartPendingActor(t, nil)
	a.appStateStore = persist.NewFSPersist(filepath.Join(t.TempDir(), "appstate"))

	const pluginID = "app.zombie"
	a.Plugins = []PluginDescriptor{{
		ID: pluginID, Name: "Zombie", Version: "0.1.0",
		Runtime: "native", Status: "error",
	}}
	a.ArtifactLoads[pluginID] = gen.PluginArtifactLoadReq{
		ArtifactPath: t.TempDir() + "/missing.exe",
		Manifest:     gen.AppManifest{ID: pluginID, Name: "Zombie", Version: "0.1.0", Runtime: "native"},
	}

	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: pluginID, Key: "counter", Value: []byte("9")}); err != nil {
		t.Fatalf("state.set: %v", err)
	}

	if _, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: pluginID}); err != nil {
		t.Fatalf("handleArtifactUnload: %v", err)
	}

	resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: pluginID, Key: "counter"})
	if err != nil {
		t.Fatalf("state.get after unload: %v", err)
	}
	if !resp.Found || string(resp.Value) != "9" {
		t.Fatalf("state.get after unload = found=%v value=%q, want found=true value=9", resp.Found, resp.Value)
	}
}

func TestStateHandlerList(t *testing.T) {
	a := hermeticAppStateActor(t)

	for _, kv := range [][2]string{
		{"pluginA", "config/theme"},
		{"pluginA", "config/locale"},
		{"pluginA", "cache/last"},
		{"pluginB", "config/theme"},
	} {
		if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: kv[0], Key: kv[1], Value: []byte("v")}); err != nil {
			t.Fatalf("set %s/%s: %v", kv[0], kv[1], err)
		}
	}

	resp, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "pluginA"})
	if err != nil {
		t.Fatalf("state.list: %v", err)
	}
	got := append([]string(nil), resp.Keys...)
	sort.Strings(got)
	want := []string{"cache/last", "config/locale", "config/theme"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("list keys = %v, want %v", got, want)
	}

	// Prefix narrows the scan; keys come back prefix-relative.
	sub, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "pluginA", Prefix: "config"})
	if err != nil {
		t.Fatalf("state.list prefix: %v", err)
	}
	gotSub := append([]string(nil), sub.Keys...)
	sort.Strings(gotSub)
	if strings.Join(gotSub, ",") != "config/locale,config/theme" {
		t.Fatalf("prefixed list = %v", gotSub)
	}

	// pluginB sees only its own keys — no cross-app leakage.
	other, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "pluginB"})
	if err != nil {
		t.Fatalf("state.list pluginB: %v", err)
	}
	if len(other.Keys) != 1 || other.Keys[0] != "config/theme" {
		t.Fatalf("pluginB keys = %v", other.Keys)
	}
}

// TestStateHandlerListSurvivesAppendLogs pins that List is document-only:
// append-only logs must not inflate the document count (quota accounting)
// and must not show up as app keys.
func TestStateHandlerListSurvivesAppendLogs(t *testing.T) {
	a := hermeticAppStateActor(t)

	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "cfg", Value: []byte("{}")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := a.handleStateAppend(nil, gen.PluginStateAppendReq{Plugin: "p", Key: "logs/events.jsonl", Data: []byte("{\"n\":1}\n")}); err != nil {
		t.Fatalf("append: %v", err)
	}

	resp, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "p"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.Keys) != 1 || resp.Keys[0] != "cfg" {
		t.Fatalf("keys = %v, want only [cfg]", resp.Keys)
	}
}

func TestStateHandlerAppend(t *testing.T) {
	a := hermeticAppStateActor(t)

	for _, line := range []string{"{\"n\":1}\n", "{\"n\":2}\n"} {
		if _, err := a.handleStateAppend(nil, gen.PluginStateAppendReq{Plugin: "p", Key: "logs/events.jsonl", Data: []byte(line)}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	base, ok := a.appStateStore.(persist.BasePather)
	if !ok {
		t.Fatalf("fs appstate store must implement BasePather, got %T", a.appStateStore)
	}
	data, err := os.ReadFile(filepath.Join(base.BasePath(), "p", "logs", "events.jsonl"))
	if err != nil {
		t.Fatalf("read append log: %v", err)
	}
	if string(data) != "{\"n\":1}\n{\"n\":2}\n" {
		t.Fatalf("append log = %q", data)
	}
}

func TestStateHandlerGetSetMany(t *testing.T) {
	a := hermeticAppStateActor(t)

	if _, err := a.handleStateSetMany(nil, gen.PluginStateSetManyReq{Plugin: "p", Entries: map[string][]byte{
		"k1": []byte("v1"),
		"k2": []byte("v2"),
	}}); err != nil {
		t.Fatalf("set_many: %v", err)
	}

	resp, err := a.handleStateGetMany(nil, gen.PluginStateGetManyReq{Plugin: "p", Keys: []string{"k1", "k2", "missing"}})
	if err != nil {
		t.Fatalf("get_many: %v", err)
	}
	if string(resp.Entries["k1"]) != "v1" || string(resp.Entries["k2"]) != "v2" {
		t.Fatalf("entries = %v", resp.Entries)
	}
	if _, ok := resp.Entries["missing"]; ok {
		t.Fatal("missing key must be absent from entries")
	}

	// Batch cap rejects oversized batches.
	tooMany := make([]string, maxStateBatchKeys+1)
	if _, err := a.handleStateGetMany(nil, gen.PluginStateGetManyReq{Plugin: "p", Keys: tooMany}); err == nil {
		t.Fatal("expected batch cap error for get_many")
	}
	if _, err := a.handleStateSetMany(nil, gen.PluginStateSetManyReq{Plugin: "p", Entries: map[string][]byte{"only": []byte("x"), "extra": nil}}); err != nil {
		// 2 entries is under the cap; must succeed.
		t.Fatalf("set_many small batch: %v", err)
	}
}

// TestStateHandlerKeyTraversalRejected pins the namespace isolation red
// line: an app-controlled key must never escape its "<pluginID>/" prefix.
func TestStateHandlerKeyTraversalRejected(t *testing.T) {
	a := hermeticAppStateActor(t)

	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "a", Key: "../b/secret", Value: []byte("x")}); err == nil {
		t.Fatal("expected traversal rejection for .. key")
	}
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "a", Key: `/abs`, Value: []byte("x")}); err == nil {
		t.Fatal("expected rejection for root-relative key")
	}
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "a", Key: `win\path`, Value: []byte("x")}); err == nil {
		t.Fatal("expected rejection for backslash key")
	}
	if _, err := a.handleStateSetMany(nil, gen.PluginStateSetManyReq{Plugin: "a", Entries: map[string][]byte{"ok": []byte("1"), "../b/x": []byte("2")}}); err == nil {
		t.Fatal("expected traversal rejection in set_many")
	}

	// The rejected writes must have persisted nothing.
	resp, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "b"})
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(resp.Keys) != 0 {
		t.Fatalf("plugin b keys = %v, want none", resp.Keys)
	}
}

func TestStateHandlerQuotas(t *testing.T) {
	a := hermeticAppStateActor(t)
	a.appStateMaxDocsPerApp = 2
	a.appStateMaxValueBytes = 8
	a.appStateMaxAppendBytes = 16

	// Per-document byte cap.
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "big", Value: make([]byte, 9)}); err == nil {
		t.Fatal("expected per-document byte quota rejection")
	}
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "ok1", Value: []byte("12345678")}); err != nil {
		t.Fatalf("set at exactly the cap: %v", err)
	}

	// Per-app document cap.
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "ok2", Value: []byte("x")}); err != nil {
		t.Fatalf("second doc: %v", err)
	}
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "ok3", Value: []byte("x")}); err == nil {
		t.Fatal("expected per-app document quota rejection")
	}

	// Overwriting an existing doc does not consume new document quota.
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "p", Key: "ok1", Value: []byte("fresh")}); err != nil {
		t.Fatalf("overwrite within quota: %v", err)
	}

	// The cap applies per app, not globally.
	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "q", Key: "ok1", Value: []byte("x")}); err != nil {
		t.Fatalf("other plugin under cap: %v", err)
	}

	// Append byte cap.
	if _, err := a.handleStateAppend(nil, gen.PluginStateAppendReq{Plugin: "p", Key: "log.jsonl", Data: make([]byte, 17)}); err == nil {
		t.Fatal("expected append byte quota rejection")
	}
}

func TestStateHandlerPurgeCascade(t *testing.T) {
	a := hermeticAppStateActor(t)

	for _, kv := range [][2]string{{"app.a", "k1"}, {"app.a", "sub/k2"}, {"app.b", "k1"}} {
		if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: kv[0], Key: kv[1], Value: []byte("v")}); err != nil {
			t.Fatalf("set: %v", err)
		}
	}
	if _, err := a.handleStateAppend(nil, gen.PluginStateAppendReq{Plugin: "app.a", Key: "logs/e.jsonl", Data: []byte("{}\n")}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if _, err := a.handleStatePurge(nil, gen.PluginStatePurgeReq{PluginID: "app.a"}); err != nil {
		t.Fatalf("purge: %v", err)
	}

	if resp, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "app.a"}); err != nil || len(resp.Keys) != 0 {
		t.Fatalf("app.a keys after purge = %v (err %v), want none", resp.Keys, err)
	}
	if resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "app.a", Key: "k1"}); err != nil || resp.Found {
		t.Fatalf("app.a k1 after purge: found=%v err=%v", resp.Found, err)
	}
	// The append log is reclaimed with the subtree.
	base := a.appStateStore.(persist.BasePather)
	if _, err := os.Stat(filepath.Join(base.BasePath(), "app.a")); !os.IsNotExist(err) {
		t.Fatalf("app.a subtree must be gone, stat err = %v", err)
	}

	// Sibling app data is untouched.
	if resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "app.b", Key: "k1"}); err != nil || !resp.Found {
		t.Fatalf("app.b k1 must survive purge: found=%v err=%v", resp.Found, err)
	}

	// Purge is idempotent.
	if _, err := a.handleStatePurge(nil, gen.PluginStatePurgeReq{PluginID: "app.a"}); err != nil {
		t.Fatalf("second purge: %v", err)
	}
}

// TestStateHandlerPurgeAppData pins the appdata half of state_purge: the
// app.data grant directory is derived from the persisted artifact load record
// and reclaimed with the document subtree; a plugin without a load record (or
// one whose artifact path carries no project app-dir layout) purges the
// persist subtree only — never a guessed location.
func TestStateHandlerPurgeAppData(t *testing.T) {
	a := hermeticAppStateActor(t)

	root := t.TempDir()
	appDir := filepath.Join(root, "myapp")
	appdata := filepath.Join(appDir, ".sporecode", "appdata")
	if err := os.MkdirAll(filepath.Join(appDir, ".sporecode", "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(appdata, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appdata, "db", "CURRENT"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "other", ".sporecode", "appdata", "db"), 0o755); err != nil {
		t.Fatal(err)
	}

	a.mu.Lock()
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	a.ArtifactLoads["app.a"] = gen.PluginArtifactLoadReq{
		ArtifactPath: filepath.Join(appDir, ".sporecode", "build", "plugin-a.exe"),
	}
	// app.b has no load record: its appdata (if any) is out of reach.
	a.mu.Unlock()

	if _, err := a.handleStatePurge(nil, gen.PluginStatePurgeReq{PluginID: "app.a"}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(appdata); !os.IsNotExist(err) {
		t.Fatalf("app.a appdata must be reclaimed, stat err = %v", err)
	}
	// The rest of the app tree survives (source, manifest, build dir).
	if _, err := os.Stat(filepath.Join(appDir, ".sporecode", "build")); err != nil {
		t.Fatalf("build dir must survive purge: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "other", ".sporecode", "appdata", "db")); err != nil {
		t.Fatalf("unrelated appdata must survive purge: %v", err)
	}
}

// TestAppDataGrantLedgerLifecycle pins the grant ledger semantics end to end:
// recording from an OnLoad config (both granted and ungranted loads),
// survival past artifact unload (ArtifactLoads entry gone), reclamation by
// state_purge (ledger entry dropped), and the Save/Load roundtrip.
func TestAppDataGrantLedgerLifecycle(t *testing.T) {
	a := hermeticAppStateActor(t)
	a.store = persist.NewFSPersist(filepath.Join(t.TempDir(), "pluginhost"))
	a.actorID = "pluginhost-test"

	root := t.TempDir()
	granted := filepath.Join(root, "myapp", ".sporecode", "appdata")
	if err := os.MkdirAll(filepath.Join(granted, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(granted, "db", "CURRENT"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	onLoad, err := json.Marshal(map[string]string{"dataDir": granted})
	if err != nil {
		t.Fatal(err)
	}

	// Load with a granted dataDir records the grant.
	a.mu.Lock()
	a.recordAppDataGrant("app.a", onLoad)
	a.recordAppDataGrant("app.b", []byte(`{"staticDir":"/x"}`)) // no dataDir → no grant
	if a.AppDataGrants == nil {
		a.AppDataGrants = map[string]string{}
	}
	a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	a.mu.Unlock()
	if got := a.appDataDirOf("app.a"); got != granted {
		t.Fatalf("appDataDirOf(app.a) = %q, want granted dir", got)
	}
	if got := a.appDataDirOf("app.b"); got != "" {
		t.Fatalf("appDataDirOf(app.b) = %q, want empty (no grant)", got)
	}

	// Persist → fresh actor (host restart) → grant survives.
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	b := &Actor{store: a.store, actorID: a.actorID}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if b.AppDataGrants["app.a"] != granted {
		t.Fatalf("grant after restart = %v, want app.a → granted", b.AppDataGrants)
	}

	// Unload drops the artifact load record but not the grant.
	b.mu.Lock()
	delete(b.ArtifactLoads, "app.a")
	b.mu.Unlock()
	if got := b.appDataDirOf("app.a"); got != granted {
		t.Fatalf("grant must survive unload, got %q", got)
	}

	// state_purge reclaims the directory AND the ledger entry.
	if _, err := b.handleStatePurge(nil, gen.PluginStatePurgeReq{PluginID: "app.a"}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(granted); !os.IsNotExist(err) {
		t.Fatalf("appdata must be reclaimed, stat err = %v", err)
	}
	if _, ok := b.AppDataGrants["app.a"]; ok {
		t.Fatal("ledger entry must be dropped by purge")
	}
	if err := b.Save(); err != nil {
		t.Fatalf("save after purge: %v", err)
	}
}

// TestAppDataUsage pins the observability surface: per-grant footprint from a
// live directory walk, zero bytes for vanished directories, per-plugin
// filter, the surfaced soft limit, and the loaded flag.
func TestAppDataUsage(t *testing.T) {
	a := hermeticAppStateActor(t)

	root := t.TempDir()
	live := filepath.Join(root, "a", ".sporecode", "appdata")
	if err := os.MkdirAll(filepath.Join(live, "db"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "db", "k1"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "db", "k2"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}

	a.mu.Lock()
	if a.AppDataGrants == nil {
		a.AppDataGrants = map[string]string{}
	}
	a.AppDataGrants["app.a"] = live
	a.AppDataGrants["app.gone"] = filepath.Join(root, "gone", ".sporecode", "appdata")
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	a.ArtifactLoads["app.a"] = gen.PluginArtifactLoadReq{}
	a.mu.Unlock()

	resp, err := a.handleAppDataUsage(nil, gen.PluginAppDataUsageReq{})
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if resp.SoftLimitBytes != appDataSoftLimitBytes {
		t.Fatalf("soft limit = %d, want %d", resp.SoftLimitBytes, appDataSoftLimitBytes)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %v, want 2", resp.Items)
	}
	if resp.Items[0].PluginID != "app.a" || resp.Items[0].Bytes != 150 || resp.Items[0].Files != 2 || !resp.Items[0].Loaded {
		t.Fatalf("app.a item = %+v, want 150 bytes / 2 files / loaded", resp.Items[0])
	}
	if resp.Items[1].PluginID != "app.gone" || resp.Items[1].Bytes != 0 || resp.Items[1].Loaded {
		t.Fatalf("app.gone item = %+v, want zeros / unloaded", resp.Items[1])
	}
	if resp.TotalBytes != 150 {
		t.Fatalf("total = %d, want 150", resp.TotalBytes)
	}

	filtered, err := a.handleAppDataUsage(nil, gen.PluginAppDataUsageReq{PluginID: "app.gone"})
	if err != nil {
		t.Fatalf("filtered usage: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].PluginID != "app.gone" {
		t.Fatalf("filtered items = %+v, want only app.gone", filtered.Items)
	}
}

// TestStateHandlerBackendSwitch is the B9 backend-portability check: the
// same app-facing handler surface (set/get/list/append/purge) runs
// unchanged against every registered document backend. Switching backends
// is a host config change (sporemind.yaml `backend:`); the app contract
// does not move. (webdav and the other network backends register through
// the same registry as they land — B4/B5/B6/B8.)
func TestStateHandlerBackendSwitch(t *testing.T) {
	for _, backend := range []persist.BackendType{persist.BackendFS, persist.BackendLevelDB} {
		t.Run(string(backend), func(t *testing.T) {
			store, err := persist.New(persist.PersistConfig{
				Backend: backend,
				DataDir: t.TempDir(),
				Prefix:  appStatePrefix,
			})
			if err != nil {
				t.Fatalf("persist.New(%s): %v", backend, err)
			}
			if c, ok := store.(interface{ Close() error }); ok {
				t.Cleanup(func() { _ = c.Close() })
			}

			a := &Actor{appStateStore: store}
			exerciseAppStateSurface(t, a)
		})
	}
}

func exerciseAppStateSurface(t *testing.T, a *Actor) {
	t.Helper()

	if _, err := a.handleStateSet(nil, gen.PluginStateSetReq{Plugin: "app.x", Key: "cfg/a", Value: []byte("42")}); err != nil {
		t.Fatalf("set: %v", err)
	}
	resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "app.x", Key: "cfg/a"})
	if err != nil || !resp.Found || string(resp.Value) != "42" {
		t.Fatalf("get = %v %q (err %v)", resp.Found, resp.Value, err)
	}

	list, err := a.handleStateList(nil, gen.PluginStateListReq{Plugin: "app.x"})
	if err != nil || len(list.Keys) != 1 || list.Keys[0] != "cfg/a" {
		t.Fatalf("list = %v (err %v)", list.Keys, err)
	}

	if _, err := a.handleStateAppend(nil, gen.PluginStateAppendReq{Plugin: "app.x", Key: "logs/e.jsonl", Data: []byte("{\"n\":1}\n")}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if backend, ok := a.appStateStore.(persist.BasePather); ok {
		data, err := os.ReadFile(filepath.Join(backend.BasePath(), "app.x", "logs", "e.jsonl"))
		if err != nil || string(data) != "{\"n\":1}\n" {
			t.Fatalf("append log = %q (err %v)", data, err)
		}
	}

	if _, err := a.handleStatePurge(nil, gen.PluginStatePurgeReq{PluginID: "app.x"}); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if resp, err := a.handleStateGet(nil, gen.PluginStateGetReq{Plugin: "app.x", Key: "cfg/a"}); err != nil || resp.Found {
		t.Fatalf("get after purge: found=%v err=%v", resp.Found, err)
	}
}
