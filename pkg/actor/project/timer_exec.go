package project

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const schedulerCardPrefix = "scheduler:"
const monitorInterval = 5 * time.Second

// globalPermissionMode reads the global permission mode from account
// preferences via the workspace service. Returns "" when unset or on error;
// the agent's normalizePermissionMode defaults that to "permission".
func (a *Actor) globalPermissionMode(ctx actor.PureContext) string {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return ""
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), workflowPollTimeout)
	defer cancel()
	call := wsRef.Invoke(callCtx, "workspace.preferences_get", nil)
	if call == nil {
		return ""
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil {
		return ""
	}
	prefs, ok := v.(domain.AccountPreferencesSnapshot)
	if !ok {
		return ""
	}
	if prefs.Preferences == nil {
		return ""
	}
	return prefs.Preferences["permissionMode"]
}

// pendingExecution tracks an in-flight timer-card execution.
type pendingExecution struct {
	CardID          string
	ExecutorActorID string
	ReviewerActorID string // empty if no reviewer
	Phase           string // "executor" | "reviewer"
	StartedAt       time.Time
	Tags            []string // original card tags for result card
}

// handleExecuteTimerCard is called by the scheduler actor when a timer fires.
// It reads the card, resolves the fire branch, and dispatches to the matching
// execution path: chat.submit for prompt cards, workflow instantiation for
// workflow cards, or workspace.agent_pause/agent_resume for agent_action
// cards. agent_action is dispatched before executor resolution because those
// cards carry no executor — the target agent lives in data.target_agent.
// Unified task cards dispatch the same way: prompt-mode task cards always run
// on a freshly created agent (ephemeral semantics, no executor/bound_agent
// required), template-mode task cards instantiate the bound workflow template.
func (a *Actor) handleExecuteTimerCard(ctx actor.PureContext, req gen.ProjectExecuteTimerCardReq) error {
	ctx.Logger().Info("project: execute timer card", "card", req.CardID)

	card, err := a.store.Get(req.CardID)
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: read card %s: %w", req.CardID, err)
	}

	// Execution branch: the scheduler card's effective schedule_type decides
	// between instantiating a workflow template, submitting the card body, and
	// firing an agent_action (pause/resume). scheduleTypeOf applies the legacy
	// derivation for old cards, so the explicit-value and empty-value cases
	// both converge here.
	kind, tpl, err := timerFireBranch(card)
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: %w", err)
	}

	// Unified agent_actions list. It is fired after the primary branch, and
	// for prompt-mode task cards that carry only actions (no body/template) it
	// is the entire fire.
	actions := agentActionsOf(card)

	if kind == fireAgentAction {
		return a.executeAgentActionTimerCard(ctx, card)
	}

	// prompt-mode task cards whose only payload is a list of agent_actions
	// have no primary branch to run; skip the empty-body prompt path.
	skipPrimary := kind == firePrompt && isPromptTaskCard(card) && strings.TrimSpace(card.Body) == "" && len(actions) > 0

	// agent_task cards carry their own agent resolution (bound_agent or a
	// fresh ephemeral agent) and never route through the executor fields, so
	// they dispatch before resolveAgentRefs — mirroring agent_action.
	if kind == fireAgentTask {
		if err := a.executeAgentTaskTimerCard(ctx, card); err != nil {
			return err
		}
		return a.executeAgentActions(ctx, card, actions)
	}

	// Unified task cards in prompt mode always run on a freshly created agent
	// (ephemeral semantics): no executor binding and no bound_agent is
	// required — an unbound task must never fail its fire — and the run agent
	// is recorded on the card (data.last_run_agent) so the timer list and
	// badges can display it. Template-mode task cards fall through to the
	// workflow branch below.
	if kind == firePrompt && isPromptTaskCard(card) {
		if !skipPrimary {
			if err := a.executeEphemeralAgentTask(ctx, card); err != nil {
				return err
			}
		}
		return a.executeAgentActions(ctx, card, actions)
	}

	executorRef, reviewerRef, err := a.resolveAgentRefs(ctx, card.Data, card.Raw)
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: %w", err)
	}
	if executorRef == "" && !isTaskCard(card) {
		return fmt.Errorf("project.execute_timer_card: no executor agent specified in card Data")
	}

	if kind == fireWorkflow {
		if err := a.executeWorkflowTimerCard(ctx, card, tpl, executorRef, reviewerRef); err != nil {
			return err
		}
		return a.executeAgentActions(ctx, card, actions)
	}

	// Remaining legacy prompt-mode cards (schedule_type=prompt or unset).
	// In-flight guard: a fire landing while the previous run is still
	// executing (or awaiting review) must be a no-op — checking after the
	// submit would re-send the body to the agent on overlapping fires
	// (short cron interval + long-running agent turn).
	if runStatus := timerState(card); runStatus == timerStatusRunning || runStatus == timerStatusPendingReview {
		ctx.Logger().Info("project: timer fire skipped (previous run still active)", "card", req.CardID, "run_status", runStatus)
		return nil
	}

	// Submit card body to executor agent. If the agent is registered but
	// not yet loaded, spawn it locally. If it is not registered at all,
	// the frontend must create it via workspace.create_agent first.
	agentRef, ok := a.lookupAgentRef(ctx, executorRef)
	if !ok {
		spawnedRef, spawnErr := a.ensureExecutorAgent(ctx, executorRef)
		if spawnErr != nil {
			return fmt.Errorf("project.execute_timer_card: executor agent %s is not registered in this project — create it first or bind an existing agent: %w", executorRef, spawnErr)
		}
		agentRef = spawnedRef
	}

	// Persist the running marker BEFORE dispatching chat_submit (audit #10).
	// run_status is both the in-flight guard's key and the monitor's trigger;
	// a fire that submitted the body but failed to persist running would
	// repeat the submit on the next tick and leak the run from the monitor.
	// save failure therefore aborts before any side effect.
	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"last_run":     timerNow(),
		"executor_ref": executorRef,
		"reviewer_ref": reviewerRef,
	}); err != nil {
		return fmt.Errorf("project.execute_timer_card: persist running state: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	call := agentRef.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{
		Text: card.Body,
	})
	if call == nil {
		return fmt.Errorf("project.execute_timer_card: agent.chat.submit returned nil call")
	}
	_ = call.Close()

	return a.executeAgentActions(ctx, card, actions)
}

