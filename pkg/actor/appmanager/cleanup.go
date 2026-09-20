package appmanager

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Cleanup state-machine constants. These represent the phases an app passes
// through during unregister after the front half (artifact unload / child
// destroy) has begun:
//
//	unloading → protocol_cleanup_pending → cleanup_pending → unregistered(gone)
//
// The unloading state is set before the artifact/child is torn down.
// protocol_cleanup_pending means the artifact/child is gone but the protocol
// descriptor has not yet been removed. cleanup_pending means the protocol
// descriptor is removed and only the in-memory record deletion + Save remain.
//
// unload_failed is the terminal failure state when the front half itself fails;
// it is retryable via unregister or retry_cleanup.
//
// restart_pending (below) is the deferred-activation state: a plugin whose
// new artifact is persisted (appmanager record + pluginhost ArtifactLoads) but
// could not be swapped into the in-process host (c-shared DLLs cannot be
// unmapped while the host lives). The next host restart activates it.
const (
	stateUnloading              = "unloading"
	stateProtocolCleanupPending = "protocol_cleanup_pending"
	stateCleanupPending         = "cleanup_pending"
	stateUnloadFailed           = "unload_failed"
	stateRestartPending         = "restart_pending"
	stateStarting               = "starting"
	stateRunning                = "running"
	// stateStopped is reported by pluginhost when a subprocess plugin is
	// killed by an explicit unload (recordExit/close with a nil cause). The
	// process — and its HTTP listener — is gone, so backend state clears.
	stateStopped = "stopped"
	stateFailed  = "failed"
	// stateCrashed is reported by pluginhost when a native plugin's process
	// dies abnormally (session EOF, protocol violation, invoke-timeout kill).
	// It is written verbatim from appmanager.report_process_state.
	stateCrashed = "crashed"
	// stateActive is pluginhost's vocabulary for a loaded artifact. It must
	// never persist into appRecord.State — every state-machine branch here
	// compares against the constants above — so pluginhost statuses are
	// normalized where they flow in (normalizePluginState).
	stateActive = "active"
)

// normalizePluginState maps pluginhost load vocabulary onto the appmanager
// record state vocabulary. Observed live: a native reload copied
// prepared.Status.State ("active") into the record, and the next
// plugin_unload hit the idempotent "not running" early-return and silently
// did nothing while the app kept serving.
func normalizePluginState(state string) string {
	if state == stateActive {
		return stateRunning
	}
	return state
}

// recordLive reports whether a record state means "artifact currently
// serving" in appmanager vocabulary; legacy persisted records may still
// carry the raw pluginhost value "active".
func recordLive(state string) bool {
	return state == stateRunning || state == stateActive
}

// isCleanupPendingState reports whether the given app state is a
// pending-cleanup or cleanup-failed state during which invoke must be rejected
// and the back-half cleanup is incomplete.
func isCleanupPendingState(state string) bool {
	switch state {
	case stateUnloading,
		stateProtocolCleanupPending,
		stateCleanupPending,
		stateUnloadFailed:
		return true
	}
	return false
}

