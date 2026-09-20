package appmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// memPersist is a Persist implementation WITHOUT BasePather: it simulates
// non-filesystem backends and pins the inline-fallback path.
type memPersist struct {
	docs map[string]json.RawMessage
}

func newMemPersist() *memPersist { return &memPersist{docs: map[string]json.RawMessage{}} }

func (m *memPersist) Load(name string, v any) error {
	raw, ok := m.docs[name]
	if !ok {
		return persist.ErrNotExist
	}
	return json.Unmarshal(raw, v)
}
func (m *memPersist) Save(name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.docs[name] = raw
	return nil
}
func (m *memPersist) Delete(name string) error {
	delete(m.docs, name)
	return nil
}

func assetTestActor(t *testing.T, dir string) *Actor {
	t.Helper()
	ps := persist.NewFSPersist(dir)
	aid := testutil.GenActorID()
	a := &Actor{
		actorID:  aid.String(),
		store:    ps,
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
	return a
}

func assetManifest(id string) gen.AppManifest {
	return gen.AppManifest{ID: id, Name: id, Version: "1.0.0", Runtime: "native", Namespace: "ns." + id}
}

// TestAssetsRoundTripThroughSideStore pins the state-document diet: after
// Save, the persisted JSON carries assetRefs only — no base64 blobs — and
// the blobs live content-addressed under <base>/<actorID>/assets/. A Load
// rehydrates Assets byte-exact from those refs.
func TestAssetsRoundTripThroughSideStore(t *testing.T) {
	dir := t.TempDir()
	a := assetTestActor(t, dir)
	manifest := assetManifest("app.assets")
	assets := map[string][]byte{
		"index.html":  []byte("<html>hello</html>"),
		"icons/a.png": {1, 2, 3},
	}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = appRecord{Manifest: manifest, State: "running", Assets: assets}

	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, a.actorID+".json"))
	if err != nil {
		t.Fatalf("read state doc: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode state doc: %v", err)
	}
	rec := doc["records"].(map[string]any)["app.assets"].(map[string]any)
	if _, inline := rec["assets"]; inline {
		t.Fatalf("state document still carries inline assets (%d bytes)", len(raw))
	}
	refs, _ := rec["assetRefs"].(map[string]any)
	if len(refs) != 2 {
		t.Fatalf("expected 2 assetRefs, got %v", refs)
	}
	if len(raw) > 4096 {
		t.Fatalf("state document unexpectedly large: %d bytes", len(raw))
	}

	blobDir := filepath.Join(dir, a.actorID, "assets", "app.assets")
	entries, err := os.ReadDir(blobDir)
	if err != nil {
		t.Fatalf("read side store: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 blobs, got %d", len(entries))
	}

	restored := &Actor{actorID: a.actorID, store: persist.NewFSPersist(dir), bindings: appbinding.NewRegistry()}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := restored.Records["app.assets"].Assets
	if len(got) != 2 || string(got["index.html"]) != "<html>hello</html>" || string(got["icons/a.png"]) != string([]byte{1, 2, 3}) {
		t.Fatalf("assets not rehydrated byte-exact: %+v", got)
	}
}

