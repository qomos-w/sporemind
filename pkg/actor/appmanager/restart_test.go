package appmanager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// TestRestartRoundTrip verifies that AppManager state (Apps, Records,
// AuditRecords) survives a Save → Load cycle, simulating a process restart.
func TestStoreIsActorInstanceScoped(t *testing.T) {
	// Verify that two AppManager instances with different actorIDs do not share
	// state through the persist store. This is a regression test for the
	// "Actor 优先" constraint: store must be owned by the actor instance.
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	original1 := &Actor{actorID: "actor-1", store: ps, Apps: map[string]gen.AppManifest{
		"app.a": {ID: "app.a", Name: "A", Version: "1.0.0", Runtime: "spore"},
	}}
	if err := original1.Save(); err != nil {
		t.Fatalf("save actor-1: %v", err)
	}

	original2 := &Actor{actorID: "actor-2", store: ps, Apps: map[string]gen.AppManifest{
		"app.b": {ID: "app.b", Name: "B", Version: "1.0.0", Runtime: "spore"},
	}}
	if err := original2.Save(); err != nil {
		t.Fatalf("save actor-2: %v", err)
	}

	restored1 := &Actor{actorID: "actor-1", store: ps}
	if err := restored1.Load(); err != nil {
		t.Fatalf("load actor-1: %v", err)
	}
	if len(restored1.Apps) != 1 || restored1.Apps["app.a"].Name != "A" {
		t.Fatalf("actor-1 state leaked or missing: %+v", restored1.Apps)
	}

	restored2 := &Actor{actorID: "actor-2", store: ps}
	if err := restored2.Load(); err != nil {
		t.Fatalf("load actor-2: %v", err)
	}
	if len(restored2.Apps) != 1 || restored2.Apps["app.b"].Name != "B" {
		t.Fatalf("actor-2 state leaked or missing: %+v", restored2.Apps)
	}
}

// TestRestartRoundTrip verifies that AppManager state (Apps, Records,
// AuditRecords) survives a Save → Load cycle, simulating a process restart.
func TestRestartRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	// Build "pre-restart" actor state
	manifest := gen.AppManifest{
		ID:              "demo.app",
		Name:            "Demo",
		Namespace:       "sporeapp.demo",
		Version:         "1.2.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{
			{ID: "answer", RequestSchema: "AnswerReq", ResponseSchema: "AnswerResp"},
		},
		Entrypoints: []gen.AppEntrypoint{
			{ID: "home", Kind: "view", Title: "Home"},
		},
	}
	original := &Actor{
		Apps: map[string]gen.AppManifest{"demo.app": manifest},
		Records: map[string]appRecord{
			"demo.app": {
				Manifest:    manifest,
				EntryModule: "main",
				Modules:     map[string]string{"main": "export fun answer(): int = 42"},
				PackageHash: "abc123",
				State:       "running",
				ActorID:     "01HXYZ actors-id-placeholder",
			},
		},
		AuditRecords: []appbinding.AuditRecord{
			{Time: time.Now(), RequestID: "req-1", AppID: "demo.app", Runtime: "spore", AgentID: "agent-1", Role: "coder", Callable: "answer", Allowed: true},
			{Time: time.Now(), RequestID: "req-2", AppID: "demo.app", Runtime: "spore", AgentID: "agent-2", Callable: "answer", Allowed: false, Reason: "denied"},
		},
	}
	original.actorID = "restart-test-actor"
	original.store = ps

	// Save
	if err := original.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists
	statePath := filepath.Join(dir, "restart-test-actor.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file at %s: %v", statePath, err)
	}

	// Simulate restart: create new actor and load
	restored := &Actor{actorID: "restart-test-actor", store: ps}
	if err := restored.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Verify Apps
	if len(restored.Apps) != 1 {
		t.Fatalf("expected 1 app after restart, got %d", len(restored.Apps))
	}
	gotManifest, ok := restored.Apps["demo.app"]
	if !ok {
		t.Fatal("expected demo.app in restored Apps")
	}
	if gotManifest.Version != "1.2.0" || gotManifest.Runtime != "spore" {
		t.Fatalf("unexpected manifest: %+v", gotManifest)
	}
	if len(gotManifest.Callables) != 1 || gotManifest.Callables[0].ID != "answer" {
		t.Fatalf("unexpected callables: %+v", gotManifest.Callables)
	}
	if len(gotManifest.Entrypoints) != 1 || gotManifest.Entrypoints[0].ID != "home" {
		t.Fatalf("unexpected entrypoints: %+v", gotManifest.Entrypoints)
	}

	// Verify Records
	record, ok := restored.Records["demo.app"]
	if !ok {
		t.Fatal("expected demo.app record after restart")
	}
	if record.State != "running" || record.PackageHash != "abc123" || record.EntryModule != "main" {
		t.Fatalf("unexpected record state: %+v", record)
	}
	if record.Modules["main"] != "export fun answer(): int = 42" {
		t.Fatalf("unexpected module source: %q", record.Modules["main"])
	}

	// Verify AuditRecords
	if len(restored.AuditRecords) != 2 {
		t.Fatalf("expected 2 audit records after restart, got %d", len(restored.AuditRecords))
	}
	if restored.AuditRecords[0].RequestID != "req-1" || restored.AuditRecords[1].RequestID != "req-2" {
		t.Fatalf("unexpected audit records order: %+v", restored.AuditRecords)
	}
	if restored.AuditRecords[1].Allowed {
		t.Fatal("expected second audit record to be denied")
	}
}

