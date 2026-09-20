package appmanager

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/persist"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestNativeRuntimeRoutingRegistersChild validates that when a plugin is
// registered (post-artifact-load), spawnChild records it as routing through the
// pluginhost service rather than spawning a child actor.
func TestNativeRuntimeRoutingRegistersChild(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.test", Name: "NativeTest", Version: "1.0.0", Runtime: "native",
		Namespace: "native.test", ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{
			{ID: "ping", RequestSchema: "Any", ResponseSchema: "Any"},
		},
	}

	a := &Actor{
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
	record := appRecord{Manifest: manifest, State: "registered"}
	err := a.spawnChild(nil, "native.test", record, true)
	if err != nil {
		t.Fatalf("spawnChild native: %v", err)
	}

	a.mu.Lock()
	target := a.children["native.test"]
	rec := a.Records["native.test"]
	a.mu.Unlock()

	if target != pluginhostServiceName {
		t.Fatalf("plugin should route to %q, got %q", pluginhostServiceName, target)
	}
	if rec.State != "running" {
		t.Fatalf("expected state running, got %q", rec.State)
	}
	if rec.ActorID != pluginhostServiceName {
		t.Fatalf("expected actor id %q, got %q", pluginhostServiceName, rec.ActorID)
	}
}

// TestNativeRuntimeRoutingRequiresLoadedArtifact ensures spawnChild is not
// responsible for loading the artifact; it assumes the caller has already
// loaded it via pluginhost.artifact_load. The test documents this contract
// by verifying spawnChild does not return a loader-not-connected error.
func TestNativeRuntimeRoutingDoesNotSpawnChildActor(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.noload", Name: "NoLoad", Version: "1.0.0", Runtime: "native",
		Namespace: "native.noload", ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{
			{ID: "ping", RequestSchema: "Any", ResponseSchema: "Any"},
		},
	}

	a := &Actor{
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
	// Verify there are no children spawned for plugins.
	if err := a.spawnChild(nil, "native.noload", appRecord{Manifest: manifest, State: "registered"}, true); err != nil {
		t.Fatalf("spawnChild native: %v", err)
	}
	// The only entry should be the pluginhost service mapping, not a real
	// canonical actor ID.
	for appID, target := range a.children {
		if appID == "native.noload" && target != pluginhostServiceName {
			t.Fatalf("plugin target = %q, want %q", target, pluginhostServiceName)
		}
	}
}

// TestInvokeNativeRejectsNilRef ensures invokeNative fails fast if the
// pluginhost service ref is missing (defensive guard for misconfigured
// systems).
func TestInvokeNativeRejectsNilRef(t *testing.T) {
	a := &Actor{}
	_, err := a.invokeNative(nil, nil, gen.AppManagerInvokeReq{ID: "app", Callable: "call"}, callerIdentity{}, gen.AppCallableDescriptor{})
	if err == nil {
		t.Fatal("expected error for nil pluginhost ref")
	}
}
