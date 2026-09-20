package appmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type lifecyclePlanner struct {
	call func(string, any) (any, error)
}

func (p lifecyclePlanner) Plan(ref.Ref, string, any, ...plan.Option) (plan.Node, error) {
	return nil, nil
}
func (p lifecyclePlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	value, err := p.call(callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](value)
}
func (p lifecyclePlanner) Stream(context.Context, ref.Ref, string, any, func(any) error) *promise.Promise[any] {
	return nil
}

func newNativeReloadActor(t *testing.T) (*Actor, gen.AppManifest) {
	t.Helper()
	manifest := gen.AppManifest{ID: "native.app", Name: "Native", Namespace: "native.app", Version: "1.0.0", Runtime: "native", Entrypoints: []gen.AppEntrypoint{{ID: "main", Kind: "view"}}}
	// Subprocess transport: prepare+commit hot-swap is the subprocess
	// behavior; in-process reloads defer to restart_pending instead.
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "subprocess", TrustClass: "first_party", Signer: "first-party"}
	a := &Actor{actorID: "appmanager-test", store: persist.NewFSPersist(t.TempDir()), bindings: appbinding.NewRegistry(), Apps: map[string]gen.AppManifest{manifest.ID: manifest}, Records: map[string]appRecord{manifest.ID: {Manifest: manifest, State: "active", PackageHash: "old-package", ArtifactPath: "old.dll", ArtifactHash: "old-artifact", Abi: abi}}, children: map[string]string{manifest.ID: pluginhostServiceName}}
	return a, manifest
}

func TestNativeReloadUsesPrepareCommitWithoutUnload(t *testing.T) {
	a, manifest := newNativeReloadActor(t)
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
			case "pluginhost.artifact_reload_commit":
				if payload.(gen.PluginArtifactReloadCommitReq).Token != "token" {
					t.Fatal("wrong commit token")
				}
				return gen.PluginArtifactReloadCommitResp{PluginID: manifest.ID, ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: manifest.ID, Runtime: "native", State: "active", Version: "2.0.0"}}, nil
			default:
				t.Fatalf("unexpected lifecycle call %s", callID)
				return nil, nil
			}
		}}
	}
	candidateManifest := manifest
	candidateManifest.Version = "2.0.0"
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi, CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact"})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0] != "pluginhost.artifact_reload_prepare" || calls[1] != "pluginhost.artifact_reload_commit" {
		t.Fatalf("calls = %v", calls)
	}
	if resp.Status.Version != "2.0.0" || a.Records[manifest.ID].ArtifactHash != "new-artifact" {
		t.Fatalf("reload state not committed: resp=%+v record=%+v", resp, a.Records[manifest.ID])
	}
	if resp.Status.PackageHash != "new-package" {
		t.Fatalf("reload PackageHash = %q, want %q", resp.Status.PackageHash, "new-package")
	}
	if resp.Status.PackageHash == resp.Status.ArtifactHash {
		t.Fatalf("reload PackageHash must differ from ArtifactHash; got %q", resp.Status.PackageHash)
	}
	if a.Records[manifest.ID].PackageHash != "new-package" {
		t.Fatalf("record PackageHash = %q, want %q", a.Records[manifest.ID].PackageHash, "new-package")
	}
}

func TestNativeReloadCommitFailureRestoresManagerRecord(t *testing.T) {
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
				return nil, errors.New("injected commit failure")
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
	if _, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi, CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact"}); err == nil {
		t.Fatal("expected commit failure")
	}
	record := a.Records[manifest.ID]
	if record.ArtifactHash != "old-artifact" || record.PackageHash != "old-package" || record.State != "active" {
		t.Fatalf("manager record not restored: %+v", record)
	}
}

func TestFinishProjectRegistrationCompensatesLoadedArtifact(t *testing.T) {
	manifest := gen.AppManifest{ID: "native.invalid", Name: "Invalid", Namespace: "native.invalid", Version: "1.0.0", Runtime: "native", ProtocolVersion: 2, Callables: []gen.AppCallableDescriptor{{ID: "dup", RequestSchema: "Any", ResponseSchema: "Any"}, {ID: "dup", RequestSchema: "Any", ResponseSchema: "Any"}}}
	a := &Actor{actorID: "appmanager-test", store: persist.NewFSPersist(t.TempDir()), bindings: appbinding.NewRegistry(), Apps: map[string]gen.AppManifest{}, Records: map[string]appRecord{}, children: map[string]string{}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	unloads := 0
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected call %s", callID)
			}
			if payload.(gen.PluginArtifactUnloadReq).PluginID != manifest.ID {
				t.Fatal("wrong plugin id")
			}
			unloads++
			return gen.PluginArtifactUnloadResp{}, nil
		}}
	}
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "inprocess", TrustClass: "first_party", Signer: "first-party"}
	_, err := a.finishProjectRegistration(ctx, gen.AppManagerRegisterReq{Manifest: manifest, ArtifactPath: "candidate.dll", ArtifactHash: "abc", Abi: abi}, pluginRef, true, false)
	if err == nil {
		t.Fatal("expected registration failure")
	}
	if unloads != 1 {
		t.Fatalf("unload compensation calls = %d", unloads)
	}
	if _, exists := a.Apps[manifest.ID]; exists {
		t.Fatal("failed registration mutated app state")
	}
}