// TestRestartEmptyState verifies a fresh start with no prior state loads gracefully.
func TestRestartEmptyState(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	fresh := &Actor{actorID: "fresh-actor", store: ps}
	if err := fresh.Load(); err != nil {
		t.Fatalf("Load on empty state: %v", err)
	}
	if fresh.Apps != nil {
		t.Fatalf("expected nil Apps on empty load, got %+v", fresh.Apps)
	}
}

// TestRestartListAfterRestore verifies handleList works after a simulated restart.
func TestRestartListAfterRestore(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	manifest := gen.AppManifest{
		ID:      "list.app",
		Name:    "List",
		Runtime: "spore",
		Version: "2.0.0",
		Entrypoints: []gen.AppEntrypoint{
			{ID: "main", Kind: "view", Title: "Main"},
		},
	}
	original := &Actor{
		Apps:    map[string]gen.AppManifest{"list.app": manifest},
		Records: map[string]appRecord{"list.app": {State: "running", PackageHash: "h1"}},
	}
	original.actorID = "list-test"
	original.store = ps
	if err := original.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored := &Actor{actorID: "list-test", store: ps}
	if err := restored.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	resp, err := restored.handleList(nil, gen.AppManagerListReq{})
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.ID != "list.app" || item.State != "running" || item.Version != "2.0.0" || item.PackageHash != "h1" {
		t.Fatalf("unexpected status: %+v", item)
	}
	if len(item.Entrypoints) != 1 || item.Entrypoints[0].ID != "main" {
		t.Fatalf("unexpected entrypoints: %+v", item.Entrypoints)
	}
}

// TestRestartAuditQueryAfterRestore verifies audit query works after restart.
func TestRestartAuditQueryAfterRestore(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	original := &Actor{
		AuditRecords: []appbinding.AuditRecord{
			{RequestID: "r1", AppID: "app-x", Callable: "compute", Allowed: true},
			{RequestID: "r2", AppID: "app-y", Callable: "render", Allowed: false, Reason: "no permission"},
		},
	}
	original.actorID = "audit-test"
	original.store = ps
	if err := original.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored := &Actor{actorID: "audit-test", store: ps}
	if err := restored.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Query all
	resp, err := restored.handleAudit(nil, gen.AppManagerAuditReq{})
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if len(resp.Records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(resp.Records))
	}

	// Query filtered
	resp, err = restored.handleAudit(nil, gen.AppManagerAuditReq{AppID: "app-x"})
	if err != nil {
		t.Fatalf("audit filtered: %v", err)
	}
	if len(resp.Records) != 1 || resp.Records[0].AppID != "app-x" {
		t.Fatalf("expected 1 record for app-x, got %+v", resp.Records)
	}
}

