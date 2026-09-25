package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// stateRestartPending is the deferred-load state: the new artifact is
// persisted (ArtifactLoads + appmanager record) and will be activated by the
// next host restart. It is a plain string value of AppStatus.State (free
// string per app._1900.spore:178-191), so no schema change is involved.
const stateRestartPending = "restart_pending"

// handleArtifactLoad exposes the ArtifactLoader as an AdminOnly callable.
// On success the plugin is loaded, handlers are installed, the descriptor is
// published and the resulting AppStatus is returned. Dependencies declared in
// the manifest are checked against already-loaded plugins; missing
// dependencies cause a structured error so the caller can load them first.
//
// In-process deferral: when the loader hits an in-process (c-shared) reload
// blocker — the plugin is unload-pending or already holds a different
// artifact — the new artifact cannot be swapped while the host lives. The
// loaded artifact request is persisted into ArtifactLoads so the next host
// restart activates it, and a successful response with Status.State
// "restart_pending" is returned instead of an error. The caller (appmanager)
// consumes the state field programmatically and records the same state.
func (a *Actor) handleArtifactLoad(ctx actor.Context, req gen.PluginArtifactLoadReq) (gen.PluginArtifactLoadResp, error) {
	if a.loader == nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: artifact loader not initialized")
	}
	// Validate that all declared dependencies are already loaded.
	if missing := a.missingDependencies(req.Manifest.Dependencies); len(missing) > 0 {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: dependencies not loaded for %q: %s", req.Manifest.ID, joinStrings(missing))
	}
	resp, err := a.loader.Load(ctx.Lifecycle(), req)
	if err != nil {
		if pluginhost.IsInProcessDeferredError(err) && req.Abi.Isolation == pluginhost.IsolationInProcess {
			return a.deferArtifactLoad(req)
		}
		return gen.PluginArtifactLoadResp{}, err
	}
	a.mu.Lock()
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	req.ArtifactHash = resp.ArtifactHash
	a.recordAppDataGrant(req.Manifest.ID, req.OnLoadConfig)
	a.ArtifactLoads[req.Manifest.ID] = req
	a.mu.Unlock()
	// Dev is sticky on the descriptor (panel-op gate): it must be set before
	// Save so the persisted Plugins list carries it across restarts.
	a.markPanelOpDev(req.Manifest.ID, req.Dev)
	if err := a.Save(); err != nil {
		_, _ = a.loader.Unload(ctx.Lifecycle(), gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID})
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: persist artifact load: %w", err)
	}
	return resp, nil
}

// deferArtifactLoad persists the candidate load request so the next host
// restart activates it, and reports restart_pending instead of an error.
// The old mapping stays loaded until then; nothing is unloaded.
func (a *Actor) deferArtifactLoad(req gen.PluginArtifactLoadReq) (gen.PluginArtifactLoadResp, error) {
	req.ArtifactHash = plausibleArtifactHash(req)
	a.mu.Lock()
	if a.ArtifactLoads == nil {
		a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{}
	}
	a.recordAppDataGrant(req.Manifest.ID, req.OnLoadConfig)
	a.ArtifactLoads[req.Manifest.ID] = req
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		return gen.PluginArtifactLoadResp{}, fmt.Errorf("pluginhost: persist deferred artifact load: %w", err)
	}
	return gen.PluginArtifactLoadResp{
		PluginID:     req.Manifest.ID,
		ArtifactHash: req.ArtifactHash,
		Status: gen.AppStatus{
			ID: req.Manifest.ID, Runtime: "native", State: stateRestartPending,
			Version: req.Manifest.Version, Namespace: req.Manifest.Namespace,
			ArtifactHash: req.ArtifactHash,
		},
	}, nil
}

