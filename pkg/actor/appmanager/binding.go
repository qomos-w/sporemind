package appmanager

import (
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// freeAgentWildcard is the agent ID used to register free-agent policies
// that are not tied to a specific agent. When a free_agent block is declared
// in .appdef without an agent_binding (the only valid model post-agent_binding
// removal), the policy is registered under this wildcard so any authenticated
// caller can be authorized against it.
const freeAgentWildcard = "*"

// freeAgentKey returns the lookup key for the free-agent policy map.
func freeAgentKey(appID string) string {
	return appID + "\x00" + freeAgentWildcard
}

// bindFreeAgentPolicy registers the free-agent policy declared by a manifest
// onto the actor's policy map. The map is shared across lanes (register /
// register_project write it on owner and ops lanes; agent_action reads it on
// the ops lane), so the write takes a.mu. Callers must NOT hold a.mu.
func (a *Actor) bindFreeAgentPolicy(manifest gen.AppManifest) error {
	binding := manifest.AgentBinding
	if binding == nil || binding.FreeAgent == nil {
		return nil
	}
	kinds := make(map[string]struct{}, len(binding.FreeAgent.AgentKinds))
	for _, kind := range binding.FreeAgent.AgentKinds {
		kinds[kind] = struct{}{}
	}
	a.withMu(func() {
		a.FreeAgentPolicies[freeAgentKey(manifest.ID)] = appbinding.FreeAgentPolicy{
			AllowCreate:  binding.FreeAgent.AllowCreate,
			AllowSwitch:  binding.FreeAgent.AllowSwitch,
			AllowMessage: binding.FreeAgent.AllowMessage,
			AgentKinds:   kinds,
		}
	})
	return nil
}

// unbindFreeAgentPolicy removes the free-agent policy for a manifest from the
// actor's policy map. See bindFreeAgentPolicy for the locking rationale.
func (a *Actor) unbindFreeAgentPolicy(manifest gen.AppManifest) {
	if manifest.AgentBinding != nil && manifest.AgentBinding.FreeAgent != nil {
		a.withMu(func() {
			delete(a.FreeAgentPolicies, freeAgentKey(manifest.ID))
		})
	}
}

// authorizeFreeAgent checks the wildcard free-agent policy for the app. When a
// wildcard policy exists but denies the action, the denial is authoritative;
// fall back to a direct agent binding only when no wildcard exists at all
// (legacy manifests with Surface.AgentID).
func authorizeFreeAgent(policies map[string]appbinding.FreeAgentPolicy, appID, agentID, action, kind string) error {
	p, ok := policies[freeAgentKey(appID)]
	if !ok {
		// No wildcard policy — fall back to the agent-specific key.
		p, ok = policies[appID+"\x00"+agentID]
	}
	if !ok {
		return appbinding.Deny(appbinding.CodeBindingMissing, "no free-agent binding for app "+appID+" agent "+agentID)
	}
	return p.Authorize(action, kind)
}
