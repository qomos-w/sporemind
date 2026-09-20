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
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ── test identifiers ─────────────────────────────────────────────────────────

const (
	agentTaskBoundHex = "019efa0a000000000000000000000101"
	agentTaskRebindHex = "019efa0a000000000000000000000102"
	agentTaskEphemeralHex = "019efa0a000000000000000000000103"
)

func agentTaskID(hex string) id.ActorID {
	cid, err := identity.ParseCanonicalID(hex)
	if err != nil {
		panic(err)
	}
	return id.From(cid)
}

// ── mocks ───────────────────────────────────────────────────────────────────

// agentTaskRecorder records every Invoke sent to the mocked scheduler-run agent.
type agentTaskRecorder struct {
	mu     sync.Mutex
	calls  []agentTaskCall
	counts map[string]int
	state  string
	self   id.ActorID
}

type agentTaskCall struct {
	ID      string
	Payload any
}

func newAgentTaskRecorder(hex, state string) *agentTaskRecorder {
	return &agentTaskRecorder{
		counts: map[string]int{},
		state:  state,
		self:   agentTaskID(hex),
	}
}

func (r *agentTaskRecorder) ref() ref.Ref {
	return testutil.NewFakeRef(r.self, r.invokeFn)
}

func (r *agentTaskRecorder) invokeFn(callID string, payload any) any {
	r.mu.Lock()
	r.calls = append(r.calls, agentTaskCall{ID: callID, Payload: payload})
	r.counts[callID]++
	state := r.state
	r.mu.Unlock()

	if callID == "agent_status" {
		return gen.AgentStatusResp{State: state}
	}
	return nil
}

func (r *agentTaskRecorder) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i] = c.ID
	}
	return out
}

func (r *agentTaskRecorder) count(callID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[callID]
}

func (r *agentTaskRecorder) chatTexts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, c := range r.calls {
		if c.ID == "chat_submit" {
			if req, ok := c.Payload.(gen.AgentChatSubmitReq); ok {
				out = append(out, req.Text)
			}
		}
	}
	return out
}

func (r *agentTaskRecorder) bindReqs() []gen.AgentSchedulerBindReq {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []gen.AgentSchedulerBindReq
	for _, c := range r.calls {
		if c.ID == "scheduler_bind" {
			if req, ok := c.Payload.(gen.AgentSchedulerBindReq); ok {
				out = append(out, req)
			}
		}
	}
	return out
}

// agentTaskWorkspace mocks the workspace service for agent_task tests.
type agentTaskWorkspace struct {
	mu           sync.Mutex
	agents       []gen.AgentRef
	spawnActorID string // ActorID assigned to spawned scheduler agents
	// spawnSlots, when set, is copied onto the freshly registered scheduler
	// agent row to emulate the workspace applying agent-kind config defaults
	// to nil slots at registration time (wiring #2).
	spawnSlots gen.AgentRef
	spawned    []gen.WorkspaceAgentSpawnSchedulerReq
	spawnedIDs []string
	loaded     []gen.WorkspaceAgentLoadedReq
	unloaded   []gen.AgentUnloadReq
}

func (w *agentTaskWorkspace) ID() id.ActorID          { return id.ActorID{} }
func (w *agentTaskWorkspace) Service() (string, bool) { return "workspace", true }

func (w *agentTaskWorkspace) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch callID {
	case "workspace.list_agents":
		items := append([]gen.AgentRef(nil), w.agents...)
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.AgentRefListResp{Items: items}})

	case "workspace.agent_spawn_scheduler":
		var req gen.WorkspaceAgentSpawnSchedulerReq
		if r, ok := payload.(gen.WorkspaceAgentSpawnSchedulerReq); ok {
			req = r
		}
		w.spawned = append(w.spawned, req)
		agentID := fmt.Sprintf("Sched#%d", len(w.spawned))
		w.spawnedIDs = append(w.spawnedIDs, agentID)
		w.agents = append(w.agents, gen.AgentRef{
			ID:             agentID,
			ProjectID:      req.ProjectID,
			DisplayName:    agentID,
			AgentKind:      req.AgentKind,
			ActorID:        w.spawnActorID,
			LifecycleScope: "scheduler",
			Status:         "idle",
			LoadState:      "unloaded",
			Primary:        w.spawnSlots.Primary,
			Fast:           w.spawnSlots.Fast,
			Execution:      w.spawnSlots.Execution,
			Review:         w.spawnSlots.Review,
			Summary:        w.spawnSlots.Summary,
		})
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: gen.WorkspaceAgentSpawnSchedulerResp{
			AgentID:     agentID,
			DisplayName: agentID,
			AgentKind:   req.AgentKind,
			ActorID:     w.spawnActorID,
		}})

	case "workspace.agent_loaded":
		var req gen.WorkspaceAgentLoadedReq
		if r, ok := payload.(gen.WorkspaceAgentLoadedReq); ok {
			req = r
		}
		w.loaded = append(w.loaded, req)
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: nil})

	case "workspace.agent_unload":
		var req gen.AgentUnloadReq
		if r, ok := payload.(gen.AgentUnloadReq); ok {
			req = r
		}
		w.unloaded = append(w.unloaded, req)
		return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{value: nil})
	}
	return nil
}

