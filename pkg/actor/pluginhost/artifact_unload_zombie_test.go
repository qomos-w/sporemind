package pluginhost

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestArtifactUnloadPurgesZombieDescriptor covers the restore-failure zombie:
// a plugin whose artifact file no longer exists leaves an "error" descriptor in
// the persisted Plugins list while the loader has no runtime entry. An explicit
// artifact_unload must purge both the ArtifactLoads record and that stale
// descriptor — otherwise the zombie survives every restart (observed live as
// app.totp / app.cloud.authenticator error entries with LoadedAt zero).
func TestArtifactUnloadPurgesZombieDescriptor(t *testing.T) {
	a := restartPendingActor(t, nil)
	a.Plugins = []PluginDescriptor{{
		ID: "app.zombie", Name: "Zombie", Version: "0.1.0",
		Runtime: "native", Status: "error",
	}}
	a.ArtifactLoads["app.zombie"] = gen.PluginArtifactLoadReq{
		ArtifactPath: t.TempDir() + "/missing.exe", // file intentionally absent
		Manifest:     gen.AppManifest{ID: "app.zombie", Name: "Zombie", Version: "0.1.0", Runtime: "native"},
	}

	resp, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: "app.zombie"})
	if err != nil {
		t.Fatalf("handleArtifactUnload: %v", err)
	}
	if resp.Removed != 0 {
		t.Fatalf("Removed = %d, want 0 (loader never had it loaded)", resp.Removed)
	}

	if _, ok := a.ArtifactLoads["app.zombie"]; ok {
		t.Fatal("ArtifactLoads entry must be purged on explicit unload")
	}
	for _, p := range a.Plugins {
		if p.ID == "app.zombie" {
			t.Fatalf("stale error descriptor must be removed from Plugins, got %+v", a.Plugins)
		}
	}
	list, err := a.handleListPlugins(nil, listPluginsReq{})
	if err != nil {
		t.Fatalf("handleListPlugins: %v", err)
	}
	if len(list.Plugins) != 0 {
		t.Fatalf("list after purge = %+v, want empty", list.Plugins)
	}
	_ = ph.PluginDescriptorData{} // keep import aligned with harness package
}
