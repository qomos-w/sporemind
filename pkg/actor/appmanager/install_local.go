package appmanager

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const (
	localPackageManifestName = "app.manifest.json"
	localPackageSigName      = "PACKAGE.sig"
	localPackagePubName      = "PACKAGE.pub"
)

// localPackageFiles holds the parsed contents of a local install source.
type localPackageFiles struct {
	Manifest    gen.AppManifest
	EntryModule string
	Modules     map[string]string
	Assets      map[string][]byte
	// SchemaDescriptors is the app's declared struct descriptors
	// (app.descriptors.json). They are package content, not assets: the host
	// protocol registry needs them to register the app's typed callables, and
	// the canonical package hash covers them exactly as register_project does.
	SchemaDescriptors map[string]gen.AppObjectDescriptor
	Sig               []byte
	Pub               []byte
	HasSig            bool
	HasPub            bool
	// Native-only fields, populated when Runtime == "native".
	Artifact     []byte
	ArtifactName string
	Abi          *gen.PluginAbi
}

// localPackageEntry is one regular file from an install source (zip entry or
// directory walk), named by its slash-separated package-relative path.
type localPackageEntry struct {
	name    string
	content []byte
}

// handleInstallLocal installs an app from a local package: a .zip archive or
// a package directory (via Path).
//
// The source must contain app.manifest.json at the root. Module sources are
// collected from .spore / .ss / .go files; everything else (except build
// artifacts, signature files, and dot files) is treated as an asset. If the
// package carries PACKAGE.sig, PACKAGE.pub must also be present and the
// Ed25519 signature over the canonical package hash must verify.
//
// Spore apps are registered directly. Plugins first load their artifact
// through pluginhost.artifact_load, then reuse handleRegister.
func (a *Actor) handleInstallLocal(ctx actor.Context, req gen.AppManagerInstallLocalReq) (gen.AppManagerInstallLocalResp, error) {
	// Resolve + validate the source first. Nothing below this point may
	// touch the inventory: signature/manifest failures must not leave half-
	// staged package files and must not disturb an existing install.
	pkg, zipData, err := a.loadLocalPackage(req)
	if err != nil {
		return gen.AppManagerInstallLocalResp{}, fmt.Errorf("appmanager.install_local: %w", err)
	}

	// Stage the zip into the content-addressed packages inventory BEFORE
	// registering: doRegister only commits the new record after a successful
	// load, and the old package is garbage-collected only after that commit.
	// Raw zip bytes are stored verbatim (PACKAGE.sig preserved) so the
	// inventory keeps the full audit chain. Directory installs (zipData ==
	// nil) still store a zip so the update/GC bookkeeping has one shape.
	registerReq, deferred, backend, oldRecord, hadOld, stagedZip, err := a.buildLocalRegisterReq(ctx, pkg, zipData)
	if err != nil {
		return gen.AppManagerInstallLocalResp{}, fmt.Errorf("appmanager.install_local: %w", err)
	}

	status, err := a.doRegister(ctx, registerReq, deferred)
	if err != nil {
		// Failure path: doRegister never committed the new record. Remove the
		// staged zip/artifact if this install staged them (best-effort — a
		// leftover here is only disk, never a corrupt record), and leave the
		// old package untouched.
		a.gcFailedInstall(registerReq.Manifest.ID, stagedZip)
		return gen.AppManagerInstallLocalResp{}, fmt.Errorf("appmanager.install_local: %w", err)
	}
	// The artifact load happened before the record existed; now that
	// doRegister landed it, commit the per-instance backend (secret +
	// bootstrap URL) the plugin process confirmed at OnLoad.
	if backend.hasBackend() {
		a.commitBackend(ctx, registerReq.Manifest.ID, backend.secret, backend.httpAddr)
	}
	// Same-ID reinstall (update): the new record committed. GC the superseded
	// zip/artifact and audit the pointer swap.
	if hadOld {
		a.gcStalePackageFiles(registerReq.Manifest.ID, oldRecord, a.Records[registerReq.Manifest.ID])
		if !strings.EqualFold(oldRecord.PackageHash, registerReq.PackageHash) {
			a.auditPackageReplaced(registerReq.Manifest.ID, oldRecord, a.Records[registerReq.Manifest.ID], registerReq.Manifest.Runtime)
		}
	}
	return gen.AppManagerInstallLocalResp{Status: status}, nil
}

