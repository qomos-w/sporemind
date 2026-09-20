package appmanager

import (
	"fmt"
	"slices"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// reexpandDependents refreshes the bundle permission expansion of every app
// whose manifest depends on changedID, after changedID's manifest was
// replaced by a reload. A dependent's pluginhost-side authorization is the
// expanded callID set persisted in ArtifactLoads at its own load boundary;
// when the dependency's bundles/callables changed, that set is stale and the
// host bridge would allow or deny calls against the old shape.
//
// Refresh sequence per dependent: re-expand against current records, then
// artifact_unload + artifact_load so the pluginhost re-applies the expanded
// set — for a subprocess plugin this respawns the process with fresh auth
// (the idempotent same-path load would keep the old set), for an in-process
// plugin the unload is a tombstone and the load defers into ArtifactLoads,
// landing in restart_pending until the next host restart.
//
// Failure is recorded on the dependent (record.Error + audit) but never
// fails the dependency's own reload: a stale dependent keeps serving under
// its previous authorization, which is the honest degradation.
func (a *Actor) reexpandDependents(ctx actor.Context, changedID string) {
	for _, dependentID := range a.dependentsOf(changedID) {
		a.refreshDependentAuth(ctx, dependentID, changedID)
	}
}

// refreshDependentAuth re-expands one dependent's bundle permissions and
// re-applies them to the pluginhost. See reexpandDependents.
func (a *Actor) refreshDependentAuth(ctx actor.Context, dependentID, changedDep string) {
	pluginRef, ok := ctx.LookupService(pluginhostServiceName)
	if !ok || pluginRef == nil || ctx.Planner() == nil {
		return
	}
	planner := ctx.Planner()
	var manifest gen.AppManifest
	var record appRecord
	var reloading bool
	a.withMu(func() {
		manifest = a.Apps[dependentID]
		record = a.Records[dependentID]
		reloading = a.reloadingApps[dependentID]
	})
	if manifest.Runtime != "native" || record.ArtifactPath == "" || record.Abi == nil || reloading {
		// Non-native / artifact-less apps carry no pluginhost auth set; an
		// app mid-reload re-expands through its own reload path.
		return
	}
	if !recordLive(record.State) && record.State != stateRestartPending {
		// Stopped dependents re-expand at their next plugin_load boundary;
		// nothing is loaded, so nothing can be stale.
		return
	}
	authManifest, err := a.loadAuthManifest(manifest)
	if err != nil {
		// The dependency changed shape under a bundle declaration (bundle
		// gone, dep unregistered). Keep the old expanded set and record why.
		a.withMu(func() {
			r := a.Records[dependentID]
			r.Error = fmt.Sprintf("dependency %q reloaded; bundle re-expansion failed: %v", changedDep, err)
			a.Records[dependentID] = r
		})
		_ = a.Save()
		a.auditLifecycle(dependentID, "dep_reload", "re-expansion failed: "+err.Error())
		return
	}
	if slices.Equal(authManifest.Permissions, manifest.Permissions) {
		// No bundle-level declarations: the persisted auth set is the
		// declared one verbatim and cannot be stale.
		return
	}
	loadReq, backendSecret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
		Manifest: authManifest, Abi: *record.Abi, ArtifactPath: record.ArtifactPath, ArtifactHash: record.ArtifactHash,
	})
	if err != nil {
		a.auditLifecycle(dependentID, "dep_reload", "load request: "+err.Error())
		return
	}
	// Unload first: the loader's idempotent same-path/same-hash load returns
	// the existing record and would keep the old authorization set.
	if _, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: dependentID}).Await(); err != nil {
		a.auditLifecycle(dependentID, "dep_reload", "unload for re-auth: "+err.Error())
		return
	}
	loadValue, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", loadReq).Await()
	if err != nil {
		a.withMu(func() {
			r := a.Records[dependentID]
			r.State = "stopped"
			r.Error = fmt.Sprintf("dependency %q reloaded; re-auth load failed: %v", changedDep, err)
			a.Records[dependentID] = r
		})
		_ = a.Save()
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "stopped", ID: dependentID, Runtime: manifest.Runtime, State: "stopped"})
		a.auditLifecycle(dependentID, "dep_reload", "re-auth load failed: "+err.Error())
		return
	}
	loaded, ok := loadValue.(gen.PluginArtifactLoadResp)
	if !ok || loaded.PluginID != dependentID {
		a.auditLifecycle(dependentID, "dep_reload", "re-auth load returned invalid response")
		return
	}
	record.Generation++ // the respawned process minted a fresh instance secret
	if loaded.Status.State == stateRestartPending {
		// In-process dependent: the tombstoned mapping lives until host
		// restart; the deferred load persisted the re-expanded auth and
		// activates then.
		record.State = stateRestartPending
		record.Error = fmt.Sprintf("dependency %q reloaded; re-expanded auth activates on host restart", changedDep)
		setBackendFromLoad(&record, backendSecret, "")
	} else {
		if err := a.pushNativeAssets(ctx, dependentID, record.Assets); err != nil {
			record.Error = err.Error()
		} else {
			record.Error = ""
		}
		record.State = stateRunning
		record.ActorID = pluginhostServiceName
		setBackendFromLoad(&record, backendSecret, loaded.HttpAddr)
	}
	a.withMu(func() {
		a.children[dependentID] = pluginhostServiceName
		a.Records[dependentID] = record
	})
	if err := a.Save(); err != nil {
		a.auditLifecycle(dependentID, "dep_reload", "persist re-auth: "+err.Error())
		return
	}
	if record.BackendUrl != "" {
		a.attachBackendProxy(ctx, dependentID, record)
	} else {
		a.detachBackendProxy(ctx, dependentID)
	}
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
		Kind: record.State, ID: dependentID, Runtime: manifest.Runtime, State: record.State, Generation: record.Generation,
	})
	a.auditLifecycle(dependentID, "dep_reload", "re-expanded permissions after dependency "+changedDep+" reload")
}
