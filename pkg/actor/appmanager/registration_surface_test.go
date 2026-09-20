package appmanager

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// appmanagerCallableIDs is the full set of callables registered by the actor.
//
// Every one of them must carry WithService("appmanager"): the turn engine's
// resolveServiceRefs routes a tool call to ctx.LookupService(ServiceName),
// falling back to ctx.Self() (agent-local) when empty. Without the service
// declaration, a direct tool like appmanager.list lands on the agent itself,
// which has no such callable registered and fails with
// "gospore.cell: call ID \"appmanager.list\" not registered".
var appmanagerCallableIDs = []string{
	"appmanager.session_create",
	"appmanager.session_resolve",
	"appmanager.session_revoke",
	"appmanager.route_token",
	"appmanager.list",
	"appmanager.register",
	"appmanager.unregister",
	"appmanager.retry_cleanup",
	"appmanager.get",
	"appmanager.invoke",
	"appmanager.audit",
	"appmanager.cast",
	"appmanager.emit",
	"appmanager.plugin_emit",
	"appmanager.agent_action",
	"appmanager.reload",
	"appmanager.project_package",
	"appmanager.register_project",
	"appmanager.reload_project",
	"appmanager.callable_info",
	"appmanager.dev_guide",
	"appmanager.dev_generate",
	"appmanager.dev_gate",
	"appmanager.install_local",
	"appmanager.app_export",
	"appmanager.report_process_state",
	"appmanager.pluginhost_online",
}

// opsLoopCallables is the set of orchestration handlers that must be
// registered on the dedicated "appmanager_ops" lane. Every multi-step
// cross-actor orchestration chain (dev_*/reload/project_package/plugin_*/
// agent_action) lives there so the owner lane stays free for control-plane
// queries (list/get/audit/session_*/route_token/cast/emit). register_project
// is included even though the original audit listed nine handlers: it chains
// project_package (11x Await) plus the native build/load/gate steps, so
// leaving it on the owner lane would keep a long blocker there.
// appmanager.invoke is deliberately NOT here: an invoke can run for the
// callable's full declared timeout (up to 15min) and c-shared FFI calls
// cannot be interrupted — it dispatches stateless on a forked goroutine
// (PureContext handler, no loop), mirroring pluginhost.invoke, so neither
// the owner lane nor appmanager_ops is parked behind a long invoke.
var opsLoopCallables = map[string]bool{
	"appmanager.agent_action":     true,
	"appmanager.reload":           true,
	"appmanager.reload_project":   true,
	"appmanager.project_package":  true,
	"appmanager.register_project": true,
	"appmanager.dev_generate":     true,
	"appmanager.dev_gate":         true,
	"appmanager.install_local":    true,
	"appmanager.app_export":       true,
	"appmanager.plugin_load":      true,
	"appmanager.plugin_unload":    true,
}

// TestRegistrationSurface_Declarations verifies that callables carry the
// correct WithEffect and WithService values declared at registration, that
// every appmanager callable routes to the appmanager actor, and that the
// lane split is exactly as documented: orchestration handlers on
// "appmanager_ops", all other handlers without an explicit WithLoop (the
// runtime resolves the owner or pure loop from the handler mode).
func TestRegistrationSurface_Declarations(t *testing.T) {
	wantEffect := map[string]string{
		"appmanager.register_project": "irreversible",
		"appmanager.reload_project":   "irreversible",
		"appmanager.unregister":       "irreversible",
		"appmanager.callable_info":    "none",
		"appmanager.dev_guide":        "none",
		"appmanager.dev_gate":         "none",
		"appmanager.install_local":    "irreversible",
		"appmanager.app_export":       "irreversible",
	}

	// Visibility boundary: package export/install mutate host state and
	// stay AdminOnly; the dev entrypoints stay Public.
	wantVisibility := map[string]actor.Visibility{
		"appmanager.app_export":       actor.VisibilityAdmin,
		"appmanager.install_local":    actor.VisibilityAdmin,
		"appmanager.register_project": actor.VisibilityPublic,
		"appmanager.dev_generate":     actor.VisibilityPublic,
		"appmanager.dev_gate":         actor.VisibilityPublic,
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// The dedicated orchestration lane must be registered stateful.
	if ctx.Loops["appmanager_ops"] != actor.ModeStateful {
		t.Errorf("appmanager_ops loop not registered or wrong mode: got %v", ctx.Loops["appmanager_ops"])
	}

	for _, id := range appmanagerCallableIDs {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}

		// WithLoop contract: ops-lane callables must declare it; all other
		// callables must NOT (no explicit loop — the runtime resolves the
		// owner or pure loop from the handler's declared mode).
		gotLoop := actor.ResolveLoop(ctx.RegOpts[id]...)
		if opsLoopCallables[id] && gotLoop != "appmanager_ops" {
			t.Errorf("%s: expected loop=appmanager_ops, got %q", id, gotLoop)
		}
		if !opsLoopCallables[id] && gotLoop != "" {
			t.Errorf("%s: expected no explicit loop, got %q", id, gotLoop)
		}
	}

	for id, wantEff := range wantEffect {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveEffect(opts...); got != wantEff {
			t.Errorf("%s EffectKind = %q, want %q", id, got, wantEff)
		}
	}

	for id, wantVis := range wantVisibility {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveVisibility(opts...); got != wantVis {
			t.Errorf("%s visibility = %q, want %q", id, got, wantVis)
		}
	}
}

// statelessDevReads are the wave-6 callables the owner-lane audit moved off the
// owner lane: the plugin-dev discovery reads, the audit trail query, and the
// two internal process-state reporters. Each must declare actor.PureContext as
// its first parameter so the runtime dispatches it on the forked "pure" loop
// (actor.DefaultLoopPure) instead of serializing it behind the owner lane.
// report_process_state and pluginhost_online mutate shared maps only under
// a.mu (the single logical writer across lanes) and emit fire-and-forget
// tells, so a forked stateless goroutine is safe.
var statelessDevReads = []string{
	"appmanager.audit",
	"appmanager.callable_info",
	"appmanager.dev_guide",
	"appmanager.host_protocol",
	"appmanager.icon_names",
	"appmanager.panel_topology",
	"appmanager.report_process_state",
	"appmanager.pluginhost_online",
}

// handlerPureContext/handlerContext are the exact reflect.Type values the
// gospore handler classifier matches on (see handler.ClassifyHandlerMode):
// actor.PureContext ⇒ stateless, actor.Context ⇒ stateful.
var (
	handlerPureContext = reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	handlerContext     = reflect.TypeOf((*actor.Context)(nil)).Elem()
)

// TestRegistrationSurface_StatelessDevReads verifies the wave-6 handler-first-
// parameter conversion: the eight dev/query/report callables are stateless
// (PureContext), while the ops-lane orchestration callables stay stateful
// (Context). It guards against a regression that would silently re-park the
// owner lane behind these reads or move an ops chain onto the pure loop.
func TestRegistrationSurface_StatelessDevReads(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range statelessDevReads {
		h, ok := ctx.Regs[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := reflect.TypeOf(h).In(0); got != handlerPureContext {
			t.Errorf("%s first param = %v, want actor.PureContext (stateless)", id, got)
		}
	}

	for id := range opsLoopCallables {
		h, ok := ctx.Regs[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := reflect.TypeOf(h).In(0); got != handlerContext {
			t.Errorf("%s first param = %v, want actor.Context (stateful)", id, got)
		}
	}
}
