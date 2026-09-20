package im

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that every exposed callable
// in the im actor derives ServiceName "im" from its callID's first segment
// matching the registered domain. The agent tool router falls back to the
// calling agent's own cell when ServiceName is empty, which surfaces as
// `gospore.cell: call ID "im.status" not registered`.
func TestRegistrationSurface_Declarations(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.RegisteredDomains = []string{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"im.account.list",
		"im.account.create",
		"im.account.update",
		"im.account.delete",
		"im.route.list",
		"im.route.set",
		"im.route.delete",
		"im.status",
		"im.send",
	}
	for _, id := range want {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ServiceNameFor(ctx.RegisteredDomains, id); got != "im" {
			t.Errorf("%s ServiceName = %q, want %q", id, got, "im")
		}
	}
}

// TestRegistrationSurface_AccountConfigAdminOnly verifies the account
// configuration and route callables stay admin-gated: bot tokens are sensitive
// configuration, and routing decides which agent sees a chat — neither may
// reach non-admin roles.
func TestRegistrationSurface_AccountConfigAdminOnly(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	adminOnly := []string{
		"im.account.list",
		"im.account.create",
		"im.account.update",
		"im.account.delete",
		"im.route.list",
		"im.route.set",
		"im.route.delete",
	}
	for _, id := range adminOnly {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Fatalf("%s not registered", id)
		}
		if got := actor.ResolveVisibility(opts...); got != actor.VisibilityAdmin {
			t.Errorf("%s Visibility = %v, want %v", id, got, actor.VisibilityAdmin)
		}
	}
}

// TestRegistrationSurface_ReplyPollLane pins the reply-poll lane routing: the
// self-rearming turn watcher (im.reply_poll_tick) performs cross-actor awaits
// and must run on the dedicated stateful "im_poll" lane, not the owner lane
// (Owner Lane 禁阻塞). The lane must be declared before the callable is
// registered (WithLoop validates against the declared loops).
func TestRegistrationSurface_ReplyPollLane(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops[imPollLoop]; !ok || got != actor.ModeStateful {
		t.Fatalf("loop %q = %v (present=%v), want ModeStateful", imPollLoop, got, ok)
	}
	opts, ok := ctx.RegOpts[callableReplyPollTick]
	if !ok {
		t.Fatalf("%s not registered", callableReplyPollTick)
	}
	if got := actor.ResolveLoopOrDefault(actor.ModeStateful, opts...); got != imPollLoop {
		t.Fatalf("%s loop = %q, want %q", callableReplyPollTick, got, imPollLoop)
	}
}
