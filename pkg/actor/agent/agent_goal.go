package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// DefaultGoalMaxTurns is the fallback autonomous turn budget when the agent
// kind config does not specify MaxTurns. Mirrors workspace.DefaultGoalMaxTurns
// to avoid a workspace dependency from the agent package.
const DefaultGoalMaxTurns int32 = 200

// handleTurnAssess is registered as the agent-owned callable so it is included
// in tool discovery. The turn engine intercepts this callable before generic
// dispatch and records the assessment on the active turn.
func (a *Actor) handleTurnAssess(_ actor.Context, req gen.TurnAssessment) (string, error) {
	decision := strings.TrimSpace(req.Decision)
	if decision != "complete_candidate" && decision != "ready_for_review" {
		return "", fmt.Errorf("turn.assess Decision must be %q or %q (do not call turn.assess while the goal still needs work — the system continues automatically)", "complete_candidate", "ready_for_review")
	}
	if err := a.validateAssessDecision(decision); err != nil {
		return "", err
	}
	return "Turn assessment recorded.", nil
}

// validateAssessDecision rejects complete_candidate for an agent whose goal is
// bound to a task card AND that has a parent agent: such a bound goal may not
// self-complete — clearing the goal here would unbind it from the card before
// the map owner reviews the changeset. Completion of a parented bound goal
// must go through ready_for_review instead.
//
// A bound goal on a root agent (no parent agent, bound via goal_card_submit)
// has no map-owner reviewer; the user reviews directly in the session, so
// complete_candidate stays allowed — clearing the goal unbinds the card and
// unmounts it from context (see clearGoal and the completion path in
// handleTurnComplete, which also marks the card done).
func (a *Actor) validateAssessDecision(decision string) error {
	if decision == "complete_candidate" &&
		a.RawSession.Goal != nil && a.RawSession.Goal.BoundTaskCardID != "" &&
		a.hasParentAgent() {
		return fmt.Errorf("turn.assess: goal is bound to task card %q; use \"ready_for_review\" instead of \"complete_candidate\" — completion requires external review", a.RawSession.Goal.BoundTaskCardID)
	}
	// ready_for_review pauses the agent until a reviewer resumes or tears it
	// down via workspace.agent_review. A root agent has no parent reviewer and
	// would pause forever, leaving its bound task card stuck in
	// pending_review — reject and redirect to the direct completion path.
	if decision == "ready_for_review" && !a.hasParentAgent() {
		return fmt.Errorf("turn.assess: this agent has no parent reviewer; use \"complete_candidate\" instead of \"ready_for_review\" (or do not call turn.assess and keep working)")
	}
	return nil
}

// handleGoalSubmit is the callable handler for goal.submit, registered so the
// tool appears in the component-driven tool discovery chain. The turn engine
// intercepts goal.submit calls via onGoalSubmit for interactive confirmation.
func (a *Actor) handleGoalSubmit(ctx actor.Context, req gen.AgentGoalSubmitReq) (string, error) {
	return a.applyGoalSubmit(ctx, req)
}

// applyGoalSubmit stores the LLM's interpreted goal and returns a request ID for
// the interaction step. The turn engine blocks on this until the user resolves.
func (a *Actor) applyGoalSubmit(ctx actor.Context, req gen.AgentGoalSubmitReq) (string, error) {
	if a.RawSession.Goal == nil {
		return "", fmt.Errorf("goal_submit: no active goal")
	}
	if a.RawSession.Goal.Confirmed {
		return "", fmt.Errorf("goal_submit: goal already confirmed (interpretedGoal=%q)", a.RawSession.Goal.InterpretedGoal)
	}
	goalText := strings.TrimSpace(req.InterpretedGoal)
	if goalText == "" {
		return "", fmt.Errorf("goal_submit: InterpretedGoal is empty")
	}
	a.RawSession.Goal.InterpretedGoal = goalText
	a.invalidateComponentSnapshot(ctx)
	requestID := ctx.NewID().String()
	a.goalSubmitPending = true
	a.goalSubmitRequestID = requestID
	// Persist immediately: the turn pauses here waiting for the user to
	// confirm. If the program crashes during this pause, the interpreted
	// goal must survive so the agent can resume the goal_submit flow after
	// restart instead of losing the interpretation.
	a.saveMailbox(ctx)
	a.takeSnapshot()
	return requestID, nil
}

// emitGoalSubmitEvent emits the goal_submit interaction step event so the
// frontend can render a confirmation card.
func (a *Actor) emitGoalSubmitEvent(ctx actor.Context, turnID, requestID string) {
	goal := a.RawSession.Goal
	if goal == nil || goal.InterpretedGoal == "" {
		return
	}
	stepID := turnID + "-goal-submit-" + requestID
	payload := map[string]any{
		"condition":       goal.Condition,
		"interpretedGoal": goal.InterpretedGoal,
	}
	ev := domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_submit",
		RequestID:       requestID,
		Task:            payload,
		Seq:             a.allocSeq(),
	}
	a.applyStepEvent(ev)
	// Record the pending interaction before advertising the step so restart
	// recovery cannot downgrade this confirmation to a generic pause.
	a.setPendingInteraction(ctx, turnID, stepID, requestID, "goal_submit", payload)
	_ = a.emitActorStepEvent(ctx, ev)
	a.notifyWorkspaceStatus(ctx)
	a.takeSnapshot()
}

