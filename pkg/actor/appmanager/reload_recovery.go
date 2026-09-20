package appmanager

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// recoverPendingReloads is called during OnStart to detect apps whose native
// reload was interrupted by a crash after the candidate manifest was persisted
// but before the pluginhost commit completed. For each such app it tries to
// complete the reload (re-prepare + commit the candidate); on any failure it
// rolls back the record to the pre-reload state so the appmanager state and
// the pluginhost's loaded artifact agree.
//
// On a fresh restart the pluginhost has loaded the OLD artifact from its own
// persisted ArtifactLoads (the candidate was never committed, so the pluginhost
// never persisted it). The recovery either:
//   - completes the reload by re-prepare + commit, or
//   - reverts the appmanager record to the old manifest/record (rollback).
//
// Both outcomes restore consistency: record and pluginhost agree.
func (a *Actor) recoverPendingReloads(ctx actor.Context) {
	var pending []string
	a.withMu(func() {
		pending = make([]string, 0, len(a.PendingReloads))
		for appID := range a.PendingReloads {
			pending = append(pending, appID)
		}
	})
	if len(pending) == 0 {
		return
	}
	pluginRef, pluginFound := ctx.LookupService(pluginhostServiceName)
	planner := ctx.Planner()
	for _, appID := range pending {
		var pr pendingReload
		a.withMu(func() {
			pr = a.PendingReloads[appID]
		})
		if err := a.resolvePendingReload(ctx, appID, pr, pluginRef, pluginFound, planner); err != nil {
			ctx.Logger().Error("appmanager: recover pending reload failed", "app", appID, "error", err)
		}
	}
}

// resolvePendingReload attempts to complete a single pending reload. It first
// tries to re-prepare and commit the candidate via the pluginhost; on success
// the marker is cleared. On any failure it rolls back to the old record.
func (a *Actor) resolvePendingReload(ctx actor.Context, appID string, pr pendingReload, pluginRef ref.Ref, pluginFound bool, planner actor.Planner) error {
	// Attempt to complete the reload: re-prepare + commit.
	if pluginFound && planner != nil && pr.CandidateAbi != nil {
		abi := *pr.CandidateAbi
		prepareReq, backendSecret, backendErr := a.withBackendPrepareReq(gen.PluginArtifactReloadPrepareReq{
			Manifest: pr.CandidateManifest, Abi: abi, ArtifactPath: pr.CandidateArtifactPath, ArtifactHash: pr.CandidateArtifactHash,
			Assets: pr.CandidateAssets,
		})
		if backendErr == nil {
			prepareValue, prepareErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_prepare", prepareReq).Await()
			if prepareErr == nil {
				if prepared, ok := prepareValue.(gen.PluginArtifactReloadPrepareResp); ok && prepared.PluginID == appID && prepared.Token != "" {
					commitValue, commitErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_commit", gen.PluginArtifactReloadCommitReq{Token: prepared.Token}).Await()
					if commitErr == nil {
						if committed, ok := commitValue.(gen.PluginArtifactReloadCommitResp); ok && committed.PluginID == appID {
							// Reload completed — clear the marker and persist.
							a.withMu(func() {
								delete(a.PendingReloads, appID)
							})
							_ = a.Save()
							// The recovered candidate confirmed its listener:
							// adopt its fresh per-instance backend.
							a.commitBackend(ctx, appID, backendSecret, committed.HttpAddr)
							ctx.Logger().Info("appmanager: recovered pending reload (committed candidate)", "app", appID)
							return nil
						}
					}
					// Commit failed — abort the prepared reload and fall through
					// to rollback.
					_, _ = planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_abort", gen.PluginArtifactReloadAbortReq{Token: prepared.Token}).Await()
				}
			}
		}
	}
	// Rollback to the pre-reload state. The pluginhost has the old artifact
	// loaded (from its persisted ArtifactLoads), so reverting the record
	// restores consistency.
	ctx.Logger().Warn("appmanager: rolling back pending reload to old state", "app", appID)
	a.withMu(func() {
		delete(a.PendingReloads, appID)
		a.Records[appID] = pr.OldRecord
		a.Apps[appID] = pr.OldManifest
	})
	if err := a.Save(); err != nil {
		return fmt.Errorf("appmanager: persist pending-reload rollback for %q: %w", appID, err)
	}
	ctx.Logger().Info("appmanager: recovered pending reload (rolled back)", "app", appID)
	return nil
}

// abortPendingReload discards an unresolved pending reload for appID: it
// restores the pre-reload record/manifest and clears the PendingReloads
// marker. It is the mutual-exclusion primitive (R4) between the reload and
// unregister recovery state machines — unregister calls it before entering
// the cleanup flow so that cleanup tears down the artifact the pluginhost
// actually holds (the old, uncommitted one) and no stale marker survives a
// later crash.
//
// The candidate was never committed to the pluginhost, so the pluginhost
// still has the old artifact loaded; rolling back the appmanager record to
// the old state keeps the two consistent. Safe to call when no pending
// reload exists (no-op). The caller must NOT hold a.mu.
func (a *Actor) abortPendingReload(appID string) {
	a.withMu(func() {
		pr, ok := a.PendingReloads[appID]
		if !ok {
			return
		}
		delete(a.PendingReloads, appID)
		a.Records[appID] = pr.OldRecord
		a.Apps[appID] = pr.OldManifest
	})
	_ = a.Save()
}
