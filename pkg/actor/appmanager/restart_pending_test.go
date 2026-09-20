package appmanager

import (
	"errors"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// restartPendingTestActor is an appmanager Actor preconditioned for the
// restart_pending lifecycle tests.
func restartPendingTestActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-restart-pending-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

func nativeManifestForTest(id string) gen.AppManifest {
	return gen.AppManifest{
		ID: id, Name: "Native", Namespace: "sporeapp." + id, Version: "1.0.0", Runtime: "native",
	}
}

func inprocessAbiForTest() *gen.PluginAbi {
	return &gen.PluginAbi{Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1", InvokeSymbol: "PluginInvoke", ContractVersion: "1", Isolation: pluginhost.IsolationInProcess, TrustClass: "first_party", Signer: "sporemind.first-party"}
}

func subprocessAbiForTest() *gen.PluginAbi {
	return &gen.PluginAbi{Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1", InvokeSymbol: "PluginInvoke", ContractVersion: "1", Isolation: pluginhost.IsolationSubprocess, TrustClass: "first_party", Signer: "sporemind.first-party"}
}

// --- A1: re-register deferral ---

// TestRegisterDeferredPersistsRestartPending verifies the A1 deferred
// registration terminal: the record lands in restart_pending with the new
// artifact, the response is a success carrying that state, and no child is
// spawned.
func TestRegisterDeferredPersistsRestartPending(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred")
	manifest.Version = "2.0.0"
	req := gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: "new.so", ArtifactHash: "new-artifact-hash", Abi: inprocessAbiForTest(),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	status, err := a.doRegister(ctx, req, true)
	if err != nil {
		t.Fatalf("deferred register must not error: %v", err)
	}
	if status.State != stateRestartPending {
		t.Fatalf("status.State = %q, want restart_pending", status.State)
	}
	rec := a.Records[manifest.ID]
	if rec.State != stateRestartPending {
		t.Fatalf("record.State = %q, want restart_pending", rec.State)
	}
	if rec.ArtifactHash != "new-artifact-hash" || rec.ArtifactPath != "new.so" {
		t.Fatalf("record must carry the new artifact: %+v", rec)
	}
	if _, ok := a.children[manifest.ID]; ok {
		t.Fatal("deferred registration must not spawn a child")
	}
	found := false
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "app_lifecycle" {
			if le, ok := ev.Payload.(gen.AppLifecycleEvent); ok && le.Kind == "restart_pending" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("expected app_lifecycle restart_pending event")
	}
}

// TestRegisterDeferredOnExistingRunningAppKeepsRouting verifies that
// re-registering a different artifact into an in-process host keeps the
// previous child route (the old artifact stays loaded and routeable) while
// the record goes restart_pending.
func TestRegisterDeferredOnExistingRunningAppKeepsRouting(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred")
	oldRec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "old.so", ArtifactHash: "old-hash", Abi: inprocessAbiForTest(), Generation: 3}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = oldRec
	a.children[manifest.ID] = pluginhostServiceName
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	manifest.Version = "2.0.0"
	req := gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: "new.so", ArtifactHash: "new-hash", Abi: inprocessAbiForTest(),
	}
	status, err := a.doRegister(testutil.HumanCtx(testutil.GenActorID()), req, true)
	if err != nil {
		t.Fatalf("deferred re-register: %v", err)
	}
	if status.State != stateRestartPending {
		t.Fatalf("status.State = %q, want restart_pending", status.State)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatal("previous child route must survive the deferred registration")
	}
	if got := a.Records[manifest.ID]; got.Generation != 4 {
		t.Fatalf("generation not bumped on re-register: %+v", got)
	}
}

// TestBuildLocalRegisterReqDetectsDeferral verifies the install_local seam:
// when pluginhost.artifact_load reports restart_pending, the register request
// carries the new artifact (temp file retained) and the deferral flag is set.
func TestBuildLocalRegisterReqDetectsDeferral(t *testing.T) {
	config.SetExeDirForTest(t.TempDir())
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred")
	abi := inprocessAbiForTest()
	pkg := localPackageFiles{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:  map[string]string{"main.gen.go": "package main"},
		Artifact: []byte("fake-native-binary"), ArtifactName: "plugin.so",
		Abi: abi,
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			req := payload.(gen.PluginArtifactLoadReq)
			return gen.PluginArtifactLoadResp{
				PluginID:     manifest.ID,
				ArtifactHash: req.ArtifactHash,
				Status:       gen.AppStatus{ID: manifest.ID, Runtime: "native", State: stateRestartPending},
			}, nil
		}}
	}
	registerReq, deferred, _, _, _, _, err := a.buildLocalRegisterReq(ctx, pkg, nil)
	if err != nil {
		t.Fatalf("buildLocalRegisterReq: %v", err)
	}
	if !deferred {
		t.Fatal("expected deferral flag when the load reports restart_pending")
	}
	if registerReq.ArtifactHash == "" || registerReq.PackageHash == "" {
		t.Fatalf("register req must carry artifact/package hashes: %+v", registerReq)
	}
}

