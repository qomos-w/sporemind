package appmanager

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegisterProjectExpandsBundlePermissionsAtLoadBoundary pins the
// register/load boundary contract: the artifact_load request sent to the
// pluginhost carries bundle-level plugin.* permissions expanded to exact
// callIDs (what the host bridge gates on and what ArtifactLoads persists),
// while the appmanager record keeps the declared bundle form.
func TestRegisterProjectExpandsBundlePermissionsAtLoadBoundary(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 43)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	callerManifest := gen.AppManifest{
		ID: "app.caller", Name: "caller", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1,
		Namespace:   "app.caller",
		Permissions: []string{"fs.read", "plugin.app.translator.translate"},
		Dependencies: []gen.AppDependency{
			{ID: "app.translator", Version: "1.0.0"},
		},
		Callables: []gen.AppCallableDescriptor{
			{ID: "use_translate", RequestSchema: "UseReq", ResponseSchema: "UseResp", Service: "app.caller"},
		},
	}
	manifestJSON, err := json.Marshal(callerManifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	abi := gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1",
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: "subprocess", TrustClass: "first_party", Signer: "sporemind.first-party",
	}

	var mu sync.Mutex
	var capturedLoad gen.PluginArtifactLoadReq
	loadOrder := []string{}
	planner := lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "caller", Path: "/test/caller"}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			switch {
			case strings.HasSuffix(req.Path, "app.manifest.json"):
				return gen.FileSystemReadResp{Content: string(manifestJSON)}, nil
			case strings.HasSuffix(req.Path, "main.gen.go"):
				return gen.FileSystemReadResp{Content: "package main\n\nfunc main() {}\n"}, nil
			default:
				return gen.FileSystemReadResp{}, nil
			}
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			b64 := func(s string) gen.FileSystemReadBase64Resp {
				return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString([]byte(s))}
			}
			switch {
			case strings.HasSuffix(req.Path, "app.manifest.json"):
				return b64(string(manifestJSON)), nil
			case strings.HasSuffix(req.Path, "main.gen.go"):
				return b64("package main\n\nfunc main() {}\n"), nil
			default:
				return gen.FileSystemReadBase64Resp{}, nil
			}
		case "project.list":
			return "", nil
		case "pluginhost.native_build":
			return gen.NativeBuildResp{
				Result:       gen.NativeBuildResult{Success: true, ArtifactPath: "/test/build/plugin-app-caller", ArtifactHash: "ccdd00000001"},
				ManifestPath: "/test/caller/app.manifest.json",
				Abi:          abi,
			}, nil
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			mu.Lock()
			loadOrder = append(loadOrder, req.Manifest.ID)
			if req.Manifest.ID == "app.caller" {
				capturedLoad = req
			}
			mu.Unlock()
			return gen.PluginArtifactLoadResp{
				PluginID:     req.Manifest.ID,
				ArtifactHash: req.ArtifactHash,
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.artifact_unload":
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == id.From(projectCID)
	}
	ctx.PlannerFn = func() actor.Planner { return planner }

	a := newNativeScaffoldE2EActor(t)
	// Pre-register the dependency: app.translator with a Translate bundle,
	// STOPPED — registration must pull it up in dependency order before the
	// caller's own artifact load (the pluginhost refuses loads whose declared
	// dependencies are not loaded).
	a.Records["app.translator"] = appRecord{
		Manifest: gen.AppManifest{
			ID: "app.translator", Name: "translator", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1,
			Callables: []gen.AppCallableDescriptor{
				{ID: "translate_detect"}, {ID: "translate_run"}, {ID: "summarize"},
			},
			Bundles: []gen.AppBundle{{
				Title: "Translate",
				Tools: []gen.AppBundleTool{{CallableID: "translate_detect"}, {CallableID: "translate_run"}},
			}},
		},
		PackageHash: "dep-h1", State: "stopped",
		ArtifactPath: "/test/build/plugin-app-translator", ArtifactHash: "dep-hh", Abi: depTestAbi(),
	}
	a.Apps["app.translator"] = a.Records["app.translator"].Manifest

	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("state = %q, want running", regResp.Status.State)
	}

	// The load request manifest carries the expanded exact-callID set.
	mu.Lock()
	got := strings.Join(capturedLoad.Manifest.Permissions, ",")
	mu.Unlock()
	want := "fs.read,plugin.app.translator.translate_detect,plugin.app.translator.translate_run"
	if got != want {
		t.Fatalf("load request permissions = %q, want %q", got, want)
	}

	// The appmanager record keeps the declared bundle-level form.
	rec := a.Records["app.caller"]
	recPerms := strings.Join(rec.Manifest.Permissions, ",")
	if recPerms != "fs.read,plugin.app.translator.translate" {
		t.Fatalf("record permissions = %q, want declared bundle form", recPerms)
	}

	// Dependency-order assertion: the stopped dependency was pulled up
	// BEFORE the caller's own artifact load, and is running again.
	mu.Lock()
	gotOrder := append([]string(nil), loadOrder...)
	mu.Unlock()
	if wantOrder := []string{"app.translator", "app.caller"}; !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("artifact_load order = %v, want %v", gotOrder, wantOrder)
	}
	if state := a.Records["app.translator"].State; state != "running" {
		t.Fatalf("dependency state = %q, want running (pulled up)", state)
	}
}