// plausibleArtifactHash returns the caller-declared artifact hash when set,
// otherwise derives one from the artifact file on disk.
func plausibleArtifactHash(req gen.PluginArtifactLoadReq) string {
	if req.ArtifactHash != "" {
		return req.ArtifactHash
	}
	data, err := os.ReadFile(req.ArtifactPath)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (a *Actor) handleArtifactReloadPrepare(ctx actor.Context, req gen.PluginArtifactReloadPrepareReq) (gen.PluginArtifactReloadPrepareResp, error) {
	if a.loader == nil {
		return gen.PluginArtifactReloadPrepareResp{}, fmt.Errorf("pluginhost: artifact loader not initialized")
	}
	resp, err := a.loader.PrepareReload(ctx.Lifecycle(), req)
	if err != nil {
		return gen.PluginArtifactReloadPrepareResp{}, err
	}
	a.mu.Lock()
	// Carry OnLoadConfig into the staged candidate: it is persisted into
	// ArtifactLoads at commit time and replayed on host restart, so the
	// restored process must receive the same per-instance bootstrap payload.
	a.pendingArtifactReloads[resp.Token] = gen.PluginArtifactLoadReq{Manifest: req.Manifest, Abi: req.Abi, ArtifactPath: req.ArtifactPath, ArtifactHash: resp.ArtifactHash, EntrySymbol: req.EntrySymbol, OnLoadConfig: req.OnLoadConfig, AgentID: req.AgentID, RequestID: req.RequestID}
	// Stage the candidate asset bundle (nil = keep the installed bundle) so
	// the commit applies the artifact and asset swaps together.
	if req.Assets != nil {
		a.pendingArtifactAssets[resp.Token] = req.Assets
	}
	a.mu.Unlock()
	return resp, nil
}

func (a *Actor) handleArtifactReloadCommit(ctx actor.Context, req gen.PluginArtifactReloadCommitReq) (gen.PluginArtifactReloadCommitResp, error) {
	a.mu.Lock()
	candidate, ok := a.pendingArtifactReloads[req.Token]
	if !ok {
		a.mu.Unlock()
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: reload token not found")
	}
	pluginID := candidate.Manifest.ID
	assets, swapAssets := a.pendingArtifactAssets[req.Token]
	old, hadOld := a.ArtifactLoads[pluginID]
	a.ArtifactLoads[pluginID] = candidate
	// Stage the asset swap in the same persist window as the candidate
	// artifact so a crash cannot leave one side committed and the other not.
	oldAssets, hadOldAssets := a.AssetStores[pluginID]
	if swapAssets {
		if len(assets) == 0 {
			delete(a.AssetStores, pluginID)
		} else {
			a.AssetStores[pluginID] = assets
		}
	}
	a.mu.Unlock()
	restoreAssets := func() {
		a.mu.Lock()
		if hadOldAssets {
			a.AssetStores[pluginID] = oldAssets
		} else {
			delete(a.AssetStores, pluginID)
		}
		a.mu.Unlock()
	}
	if err := a.Save(); err != nil {
		a.mu.Lock()
		if hadOld {
			a.ArtifactLoads[pluginID] = old
		} else {
			delete(a.ArtifactLoads, pluginID)
		}
		a.mu.Unlock()
		restoreAssets()
		_, _ = a.loader.AbortReload(ctx.Lifecycle(), gen.PluginArtifactReloadAbortReq{Token: req.Token})
		return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: persist reload candidate: %w", err)
	}
	resp, err := a.loader.CommitReload(ctx.Lifecycle(), req)
	if err != nil {
		a.mu.Lock()
		if hadOld {
			a.ArtifactLoads[pluginID] = old
		} else {
			delete(a.ArtifactLoads, pluginID)
		}
		a.mu.Unlock()
		restoreAssets()
		if saveErr := a.Save(); saveErr != nil {
			return gen.PluginArtifactReloadCommitResp{}, fmt.Errorf("pluginhost: commit reload: %v; restore persisted artifact: %w", err, saveErr)
		}
		return gen.PluginArtifactReloadCommitResp{}, err
	}
	// Artifact swap committed — flip the router to the new bundle. An empty
	// candidate bundle removes the route entirely.
	if swapAssets {
		if len(assets) == 0 {
			a.dropAssetsRouter(pluginID)
		} else {
			a.applyAssetsRouter(pluginID, assets)
		}
	}
	a.mu.Lock()
	delete(a.pendingArtifactReloads, req.Token)
	delete(a.pendingArtifactAssets, req.Token)
	a.mu.Unlock()
	return resp, nil
}

func (a *Actor) handleArtifactReloadAbort(ctx actor.Context, req gen.PluginArtifactReloadAbortReq) (gen.PluginArtifactReloadAbortResp, error) {
	if a.loader == nil {
		return gen.PluginArtifactReloadAbortResp{}, fmt.Errorf("pluginhost: artifact loader not initialized")
	}
	resp, err := a.loader.AbortReload(ctx.Lifecycle(), req)
	if err != nil {
		return gen.PluginArtifactReloadAbortResp{}, err
	}
	a.mu.Lock()
	delete(a.pendingArtifactReloads, req.Token)
	delete(a.pendingArtifactAssets, req.Token)
	a.mu.Unlock()
	return resp, nil
}

// handleArtifactUnload removes a previously loaded native plugin and closes
// the library handle. Returns the number of handlers removed. The plugin's
// HTTP-served asset bundle (if any) is dropped together with the artifact so
// /plugin/{pluginId}/ returns 404 after unload.
func (a *Actor) handleArtifactUnload(ctx actor.Context, req gen.PluginArtifactUnloadReq) (gen.PluginArtifactUnloadResp, error) {
	if a.loader == nil {
		return gen.PluginArtifactUnloadResp{}, fmt.Errorf("pluginhost: artifact loader not initialized")
	}
	resp, err := a.loader.Unload(ctx.Lifecycle(), req)
	if err != nil {
		return gen.PluginArtifactUnloadResp{}, err
	}
	// Capture the manifest before purging ArtifactLoads so we can leave
	// tombstone handlers for every declared callable. Stopped plugins then
	// answer direct invokes with a clear "how to restart" message instead of
	// the generic "call ID not registered" error.
	a.mu.Lock()
	loadReq, loadReqExists := a.ArtifactLoads[req.PluginID]
	delete(a.ArtifactLoads, req.PluginID)
	delete(a.AssetStores, req.PluginID)
	a.mu.Unlock()
	if loadReqExists {
		a.registerTombstoneHandlers(req.PluginID, loadReq.Manifest.Callables)
	}
	// Dropping the asset bundle together with the artifact keeps the
	// gateway route lifecycle 1:1 with the plugin lifecycle: unload →
	// 404 (BP5 acceptance).
	a.dropAssetsRouter(req.PluginID)
	// An explicit unload must also clear the persisted descriptor. When the
	// loader had the plugin loaded it removes the descriptor itself
	// (Removed>0); when the runtime is not loaded — e.g. a restore-failed
	// zombie whose artifact file is gone — nothing else removes the stale
	// "error" entry and it would survive every restart.
	a.RemoveDescriptor(req.PluginID)
	// Drop the log ring and process verdict with the artifact: the plugin is
	// gone, and a later load starts from a clean capture surface.
	a.resetPluginLogs(req.PluginID)
	if err := a.Save(); err != nil {
		return gen.PluginArtifactUnloadResp{}, fmt.Errorf("pluginhost: persist artifact unload: %w", err)
	}
	return resp, nil
}

// registerTombstoneHandlers installs stub handlers for every callable declared
// by a stopped native plugin. Direct invokes hit a {"__host_error__": ...}
// envelope that tells the caller how to restart the plugin via
// appmanager.plugin_load. load/restore naturally overwrite these stubs with
// real handlers; unregister removes them via artifact_unload.
func (a *Actor) registerTombstoneHandlers(pluginID string, callables []gen.AppCallableDescriptor) {
	msg := fmt.Sprintf("plugin %s is stopped; call appmanager.plugin_load to start it", pluginID)
	env, _ := json.Marshal(map[string]string{"__host_error__": msg})
	for _, callable := range callables {
		callID := pluginhost.PluginCallID(pluginID, callable.ID)
		a.RegisterHandler(callID, func(_ context.Context, _ []byte) ([]byte, error) {
			return env, nil
		})
	}
}

// handleNativeBuild compiles a project source tree to a native artifact and
// validates the result. The project actor owns the source tree; this handler
// drives `go build` against it — a standalone executable (subprocess mode,
// the dev default) or a c-shared library generated from a staged copy plus
// the cgo shim (inprocess mode, release). The resulting ABI carries the same
// transport so registration and loading agree on the artifact kind.
// Cross-compilation is rejected with a stable diagnostic.
func (a *Actor) handleNativeBuild(ctx actor.Context, req gen.NativeBuildReq) (gen.NativeBuildResp, error) {
	if req.ProjectID == "" {
		return gen.NativeBuildResp{}, fmt.Errorf("pluginhost: project id is required")
	}
	if req.AppID == "" {
		return gen.NativeBuildResp{}, fmt.Errorf("pluginhost: app id is required")
	}
	projectRoot, manifestPath, err := a.resolveProjectSource(ctx, req)
	if err != nil {
		return gen.NativeBuildResp{}, err
	}
	entry := req.EntryModule
	if entry == "" {
		entry = "main.gen.go"
	}
	mode := req.Mode
	if mode == "" {
		mode = pluginhost.ModeSubprocess
	}
	result, err := pluginhost.CompileFromProject(pluginhost.NativeBuildOptions{
		ProjectID:   req.ProjectID,
		SourceRoot:  projectRoot,
		EntryModule: entry,
		OutDir:      artifactOutputDir(projectRoot),
		Mode:        mode,
		GoBuild:     a.goBuildOverride, // nil → production default go build
	}, manifestPath, req.ArtifactHash)
	if err != nil {
		return gen.NativeBuildResp{Result: result}, err
	}
	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: pluginhost.BinaryCodecV1,
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		TrustClass: pluginhost.TrustFirstParty,
		Signer:     pluginhost.NativeBuildSigner,
	}
	if mode == pluginhost.ModeInprocess {
		abi.Isolation = pluginhost.IsolationInProcess
	} else {
		abi.Isolation = pluginhost.IsolationSubprocess
	}
	return gen.NativeBuildResp{Result: result, ManifestPath: manifestPath, Abi: abi}, nil
}

// missingDependencies returns the subset of declared dependencies that are not
// present in the loaded plugins map. Returns nil when all deps are satisfied.
func (a *Actor) missingDependencies(deps []gen.AppDependency) []string {
	if len(deps) == 0 {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	loaded := make(map[string]bool, len(a.ArtifactLoads))
	for id := range a.ArtifactLoads {
		loaded[id] = true
	}
	// Also consider plugins registered via handleRegisterActor (non-artifact).
	for _, p := range a.Plugins {
		loaded[p.ID] = true
	}
	var missing []string
	for _, d := range deps {
		if !loaded[d.ID] {
			missing = append(missing, d.ID)
		}
	}
	return missing
}

func joinStrings(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	b := make([]byte, 0, 64)
	for i, s := range ss {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, s...)
	}
	return string(b)
}