// gcStalePackageFiles removes the zip/artifact files of a superseded
// install (old record) after the new registration has committed. Files are
// only deleted when their content address differs from the new one, so a
// reinstall of identical bytes never churns the store. Missing files are
// ignored (best-effort GC — a leftover only wastes disk, never breaks the
// new install).
func (a *Actor) gcStalePackageFiles(appID string, oldRecord, newRecord appRecord) {
	// The new zip was stored (if any) before doRegister committed; never
	// delete a file the new record still references. Removal is additionally
	// gated on inventory ownership: only files under the appmanager inventory
	// root are reclaimed by lifecycle GC — a record whose PackagePath /
	// ArtifactPath points into a project directory is never touched here.
	if oldRecord.PackagePath != "" && oldRecord.PackagePath != newRecord.PackagePath {
		removeOwnedInventoryFile(oldRecord.PackagePath)
	}
	// The new artifact path (if any) was stored before commit as well.
	// Only remove the old artifact when the content hash actually changed —
	// the record's ArtifactHash is authoritative, not the file name.
	if oldRecord.ArtifactPath != "" &&
		oldRecord.ArtifactPath != newRecord.ArtifactPath &&
		oldRecord.ArtifactHash != "" && oldRecord.ArtifactHash != newRecord.ArtifactHash {
		removeOwnedInventoryFile(oldRecord.ArtifactPath)
	}
}

// gcFailedInstall removes inventory files staged by an install attempt that
// failed after staging (register/load error). The old package files are
// never touched here: a staged file that a surviving record references
// (content-addressed reinstall of identical bytes — the staged path equals
// the old record's path) is left in place, and unreferenced artifact files
// are swept by gcUnreferencedArtifacts.
func (a *Actor) gcFailedInstall(appID, stagedZip string) {
	if stagedZip == "" {
		return
	}
	referenced := false
	a.withMu(func() {
		for _, rec := range a.Records {
			if rec.PackagePath == stagedZip || rec.ArtifactPath == stagedZip {
				referenced = true
				break
			}
		}
	})
	if !referenced {
		_ = os.Remove(stagedZip)
	}
	// Native staging also wrote an artifact under the app's artifacts dir;
	// remove it when it is not referenced by any surviving record.
	a.gcUnreferencedArtifacts(appID, "")
	// A failed install may have materialized a frontend tree for an app that
	// never committed a record; reclaim it when no surviving record owns it.
	// A failed reinstall (old record intact) keeps the directory.
	hasRecord := false
	a.withMu(func() {
		_, hasRecord = a.Records[appID]
	})
	if !hasRecord {
		removeInstalledAppDir(appID)
	}
}