// unloadFrontHalf performs the irreversible front-half teardown: artifact
// unload for plugins (via pluginhost) and child-actor destruction for
// spore runtimes. It is safe to call when the child entry is absent
// (returns nil). The caller must have already marked the record as
// "unloading" and persisted.
func (a *Actor) unloadFrontHalf(ctx actor.Context, appID string) error {
	var manifest gen.AppManifest
	var actorID string
	a.withMu(func() {
		manifest = a.Apps[appID]
		actorID = a.children[appID]
	})
	// Plugins hold no child actor: their live state is the pluginhost
	// artifact. Unload it whenever the manifest says native, regardless of
	// the in-memory children sentinel — a wedged record (child entry lost
	// while the record still says running) must not orphan the loaded
	// artifact: unregister would delete the appmanager record while the
	// subprocess and its handlers stay registered in pluginhost.
	// artifact_unload is idempotent (nothing loaded → Removed: 0), so
	// calling it for a wedged record that never loaded is safe.
	if manifest.Runtime == "native" {
		pluginRef, found := ctx.LookupService(pluginhostServiceName)
		if !found || pluginRef == nil || ctx.Planner() == nil {
			return fmt.Errorf("appmanager: pluginhost service not available")
		}
		if _, err := ctx.Planner().Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_unload", gen.PluginArtifactUnloadReq{PluginID: appID}).Await(); err != nil {
			return fmt.Errorf("appmanager: native artifact unload: %w", err)
		}
		// The app's "<appID>/" app-state subtree (documents + append logs)
		// is deliberately preserved: unregister is reversible by re-register
		// and the user's saved data must survive teardown. The
		// pluginhost.state_purge callable remains available for explicit
		// data reclamation but is not invoked during unregister.
		// The artifact (and its listener) is gone: drop the gateway proxy
		// route from the unregister cascade. Idempotent no-op when no proxy
		// was attached.
		a.detachBackendProxy(ctx, appID)
		// The sentinel entry (if any) is dead weight now; finishCleanup
		// deletes the map entry, but clear it early so a crash between
		// here and there cannot route to a torn-down artifact.
		a.withMu(func() {
			delete(a.children, appID)
		})
		return nil
	}
	if actorID == "" {
		return nil
	}
	canonical, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return err
	}
	if ref, found := ctx.LookupID(id.From(canonical)); found && ref != nil {
		if err := ctx.Destroy(ref); err != nil {
			return err
		}
	}
	return nil
}

// finishCleanup executes the back-half unregister cleanup: protocol descriptor
// removal, agent-binding unbind, record deletion, session revocation, and
// persistence. It is idempotent — calling it on an app whose records are
// already deleted returns nil immediately.
//
// On failure the app is left in protocol_cleanup_pending or cleanup_pending
// (whichever was last persisted) so that retry_cleanup or OnInit recovery can
// resume. Both protocol.UnregisterAppProtocol and map deletion are individually
// idempotent, so a retry from any pending state is safe.
func (a *Actor) finishCleanup(ctx actor.Context, appID string) error {
	var record appRecord
	var exists bool
	a.withMu(func() {
		record, exists = a.Records[appID]
	})
	if !exists {
		return nil // already fully removed
	}

	// Step 1 — unregister the protocol descriptor (idempotent).
	if a.protocol != nil {
		if err := a.protocol.UnregisterAppProtocol(record.Manifest.Namespace); err != nil {
			a.withMu(func() {
				rec := a.Records[appID]
				rec.State = stateProtocolCleanupPending
				rec.Error = err.Error()
				a.Records[appID] = rec
			})
			_ = a.Save()
			return fmt.Errorf("appmanager: protocol cleanup for %q: %w", appID, err)
		}
	}

	// Step 2 — transition to cleanup_pending and persist so a crash between
	// here and the final delete is recoverable.
	a.withMu(func() {
		record = a.Records[appID]
		record.State = stateCleanupPending
		record.Error = ""
		a.Records[appID] = record
	})
	if err := a.Save(); err != nil {
		return fmt.Errorf("appmanager: persist cleanup_pending for %q: %w", appID, err)
	}

	// Step 2b — reclaim the app's inventory files (the stored install zip
	// under record.PackagePath and the content-addressed native artifact under
	// record.ArtifactPath). Both live in <DataDir>/.actors/appmanager/ and are
	// owned by appmanager; nothing outside that root is ever removed here, so
	// register_project artifacts (project directory) are untouched. Files are
	// removed BEFORE the record deletion persists: a crash in between leaves a
	// cleanup_pending record whose files are already gone — the retry path
	// (finishCleanup re-entry) deletes the record and tolerates the missing
	// files, never the reverse (record gone, files orphaned forever).
	removeOwnedInventoryFile(record.PackagePath)
	removeOwnedInventoryFile(record.ArtifactPath)
	// The materialized installed-app tree (frontend assets + app.data) is
	// exclusive to this app's record; reclaim it with the record.
	removeInstalledAppDir(appID)
	// The side-store asset blobs are exclusive to this app's record; with
	// the record about to be deleted they are unreclaimable otherwise
	// (content addressing deliberately keeps stale blobs across reloads).
	a.removeAppAssetDir(appID)

	// Step 3 — persist the deletion BEFORE mutating in-memory maps. This way
	// a Save failure leaves the app in memory at cleanup_pending for retry,
	// rather than losing it entirely.
	var manifest gen.AppManifest
	var appsSnapshot map[string]gen.AppManifest
	var recordsSnapshot map[string]appRecord
	a.withMu(func() {
		manifest = a.Apps[appID]
		appsSnapshot = make(map[string]gen.AppManifest, len(a.Apps))
		for k, v := range a.Apps {
			if k != appID {
				appsSnapshot[k] = v
			}
		}
		recordsSnapshot = make(map[string]appRecord, len(a.Records))
		for k, v := range a.Records {
			if k != appID {
				recordsSnapshot[k] = v
			}
		}
	})
	if err := a.saveState(appsSnapshot, recordsSnapshot); err != nil {
		return fmt.Errorf("appmanager: persist unregister for %q: %w", appID, err)
	}

	// Step 4 — mutate in-memory state only after persist succeeded.
	a.withMu(func() {
		// Remove the wildcard free-agent binding if present.
		if manifest.AgentBinding != nil && manifest.AgentBinding.FreeAgent != nil {
			delete(a.FreeAgentPolicies, freeAgentKey(appID))
		}
		delete(a.Apps, appID)
		delete(a.Records, appID)
		delete(a.children, appID)
		a.revokeAppSessions(appID)
	})

	// The app is gone; its dedicated plugin agents must go with it (AppId
	// cascade across every binding slot). Best-effort: a workspace hiccup
	// must not wedge the app in cleanup_pending, and leftover agents are
	// recoverable by a later reconcile (keyed by appID).
	if manifest.AgentBinding != nil && len(manifest.AgentBinding.PluginAgents) > 0 {
		a.removePluginAgent(ctx, appID)
	}

	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{
		Kind: "unloaded", ID: appID, Runtime: manifest.Runtime,
		State: "unloaded", Version: manifest.Version,
	})
	a.auditLifecycle(appID, "unregister", "unregistered")
	return nil
}

