package appmanager

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// failingPersist wraps a real persist and injects an error on Save after a
// configurable number of successful saves. This lets tests simulate a Save
// failure at a specific point in the cleanup state machine.
type failingPersist struct {
	inner     persist.Persist
	failAfter int // fail once saveCount exceeds this value (-1 = never)
	saveCount int
	lastErr   error
}

func (f *failingPersist) Load(name string, v any) error { return f.inner.Load(name, v) }
func (f *failingPersist) Save(name string, v any) error {
	f.saveCount++
	if f.failAfter >= 0 && f.saveCount > f.failAfter {
		f.lastErr = errors.New("injected save failure")
		return f.lastErr
	}
	return f.inner.Save(name, v)
}
func (f *failingPersist) Delete(name string) error { return f.inner.Delete(name) }

func newCleanupTestActor(t *testing.T) (*Actor, gen.AppManifest) {
	t.Helper()
	manifest := gen.AppManifest{
		ID: "test.app", Name: "Test", Namespace: "test.app",
		Version: "1.0.0", Runtime: "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "ping", RequestSchema: "Any", ResponseSchema: "Any"}},
	}
	a := &Actor{
		actorID:  "appmanager-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running", Generation: 1}},
		children: map[string]string{},
		sessions: map[string]appSession{},
	}
	return a, manifest
}

func newProtocolManager(t *testing.T) *protocol.Manager {
	t.Helper()
	mgr, err := protocol.NewManager(protocol.StaticFragment{
		NamespaceOffsets: map[string]uint64{protocol.SystemNamespace: 0},
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

// TestUnregisterTransitionsThroughCleanupStates verifies that a successful
// unregister passes through the unloading → protocol_cleanup_pending →
// cleanup_pending states and ends with the app fully removed.
func TestUnregisterTransitionsThroughCleanupStates(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	ctx := &testutil.FakeCtx{}

	// Register the namespace so protocol unregister has something to remove.
	if err := a.protocol.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app should be removed from Apps")
	}
	if _, ok := a.Records[manifest.ID]; ok {
		t.Fatal("app should be removed from Records")
	}
	if _, ok := a.children[manifest.ID]; ok {
		t.Fatal("app should be removed from children")
	}

	// Verify lifecycle event emitted.
	var foundUnloaded bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind == "app_lifecycle" {
			le := ev.Payload.(gen.AppLifecycleEvent)
			if le.Kind == "unloaded" {
				foundUnloaded = true
			}
		}
	}
	if !foundUnloaded {
		t.Fatal("expected unloaded lifecycle event")
	}
}

// TestProtocolUnregisterFailureLeavesProtocolCleanupPending verifies that when
// protocol.UnregisterAppProtocol fails, the app is left in
// protocol_cleanup_pending with the error recorded, and the records are
// retained for retry.
func TestProtocolUnregisterFailureLeavesProtocolCleanupPending(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	// Use the system namespace which is reserved and cannot be unregistered.
	manifest.Namespace = protocol.SystemNamespace
	a.Apps[manifest.ID] = manifest
	rec := a.Records[manifest.ID]
	rec.Manifest = manifest
	a.Records[manifest.ID] = rec

	ctx := &testutil.FakeCtx{}
	err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID})
	if err == nil {
		t.Fatal("expected protocol unregister failure")
	}

	rec = a.Records[manifest.ID]
	if rec.State != stateProtocolCleanupPending {
		t.Fatalf("expected state %q, got %q", stateProtocolCleanupPending, rec.State)
	}
	if rec.Error == "" {
		t.Fatal("expected non-empty error")
	}
	if _, ok := a.Apps[manifest.ID]; !ok {
		t.Fatal("app should be retained in Apps for retry")
	}
}

// TestRetryCleanupCompletesAfterProtocolFailure verifies that retry_cleanup
// can complete the cleanup after a protocol failure is resolved.
func TestRetryCleanupCompletesAfterProtocolFailure(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	manifest.Namespace = protocol.SystemNamespace
	a.Apps[manifest.ID] = manifest
	rec := a.Records[manifest.ID]
	rec.Manifest = manifest
	a.Records[manifest.ID] = rec

	ctx := &testutil.FakeCtx{}

	// First attempt fails (reserved namespace).
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err == nil {
		t.Fatal("expected first attempt to fail")
	}
	if a.Records[manifest.ID].State != stateProtocolCleanupPending {
		t.Fatalf("expected protocol_cleanup_pending, got %q", a.Records[manifest.ID].State)
	}

	// Fix the namespace so unregister will succeed, then retry.
	manifest.Namespace = "test.app.fixable"
	a.Apps[manifest.ID] = manifest
	rec = a.Records[manifest.ID]
	rec.Manifest = manifest
	a.Records[manifest.ID] = rec

	resp, err := a.handleRetryCleanup(ctx, gen.AppManagerRetryCleanupReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("retry_cleanup: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("expected status 'completed', got %q", resp.Status)
	}
	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app should be removed after successful retry")
	}
}

