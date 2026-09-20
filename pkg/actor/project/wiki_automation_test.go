package project

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ── pure helpers ───────────────────────────────────────────────────────────

func TestTemplateIDForMap(t *testing.T) {
	if got := templateIDForMap("MyFlow"); got != "tpl::MyFlow" {
		t.Errorf("templateIDForMap(%q) = %q, want tpl::MyFlow", "MyFlow", got)
	}
}

func TestWorkflowInstanceID(t *testing.T) {
	got := workflowInstanceID("tpl::MyFlow")
	if !strings.HasPrefix(got, "inst::") {
		t.Errorf("workflowInstanceID = %q, want inst:: prefix", got)
	}
	if !strings.HasSuffix(got, "::tpl::MyFlow") {
		t.Errorf("workflowInstanceID = %q, want ::tpl::MyFlow suffix", got)
	}
}

func TestWorkflowTemplateAndCurrentInstanceHelpers(t *testing.T) {
	// Primary path: values stored in the data: block (what automation_bind and
	// executeWorkflowTimerCard write). These round-trip through parseDataBlock
	// into CardRecord.Data, which is what the helpers read first.
	dataBlockRaw := "---\nid: scheduler:daily\ntype: scheduler\n" +
		"data:\n  executor: agent:coder\n  workflow_template: \"tpl::MyFlow\"\n  current_instance: \"inst::123::tpl::MyFlow\"\n---\n\nbody"
	dataCard := decodeCard("scheduler:daily", dataBlockRaw)
	if got := workflowTemplate(dataCard); got != "tpl::MyFlow" {
		t.Errorf("workflowTemplate(data block) = %q, want tpl::MyFlow", got)
	}
	if got := currentInstance(dataCard); got != "inst::123::tpl::MyFlow" {
		t.Errorf("currentInstance(data block) = %q, want inst::123::tpl::MyFlow", got)
	}
	// Confirm the value really came from Data (the frontend contract).
	if got, _ := dataCard.Data["workflow_template"].(string); got != "tpl::MyFlow" {
		t.Errorf("Data[workflow_template] = %q, want tpl::MyFlow", got)
	}

	// Fallback path: values stored at frontmatter top-level (cards authored
	// per the design doc's conceptual top-level form). The helpers still
	// resolve these.
	topLevelRaw := "---\nid: scheduler:daily\nrun_status: running\n" +
		"workflow_template: \"tpl::MyFlow\"\ncurrent_instance: \"inst::123::tpl::MyFlow\"\n---\n\nbody"
	topCard := &CardRecord{Title: "scheduler:daily", Raw: topLevelRaw}
	if got := workflowTemplate(topCard); got != "tpl::MyFlow" {
		t.Errorf("workflowTemplate(top-level fallback) = %q, want tpl::MyFlow", got)
	}
	if got := currentInstance(topCard); got != "inst::123::tpl::MyFlow" {
		t.Errorf("currentInstance(top-level fallback) = %q, want inst::123::tpl::MyFlow", got)
	}

	// Data block takes precedence over top-level when both are present.
	bothRaw := "---\nid: scheduler:x\nworkflow_template: \"tpl::top\"\n" +
		"data:\n  workflow_template: \"tpl::data\"\n---\n\nbody"
	bothCard := decodeCard("scheduler:x", bothRaw)
	if got := workflowTemplate(bothCard); got != "tpl::data" {
		t.Errorf("workflowTemplate should prefer data block, got %q want tpl::data", got)
	}

	// Card without the fields returns "".
	emptyCard := &CardRecord{Title: "scheduler:other", Raw: "---\nid: scheduler:other\n---\n\nbody"}
	if got := workflowTemplate(emptyCard); got != "" {
		t.Errorf("workflowTemplate(empty) = %q, want empty", got)
	}
	if got := currentInstance(emptyCard); got != "" {
		t.Errorf("currentInstance(empty) = %q, want empty", got)
	}
}

// ── automation_bind ─────────────────────────────────────────────────────────

// createWorkflowMapWithTask creates a workflow map with one task card. Used by
// automation_bind and timer exec tests as a prerequisite for template_save.
func createWorkflowMapWithTask(t *testing.T, a *Actor, ctx actor.Context, mapID string) {
	t.Helper()
	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: mapID}); err != nil {
		t.Fatalf("create map %q: %v", mapID, err)
	}
	taskID := mapID + "-task-1"
	if _, err := a.handleWikiCreateTaskCard(ctx, domain.WikiCreateTaskCardReq{
		MapID: mapID, Title: taskID, Question: "Q1",
	}); err != nil {
		t.Fatalf("create task %q in %q: %v", taskID, mapID, err)
	}
}

