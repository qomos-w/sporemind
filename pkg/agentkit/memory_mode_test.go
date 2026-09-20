package agentkit

import "testing"

func TestMemoryModeRequiresForkDreamBundle(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var memoryMode *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:mode:memory" {
			memoryMode = &assets[i]
			break
		}
	}
	if memoryMode == nil {
		t.Fatal("builtin:mode:memory not registered")
	}
	for _, dep := range memoryMode.Dependencies {
		if dep == "builtin:bundle:fork-dream" {
			return
		}
	}
	t.Fatalf("memory mode must require builtin:bundle:fork-dream, got dependencies %v", memoryMode.Dependencies)
}

func TestForkDreamBundleIsInternal(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title != "builtin:bundle:fork-dream" {
			continue
		}
		if asset.Fork == nil || asset.Fork.ToolName != "fork_dream" || asset.Fork.ChildKind != "dreamer" {
			t.Fatalf("unexpected fork-dream declaration: %+v", asset.Fork)
		}
		if len(asset.Tools) != 0 {
			t.Fatalf("fork-dream must not expose generic tools: %v", asset.Tools)
		}
		return
	}
	t.Fatal("builtin:bundle:fork-dream not registered")
}
