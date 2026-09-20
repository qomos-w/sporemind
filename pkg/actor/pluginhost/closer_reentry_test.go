package pluginhost

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// processCloseSim mimics processOpener.close: killing the plugin process
// reports "stopped", whose path re-enters a.mu via ReportProcessState →
// detachProxy. Running this closer while a.mu is held self-deadlocks the
// actor — the 2026-09-04 agent-creation hang on the dev gateway.
func processCloseSim(a *Actor) func() error {
	return func() error {
		a.ReportProcessState("app.sim", "stopped", "", "", 0)
		return nil
	}
}

// runBounded fails the test if fn does not return within the deadline —
// the deadlock manifests as a hang, not an error return.
func runBounded(t *testing.T, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return: a.mu self-deadlock via closer re-entry", name)
	}
}

// TestReplaceArtifactCloserReentryNoDeadlock pins the reload-commit path:
// CommitReload → ReplaceArtifact invokes the old process closer, which
// reports "stopped". The closer must run outside a.mu.
func TestReplaceArtifactCloserReentryNoDeadlock(t *testing.T) {
	a := &Actor{actorCtx: testutil.HumanCtx(testutil.GenActorID())}
	desc := pluginhost.PluginDescriptorData{ID: "app.sim", Status: "running"}
	if err := a.ReplaceArtifact(nil, nil, nil, desc, nil, nil); err != nil {
		t.Fatal(err)
	}

	runBounded(t, "ReplaceArtifact reload commit", func() {
		if err := a.ReplaceArtifact([]string{"plugin.app.sim.actor.run"}, nil, nil, desc, processCloseSim(a), nil); err != nil {
			t.Fatal(err)
		}
	})

	// The close report must have detoured through the proxy table without
	// corrupting state: the descriptor is still present.
	if id := a.findPluginID("plugin.app.sim.actor.run"); id != "app.sim" {
		t.Fatalf("findPluginID after reload = %q, want app.sim", id)
	}
}

// TestUnregisterActorCloserReentryNoDeadlock pins the unregister path:
// handleUnregisterActor must not invoke the closer under a.mu either.
func TestUnregisterActorCloserReentryNoDeadlock(t *testing.T) {
	a := &Actor{actorCtx: testutil.HumanCtx(testutil.GenActorID())}
	a.closers = map[string]func() error{} // OnStart initializes this in production
	desc := pluginhost.PluginDescriptorData{ID: "app.sim", Status: "running"}
	if err := a.ReplaceArtifact(nil, nil, nil, desc, nil, processCloseSim(a)); err != nil {
		t.Fatal(err)
	}

	var res unregisterActorRes
	runBounded(t, "handleUnregisterActor", func() {
		var err error
		res, err = a.handleUnregisterActor(testutil.HumanCtx(testutil.GenActorID()), unregisterActorReq{PluginID: "app.sim"})
		if err != nil {
			t.Fatal(err)
		}
	})
	_ = res

	// Closer ran, descriptor flipped to unloaded.
	for _, p := range a.Plugins {
		if p.ID == "app.sim" && p.Status != "unloaded" {
			t.Fatalf("status after unregister = %q, want unloaded", p.Status)
		}
	}
	if _, ok := a.closers["app.sim"]; ok {
		t.Fatal("closer not consumed by unregister")
	}
}