// executeWorkflowTimerCard handles the workflow branch of a timer fire. It
// instantiates a fresh workflow instance from the bound template, binds the
// executor agent as the instance's owner (activating frontier-driven
// execution), and transitions the scheduler card to running. The instance map
// id is recorded as current_instance so the monitor can track its completion.
//
// Failure convergence: once the instance map exists it is never left as a
// dangling artifact — a stale current_instance being superseded is liquidated
// to failed (F2), and any failure in the start path marks the fresh instance
// failed and transitions run_status to the failed terminal state (F3), so the
// scheduler never polls a dead instance forever.
func (a *Actor) executeWorkflowTimerCard(ctx actor.PureContext, card *CardRecord, tplID, executorRef, reviewerRef string) error {
	// In-flight guard: if the previous run is still executing (running) or is
	// awaiting review (pending_review), reject the new trigger so a short cron
	// or manual run-now cannot clobber an active instance/review. pending_review
	// means the instance already reached a terminal status but its review turn
	// is live — a fresh fire would overwrite current_instance and orphan that
	// review, so it is skipped outright. For running, the fire is rejected only
	// while the instance is non-terminal; a terminal/gone instance self-heals
	// (the monitor path owns convergence). Mirrors the prompt-mode dual-state
	// guard in handleExecuteTimerCard (audit #4).
	if runStatus := timerState(card); runStatus == timerStatusRunning || runStatus == timerStatusPendingReview {
		if runStatus == timerStatusPendingReview {
			ctx.Logger().Info("project: workflow timer fire skipped (previous run awaiting review)", "card", card.Title)
			return nil
		}
		if instID := currentInstance(card); instID != "" {
			if inst, err := a.store.Get(instID); err == nil && inst.Status != "done" && inst.Status != "failed" {
				return fmt.Errorf("project.execute_timer_card: instance %q is still running", instID)
			}
		}
	}

	instanceMapID := workflowInstanceID(tplID)

	if _, err := a.handleWikiTemplateInstantiate(ctx, domain.WikiTemplateInstantiateReq{
		TemplateMapID:   tplID,
		InstanceMapID:   instanceMapID,
		Source:          "scheduler",
		SchedulerCardID: card.Title,
	}); err != nil {
		return fmt.Errorf("project.execute_timer_card: instantiate workflow template %q: %w", tplID, err)
	}

	// F2: liquidate a stale current_instance before overwriting it. If the
	// previous fire's instance map is still active (neither done nor failed),
	// mark it failed so an orphaned instance never lingers as dirty data while
	// the scheduler card moves on to a new fire.
	if oldInstID := currentInstance(card); oldInstID != "" && oldInstID != instanceMapID {
		if oldInst, err := a.store.Get(oldInstID); err == nil {
			a.markCardFailed(oldInst)
		} else {
			// Old instance already gone — nothing to liquidate.
			ctx.Logger().Warn("project: stale current_instance unreadable, skipping liquidation", "card", card.Title, "instance", oldInstID, "error", err)
		}
	}

	// Spawn the agent locally and activate the workflow directly on the
	// agent, bypassing the workspace.workflow_start round trip that used to
	// deadlock the project loop (project→workspace→loadAgentByID→
	// spawnAgentViaProject→project.spawn_agent). The agent's
	// activateWorkflow function calls back into project (wiki_set_map_owner,
	// workflow_create_worktree), so the workflow_start invoke is fire-and-
	// forget — the project loop must be free to process those callbacks.
	ownerActorID, ok := a.resolveExecutorActorID(ctx, executorRef)
	if !ok {
		return a.failWorkflowFire(ctx, card, instanceMapID, fmt.Sprintf("executor agent %q not found", executorRef), nil)
	}

	// Find the full agent info from the workspace registry for spawn
	// parameters. listProjectAgents is a read-only query that does not
	// call back to project, so it is safe from the deadlock perspective.
	var agent gen.AgentRef
	for _, ag := range a.listProjectAgents(ctx) {
		if ag.ActorID == ownerActorID || ag.ID == ownerActorID {
			agent = ag
			break
		}
	}
	if agent.ID == "" {
		return a.failWorkflowFire(ctx, card, instanceMapID, fmt.Sprintf("executor agent %q not found in project agents", executorRef), nil)
	}

	// Look up the agent's bound worktree (if any).
	worktreeID := ""
	a.bindingMu.RLock()
	worktreeID = a.agentWorktree[agent.ActorID]
	a.bindingMu.RUnlock()

	// Spawn the agent locally on the project owner loop. This avoids the
	// deadlock cycle that occurred when workspace.workflow_start called
	// loadAgentByID → spawnAgentViaProject → project.spawn_agent while
	// the project loop was already occupied by executeWorkflowTimerCard.
	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:      agent.ID,
		ProjectID:      a.actorID,
		AgentKind:      agent.AgentKind,
		WorkspaceID:    "",
		ActorID:        agent.ActorID,
		DisplayName:    agent.DisplayName,
		Primary:        agent.Primary,
		Fast:           agent.Fast,
		Execution:      agent.Execution,
		Review:         agent.Review,
		Summary:        agent.Summary,
		WorktreeID:     worktreeID,
		ParentAgentID:  agent.ParentAgentID,
		PermissionMode: a.globalPermissionMode(ctx),
	})
	if err != nil {
		return a.failWorkflowFire(ctx, card, instanceMapID, "local spawn", err)
	}

	// Persist the worktree binding so the agent's file/git/shell calls
	// route to the correct worktree even after a process restart.
	if worktreeID != "" {
		if err := a.persistWorktreeManifest(worktreeID); err != nil {
			ctx.Logger().Error("project: persist worktree manifest after local spawn failed", "error", err)
		}
	}

	// Resolve the spawned agent's ref so we can invoke workflow_start and
	// chat_submit directly on it (bypassing workspace entirely).
	cid, err := identity.ParseCanonicalID(resp.ActorID)
	if err != nil {
		return a.failWorkflowFire(ctx, card, instanceMapID, fmt.Sprintf("invalid actor id %q", resp.ActorID), err)
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return a.failWorkflowFire(ctx, card, instanceMapID, fmt.Sprintf("spawned agent %q not addressable", resp.ActorID), nil)
	}

	// Fire-and-forget activate the workflow on the agent. The agent's
	// activateWorkflow synchronously calls project.wiki_set_map_owner and
	// project.workflow_create_worktree; since the project loop is free
	// (we are not waiting for a response), those calls will be processed
	// normally. The 10s context timeout caps a wedged activation.
	actCtx, actCancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer actCancel()
	actCall := agentRef.Invoke(actCtx, "workflow_start", gen.AgentWorkflowStartReq{MapCardID: instanceMapID})
	if actCall != nil {
		_ = actCall.Close()
	}

	// Fire-and-forget kick off the first orchestration turn. Delivered
	// after the workflow_start message so the agent processes activation
	// before the turn start.
	kickoff := fmt.Sprintf("开始执行 workflow 地图 %q：读取 frontier，编排并推进任务卡片。", instanceMapID)
	chatCall := agentRef.Invoke(ctx.Lifecycle(), "chat_submit", gen.AgentChatSubmitReq{Text: kickoff})
	if chatCall != nil {
		_ = chatCall.Close()
	}

	// Fire-and-forget notify workspace to update its in-memory agent
	// registry with the fresh ActorID and LoadState.
	wsRef, ok := ctx.LookupService("workspace")
	if ok && wsRef != nil {
		notifyCtx, notifyCancel := context.WithTimeout(ctx.Lifecycle(), notifyTimeout)
		defer notifyCancel()
		notifyCall := wsRef.Invoke(notifyCtx, "workspace.agent_loaded", gen.WorkspaceAgentLoadedReq{
			AgentID:   agent.ID,
			ActorID:   resp.ActorID,
			ProjectID: a.actorID,
		})
		if notifyCall != nil {
			_ = notifyCall.Close()
		}
	}
	ctx.Logger().Info("project: workflow timer card activated locally", "card", card.Title, "instance", instanceMapID, "actorID", resp.ActorID)

	now := timerNow()
	// current_instance lives in the data: block alongside workflow_template
	// (same rationale: it must round-trip into CardRecord.Data for the
	// frontend). saveTimerState also writes into the data block, so order no
	// longer matters; stamp the data field first for clarity.
	card.Raw = setCardDataStringInRaw(card.Raw, "current_instance", instanceMapID)

	// Increment run_count on the scheduler card.
	runCount := cardDataInt(card, "run_count") + 1
	card.Raw = setCardDataIntInRaw(card.Raw, "run_count", runCount)

	if err := a.saveTimerState(card, map[string]string{
		"run_status":   timerStatusRunning,
		"last_run":     now,
		"executor_ref": executorRef,
		"reviewer_ref": reviewerRef,
	}); err != nil {
		return fmt.Errorf("project.execute_timer_card: persist running state: %w", err)
	}
	return nil
}

