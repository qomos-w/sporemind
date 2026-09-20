package project

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── shared test doubles ──────────────────────────────────────────────────────

// failSaveStore wraps a CardStore and fails every Save, to exercise the
// persist-before-dispatch guard and the failure unwinds.
type failSaveStore struct {
	CardStore
}

func (failSaveStore) Save(*CardRecord) error { return fmt.Errorf("save boom") }

// chatFailRef delegates to a recorder ref but returns a nil call for
// chat_submit, to exercise the submitTaskBody failure unwind.
type chatFailRef struct {
	inner ref.Ref
	mu    sync.Mutex
	calls []string
}

func (f *chatFailRef) ID() id.ActorID          { return f.inner.ID() }
func (f *chatFailRef) Service() (string, bool) { return f.inner.Service() }
func (f *chatFailRef) Invoke(ctx context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	f.mu.Lock()
	f.calls = append(f.calls, callID)
	f.mu.Unlock()
	if callID == "chat_submit" {
		return nil
	}
	return f.inner.Invoke(ctx, callID, payload, headers...)
}

func (f *chatFailRef) called(callID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == callID {
			return true
		}
	}
	return false
}

func hasInstanceCard(a *Actor) bool {
	cards, _ := a.store.List()
	for _, c := range cards {
		if strings.HasPrefix(c.Title, "inst::") {
			return true
		}
	}
	return false
}

// ── audit #4: workflow in-flight guard covers pending_review ─────────────────

func TestExecuteWorkflowTimerCard_SkipsPendingReview(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	card := schedulerCard("scheduler:wf-pr",
		"  schedule_type: workflow\n  workflow_template: tpl::a\n  executor: agent:coder\n  run_status: pending_review",
		"Body.")

	if err := a.executeWorkflowTimerCard(ctx, card, "tpl::a", "agent:coder", ""); err != nil {
		t.Fatalf("pending_review fire must be a no-op, got: %v", err)
	}
	if hasInstanceCard(a) {
		t.Fatal("pending_review fire instantiated a new workflow instance")
	}
}

// ── audit #8: bound-mode agent_task in-flight guard ──────────────────────────

func TestExecuteAgentTask_Bound_InFlightGuard(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded", LifecycleScope: "member"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	card := schedulerCard("scheduler:bound-guard",
		"  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder\n  run_status: running",
		"Guard task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}
	if len(ws.spawned) != 0 {
		t.Fatalf("spawned %d agents, want 0 (in-flight guard)", len(ws.spawned))
	}
	if count := rec.count("chat_submit"); count != 0 {
		t.Fatalf("chat_submit calls = %d, want 0 (in-flight guard)", count)
	}
	if count := rec.count("turn_cancel"); count != 0 {
		t.Fatalf("turn_cancel calls = %d, want 0 (in-flight guard)", count)
	}
}

// ── audit #10: running marker persisted before side effects ──────────────────

func TestStartSchedulerAgentRun_PersistsRunningBeforeDispatch(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	a.store = failSaveStore{CardStore: a.store}

	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:persist-first", "  schedule_type: agent_task\n  bind_mode: ephemeral", "Body.")
	args := agentTaskRunArgs{agentID: "Sched#1", agentRef: rec.ref(), card: card, lastRunID: "Sched#1"}

	err := a.startSchedulerAgentRun(ctx, args, true)
	if err == nil {
		t.Fatal("expected persist failure error")
	}
	if !strings.Contains(err.Error(), "persist running state") {
		t.Fatalf("error = %v, want persist running state", err)
	}
	if len(rec.ids()) != 0 {
		t.Fatalf("agent touched before running state persisted: %v", rec.ids())
	}
}

// ── audit #6 / wiring #6: submitTaskBody failure unwind ──────────────────────

func TestStartSchedulerAgentRun_SubmitFailureUnwinds(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	inner := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	fref := &chatFailRef{inner: inner.ref()}
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, nil, false)

	card := schedulerCard("scheduler:submit-fail", "  schedule_type: agent_task\n  bind_mode: ephemeral", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	args := agentTaskRunArgs{agentID: "Sched#9", agentRef: fref, card: card, lastRunID: "Sched#9"}
	err := a.startSchedulerAgentRun(ctx, args, true)
	if err == nil {
		t.Fatal("expected submit failure error")
	}
	if !strings.Contains(err.Error(), "submit task body") {
		t.Fatalf("error = %v, want submit task body", err)
	}
	// The unwind must unmount the scheduler mode + clear ActiveScheduler ...
	if !fref.called("scheduler_unbind") {
		t.Fatalf("unwind did not scheduler_unbind; calls = %v", fref.calls)
	}
	// ... and unload the (ephemeral) run agent ...
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != "Sched#9" {
		t.Fatalf("unwind unloaded = %v, want [{Sched#9}]", ws.unloaded)
	}
	// ... and converge run_status to failed so the in-flight guard releases.
	saved, err := a.store.Get("scheduler:submit-fail")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want %q", got, timerStatusFailed)
	}
}

// ── audit #5: ephemeral load failure unwinds the registry row ────────────────

func TestExecuteEphemeralAgentTask_LoadFailureUnwindsRegistryRow(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, nil, false)
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return nil, fmt.Errorf("spawn boom")
	}

	card := schedulerCard("scheduler:load-fail", "  schedule_type: agent_task\n  bind_mode: ephemeral", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	err := a.executeAgentTaskTimerCard(ctx, card)
	if err == nil {
		t.Fatal("expected load failure error")
	}
	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d rows, want 1", len(ws.spawned))
	}
	spawnedID := ws.spawnedIDs[0]
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != spawnedID {
		t.Fatalf("registry row not unwound: unloaded = %v, want [{%s}]", ws.unloaded, spawnedID)
	}
	saved, err := a.store.Get("scheduler:load-fail")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if cardDataString(saved, "run_status") == timerStatusRunning {
		t.Fatal("failed load left run_status=running")
	}
}