// schedulerNoOp is a minimal scheduler service ref used only to let
// syncSchedulerCard proceed past the service lookup check.
type schedulerNoOp struct{}

func (schedulerNoOp) ID() id.ActorID                { return id.ActorID{} }
func (schedulerNoOp) Service() (string, bool)       { return "scheduler", true }
func (schedulerNoOp) Invoke(context.Context, string, any, ...map[string]string) *invoke.Call {
	return nil
}

// agentTaskCtx builds a FakeCtx wired with workspace + agent lookups and spawn.
func agentTaskCtx(t *testing.T, ws *agentTaskWorkspace, agents map[string]*agentTaskRecorder, scheduler bool) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		switch name {
		case "workspace":
			if ws != nil {
				return ws, true
			}
		case "scheduler":
			if scheduler {
				return schedulerNoOp{}, true
			}
		}
		return nil, false
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if rec, ok := agents[aid.String()]; ok {
			return rec.ref(), true
		}
		return nil, false
	}
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		// Local spawn for the ephemeral agent: SpawnFn returns the ephemeral
		// recorder; handleSpawnAgent uses spawned.ID().String() as resp.ActorID
		// and ensureSchedulerAgentLoaded resolves it via LookupID.
		if rec, ok := agents[agentTaskEphemeralHex]; ok {
			return rec.ref(), nil
		}
		return nil, fmt.Errorf("no spawn target wired")
	}
	return ctx
}

// ── fire branch dispatch ──────────────────────────────────────────────────────

func TestTimerFireBranch_AgentTask(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want timerFireKind
	}{
		{
			name: "explicit agent_task bound",
			raw: "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody.",
			want: fireAgentTask,
		},
		{
			name: "prompt with bind_mode=bound",
			raw: "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: prompt\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody.",
			want: fireAgentTask,
		},
		{
			name: "prompt with bind_mode=ephemeral",
			raw: "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: prompt\n  bind_mode: ephemeral\n---\n\nBody.",
			want: fireAgentTask,
		},
		{
			name: "legacy prompt stays prompt",
			raw: "---\nid: t\ntype: scheduler\ndata:\n  executor: agent:coder\n---\n\nBody.",
			want: firePrompt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := decodeCard("t", tt.raw)
			kind, _, err := timerFireBranch(card)
			if err != nil {
				t.Fatalf("timerFireBranch: %v", err)
			}
			if kind != tt.want {
				t.Errorf("kind = %v, want %v", kind, tt.want)
			}
		})
	}
}