func TestHandleWikiAutomationBind(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Workflow map with a task card (template_save needs ≥1 task).
	createWorkflowMapWithTask(t, a, ctx, "wf-source")

	// Scheduler card.
	schedRaw := "---\nid: scheduler:daily\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nRun workflow."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:daily", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	resp, err := a.handleWikiAutomationBind(ctx, domain.WikiAutomationBindReq{
		MapID:           "wf-source",
		SchedulerCardID: "scheduler:daily",
	})
	if err != nil {
		t.Fatalf("automation_bind: %v", err)
	}

	// Template ID follows the tpl::<MapId> convention.
	if resp.TemplateID != "tpl::wf-source" {
		t.Fatalf("TemplateID = %q, want tpl::wf-source", resp.TemplateID)
	}

	// Template map exists and is marked as a template.
	tpl, err := a.store.Get("tpl::wf-source")
	if err != nil {
		t.Fatalf("get template map: %v", err)
	}
	if !cardIsTemplate(tpl) {
		t.Errorf("template map should carry data.template:true; data=%v", tpl.Data)
	}

	// Scheduler card now carries the workflow_template binding in its data:
	// block (round-trips into CardRecord.Data, which the frontend reads since
	// the list view strips Raw). Verify both the Data projection and the
	// helper, plus that the binding is NOT stranded at frontmatter top-level.
	sched, _ := a.store.Get("scheduler:daily")
	if got := workflowTemplate(sched); got != "tpl::wf-source" {
		t.Errorf("scheduler workflow_template = %q, want tpl::wf-source", got)
	}
	if got, _ := sched.Data["workflow_template"].(string); got != "tpl::wf-source" {
		t.Errorf("card.Data[workflow_template] = %q, want tpl::wf-source (frontend reads Data, not Raw)", got)
	}
	if got := frontmatterValue(sched.Raw, "workflow_template"); got != "" {
		t.Errorf("workflow_template must live in the data: block, but a top-level value %q leaked", got)
	}
	if resp.SchedulerCard.ID != "scheduler:daily" {
		t.Errorf("SchedulerCard.ID = %q, want scheduler:daily", resp.SchedulerCard.ID)
	}
}

func TestHandleWikiAutomationBind_RebindIsIdempotent(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createWorkflowMapWithTask(t, a, ctx, "wf-src")

	schedRaw := "---\nid: scheduler:re\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nRun."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:re", Raw: schedRaw})

	// First bind creates the template.
	if _, err := a.handleWikiAutomationBind(ctx, domain.WikiAutomationBindReq{
		MapID: "wf-src", SchedulerCardID: "scheduler:re",
	}); err != nil {
		t.Fatalf("first bind: %v", err)
	}

	// Second bind of the same map reuses the template without error.
	resp, err := a.handleWikiAutomationBind(ctx, domain.WikiAutomationBindReq{
		MapID: "wf-src", SchedulerCardID: "scheduler:re",
	})
	if err != nil {
		t.Fatalf("re-bind should be idempotent: %v", err)
	}
	if resp.TemplateID != "tpl::wf-src" {
		t.Errorf("TemplateID = %q, want tpl::wf-src", resp.TemplateID)
	}
}

func TestHandleWikiAutomationBind_Errors(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createWorkflowMapWithTask(t, a, ctx, "wf-err")

	schedRaw := "---\nid: scheduler:e\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nRun."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:e", Raw: schedRaw})

	// Create a non-scheduler card.
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "note-1", Raw: "---\nid: note-1\ntype: wiki\ntags: []\n---\n\nA note."})

	tests := []struct {
		name string
		req  domain.WikiAutomationBindReq
	}{
		{"missing map id", domain.WikiAutomationBindReq{SchedulerCardID: "scheduler:e"}},
		{"missing scheduler id", domain.WikiAutomationBindReq{MapID: "wf-err"}},
		{"non-scheduler card", domain.WikiAutomationBindReq{MapID: "wf-err", SchedulerCardID: "note-1"}},
		{"nonexistent scheduler", domain.WikiAutomationBindReq{MapID: "wf-err", SchedulerCardID: "scheduler:nope"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.handleWikiAutomationBind(ctx, tc.req); err == nil {
				t.Errorf("expected error for %s, got nil", tc.name)
			}
		})
	}
}

// ── list_templates ──────────────────────────────────────────────────────────

func TestHandleWikiListTemplates(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Two workflow maps: one we snapshot as a template, one left as-is.
	createWorkflowMapWithTask(t, a, ctx, "wf-a")
	createWorkflowMapWithTask(t, a, ctx, "wf-b")

	// Snapshot wf-a as a template.
	a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{MapID: "wf-a", TemplateID: "tpl::wf-a"})

	resp, err := a.handleWikiListTemplates(ctx, domain.WikiListTemplatesReq{})
	if err != nil {
		t.Fatalf("list_templates: %v", err)
	}

	if len(resp.Templates) != 1 {
		t.Fatalf("len(Templates) = %d, want 1 (only tpl::wf-a)", len(resp.Templates))
	}
	if resp.Templates[0].ID != "tpl::wf-a" {
		t.Errorf("Templates[0].ID = %q, want tpl::wf-a", resp.Templates[0].ID)
	}
}

// ── timer exec workflow branch ──────────────────────────────────────────────

