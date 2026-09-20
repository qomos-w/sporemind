package project

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// convAgentID returns a valid canonical ActorID string for use as an executor /
// reviewer reference in convergence tests (seq keeps them distinct).
func convAgentID(t *testing.T, seq uint64) string {
	t.Helper()
	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, seq)
	if err != nil {
		t.Fatalf("NewCanonicalID: %v", err)
	}
	return cid.String()
}

// statusRef returns a fake agent ref whose agent_status reports the given state.
func statusRef(actorID string, state string) ref.Ref {
	cid, _ := identity.ParseCanonicalID(actorID)
	return testutil.NewFakeRef(id.From(cid), func(callID string, _ any) any {
		if callID == "agent_status" {
			return gen.AgentStatusResp{State: state}
		}
		return nil
	})
}

// monitorCtx wires a FakeCtx whose LookupID resolves the given agent refs by
// canonical ActorID string.
func monitorCtx(agents map[string]ref.Ref) *testutil.FakeCtx {
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		r, ok := agents[aid.String()]
		return r, ok
	}
	return ctx
}

// seedSchedulerCard persists a scheduler card and marks it running with the
// given executor/reviewer refs.
func seedSchedulerCard(t *testing.T, a *Actor, id, dataBlock, body, executor, reviewer string) *CardRecord {
	t.Helper()
	card := schedulerCard(id, dataBlock, body)
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": executor,
		"reviewer_ref": reviewer,
	}); err != nil {
		t.Fatalf("set running %s: %v", id, err)
	}
	card, err := a.store.Get(id)
	if err != nil {
		t.Fatalf("reload %s: %v", id, err)
	}
	return card
}

func resultCardStatuses(a *Actor, cardID string) []string {
	all, _ := a.store.List()
	var out []string
	for _, c := range all {
		if strings.HasPrefix(c.Title, cardID+" #") {
			out = append(out, c.Body)
		}
	}
	return out
}

func hasResultBody(a *Actor, cardID, want string) bool {
	for _, body := range resultCardStatuses(a, cardID) {
		if strings.Contains(body, want) {
			return true
		}
	}
	return false
}

// ── #1 template-mode task completes on instance status ───────────────────────

// TestAdvanceExecutor_TemplateTask_InstanceTerminal pins the routing fix: a
// unified task card bound to a workflow template (schedule_type=task +
// workflow_template) completes on the instance map's status, not on the
// executor agent's idle state — with an empty executor_ref it must still
// converge instead of erroring every tick.
func TestAdvanceExecutor_TemplateTask_InstanceTerminal(t *testing.T) {
	cases := []struct {
		name       string
		instStatus string
		wantRun    string
		wantBody   string
	}{
		{name: "done completes", instStatus: "done", wantRun: timerStatusIdle, wantBody: "- Status: completed"},
		{name: "failed converges", instStatus: "failed", wantRun: timerStatusFailed, wantBody: "- Status: failed"},
		{name: "cancelled converges", instStatus: "cancelled", wantRun: timerStatusFailed, wantBody: "- Status: cancelled"},
		{name: "blocked converges", instStatus: "blocked", wantRun: timerStatusFailed, wantBody: "- Status: blocked"},
		{name: "running keeps polling", instStatus: "doing", wantRun: timerStatusRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := freshProject(t, t.TempDir())
			ctx := testutil.AdminCtx(testutil.GenActorID())

			instID := "inst::tpl-" + tc.instStatus
			if err := a.store.Save(&CardRecord{Title: instID,
				Raw: "---\nid: " + instID + "\ntype: workflow\nstatus: " + tc.instStatus + "\n---\n\nInstance."}); err != nil {
				t.Fatalf("seed instance: %v", err)
			}
			card := seedSchedulerCard(t, a, "scheduler:tpl-task",
				"  schedule_type: task\n  workflow_template: tpl::x\n  current_instance: "+instID,
				"", "", "")

			if err := a.advanceExecutor(ctx, card); err != nil {
				t.Fatalf("advanceExecutor: %v", err)
			}
			updated, err := a.store.Get("scheduler:tpl-task")
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if got := timerState(updated); got != tc.wantRun {
				t.Fatalf("run_status = %q, want %q", got, tc.wantRun)
			}
			if tc.wantBody != "" && !hasResultBody(a, "scheduler:tpl-task", tc.wantBody) {
				t.Fatalf("missing result card %q; got %v", tc.wantBody, resultCardStatuses(a, "scheduler:tpl-task"))
			}
			if tc.wantRun == timerStatusRunning && len(resultCardStatuses(a, "scheduler:tpl-task")) != 0 {
				t.Fatalf("running instance must not emit a result card")
			}
		})
	}
}

