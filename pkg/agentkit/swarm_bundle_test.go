package agentkit

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestSwarmBundleDeclaresSwarmSurface pins the swarm bundle contract: it must
// exist as a builtin card and resolve to the agent-to-agent swarm callables
// (spawn + observe + message + terminate). The spawn handler re-attaches this
// bundle to every swarm child, so these entries are the recursion capability.
func TestSwarmBundleDeclaresSwarmSurface(t *testing.T) {
	found := false
	for _, c := range BuiltinCards {
		if c.Title == SwarmBundleID {
			found = true
		}
	}
	if !found {
		t.Fatalf("%s missing from BuiltinCards", SwarmBundleID)
	}

	tools := CallableIDsForBundles([]string{SwarmBundleID})
	want := map[string]bool{
		"workspace.agent_spawn_swarm":  false,
		"workspace.list_agents":        false,
		"workspace.agent_send_message": false,
		"workspace.agent_terminate":    false,
	}
	for _, id := range tools {
		if _, ok := want[id]; ok {
			want[id] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("swarm bundle must declare %s", id)
		}
	}

	// The kinds that mount the bundle by default spawn-capable agents; the
	// swarm handler rejects read-only kinds, so none of these may be read-only.
	for _, kind := range BaseKindConfigs() {
		hasSwarm := false
		for _, id := range kind.DefaultBundleIDs {
			if id == SwarmBundleID {
				hasSwarm = true
			}
		}
		if hasSwarm && IsReadOnlyAgentKind(kind.Kind) {
			t.Errorf("read-only kind %q must not mount the swarm bundle", kind.Kind)
		}
	}

	_ = domain.MaxSwarmDepth // keep the domain contract referenced in one place
}
