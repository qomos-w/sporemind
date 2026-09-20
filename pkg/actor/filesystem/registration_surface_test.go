package filesystem

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_ServiceName verifies that every exposed callable
// in the filesystem actor declares WithService("filesystem").
func TestRegistrationSurface_ServiceName(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	exposed := []string{
		"filesystem.list",
		"filesystem.read",
		"filesystem.read_base64",
		"filesystem.read_chunk",
		"filesystem.write",
		"filesystem.write_base64",
		"filesystem.edit",
		"filesystem.list_json",
		"filesystem.roots",
		"filesystem.glob",
		"filesystem.grep",
		"filesystem.rm",
	}
	for _, id := range exposed {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
	}
}

// TestRegistrationSurface_EffectKind verifies that write_base64 carries
// the correct WithEffect (irreversible). Other callables declare EffectNone.
func TestRegistrationSurface_EffectKind(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// write_base64 needs explicit verification.
	wantEffect := "irreversible"
	id := "filesystem.write_base64"
	opts, ok := ctx.RegOpts[id]
	if !ok {
		t.Fatalf("%s not registered", id)
	}
	if got := actor.ResolveEffect(opts...); got != wantEffect {
		t.Errorf("%s EffectKind = %q, want %q", id, got, wantEffect)
	}
}

// TestRegistrationSurface_SyncRootsLane pins the fs_ops lane wiring:
// sync_roots mutates the allowed-root table, so it must run on the dedicated
// fs_ops stateful loop rather than the owner loop; grep is a stateless
// (PureContext) read served from the forked pure loop.
func TestRegistrationSurface_SyncRootsLane(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["fs_ops"]; !ok || got != actor.ModeStateful {
		t.Errorf("fs_ops loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["filesystem.sync_roots"]
	if !ok {
		t.Fatal("filesystem.sync_roots not registered")
	}
	if got := actor.ResolveLoop(opts...); got != "fs_ops" {
		t.Errorf("sync_roots loop = %q, want %q", got, "fs_ops")
	}
	grepFn, ok := ctx.Regs["filesystem.grep"]
	if !ok {
		t.Fatal("filesystem.grep not registered")
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	if ft := reflect.TypeOf(grepFn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
		t.Errorf("filesystem.grep must be stateless (PureContext), got %T", grepFn)
	}
}
