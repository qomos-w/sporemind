package appmanager

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// legacyBuiltinManifests returns the two manifests left behind by the
// 2026-08-19 builtinapp removal: they persisted with Runtime "builtin", which
// register no longer accepts and spawnChild rejects as unsupported.
func legacyBuiltinManifests() (browser, ssh gen.AppManifest) {
	browser = gen.AppManifest{
		ID:        "builtin.browser",
		Name:      "Browser",
		Namespace: "builtin",
		Version:   "1.0.0",
		Runtime:   "builtin",
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view", Title: "Browser"},
		},
	}
	ssh = gen.AppManifest{
		ID:        "builtin.ssh",
		Name:      "SSH",
		Namespace: "builtin",
		Version:   "1.0.0",
		Runtime:   "builtin",
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view", Title: "SSH"},
		},
	}
	return browser, ssh
}

// TestOnInitPurgesLegacyBuiltinappRecords verifies that the OnInit restore
// loop drops persisted records whose Runtime is neither spore nor native
// (the builtin.browser / builtin.ssh leftovers from the builtinapp removal)
// while leaving healthy native/spore records fully restored, so
// appmanager.list shows no failed ghost entries.
func TestOnInitPurgesLegacyBuiltinappRecords(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	builtinBrowser, builtinSSH := legacyBuiltinManifests()
	sporecallManifest := gen.AppManifest{
		ID:        "builtin.sporecall",
		Name:      "Sporecall",
		Namespace: "sporeapp.builtin.sporecall",
		Version:   "1.0.0",
		Runtime:   "spore",
	}
	nativeManifest := gen.AppManifest{
		ID:        "native.tool",
		Name:      "Tool",
		Namespace: "sporeapp.tool",
		Version:   "1.0.0",
		Runtime:   "native",
	}
	sporeManifest := gen.AppManifest{
		ID:              "spore.app",
		Name:            "Spore",
		Namespace:       "sporeapp.demo",
		Version:         "1.2.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{
			{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"},
		},
	}

	// Pre-migration persisted state: two legacy builtin records, the retired
	// builtin.sporecall spore app, plus one healthy native and one healthy
	// spore record.
	pre := &Actor{
		Apps: map[string]gen.AppManifest{
			"builtin.browser":   builtinBrowser,
			"builtin.ssh":       builtinSSH,
			"builtin.sporecall": sporecallManifest,
			"native.tool":       nativeManifest,
			"spore.app":         sporeManifest,
		},
		Records: map[string]appRecord{
			"builtin.browser":   {Manifest: builtinBrowser, State: "running", PackageHash: "h-browser"},
			"builtin.ssh":       {Manifest: builtinSSH, State: "running", PackageHash: "h-ssh"},
			"builtin.sporecall": {Manifest: sporecallManifest, State: "running", PackageHash: "h-sporecall"},
			"native.tool":       {Manifest: nativeManifest, State: "running", PackageHash: "h-native", ArtifactPath: "a.so", ArtifactHash: "ah", Abi: &gen.PluginAbi{}},
			"spore.app":         {Manifest: sporeManifest, State: "running", PackageHash: "h-spore", EntryModule: "main", Modules: map[string]string{"main": "export fun answer(): int = 42"}},
		},
	}
	pre.actorID = testutil.GenActorID().String()
	pre.store = ps
	if err := pre.Save(); err != nil {
		t.Fatalf("save pre-migration state: %v", err)
	}

	// Restart: OnInit restores the persisted state and must purge the legacy
	// records without failing.
	a := &Actor{store: ps}
	if err := a.OnInit(testutil.HumanCtx(testutil.GenActorID())); err != nil {
		t.Fatalf("OnInit: %v", err)
	}

	for _, id := range []string{"builtin.browser", "builtin.ssh", "builtin.sporecall"} {
		if _, ok := a.Apps[id]; ok {
			t.Errorf("legacy app %q still present in Apps after restore", id)
		}
		if _, ok := a.Records[id]; ok {
			t.Errorf("legacy record %q still present in Records after restore", id)
		}
		if _, ok := a.children[id]; ok {
			t.Errorf("legacy child %q still present in children after restore", id)
		}
	}

	// Healthy records restore untouched.
	if len(a.Apps) != 2 || len(a.Records) != 2 {
		t.Fatalf("expected exactly the 2 healthy records after purge, got apps=%d records=%d", len(a.Apps), len(a.Records))
	}
	if rec, ok := a.Records["native.tool"]; !ok || rec.State != "running" || rec.PackageHash != "h-native" {
		t.Errorf("native.tool record not restored intact: %+v", rec)
	}
	if rec, ok := a.Records["spore.app"]; !ok || rec.State != "running" || rec.PackageHash != "h-spore" || rec.Modules["main"] != "export fun answer(): int = 42" {
		t.Errorf("spore.app record not restored intact: %+v", rec)
	}
	if m, ok := a.Apps["spore.app"]; !ok || len(m.Callables) != 1 || m.Callables[0].ID != "answer" {
		t.Errorf("spore.app manifest not restored intact: %+v", m)
	}

	// appmanager.list shows only the healthy apps and no failed ghosts.
	resp, err := a.handleList(nil, gen.AppManagerListReq{})
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 list items after purge, got %d: %+v", len(resp.Items), resp.Items)
	}
	for _, item := range resp.Items {
		if item.ID == "builtin.browser" || item.ID == "builtin.ssh" {
			t.Errorf("legacy app %q still listed", item.ID)
		}
		if item.State == "failed" {
			t.Errorf("failed ghost item in list: %+v", item)
		}
	}

	// The purge is audited.
	audited := map[string]bool{}
	for _, r := range a.AuditRecords {
		if r.Callable == "load_migration" {
			audited[r.AppID] = true
			if !r.Allowed || r.Reason == "" {
				t.Errorf("unexpected migration audit record: %+v", r)
			}
		}
	}
	if !audited["builtin.browser"] || !audited["builtin.ssh"] || !audited["builtin.sporecall"] {
		t.Errorf("migration cleanup not audited for all legacy apps: %+v", audited)
	}

	// The purged state is persisted: a later restart must not see the ghosts.
	after := &Actor{actorID: pre.actorID, store: ps}
	if err := after.Load(); err != nil {
		t.Fatalf("reload purged state: %v", err)
	}
	if _, ok := after.Apps["builtin.browser"]; ok {
		t.Error("builtin.browser still present in persisted Apps after purge")
	}
	if _, ok := after.Records["builtin.ssh"]; ok {
		t.Error("builtin.ssh still present in persisted Records after purge")
	}
	if len(after.Apps) != 2 || len(after.Records) != 2 {
		t.Errorf("persisted state should keep only healthy records, got apps=%d records=%d", len(after.Apps), len(after.Records))
	}
}

