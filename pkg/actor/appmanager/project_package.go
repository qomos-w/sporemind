package appmanager

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

const canonicalPackageHashVersion = "sporemind.app-package.v2"

// descriptorFile is the dev_generate artifact carrying schema descriptors
// (pkg/codegen FileDescriptorsJSON).
const descriptorFile = "app.descriptors.json"

// excludedAssets lists generated/build artifacts that are not runtime assets.
// They must not be packaged and HTTP-exposed (which would leak dependency
// lists and churn packageHash). Real assets like index.html stay packaged.
var excludedAssets = map[string]bool{
	"go.sum":               true,
	"client.gen.ts":        true,
	"main.gen.go":          true,
	"schemas_gen.go":       true,
	"app.descriptors.json": true,
	".appdef":              true,
	"go.work":              true,
	"go.work.sum":          true,
}

// excludedAssetDirs names directory subtrees that never contain runtime
// assets or source modules. The project.list call below runs with
// NoIgnore=true, so dependency trees (frontend/node_modules, vendor) under
// the app dir are listed and would otherwise be swept wholesale into the
// package — megabytes of toolchain binaries that bloat the appmanager state
// document on every Save. Mirrors the walk skip in install_local.go.
var excludedAssetDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	// The vendored plugin SDK is generated material (a copy of the host SDK
	// re-materialized by dev_generate / sdk_vendor) — never runtime assets
	// nor agent source modules. Its .go files would otherwise be swept into
	// Modules wholesale.
	"vendor-sdk": true,
}

// nativeArtifactExts are the file extensions recognized as native plugin
// binaries by install_local's extractNativeArtifact. Directory-scoped legacy
// build outputs with these extensions must never be packaged as assets: the
// authoritative artifact comes from pluginhost.native_build and is added to
// the package separately; any stray binary swept in as an asset becomes a
// second artifact candidate and the install rejects the package with
// "multiple artifact binaries".
var nativeArtifactExts = map[string]bool{
	".exe":   true,
	".dll":   true,
	".so":    true,
	".dylib": true,
}

// isExcludedAssetPath reports whether rel (a "/"-separated path relative to
// the app dir) passes through an excluded directory.
func isExcludedAssetPath(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if excludedAssetDirs[seg] {
			return true
		}
	}
	return false
}

func canonicalPackageHash(manifest gen.AppManifest, entryModule string, modules map[string]string, assets map[string][]byte, descriptors map[string]gen.AppObjectDescriptor, abi *gen.PluginAbi, artifactHash string) (string, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("canonical package manifest: %w", err)
	}
	descriptorJSON, err := json.Marshal(descriptors)
	if err != nil {
		return "", fmt.Errorf("canonical package schema descriptors: %w", err)
	}
	abiJSON, err := json.Marshal(abi)
	if err != nil {
		return "", fmt.Errorf("canonical package ABI: %w", err)
	}
	moduleKeys := make([]string, 0, len(modules))
	for key := range modules {
		moduleKeys = append(moduleKeys, key)
	}
	sort.Strings(moduleKeys)
	assetKeys := make([]string, 0, len(assets))
	for key := range assets {
		assetKeys = append(assetKeys, key)
	}
	sort.Strings(assetKeys)
	h := sha256.New()
	write := func(value []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		h.Write(size[:])
		h.Write(value)
	}
	write([]byte(canonicalPackageHashVersion))
	write(manifestJSON)
	write([]byte(entryModule))
	for _, key := range moduleKeys {
		write([]byte(key))
		write([]byte(modules[key]))
	}
	for _, key := range assetKeys {
		write([]byte(key))
		write(assets[key])
	}
	write(descriptorJSON)
	write(abiJSON)
	write([]byte(strings.ToLower(artifactHash)))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// handleProjectPackage assembles an opaque App package by reading through the
