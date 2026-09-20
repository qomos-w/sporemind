package appmanager

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// T1 acceptance tests for the zip package inventory (packages.go):
// install_local must store the install zip under
// <DataDir>/.actors/appmanager/packages/, point the app record's
// PackagePath at it, and treat a same-ID reinstall of a new zip as an
// update: stage first, then after commit GC the old zip/artifact and audit
// the old→new package hash swap. Failed installs must not leave inventory
// files behind and must not touch an existing install.

// inventoryPackagesFor returns the absolute packages dir for the current
// test DataDir (SetDataDirForTest is called by each test via
// configSetupForInstallTest / installLocalTestActor).
func inventoryPackagesFor(t *testing.T) string {
	t.Helper()
	return inventoryPackagesDir()
}

func listInventoryFiles(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// TestInstallLocal_SporeZipStoresInventory: a signed spore zip install lands
// the raw zip bytes in the packages inventory under its content address, the
// record's PackagePath points at the stored file, and reinstalling the same
// zip is idempotent (no new file, no audit churn beyond the running record).
func TestInstallLocal_SporeZipStoresInventory(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.inventory.spore.store"
	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest(appID)
	zipData, _ := signedLocalZip(t, manifest, false)

	resp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData})
	if err != nil {
		t.Fatalf("install_local: %v", err)
	}
	if resp.Status.ID != appID || resp.Status.State != stateRunning {
		t.Fatalf("status = %+v, want running", resp.Status)
	}

	record := a.Records[appID]
	if record.PackagePath == "" {
		t.Fatal("record.PackagePath is empty after zip install")
	}
	pkgDir := inventoryPackagesFor(t)
	if !strings.HasPrefix(record.PackagePath, pkgDir+string(filepath.Separator)) {
		t.Fatalf("PackagePath %q not under packages inventory %q", record.PackagePath, pkgDir)
	}
	stored, err := os.ReadFile(record.PackagePath)
	if err != nil {
		t.Fatalf("stored package zip unreadable: %v", err)
	}
	if string(stored) != string(zipData) {
		t.Fatal("stored zip bytes differ from the installed raw zip (audit chain lost)")
	}

	// Same-ID reinstall of identical bytes: content-addressed no-op.
	zipPath1 := record.PackagePath
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData}); err != nil {
		t.Fatalf("reinstall same zip: %v", err)
	}
	if got := a.Records[appID].PackagePath; got != zipPath1 {
		t.Fatalf("PackagePath changed on identical reinstall: %q -> %q", zipPath1, got)
	}
	files := listInventoryFiles(t, pkgDir, appID+"-")
	if len(files) != 1 {
		t.Fatalf("packages inventory for %q = %v, want exactly 1 file (content-addressed)", appID, files)
	}
}

// TestInstallLocal_UpdateSwapsPackagePointerGCAudits: installing a new zip
// under the same app ID (a version bump) must (a) land the new zip in the
// inventory, (b) switch record.PackagePath to it, (c) delete the old zip
// after commit, and (d) leave an audit trail recording old→new.
func TestInstallLocal_UpdateSwapsPackagePointerGCAudits(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.inventory.spore.update"
	a, ctx := installLocalTestActor(t)

	v1 := minimalSporeManifest(appID)
	v1.Version = "1.0.0"
	v1zip, _ := signedLocalZip(t, v1, false)
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: v1zip}); err != nil {
		t.Fatalf("install v1: %v", err)
	}
	oldRec := a.Records[appID]
	if oldRec.PackagePath == "" {
		t.Fatal("v1 record missing PackagePath")
	}
	oldZipExists := func() bool {
		_, err := os.Stat(oldRec.PackagePath)
		return err == nil
	}
	if !oldZipExists() {
		t.Fatalf("v1 zip %q missing after install", oldRec.PackagePath)
	}

	v2 := minimalSporeManifest(appID)
	v2.Version = "2.0.0"
	v2zip, _ := signedLocalZip(t, v2, false)
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: v2zip}); err != nil {
		t.Fatalf("install v2: %v", err)
	}

	newRec := a.Records[appID]
	if newRec.PackagePath == "" || newRec.PackagePath == oldRec.PackagePath {
		t.Fatalf("PackagePath did not switch on update: old %q new %q", oldRec.PackagePath, newRec.PackagePath)
	}
	if _, err := os.Stat(newRec.PackagePath); err != nil {
		t.Fatalf("new zip %q missing after update: %v", newRec.PackagePath, err)
	}
	if oldZipExists() {
		t.Fatalf("old zip %q was not garbage-collected after the update commit", oldRec.PackagePath)
	}

	// Audit: an install_local record must carry the old→new package hash.
	found := false
	for _, ar := range a.AuditRecords {
		if ar.AppID == appID && ar.Callable == "install_local" && strings.Contains(ar.Reason, oldRec.PackageHash) && strings.Contains(ar.Reason, newRec.PackageHash) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no audit record with old->new package hash swap for %q (audit=%d records)", appID, len(a.AuditRecords))
	}
}