// TestExecuteTimerCard_WorkflowBranch verifies that a scheduler card with a
// bound workflow_template, when fired, instantiates a fresh instance map,
// binds the executor as its owner, and transitions to running. The executor
// is resolved via a mock workspace ref.
func TestExecuteTimerCard_WorkflowBranch(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	// Valid canonical IDs for the executor agent.
	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 42)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	// Workflow map + task → snapshot as template.
	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-fire")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-fire", TemplateID: "tpl::wf-fire",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	// Scheduler card bound to the template. The binding lives in the data:
	// block (as automation_bind writes it), proving data-block storage
	// triggers the workflow branch. The executor is agent:coder.
	schedRaw := "---\nid: scheduler:wf-fire\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::wf-fire\"\n---\n\nFire the workflow."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:wf-fire", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	// Fake context with a mock workspace so listProjectAgents can resolve
	// agent:coder → the executor's canonical ActorID, and a mock agent ref
	// so the local spawn + fire-and-forget workflow_start sets the map owner.
	fctx := testutil.AdminCtx(testutil.GenActorID())
	// Mock agent ref: handles workflow_start (stamps owner on the instance map)
	// and chat_submit (returns success).
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			req, ok := payload.(gen.AgentWorkflowStartReq)
			if !ok {
				if p, ok2 := payload.(*gen.AgentWorkflowStartReq); ok2 {
					req = *p
				} else {
					return fmt.Errorf("unexpected workflow_start payload type %T", payload)
				}
			}
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
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
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}

	// Fire the timer card.
	if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
		CardID: "scheduler:wf-fire", ProjectID: a.actorID,
	}); err != nil {
		t.Fatalf("execute timer card: %v", err)
	}

	// Scheduler card should be in running state with a current_instance
	// written into its data: block (frontend-readable via Data).
	sched, _ := a.store.Get("scheduler:wf-fire")
	if timerState(sched) != timerStatusRunning {
		t.Errorf("run_status = %q, want running", timerState(sched))
	}
	instID := currentInstance(sched)
	if instID == "" {
		t.Fatal("current_instance is empty after fire")
	}
	if !strings.HasPrefix(instID, "inst::") {
		t.Errorf("current_instance = %q, want inst:: prefix", instID)
	}
	if dataInst, _ := sched.Data["current_instance"].(string); dataInst != instID {
		t.Errorf("Data[current_instance] = %q, want %q (must round-trip into Data)", dataInst, instID)
	}

	// Instance map should exist and carry the executor as ownerAgentId.
	inst, err := a.store.Get(instID)
	if err != nil {
		t.Fatalf("instance map %q not found: %v", instID, err)
	}
	owner, _ := inst.Data["ownerAgentId"].(string)
	if owner != executorActorID {
		t.Errorf("instance ownerAgentId = %q, want %q", owner, executorActorID)
	}

	// Scheduler card run_count should be incremented.
	if got := cardDataInt(sched, "run_count"); got != 1 {
		t.Errorf("scheduler run_count = %d, want 1", got)
	}

	// Template run history should have one entry from the scheduler fire.
	runsResp, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl::wf-fire"})
	if err != nil {
		t.Fatalf("list_template_runs: %v", err)
	}
	if got, want := len(runsResp.Runs), 1; got != want {
		t.Fatalf("len(Runs) = %d, want %d", got, want)
	}
	if got, want := runsResp.Runs[0].Source, "scheduler"; got != want {
		t.Errorf("run source = %q, want %q", got, want)
	}
	if got, want := runsResp.Runs[0].SchedulerCardID, "scheduler:wf-fire"; got != want {
		t.Errorf("run SchedulerCardID = %q, want %q", got, want)
	}
	if got, want := runsResp.Runs[0].InstanceMapID, instID; got != want {
		t.Errorf("run InstanceMapID = %q, want %q", got, want)
	}
}