func TestIsAgentTaskCard(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "explicit agent_task",
			raw:  "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: agent_task\n  bind_mode: bound\n---\n\nBody.",
			want: true,
		},
		{
			name: "prompt + bind_mode",
			raw:  "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: prompt\n  bind_mode: ephemeral\n---\n\nBody.",
			want: true,
		},
		{
			name: "legacy prompt",
			raw:  "---\nid: t\ntype: scheduler\ndata:\n  executor: agent:coder\n---\n\nBody.",
			want: false,
		},
		{
			name: "workflow",
			raw:  "---\nid: t\ntype: scheduler\ndata:\n  schedule_type: workflow\n  workflow_template: tpl::a\n---\n\nBody.",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := decodeCard("t", tt.raw)
			if got := isAgentTaskCard(card); got != tt.want {
				t.Errorf("isAgentTaskCard = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── bound branch ─────────────────────────────────────────────────────────────

func TestExecuteAgentTask_Bound_Run(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, DisplayName: "Coder", AgentKind: "coder", LoadState: "loaded", LifecycleScope: "member"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	card := schedulerCard("scheduler:bound-run",
		"  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder",
		"Do the daily task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}

	ids := rec.ids()
	want := []string{"turn_cancel", "modes_unload_all", "scheduler_bind", "chat_submit"}
	if !strings.HasPrefix(joinIDs(ids), joinIDs(want)) {
		t.Fatalf("agent calls = %v, want prefix %v", ids, want)
	}
	if got := rec.chatTexts(); len(got) != 1 || got[0] != "Do the daily task." {
		t.Fatalf("chat texts = %v, want [Do the daily task.]", got)
	}

	saved, err := a.store.Get("scheduler:bound-run")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusRunning {
		t.Fatalf("run_status = %q, want %q", got, timerStatusRunning)
	}
	if got := cardDataString(saved, "executor_ref"); got != agentTaskBoundHex {
		t.Fatalf("executor_ref = %q, want %q", got, agentTaskBoundHex)
	}
	if got := cardDataString(saved, "last_run_agent"); got != "" {
		t.Fatalf("last_run_agent = %q, want empty for bound", got)
	}
	if len(ws.spawned) != 0 {
		t.Fatalf("spawned %d agents, want 0", len(ws.spawned))
	}
}

func TestExecuteAgentTask_Bound_Rebind(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskRebindHex, "idle")
	ws := &agentTaskWorkspace{spawnActorID: agentTaskRebindHex}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskRebindHex: rec}, false)

	card := schedulerCard("scheduler:bound-rebind",
		"  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder",
		"Rebind task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}

	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d agents, want 1", len(ws.spawned))
	}
	newAgentID := ws.spawnedIDs[0]

	saved, err := a.store.Get("scheduler:bound-rebind")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "bound_agent"); got != newAgentID {
		t.Fatalf("bound_agent = %q, want %q", got, newAgentID)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusRunning {
		t.Fatalf("run_status = %q, want %q", got, timerStatusRunning)
	}
	if count := rec.count("chat_submit"); count != 1 {
		t.Fatalf("chat_submit calls = %d, want 1", count)
	}
}

func TestExecuteAgentTask_Bound_NoBoundAgent_SpawnsFresh(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskRebindHex, "idle")
	ws := &agentTaskWorkspace{spawnActorID: agentTaskRebindHex}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskRebindHex: rec}, false)

	// A bound-mode card that was never bound (no data.bound_agent) must not
	// fail its fire: the fire path spawns a fresh scheduler-scoped agent,
	// rebinds the card to it, and runs the body on it.
	card := schedulerCard("scheduler:unbound-bound",
		"  schedule_type: agent_task\n  bind_mode: bound",
		"Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}

	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d agents, want 1", len(ws.spawned))
	}
	newAgentID := ws.spawnedIDs[0]

	saved, err := a.store.Get("scheduler:unbound-bound")
	if err != nil {
		t.Fatalf("reload card: %v", err)
	}
	if got := cardDataString(saved, "bound_agent"); got != newAgentID {
		t.Fatalf("bound_agent = %q, want %q", got, newAgentID)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusRunning {
		t.Fatalf("run_status = %q, want %q", got, timerStatusRunning)
	}
	if count := rec.count("chat_submit"); count != 1 {
		t.Fatalf("chat_submit calls = %d, want 1", count)
	}
}

// ── ephemeral branch ────────────────────────────────────────────────────────