// project actor. AppManager never reaches into project filesystem state.
// AppDir optionally scopes all reads to a subdirectory of the project root.
func (a *Actor) handleProjectPackage(ctx actor.Context, req gen.AppManagerProjectPackageReq) (gen.AppManagerProjectPackageResp, error) {
	if strings.TrimSpace(req.ProjectID) == "" {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: project id is required")
	}
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: invalid AppDir: %w", err)
	}
	canonical, err := identity.ParseCanonicalID(req.ProjectID)
	if err != nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: invalid project id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: project %q not found", req.ProjectID)
	}
	planner := ctx.Planner()
	if planner == nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: planner not available")
	}
	call := func(name string, payload any) (any, error) {
		v, err := planner.Call(ctx.Lifecycle(), projectRef, name, payload).Await()
		if err != nil {
			return nil, err
		}
		return v, nil
	}
	// Effective root: the caller's worktree checkout when the calling agent
	// is worktree-bound (all package reads below then target the worktree via
	// absolute paths), otherwise the main-tree root from project.info.
	st, err := a.resolveDevRootState(ctx, planner, projectRef, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: resolve project root: %w", err)
	}
	root := st.root
	// App directory: root when AppDir is empty, otherwise root/AppDir.
	appRoot := root
	if appDir != "" {
		appRoot = root + "/" + appDir
	}
	readAsset := func(path string) ([]byte, error) {
		v, err := call("project.read_base64", gen.FileSystemReadBase64Req{Path: path})
		if err != nil {
			return nil, err
		}
		resp, ok := v.(gen.FileSystemReadBase64Resp)
		if !ok {
			return nil, fmt.Errorf("unexpected project.read_base64 response")
		}
		return base64.StdEncoding.DecodeString(resp.Content)
	}
	// Byte-exact text read via read_base64: project.read applies an implicit
	// 2000-line cap meant for interactive display and silently truncates
	// anything longer (Truncated flag), so a pretty-printed
	// app.descriptors.json or a long .go module was cut mid-document and
	// then failed to decode. read_base64 has a 16MB byte cap and no line
	// semantics.
	read := func(path string) (string, error) {
		b, err := readAsset(path)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	manifestPath := appRoot + "/app.manifest.json"
	manifestText, err := read(manifestPath)
	if err != nil {
		// Subdirectory apps hit this whenever AppDir is omitted: the manifest
		// lives under root/<AppDir>, not the project root. Say so instead of a
		// bare not-found pointing at the root (BP-4 from the external run).
		if appDir == "" {
			return gen.AppManagerProjectPackageResp{}, fmt.Errorf(
				"appmanager: read manifest: %w (no app.manifest.json at the project root; if this app lives in a subdirectory, pass AppDir)",
				err)
		}
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: read manifest: %w", err)
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal([]byte(manifestText), &manifest); err != nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: decode manifest: %w", err)
	}
	if req.AppID != "" && req.AppID != manifest.ID {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: requested app id %q does not match manifest %q", req.AppID, manifest.ID)
	}
	entry := req.EntryModule
	if entry == "" {
		if manifest.Runtime == "native" {
			entry = "main.gen.go"
		} else {
			entry = "main.spore"
		}
	}
	entryPath := appRoot + "/" + entry
	entryText, err := read(entryPath)
	if err != nil {
		return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: read entry module %q: %w", entry, err)
	}
	modules := map[string]string{entry: entryText}
	assets := map[string][]byte{}
	listValue, err := call("project.list", gen.FileSystemListReq{Path: appRoot, Depth: -1, NoIgnore: true})
	if err == nil {
		if list, ok := listValue.(string); ok {
			for _, item := range parseListLines(list) {
				if item.isDir || item.name == "app.manifest.json" || item.name == entry || strings.HasPrefix(item.name, ".") {
					continue
				}
				if isExcludedAssetPath(item.name) {
					continue
				}
				if strings.HasSuffix(item.name, ".spore") || strings.HasSuffix(item.name, ".go") {
					source, readErr := read(appRoot + "/" + item.name)
					if readErr != nil {
						return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: read module %q: %w", item.name, readErr)
					}
					if source != "" {
						modules[item.name] = source
					}
					continue
				}
				if excludedAssets[item.name] {
					continue
				}
				// Stray native binaries (legacy build outputs committed next
				// to the app) are not runtime assets — packaging one makes the
				// installed zip carry multiple artifact candidates and the
				// install is rejected. The authoritative binary is built by
				// native_build and packaged separately.
				if nativeArtifactExts[strings.ToLower(filepath.Ext(item.name))] {
					continue
				}
				asset, readErr := readAsset(appRoot + "/" + item.name)
				if readErr != nil {
					return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: read asset %q: %w", item.name, readErr)
				}
				assets[item.name] = asset
			}
		}
	}
	// Schema descriptors emitted by dev_generate (app.descriptors.json).
	// Absent for apps whose callable schemas are not plain declared structs
	// (legacy no-refs passthrough) or apps generated before the artifact
	// existed.
	var descriptors map[string]gen.AppObjectDescriptor
	if descriptorText, rerr := read(appRoot + "/" + descriptorFile); rerr == nil && strings.TrimSpace(descriptorText) != "" {
		if uerr := json.Unmarshal([]byte(descriptorText), &descriptors); uerr != nil {
			return gen.AppManagerProjectPackageResp{}, fmt.Errorf("appmanager: decode %s: %w", descriptorFile, uerr)
		}
	} else if rerr == nil {
		descriptors = nil
	}
	packageHash, err := canonicalPackageHash(manifest, entry, modules, assets, descriptors, nil, "")
	if err != nil {
		return gen.AppManagerProjectPackageResp{}, err
	}
	return gen.AppManagerProjectPackageResp{Manifest: manifest, EntryModule: entry, Modules: modules, Assets: assets, SchemaDescriptors: descriptors, PackageHash: packageHash}, nil
}

