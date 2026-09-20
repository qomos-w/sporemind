package aiaggregator

import (
	"context"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// nilLifecycleCtx is a FakeCtx whose Lifecycle() reports nil. OnStart's
// background refreshers (startStatsRefresher / startAssignmentRefresher) bail
// out when lifecycleCtx is nil, so this keeps the registration test free of
// process-wide background goroutines that would otherwise outlive the test and
// race other tests over globals (e.g. the provider gate).
type nilLifecycleCtx struct{ *testutil.FakeCtx }

func (nilLifecycleCtx) Lifecycle() context.Context { return nil }

// TestOnStart_ConfigPollLane pins the config-poll lane routing: handlePoll →
// resolveConfig awaits a cross-actor aimanager.aggregator_resolve Invoke, so
// the self-rearming tick must run on the dedicated stateful "aiagg_poll" lane,
// not the owner lane (Owner Lane 禁阻塞). The lane must be declared before the
// callable is registered (WithLoop validates against the declared loops).
func TestOnStart_ConfigPollLane(t *testing.T) {
	ctx := nilLifecycleCtx{testutil.HumanCtx(testutil.GenActorID())}
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.RegisteredDomains = []string{}
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any { return nil })

	a := NewActor()().(*Actor)
	if err := a.OnStart(ctx); err != nil {
		t.Fatalf("OnStart: %v", err)
	}

	if got, ok := ctx.Loops["aiagg_poll"]; !ok || got != actor.ModeStateful {
		t.Fatalf("loop aiagg_poll = %v (present=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["aiaggregator.config_poll"]
	if !ok {
		t.Fatal("aiaggregator.config_poll not registered")
	}
	if got := actor.ResolveLoopOrDefault(actor.ModeStateful, opts...); got != "aiagg_poll" {
		t.Fatalf("config_poll loop = %q, want aiagg_poll", got)
	}
}