func TestExecuteAgentTask_Ephemeral_Run(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	// spawnActorID empty => the spawned agent is "unloaded"; the fire branch must
	// locally spawn it via handleSpawnAgent -> ctx.SpawnFn.
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:eph-run",
		"  schedule_type: agent_task\n  bind_mode: ephemeral",
		"Do the daily task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}

	if len(ws.spawned) != 1 {
		t.Fatalf("spawned %d agents, want 1", len(ws.spawned))
	}
	spawnedID := ws.spawnedIDs[0]

	// agent_loaded notify is sent after local spawn.
	if len(ws.loaded) != 1 {
		t.Fatalf("agent_loaded calls = %d, want 1", len(ws.loaded))
	}
	if ws.loaded[0].AgentID != spawnedID {
		t.Fatalf("loaded AgentID = %q, want %q", ws.loaded[0].AgentID, spawnedID)
	}

	ids := rec.ids()
	want := []string{"turn_cancel", "modes_unload_all", "scheduler_bind", "chat_submit"}
	if !strings.HasPrefix(joinIDs(ids), joinIDs(want)) {
		t.Fatalf("agent calls = %v, want prefix %v", ids, want)
	}

	saved, err := a.store.Get("scheduler:eph-run")
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

func TestExecuteAgentTask_Ephemeral_InFlightGuard(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:eph-guard",
		"  schedule_type: agent_task\n  bind_mode: ephemeral",
		"Guard task.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{"run_status": timerStatusRunning}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card, _ = a.store.Get("scheduler:eph-guard")

	if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
		t.Fatalf("executeAgentTaskTimerCard: %v", err)
	}

	if len(ws.spawned) != 0 {
		t.Fatalf("spawned %d agents, want 0 (in-flight guard)", len(ws.spawned))
	}
	if count := rec.count("chat_submit"); count != 0 {
		t.Fatalf("chat_submit calls = %d, want 0", count)
	}
}

// ── monitor completion ───────────────────────────────────────────────────────

func TestAdvanceAgentTaskExecutor_Bound_Completed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded", LifecycleScope: "member"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	card := schedulerCard("scheduler:bound-done",
		"  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder",
		"Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": agentTaskBoundHex,
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card, _ = a.store.Get("scheduler:bound-done")

	if err := a.advanceAgentTaskExecutor(ctx, card); err != nil {
		t.Fatalf("advanceAgentTaskExecutor: %v", err)
	}

	saved, err := a.store.Get("scheduler:bound-done")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want %q", got, timerStatusIdle)
	}
	if len(ws.unloaded) != 0 {
		t.Fatalf("unloaded %d agents, want 0 for bound", len(ws.unloaded))
	}
	// A result log card should have been created.
	if !hasAutomationLog(a) {
		t.Fatal("expected automation-log result card")
	}
}

// A prompt-mode task card that opted into the bound contract keeps its run
// agent resident after completion — the explicit bind_mode overrides the
// task-card ephemeral default.
func TestAdvanceAgentTaskExecutor_TaskBound_Completed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded", LifecycleScope: "member"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	card := schedulerCard("scheduler:task-bound-done",
		"  schedule_type: task\n  bind_mode: bound\n  bound_agent: agent:coder",
		"Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": agentTaskBoundHex,
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card, _ = a.store.Get("scheduler:task-bound-done")

	if err := a.advanceAgentTaskExecutor(ctx, card); err != nil {
		t.Fatalf("advanceAgentTaskExecutor: %v", err)
	}

	saved, err := a.store.Get("scheduler:task-bound-done")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := cardDataString(saved, "run_status"); got != timerStatusIdle {
		t.Fatalf("run_status = %q, want %q", got, timerStatusIdle)
	}
	if len(ws.unloaded) != 0 {
		t.Fatalf("unloaded %d agents, want 0 for bound task card", len(ws.unloaded))
	}
}

func TestAdvanceAgentTaskExecutor_Ephemeral_Completed(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskEphemeralHex, "idle")
	ws := &agentTaskWorkspace{}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskEphemeralHex: rec}, false)

	card := schedulerCard("scheduler:eph-done",
		"  schedule_type: agent_task\n  bind_mode: ephemeral",
		"Body.")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("seed card: %v", err)
	}
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"executor_ref": agentTaskEphemeralHex,
	}); err != nil {
		t.Fatalf("set running: %v", err)
	}
	card.Raw = setCardDataStringInRaw(card.Raw, "last_run_agent", "Sched#1")
	if err := a.store.Save(card); err != nil {
		t.Fatalf("set last_run_agent: %v", err)
	}
	card, _ = a.store.Get("scheduler:eph-done")

	if err := a.advanceAgentTaskExecutor(ctx, card); err != nil {
		t.Fatalf("advanceAgentTaskExecutor: %v", err)
	}

	saved, err := a.store.Get("scheduler:eph-done")
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

// ── validation ───────────────────────────────────────────────────────────────

