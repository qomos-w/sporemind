package appmanager

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleAppExport packages a project app into a signed, installable zip and
// returns the zip bytes to the caller (the caller persists PackageData itself,
// e.g. via project.write — AppManager never writes export output to disk).
//
// Assembly reuses handleProjectPackage for manifest/modules/assets. For
// runtime=native the artifact and ABI come from pluginhost.native_build, the
// same build path register_project uses. The zip then carries the entry
// contract install_local reads: app.manifest.json, module sources, assets,
// abi.json + the single native binary (native only), and base64-encoded
// PACKAGE.sig / PACKAGE.pub. Schema descriptors deliberately stay out of the
// zip; per the reader-side contract they do not enter the signed hash either.
//
// The signature covers the canonical package hash (sporemind.app-package.v2,
// including abi + artifactHash) computed to reproduce exactly what
// install_local recomputes at verification time — including the reader's
// entry classification, which counts abi.json and the artifact binary as
// assets.
func (a *Actor) handleAppExport(ctx actor.Context, req gen.AppManagerAppExportReq) (gen.AppManagerAppExportResp, error) {
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerAppExportResp{}, err
	}
	req.ProjectID = projectID
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: invalid AppDir: %w", err)
	}
	pkg, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{
		ProjectID: req.ProjectID,
		AppID:     req.AppID,
		AppDir:    appDir,
	})
	if err != nil {
		return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: read project package: %w", err)
	}

	var abi *gen.PluginAbi
	var artifact []byte
	var artifactName string
	artifactHash := ""
	if pkg.Manifest.Runtime == "native" {
		pluginRef, ok := ctx.LookupService(pluginhostServiceName)
		if !ok || pluginRef == nil || ctx.Planner() == nil {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: pluginhost service not available")
		}
		buildValue, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.native_build", gen.NativeBuildReq{
			ProjectID: req.ProjectID, AppID: pkg.Manifest.ID, EntryModule: pkg.EntryModule, AppDir: appDir,
		}).Await()
		if err != nil {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: native build: %w", err)
		}
		build, ok := buildValue.(gen.NativeBuildResp)
		if !ok || !build.Result.Success {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: native build returned invalid result")
		}
		abi = &build.Abi
		artifact, err = os.ReadFile(build.Result.ArtifactPath)
		if err != nil {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: read native artifact %q: %w", build.Result.ArtifactPath, err)
		}
		// The verification side hashes the artifact bytes it reads back from
		// the zip (install_local.go), so the signed hash must use the same
		// sha256 over the exact bytes packaged here — not the build report's
		// hash field.
		sum := sha256.Sum256(artifact)
		artifactHash = hex.EncodeToString(sum[:])
		artifactName = path.Base(filepath.ToSlash(build.Result.ArtifactPath))
		if _, exists := pkg.Assets[artifactName]; exists {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: native artifact name %q collides with a packaged asset", artifactName)
		}
		if _, exists := pkg.Modules[artifactName]; exists {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: native artifact name %q collides with a packaged module", artifactName)
		}
	}

	// Reader-matching hash inputs. The receiving host parses the package
	// exactly as register_project does: schema descriptors are content
	// (app.descriptors.json, hash input), and for native packages the reader
	// classifies abi.json and the artifact binary as assets, so both must
	// count as assets here or the hashes disagree.
	hashAssets := make(map[string][]byte, len(pkg.Assets)+2)
	for name, content := range pkg.Assets {
		hashAssets[name] = content
	}
	if abi != nil {
		abiJSON, err := json.Marshal(abi)
		if err != nil {
			return gen.AppManagerAppExportResp{}, fmt.Errorf("appmanager: encode abi: %w", err)
		}
		hashAssets["abi.json"] = abiJSON
		hashAssets[artifactName] = artifact
	}
	packageHash, err := canonicalPackageHash(pkg.Manifest, pkg.EntryModule, pkg.Modules, hashAssets, pkg.SchemaDescriptors, abi, artifactHash)
	if err != nil {
		return gen.AppManagerAppExportResp{}, err
	}

	priv, err := a.exportSigningKey(ctx)
	if err != nil {
		return gen.AppManagerAppExportResp{}, err
	}
	sig := ed25519.Sign(priv, []byte(packageHash))
	pub, _ := priv.Public().(ed25519.PublicKey)

	zipData, err := buildExportZip(pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, abi, artifactName, artifact, sig, pub)
	if err != nil {
		return gen.AppManagerAppExportResp{}, err
	}
	return gen.AppManagerAppExportResp{
		PackageData: zipData,
		PackageHash: packageHash,
		PublicKey:   base64.StdEncoding.EncodeToString(pub),
	}, nil
}

