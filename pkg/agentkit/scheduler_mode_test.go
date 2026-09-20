package agentkit

import "testing"

// TestSchedulerModeCardRegistered verifies builtin:mode:scheduler is registered
// in the builtin registry with mode metadata (type, flow, lifecycle) and is
// projected for slash-mode discovery (workspace.builtin.modes.list / the
// "/scheduler" composer interception) just like goal/workflow modes.
func TestSchedulerModeCardRegistered(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:scheduler" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:mode:scheduler not registered")
	}
	if asset.Type != "mode" {
		t.Fatalf("builtin:mode:scheduler type = %q, want mode", asset.Type)
	}
	if asset.BuiltinTitle == "" {
		t.Fatal("builtin:mode:scheduler must derive a human-readable title from Markdown frontmatter")
	}
	if asset.Icon == "" {
		t.Fatal("builtin:mode:scheduler must declare an icon for badge rendering")
	}
	if asset.Flow != "orchestration" {
		t.Fatalf("builtin:mode:scheduler flow = %q, want orchestration (mutually exclusive with goal/workflow)", asset.Flow)
	}
	if !asset.LifecycleManaged {
		t.Fatal("builtin:mode:scheduler must be lifecycleManaged (system-mounted like goal/workflow)")
	}
	if len(asset.Tools) != 0 {
		t.Fatalf("builtin:mode:scheduler tools should be minimal, got %v", asset.Tools)
	}
	if body := string(asset.Body); body == "" {
		t.Fatal("builtin:mode:scheduler must carry a scheduling-execution system prompt body")
	}

	modes, err := BuiltinModeCards()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range modes {
		if m.CardID == "builtin:mode:scheduler" {
			found = true
			if m.Name != "scheduler" {
				t.Fatalf("scheduler mode slash name = %q, want scheduler", m.Name)
			}
		}
	}
	if !found {
		t.Fatal("builtin:mode:scheduler missing from BuiltinModeCards")
	}
}

// TestBuiltinModesFlowOrchestrationIncludesScheduler verifies the scheduler
// mode joins the orchestration flow group shared by goal and workflow, which
// is what makes the three modes mutually exclusive.
func TestBuiltinModesFlowOrchestrationIncludesScheduler(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	flowModes := map[string]bool{
		"builtin:mode:goal":      false,
		"builtin:mode:workflow":  false,
		"builtin:mode:scheduler": false,
	}
	for _, asset := range assets {
		if _, want := flowModes[asset.Title]; !want {
			continue
		}
		if asset.Flow != "orchestration" {
			t.Errorf("expected %s to have flow: orchestration, got %q", asset.Title, asset.Flow)
		}
		flowModes[asset.Title] = true
	}
	for id, found := range flowModes {
		if !found {
			t.Errorf("%s not found in builtin assets", id)
		}
	}
}