// TestInstallLocal_TamperedZipLeavesInventoryUntouched: a tampered zip that
// fails signature verification must not leave any staged inventory file and
// must not disturb an already-installed package.
func TestInstallLocal_TamperedZipLeavesInventoryUntouched(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.inventory.spore.tamper"
	a, ctx := installLocalTestActor(t)

	good := minimalSporeManifest(appID)
	goodZip, _ := signedLocalZip(t, good, false)
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: goodZip}); err != nil {
		t.Fatalf("install good: %v", err)
	}
	goodRec := a.Records[appID]
	goodFiles := listInventoryFiles(t, inventoryPackagesFor(t), appID+"-")

	// Tamper: change a module byte so signature verification fails.
	bad := minimalSporeManifest(appID)
	badZip, _ := signedLocalZip(t, bad, false)
	badZip = rewriteZipEntries(t, badZip, func(files map[string][]byte) {
		files["main.spore"][0] ^= 0xff
	})
	_, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: badZip})
	if err == nil || !strings.Contains(err.Error(), "PACKAGE.sig verification failed") {
		t.Fatalf("expected verification failure, got: %v", err)
	}

	// The old install is intact, and no new file was staged.
	if _, ok := a.Records[appID]; !ok || a.Records[appID].PackagePath != goodRec.PackagePath {
		t.Fatalf("existing record disturbed by failed install: %+v", a.Records[appID])
	}
	if _, err := os.Stat(goodRec.PackagePath); err != nil {
		t.Fatalf("existing zip %q removed by failed install: %v", goodRec.PackagePath, err)
	}
	after := listInventoryFiles(t, inventoryPackagesFor(t), appID+"-")
	if len(after) != len(goodFiles) {
		t.Fatalf("inventory grew on failed install: before %v after %v", goodFiles, after)
	}
}

// TestInstallLocal_DirectoryStoresUnsignedZip: a directory install has no
// raw zip; install_local must assemble the canonical unsigned zip (the same
// shape buildExportZip produces) and store it, so directory installs keep
// the same update/GC bookkeeping.
func TestInstallLocal_DirectoryStoresUnsignedZip(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.inventory.dir.store"
	a, ctx := installLocalTestActor(t)
	manifest := minimalSporeManifest(appID)
	dir := writeLocalPackageDir(t, map[string][]byte{
		"app.manifest.json": manifestJSON(t, manifest),
		"main.spore":        []byte("export default {}"),
		"assets/icon.png":   []byte("png-bytes"),
	})

	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{Path: dir}); err != nil {
		t.Fatalf("install_local (dir): %v", err)
	}
	record := a.Records[appID]
	if record.PackagePath == "" {
		t.Fatal("directory install left no PackagePath")
	}
	if !strings.HasSuffix(record.PackagePath, ".zip") {
		t.Fatalf("directory install PackagePath %q is not a zip", record.PackagePath)
	}
	stored, err := os.ReadFile(record.PackagePath)
	if err != nil {
		t.Fatalf("read stored dir zip: %v", err)
	}
	// The assembled zip must be a valid zip carrying the manifest and asset.
	zr, err := zip.NewReader(bytes.NewReader(stored), int64(len(stored)))
	if err != nil {
		t.Fatalf("stored dir zip is not a valid archive: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["app.manifest.json"] || !names["assets/icon.png"] {
		t.Fatalf("stored dir zip missing manifest/asset entries: %+v", names)
	}
}

