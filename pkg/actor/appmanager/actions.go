package appmanager

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// originRank orders registration origins: project > user > builtin. A new
// registration for an existing app ID must have rank >= the existing one.
func originRank(origin string) int {
	switch origin {
	case "project":
		return 3
	case "builtin":
		return 1
	default: // "user" and empty both rank as user
		return 2
	}
}

// checkOriginOverride enforces the origin override rule against existing
// records: a replacement registration must rank >= the existing origin.
func checkOriginOverride(records map[string]appRecord, rec appRecord) error {
	existing, ok := records[rec.Manifest.ID]
	if !ok {
		return nil
	}
	if originRank(rec.Origin) < originRank(normalizeOrigin(existing.Origin)) {
		return appbinding.Deny(appbinding.CodePermissionDenied,
			fmt.Sprintf("app %q origin %q cannot override existing origin %q",
				rec.Manifest.ID, rec.Origin, normalizeOrigin(existing.Origin)))
	}
	return nil
}

// normalizeOrigin maps empty/unknown origins to "user".
func normalizeOrigin(origin string) string {
	switch origin {
	case "project", "builtin":
		return origin
	default:
		return "user"
	}
}

// workspaceActionCall maps a free-agent action to the workspace callable that
// executes it. The app-supplied payload is forwarded verbatim.
func workspaceActionCall(action string) (string, error) {
	switch action {
	case "create":
		return "workspace.create_agent", nil
	case "switch":
		return "workspace.load_agent", nil
	case "message":
		return "workspace.agent_send_message", nil
	default:
		return "", appbinding.Deny(appbinding.CodeFreeAgentDenied, "unknown action "+action)
	}
}

