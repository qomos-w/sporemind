package agentkit

import (
	"strings"
	"testing"
)

func TestBuiltinAssetMetadataComesFromMarkdown(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]CardAsset, len(assets))
	for _, asset := range assets {
		byID[asset.Title] = asset
	}
	if len(byID) != len(BuiltinCards) {
		t.Fatalf("registration assets=%d runtime cards=%d", len(byID), len(BuiltinCards))
	}
	for _, card := range BuiltinCards {
		asset, ok := byID[card.Title]
		if !ok {
			t.Fatalf("runtime card %q has no registration", card.Title)
		}
		if asset.Title != card.Title || asset.BuiltinTitle == "" || asset.Type == "" {
			t.Fatalf("card %q must derive identity and type from Markdown frontmatter: %+v", card.Title, asset)
		}
	}
}

func TestSlashSlug(t *testing.T) {
	cases := map[string]string{
		"  Web   Search ": "web-search",
		"web_search":     "web-search",
		"WebSearch":      "websearch",
		"":               "",
		"---":            "---",
	}
	for in, want := range cases {
		if got := SlashSlug(in); got != want {
			t.Fatalf("SlashSlug(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestResolveBuiltinBundleSlashForms(t *testing.T) {
	cases := []struct {
		name     string
		args     string
		wantID   string
		wantRest string
	}{
		{"websearch", "", "builtin:bundle:web-search", ""},
		{"web-search", "", "builtin:bundle:web-search", ""},
		{"Web-Search", "", "builtin:bundle:web-search", ""},
		{"web", "search", "builtin:bundle:web-search", ""},
		{"web", "search cats", "builtin:bundle:web-search", "cats"},
		{"websearch", "find cats", "builtin:bundle:web-search", "find cats"},
	}
	for _, tc := range cases {
		cardID, rest, ok := ResolveBuiltinBundle(tc.name, tc.args)
		if !ok || cardID != tc.wantID || rest != tc.wantRest {
			t.Fatalf("ResolveBuiltinBundle(%q, %q) = %q, %q, %v; want %q, %q, true",
				tc.name, tc.args, cardID, rest, ok, tc.wantID, tc.wantRest)
		}
	}
	if _, _, ok := ResolveBuiltinBundle("definitely-not-a-bundle", ""); ok {
		t.Fatal("unknown bundle must not resolve")
	}
	if _, _, ok := ResolveBuiltinBundle("", "args"); ok {
		t.Fatal("empty name must not resolve")
	}
}

func TestBuiltinProjectWikiCardExposesConceptTree(t *testing.T) {
	if strings.TrimSpace(BuiltinCards[0].Title) == "" {
		t.Fatal("builtin registry must not contain empty IDs")
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var projectWiki string
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:project-wiki" {
			projectWiki = asset.Raw
			break
		}
	}
	for _, required := range []string{"project.wiki_get_concept_tree"} {
		if !strings.Contains(projectWiki, "- "+required) {
			t.Fatalf("project wiki raw card must expose %q", required)
		}
	}
}
func TestBuiltinPromptCardsCoverAllEmbeddedPromptMarkdown(t *testing.T) {
	assets, err := LoadPromptAssets()
	if err != nil {
		t.Fatal(err)
	}
	cards, err := BuiltinPromptCards()
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != len(assets) {
		t.Fatalf("prompt cards=%d embedded markdown=%d", len(cards), len(assets))
	}
	for _, card := range cards {
		if card.Title == "" || strings.TrimSpace(card.Body) == "" {
			t.Fatalf("invalid prompt card: %#v", card)
		}
	}
}

func TestCardAssetCatalogContainsAllResourceKinds(t *testing.T) {
	assets, err := LoadCardAssets()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, asset := range assets {
		seen[strings.SplitN(asset.Title, ":", 2)[0]] = true
	}
	for _, kind := range []string{"builtin", "prompt", "skill"} {
		if !seen[kind] {
			t.Fatalf("unified catalog missing %s assets", kind)
		}
	}
}

func TestBuiltinCardAssetsMatchRegistry(t *testing.T) {
	bodies, err := BuiltinCardAssets()
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != len(BuiltinCards) {
		t.Fatalf("embedded assets=%d want=%d", len(bodies), len(BuiltinCards))
	}
	for _, card := range BuiltinCards {
		body := bodies[card.Title]
		if !strings.Contains(body, "Use project wiki") && card.Title == "builtin:bundle:project-wiki" {
			t.Fatalf("builtin card %q has unexpected body: %q", card.Title, body)
		}
		if strings.TrimSpace(body) == "" {
			t.Fatalf("builtin card %q has empty Markdown body", card.Title)
		}
	}
}

func TestParseCardAssetDependencies(t *testing.T) {
	raw := `---
id: builtin:mode:test
title: Test Mode
data:
  componentKind: mode
  tools:
    - test.tool
  dependencies:
    - builtin:bundle:file-tools
    - builtin:bundle:shell-tools
---
Test body.
`
	asset, err := parseCardAsset("builtin/cards/mode/test-mode.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(asset.Dependencies) != 2 {
		t.Fatalf("expected 2 dependencies, got %d: %v", len(asset.Dependencies), asset.Dependencies)
	}
	if asset.Dependencies[0] != "builtin:bundle:file-tools" || asset.Dependencies[1] != "builtin:bundle:shell-tools" {
		t.Fatalf("unexpected dependencies: %v", asset.Dependencies)
	}
}

func TestParseCardAssetDependenciesAsObjects(t *testing.T) {
	raw := `---
id: builtin:mode:test
title: Test Mode
data:
  componentKind: mode
  dependencies:
    - id: builtin:bundle:file-tools
    - id: builtin:bundle:shell-tools
---
Test body.
`
	asset, err := parseCardAsset("builtin/cards/mode/test-mode.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(asset.Dependencies) != 2 {
		t.Fatalf("expected 2 object dependencies, got %d: %v", len(asset.Dependencies), asset.Dependencies)
	}
}

func TestParseCardAssetNoDependencies(t *testing.T) {
	raw := `---
id: builtin:bundle:simple
title: Simple Bundle
data:
  componentKind: bundle
  tools:
    - simple.tool
---
Simple body.
`
	asset, err := parseCardAsset("builtin/cards/bundle/simple.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(asset.Dependencies) != 0 {
		t.Fatalf("expected 0 dependencies, got %d: %v", len(asset.Dependencies), asset.Dependencies)
	}
}

func TestParseCardAssetModeManaged(t *testing.T) {
	raw := `---
id: builtin:bundle:test
title: Test Bundle
data:
  componentKind: bundle
  modeManaged: true
  tools:
    - test.tool
---
Test body.
`
	asset, err := parseCardAsset("builtin/cards/bundle/test.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !asset.ModeManaged {
		t.Fatal("expected ModeManaged to be true")
	}
}

func TestParseCardAssetNotModeManaged(t *testing.T) {
	raw := `---
id: builtin:bundle:test
title: Test Bundle
data:
  componentKind: bundle
  tools:
    - test.tool
---
Test body.
`
	asset, err := parseCardAsset("builtin/cards/bundle/test.md", raw)
	if err != nil {
		t.Fatal(err)
	}
	if asset.ModeManaged {
		t.Fatal("expected ModeManaged to be false when not declared")
	}
}

func TestBuiltinGoalBundleIsModeManaged(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:goal" {
			if !asset.ModeManaged {
				t.Fatal("expected builtin:bundle:goal to have modeManaged: true")
			}
			return
		}
	}
	t.Fatal("builtin:bundle:goal not found in builtin assets")
}

