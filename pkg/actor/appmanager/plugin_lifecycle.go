package appmanager

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// Plugin artifact load/unload without unregistering the app. The appRecord
// persists manifest + ABI + artifact path, so a stopped plugin re-loads from
// its recorded artifact. Complements register/unregister (which install and
// remove the app record itself) — this pair only drives pluginhost.
//
// Unload drops the pluginhost artifact (handlers, HTTP asset route) and marks
// the record stopped; load re-installs the artifact from the record and marks
// it running. Stopped state does not survive a host restart: pluginhost
// restore loads every persisted artifact, which is consistent with the
// register-time behavior.

// pluginLoadFastPath applies plugin_load's early paths against the CURRENT
// record snapshot: registration/native validation, the live-record routing
// self-heal, unload_pending staging, and the missing-artifact rejection.
// handled=true means (resp, err) is final. It is invoked once before the
// dependency pulls (no app lock held) and again after the per-app
// transaction lock is acquired, so a concurrent load of the same app that
// finished in between short-circuits instead of double-loading.
func (a *Actor) pluginLoadFastPath(ctx actor.PureContext, reqID string) (gen.AppManagerPluginLoadResp, gen.AppManifest, bool, error) {
	var manifest gen.AppManifest
	var record appRecord
	var hasApp, hasRecord bool
	a.withMu(func() {
		manifest, hasApp = a.Apps[reqID]
		record, hasRecord = a.Records[reqID]
	})
	if !hasApp || !hasRecord {
		return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: app %q is not registered", reqID)
	}
	if manifest.Runtime != "native" {
		return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: plugin load requires native runtime, %q is %q", reqID, manifest.Runtime)
	}
	if recordLive(record.State) {
		routed := false
		a.withMu(func() {
			_, routed = a.children[reqID]
			if !routed {
				a.children[reqID] = pluginhostServiceName
				record.ActorID = pluginhostServiceName
				a.Records[reqID] = record
			}
		})
		if !routed {
			if err := a.Save(); err != nil {
				return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: persist plugin load self-heal: %w", err)
			}
		}
		return gen.AppManagerPluginLoadResp{Status: a.statusFromRecord(manifest, record)}, manifest, true, nil
	}
	if record.State == "unload_pending" {
		// A2: an in-process (c-shared) plugin's DLL stays mapped until the
		// host restarts, so re-loading is impossible right now. Stage the
		// recorded artifact as restart_pending — it activates on the next
		// host restart — instead of reviving the stale mapping.
		if record.Abi != nil && record.Abi.Isolation == pluginhost.IsolationInProcess {
			record.State = stateRestartPending
			record.Error = "in-process plugin was unloaded; staged for restart activation"
			a.withMu(func() {
				a.Records[reqID] = record
			})
			if err := a.Save(); err != nil {
				return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: persist plugin load staging: %w", err)
			}
			a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "restart_pending", ID: manifest.ID, Runtime: manifest.Runtime, State: stateRestartPending, Version: manifest.Version})
			a.auditLifecycle(manifest.ID, "plugin_load", "restart_pending: unload_pending staged for restart")
			return gen.AppManagerPluginLoadResp{Status: a.statusFromRecord(manifest, record)}, manifest, true, nil
		}
		return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: app %q is unload-pending; restart the host to re-load it", reqID)
	}
	if record.ArtifactPath == "" || record.Abi == nil {
		return gen.AppManagerPluginLoadResp{}, manifest, true, fmt.Errorf("appmanager: app %q has no recorded artifact to load; re-register it", reqID)
	}
	return gen.AppManagerPluginLoadResp{}, manifest, false, nil
}