// ── #1 empty executor converges failed ───────────────────────────────────────

func TestAdvanceExecutor_EmptyExecutor_ConvergesFailed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Legacy prompt card left running with no executor recorded.
	card := seedSchedulerCard(t, a, "scheduler:no-exec", "  schedule_type: prompt", "Body.", "", "")

	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor must converge, not error: %v", err)
	}
	updated, _ := a.store.Get("scheduler:no-exec")
	if got := timerState(updated); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want failed", got)
	}
	if got := frontmatterValue(updated.Raw, "status"); got != "failed" {
		t.Fatalf("frontmatter status = %q, want failed (markCardFailed semantics)", got)
	}
	if !hasResultBody(a, "scheduler:no-exec", "- Status: failed") {
		t.Fatal("expected a failed result card")
	}
}

// ── #3 / wiring #1 agent gone or failed converges ────────────────────────────

func TestAdvanceExecutor_AgentNotFound_ConvergesFailed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	// LookupIDFn resolves nothing: the executor actor is gone.
	ctx := testutil.AdminCtx(testutil.GenActorID())
	missing := convAgentID(t, 901)
	card := seedSchedulerCard(t, a, "scheduler:gone-exec", "  schedule_type: prompt", "Body.", missing, "")

	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor must converge, not error: %v", err)
	}
	updated, _ := a.store.Get("scheduler:gone-exec")
	if got := timerState(updated); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want failed", got)
	}
	if !hasResultBody(a, "scheduler:gone-exec", "- Status: failed") {
		t.Fatal("expected a failed result card")
	}
}

func TestAdvanceExecutor_AgentFailed_ConvergesFailed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	execID := convAgentID(t, 902)
	ctx := monitorCtx(map[string]ref.Ref{execID: statusRef(execID, "failed")})
	card := seedSchedulerCard(t, a, "scheduler:failed-exec", "  schedule_type: prompt", "Body.", execID, "")

	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}
	updated, _ := a.store.Get("scheduler:failed-exec")
	if got := timerState(updated); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want failed", got)
	}
}

// TestAdvanceExecutor_AgentPausedOrRunning_KeepsPolling pins the "only idle
// completes" rule: a paused (or still-running) agent must not produce a false
// "completed".
func TestAdvanceExecutor_AgentPausedOrRunning_KeepsPolling(t *testing.T) {
	for _, state := range []string{"paused", "running", "waiting"} {
		t.Run(state, func(t *testing.T) {
			a, _ := freshProject(t, t.TempDir())
			execID := convAgentID(t, 903)
			ctx := monitorCtx(map[string]ref.Ref{execID: statusRef(execID, state)})
			card := seedSchedulerCard(t, a, "scheduler:poll-exec", "  schedule_type: prompt", "Body.", execID, "")

			if err := a.advanceExecutor(ctx, card); err != nil {
				t.Fatalf("advanceExecutor: %v", err)
			}
			updated, _ := a.store.Get("scheduler:poll-exec")
			if got := timerState(updated); got != timerStatusRunning {
				t.Fatalf("run_status = %q, want running (still polling)", got)
			}
			if len(resultCardStatuses(a, "scheduler:poll-exec")) != 0 {
				t.Fatal("paused/running agent must not emit a result card")
			}
		})
	}
}