// --- A3: reload deferral ---

// TestNativeReloadInProcessDefersToRestartPending verifies the A3 in-process
// reload: prepare validates, then the reload SKIPS commit, persists the new
// artifact on the pluginhost (artifact_load), closes the prepared candidate
// (abort), and returns a success with restart_pending.
func TestNativeReloadInProcessDefersToRestartPending(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.reload")
	abi := inprocessAbiForTest()
	rec := appRecord{Manifest: manifest, State: stateRunning, PackageHash: "old-package", ArtifactPath: "old.so", ArtifactHash: "old-artifact", Abi: abi}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec
	a.children[manifest.ID] = pluginhostServiceName

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "token", PluginID: manifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_load":
				req := payload.(gen.PluginArtifactLoadReq)
				return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: req.ArtifactHash, Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: stateRestartPending}}, nil
			case "pluginhost.artifact_reload_abort":
				return gen.PluginArtifactReloadAbortResp{}, nil
			default:
				t.Fatalf("unexpected lifecycle call %s", callID)
				return nil, nil
			}
		}}
	}
	candidateManifest := manifest
	candidateManifest.Version = "2.0.0"
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: abi, CandidateArtifactPath: "candidate.so", CandidateArtifactHash: "new-artifact"})
	if err != nil {
		t.Fatalf("in-process reload must not error: %v", err)
	}
	if resp.Status.State != stateRestartPending {
		t.Fatalf("resp.Status.State = %q, want restart_pending", resp.Status.State)
	}
	if got := a.Records[manifest.ID]; got.State != stateRestartPending || got.ArtifactHash != "new-artifact" {
		t.Fatalf("record not staged for restart: %+v", got)
	}
	for _, c := range calls {
		if c == "pluginhost.artifact_reload_commit" {
			t.Fatal("in-process reload must NOT commit (ReplaceArtifact/FreeLibrary is unsafe)")
		}
	}
	if len(a.PendingReloads) != 0 {
		t.Fatalf("deferred reload must not leave a PendingReloads marker: %v", a.PendingReloads)
	}
}

// --- B1: spawn-loop cross-validation ---

// TestRestoreDeferredSwapBumpsGeneration pins the 2026-09-14 generation-drift
// finding: a restart_pending record whose deferred artifact swap activates on
// restore MUST bump the session generation and announce it (reloaded event),
// so connected SPAs stop pointing iframes at the old ?v= cache-bust value.
func TestRestoreDeferredSwapBumpsGeneration(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred.swap")
	rec := appRecord{Manifest: manifest, State: stateRestartPending, ArtifactPath: "staged.so", ArtifactHash: "new-hash", Generation: 10, Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "new-hash", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: stateActive}}, nil
		}}
	}
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild: %v", err)
	}
	a.activateDeferredGeneration(ctx, manifest.ID, rec, true)

	got := a.Records[manifest.ID]
	if got.Generation != 11 {
		t.Fatalf("generation = %d, want 11 (bumped on deferred-swap activation)", got.Generation)
	}
	var lifecycle *gen.AppLifecycleEvent
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "app_lifecycle" {
			continue
		}
		if le, ok := ev.Payload.(gen.AppLifecycleEvent); ok {
			lifecycle = &le
		}
	}
	if lifecycle == nil {
		t.Fatal("no app_lifecycle event emitted for the deferred-swap activation")
	}
	if lifecycle.Kind != "reloaded" || lifecycle.ID != manifest.ID || lifecycle.Generation != 11 {
		t.Fatalf("lifecycle event = %+v, want reloaded/%s/11", lifecycle, manifest.ID)
	}
}