// TestOnInitKeepsCleanStateUnpurged verifies that a persisted state without
// legacy records restores exactly as before: no audit noise, no extra saves.
func TestOnInitKeepsCleanStateUnpurged(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	nativeManifest := gen.AppManifest{
		ID: "native.only", Name: "Only", Namespace: "sporeapp.only", Version: "1.0.0", Runtime: "native",
	}
	pre := &Actor{
		Apps:    map[string]gen.AppManifest{"native.only": nativeManifest},
		Records: map[string]appRecord{"native.only": {Manifest: nativeManifest, State: "running", PackageHash: "h"}},
	}
	pre.actorID = testutil.GenActorID().String()
	pre.store = ps
	if err := pre.Save(); err != nil {
		t.Fatalf("save clean state: %v", err)
	}

	a := &Actor{store: ps}
	if err := a.OnInit(testutil.HumanCtx(testutil.GenActorID())); err != nil {
		t.Fatalf("OnInit: %v", err)
	}
	if len(a.Apps) != 1 || len(a.Records) != 1 {
		t.Fatalf("clean state must restore unchanged, got apps=%d records=%d", len(a.Apps), len(a.Records))
	}
	for _, r := range a.AuditRecords {
		if r.Callable == "load_migration" {
			t.Errorf("unexpected migration audit on clean state: %+v", r)
		}
	}
}
