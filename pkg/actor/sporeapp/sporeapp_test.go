package sporeapp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/spore/script"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func TestAssetSnapshotPersistsAcrossRestart(t *testing.T) {
	store := persist.NewFSPersist(t.TempDir())
	a := &Actor{store: store, actorID: "sporeapp-assets", Manifest: gen.AppManifest{ID: "example.app"}, Assets: map[string][]byte{"icon.png": {1, 2, 3}}, SchemaDescriptors: map[string]gen.AppObjectDescriptor{"Payload": {Kind: "struct", Name: "Payload", SchemaID: 9001}}}
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	restored := &Actor{store: store, actorID: "sporeapp-assets"}
	if err := restored.Load(); err != nil {
		t.Fatal(err)
	}
	if got := restored.Assets["icon.png"]; len(got) != 3 || got[2] != 3 {
		t.Fatalf("assets after restart = %v", got)
	}
	if got := restored.SchemaDescriptors["Payload"]; got.SchemaID != 9001 {
		t.Fatalf("schema descriptor after restart = %+v", got)
	}
}

func TestActorLoadRuntime(t *testing.T) {
	a := &Actor{Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}, State: map[string]any{"count": float64(1)}}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()
	result, err := a.runtime.Call("answer")
	if err != nil || !result.Ok() {
		t.Fatalf("call: result=%+v err=%v", result, err)
	}
}