// handleRetryCleanup is the idempotent callable exposed as
// appmanager.retry_cleanup. It completes any pending back-half cleanup for an
// app left in a cleanup_pending / protocol_cleanup_pending state, or retries
// the full unregister when the app is still in the unload_failed / unloading
// front-half states.
func (a *Actor) handleRetryCleanup(ctx actor.Context, req gen.AppManagerRetryCleanupReq) (gen.AppManagerRetryCleanupResp, error) {
	var record appRecord
	var exists bool
	a.withMu(func() {
		record, exists = a.Records[req.ID]
	})
	if !exists {
		return gen.AppManagerRetryCleanupResp{Status: "not_found"}, nil
	}

	switch record.State {
	case stateProtocolCleanupPending, stateCleanupPending:
		if err := a.finishCleanup(ctx, req.ID); err != nil {
			return gen.AppManagerRetryCleanupResp{Status: "failed", Error: err.Error()}, err
		}
		return gen.AppManagerRetryCleanupResp{Status: "completed"}, nil

	case stateUnloadFailed, stateUnloading:
		// Front half incomplete — retry the full unregister flow.
		if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: req.ID}); err != nil {
			return gen.AppManagerRetryCleanupResp{Status: "failed", Error: err.Error()}, err
		}
		return gen.AppManagerRetryCleanupResp{Status: "completed"}, nil

	default:
		return gen.AppManagerRetryCleanupResp{Status: "not_pending"}, nil
	}
}

// recoverPendingCleanup is called during OnStart to detect apps that were left
// in a pending-cleanup state by a crash or failed unregister, and execute the
// remaining cleanup. On a fresh restart the artifact/child is already gone, so
// the front half is effectively complete for all states; finishCleanup handles
// the back half idempotently.
func (a *Actor) recoverPendingCleanup(ctx actor.Context) {
	var pending []string
	a.withMu(func() {
		for appID, record := range a.Records {
			if isCleanupPendingState(record.State) {
				pending = append(pending, appID)
			}
		}
	})
	for _, appID := range pending {
		ctx.Logger().Info("appmanager: recovering pending cleanup", "app", appID)
		if err := a.finishCleanup(ctx, appID); err != nil {
			ctx.Logger().Error("appmanager: recovery cleanup failed", "app", appID, "error", err)
		}
	}
}