// TestRestoreRunningRecordDoesNotBumpGeneration pins the other half: a plain
// running record restored with the SAME artifact must not bump the generation
// — every host restart would otherwise gratuitously reload all open panels.
func TestRestoreRunningRecordDoesNotBumpGeneration(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.restore.same")
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "same.so", ArtifactHash: "hash", Generation: 10, Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "hash", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: stateActive}}, nil
		}}
	}
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild: %v", err)
	}
	a.activateDeferredGeneration(ctx, manifest.ID, rec, false)

	if got := a.Records[manifest.ID]; got.Generation != 10 {
		t.Fatalf("generation = %d, want unchanged 10 for a same-artifact restore", got.Generation)
	}
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "app_lifecycle" {
			t.Fatalf("unexpected app_lifecycle event for a same-artifact restore: %+v", ev.Payload)
		}
	}
}

// TestRestoreSpawnLoopMarksNativeFailedWithoutPluginhostRecord is the B1
// no-虚标 case: when the pluginhost cannot load the restored artifact, the
// spawn loop's failRestoredApp marks the app failed and does not create a
// child.
func TestRestoreSpawnLoopMarksNativeFailedWithoutPluginhostRecord(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.restore")
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "missing.so", ArtifactHash: "hash", Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return nil, errors.New("pluginhost: read artifact: no such file")
		}}
	}
	// The OnStart spawn loop: spawnChild error → failRestoredApp.
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err == nil {
		t.Fatal("spawnChild must fail when the pluginhost cannot load the artifact")
	} else {
		a.failRestoredApp(ctx, manifest.ID, rec, err)
	}
	if got := a.Records[manifest.ID]; got.State != stateFailed || got.Error == "" {
		t.Fatalf("record must be failed with a diagnostic: %+v", got)
	}
	if _, ok := a.children[manifest.ID]; ok {
		t.Fatal("no child must be created for a failed restore")
	}
}

// TestRestoreSpawnLoopConfirmsRunning is the B1 success case: the
// idempotent artifact_load confirms the pluginhost holds the artifact and the
// record is activated (running) with the pluginhost route.
func TestRestoreSpawnLoopConfirmsRunning(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.restore")
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "present.so", ArtifactHash: "hash", Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "hash", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
		}}
	}
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild: %v", err)
	}
	if got := a.Records[manifest.ID]; got.State != stateRunning {
		t.Fatalf("record state = %q, want running", got.State)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route", a.children[manifest.ID])
	}
}

// TestRestoreSpawnLoopKeepsRestartPendingHonest verifies that a
// restart_pending record is NOT flipped to running when the pluginhost still
// reports a deferral (the artifact is not confirmed).
func TestRestoreSpawnLoopKeepsRestartPendingHonest(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.restore")
	rec := appRecord{Manifest: manifest, State: stateRestartPending, ArtifactPath: "staged.so", ArtifactHash: "new-hash", Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "other-hash", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: stateRestartPending}}, nil
		}}
	}
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild: %v", err)
	}
	if got := a.Records[manifest.ID]; got.State != stateRestartPending {
		t.Fatalf("record state must stay restart_pending when unverified, got %q", got.State)
	}
}

