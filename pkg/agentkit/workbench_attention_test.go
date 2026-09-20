package agentkit

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestWorkbenchAttentionBundleAllowlist pins the workbench controller bundle's
// tool surface: exactly the six workbench.* callables, nothing else.
func TestWorkbenchAttentionBundleAllowlist(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:workbench-attention"})
	want := []string{
		"workbench.attention_report",
		"workbench.snapshot",
		"workbench.promote",
		"workbench.set_pinned",
		"workbench.set_hidden",
		"workbench.upsert_card",
		"workbench.set_frozen",
		"workbench.set_maximized",
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("workbench-attention bundle missing callable %q (got %v)", w, ids)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("workbench-attention bundle resolves %v, want exactly %v", ids, want)
	}
}

// TestWorkbenchAttentionBundleOnCoordinator guards the grant: the controller
// bundle is mounted on the coordinator kind by default and on no other kind —
// only the coordinator may steer the user's attention.
func TestWorkbenchAttentionBundleOnCoordinator(t *testing.T) {
	const bundleID = "builtin:bundle:workbench-attention"
	coordinatorHas := false
	for _, cfg := range BaseKindConfigs() {
		found := false
		for _, id := range cfg.DefaultBundleIDs {
			if id == bundleID {
				found = true
				break
			}
		}
		switch cfg.Kind {
		case domain.AgentKindCoordinator:
			coordinatorHas = found
		default:
			if found {
				t.Errorf("kind %s also mounts %s; it is coordinator-only", cfg.Kind, bundleID)
			}
		}
	}
	if !coordinatorHas {
		t.Errorf("coordinator kind does not mount %s in DefaultBundleIDs", bundleID)
	}
}

// TestWorkbenchAttentionBundleNotDevOnly ensures the controller bundle ships
// outside dev builds.
func TestWorkbenchAttentionBundleNotDevOnly(t *testing.T) {
	if IsDevOnlyBundle("builtin:bundle:workbench-attention") {
		t.Fatal("workbench-attention must not be dev-only")
	}
}