// executeAgentActionTimerCard handles the legacy agent_action branch of a timer
// fire. It reads the single-value agent_action + target_agent pair and invokes
// workspace.agent_pause/agent_resume. New unified cards use executeAgentActions
// after their primary branch; this function remains for backward compatibility
// with schedule_type=agent_action cards.
func (a *Actor) executeAgentActionTimerCard(ctx actor.PureContext, card *CardRecord) error {
	agentAction := cardDataString(card, "agent_action")
	if agentAction != "pause" && agentAction != "resume" {
		return fmt.Errorf("project.execute_timer_card: agent_action %q must be pause or resume", agentAction)
	}
	targetAgent := cardDataString(card, "target_agent")
	if targetAgent == "" {
		targetAgent = frontmatterValue(card.Raw, "target_agent")
	}
	if targetAgent == "" {
		return fmt.Errorf("project.execute_timer_card: target_agent is required for agent_action cards")
	}

	if err := a.executeSingleAgentAction(ctx, card.Title, agentAction, targetAgent); err != nil {
		return err
	}

	now := timerNow()
	runCount := cardDataInt(card, "run_count") + 1
	card.Raw = setCardDataIntInRaw(card.Raw, "run_count", runCount)

	return a.saveTimerState(card, map[string]string{
		"run_status": timerStatusIdle,
		"last_run":   now,
	})
}

// executeSingleAgentAction resolves the target agent and invokes
// workspace.agent_pause or workspace.agent_resume once. It is the shared primitive
// for both the legacy agent_action branch and the unified agent_actions list.
// CallerAgentID carries the target's own ActorID so the workspace
// requireOwnerOrSelf check authorizes the scheduler's self-action request.
func (a *Actor) executeSingleAgentAction(ctx actor.PureContext, cardID, action, target string) error {
	if action != "pause" && action != "resume" {
		return fmt.Errorf("project.execute_timer_card: agent_action %q must be pause or resume", action)
	}
	if target == "" {
		return fmt.Errorf("project.execute_timer_card: target_agent is required for agent_action cards")
	}

	actorID, ok := a.resolveExecutorActorID(ctx, target)
	if !ok {
		return fmt.Errorf("project.execute_timer_card: target agent %q not found", target)
	}

	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return fmt.Errorf("project.execute_timer_card: workspace service unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()

	callable := "workspace.agent_pause"
	var payload any = gen.AgentPauseReq{
		ToAgentID:     actorID,
		CallerAgentID: actorID,
	}
	if action == "resume" {
		callable = "workspace.agent_resume"
		payload = gen.AgentResumeReq{
			ToAgentID:     actorID,
			CallerAgentID: actorID,
		}
	}

	call := wsRef.Invoke(callCtx, callable, payload)
	if call == nil {
		return fmt.Errorf("project.execute_timer_card: %s returned nil call", callable)
	}
	if _, err := call.Final(callCtx); err != nil {
		return fmt.Errorf("project.execute_timer_card: %s: %w", callable, err)
	}
	ctx.Logger().Info("project: scheduler agent action fired", "card", cardID, "action", action, "target", target, "actorID", actorID)
	return nil
}

// executeAgentActions runs the unified agent_actions list on a scheduler card.
// It is invoked after the primary fire branch (prompt/template/agent_task) so
// that actions ride along with the scheduled work, and it is also the entire
// fire for cards that carry only agent_actions. When no primary branch set a
// running state, the scheduler card is bumped to idle with last_run/run_count
// updated.
func (a *Actor) executeAgentActions(ctx actor.PureContext, card *CardRecord, actions []agentAction) error {
	if len(actions) == 0 {
		return nil
	}
	var actionErr error
	for _, act := range actions {
		if err := a.executeSingleAgentAction(ctx, card.Title, act.Action, act.Target); err != nil {
			actionErr = err
			break
		}
	}
	// Only mutate run state when the fire was actions-only. If a primary branch
	// ran it already stamped running/pending_review and the monitor owns the
	// completion.
	if timerState(card) != timerStatusIdle {
		return actionErr
	}
	// Bump the run bookkeeping even when an action failed midway (audit #11):
	// the fire did happen, so run_count/last_run must reflect it — otherwise a
	// persistently failing action leaves the card looking never-run.
	now := timerNow()
	runCount := cardDataInt(card, "run_count") + 1
	card.Raw = setCardDataIntInRaw(card.Raw, "run_count", runCount)
	if err := a.saveTimerState(card, map[string]string{
		"run_status": timerStatusIdle,
		"last_run":   now,
	}); err != nil {
		if actionErr != nil {
			return actionErr
		}
		return err
	}
	return actionErr
}

// failWorkflowFire converges a timer fire that failed after instance creation
// (F3): it marks the freshly instantiated instance map as failed (audit
// trail), points current_instance at it, and transitions the scheduler card's
// run_status to the failed terminal state so the monitor stops polling. It
// returns a wrapped error so the scheduler still sees why the fire failed.
func (a *Actor) failWorkflowFire(ctx actor.PureContext, card *CardRecord, instanceMapID, cause string, err error) error {
	if inst, gerr := a.store.Get(instanceMapID); gerr == nil {
		a.markCardFailed(inst)
	}
	card.Raw = setCardDataStringInRaw(card.Raw, "current_instance", instanceMapID)
	if serr := a.saveTimerState(card, map[string]string{"run_status": timerStatusFailed}); serr != nil {
		ctx.Logger().Error("project: persist failed run_status after workflow start failure", "card", card.Title, "error", serr)
	}
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: %s: %w", cause, err)
	}
	return fmt.Errorf("project.execute_timer_card: %s", cause)
}