// TestAdvanceAgentTaskExecutor_DeadAgent_ConvergesFailed covers wiring #1: the
// agent_task completion path must converge when the last run agent is gone
// instead of returning an error forever.
func TestAdvanceAgentTaskExecutor_DeadAgent_ConvergesFailed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID()) // resolves no agent
	missing := convAgentID(t, 904)
	card := seedSchedulerCard(t, a, "scheduler:at-dead",
		"  schedule_type: agent_task\n  bind_mode: ephemeral", "Body.", missing, "")

	if err := a.advanceAgentTaskExecutor(ctx, card); err != nil {
		t.Fatalf("advanceAgentTaskExecutor must converge, not error: %v", err)
	}
	updated, _ := a.store.Get("scheduler:at-dead")
	if got := timerState(updated); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want failed", got)
	}
}

// TestAdvanceReviewer_AgentNotFound_ConvergesFailed covers the reviewer idle
// branch of #3.
func TestAdvanceReviewer_AgentNotFound_ConvergesFailed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())
	missing := convAgentID(t, 905)
	card := seedSchedulerCard(t, a, "scheduler:gone-rev", "  schedule_type: prompt", "Body.", "", missing)
	if err := a.saveTimerState(card, map[string]string{"run_status": timerStatusPendingReview}); err != nil {
		t.Fatalf("set pending_review: %v", err)
	}
	card, _ = a.store.Get("scheduler:gone-rev")

	if err := a.advanceReviewer(ctx, card); err != nil {
		t.Fatalf("advanceReviewer must converge, not error: %v", err)
	}
	updated, _ := a.store.Get("scheduler:gone-rev")
	if got := timerState(updated); got != timerStatusFailed {
		t.Fatalf("run_status = %q, want failed", got)
	}
}

// ── #5 checkAgentIdle state machine ──────────────────────────────────────────

func TestCheckAgentIdle_StateMachine(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())

	idleEmpty := convAgentID(t, 910)
	idleNamed := convAgentID(t, 911)
	paused := convAgentID(t, 912)
	running := convAgentID(t, 913)
	failed := convAgentID(t, 914)
	nilResp := convAgentID(t, 915)

	nilRef := func(hex string) ref.Ref {
		cid, _ := identity.ParseCanonicalID(hex)
		return testutil.NewFakeRef(id.From(cid), func(string, any) any { return nil })
	}
	ctx := monitorCtx(map[string]ref.Ref{
		idleEmpty: statusRef(idleEmpty, ""),
		idleNamed: statusRef(idleNamed, "idle"),
		paused:    statusRef(paused, "paused"),
		running:   statusRef(running, "running"),
		failed:    statusRef(failed, "failed"),
		nilResp:   nilRef(nilResp),
	})

	cases := []struct {
		name     string
		agent    string
		wantIdle bool
		wantErr  error
	}{
		{name: "empty state is idle", agent: idleEmpty, wantIdle: true},
		{name: "named idle is idle", agent: idleNamed, wantIdle: true},
		{name: "paused keeps polling", agent: paused, wantIdle: false},
		{name: "running keeps polling", agent: running, wantIdle: false},
		{name: "failed converges", agent: failed, wantIdle: false, wantErr: errAgentFailed},
		{name: "nil response keeps polling", agent: nilResp, wantIdle: false},
		{name: "missing agent converges", agent: convAgentID(t, 916), wantIdle: false, wantErr: errAgentNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			done, err := a.checkAgentIdle(ctx, tc.agent)
			if done != tc.wantIdle {
				t.Fatalf("done = %v, want %v", done, tc.wantIdle)
			}
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			if err == nil || !isAgentConvergenceError(err) {
				t.Fatalf("err = %v, want convergence error", err)
			}
			if !strings.Contains(err.Error(), tc.wantErr.Error()) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// ── wiring #4 ephemeral completion unbinds before unload ─────────────────────

func TestAdvanceAgentTaskExecutor_Ephemeral_UnbindsBeforeUnload(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:eph-unbind", "  schedule_type: task", "Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": agentTaskEphemeralHex,
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card, _ = a.store.Get("scheduler:eph-unbind")
	card.Raw = setCardDataStringInRaw(card.Raw, "last_run_agent", "Sched#1")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("set last_run_agent: %v", err)
	}
	card, _ = a.store.Get("scheduler:eph-unbind")

	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	if got := rec.count("scheduler_unbind"); got != 1 {
		t.Fatalf("scheduler_unbind calls = %d, want 1", got)
	}
	if len(ws.unloaded) != 1 || ws.unloaded[0].AgentID != "Sched#1" {
		t.Fatalf("unloaded = %v, want [{Sched#1}]", ws.unloaded)
	}
	saved, _ := a.store.Get("scheduler:eph-unbind")
	if got := timerState(saved); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want idle", got)
	}
	if got := cardDataString(saved, "last_run_agent"); got != "" {
		t.Fatalf("last_run_agent = %q, want empty", got)
	}
}