// TestSaveFailureDuringCleanupRetainsPendingState verifies that when Save
// fails during the cleanup_pending persist, the app retains its record so
// the cleanup can be retried.
func TestSaveFailureDuringCleanupRetainsPendingState(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	if err := a.protocol.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	// Make Save fail after the protocol_cleanup_pending persist succeeds
	// (failAfter = 3: unloading-save, protocol_cleanup_pending-save,
	// cleanup_pending-save — the 4th save for deletion will fail).
	fp := &failingPersist{inner: persist.NewFSPersist(t.TempDir()), failAfter: 3}
	a.store = fp

	ctx := &testutil.FakeCtx{}
	err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID})
	if err == nil {
		t.Fatal("expected save failure")
	}

	// The app should still be present because the final save failed.
	rec, ok := a.Records[manifest.ID]
	if !ok {
		t.Fatal("app should be retained when final save fails")
	}
	// State should be cleanup_pending (protocol unregister succeeded,
	// cleanup_pending state was persisted, but the deletion save failed).
	if rec.State != stateCleanupPending {
		t.Fatalf("expected state %q, got %q", stateCleanupPending, rec.State)
	}
}

// TestRetryCleanupSucceedsAfterSaveFailure verifies that after a Save failure
// leaves the app in cleanup_pending, a retry completes the deletion.
func TestRetryCleanupSucceedsAfterSaveFailure(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	if err := a.protocol.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	fp := &failingPersist{inner: persist.NewFSPersist(t.TempDir()), failAfter: 3}
	a.store = fp

	ctx := &testutil.FakeCtx{}
	// First attempt fails on save.
	_ = a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID})

	if a.Records[manifest.ID].State != stateCleanupPending {
		t.Fatalf("expected cleanup_pending, got %q", a.Records[manifest.ID].State)
	}

	// Fix the persist and retry.
	fp.failAfter = -1
	resp, err := a.handleRetryCleanup(ctx, gen.AppManagerRetryCleanupReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("retry_cleanup: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("expected 'completed', got %q", resp.Status)
	}
	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app should be removed after retry")
	}
}

// TestInvokeRejectedDuringPendingCleanup verifies that invoke is rejected for
// apps in any cleanup-pending state.
func TestInvokeRejectedDuringPendingCleanup(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.children[manifest.ID] = "01HXYZ actors-id-placeholder"

	pendingStates := []string{stateUnloading, stateProtocolCleanupPending, stateCleanupPending, stateUnloadFailed}
	ctx := &testutil.FakeCtx{}

	for _, state := range pendingStates {
		a.mu.Lock()
		rec := a.Records[manifest.ID]
		rec.State = state
		a.Records[manifest.ID] = rec
		a.mu.Unlock()

		_, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{
			ID: manifest.ID, Callable: "ping",
			AgentID: "agent-1",
		})
		if err == nil {
			t.Fatalf("invoke should be rejected in state %q", state)
		}
	}
}

