package dbmanager

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// dbmanagerCallableIDs is the full set of callables registered by the actor.
// CRUD stays admin-gated and admin-visible; profile_resolve carries raw
// secrets and must remain Internal (unreachable from every frontend build,
// including admin ones).
var dbmanagerCallableIDs = []string{
	"dbmanager.profile_save",
	"dbmanager.profile_list",
	"dbmanager.profile_get",
	"dbmanager.profile_remove",
	"dbmanager.profile_resolve",
	"dbmanager.profile_lookup",
}

func TestRegistrationSurface_Declarations(t *testing.T) {
	t.Cleanup(func() { persist.SetCredentialResolver(nil) })
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range dbmanagerCallableIDs {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		want := actor.VisibilityAdmin
		if id == "dbmanager.profile_resolve" || id == "dbmanager.profile_lookup" {
			want = actor.VisibilityInternal
		}
		if got := actor.ResolveVisibility(opts...); got != want {
			t.Errorf("%s Visibility = %v, want %v", id, got, want)
		}
	}
}
