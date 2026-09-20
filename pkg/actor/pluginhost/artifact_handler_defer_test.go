package pluginhost

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	ph "github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// restartPendingActor builds a pluginhost Actor wired with an in-memory
// ArtifactLoader over a stub opener, plus a persist store keyed by actorID so
// a "restart" (a second Actor on the same store) restores the same state.
func restartPendingActor(t *testing.T, store persist.Persist) *Actor {
	t.Helper()
	a := &Actor{
		actorID:       "pluginhost-restart-test",
		store:         store,
		ArtifactLoads: map[string]gen.PluginArtifactLoadReq{},
	}
	a.loader = ph.NewArtifactLoader(a)
	a.loader.SetOpener(&recordingOpener{transport: "ffi"})
	return a
}

func inprocessLoadReqFor(t *testing.T, artifactPath string) gen.PluginArtifactLoadReq {
	t.Helper()
	if err := os.WriteFile(artifactPath, []byte(artifactPath), 0o600); err != nil {
		t.Fatal(err)
	}
	return gen.PluginArtifactLoadReq{
		ArtifactPath: artifactPath,
		Manifest: gen.AppManifest{
			ID: "test.native", Name: "Test", Version: "1.0.0", Runtime: "native",
			Namespace: "native.test", ProtocolVersion: 2,
			Callables: []gen.AppCallableDescriptor{
				{ID: "ping", RequestSchema: "Empty", ResponseSchema: "Empty"},
			},
		},
		Abi: gen.PluginAbi{
			Name: "c-abi", Version: 1, Encoding: ph.BinaryCodecV1,
			InvokeSymbol: "PluginInvoke", ContractVersion: "1",
			Isolation: ph.IsolationInProcess, TrustClass: ph.TrustFirstParty, Signer: "sporemind.first-party",
		},
	}
}

// TestHandleArtifactLoadDefersDifferentArtifact is the A1 pluginhost half:
// loading a different artifact into an in-process host returns a successful
// restart_pending response (no error) and persists the new artifact request
// into ArtifactLoads so the next restart activates it.
func TestHandleArtifactLoadDefersDifferentArtifact(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	oldReq := inprocessLoadReqFor(t, filepath.Join(dir, "old.dll"))
	newReq := inprocessLoadReqFor(t, filepath.Join(dir, "new.dll"))

	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), oldReq); err != nil {
		t.Fatalf("load old artifact: %v", err)
	}
	resp, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), newReq)
	if err != nil {
		t.Fatalf("deferred load must not error: %v", err)
	}
	if resp.Status.State != "restart_pending" {
		t.Fatalf("Status.State = %q, want restart_pending", resp.Status.State)
	}
	if resp.PluginID != "test.native" {
		t.Fatalf("PluginID = %q", resp.PluginID)
	}
	got := a.ArtifactLoads["test.native"]
	if got.ArtifactPath != newReq.ArtifactPath {
		t.Fatalf("ArtifactLoads still points at %q, want the new artifact %q", got.ArtifactPath, newReq.ArtifactPath)
	}
	// The old mapping stays reachable (nothing was unloaded) and the new
	// artifact was never opened.
	if _, ok := a.loader.Get("test.native"); !ok {
		t.Fatal("old artifact record must stay loaded after the deferral")
	}
}

// TestHandleArtifactLoadDefersUnloadPending is the A2 pluginhost half: load
// against an unload-pending in-process record returns restart_pending
// instead of reviving the stale DLL mapping.
func TestHandleArtifactLoadDefersUnloadPending(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	req := inprocessLoadReqFor(t, filepath.Join(dir, "plugin.so"))

	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := a.loader.Unload(context.Background(), gen.PluginArtifactUnloadReq{PluginID: "test.native"}); err != nil {
		t.Fatalf("unload: %v", err)
	}

	resp, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), req)
	if err != nil {
		t.Fatalf("deferred load after unload-pending must not error: %v", err)
	}
	if resp.Status.State != "restart_pending" {
		t.Fatalf("Status.State = %q, want restart_pending", resp.Status.State)
	}
	if _, ok := a.loader.Get("test.native"); !ok {
		t.Fatal("unload-pending record must survive (no DLL revival)")
	}
}

// TestHandleArtifactLoadSubprocessBlockerStaysError pins that the same
// loader failure is a HARD error for subprocess targets: only in-process
// transports defer.
func TestHandleArtifactLoadSubprocessBlockerStaysError(t *testing.T) {
	dir := t.TempDir()
	a := restartPendingActor(t, persist.NewFSPersist(dir))
	subReq := func(p string) gen.PluginArtifactLoadReq {
		r := inprocessLoadReqFor(t, p)
		r.Abi.Isolation = ph.IsolationSubprocess
		return r
	}
	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), subReq(filepath.Join(dir, "a.exe"))); err != nil {
		t.Fatalf("load A: %v", err)
	}
	if _, err := a.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), subReq(filepath.Join(dir, "b.exe"))); err == nil {
		t.Fatal("subprocess different-artifact load must stay a hard error")
	}
}

// TestRestartPendingActivatesOnNewLoaderInstance simulates the full restart
// activation: after a deferred load, a fresh pluginhost instance (new loader,
// same store) restores ArtifactLoads at its OnStart; the appmanager's
// idempotent re-load then confirms the artifact and the app is active —
// restart_pending → active/running is the activation path.
func TestRestartPendingActivatesOnNewLoaderInstance(t *testing.T) {
	dir := t.TempDir()
	store := persist.NewFSPersist(dir)
	oldReq := inprocessLoadReqFor(t, filepath.Join(dir, "old.dll"))
	newReq := inprocessLoadReqFor(t, filepath.Join(dir, "new.dll"))

	// Pre-restart host: old artifact loaded, new artifact deferred.
	a1 := restartPendingActor(t, store)
	if _, err := a1.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), oldReq); err != nil {
		t.Fatalf("load old: %v", err)
	}
	resp, err := a1.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), newReq)
	if err != nil || resp.Status.State != "restart_pending" {
		t.Fatalf("defer new: resp=%+v err=%v", resp, err)
	}

	// Restart: fresh host instance restores ArtifactLoads and loads the new
	// artifact (simulating the pluginhost OnStart restore loop).
	a2 := restartPendingActor(t, store)
	if err := a2.Load(); err != nil {
		t.Fatalf("restore ArtifactLoads: %v", err)
	}
	if got := a2.ArtifactLoads["test.native"]; got.ArtifactPath != newReq.ArtifactPath {
		t.Fatalf("restored ArtifactLoads = %q, want the deferred new artifact", got.ArtifactPath)
	}
	for _, id := range artifactLoadOrder(a2.ArtifactLoads) {
		if _, err := a2.loader.Load(context.Background(), a2.ArtifactLoads[id]); err != nil {
			t.Fatalf("restore load %s: %v", id, err)
		}
	}

	// Activation: the appmanager spawn-loop re-load carries the recorded
	// artifact hash (records always persist it), so the pluginhost's
	// idempotent same-artifact branch confirms the plugin is active.
	data, err := os.ReadFile(newReq.ArtifactPath)
	if err != nil {
		t.Fatalf("read new artifact: %v", err)
	}
	activateReq := newReq
	activateReq.ArtifactHash = fmt.Sprintf("%x", sha256.Sum256(data))
	activateResp, err := a2.handleArtifactLoad(testutil.HumanCtx(testutil.GenActorID()), activateReq)
	if err != nil {
		t.Fatalf("activation load: %v", err)
	}
	if activateResp.Status.State != "active" {
		t.Fatalf("activation Status.State = %q, want active (running)", activateResp.Status.State)
	}
}