func (a *Actor) handlePluginLoad(ctx actor.Context, req gen.AppManagerPluginLoadReq) (gen.AppManagerPluginLoadResp, error) {
	resp, manifest, handled, err := a.pluginLoadFastPath(ctx, req.ID)
	if handled {
		return resp, err
	}
	var record appRecord
	a.withMu(func() {
		record = a.Records[req.ID]
	})

	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil || ctx.Planner() == nil {
		return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: pluginhost service not available")
	}
	// The pluginhost refuses loads whose declared dependencies are not
	// loaded, so a stopped dependency dead-ends plugin_load of every
	// dependent. Pull registered-but-stopped dependencies up first, in
	// dependency order (transitively), reusing this handler so each dep gets
	// the same state handling, audit, and lifecycle event as an explicit
	// plugin_load. Unregistered deps are left for the pluginhost to report.
	// This runs BEFORE the per-app lock so dependency pulls never nest one
	// app's lock inside another's.
	if err := a.ensureDependenciesLoaded(ctx, req.ID, manifest); err != nil {
		return gen.AppManagerPluginLoadResp{}, err
	}
	unlock := a.lockApp(req.ID)
	defer unlock()
	// Re-check under the app lock: a concurrent plugin_load of the same app
	// may have finished while dependencies were pulled.
	if resp, _, handled, ferr := a.pluginLoadFastPath(ctx, req.ID); handled {
		return resp, ferr
	}
	authManifest, err := a.loadAuthManifest(manifest)
	if err != nil {
		return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: plugin load permissions: %w", err)
	}
	loadReq, backendSecret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
		Manifest: authManifest, Abi: *record.Abi, ArtifactPath: record.ArtifactPath, ArtifactHash: record.ArtifactHash,
	})
	if err != nil {
		return gen.AppManagerPluginLoadResp{}, err
	}
	loadValue, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", loadReq).Await()
	if err != nil {
		return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: plugin load: %w", err)
	}
	loaded, ok := loadValue.(gen.PluginArtifactLoadResp)
	if !ok || loaded.PluginID != manifest.ID {
		return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: plugin load returned invalid status")
	}
	if loaded.Status.State == stateRestartPending {
		// The pluginhost could not swap the artifact (still holds a
		// different in-process mapping); the load was staged for restart.
		// Keep the record honest instead of marking it running.
		record.State = stateRestartPending
		record.Error = loaded.Status.Error
		if record.Error == "" {
			record.Error = "in-process artifact blocked; activates on host restart"
		}
		a.withMu(func() {
			a.Records[req.ID] = record
		})
		if err := a.Save(); err != nil {
			return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: persist plugin load staging: %w", err)
		}
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "restart_pending", ID: manifest.ID, Runtime: manifest.Runtime, State: stateRestartPending, Version: manifest.Version})
		a.auditLifecycle(manifest.ID, "plugin_load", "restart_pending: artifact blocked by in-process mapping")
		return gen.AppManagerPluginLoadResp{Status: a.statusFromRecord(manifest, record)}, nil
	}
	if err := a.pushNativeAssets(ctx, manifest.ID, record.Assets); err != nil {
		// The plugin runs without its static bundle; surface the failure but
		// keep the artifact loaded — a partial load is better than none.
		record.Error = err.Error()
	} else {
		record.Error = ""
	}
	// The respawned process confirmed its HTTP listener (or reported none):
	// adopt the per-instance secret and the bootstrap URL. Cleared instead
	// when the new instance runs no listener (old SDK / in-process transport).
	setBackendFromLoad(&record, backendSecret, loaded.HttpAddr)

	a.withMu(func() {
		record.State = stateRunning
		record.ActorID = pluginhostServiceName
		if _, routed := a.children[req.ID]; !routed {
			// Same self-heal as reload (actions.go handleReload): a wedged
			// record whose children entry was lost (e.g. restore-time
			// verification failed after a host restart) must not stay
			// uninvocable after the artifact is confirmed loaded — restores
			// the pluginhost routing sentinel resolveInvokeAuth gates on.
			a.children[req.ID] = pluginhostServiceName
			if ctx != nil {
				ctx.Logger().Info("appmanager: plugin load self-healed missing children sentinel", "app", req.ID)
			}
		}
		a.Records[req.ID] = record
	})
	if err := a.Save(); err != nil {
		return gen.AppManagerPluginLoadResp{}, fmt.Errorf("appmanager: persist plugin load: %w", err)
	}
	// The respawned backend must serve traffic through the gateway: attach
	// (or, when this instance runs no listener, detach) pluginhost's
	// reverse proxy, mirroring the commit path. Without this the revived
	// app answers 405/404 on /plugin/{id}/invoke|events until a reload.
	if record.BackendUrl != "" {
		a.attachBackendProxy(ctx, req.ID, record)
	} else {
		a.detachBackendProxy(ctx, req.ID)
	}
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "running", ID: manifest.ID, Runtime: manifest.Runtime, State: "running", Version: manifest.Version})
	a.auditLifecycle(manifest.ID, "plugin_load", "loaded")
	return gen.AppManagerPluginLoadResp{Status: a.statusFromRecord(manifest, record)}, nil
}