// gcUnreferencedArtifacts removes artifact files under appID that no current
// record references. keepHash optionally exempts the artifact of the install
// currently in flight (which may have been staged but not yet committed).
func (a *Actor) gcUnreferencedArtifacts(appID, keepHash string) {
	dir := inventoryArtifactsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := appID + "-"
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		name := strings.TrimPrefix(e.Name(), prefix)
		// <hash><ext>
		dot := strings.IndexByte(name, '.')
		hash := name
		if dot >= 0 {
			hash = name[:dot]
		}
		if hash == keepHash {
			continue
		}
		referenced := false
		for _, rec := range a.Records {
			if strings.EqualFold(rec.ArtifactHash, hash) && strings.HasPrefix(rec.ArtifactPath, dir+string(filepath.Separator)) {
				referenced = true
				break
			}
		}
		if !referenced {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// loadLocalPackage resolves the install source: PackageData zip bytes, a
// .zip Path, or a package directory Path. All sources are parsed into the
// same package shape. It additionally returns the original zip bytes when
// the source was a zip (PackageData or .zip Path) so install_local can store
// the verbatim archive (PACKAGE.sig preserved) in the packages inventory;
// directory sources return nil zipData.
func (a *Actor) loadLocalPackage(req gen.AppManagerInstallLocalReq) (localPackageFiles, []byte, error) {
	if len(req.PackageData) > 0 {
		pkg, err := a.parseLocalZipPackage(req.PackageData)
		return pkg, req.PackageData, err
	}
	if strings.TrimSpace(req.Path) == "" {
		return localPackageFiles{}, nil, fmt.Errorf("Path or PackageData is required")
	}
	info, err := os.Stat(req.Path)
	if err != nil {
		return localPackageFiles{}, nil, fmt.Errorf("stat %q: %w", req.Path, err)
	}
	if info.IsDir() {
		entries, err := walkLocalPackageDir(req.Path)
		if err != nil {
			return localPackageFiles{}, nil, err
		}
		pkg, err := parseLocalPackageEntries(entries)
		return pkg, nil, err
	}
	data, err := os.ReadFile(req.Path)
	if err != nil {
		return localPackageFiles{}, nil, fmt.Errorf("read %q: %w", req.Path, err)
	}
	pkg, err := a.parseLocalZipPackage(data)
	return pkg, data, err
}

// parseLocalZipPackage parses zip package bytes into package entries and
// hands them to the shared entry parser.
func (a *Actor) parseLocalZipPackage(data []byte) (localPackageFiles, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return localPackageFiles{}, fmt.Errorf("open zip: %w", err)
	}
	entries := make([]localPackageEntry, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		content, err := a.readZipEntry(f)
		if err != nil {
			return localPackageFiles{}, err
		}
		entries = append(entries, localPackageEntry{name: filepath.ToSlash(f.Name), content: content})
	}
	return parseLocalPackageEntries(entries)
}

// walkLocalPackageDir collects the regular files of a package directory as
// slash-separated package-relative entries. Dot entries (.git, .DS_Store,
// ...) and node_modules trees are skipped; everything else follows the same
// module/asset classification as zip packages.
func walkLocalPackageDir(dir string) ([]localPackageEntry, error) {
	var entries []localPackageEntry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		// Symlinks are skipped: a package directory must not pull in host
		// files outside itself, and installed assets are HTTP-exposed.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %q: %w", path, readErr)
		}
		entries = append(entries, localPackageEntry{name: filepath.ToSlash(rel), content: content})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %q: %w", dir, err)
	}
	return entries, nil
}