// TestRestoreSpawnLoopDefersWhenPluginhostUnreachable pins the startup
// ordering tolerance: when the pluginhost is not reachable (it starts after
// the appmanager), the spawn loop keeps the persisted state instead of
// marking the app failed.
func TestRestoreSpawnLoopDefersWhenPluginhostUnreachable(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.restore")
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "present.so", ArtifactHash: "hash", Abi: inprocessAbiForTest(), ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("spawnChild must defer (not fail) when the pluginhost is unreachable: %v", err)
	}
	if got := a.Records[manifest.ID]; got.State != stateRunning {
		t.Fatalf("record state = %q, want persisted running (deferred verification)", got.State)
	}
}

// --- A6: plugin_unload failure ---

// TestPluginUnloadFailureMarksUnloadFailedAndRetries verifies A6: a failed
// artifact_unload leaves the record in unload_failed with a diagnostic, and a
// later plugin_unload retry performs the unload.
func TestPluginUnloadFailureMarksUnloadFailedAndRetries(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.unload")
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "plugin.so", ArtifactHash: "hash", Abi: inprocessAbiForTest()}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	unloadErr := errors.New("injected unload failure")
	fail := true
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected call %s", callID)
			}
			if fail {
				return nil, unloadErr
			}
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}

	if _, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: manifest.ID}); err == nil {
		t.Fatal("expected unload failure")
	}
	if got := a.Records[manifest.ID]; got.State != stateUnloadFailed || got.Error != unloadErr.Error() {
		t.Fatalf("record must be unload_failed with a diagnostic: %+v", got)
	}

	// Retry succeeds.
	fail = false
	resp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("retry unload: %v", err)
	}
	if resp.Status.State != "unload_pending" {
		t.Fatalf("retry state = %q, want unload_pending (in-process unload)", resp.Status.State)
	}
	if got := a.Records[manifest.ID]; got.State != "unload_pending" {
		t.Fatalf("retry record state = %q", got.State)
	}
}

// --- subprocess full-chain regression ---

// TestSubprocessFullLifecycleChain runs the whole operation chain over the
// subprocess transport to pin "subprocess 行为不变": register → reload
// (prepare+commit hot swap) → unload → load → unregister, all immediate.
func TestSubprocessFullLifecycleChain(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.subproc")
	abi := subprocessAbiForTest()
	rec := appRecord{Manifest: manifest, State: stateRunning, PackageHash: "pkg-1", ArtifactPath: "v1.exe", ArtifactHash: "h1", Abi: abi}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec
	a.children[manifest.ID] = pluginhostServiceName

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_load":
				return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "h2", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
			case "pluginhost.artifact_unload":
				return gen.PluginArtifactUnloadResp{Removed: 1}, nil
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "t", PluginID: manifest.ID, ArtifactHash: "h2", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
			case "pluginhost.artifact_reload_commit":
				return gen.PluginArtifactReloadCommitResp{PluginID: manifest.ID, ArtifactHash: "h2", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
			default:
				t.Fatalf("unexpected call %s", callID)
				return nil, nil
			}
		}}
	}

	// 1. unload: real stop (subprocess keeps the immediate behavior).
	unloadResp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("unload: %v", err)
	}
	if unloadResp.Status.State != "stopped" {
		t.Fatalf("subprocess unload state = %q, want stopped", unloadResp.Status.State)
	}

	// 2. load: immediate re-load.
	loadResp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loadResp.Status.State != stateRunning {
		t.Fatalf("subprocess load state = %q, want running", loadResp.Status.State)
	}

	// 3. reload: subprocess keeps prepare+commit hot swap. (The committed
	// record carries the pluginhost's loader state, per existing semantics.)
	candidate := manifest
	candidate.Version = "2.0.0"
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "pkg-2", CandidateManifest: &candidate, CandidateAbi: abi, CandidateArtifactPath: "v2.exe", CandidateArtifactHash: "h2"})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if resp.Status.State != stateRunning {
		t.Fatalf("subprocess reload state = %q, want the committed state in appmanager vocabulary (running)", resp.Status.State)
	}
	if got := a.Records[manifest.ID]; got.State != stateRunning || got.ArtifactHash != "h2" {
		t.Fatalf("subprocess reload must commit immediately (state normalized to running): %+v", got)
	}

	// 4. unregister: full removal.
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app still present after unregister")
	}
	if _, ok := a.Records[manifest.ID]; ok {
		t.Fatal("record still present after unregister")
	}
	if a.children[manifest.ID] != "" {
		t.Fatalf("child route still present after unregister: %q", a.children[manifest.ID])
	}
	// Sanity: reload used prepare+commit (no deferral internals).
	sawCommit := false
	for _, c := range calls {
		if c == "pluginhost.artifact_reload_commit" {
			sawCommit = true
		}
	}
	if !sawCommit {
		t.Fatal("subprocess reload must commit (hot swap)")
	}
}

