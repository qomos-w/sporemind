package appmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleDevGate runs the five registration gates server-side. It is a
// diagnostic callable: if all gates pass it does not register the app, it just
// returns the report. The artifact is temporarily loaded to verify coverage and
// then unloaded.
func (a *Actor) handleDevGate(ctx actor.Context, req gen.AppManagerDevGateReq) (gen.AppManagerDevGateResp, error) {
	projectID, err := resolveDevProjectID(ctx, req.ProjectID, req.CallerAgentID)
	if err != nil {
		return gateErrorResponse(err.Error()), nil
	}
	appDir, err := codegen.NormalizeAppDir(req.AppDir)
	if err != nil {
		return gateErrorResponse(fmt.Sprintf("invalid AppDir: %v", err)), nil
	}
	canonical, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gateErrorResponse(fmt.Sprintf("invalid project id: %v%s", err, availableProjectsHint(ctx))), nil
	}
	projectRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || projectRef == nil {
		return gateErrorResponse(fmt.Sprintf("project %q not found", projectID)), nil
	}
	planner := ctx.Planner()
	if planner == nil {
		return gateErrorResponse("planner not available"), nil
	}
	st, err := a.resolveDevRootState(ctx, planner, projectRef, req.CallerAgentID)
	if err != nil {
		return gateErrorResponse(err.Error()), nil
	}
	root := st.root
	// App directory: root when AppDir is empty, otherwise root/AppDir.
	appRoot := root
	if appDir != "" {
		appRoot = root + "/" + appDir
	}

	pc := newProjectCaller(ctx, projectRef)

	// Resolve .appdef and app.manifest.json.
	appdefContent, appdefHash, err := pc.resolveSingleAppDef(appRoot)
	if err != nil {
		return gateErrorResponse(fmt.Sprintf("resolve .appdef: %v", err)), nil
	}

	manifestPath := appRoot + "/app.manifest.json"
	manifestText, err := pc.readText(manifestPath)
	if err != nil {
		return gateErrorResponse(fmt.Sprintf("read app.manifest.json: %v", err)), nil
	}
	var manifest gen.AppManifest
	if err := json.Unmarshal([]byte(manifestText), &manifest); err != nil {
		return gateErrorResponse(fmt.Sprintf("decode app.manifest.json: %v", err)), nil
	}

	// Collect .go source files for the stub-filling gate.
	sourceFiles, err := pc.collectGoSourceFiles(appRoot)
	if err != nil {
		return gateErrorResponse(fmt.Sprintf("collect source files: %v", err)), nil
	}

	// Look up the persisted generation record for drift detection.
	var generatedManifest codegen.GeneratedManifest
	var hasGeneratedManifest bool
	a.withMu(func() {
		generatedManifest, hasGeneratedManifest = a.GeneratedManifests[generatedManifestKey(projectID, appDir, st.worktreePath)]
	})

	// For native projects, build and temporarily load the artifact so the
	// coverage gate can inspect the dispatch registry. If the plugin is
	// already loaded (e.g. previously registered), reuse its existing
	// registration instead of load→unload cycling — the FreeLibrary path
	// crashes the host process on Windows (0xc0000005).
	var buildResult gen.NativeBuildResult
	var registeredCallables []string
	var loadDiagnostic string
	var unloadPluginID string
	if manifest.Runtime == "native" {
		pluginRef, ok := ctx.LookupService(pluginhostServiceName)
		if !ok || pluginRef == nil {
			return gateErrorResponse("pluginhost service not available"), nil
		}

		// Always build: the coverage gate is only meaningful when the
		// registry it inspects came from THIS build, and the freshness
		// check below needs the built artifact hash. (The previous fast
		// path skipped the build whenever the plugin was loaded and
		// compared the new manifest against the old running registry —
		// newly declared callables were then reported missing, a false
		// positive.)
		buildValue, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.native_build", gen.NativeBuildReq{
			ProjectID: projectID, AppID: manifest.ID, AppDir: appDir, SourceRoot: root,
		}).Await()
		var build gen.NativeBuildResp
		if err != nil {
			// Build gate will report the structured failure; still run the
			// other gates so the caller gets the complete picture.
			buildResult = gen.NativeBuildResult{Success: false, Diagnostic: err.Error()}
		} else if b, ok := buildValue.(gen.NativeBuildResp); ok {
			build = b
			buildResult = b.Result
		} else {
			buildResult = gen.NativeBuildResult{Success: false, Diagnostic: fmt.Sprintf("unexpected native_build response type %T", buildValue)}
		}

		if buildResult.Success {
			// Plugin already running: reuse its live registry instead of a
			// load→unload cycle against the running instance (the
			// FreeLibrary path crashes the host process on Windows,
			// 0xc0000005) — but only the appmanager record can prove the
			// running artifact IS this build. When it is not, the live
			// registry is stale and the diagnostic must name that root
			// cause instead of letting the gate diff against it.
			if existing, lerr := listPluginCallables(ctx, pluginRef, manifest.ID); lerr == nil && len(existing) > 0 {
				registeredCallables = existing
				loadDiagnostic = a.staleRegistryDiagnostic(manifest.ID, build.Result.ArtifactHash)
			} else {
				// Expand bundle permissions exactly like register_project would,
				// so the coverage measurement runs against the authorization
				// form production installs.
				gateManifest, expErr := a.loadAuthManifest(manifest)
				if expErr != nil {
					loadDiagnostic = fmt.Sprintf("bundle permission expansion failed: %v", expErr)
				} else {
					loadValue, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", gen.PluginArtifactLoadReq{
						Manifest:     gateManifest,
						Abi:          build.Abi,
						ArtifactPath: build.Result.ArtifactPath,
						ArtifactHash: build.Result.ArtifactHash,
					}).Await()
					if err != nil {
						// Record the load error so the coverage gate reports
						// the root cause instead of a misleading
						// empty-registry diff.
						registeredCallables = nil
						loadDiagnostic = err.Error()
					} else if loadResp, ok := loadValue.(gen.PluginArtifactLoadResp); ok {
						if loadResp.Status.State == stateRestartPending {
							// Deferred, not loaded: an older instance is
							// already running (the listing above raced it,
							// or the plugin exposes no callables). The gate
							// must not unload the live instance, and its
							// registry predates this build by definition.
							registeredCallables = nil
							loadDiagnostic = fmt.Sprintf("stale running artifact: %q is already running an older artifact; the new build is staged for the next host restart (restart_pending)", manifest.ID)
						} else {
							registeredCallables, err = listPluginCallables(ctx, pluginRef, manifest.ID)
							if err != nil {
								registeredCallables = nil
								loadDiagnostic = fmt.Sprintf("post-load callable listing failed: %v", err)
							}
							unloadPluginID = loadResp.PluginID
						}
					} else {
						loadDiagnostic = fmt.Sprintf("unexpected artifact_load response type %T", loadValue)
					}
				}
			}
		}
		if unloadPluginID != "" {
			defer func(ref ref.Ref, pid string) {
				// The unload handler's FreeLibrary path is a no-op on
				// Windows (see loaderOpener.Open); the records/handlers
				// teardown still runs and the library handle is kept.
				_, _ = planner.Call(ctx.Lifecycle(), ref, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: pid}).Await()
			}(pluginRef, unloadPluginID)
		}
	}

	deps := GateDeps{
		SourceFiles:          sourceFiles,
		AppDefContent:        appdefContent,
		AppDefHash:           appdefHash,
		Manifest:             manifest,
		GeneratedManifest:    &generatedManifest,
		BuildResult:          buildResult,
		RegisteredCallables:  registeredCallables,
		LoadDiagnostic:       loadDiagnostic,
		BundleSnapshots:      generatedManifest.BundleSnapshots,
		HasGeneratedManifest: hasGeneratedManifest,
	}
	deps.VendorStamp, deps.HostSDKDigest = collectVendorGateInputs(root, appRoot)
	deps.FrontendFiles = collectFrontendGateInputs(appRoot)
	deps.ExpectedBundleSnapshots, deps.BundleDriftDiagnostic = a.computeBundleDrift(appdefContent)
	report := RunAllGates(deps)
	return report.ToResponse(), nil
}

