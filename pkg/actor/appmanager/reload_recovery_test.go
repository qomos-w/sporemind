package appmanager

import (
	"errors"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// pendingReloadTestSetup creates an Actor that looks like it crashed mid-reload:
// the candidate manifest is the active record (Save succeeded) but the
// pending_reload marker is still present (commit never ran or never completed).
func pendingReloadTestSetup(t *testing.T) (*Actor, gen.AppManifest, gen.AppManifest, *testutil.FakeCtx) {
	t.Helper()
	oldManifest := gen.AppManifest{ID: "native.app", Name: "Native", Namespace: "native.app", Version: "1.0.0", Runtime: "native", Entrypoints: []gen.AppEntrypoint{{ID: "main", Kind: "view"}}}
	candidateManifest := oldManifest
	candidateManifest.Version = "2.0.0"
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "inprocess", TrustClass: "first_party", Signer: "first-party"}
	oldRecord := appRecord{Manifest: oldManifest, State: "active", PackageHash: "old-package", ArtifactPath: "old.dll", ArtifactHash: "old-artifact", Abi: abi}
	candidateRecord := appRecord{Manifest: candidateManifest, State: "active", PackageHash: "new-package", ArtifactPath: "candidate.dll", ArtifactHash: "new-artifact", Abi: abi, Generation: 2}
	a := &Actor{
		actorID:  "appmanager-recovery-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{oldManifest.ID: candidateManifest},
		Records:  map[string]appRecord{oldManifest.ID: candidateRecord},
		PendingReloads: map[string]pendingReload{oldManifest.ID: {
			CandidateManifest:     candidateManifest,
			OldManifest:           oldManifest,
			OldRecord:             oldRecord,
			CandidateArtifactPath: "candidate.dll",
			CandidateArtifactHash: "new-artifact",
			CandidateAbi:          abi,
			PackageHash:           "new-package",
		}},
		children: map[string]string{oldManifest.ID: pluginhostServiceName},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	return a, oldManifest, candidateManifest, ctx
}

func TestRecoverPendingReloadCompletesReload(t *testing.T) {
	a, oldManifest, candidateManifest, ctx := pendingReloadTestSetup(t)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "recovery-token", PluginID: oldManifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: oldManifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_reload_commit":
				return gen.PluginArtifactReloadCommitResp{PluginID: oldManifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: oldManifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			default:
				t.Fatalf("unexpected call %s", callID)
				return nil, nil
			}
		}}
	}
	a.recoverPendingReloads(ctx)
	if len(calls) != 2 || calls[0] != "pluginhost.artifact_reload_prepare" || calls[1] != "pluginhost.artifact_reload_commit" {
		t.Fatalf("expected prepare+commit calls, got %v", calls)
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared, got %v", a.PendingReloads)
	}
	if a.Apps[oldManifest.ID].Version != candidateManifest.Version {
		t.Fatalf("app should point to candidate after recovery: %v", a.Apps[oldManifest.ID])
	}
	if a.Records[oldManifest.ID].ArtifactHash != "new-artifact" {
		t.Fatalf("record should have candidate artifact: %+v", a.Records[oldManifest.ID])
	}
}

func TestRecoverPendingReloadRollsBackOnPrepareFailure(t *testing.T) {
	a, oldManifest, _, ctx := pendingReloadTestSetup(t)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return nil, errors.New("candidate artifact missing")
			default:
				t.Fatalf("unexpected call %s", callID)
				return nil, nil
			}
		}}
	}
	a.recoverPendingReloads(ctx)
	if len(calls) != 1 {
		t.Fatalf("expected only prepare call, got %v", calls)
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared after rollback, got %v", a.PendingReloads)
	}
	if a.Apps[oldManifest.ID].Version != oldManifest.Version {
		t.Fatalf("app should be rolled back to old manifest: %+v", a.Apps[oldManifest.ID])
	}
	rec := a.Records[oldManifest.ID]
	if rec.ArtifactHash != "old-artifact" || rec.PackageHash != "old-package" {
		t.Fatalf("record should be rolled back to old state: %+v", rec)
	}
}

func TestRecoverPendingReloadRollsBackOnCommitFailure(t *testing.T) {
	a, oldManifest, _, ctx := pendingReloadTestSetup(t)
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "recovery-token", PluginID: oldManifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: oldManifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_reload_commit":
				return nil, errors.New("commit failed")
			case "pluginhost.artifact_reload_abort":
				return gen.PluginArtifactReloadAbortResp{}, nil
			default:
				t.Fatalf("unexpected call %s", callID)
				return nil, nil
			}
		}}
	}
	a.recoverPendingReloads(ctx)
	// prepare, commit(fail), abort
	if len(calls) != 3 {
		t.Fatalf("expected prepare+commit+abort calls, got %v", calls)
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared after rollback, got %v", a.PendingReloads)
	}
	if a.Apps[oldManifest.ID].Version != oldManifest.Version {
		t.Fatalf("app should be rolled back: %+v", a.Apps[oldManifest.ID])
	}
	rec := a.Records[oldManifest.ID]
	if rec.ArtifactHash != "old-artifact" {
		t.Fatalf("record should be rolled back: %+v", rec)
	}
}