// timerFireKind is the execution branch a scheduler card fire takes. The pure
// timerFireBranch derives it from the effective schedule_type so
// handleExecuteTimerCard can dispatch agent_action before resolving executor
// refs (agent_action cards carry no executor).
type timerFireKind int

const (
	firePrompt      timerFireKind = iota // card body goes to chat.submit
	fireWorkflow                         // workflow_template is instantiated
	fireAgentAction                      // workspace.agent_pause / agent_resume fires
	fireAgentTask                        // dual-mode scheduler: bound / ephemeral agent run
)

// timerFireBranch returns the execution branch for a scheduler card fire. The
// workflow branch also returns the template id to instantiate; prompt and
// agent_action branches return no template. This is a pure function; the
// caller feeds the result into executeWorkflowTimerCard, the chat.submit path,
// or executeAgentActionTimerCard.
func timerFireBranch(card *CardRecord) (kind timerFireKind, tpl string, err error) {
	switch scheduleTypeOf(card) {
	case scheduleTypeTask:
		// Unified task type: the mode is derived from the template binding —
		// template mode instantiates the workflow template exactly like a
		// legacy workflow card, prompt mode fires the body via a fresh agent.
		if tpl := workflowTemplate(card); tpl != "" {
			return fireWorkflow, tpl, nil
		}
		// A prompt-mode task card that explicitly opts into the dual-mode
		// scheduler contract (data.bind_mode = bound|ephemeral) fires as an
		// agent_task, mirroring the prompt-type opt-in: bound reuses a stable
		// agent, ephemeral keeps the fresh-agent-per-fire default. Action-only
		// cards (empty body) keep the prompt path's skip semantics.
		if strings.TrimSpace(card.Body) != "" {
			if mode, ok := explicitBindMode(card); ok && (mode == schedulerBindModeBound || mode == schedulerBindModeEphemeral) {
				return fireAgentTask, "", nil
			}
		}
		return firePrompt, "", nil
	case scheduleTypeWorkflow:
		tpl := workflowTemplate(card)
		if tpl == "" {
			return 0, "", fmt.Errorf("schedule_type=workflow requires workflow_template")
		}
		return fireWorkflow, tpl, nil
	case scheduleTypeAgentAction:
		return fireAgentAction, "", nil
	case scheduleTypeAgentTask:
		// Explicit agent_task schedule_type: dispatch by bind_mode.
		return fireAgentTask, "", nil
	case scheduleTypePrompt:
		// A prompt card that explicitly opts into the dual-mode scheduler
		// contract (data.bind_mode = bound|ephemeral) fires as an agent_task.
		// Legacy prompt cards without the field keep the executor prompt flow.
		if mode, ok := explicitBindMode(card); ok && (mode == schedulerBindModeBound || mode == schedulerBindModeEphemeral) {
			return fireAgentTask, "", nil
		}
		return firePrompt, "", nil // prompt-type fires the card body via chat.submit
	default:
		return 0, "", fmt.Errorf("schedule_type %q must be task, workflow, prompt, agent_action, or agent_task", scheduleTypeOf(card))
	}
}

// resolveExecutorActorID converts an executor reference ("agent:<name>" or a
// raw canonical ActorID) to the agent's canonical ActorID string — the format
// that handleWikiSetMapOwner stamps into ownerAgentId. Returns (id, false) when
// the agent cannot be resolved.
func (a *Actor) resolveExecutorActorID(ctx actor.PureContext, executorRef string) (string, bool) {
	// Direct canonical ActorID — return as-is.
	if !strings.HasPrefix(executorRef, agentExternalPrefix) {
		return executorRef, true
	}
	name := strings.TrimPrefix(executorRef, agentExternalPrefix)
	for _, ag := range a.listProjectAgents(ctx) {
		if sanitizePathSegment(ag.ID) == name || ag.ID == name {
			return ag.ActorID, true
		}
	}
	return "", false
}

// resolveAgentRefs extracts executor and reviewer agent ActorIDs from card Data.
// Supports "agent:<name>" references and raw actor IDs.
func (a *Actor) resolveAgentRefs(_ actor.PureContext, data map[string]any, raw string) (executor, reviewer string, err error) {
	if data != nil {
		if value, ok := data["executor"].(string); ok {
			executor = strings.TrimSpace(value)
		}
		if value, ok := data["reviewer"].(string); ok {
			reviewer = strings.TrimSpace(value)
		}
	}
	if executor == "" {
		executor = frontmatterValue(raw, "executor")
	}
	if reviewer == "" {
		reviewer = frontmatterValue(raw, "reviewer")
	}
	if executor == "" {
		return "", "", fmt.Errorf("card Data missing 'executor'")
	}
	return executor, reviewer, nil
}

