package codegen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVendorStampLifecycle covers the digest/stamp pair that powers the
// vendor_freshness gate: vendoring writes a stamp matching SDKDigest, drift
// (host file changed) flips VendorSDKIsStale, and re-vendor heals it. Also
// pins the exclusions — examples/ and dot entries never affect the digest,
// so cosmetic host-tree noise cannot produce false drift.
func TestVendorStampLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("vendor test copies the real SDK tree")
	}
	appDir := t.TempDir()
	sdk := testSDKPath(t)

	if _, err := VendorSDKIntoAppDir(appDir, sdk); err != nil {
		t.Fatalf("VendorSDKIntoAppDir: %v", err)
	}
	digest, err := SDKDigest(sdk)
	if err != nil {
		t.Fatalf("SDKDigest: %v", err)
	}
	if got := ReadVendorStamp(appDir); got != digest {
		t.Fatalf("stamp = %s, want digest %s", got, digest)
	}
	stale, err := VendorSDKIsStale(appDir, sdk)
	if err != nil || stale {
		t.Fatalf("fresh vendor reported stale=%v err=%v", stale, err)
	}

	// Simulate an upgraded host SDK: changing one file changes the digest.
	scratch := t.TempDir()
	if err := copyTree(scratch, sdk); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(scratch, "host.go")
	if err := os.WriteFile(marker, []byte("package sdk\n// upgraded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err = VendorSDKIsStale(appDir, scratch)
	if err != nil {
		t.Fatalf("VendorSDKIsStale(scratch): %v", err)
	}
	if !stale {
		t.Fatal("changed host SDK must be detected as stale")
	}
	// Excluded noise (a new dot file) must not flip freshness either way.
	if err := os.WriteFile(filepath.Join(scratch, ".gitignore"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if stale, err = VendorSDKIsStale(appDir, scratch); err != nil || !stale {
		t.Fatalf("dot file must not mask drift: stale=%v err=%v", stale, err)
	}

	// Re-vendor from the scratch tree heals the drift.
	if _, err := VendorSDKIntoAppDir(appDir, scratch); err != nil {
		t.Fatalf("re-vendor: %v", err)
	}
	if got := ReadVendorStamp(appDir); got == digest {
		t.Fatal("re-vendor stamp must reflect the upgraded source")
	}
	if stale, err = VendorSDKIsStale(appDir, scratch); err != nil || stale {
		t.Fatalf("re-vendored tree still stale=%v err=%v", stale, err)
	}
}

// copyTree copies src into dst preserving relative layout (test helper).
func copyTree(dst, src string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// TestVendorSDKMissingStampNotStale pins the legacy policy: a vendored tree
// without a stamp (pre-stamp vendor) is never judged stale.
func TestVendorSDKMissingStampNotStale(t *testing.T) {
	if testing.Short() {
		t.Skip("vendor test copies the real SDK tree")
	}
	appDir := t.TempDir()
	if _, err := VendorSDKIntoAppDir(appDir, testSDKPath(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(appDir, VendorSDKDir, VendorStampFileName)); err != nil {
		t.Fatal(err)
	}
	stale, err := VendorSDKIsStale(appDir, testSDKPath(t))
	if err != nil {
		t.Fatalf("VendorSDKIsStale: %v", err)
	}
	if stale {
		t.Fatal("missing stamp must not report stale")
	}
}

func TestVendorSDKIntoAppDirCopiesModuleAndExcludesExamples(t *testing.T) {
	if testing.Short() {
		t.Skip("vendor test copies the real SDK tree")
	}
	appDir := t.TempDir()
	sdk := testSDKPath(t)

	vendored, err := VendorSDKIntoAppDir(appDir, sdk)
	if err != nil {
		t.Fatalf("VendorSDKIntoAppDir: %v", err)
	}
	if vendored != filepath.Join(appDir, VendorSDKDir) {
		t.Fatalf("vendored path = %s, want %s", vendored, filepath.Join(appDir, VendorSDKDir))
	}

	// Module markers must exist; the examples module must not.
	for _, name := range []string{FileGoMod, "plugin.go", "host.go"} {
		if _, err := os.Stat(filepath.Join(vendored, name)); err != nil {
			t.Errorf("vendored SDK missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(vendored, "examples")); !os.IsNotExist(err) {
		t.Errorf("vendored SDK must exclude examples/, stat err = %v", err)
	}
}

func TestVendorSDKRewritesHostPathReplace(t *testing.T) {
	appDir := t.TempDir()
	goMod := "module com.example.app\n\ngo 1.24\n\nrequire github.com/qomos-w/sporemind-plugin-sdk v0.0.0\n\nreplace github.com/qomos-w/sporemind-plugin-sdk => F:/dev/sporemind/sporemind-plugin-sdk\n"
	if err := os.WriteFile(filepath.Join(appDir, FileGoMod), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := VendorSDKIntoAppDir(appDir, testSDKPath(t)); err != nil {
		t.Fatalf("VendorSDKIntoAppDir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(appDir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "F:/dev/sporemind/sporemind-plugin-sdk") {
		t.Errorf("host path leaked into go.mod:\n%s", data)
	}
	if !strings.Contains(string(data), "replace "+sdkModuleName+" => "+VendoredSDKReplace) {
		t.Errorf("go.mod missing vendored replace:\n%s", data)
	}
}

// TestGenerateTemplateVendorsSDK pins the round-7 portability fix: the
// scaffold must be self-contained (relative ./vendor-sdk replace, SDK copy on
// disk) so user machines without a sporemind checkout can build the app.
func TestGenerateTemplateVendorsSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("template test copies the real SDK tree")
	}
	dir := t.TempDir()

	result, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.SDKPath != filepath.Join(dir, VendorSDKDir) {
		t.Errorf("template SDKPath = %s, want vendored %s", result.SDKPath, filepath.Join(dir, VendorSDKDir))
	}
	data, err := os.ReadFile(filepath.Join(dir, FileGoMod))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "=> "+VendoredSDKReplace) {
		t.Errorf("template go.mod missing vendored replace:\n%s", data)
	}
	if strings.Contains(string(data), testSDKPath(t)) {
		t.Errorf("template go.mod leaks host SDK path:\n%s", data)
	}
	if _, err := os.Stat(filepath.Join(dir, VendorSDKDir, FileGoMod)); err != nil {
		t.Errorf("template vendor-sdk missing go.mod: %v", err)
	}
}

// TestGenerateRematerializesVendoredSDK: a committed vendored app carries only
// the relative replace; regenerate on any host restores the missing tree.
func TestGenerateRematerializesVendoredSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("template test copies the real SDK tree")
	}
	dir := t.TempDir()
	if _, err := Generate(dir, Options{SDKPath: testSDKPath(t), Template: true}); err != nil {
		t.Fatalf("Generate template: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, VendorSDKDir)); err != nil {
		t.Fatal(err)
	}

	// Regenerate from the existing .appdef (non-template path) with the host
	// SDK as source: the vendored tree must be restored. The reported SDKPath
	// must follow the vendored replace (round-8 #6: it used to echo the
	// host-resolved path, which may not exist at all on this machine layout).
	res, err := Generate(dir, Options{SDKPath: testSDKPath(t)})
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, VendorSDKDir, FileGoMod)); err != nil {
		t.Errorf("vendor-sdk not rematerialized: %v", err)
	}
	if !GoModUsesVendoredSDK(dir) {
		t.Error("regenerated go.mod lost the vendored replace")
	}
	if want := filepath.Join(dir, VendorSDKDir); res.SDKPath != want {
		t.Errorf("SDKPath = %q, want vendored %q", res.SDKPath, want)
	}
}

// TestVendorStampGenSetSummary verifies that vendoring writes a gen-set
// summary line to the stamp, recording the slimmed gen/ directory contents.
func TestVendorStampGenSetSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("vendor test copies the real SDK tree")
	}
	appDir := t.TempDir()
	sdk := testSDKPath(t)

	if _, err := VendorSDKIntoAppDir(appDir, sdk); err != nil {
		t.Fatalf("VendorSDKIntoAppDir: %v", err)
	}

	genSet := ReadVendorStampGenSet(appDir)
	if genSet == "" {
		t.Fatal("expected gen-set summary in vendor stamp")
	}
	if !strings.HasPrefix(genSet, "gen-set:") {
		t.Errorf("gen-set line must start with 'gen-set:', got %q", genSet)
	}
	// The summary must mention the gen files listed in sdk_gen_deps.json.
	depsData, err := os.ReadFile(filepath.Join(sdk, SDKGenDepsFileName))
	if err != nil {
		t.Fatalf("read sdk_gen_deps.json: %v", err)
	}
	var deps struct {
		GenFiles []string `json:"gen_files"`
	}
	if err := json.Unmarshal(depsData, &deps); err != nil {
		t.Fatalf("parse sdk_gen_deps.json: %v", err)
	}
	for _, f := range deps.GenFiles {
		if !strings.Contains(genSet, f) {
			t.Errorf("gen-set summary %q must reference gen file %q", genSet, f)
		}
	}

	// The digest line must still match SDKDigest (backward compatible).
	digest, err := SDKDigest(sdk)
	if err != nil {
		t.Fatalf("SDKDigest: %v", err)
	}
	if got := ReadVendorStamp(appDir); got != digest {
		t.Errorf("ReadVendorStamp = %s, want digest %s", got, digest)
	}
}