func TestBuiltinWorkflowToolsBundleIsModeManaged(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:workflow-tools" {
			if !asset.ModeManaged {
				t.Fatal("expected builtin:bundle:workflow-tools to have modeManaged: true")
			}
			return
		}
	}
	t.Fatal("builtin:bundle:workflow-tools not found in builtin assets")
}

func TestBuiltinWorktreeBundleIsModeManaged(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:worktree" {
			if !asset.ModeManaged {
				t.Fatal("expected builtin:bundle:worktree to have modeManaged: true")
			}
			return
		}
	}
	t.Fatal("builtin:bundle:worktree not found in builtin assets")
}

func TestBuiltinModesLifecycleManaged(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	lifecycleModes := map[string]bool{
		"builtin:mode:goal":     false,
		"builtin:mode:workflow": false,
		"builtin:mode:worktree": false,
	}
	for _, asset := range assets {
		if _, want := lifecycleModes[asset.Title]; !want {
			continue
		}
		if !asset.LifecycleManaged {
			t.Errorf("expected %s to have lifecycleManaged: true", asset.Title)
		}
		lifecycleModes[asset.Title] = true
	}
	for id, found := range lifecycleModes {
		if !found {
			t.Errorf("%s not found in builtin assets", id)
		}
	}
}

func TestBuiltinMemoryModeNotLifecycleManaged(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:mode:memory" {
			if asset.LifecycleManaged {
				t.Fatal("expected builtin:mode:memory to have lifecycleManaged: false (user-mountable)")
			}
			return
		}
	}
	t.Fatal("builtin:mode:memory not found in builtin assets")
}

func TestBuiltinModesFlowOrchestration(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	flowModes := map[string]bool{
		"builtin:mode:goal":     false,
		"builtin:mode:workflow": false,
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

func TestBuiltinWorktreeModeNoFlow(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:mode:worktree" {
			if asset.Flow != "" {
				t.Fatalf("expected builtin:mode:worktree to have no flow, got %q", asset.Flow)
			}
			return
		}
	}
	t.Fatal("builtin:mode:worktree not found in builtin assets")
}
