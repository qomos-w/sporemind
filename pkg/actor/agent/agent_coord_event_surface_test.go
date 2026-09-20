package agent

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// The agent read / inbound-callback migration (card "w3-agent-reads-and-callbacks",
// violations #4/#7/#8/#9/#10 of callable-inventory-hot-stateful-table). The map
// below pins the migration contract so a later edit cannot silently move a
// callable back onto the stateful owner lane.
//
//   - "pure"  → handler first param is actor.PureContext (stateless pool; the
//     read no longer serializes behind the owner lane's business ingress)
//   - "coord" → registered WithLoop(coordEventLoop), a dedicated stateful lane
//     for state-writing inbound callbacks pushed at the agent by other actors
//
// agent_exec (the inline turn engine lane) and inspect_actor (documented
// existing exception) are intentionally out of scope and absent here.
var agentReadsAndCoordEventSurface = map[string]string{
	// Reads: 2s frontend poll / status-bar / on-demand introspection.
	"component_list":                  "pure",
	"session_stats":                   "pure",
	"list_callables":                  "pure",
	"coordinator_guidance_profile_query": "pure",

	// Inbound event callbacks: state writers, off the owner lane.
	"coordinator_ingest_glass_transcript": "coord",
	"coordinator_ingest_glass_event":      "coord",
	"coordinator_lifecycle_notify":        "coord",
	"internal_refresh_worktree_status":    "coord",
	"tools_refresh_notify":                "coord",
	"status_notify":                       "coord",
}

// TestAgentReadsAndCoordEventSurface asserts every migrated callable is
// registered with the intended handler mode (PureContext) or lane
// (coord_event), and that coord_event is declared as a stateful loop.
func TestAgentReadsAndCoordEventSurface(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Regs = map[string]any{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.Loops = map[string]actor.HandlerMode{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// The coordinator-only callables are registered only on coordinator-kind
	// agents; graft their registration records onto the base capture.
	coord := &Actor{agentKind: domain.AgentKindCoordinator}
	coordCtx := testutil.HumanCtx(testutil.GenActorID())
	coordCtx.Regs = map[string]any{}
	coordCtx.RegOpts = map[string][]actor.RegisterOption{}
	coordCtx.Loops = map[string]actor.HandlerMode{}
	if err := coord.OnStart(coordCtx); err != nil {
		t.Fatal(err)
	}
	for _, callID := range []string{
		"coordinator_guidance_profile_query",
		"coordinator_ingest_glass_transcript",
		"coordinator_ingest_glass_event",
		"coordinator_lifecycle_notify",
	} {
		ctx.RegOpts[callID] = coordCtx.RegOpts[callID]
		ctx.Regs[callID] = coordCtx.Regs[callID]
	}

	if got, ok := ctx.Loops[coordEventLoop]; !ok || got != actor.ModeStateful {
		t.Errorf("loop %q = %v (present=%v), want stateful", coordEventLoop, got, ok)
	}

	for callID, want := range agentReadsAndCoordEventSurface {
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
		case "pure":
			if got := actor.ResolveLoop(opts...); got != "" {
				t.Errorf("%s loop = %q, want owner/default lane", callID, got)
			}
			if fnType := reflect.TypeOf(handler); fnType.NumIn() < 1 ||
				fnType.In(0) != reflect.TypeOf((*actor.PureContext)(nil)).Elem() {
				t.Errorf("%s first param = %v, want actor.PureContext", callID, fnType.In(0))
			}
		case "coord":
			if got := actor.ResolveLoop(opts...); got != coordEventLoop {
				t.Errorf("%s loop = %q, want %q", callID, got, coordEventLoop)
			}
		default:
			t.Fatalf("bad want value %q for %s", want, callID)
		}
	}
}
