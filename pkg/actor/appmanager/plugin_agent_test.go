package appmanager

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakeWorkspaceCtx returns a FakeCtx whose workspace service dispatches via
// the provided call function (planner.Call semantics).
func fakeWorkspaceCtx(t *testing.T, call func(string, any) (any, error)) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return wsRef, true
	}
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: call}
	}
	return ctx
}

func TestPluginAgentBundleCardsOwnPlusExtras(t *testing.T) {
	manifest := gen.AppManifest{
		ID:      "app.demo",
		Bundles: []gen.AppBundle{{Title: "Search Tools"}},
	}
	binding := &gen.PluginAgentBinding{Bundles: []string{"builtin:bundle:web-search"}}
	got := pluginAgentBundleCards(manifest, binding)
	want := []string{"app-bundle:app.demo:search-tools", "builtin:bundle:web-search"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bundle cards = %v, want %v", got, want)
	}

	// Extras referencing the app's own card collapse (no duplicates).
	binding.Bundles = append(binding.Bundles, "app-bundle:app.demo:search-tools")
	if got := pluginAgentBundleCards(manifest, binding); !reflect.DeepEqual(got, want) {
		t.Fatalf("deduped bundle cards = %v, want %v", got, want)
	}
}

func TestReconcilePluginAgentEnsureAndRemove(t *testing.T) {
	manifest := gen.AppManifest{
		ID: "app.demo", Name: "Demo", Version: "1.0.0", Runtime: "native",
		Bundles:      []gen.AppBundle{{Title: "Search Tools"}},
		AgentBinding: &gen.AppAgentBinding{},
	}
	manifest.AgentBinding.PluginAgents = []gen.PluginAgentBinding{
		{
			Name:         "default",
			DisplayName:  "Demo Assistant",
			SystemPrompt: "Run demo.",
			Bundles:      []string{"builtin:bundle:web-search"},
			Model:        "minimax|MiniMax-M3",
		},
		{
			Name:        "reviewer",
			DisplayName: "Demo Reviewer",
		},
	}
	a := &Actor{Apps: map[string]gen.AppManifest{"app.demo": manifest}}

	var ensured []gen.WorkspaceCreateAppAgentReq
	var removed []string
	calls := func(callID string, payload any) (any, error) {
		switch callID {
		case "workspace.create_app_agent":
			req := payload.(gen.WorkspaceCreateAppAgentReq)
			ensured = append(ensured, req)
			return gen.WorkspaceEnsureAppAgentResp{AgentID: "demo-assistant#1", Created: true}, nil
		case "workspace.remove_app_agent":
			removed = append(removed, payload.(gen.WorkspaceRemoveAppAgentReq).AppID)
			return struct{}{}, nil
		}
		t.Fatalf("unexpected planner call %s", callID)
		return nil, nil
	}

	if err := a.reconcilePluginAgent(fakeWorkspaceCtx(t, calls), "app.demo"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(ensured) != 2 || len(removed) != 0 {
		t.Fatalf("create calls = %d, removes = %d", len(ensured), len(removed))
	}
	wantCards := []string{"app-bundle:app.demo:search-tools", "builtin:bundle:web-search"}
	if !reflect.DeepEqual(ensured[0].BundleCardIds, wantCards) {
		t.Fatalf("bundle cards = %v, want %v", ensured[0].BundleCardIds, wantCards)
	}
	if ensured[0].Slot != "default" || ensured[0].DisplayName != "Demo Assistant" || ensured[0].SystemPrompt != "Run demo." {
		t.Fatalf("create payload = %+v", ensured[0])
	}
	if ensured[0].Model != "minimax|MiniMax-M3" {
		t.Errorf("create payload Model = %q, want minimax|MiniMax-M3", ensured[0].Model)
	}
	if ensured[1].Slot != "reviewer" || ensured[1].DisplayName != "Demo Reviewer" {
		t.Fatalf("create payload = %+v", ensured[1])
	}
	if ensured[1].Model != "" {
		t.Errorf("create payload Model = %q, want empty (backward-compat)", ensured[1].Model)
	}

	// A reload that drops all plugin_agent blocks removes the leftover agents.
	without := manifest
	without.AgentBinding = &gen.AppAgentBinding{}
	a.Apps["app.demo"] = without
	if err := a.reconcilePluginAgent(fakeWorkspaceCtx(t, calls), "app.demo"); err != nil {
		t.Fatalf("reconcile after drop: %v", err)
	}
	if len(removed) != 1 || removed[0] != "app.demo" {
		t.Fatalf("removed = %v, want [app.demo]", removed)
	}
}

func TestReconcilePluginAgentKeepsRemainingSlots(t *testing.T) {
	// Dropping some (not all) slots reconciles the remaining ones without an
	// AppId-cascade removal; the stale slot agent lingers until unregister
	// (remove_app_agent is app-scoped by contract).
	manifest := gen.AppManifest{
		ID: "app.demo", Name: "Demo", Version: "1.0.0", Runtime: "native",
		AgentBinding: &gen.AppAgentBinding{},
	}
	manifest.AgentBinding.PluginAgents = []gen.PluginAgentBinding{
		{Name: "default", DisplayName: "Demo Assistant"},
		{Name: "reviewer", DisplayName: "Demo Reviewer"},
	}
	a := &Actor{Apps: map[string]gen.AppManifest{"app.demo": manifest}}

	var ensured []gen.WorkspaceCreateAppAgentReq
	var removed []string
	calls := func(callID string, payload any) (any, error) {
		switch callID {
		case "workspace.create_app_agent":
			ensured = append(ensured, payload.(gen.WorkspaceCreateAppAgentReq))
			return gen.WorkspaceEnsureAppAgentResp{AgentID: "demo-assistant#1", Created: true}, nil
		case "workspace.remove_app_agent":
			removed = append(removed, payload.(gen.WorkspaceRemoveAppAgentReq).AppID)
			return struct{}{}, nil
		}
		t.Fatalf("unexpected planner call %s", callID)
		return nil, nil
	}

	kept := manifest
	kept.AgentBinding.PluginAgents = kept.AgentBinding.PluginAgents[:1]
	a.Apps["app.demo"] = kept
	if err := a.reconcilePluginAgent(fakeWorkspaceCtx(t, calls), "app.demo"); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(ensured) != 1 || ensured[0].Slot != "default" {
		t.Fatalf("ensured = %+v", ensured)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want none (cascade would kill surviving slots)", removed)
	}
}

// TestPluginAgentSurfaceIDsInitializedOnStart pins the 2026-09-09 reload
// crash: reconcilePluginAgent (run on reload and OnStart restore) writes
// a.pluginAgentSurfaceIDs, which stayed nil because OnInit never initialized
// it — every plugin_agent-bearing app reload panicked with "assignment to
// entry in nil map". OnInit must leave the map writable and the
// bind/unbind round trip must not panic.
func TestPluginAgentSurfaceIDsInitializedOnStart(t *testing.T) {
	a := &Actor{
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
	}
	if err := a.OnInit(testutil.HumanCtx(testutil.GenActorID())); err != nil {
		t.Fatalf("OnInit: %v", err)
	}
	if a.pluginAgentSurfaceIDs == nil {
		t.Fatal("pluginAgentSurfaceIDs must be initialized by OnInit (nil-map panic on first reconcile)")
	}

	manifest := gen.AppManifest{
		ID:      "app.agent",
		Runtime: "native",
		Entrypoints: []gen.AppEntrypoint{
			{Kind: "view", ID: "main"},
		},
	}
	binding := &gen.PluginAgentBinding{Name: "slot1"}
	if err := a.bindPluginAgentSurface(manifest, binding, "agent-actor-1"); err != nil {
		t.Fatalf("bindPluginAgentSurface: %v", err)
	}
	key := pluginAgentSurfaceKey("app.agent", "slot1")
	a.withMu(func() {
		if a.pluginAgentSurfaceIDs[key] != "agent-actor-1" {
			t.Fatalf("surface id not tracked: %+v", a.pluginAgentSurfaceIDs)
		}
	})
	a.unbindPluginAgentSurface("app.agent")
	a.withMu(func() {
		if _, ok := a.pluginAgentSurfaceIDs[key]; ok {
			t.Fatal("surface id not cleared by unbind")
		}
	})
}
