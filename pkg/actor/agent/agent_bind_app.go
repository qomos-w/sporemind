package agent

import (
	"fmt"
	"slices"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// BoundAppID returns the app (plugin) id this agent was provisioned for via
// agent_bind_app, or "" for agents not provisioned by an app registration.
func (a *Actor) BoundAppID() string {
	a.boundAppMu.RLock()
	defer a.boundAppMu.RUnlock()
	return a.boundAppID
}

// BoundAppPrompt returns the per-app system prompt overlay ("" when none).
func (a *Actor) BoundAppPrompt() string {
	a.boundAppMu.RLock()
	defer a.boundAppMu.RUnlock()
	return a.boundAppPrompt
}

// removeComponentCardMount removes one mount (both cardRefs and
// ComponentMounts projections). Returns true when a mount was actually
// removed. Does not touch revision/snapshot — callers batch that.
func (a *Actor) removeComponentCardMount(cardID string) bool {
	removed := false
	if a.cardRefs != nil {
		kept := a.cardRefs[:0]
		for _, r := range a.cardRefs {
			if r.ID == cardID {
				removed = true
				continue
			}
			kept = append(kept, r)
		}
		if removed {
			a.cardRefs = kept
			a.ComponentMounts = componentMountsFromCardRefs(a.cardRefs)
		}
	} else {
		kept := a.ComponentMounts[:0]
		for _, m := range a.ComponentMounts {
			if m.CardID == cardID {
				removed = true
				continue
			}
			kept = append(kept, m)
		}
		if removed {
			a.ComponentMounts = kept
			a.syncCanonicalCardRefs()
		}
	}
	if removed {
		a.ComponentRevision++
	}
	return removed
}

// handleBindApp implements agent_bind_app (internal): the workspace calls it
// right after spawning a plugin agent and again after app reload. It stores
// the binding (app id + prompt overlay, persisted via the mailbox snapshot)
// and reconciles the bound bundle mounts to the desired list — mounting
// missing entries and unmounting bundles that are no longer bound. The
// desired list is the full list computed by appmanager (app-bundle cards plus
// declared builtin extras); nil means no bound bundles.
func (a *Actor) handleBindApp(ctx actor.Context, req gen.AgentBindAppReq) error {
	if req.AppID == "" {
		return fmt.Errorf("agent_bind_app: AppId is required")
	}
	if a.agentKind != domain.AgentKindPlugin {
		return fmt.Errorf("agent_bind_app: agent kind %q cannot be bound to an app (only %q agents are app-provisioned)", a.agentKind, domain.AgentKindPlugin)
	}

	desired := req.BundleCardIds
	mountsChanged := false
	for _, id := range desired {
		if id == "" {
			continue
		}
		mountsChanged = a.ensureComponentCardMounted(ctx, id, "app", true) || mountsChanged
	}
	a.boundAppMu.Lock()
	for _, prev := range a.boundBundleCardIDs {
		if slices.Contains(desired, prev) {
			continue
		}
		mountsChanged = a.removeComponentCardMount(prev) || mountsChanged
	}
	a.boundAppID = req.AppID
	a.boundAppPrompt = req.SystemPrompt
	a.boundBundleCardIDs = append([]string(nil), desired...)
	a.boundAppMu.Unlock()

	if mountsChanged {
		a.invalidateComponentSnapshot(ctx)
		a.stageToolsRefresh()
	} else {
		// The prompt overlay may still have changed even when mounts did not.
		a.invalidateComponentSnapshot(ctx)
	}
	a.saveMailbox(ctx)
	ctx.Logger().Info("agent: bound to app", "appId", req.AppID, "bundles", len(desired), "mountsChanged", mountsChanged)
	return nil
}