// ── audit #10: rebind save failure unwinds the fresh spawn ───────────────────

func TestExecuteBoundAgentTask_RebindSaveFailureUnwindsSpawn(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	a.store = failSaveStore{CardStore: a.store}

	rec := newAgentTaskRecorder(agentTaskRebindHex, "idle")
	ws := &agentTaskWorkspace{spawnActorID: agentTaskRebindHex}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskRebindHex: rec}, false)

	card := schedulerCard("scheduler:rebind-fail",
		"  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder",
		"Body.")

	err := a.executeAgentTaskTimerCard(ctx, card)
	if err == nil {
		t.Fatal("expected rebind save failure error")
	}
	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d, want 1", len(ws.spawned))
	}
	spawnedID := ws.spawnedIDs[0]
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != spawnedID {
		t.Fatalf("rebind spawn not unwound: unloaded = %v, want [{%s}]", ws.unloaded, spawnedID)
	}
}

// ── audit #11: action failure still bumps run_count/last_run ─────────────────

func TestExecuteAgentActions_PartialFailureBumpsRunCount(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	card := schedulerCard("scheduler:actions-fail", "  schedule_type: prompt", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	// Bad target: executeSingleAgentAction fails before any invoke.
	err := a.executeAgentActions(ctx, card, []agentAction{{Action: "pause", Target: "agent:missing"}})
	if err == nil {
		t.Fatal("expected action failure error")
	}

	saved, err := a.store.Get("scheduler:actions-fail")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataInt(saved, "run_count"); got != 1 {
		t.Fatalf("run_count = %d, want 1 (bumped despite action failure)", got)
	}
	if lastRunOf(saved) == "" {
		t.Fatal("last_run not stamped despite action failure")
	}
}

// ── wiring #7: setCardDataStringAndSave keeps Data in lockstep with Raw ──────

func TestSetCardDataStringAndSave_SyncsData(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	card := schedulerCard("scheduler:data-sync", "  schedule_type: prompt", "Body.")

	a.setCardDataStringAndSave(card, "bound_agent", "agent:x")
	if got := cardDataString(card, "bound_agent"); got != "agent:x" {
		t.Fatalf("in-memory Data not synced: %q", got)
	}
	saved, err := a.store.Get("scheduler:data-sync")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "bound_agent"); got != "agent:x" {
		t.Fatalf("persisted bound_agent = %q, want agent:x", got)
	}
}

// ── wiring #2: first spawn carries the kind-config default slots ─────────────

func TestSpawnSchedulerAgent_UsesRegistryRowSlots(t *testing.T) {
	t.Run("kind-config default from registry row", func(t *testing.T) {
		a, _ := freshProject(t, t.TempDir())
		def := &gen.ModelSlot{}
		ws := &agentTaskWorkspace{spawnSlots: gen.AgentRef{Primary: def}}
		ctx := agentTaskCtx(t, ws, nil, false)

		card := schedulerCard("scheduler:slots-default", "  schedule_type: task", "Body.")
		ag, err := a.spawnSchedulerAgent(ctx, card)
		if err != nil {
			t.Fatalf("spawnSchedulerAgent: %v", err)
		}
		if ag.Primary != def {
			t.Fatalf("returned Primary = %p, want registry row slot %p", ag.Primary, def)
		}
	})

	t.Run("explicit card override survives an empty row", func(t *testing.T) {
		a, _ := freshProject(t, t.TempDir())
		ws := &agentTaskWorkspace{}
		ctx := agentTaskCtx(t, ws, nil, false)

		card := schedulerCard("scheduler:slots-override",
			"  schedule_type: task\n"+
				"  model_slots: '{\"primary\":{\"Candidates\":[{\"kind\":\"unit\",\"Unit\":{\"model\":\"gpt-test\",\"provider\":\"openai-test\"}}]}}'",
			"Body.")
		ag, err := a.spawnSchedulerAgent(ctx, card)
		if err != nil {
			t.Fatalf("spawnSchedulerAgent: %v", err)
		}
		if ag.Primary == nil || len(ag.Primary.Candidates) != 1 || ag.Primary.Candidates[0].Unit == nil {
			t.Fatalf("returned Primary = %+v, want the card override", ag.Primary)
		}
		if got := ag.Primary.Candidates[0].Unit.Model; got != "gpt-test" {
			t.Fatalf("returned Primary model = %q, want gpt-test", got)
		}
	})
}

// ── audit #13: deleting a scheduler card cancels the in-flight run ───────────

func TestWikiDeleteCard_CancelsRunningRun(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "running")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:delete-run",
		"  schedule_type: agent_task\n  bind_mode: ephemeral\n"+
			"  run_status: running\n  last_run_agent: Sched#7\n"+
			"  executor_ref: \""+agentTaskEphemeralHex+"\"\n"+
			"  schedule:\n    cron: \"0 9 * * *\"",
		"Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "scheduler:delete-run"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// The ephemeral run agent is unloaded ...
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != "Sched#7" {
		t.Fatalf("unloaded = %v, want [{Sched#7}]", ws.unloaded)
	}
	// ... and the recorded executor's turn is cancelled.
	if count := rec.count("turn_cancel"); count != 1 {
		t.Fatalf("turn_cancel calls = %d, want 1", count)
	}
	if _, err := a.store.Get("scheduler:delete-run"); err == nil {
		t.Fatal("scheduler card still exists after delete")
	}
}