// handleAgentAction gates free-agent actions (create/switch/message) requested
// by an app on behalf of its bound agent. Authorization uses the app's
// FreeAgentBinding; allowed actions are forwarded to the workspace service.
func (a *Actor) handleAgentAction(ctx actor.Context, req gen.AppManagerAgentActionReq) (gen.AppManagerAgentActionResp, error) {
	var manifest gen.AppManifest
	var ok bool
	a.withMu(func() {
		manifest, ok = a.Apps[req.ID]
	})
	if !ok {
		return gen.AppManagerAgentActionResp{}, fmt.Errorf("appmanager: app %q not found", req.ID)
	}
	ci := resolveCaller(ctx, req.AgentID, req.Role, req.ProjectID)
	audit := func(allowed bool, reason string) {
		a.recordAudit(appbinding.AuditRecord{
			RequestID: req.RequestID, AppID: req.ID, Runtime: manifest.Runtime,
			AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID,
			Callable: "agent." + req.Action, Allowed: allowed, Reason: reason,
		})
	}
	if ci.AgentID == "" {
		err := appbinding.Deny(appbinding.CodeIdentityIncomplete, "appmanager: agent identity is required")
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	callID, err := workspaceActionCall(req.Action)
	if err != nil {
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	var policy appbinding.FreeAgentPolicy
	a.withMu(func() {
		policy, ok = a.FreeAgentPolicies[freeAgentKey(req.ID)]
		if !ok {
			policy, ok = a.FreeAgentPolicies[req.ID+"\x00"+ci.AgentID]
		}
	})
	if !ok {
		err := appbinding.Deny(appbinding.CodeBindingMissing, "no free-agent binding for app "+req.ID+" agent "+ci.AgentID)
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	if err := policy.Authorize(req.Action, req.Kind); err != nil {
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	// Forward to the workspace service. The payload is app-supplied and must
	// match the target callable's request schema.
	wsRef, found := ctx.LookupService("workspace")
	if !found {
		err := fmt.Errorf("appmanager: workspace service not available")
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	payload := map[string]any{}
	for k, v := range req.Payload {
		payload[k] = v
	}
	if req.TargetAgentID != "" {
		if _, exists := payload["AgentId"]; !exists {
			payload["AgentId"] = req.TargetAgentID
		}
		if _, exists := payload["ID"]; !exists {
			payload["ID"] = req.TargetAgentID
		}
	}
	// A panel that addresses its own assistant without naming an agent
	// (agentAction('message'/'switch', {})) gets the app's dedicated plugin
	// agent as the implicit target. Plugin agents are workspace-global rows
	// hidden from project-scoped agent lists and appmanager exposes no
	// discovery, so the reconcile-tracked actor id is the only handle the app
	// owns to talk to its own assistant.
	if req.TargetAgentID == "" {
		a.fillDefaultPluginAgentTarget(req.ID, manifest, req.Action, payload)
	}
	if req.Kind != "" && req.Action == "create" {
		if _, exists := payload["Kind"]; !exists {
			payload["Kind"] = req.Kind
		}
	}
	planner := ctx.Planner()
	if planner == nil {
		err := fmt.Errorf("appmanager: planner not available")
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	if _, err := planner.Call(ctx.Lifecycle(), wsRef, callID, payload).Await(); err != nil {
		audit(false, err.Error())
		return gen.AppManagerAgentActionResp{}, err
	}
	audit(true, "")
	return gen.AppManagerAgentActionResp{Accepted: true}, nil
}

// payloadHasAgentTarget reports whether the app-supplied payload already names
// a recipient under one of the target keys the forwarded workspace callables
// read (AgentId / ID for create and switch, ToAgentId for message). Absent,
// empty, or null values mean no explicit target: the panel expects the host to
// default it.
func payloadHasAgentTarget(payload map[string]any) bool {
	for _, key := range []string{"TargetAgentId", "AgentId", "ID", "ToAgentId"} {
		if v, ok := payload[key]; ok && v != nil {
			if s, ok := v.(string); !ok || s != "" {
				return true
			}
		}
	}
	return false
}

// fillDefaultPluginAgentTarget resolves the app's own dedicated plugin_agent —
// the first declared AgentBinding.PluginAgents slot — as the implicit target
// of a free-agent message/switch action whose payload names no agent, and
// writes its reconcile-tracked actor id under the workspace payload
// conventions. It returns true when a target was filled. No-op for apps
// without a plugin_agent binding or before reconcile has surfaced the agent.
func (a *Actor) fillDefaultPluginAgentTarget(appID string, manifest gen.AppManifest, action string, payload map[string]any) bool {
	if action != "message" && action != "switch" {
		return false
	}
	if payloadHasAgentTarget(payload) {
		return false
	}
	if manifest.AgentBinding == nil || len(manifest.AgentBinding.PluginAgents) == 0 {
		return false
	}
	slot := manifest.AgentBinding.PluginAgents[0].Name
	if slot == "" {
		return false
	}
	var actorID string
	a.withMu(func() {
		actorID = a.pluginAgentSurfaceIDs[pluginAgentSurfaceKey(appID, slot)]
	})
	if actorID == "" {
		return false
	}
	payload["AgentId"] = actorID
	payload["ID"] = actorID
	if action == "message" {
		payload["ToAgentId"] = actorID
		// workspace.agent_send_message authorizes via
		// requireConversableGrant, which rejects any non-developer caller
		// whose CallerAgentId is empty ("caller identity is not trusted").
		// The panel message is addressed to the app's own plugin agent, so
		// the caller identity is that same agent (self) — callerAgentID ==
		// target.ActorID is the self allowance. The turn engine does not
		// sit on this planner.Call path, so CallerAgentId is not injected
		// elsewhere; it must be set here.
		payload["CallerAgentId"] = actorID
	}
	return true
}

// handleReload proxies a package reload to the child SporeApp through the
// manager so the reload is audited and pre-validated. Both success and
// failure (validation, timeout, crash, persist) are recorded.
func (a *Actor) handleReload(ctx actor.Context, req gen.AppManagerReloadReq) (gen.AppManagerReloadResp, error) {
	var actorID string
	var ok bool
	var manifest gen.AppManifest
	var recordHasState bool
	a.withMu(func() {
		actorID, ok = a.children[req.ID]
		manifest = a.Apps[req.ID]
		recordHasState = a.Records[req.ID].State != ""
	})
	ci := resolveCaller(ctx, req.AgentID, "", "")
	audit := func(allowed bool, reason string) {
		a.recordAudit(appbinding.AuditRecord{
			RequestID: req.RequestID, AppID: req.ID, Runtime: manifest.Runtime,
			AgentID: ci.AgentID, Role: ci.Role, ProjectID: ci.ProjectID,
			Callable: "reload", Allowed: allowed, Reason: reason,
		})
	}
	if !ok {
		// Plugin self-heal: a plugin's live state is the pluginhost
		// artifact, not a child actor. A wedged record (children entry lost
		// while the record still says running) must not wedge reload
		// forever — restore the pluginhost sentinel and proceed through
		// the pluginhost path. The spore runtime genuinely needs a live
		// child, so it keeps the hard error.
		if manifest.Runtime == "native" && recordHasState {
			a.withMu(func() {
				a.children[req.ID] = pluginhostServiceName
			})
			if ctx != nil {
				ctx.Logger().Info("appmanager: reload self-healed missing children sentinel", "app", req.ID)
			}
		} else {
			err := fmt.Errorf("appmanager: app %q is not running", req.ID)
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
	}
	// Plugins reload by unloading and reloading the artifact in the
	// pluginhost service. Spore children reload via sporeapp.reload.
	if manifest.Runtime == "native" {
		pluginRef, found := ctx.LookupService(pluginhostServiceName)
		if !found || pluginRef == nil {
			err := fmt.Errorf("appmanager: pluginhost service not available for native reload of %q", req.ID)
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		planner := ctx.Planner()
		if planner == nil {
			err := fmt.Errorf("appmanager: planner not available")
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		// Fetch the current record to get the manifest/abi/artifact path.
		var rec appRecord
		a.withMu(func() {
			rec = a.Records[req.ID]
		})
		if rec.ArtifactPath == "" {
			err := fmt.Errorf("appmanager: plugin %q has no artifact path; rebuild and register first", req.ID)
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		if req.CandidateManifest == nil || req.CandidateAbi == nil || req.CandidateArtifactPath == "" || req.CandidateArtifactHash == "" {
			err := fmt.Errorf("appmanager: native reload requires a built candidate artifact; use reload_project")
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		candidateManifest := *req.CandidateManifest
		candidateAbi := req.CandidateAbi
		candidatePath := req.CandidateArtifactPath
		candidateHash := req.CandidateArtifactHash
		if candidateAbi == nil {
			return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: plugin %q has no ABI", req.ID)
		}
		// The candidate manifest may declare permissions the previous
		// registration did not (e.g. codegen-derived app.emit from newly
		// added event blocks). Re-run the registration security gate so a
		// reload cannot silently install an invalid manifest.
		var securityErr error
		a.withMu(func() {
			securityErr = validateManifestSecurity(candidateManifest, a.Records)
		})
		if securityErr != nil {
			audit(false, securityErr.Error())
			return gen.AppManagerReloadResp{}, securityErr
		}
		// Guard the reload window: the candidate process reports "running"
		// after prepare but before commit, and its listener already runs the
		// fresh secret. An early handleReportProcessState proxy switch would
		// mint a token against the record's pre-reload secret and 401 every
		// request until commitBackend installs the new one. Cleared by the
		// defer below on every exit path.
		a.withMu(func() {
			if a.reloadingApps == nil {
				a.reloadingApps = map[string]bool{}
			}
			a.reloadingApps[req.ID] = true
		})
		defer func() {
			a.withMu(func() {
				delete(a.reloadingApps, req.ID)
			})
		}()
		packageHash := req.PackageHash
		if packageHash == "" {
			ph, err := canonicalPackageHash(candidateManifest, req.EntryModule, req.Modules, req.Assets, req.SchemaDescriptors, candidateAbi, candidateHash)
			if err != nil {
				audit(false, err.Error())
				return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: compute reload package hash: %w", err)
			}
			packageHash = ph
		}
		// Reload is a load boundary too: a dependency stopped since the last
		// load would dead-end the pluginhost's dependency gate. Runs before the
		// per-app lock so dependency pulls never nest one app's lock in
		// another's.
		if err := a.ensureDependenciesLoaded(ctx, req.ID, candidateManifest); err != nil {
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		// Serialize this app's reload against concurrent stateless
		// plugin_load / plugin_unload transactions on the same id.
		reloadUnlock := a.lockApp(req.ID)
		defer reloadUnlock()
		authManifest, err := a.loadAuthManifest(candidateManifest)
		if err != nil {
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native artifact permissions: %w", err)
		}
		prepareReq, backendSecret, err := a.withBackendPrepareReq(gen.PluginArtifactReloadPrepareReq{
			Manifest: authManifest, Abi: *candidateAbi, ArtifactPath: candidatePath, ArtifactHash: candidateHash,
			Assets: req.Assets,
		})
		if err != nil {
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		prepareStart := time.Now()
		prepareValue, prepareErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_prepare", prepareReq).Await()
		if prepareErr == nil {
			slog.Info("appmanager: reload phase", "app", req.ID, "phase", "prepare", "ms", time.Since(prepareStart).Milliseconds())
		}
		if prepareErr != nil {
			audit(false, prepareErr.Error())
			return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native reload prepare: %w", prepareErr)
		}
		prepared, ok := prepareValue.(gen.PluginArtifactReloadPrepareResp)
		if !ok || prepared.PluginID != req.ID || prepared.Token == "" {
			err := fmt.Errorf("appmanager: native reload prepare returned invalid response")
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		abort := func() {
			_, _ = planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_abort", gen.PluginArtifactReloadAbortReq{Token: prepared.Token}).Await()
		}
		status := prepared.Status
		status.State = normalizePluginState(status.State)
		status.Entrypoints = manifest.Entrypoints
		status.PackageHash = packageHash
		previous := rec
		previousManifest := manifest
		// A native reload may carry a replacement asset bundle; nil keeps
		// the installed one. The record copy makes the appmanager-side
		// state agree with what the pluginhost commit installs.
		if req.Assets != nil {
			rec.Assets = req.Assets
		}
		rec.Manifest = candidateManifest
		// The manifest's schema hashes and the descriptors form one unit:
		// OnStart re-validates the pair, so a reload that swaps only the
		// manifest leaves a record that fails re-registration with an
		// "app schema hash mismatch" after the next host restart.
		rec.SchemaDescriptors = req.SchemaDescriptors
		rec.Abi = candidateAbi
		rec.ArtifactPath = candidatePath
		rec.State = normalizePluginState(status.State)
		rec.Error = redactProcessCause(status.Error)
		rec.PackageHash = status.PackageHash
		rec.ArtifactHash = prepared.ArtifactHash
		rec.Generation++ // invalidate sessions bound to the pre-reload app
		if candidateAbi.Isolation == pluginhost.IsolationInProcess {
			// In-process deferral (A3): a c-shared library cannot be swapped
			// while the host lives (ReplaceArtifact/FreeLibrary crashes the
			// process), so the reload validates via prepare but does NOT
			// commit. The new artifact and asset bundle are persisted on the
			// pluginhost (ArtifactLoads / AssetStores) and the record lands
			// in restart_pending; the next host restart activates them
			// (pluginhost OnStart loads ArtifactLoads, then the appmanager
			// spawn-loop verification marks the app running).
			//
			// No PendingReloads marker is written: that marker exists to
			// recover a crash between Save and commit; here there is no
			// commit, and on restart the appmanager OnStart recovery would
			// not reach the pluginhost (it starts after the appmanager) and
			// would wrongly roll back to the previous record while the
			// pluginhost is about to activate the new artifact.
			if req.Assets != nil {
				if _, putErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.assets_put", gen.PluginAssetsPutReq{
					PluginID: manifest.ID, Assets: req.Assets,
				}).Await(); putErr != nil {
					abort()
					audit(false, putErr.Error())
					return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native reload deferral: persist assets: %w", putErr)
				}
			}
			loadReq, backendSecret, err := a.withBackendLoadReq(gen.PluginArtifactLoadReq{
				Manifest: authManifest, Abi: *candidateAbi, ArtifactPath: candidatePath, ArtifactHash: candidateHash,
			})
			if err != nil {
				abort()
				audit(false, err.Error())
				return gen.AppManagerReloadResp{}, err
			}
			loadValue, loadErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_load", loadReq).Await()
			if loadErr != nil {
				abort()
				audit(false, loadErr.Error())
				return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native reload deferral: persist artifact: %w", loadErr)
			}
			if loaded, ok := loadValue.(gen.PluginArtifactLoadResp); !ok || loaded.PluginID != req.ID {
				abort()
				err := fmt.Errorf("appmanager: native reload deferral returned invalid response")
				audit(false, err.Error())
				return gen.AppManagerReloadResp{}, err
			}
			// Close the candidate library handle opened by prepare; only the
			// old artifact stays mapped until restart.
			abort()
			rec.State = stateRestartPending
			rec.Error = "in-process artifact staged; activates on host restart"
			// The staged candidate was closed above: its listener (if any) is
			// dead, so drop the bootstrap state rather than advertise it.
			setBackendFromLoad(&rec, backendSecret, "")
			status.State = stateRestartPending
			status.Error = rec.Error
			a.withMu(func() {
				a.Records[req.ID] = rec
				a.Apps[req.ID] = candidateManifest
			})
			if err := a.Save(); err != nil {
				a.withMu(func() {
					a.Records[req.ID] = previous
					a.Apps[req.ID] = previousManifest
				})
				audit(false, err.Error())
				return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: persist native reload deferral: %w", err)
			}
			audit(true, "restart_pending")
			a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "restart_pending", ID: status.ID, Runtime: status.Runtime, State: status.State, Version: status.Version, Generation: rec.Generation})
			// The dependency's manifest changed on disk (ArtifactLoads carries
			// it); dependents' bundle expansions may be stale. In-process
			// dependents defer to restart_pending alongside this artifact.
			a.reexpandDependents(ctx, req.ID)
			return gen.AppManagerReloadResp{Status: status}, nil
		}
		a.withMu(func() {
			// Record the pending-reload marker so a crash between this Save and
			// the pluginhost commit can be recovered on restart (complete or
			// roll back). The marker is persisted atomically with the candidate
			// record below.
			if a.PendingReloads == nil {
				a.PendingReloads = map[string]pendingReload{}
			}
			a.PendingReloads[req.ID] = pendingReload{
				CandidateManifest:     candidateManifest,
				OldManifest:           previousManifest,
				OldRecord:             previous,
				CandidateArtifactPath: candidatePath,
				CandidateArtifactHash: candidateHash,
				CandidateAbi:          candidateAbi,
				CandidateAssets:       req.Assets,
				PackageHash:           status.PackageHash,
			}
			a.Records[req.ID] = rec
			a.Apps[req.ID] = candidateManifest
		})
		if err := a.Save(); err != nil {
			a.withMu(func() {
				delete(a.PendingReloads, req.ID)
				a.Records[req.ID] = previous
				a.Apps[req.ID] = previousManifest
			})
			abort()
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: persist native reload candidate: %w", err)
		}
		commitStart := time.Now()
		commitValue, commitErr := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.artifact_reload_commit", gen.PluginArtifactReloadCommitReq{Token: prepared.Token}).Await()
		if commitErr == nil {
			slog.Info("appmanager: reload phase", "app", req.ID, "phase", "commit", "ms", time.Since(commitStart).Milliseconds())
		}
		if commitErr != nil {
			abort()
			a.withMu(func() {
				delete(a.PendingReloads, req.ID)
				a.Records[req.ID] = previous
				a.Apps[req.ID] = previousManifest
			})
			if saveErr := a.Save(); saveErr != nil {
				audit(false, fmt.Sprintf("commit: %v; restore: %v", commitErr, saveErr))
				return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native reload commit: %v; restore state: %w", commitErr, saveErr)
			}
			audit(false, commitErr.Error())
			return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: native reload commit: %w", commitErr)
		}
		committed, ok := commitValue.(gen.PluginArtifactReloadCommitResp)
		if !ok || committed.PluginID != req.ID {
			err := fmt.Errorf("appmanager: native reload commit returned invalid response")
			audit(false, err.Error())
			return gen.AppManagerReloadResp{}, err
		}
		// The committed candidate process confirmed its listener (or none):
		// adopt its fresh per-instance secret and bootstrap URL. The
		// generation bump above already invalidated cookies minted against
		// the pre-reload instance.
		a.commitBackend(ctx, req.ID, backendSecret, committed.HttpAddr)
		// Reload committed — clear the pending-reload marker so OnStart
		// recovery does not see a stale marker. This Save is best-effort:
		// if it fails the marker persists but recovery will re-commit the
		// already-active candidate harmlessly or roll back to old.
		a.withMu(func() {
			delete(a.PendingReloads, req.ID)
		})
		_ = a.Save()
		// committed.Status is a skeleton (pluginhost's artifactStatus carries
		// only id/runtime/state/version/hashes/entrypoints): upserting it into
		// the frontend registry would strip name/callables/bundles and the
		// backend info until the next sync. Rebuild the full status from the
		// committed record — commitBackend just wrote the fresh listener into
		// it — so the response and the registry agree.
		a.withMu(func() {
			if stored, ok := a.Records[req.ID]; ok {
				status = a.statusFromRecord(candidateManifest, stored)
				return
			}
			status = committed.Status
			status.State = normalizePluginState(status.State)
			status.PackageHash = packageHash
			status.Generation = rec.Generation
		})
		audit(true, "")
		// The committed exe supersedes every older content-addressed build of
		// this app (any version); sweep them now (locked files are skipped,
		// the orphan reaper unlocks them at the next host start).
		if n := a.gcSupersededArtifacts(rec.ArtifactPath, pluginhost.PluginArtifactSweepPrefix(candidateManifest.Name)); n > 0 {
			slog.Info("appmanager: removed superseded plugin artifacts", "app", req.ID, "files", n)
		}
		a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "reloaded", ID: status.ID, Runtime: status.Runtime, State: status.State, Version: status.Version, Generation: rec.Generation})
		// The committed manifest replaced the dependency's callables/bundles;
		// dependents holding bundle-level plugin.* permissions must re-expand
		// so the host bridge gates on the fresh callID set.
		a.reexpandDependents(ctx, req.ID)
		// Re-sync the dedicated plugin agent (display name / prompt overlay /
		// bound bundle cards may have changed with the new manifest). Log-only:
		// the artifact reload already committed.
		if err := a.reconcilePluginAgent(ctx, req.ID); err != nil {
			ctx.Logger().Error("appmanager: reconcile app agent after reload failed", "app", req.ID, "error", err)
		}
		return gen.AppManagerReloadResp{Status: status}, nil
	}
	if manifest.Runtime != "spore" {
		err := fmt.Errorf("appmanager: unsupported reload runtime %q", manifest.Runtime)
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	canonical, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	ref, ok := ctx.LookupID(id.From(canonical))
	if !ok || ref == nil {
		err := fmt.Errorf("appmanager: child for %q not found", req.ID)
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	planner := ctx.Planner()
	if planner == nil {
		err := fmt.Errorf("appmanager: planner not available")
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	value, err := planner.Call(ctx.Lifecycle(), ref, "sporeapp.reload", gen.SporeAppReloadReq{
		ID: req.ID, EntryModule: req.EntryModule, Modules: req.Modules, Assets: req.Assets, SchemaDescriptors: req.SchemaDescriptors,
		PackageHash: req.PackageHash, ExpectedStateVersion: req.ExpectedStateVersion,
		MigratedState: req.MigratedState,
	}).Await()
	if err != nil {
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	resp, ok := value.(gen.SporeAppReloadResp)
	if !ok {
		err := fmt.Errorf("appmanager: reload returned unexpected response type")
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, err
	}
	status := gen.AppStatus{
		ID:          resp.ID,
		Runtime:     manifest.Runtime,
		State:       "running",
		Version:     resp.Version,
		PackageHash: req.PackageHash,
		Entrypoints: manifest.Entrypoints,
	}
	if req.CandidateManifest != nil {
		status.Entrypoints = req.CandidateManifest.Entrypoints
	}
	var previous appRecord
	var previousManifest gen.AppManifest
	var generation int64
	a.withMu(func() {
		record := a.Records[req.ID]
		previous = record
		record.EntryModule = req.EntryModule
		record.Modules = req.Modules
		record.Assets = req.Assets
		record.SchemaDescriptors = req.SchemaDescriptors
		// The manifest's schema hashes and the descriptors form one unit:
		// OnStart re-validates the pair, so a reload that swaps only the
		// descriptors leaves a record that fails re-registration with an
		// "app schema hash mismatch" after the next host restart.
		// reload_project passes the candidate manifest for both runtimes;
		// a direct reload without one keeps the previous manifest.
		if req.CandidateManifest != nil {
			record.Manifest = *req.CandidateManifest
		}
		record.PackageHash = req.PackageHash
		record.State = status.State
		record.Error = ""
		record.Generation++ // invalidate sessions bound to the pre-reload app
		status.Generation = record.Generation
		generation = record.Generation
		a.Records[req.ID] = record
		if req.CandidateManifest != nil {
			previousManifest = manifest
			a.Apps[req.ID] = *req.CandidateManifest
		}
	})
	if err := a.Save(); err != nil {
		a.withMu(func() {
			a.Records[req.ID] = previous
			if req.CandidateManifest != nil {
				a.Apps[req.ID] = previousManifest
			}
		})
		audit(false, err.Error())
		return gen.AppManagerReloadResp{}, fmt.Errorf("appmanager: persist reload: %w", err)
	}
	audit(true, "")
	a.emitLifecycleEvent(ctx, gen.AppLifecycleEvent{Kind: "reloaded", ID: status.ID, Runtime: status.Runtime, State: status.State, Version: status.Version, Generation: generation})
	// Re-sync the dedicated plugin agent (display name / prompt overlay /
	// bound bundle cards may have changed with the new manifest). Log-only:
	// the app reload already committed.
	if err := a.reconcilePluginAgent(ctx, req.ID); err != nil {
		ctx.Logger().Error("appmanager: reconcile app agent after reload failed", "app", req.ID, "error", err)
	}
	return gen.AppManagerReloadResp{Status: status, StateVersion: resp.StateVersion}, nil
}