// --- A1 restart-activation route carry (S3 regression) ---

// TestRegisterDeferredCarriesPluginhostRoute pins the A1 restart-activation
// fix: a deferred registration persists the pluginhost route on the record
// (ActorID = pluginhostServiceName) so the OnStart spawn loop cross-validates
// the restored artifact and flips restart_pending → running after a host
// restart. Without the carried route (ActorID == "") the spawn loop skips the
// record and a deferred re-register would sit in restart_pending forever even
// though the pluginhost restored the new artifact.
func TestRegisterDeferredCarriesPluginhostRoute(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred")
	manifest.Version = "2.0.0"
	req := gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: "new.so", ArtifactHash: "new-artifact-hash", Abi: inprocessAbiForTest(),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	status, err := a.doRegister(ctx, req, true)
	if err != nil {
		t.Fatalf("deferred register must not error: %v", err)
	}
	if status.State != stateRestartPending {
		t.Fatalf("status.State = %q, want restart_pending", status.State)
	}
	if got := a.Records[manifest.ID].ActorID; got != pluginhostServiceName {
		t.Fatalf("deferred record ActorID = %q, want pluginhost route (spawn loop must not skip it)", got)
	}

	// Simulate the post-restart spawn loop: the pluginhost restored the
	// artifact via its ArtifactLoads and the idempotent artifact_load
	// confirms it → running, with the pluginhost route.
	rec := a.Records[manifest.ID]
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "new-artifact-hash", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
		}}
	}
	if err := a.spawnChild(ctx, manifest.ID, rec, false); err != nil {
		t.Fatalf("restart spawnChild: %v", err)
	}
	if got := a.Records[manifest.ID].State; got != stateRunning {
		t.Fatalf("record state after restart verification = %q, want running", got)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route", a.children[manifest.ID])
	}
}

// TestRegisterDeferredRouteCarryDoesNotDisturbRunningRoute verifies the
// pre-restart half of the fix: carrying ActorID must not touch the in-memory
// children map, so the still-loaded old artifact keeps routing through the
// pluginhost until the host restarts.
func TestRegisterDeferredRouteCarryDoesNotDisturbRunningRoute(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.deferred")
	oldRec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "old.so", ArtifactHash: "old-hash", Abi: inprocessAbiForTest(), Generation: 3, ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = oldRec
	a.children[manifest.ID] = pluginhostServiceName

	manifest.Version = "2.0.0"
	req := gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: "new.so", ArtifactHash: "new-hash", Abi: inprocessAbiForTest(),
	}
	if _, err := a.doRegister(testutil.HumanCtx(testutil.GenActorID()), req, true); err != nil {
		t.Fatalf("deferred re-register: %v", err)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatal("children route must survive the deferred registration")
	}
	if got := a.Records[manifest.ID]; got.ActorID != pluginhostServiceName || got.State != stateRestartPending {
		t.Fatalf("record must be restart_pending with the pluginhost route carried: %+v", got)
	}
}

