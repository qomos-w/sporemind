package agentkit

import (
	"strings"
	"testing"
)

func TestBundleToolDescriptionGaps(t *testing.T) {
	descriptions := map[string]string{
		"workspace.agent_spawn_assign": "Spawns a temporary worker bound to a task card.",
		"workspace.agent_review":       "   ", // whitespace-only → violation
		"mcp.list_servers":             "",
	}
	gaps, err := BundleToolDescriptionGaps(descriptions)
	if err != nil {
		t.Fatal(err)
	}
	var sawReview, sawMCP bool
	for _, gap := range gaps {
		switch {
		case strings.HasPrefix(gap, "builtin:bundle:workflow-tools: workspace.agent_review "):
			sawReview = true
		case strings.HasPrefix(gap, "builtin:bundle:bundle-use: mcp.list_servers "):
			sawMCP = true
		}
	}
	if !sawReview {
		t.Errorf("expected whitespace-only workspace.agent_review to be a gap, got %v", gaps)
	}
	if !sawMCP {
		t.Errorf("expected empty mcp.list_servers to be a gap, got %v", gaps)
	}
}

func TestBundleToolDescriptionGaps_UnknownServiceToolIsGap(t *testing.T) {
	gaps, err := BundleToolDescriptionGaps(map[string]string{
		"workspace.agent_spawn_assign": "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, gap := range gaps {
		if strings.Contains(gap, "workspace.agent_review — not found in exported manifest") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected card-listed tool absent from the manifest to be a gap, got %v", gaps)
	}
}

func TestBundleToolDescriptionGaps_BareAgentLocalSkipped(t *testing.T) {
	// Bare names (agent-local interaction tools) must never produce a gap even
	// when absent from the descriptions map.
	gaps, err := BundleToolDescriptionGaps(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range gaps {
		for _, bare := range []string{"plan_submit", "task_create", "component_mount", "memory_save", "workflow_start", "fork_agent"} {
			if strings.Contains(gap, bare) {
				t.Fatalf("bare agent-local tool %q must be skipped, got gap %q", bare, gap)
			}
		}
	}
}

func TestBundleToolDescriptionGaps_AllDescribedNoGaps(t *testing.T) {
	// Feed every dotted tool ref from the real card assets with a description:
	// the gate must report nothing.
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	descriptions := map[string]string{}
	for _, asset := range assets {
		for _, tool := range asset.Tools {
			if strings.Contains(tool, ".") {
				descriptions[tool] = "described"
			}
		}
	}
	gaps, err := BundleToolDescriptionGaps(descriptions)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 {
		t.Fatalf("expected zero gaps when all dotted refs are described, got %v", gaps)
	}
}