func TestValidateSchedulerCard_AgentTask(t *testing.T) {
	baseData := "  schedule:\n    cron: \"0 9 * * *\"\n"
	cases := []struct {
		name     string
		extra    string
		wantErr  bool
		wantCode string
	}{
		{
			name: "valid bound",
			extra: "  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder\n",
		},
		{
			name: "valid ephemeral",
			extra: "  schedule_type: agent_task\n  bind_mode: ephemeral\n",
		},
		{
			name: "prompt + bind_mode valid without executor",
			extra: "  schedule_type: prompt\n  bind_mode: bound\n  bound_agent: agent:coder\n",
		},
		{
			name:  "missing bind_mode",
			extra: "  schedule_type: agent_task\n",
			wantErr: true, wantCode: "scheduler_bind_mode_missing",
		},
		{
			name:  "invalid bind_mode",
			extra: "  schedule_type: agent_task\n  bind_mode: hybrid\n",
			wantErr: true, wantCode: "scheduler_bind_mode_invalid",
		},
		{
			name: "bound without bound_agent is valid (self-heals on fire)",
			extra: "  schedule_type: agent_task\n  bind_mode: bound\n",
		},
		{
			name:  "ephemeral with bound_agent",
			extra: "  schedule_type: agent_task\n  bind_mode: ephemeral\n  bound_agent: agent:coder\n",
			wantErr: true, wantCode: "scheduler_ephemeral_bound_agent_conflict",
		},
		{
			name:  "agent_task with executor conflict",
			extra: "  schedule_type: agent_task\n  bind_mode: bound\n  bound_agent: agent:coder\n  executor: agent:other\n",
			wantErr: true, wantCode: "scheduler_executor_conflict",
		},
		{
			name:  "empty body",
			extra: "  schedule_type: agent_task\n  bind_mode: ephemeral\n",
			wantErr: true, wantCode: "scheduler_agent_task_body_empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := "Do work."
			if tc.wantCode == "scheduler_agent_task_body_empty" {
				body = ""
			}
			raw := "---\nid: v\ntype: scheduler\ntags: []\ndata:\n" + baseData + tc.extra + "---\n\n" + body
			errs := validateCardStructured("v", raw)
			codes := errorCodes(errs)
			if !tc.wantErr {
				for _, c := range []string{"scheduler_bind_mode_missing", "scheduler_bind_mode_invalid", "scheduler_bound_agent_missing", "scheduler_ephemeral_bound_agent_conflict", "scheduler_executor_conflict", "scheduler_agent_task_body_empty"} {
					if codes[c] {
						t.Fatalf("unexpected error %q: %v", c, errs)
					}
				}
				return
			}
			if !codes[tc.wantCode] {
				t.Fatalf("expected error %q, got %v", tc.wantCode, errs)
			}
		})
	}
}

// ── binding lifecycle hooks ──────────────────────────────────────────────────

func TestApplySchedulerBind_Bound_Loaded(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded", LifecycleScope: "member"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	a.applySchedulerBind(ctx, "scheduler:test", raw)

	if count := rec.count("scheduler_bind"); count != 1 {
		t.Fatalf("scheduler_bind calls = %d, want 1", count)
	}
	bindReqs := rec.bindReqs()
	if len(bindReqs) != 1 || bindReqs[0].SchedulerCardID != "scheduler:test" {
		t.Fatalf("bind reqs = %v, want one for scheduler:test", bindReqs)
	}
}

func TestApplySchedulerBind_Bound_NotLoaded_NoOp(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: "not-hex", AgentKind: "coder", LoadState: "unloaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	a.applySchedulerBind(ctx, "scheduler:test", raw)

	if count := rec.count("scheduler_bind"); count != 0 {
		t.Fatalf("scheduler_bind calls = %d, want 0 (not loaded)", count)
	}
}

func TestApplySchedulerBind_Ephemeral_NoOp(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: ephemeral\n---\n\nBody."
	a.applySchedulerBind(ctx, "scheduler:test", raw)

	if count := rec.count("scheduler_bind"); count != 0 {
		t.Fatalf("scheduler_bind calls = %d, want 0 for ephemeral", count)
	}
}