func (a *Actor) finishProjectRegistration(ctx actor.Context, registerReq gen.AppManagerRegisterReq, nativePluginRef ref.Ref, nativeLoaded bool, deferred bool) (gen.AppManagerRegisterProjectResp, error) {
	appStatus, err := a.doRegister(ctx, registerReq, deferred)
	if err != nil {
		// Roll the artifact load back on any registration failure. Leaving it
		// loaded leaks a half-registered plugin: no registry record exists (so
		// plugin_unload / retry_cleanup answer "not registered"), yet a retry
		// register with changed source hits "already loaded with a different
		// artifact" — an uncleanable state until host restart (round-9
		// breakpoint #2). The retry rebuilds deterministically anyway.
		if nativeLoaded {
			_, rollbackErr := ctx.Planner().Call(ctx.Lifecycle(), nativePluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: registerReq.Manifest.ID}).Await()
			if rollbackErr != nil {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: register: %w; native rollback: %v", err, rollbackErr)
			}
		} else if deferred {
			// The in-process host deferred this artifact (restart_pending):
			// nothing was loaded by this registration, but the pluginhost
			// ArtifactLoads was repointed at the new artifact. Repoint it
			// back at the previous record's artifact so a later restart does
			// not activate a registration that failed its gates. Best-effort.
			var old appRecord
			a.withMu(func() {
				old = a.Records[registerReq.Manifest.ID]
			})
			if old.ArtifactPath != "" && old.Abi != nil {
				// Best-effort rollback: re-expand the old manifest's bundle
				// permissions; if a dependency vanished mid-registration the
				// declared form is still better than leaving the staged new
				// artifact pointing at a failed registration.
				rollbackManifest := old.Manifest
				if expanded, expErr := a.loadAuthManifest(old.Manifest); expErr == nil {
					rollbackManifest = expanded
				}
				rollbackReq, rollbackSecret, backendErr := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
					Manifest: rollbackManifest, Abi: *old.Abi, ArtifactPath: old.ArtifactPath, ArtifactHash: old.ArtifactHash,
				})
				if backendErr == nil {
					if rollbackValue, rollbackErr := ctx.Planner().Call(ctx.Lifecycle(), nativePluginRef, "pluginhost.artifact_load", rollbackReq).Await(); rollbackErr == nil {
						if rollbackLoaded, rollbackOK := rollbackValue.(gen.PluginArtifactLoadResp); rollbackOK {
							// The rolled-back respawn reports a fresh
							// ephemeral port: adopt it (or clear the backend
							// when no listener runs).
							a.commitBackend(ctx, registerReq.Manifest.ID, rollbackSecret, rollbackLoaded.HttpAddr)
						}
					}
				}
			}
		}
		return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: register: %w", err)
	}
	return gen.AppManagerRegisterProjectResp{Status: appStatus}, nil
}

