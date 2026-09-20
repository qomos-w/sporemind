package appmanager

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func canonicalHashFixture(t *testing.T, manifest gen.AppManifest, entry string, modules map[string]string, abi *gen.PluginAbi, artifactHash string) string {
	t.Helper()
	hash, err := canonicalPackageHash(manifest, entry, modules, nil, nil, abi, artifactHash)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestCanonicalPackageHashStableAndSensitiveToSecurityInputs(t *testing.T) {
	base := gen.AppManifest{
		ID: "app.hash", Name: "Hash", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Namespace: "app.hash",
		Permissions: []string{"state"}, Dependencies: []gen.AppDependency{{ID: "dep", Version: "1.0.0", Hash: "abc12345"}},
		Security: &gen.AppSecurityPolicy{AllowCapabilities: []string{"state"}, MaxInstructions: 100},
	}
	modulesA := map[string]string{"b.spore": "b", "a.spore": "a"}
	modulesB := map[string]string{"a.spore": "a", "b.spore": "b"}
	baseline := canonicalHashFixture(t, base, "a.spore", modulesA, nil, "")
	if got := canonicalHashFixture(t, base, "a.spore", modulesB, nil, ""); got != baseline {
		t.Fatalf("map order changed hash: %s != %s", got, baseline)
	}

	assertChanged := func(name string, manifest gen.AppManifest, entry string, modules map[string]string, abi *gen.PluginAbi, artifact string) {
		t.Helper()
		if got := canonicalHashFixture(t, manifest, entry, modules, abi, artifact); got == baseline {
			t.Fatalf("%s did not change canonical hash", name)
		}
	}
	changed := base
	changed.Permissions = []string{"filesystem"}
	assertChanged("permissions", changed, "a.spore", modulesA, nil, "")
	changed = base
	changed.Dependencies = []gen.AppDependency{{ID: "dep", Version: "2.0.0", Hash: "abc12345"}}
	assertChanged("dependencies", changed, "a.spore", modulesA, nil, "")
	changed = base
	changed.Security = &gen.AppSecurityPolicy{AllowCapabilities: []string{"state"}, MaxInstructions: 101}
	assertChanged("security budget", changed, "a.spore", modulesA, nil, "")
	assertChanged("entry module", base, "b.spore", modulesA, nil, "")
	assertChanged("module content", base, "a.spore", map[string]string{"a.spore": "changed", "b.spore": "b"}, nil, "")
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "inprocess", TrustClass: "first_party", Signer: "first-party"}
	assertChanged("ABI", base, "a.spore", modulesA, abi, "")
	assertChanged("artifact", base, "a.spore", modulesA, nil, "deadbeef")
	descriptorsA := map[string]gen.AppObjectDescriptor{"Payload": {Kind: "struct", Name: "Payload", SchemaID: 9001}}
	descriptorsB := map[string]gen.AppObjectDescriptor{"Payload": {Kind: "struct", Name: "Payload", SchemaID: 9002}}
	withDescriptorsA, err := canonicalPackageHash(base, "a.spore", modulesA, nil, descriptorsA, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	withDescriptorsB, err := canonicalPackageHash(base, "a.spore", modulesA, nil, descriptorsB, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if withDescriptorsA == withDescriptorsB {
		t.Fatal("schema descriptor change did not change canonical hash")
	}
	assetsA := map[string][]byte{"icons/b.png": {2}, "icons/a.png": {1}}
	assetsB := map[string][]byte{"icons/a.png": {1}, "icons/b.png": {2}}
	withAssets, err := canonicalPackageHash(base, "a.spore", modulesA, assetsA, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	withAssetsReordered, err := canonicalPackageHash(base, "a.spore", modulesA, assetsB, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if withAssets != withAssetsReordered {
		t.Fatal("asset map order changed canonical hash")
	}
	changedAssets, err := canonicalPackageHash(base, "a.spore", modulesA, map[string][]byte{"icons/a.png": {9}, "icons/b.png": {2}}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if changedAssets == withAssets || withAssets == baseline {
		t.Fatal("asset bytes were not included in canonical hash")
	}
}

func TestPackageHashVersionPersistsAcrossRestart(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	a := &Actor{store: store, actorID: "appmanager-hash", Apps: map[string]gen.AppManifest{"app.hash": {ID: "app.hash"}}, Records: map[string]appRecord{"app.hash": {PackageHash: "hash", PackageHashVersion: canonicalPackageHashVersion, Assets: map[string][]byte{"icon.png": {1, 2, 3}}}}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	restored := &Actor{store: store, actorID: "appmanager-hash"}
	if err := restored.Load(); err != nil {
		t.Fatal(err)
	}
	if got := restored.Records["app.hash"].PackageHashVersion; got != canonicalPackageHashVersion {
		t.Fatalf("hash version after restart = %q", got)
	}
	if got := restored.Records["app.hash"].Assets["icon.png"]; len(got) != 3 || got[2] != 3 {
		t.Fatalf("assets after restart = %v", got)
	}
}

// TestPackagePathPersistsAcrossRestart: the inventory PackagePath pointer is
// part of the app record and must survive a Save/Load round trip (restart),
// so a zip-installed app keeps pointing at its stored package.
func TestPackagePathPersistsAcrossRestart(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	const pkgPath = "/data/.actors/appmanager/packages/app.zip.pkg/app.hash-deadbeef.zip"
	a := &Actor{store: store, actorID: "appmanager-pkgpath", Apps: map[string]gen.AppManifest{"app.hash": {ID: "app.hash"}}, Records: map[string]appRecord{"app.hash": {PackageHash: "deadbeef", PackagePath: pkgPath}}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	restored := &Actor{store: store, actorID: "appmanager-pkgpath"}
	if err := restored.Load(); err != nil {
		t.Fatal(err)
	}
	if got := restored.Records["app.hash"].PackagePath; got != pkgPath {
		t.Fatalf("PackagePath after restart = %q, want %q", got, pkgPath)
	}
}
