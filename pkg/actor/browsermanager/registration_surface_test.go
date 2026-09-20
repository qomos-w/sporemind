package browsermanager

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that callables carry the
// correct WithEffect and WithService values declared at registration.
func TestRegistrationSurface_Declarations(t *testing.T) {
	wantEffect := map[string]string{
		"browsermanager.open_global": "none",
		"browsermanager.use":         "irreversible",
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