// handleRegisterProject combines project_package + register into one callable.
// AppDir optionally scopes package reads, the native build, and the inline
// gates to a subdirectory of the project root.
func (a *Actor) handleRegisterProject(ctx actor.Context, req gen.AppManagerRegisterProjectReq) (gen.AppManagerRegisterProjectResp, error) {
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: invalid AppDir: %w", err)
	}
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerRegisterProjectResp{}, err
	}
	req.ProjectID = projectID
	canonicalProjectID, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: invalid project id: %w%s", err, availableProjectsHint(ctx))
	}
	// Resolve the effective root once at handler level: the caller's
	// worktree checkout when worktree-bound (native_build's SourceRoot then
	// compiles inside the isolation fence), otherwise the main-tree root.
	// Also drives the bound-caller warning on success. handleProjectPackage
	// resolves the same state from CallerAgentID for its reads.
	var st devRootState
	if projectRef, ok := ctx.LookupID(id.From(canonicalProjectID)); ok && projectRef != nil {
		if planner := ctx.Planner(); planner != nil {
			st, err = a.resolveDevRootState(ctx, planner, projectRef, req.CallerAgentID)
			if err != nil {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: resolve project root: %w", err)
			}
		}
	}
	pkg, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{
		ProjectID:     req.ProjectID,
		AppID:         req.AppID,
		EntryModule:   req.EntryModule,
		AppDir:        appDir,
		CallerAgentID: req.CallerAgentID,
	})
	if err != nil {
		return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: read project package: %w", err)
	}
	registerReq := gen.AppManagerRegisterReq{
		Manifest: pkg.Manifest, EntryModule: pkg.EntryModule, Modules: pkg.Modules, Assets: pkg.Assets, SchemaDescriptors: pkg.SchemaDescriptors,
		PackageHash: pkg.PackageHash, Origin: "project",
	}
	nativeLoaded := false
	deferred := false
	// registerBackend carries the per-instance secret + listener address of
	// the native artifact load below; committed onto the record after
	// finishProjectRegistration lands it.
	registerBackend := backendReport{}
	var nativePluginRef ref.Ref
	if pkg.Manifest.Runtime == "native" {
		pluginRef, ok := ctx.LookupService(pluginhostServiceName)
		if !ok || pluginRef == nil || ctx.Planner() == nil {
			return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: pluginhost service not available")
		}
		nativePluginRef = pluginRef

		projectRef, ok := ctx.LookupID(id.From(canonicalProjectID))
		if !ok || projectRef == nil {
			return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: project %q not found", req.ProjectID)
		}
		root := st.root
		// App directory: root when AppDir is empty, otherwise root/AppDir.
		appRoot := root
		if appDir != "" {
			appRoot = root + "/" + appDir
		}
		pc := newProjectCaller(ctx, projectRef)
		appdefContent, appdefHash, hasAppdef := resolveSingleAppDefOptional(pc, appRoot)

		// If the plugin is already loaded (e.g. re-registration after a
		// previous register or dev_gate), artifact_load is now idempotent
		// (returns the existing record). We still build to get the ABI
		// and artifact path for registerReq, but the load won't fail.
		var build gen.NativeBuildResp
		var loaded gen.PluginArtifactLoadResp
		{
			buildValue, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.native_build", gen.NativeBuildReq{
				ProjectID: req.ProjectID, AppID: pkg.Manifest.ID, EntryModule: pkg.EntryModule, AppDir: appDir, SourceRoot: root,
			}).Await()
			if err != nil {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: native build: %w", err)
			}
			build, ok = buildValue.(gen.NativeBuildResp)
			if !ok || !build.Result.Success {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: native build returned invalid result")
			}
			// Register is a load boundary like plugin_load: pull
			// registered-but-stopped native dependencies up in dependency
			// order so the pluginhost's dependency gate cannot dead-end the
			// first registration of an app whose dep is stopped.
			if err := a.ensureDependenciesLoaded(ctx, pkg.Manifest.ID, pkg.Manifest); err != nil {
				return gen.AppManagerRegisterProjectResp{}, err
			}
			authManifest, err := a.loadAuthManifest(pkg.Manifest)
			if err != nil {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: native artifact permissions: %w", err)
			}
			loadReq, backendSecret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
				Manifest: authManifest, Abi: build.Abi, ArtifactPath: build.Result.ArtifactPath, ArtifactHash: build.Result.ArtifactHash,
			})
			if err != nil {
				return gen.AppManagerRegisterProjectResp{}, err
			}
			loadValue, err := a.loadArtifactWithReplace(ctx, pluginRef, loadReq)
			if err != nil {
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: native artifact load: %w", err)
			}
			loaded, ok = loadValue.(gen.PluginArtifactLoadResp)
			if !ok || loaded.PluginID != pkg.Manifest.ID {
				_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: native artifact load returned invalid status")
			}
			// The subprocess confirmed its HTTP listener at OnLoad: stash the
			// per-instance backend so it can be committed onto the record after
			// finishProjectRegistration lands it (the load precedes doRegister).
			registerBackend = backendReport{secret: backendSecret, httpAddr: loaded.HttpAddr}
			// The freshly built exe supersedes older content-addressed builds
			// of this app (any version) in the shared .sporecode/build dir.
			if n := a.gcSupersededArtifacts(build.Result.ArtifactPath, pluginhost.PluginArtifactSweepPrefix(pkg.Manifest.Name)); n > 0 {
				slog.Info("appmanager: removed superseded plugin artifacts", "app", pkg.Manifest.ID, "files", n)
			}
			// In-process deferral (A1): the host could not swap the artifact
			// (unload-pending / different artifact loaded); the load response
			// reports restart_pending and the new artifact is persisted in
			// pluginhost ArtifactLoads for restart activation. Nothing was
			// loaded, so no unload compensation on later failures.
			deferred = loaded.Status.State == stateRestartPending
			if deferred {
				nativeLoaded = false
			} else {
				nativeLoaded = true
			}
		}

		// Run the five gates inline when .appdef exists. Gate checks are
		// skipped for legacy projects without .appdef files.
		if hasAppdef {
			sourceFiles, err := pc.collectGoSourceFiles(appRoot)
			if err != nil {
				_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: collect source files: %w", err)
			}
			var generatedManifest codegen.GeneratedManifest
			var hasGeneratedManifest bool
			a.withMu(func() {
				generatedManifest, hasGeneratedManifest = a.GeneratedManifests[generatedManifestKey(req.ProjectID, appDir, st.worktreePath)]
			})
			registeredCallables, err := listPluginCallables(ctx, pluginRef, pkg.Manifest.ID)
			if err != nil {
				_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
				return gen.AppManagerRegisterProjectResp{}, fmt.Errorf("appmanager: list plugin callables: %w", err)
			}
			gateDeps := GateDeps{
				SourceFiles:          sourceFiles,
				AppDefContent:        appdefContent,
				AppDefHash:           appdefHash,
				Manifest:             pkg.Manifest,
				GeneratedManifest:    &generatedManifest,
				BuildResult:          build.Result,
				RegisteredCallables:  registeredCallables,
				BundleSnapshots:      generatedManifest.BundleSnapshots,
				HasGeneratedManifest: hasGeneratedManifest,
			}
			gateDeps.VendorStamp, gateDeps.HostSDKDigest = collectVendorGateInputs(root, appRoot)
			gateDeps.FrontendFiles = collectFrontendGateInputs(appRoot)
			gateDeps.ExpectedBundleSnapshots, gateDeps.BundleDriftDiagnostic = a.computeBundleDrift(appdefContent)
			report := RunAllGates(gateDeps)
			if !report.Passed {
				_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
				return gen.AppManagerRegisterProjectResp{}, gateFailureError(report)
			}
		}

		registerReq.ArtifactPath = build.Result.ArtifactPath
		registerReq.ArtifactHash = loaded.ArtifactHash
		registerReq.Abi = &build.Abi
		registerReq.PackageHash, err = canonicalPackageHash(pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, &build.Abi, loaded.ArtifactHash)
		if err != nil {
			_, _ = ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pkg.Manifest.ID}).Await()
			return gen.AppManagerRegisterProjectResp{}, err
		}
	}
	resp, err := a.finishProjectRegistration(ctx, registerReq, nativePluginRef, nativeLoaded, deferred)
	if err != nil {
		return gen.AppManagerRegisterProjectResp{}, err
	}
	// Persist the project binding (record.proj) now that the record exists:
	// later loads/reloads embed it in the OnLoad config, where the
	// pluginhost reads it back to route project.wiki_* host calls.
	a.recordProjectBinding(ctx, registerReq.Manifest.ID, projectID)
	if registerBackend.hasBackend() {
		a.commitBackend(ctx, registerReq.Manifest.ID, registerBackend.secret, registerBackend.httpAddr)
	}
	if st.bound {
		scope := "packaged content"
		if pkg.Manifest.Runtime == "native" {
			scope = "packaged content and native artifact"
		}
		resp.Warnings = append(resp.Warnings, fmt.Sprintf(
			"registered from worktree %s: the %s was read from the worktree checkout and is valid until worktree merge/exit; re-run appmanager.register_project after merge to activate the main-tree build",
			st.worktreePath, scope))
	}
	return resp, nil
}

