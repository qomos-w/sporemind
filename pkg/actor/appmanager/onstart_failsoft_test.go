package appmanager

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestOnStartSurvivesRestorePanic pins the 2026-09-09 incident class: a
// panic anywhere in the OnStart recovery tail used to kill the cell before
// Expose() ran, leaving every appmanager.* call "not registered". Now the
// domain is exposed before recovery and every recovery step is panic-
// guarded: a poisoned record marks only itself failed, later steps and
// other apps' restores still run, and OnStart returns nil.
func TestOnStartSurvivesRestorePanic(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{
		actorID:  "failsoft",
		store:    persist.NewFSPersist(dir),
		bindings: nil,
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
	poison := gen.AppManifest{ID: "app.poison", Name: "Poison", Version: "1.0.0", Runtime: "spore", Namespace: "ns.poison"}
	healthy := gen.AppManifest{ID: "app.healthy", Name: "Healthy", Version: "1.0.0", Runtime: "spore", Namespace: "ns.healthy"}
	// Valid canonical child IDs: spawnChild parses record.ActorID to
	// pre-allocate the restored child's identity before spawning.
	const childID = "019f5d9947c200000000000000000001"
	for _, m := range []gen.AppManifest{poison, healthy} {
		a.Apps[m.ID] = m
		a.Records[m.ID] = appRecord{
			Manifest: m, State: "running", ActorID: childID,
			EntryModule: "main", Modules: map[string]string{"main": "export fun main() {}"},
		}
		a.children[m.ID] = childID
	}

	spawned := map[string]bool{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegisteredDomains = []string{}
	ctx.SpawnFn = func(_ actor.Props, name string) (ref.Ref, error) {
		if name == "app.poison" {
			panic("poisoned persisted state")
		}
		spawned[name] = true
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}

	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart must survive a restore panic, got error: %v", err)
	}

	if a.Records["app.poison"].State != stateFailed {
		t.Fatalf("poisoned app must be marked failed, got %q", a.Records["app.poison"].State)
	}
	if a.Records["app.poison"].Error == "" {
		t.Fatal("poisoned app must carry a diagnostic error")
	}
	if !spawned["app.healthy"] {
		t.Fatal("healthy app restore must still run after the poisoned one")
	}
	if a.Records["app.healthy"].State != stateRunning {
		t.Fatalf("healthy app must be running, got %q", a.Records["app.healthy"].State)
	}
	found := false
	for _, d := range ctx.RegisteredDomains {
		if d == "appmanager" {
			found = true
		}
	}
	if !found {
		t.Fatal("appmanager domain must be exposed")
	}
}

// TestRunGuardedConvertsPanicToError pins the panic→error conversion used by
// the per-app restore guard.
func TestRunGuardedConvertsPanicToError(t *testing.T) {
	if err := runGuarded(func() error { return nil }); err != nil {
		t.Fatalf("clean step must pass through, got %v", err)
	}
	err := runGuarded(func() error { panic("boom") })
	if err == nil {
		t.Fatal("panic must convert to error")
	}
}
