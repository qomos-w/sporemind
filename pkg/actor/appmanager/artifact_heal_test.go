package appmanager

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// nativeHealFixture builds an appmanager actor plus one native app record
// whose inventory state mirrors a zip install: a content-addressed artifact
// file on disk and a package zip in the packages store, both recorded on the
// record (PackagePath / ArtifactPath / ArtifactHash). Returns the actor, the
// record, and both on-disk paths.
func nativeHealFixture(t *testing.T, appID, artifactContent string) (*Actor, appRecord, string, string) {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	manifest := nativeManifestForTest(appID)
	abi := inprocessAbiForTest()
	artifact := []byte(artifactContent)

	// Build a valid native package zip (manifest + abi.json + artifact.so +
	// the entry module) exactly like an exported/install_local package.
	zipBytes := buildLocalZip(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, manifest),
		"abi.json":          mustJSONBytes(t, abi),
		"main.gen.go":       []byte("package main\n"),
		"plugin.so":         artifact,
	})
	pkgPath, err := storePackageZip(appID, zipBytes)
	if err != nil {
		t.Fatalf("store package zip: %v", err)
	}
	artPath, err := storeNativeArtifact(appID, sha256Hex(artifact), "plugin.so", artifact)
	if err != nil {
		t.Fatalf("store native artifact: %v", err)
	}
	rec := appRecord{
		Manifest:     manifest,
		PackageHash:  sha256Hex(zipBytes),
		PackagePath:  pkgPath,
		ArtifactPath: artPath,
		ArtifactHash: sha256Hex(artifact),
		Abi:          abi,
		State:        stateRunning,
		ActorID:      pluginhostServiceName,
	}
	a := &Actor{
		actorID:           "appmanager-heal-test",
		store:             persist.NewFSPersist(t.TempDir()),
		bindings:          appbinding.NewRegistry(),
		Apps:              map[string]gen.AppManifest{manifest.ID: manifest},
		Records:           map[string]appRecord{manifest.ID: rec},
		children:          map[string]string{manifest.ID: pluginhostServiceName},
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	return a, rec, pkgPath, artPath
}

func mustJSONBytes(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestHealMissingArtifactRestoresFromPackageZip: deleting the artifact file
// (external wipe of the artifacts store) leaves the record untouched while
// healMissingArtifacts re-extracts the bytes from the surviving package zip
// at the recorded content-addressed path.
func TestHealMissingArtifactRestoresFromPackageZip(t *testing.T) {
	a, rec, pkgPath, artPath := nativeHealFixture(t, "heal.unit.restore", "heal-unit-v1")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	if err := os.Remove(artPath); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}
	if _, err := os.Stat(pkgPath); err != nil {
		t.Fatalf("package zip %q vanished: %v", pkgPath, err)
	}

	a.healMissingArtifacts(ctx)

	data, err := os.ReadFile(artPath)
	if err != nil {
		t.Fatalf("healed artifact unreadable: %v", err)
	}
	if string(data) != "heal-unit-v1" {
		t.Fatalf("healed artifact content = %q, want %q", data, "heal-unit-v1")
	}
	if got := sha256Hex(data); !strings.EqualFold(got, rec.ArtifactHash) {
		t.Fatalf("healed artifact hash = %s, want recorded %s", got, rec.ArtifactHash)
	}
	// The heal is pure file restoration: the record is untouched.
	got := a.Records[rec.Manifest.ID]
	if got.PackagePath != rec.PackagePath || got.ArtifactPath != rec.ArtifactPath || got.State != rec.State {
		t.Fatalf("record mutated by heal: got %+v, want %+v", got, rec)
	}
}