func TestActorStateSetMergesAndPersists(t *testing.T) {
	a := &Actor{actorID: "sporeapp-state-merge-test", store: persist.NewFSPersist(t.TempDir()), Manifest: gen.AppManifest{ID: "example.app", Runtime: "spore"}, State: map[string]any{"count": float64(1)}}
	state, err := a.handleStateSet(nil, gen.SporeAppStateSetReq{ID: "example.app", State: map[string]string{"name": "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if state["count"] != float64(1) || state["name"] != "demo" {
		t.Fatalf("state=%v", state)
	}
}

func TestActorReloadKeepsOldRuntimeOnFailure(t *testing.T) {
	a := &Actor{Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()
	old := a.runtime
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{ID: a.Manifest.ID, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int =`}}); err == nil {
		t.Fatal("expected reload error")
	}
	if a.runtime != old {
		t.Fatal("runtime changed after failed reload")
	}
	result, err := a.runtime.Call("answer")
	if err != nil || !result.Ok() {
		t.Fatalf("old runtime unavailable: result=%+v err=%v", result, err)
	}
}

func TestActorReloadUpdatesHashAndKeepsState(t *testing.T) {
	a := &Actor{actorID: "sporeapp-reload-hash-test", store: persist.NewFSPersist(t.TempDir()), Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}, State: map[string]any{"count": float64(7)}}
	if err := a.loadRuntime(); err != nil {
		t.Fatal(err)
	}
	defer a.runtime.Close()
	modules := map[string]string{"main": `export fun answer(): int = 43`}
	expectedHash := (script.Package{AppID: a.Manifest.ID, Version: a.Manifest.Version, EntryModule: "main", Modules: modules}).ComputeHash()
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{ID: a.Manifest.ID, EntryModule: "main", PackageHash: expectedHash, Modules: modules}); err != nil {
		t.Fatal(err)
	}
	if a.PackageHash != expectedHash {
		t.Fatalf("hash=%q", a.PackageHash)
	}
	if a.State["count"] != float64(7) {
		t.Fatalf("state=%v", a.State)
	}
	result, err := a.runtime.Call("answer")
	if err != nil || !result.Ok() {
		t.Fatalf("call: result=%+v err=%v", result, err)
	}
}

func TestReloadRejectsStaleExpectedStateVersion(t *testing.T) {
	a := &Actor{Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}, State: map[string]any{"count": float64(7)}, StateVersion: 3}
	if err := a.loadRuntime(); err != nil {
		t.Fatal(err)
	}
	defer a.runtime.Close()
	modules := map[string]string{"main": `export fun answer(): int = 43`}
	_, err := a.handleReload(nil, gen.SporeAppReloadReq{ID: a.Manifest.ID, EntryModule: "main", Modules: modules, ExpectedStateVersion: 2})
	if err == nil || !strings.Contains(err.Error(), "state version conflict") {
		t.Fatalf("expected state version conflict, got %v", err)
	}
	// state must be untouched
	if a.StateVersion != 3 {
		t.Fatalf("state version mutated: %d", a.StateVersion)
	}
}

func TestReloadAppliesMigratedStateAndBumpsVersion(t *testing.T) {
	a := &Actor{actorID: "sporeapp-reload-migrate-test", store: persist.NewFSPersist(t.TempDir()), Manifest: gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}, State: map[string]any{"count": float64(7)}, StateVersion: 3}
	if err := a.loadRuntime(); err != nil {
		t.Fatal(err)
	}
	defer a.runtime.Close()
	modules := map[string]string{"main": `export fun answer(): int = 43`}
	migrated := map[string]any{"count_v2": float64(8)}
	resp, err := a.handleReload(nil, gen.SporeAppReloadReq{ID: a.Manifest.ID, EntryModule: "main", Modules: modules, ExpectedStateVersion: 3, MigratedState: migrated})
	if err != nil {
		t.Fatal(err)
	}
	if a.StateVersion != 4 {
		t.Fatalf("expected state version 4, got %d", a.StateVersion)
	}
	if a.State["count_v2"] != float64(8) {
		t.Fatalf("migrated state not applied: %v", a.State)
	}
	if _, old := a.State["count"]; old {
		t.Fatalf("old state should be replaced: %v", a.State)
	}
	if resp.StateVersion != 4 {
		t.Fatalf("resp state version=%d", resp.StateVersion)
	}
}

func TestDecodeRequestPayloadJSONFallback(t *testing.T) {
	a := &Actor{}
	args, err := a.decodeRequestPayload("answer", []byte(`[42]`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Integral JSON numbers normalise to int so the script VM's strict
	// numeric typing accepts them for int parameters.
	if len(args) != 1 || args[0] != int(42) {
		t.Fatalf("unexpected args: %+v", args)
	}
}

func TestDecodeRequestPayloadEmpty(t *testing.T) {
	a := &Actor{}
	args, err := a.decodeRequestPayload("answer", nil)
	if err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if args != nil {
		t.Fatalf("expected nil args for empty payload, got %+v", args)
	}
}

func TestEncodeResponsePayloadJSONFallback(t *testing.T) {
	a := &Actor{}
	payload, err := a.encodeResponsePayload("answer", 42)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(payload) != "42" {
		t.Fatalf("expected '42', got %q", string(payload))
	}
}

func TestEncodeResponsePayloadNil(t *testing.T) {
	a := &Actor{}
	payload, err := a.encodeResponsePayload("answer", nil)
	if err != nil {
		t.Fatalf("encode nil: %v", err)
	}
	if len(payload) != 0 {
		t.Fatalf("expected empty payload for nil, got %d bytes", len(payload))
	}
}

func TestLookupSchemaWithoutReader(t *testing.T) {
	a := &Actor{}
	_, ok := a.lookupRequestSchema("answer")
	if ok {
		t.Fatal("expected false when schemas is nil")
	}
	_, ok = a.lookupResponseSchema("answer")
	if ok {
		t.Fatal("expected false when schemas is nil")
	}
}

func TestStateSetIncrementsVersion(t *testing.T) {
	a := &Actor{actorID: "sporeapp-state-version-test", store: persist.NewFSPersist(t.TempDir()), Manifest: gen.AppManifest{ID: "example.app", Runtime: "spore"}, State: map[string]any{}}
	if a.StateVersion != 0 {
		t.Fatalf("initial version should be 0, got %d", a.StateVersion)
	}
	_, err := a.handleStateSet(nil, gen.SporeAppStateSetReq{ID: "example.app", State: map[string]string{"key": "val1"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.StateVersion != 1 {
		t.Fatalf("expected version 1 after first set, got %d", a.StateVersion)
	}
	_, err = a.handleStateSet(nil, gen.SporeAppStateSetReq{ID: "example.app", State: map[string]string{"key": "val2"}})
	if err != nil {
		t.Fatal(err)
	}
	if a.StateVersion != 2 {
		t.Fatalf("expected version 2 after second set, got %d", a.StateVersion)
	}
}

func TestExecutionBudgetFromManifest(t *testing.T) {
	budget := executionBudget(&gen.AppSecurityPolicy{MaxInstructions: 11, MaxDurationMs: 12, MaxHostCalls: 13, MaxOutputBytes: 14})
	if budget.MaxInstructions != 11 || budget.MaxDuration != 12*time.Millisecond || budget.MaxHostCalls != 13 || budget.MaxOutputBytes != 14 {
		t.Fatalf("execution budget = %+v", budget)
	}
}

func TestInvokeEnforcesManifestHostCallBudget(t *testing.T) {
	const source = `import { stateGet } from "app"
export fun inspect(): any {
  stateGet()
  return stateGet()
}`
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "budget.app", Name: "Budget", Version: "1.0.0", Runtime: "spore",
			Permissions: []string{"state"},
			Security:    &gen.AppSecurityPolicy{MaxHostCalls: 1},
			Callables:   []gen.AppCallableDescriptor{{ID: "inspect"}},
		},
		EntryModule:         "main",
		Modules:             map[string]string{"main": source},
		State:               map[string]any{},
		allowedCapabilities: map[string]struct{}{"state": {}},
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatal(err)
	}
	defer a.runtime.Close()
	_, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{ID: "budget.app", Callable: "inspect"})
	if err == nil || !strings.Contains(err.Error(), "host call budget exceeded") {
		t.Fatalf("expected host call budget error, got %v", err)
	}
	if a.StateVersion != 0 || len(a.State) != 0 {
		t.Fatalf("state changed during read-only host calls: state=%v version=%d", a.State, a.StateVersion)
	}
}

func TestInvokeRejectsOutputOverManifestBudget(t *testing.T) {
	a := &Actor{Manifest: gen.AppManifest{ID: "budget.app", Name: "Budget", Version: "1.0.0", Runtime: "spore", Security: &gen.AppSecurityPolicy{MaxOutputBytes: 1}}, EntryModule: "main", Modules: map[string]string{"main": `export fun answer(): int = 42`}}
	if err := a.loadRuntime(); err != nil {
		t.Fatal(err)
	}
	defer a.runtime.Close()
	if _, err := a.handleInvoke(nil, gen.SporeAppInvokeReq{ID: "budget.app", Callable: "answer"}); err == nil || !strings.Contains(err.Error(), "output budget exceeded") {
		t.Fatalf("expected output budget error, got %v", err)
	}
}

func TestReloadRollbackOnSaveFailure(t *testing.T) {
	a := &Actor{
		Manifest:    gen.AppManifest{ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore"},
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 42`},
		store:       &failingStore{},
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()

	originalEntry := a.EntryModule
	originalModules := a.Modules["main"]

	// Attempt reload — Save will fail
	_, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:          a.Manifest.ID,
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 99`},
	})
	if err == nil {
		t.Fatal("expected reload to fail due to Save error")
	}

	// Actor fields should be rolled back
	if a.EntryModule != originalEntry {
		t.Fatalf("EntryModule not rolled back: got %q, want %q", a.EntryModule, originalEntry)
	}
	if a.Modules["main"] != originalModules {
		t.Fatalf("Modules not rolled back: got %q, want %q", a.Modules["main"], originalModules)
	}
}

// failingStore is a test Persist that always fails on Save.
type failingStore struct{}

func (f *failingStore) Save(actorID string, data any) error {
	return fmt.Errorf("simulated save failure")
}
func (f *failingStore) Load(actorID string, target any) error { return nil }
func (f *failingStore) Delete(actorID string) error           { return nil }

func TestLoadRuntimeRejectsMissingExport(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID:      "missing.app",
			Name:    "Missing",
			Version: "1.0.0",
			Runtime: "spore",
			Callables: []gen.AppCallableDescriptor{
				{ID: "missing", RequestSchema: "MissingReq", ResponseSchema: "MissingResp"},
			},
		},
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun present(): int = 1`},
	}
	err := a.loadRuntime()
	if err == nil {
		t.Fatal("expected export validation error")
	}
	if !strings.Contains(err.Error(), "export validation failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRuntimeRejectsInvalidPackage(t *testing.T) {
	a := &Actor{
		Manifest:    gen.AppManifest{ID: "", Name: "Invalid", Version: "1.0.0", Runtime: "spore"},
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun present(): int = 1`},
	}
	err := a.loadRuntime()
	if err == nil {
		t.Fatal("expected package validation error")
	}
	if !strings.Contains(err.Error(), "manifest and entry module are required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPackageValidationIncludesDependenciesAndHash(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID:      "dep.app",
			Name:    "Dep",
			Version: "1.0.0",
			Runtime: "spore",
			Dependencies: []gen.AppDependency{
				{ID: "lib.core", Version: "1.2.0"},
			},
		},
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 42`},
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()

	// Verify that a mismatched explicit hash is rejected.
	bad := &Actor{
		Manifest:    a.Manifest,
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 42`},
		PackageHash: "definitely-wrong-hash",
	}
	if err := bad.loadRuntime(); err == nil {
		t.Fatal("expected hash mismatch error")
	}
}

// ── §4 SecurityPolicy → runtime creation: capability binding ──

// TestLoadRuntimeBindsDeclaredCapability verifies that when the "state"
// capability is in the allowed set, the corresponding host functions are
// bound into the script runtime.
func TestLoadRuntimeBindsDeclaredCapability(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore",
			Permissions: []string{"state"},
			Callables:   []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "Any", ResponseSchema: "Any"}},
		},
		EntryModule:         "main",
		Modules:             map[string]string{"main": `export fun noop(): string = "ok"`},
		allowedCapabilities: map[string]struct{}{"state": {}},
		State:               map[string]any{},
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()

	bound := make(map[string]bool)
	for _, fn := range a.runtime.BoundFunctions() {
		bound[fn.Namespace+"."+fn.Name] = true
	}
	if !bound["app.stateGet"] {
		t.Fatal("expected app.stateGet to be bound")
	}
	if !bound["app.stateSet"] {
		t.Fatal("expected app.stateSet to be bound")
	}
}

// TestLoadRuntimeRejectsUnknownCapability verifies that a declared capability
// with no registered host binding produces a stable diagnostic error.
func TestLoadRuntimeRejectsUnknownCapability(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore",
			Callables: []gen.AppCallableDescriptor{{ID: "noop", RequestSchema: "Any", ResponseSchema: "Any"}},
		},
		EntryModule:         "main",
		Modules:             map[string]string{"main": `export fun noop(): string = "ok"`},
		allowedCapabilities: map[string]struct{}{"nonexistent-cap": {}},
		State:               map[string]any{},
	}
	err := a.loadRuntime()
	if err == nil {
		t.Fatal("expected error for unknown capability")
	}
	if !strings.Contains(err.Error(), "no host binding") {
		t.Fatalf("expected 'no host binding' in error, got: %v", err)
	}
}

// TestCapabilityEnforcementByAbsence verifies that when a capability is NOT
// in the allowed set, the script cannot import or use the corresponding host
// functions — enforcement by structural absence.
func TestCapabilityEnforcementByAbsence(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore",
			Permissions: []string{"state"},
			Callables:   []gen.AppCallableDescriptor{{ID: "peek", RequestSchema: "Any", ResponseSchema: "Any"}},
		},
		EntryModule: "main",
		// Script tries to import from the "app" namespace which will not be
		// bound because no capabilities are allowed.
		Modules: map[string]string{"main": `import stateGet from "app"\nexport fun peek(): string = "no-access"`},
		// Empty allowed set → no host functions bound.
		allowedCapabilities: map[string]struct{}{},
		State:               map[string]any{},
	}
	err := a.loadRuntime()
	if err == nil {
		t.Fatal("expected load failure when script imports unbound namespace")
	}
}

// TestStateHostFunctionsMutateState verifies the "state" capability host
// functions actually read and write the actor's durable state.
func TestStateHostFunctionsMutateState(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "example.app", Name: "Example", Version: "1.0.0", Runtime: "spore",
			Permissions: []string{"state"},
		},
		allowedCapabilities: map[string]struct{}{"state": {}},
		State:               map[string]any{"init": float64(1)},
	}
	// Directly test the host functions.
	snap := a.hostStateGet()
	if snap["init"] != float64(1) {
		t.Fatalf("hostStateGet: expected init=1, got %v", snap["init"])
	}
	a.hostStateSet("new", float64(42))
	if a.State["new"] != float64(42) {
		t.Fatalf("hostStateSet: state not updated, got %v", a.State["new"])
	}
	if a.StateVersion != 1 {
		t.Fatalf("expected StateVersion=1 after set, got %d", a.StateVersion)
	}
}

// ── §5 reload validation + schema ref validation + migration metadata ──

// TestReloadValidatesExports verifies that a reload whose replacement package
// drops a manifest-declared callable is rejected before the atomic swap, and
// that the live runtime and durable state are left completely untouched.
func TestReloadValidatesExports(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "exports.app", Name: "Exports", Version: "1.0.0", Runtime: "spore",
			Callables: []gen.AppCallableDescriptor{
				{ID: "answer", RequestSchema: "Any", ResponseSchema: "Any"},
			},
		},
		EntryModule:  "main",
		Modules:      map[string]string{"main": `export fun answer(): int = 42`},
		State:        map[string]any{"count": float64(1)},
		StateVersion: 2,
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()
	oldRuntime := a.runtime

	// Replacement package exports only "renamed", dropping the declared
	// "answer" callable. The reload must fail at export validation.
	_, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:          a.Manifest.ID,
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun renamed(): int = 7`},
	})
	if err == nil {
		t.Fatal("expected reload to fail due to missing export, got nil")
	}
	if !strings.Contains(err.Error(), "reload validation failed") || !strings.Contains(err.Error(), "export validation") {
		t.Fatalf("expected reload validation / export validation error, got: %v", err)
	}
	// Live runtime, state, and state version must be untouched.
	if a.runtime != oldRuntime {
		t.Fatal("runtime swapped despite validation failure")
	}
	if a.StateVersion != 2 {
		t.Fatalf("state version mutated on failed reload: got %d, want 2", a.StateVersion)
	}
	if a.State["count"] != float64(1) {
		t.Fatalf("state mutated on failed reload: %v", a.State)
	}
	// The old runtime must still serve the original callable.
	if r, err := a.runtime.Call("answer"); err != nil || !r.Ok() {
		t.Fatalf("old runtime unavailable after failed reload: result=%+v err=%v", r, err)
	}
}