// loadArtifactWithReplace loads a freshly built artifact, auto-replacing an
// already-loaded artifact for the same plugin ID when it differs. Registering
// an app whose ID already exists is a replace intent (the register gate
// already carried the user's authorization), so instead of dead-ending with
// "different artifact; unload first" the old artifact is unloaded and the
// load retried once. The idempotent fast path is preserved: an unchanged
// artifact loads without any swap. In-process conflicts never reach this
// retry — the pluginhost converts them to a restart_pending response, which
// the caller handles as deferred activation.
func (a *Actor) loadArtifactWithReplace(ctx actor.Context, pluginRef ref.Ref, req gen.PluginArtifactLoadReq) (any, error) {
	load := func() (any, error) {
		return ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", req).Await()
	}
	value, err := load()
	if err == nil || !pluginhost.IsArtifactConflictError(err) {
		return value, err
	}
	if _, unloadErr := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID}).Await(); unloadErr != nil {
		return nil, fmt.Errorf("appmanager: replace existing artifact for %q (unload failed): %v (load: %w)", req.Manifest.ID, unloadErr, err)
	}
	a.auditLifecycle(req.Manifest.ID, "register", "auto-replaced differing artifact for same plugin id")
	value, err = load()
	if err != nil {
		return nil, fmt.Errorf("appmanager: artifact load after replacing existing artifact for %q: %w", req.Manifest.ID, err)
	}
	return value, nil
}

