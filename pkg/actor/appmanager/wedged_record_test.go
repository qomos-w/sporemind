package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newWedgedNativeReloadActor builds a plugin whose children sentinel was
// lost while the record still claims a live state — the wedge that previously
// made reload reject with "not running" and unregister skip the pluginhost
// artifact unload.
func newWedgedNativeReloadActor(t *testing.T) (*Actor, gen.AppManifest) {
	t.Helper()
	manifest := gen.AppManifest{ID: "native.wedged", Name: "Wedged", Namespace: "native.wedged", Version: "1.0.0", Runtime: "native", Entrypoints: []gen.AppEntrypoint{{ID: "main", Kind: "view"}}}
	abi := &gen.PluginAbi{Name: "c-abi", Version: 1, Encoding: "binarycodec-v1", Isolation: "subprocess", TrustClass: "first_party", Signer: "first-party"}
	a, _ := newNativeReloadActor(t)
	a.Apps = map[string]gen.AppManifest{manifest.ID: manifest}
	a.Records = map[string]appRecord{manifest.ID: {Manifest: manifest, State: "active", PackageHash: "old-package", ArtifactPath: "old.dll", ArtifactHash: "old-artifact", Abi: abi}}
	a.children = map[string]string{} // wedge: sentinel lost
	return a, manifest
}

// TestNativeReloadSelfHealsMissingChildrenSentinel: a wedged native record
// (children entry lost, record still live) reloads through the pluginhost path
// instead of rejecting with "not running", and the sentinel is restored.
func TestNativeReloadSelfHealsMissingChildrenSentinel(t *testing.T) {
	a, manifest := newWedgedNativeReloadActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (r ref.Ref, found bool) { return pluginRef, name == pluginhostServiceName }
	var calls []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			calls = append(calls, callID)
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
	resp, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: manifest.ID, PackageHash: "new-package", CandidateManifest: &candidateManifest, CandidateAbi: a.Records[manifest.ID].Abi, CandidateArtifactPath: "candidate.dll", CandidateArtifactHash: "new-artifact"})
	if err != nil {
		t.Fatalf("wedged native reload must self-heal, got: %v", err)
	}
	if resp.Status.State != "running" || resp.Status.Version != "2.0.0" {
		t.Fatalf("status = %+v, want running/2.0.0", resp.Status)
	}
	if len(calls) != 2 || calls[0] != "pluginhost.artifact_reload_prepare" || calls[1] != "pluginhost.artifact_reload_commit" {
		t.Fatalf("calls = %v, want prepare+commit", calls)
	}
	if a.children[manifest.ID] != pluginhostServiceName {
		t.Fatalf("children sentinel not restored: %q", a.children[manifest.ID])
	}
}

// TestUnregisterWedgedNativeUnloadsArtifact: unregistering a wedged native
// record must still unload the pluginhost artifact — previously the missing
// children entry made unloadFrontHalf return nil and orphan the loaded
// artifact (subprocess + handlers) in pluginhost.
func TestUnregisterWedgedNativeUnloadsArtifact(t *testing.T) {
	a, manifest := newWedgedNativeReloadActor(t)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (r ref.Ref, found bool) { return pluginRef, name == pluginhostServiceName }
	var unloadIDs []string
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			if callID != "pluginhost.artifact_unload" {
				t.Fatalf("unexpected lifecycle call %s", callID)
			}
			unloadIDs = append(unloadIDs, payload.(gen.PluginArtifactUnloadReq).PluginID)
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		}}
	}
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: manifest.ID}); err != nil {
		t.Fatalf("unregister wedged native: %v", err)
	}
	if len(unloadIDs) != 1 || unloadIDs[0] != manifest.ID {
		t.Fatalf("artifact_unload calls = %v, want exactly [%s]", unloadIDs, manifest.ID)
	}
	if _, exists := a.Records[manifest.ID]; exists {
		t.Fatal("record survived unregister")
	}
	if _, exists := a.children[manifest.ID]; exists {
		t.Fatal("children entry survived unregister")
	}
}

// TestPluginUnloadOrphanedArtifact: with the appmanager record gone but the
// pluginhost artifact still loaded (the pre-self-heal unregister wedge),
// plugin_unload must clear the orphan instead of dead-ending with "not
// registered"; with nothing loaded it keeps the not-registered error.
func TestPluginUnloadOrphanedArtifact(t *testing.T) {
	for _, tc := range []struct {
		name    string
		removed int32
		wantErr string
	}{
		{name: "orphan cleared", removed: 3},
		{name: "nothing loaded", removed: 0, wantErr: "is not registered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newNativeReloadActor(t) // store only; records cleared below
			a.Apps = map[string]gen.AppManifest{}
			a.Records = map[string]appRecord{}
			a.children = map[string]string{}
			ctx := testutil.HumanCtx(testutil.GenActorID())
			pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
			ctx.LookupServiceFn = func(name string) (r ref.Ref, found bool) { return pluginRef, name == pluginhostServiceName }
			var unloadIDs []string
			ctx.PlannerFn = func() actor.Planner {
				return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
					if callID != "pluginhost.artifact_unload" {
						t.Fatalf("unexpected lifecycle call %s", callID)
					}
					unloadIDs = append(unloadIDs, payload.(gen.PluginArtifactUnloadReq).PluginID)
					return gen.PluginArtifactUnloadResp{Removed: tc.removed}, nil
				}}
			}
			resp, err := a.handlePluginUnload(ctx, gen.AppManagerPluginUnloadReq{ID: "app.admin-tools"})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("orphan unload: %v", err)
			}
			if len(unloadIDs) != 1 || unloadIDs[0] != "app.admin-tools" {
				t.Fatalf("artifact_unload calls = %v", unloadIDs)
			}
			if resp.Status.State != "stopped" || resp.Status.ID != "app.admin-tools" {
				t.Fatalf("status = %+v, want stopped/app.admin-tools", resp.Status)
			}
		})
	}
}