// resolveGoalSubmit processes the user's goal_submit decision. Returns the
// decision ("approve" / "reject"), the request ID, and any user feedback.
func (a *Actor) resolveGoalSubmit(ctx actor.Context, answer string) (decision, requestID, feedback string) {
	a.goalSubmitPending = false
	requestID = a.goalSubmitRequestID
	a.goalSubmitRequestID = ""
	a.notifyWorkspaceStatus(ctx)

	var ans struct {
		Decision string `json:"decision"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(answer), &ans); err != nil {
		// Fallback: treat plain text as approve.
		decision = "approve"
	}
	decision = ans.Decision
	feedback = ans.Feedback
	if decision == "" {
		decision = "approve"
	}

	if decision == "approve" || decision == "edit" {
		a.RawSession.Goal.Confirmed = true
		if a.RawSession.Goal.Status == "" {
			a.RawSession.Goal.Status = "active"
			a.invalidateComponentSnapshot(ctx)
		}
		// Persist immediately: Confirmed is a critical state transition that
		// gates autonomous execution. If the program crashes after the user
		// approves but before the next turn checkpoint, the approval must
		// survive so the agent does not re-ask for confirmation.
		a.saveMailbox(ctx)
		a.takeSnapshot()
		a.applyGoalTitle(ctx, a.RawSession.Goal.InterpretedGoal, a.RawSession.Goal.Condition)
	}
	return decision, requestID, feedback
}

// expireGoalSubmit degrades a pending goal_submit when the turn is cancelled.
// The goal stays active but unconfirmed, so the next turn can re-submit.
func (a *Actor) expireGoalSubmit(ctx actor.Context, requestID string) {
	if a.goalSubmitRequestID == requestID {
		a.goalSubmitPending = false
		a.goalSubmitRequestID = ""
		a.notifyWorkspaceStatus(ctx)
	}
}

// goalCondition returns the authoritative goal condition text: InterpretedGoal
// if confirmed, otherwise the raw Condition.
func (a *Actor) goalCondition(ctx actor.Context) string {
	if a.RawSession.Goal == nil {
		return ""
	}
	if a.RawSession.Goal.InterpretedGoal != "" {
		return a.RawSession.Goal.InterpretedGoal
	}
	return a.RawSession.Goal.Condition
}

// clearGoal removes the active goal state and unmounts the goal card if it is
// currently mounted. Called when a goal is completed or explicitly cleared.
//
// It must keep cardRefs and ComponentMounts in sync, mirroring
// handleComponentUnmount: handleComponentList rebuilds the mount list from
// cardRefs when it is non-nil, so leaving the goal entry in cardRefs would
// resurrect the goal mount (and the composer badge) on the next list poll.
func (a *Actor) clearGoal(ctx actor.Context) {
	a.RawSession.Goal = nil
	a.deactivateMode(ctx, "builtin:mode:goal")
	a.saveMailbox(ctx)
}

// ensureGoalCardMounted makes sure the builtin:mode:goal component (and its
// required goal-tools bundle) is mounted whenever an active goal exists.
// This repairs sessions where the goal state survived a reload but the card
// mount did not (for example after session import or an incomplete
// persistence round-trip), so the composer badge and the goal tools
// (goal.submit, turn.assess) are restored.
func (a *Actor) ensureGoalCardMounted(ctx actor.Context) bool {
	if a.RawSession.Goal == nil {
		return false
	}
	return a.ensureModeActive(ctx, "builtin:mode:goal")
}

func (a *Actor) workflowActive() bool {
	return a.RawSession.ActiveWorkflow != nil && a.RawSession.ActiveWorkflow.MapCardID != ""
}

func (a *Actor) ensureWorkflowCardMounted(ctx actor.Context) bool {
	if !a.workflowActive() {
		return false
	}
	return a.ensureModeActive(ctx, "builtin:mode:workflow")
}

func (a *Actor) clearWorkflow(ctx actor.Context) {
	a.RawSession.ActiveWorkflow = nil
	a.deactivateMode(ctx, "builtin:mode:workflow")
	a.saveMailbox(ctx)
	a.notifyWorkspaceStatus(ctx)
}

func (a *Actor) handleWorkflowStart(ctx actor.Context, req domain.AgentWorkflowStartReq) (domain.AgentWorkflowStartResp, error) {
	if req.MapCardID == "" {
		return domain.AgentWorkflowStartResp{}, fmt.Errorf("agent.workflow_start: MapCardId is required")
	}
	if err := a.activateWorkflow(ctx, req.MapCardID, false); err != nil {
		return domain.AgentWorkflowStartResp{}, fmt.Errorf("agent.workflow_start: %w", err)
	}
	return domain.AgentWorkflowStartResp{MapCardID: req.MapCardID}, nil
}

// activateWorkflow sets the map owner, mounts the workflow mode card, and
// records the active workflow in the session. It is the final step of the
// two-phase workflow_start confirmation.
//
// explicitNonCoding carries the submitter's intent for the workflow_plan_submit
// path (true = non-coding, false/absent = auto-infer at activation time from
// the map's task-card scope). For other entry points (handleWorkflowStart,
// activateWorkflowFromPlan) callers pass false so the scope-inference rule
// applies uniformly.
//
// Inference rule when explicitNonCoding is false:
//   - empty scope (no task cards) → coding (default — safer to create a
//     worktree so a coding card authored later has somewhere to land)
//   - any task card whose data.category is not in {research, explore,
//     review} → coding
//   - all task cards have data.category in {research, explore, review} →
//     non-coding
//
// The decision is stamped on the map card as data.coding (true = coding,
// false = non-coding) via project.wiki_edit_card so downstream paths
// (review changeset freeze, child-worktree guard) can read the source of
// truth without re-running the inference.
func (a *Actor) activateWorkflow(ctx actor.Context, mapID string, explicitNonCoding bool) error {
	if a.workflowActive() && a.RawSession.ActiveWorkflow.MapCardID != mapID {
		return fmt.Errorf("workflow %q is already active", a.RawSession.ActiveWorkflow.MapCardID)
	}
	if a.workflowActive() && a.RawSession.ActiveWorkflow.MapCardID == mapID {
		return nil // idempotent
	}
	project := ctx.Parent()
	if project == nil {
		return fmt.Errorf("project is unavailable")
	}
	owner := a.actorID
	if owner == "" {
		owner = ctx.Self().ID().String()
	}
	call := project.Invoke(ctx.Lifecycle(), "project.wiki_set_map_owner", domain.WikiSetMapOwnerReq{MapID: mapID, OwnerActorID: owner})
	if call == nil {
		return fmt.Errorf("set map owner failed")
	}
	callCtx, cancelCall := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancelCall()
	if _, err := call.Final(callCtx); err != nil {
		return err
	}
	// Resolve coding vs non-coding for this workflow. explicitNonCoding=true
	// short-circuits inference; otherwise the map's task-card scope decides.
	coding, err := a.resolveWorkflowCoding(ctx, mapID, explicitNonCoding)
	if err != nil {
		// Inference failure must not block activation: treat as coding
		// (safe default — owner gets a worktree; downstream review gate
		// can re-evaluate if needed).
		ctx.Logger().Warn("agent: workflow coding inference failed, defaulting to coding", "error", err, "mapID", mapID)
		coding = true
	}
	if stampErr := a.stampWorkflowCoding(ctx, mapID, coding); stampErr != nil {
		// Stamp failure is non-fatal: the decision is still in effect for
		// this activation (ActiveWorkflow.WorktreeID), and downstream
		// readers fall back to scope-inference when data.coding is absent.
		ctx.Logger().Warn("agent: failed to stamp data.coding on workflow map", "error", stampErr, "mapID", mapID, "coding", coding)
	}
	if coding {
		// Coding workflow: create the workflow owner worktree and bind the
		// owner agent to it.
		wtCall := project.Invoke(ctx.Lifecycle(), "project.workflow_create_worktree", gen.ProjectWorkflowCreateWorktreeReq{
			WorkflowMapID: mapID,
			AgentActorID:  owner,
		})
		if wtCall == nil {
			return fmt.Errorf("workflow_create_worktree failed")
		}
		wtCtx, cancelWT := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
		defer cancelWT()
		wtResult, err := wtCall.Final(wtCtx)
		if err != nil {
			return fmt.Errorf("workflow_create_worktree: %w", err)
		}
		wtResp, ok := wtResult.(gen.ProjectWorkflowCreateWorktreeResp)
		if !ok {
			return fmt.Errorf("workflow_create_worktree: unexpected response type %T", wtResult)
		}
		// The project callable stamps data.ownerWorktreeId into the map card
		// frontmatter as part of worktree creation.
		a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: mapID, WorktreeID: wtResp.WorktreeID}
		// Sync the builtin:mode:worktree marker now — without this the owner
		// runs the activation turn (and any UI render before the next turn
		// start) without the worktree badge, context card, or worktree_exit
		// tool, even though the binding is already live server-side.
		a.refreshWorktreeStatus(ctx)
	} else {
		// Non-coding workflow: skip worktree creation. WorktreeID stays
		// empty so the downstream paths (spawn worker, review approve/reject)
		// already branch on an empty ID and run in main-repo read-only
		// mode (research / explore / review only).
		a.RawSession.ActiveWorkflow = &gen.ActiveWorkflow{MapCardID: mapID}
		// Clear any stale worktree binding + unwrap the worktree mode card
		// if this agent was previously bound (e.g. a prior workflow). A
		// non-coding owner must not advertise a worktree badge.
		a.refreshWorktreeStatus(ctx)
	}
	a.ensureWorkflowCardMounted(ctx)
	a.invalidateComponentSnapshot(ctx)
	a.saveMailbox(ctx)
	a.notifyWorkspaceStatus(ctx)

	// Security gate v2: stamp mutating toolcall effects onto the workflow
	// map card so the toolcall executor's Preflight can verify
	// plan-level approval before invoking each callable. The stamp logic
	// lives in pkg/actor/workspace (next to the toolcall executor that
	// consumes it) and is exposed as workspace.stamp_workflow_mutating_effects.
	//
	// Fire-and-forget: activateWorkflow runs on the owner's stateful
	// lane, so we must NOT Await() the cross-actor call here — that
	// would block the owner turn for up to the invoke timeout. A stamp
	// failure is non-fatal: the toolcall executor's Preflight will
	// reject the affected mutating toolcalls until the stamp lands,
	// which is the user-visible signal that re-running workflow_plan_submit
	// is required. The invoke itself runs on a forked goroutine inside
	// the workspace actor (workspace.stamp_workflow_mutating_effects is
	// registered as actor.Internal() with a PureContext handler), so the
	// owner turn returns immediately.
	a.stampWorkflowMutatingEffects(ctx, mapID)
	return nil
}

// stampWorkflowMutatingEffects invokes workspace.stamp_workflow_mutating_effects
// in Tell mode (fire-and-forget). The owner turn must not block on the
// cross-actor call; any failure surfaces as a mutating-toolcall rejection
// at execution time (the executor's Preflight double-checks the stamp).
//
// Invoked once per workflow activation with a non-empty map ID; the empty
// case is a no-op (idempotent activation re-entered the same map).
func (a *Actor) stampWorkflowMutatingEffects(ctx actor.Context, mapID string) {
	if mapID == "" {
		return
	}
	project := ctx.Parent()
	if project == nil {
		return
	}
	projectID := project.ID().String()
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return
	}
	stampCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
	defer cancel()
	wsRef.Invoke(stampCtx, "workspace.stamp_workflow_mutating_effects", map[string]any{
		"ProjectID": projectID,
		"MapCardID": mapID,
	}).Close()
}

// workflowNonCodingCategories lists the data.category values that count as
// "non-coding". Mirrors CardCategories in pkg/actor/project/vars.go (the
// authoritative validator) but kept locally so agent-inference stays
// self-contained — adding a new non-coding category requires updating both
// this list and CardCategories together.
var workflowNonCodingCategories = map[string]bool{
	"research": true,
	"explore":  true,
	"review":   true,
}

// resolveWorkflowCoding decides whether a workflow should run in coding
// mode (owner worktree) or non-coding mode (main-repo read-only).
//
// When explicitNonCoding is true the caller has declared non-coding intent
// (workflow_plan_submit with NonCoding=true) and we short-circuit. When
// false the decision is inferred from the map's task-card scope: coding
// when any card lacks a non-coding category, non-coding when every card's
// category is research/explore/review. An empty scope defaults to coding
// so a coding card authored later has a worktree to land in.
func (a *Actor) resolveWorkflowCoding(ctx actor.Context, mapID string, explicitNonCoding bool) (bool, error) {
	if explicitNonCoding {
		return false, nil
	}
	cards, err := a.listMapTaskCardCategories(ctx, mapID)
	if err != nil {
		return false, fmt.Errorf("list map task cards: %w", err)
	}
	if len(cards) == 0 {
		return true, nil
	}
	for _, cat := range cards {
		if !workflowNonCodingCategories[strings.ToLower(strings.TrimSpace(cat))] {
			return true, nil
		}
	}
	return false, nil
}

// listMapTaskCardCategories returns the data.category value (trimmed,
// lowercased) for every task card whose Parent == mapID. An empty scope
// returns an empty slice. Categories are read from MonoCardListItem.Data
// so no frontmatter parsing is needed on the agent side.
func (a *Actor) listMapTaskCardCategories(ctx actor.Context, mapID string) ([]string, error) {
	if mapID == "" {
		return nil, nil
	}
	projectRef, ok := ctx.LookupService("project")
	if !ok || projectRef == nil {
		return nil, fmt.Errorf("project service unavailable")
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil, fmt.Errorf("planner unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, projectRef, "project.wiki_list_cards", domain.WikiListCardsReq{
		Parent: mapID,
		Type:   "task",
	}).Await()
	if err != nil {
		return nil, err
	}
	resp, ok := result.(domain.WikiListCardsResp)
	if !ok {
		return nil, fmt.Errorf("unexpected list_cards response type %T", result)
	}
	categories := make([]string, 0, len(resp.Cards))
	for _, c := range resp.Cards {
		cat, _ := c.Data["category"].(string)
		categories = append(categories, strings.TrimSpace(cat))
	}
	return categories, nil
}

// stampWorkflowCoding writes data.coding (bool) onto the map card's
// frontmatter via project.wiki_edit_card. The card is fetched first so the
// rest of the frontmatter (title, scope, workflowTopo, etc.) is preserved
// — edit_card replaces the raw whole, so a missing fetch would clobber the
// other fields. The bool is encoded as a plain YAML scalar (`true` /
// `false`) under the `data:` block to match the project's parseDataBlock
// round-trip convention (see pkg/actor/project/review_changeset_carddata.go
// for the same setCardDataFieldInRaw pattern).
func (a *Actor) stampWorkflowCoding(ctx actor.Context, mapID string, coding bool) error {
	if mapID == "" {
		return nil
	}
	projectRef, ok := ctx.LookupService("project")
	if !ok || projectRef == nil {
		return fmt.Errorf("project service unavailable")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("planner unavailable")
	}
	fetchCtx, cancelFetch := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancelFetch()
	fetchResult, err := planner.Call(fetchCtx, projectRef, "project.wiki_get_card", domain.WikiGetCardReq{ID: mapID}).Await()
	if err != nil {
		return fmt.Errorf("fetch map card: %w", err)
	}
	fetchResp, ok := fetchResult.(domain.WikiGetCardResp)
	if !ok {
		return fmt.Errorf("unexpected get_card response type %T", fetchResult)
	}
	updated := setCardDataBoolInRaw(fetchResp.Raw, "coding", coding)
	if updated == fetchResp.Raw {
		// No change to write — skip the round-trip so we don't churn the
		// card's modified timestamp or trigger the project's dirty-cache.
		return nil
	}
	editCtx, cancelEdit := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancelEdit()
	_, err = planner.Call(editCtx, projectRef, "project.wiki_edit_card", domain.WikiEditCardReq{
		ID:  mapID,
		Raw: updated,
	}).Await()
	if err != nil {
		return fmt.Errorf("edit map card: %w", err)
	}
	return nil
}

// setCardDataBoolInRaw inserts or replaces a bool scalar in the data:
// frontmatter block. Mirrors setCardDataStringInRaw in
// pkg/actor/project/review_changeset_carddata.go but kept private here so
// agent-side data writes don't have to import unexported project helpers.
// Encoding: a plain YAML scalar (`true` or `false`) so parseDataBlock
// round-trips into a Go bool under CardRecord.Data.
func setCardDataBoolInRaw(raw, key string, value bool) string {
	scalar := "false"
	if value {
		scalar = "true"
	}
	dataLine := "  " + key + ": " + scalar

	// No frontmatter at all — create a minimal block.
	if !strings.HasPrefix(raw, "---") {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "---\ndata:\n" + dataLine + "\n---\n\n" + raw
	}
	front := raw[3 : 3+end]
	rest := raw[3+end:]
	lines := strings.Split(front, "\n")

	// Find data: block.
	dataIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "data:" {
			dataIdx = i
			break
		}
	}
	if dataIdx < 0 {
		// No data: block — append one before the closing delimiter.
		// Trim any trailing blank lines so the data block sits flush
		// against the closing delimiter.
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
			lines = lines[:len(lines)-1]
		}
		lines = append(lines, "data:")
		lines = append(lines, dataLine)
		return "---" + strings.Join(lines, "\n") + "\n" + rest
	}

	// Look for an existing key within the data: block.
	keyPrefix := "  " + key + ":"
	for i := dataIdx + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		// Non-indented line → end of data: block.
		if len(lines[i]) > 0 && lines[i][0] != ' ' && lines[i][0] != '\t' {
			break
		}
		if strings.HasPrefix(lines[i], keyPrefix) {
			if strings.TrimSpace(strings.TrimPrefix(lines[i], keyPrefix)) == scalar {
				// Same value already present — leave the raw untouched.
				return raw
			}
			lines[i] = dataLine
			return "---" + strings.Join(lines, "\n") + rest
		}
	}

	// Key not found — insert it after the data: line, skipping blanks.
	insertAt := dataIdx + 1
	for insertAt < len(lines) {
		trimmed := strings.TrimSpace(lines[insertAt])
		if trimmed == "" {
			insertAt++
			continue
		}
		if len(lines[insertAt]) > 0 && lines[insertAt][0] != ' ' && lines[insertAt][0] != '\t' {
			break
		}
		insertAt++
	}
	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:insertAt]...)
	out = append(out, dataLine)
	out = append(out, lines[insertAt:]...)
	// Join + ensure exactly one newline between the last frontmatter line
	// and the closing `---` so the result is well-formed. front was sliced
	// from the raw between the two `---` delimiters without the leading
	// newline of `---` (already stripped) and without a guaranteed trailing
	// newline (the original may end with one or not). If the joined
	// payload does not end with `\n` we add one so `rest` (which starts
	// with `---`) does not collide with the last key line.
	joined := strings.Join(out, "\n")
	if !strings.HasSuffix(joined, "\n") {
		joined += "\n"
	}
	return "---" + joined + rest
}

func (a *Actor) handleWorkflowStop(ctx actor.Context, _ domain.AgentWorkflowStopReq) (domain.AgentWorkflowStopResp, error) {
	if !a.workflowActive() {
		return domain.AgentWorkflowStopResp{}, fmt.Errorf("agent.workflow_stop: no active workflow")
	}
	mapID := a.RawSession.ActiveWorkflow.MapCardID
	project := ctx.Parent()
	if project == nil {
		return domain.AgentWorkflowStopResp{}, fmt.Errorf("agent.workflow_stop: project is unavailable")
	}
	if err := a.requireNoWorkflowChildren(ctx); err != nil {
		return domain.AgentWorkflowStopResp{}, err
	}
	// Completion steps. When the worktree merge fails, the workflow stays
	// active so the agent (or user) can resolve the conflict and retry
	// workflow_stop without re-establishing the workflow first. The map is
	// only marked done and workflow mode cleared on full success.
	//
	// workflow_stop runs in RequireSynced mode: it does NOT auto-rebase the
	// owner worktree. The owner is expected to have rebased onto the latest
	// base HEAD itself (with full context to resolve conflicts and run tests)
	// before calling workflow_stop. An "outdated" result means the worktree
	// still lacks the latest base HEAD — the owner must rebase and retry.
	// residuePath carries a leftover worktree directory when the merge landed
	// but the directory was locked by a live process handle (Windows sharing
	// violation); the project sweeps it in the background. Surfaced so the
	// owner can report it instead of treating the stop as failed.
	var residuePath string
	var stopErr error
	if wtID := a.activeWorkflowWorktreeID(); wtID != "" {
		// Merge the workflow owner worktree back to the main repo before the
		// workflow is cleared. With RequireSynced=true the project callable
		// verifies the worktree contains the latest base HEAD (no auto-rebase)
		// and rejects with "outdated" when the owner forgot to rebase; the
		// merge itself is then conflict-free in the common case.
		stopCall := project.Invoke(ctx.Lifecycle(), "project.workflow_stop_merge_worktree", gen.ProjectWorkflowStopMergeWorktreeReq{WorktreeID: wtID, RequireSynced: true})
		if stopCall == nil {
			stopErr = fmt.Errorf("workflow_stop_merge_worktree failed")
		} else {
			mergeCtx, cancelMerge := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
			defer cancelMerge()
			stopResult, err := stopCall.Final(mergeCtx)
			switch {
			case err != nil:
				stopErr = err
			default:
				stopResp, ok := stopResult.(gen.ProjectWorkflowStopMergeWorktreeResp)
				if !ok {
					stopErr = fmt.Errorf("unexpected merge response type %T", stopResult)
				} else if stopResp.Status == "outdated" {
					stopErr = fmt.Errorf("owner worktree %q is not based on the latest main HEAD; commit all work, rebase the worktree (git rebase <base>), resolve conflicts, verify tests, then retry workflow_stop", wtID)
				} else if stopResp.Status == "conflict" {
					stopErr = fmt.Errorf("merging owner worktree %q to main conflicts; resolve manually then retry", wtID)
				} else if stopResp.ResiduePath != "" {
					residuePath = stopResp.ResiduePath
				}
			}
		}
	}
	if stopErr != nil {
		// Idempotent recovery before failing the stop: the merge error may
		// be stale — the project can land the merge and still return an
		// error (transport timeout after the commit, a post-merge cleanup
		// failure, or the worktree being swept as a dangling remnant).
		// data.ownerWorktreeId on the map card is the authoritative stamp:
		// the project clears it once the owner worktree is merged away, so
		// a cleared stamp proves the merge landed. Complete the stop
		// instead of keeping the workflow alive on an already-merged
		// worktree; a stamp that is still set — or any failure to read it
		// — keeps the workflow active so workflow_stop can be retried.
		if a.ownerWorktreeStampCleared(ctx, project, mapID) {
			ctx.Logger().Info("agent.workflow_stop: merge reported an error but the map's ownerWorktreeId stamp is cleared; merge already landed, completing stop",
				"map", mapID, "merge_error", stopErr)
			stopErr = nil
		}
	}
	if stopErr != nil {
		// Keep workflow mode active on merge failure. The owner worktree
		// binding persists (the merge callable did not discard it on
		// conflict). The agent stays in workflow mode and can retry
		// workflow_stop after resolving the conflict — no need to
		// re-activate via workflow_start.
		a.refreshWorktreeStatus(ctx)
		return domain.AgentWorkflowStopResp{MapCardID: mapID}, fmt.Errorf(
			"agent.workflow_stop: completing workflow %q failed (workflow still active; resolve the cause and retry workflow_stop): %w",
			mapID, stopErr)
	}
	if call := project.Invoke(ctx.Lifecycle(), "project.wiki_set_status", domain.WikiSetStatusReq{ID: mapID, Status: "done"}); call == nil {
		stopErr = fmt.Errorf("complete map failed")
	} else {
		statusCtx, cancelStatus := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
		defer cancelStatus()
		if _, err := call.Final(statusCtx); err != nil {
			stopErr = err
		}
	}
	if stopErr != nil {
		a.refreshWorktreeStatus(ctx)
		return domain.AgentWorkflowStopResp{MapCardID: mapID}, fmt.Errorf(
			"agent.workflow_stop: workflow %q merge succeeded but marking map done failed: %w", mapID, stopErr)
	}
	// Normalize any leftover waiting turns (a parked owner completion that
	// never got a follow-up chat_submit) back to completed before the
	// workflow is cleared, so stopping leaves no dangling waiting records.
	a.normalizeWaitingTurns(ctx)
	a.clearWorkflow(ctx)
	a.refreshWorktreeStatus(ctx)
	return domain.AgentWorkflowStopResp{MapCardID: mapID, ResiduePath: residuePath}, nil
}

// ownerWorktreeStampCleared reports whether the workflow map card's
// data.ownerWorktreeId stamp has been cleared on the project side. The
// project actor stamps data.ownerWorktreeId when a workflow owner worktree is
// created (project.workflow_create_worktree) and clears it once that worktree
// has been merged away (mergeWorktreeIntoBase / dangling-remnant sweep), so an
// absent stamp means no live owner worktree remains. Used by handleWorkflowStop
// to distinguish a merge error that actually landed from a real failure.
// Conservative by design: any failure to fetch or decode the card returns
// false, keeping the workflow active so workflow_stop can be retried.
func (a *Actor) ownerWorktreeStampCleared(ctx actor.Context, project ref.Ref, mapID string) bool {
	call := project.Invoke(ctx.Lifecycle(), "project.wiki_get_card", domain.WikiGetCardReq{ID: mapID})
	if call == nil {
		return false
	}
	queryCtx, cancelQuery := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancelQuery()
	result, err := call.Final(queryCtx)
	if err != nil {
		return false
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return false
	}
	stamp, parsed := ownerWorktreeStampFromRaw(resp.Raw)
	return parsed && stamp == ""
}

// ownerWorktreeStampFromRaw extracts data.ownerWorktreeId from a raw card.
// parsed=false means the raw carries no parseable frontmatter block — the
// caller cannot conclude the stamp is cleared and must stay conservative.
// Scoped to the frontmatter (the project only writes and clears the key inside
// the data: block) so body prose that merely mentions ownerWorktreeId cannot
// fake a stamp.
func ownerWorktreeStampFromRaw(raw string) (stamp string, parsed bool) {
	if !strings.HasPrefix(raw, "---") {
		return "", false
	}
	end := strings.Index(raw[3:], "---")
	if end < 0 {
		return "", false
	}
	front := raw[3 : 3+end]
	return frontmatterDataValue(front, "ownerWorktreeId"), true
}

// normalizeWaitingTurns flips every waiting turn record in Session.Turns back
// to completed through the canonical lifecycle reducer. workflow_stop runs it
// while clearing ActiveWorkflow so the session keeps no waiting records after
// the workflow ends; if ActiveTurnRef/status point at one of them they are
// cleared too.
func (a *Actor) normalizeWaitingTurns(ctx actor.Context) {
	var waiting []string
	for _, t := range a.Session.Turns {
		if t.State == domain.TurnStateWaiting {
			waiting = append(waiting, t.ID)
		}
	}
	if len(waiting) == 0 {
		return
	}
	activeRef := a.getActiveTurnRef()
	for _, id := range waiting {
		if _, err := a.applyTurnLifecycle(ctx, domain.TurnLifecycleCompleted, id, nil, turnLifecycleOptions{}); err != nil {
			ctx.Logger().Warn("agent: normalize leftover waiting turn failed", "turn", id, "error", err)
			continue
		}
		if activeRef == id {
			a.setActiveTurnRef("")
			a.status.TurnID = ""
		}
	}
}

// autoMergeWorktreeAfterGoalCompletion merges the agent's isolated worktree
// back into the main repo when a bundled goal completes inside that worktree.
// Called from handleTurnComplete's complete_candidate branch after
// clearGoal. It is strictly best-effort: on conflict or any other failure the
// goal completion is NOT rolled back and the worktree binding is kept so the
// user can resolve the merge manually (a system step announces the manual
// step). Workflow-owned worktrees are excluded — they are merged by
// workflow_stop (handleWorkflowStop), not by goal completion.
func (a *Actor) autoMergeWorktreeAfterGoalCompletion(ctx actor.Context, turnID string) {
	if a.worktreeID == "" || a.workflowActive() {
		return
	}
	project := ctx.Parent()
	if project == nil {
		a.emitWorktreeMergeFailureStep(ctx, turnID, fmt.Errorf("project actor unavailable"))
		return
	}
	mergeCall := project.Invoke(ctx.Lifecycle(), "project.workflow_stop_merge_worktree", gen.ProjectWorkflowStopMergeWorktreeReq{WorktreeID: a.worktreeID})
	if mergeCall == nil {
		a.emitWorktreeMergeFailureStep(ctx, turnID, fmt.Errorf("workflow_stop_merge_worktree failed"))
		return
	}
	// Same call-and-error pattern as handleWorkflowStop: a status of
	// "conflict" (or any transport/type error) is a merge failure.
	mergeCtx, cancelMerge := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
	defer cancelMerge()
	mergeResult, err := mergeCall.Final(mergeCtx)
	switch {
	case err != nil:
		a.emitWorktreeMergeFailureStep(ctx, turnID, err)
		return
	default:
		mergeResp, ok := mergeResult.(gen.ProjectWorkflowStopMergeWorktreeResp)
		if !ok {
			a.emitWorktreeMergeFailureStep(ctx, turnID, fmt.Errorf("unexpected merge response type %T", mergeResult))
			return
		}
		if mergeResp.Status == "conflict" {
			a.emitWorktreeMergeFailureStep(ctx, turnID, fmt.Errorf("merging worktree %q to main conflicts; resolve manually then retry", a.worktreeID))
			return
		}
	}
	// Merged and unbound server-side: refresh the cached binding so the UI
	// stops tracking the removed worktree and the mode card unmounts.
	a.refreshWorktreeStatus(ctx)
}

// emitWorktreeMergeFailureStep logs the goal-completion auto-merge failure and
// emits a system step telling the user to resolve the worktree manually. The
// goal completion stays in place and the worktree binding is retained — this
// step only informs, it never rolls back.
func (a *Actor) emitWorktreeMergeFailureStep(ctx actor.Context, turnID string, mergeErr error) {
	ctx.Logger().Error("agent: auto-merge worktree after goal completion failed; binding kept, resolve manually",
		"error", mergeErr, "worktreeID", a.worktreeID)
	stepID := ctx.NewID().String()
	text := "Worktree auto-merge failed after goal completion.\n\n" + mergeErr.Error() + "\n\nThe goal is complete; the isolated worktree binding is kept so you can resolve the merge manually (e.g. with /worktree exit or project.worktree_exit)."
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.appendStep(domain.Step{
		ID:        stepID,
		Role:      "system",
		Type:      "text",
		Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: text}},
		Closed:    true,
		Timestamp: timestamp,
		Seq:       a.allocSeq(),
		Meta:      "system",
		TurnID:    turnID,
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.opened",
		StepID: stepID,
		TurnID: turnID,
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: stepID,
		TurnID: turnID,
	})
}

// requireNoWorkflowChildren blocks workflow_stop while live workflow child
// agents spawned by this owner still exist in the workspace. Workers must be
// reviewed (approved) or terminated first; stopping the map with live children
// would leak them past the workflow's lifetime. Children tombstoned for
// deletion (DeletionStatus="deleting") are already being torn down and do not
// block, matching deriveUnifiedChildren's projection semantics.
func (a *Actor) requireNoWorkflowChildren(ctx actor.Context) error {
	owner := a.actorID
	if owner == "" {
		owner = ctx.Self().ID().String()
	}
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return fmt.Errorf("agent.workflow_stop: workspace is unavailable")
	}
	call := wsRef.Invoke(ctx.Lifecycle(), "workspace.list_agents", domain.WorkspaceListAgentsReq{ProjectID: parentProjectID(ctx)})
	if call == nil {
		return fmt.Errorf("agent.workflow_stop: list agents failed")
	}
	defer call.Close()
	listCtx, cancelList := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancelList()
	result, err := call.Final(listCtx)
	if err != nil {
		return fmt.Errorf("agent.workflow_stop: list agents: %w", err)
	}
	var list domain.AgentRefListResp
	switch value := result.(type) {
	case domain.AgentRefListResp:
		list = value
	case *domain.AgentRefListResp:
		if value == nil {
			return fmt.Errorf("agent.workflow_stop: list agents returned nil")
		}
		list = *value
	default:
		return fmt.Errorf("agent.workflow_stop: unexpected list agents response %T", result)
	}
	var leftovers []string
	for _, ag := range list.Items {
		if ag.ParentAgentID != owner || ag.DeletionStatus == "deleting" {
			continue
		}
		leftovers = append(leftovers, fmt.Sprintf("id=%s name=%q actorId=%s", ag.ID, ag.DisplayName, ag.ActorID))
	}
	if len(leftovers) > 0 {
		return fmt.Errorf(
			"agent.workflow_stop: %d workflow child agent(s) still exist: %s. Terminate each child before stopping: call workspace.agent_terminate with AgentActorId=<actorId above> and CallerAgentId=%q (an agent_review-approved worker is already cleaned up; approve instead only if its output should be kept), then retry workflow_stop",
			len(leftovers), strings.Join(leftovers, ", "), owner,
		)
	}
	return nil
}

// applyWorkflowStart is the first phase of a two-phase workflow_start. It
// derives an InterpretedGoal from the map card's Destination and the initial
// frontier, stores the pending confirmation in RawSession.PendingWorkflowStart,
// and returns a request ID for the confirmation interaction. It is invoked by
// the turn engine when it intercepts a workflow_start tool call.
func (a *Actor) applyWorkflowStart(ctx actor.Context, req gen.AgentWorkflowStartReq) (string, error) {
	if req.MapCardID == "" {
		return "", fmt.Errorf("workflow_start: MapCardId is required")
	}
	if a.workflowActive() && a.RawSession.ActiveWorkflow.MapCardID != req.MapCardID {
		return "", fmt.Errorf("workflow_start: workflow %q is already active", a.RawSession.ActiveWorkflow.MapCardID)
	}
	if a.RawSession.PendingWorkflowStart != nil && a.RawSession.PendingWorkflowStart.MapCardID != req.MapCardID {
		return "", fmt.Errorf("workflow_start: confirmation already pending for %q", a.RawSession.PendingWorkflowStart.MapCardID)
	}
	if a.RawSession.PendingWorkflowStart != nil && a.RawSession.PendingWorkflowStart.MapCardID == req.MapCardID {
		return a.RawSession.PendingWorkflowStart.RequestID, nil
	}
	interpretedGoal, frontierSummary, destination, err := a.deriveWorkflowStartGoal(ctx, req.MapCardID)
	if err != nil {
		return "", fmt.Errorf("workflow_start: derive goal failed: %w", err)
	}
	if interpretedGoal == "" {
		interpretedGoal = req.MapCardID
	}
	requestID := ctx.NewID().String()
	a.RawSession.PendingWorkflowStart = &gen.PendingWorkflowStart{
		MapCardID:       req.MapCardID,
		Destination:     destination,
		InterpretedGoal: interpretedGoal,
		FrontierSummary: frontierSummary,
		RequestID:       requestID,
	}
	a.workflowStartPending = true
	a.workflowStartRequestID = requestID
	a.invalidateComponentSnapshot(ctx)
	a.saveMailbox(ctx)
	a.takeSnapshot()
	return requestID, nil
}

// deriveWorkflowStartGoal fetches the map card and its initial frontier, then
// formats an InterpretedGoal suitable for the goal_submit confirmation UI.
func (a *Actor) deriveWorkflowStartGoal(ctx actor.Context, mapID string) (interpretedGoal, frontierSummary, destination string, err error) {
	projectRef, ok := ctx.LookupService("project")
	if !ok || ctx.Planner() == nil {
		return "", "", "", fmt.Errorf("project service is unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_get_card", domain.WikiGetCardReq{ID: mapID}).Await()
	if err != nil {
		return "", "", "", fmt.Errorf("fetch map card: %w", err)
	}
	card, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return "", "", "", fmt.Errorf("fetch map card returned unexpected type %T", result)
	}
	destination = cardField(card.Raw, "destination")
	if destination == "" {
		destination = mapID
	}
	frontierResult, err := ctx.Planner().Call(callCtx, projectRef, "project.wiki_frontier", domain.WikiFrontierReq{MapID: mapID}).Await()
	if err != nil {
		return "", "", destination, fmt.Errorf("fetch frontier: %w", err)
	}
	frontier, ok := frontierResult.(domain.WikiFrontierResp)
	if !ok {
		return "", "", destination, fmt.Errorf("fetch frontier returned unexpected type %T", frontierResult)
	}
	var b strings.Builder
	if len(frontier.TaskCards) == 0 {
		b.WriteString("No frontier tasks yet.")
	} else {
		for i, task := range frontier.TaskCards {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(fmt.Sprintf("- %s (%s)", task.ID, task.Status))
		}
	}
	frontierSummary = b.String()
	interpretedGoal = fmt.Sprintf("Start workflow map %s.\nDestination: %s\n\nInitial frontier:\n%s", mapID, destination, frontierSummary)
	return interpretedGoal, frontierSummary, destination, nil
}

// emitWorkflowStartEvent emits the goal_submit-style confirmation step for a
// pending workflow_start. It uses the existing goal_submit interaction type so
// the frontend renders the same approval UI; the pendingInteraction record is
// stored under type "workflow_start" so restart recovery can distinguish it.
func (a *Actor) emitWorkflowStartEvent(ctx actor.Context, turnID, requestID string) {
	pending := a.RawSession.PendingWorkflowStart
	if pending == nil || pending.RequestID != requestID {
		return
	}
	stepID := turnID + "-workflow-start-" + requestID
	payload := map[string]any{
		"condition":       pending.Destination,
		"interpretedGoal": pending.InterpretedGoal,
	}
	ev := domain.StepEvent{
		Kind:            "step.interaction_requested",
		StepID:          stepID,
		TurnID:          turnID,
		InteractionType: "goal_submit",
		RequestID:       requestID,
		Task:            payload,
		Seq:             a.allocSeq(),
	}
	a.applyStepEvent(ev)
	a.setPendingInteraction(ctx, turnID, stepID, requestID, "workflow_start", payload)
	_ = a.emitActorStepEvent(ctx, ev)
	a.takeSnapshot()
}

// resolveWorkflowStart applies the user's approve/reject answer to a pending
// workflow_start. On approval it activates the workflow; on rejection it
// clears the pending state and leaves the workflow inactive.
func (a *Actor) resolveWorkflowStart(ctx actor.Context, answer string) (decision, requestID, feedback string) {
	pending := a.RawSession.PendingWorkflowStart
	if pending != nil {
		requestID = pending.RequestID
	}
	var ans struct {
		Decision string `json:"decision"`
		Feedback string `json:"feedback"`
	}
	if err := json.Unmarshal([]byte(answer), &ans); err == nil {
		decision = ans.Decision
		feedback = ans.Feedback
	}
	if decision == "" {
		decision = "approve"
	}
	if decision == "approve" || decision == "edit" {
		if pending != nil {
			if err := a.activateWorkflow(ctx, pending.MapCardID, pending.NonCoding); err != nil {
				decision = "reject"
				feedback = fmt.Sprintf("workflow activation failed: %v", err)
			}
		}
	}
	if decision == "approve" || decision == "edit" || decision == "reject" {
		a.RawSession.PendingWorkflowStart = nil
		a.workflowStartPending = false
		a.workflowStartRequestID = ""
		a.invalidateComponentSnapshot(ctx)
		a.saveMailbox(ctx)
		a.takeSnapshot()
	}
	return
}

// expireWorkflowStart clears the pending workflow_start state when the blocking
// turn is cancelled or otherwise discarded without a user answer.
func (a *Actor) expireWorkflowStart(ctx actor.Context, requestID string) {
	pending := a.RawSession.PendingWorkflowStart
	if pending != nil && pending.RequestID == requestID {
		a.RawSession.PendingWorkflowStart = nil
		a.workflowStartPending = false
		a.workflowStartRequestID = ""
		a.invalidateComponentSnapshot(ctx)
		a.saveMailbox(ctx)
		a.takeSnapshot()
	}
}

// emitGoalCompletedStep emits a system step event announcing that the active
// goal has been completed and cleared, so the frontend can render a timeline
// marker with the assessment reason.
func (a *Actor) emitGoalCompletedStep(ctx actor.Context, turnID, summary string) {
	stepID := ctx.NewID().String()
	text := "Goal completed."
	if summary != "" {
		text += "\n\n" + summary
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	a.appendStep(domain.Step{
		ID:        stepID,
		Role:      "system",
		Type:      "text",
		Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: text}},
		Closed:    true,
		Timestamp: timestamp,
		Seq:       a.allocSeq(),
		Meta:      "system",
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.opened",
		StepID: stepID,
		TurnID: turnID,
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: stepID,
		TurnID: turnID,
	})
}

// handleInternalAssignGoal directly assigns a confirmed goal to this agent
// without the interactive goal.submit confirmation step. Used by
// workspace.agent.spawn_assign to atomically create a worker agent, bind it to
// a task card, and start it on a goal. The goal is Confirmed=true from the
// outset so the agent begins autonomous execution immediately.
func (a *Actor) handleInternalAssignGoal(ctx actor.Context, req domain.AgentInternalAssignGoalReq) (domain.AgentInternalAssignGoalResp, error) {
	maxTurns := int32(req.MaxTurns)
	if maxTurns <= 0 {
		maxTurns = DefaultGoalMaxTurns
		if cfg := a.fetchAgentKindConfig(ctx); cfg.MaxTurns > 0 {
			maxTurns = cfg.MaxTurns
		}
	}

	a.RawSession.Goal = &gen.SessionGoal{
		Condition:       req.Condition,
		MaxTurns:        maxTurns,
		TurnCount:       0,
		InterpretedGoal: req.InterpretedGoal,
		Confirmed:       true,
		Status:          "active",
		BoundTaskCardID: req.BoundTaskCardID,
	}
	a.ensureGoalCardMounted(ctx)
	a.invalidateComponentSnapshot(ctx)

	firstMsg := req.Condition
	if req.PromptPrelude != "" {
		firstMsg = req.PromptPrelude + "\n\n---\n\n" + req.Condition
	}
	_, _, _ = a.createUserTurn(ctx, domain.TurnInput{Text: firstMsg})
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)
	a.takeSnapshot()
	a.applyGoalTitle(ctx, req.InterpretedGoal, req.Condition)

	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: firstMsg},
		TurnName:  turnName,
	}); err != nil {
		return domain.AgentInternalAssignGoalResp{}, fmt.Errorf("agent.internal.assign_goal: schedule start_turn: %w", err)
	}

	g := a.RawSession.Goal
	return domain.AgentInternalAssignGoalResp{
		Goal: gen.GoalSummary{
			Condition:       g.Condition,
			Status:          g.Status,
			Confirmed:       g.Confirmed,
			BoundTaskCardID: g.BoundTaskCardID,
			TurnCount:       g.TurnCount,
			MaxTurns:        g.MaxTurns,
		},
	}, nil
}

// applySpawnGoal applies a spawn-time goal threaded through the spawn chain
// (workspace.agent.spawn_assign → ProjectSpawnAgentReq → pendingSpawnGoal).
// Called from OnStart after all handlers are registered. Mirrors
// handleInternalAssignGoal, including mirroring the goal text into the agent
// title via applyGoalTitle.
func (a *Actor) applySpawnGoal(ctx actor.Context, goal *domain.AgentInternalAssignGoalReq) {
	maxTurns := int32(goal.MaxTurns)
	if maxTurns <= 0 {
		maxTurns = DefaultGoalMaxTurns
		if cfg := a.fetchAgentKindConfig(ctx); cfg.MaxTurns > 0 {
			maxTurns = cfg.MaxTurns
		}
	}
	a.RawSession.Goal = &gen.SessionGoal{
		Condition:       goal.Condition,
		MaxTurns:        maxTurns,
		TurnCount:       0,
		InterpretedGoal: goal.InterpretedGoal,
		Confirmed:       true,
		Status:          "active",
		BoundTaskCardID: goal.BoundTaskCardID,
	}
	a.ensureGoalCardMounted(ctx)
	a.invalidateComponentSnapshot(ctx)

	firstMsg := goal.Condition
	if goal.PromptPrelude != "" {
		firstMsg = goal.PromptPrelude + "\n\n---\n\n" + goal.Condition
	}
	_, _, _ = a.createUserTurn(ctx, domain.TurnInput{Text: firstMsg})
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)
	a.takeSnapshot()
	a.applyGoalTitle(ctx, goal.InterpretedGoal, goal.Condition)

	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: firstMsg},
		TurnName:  turnName,
	}); err != nil {
		ctx.Logger().Error("agent: schedule spawn goal start_turn", "error", err)
	}
}

// handleInternalResumeFromReview resets a ready_for_review goal back to active
// and resumes the agent with reviewer feedback. Used by workspace.agent.review
// (reject path). Goal.Status → "active", TurnCount → 0 (per-attempt budget:
// each attempt from spawn or reject-resume gets a fresh turn budget).
func (a *Actor) handleInternalResumeFromReview(ctx actor.Context, req domain.AgentInternalResumeFromReviewReq) error {
	if a.RawSession.Goal == nil {
		return fmt.Errorf("agent.internal.resume_from_review: no active goal")
	}
	if a.RawSession.Goal.Status != "ready_for_review" {
		ctx.Logger().Info("agent: resume_from_review skipped, goal not in ready_for_review",
			"status", a.RawSession.Goal.Status)
		return nil
	}
	a.RawSession.Goal.Status = "active"
	a.RawSession.Goal.TurnCount = 0
	// Clear the captured outputs: the worker must produce fresh, contract-
	// conforming outputs on its next ready_for_review. Stale outputs from the
	// rejected attempt would silently satisfy the review gate.
	a.RawSession.Goal.Outputs = nil
	a.saveMailbox(ctx)
	a.takeSnapshot()

	text := req.Feedback
	if text == "" {
		text = "Your previous work was rejected. Please revise."
	}
	_, _, _ = a.createUserTurn(ctx, domain.TurnInput{Text: text, Meta: "review_reject"})
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)

	turnName := fmt.Sprintf("turn-%d", a.nextTurnOrder())
	return ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: text},
		TurnName:  turnName,
	})
}