// publishRegisteredBundleCards is retained as a no-op seam: app bundles are
// virtual components projected from the registry (see bundle_cards.go), so
// registration no longer writes any card entity.
// gateFailureError converts a failed GateReport into a single error carrying
// structured diagnostics. The message names the failing gate(s), what differs,
// and how to fix each problem.
func gateFailureError(report GateReport) error {
	var parts []string
	for _, r := range report.Results {
		if r.Passed || r.Error == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s (fix: %s)", r.Error.Gate, r.Error.Detail, r.Error.Fix))
	}
	return fmt.Errorf("registration gate check failed: %s", strings.Join(parts, "; "))
}

// handleReloadProject combines project_package + reload into one callable.
func (a *Actor) handleReloadProject(ctx actor.Context, req gen.AppManagerReloadProjectReq) (gen.AppManagerReloadProjectResp, error) {
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: invalid AppDir: %w", err)
	}
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gen.AppManagerReloadProjectResp{}, err
	}
	req.ProjectID = projectID
	canonicalProjectID, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: invalid project id: %w%s", err, availableProjectsHint(ctx))
	}
	// Effective root for the native build below: the caller's worktree
	// checkout when worktree-bound, otherwise the main-tree root.
	// handleProjectPackage resolves the same state from CallerAgentID for
	// its reads.
	var root string
	if projectRef, ok := ctx.LookupID(id.From(canonicalProjectID)); ok && projectRef != nil {
		if planner := ctx.Planner(); planner != nil {
			root, err = a.resolveDevRoot(ctx, planner, projectRef, req.CallerAgentID)
			if err != nil {
				return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: resolve project root: %w", err)
			}
		}
	}
	totalStart := time.Now()
	packageStart := time.Now()
	pkg, err := a.handleProjectPackage(ctx, gen.AppManagerProjectPackageReq{
		ProjectID:     projectID,
		AppID:         req.AppID,
		EntryModule:   req.EntryModule,
		AppDir:        appDir,
		CallerAgentID: req.CallerAgentID,
	})
	if err != nil {
		return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: read project package: %w", err)
	}
	// slog global, not ctx.Logger: phase timings must reach the persisted
	// log stream (ctx.Logger entries from this actor have been observed
	// missing from it; the global sink is the reliable channel).
	slog.Info("appmanager: reload phase", "app", req.AppID, "phase", "package", "ms", time.Since(packageStart).Milliseconds(), "modules", len(pkg.Modules), "assets", len(pkg.Assets))
	// Backfill the project binding BEFORE the reload builds the OnLoad
	// config (withBackendPrepareReq reads Records[].ProjectID): a record
	// registered before the field existed gets its binding embedded in this
	// same reload, not the next one. A failed reload rolls the record back
	// to the previous snapshot, undoing the patch.
	a.recordProjectBinding(ctx, req.AppID, projectID)
	reloadReq := gen.AppManagerReloadReq{
		ID: req.AppID, EntryModule: pkg.EntryModule, Modules: pkg.Modules, Assets: pkg.Assets, SchemaDescriptors: pkg.SchemaDescriptors, PackageHash: pkg.PackageHash,
		ExpectedStateVersion: req.ExpectedStateVersion, AgentID: req.AgentID, RequestID: req.RequestID,
		// The candidate manifest is the fresh package manifest for both
		// runtimes; the reload stores it alongside the descriptors so the
		// record's schema-hash pair stays consistent across restarts.
		CandidateManifest: &pkg.Manifest,
	}
	if pkg.Manifest.Runtime == "native" {
		pluginRef, ok := ctx.LookupService(pluginhostServiceName)
		if !ok || pluginRef == nil || ctx.Planner() == nil {
			return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: pluginhost service not available")
		}
		buildStart := time.Now()
		buildValue, buildErr := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.native_build", gen.NativeBuildReq{ProjectID: req.ProjectID, AppID: req.AppID, EntryModule: pkg.EntryModule, AppDir: appDir, SourceRoot: root, AgentID: req.AgentID, RequestID: req.RequestID}).Await()
		if buildErr == nil {
			slog.Info("appmanager: reload phase", "app", req.AppID, "phase", "native_build", "ms", time.Since(buildStart).Milliseconds())
		}
		if buildErr != nil {
			return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: native build: %w", buildErr)
		}
		build, ok := buildValue.(gen.NativeBuildResp)
		if !ok || !build.Result.Success {
			return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: native build failed")
		}
		packageHash, hashErr := canonicalPackageHash(pkg.Manifest, pkg.EntryModule, pkg.Modules, pkg.Assets, pkg.SchemaDescriptors, &build.Abi, build.Result.ArtifactHash)
		if hashErr != nil {
			return gen.AppManagerReloadProjectResp{}, hashErr
		}
		reloadReq.PackageHash = packageHash
		reloadReq.CandidateAbi = &build.Abi
		reloadReq.CandidateArtifactPath = build.Result.ArtifactPath
		reloadReq.CandidateArtifactHash = build.Result.ArtifactHash
	}
	resp, err := a.handleReload(ctx, reloadReq)
	if err != nil {
		return gen.AppManagerReloadProjectResp{}, fmt.Errorf("appmanager: reload: %w", err)
	}
	slog.Info("appmanager: reload phase", "app", req.AppID, "phase", "reload+total", "ms", time.Since(totalStart).Milliseconds())
	// Zip-install takeover (reload leg): reloading a zip-installed app from
	// the project directory supersedes the stored install zip — the record
	// still carries PackagePath pointing at it (reload never rewrites that
	// field). Clear the reference, remove the inventory zip, and sweep any
	// inventory artifact the reloaded record no longer references (the
	// candidate artifact lives in the project directory). Native artifacts
	// referenced by no surviving record are reclaimed by the sweep.
	if removed := a.clearSupersededZipAuthority(req.AppID, "reload_project"); removed != "" {
		a.gcUnreferencedArtifacts(req.AppID, "")
	}
	// App bundles are virtual components; reload swaps the registry record
	// and the new manifest's bundles project automatically.
	return gen.AppManagerReloadProjectResp{
		Status:       resp.Status,
		StateVersion: resp.StateVersion,
	}, nil
}