func TestRecoverPendingReloadRollsBackWithoutPluginhost(t *testing.T) {
	a, oldManifest, _, ctx := pendingReloadTestSetup(t)
	// Pluginhost not available — recovery should fall back to rollback.
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(string, any) (any, error) {
			t.Fatal("no planner calls expected without pluginhost")
			return nil, nil
		}}
	}
	a.recoverPendingReloads(ctx)
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared, got %v", a.PendingReloads)
	}
	if a.Apps[oldManifest.ID].Version != oldManifest.Version {
		t.Fatalf("app should be rolled back to old: %+v", a.Apps[oldManifest.ID])
	}
	if a.Records[oldManifest.ID].ArtifactHash != "old-artifact" {
		t.Fatalf("record should be rolled back: %+v", a.Records[oldManifest.ID])
	}
}

func TestRecoverPendingReloadNoOpWhenEmpty(t *testing.T) {
	a, _, _, ctx := pendingReloadTestSetup(t)
	a.PendingReloads = nil
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		t.Fatal("no service lookup expected when no pending reloads")
		return nil, false
	}
	a.recoverPendingReloads(ctx)
}

// TestReloadPersistsAndClearsPendingMarker verifies that the handleReload
// native path sets the pending_reload marker during the reload and clears it
// after successful commit.
func TestReloadPersistsAndClearsPendingMarker(t *testing.T) {
	a, manifest := newNativeReloadActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "token", PluginID: manifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_reload_commit":
				return gen.PluginArtifactReloadCommitResp{PluginID: manifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			default:
				return nil, errors.New("unexpected call")
			}
		}}
	}
	candidateManifest := manifest
	candidateManifest.Version = "2.0.0"
	if _, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi, CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact"}); err != nil {
		t.Fatal(err)
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared after successful reload, got %v", a.PendingReloads)
	}
}

// TestReloadCommitFailureClearsPendingMarker verifies that on commit failure
// the pending_reload marker is cleared even though the record is rolled back.
func TestReloadCommitFailureClearsPendingMarker(t *testing.T) {
	a, manifest := newNativeReloadActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "token", PluginID: manifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_reload_commit":
				return nil, errors.New("commit failure")
			case "pluginhost.artifact_reload_abort":
				return gen.PluginArtifactReloadAbortResp{}, nil
			default:
				return nil, nil
			}
		}}
	}
	candidateManifest := manifest
	candidateManifest.Version = "2.0.0"
	_, _ = a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi, CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact"})
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared on commit failure, got %v", a.PendingReloads)
	}
}

// TestRecoverPendingReloadSurvivesRestart verifies the full crash-recovery
// cycle: persist state with a pending reload, reload it in a new Actor, and
// run recovery to verify the marker is loaded from persisted state.
func TestRecoverPendingReloadSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	store := persist.NewFSPersist(dir)
	oldManifest := gen.AppManifest{ID: "native.app", Name: "Native", Namespace: "native.app", Version: "1.0.0", Runtime: "native"}
	candidateManifest := oldManifest
	candidateManifest.Version = "2.0.0"
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "inprocess", TrustClass: "first_party", Signer: "first-party"}

	// Actor that crashed mid-reload: candidate persisted, marker persisted.
	a1 := &Actor{
		actorID:  "appmanager-restart-test",
		store:    store,
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{oldManifest.ID: candidateManifest},
		Records:  map[string]appRecord{oldManifest.ID: {Manifest: candidateManifest, State: "active", PackageHash: "new-package", ArtifactPath: "candidate.dll", ArtifactHash: "new-artifact", Abi: abi, Generation: 2}},
		PendingReloads: map[string]pendingReload{oldManifest.ID: {
			CandidateManifest:     candidateManifest,
			OldManifest:           oldManifest,
			OldRecord:             appRecord{Manifest: oldManifest, State: "active", PackageHash: "old-package", ArtifactPath: "old.dll", ArtifactHash: "old-artifact", Abi: abi},
			CandidateArtifactPath: "candidate.dll",
			CandidateArtifactHash: "new-artifact",
			CandidateAbi:          abi,
			PackageHash:           "new-package",
		}},
		children: map[string]string{oldManifest.ID: pluginhostServiceName},
	}
	if err := a1.Save(); err != nil {
		t.Fatal(err)
	}

	// New Actor that restores from the same persisted store.
	a2 := &Actor{
		actorID:  "appmanager-restart-test",
		store:    persist.NewFSPersist(dir),
		bindings: appbinding.NewRegistry(),
	}
	if err := a2.Load(); err != nil {
		t.Fatal(err)
	}
	if len(a2.PendingReloads) != 1 {
		t.Fatalf("pending reload should be restored from persisted state, got %d", len(a2.PendingReloads))
	}
	pr := a2.PendingReloads[oldManifest.ID]
	if pr.CandidateManifest.Version != "2.0.0" || pr.OldManifest.Version != "1.0.0" {
		t.Fatalf("pending reload data not restored correctly: %+v", pr)
	}
	if pr.OldRecord.ArtifactHash != "old-artifact" {
		t.Fatalf("old record not restored: %+v", pr.OldRecord)
	}

	// Run recovery: pluginhost prepare fails → rollback.
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID == "pluginhost.artifact_reload_prepare" {
				return nil, errors.New("candidate artifact missing on restart")
			}
			t.Fatalf("unexpected call %s", callID)
			return nil, nil
		}}
	}
	a2.recoverPendingReloads(ctx)
	if len(a2.PendingReloads) != 0 {
		t.Fatalf("marker should be cleared after rollback, got %v", a2.PendingReloads)
	}
	if a2.Apps[oldManifest.ID].Version != "1.0.0" {
		t.Fatalf("app should be rolled back to old: %+v", a2.Apps[oldManifest.ID])
	}
	if a2.Records[oldManifest.ID].ArtifactHash != "old-artifact" {
		t.Fatalf("record should be rolled back: %+v", a2.Records[oldManifest.ID])
	}
}

