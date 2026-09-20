package project

import (
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestAgentActionsOf_ParseBlockList(t *testing.T) {
	card := schedulerCard("scheduler:actions",
		"  schedule_type: task\n"+
			"  agent_actions:\n"+
			"    - action: pause\n"+
			"      target: agent:coder\n"+
			"    - action: resume\n"+
			"      target: agent:reviewer",
		"")
	got := agentActionsOf(card)
	if len(got) != 2 {
		t.Fatalf("len(actions) = %d, want 2; got %+v", len(got), got)
	}
	if got[0].Action != "pause" || got[0].Target != "agent:coder" {
		t.Fatalf("action[0] = %+v, want pause/agent:coder", got[0])
	}
	if got[1].Action != "resume" || got[1].Target != "agent:reviewer" {
		t.Fatalf("action[1] = %+v, want resume/agent:reviewer", got[1])
	}
}

func TestAgentActionsOf_ParseJSONString(t *testing.T) {
	card := schedulerCard("scheduler:actions",
		"  schedule_type: task\n"+
			"  agent_actions: '[{\"action\":\"pause\",\"target\":\"agent:coder\"}]'",
		"")
	got := agentActionsOf(card)
	if len(got) != 1 || got[0].Action != "pause" || got[0].Target != "agent:coder" {
		t.Fatalf("actions = %+v, want one pause/agent:coder", got)
	}
}

func TestAgentActionsOf_LegacySingleValue(t *testing.T) {
	// schedule_type=agent_action falls back to the old agent_action/target_agent pair.
	card := schedulerCard("scheduler:legacy",
		"  schedule_type: agent_action\n  agent_action: resume\n  target_agent: agent:reviewer",
		"")
	got := agentActionsOf(card)
	if len(got) != 1 || got[0].Action != "resume" || got[0].Target != "agent:reviewer" {
		t.Fatalf("actions = %+v, want one resume/agent:reviewer", got)
	}
}

func TestAgentActionsOf_TaskIgnoresLegacyFields(t *testing.T) {
	// A schedule_type=task card should only read the unified list, not the stale
	// single-value fields.
	card := schedulerCard("scheduler:task-mixed",
		"  schedule_type: task\n"+
			"  agent_action: resume\n"+
			"  target_agent: agent:reviewer\n"+
			"  agent_actions:\n"+
			"    - action: pause\n"+
			"      target: agent:coder",
		"")
	got := agentActionsOf(card)
	if len(got) != 1 || got[0].Action != "pause" || got[0].Target != "agent:coder" {
		t.Fatalf("actions = %+v, want one pause/agent:coder (legacy fields ignored)", got)
	}
}

func TestAgentActionsOf_InvalidActionDropped(t *testing.T) {
	card := schedulerCard("scheduler:bad",
		"  schedule_type: task\n"+
			"  agent_actions:\n"+
			"    - action: kill\n"+
			"      target: agent:coder",
		"")
	if got := agentActionsOf(card); len(got) != 0 {
		t.Fatalf("actions = %+v, want empty for invalid action", got)
	}
}

func TestHandleExecuteTimerCard_ActionsOnlyTask(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	wsRef := &agentActionWorkspaceRef{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentActionTestAgentID, LoadState: "loaded"},
		},
	}
	ctx := agentActionCtx(wsRef)

	card := schedulerCard("scheduler:actions-only",
		"  schedule_type: task\n"+
			"  agent_actions:\n"+
			"    - action: pause\n"+
			"      target: agent:coder\n"+
			"    - action: resume\n"+
			"      target: agent:coder",
		"")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{CardID: "scheduler:actions-only"}); err != nil {
		t.Fatalf("handleExecuteTimerCard: %v", err)
	}

	pause, resume, _, _ := wsRef.calls()
	if pause != 1 || resume != 1 {
		t.Fatalf("pause/resume calls = %d/%d, want 1/1", pause, resume)
	}

	saved, err := a.store.Get("scheduler:actions-only")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want idle", got)
	}
	if got := cardDataInt(saved, "run_count"); got != 1 {
		t.Fatalf("run_count = %d, want 1", got)
	}
	if got := cardDataString(saved, "last_run"); got == "" {
		t.Fatal("last_run not set")
	}
}

func TestHandleExecuteTimerCard_ActionsOnlyTask_BodySkipped(t *testing.T) {
	// The prompt-mode task path would normally require a non-empty body and
	// would try to spawn/submit to an ephemeral agent. With only agent_actions
	// present, the empty body must be ignored and no executor agent is needed.
	tmp := t.TempDir()
	a := newTestActor(tmp)
	wsRef := &agentActionWorkspaceRef{}
	ctx := agentActionCtx(wsRef)

	card := schedulerCard("scheduler:skip-body",
		"  schedule_type: task\n"+
			"  agent_actions:\n"+
			"    - action: pause\n"+
			"      target: "+agentActionTestAgentID,
		"")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{CardID: "scheduler:skip-body"}); err != nil {
		t.Fatalf("handleExecuteTimerCard: %v", err)
	}

	pause, _, _, _ := wsRef.calls()
	if pause != 1 {
		t.Fatalf("pause calls = %d, want 1", pause)
	}
}

func TestSchedulerValidator_TaskActionsOnly_NoBody(t *testing.T) {
	raw := "---\nid: scheduler:valid-actions-only\ntype: scheduler\ndata:\n" +
		"  schedule_type: task\n" +
		"  agent_actions:\n" +
		"    - action: pause\n" +
		"      target: agent:coder\n---\n\n"
	errs := validateCardStructured("scheduler:valid-actions-only", raw)
	if len(errs) > 0 {
		t.Fatalf("validation errors = %+v, want none", errs)
	}
}

func TestSchedulerValidator_TaskNoBodyNoActions_Rejected(t *testing.T) {
	raw := "---\nid: scheduler:invalid-task\ntype: scheduler\ndata:\n" +
		"  schedule_type: task\n---\n\n"
	errs := validateCardStructured("scheduler:invalid-task", raw)
	found := false
	for _, e := range errs {
		if e.Code == "scheduler_task_body_empty" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected scheduler_task_body_empty error, got %+v", errs)
	}
}

func TestTimerMonitor_ActionsOnlyTaskConvergesToIdle(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	card := schedulerCard("scheduler:stale-running",
		"  schedule_type: task\n"+
			"  agent_actions:\n"+
			"    - action: pause\n"+
			"      target: agent:coder\n"+
			"  run_status: running\n"+
			"  run_count: 3",
		"")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	card, _ = a.store.Get("scheduler:stale-running")

	ctx := agentActionCtx(&agentActionWorkspaceRef{})
	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	saved, err := a.store.Get("scheduler:stale-running")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want idle", got)
	}
}

// agentActionCtxFakePureCtx adapts agentActionCtx to the actor.PureContext
// used by handleExecuteTimerCard. The helper already returns actor.PureContext
// compatible *testutil.FakeCtx, so this compile-only guard is kept for clarity.
var _ actor.PureContext = agentActionCtx(nil)
