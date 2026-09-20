package appmanager

import (
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// plugin_agent lifecycle glue. An app whose manifest declares
// agent_binding.plugin_agents entries owns one dedicated workspace-global
// agent per binding slot (created coordinator-position via
// workspace.create_app_agent, keyed by (BoundAppID, BoundAppSlot)).
// appmanager drives the whole lifecycle:
//
//   - register          → one create per declared slot (failure fails the
//                         registration)
//   - reload / recovery → one create per declared slot (reconcile prompt
//                         overlay + bundle mounts; log-only failure, the
//                         artifact reload already committed)
//   - unregister/cleanup→ workspace.remove_app_agent (AppId cascade: every
//                         slot of the app)
//   - OnStart restore   → create sweep over every registered app
//
// The bound bundle card list is recomputed from the current manifest on every
// create: the app's own virtual app-bundle cards plus the slot's declared
// extra bundle cards (e.g. builtin:bundle:web-search). If a reload drops all
// plugin_agent blocks, reconcile removes the leftover agents instead. A
// reload that drops only some slots cannot be reconciled per-slot
// (remove_app_agent cascades the whole app), so a stale slot agent lingers
// until unregister or a full drop.

// pluginAgentBundleCards computes the bundle card ids for a plugin agent bind:
// the app's own projected app-bundle:{appID}:{slug} cards followed by the
// manifest-declared extras (which may reference builtin bundle cards only —
// mounting another app's bundles requires that app's own consent surface).
func pluginAgentBundleCards(manifest gen.AppManifest, binding *gen.PluginAgentBinding) []string {
	own := bundleCardIDs(manifest.ID, manifest.Bundles)
	if len(binding.Bundles) == 0 {
		return own
	}
	seen := make(map[string]struct{}, len(own)+len(binding.Bundles))
	cards := make([]string, 0, len(own)+len(binding.Bundles))
	for _, id := range append(append([]string(nil), own...), binding.Bundles...) {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		cards = append(cards, id)
	}
	return cards
}

// workspaceRef resolves the workspace service ref and planner, or an error
// explaining what is missing.
func workspaceRef(ctx actor.Context) (actor.Planner, ref.Ref, error) {
	planner := ctx.Planner()
	wsRef, found := ctx.LookupService(workspaceServiceName)
	if planner == nil || !found {
		return nil, nil, fmt.Errorf("appmanager: workspace service unavailable")
	}
	return planner, wsRef, nil
}

// reconcilePluginAgent brings the dedicated agents for appID in sync with the
// currently active manifest: one workspace.create_app_agent call per declared
// binding slot. Called on register (strict), reload and OnStart restore
// (best-effort by the caller's choice).
func (a *Actor) reconcilePluginAgent(ctx actor.Context, appID string) error {
	var manifest gen.AppManifest
	var bindings []gen.PluginAgentBinding
	a.withMu(func() {
		var ok bool
		manifest, ok = a.Apps[appID]
		if ok && manifest.AgentBinding != nil {
			bindings = manifest.AgentBinding.PluginAgents
		}
	})

	planner, wsRef, err := workspaceRef(ctx)
	if len(bindings) == 0 {
		// No plugin_agent blocks: remove any leftover agents from a previous
		// manifest revision (e.g. a reload that dropped all blocks).
		// Best-effort — hosts that run without a workspace service must still
		// register plugin-less apps.
		if err != nil {
			return nil
		}
		if _, err := planner.Call(ctx.Lifecycle(), wsRef, "workspace.remove_app_agent",
			gen.WorkspaceRemoveAppAgentReq{AppID: appID}).Await(); err != nil {
			ctx.Logger().Warn("appmanager: stale app agent removal failed", "app", appID, "error", err)
		}
		return nil
	}
	if err != nil {
		return err
	}
	for i := range bindings {
		binding := &bindings[i]
		ensureValue, err := planner.Call(ctx.Lifecycle(), wsRef, "workspace.create_app_agent",
			gen.WorkspaceCreateAppAgentReq{
				AppID:         appID,
				Slot:          binding.Name,
				DisplayName:   binding.DisplayName,
				SystemPrompt:  binding.SystemPrompt,
				BundleCardIds: pluginAgentBundleCards(manifest, binding),
				Model:         binding.Model,
			}).Await()
		if err != nil {
			return fmt.Errorf("appmanager: create app agent for %q slot %q: %w", appID, binding.Name, err)
		}
		ensureResp, _ := ensureValue.(gen.WorkspaceEnsureAppAgentResp)
		if err := a.bindPluginAgentSurface(manifest, binding, ensureResp.ActorID); err != nil {
			return fmt.Errorf("appmanager: bind plugin agent surface for %q slot %q: %w", appID, binding.Name, err)
		}
	}
	return nil
}

// reconcilePluginAgentsOnStart is the OnStart restore sweep: re-assert the
// dedicated agents for every registered app so they survive host restarts
// (the workspace registry rows persist, but their actors and bind state are
// re-established here). Failures are logged, not fatal — the app itself is
// already running and the next lifecycle event re-tries.
func (a *Actor) reconcilePluginAgentsOnStart(ctx actor.Context) {
	var appIDs []string
	a.withMu(func() {
		appIDs = make([]string, 0, len(a.Apps))
		for appID, manifest := range a.Apps {
			if manifest.AgentBinding != nil && len(manifest.AgentBinding.PluginAgents) > 0 {
				appIDs = append(appIDs, appID)
			}
		}
	})
	for _, appID := range appIDs {
		if err := a.reconcilePluginAgent(ctx, appID); err != nil {
			ctx.Logger().Error("appmanager: restore app agent failed", "app", appID, "error", err)
		}
	}
}

// removePluginAgent is called from the unregister cleanup path: the app is
// gone, so its dedicated agent must go with it (cascade delete by appID).
// Log-only: a workspace hiccup must not wedge the app in cleanup_pending, and
// a leftover agent is recoverable by a later reconcile/unregister retry.
func (a *Actor) removePluginAgent(ctx actor.Context, appID string) {
	planner, wsRef, err := workspaceRef(ctx)
	if err != nil {
		ctx.Logger().Error("appmanager: cannot remove app agent", "app", appID, "error", err)
		return
	}
	a.unbindPluginAgentSurface(appID)
	if _, err := planner.Call(ctx.Lifecycle(), wsRef, "workspace.remove_app_agent",
		gen.WorkspaceRemoveAppAgentReq{AppID: appID}).Await(); err != nil {
		ctx.Logger().Error("appmanager: remove app agent failed", "app", appID, "error", err)
	}
}

// bindPluginAgentSurface activates the appbinding surface registry (so far
// only read by authorizeCastEmit) for the dedicated plugin agent: with a
// live binding the bound agent can cast/emit this app's events. The binding
// key is the caller identity the turn engine stamps (AgentId = the agent's
// ACTOR id), so bind by ActorID when available, else the agent name — and
// keep both keys in sync when the reconcile re-runs. Entrypoint defaults to
// the manifest's first view entrypoint (surface semantics, not routing).
// The pluginAgentSurfaceIDs entry is keyed appID+slot so multiple bindings
// of the same app track their own agent.
func (a *Actor) bindPluginAgentSurface(manifest gen.AppManifest, binding *gen.PluginAgentBinding, agentActorID string) error {
	entry := ""
	for _, ep := range manifest.Entrypoints {
		if ep.Kind == "view" {
			entry = ep.ID
			break
		}
	}
	if entry == "" && len(manifest.Entrypoints) > 0 {
		entry = manifest.Entrypoints[0].ID
	}
	events := map[string]struct{}{}
	for _, e := range manifest.Events {
		events[e.ID] = struct{}{}
	}
	if a.bindings == nil {
		a.bindings = appbinding.NewRegistry()
	}
	if agentActorID != "" {
		if err := a.bindings.BindSurface(appbinding.SurfaceBinding{
			AppID: manifest.ID, AgentID: agentActorID, Entrypoint: entry, Events: events,
		}); err != nil {
			return err
		}
		a.withMu(func() {
			a.pluginAgentSurfaceIDs[pluginAgentSurfaceKey(manifest.ID, binding.Name)] = agentActorID
		})
	}
	return nil
}

// pluginAgentSurfaceKey composes the pluginAgentSurfaceIDs map key: one entry
// per (app, binding slot).
func pluginAgentSurfaceKey(appID, slot string) string { return appID + "\x00" + slot }

// unbindPluginAgentSurface drops any surface bindings for the app. The
// registry is keyed (appID, agentID) and has no per-app unbind, so the actor
// tracks the agent id each reconcile bound (pluginAgentSurfaceIDs, keyed
// appID+slot). After the bound agents are torn down, stale entries are inert
// — HasBinding only fires for callers presenting that agent's identity,
// which no longer exists — but dropping the tracked ids keeps the map honest
// on re-register.
func (a *Actor) unbindPluginAgentSurface(appID string) {
	var agentIDs []string
	a.withMu(func() {
		for key, agentID := range a.pluginAgentSurfaceIDs {
			if strings.HasPrefix(key, appID+"\x00") {
				agentIDs = append(agentIDs, agentID)
				delete(a.pluginAgentSurfaceIDs, key)
			}
		}
	})
	if a.bindings != nil {
		for _, agentID := range agentIDs {
			a.bindings.Unbind(appID, agentID)
		}
	}
}