func (a *Actor) handlePluginUnload(ctx actor.Context, req gen.AppManagerPluginUnloadReq) (gen.AppManagerPluginUnloadResp, error) {
	var manifest gen.AppManifest
	var record appRecord
	var hasApp, hasRecord bool
	a.withMu(func() {
		manifest, hasApp = a.Apps[req.ID]
		record, hasRecord = a.Records[req.ID]
	})
	if !hasApp || !hasRecord {
		// Orphan-cleanup fallback: the appmanager record is gone but the
		// pluginhost may still hold the artifact (e.g. an unregister that
		// ran before the wedged-record self-heal fix). artifact_unload
		// only needs the PluginID and is idempotent, so attempt it
		// directly instead of dead-ending the caller into a host restart.
		return a.unloadOrphanedArtifact(ctx, req.ID)
	}
	if manifest.Runtime != "native" {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: plugin unload requires native runtime, %q is %q", req.ID, manifest.Runtime)
	}
	// A6: a failed unload lands in unload_failed and may be retried by
	// calling plugin_unload again; running apps unload normally. Legacy
	// records persisted before state normalization may carry pluginhost's
	// "active" — still live, still unloadable.
	if !recordLive(record.State) && record.State != stateUnloadFailed {
		return gen.AppManagerPluginUnloadResp{Status: a.statusFromRecord(manifest, record)}, nil
	}
	// Serialize this app's unload against concurrent plugin_load / reload
	// transactions on the same id; other apps proceed in parallel.
	unlock := a.lockApp(req.ID)
	defer unlock()

	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil || ctx.Planner() == nil {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: pluginhost service not available")
	}
	if _, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: manifest.ID}).Await(); err != nil {
		// A6: record the failure as unload_failed (instead of returning a
		// bare error with the record still marked running), so the frontend
		// can show a retryable failure state and plugin_unload can retry.
		record.State = stateUnloadFailed
		record.Error = err.Error()
		a.withMu(func() {
			a.Records[req.ID] = record
		})
		if saveErr := a.Save(); saveErr != nil {
			return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: plugin unload: %v; persist unload_failed: %w", err, saveErr)
		}
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "unload_failed", ID: manifest.ID, Runtime: manifest.Runtime, State: stateUnloadFailed, Version: manifest.Version, Error: err.Error()})
		a.auditLifecycle(manifest.ID, "plugin_unload", "unload_failed: "+err.Error())
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: plugin unload: %w", err)
	}

	unloadState := "stopped"
	unloadKind := "stopped"
	a.withMu(func() {
		// In-process (c-shared) plugins degrade to unload-pending: the library
		// stays mapped for the host process lifetime, so the app is unavailable
		// but not re-loadable until the host restarts. Subprocess plugins are
		// killed for real and may load again immediately.
		if record.Abi != nil && record.Abi.Isolation == "inprocess" {
			unloadState = "unload_pending"
			unloadKind = "unload_pending"
			record.Error = "in-process plugin unloaded; restart the host to fully reclaim it"
		} else {
			record.Error = ""
		}
		record.State = unloadState
		// The process (and its HTTP listener) is gone; drop the bootstrap URL
		// and the per-instance cookie secret with it.
		clearBackend(&record)
		a.Records[req.ID] = record
	})
	if err := a.Save(); err != nil {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: persist plugin unload: %w", err)
	}
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: unloadKind, ID: manifest.ID, Runtime: manifest.Runtime, State: unloadState, Version: manifest.Version})
	a.auditLifecycle(manifest.ID, "plugin_unload", "unloaded")
	return gen.AppManagerPluginUnloadResp{Status: a.statusFromRecord(manifest, record)}, nil
}

