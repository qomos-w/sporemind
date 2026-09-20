package appmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Lifecycle cleanup acceptance for the package inventory (T2): unregister
// must reclaim the stored install zip and the content-addressed native
// artifact (install_local files under <DataDir>/.actors/appmanager/), and a
// same-ID project registration over a zip-installed app must clear the
// record's PackagePath and remove the superseded inventory files while
// auditing the takeover. Files outside the inventory root — register_project
// artifacts in the project directory — must never be removed by either path.

// assertInventoryFileGone fails when path still exists.
func assertInventoryFileGone(t *testing.T, what, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s %q still exists after unregister", what, path)
	}
}

// auditReasonsContaining returns the reasons of audit records whose Reason
// contains needle.
func auditReasonsContaining(a *Actor, needle string) []string {
	var out []string
	for _, r := range a.AuditRecords {
		if strings.Contains(r.Reason, needle) {
			out = append(out, r.Reason)
		}
	}
	return out
}

// TestUnregisterReclaimsSporeZipInventory: a zip-installed spore app's stored
// inventory zip (record.PackagePath) must be deleted by unregister.
func TestUnregisterReclaimsSporeZipInventory(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.cleanup.sporezip"
	a, ctx := installLocalTestActor(t)
	zipData, _ := signedLocalZip(t, minimalSporeManifest(appID), false)
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: zipData}); err != nil {
		t.Fatalf("install_local: %v", err)
	}
	record := a.Records[appID]
	if record.PackagePath == "" {
		t.Fatal("zip install left no PackagePath")
	}
	if _, err := os.Stat(record.PackagePath); err != nil {
		t.Fatalf("stored zip %q missing after install: %v", record.PackagePath, err)
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: appID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := a.Records[appID]; ok {
		t.Fatal("record still present after unregister")
	}
	assertInventoryFileGone(t, "stored package zip", record.PackagePath)
	if leftover := listInventoryFiles(t, inventoryPackagesDir(), appID+"-"); len(leftover) > 0 {
		t.Fatalf("package inventory still holds %q after unregister", leftover)
	}
}

// TestUnregisterReclaimsNativeZipAndArtifactInventory: a zip-installed native
// app's stored zip AND content-addressed artifact (record.PackagePath and
// record.ArtifactPath, both under the inventory) must both be deleted by
// unregister. The project-dir build artifact the zip was exported from must
// survive (it is not inventory-owned).
func TestUnregisterReclaimsNativeZipAndArtifactInventory(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.cleanup.nativeinv"
	env := newBP7Project(t, "cleanup-native-")
	env.write("app.manifest.json", exportLoopNativeManifestJSON(t, appID))
	env.write("main.gen.go", "package main\n")
	env.write(".sporecode/build/app-cleanup.exe", "MZ-fake-native-binary")

	a, ictx := installLocalTestActor(t)
	ctx := exportLoopNativeCtx(t, env, ".sporecode/build/app-cleanup.exe")
	ctx.SpawnFn = ictx.SpawnFn

	resp, err := a.handleAppExport(ctx, gen.AppManagerAppExportReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleAppExport: %v", err)
	}
	if _, err := a.handleInstallLocal(ctx, gen.AppManagerInstallLocalReq{PackageData: resp.PackageData}); err != nil {
		t.Fatalf("install_local (native): %v", err)
	}
	record := a.Records[appID]
	if record.PackagePath == "" || record.ArtifactPath == "" {
		t.Fatalf("native install left incomplete inventory refs: %+v", record)
	}
	for _, p := range []string{record.PackagePath, record.ArtifactPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("inventory file %q missing after install: %v", p, err)
		}
	}
	// The source artifact in the project build dir is NOT inventory-owned.
	projectArtifact := filepath.Join(env.root, ".sporecode", "build", "app-cleanup.exe")
	if _, err := os.Stat(projectArtifact); err != nil {
		t.Fatalf("project artifact %q missing before unregister: %v", projectArtifact, err)
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: appID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	assertInventoryFileGone(t, "stored package zip", record.PackagePath)
	assertInventoryFileGone(t, "stored artifact", record.ArtifactPath)
	if leftover := listInventoryFiles(t, inventoryPackagesDir(), appID+"-"); len(leftover) > 0 {
		t.Fatalf("package inventory still holds %q after unregister", leftover)
	}
	if leftover := listInventoryFiles(t, inventoryArtifactsDir(), appID+"-"); len(leftover) > 0 {
		t.Fatalf("artifact inventory still holds %q after unregister", leftover)
	}
	// The project-dir build artifact is not appmanager-owned and must survive.
	if _, err := os.Stat(projectArtifact); err != nil {
		t.Fatalf("project build artifact %q was removed by unregister: %v", projectArtifact, err)
	}
}