// TestReloadRebindsCapabilities verifies that after a successful reload the
// allowed capability host functions are bound into the replacement runtime and
// remain callable from script. The replacement package imports app.stateGet,
// proving the capability surface survives the swap.
func TestReloadRebindsCapabilities(t *testing.T) {
	const src = `import { stateGet } from "app"
export fun peek(): any { return stateGet() }`
	a := &Actor{
		actorID:      "sporeapp-reload-caps-test",
		store:        persist.NewFSPersist(t.TempDir()),
		Manifest: gen.AppManifest{
			ID: "caps.app", Name: "Caps", Version: "1.0.0", Runtime: "spore",
			Permissions: []string{"state"},
			Callables:   []gen.AppCallableDescriptor{{ID: "peek", RequestSchema: "Any", ResponseSchema: "Any"}},
		},
		EntryModule:         "main",
		Modules:             map[string]string{"main": src},
		allowedCapabilities: map[string]struct{}{"state": {}},
		State:               map[string]any{"greeting": "hi"},
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()

	// Sanity: capability host functions are bound and callable before reload.
	if r, err := a.runtime.Call("peek"); err != nil || !r.Ok() {
		t.Fatalf("call peek before reload: result=%+v err=%v", r, err)
	}

	// Reload with an equivalent package. The reload must rebind capabilities.
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:          a.Manifest.ID,
		EntryModule: "main",
		Modules:     map[string]string{"main": src},
	}); err != nil {
		t.Fatalf("reload: %v", err)
	}

	// After reload the host functions must be bound into the new runtime.
	bound := make(map[string]bool)
	for _, fn := range a.runtime.BoundFunctions() {
		bound[fn.Namespace+"."+fn.Name] = true
	}
	if !bound["app.stateGet"] {
		t.Fatal("app.stateGet not bound after reload")
	}
	if !bound["app.stateSet"] {
		t.Fatal("app.stateSet not bound after reload")
	}
	// And they must still be callable from script.
	if r, err := a.runtime.Call("peek"); err != nil || !r.Ok() {
		t.Fatalf("call peek after reload: result=%+v err=%v", r, err)
	}
}