// ── wiring #3 applySchedulerUnbind handles unloaded (gray) agents ────────────

func TestApplySchedulerUnbind_UnloadedAgent_LoadsThenUnbinds(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "unloaded"},
		},
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	loaded := false
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return ws, true
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if loaded && aid == agentTaskID(agentTaskBoundHex) {
			return rec.ref(), true
		}
		return nil, false
	}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		loaded = true
		return rec.ref(), nil
	}

	raw := "---\nid: scheduler:unloaded-bind\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	a.applySchedulerUnbind(ctx, "scheduler:unloaded-bind", raw)

	if got := rec.count("scheduler_unbind"); got != 1 {
		t.Fatalf("scheduler_unbind calls = %d, want 1 (unloaded agent must be loaded first)", got)
	}
}

// A truly missing registry row is a no-op, not an error path.
func TestApplySchedulerUnbind_MissingAgent_NoOp(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ws := &agentTaskWorkspace{}
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:missing-agent\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:ghost\n---\n\nBody."
	a.applySchedulerUnbind(ctx, "scheduler:missing-agent", raw)

	if got := rec.count("scheduler_unbind"); got != 0 {
		t.Fatalf("scheduler_unbind calls = %d, want 0 for a missing registry row", got)
	}
}

// ── #6 toggle writes enabled into the schedule block ─────────────────────────

func TestSetScheduleEnabled_LegacyTopLevelSchedule(t *testing.T) {
	raw := "---\nid: scheduler:legacy\ntype: scheduler\ntags: []\nschedule:\n  cron: \"0 9 * * *\"\n  enabled: true\n---\n\nRun."

	off := setScheduleEnabled(raw, false)
	if got := parseTopLevelSchedule(off); got.Enabled {
		t.Fatalf("parseTopLevelSchedule(enabled) = true, want false; raw:\n%s", off)
	}
	if !strings.Contains(off, "  enabled: false") {
		t.Fatalf("enabled was not written as a child of the schedule block:\n%s", off)
	}
	// And parseScheduleFromFrontmatter (the sync reader) agrees.
	if got := parseScheduleFromFrontmatter(off); got.Enabled || got.Cron == "" {
		t.Fatalf("parseScheduleFromFrontmatter = %+v, want enabled=false with cron intact", got)
	}

	on := setScheduleEnabled(off, true)
	if got := parseScheduleFromFrontmatter(on); !got.Enabled {
		t.Fatalf("re-enabled schedule reads enabled=%v, want true", got.Enabled)
	}
}

// The primary layout (data.schedule) still round-trips through the toggle.
func TestSetScheduleEnabled_DataSchedule(t *testing.T) {
	raw := "---\nid: scheduler:data-tz\ntype: scheduler\ndata:\n  schedule:\n    cron: \"0 9 * * *\"\n    timezone: \"Asia/Shanghai\"\n    enabled: true\n---\n\nRun."

	off := setScheduleEnabled(raw, false)
	got := parseScheduleFromFrontmatter(off)
	if got.Enabled || got.Cron != "0 9 * * *" || got.Timezone != "Asia/Shanghai" {
		t.Fatalf("parseScheduleFromFrontmatter = %+v, want enabled=false with cron/tz intact", got)
	}
}