// unloadOrphanedArtifact clears a pluginhost artifact whose appmanager record
// no longer exists. The pluginhost unload only needs the PluginID and is
// idempotent (nothing loaded → Removed: 0), so when nothing was loaded the
// original not-registered error is the honest answer; when handlers were
// removed the caller gets a synthesized stopped status and the orphan is gone
// without a host restart.
func (a *Actor) unloadOrphanedArtifact(ctx actor.PureContext, appID string) (gen.AppManagerPluginUnloadResp, error) {
	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil || ctx.Planner() == nil {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: pluginhost service not available")
	}
	unloadValue, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: appID}).Await()
	if err != nil {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: orphaned artifact unload for %q: %w", appID, err)
	}
	unloaded, ok := unloadValue.(gen.PluginArtifactUnloadResp)
	if !ok {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: orphaned artifact unload for %q returned invalid response", appID)
	}
	if unloaded.Removed == 0 {
		return gen.AppManagerPluginUnloadResp{}, fmt.Errorf("appmanager: app %q is not registered", appID)
	}
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "stopped", ID: appID, Runtime: "native", State: "stopped"})
	a.auditLifecycle(appID, "plugin_unload", "orphaned artifact unloaded (no appmanager record)")
	return gen.AppManagerPluginUnloadResp{Status: gen.AppStatus{ID: appID, Runtime: "native", State: "stopped"}}, nil
}

// ensureDependenciesLoaded walks the declared dependency graph of the given
// manifest depth-first and re-loads registered-but-stopped native
// dependencies before the app's own artifact load, so the pluginhost's
// "dependencies not loaded" gate never dead-ends a dependent. Callers pass
// the manifest explicitly (a.Apps may not yet hold it mid-register). Live
// dependencies and spore-runtime dependencies need no artifact; unregistered
// dependencies are left for the pluginhost to report (its error names them).
// Loading reuses handlePluginLoad so each pulled-up dependency gets identical
// state handling (live self-heal, unload_pending staging, restart_pending)
// plus audit and lifecycle events.
func (a *Actor) ensureDependenciesLoaded(ctx actor.Context, appID string, manifest gen.AppManifest) error {
	visited := map[string]bool{appID: true}
	var load func(depID string) error
	load = func(depID string) error {
		if visited[depID] {
			return nil
		}
		visited[depID] = true
		var depManifest gen.AppManifest
		var depRecord appRecord
		var hasApp bool
		a.withMu(func() {
			depManifest, hasApp = a.Apps[depID]
			depRecord = a.Records[depID]
		})
		if !hasApp || depManifest.Runtime != "native" {
			return nil
		}
		for _, d := range depManifest.Dependencies {
			if err := load(d.ID); err != nil {
				return err
			}
		}
		if recordLive(depRecord.State) {
			return nil
		}
		if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: depID}); err != nil {
			return fmt.Errorf("appmanager: plugin_load dependency %q: %w", depID, err)
		}
		return nil
	}
	for _, d := range manifest.Dependencies {
		if err := load(d.ID); err != nil {
			return err
		}
	}
	return nil
}