func TestApplySchedulerUnbind_Bound(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	a.applySchedulerUnbind(ctx, "scheduler:test", raw)

	if count := rec.count("scheduler_unbind"); count != 1 {
		t.Fatalf("scheduler_unbind calls = %d, want 1", count)
	}
}

func TestMaybeUnbindOldSchedulerAgent_Rebind(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	recOld := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	recNew := newAgentTaskRecorder(agentTaskRebindHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded"},
			{ID: "coder2", ActorID: agentTaskRebindHex, AgentKind: "coder", LoadState: "loaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{
		agentTaskBoundHex:  recOld,
		agentTaskRebindHex: recNew,
	}, false)

	oldRaw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	newRaw := "---\nid: scheduler:test\ntype: scheduler\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder2\n---\n\nBody."

	a.maybeUnbindOldSchedulerAgent(ctx, oldRaw, newRaw)

	if count := recOld.count("scheduler_unbind"); count != 1 {
		t.Fatalf("old agent scheduler_unbind calls = %d, want 1", count)
	}
	if count := recNew.count("scheduler_bind"); count != 0 {
		t.Fatalf("new agent scheduler_bind calls = %d, want 0 before sync", count)
	}
}

// ── wiki lifecycle paths ─────────────────────────────────────────────────────

func TestWikiDeleteCard_SchedulerUnbinds(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	rec := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{agentTaskBoundHex: rec}, false)

	raw := "---\nid: scheduler:delete\ntype: scheduler\ntags: []\ndata:\n  bind_mode: bound\n  bound_agent: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nBody."
	if err := a.store.Save(&CardRecord{Title: "scheduler:delete", Raw: raw}); err != nil {
		t.Fatalf("seed card: %v", err)
	}

	_, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "scheduler:delete"})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if count := rec.count("scheduler_unbind"); count != 1 {
		t.Fatalf("scheduler_unbind calls = %d, want 1", count)
	}
	if _, err := a.store.Get("scheduler:delete"); err == nil {
		t.Fatal("scheduler card still exists after delete")
	}
}

func TestWikiEditCard_RebindBindsAndUnbinds(t *testing.T) {
	a, _ := freshProject(t, t.TempDir())
	recOld := newAgentTaskRecorder(agentTaskBoundHex, "idle")
	recNew := newAgentTaskRecorder(agentTaskRebindHex, "idle")
	ws := &agentTaskWorkspace{
		agents: []gen.AgentRef{
			{ID: "coder", ActorID: agentTaskBoundHex, AgentKind: "coder", LoadState: "loaded"},
			{ID: "coder2", ActorID: agentTaskRebindHex, AgentKind: "coder", LoadState: "loaded"},
		},
	}
	ctx := agentTaskCtx(t, ws, map[string]*agentTaskRecorder{
		agentTaskBoundHex:  recOld,
		agentTaskRebindHex: recNew,
	}, true) // scheduler service needed for syncSchedulerCard to mount new binding

	oldRaw := "---\nid: scheduler:edit\ntype: scheduler\ntags: []\ndata:\n  schedule:\n    cron: \"0 9 * * *\"\n  bind_mode: bound\n  bound_agent: agent:coder\n---\n\nBody."
	newRaw := "---\nid: scheduler:edit\ntype: scheduler\ntags: []\ndata:\n  schedule:\n    cron: \"0 9 * * *\"\n  bind_mode: bound\n  bound_agent: agent:coder2\n---\n\nBody."

	if err := a.store.Save(&CardRecord{Title: "scheduler:edit", Raw: oldRaw}); err != nil {
		t.Fatalf("seed old card: %v", err)
	}

	_, err := a.handleWikiEditCard(ctx, domain.WikiEditCardReq{ID: "scheduler:edit", Raw: newRaw})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}

	if count := recOld.count("scheduler_unbind"); count != 1 {
		t.Fatalf("old agent scheduler_unbind calls = %d, want 1", count)
	}
	if count := recNew.count("scheduler_bind"); count != 1 {
		t.Fatalf("new agent scheduler_bind calls = %d, want 1", count)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func joinIDs(ids []string) string { return strings.Join(ids, ",") }

func hasAutomationLog(a *Actor) bool {
	all, _ := a.store.List()
	for _, c := range all {
		for _, tag := range c.Tags {
			if tag == "automation-log" {
				return true
			}
		}
	}
	return false
}
