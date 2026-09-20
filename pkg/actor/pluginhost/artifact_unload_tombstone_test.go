package pluginhost

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestArtifactUnloadLeavesTombstoneHandler verifies that a stopped native
// plugin's callables still have a registered handler, but it answers with a
// clear "plugin is stopped" envelope instead of the generic "call ID not
// registered" error.
func TestArtifactUnloadLeavesTombstoneHandler(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	req := inprocessLoadReqFor(t, filepath.Join(dir, "plugin.so"))

	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}

	callID := "plugin.test.native.ping"
	h, ok := a.handler(callID)
	if !ok {
		t.Fatalf("tombstone handler for %q not registered after unload", callID)
	}
	resp, err := h(t.Context(), nil)
	if err != nil {
		t.Fatalf("tombstone handler returned error: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal(resp, &env); err != nil {
		t.Fatalf("tombstone response is not valid JSON: %v (raw=%q)", err, resp)
	}
	got, ok := env["__host_error__"]
	if !ok {
		t.Fatalf("tombstone response missing __host_error__: %q", resp)
	}
	want := "plugin test.native is stopped; call appmanager.plugin_load to start it"
	if got != want {
		t.Fatalf("tombstone message = %q, want %q", got, want)
	}
}

// TestArtifactLoadReplacesTombstoneHandler verifies that loading a plugin
// after it was stopped overwrites the tombstone stubs with real handlers.
// Subprocess transport is used because in-process unload is deferred and
// refuses re-entry until a host restart.
func TestArtifactLoadReplacesTombstoneHandler(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	req := inprocessLoadReqFor(t, filepath.Join(dir, "plugin.exe"))
	req.Abi.Isolation = ph.IsolationSubprocess

	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}

	// Reload the same artifact: the real handler must replace the tombstone.
	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req); err != nil {
		t.Fatalf("reload: %v", err)
	}

	callID := "plugin.test.native.ping"
	h, ok := a.handler(callID)
	if !ok {
		t.Fatalf("real handler for %q not registered after reload", callID)
	}
	resp, err := h(t.Context(), nil)
	if err != nil {
		t.Fatalf("real handler returned error: %v", err)
	}
	if string(resp) != "ffi" {
		t.Fatalf("real handler response = %q, want %q", resp, "ffi")
	}
}

// TestArtifactUnloadRemovesRealHandler verifies that the original handler is
// gone after unload: the response is the tombstone envelope, not the real
// plugin response.
func TestArtifactUnloadRemovesRealHandler(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	req := inprocessLoadReqFor(t, filepath.Join(dir, "plugin.so"))

	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req); err != nil {
		t.Fatalf("load: %v", err)
	}
	callID := "plugin.test.native.ping"
	if _, ok := a.handler(callID); !ok {
		t.Fatalf("real handler for %q not registered after load", callID)
	}
	if _, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: req.Manifest.ID}); err != nil {
		t.Fatalf("unload: %v", err)
	}

	h, ok := a.handler(callID)
	if !ok {
		t.Fatalf("handler for %q should still exist as tombstone after unload", callID)
	}
	resp, err := h(t.Context(), nil)
	if err != nil {
		t.Fatalf("tombstone handler returned error: %v", err)
	}
	if !strings.Contains(string(resp), "__host_error__") {
		t.Fatalf("after unload response should be tombstone envelope, got %q", resp)
	}
}

// TestArtifactUnloadZombieLeavesTombstoneHandler verifies that the zombie
// cleanup path (artifact file missing, loader returns Removed=0) still
// installs tombstone handlers before purging the persisted load record.
func TestArtifactUnloadZombieLeavesTombstoneHandler(t *testing.T) {
	a := restartPendingActor(t, nil)
	a.Plugins = []PluginDescriptor{{
		ID: "app.zombie", Name: "Zombie", Version: "0.1.0",
		Runtime: "native", Status: "error",
	}}
	loadReq := inprocessLoadReqFor(t, t.TempDir()+"/missing.so")
	loadReq.Manifest.ID = "app.zombie"
	loadReq.Manifest.Name = "Zombie"
	a.ArtifactLoads["app.zombie"] = loadReq

	if _, err := a.handleArtifactUnload(testutil.HumanCtx(testutil.GenActorID()), gen.PluginArtifactUnloadReq{PluginID: "app.zombie"}); err != nil {
		t.Fatalf("unload zombie: %v", err)
	}

	callID := "plugin.app.zombie.ping"
	h, ok := a.handler(callID)
	if !ok {
		t.Fatalf("tombstone handler for zombie %q not registered", callID)
	}
	resp, err := h(t.Context(), nil)
	if err != nil {
		t.Fatalf("zombie tombstone handler returned error: %v", err)
	}
	if !strings.Contains(string(resp), "__host_error__") {
		t.Fatalf("zombie tombstone response should contain __host_error__, got %q", resp)
	}
}
