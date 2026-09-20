package oracle

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_DiagLane pins the diag_ops lane wiring:
// report_diagnostic appends to the persisted diagnostic ring buffer and writes
// it to disk, so it runs on the dedicated diag_ops stateful loop rather than
// the owner loop; list_diagnostics stays a stateless (PureContext) read served
// from the forked pure loop.
func TestRegistrationSurface_DiagLane(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["diag_ops"]; !ok || got != actor.ModeStateful {
		t.Errorf("diag_ops loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["oracle.report_diagnostic"]
	if !ok {
		t.Fatal("oracle.report_diagnostic not registered")
	}
	if got := actor.ResolveLoop(opts...); got != "diag_ops" {
		t.Errorf("report_diagnostic loop = %q, want %q", got, "diag_ops")
	}
	listFn, ok := ctx.Regs["oracle.list_diagnostics"]
	if !ok {
		t.Fatal("oracle.list_diagnostics not registered")
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	if ft := reflect.TypeOf(listFn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
		t.Errorf("oracle.list_diagnostics must be stateless (PureContext), got %T", listFn)
	}
}
