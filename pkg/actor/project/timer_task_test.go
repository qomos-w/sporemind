package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── unified task fire branch ────────────────────────────────────────────────

// TestExecuteTimerCard_TaskPrompt_CreatesNewAgent pins the unified task
// contract: a schedule_type=task card without a bound template (prompt mode)
// fires by creating a fresh agent — no executor binding and no bound_agent are
// required, and the unbound card must not fail its fire. The run agent is
// recorded on the card (data.last_run_agent) for the list/badge display.
func TestExecuteTimerCard_TaskPrompt_CreatesNewAgent(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:task-run",
		"  schedule_type: task",
		"Do the daily task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{CardID: "scheduler:task-run", ProjectID: a.actorID}); err != nil {
		t.Fatalf("handleExecuteTimerCard: %v", err)
	}

	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d agents, want 1 (fresh agent per fire)", len(ws.spawned))
	}
	spawnedID := ws.spawnedIDs[0]

	ids := rec.ids()
	want := []string{"turn_cancel", "modes_unload_all", "scheduler_bind", "chat_submit"}
	if !strings.HasPrefix(joinIDs(ids), joinIDs(want)) {
		t.Fatalf("agent calls = %v, want prefix %v", ids, want)
	}

	saved, err := a.store.Get("scheduler:task-run")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusRunning {
		t.Fatalf("run_status = %q, want %q", got, timerStatusRunning)
	}
	if got := cardDataString(saved, "last_run_agent"); got != spawnedID {
		t.Fatalf("last_run_agent = %q, want %q", got, spawnedID)
	}
}

// TestExecuteTimerCard_TaskTemplate_RoutesWorkflow pins the template mode of
// the unified task type: a schedule_type=task card bound to a workflow
// template instantiates the template through the workflow branch (it does not
// submit the body via chat_submit). Mirrors the legacy workflow fire path.
func TestExecuteTimerCard_TaskTemplate_RoutesWorkflow(t *testing.T) {
	a := newTestActor(t.TempDir())
	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-task")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-task", TemplateID: "tpl::wf-task",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	schedRaw := "---\nid: scheduler:task-tpl\ntype: scheduler\ntags: []\n" +
		"data:\n  schedule_type: task\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::wf-task\"\n---\n\nEmpty prompt is fine for template mode."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:task-tpl", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 77)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	fctx := testutil.AdminCtx(testutil.GenActorID())
	// lastChatText captures every chat_submit payload so the test can prove the
	// workflow branch never submits the card's prompt body.
	var lastChatText string
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			var req gen.AgentWorkflowStartReq
			switch r := payload.(type) {
			case gen.AgentWorkflowStartReq:
				req = r
			case *gen.AgentWorkflowStartReq:
				req = *r
			}
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
			var req gen.AgentChatSubmitReq
			switch r := payload.(type) {
			case gen.AgentChatSubmitReq:
				req = r
			case *gen.AgentChatSubmitReq:
				req = *r
			}
			lastChatText = req.Text
			return gen.AgentChatSubmitResp{TurnActorID: "turn-1"}
		}
		return nil
	})
	wsRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workspace.list_agents":
			return gen.AgentRefListResp{Items: []gen.AgentRef{
				{ID: "coder", ActorID: executorActorID, LoadState: "loaded"},
			}}
		case "workspace.agent_loaded":
			return gen.WorkspaceAgentLoadedResp{}
		}
		return nil
	})
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return wsRef, true
		}
		return nil, false
	}
	fctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return agentRef, nil
	}
	fctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		// The local spawn returns agentRef, whose identity drives
		// workflow_start / chat_submit dispatch after the fire.
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}

	if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{CardID: "scheduler:task-tpl", ProjectID: a.actorID}); err != nil {
		t.Fatalf("handleExecuteTimerCard: %v", err)
	}

	saved, err := a.store.Get("scheduler:task-tpl")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusRunning {
		t.Fatalf("run_status = %q, want %q", got, timerStatusRunning)
	}

	// The template must have been instantiated: the scheduler card stamps the
	// created instance map as current_instance, and that instance exists and
	// is linked back to this scheduler card (scheduler_card_id), keeping the
	// agent-side execution chain intact.
	instID := currentInstance(saved)
	if instID == "" {
		t.Fatalf("no current_instance stamped on scheduler card")
	}
	inst, err := a.store.Get(instID)
	if err != nil {
		t.Fatalf("instantiated instance map %q not found: %v", instID, err)
	}
	if got := cardDataString(inst, "scheduler_card_id"); got != "scheduler:task-tpl" {
		t.Fatalf("instance scheduler_card_id = %q, want scheduler:task-tpl", got)
	}

	// The prompt body must NOT be submitted: chat_submit only ever carries the
	// orchestration kickoff text, never the card's prompt (which belongs to the
	// prompt-mode path).
	if lastChatText == "" {
		t.Fatal("expected a chat_submit orchestration kickoff after instantiation")
	}
	if strings.Contains(lastChatText, "Empty prompt is fine for template mode.") {
		t.Fatalf("card prompt must not be submitted via chat_submit, got: %q", lastChatText)
	}
}

