package appmanager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// conflictWireError mimics how the planner surfaces a pluginhandler handler
// error across the actor wire: wrapped text, no sentinel chain.
func conflictWireError(pluginID string) error {
	return fmt.Errorf("gospore.handler.error: pluginhost: artifact already loaded with a different artifact: plugin %q; unload first", pluginID)
}

// TestLoadArtifactWithReplace: a differing artifact under the same plugin ID
// must auto-unload and retry once instead of dead-ending the caller; a
// non-conflict error passes through untouched; a failed unload reports both
// errors; the idempotent path (no conflict) never unloads.
func TestLoadArtifactWithReplace(t *testing.T) {
	manifest := gen.AppManifest{ID: "app.admin-tools", Runtime: "native"}
	loadReq := gen.PluginArtifactLoadReq{Manifest: manifest}
	loadOK := gen.PluginArtifactLoadResp{PluginID: manifest.ID, ArtifactHash: "h2"}

	for _, tc := range []struct {
		name       string
		firstErr   error
		unloadErr  error
		secondErr  error
		wantErr    string
		wantUnload bool
		wantLoads  int
	}{
		{name: "conflict swaps", firstErr: conflictWireError(manifest.ID), wantUnload: true, wantLoads: 2},
		{name: "no conflict passes through", firstErr: fmt.Errorf("some other failure"), wantErr: "some other failure", wantLoads: 1},
		{name: "failed unload reports both", firstErr: conflictWireError(manifest.ID), unloadErr: fmt.Errorf("boom"), wantErr: "unload failed", wantLoads: 1},
		{name: "retry still failing surfaces", firstErr: conflictWireError(manifest.ID), secondErr: fmt.Errorf("second load failed"), wantErr: "after replacing", wantUnload: true, wantLoads: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &Actor{}
			ctx := testutil.HumanCtx(testutil.GenActorID())
			pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
			ctx.LookupServiceFn = func(name string) (r ref.Ref, found bool) { return pluginRef, name == pluginhostServiceName }
			loads := 0
			unloads := 0
			ctx.PlannerFn = func() actor.Planner {
				return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
					switch callID {
					case "pluginhost.artifact_load":
						loads++
						if loads == 1 {
							return gen.PluginArtifactLoadResp{}, tc.firstErr
						}
						if tc.secondErr != nil {
							return gen.PluginArtifactLoadResp{}, tc.secondErr
						}
						return loadOK, nil
					case "pluginhost.artifact_unload":
						unloads++
						if tc.unloadErr != nil {
							return gen.PluginArtifactUnloadResp{}, tc.unloadErr
						}
						return gen.PluginArtifactUnloadResp{Removed: 2}, nil
					default:
						t.Fatalf("unexpected planner call %s", callID)
						return nil, nil
					}
				}}
			}
			value, err := a.loadArtifactWithReplace(ctx, pluginRef, loadReq)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("loadArtifactWithReplace: %v", err)
			}
			if loads != tc.wantLoads {
				t.Fatalf("loads = %d, want %d", loads, tc.wantLoads)
			}
			if (unloads > 0) != tc.wantUnload {
				t.Fatalf("unloads = %d, wantUnload %v", unloads, tc.wantUnload)
			}
			resp, ok := value.(gen.PluginArtifactLoadResp)
			if !ok || resp.PluginID != manifest.ID {
				t.Fatalf("value = %#v, want PluginArtifactLoadResp for %s", value, manifest.ID)
			}
		})
	}
}

// TestRegisterOverSameIDDropsOldSchemaRegistration pins the admin-tools
// migration failure: register_project over an already-registered app hit
// "schema conflict: duplicate dynamic struct" because the replace path never
// unregistered the old namespace. Now doRegister drops the old registration
// first (mirroring the reload rollback order).
func TestRegisterOverSameIDDropsOldSchemaRegistration(t *testing.T) {
	a := restartPendingTestActor(t)
	a.protocol = newProtocolManager(t)

	buildReq := func(artifactHash string) gen.AppManagerRegisterReq {
		descriptors := map[string]gen.AppObjectDescriptor{"Task": {Kind: "struct", Name: "Task", SchemaID: 1900}}
		objects, err := protocol.AppObjectDescriptors(descriptors)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(objects["Task"])
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(encoded)
		manifest := nativeManifestForTest("app.replace-schemas")
		manifest.ProtocolVersion = 1
		manifest.Schemas = []gen.AppSchemaRef{{Name: "Task", Hash: hex.EncodeToString(sum[:])}}
		return gen.AppManagerRegisterReq{
			Manifest: manifest, EntryModule: "main.gen.go",
			Modules:           map[string]string{"main.gen.go": "package main"},
			SchemaDescriptors: descriptors,
			ArtifactPath:      "plugin.so", ArtifactHash: artifactHash, Abi: inprocessAbiForTest(),
		}
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	if _, err := a.doRegister(ctx, buildReq("h1"), true); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := a.doRegister(ctx, buildReq("h2"), true); err != nil {
		t.Fatalf("re-register over same ID must drop the old schema registration, got: %v", err)
	}
	if a.Records["app.replace-schemas"].ArtifactHash != "h2" {
		t.Fatalf("record must carry the replacement artifact")
	}
}