// exportSigningKey returns the per-host Ed25519 key that signs exported
// packages, generating and persisting it on first use. The key lives in the
// actor's saveState document (base64 via JSON []byte encoding) and must be
// restored rather than regenerated across restarts — regenerating would
// invalidate every previously exported zip's embedded PACKAGE.pub.
func (a *Actor) exportSigningKey(ctx actor.Context) (ed25519.PrivateKey, error) {
	var existing ed25519.PrivateKey
	a.withMu(func() {
		existing = a.exportSigKey
	})
	if len(existing) == ed25519.PrivateKeySize {
		return existing, nil
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("appmanager: generate export signing key: %w", err)
	}
	a.withMu(func() {
		a.exportSigKey = priv
	})
	// Persist outside the lock (store.Save blocks on I/O). Best-effort: a
	// failed save only risks a regenerated key on next boot, which is safe
	// for state but would orphan previously exported zips' signatures.
	if err := a.saveState(a.Apps, a.Records); err != nil {
		ctx.Logger().Error("appmanager: persist export signing key failed", "error", err)
	}
	return priv, nil
}

// buildExportZip assembles the export archive in the entry contract
// install_local reads. Entry order is deterministic (sorted maps) so repeated
// exports of unchanged sources produce identical bytes.
func buildExportZip(manifest gen.AppManifest, entry string, modules map[string]string, assets map[string][]byte, descriptors map[string]gen.AppObjectDescriptor, abi *gen.PluginAbi, artifactName string, artifact, sig, pub []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	writeEntry := func(name string, content []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create zip entry %q: %w", name, err)
		}
		if _, err := w.Write(content); err != nil {
			return fmt.Errorf("write zip entry %q: %w", name, err)
		}
		return nil
	}

	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("appmanager: encode manifest: %w", err)
	}
	if err := writeEntry(localPackageManifestName, manifestJSON); err != nil {
		return nil, err
	}
	moduleNames := make([]string, 0, len(modules))
	for name := range modules {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)
	for _, name := range moduleNames {
		if err := writeEntry(name, []byte(modules[name])); err != nil {
			return nil, err
		}
	}
	assetNames := make([]string, 0, len(assets))
	for name := range assets {
		assetNames = append(assetNames, name)
	}
	sort.Strings(assetNames)
	for _, name := range assetNames {
		if err := writeEntry(name, assets[name]); err != nil {
			return nil, err
		}
	}
	// Schema descriptors are package content: the receiving host needs them to
	// register the app's typed schemas, and they are covered by the signed
	// package hash. Written after assets so entry order stays deterministic.
	if len(descriptors) > 0 {
		descriptorJSON, err := json.Marshal(descriptors)
		if err != nil {
			return nil, fmt.Errorf("appmanager: encode schema descriptors: %w", err)
		}
		if err := writeEntry(descriptorFile, descriptorJSON); err != nil {
			return nil, err
		}
	}
	if abi != nil {
		abiJSON, err := json.Marshal(abi)
		if err != nil {
			return nil, fmt.Errorf("appmanager: encode abi: %w", err)
		}
		if err := writeEntry("abi.json", abiJSON); err != nil {
			return nil, err
		}
		if err := writeEntry(artifactName, artifact); err != nil {
			return nil, err
		}
	}
	if err := writeEntry(localPackageSigName, []byte(base64.StdEncoding.EncodeToString(sig))); err != nil {
		return nil, err
	}
	if err := writeEntry(localPackagePubName, []byte(base64.StdEncoding.EncodeToString(pub))); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("appmanager: finalize export zip: %w", err)
	}
	return buf.Bytes(), nil
}