// TestRetryCleanupNotFoundForRemovedApp verifies that retry_cleanup on an
// already-removed app returns not_found (idempotent).
func TestRetryCleanupNotFoundForRemovedApp(t *testing.T) {
	a, _ := newCleanupTestActor(t)
	ctx := &testutil.FakeCtx{}

	resp, err := a.handleRetryCleanup(ctx, gen.AppManagerRetryCleanupReq{ID: "nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "not_found" {
		t.Fatalf("expected 'not_found', got %q", resp.Status)
	}
}

// TestRetryCleanupNotPendingForRunningApp verifies that retry_cleanup on a
// running app returns not_pending.
func TestRetryCleanupNotPendingForRunningApp(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	ctx := &testutil.FakeCtx{}

	resp, err := a.handleRetryCleanup(ctx, gen.AppManagerRetryCleanupReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Status != "not_pending" {
		t.Fatalf("expected 'not_pending', got %q", resp.Status)
	}
}

// TestRecoverPendingCleanupOnStartup verifies that recoverPendingCleanup
// detects apps in pending states and completes their cleanup.
func TestRecoverPendingCleanupOnStartup(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	mgr := newProtocolManager(t)

	manifest := gen.AppManifest{
		ID: "stale.app", Name: "Stale", Namespace: "stale.app",
		Version: "1.0.0", Runtime: "spore",
	}
	// Register the namespace so protocol unregister has something to remove.
	if err := mgr.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	// Simulate a crashed process that left the app in cleanup_pending.
	a := &Actor{
		actorID:  "appmanager",
		store:    ps,
		protocol: mgr,
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateCleanupPending}},
		children: map[string]string{},
		sessions: map[string]appSession{},
	}
	_ = a.Save()

	// Simulate restart: new instance loads the persisted state.
	a2 := &Actor{
		actorID:  "appmanager",
		store:    ps,
		protocol: mgr,
		bindings: appbinding.NewRegistry(),
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if a2.Records[manifest.ID].State != stateCleanupPending {
		t.Fatalf("expected cleanup_pending after load, got %q", a2.Records[manifest.ID].State)
	}

	ctx := &testutil.FakeCtx{}
	a2.recoverPendingCleanup(ctx)

	if _, ok := a2.Records[manifest.ID]; ok {
		t.Fatal("pending app should be removed after recovery")
	}
	if _, ok := a2.Apps[manifest.ID]; ok {
		t.Fatal("pending app should be removed from Apps after recovery")
	}
}

// TestRecoverPendingCleanupProtocolPending verifies recovery of an app stuck
// in protocol_cleanup_pending (protocol unregister had failed before crash).
func TestRecoverPendingCleanupProtocolPending(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	mgr := newProtocolManager(t)

	manifest := gen.AppManifest{
		ID: "proto-stale.app", Name: "ProtoStale", Namespace: "proto-stale.app",
		Version: "1.0.0", Runtime: "spore",
	}
	if err := mgr.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	a := &Actor{
		actorID:  "appmanager",
		store:    ps,
		protocol: mgr,
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateProtocolCleanupPending}},
		children: map[string]string{},
		sessions: map[string]appSession{},
	}
	_ = a.Save()

	a2 := &Actor{
		actorID:  "appmanager",
		store:    ps,
		protocol: mgr,
		bindings: appbinding.NewRegistry(),
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	ctx := &testutil.FakeCtx{}
	a2.recoverPendingCleanup(ctx)

	if _, ok := a2.Records[manifest.ID]; ok {
		t.Fatal("pending app should be removed after recovery")
	}
}

// TestUnregisterIdempotentOnPendingState verifies that calling unregister on
// an app already in a pending state delegates to finishCleanup rather than
// re-attempting the front half.
func TestUnregisterIdempotentOnPendingState(t *testing.T) {
	a, manifest := newCleanupTestActor(t)
	a.protocol = newProtocolManager(t)
	if err := a.protocol.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	ctx := &testutil.FakeCtx{}

	// Manually place the app in protocol_cleanup_pending.
	a.mu.Lock()
	rec := a.Records[manifest.ID]
	rec.State = stateProtocolCleanupPending
	a.Records[manifest.ID] = rec
	a.mu.Unlock()

	// Calling unregister should delegate to finishCleanup and succeed.
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("unregister on pending app: %v", err)
	}
	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app should be removed")
	}
}

// TestRetryCleanupOnUnloadFailedRetriesFullUnregister verifies that
// retry_cleanup on an app in unload_failed retries the full unregister flow.
func TestRetryCleanupOnUnloadFailedRetriesFullUnregister(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.retry", Name: "NativeRetry", Namespace: "native.retry",
		Version: "1.0.0", Runtime: "native",
	}
	a := &Actor{
		actorID:  "appmanager-retry-cleanup-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateUnloadFailed}},
		children: map[string]string{manifest.ID: pluginhostServiceName},
		sessions: map[string]appSession{},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				return nil, nil
			}
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}

	resp, err := a.handleRetryCleanup(ctx, gen.AppManagerRetryCleanupReq{ID: manifest.ID})
	if err != nil {
		t.Fatalf("retry_cleanup: %v", err)
	}
	if resp.Status != "completed" {
		t.Fatalf("expected 'completed', got %q", resp.Status)
	}
	if _, ok := a.Apps[manifest.ID]; ok {
		t.Fatal("app should be removed after retry")
	}
}

// TestUnregisterPluginPreservesAppState verifies that unregistering a
// plugin does NOT fire pluginhost.state_purge. The app's "<appID>/"
// document subtree must survive teardown so saved data persists across
// unregister/re-register cycles.
func TestUnregisterPluginPreservesAppState(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "native.purge", Name: "NativePurge", Namespace: "native.purge",
		Version: "1.0.0", Runtime: "native",
	}
	a := &Actor{
		actorID:  "appmanager-purge-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateRunning, Generation: 1}},
		children: map[string]string{},
		sessions: map[string]appSession{},
	}
	a.protocol = newProtocolManager(t)

	var mu sync.Mutex
	var purges []gen.PluginStatePurgeReq
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		mu.Lock()
		defer mu.Unlock()
		if callID == "pluginhost.state_purge" {
			purges = append(purges, payload.(gen.PluginStatePurgeReq))
		}
		return nil
	})
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				return nil, fmt.Errorf("unexpected planner call %q", callID)
			}
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(purges) != 0 {
		t.Fatalf("state_purge calls = %v, want none — unregister must preserve app data", purges)
	}
}
