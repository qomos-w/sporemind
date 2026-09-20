package appmanager

import (
	"reflect"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// depOrderPlanner captures the ordered sequence of pluginhost.artifact_load
// manifests so tests can assert dependency-order loading.
type depOrderPlanner struct {
	lifecyclePlanner
}

func newDepOrderEnv(t *testing.T) (actor.Context, *Actor, *[]string) {
	t.Helper()
	var mu sync.Mutex
	order := []string{}
	planner := lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			mu.Lock()
			order = append(order, req.Manifest.ID)
			mu.Unlock()
			return gen.PluginArtifactLoadResp{
				PluginID: req.Manifest.ID,
				Status:   gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	ctx.PlannerFn = func() actor.Planner { return planner }
	a := newNativeScaffoldE2EActor(t)
	return ctx, a, &order
}

func depTestAbi() *gen.PluginAbi {
	return &gen.PluginAbi{
		Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1",
		InvokeSymbol: "PluginInvoke", ContractVersion: "1",
		Isolation: "subprocess", TrustClass: "first_party", Signer: "sporemind.first-party",
	}
}

func putDepApp(a *Actor, appID string, state string, deps ...string) {
	manifest := gen.AppManifest{
		ID: appID, Name: appID, Version: "1.0.0", Runtime: "native", ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{{ID: "op", Service: appID}},
	}
	for _, d := range deps {
		manifest.Dependencies = append(manifest.Dependencies, gen.AppDependency{ID: d, Version: "1.0.0"})
	}
	a.Apps[appID] = manifest
	a.Records[appID] = appRecord{
		Manifest: manifest, State: state,
		ArtifactPath: "/test/build/" + appID, ArtifactHash: "h-" + appID, Abi: depTestAbi(),
	}
}

// TestPluginLoadPullsStoppedDependenciesInOrder: the pluginhost refuses
// loads whose dependencies are not loaded, so plugin_load of a dependent
// must first pull up registered-but-stopped dependencies transitively, in
// dependency order, while already-live apps are untouched.
func TestPluginLoadPullsStoppedDependenciesInOrder(t *testing.T) {
	ctx, a, orderPtr := newDepOrderEnv(t)
	putDepApp(a, "app.base", "stopped")
	putDepApp(a, "app.mid", "stopped", "app.base")
	putDepApp(a, "app.top", "stopped", "app.mid")
	putDepApp(a, "app.live", "running")

	resp, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.top"})
	if err != nil {
		t.Fatalf("plugin_load app.top: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("app.top state = %q, want running", resp.Status.State)
	}
	want := []string{"app.base", "app.mid", "app.top"}
	if !reflect.DeepEqual(*orderPtr, want) {
		t.Fatalf("artifact_load order = %v, want %v", *orderPtr, want)
	}
	for _, id := range []string{"app.base", "app.mid", "app.top"} {
		if state := a.Records[id].State; state != "running" {
			t.Fatalf("%s state = %q, want running", id, state)
		}
		if _, routed := a.children[id]; !routed {
			t.Fatalf("%s missing children routing sentinel", id)
		}
	}
}

// TestPluginLoadSkipsLiveDependencies: live dependencies are not reloaded;
// only the requested app's artifact is loaded.
func TestPluginLoadSkipsLiveDependencies(t *testing.T) {
	ctx, a, orderPtr := newDepOrderEnv(t)
	putDepApp(a, "app.base", "running")
	putDepApp(a, "app.top", "stopped", "app.base")

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.top"}); err != nil {
		t.Fatalf("plugin_load app.top: %v", err)
	}
	want := []string{"app.top"}
	if !reflect.DeepEqual(*orderPtr, want) {
		t.Fatalf("artifact_load order = %v, want %v", *orderPtr, want)
	}
}

// TestPluginLoadSporeDependencyIgnored: spore-runtime dependencies have no
// pluginhost artifact; they must not be pulled up and must not fail the load.
func TestPluginLoadSporeDependencyIgnored(t *testing.T) {
	ctx, a, orderPtr := newDepOrderEnv(t)
	manifest := gen.AppManifest{ID: "app.script", Name: "script", Runtime: "spore", ProtocolVersion: 1}
	a.Apps["app.script"] = manifest
	a.Records["app.script"] = appRecord{Manifest: manifest, State: "stopped"}
	putDepApp(a, "app.top", "stopped", "app.script")

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.top"}); err != nil {
		t.Fatalf("plugin_load app.top: %v", err)
	}
	want := []string{"app.top"}
	if !reflect.DeepEqual(*orderPtr, want) {
		t.Fatalf("artifact_load order = %v, want %v", *orderPtr, want)
	}
}

// TestPluginLoadUnregisteredDependencyLeftToPluginhost: an unregistered
// dependency is not an appmanager-side error; the pluginhost's
// missing-dependencies gate reports it.
func TestPluginLoadUnregisteredDependencyLeftToPluginhost(t *testing.T) {
	ctx, a, orderPtr := newDepOrderEnv(t)
	putDepApp(a, "app.top", "stopped", "app.ghost")

	if _, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.top"}); err != nil {
		t.Fatalf("plugin_load app.top: %v", err)
	}
	want := []string{"app.top"}
	if !reflect.DeepEqual(*orderPtr, want) {
		t.Fatalf("artifact_load order = %v, want %v", *orderPtr, want)
	}
}

// TestGetListExposeDependencyGraph: appmanager.get/list surface the declared
// dependency edges (Dependencies) and the reverse edges (Dependents).
func TestGetListExposeDependencyGraph(t *testing.T) {
	ctx, a, _ := newDepOrderEnv(t)
	putDepApp(a, "app.base", "running")
	putDepApp(a, "app.mid", "running", "app.base")
	putDepApp(a, "app.top", "running", "app.mid")

	get := func(id string) gen.AppStatus {
		resp, err := a.handleGet(ctx, gen.AppManagerGetReq{ID: id})
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		return resp.Status
	}

	if got := get("app.top"); len(got.Dependencies) != 1 || got.Dependencies[0].ID != "app.mid" {
		t.Fatalf("app.top Dependencies = %+v, want [app.mid]", got.Dependencies)
	} else if len(got.Dependents) != 0 {
		t.Fatalf("app.top Dependents = %v, want empty", got.Dependents)
	}
	if got := get("app.mid"); len(got.Dependents) != 1 || got.Dependents[0] != "app.top" {
		t.Fatalf("app.mid Dependents = %v, want [app.top]", got.Dependents)
	}
	if got := get("app.base"); !reflect.DeepEqual(got.Dependents, []string{"app.mid"}) {
		t.Fatalf("app.base Dependents = %v, want [app.mid]", got.Dependents)
	}

	listResp, err := a.handleList(ctx, gen.AppManagerListReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	byID := map[string]gen.AppStatus{}
	for _, item := range listResp.Items {
		byID[item.ID] = item
	}
	if item := byID["app.base"]; !reflect.DeepEqual(item.Dependents, []string{"app.mid"}) {
		t.Fatalf("list app.base Dependents = %v, want [app.mid]", item.Dependents)
	}
	if item := byID["app.top"]; len(item.Dependencies) != 1 || item.Dependencies[0].ID != "app.mid" {
		t.Fatalf("list app.top Dependencies = %+v, want [app.mid]", item.Dependencies)
	}
}