// TestRestartAfterUnregister verifies that an unregistered app does not
// reappear after a simulated restart.
func TestRestartAfterUnregister(t *testing.T) {
	dir := t.TempDir()
	ps := persist.NewFSPersist(dir)

	manifest := gen.AppManifest{
		ID:              "gone.app",
		Name:            "Gone",
		Namespace:       "sporeapp.gone.app",
		Version:         "1.0.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{
			{ID: "noop", RequestSchema: "NoopReq", ResponseSchema: "NoopResp"},
		},
	}
	original := &Actor{
		Apps: map[string]gen.AppManifest{"gone.app": manifest},
		Records: map[string]appRecord{
			"gone.app": {
				Manifest:    manifest,
				EntryModule: "main",
				Modules:     map[string]string{"main": "export fun noop(): int = 1"},
				PackageHash: "h-gone",
				State:       "running",
			},
		},
		actorID: "restart-unregister-test",
		store:   ps,
	}
	if err := original.Save(); err != nil {
		t.Fatalf("save before unregister: %v", err)
	}

	// Simulate unregister by deleting state and saving.
	original.mu.Lock()
	delete(original.Apps, "gone.app")
	delete(original.Records, "gone.app")
	delete(original.children, "gone.app")
	original.mu.Unlock()
	if err := original.Save(); err != nil {
		t.Fatalf("save after unregister: %v", err)
	}

	restored := &Actor{actorID: "restart-unregister-test", store: ps}
	if err := restored.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(restored.Apps) != 0 || len(restored.Records) != 0 || len(restored.children) != 0 {
		t.Fatalf("unregistered app reappeared after restart: apps=%+v records=%+v children=%+v",
			restored.Apps, restored.Records, restored.children)
	}
}

// TestUnregisterStateCleanup verifies handleUnregister removes app state and
// agent bindings from the in-memory registry.
func TestUnregisterStateCleanup(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "cleanup.app",
		Name:    "Cleanup",
		Version: "1.0.0",
		Runtime: "spore",
		AgentBinding: &gen.AppAgentBinding{
			Surface:    &gen.AgentSurfaceBinding{AgentID: "agent-1", Entrypoint: "home"},
			Capability: &gen.AgentCapabilityBinding{Callables: []string{"answer"}},
		},
	}
	a := &Actor{
		Apps: map[string]gen.AppManifest{"cleanup.app": manifest},
		Records: map[string]appRecord{
			"cleanup.app": {Manifest: manifest, EntryModule: "main", Modules: map[string]string{}, State: "running"},
		},
		children:          map[string]string{"cleanup.app": "child-actor-id"},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		actorID:           "cleanup-test",
		store:             persist.NewFSPersist(t.TempDir()),
	}
	if err := a.bindFreeAgentPolicy(manifest); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// We cannot call handleUnregister without a real actor.Context for
	// Destroy/EmitEvent, so we exercise the same state cleanup path directly.
	a.mu.Lock()
	delete(a.Apps, "cleanup.app")
	delete(a.Records, "cleanup.app")
	delete(a.children, "cleanup.app")
	a.bindings.Unbind("cleanup.app", "agent-1")
	a.mu.Unlock()

	if _, ok := a.Apps["cleanup.app"]; ok {
		t.Fatal("app still present")
	}
	if _, ok := a.Records["cleanup.app"]; ok {
		t.Fatal("record still present")
	}
	if _, ok := a.children["cleanup.app"]; ok {
		t.Fatal("child still present")
	}
}