// lookupAgentRef resolves an agent reference to a gospore ref.Ref.
// Accepts "agent:<name>" (looked up via workspace) or a raw actor ID.
func (a *Actor) lookupAgentRef(ctx actor.PureContext, agentRef string) (ref.Ref, bool) {
	// Direct actor ID (hex string).
	if !strings.HasPrefix(agentRef, agentExternalPrefix) {
		cid, err := identity.ParseCanonicalID(agentRef)
		if err != nil {
			return nil, false
		}
		return ctx.LookupID(id.From(cid))
	}

	// agent:<name> — resolve via workspace.list_agents.
	name := strings.TrimPrefix(agentRef, agentExternalPrefix)
	agents := a.listProjectAgents(ctx)
	for _, ag := range agents {
		if sanitizePathSegment(ag.ID) == name || ag.ID == name {
			cid, err := identity.ParseCanonicalID(ag.ActorID)
			if err != nil {
				continue
			}
			r, ok := ctx.LookupID(id.From(cid))
			return r, ok
		}
	}
	return nil, false
}

// ensureExecutorAgent spawns the executor agent locally if it is not already
// loaded, returning its ref. Used by the prompt-type timer fire path so a
// trigger works even when the agent has no active session. Mirrors the spawn
// logic in executeWorkflowTimerCard but without workflow activation.
func (a *Actor) ensureExecutorAgent(ctx actor.PureContext, executorRef string) (ref.Ref, error) {
	ownerActorID, ok := a.resolveExecutorActorID(ctx, executorRef)
	if !ok {
		return nil, fmt.Errorf("executor agent %q not found in project agents", executorRef)
	}

	var agent gen.AgentRef
	for _, ag := range a.listProjectAgents(ctx) {
		if ag.ActorID == ownerActorID || ag.ID == ownerActorID {
			agent = ag
			break
		}
	}
	if agent.ID == "" {
		return nil, fmt.Errorf("executor agent %q not found in project agents", executorRef)
	}

	worktreeID := ""
	a.bindingMu.RLock()
	worktreeID = a.agentWorktree[agent.ActorID]
	a.bindingMu.RUnlock()

	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:      agent.ID,
		ProjectID:      a.actorID,
		AgentKind:      agent.AgentKind,
		WorkspaceID:    "",
		ActorID:        agent.ActorID,
		DisplayName:    agent.DisplayName,
		Primary:        agent.Primary,
		Fast:           agent.Fast,
		Execution:      agent.Execution,
		Review:         agent.Review,
		Summary:        agent.Summary,
		WorktreeID:     worktreeID,
		ParentAgentID:  agent.ParentAgentID,
		PermissionMode: a.globalPermissionMode(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("local spawn: %w", err)
	}

	if worktreeID != "" {
		if err := a.persistWorktreeManifest(worktreeID); err != nil {
			ctx.Logger().Error("project: persist worktree manifest after local spawn failed", "error", err)
		}
	}

	cid, err := identity.ParseCanonicalID(resp.ActorID)
	if err != nil {
		return nil, fmt.Errorf("invalid actor id %q: %w", resp.ActorID, err)
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return nil, fmt.Errorf("spawned agent %q not addressable", resp.ActorID)
	}

	wsRef, ok := ctx.LookupService("workspace")
	if ok && wsRef != nil {
		notifyCtx, notifyCancel := context.WithTimeout(ctx.Lifecycle(), notifyTimeout)
		defer notifyCancel()
		notifyCall := wsRef.Invoke(notifyCtx, "workspace.agent_loaded", gen.WorkspaceAgentLoadedReq{
			AgentID:   agent.ID,
			ActorID:   resp.ActorID,
			ProjectID: a.actorID,
		})
		if notifyCall != nil {
			_ = notifyCall.Close()
		}
	}

	return agentRef, nil
}

func (a *Actor) listProjectAgents(ctx actor.PureContext) []gen.AgentRef {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), workflowPollTimeout)
	defer cancel()
	payload, _ := json.Marshal(gen.WorkspaceListAgentsReq{ProjectID: a.actorID})
	call := wsRef.Invoke(callCtx, "workspace.list_agents", payload)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil || v == nil {
		return nil
	}
	var resp gen.AgentRefListResp
	switch v := v.(type) {
	case gen.AgentRefListResp:
		resp = v
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	return resp.Items
}

// ── agent_task fire branch ─────────────────────────────────────────────────
//
// A scheduler card with the dual-mode contract (explicit data.bind_mode =
// bound|ephemeral, or schedule_type=agent_task) fires through this branch.
// The two sub-branches share the same "stop → unload modes → mount scheduler
// mode + record ActiveScheduler → chat_submit body → running" sequence; they
// differ only in how the executing agent is obtained and what happens after
// completion (bound keeps the agent resident; ephemeral unloads it).

// executeAgentTaskTimerCard dispatches an agent_task fire to the bound or
// ephemeral sub-branch based on the card's explicit bind_mode.
func (a *Actor) executeAgentTaskTimerCard(ctx actor.PureContext, card *CardRecord) error {
	mode, ok := explicitBindMode(card)
	if !ok {
		return fmt.Errorf("project.execute_timer_card: agent_task card %q must declare data.bind_mode", card.Title)
	}
	switch mode {
	case schedulerBindModeBound:
		return a.executeBoundAgentTask(ctx, card)
	case schedulerBindModeEphemeral:
		return a.executeEphemeralAgentTask(ctx, card)
	default:
		return fmt.Errorf("project.execute_timer_card: bind_mode %q must be bound or ephemeral", mode)
	}
}