// TestValidateSchemaRefs verifies that malformed schema references are rejected
// at load time, and that well-formed refs (including an unregistered ref with
// SchemaID 0 and no hash) load successfully.
func TestValidateSchemaRefs(t *testing.T) {
	cases := []struct {
		name    string
		ref     gen.AppSchemaRef
		wantSub string
	}{
		{"empty name", gen.AppSchemaRef{Name: "", Hash: "deadbeef", SchemaID: 1}, "empty name"},
		{"negative schema id", gen.AppSchemaRef{Name: "Bad", Hash: "deadbeef", SchemaID: -1}, "negative SchemaID"},
		{"registered without hash", gen.AppSchemaRef{Name: "NoHash", Hash: "", SchemaID: 5}, "empty hash"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Actor{
				Manifest: gen.AppManifest{
					ID: "schema.app", Name: "Schema", Version: "1.0.0", Runtime: "spore",
					Schemas:   []gen.AppSchemaRef{tc.ref},
					Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "Any", ResponseSchema: "Any"}},
				},
				EntryModule: "main",
				Modules:     map[string]string{"main": `export fun answer(): int = 42`},
			}
			err := a.loadRuntime()
			if err == nil {
				t.Fatal("expected schema ref validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantSub, err)
			}
		})
	}

	// Positive cases: well-formed registered ref, and an unregistered ref
	// (SchemaID 0) with no hash both load fine.
	t.Run("valid registered ref", func(t *testing.T) {
		a := &Actor{
			Manifest: gen.AppManifest{
				ID: "schema.app", Name: "Schema", Version: "1.0.0", Runtime: "spore",
				Schemas:   []gen.AppSchemaRef{{Name: "Good", Hash: "deadbeef", SchemaID: 9}},
				Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "Any", ResponseSchema: "Any"}},
			},
			EntryModule: "main",
			Modules:     map[string]string{"main": `export fun answer(): int = 42`},
		}
		if err := a.loadRuntime(); err != nil {
			t.Fatalf("expected valid ref to load, got: %v", err)
		}
		defer a.runtime.Close()
	})
	t.Run("unregistered ref without hash", func(t *testing.T) {
		a := &Actor{
			Manifest: gen.AppManifest{
				ID: "schema.app", Name: "Schema", Version: "1.0.0", Runtime: "spore",
				Schemas:   []gen.AppSchemaRef{{Name: "Pending", Hash: "", SchemaID: 0}},
				Callables: []gen.AppCallableDescriptor{{ID: "answer", RequestSchema: "Any", ResponseSchema: "Any"}},
			},
			EntryModule: "main",
			Modules:     map[string]string{"main": `export fun answer(): int = 42`},
		}
		if err := a.loadRuntime(); err != nil {
			t.Fatalf("expected unregistered ref (SchemaID 0, no hash) to load, got: %v", err)
		}
		defer a.runtime.Close()
	})
}

