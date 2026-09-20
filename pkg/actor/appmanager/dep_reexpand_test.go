package appmanager

import (
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// depReloadPlannerEnv captures pluginhost traffic (unload order, load
// manifests) while answering artifact_load/unload for any plugin ID.
type depReloadPlannerEnv struct {
	mu       sync.Mutex
	unloads  []string
	loads    []gen.PluginArtifactLoadReq
	loadResp func(pluginID string) gen.PluginArtifactLoadResp
}

func newDepReloadEnv(t *testing.T, loadResp func(pluginID string) gen.PluginArtifactLoadResp) (actor.Context, *Actor, *depReloadPlannerEnv) {
	t.Helper()
	env := &depReloadPlannerEnv{loadResp: loadResp}
	planner := lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			env.mu.Lock()
			env.loads = append(env.loads, req)
			env.mu.Unlock()
			return env.loadResp(req.Manifest.ID), nil
		case "pluginhost.artifact_unload":
			req := payload.(gen.PluginArtifactUnloadReq)
			env.mu.Lock()
			env.unloads = append(env.unloads, req.PluginID)
			env.mu.Unlock()
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
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
	return ctx, newNativeScaffoldE2EActor(t), env
}

func activeLoadResp(pluginID string) gen.PluginArtifactLoadResp {
	return gen.PluginArtifactLoadResp{
		PluginID: pluginID,
		Status:   gen.AppStatus{ID: pluginID, Runtime: "native", State: "active"},
	}
}

// putDepReloadApps installs a running dependency (app.translator with a
// Translate bundle) and a running dependent that holds the bundle-level
// permission plugin.app.translator.translate.
func putDepReloadApps(a *Actor, dependentIsolation string) {
	depManifest := gen.AppManifest{
		ID: "app.translator", Name: "translator", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1,
		Callables: []gen.AppCallableDescriptor{{ID: "translate_run"}},
		Bundles:   []gen.AppBundle{{Title: "Translate", Tools: []gen.AppBundleTool{{CallableID: "translate_run"}}}},
	}
	a.Apps["app.translator"] = depManifest
	a.Records["app.translator"] = appRecord{Manifest: depManifest, State: "running"}

	callerManifest := gen.AppManifest{
		ID: "app.caller", Name: "caller", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1,
		Dependencies: []gen.AppDependency{{ID: "app.translator", Version: "1.0.0"}},
		Callables:    []gen.AppCallableDescriptor{{ID: "use_translate", Service: "app.caller"}},
		Permissions:  []string{"plugin.app.translator.translate"},
	}
	abi := depTestAbi()
	abi.Isolation = dependentIsolation
	a.Apps["app.caller"] = callerManifest
	a.Records["app.caller"] = appRecord{
		Manifest: callerManifest, State: "running", Generation: 1,
		ArtifactPath: "/test/build/plugin-app-caller", ArtifactHash: "h-caller", Abi: abi,
	}
	a.children["app.caller"] = pluginhostServiceName
}

// TestReexpandDependentsSubprocessReloadsFreshExpansion: after the
// dependency's manifest gains a bundle callable, the subprocess dependent's
// artifact is unloaded and re-loaded with the re-expanded permission set.
func TestReexpandDependentsSubprocessReloadsFreshExpansion(t *testing.T) {
	ctx, a, env := newDepReloadEnv(t, activeLoadResp)
	putDepReloadApps(a, "subprocess")
	// The dependency reloaded: its Translate bundle now also exposes
	// translate_new.
	dep := a.Apps["app.translator"]
	dep.Callables = append(dep.Callables, gen.AppCallableDescriptor{ID: "translate_new"})
	dep.Bundles[0].Tools = append(dep.Bundles[0].Tools, gen.AppBundleTool{CallableID: "translate_new"})
	a.Apps["app.translator"] = dep

	a.reexpandDependents(ctx, "app.translator")

	env.mu.Lock()
	unloads, loads := append([]string(nil), env.unloads...), append([]gen.PluginArtifactLoadReq(nil), env.loads...)
	env.mu.Unlock()
	if len(unloads) != 1 || unloads[0] != "app.caller" {
		t.Fatalf("unloads = %v, want [app.caller]", unloads)
	}
	if len(loads) != 1 || loads[0].Manifest.ID != "app.caller" {
		t.Fatalf("loads = %+v, want one app.caller load", loads)
	}
	got := strings.Join(loads[0].Manifest.Permissions, ",")
	want := "plugin.app.translator.translate_run,plugin.app.translator.translate_new"
	if got != want {
		t.Fatalf("re-expanded permissions = %q, want %q", got, want)
	}
	rec := a.Records["app.caller"]
	if rec.State != "running" {
		t.Fatalf("dependent state = %q, want running", rec.State)
	}
	if rec.Generation != 2 {
		t.Fatalf("dependent generation = %d, want 2 (bumped for the respawned instance)", rec.Generation)
	}
}

// TestReexpandDependentsInProcessDefersToRestart: an in-process dependent
// cannot be re-opened while mapped; the refresh must land in restart_pending
// with the deferred load (fresh auth) persisted by the pluginhost.
func TestReexpandDependentsInProcessDefersToRestart(t *testing.T) {
	ctx, a, env := newDepReloadEnv(t, func(pluginID string) gen.PluginArtifactLoadResp {
		return gen.PluginArtifactLoadResp{
			PluginID: pluginID,
			Status:   gen.AppStatus{ID: pluginID, Runtime: "native", State: "restart_pending"},
		}
	})
	putDepReloadApps(a, "inprocess")

	a.reexpandDependents(ctx, "app.translator")

	rec := a.Records["app.caller"]
	if rec.State != stateRestartPending {
		t.Fatalf("dependent state = %q, want restart_pending", rec.State)
	}
	if !strings.Contains(rec.Error, "activates on host restart") {
		t.Fatalf("dependent error = %q, want restart explanation", rec.Error)
	}
	if rec.Generation != 2 {
		t.Fatalf("dependent generation = %d, want 2", rec.Generation)
	}
	env.mu.Lock()
	loads := len(env.loads)
	env.mu.Unlock()
	if loads != 1 {
		t.Fatalf("artifact_load calls = %d, want 1 (deferred re-auth)", loads)
	}
}

// TestReexpandDependentsExpansionFailureRecordsError: when the bundle's
// dependency can no longer be resolved (unregistered by the reload), the
// dependent keeps its old authorization and records why instead of failing
// the reload.
func TestReexpandDependentsExpansionFailureRecordsError(t *testing.T) {
	ctx, a, env := newDepReloadEnv(t, activeLoadResp)
	putDepReloadApps(a, "subprocess")
	// The dependency was unregistered by the reload cycle; the bundle
	// reference can no longer resolve.
	delete(a.Apps, "app.translator")
	delete(a.Records, "app.translator")

	a.reexpandDependents(ctx, "app.translator")

	rec := a.Records["app.caller"]
	if rec.State != "running" {
		t.Fatalf("dependent state = %q, want running (kept old auth)", rec.State)
	}
	if !strings.Contains(rec.Error, "re-expansion failed") {
		t.Fatalf("dependent error = %q, want re-expansion failure note", rec.Error)
	}
	env.mu.Lock()
	traffic := len(env.unloads) + len(env.loads)
	env.mu.Unlock()
	if traffic != 0 {
		t.Fatalf("pluginhost traffic = %d calls, want 0 (no re-auth on expansion failure)", traffic)
	}
}

// TestReexpandDependentsCallableLevelUnaffected: a dependent without
// bundle-level permissions cannot have a stale set; no pluginhost traffic.
func TestReexpandDependentsCallableLevelUnaffected(t *testing.T) {
	ctx, a, env := newDepReloadEnv(t, activeLoadResp)
	putDepReloadApps(a, "subprocess")
	caller := a.Apps["app.caller"]
	caller.Permissions = []string{"plugin.app.translator.translate_run"}
	a.Apps["app.caller"] = caller

	a.reexpandDependents(ctx, "app.translator")

	env.mu.Lock()
	traffic := len(env.unloads) + len(env.loads)
	env.mu.Unlock()
	if traffic != 0 {
		t.Fatalf("pluginhost traffic = %d calls, want 0", traffic)
	}
	if rec := a.Records["app.caller"]; rec.Generation != 1 {
		t.Fatalf("dependent generation = %d, want unchanged 1", rec.Generation)
	}
}
