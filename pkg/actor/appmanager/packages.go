package appmanager

// Package inventory helpers for zip-installed apps (install_local).
//
// Inventory layout (filesystem-backed, never JSON documents — CLAUDE.md
// persist semantics; the persist backend only ever sees the actor state
// record files under <DataDir>/.actors/appmanager/*.json):
//
//	<DataDir>/.actors/appmanager/packages/<appID>-<PackageHash>.zip
//	<DataDir>/.actors/appmanager/artifacts/<appID>-<ArtifactHash><ext>
//
// Both are content-addressed per app: reinstalling the same bytes is a
// no-op (same file name), and installing different bytes lands in a new
// file while the old one is garbage-collected only after the new record
// commit succeeds ("同 ID 装新 zip = 更新"的原子换指针).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// inventoryRoot returns the directory that holds the packages/ and
// artifacts/ stores. It lives under ActorDataDir()/.actors/appmanager — the
// same root the appmanager actor state documents use, but as plain
// directories that never enter the persist backend.
func inventoryRoot() string {
	return filepath.Join(config.ActorDataDir(), "appmanager")
}

// inventoryPackagesDir is the content-addressed zip store: one file per
// (appID, packageHash), named <appID>-<PackageHash>.zip.
func inventoryPackagesDir() string { return filepath.Join(inventoryRoot(), "packages") }

// inventoryArtifactsDir is the content-addressed native artifact store:
// <appID>-<ArtifactHash><original-extension>.
func inventoryArtifactsDir() string { return filepath.Join(inventoryRoot(), "artifacts") }

// installedAppsDir holds the materialized frontend trees of inventory-installed
// (zip) apps: one directory per app id.
func installedAppsDir() string { return filepath.Join(inventoryRoot(), "apps") }

// installedAppDir is the materialized directory of one installed app. Unlike a
// project-registered app (artifact under <appDir>/.sporecode/build/), an
// installed app's artifact lives in the content-addressed artifacts store, so
// the app directory is explicit rather than derivable from the artifact path.
// It is the static root the plugin's own HTTP listener serves (LoadConfig
// StaticDir) and the anchor for the app.data grant (<dir>/.sporecode/appdata).
func installedAppDir(appID string) string {
	return filepath.Join(installedAppsDir(), appID)
}

// removeInstalledAppDir reclaims an installed app's materialized directory.
// Only directories under the inventory root are touched; a project-registered
// app has no such directory and this is a no-op.
func removeInstalledAppDir(appID string) {
	dir := installedAppDir(appID)
	if !inventoryOwned(dir) {
		return
	}
	_ = os.RemoveAll(dir)
}

// packageFileName returns the content-addressed zip file name for an app
// package: <appID>-<PackageHash>.zip.
func packageFileName(appID, packageHash string) string {
	return fmt.Sprintf("%s-%s.zip", appID, strings.ToLower(packageHash))
}

// packageFilePath resolves the inventory path for an app package.
func packageFilePath(appID, packageHash string) string {
	return filepath.Join(inventoryPackagesDir(), packageFileName(appID, packageHash))
}

// artifactFileName returns the content-addressed artifact file name for a
// native app artifact: <appID>-<ArtifactHash><original-extension>.
func artifactFileName(appID, artifactHash, artifactName string) string {
	return fmt.Sprintf("%s-%s%s", appID, strings.ToLower(artifactHash), filepath.Ext(artifactName))
}

// artifactFilePath resolves the inventory path for a native artifact.
func artifactFilePath(appID, artifactHash, artifactName string) string {
	return filepath.Join(inventoryArtifactsDir(), artifactFileName(appID, artifactHash, artifactName))
}

// inventoryOwned reports whether path lies under the appmanager inventory root
// (<DataDir>/.actors/appmanager/). Only files under this root are owned by
// appmanager lifecycle cleanup and may be deleted when a record is superseded
// or removed. register_project artifacts live in the project directory —
// never under this root — and must never be touched by that cleanup.
func inventoryOwned(path string) bool {
	if path == "" {
		return false
	}
	root := filepath.Clean(inventoryRoot()) + string(filepath.Separator)
	return strings.HasPrefix(filepath.Clean(path), root)
}