// A card with a data: block but a legacy top-level schedule must keep writing
// into the top-level schedule (inserting an empty data.schedule would make the
// parser read empty cron and unregister the timer).
func TestSetScheduleEnabled_HybridTopLevelSchedule(t *testing.T) {
	raw := "---\nid: scheduler:hybrid\ntype: scheduler\ndata:\n  run_status: running\nschedule:\n  cron: \"0 9 * * *\"\n  enabled: true\n---\n\nRun."

	off := setScheduleEnabled(raw, false)
	if strings.Contains(off, "data:\n  run_status: running\n  schedule:") {
		t.Fatalf("must not inject an empty data.schedule block:\n%s", off)
	}
	got := parseScheduleFromFrontmatter(off)
	if got.Enabled || got.Cron != "0 9 * * *" {
		t.Fatalf("parseScheduleFromFrontmatter = %+v, want enabled=false cron intact", got)
	}
}

// A card with no schedule block at all gets a top-level schedule block the
// parser reads (the old code wrote a top-level `enabled:` key the parser
// ignored).
func TestSetScheduleEnabled_NoScheduleBlock(t *testing.T) {
	raw := "---\nid: scheduler:bare\ntype: scheduler\ntags: []\n---\n\nRun."
	off := setScheduleEnabled(raw, false)
	if got := parseTopLevelSchedule(off); got.Enabled {
		t.Fatalf("bare card enabled = true, want false; raw:\n%s", off)
	}
}

func TestHandleToggleTimer_LegacyTopLevelSchedule(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	raw := "---\nid: scheduler:toggle-legacy\ntype: scheduler\ntags: []\nschedule:\n  cron: \"0 9 * * *\"\n  enabled: true\n---\n\nRun."
	if err := a.store.Save(&CardRecord{Title: "scheduler:toggle-legacy", Raw: raw}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	schedRef := testutil.NewFakeRef(testutil.GenActorID(), func(string, any) any { return nil })
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "scheduler" {
			return schedRef, true
		}
		return nil, false
	}

	resp, err := a.handleToggleTimer(ctx, gen.WikiToggleTimerReq{ID: "scheduler:toggle-legacy", Enabled: false})
	if err != nil {
		t.Fatalf("handleToggleTimer: %v", err)
	}
	if resp.Enabled {
		t.Fatalf("resp.Enabled = true, want false")
	}
	saved, _ := a.store.Get("scheduler:toggle-legacy")
	if got := parseScheduleFromFrontmatter(saved.Raw); got.Enabled {
		t.Fatalf("toggled-off schedule still parses enabled=true; raw:\n%s", saved.Raw)
	}
}

// ── #9 monitor save preserves concurrent edits ───────────────────────────────

func TestMonitorSaveTimerState_PreservesConcurrentEdit(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	raw := "---\nid: scheduler:concurrent\ntype: scheduler\ndata:\n  run_status: running\n---\n\nRun."
	if err := a.store.Save(&CardRecord{Title: "scheduler:concurrent", Raw: raw}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The monitor tick reads a stale snapshot.
	stale, err := a.store.Get("scheduler:concurrent")
	if err != nil {
		t.Fatalf("get stale: %v", err)
	}

	// A concurrent user edit lands between the tick's List and its Save.
	fresh, _ := a.store.Get("scheduler:concurrent")
	fresh.Raw = setCardDataStringInRaw(fresh.Raw, "note", "user-edit")
	if err := a.store.Save(fresh); err != nil {
		t.Fatalf("concurrent save: %v", err)
	}

	if err := a.monitorSaveTimerState(stale, map[string]string{"run_status": timerStatusIdle}, false); err != nil {
		t.Fatalf("monitorSaveTimerState: %v", err)
	}

	saved, _ := a.store.Get("scheduler:concurrent")
	if got := cardDataString(saved, "note"); got != "user-edit" {
		t.Fatalf("concurrent edit lost: note = %q, want user-edit", got)
	}
	if got := timerState(saved); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want idle", got)
	}
}

// ── #14 result card IDs do not collide within the same second ────────────────

func TestCreateResultCard_NoSameSecondCollision(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	ctx := testutil.AdminCtx(testutil.GenActorID())

	a.createResultCard(ctx, pendingExecution{CardID: "scheduler:collide"}, "completed", "")
	a.createResultCard(ctx, pendingExecution{CardID: "scheduler:collide"}, "completed", "")

	if got := len(resultCardStatuses(a, "scheduler:collide")); got != 2 {
		t.Fatalf("result cards = %d, want 2 (same-second IDs must not overwrite)", got)
	}
}
