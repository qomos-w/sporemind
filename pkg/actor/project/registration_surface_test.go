package project

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that callables carry the
// correct WithEffect and WithService values declared at registration.
func TestRegistrationSurface_Declarations(t *testing.T) {
	wantEffect := map[string]string{
		"project.list":           "none",
		"project.read":           "none",
		"project.read_base64":    "none",
		"project.read_chunk":     "none",
		"project.glob":           "none",
		"project.grep":           "none",
		"project.write":          "reversible",
		"project.edit":           "reversible",
		"project.rm":             "irreversible",
		"project.shell_exec":     "irreversible",
		"project.git_status":     "none",
		"project.git_log":        "none",
		"project.git_diff":       "none",
		"project.git_add":        "reversible",
		"project.git_commit":     "reversible",
		"project.git_push":       "irreversible",
		"project.git_pull":       "reversible",
		"project.git_branch":     "reversible",
		"project.git_checkout":   "irreversible",
		"project.component_list": "none",
		"project.component_get":  "none",
	}

	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
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
}

// TestRegistrationSurface_AllHandlersPureContext guards the PureContext
// signatures of every handler registered via ctx.Register: the framework
// dispatches handlers whose first parameter is actor.PureContext on forked
// goroutines, while actor.Context handlers serialize on the actor mailbox.
// A silent regression to actor.Context would reintroduce that serialization;
// this test fails and names the offending callable instead. Lifecycle hooks
// (OnInit/OnStart/OnStop/OnDestroy) do not go through ctx.Register, so they
// are naturally outside the captured set and need no whitelist entries.
func TestRegistrationSurface_AllHandlersPureContext(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// HumanCtx pre-initializes Regs; an empty set means the capture mechanism
	// broke and this guard would silently pass.
	if len(ctx.Regs) == 0 {
		t.Fatal("no callables captured in Regs; guard would be vacuous")
	}

	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	for id, h := range ctx.Regs {
		typ := reflect.TypeOf(h)
		if typ == nil || typ.Kind() != reflect.Func || typ.NumIn() < 1 {
			t.Errorf("%s: handler type is %v, want func with a context parameter", id, typ)
			continue
		}
		if got := typ.In(0); got != pure {
			t.Errorf("%s first parameter = %v, want actor.PureContext", id, got)
		}
	}
}
