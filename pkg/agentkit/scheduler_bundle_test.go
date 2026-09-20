package agentkit

import (
	"sort"
	"testing"
)

// schedulerBundleTools is the scheduler bundle's declared tool surface: the
// project callables that inspect, toggle and manually fire the project's timer
// cards. Kept sorted so the assertion is order-independent.
var schedulerBundleTools = []string{
	"project.wiki_list_timers",
	"project.wiki_toggle_timer",
	"project.wiki_trigger_timer_card",
}

// TestSchedulerBundleRegistered verifies builtin:bundle:scheduler is registered
// as a mode-managed tool-usage bundle declaring exactly the three scheduler
// callables that no other bundle exposed.
func TestSchedulerBundleRegistered(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:bundle:scheduler" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:bundle:scheduler not registered")
	}
	if asset.Type != "bundle" {
		t.Fatalf("builtin:bundle:scheduler type = %q, want bundle", asset.Type)
	}
	if !asset.ModeManaged {
		t.Fatal("builtin:bundle:scheduler must be modeManaged (mounted by builtin:mode:scheduler)")
	}
	if !asset.Protected {
		t.Fatal("builtin:bundle:scheduler must be protected")
	}
	got := append([]string(nil), asset.Tools...)
	sort.Strings(got)
	want := append([]string(nil), schedulerBundleTools...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("builtin:bundle:scheduler tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("builtin:bundle:scheduler tools = %v, want %v", got, want)
		}
	}
}

// TestSchedulerModeRequiresSchedulerBundle verifies the scheduler mode pulls in
// the scheduler bundle via its requires list, mirroring goal-mode ->
// builtin:bundle:goal and workflow-mode -> builtin:bundle:workflow-tools.
func TestSchedulerModeRequiresSchedulerBundle(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var mode *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:scheduler" {
			mode = &assets[i]
			break
		}
	}
	if mode == nil {
		t.Fatal("builtin:mode:scheduler not registered")
	}
	for _, dep := range mode.Dependencies {
		if dep == "builtin:bundle:scheduler" {
			return
		}
	}
	t.Fatalf("builtin:mode:scheduler requires = %v, want to include builtin:bundle:scheduler", mode.Dependencies)
}

// TestCallableIDsForBundles_SchedulerBundle verifies the bundle resolves to its
// three callables through the standard bundle -> callable path.
func TestCallableIDsForBundles_SchedulerBundle(t *testing.T) {
	got := CallableIDsForBundles([]string{"builtin:bundle:scheduler"})
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, want := range schedulerBundleTools {
		if !seen[want] {
			t.Errorf("CallableIDsForBundles(builtin:bundle:scheduler) missing %q; got %v", want, got)
		}
	}
	if len(got) != len(schedulerBundleTools) {
		t.Errorf("CallableIDsForBundles(builtin:bundle:scheduler) = %v, want %v", got, schedulerBundleTools)
	}
}