// TestExecuteTimerCard_TwoFiresTwoInstancesVisible verifies that after two
// timer fires of the same workflow-bound scheduler card, BOTH instance maps
// survive the list pipeline (list_all_cards with a workflow filter) — the
// workflow graph must show both, not collapse to one.
func TestExecuteTimerCard_TwoFiresTwoInstancesVisible(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 42)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-two")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-two", TemplateID: "tpl::wf-two",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}
	schedRaw := "---\nid: scheduler:wf-two\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::wf-two\"\n---\n\nFire."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:wf-two", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	// Mock agent ref: handles workflow_start (stamps owner on the instance map)
	// and chat_submit (returns success).
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			req, _ := payload.(gen.AgentWorkflowStartReq)
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
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
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}

	var instIDs []string
	for i := 0; i < 2; i++ {
		if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
			CardID: "scheduler:wf-two", ProjectID: a.actorID,
		}); err != nil {
			t.Fatalf("fire %d: %v", i+1, err)
		}
		sched, _ := a.store.Get("scheduler:wf-two")
		instIDs = append(instIDs, currentInstance(sched))

		// Mark the instance as done so the next fire passes the in-flight guard
		// (executeWorkflowTimerCard rejects a new fire while the previous instance
		// is still active).
		if inst, err := a.store.Get(currentInstance(sched)); err == nil {
			inst.Raw = setCardStatusInRaw(inst.Raw, "done")
			a.store.Save(inst)
		}
	}
	if instIDs[0] == instIDs[1] {
		t.Fatalf("two fires produced identical instance id %q (timestamp collision)", instIDs[0])
	}

	// Store-level: both instance maps exist.
	for i, id := range instIDs {
		if _, err := a.store.Get(id); err != nil {
			t.Fatalf("instance %d (%s) missing from store: %v", i, id, err)
		}
	}

	// List pipeline with a workflow filter (as the frontend sends): both
	// instances must appear.
	listResp, err := a.handleWikiListCards(ctx, domain.WikiListCardsReq{
		Flat:           true,
		WorkflowFilter: &domain.WikiWorkflowFilter{},
	})
	if err != nil {
		t.Fatalf("wiki_list_cards: %v", err)
	}
	visible := map[string]bool{}
	for _, it := range listResp.Cards {
		if strings.HasPrefix(it.ID, "inst::") {
			visible[it.ID] = true
		}
	}
	for i, id := range instIDs {
		if !visible[id] {
			t.Errorf("instance %d (%s) dropped by list pipeline; visible inst:: ids = %v", i, id, visible)
		}
	}
}

// TestExecuteTimerCard_BackwardCompatible verifies that a scheduler card
// WITHOUT a workflow_template continues down the chat.submit path rather than
// the workflow branch.
func TestExecuteTimerCard_BackwardCompatible(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Scheduler card without workflow_template.
	schedRaw := "---\nid: scheduler:plain\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n---\n\nRun."
	a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:plain", Raw: schedRaw})

	// Without a mock workspace, lookupAgentRef for agent:coder fails. The
	// error must mention the executor lookup, NOT workflow instantiation.
	err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{
		CardID: "scheduler:plain", ProjectID: a.actorID,
	})
	if err == nil {
		t.Skip("no error returned — executor resolved unexpectedly in test env")
	}
	if strings.Contains(err.Error(), "instantiate workflow") {
		t.Errorf("backward-compatible card took workflow branch: %v", err)
	}
}

// TestExecuteTimerCard_SkipsWhileRunActive verifies the in-flight guard on the
// chat-submit branch: a fire landing while run_status is running or
// pending_review must be a no-op BEFORE the executor is resolved/submitted.
// In the test env the executor lookup would fail with an error, so a nil
// return proves the guard fired first — this is exactly the ordering that
// prevents duplicate executions on overlapping fires (short cron interval +
// long-running agent turn).
func TestExecuteTimerCard_SkipsWhileRunActive(t *testing.T) {
	for _, status := range []string{timerStatusRunning, timerStatusPendingReview} {
		t.Run(status, func(t *testing.T) {
			tmp := t.TempDir()
			a := newTestActor(tmp)
			ctx := newTestContext(t)

			schedRaw := "---\nid: scheduler:plain\ntype: scheduler\ntags: []\n" +
				"run_status: \"" + status + "\"\n" +
				"last_run: \"2026-08-23T00:00:00Z\"\n" +
				"data:\n  executor: agent:coder\n  schedule:\n    cron: \"* * * * *\"\n---\n\nRun."
			a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:plain", Raw: schedRaw})

			if err := a.handleExecuteTimerCard(ctx, gen.ProjectExecuteTimerCardReq{
				CardID: "scheduler:plain", ProjectID: a.actorID,
			}); err != nil {
				t.Fatalf("fire during %s must be a no-op (guard before submit), got: %v", status, err)
			}

			// The skip must not restamp run state.
			card, err := a.store.Get("scheduler:plain")
			if err != nil {
				t.Fatalf("get card: %v", err)
			}
			if got := frontmatterValue(card.Raw, "last_run"); got != "2026-08-23T00:00:00Z" {
				t.Errorf("last_run restamped on skipped fire: %q", got)
			}
		})
	}
}

// ── advanceExecutor workflow branch ─────────────────────────────────────────

// TestAdvanceExecutor_WorkflowBranch_NotDone verifies that advanceExecutor
// does NOT transition the scheduler card to idle when the instance map is
// still running (status != done). The binding is stored in the data: block to
// confirm data-block storage drives the workflow monitor branch.
func TestAdvanceExecutor_WorkflowBranch_NotDone(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Instance map still doing.
	a.store.Save(&CardRecord{Title: "inst::running", Raw: "---\nid: inst::running\ntype: workflow\nstatus: doing\n---\n\nInstance."})

	// Scheduler card in running state, bound to the instance. Stored directly
	// to bypass scheduler validation (these are operational test fixtures).
	// executor_ref stays at frontmatter top-level (existing timer runtime field
	// read by advanceExecutor via frontmatterValue); workflow_template +
	// current_instance live in the data: block to exercise the dual-path reader.
	schedRaw := "---\nid: scheduler:w\nrun_status: running\n" +
		"executor_ref: \"agent:coder\"\n" +
		"data:\n  workflow_template: \"tpl::wf\"\n  current_instance: \"inst::running\"\n---\n\nRun."
	a.store.Save(&CardRecord{Title: "scheduler:w", Raw: schedRaw})

	card, err := a.store.Get("scheduler:w")
	if err != nil {
		t.Fatalf("get scheduler card: %v", err)
	}
	// Confirm the data-block binding round-tripped into Data and the helper
	// resolves it (proves the workflow branch will be taken).
	if got := workflowTemplate(card); got != "tpl::wf" {
		t.Fatalf("workflowTemplate = %q, want tpl::wf (data-block binding not read)", got)
	}
	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	// Card must still be running — instance not done.
	updated, _ := a.store.Get("scheduler:w")
	if timerState(updated) != timerStatusRunning {
		t.Errorf("run_status = %q, want running (instance not done)", timerState(updated))
	}
}

