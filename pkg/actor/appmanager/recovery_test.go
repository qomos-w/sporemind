package appmanager

import (
	"fmt"
	"testing"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestProtocolDescriptorReregisteredAfterRestore verifies that after a
// simulated process restart (new AppManager instance loading persisted state),
// the protocol descriptors for restored apps are re-registered into the
// protocol.Manager so namespace resolution survives restart.
func TestProtocolDescriptorReregisteredAfterRestore(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	manifest := gen.AppManifest{
		ID: "demo.app", Name: "Demo", Version: "1.0.0", Runtime: "spore",
		Namespace: "sporeapp.demo.app", ProtocolVersion: 1,
		Schemas: []gen.AppSchemaRef{{Name: "Task", Hash: "hash", SchemaID: 1900}},
	}

	// Simulate a previously-running app that was saved to disk.
	a1 := &Actor{
		actorID:  "appmanager",
		store:    ps,
		Apps:     map[string]gen.AppManifest{"demo.app": manifest},
		Records:  map[string]appRecord{"demo.app": {Manifest: manifest, State: "running", ActorID: "01HXYZ actors-id-placeholder"}},
		children: map[string]string{"demo.app": "01HXYZ actors-id-placeholder"},
	}
	if err := a1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// New instance — simulate restart: fresh protocol.Manager, same persisted state.
	mgr, err := protocol.NewManager(protocol.StaticFragment{NamespaceOffsets: map[string]uint64{protocol.SystemNamespace: 0}})
	if err != nil {
		t.Fatal(err)
	}
	a2 := &Actor{
		actorID:           "appmanager",
		store:             ps,
		protocol:          mgr,
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Simulate OnStart descriptor re-registration for restored apps.
	ctx := &testutil.FakeCtx{}
	a2.reregisterProtocolDescriptors(ctx)

	// Verify the namespace is registered in the protocol manager.
	wire, err := mgr.WireID(manifest.Namespace, 1900)
	if err != nil {
		t.Fatalf("WireID after restore: %v", err)
	}
	if wire == 1900 {
		t.Fatal("expected external offset wire ID, got system-local")
	}

	// Verify record is still running (descriptor registration succeeded).
	rec := a2.Records["demo.app"]
	if rec.State != "running" {
		t.Fatalf("expected state 'running', got %q", rec.State)
	}
}

// TestProtocolDescriptorRegistrationFailureMarksFailed verifies that when
// protocol descriptor re-registration fails (e.g. duplicate namespace), the
// app is marked failed with a stable diagnostic and emits a lifecycle event.
func TestProtocolDescriptorRegistrationFailureMarksFailed(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	manifest := gen.AppManifest{
		ID: "dup.app", Name: "Dup", Version: "1.0.0", Runtime: "spore",
		Namespace: "sporeapp.dup.app", ProtocolVersion: 1,
		Schemas: []gen.AppSchemaRef{{Name: "Task", Hash: "hash", SchemaID: 1900}},
	}

	mgr, err := protocol.NewManager(protocol.StaticFragment{NamespaceOffsets: map[string]uint64{protocol.SystemNamespace: 0}})
	if err != nil {
		t.Fatal(err)
	}
	// Pre-register the namespace to force a duplicate error.
	if err := mgr.RegisterAppManifest(manifest); err != nil {
		t.Fatal(err)
	}

	a := &Actor{
		actorID:  "appmanager",
		store:    ps,
		protocol: mgr,
		Apps:     map[string]gen.AppManifest{"dup.app": manifest},
		Records:  map[string]appRecord{"dup.app": {Manifest: manifest, State: "running", ActorID: "01HXYZ actors-id-placeholder"}},
		children: map[string]string{"dup.app": "01HXYZ actors-id-placeholder"},
	}

	ctx := &testutil.FakeCtx{}
	a.reregisterProtocolDescriptors(ctx)

	rec := a.Records["dup.app"]
	if rec.State != "failed" {
		t.Fatalf("expected state 'failed', got %q", rec.State)
	}
	if rec.Error == "" {
		t.Fatal("expected non-empty error diagnostic")
	}
	// Lifecycle event emitted.
	if len(ctx.EmittedEvents) == 0 {
		t.Fatal("expected lifecycle event")
	}
}

// TestRestoreRebindsFreeAgentWildcard verifies that restoring a persisted
// appmanager rebinds the app's free-agent policy under the wildcard so
// agent_action continues to work after a restart.
func TestRestoreRebindsFreeAgentWildcard(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	manifest := gen.AppManifest{
		ID: "restore.app", Name: "Restore", Version: "1.0.0", Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			FreeAgent: &gen.FreeAgentBinding{AllowMessage: true},
		},
	}

	// The actorID must match between save and OnInit (which derives it from ctx).
	ctx := testutil.HumanCtx(testutil.GenActorID())
	actorID := ctx.Self().ID().String()

	a1 := &Actor{
		actorID: actorID,
		store:   ps,
		Apps:    map[string]gen.AppManifest{"restore.app": manifest},
		Records: map[string]appRecord{"restore.app": {Manifest: manifest, State: "running", ActorID: "01HXYZ actors-id-placeholder"}},
	}
	if err := a1.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Simulate restart + OnInit binding restore.
	a2 := &Actor{
		store:    ps,
		bindings: appbinding.NewRegistry(),
	}
	if err := a2.OnInit(ctx); err != nil {
		t.Fatalf("OnInit: %v", err)
	}

	rec, ok := a2.Records["restore.app"]
	if !ok {
		t.Fatal("record missing after restore")
	}
	if rec.State != "running" {
		t.Fatalf("expected state 'running', got %q", rec.State)
	}
	// The wildcard free-agent policy must be re-authorized for any caller.
	if err := authorizeFreeAgent(a2.FreeAgentPolicies, "restore.app", "any-agent", "message", ""); err != nil {
		t.Fatalf("wildcard policy should authorize after restore: %v", err)
	}
}

// TestFailRestoredAppEmitsLifecycleEvent verifies that failRestoredApp
// emits an app_lifecycle event with Kind=failed.
func TestFailRestoredAppEmitsLifecycleEvent(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)
	manifest := gen.AppManifest{ID: "crash.app", Name: "Crash", Version: "1.0.0", Runtime: "spore"}
	a := &Actor{
		actorID: "appmanager",
		store:   ps,
		Apps:    map[string]gen.AppManifest{"crash.app": manifest},
		Records: map[string]appRecord{"crash.app": {Manifest: manifest, State: "running"}},
	}
	ctx := &testutil.FakeCtx{}
	a.failRestoredApp(ctx, "crash.app", a.Records["crash.app"], fmt.Errorf("spawn child failed: no actor system"))

	rec := a.Records["crash.app"]
	if rec.State != "failed" {
		t.Fatalf("expected state 'failed', got %q", rec.State)
	}
	if len(ctx.EmittedEvents) == 0 {
		t.Fatal("expected lifecycle event emitted")
	}
	ev := ctx.EmittedEvents[0]
	if ev.Kind != "app_lifecycle" {
		t.Fatalf("expected kind 'app_lifecycle', got %q", ev.Kind)
	}
	le, ok := ev.Payload.(gen.AppLifecycleEvent)
	if !ok {
		t.Fatalf("expected AppLifecycleEvent payload, got %T", ev.Payload)
	}
	if le.Kind != "failed" {
		t.Fatalf("expected event Kind 'failed', got %q", le.Kind)
	}
	if le.ID != "crash.app" {
		t.Fatalf("expected event ID 'crash.app', got %q", le.ID)
	}
	if le.Error == "" {
		t.Fatal("expected non-empty error in lifecycle event")
	}
}
