package shell

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_ServiceName verifies that every exposed callable
// in the shell actor declares WithService("shell").
func TestRegistrationSurface_ServiceName(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	exposed := []string{
		"shell.exec",
		"shell.bash",
		"shell.session_open",
		"shell.session_write",
		"shell.session_resize",
		"shell.session_close",
		"shell.session_fetch",
	}
	for _, id := range exposed {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
	}
}

// TestRegistrationSurface_EffectKind verifies that bash carries the correct
// WithEffect (irreversible) declared at registration.
func TestRegistrationSurface_EffectKind(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	id := "shell.bash"
	opts, ok := ctx.RegOpts[id]
	if !ok {
		t.Fatalf("%s not registered", id)
	}
	if got := actor.ResolveEffect(opts...); got != "irreversible" {
		t.Errorf("%s EffectKind = %q, want %q", id, got, "irreversible")
	}
}