func TestNativeUnregisterFailureRetainsStateForRetry(t *testing.T) {
	manifest := gen.AppManifest{ID: "native.app", Name: "Native", Namespace: "native.app", Version: "1.0.0", Runtime: "native"}
	a := &Actor{
		actorID: "appmanager-native-unregister-test", store: persist.NewFSPersist(t.TempDir()), bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running"}},
		children: map[string]string{manifest.ID: pluginhostServiceName},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	unloadErr := errors.New("injected unload failure")
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, _ any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected call %s", callID)
			}
			return gen.PluginArtifactUnloadResp{}, unloadErr
		}}
	}
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err == nil {
		t.Fatal("expected unload failure")
	}
	if _, ok := a.Apps[manifest.ID]; !ok || a.children[manifest.ID] != pluginhostServiceName || a.Records[manifest.ID].State != "unload_failed" {
		t.Fatalf("failed unload must retain managed state: apps=%v children=%v record=%+v", a.Apps, a.children, a.Records[manifest.ID])
	}
	unloadErr = nil
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("retry unregister: %v", err)
	}
	if _, ok := a.Apps[manifest.ID]; ok || a.children[manifest.ID] != "" {
		t.Fatalf("successful retry must clean managed state: apps=%v children=%v", a.Apps, a.children)
	}
}

func testObjectDescriptors(name string, schemaID int64) map[string]gen.AppObjectDescriptor {
	return map[string]gen.AppObjectDescriptor{name: {
		Kind: "struct", Name: name, SchemaID: schemaID,
		Fields: []gen.AppFieldDescriptor{{Name: "Id", Type: gen.AppTypeDescriptor{Kind: "scalar", Name: "string"}}},
	}}
}

// Regression: a native reload that swapped the manifest but kept the record's
// old schema descriptors left a persisted record whose manifest hashes no
// longer matched the descriptors — the next host restart failed with
// `appmanager: re-register protocol descriptor: app schema hash mismatch`.
func TestNativeReloadStoresCandidateSchemaDescriptors(t *testing.T) {
	a, manifest := newNativeReloadActor(t)
	seeded := a.Records[manifest.ID]
	seeded.SchemaDescriptors = testObjectDescriptors("Old", 100)
	a.Records[manifest.ID] = seeded
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
				t.Fatalf("unexpected lifecycle call %s", callID)
				return nil, nil
			}
		}}
	}
	candidateManifest := manifest
	candidateManifest.Version = "2.0.0"
	if _, err := a.handleReload(ctx, gen.AppManagerReloadReq{
		ID: manifest.ID, PackageHash: "new-package", SchemaDescriptors: testObjectDescriptors("New", 101),
		CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi,
		CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact",
	}); err != nil {
		t.Fatal(err)
	}
	record := a.Records[manifest.ID]
	if record.Manifest.Version != "2.0.0" {
		t.Fatalf("record manifest version = %q, want 2.0.0", record.Manifest.Version)
	}
	if _, ok := record.SchemaDescriptors["New"]; !ok {
		t.Fatalf("candidate schema descriptors not stored: %+v", record.SchemaDescriptors)
	}
	if _, ok := record.SchemaDescriptors["Old"]; ok {
		t.Fatalf("stale schema descriptors kept alongside new manifest: %+v", record.SchemaDescriptors)
	}
}

// Regression (spore side of the same bug): a spore reload swapped the schema
// descriptors but kept the record's old manifest, whose schema hashes no
// longer matched — same restart failure as the native case above.
func TestSporeReloadStoresCandidateManifestAndDescriptors(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.sporedemo", Name: "SporeDemo", Namespace: "sporeapp.app.sporedemo", Version: "1.0.0", Runtime: "spore", ProtocolVersion: 1, Callables: []gen.AppCallableDescriptor{{ID: "main", RequestSchema: "Req", ResponseSchema: "Resp"}}}
	a := &Actor{
		actorID: "appmanager-test", store: persist.NewFSPersist(t.TempDir()), bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{manifest.ID: manifest},
		Records:  map[string]appRecord{manifest.ID: {Manifest: manifest, State: "running", SchemaDescriptors: testObjectDescriptors("Old", 100)}},
		children: map[string]string{manifest.ID: ""},
	}
	canonical, err := identity.NewCanonicalID(1700000000000, 2, 2, 42)
	if err != nil {
		t.Fatal(err)
	}
	child := id.From(canonical)
	a.children[manifest.ID] = canonical.String()
	childRef := testutil.NewFakeRef(child, nil)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) { return childRef, aid == child }
	newDescriptors := testObjectDescriptors("New", 101)
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			if callID != "sporeapp.reload" {
				t.Fatalf("unexpected lifecycle call %s", callID)
			}
			if req, ok := payload.(gen.SporeAppReloadReq); !ok || len(req.SchemaDescriptors) == 0 {
				t.Fatalf("sporeapp.reload did not receive schema descriptors: %+v", payload)
			}
			return gen.SporeAppReloadResp{ID: manifest.ID, Version: "2.0.0", StateVersion: 2}, nil
		}}
	}
	candidate := manifest
	candidate.Version = "2.0.0"
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{
		ID: manifest.ID, PackageHash: "new-package", SchemaDescriptors: newDescriptors, CandidateManifest: &candidate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status.Version != "2.0.0" {
		t.Fatalf("resp.Status.Version = %q, want 2.0.0", resp.Status.Version)
	}
	record := a.Records[manifest.ID]
	if record.Manifest.Version != "2.0.0" || a.Apps[manifest.ID].Version != "2.0.0" {
		t.Fatalf("candidate manifest not stored: record=%+v apps=%+v", record.Manifest, a.Apps[manifest.ID])
	}
	if _, ok := record.SchemaDescriptors["New"]; !ok {
		t.Fatalf("candidate schema descriptors not stored: %+v", record.SchemaDescriptors)
	}
	if _, ok := record.SchemaDescriptors["Old"]; ok {
		t.Fatalf("stale schema descriptors kept: %+v", record.SchemaDescriptors)
	}
}