// removeOwnedInventoryFile deletes a single inventory file, but only when it
// lies under the inventory root. Everything else (project-dir artifacts, OS
// temp paths) is never removed by lifecycle cleanup. Best-effort: a missing
// file is ignored, and a removal failure only wastes disk.
func removeOwnedInventoryFile(path string) {
	if !inventoryOwned(path) {
		return
	}
	_ = os.Remove(path)
}

// clearSupersededZipAuthority drops a record's PackagePath after a project
// register/reload takeover replaced a zip-installed app: the stored install
// zip no longer matches the authoritative source (the project directory), so
// the reference is cleared and the inventory file removed. The cleared field
// is persisted before the file is deleted (a crash in between leaves only a
// dangling reference to a missing backup zip, never a record that points at
// nothing authoritative). Returns the removed zip path for audit purposes.
func (a *Actor) clearSupersededZipAuthority(appID, action string) (zipPath string) {
	a.withMu(func() {
		rec := a.Records[appID]
		zipPath = rec.PackagePath
		if zipPath != "" {
			rec.PackagePath = ""
			a.Records[appID] = rec
		}
	})
	if zipPath == "" {
		return ""
	}
	if err := a.Save(); err != nil {
		// Roll the field back so the record stays consistent with the file.
		a.withMu(func() {
			cur := a.Records[appID]
			cur.PackagePath = zipPath
			a.Records[appID] = cur
		})
		_ = a.Save()
		return ""
	}
	removeOwnedInventoryFile(zipPath)
	if zipPath != "" {
		a.auditLifecycle(appID, action, fmt.Sprintf("zip-install authority superseded: removed stored zip %q", zipPath))
	}
	return zipPath
}

// sha256Hex is a small helper returning the lowercase hex sha256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// storePackageZip writes the install source zip bytes into the packages
// inventory under its content address. If the file already exists the write
// is skipped (content-addressed idempotency). The returned path is stable
// and is recorded on the app record as PackagePath.
func storePackageZip(appID string, zipData []byte) (string, error) {
	if len(zipData) == 0 {
		return "", fmt.Errorf("appmanager: package bytes are empty")
	}
	pkgHash := sha256Hex(zipData)
	return storePackageZipHashed(appID, pkgHash, zipData)
}

// storePackageZipHashed is storePackageZip for callers that already know the
// canonical package hash (the signed-verification flow hashes the zip bytes,
// while the canonical hash covers the package contents). The zip content
// address must be stable and byte-faithful (preserving PACKAGE.sig for the
// audit chain), so the raw-zip sha256 is the address.
func storePackageZipHashed(appID, pkgHash string, zipData []byte) (string, error) {
	path := packageFilePath(appID, pkgHash)
	if _, err := os.Stat(path); err == nil {
		// Content-addressed: same app + same hash already stored.
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("appmanager: mkdir packages dir: %w", err)
	}
	if err := persist.WriteFileAtomic(path, zipData, 0o644); err != nil {
		return "", fmt.Errorf("appmanager: write package zip %q: %w", path, err)
	}
	return path, nil
}

// storeNativeArtifact writes the native artifact bytes into the artifacts
// inventory under their content address, returning the stable path to hand
// to pluginhost as ArtifactPath. An existing file (same app + same hash) is
// reused rather than rewritten.
func storeNativeArtifact(appID, artifactHash, artifactName string, artifact []byte) (string, error) {
	path := artifactFilePath(appID, artifactHash, artifactName)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("appmanager: mkdir artifacts dir: %w", err)
	}
	if err := persist.WriteFileAtomic(path, artifact, 0o644); err != nil {
		return "", fmt.Errorf("appmanager: write artifact %q: %w", path, err)
	}
	return path, nil
}

// gcStalePackageFiles was moved into install_local.go (it needs the actor's
// record view). See (a *Actor) gcStalePackageFiles there.

// auditPackageReplaced records the old→new package hash transition of a
// same-ID reinstall ("同 ID 装新 zip = 更新") on the actor's audit trail.
func (a *Actor) auditPackageReplaced(appID string, oldRecord, newRecord appRecord, runtime string) {
	reason := fmt.Sprintf("package replaced: %s -> %s", oldRecord.PackageHash, newRecord.PackageHash)
	a.recordAudit(appbinding.AuditRecord{AppID: appID, Runtime: runtime, Callable: "install_local", Allowed: true, Reason: reason})
}
