package pluginhost

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestArtifactLoadOrderEmpty(t *testing.T) {
	result := artifactLoadOrder(nil)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

func TestArtifactLoadOrderSingle(t *testing.T) {
	loads := map[string]gen.PluginArtifactLoadReq{
		"app.a": {Manifest: gen.AppManifest{ID: "app.a"}},
	}
	result := artifactLoadOrder(loads)
	if len(result) != 1 || result[0] != "app.a" {
		t.Fatalf("expected [app.a], got %v", result)
	}
}

func TestArtifactLoadOrderNoDeps(t *testing.T) {
	loads := map[string]gen.PluginArtifactLoadReq{
		"app.c": {Manifest: gen.AppManifest{ID: "app.c"}},
		"app.a": {Manifest: gen.AppManifest{ID: "app.a"}},
		"app.b": {Manifest: gen.AppManifest{ID: "app.b"}},
	}
	result := artifactLoadOrder(loads)
	// Should be alphabetically sorted (deterministic fallback).
	expected := []string{"app.a", "app.b", "app.c"}
	if len(result) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	for i, n := range expected {
		if result[i] != n {
			t.Fatalf("expected result[%d]=%q, got %q", i, n, result[i])
		}
	}
}

func TestArtifactLoadOrderWithDeps(t *testing.T) {
	loads := map[string]gen.PluginArtifactLoadReq{
		"app.a": {Manifest: gen.AppManifest{ID: "app.a"}},
		"app.b": {Manifest: gen.AppManifest{ID: "app.b", Dependencies: []gen.AppDependency{{ID: "app.a"}}}},
		"app.c": {Manifest: gen.AppManifest{ID: "app.c", Dependencies: []gen.AppDependency{{ID: "app.b"}}}},
	}
	result := artifactLoadOrder(loads)
	// a must come before b, b must come before c.
	idx := map[string]int{}
	for i, n := range result {
		idx[n] = i
	}
	if idx["app.a"] >= idx["app.b"] {
		t.Fatalf("app.a should come before app.b, got %v", result)
	}
	if idx["app.b"] >= idx["app.c"] {
		t.Fatalf("app.b should come before app.c, got %v", result)
	}
}

func TestArtifactLoadOrderDepsNotInLoadSet(t *testing.T) {
	// Dependency references a plugin not in the load set — should be ignored.
	loads := map[string]gen.PluginArtifactLoadReq{
		"app.a": {Manifest: gen.AppManifest{ID: "app.a", Dependencies: []gen.AppDependency{{ID: "app.missing"}}}},
	}
	result := artifactLoadOrder(loads)
	if len(result) != 1 || result[0] != "app.a" {
		t.Fatalf("expected [app.a], got %v", result)
	}
}

func TestArtifactLoadOrderCycleFallback(t *testing.T) {
	// Cycle: a depends on b, b depends on a.
	loads := map[string]gen.PluginArtifactLoadReq{
		"app.a": {Manifest: gen.AppManifest{ID: "app.a", Dependencies: []gen.AppDependency{{ID: "app.b"}}}},
		"app.b": {Manifest: gen.AppManifest{ID: "app.b", Dependencies: []gen.AppDependency{{ID: "app.a"}}}},
	}
	result := artifactLoadOrder(loads)
	// Should fall back to sorted insertion order (a, b).
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %v", result)
	}
	// Alphabetical order as fallback.
	if result[0] != "app.a" || result[1] != "app.b" {
		t.Fatalf("expected [app.a, app.b] fallback, got %v", result)
	}
}
