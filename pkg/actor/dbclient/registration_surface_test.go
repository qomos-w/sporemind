package dbclient

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// dbclientCallableIDs is the full set of callables registered by the actor.
// Every surface is admin-gated; the operator-side concern is consistency
// with the dbmanager registration style (no Internal leak — dbclient
// profiles are loaded through dbmanager.profile_lookup, the only
// peer-actor surface it depends on).
var dbclientCallableIDs = []string{
	"dbclient.dial_test",
	"dbclient.tree",
	"dbclient.read",
	"dbclient.query",
	"dbclient.describe",
	"dbclient.close",
}

// expectedLanes lists which callable pins the dedicated exec lane vs the
// owner lane. The query/read/tree callables do network IO and must serialize
// on dbclient_exec; dial_test's getConn is also network IO but the design
// card placed it on the owner lane — we follow that exactly (it forces a
// connection through the same pool dedup path, but the ping is short).
var expectedLanes = map[string]string{
	"dbclient.tree":     execLoop,
	"dbclient.read":     execLoop,
	"dbclient.query":    execLoop,
	"dbclient.describe": execLoop,
}

func TestRegistrationSurface_Declarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range dbclientCallableIDs {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveVisibility(opts...); got != actor.VisibilityAdmin {
			t.Errorf("%s Visibility = %v, want %v", id, got, actor.VisibilityAdmin)
		}
		wantLoop := expectedLanes[id]
		if got := actor.ResolveLoop(opts...); got != wantLoop {
			t.Errorf("%s loop = %q, want %q", id, got, wantLoop)
		}
	}

	if got, ok := ctx.Loops[execLoop]; !ok || got != actor.ModeStateful {
		t.Errorf("exec loop %q missing or wrong mode: %v", execLoop, got)
	}
}