// TestAdvanceExecutor_WorkflowBranch_Done verifies that advanceExecutor
// transitions the scheduler card to idle and creates a result card when the
// instance map reaches status done.
func TestAdvanceExecutor_WorkflowBranch_Done(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// Instance map marked done.
	a.store.Save(&CardRecord{Title: "inst::done", Raw: "---\nid: inst::done\ntype: workflow\nstatus: done\n---\n\nInstance."})

	// Scheduler card in running state, bound to the instance (no reviewer).
	schedRaw := "---\nid: scheduler:wd\nrun_status: running\n" +
		"executor_ref: \"agent:coder\"\nworkflow_template: \"tpl::wf\"\n" +
		"current_instance: \"inst::done\"\n---\n\nRun."
	a.store.Save(&CardRecord{Title: "scheduler:wd", Raw: schedRaw, Tags: []string{"automation"}})

	card, err := a.store.Get("scheduler:wd")
	if err != nil {
		t.Fatalf("get scheduler card: %v", err)
	}
	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	// Card must transition to idle.
	updated, _ := a.store.Get("scheduler:wd")
	if timerState(updated) != timerStatusIdle {
		t.Errorf("run_status = %q, want idle", timerState(updated))
	}

	// A result card should have been created (scheduler:wd #<timestamp>).
	cards, _ := a.store.List()
	foundResult := false
	for _, c := range cards {
		if strings.HasPrefix(c.Title, "scheduler:wd #") {
			foundResult = true
			if !strings.Contains(c.Body, "- Status: completed") {
				t.Errorf("result card body missing completed status: %q", c.Body)
			}
			break
		}
	}
	if !foundResult {
		t.Error("expected a result card (scheduler:wd #<ts>) after instance done")
	}
}

// ── failure convergence (F1/F2/F3) + N5 provenance ──────────────────────────

// TestAdvanceExecutor_WorkflowBranch_Failed (F1): a failed instance map
// converges the scheduler card to the failed terminal state with a failed
// result card instead of polling forever.
func TestAdvanceExecutor_WorkflowBranch_Failed(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	a.store.Save(&CardRecord{Title: "inst::failed", Raw: "---\nid: inst::failed\ntype: workflow\nstatus: failed\n---\n\nInstance."})
	schedRaw := "---\nid: scheduler:wf\nrun_status: running\n" +
		"executor_ref: \"agent:coder\"\nworkflow_template: \"tpl::wf\"\n" +
		"current_instance: \"inst::failed\"\n---\n\nRun."
	a.store.Save(&CardRecord{Title: "scheduler:wf", Raw: schedRaw, Tags: []string{"automation"}})

	card, err := a.store.Get("scheduler:wf")
	if err != nil {
		t.Fatalf("get scheduler card: %v", err)
	}
	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor: %v", err)
	}

	updated, _ := a.store.Get("scheduler:wf")
	if timerState(updated) != timerStatusFailed {
		t.Errorf("run_status = %q, want failed", timerState(updated))
	}

	cards, _ := a.store.List()
	foundFailedResult := false
	for _, c := range cards {
		if strings.HasPrefix(c.Title, "scheduler:wf #") {
			foundFailedResult = true
			if !strings.Contains(c.Body, "- Status: failed") {
				t.Errorf("result card body missing failed status: %q", c.Body)
			}
			break
		}
	}
	if !foundFailedResult {
		t.Error("expected a failed result card after instance failed")
	}
}

// TestAdvanceExecutor_WorkflowBranch_InstanceMissing (F1): a deleted instance
// map converges the scheduler to failed instead of erroring forever.
func TestAdvanceExecutor_WorkflowBranch_InstanceMissing(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	// No instance map is created — current_instance dangles.
	schedRaw := "---\nid: scheduler:gone\nrun_status: running\n" +
		"executor_ref: \"agent:coder\"\nworkflow_template: \"tpl::wf\"\n" +
		"current_instance: \"inst::deleted\"\n---\n\nRun."
	a.store.Save(&CardRecord{Title: "scheduler:gone", Raw: schedRaw, Tags: []string{"automation"}})

	card, err := a.store.Get("scheduler:gone")
	if err != nil {
		t.Fatalf("get scheduler card: %v", err)
	}
	if err := a.advanceExecutor(ctx, card); err != nil {
		t.Fatalf("advanceExecutor must converge, not error: %v", err)
	}

	updated, _ := a.store.Get("scheduler:gone")
	if timerState(updated) != timerStatusFailed {
		t.Errorf("run_status = %q, want failed (deleted instance converges)", timerState(updated))
	}
}