// parseLocalPackageEntries validates the local package format over the
// source entries (zip or directory).
func parseLocalPackageEntries(entries []localPackageEntry) (localPackageFiles, error) {
	pkg := localPackageFiles{
		Modules: map[string]string{},
		Assets:  map[string][]byte{},
	}

	// First pass: read manifest, signature, public key, and schema descriptors.
	for _, e := range entries {
		switch e.name {
		case localPackageManifestName:
			if err := json.Unmarshal(e.content, &pkg.Manifest); err != nil {
				return localPackageFiles{}, fmt.Errorf("decode app.manifest.json: %w", err)
			}
		case descriptorFile:
			if err := json.Unmarshal(e.content, &pkg.SchemaDescriptors); err != nil {
				return localPackageFiles{}, fmt.Errorf("decode %s: %w", descriptorFile, err)
			}
		case localPackageSigName:
			pkg.Sig = e.content
			pkg.HasSig = true
		case localPackagePubName:
			pkg.Pub = e.content
			pkg.HasPub = true
		}
	}

	if pkg.Manifest.ID == "" {
		return localPackageFiles{}, fmt.Errorf("missing app.manifest.json")
	}

	// Determine entry module default by runtime.
	pkg.EntryModule = defaultEntryModule(pkg.Manifest.Runtime)

	// Second pass: collect modules and assets.
	for _, e := range entries {
		name := e.name
		if isLocalPackageMetaFile(name) {
			continue
		}
		if isLocalModuleFile(name) {
			pkg.Modules[name] = string(e.content)
			continue
		}
		if isLocalExcludedAsset(name) {
			continue
		}
		pkg.Assets[name] = e.content
	}

	if _, ok := pkg.Modules[pkg.EntryModule]; !ok && pkg.Manifest.Runtime != "native" {
		return localPackageFiles{}, fmt.Errorf("entry module %q not found in package", pkg.EntryModule)
	}

	// Native packages carry a compiled artifact and ABI sidecar.
	if pkg.Manifest.Runtime == "native" {
		if err := extractNativeArtifact(entries, &pkg); err != nil {
			return localPackageFiles{}, err
		}
	}

	// Verify signature if present.
	if pkg.HasSig {
		if !pkg.HasPub {
			return localPackageFiles{}, fmt.Errorf("PACKAGE.sig present but PACKAGE.pub missing")
		}
		if err := verifyLocalPackageSignature(pkg); err != nil {
			return localPackageFiles{}, err
		}
	}

	return pkg, nil
}

func (a *Actor) readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip entry %q: %w", f.Name, err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read zip entry %q: %w", f.Name, err)
	}
	return content, nil
}

func defaultEntryModule(runtime string) string {
	if runtime == "native" {
		return "main.gen.go"
	}
	return "main.spore"
}

func isLocalPackageMetaFile(name string) bool {
	return name == localPackageManifestName ||
		name == localPackageSigName ||
		name == localPackagePubName ||
		strings.HasPrefix(name, ".")
}

func isLocalModuleFile(name string) bool {
	return strings.HasSuffix(name, ".spore") ||
		strings.HasSuffix(name, ".ss") ||
		strings.HasSuffix(name, ".go")
}

func isLocalExcludedAsset(name string) bool {
	if excludedAssets[name] {
		return true
	}
	// Dot files and signature sidecars are never runtime assets.
	if strings.HasPrefix(name, ".") {
		return true
	}
	return false
}

// extractNativeArtifact locates the compiled artifact and abi.json for a
// native local package. The artifact is the unique file with a native library
// extension; abi.json is parsed as the plugin ABI.
func extractNativeArtifact(entries []localPackageEntry, pkg *localPackageFiles) error {
	const abiFileName = "abi.json"

	for _, e := range entries {
		name := e.name
		if name == abiFileName {
			var abi gen.PluginAbi
			if err := json.Unmarshal(e.content, &abi); err != nil {
				return fmt.Errorf("decode abi.json: %w", err)
			}
			pkg.Abi = &abi
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".dll" || ext == ".so" || ext == ".dylib" || ext == ".exe" {
			if pkg.Artifact != nil {
				return fmt.Errorf("native package contains multiple artifact binaries")
			}
			pkg.Artifact = e.content
			pkg.ArtifactName = name
		}
	}

	if len(pkg.Artifact) == 0 {
		return fmt.Errorf("native package missing artifact binary (.dll/.so/.dylib/.exe)")
	}
	if pkg.Abi == nil {
		return fmt.Errorf("native package missing abi.json")
	}
	return nil
}

// verifyLocalPackageSignature verifies the Ed25519 signature over the
// canonical package hash. PACKAGE.sig and PACKAGE.pub are expected to be
// base64-encoded. For native packages the artifact bytes are hashed so the
// signature covers the binary as well as the manifest and modules/assets.
func verifyLocalPackageSignature(pkg localPackageFiles) error {
	sig, err := decodeBase64Bytes(pkg.Sig)
	if err != nil {
		return fmt.Errorf("decode PACKAGE.sig: %w", err)
	}
	pub, err := decodeBase64Bytes(pkg.Pub)
	if err != nil {
		return fmt.Errorf("decode PACKAGE.pub: %w", err)
	}
	if l := len(pub); l != ed25519.PublicKeySize {
		return fmt.Errorf("PACKAGE.pub invalid Ed25519 public key length %d", l)
	}
	if l := len(sig); l != ed25519.SignatureSize {
		return fmt.Errorf("PACKAGE.sig invalid Ed25519 signature length %d", l)
	}

	artifactHash := ""
	if len(pkg.Artifact) > 0 {
		sum := sha256.Sum256(pkg.Artifact)
		artifactHash = hex.EncodeToString(sum[:])
	}
	packageHash, err := canonicalPackageHash(
		pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, pkg.Abi, artifactHash,
	)
	if err != nil {
		return fmt.Errorf("compute package hash for signature: %w", err)
	}

	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(packageHash), sig) {
		return fmt.Errorf("PACKAGE.sig verification failed")
	}
	return nil
}

