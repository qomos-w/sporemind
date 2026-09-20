package glassinteract

import (
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/auth"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestTickHandlersRoutedToDedicatedLanes pins the registration contract that
// keeps the self-scheduling glass tick handlers off the owner lane: the two
// stateful lanes are declared, the three tick callables are routed onto them,
// and the owner-lane control handlers declare no loop. Without this the poll
// chain and the Coordinator updater would re-occupy the owner lane.
func TestTickHandlersRoutedToDedicatedLanes(t *testing.T) {
	a := &Actor{
		store: persist.NewFSPersist(t.TempDir()),
		key:   "test-key",
		jwt:   auth.NewManager(auth.JWTConfig{Secret: []byte("test-secret"), Issuer: "test", ExpiryTime: time.Hour}),
		now:   func() time.Time { return testTime() },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// HumanCtx does not pre-seed RegOpts; OnStart must record the options for
	// the assertions below.
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got := ctx.Loops[loopGlassPoll]; got != actor.ModeStateful {
		t.Errorf("loop %s mode = %v, want ModeStateful", loopGlassPoll, got)
	}
	if got := ctx.Loops[loopGlassUpdater]; got != actor.ModeStateful {
		t.Errorf("loop %s mode = %v, want ModeStateful", loopGlassUpdater, got)
	}

	pinned := map[string]string{
		callableAgentPollTick:  loopGlassPoll,
		callableAgentPollApply: loopGlassPoll,
		callableUpdaterTick:    loopGlassUpdater,
	}
	for callID, want := range pinned {
		opts, ok := ctx.RegOpts[callID]
		if !ok {
			t.Errorf("%s not registered", callID)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != want {
			t.Errorf("%s loop = %q, want %q", callID, got, want)
		}
		if got := actor.ResolveLoopOrDefault(actor.ModeStateful, opts...); got != want {
			t.Errorf("%s resolved loop = %q, want %q", callID, got, want)
		}
		// These are stateful (owner-shaped) handlers; routing them must not
		// change their mode.
		h := reflect.TypeOf(ctx.Regs[callID])
		if h == nil || h.NumIn() < 1 || h.In(0) != reflect.TypeOf((*actor.Context)(nil)).Elem() {
			t.Errorf("%s first param = %v, want actor.Context (stateful)", callID, h)
		}
	}

	// Control / read handlers stay on the owner lane (no loop pin).
	for _, callID := range []string{
		callableBootstrap,
		callableClaim,
		callableGetState,
		callableCheck,
		callableIdleRenderTick,
		callableEventEnqueue,
		callableEventComplete,
		callableInboxState,
	} {
		opts, ok := ctx.RegOpts[callID]
		if !ok {
			t.Errorf("%s not registered", callID)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; control/read handlers must stay on the owner lane", callID, got)
		}
	}
}