// executeBoundAgentTask runs the bound sub-branch. It resolves the bound agent
// (rebinding to a fresh agent when the bound one is gone), ensures the agent
// is loaded via the local spawn path, then drives the stop/unload/mount/
// submit sequence and records run_status=running.
func (a *Actor) executeBoundAgentTask(ctx actor.PureContext, card *CardRecord) error {
	// In-flight guard (audit #8): mirror the ephemeral branch — a bound fire
	// landing while the previous run is still executing (or awaiting review) is
	// a no-op. Without it a cron shorter than the run re-submits the body and
	// re-binds the same agent mid-run, clobbering the active turn.
	if runStatus := timerState(card); runStatus == timerStatusRunning || runStatus == timerStatusPendingReview {
		ctx.Logger().Info("project: bound timer fire skipped (previous run still active)", "card", card.Title, "run_status", runStatus)
		return nil
	}

	boundAgent := boundAgentOf(card)
	ag, found := a.agentRegistryEntry(ctx, boundAgent)
	if boundAgent == "" || !found {
		// 未绑定兜底: a bound card with no usable bound_agent — never bound, or
		// the binding went stale (agent deleted/unloaded away) — spawns a fresh
		// scheduler-scoped agent, rebinds the card to it, and continues. An
		// unbound card must never fail its fire.
		newAgent, err := a.spawnSchedulerAgent(ctx, card)
		if err != nil {
			return fmt.Errorf("project.execute_timer_card: bound agent %q lost; rebind failed: %w", boundAgent, err)
		}
		card.Raw = setCardDataStringInRaw(card.Raw, "bound_agent", newAgent.ID)
		if err := a.store.Save(card); err != nil {
			// Unwind the fresh agent (audit #10): with no persisted binding
			// nothing references it, so it would linger as a permanent orphan
			// registry row no run ever unloads.
			a.unloadSchedulerAgent(ctx, newAgent.ID)
			return fmt.Errorf("project.execute_timer_card: persist rebound bound_agent: %w", err)
		}
		ag = newAgent
		ctx.Logger().Info("project: scheduler card rebound to fresh agent", "card", card.Title, "agent", ag.ID)
	}

	agentRef, err := a.ensureSchedulerAgentLoaded(ctx, ag)
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: load bound agent %s: %w", ag.ID, err)
	}

	args := agentTaskRunArgs{
		agentID:   ag.ID,
		agentRef:  agentRef,
		card:      card,
		lastRunID: ag.ID,
	}
	return a.startSchedulerAgentRun(ctx, args, false)
}

// executeEphemeralAgentTask runs the ephemeral sub-branch: in-flight guard,
// fresh scheduler-scoped agent per fire, same run sequence, and the agent is
// unloaded + unbound by the monitor when the run completes.
func (a *Actor) executeEphemeralAgentTask(ctx actor.PureContext, card *CardRecord) error {
	// In-flight guard: a fire landing while the previous ephemeral run is
	// still executing must be a no-op — the monitor owns completion, and a
	// second agent would leak (nothing would ever unload it).
	if runStatus := timerState(card); runStatus == timerStatusRunning || runStatus == timerStatusPendingReview {
		ctx.Logger().Info("project: ephemeral timer fire skipped (previous run still active)", "card", card.Title, "run_status", runStatus)
		return nil
	}

	ag, err := a.spawnSchedulerAgent(ctx, card)
	if err != nil {
		return fmt.Errorf("project.execute_timer_card: spawn ephemeral agent: %w", err)
	}
	agentRef, err := a.ensureSchedulerAgentLoaded(ctx, ag)
	if err != nil {
		// Unwind the just-registered agent (audit #5): nothing references it —
		// run_status was never stamped running and last_run_agent was never
		// written — so the monitor would never unload it, leaking a live actor
		// (and a non-gray registry row) per failed fire. Unloading destroys any
		// actor the local spawn created and drives the row into the same
		// unloaded/gray state a completed run leaves (workspace.delete_agent is
		// admin-gated and unreachable from the zero-identity project loop).
		a.unloadSchedulerAgent(ctx, ag.ID)
		return fmt.Errorf("project.execute_timer_card: load ephemeral agent %s: %w", ag.ID, err)
	}

	args := agentTaskRunArgs{
		agentID:   ag.ID,
		agentRef:  agentRef,
		card:      card,
		lastRunID: ag.ID,
	}
	return a.startSchedulerAgentRun(ctx, args, true)
}

// agentTaskRunArgs carries the resolved run context shared by both
// sub-branches of the agent_task fire.
type agentTaskRunArgs struct {
	agentID   string // workspace registry ID (stable, also last_run_agent)
	agentRef  ref.Ref
	card      *CardRecord
	lastRunID string // value to persist as data.last_run_agent
}

// startSchedulerAgentRun drives the shared agent_task run sequence on the
// target agent and persists run_status=running:
//
//	turn_cancel → modes_unload_all → scheduler_bind (mount mode + write
//	ActiveScheduler) → chat_submit(card body) → saveTimerState running.
//
// All agent invokes are fire-and-forget with bounded contexts, mirroring the
// executeWorkflowTimerCard local-spawn pattern (the agent may synchronously
// call back into project during mount/chat processing, so we never block the
// project loop waiting for its response). Because the calls land on the same
// agent inbox in order, the sequence executes deterministically.
func (a *Actor) startSchedulerAgentRun(ctx actor.PureContext, args agentTaskRunArgs, ephemeral bool) error {
	card := args.card
	now := time.Now().UTC().Format(time.RFC3339)

	// Persist the running marker BEFORE dispatching any side effect (audit #10).
	// run_status keys the in-flight guard and triggers the monitor; a fire whose
	// side effects already ran but whose running state failed to persist would
	// re-run turn_cancel + scheduler_bind + chat_submit on the next tick. So a
	// save failure here aborts before touching the agent.
	state := map[string]string{
		"run_status":   timerStatusRunning,
		"last_run":     now,
		"executor_ref": args.agentRef.ID().String(),
	}
	if ephemeral {
		// Record the run agent up front so the monitor can unload exactly this
		// agent even if the body submit fails below.
		state["last_run_agent"] = args.lastRunID
	}
	if err := a.saveTimerState(card, state); err != nil {
		return fmt.Errorf("project.execute_timer_card: persist running state: %w", err)
	}

	// 1. Stop any current conversation on the target agent.
	a.fireAndForgetAgentCall(ctx, args.agentRef, "turn_cancel", nil)
	// 2. Unload every mounted mode so the scheduler mode can mount cleanly
	//    (goal/workflow are orchestration-flow modes, mutually exclusive with
	//    builtin:mode:scheduler).
	a.fireAndForgetAgentCall(ctx, args.agentRef, "modes_unload_all", gen.AgentModesUnloadAllReq{})
	// 3. Mount builtin:mode:scheduler + record the ActiveScheduler binding.
	schedName := frontmatterValue(card.Raw, "title")
	if schedName == "" {
		schedName = card.Title
	}
	a.fireAndForgetAgentCall(ctx, args.agentRef, "scheduler_bind", gen.AgentSchedulerBindReq{
		SchedulerCardID: card.Title,
		SchedulerName:   schedName,
	})
	// 4. Submit the task body. A failure here means the run never started, so
	//    unwind the partial binding before returning (audit #6 / wiring #6).
	if err := a.submitTaskBody(ctx, args.agentRef, card.Body); err != nil {
		return a.unwindSchedulerAgentRun(ctx, args, ephemeral, err)
	}
	return nil
}

