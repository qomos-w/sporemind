package runtime

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// runtimeObservationSurface pins the registration contract of the root
// actor's observation callables so a later edit cannot silently move a hot
// callable back onto the stateful owner lane (spec #1):
//
//   - "graph_sync" → unified_graph.sync fires once per topology epoch
//     (frontend observationStore driven) and rebuilds the full graph. It stays
//     stateful (Sync mutates epoch history) but is routed to the dedicated
//     "graph_sync" lane so a rebuild never parks the owner lane.
//   - "pure"       → read-only handlers take actor.PureContext (stateless
//     pool), so history / inspector / service listing never touch the owner
//     lane.
var runtimeObservationSurface = map[string]string{
	"unified_graph.sync":    "graph_sync",
	"unified_graph.history": "pure",
	"inspect.document":      "pure",
	"runtime.list_services": "pure",
	"runtime.build_info":    "pure",
}

// TestRuntimeObservationLaneSurface asserts the lane split documented above.
func TestRuntimeObservationLaneSurface(t *testing.T) {
	r := &rootActor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := r.registerCallables(ctx); err != nil {
		t.Fatal(err)
	}

	// The dedicated rebuild lane must be registered stateful.
	if got := ctx.Loops["graph_sync"]; got != actor.ModeStateful {
		t.Errorf("graph_sync loop mode = %v, want ModeStateful", got)
	}

	for callID, want := range runtimeObservationSurface {
		opts, ok := ctx.RegOpts[callID]
		if !ok {
			t.Errorf("%s not registered", callID)
			continue
		}
		handler, ok := ctx.Regs[callID]
		if !ok {
			t.Errorf("%s handler not recorded", callID)
			continue
		}
		switch want {
		case "graph_sync":
			if got := actor.ResolveLoop(opts...); got != "graph_sync" {
				t.Errorf("%s loop = %q, want graph_sync", callID, got)
			}
		case "pure":
			if got := actor.ResolveLoop(opts...); got != "" {
				t.Errorf("%s loop = %q, want owner lane (no WithLoop)", callID, got)
			}
			fnType := reflect.TypeOf(handler)
			if fnType.NumIn() < 1 ||
				fnType.In(0) != reflect.TypeOf((*actor.PureContext)(nil)).Elem() {
				t.Errorf("%s first param = %v, want actor.PureContext", callID, fnType.In(0))
			}
		default:
			t.Fatalf("bad want value %q for %s", want, callID)
		}
	}
}

// TestRuntimeBuildInfoHandler pins the build identity mapping: the callable
// must surface the ldflags-injected buildinfo values verbatim so an agent can
// detect a stale process in one call.
func TestRuntimeBuildInfoHandler(t *testing.T) {
	r := &rootActor{}
	resp, err := r.handleBuildInfo(nil, domain.RuntimeBuildInfoReq{})
	if err != nil {
		t.Fatalf("handleBuildInfo: %v", err)
	}
	want := buildinfo.Get()
	if resp.Version != want.Version {
		t.Errorf("Version = %q, want %q", resp.Version, want.Version)
	}
	if resp.BuildType != want.BuildType {
		t.Errorf("BuildType = %q, want %q", resp.BuildType, want.BuildType)
	}
	if resp.Dirty != want.Dirty {
		t.Errorf("Dirty = %v, want %v", resp.Dirty, want.Dirty)
	}
}