// TestRegisterProjectTakeoverClearsZipInventory: a project registration
// (Origin "project") over a same-ID zip-installed app (Origin "user") is a
// takeover — the record's PackagePath must be cleared, the stored zip must be
// removed, and the takeover must be audited.
func TestRegisterProjectTakeoverClearsZipInventory(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.takeover.spore"
	// The project directory carries the same app ID (spore: no native build,
	// no .appdef gates — register_project goes straight through doRegister).
	env := newBP7Project(t, "takeover-reg-")
	env.write("app.manifest.json", string(manifestJSON(t, minimalSporeManifest(appID))))
	env.write("main.spore", "export default {}")

	a, ictx := installLocalTestActor(t)
	wireExportEnv(ictx, env)

	// Zip-install first: user origin, stored zip under the inventory.
	zipData, _ := signedLocalZip(t, minimalSporeManifest(appID), false)
	if _, err := a.handleInstallLocal(ictx, gen.AppManagerInstallLocalReq{PackageData: zipData}); err != nil {
		t.Fatalf("install_local: %v", err)
	}
	oldRec := a.Records[appID]
	if oldRec.PackagePath == "" || normalizeOrigin(oldRec.Origin) != "user" {
		t.Fatalf("expected user zip install with PackagePath, got %+v", oldRec)
	}
	oldZip := oldRec.PackagePath
	if _, err := os.Stat(oldZip); err != nil {
		t.Fatalf("stored zip %q missing after install: %v", oldZip, err)
	}

	// register_project over the same ID: project origin takes over.
	resp, err := a.handleRegisterProject(ictx, gen.AppManagerRegisterProjectReq{ProjectID: bp7ProjectID(t)})
	if err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	if resp.Status.ID != appID || resp.Status.State != stateRunning {
		t.Fatalf("register status = %+v, want %s running", resp.Status, appID)
	}
	newRec := a.Records[appID]
	if newRec.PackagePath != "" {
		t.Fatalf("record.PackagePath = %q after project takeover, want empty (authority back to project dir)", newRec.PackagePath)
	}
	if newRec.Origin != "project" {
		t.Fatalf("record origin = %q, want project", newRec.Origin)
	}
	assertInventoryFileGone(t, "superseded stored zip", oldZip)
	if leftover := listInventoryFiles(t, inventoryPackagesDir(), appID+"-"); len(leftover) > 0 {
		t.Fatalf("package inventory still holds %q after takeover", leftover)
	}
	// The project manifest is now the authoritative source and must have
	// survived (it never entered the inventory).
	if !env.exists("app.manifest.json") || !env.exists("main.spore") {
		t.Fatal("project sources missing after takeover")
	}
	reasons := auditReasonsContaining(a, "zip-install takeover")
	if len(reasons) == 0 {
		t.Fatal("no audit record for the zip-install takeover")
	}
}

// TestUnregisterPreservesNonInventoryArtifactPath: an artifact path that is
// NOT under the inventory root (register_project artifacts live in the
// project directory) must never be deleted by unregister, while an
// inventory-owned stored zip on the same record is still reclaimed.
func TestUnregisterPreservesNonInventoryArtifactPath(t *testing.T) {
	configSetupForInstallTest(t)

	const appID = "app.cleanup.noninv"
	manifest := minimalSporeManifest(appID)

	// Project-directory artifact (register_project shape): not appmanager's
	// to delete.
	projDir := t.TempDir()
	artifactPath := filepath.Join(projDir, ".sporecode", "build", "app-cleanup.exe")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactPath, []byte("project-artifact"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Inventory-owned stored zip on the same record (reclaimable).
	zipData, _ := signedLocalZip(t, manifest, false)
	zipPath, err := storePackageZip(appID, zipData)
	if err != nil {
		t.Fatalf("store package zip: %v", err)
	}

	a, ctx := installLocalTestActor(t)
	a.Apps[appID] = manifest
	a.Records[appID] = appRecord{
		Manifest:     manifest,
		State:        stateRunning,
		Generation:   1,
		PackagePath:  zipPath,
		ArtifactPath: artifactPath,
		Origin:       "user",
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: appID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := a.Records[appID]; ok {
		t.Fatal("record still present after unregister")
	}
	// The non-inventory artifact must survive.
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("non-inventory artifact %q removed by unregister: %v", artifactPath, err)
	}
	// The inventory zip is reclaimed.
	assertInventoryFileGone(t, "stored package zip", zipPath)
}