func decodeBase64Bytes(data []byte) ([]byte, error) {
	s := strings.TrimSpace(string(data))
	return base64.StdEncoding.DecodeString(s)
}

// buildLocalRegisterReq assembles the AppManagerRegisterReq from the parsed
// local package. For native runtimes it loads the artifact into pluginhost
// before filling the artifact fields, mirroring project_package.go:305-315.
//
// The second return value is the deferral flag: when the in-process host
// blocks swapping the artifact (unload-pending or a different artifact
// already loaded), the load is deferred to the next host restart and the
// caller must register with State=restart_pending instead of an error.
//
// zipData carries the original install zip bytes (nil for directory
// installs). The zip is stored into the content-addressed packages inventory
// (packages/<appID>-<PackageHash>.zip) and the resulting stable path is put
// on registerReq.PackagePath; native artifacts are stored into the artifacts
// inventory instead of an OS temp file, and registerReq.ArtifactPath points
// at that stable path. On any error nothing is left staged: files written
// here are removed before returning.
func (a *Actor) buildLocalRegisterReq(ctx actor.Context, pkg localPackageFiles, zipData []byte) (req gen.AppManagerRegisterReq, deferred bool, backend backendReport, oldRecord appRecord, hadOld bool, stagedZip string, err error) {
	registerReq := gen.AppManagerRegisterReq{
		Manifest:          pkg.Manifest,
		EntryModule:       pkg.EntryModule,
		Modules:           pkg.Modules,
		Assets:            pkg.Assets,
		SchemaDescriptors: pkg.SchemaDescriptors,
		Origin:            "user",
	}
	// Read the pre-existing record (if any) so the caller can GC the old
	// package only after the new registration commits.
	a.withMu(func() {
		oldRecord, hadOld = a.Records[pkg.Manifest.ID]
	})

	// Failure cleanup: remove files staged by this attempt. stagedZip and
	// stagedArtifact may be empty (nothing staged yet).
	var stagedArtifact string
	fail := func(err error) (gen.AppManagerRegisterReq, bool, backendReport, appRecord, bool, string, error) {
		if stagedZip != "" {
			_ = os.Remove(stagedZip)
		}
		if stagedArtifact != "" {
			_ = os.Remove(stagedArtifact)
		}
		return gen.AppManagerRegisterReq{}, false, backendReport{}, appRecord{}, false, "", err
	}

	// Stage the zip into the content-addressed packages inventory first (all
	// runtimes). The raw zip bytes (PACKAGE.sig preserved) are stored
	// verbatim when the source was a zip; directory installs are assembled
	// into a canonical unsigned zip below.
	if len(zipData) > 0 {
		zipPath, err := storePackageZipHashed(pkg.Manifest.ID, sha256Hex(zipData), zipData)
		if err != nil {
			return fail(err)
		}
		stagedZip = zipPath
		registerReq.PackagePath = zipPath
	}

	if pkg.Manifest.Runtime == "native" {
		pluginRef, ok := ctx.LookupService(pluginhostServiceName)
		if !ok || pluginRef == nil || ctx.Planner() == nil {
			return fail(fmt.Errorf("pluginhost service not available"))
		}

		// Store the artifact under its content address in the artifacts
		// inventory (reusing an existing file when the same app+hash is
		// already present) instead of an OS temp file, so the recorded
		// ArtifactPath survives restarts and updates can GC the old file.
		sum := sha256.Sum256(pkg.Artifact)
		artifactHash := hex.EncodeToString(sum[:])
		artifactPath, err := storeNativeArtifact(pkg.Manifest.ID, artifactHash, pkg.ArtifactName, pkg.Artifact)
		if err != nil {
			return fail(err)
		}
		stagedArtifact = artifactPath
		registerReq.ArtifactPath = artifactPath
		registerReq.ArtifactHash = artifactHash
		registerReq.Abi = pkg.Abi

		// Load boundary: pull stopped native dependencies up first, same as
		// register_project / plugin_load.
		if err := a.ensureDependenciesLoaded(ctx, pkg.Manifest.ID, pkg.Manifest); err != nil {
			return fail(err)
		}
		authManifest, err := a.loadAuthManifest(pkg.Manifest)
		if err != nil {
			return fail(fmt.Errorf("native artifact permissions: %w", err))
		}
		loadReq, backendSecret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
			Manifest:     authManifest,
			Abi:          *pkg.Abi,
			ArtifactPath: artifactPath,
			ArtifactHash: artifactHash,
		})
		if err != nil {
			return fail(err)
		}
		loadValue, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", loadReq).Await()
		if err != nil {
			return fail(fmt.Errorf("native artifact load: %w", err))
		}
		loaded, ok := loadValue.(gen.PluginArtifactLoadResp)
		if !ok || loaded.PluginID != pkg.Manifest.ID {
			_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
			return fail(fmt.Errorf("native artifact load returned invalid status"))
		}

		registerReq.ArtifactHash = loaded.ArtifactHash
		packageHash, err := canonicalPackageHash(
			pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, pkg.Abi, registerReq.ArtifactHash,
		)
		if err != nil {
			return fail(fmt.Errorf("compute package hash: %w", err))
		}
		registerReq.PackageHash = packageHash
		// Materialize the frontend assets at the installed app dir: the running
		// plugin's own HTTP listener serves that directory as its static root
		// (the LoadConfig.StaticDir set by withBackendLoadReq). Project-
		// registered apps serve from their source tree; installed apps have no
		// source tree, so the asset bundle must land on disk (the stored zip
		// stays the authority — this directory is a derived projection).
		if err := materializeInstalledAssets(pkg.Manifest.ID, pkg.Assets); err != nil {
			return fail(err)
		}
		// Directory installs carry no zip: assemble a canonical unsigned zip
		// (manifest/modules/assets + abi.json + artifact) so the inventory
		// holds a runnable archive for native dir installs too.
		if len(zipData) == 0 {
			unsignedZip, zipErr := buildExportZip(pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, pkg.Abi, pkg.ArtifactName, pkg.Artifact, nil, nil)
			if zipErr != nil {
				return fail(zipErr)
			}
			zipPath, zipErr := storePackageZipHashed(pkg.Manifest.ID, sha256Hex(unsignedZip), unsignedZip)
			if zipErr != nil {
				return fail(zipErr)
			}
			stagedZip = zipPath
			registerReq.PackagePath = zipPath
		}
		// The artifact was stored under the byte hash; the load response may
		// carry a different (e.g. pluginhost-side) hash. Rename the file to
		// the record hash only when they differ, keeping the content address
		// truthful. When they match the file is already at the right name.
		if !strings.EqualFold(artifactHash, registerReq.ArtifactHash) {
			finalPath := artifactFilePath(pkg.Manifest.ID, registerReq.ArtifactHash, pkg.ArtifactName)
			if _, statErr := os.Stat(finalPath); statErr != nil {
				if renameErr := os.Rename(artifactPath, finalPath); renameErr == nil {
					artifactPath = finalPath
					registerReq.ArtifactPath = finalPath
					stagedArtifact = finalPath
				}
			}
		}
		if loaded.Status.State == stateRestartPending {
			// In-process deferral: the pluginhost persisted the new artifact
			// for restart activation and reported restart_pending instead of
			// an error. Keep the staged files (they are now the persisted
			// artifact/package paths) and register with the deferred flag so
			// the record lands in restart_pending rather than spawning.
			return registerReq, true, backendReport{secret: backendSecret, httpAddr: loaded.HttpAddr}, oldRecord, hadOld, stagedZip, nil
		}
		return registerReq, false, backendReport{secret: backendSecret, httpAddr: loaded.HttpAddr}, oldRecord, hadOld, stagedZip, nil
	}

	packageHash, err := canonicalPackageHash(
		pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, pkg.Abi, registerReq.ArtifactHash,
	)
	if err != nil {
		return fail(fmt.Errorf("compute package hash: %w", err))
	}
	registerReq.PackageHash = packageHash
	// Directory installs carry no zip: assemble a canonical unsigned zip from
	// the parsed package (the same assembly app_export uses, minus the
	// signature sidecars) so the update/GC bookkeeping has one shape and the
	// inventory always holds a runnable archive.
	if len(zipData) == 0 {
		unsignedZip, zipErr := buildExportZip(pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, pkg.Abi, pkg.ArtifactName, pkg.Artifact, nil, nil)
		if zipErr != nil {
			return fail(zipErr)
		}
		zipPath, zipErr := storePackageZipHashed(pkg.Manifest.ID, sha256Hex(unsignedZip), unsignedZip)
		if zipErr != nil {
			return fail(zipErr)
		}
		stagedZip = zipPath
		registerReq.PackagePath = zipPath
	}

	return registerReq, false, backendReport{}, oldRecord, hadOld, stagedZip, nil
}

