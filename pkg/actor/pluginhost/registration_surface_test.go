package pluginhost

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that native_build carries
// the correct WithEffect and WithService values declared at registration.
func TestRegistrationSurface_Declarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	id := "pluginhost.native_build"
	opts, ok := ctx.RegOpts[id]
	if !ok {
		t.Fatalf("%s not registered", id)
	}
	if got := actor.ResolveEffect(opts...); got != "irreversible" {
		t.Errorf("%s EffectKind = %q, want %q", id, got, "irreversible")
	}
}

// TestRegistrationSurface_NativeBuildLoop pins the plugin_build lane wiring:
// native_build (subprocess go build, minutes) must run on the dedicated
// "plugin_build" stateful loop so the owner lane keeps answering query
// callables while a compile is in flight.
func TestRegistrationSurface_NativeBuildLoop(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["plugin_build"]; !ok || got != actor.ModeStateful {
		t.Errorf("plugin_build loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["pluginhost.native_build"]
	if !ok {
		t.Fatal("pluginhost.native_build not registered")
	}
	if got := actor.ResolveLoop(opts...); got != "plugin_build" {
		t.Errorf("native_build loop = %q, want %q", got, "plugin_build")
	}
	// Query/control callables must NOT be pinned to the build lane: list_plugins
	// is a stateless (PureContext) snapshot read; invoke keeps the owner lane so
	// plugin routing state stays single-writer.
	for _, id := range []string{"pluginhost.list_plugins", "pluginhost.invoke"} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; query callables must stay on the owner lane", id, got)
		}
	}
}

// TestRegistrationSurface_AppStateLoop pins the app_state lane wiring:
// state.* handlers talk to a backend-pluggable store (network backends via
// the persist registry), so their IO must stay off the owner lane. The lane
// is stateful so per-app quota checks keep single-writer exactness.
func TestRegistrationSurface_AppStateLoop(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["app_state"]; !ok || got != actor.ModeStateful {
		t.Errorf("app_state loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	for _, id := range []string{
		"pluginhost.state_get", "pluginhost.state_set", "pluginhost.state_delete",
		"pluginhost.state_list", "pluginhost.state_append",
		"pluginhost.state_get_many", "pluginhost.state_set_many",
		"pluginhost.state_purge",
	} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "app_state" {
			t.Errorf("%s loop = %q, want %q", id, got, "app_state")
		}
	}
}

// TestRegistrationSurface_AppDataScanLoop pins the appdata_scan lane wiring:
// appdata_usage walks grant directories (file I/O that can take seconds on
// large trees), so it must stay off both the owner lane and the app_state
// lane.
func TestRegistrationSurface_AppDataScanLoop(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["appdata_scan"]; !ok || got != actor.ModeStateful {
		t.Errorf("appdata_scan loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["pluginhost.appdata_usage"]
	if !ok {
		t.Fatal("pluginhost.appdata_usage not registered")
	}
	if got := actor.ResolveLoop(opts...); got != "appdata_scan" {
		t.Errorf("pluginhost.appdata_usage loop = %q, want %q", got, "appdata_scan")
	}
}