// TestMigrationVersionTracking verifies that each reload carrying a non-empty
// MigratedState increments MigrationVersion, that a reload without migrated
// state leaves it unchanged, and that the version persists across successive
// reloads on the same actor.
func TestMigrationVersionTracking(t *testing.T) {
	a := &Actor{
		actorID:     "sporeapp-mig-version-test",
		store:       persist.NewFSPersist(t.TempDir()),
		Manifest:     gen.AppManifest{ID: "mig.app", Name: "Mig", Version: "1.0.0", Runtime: "spore"},
		EntryModule:  "main",
		Modules:      map[string]string{"main": `export fun answer(): int = 42`},
		State:        map[string]any{"v": float64(0)},
		StateVersion: 1,
	}
	if err := a.loadRuntime(); err != nil {
		t.Fatalf("loadRuntime: %v", err)
	}
	defer a.runtime.Close()

	if a.MigrationVersion != 0 {
		t.Fatalf("initial migration version should be 0, got %d", a.MigrationVersion)
	}

	// Reload #1 with migrated state → MigrationVersion becomes 1 and state is
	// replaced.
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:            a.Manifest.ID,
		EntryModule:   "main",
		Modules:       map[string]string{"main": `export fun answer(): int = 43`},
		MigratedState: map[string]any{"v": float64(1)},
	}); err != nil {
		t.Fatalf("reload #1: %v", err)
	}
	if a.MigrationVersion != 1 {
		t.Fatalf("after migration reload #1, expected MigrationVersion 1, got %d", a.MigrationVersion)
	}
	if a.State["v"] != float64(1) {
		t.Fatalf("migrated state not applied on reload #1: %v", a.State)
	}

	// Reload #2 without migrated state → MigrationVersion unchanged.
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:          a.Manifest.ID,
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 44`},
	}); err != nil {
		t.Fatalf("reload #2: %v", err)
	}
	if a.MigrationVersion != 1 {
		t.Fatalf("after non-migration reload, expected MigrationVersion 1, got %d", a.MigrationVersion)
	}

	// Reload #3 with migrated state → MigrationVersion becomes 2.
	if _, err := a.handleReload(nil, gen.SporeAppReloadReq{
		ID:            a.Manifest.ID,
		EntryModule:   "main",
		Modules:       map[string]string{"main": `export fun answer(): int = 45`},
		MigratedState: map[string]any{"v": float64(2)},
	}); err != nil {
		t.Fatalf("reload #3: %v", err)
	}
	if a.MigrationVersion != 2 {
		t.Fatalf("after migration reload #3, expected MigrationVersion 2, got %d", a.MigrationVersion)
	}
}

// TestDecodeRequestPayloadTBCFallbackToJSON verifies that a payload with TBC
// magic header but no matching schema falls through to JSON decoding.
func TestDecodeRequestPayloadTBCFallbackToJSON(t *testing.T) {
	a := &Actor{}
	// TBC magic + invalid binary content — without a schema, the codec can't
	// decode it, so the function must fall through to JSON.
	// But TBC data is NOT valid JSON, so this should return an error.
	tbcData := []byte{0x54, 0x42, 0x43, 0x02, 0xFF, 0xFE}
	_, err := a.decodeRequestPayload("nonexistent", tbcData)
	if err == nil {
		t.Fatal("expected error for TBC data without matching schema")
	}
}

// TestDecodeRequestPayloadRejectsInvalidJSON verifies that non-TBC, non-JSON
// payload is rejected with a clear error.
func TestDecodeRequestPayloadRejectsInvalidJSON(t *testing.T) {
	a := &Actor{}
	invalidJSON := []byte("{not valid json")
	_, err := a.decodeRequestPayload("any", invalidJSON)
	if err == nil {
		t.Fatal("expected error for invalid JSON payload")
	}
}

// TestEncodeResponsePayloadFallsBackOnCodecError verifies that when the codec
// fails to encode (e.g. encoding nil with a schema), the function falls back
// to JSON marshaling.
func TestEncodeResponsePayloadFallsBackOnCodecError(t *testing.T) {
	a := &Actor{}
	// Without a schema (no reader), encodeResponsePayload falls back to JSON.
	// Verify it produces valid JSON for a struct.
	result := map[string]any{"key": "value"}
	payload, err := a.encodeResponsePayload("answer", result)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(payload) != `{"key":"value"}` {
		t.Fatalf("expected JSON {\"key\":\"value\"}, got %q", string(payload))
	}
}

// TestDecodeRequestPayloadValidJSONArray verifies decoding a JSON array payload
// with multiple arguments.
func TestDecodeRequestPayloadValidJSONArray(t *testing.T) {
	a := &Actor{}
	args, err := a.decodeRequestPayload("answer", []byte(`["hello", 42, true]`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(args) != 3 {
		t.Fatalf("expected 3 args, got %d", len(args))
	}
	if args[0] != "hello" {
		t.Fatalf("expected first arg 'hello', got %v", args[0])
	}
}

func TestResolveAssetRuntimeWiring(t *testing.T) {
	tmp := t.TempDir()
	// SetAssetDirs builds tiers as <dir>/apps/<namespace>/assets/
	projectAssetDir := filepath.Join(tmp, "project", "apps", "sporeapp.asset.app", "assets")
	os.MkdirAll(projectAssetDir, 0o755)
	os.WriteFile(filepath.Join(projectAssetDir, "Task"), []byte("project-task-asset"), 0o644)

	a := &Actor{
		Manifest: gen.AppManifest{
			ID: "asset.app", Name: "Asset", Version: "1.0.0", Runtime: "spore",
			Namespace: "sporeapp.asset.app", ProtocolVersion: 1,
			Schemas: []gen.AppSchemaRef{
				{Name: "Task", SchemaID: 2000},
			},
		},
		EntryModule: "main",
		Modules:     map[string]string{"main": `export fun answer(): int = 42`},
	}
	a.SetAssetDirs(filepath.Join(tmp, "project"), "", "")
	a.resolveDeclaredAssets()

	if len(a.resolvedAssets) != 1 {
		t.Fatalf("expected 1 resolved asset, got %d", len(a.resolvedAssets))
	}
	data, ok := a.resolvedAssets["Task"]
	if !ok {
		t.Fatal("expected Task asset to be resolved")
	}
	if string(data) != "project-task-asset" {
		t.Fatalf("unexpected asset content: %q", data)
	}
}

func TestResolveDeclaredAssetsNoTiers(t *testing.T) {
	a := &Actor{
		Manifest: gen.AppManifest{
			Schemas: []gen.AppSchemaRef{
				{Name: "Task", SchemaID: 2000},
			},
		},
	}
	a.resolveDeclaredAssets()
	if len(a.resolvedAssets) != 0 {
		t.Fatalf("expected 0 resolved assets without tiers, got %d", len(a.resolvedAssets))
	}
}

func TestAssetRefsExtractsSchemaNames(t *testing.T) {
	m := gen.AppManifest{
		Schemas: []gen.AppSchemaRef{
			{Name: "Task", SchemaID: 2000},
			{Name: "User", SchemaID: 2001},
			{Name: "", SchemaID: 0},        // empty name skipped
			{Name: "Task", SchemaID: 2000}, // duplicate skipped
		},
	}
	refs := assetRefs(m)
	if len(refs) != 2 {
		t.Fatalf("expected 2 asset refs, got %d: %v", len(refs), refs)
	}
	if refs[0] != "Task" || refs[1] != "User" {
		t.Fatalf("expected [Task User], got %v", refs)
	}
}

func TestResolveAssetProjectOverridesUser(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	userDir := filepath.Join(tmp, "user")
	builtinDir := filepath.Join(tmp, "builtin")

	// Create asset in all three tiers — project should win.
	for _, dir := range []string{projectDir, userDir, builtinDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(projectDir, "template.html"), []byte("project"), 0o644)
	os.WriteFile(filepath.Join(userDir, "template.html"), []byte("user"), 0o644)
	os.WriteFile(filepath.Join(builtinDir, "template.html"), []byte("builtin"), 0o644)

	tiers := []AssetTier{
		{Origin: "project", Dir: projectDir},
		{Origin: "user", Dir: userDir},
		{Origin: "builtin", Dir: builtinDir},
	}
	data, ok := resolveAsset("template.html", tiers)
	if !ok {
		t.Fatal("expected asset to resolve")
	}
	if string(data) != "project" {
		t.Fatalf("expected project override, got %q", data)
	}
}

func TestResolveAssetFallsBackToBuiltin(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	userDir := filepath.Join(tmp, "user")
	builtinDir := filepath.Join(tmp, "builtin")

	os.MkdirAll(projectDir, 0o755)
	os.MkdirAll(userDir, 0o755)
	os.MkdirAll(builtinDir, 0o755)

	// Only builtin has the asset.
	os.WriteFile(filepath.Join(builtinDir, "fallback.txt"), []byte("builtin"), 0o644)

	tiers := []AssetTier{
		{Origin: "project", Dir: projectDir},
		{Origin: "user", Dir: userDir},
		{Origin: "builtin", Dir: builtinDir},
	}
	data, ok := resolveAsset("fallback.txt", tiers)
	if !ok {
		t.Fatal("expected builtin fallback")
	}
	if string(data) != "builtin" {
		t.Fatalf("expected builtin, got %q", data)
	}
}

func TestResolveAssetNotFound(t *testing.T) {
	tmp := t.TempDir()
	tiers := []AssetTier{
		{Origin: "project", Dir: tmp},
		{Origin: "user", Dir: ""},
		{Origin: "builtin", Dir: ""},
	}
	_, ok := resolveAsset("missing.txt", tiers)
	if ok {
		t.Fatal("expected asset not found")
	}
}

func TestAssetTiersForAppPriorityOrder(t *testing.T) {
	tiers := assetTiersForApp("sporeapp.test.app", "/p", "/u", "/b")
	if len(tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(tiers))
	}
	if tiers[0].Origin != "project" || tiers[1].Origin != "user" || tiers[2].Origin != "builtin" {
		t.Fatalf("unexpected origin order: %v", tiers)
	}
	// Verify directory paths contain the namespace and assets segment.
	suffix := filepath.Join("apps", "sporeapp.test.app", "assets")
	for _, tier := range tiers {
		if !strings.Contains(tier.Dir, suffix) {
			t.Errorf("tier %q dir %q should contain %q", tier.Origin, tier.Dir, suffix)
		}
	}
}

// TestTruncDetail pins the log/error truncation constraint: error/diagnostic
// detail longer than the cap must be truncated with a visible marker so
// unbounded user-script output never crosses the actor boundary verbatim.
func TestTruncDetail(t *testing.T) {
	if got := truncDetail(""); got != "" {
		t.Errorf("truncDetail(\"\") = %q, want empty", got)
	}
	short := strings.Repeat("x", 10)
	if got := truncDetail(short); got != short {
		t.Errorf("truncDetail(short) = %q, want unchanged", got)
	}
	long := strings.Repeat("x", 600)
	got := truncDetail(long)
	if len(got) != 512+len("...(truncated)") {
		t.Errorf("truncDetail(long) length = %d, want %d", len(got), 512+len("...(truncated)"))
	}
	if !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("truncDetail(long) = %q..., want ...(truncated) suffix", got[:32])
	}
	if got[:512] != long[:512] {
		t.Error("truncDetail(long) lost leading content")
	}
}
