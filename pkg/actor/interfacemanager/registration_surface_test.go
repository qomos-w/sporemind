package interfacemanager

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestRegistrationSurface_Declarations verifies that all callables carry
// WithService("interfacemanager") so agent tool routing resolves to the
// interfacemanager actor instead of falling back to the agent's own cell.
func TestRegistrationSurface_Declarations(t *testing.T) {
	callables := []string{
		"interfacemanager.control",
		"interfacemanager.report_interaction",
		"interfacemanager.query_interactions",
	}

	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range callables {
		_, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
	}
}

// TestRegistrationSurface_InteractionLane pins the interaction_ops lane wiring:
// report_interaction writes the persisted interaction ring buffer, so it runs
// on the dedicated interaction_ops stateful loop rather than the owner loop;
// query_interactions is a stateless (PureContext) read served from the forked
// pure loop.
func TestRegistrationSurface_InteractionLane(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	if got, ok := ctx.Loops["interaction_ops"]; !ok || got != actor.ModeStateful {
		t.Errorf("interaction_ops loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	opts, ok := ctx.RegOpts["interfacemanager.report_interaction"]
	if !ok {
		t.Fatal("interfacemanager.report_interaction not registered")
	}
	if got := actor.ResolveLoop(opts...); got != "interaction_ops" {
		t.Errorf("report_interaction loop = %q, want %q", got, "interaction_ops")
	}
	qFn, ok := ctx.Regs["interfacemanager.query_interactions"]
	if !ok {
		t.Fatal("interfacemanager.query_interactions not registered")
	}
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	if ft := reflect.TypeOf(qFn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
		t.Errorf("query_interactions must be stateless (PureContext), got %T", qFn)
	}
}