// workflowFireFixture assembles a scheduler card bound to a real template and
// returns a fake context whose mock agent ref drives workflow_start. When
// startFails is true the mock returns an error instead of stamping the owner.
func workflowFireFixture(t *testing.T, mapID string, startFails bool) (*Actor, actor.PureContext, string) {
	t.Helper()
	tmp := t.TempDir()
	a := newTestActor(tmp)

	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 43)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, mapID)
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: mapID, TemplateID: "tpl::" + mapID,
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	schedID := "scheduler:" + mapID
	schedRaw := "---\nid: " + schedID + "\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::" + mapID + "\"\n---\n\nFire."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: schedID, Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	// Mock agent ref: handles workflow_start (stamps owner on the instance map)
	// and chat_submit (returns success).
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			if startFails {
				return fmt.Errorf("simulated workflow_start failure")
			}
			req, ok := payload.(gen.AgentWorkflowStartReq)
			if !ok {
				if p, ok2 := payload.(*gen.AgentWorkflowStartReq); ok2 {
					req = *p
				} else {
					return fmt.Errorf("unexpected workflow_start payload type %T", payload)
				}
			}
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
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
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}
	return a, fctx, schedID
}

// TestExecuteTimerCard_WorkflowBranch_LiquidatesStaleInstance (F2): when a new
// fire supersedes a current_instance that is still active, the stale instance
// is liquidated to failed before the pointer is overwritten.
func TestExecuteTimerCard_WorkflowBranch_LiquidatesStaleInstance(t *testing.T) {
	a, fctx, schedID := workflowFireFixture(t, "wf-stale", false)

	// Seed a stale active instance plus the scheduler binding pointing at it,
	// exactly the state a crash between start and completion would leave.
	staleRaw := "---\nid: inst::old\ntype: workflow\nstatus: doing\n---\n\nOld."
	a.store.Save(&CardRecord{Title: "inst::old", Raw: staleRaw})
	sched, _ := a.store.Get(schedID)
	sched.Raw = setCardDataStringInRaw(sched.Raw, "current_instance", "inst::old")
	a.store.Save(sched)

	if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
		CardID: schedID, ProjectID: a.actorID,
	}); err != nil {
		t.Fatalf("execute timer card: %v", err)
	}

	// The stale instance must be failed, and current_instance must point at
	// the NEW instance (both ids differ).
	old, err := a.store.Get("inst::old")
	if err != nil {
		t.Fatalf("stale instance vanished: %v", err)
	}
	if old.Status != "failed" {
		t.Errorf("stale instance status = %q, want failed (must be liquidated)", old.Status)
	}
	updated, _ := a.store.Get(schedID)
	if got := currentInstance(updated); got == "" || got == "inst::old" {
		t.Errorf("current_instance = %q, want a fresh instance id", got)
	}
}

// TestExecuteTimerCard_WorkflowBranch_StartFailure (F3): when workflow_start
// fails after instantiation, the fresh instance is NOT marked failed immediately
// because workflow_start is fire-and-forget (deadlock elimination). The
// scheduler monitor detects the stalled instance via poll and handles it.
func TestExecuteTimerCard_WorkflowBranch_StartFailure(t *testing.T) {
	a, fctx, schedID := workflowFireFixture(t, "wf-startfail", true)

	// In the new code, workflow_start is fire-and-forget, so the error is not
	// propagated. The function returns nil (success) because the local spawn
	// succeeded. The instance stays "doing" and the scheduler monitor detects
	// the stalled instance via poll.
	err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
		CardID: schedID, ProjectID: a.actorID,
	})
	if err != nil {
		t.Fatalf("execute timer card should succeed (workflow_start is fire-and-forget): %v", err)
	}

	updated, _ := a.store.Get(schedID)
	if timerState(updated) != timerStatusRunning {
		t.Errorf("run_status = %q, want running (local spawn succeeded)", timerState(updated))
	}
	instID := currentInstance(updated)
	if instID == "" {
		t.Fatal("current_instance empty after fire")
	}
	inst, gerr := a.store.Get(instID)
	if gerr != nil {
		t.Fatalf("instance map %q not found: %v", instID, gerr)
	}
	// The instance is still "doing" because workflow_start (which would set the
	// owner and mark the instance as active) was fire-and-forget and the mock
	// returned an error that was never consumed. The scheduler monitor will
	// detect the stalled instance and handle it.
	if inst.Status != "doing" {
		t.Errorf("instance status = %q, want doing (workflow_start was fire-and-forget, error not propagated)", inst.Status)
	}
}