// TestSpawnSchedulerAgent_PassesKindAndModelSlots pins change 3: the fire
// path forwards the card's agent kind (default coder) and the model-slot
// overrides to workspace.agent_spawn_scheduler, and the returned registry ref
// carries the slots so the local spawn applies them to the actor.
func TestSpawnSchedulerAgent_PassesKindAndModelSlots(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, nil, false)

	card := schedulerCard("scheduler:spawn-slots",
		"  schedule_type: task\n  agent_kind: dreamer\n"+
			"  model_slots: '{\"primary\":{\"Candidates\":[{\"kind\":\"unit\",\"Unit\":{\"model\":\"gpt-test\",\"provider\":\"openai-test\"}}]}}'",
		"Body.")
	ref_, err := a.spawnSchedulerAgent(ctx, card)
	if err != nil {
		t.Fatalf("spawnSchedulerAgent: %v", err)
	}

	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d, want 1", len(ws.spawned))
	}
	req := ws.spawned[0]
	if req.AgentKind != "dreamer" {
		t.Fatalf("req.AgentKind = %q, want dreamer", req.AgentKind)
	}
	if req.Primary == nil || len(req.Primary.Candidates) != 1 || req.Primary.Candidates[0].Kind != "unit" {
		t.Fatalf("req.Primary = %+v, want one unit candidate", req.Primary)
	}
	if u := req.Primary.Candidates[0].Unit; u == nil || u.Model != "gpt-test" || u.Provider != "openai-test" {
		t.Fatalf("req.Primary candidate unit = %+v, want gpt-test/openai-test", u)
	}

	// Default kind falls back to coder when the card leaves agent_kind unset.
	card2 := schedulerCard("scheduler:spawn-default", "  schedule_type: task", "Body.")
	if _, err = a.spawnSchedulerAgent(ctx, card2); err != nil {
		t.Fatalf("spawnSchedulerAgent default: %v", err)
	}
	if len(ws.spawned) != 2 {
		t.Fatalf("spawned %d, want 2", len(ws.spawned))
	}
	if got := ws.spawned[1].AgentKind; got != "coder" {
		t.Fatalf("default req.AgentKind = %q, want coder", got)
	}
	if ref_.Primary == nil || ref_.Primary.Candidates[0].Unit == nil || ref_.Primary.Candidates[0].Unit.Model != "gpt-test" {
		t.Fatalf("returned ref Primary = %+v, want the card override", ref_.Primary)
	}
}

// ── unified task monitor completion ─────────────────────────────────────────