// computeBundleDrift re-resolves the appdef's bundle-level permissions
// against the CURRENT registry view, producing the drift gate's expected
// snapshots. Both outputs degrade independently: a non-empty diagnostic with
// nil snapshots fails the gate with the root cause instead of a diff.
func (a *Actor) computeBundleDrift(appdefContent string) ([]codegen.BundleSnapshot, string) {
	deps := a.bundleDepsSnapshot()
	if len(deps) == 0 {
		return nil, ""
	}
	snapshots, err := codegen.ComputeBundleSnapshots(appdefContent, deps)
	if err != nil {
		return nil, fmt.Sprintf("bundle permission re-resolution failed: %v", err)
	}
	return snapshots, ""
}

// collectVendorGateInputs gathers the vendor_freshness gate inputs: the app's
// vendored-SDK stamp and the current host SDK digest. Both are best-effort —
// empty returns mean "undecidable", which the gate treats as pass (never
// block registration on an unavailable SDK workspace).
func collectVendorGateInputs(root, appRoot string) (stamp, digest string) {
	stamp = codegen.ReadVendorStamp(appRoot)
	if stamp == "" {
		return "", ""
	}
	sdkPath, err := codegen.EnsureDevSDKWorkspace(root)
	if err != nil {
		return stamp, ""
	}
	digest, err = codegen.SDKDigest(sdkPath)
	if err != nil {
		return stamp, ""
	}
	return stamp, digest
}

