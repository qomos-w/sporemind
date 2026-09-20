package agentkit

import (
	"testing"
)

// TestWorkflowModeCardRegistered verifies builtin:mode:workflow is registered
// in the builtin registry with mode metadata and is projected for slash-mode
// discovery (workspace.builtin.modes.list / the "/workflow" composer
// interception).
func TestWorkflowModeCardRegistered(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:workflow" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:mode:workflow not registered")
	}
	if asset.Type != "mode" {
		t.Fatalf("builtin:mode:workflow type = %q, want mode", asset.Type)
	}
	if asset.BuiltinTitle == "" {
		t.Fatal("builtin:mode:workflow must derive a human-readable title from Markdown frontmatter")
	}

	modes, err := BuiltinModeCards()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range modes {
		if m.CardID == "builtin:mode:workflow" {
			found = true
			if m.Name != "workflow" {
				t.Fatalf("workflow mode slash name = %q, want workflow", m.Name)
			}
		}
	}
	if !found {
		t.Fatal("builtin:mode:workflow missing from BuiltinModeCards")
	}
}

// TestWorkflowModeCardRequiresWorkflowToolsBundle verifies the layered mode
// pattern: mounting builtin:mode:workflow grants the workflow orchestration
// tools by requiring builtin:bundle:workflow-tools, mirroring how
// builtin:mode:goal requires builtin:bundle:goal.
func TestWorkflowModeCardRequiresWorkflowToolsBundle(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:workflow" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:mode:workflow not registered")
	}
	found := false
	for _, dep := range asset.Dependencies {
		if dep == "builtin:bundle:workflow-tools" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("builtin:mode:workflow requires builtin:bundle:workflow-tools, got dependencies %v", asset.Dependencies)
	}
}

// TestWorkflowModeCardExposesStartStopTools verifies that the workflow
// lifecycle tools — workflow_plan_submit, workflow_start and workflow_stop —
// are listed in the workflow mode's tools so they are only available as LLM
// tools while workflow mode is active.
func TestWorkflowModeCardExposesStartStopTools(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:workflow" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:mode:workflow not registered")
	}
	want := []string{"workflow_plan_submit", "workflow_start", "workflow_stop"}
	for _, id := range want {
		found := false
		for _, got := range asset.Tools {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("builtin:mode:workflow missing tool %q in %v", id, asset.Tools)
		}
	}
}