// TestHandleListTimers_TaskFields pins change 6: the timer list carries the
// derived task mode plus the badge fields (AgentKind, BoundTemplate) for
// unified task cards, and leaves them empty for the agent_action contract.
func TestHandleListTimers_TaskFields(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())

	prompt := schedulerCard("scheduler:lp", "  schedule_type: task\n  agent_kind: dreamer", "Run.")
	if err := a.store.Save(prompt); err != nil {
		t.Fatalf("seed prompt-mode task: %v", err)
	}
	tpl := schedulerCard("scheduler:lt", "  schedule_type: task\n  workflow_template: tpl::x", "")
	if err := a.store.Save(tpl); err != nil {
		t.Fatalf("seed template-mode task: %v", err)
	}
	act := schedulerCard("scheduler:la", "  schedule_type: agent_action\n  agent_action: pause\n  target_agent: agent:coder", "Body.")
	if err := a.store.Save(act); err != nil {
		t.Fatalf("seed agent_action: %v", err)
	}

	schedRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == "scheduler.list" {
			return gen.SchedulerListResp{Entries: []gen.SchedulerEntryInfo{
				{CardID: "scheduler:lp", NextFireAt: "t1"},
				{CardID: "scheduler:lt", NextFireAt: "t2"},
				{CardID: "scheduler:la", NextFireAt: "t3"},
			}}
		}
		return nil
	})
	fctx := testutil.AdminCtx(testutil.GenActorID())
	fctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "scheduler" {
			return schedRef, true
		}
		return nil, false
	}

	resp, err := a.handleListTimers(fctx)
	if err != nil {
		t.Fatalf("handleListTimers: %v", err)
	}
	byID := map[string]gen.WikiTimerListItem{}
	for _, it := range resp.Timers {
		byID[it.ID] = it
	}

	p := byID["scheduler:lp"]
	if p.ScheduleType != scheduleTypeTask || p.TaskMode != taskModePrompt {
		t.Fatalf("prompt-mode task list item = %+v, want task/prompt", p)
	}
	if p.AgentKind != "dreamer" {
		t.Fatalf("prompt-mode task AgentKind = %q, want dreamer", p.AgentKind)
	}
	if p.BoundTemplate != "" {
		t.Fatalf("prompt-mode task BoundTemplate = %q, want empty", p.BoundTemplate)
	}

	tt := byID["scheduler:lt"]
	if tt.TaskMode != taskModeTemplate {
		t.Fatalf("template-mode task TaskMode = %q, want %q", tt.TaskMode, taskModeTemplate)
	}
	if tt.BoundTemplate != "tpl::x" {
		t.Fatalf("template-mode task BoundTemplate = %q, want tpl::x", tt.BoundTemplate)
	}

	la := byID["scheduler:la"]
	if la.TaskMode != "" || la.AgentKind != "" || la.BoundTemplate != "" {
		t.Fatalf("agent_action list item leaked task fields: %+v", la)
	}
}

// TestAdvanceExecutor_TaskPrompt_UnloadsRunAgent pins the monitor routing for
// prompt-mode task cards: completion mirrors the ephemeral agent_task path —
// the run agent is unloaded and data.last_run_agent is cleared once idle.
func TestAdvanceExecutor_TaskPrompt_UnloadsRunAgent(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:task-done", "  schedule_type: task", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": agentTaskEphemeralHex,
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card, _ = a.store.Get("scheduler:task-done")
	card.Raw = setCardDataStringInRaw(card.Raw, "last_run_agent", "Sched#1")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("set last_run_agent: %v", err)
	}
	// Re-fetch so the in-memory Data map reflects the persisted last_run_agent
	// (setCardDataStringInRaw only rewrites Raw); the monitor's completion path
	// reads last_run_agent from Data.
	card, _ = a.store.Get("scheduler:task-done")

	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	saved, err := a.store.Get("scheduler:task-done")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want %q", got, timerStatusIdle)
	}
	if got := cardDataString(saved, "last_run_agent"); got != "" {
		t.Fatalf("last_run_agent = %q, want empty", got)
	}
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != "Sched#1" {
		t.Fatalf("unloaded = %v, want [{Sched#1}]", ws.unloaded)
	}
}