// unwindSchedulerAgentRun reverts the partial agent_task run sequence after the
// body submit failed. It unmounts the scheduler mode and clears the
// ActiveScheduler entry (the exact inverse of scheduler_bind), stops any turn
// the already-queued turn_cancel did not catch, drops an ephemeral run agent,
// and converges run_status to failed so the in-flight guard no longer blocks the
// next fire.
//
// Mode restoration note: the preceding modes_unload_all is destructive and has
// no inverse. Capturing what it removed would require synchronously awaiting
// its response on the project loop, which the scheduler fire path must never do
// (the agent can synchronously call back into project during mode teardown —
// see the fire-and-forget rationale on startSchedulerAgentRun). The unwind
// therefore returns the agent to a neutral, mode-free state; the agent
// re-establishes its kind defaults on the next load, and the scheduler mode
// itself is explicitly unmounted here.
func (a *Actor) unwindSchedulerAgentRun(ctx actor.PureContext, args agentTaskRunArgs, ephemeral bool, cause error) error {
	a.fireAndForgetAgentCall(ctx, args.agentRef, "turn_cancel", nil)
	a.fireAndForgetAgentCall(ctx, args.agentRef, "scheduler_unbind", gen.AgentSchedulerUnbindReq{SchedulerCardID: args.card.Title})
	if ephemeral {
		a.unloadSchedulerAgent(ctx, args.lastRunID)
	}
	if serr := a.saveTimerState(args.card, map[string]string{"run_status": timerStatusFailed}); serr != nil {
		ctx.Logger().Error("project: persist failed run_status after submit failure", "card", args.card.Title, "error", serr)
	}
	return fmt.Errorf("project.execute_timer_card: submit task body: %w", cause)
}

// fireAndForgetAgentCall invokes one agent callable with a bounded context and
// closes the call without waiting for the response (fire-and-forget). The
// invokes are ordered because they queue on the target agent's mailbox in
// submission order.
func (a *Actor) fireAndForgetAgentCall(ctx actor.PureContext, agentRef ref.Ref, callID string, payload any) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancel()
	call := agentRef.Invoke(callCtx, callID, payload)
	if call != nil {
		_ = call.Close()
	}
}

// submitTaskBody queues the card body as a chat_submit on the target agent.
func (a *Actor) submitTaskBody(ctx actor.PureContext, agentRef ref.Ref, body string) error {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	call := agentRef.Invoke(callCtx, "chat_submit", gen.AgentChatSubmitReq{Text: body})
	if call == nil {
		return fmt.Errorf("agent.chat.submit returned nil call")
	}
	_ = call.Close()
	return nil
}

// agentRegistryEntry finds the full registry record for an agent referenced as
// "agent:<name>", a registry ID, or a raw ActorID.
func (a *Actor) agentRegistryEntry(ctx actor.PureContext, agentRef string) (gen.AgentRef, bool) {
	name := strings.TrimPrefix(agentRef, agentExternalPrefix)
	for _, ag := range a.listProjectAgents(ctx) {
		if ag.ID == name || sanitizePathSegment(ag.ID) == name || ag.ActorID == agentRef {
			return ag, true
		}
	}
	return gen.AgentRef{}, false
}