// TestRegisterImmediateKeepsSpawnedRoute pins the doRegister immediate-path
// fix: the record must persist the route spawnChild wrote (pluginhost for
// plugins) instead of being clobbered by the pre-spawn local copy. A
// lost ActorID silently disables the OnStart spawn-loop cross-validation
// (B1) for every registered plugin: the record is skipped after a host
// restart and the children rebuild is orphaned.
func TestRegisterImmediateKeepsSpawnedRoute(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.immediate")
	req := gen.AppManagerRegisterReq{
		Manifest: manifest, EntryModule: "main.gen.go",
		Modules:      map[string]string{"main.gen.go": "package main"},
		ArtifactPath: "plugin.so", ArtifactHash: "hash", Abi: inprocessAbiForTest(),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	status, err := a.doRegister(ctx, req, false)
	if err != nil {
		t.Fatalf("immediate register: %v", err)
	}
	if status.State != stateRunning {
		t.Fatalf("status.State = %q, want running", status.State)
	}
	if got := a.Records[manifest.ID]; got.ActorID != pluginhostServiceName {
		t.Fatalf("record ActorID = %q, want pluginhost route (doRegister must not clobber spawnChild's write)", got.ActorID)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatalf("children = %q, want pluginhost route", a.children[manifest.ID])
	}
}

// --- candidate-manifest security validation on native reload ---

// TestNativeReloadRejectsCandidateWithUnknownPermission pins the vocabulary
// gate on reload: a candidate manifest declaring an unknown capability string
// must fail with the same denial registration enforces — never silently
// install a manifest the bridge would not understand at runtime.
func TestNativeReloadRejectsCandidateWithUnknownPermission(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.consent")
	abi := subprocessAbiForTest()
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "v1.exe", ArtifactHash: "h1", Abi: abi, ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec
	a.children[manifest.ID] = pluginhostServiceName

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			t.Fatalf("reload with ungranted permission must not reach the pluginhost, got %s", callID)
			return nil, nil
		}}
	}

	candidate := manifest
	candidate.Version = "2.0.0"
	candidate.Permissions = []string{"app.state", "bogus.cap"}
	_, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, CandidateManifest: &candidate, CandidateAbi: abi, CandidateArtifactPath: "v2.exe", CandidateArtifactHash: "h2"})
	if err == nil {
		t.Fatal("reload with an unknown candidate capability must fail")
	}
	var denied *appbinding.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("error = %v (%T), want DeniedError", err, err)
	}
	if got := a.Records[manifest.ID]; got.ArtifactHash != "h1" || got.State != stateRunning {
		t.Fatalf("rejected reload must leave the record untouched: %+v", got)
	}
}

// TestNativeReloadAcceptsCandidateWithGrantedPermissions is the inverse: a
// candidate whose permissions are all known capabilities passes the gate
// and proceeds to prepare.
func TestNativeReloadAcceptsCandidateWithGrantedPermissions(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.consentok")
	abi := subprocessAbiForTest()
	rec := appRecord{Manifest: manifest, State: stateRunning, ArtifactPath: "v1.exe", ArtifactHash: "h1", Abi: abi, ActorID: pluginhostServiceName}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = rec
	a.children[manifest.ID] = pluginhostServiceName

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			calls = append(calls, callID)
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				return gen.PluginArtifactReloadPrepareResp{Token: "t", PluginID: manifest.ID, ArtifactHash: "h2", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
			case "pluginhost.artifact_reload_commit":
				return gen.PluginArtifactReloadCommitResp{PluginID: manifest.ID, ArtifactHash: "h2", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active"}}, nil
			default:
				t.Fatalf("unexpected call %s", callID)
				return nil, nil
			}
		}}
	}

	candidate := manifest
	candidate.Version = "2.0.0"
	candidate.Permissions = []string{"app.state", "app.emit"}
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, CandidateManifest: &candidate, CandidateAbi: abi, CandidateArtifactPath: "v2.exe", CandidateArtifactHash: "h2"})
	if err != nil {
		t.Fatalf("reload with granted permissions: %v", err)
	}
	if resp.Status.State != stateRunning {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}
	if len(calls) == 0 || calls[0] != "pluginhost.artifact_reload_prepare" {
		t.Fatalf("granted reload must reach prepare, calls = %v", calls)
	}
}

// --- Wedged-record restore (native records persisted without ActorID) ---