// TestAssetsInlineFallbackWithoutBasePather pins the graceful degradation:
// a backend that cannot host the side store keeps the legacy inline form and
// the round trip still works.
func TestAssetsInlineFallbackWithoutBasePather(t *testing.T) {
	store := newMemPersist()
	a := &Actor{
		actorID:  "inline-fallback",
		store:    store,
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
	manifest := assetManifest("app.inline")
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = appRecord{Manifest: manifest, State: "running", Assets: map[string][]byte{"index.html": []byte("x")}}

	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.Contains(string(store.docs["inline-fallback"]), `"assets"`) {
		t.Fatal("inline fallback expected assets in the document")
	}

	restored := &Actor{actorID: a.actorID, store: store, bindings: appbinding.NewRegistry()}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if string(restored.Records["app.inline"].Assets["index.html"]) != "x" {
		t.Fatalf("inline round trip broken: %+v", restored.Records["app.inline"].Assets)
	}
}

// TestPendingReloadCandidateAssetsRoundTrip pins the crash-recovery asset
// path: the pendingReload marker's CandidateAssets must survive the
// side-store round trip, because recoverPendingReloads re-prepares the
// candidate with them.
func TestPendingReloadCandidateAssetsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := assetTestActor(t, dir)
	manifest := assetManifest("app.pending")
	oldManifest := manifest
	oldManifest.Version = "0.9.0"
	a.Apps[manifest.ID] = oldManifest
	a.Records[manifest.ID] = appRecord{Manifest: oldManifest, State: "running"}
	a.PendingReloads = map[string]pendingReload{
		manifest.ID: {
			CandidateManifest: manifest,
			OldManifest:       oldManifest,
			OldRecord:         appRecord{Manifest: oldManifest, State: "running"},
			CandidateAssets:   map[string][]byte{"index.html": []byte("candidate")},
		},
	}

	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, a.actorID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"candidate"`) {
		t.Fatal("pending marker still carries inline CandidateAssets")
	}

	restored := &Actor{actorID: a.actorID, store: persist.NewFSPersist(dir), bindings: appbinding.NewRegistry()}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	pr, ok := restored.PendingReloads["app.pending"]
	if !ok {
		t.Fatal("pending marker lost")
	}
	if string(pr.CandidateAssets["index.html"]) != "candidate" {
		t.Fatalf("CandidateAssets not rehydrated: %+v", pr.CandidateAssets)
	}
}

// TestRemoveAppAssetDirReclaimsSideStore pins the unregister hook: the app's
// blob directory disappears while other apps' blobs stay.
func TestRemoveAppAssetDirReclaimsSideStore(t *testing.T) {
	dir := t.TempDir()
	a := assetTestActor(t, dir)
	for _, appID := range []string{"app.one", "app.two"} {
		m := assetManifest(appID)
		a.Apps[appID] = m
		a.Records[appID] = appRecord{Manifest: m, State: "running", Assets: map[string][]byte{"index.html": []byte(appID)}}
	}
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	a.removeAppAssetDir("app.one")
	base := filepath.Join(dir, a.actorID, "assets")
	if _, err := os.Stat(filepath.Join(base, "app.one")); !os.IsNotExist(err) {
		t.Fatal("app.one asset dir not reclaimed")
	}
	if _, err := os.Stat(filepath.Join(base, "app.two")); err != nil {
		t.Fatalf("app.two asset dir must survive: %v", err)
	}
}

// TestLegacyInlineAssetsMigrateOnNextSave pins forward migration: a legacy
// state document with inline Assets loads unchanged and migrates to the
// side store on the next Save.
func TestLegacyInlineAssetsMigrateOnNextSave(t *testing.T) {
	dir := t.TempDir()
	a := assetTestActor(t, dir)
	manifest := assetManifest("app.legacy")

	// Seed the fs store with a legacy inline document directly.
	inlineDoc := map[string]any{
		"apps": map[string]any{manifest.ID: manifest},
		"records": map[string]any{manifest.ID: map[string]any{
			"manifest": manifest, "state": "running",
			"assets": map[string]any{"index.html": "bGVnYWN5"},
		}},
	}
	rawDoc, _ := json.Marshal(inlineDoc)
	if err := os.WriteFile(filepath.Join(dir, a.actorID+".json"), rawDoc, 0o644); err != nil {
		t.Fatal(err)
	}

	restored := &Actor{actorID: a.actorID, store: persist.NewFSPersist(dir), bindings: appbinding.NewRegistry()}
	if err := restored.Load(); err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	if string(restored.Records["app.legacy"].Assets["index.html"]) != "legacy" {
		t.Fatalf("legacy inline assets must load as-is: %+v", restored.Records["app.legacy"].Assets)
	}
	if err := restored.Save(); err != nil {
		t.Fatalf("migrating save: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, a.actorID+".json"))
	if strings.Contains(string(raw), "bGVnYWN5") {
		t.Fatal("save did not migrate inline assets to the side store")
	}
}
