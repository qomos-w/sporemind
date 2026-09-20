package agentkit

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestWorkflowToolsBundleComposesSubBundles(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:bundle:workflow-tools" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:bundle:workflow-tools not registered")
	}
	for _, dep := range []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:fork-explore",
	} {
		found := false
		for _, got := range asset.Dependencies {
			if got == dep {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("workflow-tools requires %q, got dependencies %v", dep, asset.Dependencies)
		}
	}
	want := []string{"workspace.list_agents", "workspace.agent_spawn_assign", "workspace.agent_review", "workspace.agent_terminate", "workspace.agent_send_message", "workspace.agent_read_message", "workspace.agent_pause", "workspace.agent_resume"}
	for _, id := range want {
		found := false
		for _, got := range asset.Tools {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("workflow-tools missing tool %q in %v", id, asset.Tools)
		}
	}
}

func TestArchitectKindRemovedFromBaseKindConfigs(t *testing.T) {
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == string(domain.AgentKindArchitect) {
			t.Fatalf("architect kind should be removed from BaseKindConfigs, found: %+v", cfg)
		}
	}
}

func TestWorkerKindHasRolePrompt(t *testing.T) {
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == string(domain.AgentKindWorker) {
			if cfg.RolePromptRef.Key != "project.worker" {
				t.Errorf("worker RolePromptRef.Key = %q, want project.worker", cfg.RolePromptRef.Key)
			}
			return
		}
	}
	t.Fatal("worker kind missing")
}

func TestWorkerPromptCardExists(t *testing.T) {
	cards, err := BuiltinPromptCards()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"prompt:profile:project.worker": false,
	}
	for _, c := range cards {
		if _, ok := want[c.Title]; ok {
			want[c.Title] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("prompt card %q not registered in builtin assets", id)
		}
	}
}
