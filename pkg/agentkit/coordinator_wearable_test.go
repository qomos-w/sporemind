package agentkit

import (
	"sort"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestCoordinatorWearableBundleAllowlist is the acceptance check for the
// Coordinator-only high-level wearable bundle: it must expose exactly the two
// high-level coordinator.wearable tools and nothing else. The low-level
// glass_interact render/speak/debug callables must never leak through it.
func TestCoordinatorWearableBundleAllowlist(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:coordinator-wearable"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:coordinator-wearable resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	want := []string{"coordinator_wearable_notify", "coordinator_wearable_call"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("coordinator-wearable bundle missing callable %q (got %v)", w, ids)
		}
	}
	for _, leak := range []string{
		"glass_interact.render",
		"glass_interact.speak",
		"glass_interact.debug_state",
		"glass_interact.capabilities_get",
		"glass_interact.event_enqueue",
		"glass_interact.event_complete",
		"glass_interact.inbox_state",
		"glass_interact.updater_tick",
		"glass_interact.transcript_latest",
	} {
		if got[leak] {
			t.Errorf("coordinator-wearable bundle leaks low-level callable %q (got %v)", leak, ids)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("coordinator-wearable bundle resolves %v, want exactly %v", ids, want)
	}
}

// TestCoordinatorWearableBundleMountedOnlyOnCoordinator guards that the
// wearable bundle is mounted on the Coordinator kind and on no other kind, so
// no other LLM ever sees the wearable tools.
func TestCoordinatorDefaultBundles(t *testing.T) {
	var bundles []string
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == domain.AgentKindCoordinator {
			bundles = cfg.DefaultBundleIDs
			break
		}
	}
	if len(bundles) == 0 {
		t.Fatal("coordinator kind config not found")
	}
	mounted := make(map[string]bool, len(bundles))
	for _, bundle := range bundles {
		mounted[bundle] = true
	}
	for _, want := range []string{
		"builtin:bundle:interface-controls",
		"builtin:bundle:browser-tools",
		"builtin:bundle:computeruse-tools",
	} {
		if !mounted[want] {
			t.Errorf("coordinator does not mount default bundle %q (got %v)", want, bundles)
		}
	}

	tools := make(map[string]bool)
	for _, id := range CallableIDsForBundles(bundles) {
		tools[id] = true
	}
	for _, want := range []string{
		"interfacemanager.control",
		"open_global_browser",
		"computeruse.screenshot",
		"computeruse.interact",
	} {
		if !tools[want] {
			t.Errorf("coordinator default bundles do not expose %q", want)
		}
	}
}

func TestCoordinatorWearableBundleMountedOnlyOnCoordinator(t *testing.T) {
	cfgs := BaseKindConfigs()
	var mounted []string
	for _, c := range cfgs {
		for _, b := range c.DefaultBundleIDs {
			if b == "builtin:bundle:coordinator-wearable" {
				mounted = append(mounted, c.Kind)
			}
		}
	}
	sort.Strings(mounted)
	if len(mounted) != 1 || mounted[0] != string(domain.AgentKindCoordinator) {
		t.Fatalf("coordinator-wearable bundle mounted on %v, want exactly [%s]", mounted, domain.AgentKindCoordinator)
	}
}

// TestCoordinatorWearableNotDevOnly guards the production enablement: the
// wearable bundle is mounted on the coordinator kind defaults in every build
// type, and IsDevOnlyBundle no longer classifies it.
func TestCoordinatorWearableNotDevOnly(t *testing.T) {
	if IsDevOnlyBundle("builtin:bundle:coordinator-wearable") {
		t.Fatal("IsDevOnlyBundle must not classify coordinator-wearable (production-enabled)")
	}
	if IsDevOnlyBundle("builtin:bundle:web-search") {
		t.Fatal("IsDevOnlyBundle must not flag regular bundles")
	}
	for _, buildTypes := range []string{"dev", "release", "beta"} {
		var coordinatorHasWearable bool
		for _, cfg := range BaseKindConfigs() {
			if cfg.Kind != domain.AgentKindCoordinator {
				continue
			}
			for _, b := range cfg.DefaultBundleIDs {
				if b == "builtin:bundle:coordinator-wearable" {
					coordinatorHasWearable = true
				}
			}
		}
		if !coordinatorHasWearable {
			t.Fatalf("%s build coordinator defaults must list coordinator-wearable", buildTypes)
		}
	}
}

// TestCoordinatorWearableToolsOwnedByAgentKind verifies that the wearable
// callables are registered on coordinator-kind agents (not on coder/general),
// matching the runtime registration gate in pkg/actor/agent/agent.go.
func TestCoordinatorWearableToolsOwnedByAgentKind(t *testing.T) {
	cfgs := BaseKindConfigs()
	for _, c := range cfgs {
		if c.Kind == string(domain.AgentKindCoordinator) {
			ids := CallableIDsForBundles(c.DefaultBundleIDs)
			found := map[string]bool{}
			for _, id := range ids {
				found[id] = true
			}
			for _, w := range []string{"coordinator_wearable_notify", "coordinator_wearable_call"} {
				if !found[w] {
					t.Errorf("coordinator kind tools missing %q", w)
				}
			}
			continue
		}
		ids := CallableIDsForBundles(c.DefaultBundleIDs)
		for _, id := range ids {
			if id == "coordinator_wearable_notify" || id == "coordinator_wearable_call" {
				t.Errorf("kind %q must not mount wearable tool %q", c.Kind, id)
			}
		}
	}
}
