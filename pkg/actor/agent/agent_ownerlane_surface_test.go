package agent

import (
	"reflect"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// The long-tool / cross-actor-Await callables migrated off the owner lane
// (card "agent 长工具 callable 移出 owner lane"). The map below pins the
// migration contract so a later edit cannot silently move a callable back
// onto the stateful owner lane:
//
//   - "pure"  → handler first param is actor.PureContext (stateless pool;
//     safe under turn-engine tool invocation, no owner-lane blocking)
//   - "exec"  → registered WithLoop("agent_exec"), serialized with the turn
//     engine's RawSession/steps/status writes (deadlock-free: none of these
//     is invoked by the engine via planner.Call(self,...))
//
// workflow_stop is intentionally absent: it is invoked by the engine as a
// tool (workflow-mode bundle) yet writes session state, so neither lane move
// is safe (agent_exec would self-deadlock; PureContext would race the engine).
var ownerLaneMigrationSurface = map[string]string{
	"frontend_debug":              "pure",
	"capture_profile":             "pure",
	"invoke_callable":             "pure",
	"image_generate":              "pure",
	"video_generate":              "pure",
	"open_global_browser":         "pure",
	"open_ssh_session":            "pure",
	"coordinator_wearable_notify": "pure",
	"coordinator_wearable_call":   "pure",

	"complete_message":     "exec",
	"compact":              "exec",
	"workflow_start":       "exec",
	"workflow_plan_submit": "exec",
	"skill_mount":          "exec",
}

// TestOwnerLaneMigrationSurface asserts every migrated callable is registered
// with the intended handler mode (PureContext) or loop (agent_exec), so the
// long-tool migration cannot regress unnoticed.
func TestOwnerLaneMigrationSurface(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Regs = map[string]any{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	ctx.Loops = map[string]actor.HandlerMode{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	// coordinator_wearable_* are only registered for coordinator-kind agents.
	coord := &Actor{agentKind: domain.AgentKindCoordinator}
	coordCtx := testutil.HumanCtx(testutil.GenActorID())
	coordCtx.Regs = map[string]any{}
	coordCtx.RegOpts = map[string][]actor.RegisterOption{}
	if err := coord.OnStart(coordCtx); err != nil {
		t.Fatal(err)
	}
	for _, callID := range []string{"coordinator_wearable_notify", "coordinator_wearable_call"} {
		ctx.RegOpts[callID] = coordCtx.RegOpts[callID]
		ctx.Regs[callID] = coordCtx.Regs[callID]
	}

	for callID, want := range ownerLaneMigrationSurface {
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
				t.Errorf("%s loop = %q, want owner lane", callID, got)
			}
			if fnType := reflect.TypeOf(handler); fnType.NumIn() < 1 ||
				fnType.In(0) != reflect.TypeOf((*actor.PureContext)(nil)).Elem() {
				t.Errorf("%s first param = %v, want actor.PureContext", callID, fnType.In(0))
			}
		case "exec":
			if got := actor.ResolveLoop(opts...); got != "agent_exec" {
				t.Errorf("%s loop = %q, want agent_exec", callID, got)
			}
		default:
			t.Fatalf("bad want value %q for %s", want, callID)
		}
	}
}