// TestInstallLocal_NativeArtifactInInventory: a native zip install must
// resolve the artifact into the artifacts inventory
// (<DataDir>/.actors/appmanager/artifacts/) instead of an OS temp file, and
// the record's ArtifactPath must point at the stored file.
func TestInstallLocal_NativeArtifactInInventory(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.native.inventory"
	env := newBP7Project(t, "app-export-loop-native-inv-")
	env.write("app.manifest.json", exportLoopNativeManifestJSON(t, appID))
	env.write("main.gen.go", "package main\n")
	env.write("index.html", "<html>native loop inventory</html>")
	env.write(".sporecode/build/app-loop-inv.exe", "MZ-fake-native-binary")

	a, ictx := installLocalTestActor(t)
	ctx := exportLoopNativeCtx(t, env, ".sporecode/build/app-loop-inv.exe")
	ctx.SpawnFn = ictx.SpawnFn

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}
	iresp, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData})
	if err != nil {
		t.Fatalf("install_local (native): %v", err)
	}
	if iresp.Status.ID != appID || iresp.Status.Runtime != "native" || iresp.Status.State != stateRunning {
		t.Fatalf("install status = %+v, want %s native running", iresp.Status, appID)
	}

	record := a.Records[appID]
	artDir := inventoryArtifactsDir()
	if record.ArtifactPath == "" || !strings.HasPrefix(record.ArtifactPath, artDir+string(filepath.Separator)) {
		t.Fatalf("ArtifactPath %q not under artifacts inventory %q", record.ArtifactPath, artDir)
	}
	info, err := os.Stat(record.ArtifactPath)
	if err != nil {
		t.Fatalf("stored artifact %q unreadable: %v", record.ArtifactPath, err)
	}
	if info.IsDir() || info.Size() == 0 {
		t.Fatalf("stored artifact %q is not a file with content", record.ArtifactPath)
	}
	if record.PackagePath == "" {
		t.Fatal("native install left no PackagePath")
	}
	if _, err := os.Stat(record.PackagePath); err != nil {
		t.Fatalf("native package zip %q missing: %v", record.PackagePath, err)
	}
}

// TestInstallLocal_NativeUpdateGCsOldArtifact: installing a second, different
// native zip under the same app ID must swap the artifact inventory file
// (old artifact deleted after the new record commits) while the new artifact
// remains on disk.
func TestInstallLocal_NativeUpdateGCsOldArtifact(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.export.loop.native.update"
	a, ictx := installLocalTestActor(t)
	build := func(version, exeName, exeContent string) {
		t.Helper()
		env := newBP7Project(t, "app-export-loop-native-up-")
		m := gen.AppManifest{
			ID: appID, Name: "ExportLoopNativeUpdate", Version: version,
			Runtime: "native", ProtocolVersion: 1, Namespace: appID,
			Callables: []gen.AppCallableDescriptor{{ID: "ping", RequestSchema: "Req", ResponseSchema: "Resp"}},
		}
		env.write("app.manifest.json", string(manifestJSON(t, m)))
		env.write("main.gen.go", "package main\n")
		env.write(".sporecode/build/"+exeName, exeContent)
		ctx := exportLoopNativeCtx(t, env, ".sporecode/build/"+exeName)
		ctx.SpawnFn = ictx.SpawnFn
		resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
		if err != nil {
			t.Fatalf("handleAppExport(%s): %v", version, err)
		}
		if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData}); err != nil {
			t.Fatalf("install_local(%s): %v", version, err)
		}
	}

	build("1.0.0", "app-up.exe", "MZ-fake-v1")
	oldRec, ok := a.Records[appID]
	if !ok || oldRec.ArtifactPath == "" {
		t.Fatalf("v1 record missing artifact info: %+v", oldRec)
	}
	oldArt := oldRec.ArtifactPath
	oldZip := oldRec.PackagePath

	// Second, different artifact under the same ID.
	build("2.0.0", "app-up.exe", "MZ-fake-v2")

	newRec := a.Records[appID]
	if newRec.ArtifactPath == "" || newRec.ArtifactPath == oldArt {
		t.Fatalf("artifact did not switch on native update: %q -> %q", oldArt, newRec.ArtifactPath)
	}
	if _, err := os.Stat(newRec.ArtifactPath); err != nil {
		t.Fatalf("new artifact %q missing: %v", newRec.ArtifactPath, err)
	}
	if _, err := os.Stat(oldArt); err == nil {
		t.Fatalf("old artifact %q not garbage-collected after update commit", oldArt)
	}
	if _, err := os.Stat(oldZip); err == nil {
		t.Fatalf("old zip %q not garbage-collected after update commit", oldZip)
	}
	if newRec.PackagePath == "" || newRec.PackagePath == oldZip {
		t.Fatalf("package path did not switch on native update: %q", newRec.PackagePath)
	}
	if _, err := os.Stat(newRec.PackagePath); err != nil {
		t.Fatalf("new zip %q missing: %v", newRec.PackagePath, err)
	}
}
