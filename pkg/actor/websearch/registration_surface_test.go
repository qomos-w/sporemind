package websearch

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies every websearch callable is
// registered by OnStart. ServiceName is derived from RegisterDomain
// ("websearch"), so no per-callable WithService declaration is needed.
func TestRegistrationSurface_Declarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"websearch.search",
		"websearch.provider_list",
		"websearch.account_list",
		"websearch.account_create",
		"websearch.account_update",
		"websearch.account_delete",
		"websearch.account_activate",
		"websearch.fetch",
		"websearch.download",
	}
	for _, id := range want {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
	}
}