// TestWikiTemplateInstantiate_SchedulerCardIDStamp (N5): the instance map
// records scheduler_card_id only when the fire was timer-driven; manual
// instantiation carries no such field.
func TestWikiTemplateInstantiate_SchedulerCardIDStamp(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-n5")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-n5", TemplateID: "tpl::wf-n5",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	manual, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl::wf-n5", InstanceMapID: "inst::m1::tpl::wf-n5", Source: "manual",
	})
	if err != nil {
		t.Fatalf("instantiate (manual): %v", err)
	}
	if _, ok := manual.InstanceMap.Data["scheduler_card_id"]; ok {
		t.Error("manual instance must not carry scheduler_card_id")
	}

	driven, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl::wf-n5", InstanceMapID: "inst::s1::tpl::wf-n5",
		SchedulerCardID: "scheduler:wf-n5", Source: "scheduler",
	})
	if err != nil {
		t.Fatalf("instantiate (scheduler): %v", err)
	}
	got, _ := driven.InstanceMap.Data["scheduler_card_id"].(string)
	if got != "scheduler:wf-n5" {
		t.Errorf("scheduler_card_id = %q, want scheduler:wf-n5", got)
	}
}

// TestHandleWikiDeleteCard_CascadeDeletesSchedulerInstances verifies that
// deleting a scheduler card cascade-deletes all its instance workflows (maps,
// task cards, topo snapshots, and run log entries).
func TestHandleWikiDeleteCard_CascadeDeletesSchedulerInstances(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 42)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-cd")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-cd", TemplateID: "tpl::wf-cd",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	schedRaw := "---\nid: scheduler:wf-cd\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::wf-cd\"\n---\n\nFire."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:wf-cd", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	// Mock agent ref: handles workflow_start (stamps owner on the instance map)
	// and chat_submit (returns success).
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			req, _ := payload.(gen.AgentWorkflowStartReq)
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
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
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}

	var instIDs []string
	for i := 0; i < 2; i++ {
		if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
			CardID: "scheduler:wf-cd", ProjectID: a.actorID,
		}); err != nil {
			t.Fatalf("fire %d: %v", i+1, err)
		}
		sched, _ := a.store.Get("scheduler:wf-cd")
		instIDs = append(instIDs, currentInstance(sched))

		// Mark the instance as done so the next fire passes the in-flight guard.
		if inst, err := a.store.Get(currentInstance(sched)); err == nil {
			inst.Raw = setCardStatusInRaw(inst.Raw, "done")
			a.store.Save(inst)
		}
	}

	// Collect task card IDs from each instance map before deletion.
	type tidPair struct{ instID, taskID string }
	var taskPairs []tidPair
	for _, instID := range instIDs {
		allCards, _ := a.store.List()
		items := make([]domain.MonoCardListItem, 0, len(allCards))
		for _, c := range allCards {
			items = append(items, cardToListItem(c))
		}
		var mapItem domain.MonoCardListItem
		for _, item := range items {
			if item.ID == instID {
				mapItem = item
				break
			}
		}
		for _, tid := range workflowTaskIDs(mapItem, items) {
			taskPairs = append(taskPairs, tidPair{instID: instID, taskID: tid})
		}
	}

	// Verify run log has entries before deletion.
	runsBefore, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl::wf-cd"})
	if err != nil {
		t.Fatalf("list_template_runs before: %v", err)
	}
	if got, want := len(runsBefore.Runs), 2; got != want {
		t.Fatalf("len(Runs) before delete = %d, want %d", got, want)
	}

	// Delete the scheduler card — this triggers cascade deletion.
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: "scheduler:wf-cd"}); err != nil {
		t.Fatalf("delete scheduler card: %v", err)
	}

	// Assert: instance maps are gone.
	for _, id := range instIDs {
		if _, err := a.store.Get(id); err == nil {
			t.Errorf("instance map %q still exists after cascade delete", id)
		} else if !errors.Is(err, ErrCardNotFound) {
			t.Errorf("instance map %q: unexpected error: %v", id, err)
		}
	}

	// Assert: task cards are gone.
	for _, pair := range taskPairs {
		if _, err := a.store.Get(pair.taskID); err == nil {
			t.Errorf("task card %q (instance %q) still exists after cascade delete", pair.taskID, pair.instID)
		} else if !errors.Is(err, ErrCardNotFound) {
			t.Errorf("task card %q: unexpected error: %v", pair.taskID, err)
		}
	}

	// Assert: workflow_topo graph for the deleted instance maps is empty (no
	// map card, no nodes). In the stateless model the graph is derived from
	// cards — there is no separate snapshot to delete.
	for _, id := range instIDs {
		resp, err := a.handleGraphGet(ctx, gen.ProjectGraphGetReq{
			GraphKind: GraphKindWorkflowTopo, ID: id,
		})
		if err != nil {
			t.Errorf("workflow_topo graph for %q should be readable (empty) after cascade delete: %v", id, err)
			continue
		}
		var g WorkflowTopoGraph
		if err := json.Unmarshal([]byte(resp.EnvelopeText), &g); err != nil {
			t.Errorf("unmarshal graph for %q: %v", id, err)
			continue
		}
		if len(g.Nodes) != 0 {
			t.Errorf("workflow_topo graph for %q should be empty (map deleted), got %d nodes", id, len(g.Nodes))
		}
	}

	// Assert: run log entries for the template are empty.
	runsAfter, err := a.handleWikiListTemplateRuns(ctx, domain.WikiListTemplateRunsReq{TemplateMapID: "tpl::wf-cd"})
	if err != nil {
		t.Fatalf("list_template_runs after: %v", err)
	}
	if got, want := len(runsAfter.Runs), 0; got != want {
		t.Errorf("len(Runs) after cascade delete = %d, want 0", got)
	}
}