// materializeInstalledAssets projects an installed (zip/dir) package's asset
// bundle onto disk at installedAppDir(appID): the directory the plugin's own
// HTTP listener serves as its static root (LoadConfig.StaticDir) and the anchor
// for the app.data grant. Project-registered apps serve straight from their
// source tree and never call this.
//
// The stored install zip is the authority (this directory can always be
// rebuilt from it), so the tree is a derived projection: it is rewritten on
// every install and stale files from a previous install are cleared first, so
// a shrunk bundle does not keep serving retired files through the gateway.
func materializeInstalledAssets(appID string, assets map[string][]byte) error {
	if appID == "" || len(assets) == 0 {
		return nil
	}
	dir := installedAppDir(appID)
	if !inventoryOwned(dir) {
		return fmt.Errorf("appmanager: installed app dir %q is outside the inventory root", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("appmanager: mkdir installed app dir: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("appmanager: read installed app dir: %w", err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return fmt.Errorf("appmanager: clear installed app dir: %w", err)
		}
	}
	for name, content := range assets {
		rel, ok := sanitizeAssetRelPath(name)
		if !ok {
			continue
		}
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("appmanager: mkdir %q: %w", filepath.Dir(full), err)
		}
		if err := persist.WriteFileAtomic(full, content, 0o644); err != nil {
			return fmt.Errorf("appmanager: write asset %q: %w", full, err)
		}
	}
	return nil
}

// sanitizeAssetRelPath normalizes a package-relative asset path to a clean,
// traversal-free slash path. Backslashes are normalized first so a Windows
// source cannot smuggle "..\\.." past the check. Empty, root, and escaping
// names are rejected (skipped by the caller).
func sanitizeAssetRelPath(name string) (string, bool) {
	p := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." || strings.HasPrefix(p, "../") {
		return "", false
	}
	return p, true
}