// TestHealMissingArtifactZipGoneKeepsRecord: when the package zip is gone
// together with the artifact (whole inventory wiped), the heal is impossible
// — it must not create anything, must not mutate the record, and must report
// the not-exist condition (the caller logs it as "no self-heal possible").
func TestHealMissingArtifactZipGoneKeepsRecord(t *testing.T) {
	a, rec, pkgPath, artPath := nativeHealFixture(t, "heal.unit.zipgone", "heal-unit-v2")

	if err := os.Remove(artPath); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}
	if err := os.Remove(pkgPath); err != nil {
		t.Fatalf("delete package zip: %v", err)
	}

	err := a.healArtifactFromPackage(a.Records[rec.Manifest.ID])
	if err == nil {
		t.Fatal("healArtifactFromPackage = nil error, want not-exist")
	}
	if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "package zip missing (no self-heal possible)") {
		t.Fatalf("heal error = %v, want explicit zip-missing (no self-heal possible)", err)
	}
	if _, statErr := os.Stat(artPath); !os.IsNotExist(statErr) {
		t.Fatalf("heal created an artifact despite the missing zip (stat err=%v)", statErr)
	}
	if got := a.Records[rec.Manifest.ID]; got.ArtifactPath != rec.ArtifactPath || got.State != rec.State {
		t.Fatalf("record mutated by failed heal: %+v", got)
	}
}

// TestHealMissingArtifactHashMismatchRefused: a package zip whose artifact
// bytes do not reproduce the recorded hash must be refused — the zip does
// not belong to this record, so rewriting the artifact from it would corrupt
// the app. The file stays absent and the record is left for the failure path.
func TestHealMissingArtifactHashMismatchRefused(t *testing.T) {
	a, rec, pkgPath, artPath := nativeHealFixture(t, "heal.unit.mismatch", "heal-unit-v3")
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// Swap the stored package for one carrying different artifact bytes. The
	// record still points at this path, but the extracted artifact no longer
	// reproduces the recorded hash.
	badZip := buildLocalZip(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, rec.Manifest),
		"abi.json":          mustJSONBytes(t, rec.Abi),
		"main.gen.go":       []byte("package main\n"),
		"plugin.so":         []byte("tampered-artifact"),
	})
	if err := os.WriteFile(pkgPath, badZip, 0o644); err != nil {
		t.Fatalf("rewrite package zip: %v", err)
	}
	if err := os.Remove(artPath); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}

	a.healMissingArtifacts(ctx)

	if _, statErr := os.Stat(artPath); !os.IsNotExist(statErr) {
		t.Fatalf("heal wrote a mismatched artifact (stat err=%v); hash mismatch must refuse", statErr)
	}
	if got := a.Records[rec.Manifest.ID]; got.ArtifactPath != rec.ArtifactPath || got.State != rec.State {
		t.Fatalf("record mutated by refused heal: %+v", got)
	}
}

// TestHealSkipsPresentArtifactAndNonNative: the heal sweep only touches
// native records whose artifact file is actually missing; present artifacts
// and non-native (spore) records pass through untouched.
func TestHealSkipsPresentArtifactAndNonNative(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	ctx := testutil.HumanCtx(testutil.GenActorID())

	present := nativeManifestForTest("heal.unit.present")
	presentAbi := inprocessAbiForTest()
	presentArt := []byte("present")
	presPath, err := storeNativeArtifact("heal.unit.present", sha256Hex(presentArt), "plugin.so", presentArt)
	if err != nil {
		t.Fatalf("store artifact: %v", err)
	}
	sporeManifest := minimalSporeManifest("heal.unit.spore")

	a := &Actor{
		actorID: "appmanager-heal-test",
		store:   persist.NewFSPersist(t.TempDir()),
		Apps: map[string]gen.AppManifest{
			"heal.unit.present": present,
			"heal.unit.spore":   sporeManifest,
		},
		Records: map[string]appRecord{
			"heal.unit.present": {Manifest: present, PackagePath: filepath.Join(inventoryPackagesDir(), "x.zip"), ArtifactPath: presPath, ArtifactHash: sha256Hex(presentArt), Abi: presentAbi, State: stateRunning, ActorID: pluginhostServiceName},
			"heal.unit.spore":   {Manifest: sporeManifest, State: stateRunning, ActorID: "child-1"},
		},
		children:          map[string]string{},
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}

	a.healMissingArtifacts(ctx)

	data, err := os.ReadFile(presPath)
	if err != nil {
		t.Fatalf("present artifact unreadable after heal: %v", err)
	}
	if string(data) != "present" {
		t.Fatalf("present artifact rewritten by heal: %q", data)
	}
	if got := a.Records["heal.unit.spore"]; got.State != stateRunning {
		t.Fatalf("spore record touched by heal: %+v", got)
	}
}