// ensureSchedulerAgentLoaded spawns the scheduler-run agent locally if it is
// not already addressable, mirroring ensureExecutorAgent. Works for both
// existing bound agents (fresh actor ID created by the spawn / registry row)
// and freshly registered ephemeral agents.
func (a *Actor) ensureSchedulerAgentLoaded(ctx actor.PureContext, ag gen.AgentRef) (ref.Ref, error) {
	if ag.ActorID != "" {
		if cid, err := identity.ParseCanonicalID(ag.ActorID); err == nil {
			if r, ok := ctx.LookupID(id.From(cid)); ok && r != nil {
				return r, nil
			}
		}
	}

	worktreeID := ""
	a.bindingMu.RLock()
	worktreeID = a.agentWorktree[ag.ActorID]
	a.bindingMu.RUnlock()

	resp, err := a.handleSpawnAgent(ctx, domain.ProjectSpawnAgentReq{
		SpawnName:      ag.ID,
		ProjectID:      a.actorID,
		AgentKind:      ag.AgentKind,
		WorkspaceID:    "",
		ActorID:        ag.ActorID,
		DisplayName:    ag.DisplayName,
		Primary:        ag.Primary,
		Fast:           ag.Fast,
		Execution:      ag.Execution,
		Review:         ag.Review,
		Summary:        ag.Summary,
		WorktreeID:     worktreeID,
		ParentAgentID:  ag.ParentAgentID,
		PermissionMode: a.globalPermissionMode(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("local spawn: %w", err)
	}

	cid, err := identity.ParseCanonicalID(resp.ActorID)
	if err != nil {
		return nil, fmt.Errorf("invalid actor id %q: %w", resp.ActorID, err)
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return nil, fmt.Errorf("spawned agent %q not addressable", resp.ActorID)
	}

	// Notify the workspace so the registry row carries the actor identity and
	// LoadState="loaded" (fire-and-forget, existing agent_loaded contract).
	if wsRef, ok := ctx.LookupService("workspace"); ok && wsRef != nil {
		notifyCtx, notifyCancel := context.WithTimeout(ctx.Lifecycle(), notifyTimeout)
		defer notifyCancel()
		if notifyCall := wsRef.Invoke(notifyCtx, "workspace.agent_loaded", gen.WorkspaceAgentLoadedReq{
			AgentID: ag.ID, ActorID: resp.ActorID, ProjectID: a.actorID,
		}); notifyCall != nil {
			_ = notifyCall.Close()
		}
	}
	return agentRef, nil
}

// spawnSchedulerAgent registers a fresh scheduler-scoped agent in the
// workspace registry (workspace.agent_spawn_scheduler — synchronous and
// deadlock-free: the handler never calls back into project) and returns the
// registry row for the project to spawn locally. The agent kind comes from the
// card (data.agent_kind, default coder) and the card's model-slot overrides
// (data.model_slots) ride along; slots the card leaves unset stay nil so the
// workspace falls back to the agent-kind config defaults.
func (a *Actor) spawnSchedulerAgent(ctx actor.PureContext, card *CardRecord) (gen.AgentRef, error) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return gen.AgentRef{}, fmt.Errorf("workspace service unavailable")
	}
	kind := agentKindOf(card)
	if kind == "" {
		kind = string(domain.AgentKindCoder)
	}
	req := gen.WorkspaceAgentSpawnSchedulerReq{
		ProjectID:   a.actorID,
		AgentKind:   kind,
		DisplayName: "",
	}
	req.Primary, req.Fast, req.Execution, req.Review, req.Summary = schedulerModelSlots(card)
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), notifyTimeout)
	defer cancel()
	call := wsRef.Invoke(callCtx, "workspace.agent_spawn_scheduler", req)
	if call == nil {
		return gen.AgentRef{}, fmt.Errorf("workspace.agent_spawn_scheduler returned nil call")
	}
	defer call.Close()
	v, err := call.Final(callCtx)
	if err != nil {
		return gen.AgentRef{}, err
	}
	var resp gen.WorkspaceAgentSpawnSchedulerResp
	switch v := v.(type) {
	case gen.WorkspaceAgentSpawnSchedulerResp:
		resp = v
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	if resp.AgentID == "" {
		return gen.AgentRef{}, fmt.Errorf("workspace.agent_spawn_scheduler returned empty AgentId")
	}
	// The workspace applies the agent-kind config defaults to any nil slot when
	// it registers the row, but its response does not echo them. Re-read the
	// registry row so the local spawn (ensureSchedulerAgentLoaded →
	// project.spawn_agent → derefSlotReq) carries the same
	// Primary/Fast/Execution/Review/Summary the row holds; otherwise a card with
	// no data.model_slots silently spawns its first agent with every slot
	// resolved to [auto] while a later registry re-read gets the kind-config
	// defaults (audit wiring #2 — Go-side fix, no schema change). pickSlot keeps
	// an explicit request slot when the row does not carry one.
	primary, fast, execution, review, summary := req.Primary, req.Fast, req.Execution, req.Review, req.Summary
	for _, ag := range a.listProjectAgents(ctx) {
		if ag.ID == resp.AgentID {
			primary = pickModelSlot(ag.Primary, primary)
			fast = pickModelSlot(ag.Fast, fast)
			execution = pickModelSlot(ag.Execution, execution)
			review = pickModelSlot(ag.Review, review)
			summary = pickModelSlot(ag.Summary, summary)
			break
		}
	}
	return gen.AgentRef{
		ID:             resp.AgentID,
		ProjectID:      a.actorID,
		DisplayName:    resp.DisplayName,
		AgentKind:      resp.AgentKind,
		ActorID:        resp.ActorID,
		Status:         "idle",
		LoadState:      "unloaded",
		LifecycleScope: "scheduler",
		Primary:        primary,
		Fast:           fast,
		Execution:      execution,
		Review:         review,
		Summary:        summary,
	}, nil
}

// pickModelSlot returns preferred when it is non-nil, otherwise fallback. Used
// to layer the registry row's kind-config-default slots over the card's
// explicit request slots.
func pickModelSlot(preferred, fallback *gen.ModelSlot) *gen.ModelSlot {
	if preferred != nil {
		return preferred
	}
	return fallback
}

// setCardDataStringAndSave writes one data: block scalar and persists the
// card. Used for last_run_agent / bound_agent lifecycle fields. It mirrors
// saveTimerState (timer_state.go:57-59) by keeping card.Data in lockstep with
// card.Raw: the runtime readers (cardDataString) read card.Data, so a
// raw-only write would leave them returning the stale value until the card is
// re-read from the store (audit wiring #7).
func (a *Actor) setCardDataStringAndSave(card *CardRecord, key, value string) {
	card.Raw = setCardDataStringInRaw(card.Raw, key, value)
	if card.Data == nil {
		card.Data = map[string]any{}
	}
	card.Data[key] = value
	if err := a.store.Save(card); err != nil {
		if a.logger != nil {
			a.logger.Warn("project: persist card data failed", "card", card.Title, "key", key, "error", err)
		}
	}
}

// cancelSchedulerCardRun stops any in-flight run of a scheduler card that is
// being deleted (audit #13). Without it a card deleted mid-run leaves its agent
// executing a turn for a card that no longer exists. An ephemeral (or
// prompt-mode task) run records its run agent in data.last_run_agent — unload
// it so no live actor is left behind; the bound/legacy run agent is
// turn_cancelled via the recorded executor_ref. Bound agents stay resident
// (only applySchedulerUnbind unmounts their scheduler mode). Best-effort /
// fire-and-forget: card deletion must never block on agent teardown.
func (a *Actor) cancelSchedulerCardRun(ctx actor.PureContext, card *CardRecord) {
	if card == nil {
		return
	}
	ephemeral := isPromptTaskCard(card)
	if mode, explicit := explicitBindMode(card); explicit {
		ephemeral = mode == schedulerBindModeEphemeral
	}
	if ephemeral {
		if lastAgent := lastRunAgentOf(card); lastAgent != "" {
			a.unloadSchedulerAgent(ctx, lastAgent)
		}
	}
	if executor := executorRefOf(card); executor != "" {
		if agentRef, ok := a.lookupAgentRef(ctx, executor); ok {
			a.fireAndForgetAgentCall(ctx, agentRef, "turn_cancel", nil)
		}
	}
}

// unloadSchedulerAgent fire-and-forgets workspace.agent_unload for an
// ephemeral scheduler agent once its run completes. The unload keeps the
// AgentRef (gray sidebar state, lazy-loadable) per the C3 contract.
func (a *Actor) unloadSchedulerAgent(ctx actor.PureContext, agentID string) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return
	}
	a.fireAndForgetWorkspaceCall(ctx, wsRef, "workspace.agent_unload", gen.AgentUnloadReq{AgentID: agentID})
}

// fireAndForgetWorkspaceCall invokes one workspace callable with a bounded
// context and closes without waiting — the project→workspace→project round
// trip must never block the project owner loop (see executeWorkflowTimerCard).
func (a *Actor) fireAndForgetWorkspaceCall(ctx actor.PureContext, wsRef ref.Ref, callID string, payload any) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	if call := wsRef.Invoke(callCtx, callID, payload); call != nil {
		_ = call.Close()
	}
}