// TestHandleWikiDeleteCard_InstanceGuard verifies that deleting a workflow
// instance map while its scheduler card still exists is rejected with a
// "belongs to scheduler" error.
func TestHandleWikiDeleteCard_InstanceGuard(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)

	executorCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 42)
	if err != nil {
		t.Fatalf("executor CID: %v", err)
	}
	executorActorID := executorCID.String()

	ctx := newTestContext(t)
	createWorkflowMapWithTask(t, a, ctx, "wf-guard")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-guard", TemplateID: "tpl::wf-guard",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	schedRaw := "---\nid: scheduler:wf-guard\ntype: scheduler\ntags: []\n" +
		"data:\n  executor: agent:coder\n  schedule:\n    cron: \"0 9 * * *\"\n" +
		"  workflow_template: \"tpl::wf-guard\"\n---\n\nGuard."
	if _, err := a.handleWikiCreateCard(ctx, domain.WikiCreateCardReq{ID: "scheduler:wf-guard", Raw: schedRaw}); err != nil {
		t.Fatalf("create scheduler card: %v", err)
	}

	fctx := testutil.AdminCtx(testutil.GenActorID())
	// Mock agent ref: handles workflow_start (stamps owner on the instance map)
	// and chat_submit (returns success).
	agentRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		switch callID {
		case "workflow_start":
			req, _ := payload.(gen.AgentWorkflowStartReq)
			if _, err := a.handleWikiSetMapOwner(fctx, domain.WikiSetMapOwnerReq{
				MapID: req.MapCardID, OwnerActorID: executorActorID,
			}); err != nil {
				return err
			}
			return gen.AgentWorkflowStartResp{MapCardID: req.MapCardID}
		case "chat_submit":
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
		if aid == agentRef.ID() {
			return agentRef, true
		}
		return nil, false
	}

	if err := a.handleExecuteTimerCard(fctx, gen.ProjectExecuteTimerCardReq{
		CardID: "scheduler:wf-guard", ProjectID: a.actorID,
	}); err != nil {
		t.Fatalf("execute timer card: %v", err)
	}
	sched, _ := a.store.Get("scheduler:wf-guard")
	instID := currentInstance(sched)
	if instID == "" {
		t.Fatal("no instance created")
	}

	// Attempt to delete the instance map while the scheduler still exists.
	_, err = a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: instID})
	if err == nil {
		t.Fatal("expected error when deleting instance map with living scheduler, got nil")
	}
	if !strings.Contains(err.Error(), "belongs to scheduler") {
		t.Errorf("error = %q, want containing \"belongs to scheduler\"", err.Error())
	}

	// Verify the instance map still exists.
	if _, err := a.store.Get(instID); err != nil {
		t.Errorf("instance map %q was deleted despite guard error: %v", instID, err)
	}
}

// TestHandleWikiDeleteCard_ManualInstanceDeletable verifies that a manually
// instantiated workflow map (no scheduler_card_id) can be deleted normally.
func TestHandleWikiDeleteCard_ManualInstanceDeletable(t *testing.T) {
	tmp := t.TempDir()
	a := newTestActor(tmp)
	ctx := newTestContext(t)

	createWorkflowMapWithTask(t, a, ctx, "wf-man")
	if _, err := a.handleWikiTemplateSave(ctx, domain.WikiTemplateSaveReq{
		MapID: "wf-man", TemplateID: "tpl::wf-man",
	}); err != nil {
		t.Fatalf("template_save: %v", err)
	}

	manual, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID: "tpl::wf-man", InstanceMapID: "inst::manual::tpl::wf-man", Source: "manual",
	})
	if err != nil {
		t.Fatalf("instantiate (manual): %v", err)
	}
	instID := manual.InstanceMap.ID

	// Delete the instance map — should succeed (no scheduler_card_id).
	if _, err := a.handleWikiDeleteCard(ctx, domain.WikiDeleteCardReq{ID: instID}); err != nil {
		t.Fatalf("delete manual instance map: %v", err)
	}

	// Verify the instance map is gone.
	if _, err := a.store.Get(instID); err == nil {
		t.Errorf("manual instance map %q still exists after delete", instID)
	} else if !errors.Is(err, ErrCardNotFound) {
		t.Errorf("manual instance map %q: unexpected error: %v", instID, err)
	}
}