// TestOnStartRestoresNativeRouteOnWedgedRecord pins the restart self-heal:
// legacy native records persisted with an empty ActorID (pre-fix writers)
// must not be skipped by the OnStart spawn loop. The pluginhost confirmed it
// holds the artifact (idempotent artifact_load), so the loop re-stamps the
// route (children + record ActorID), and the healed route is persisted so a
// single restart permanently repairs the state. A spore record without
// ActorID stays skipped: restoring it would auto-generate a drifted child ID.
func TestOnStartRestoresNativeRouteOnWedgedRecord(t *testing.T) {
	a := restartPendingTestActor(t)
	native := nativeManifestForTest("native.wedged")
	a.Apps[native.ID] = native
	a.Records[native.ID] = appRecord{Manifest: native, State: stateRunning, ArtifactPath: "app.so", ArtifactHash: "h1", Abi: subprocessAbiForTest()}
	spore := gen.AppManifest{ID: "app.sporeless", Name: "Spore", Namespace: "ns.sporeless", Version: "1.0.0", Runtime: "spore"}
	a.Apps[spore.ID] = spore
	a.Records[spore.ID] = appRecord{Manifest: spore, State: stateRunning}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_load" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactLoadResp{PluginID: native.ID, ArtifactHash: "h1", Status: gen.AppStatus{ID: native.ID, Runtime: "native", State: "active"}}, nil
		}}
	}
	ctx.SpawnFn = func(_ actor.Props, name string) (ref.Ref, error) {
		if name == spore.ID {
			t.Fatal("spore record without ActorID must be skipped by the restore loop")
		}
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}

	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}
	if got := a.children[native.ID]; got != pluginhostServiceName {
		t.Fatalf("children[%s] = %q, want pluginhost route", native.ID, got)
	}
	if got := a.Records[native.ID].ActorID; got != pluginhostServiceName {
		t.Fatalf("record ActorID = %q, want pluginhost route (healed by restore loop)", got)
	}
	if got := a.Records[native.ID].State; got != stateRunning {
		t.Fatalf("record state = %q, want running", got)
	}
	var saved struct {
		Records map[string]appRecord `json:"records"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &saved); err != nil {
		t.Fatalf("reload persisted state: %v", err)
	}
	if got := saved.Records[native.ID].ActorID; got != pluginhostServiceName {
		t.Fatalf("persisted record ActorID = %q, want pluginhost route (restore must persist the heal)", got)
	}
	if _, spawned := a.children[spore.ID]; spawned {
		t.Fatal("spore record without ActorID must not gain a children entry")
	}
}

// TestResolveInvokeAuthSelfHealsWedgedNativeRecord pins the live-path
// self-heal: when the children sentinel is missing but the native record
// says running, an invoke must restore the pluginhost sentinel and proceed
// (mirror of handleReload's wedge recovery) instead of denying with
// "not running" until a host restart.
func TestResolveInvokeAuthSelfHealsWedgedNativeRecord(t *testing.T) {
	a := restartPendingTestActor(t)
	manifest := nativeManifestForTest("native.invoke-wedged")
	manifest.Callables = []gen.AppCallableDescriptor{{ID: "book_list", RequestSchema: "Any", ResponseSchema: "Any"}}
	a.Apps[manifest.ID] = manifest
	a.Records[manifest.ID] = appRecord{Manifest: manifest, State: stateRunning, PackageHash: "pkg-1"}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }

	resolved, err := a.resolveInvokeAuth(ctx, gen.AppManagerInvokeReq{ID: manifest.ID, Callable: "book_list", AgentID: "019f5d9947c200000000000000000001"})
	if err != nil {
		t.Fatalf("resolveInvokeAuth must self-heal the wedged native record: %v", err)
	}
	if resolved.actorID != pluginhostServiceName {
		t.Fatalf("resolved actorID = %q, want pluginhost route", resolved.actorID)
	}
	if got := a.children[manifest.ID]; got != pluginhostServiceName {
		t.Fatalf("children[%s] = %q, want pluginhost sentinel restored", manifest.ID, got)
	}
}