// TestUnregisterDuringPendingReloadCompletesCleanup verifies R4: calling
// unregister while a pending reload marker exists first aborts the reload
// (rolling back to the pre-reload record), then proceeds through the full
// cleanup state machine to fully unregister the app. No candidate residue
// survives.
func TestUnregisterDuringPendingReloadCompletesCleanup(t *testing.T) {
	a, oldManifest, _, ctx := pendingReloadTestSetup(t)
	a.protocol = newProtocolManager(t)
	if err := a.protocol.RegisterAppManifest(oldManifest); err != nil {
		t.Fatal(err)
	}
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: oldManifest.ID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared, got %v", a.PendingReloads)
	}
	if _, ok := a.Apps[oldManifest.ID]; ok {
		t.Fatal("app should be removed from Apps")
	}
	if _, ok := a.Records[oldManifest.ID]; ok {
		t.Fatal("app should be removed from Records")
	}
}

// TestUnregisterDuringPendingReloadLeavesCleanupPending verifies that when
// the cleanup back-half fails after aborting a pending reload, the app is left
// in a cleanup-pending state with the OLD (rolled-back) record — not the
// candidate. The pending reload marker is still cleared.
func TestUnregisterDuringPendingReloadLeavesCleanupPending(t *testing.T) {
	a, oldManifest, candidateManifest, ctx := pendingReloadTestSetup(t)
	a.protocol = newProtocolManager(t)
	// Force protocol unregister to fail by giving the old (pre-reload)
	// manifest the reserved namespace — abortPendingReload restores this
	// record, so the cleanup back-half then fails at descriptor removal.
	oldReserved := oldManifest
	oldReserved.Namespace = protocol.SystemNamespace
	pr := a.PendingReloads[oldManifest.ID]
	pr.OldManifest = oldReserved
	pr.OldRecord.Manifest = oldReserved
	a.PendingReloads[oldManifest.ID] = pr

	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}

	err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: oldManifest.ID})
	if err == nil {
		t.Fatal("expected protocol unregister failure")
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared even on cleanup failure, got %v", a.PendingReloads)
	}
	rec, ok := a.Records[oldManifest.ID]
	if !ok {
		t.Fatal("record should be retained on cleanup failure")
	}
	if !isCleanupPendingState(rec.State) {
		t.Fatalf("expected cleanup-pending state, got %q", rec.State)
	}
	if rec.Manifest.Version == candidateManifest.Version {
		t.Fatalf("record should be rolled back to old manifest, not candidate version %q", candidateManifest.Version)
	}
}

// TestOnStartRecoveryNoHandlerNotFound verifies the OnStart recovery order
// (reload-first, cleanup-second) for a plugin that crashed mid-reload:
// after rolling back the pending reload, the record reflects the old artifact
// and spawnChild succeeds — no "handler not found" from a stale candidate
// artifact path racing the pluginhost's still-loaded old artifact.
func TestOnStartRecoveryNoHandlerNotFound(t *testing.T) {
	a, oldManifest, _, _ := pendingReloadTestSetup(t)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	// Simulate restart: new actor restores from the same persisted store.
	a2 := &Actor{
		actorID:  a.actorID,
		store:    a.store,
		bindings: appbinding.NewRegistry(),
		children: map[string]string{},
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(a2.PendingReloads) != 1 {
		t.Fatalf("pending reload should be restored, got %d", len(a2.PendingReloads))
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// Pluginhost not yet available on restart → recovery rolls back.
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	// Run recovery in OnStart order: reload-first, cleanup-second.
	a2.recoverPendingReloads(ctx)
	a2.recoverPendingCleanup(ctx)
	if len(a2.PendingReloads) != 0 {
		t.Fatalf("pending reload marker should be cleared after rollback, got %v", a2.PendingReloads)
	}
	rec := a2.Records[oldManifest.ID]
	if rec.ArtifactPath != "old.dll" {
		t.Fatalf("record should be rolled back to old artifact path, got %q", rec.ArtifactPath)
	}
	// The spawn loop must handle the recovered record without error.
	if err := a2.spawnChild(ctx, oldManifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild after recovery should succeed: %v", err)
	}
}