// collectFrontendGateInputs gathers the app's top-level HTML files for the
// frontend_transport gate. Best-effort: read errors and a missing directory
// yield an empty map, which the gate treats as "nothing to lint".
func collectFrontendGateInputs(appRoot string) map[string]string {
	matches, err := filepath.Glob(appRoot + "/*.html")
	if err != nil || len(matches) == 0 {
		return nil
	}
	files := make(map[string]string, len(matches))
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		files[filepath.Base(m)] = string(data)
	}
	return files
}

// gateErrorResponse builds a response for failures that prevent any gate from
// running (e.g. missing project or unreadable files).
func gateErrorResponse(msg string) gen.AppManagerDevGateResp {
	return gen.AppManagerDevGateResp{Passed: false, Error: msg}
}

// listPluginCallables asks pluginhost for the loaded plugin descriptor and
// returns its registered callable ids.
func listPluginCallables(ctx actor.Context, pluginRef ref.Ref, pluginID string) ([]string, error) {
	planner := ctx.Planner()
	if planner == nil {
		return nil, fmt.Errorf("planner not available")
	}
	listValue, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.list_plugins", struct{}{}).Await()
	if err != nil {
		return nil, err
	}
	resp, ok := listValue.(pluginhostactor.ListPluginsResp)
	if !ok {
		return nil, fmt.Errorf("unexpected pluginhost.list_plugins response type %T", listValue)
	}
	for _, p := range resp.Plugins {
		if p.ID == pluginID {
			return p.Callables, nil
		}
	}
	return nil, fmt.Errorf("plugin %q not found in pluginhost list", pluginID)
}

// staleRegistryDiagnostic explains why the running plugin's dispatch
// registry cannot stand in for the freshly built artifact, or returns ""
// when reuse is sound. The coverage gate diffs the new manifest against
// this registry, so a stale registry reports newly declared callables as
// missing — a false positive. Freshness is judged from the appmanager
// record: restart_pending means the recorded artifact only activates on
// host restart (the running one is older by definition); otherwise the
// recorded hash is the running artifact's hash and must match the build.
func (a *Actor) staleRegistryDiagnostic(appID, builtHash string) string {
	var rec appRecord
	var ok bool
	a.withMu(func() {
		rec, ok = a.Records[appID]
	})
	if !ok {
		// Loaded outside appmanager (direct artifact_load): no record to
		// compare against, so reuse is all the gate has.
		return ""
	}
	if rec.State == stateRestartPending {
		return fmt.Sprintf("stale running artifact: a newer artifact for %q is staged but activates only on host restart (restart_pending); the running registry predates this build — restart the host, then re-run dev_gate", appID)
	}
	if builtHash != "" && rec.ArtifactHash != "" && !strings.EqualFold(rec.ArtifactHash, builtHash) {
		return fmt.Sprintf("stale running artifact: %q runs artifact %s but the gate built %s; run appmanager.reload_project to activate the new build, then re-run dev_gate", appID, rec.ArtifactHash, builtHash)
	}
	return ""
}
