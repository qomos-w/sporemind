package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// handleAgentSpawnAssign is the workflow orchestrator's primary entry
// point for advancing a task card. It validates the request, fetches
// the bound task card's frontmatter (read-only) to read
// data.exec.kind, runs the executor's Preflight (kind-specific
// validation, no side effects), claims the card (universal status
// CAS → doing), and finally calls the executor's Execute (kind-
// specific work + record). The orchestrator itself never branches on
// card type or implementation details — adding a new execKind (crawl,
// sub_map, ...) is a one-line register call plus a new Executor
// implementation.
//
// Today only worker_task is registered; cards that omit data.exec
// fall back to DefaultExecKind (also worker_task) so legacy behavior
// is preserved bit-for-bit. The fetch + Preflight + claim ordering
// preserves the prior inline handler's "fail before claim" semantics
// for invalid kinds / missing workflow / missing config.
func (a *Actor) handleAgentSpawnAssign(ctx actor.PureContext, req domain.WorkspaceAgentSpawnAssignReq) (domain.WorkspaceAgentSpawnAssignResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_spawn_assign", req, func() (domain.WorkspaceAgentSpawnAssignResp, error) {
		// Minimal preflight that the dispatcher owns regardless of
		// execKind: the request must identify a card to advance, and
		// the project must be resolvable.
		if req.BoundTaskCardID == "" {
			return domain.WorkspaceAgentSpawnAssignResp{}, fmt.Errorf("workspace.agent.spawn_assign: BoundTaskCardId is required")
		}
		projectID, err := a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return domain.WorkspaceAgentSpawnAssignResp{}, fmt.Errorf("workspace.agent.spawn_assign: %w", err)
		}

		// Read the bound card once to determine data.exec.kind. This
		// is a read-only invoke (no CAS, no event); it is required
		// before the kind lookup so an "unknown kind" error fails
		// before the claim (matches the prior inline handler's
		// "unknown agent kind" check ordering). If the card has been
		// deleted between the orchestrator's planning step and this
		// dispatch, we surface that as a clean error.
		raw, err := a.fetchTaskCardRaw(ctx, projectID, req.BoundTaskCardID)
		if err != nil {
			return domain.WorkspaceAgentSpawnAssignResp{}, fmt.Errorf("workspace.agent.spawn_assign: read bound task card %q: %w", req.BoundTaskCardID, err)
		}

		// Resolve the AgentKind from the task card's data.category when
		// the caller did not pick a non-default kind. The wire field is
		// required (schema workspace.workflow._2480.spore:13), so the
		// conventional default "worker" is treated the same as an empty
		// value and category is allowed to override it; any other
		// explicit kind wins over the card. Research cards are never
		// spawned — the owner does the exploration via fork_explore.
		agentKind, err := resolveSpawnAgentKind(req.AgentKind, cardCategoryOf(raw))
		if err != nil {
			return domain.WorkspaceAgentSpawnAssignResp{}, fmt.Errorf("workspace.agent.spawn_assign: %w", err)
		}

		// Resolve execKind from the card raw. An empty result
		// means the card did not declare data.exec.kind; fall back
		// to DefaultExecKind so legacy cards continue to take the
		// worker_task path bit-for-bit.
		kind := execKindOf(raw)
		if kind == "" {
			// A ```spore fenced block without an exec block is almost
			// certainly a script card that lost data.exec (the
			// depends_on rewrite historically stripped it). Warn loudly:
			// the worker_task fallback silently turned such cards into
			// wandering LLM workers instead of failing the dispatch.
			if scriptFenceWithoutExecKind(raw) {
				ctx.Logger().Warn("workspace.agent.spawn_assign: card body has a ```spore fenced block but no data.exec.kind — falling back to worker_task; the card likely lost its exec block",
					"card", req.BoundTaskCardID)
			}
			kind = DefaultExecKind
		}

		// Lookup the executor in the registry. Missing kinds are a
		// stable, typed error so callers (tests, UI, future
		// orchestrator agents) can distinguish "kind not registered"
		// from claim / transport failures. No claim has happened yet
		// — the card is untouched.
		a.ensureExecRegistry()
		exec, ok := a.execRegistry.Lookup(kind)
		if !ok {
			return domain.WorkspaceAgentSpawnAssignResp{}, &ErrUnknownExecKind{
				Kind:  kind,
				Known: a.execRegistry.Kinds(),
			}
		}

		// Map the wire request to ClaimReq for Preflight. The
		// dispatcher is the only legitimate caller; PreflightResult
		// is filled in below and threaded into Execute. GoalTitle
		// mirrors the prior inline handler: req.To if set, else
		// req.InterpretedGoal.
		goalTitle := req.To
		if goalTitle == "" {
			goalTitle = req.InterpretedGoal
		}
		claimReq := ClaimReq{
			CallerAgentID:   req.CallerAgentID,
			ProjectID:       projectID,
			BoundTaskCardID: req.BoundTaskCardID,
			CardRaw:         raw,
			AgentKind:       agentKind,
			GoalTitle:       goalTitle,
			Unit:            req.Unit,
			MaxTurns:        req.MaxTurns,
			WorktreeBranch:  req.WorktreeBranch,
		}

		// Run executor's Preflight. Failures here prevent the claim
		// so the card stays re-claimable by the next attempt; this
		// matches the prior "unknown agent kind", "no workflow",
		// etc. early-rejection semantics.
		preflight, preflightErr := exec.Preflight(ctx, claimReq)
		if preflightErr != nil {
			return domain.WorkspaceAgentSpawnAssignResp{}, preflightErr
		}
		claimReq.Preflight = &preflight

		// Universal claim (CAS backlog|todo → doing). Same fused
		// call the previous handler used: status CAS + body read +
		// binding resolution in a single project-side invoke. Orphan
		// reclaim (doing|pending_review → doing) is also handled
		// here so the executor can focus on kind-specific work.
		claimed, previousStatus, rawFromClaim, taskInputs, claimErr := a.claimTaskCard(ctx, projectID, req.BoundTaskCardID, []string{"backlog", "todo"})
		if !claimed {
			switch {
			case isDependencyBlockError(claimErr):
				return domain.WorkspaceAgentSpawnAssignResp{},
					fmt.Errorf("workspace.agent.spawn_assign: %w", claimErr)
			case !isClaimStatusMiss(claimErr):
				// Transport/lookup failures must surface as-is; only
				// a plain CAS miss may fall through to orphan reclaim.
				return domain.WorkspaceAgentSpawnAssignResp{},
					fmt.Errorf("workspace.agent.spawn_assign: claim task card %q: %w", req.BoundTaskCardID, claimErr)
			}
			if !a.hasLiveWorkerForTaskCard(ctx, projectID, req.BoundTaskCardID) {
				// Orphaned card: a worker died leaving it
				// doing/pending_review with no live binding. Reclaim
				// it as "doing" for this worker — the card stays
				// claimed so a concurrent spawn_assign cannot also
				// bind to it. previousStatus tracks the orphaned
				// state so a spawn rollback restores it instead of
				// dropping it to a re-claimable backlog (which would
				// allow double-booking).
				if ok, p, r, ti, err2 := a.claimTaskCard(ctx, projectID, req.BoundTaskCardID, []string{"doing", "pending_review"}); ok {
					claimed = true
					previousStatus = p
					rawFromClaim = r
					taskInputs = ti
				} else if !isClaimStatusMiss(err2) && !isDependencyBlockError(err2) {
					return domain.WorkspaceAgentSpawnAssignResp{},
						fmt.Errorf("workspace.agent.spawn_assign: reclaim task card %q: %w", req.BoundTaskCardID, err2)
				}
			}
			if !claimed {
				return domain.WorkspaceAgentSpawnAssignResp{},
					fmt.Errorf("workspace.agent.spawn_assign: card %q already claimed or not in a claimable state", req.BoundTaskCardID)
			}
		}

		// Update the claimReq with the claim's response (body +
		// inputs) so Execute sees the full picture. CardRaw in the
		// claim response is the authoritative post-CAS frontmatter
		// (status flipped to doing); we pass that through for any
		// executor that re-parses it.
		claimReq.Claimed = true
		claimReq.PreviousStatus = previousStatus
		claimReq.CardRaw = rawFromClaim
		claimReq.Body = stripCardFrontmatter(rawFromClaim)
		claimReq.Body = injectTaskInputs(claimReq.Body, taskInputs)
		claimReq.Inputs = taskInputs

		// Delegate the kind-specific execution. Failure paths roll
		// the claim back so a retry can re-claim cleanly.
		execResp, execErr := exec.Execute(ctx, claimReq)
		if execErr != nil {
			a.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, previousStatus)
			return domain.WorkspaceAgentSpawnAssignResp{}, execErr
		}

		// Stamp the worker's actor id onto the task card's
		// data.ownerAgentId so card-side consumers (workflow fold
		// classification, topology, card menu) can resolve the binding
		// without waiting for the agent list. Skipped for crawl: its
		// AgentActorID is a crawl task id, not an agent actor, and the
		// crawl executor completes synchronously (card already done).
		// The binding is cleared on review-approve/terminate by
		// handleWikiUnbindAgent (cascade delete path).
		if execResp.AgentActorID != "" && kind != ExecKindCrawl {
			a.stampTaskCardOwner(ctx, projectID, req.BoundTaskCardID, execResp.AgentActorID)
		}

		return domain.WorkspaceAgentSpawnAssignResp{
			AgentActorID: execResp.AgentActorID,
			DisplayName:  execResp.DisplayName,
			Goal:         execResp.Goal,
		}, nil
	})
}

// fetchTaskCardRaw reads the raw frontmatter of a task card via the
// project actor. It is the universal card read used by the dispatcher
// to resolve data.exec.kind before any state-changing call (claim,
// spawn). Errors include a stable "not found" phrase so callers can
// branch if they care to.
func (a *Actor) fetchTaskCardRaw(ctx actor.PureContext, projectID, cardID string) (string, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "", fmt.Errorf("invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_get_card", domain.WikiGetCardReq{ID: cardID})
	if call == nil {
		return "", fmt.Errorf("wiki_get_card invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return "", err
	}
	resp, ok := result.(domain.WikiGetCardResp)
	if !ok {
		return "", fmt.Errorf("unexpected wiki_get_card response %T", result)
	}
	return resp.Raw, nil
}

const (
	taskCategoryExplore  = "explore"
	taskCategoryExecute  = "execute"
	taskCategoryCode     = "code"
	taskCategoryReview   = "review"
	taskCategoryResearch = "research"
)

// cardCategoryOf returns the task card's data.category value (trimmed).
// An empty result means the card did not declare one. The lookup mirrors
// execKindOf: a small, line-based frontmatter reader so workspace stays
// self-contained.
func cardCategoryOf(cardRaw string) string {
	category := parseDataCategoryField(cardRaw)
	return strings.TrimSpace(category)
}

// cardStatusOf extracts the top-level `status:` field from a card's raw
// frontmatter (lowercased, trimmed). Empty string when absent or malformed.
// Used by the idempotent re-approve check, which only needs to distinguish
// done from everything else.
func cardStatusOf(cardRaw string) string {
	raw := strings.TrimSpace(cardRaw)
	if !strings.HasPrefix(raw, "---") {
		return ""
	}
	rest := raw[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.HasPrefix(trimmed, "status:") {
			return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(trimmed, "status:")))
		}
	}
	return ""
}

// taskCardStatusForReview reads the named task card's status for the
// idempotent re-approve path. Returns the observed status ("" when the card
// could not be read or has no status) — it backs the idempotent re-approve
// (agent gone + card done ⇒ the prior approve's flip landed, retry must
// succeed) and the not-found error's remediation hint. Fails closed: an
// unreadable card must not manufacture an approve.
func (a *Actor) taskCardStatusForReview(ctx actor.PureContext, req domain.WorkspaceAgentReviewReq) string {
	projectID, err := a.resolveWorkflowProjectID("", req.CallerAgentID)
	if err != nil {
		return ""
	}
	cardRaw, err := a.fetchTaskCardRaw(ctx, projectID, req.TaskCardID)
	if err != nil {
		return ""
	}
	return cardStatusOf(cardRaw)
}

// parseDataCategoryField locates the `category:` line nested directly
// under `data:` in the frontmatter and returns its value as a string.
// It returns "" when the structure is absent or malformed. The scanner
// is intentionally permissive about whitespace and inline comments;
// flow-style `data: { category: x }` is NOT supported, matching the
// project validator.
func parseDataCategoryField(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return ""
	}
	rest := raw[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return ""
	}
	block := rest[:end]

	var sawData bool
	var dataIndent int

	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpacesForExec(line)

		// Top-level fields reset data tracking.
		if indent == 0 {
			if !strings.HasPrefix(trimmed, "data:") && !strings.HasPrefix(trimmed, "data :") {
				sawData = false
				continue
			}
			sawData = true
			dataIndent = 0
			continue
		}

		if !sawData {
			continue
		}
		if indent <= dataIndent {
			sawData = false
			continue
		}

		// Inside data:.
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "category" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			first, last := value[0], value[len(value)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		return value
	}
	return ""
}

// resolveSpawnAgentKind picks the agent kind to spawn for a task card.
// Priority: caller explicit non-default kind > card data.category > default
// worker. Because WorkspaceAgentSpawnAssignReq.AgentKind is a required
// schema field, the conventional default "worker" is treated as the absence
// of an explicit choice and category is allowed to override it.
func resolveSpawnAgentKind(reqKind, category string) (string, error) {
	if reqKind != "" && reqKind != domain.AgentKindWorker {
		return reqKind, nil
	}
	switch category {
	case taskCategoryExplore:
		return domain.AgentKindScout, nil
	case taskCategoryExecute, taskCategoryCode:
		return domain.AgentKindWorker, nil
	case taskCategoryReview:
		return domain.AgentKindReviewer, nil
	case taskCategoryResearch:
		return "", fmt.Errorf("category research tasks are self-done by the owner via fork_explore, do not spawn")
	default:
		return domain.AgentKindWorker, nil
	}
}

// handleAgentAssign binds a claimed task card to an existing agent: it
// validates the agent's project membership, requires workflow mode, CAS-claims
// the card (backlog|todo → doing), then hands the goal to the agent via
// internal_assign_goal. On any failure after the claim, the card status is
// rolled back to its previous state.
//
// PureContext (stateless): every cross-actor hop is a ref.Invoke (project
// claim/stamp, agent internal_assign_goal), registry mutations run under
// agentsMu with identity re-resolution, and persistence/events go through
// the atomic-snapshot emit path. No ownerLoop occupancy.
func (a *Actor) handleAgentAssign(ctx actor.PureContext, req gen.WorkspaceAgentAssignReq) (gen.WorkspaceAgentAssignResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_assign", req, func() (gen.WorkspaceAgentAssignResp, error) {
		if req.AgentActorID == "" {
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: AgentActorId is required")
		}
		if req.BoundTaskCardID == "" {
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: BoundTaskCardId is required")
		}

		ag, found := a.findAgentByActorOrID(req.AgentActorID)
		if !found {
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: agent %q not found", req.AgentActorID)
		}

		// Resolve the project that owns the task card: prefer the explicit
		// ProjectId, otherwise the agent's recorded project. A project-less
		// agent cannot be assigned a project-bound task card.
		projectID := req.ProjectID
		if projectID == "" {
			projectID = ag.ProjectID
		}
		if projectID == "" {
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: agent %q is not bound to a project; specify ProjectId", req.AgentActorID)
		}
		// Agent project membership: the target agent must belong to the project
		// the task card lives in.
		if ag.ProjectID != "" && ag.ProjectID != projectID {
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: agent %q does not belong to project %q", req.AgentActorID, projectID)
		}

		// Workflow mode is required to claim task cards; authorize before the
		// claim so an unauthorized caller cannot move card state.
		if err := requireActiveWorkflow(ctx, "workspace.agent.assign", req.CallerAgentID); err != nil {
			return gen.WorkspaceAgentAssignResp{}, err
		}

		// CAS-claim the card (backlog|todo → doing) so a concurrent assign to the
		// same card loses cleanly instead of double-booking the agent. Fused
		// claim: CAS + body read + binding resolution in a single project-side call.
		claimed, prevStatus, raw, taskInputs, claimErr := a.claimTaskCard(ctx, projectID, req.BoundTaskCardID, []string{"backlog", "todo"})
		if !claimed {
			switch {
			case isDependencyBlockError(claimErr):
				return gen.WorkspaceAgentAssignResp{},
					fmt.Errorf("workspace.agent.assign: %w", claimErr)
			case !isClaimStatusMiss(claimErr):
				// Transport/lookup failures must surface as-is, not be
				// misreported as a claim conflict.
				return gen.WorkspaceAgentAssignResp{},
					fmt.Errorf("workspace.agent.assign: claim task card %q: %w", req.BoundTaskCardID, claimErr)
			}
			return gen.WorkspaceAgentAssignResp{},
				fmt.Errorf("workspace.agent.assign: card %q already claimed or not in a claimable state", req.BoundTaskCardID)
		}
		rollbackCard := func() {
			a.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, prevStatus)
		}
		condition := stripCardFrontmatter(raw)
		condition = injectTaskInputs(condition, taskInputs)

		cid, err := identity.ParseCanonicalID(ag.ActorID)
		if err != nil {
			rollbackCard()
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: invalid agent actor id: %w", err)
		}
		agentRef, ok := ctx.LookupID(id.From(cid))
		if !ok || agentRef == nil {
			rollbackCard()
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: agent actor not available")
		}

		maxTurns := int32(0)
		if req.MaxTurns > 0 {
			maxTurns = req.MaxTurns
		}
		prelude := fmt.Sprintf("You are an executor bound to task card %q. Resolve the goal, then declare ready_for_review to hand off for review.", req.BoundTaskCardID)

		// Fire-and-forget, like internal_resume_from_review: blocking on the
		// agent owner loop would form a circular wait, because the goal
		// assignment can synchronously call back into workspace for kind config.
		call := agentRef.Invoke(ctx.Lifecycle(), "internal_assign_goal", gen.AgentInternalAssignGoalReq{
			Condition:       condition,
			InterpretedGoal: req.InterpretedGoal,
			BoundTaskCardID: req.BoundTaskCardID,
			MaxTurns:        maxTurns,
			PromptPrelude:   prelude,
		})
		if call == nil {
			rollbackCard()
			return gen.WorkspaceAgentAssignResp{}, fmt.Errorf("workspace.agent.assign: assign invoke returned nil")
		}
		_ = call.Close()
		// Record the binding on the agent's Mode state. The index from the
		// pre-claim snapshot may be stale by now (concurrent stateless
		// handlers and the ownerLoop both mutate the registry), so re-resolve
		// by stable identity under the lock instead of indexing. agentsMu is
		// never held across the invokes above.
		a.agentsMu.Lock()
		for i := range a.Agents {
			if a.Agents[i].ActorID != ag.ActorID || a.Agents[i].ID != ag.ID {
				continue
			}
			if a.Agents[i].Mode == nil {
				a.Agents[i].Mode = &gen.AgentModeState{}
			}
			a.Agents[i].Mode.BoundTaskCardID = req.BoundTaskCardID
			break
		}
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)

		// Stamp the agent's actor id onto the task card's
		// data.ownerAgentId so card-side consumers can resolve the
		// binding without waiting for the agent list.
		a.stampTaskCardOwner(ctx, projectID, req.BoundTaskCardID, ag.ActorID)

		return gen.WorkspaceAgentAssignResp{
			Goal: gen.GoalSummary{
				Condition:       condition,
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        maxTurns,
			},
		}, nil
	})
}

// handleWorkflowStart starts a workflow map on an existing agent: the agent
// is mounted when unloaded, its agent-side workflow_start activates the map
// (claiming ownership), and a kickoff chat message starts the first
// orchestration turn. UI-facing counterpart of the agent workflow_start
// tool: the graph's to-start button and workflow start dialog call this.
func (a *Actor) handleWorkflowStart(ctx actor.PureContext, req gen.WorkspaceWorkflowStartReq) (gen.WorkspaceWorkflowStartResp, error) {
	return panicprobe.Guard(ctx, "workspace.workflow_start", req, func() (gen.WorkspaceWorkflowStartResp, error) {
		if req.AgentActorID == "" {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: AgentActorId is required")
		}

		// Resolve the target map card id. If TemplateMapId is set, instantiate
		// the template into a fresh instance map and use that as the target.
		// This provides the one-step template-start entry.
		mapCardID := req.MapCardID
		if req.TemplateMapID != "" {
			var err error
			mapCardID, err = a.instantiateTemplateAndStart(ctx, req)
			if err != nil {
				return gen.WorkspaceWorkflowStartResp{}, err
			}
		}
		if mapCardID == "" {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: MapCardId or TemplateMapId is required")
		}

		ag, found := a.findAgentByActorOrID(req.AgentActorID)
		if !found {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: agent %q not found", req.AgentActorID)
		}

		// Project membership: the agent must belong to the project the map
		// lives in. Prefer the explicit ProjectId, else the agent's own.
		projectID := req.ProjectID
		if projectID == "" {
			projectID = ag.ProjectID
		}
		if projectID == "" {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: agent %q is not bound to a project; specify ProjectId", req.AgentActorID)
		}
		if ag.ProjectID != "" && ag.ProjectID != projectID {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: agent %q does not belong to project %q", req.AgentActorID, projectID)
		}

		loaded, _, err := a.loadAgentByID(ctx, ag.ID)
		if err != nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: mount agent %q: %w", req.AgentActorID, err)
		}
		cid, err := identity.ParseCanonicalID(loaded.ActorID)
		if err != nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: invalid agent actor id: %w", err)
		}
		agentRef, ok := ctx.LookupID(id.From(cid))
		if !ok || agentRef == nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: agent actor not available")
		}

		// Activate the workflow on the agent (claims map ownership, mounts
		// workflow mode). Synchronous: ownership conflicts and "another
		// workflow already active" errors must surface to the caller. The
		// Final is bounded to 10s — it exists only to synchronously surface
		// an ownership-conflict error; a wedged activation must not block
		// the caller indefinitely.
		activateCtx, activateCancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
		defer activateCancel()
		activateCall := agentRef.Invoke(activateCtx, "workflow_start", gen.AgentWorkflowStartReq{MapCardID: mapCardID})
		if activateCall == nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: activate invoke returned nil")
		}
		if _, err := activateCall.Final(activateCtx); err != nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: activate workflow %q: %w", mapCardID, err)
		}

		// Kick off the first orchestration turn. chat_submit spawns the turn
		// asynchronously. Fire-and-forget: the agent's turn synchronously
		// queries workspace stateful callables while starting; awaiting that
		// response here would form a circular wait. Invoke acceptance is
		// enough — the kickoff is delivered and the turn runs on its own.
		// The activation stays in place regardless of kickoff outcome, so a
		// later failure (e.g. no model unit configured) leaves the map
		// claimed and the caller can retry or inspect.
		kickoff := fmt.Sprintf("开始执行 workflow 地图 %q：读取 frontier，编排并推进任务卡片。", mapCardID)
		chatCall := agentRef.Invoke(ctx.Lifecycle(), "chat_submit", gen.AgentChatSubmitReq{Text: kickoff})
		if chatCall == nil {
			return gen.WorkspaceWorkflowStartResp{}, fmt.Errorf("workspace.workflow_start: kickoff invoke returned nil")
		}
		_ = chatCall.Close()
		return gen.WorkspaceWorkflowStartResp{MapCardID: mapCardID}, nil
	})
}

// instantiateTemplateAndStart creates a fresh instance map from the given
// template via project.wiki_template_instantiate. It derives a unique instance
// map id and records the run as a workspace-sourced run. Returns the instance map
// id to be activated by handleWorkflowStart.
func (a *Actor) instantiateTemplateAndStart(ctx actor.PureContext, req gen.WorkspaceWorkflowStartReq) (string, error) {
	// Resolve the project actor id from the agent record or explicit request.
	ag, found := a.findAgentByActorOrID(req.AgentActorID)
	if !found {
		return "", fmt.Errorf("workspace.workflow_start: agent %q not found", req.AgentActorID)
	}
	projectID := req.ProjectID
	if projectID == "" {
		projectID = ag.ProjectID
	}
	if projectID == "" {
		return "", fmt.Errorf("workspace.workflow_start: agent %q is not bound to a project; specify ProjectId", req.AgentActorID)
	}

	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "", fmt.Errorf("workspace.workflow_start: invalid project actor id: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", fmt.Errorf("workspace.workflow_start: project actor unavailable")
	}

	instanceMapID := fmt.Sprintf("inst::%s::%s", time.Now().UTC().Format("20060102150405"), req.TemplateMapID)
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 30*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_template_instantiate", gen.WikiTemplateInstantiateReq{
		TemplateMapID: req.TemplateMapID,
		InstanceMapID: instanceMapID,
		Source:        "workspace",
	})
	if call == nil {
		return "", fmt.Errorf("workspace.workflow_start: template_instantiate invoke returned nil")
	}
	if _, err := call.Final(callCtx); err != nil {
		return "", fmt.Errorf("workspace.workflow_start: instantiate template %q: %w", req.TemplateMapID, err)
	}
	return instanceMapID, nil
}

// findAgentByActorOrID resolves an agent snapshot entry by actor ID or
// stable agent ID.
func (a *Actor) findAgentByActorOrID(actorOrID string) (domain.AgentRef, bool) {
	for _, ag := range a.agentSnapshot() {
		if ag.ActorID == actorOrID || ag.ID == actorOrID {
			return ag, true
		}
	}
	return domain.AgentRef{}, false
}

// requireDirectParent verifies that a lifecycle caller (terminate/review) is
// authorized to act on the target agent. The authorization decision is based on
// the caller's identity role (ctx.Identity().Role), NOT on the request's
// CallerAgentId field alone (which is Public-callable and could be forged by a
// direct non-agent caller).
//
//   - Developer/admin role (ctx.Identity): trusted UI caller, bypasses parent check.
//   - Otherwise (agent/system/anonymous): CallerAgentId (unconditionally
//     overwritten by the turn engine, so unforgeable from the LLM side) must be
//     non-empty, must equal the target's ParentAgentId, and must resolve to a
//     live agent with an active workflow. An empty CallerAgentId from a
//     non-developer caller is rejected — it means either a direct Public invocation
//     bypassing the turn engine, or a stale/forged request.
//
// This check must run BEFORE any card or agent state modification.
// requireDirectParent takes PureContext: it only reads the caller identity and
// delegates to requireActiveWorkflow (LookupID/ref.Invoke), so both the
// stateless terminate path and any stateful caller can authorize.
func requireDirectParent(ctx actor.PureContext, prefix, callerAgentID, parentAgentID string) error {
	// Trusted developer/admin callers (UI) bypass the parent authorization. The
	// identity is set by the framework's frontgate, not by the client payload.
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	// Non-developer callers must supply a CallerAgentId. An empty value here means
	// the call did not go through the turn engine (direct Public invocation or
	// an anonymous web role) — reject before any mutation.
	if callerAgentID == "" {
		return fmt.Errorf("%s: caller identity is not trusted (role=%q) and no CallerAgentId was injected", prefix, ctx.Identity().Role)
	}
	// The injected CallerAgentId must match the target's direct parent.
	if callerAgentID != parentAgentID {
		return fmt.Errorf("%s: only the direct parent agent may perform this operation", prefix)
	}
	// The caller must be a live agent with an active workflow — a stale or
	// fabricated parent ID pointing at a dead/non-workflow agent is rejected.
	if err := requireActiveWorkflow(ctx, prefix, callerAgentID); err != nil {
		return err
	}
	return nil
}

// requireOwnerOrSelf authorizes a pause/resume caller against the target
// agent. Unlike requireDirectParent (which only allows the target's direct
// owner), this function also permits the target agent itself (self-pause or
// self-resume). Trusted developer/admin identities bypass; non-developer callers
// must supply a non-empty CallerAgentId that matches either the target's
// ParentAgentID (owner) or the target's own ActorID/ID (self). The
// CallerAgentId is unconditionally overwritten by the turn engine
// (injectCallerAgentID), so an LLM/tool cannot forge it.
//
// This check must run BEFORE any state modification.
// requireOwnerOrSelf takes PureContext: it only reads the caller identity, so
// both stateful and stateless (forwardPauseResume) paths can authorize.
func requireOwnerOrSelf(ctx actor.PureContext, prefix, callerAgentID string, target domain.AgentRef) error {
	// Trusted developer/admin callers (UI) bypass authorization.
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	// Non-developer callers must supply a CallerAgentId. An empty value here means
	// the call did not go through the turn engine (direct Public invocation or
	// an anonymous web role) — reject before any mutation.
	if callerAgentID == "" {
		return fmt.Errorf("%s: caller identity is not trusted (role=%q) and no CallerAgentId was injected", prefix, ctx.Identity().Role)
	}
	// The injected CallerAgentId must match the target's direct owner or the
	// target itself. An agent may pause/resume itself (self-operation) or the
	// workflow owner may pause/resume its direct child.
	if callerAgentID != target.ParentAgentID && callerAgentID != target.ActorID && callerAgentID != target.ID {
		return fmt.Errorf("%s: only the target agent or its direct owner may perform this operation", prefix)
	}
	return nil
}

// requireConversableGrant authorizes an inter-agent chat operation
// (send/read) requested by an agent caller against a target agent. Trusted
// developer/admin callers (the human/UI path, which carries no CallerAgentId)
// bypass; an empty CallerAgentId from any other identity is rejected. An agent
// caller is allowed when it is (a) the target's owner or the target itself
// (same comparisons as requireOwnerOrSelf), (b) a DIRECT CHILD of the target —
// this preserves the documented worker→owner notification path — or (c)
// holding a mounted agent-chat:<target> component, the explicit, revocable
// conversable grant. The grant is verified by resolving the caller agent actor
// and invoking its pure agent.component_list snapshot; on any resolution
// failure the call is denied with an error naming the allowed targets.
//
// PureContext-parameterized: agentSnapshot (lock-guarded read) plus
// LookupID/ref.Invoke only, so the stateless agent_send_message /
// agent_read_message handlers can authorize without entering the owner loop.
func (a *Actor) requireConversableGrant(ctx actor.PureContext, prefix, callerAgentID string, target domain.AgentRef) error {
	// Trusted developer/admin callers (UI) bypass authorization.
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	// Non-developer callers must supply a CallerAgentId. An empty value means
	// the call did not go through the turn engine (direct Public invocation or
	// an anonymous web role) — reject before any mutation.
	if callerAgentID == "" {
		return fmt.Errorf("%s: caller identity is not trusted (role=%q) and no CallerAgentId was injected", prefix, ctx.Identity().Role)
	}
	// (a) The injected CallerAgentId matches the target's direct owner or the
	// target itself (same comparisons as requireOwnerOrSelf).
	if callerAgentID == target.ParentAgentID || callerAgentID == target.ActorID || callerAgentID == target.ID {
		return nil
	}
	// (b) The caller is a DIRECT CHILD of the target: its resolved AgentRef
	// (by ActorID or stable ID in the live snapshot) lists the target's
	// ActorID or stable ID as ParentAgentID. This keeps the documented
	// worker→owner notification path working without a mount.
	for _, ag := range a.agentSnapshot() {
		if (ag.ActorID == callerAgentID || ag.ID == callerAgentID) &&
			(ag.ParentAgentID == target.ActorID || ag.ParentAgentID == target.ID) {
			return nil
		}
	}
	// (c) Explicit conversable grant: the caller holds a mounted
	// agent-chat:<target> component. Any resolution failure (invalid id,
	// dead actor, unparseable component_list) denies with the allowed targets.
	if err := conversableGrant(ctx, prefix, callerAgentID, target); err != nil {
		return fmt.Errorf("%s: caller agent %q is not allowed to send/read messages for %q: only the target agent itself, its direct owner %q, a direct child of the target, or an agent holding an agent-chat:%s or agent-chat:%s component mount may do so", prefix, callerAgentID, target.ID, target.ParentAgentID, target.ID, target.ActorID)
	}
	return nil
}

// requireOwnerSelfOrChat authorizes a pause/resume caller against the target
// agent: the existing requireOwnerOrSelf comparisons (developer bypass; owner,
// self, or trusted human) OR the conversable-grant check. Unlike
// requireConversableGrant there is NO direct-child allowance — children must
// not pause/resume their parents. A direct child is denied even when it holds
// an agent-chat:<target> mount: the spawn-time parent chat mount exists to
// surface the messaging tools, never to grant lifecycle control over the
// parent.
//
// PureContext-parameterized: agentSnapshot (lock-guarded read) plus
// LookupID/ref.Invoke only, so both stateful and stateless
// (forwardPauseResume) paths can authorize.
func (a *Actor) requireOwnerSelfOrChat(ctx actor.PureContext, prefix, callerAgentID string, target domain.AgentRef) error {
	// Trusted developer/admin callers (UI) bypass authorization.
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	// Non-developer callers must supply a CallerAgentId, matching the
	// requireOwnerOrSelf contract.
	if callerAgentID == "" {
		return fmt.Errorf("%s: caller identity is not trusted (role=%q) and no CallerAgentId was injected", prefix, ctx.Identity().Role)
	}
	// Owner or self: the existing requireOwnerOrSelf comparisons.
	if callerAgentID == target.ParentAgentID || callerAgentID == target.ActorID || callerAgentID == target.ID {
		return nil
	}
	// A direct child of the target may never pause/resume its parent, even
	// with a conversable mount (same AgentRef matching as
	// requireConversableGrant's direct-child allowance).
	for _, ag := range a.agentSnapshot() {
		if (ag.ActorID == callerAgentID || ag.ID == callerAgentID) &&
			(ag.ParentAgentID == target.ActorID || ag.ParentAgentID == target.ID) {
			return fmt.Errorf("%s: a direct child may not pause/resume its parent agent (an agent-chat mount grants messaging only)", prefix)
		}
	}
	// Fall back to the explicit conversable grant (mount) check only. On
	// failure keep the historical error surface.
	if err := conversableGrant(ctx, prefix, callerAgentID, target); err != nil {
		return fmt.Errorf("%s: only the target agent or its direct owner may perform this operation", prefix)
	}
	return nil
}

// conversableGrant verifies that the caller agent currently holds a mounted
// agent-chat:<target.ID> or agent-chat:<target.ActorID> component — the
// explicit, revocable grant that lets a stranger converse with the target.
// The caller agent actor is resolved via LookupID and its agent.component_list
// callable is invoked (a pure snapshot read; same LookupID+Invoke pattern as
// handleAgentSendMessage's target resolution). Any resolution failure is
// returned as an error so callers deny closed.
//
// PureContext-parameterized: LookupID/ref.Invoke only.
func conversableGrant(ctx actor.PureContext, prefix, callerAgentID string, target domain.AgentRef) error {
	cid, err := identity.ParseCanonicalID(callerAgentID)
	if err != nil {
		return fmt.Errorf("%s: invalid caller agent id %q: %w", prefix, callerAgentID, err)
	}
	callerRef, ok := ctx.LookupID(id.From(cid))
	if !ok || callerRef == nil {
		return fmt.Errorf("%s: caller agent %q is not live", prefix, callerAgentID)
	}
	grantCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	grantCall := callerRef.Invoke(grantCtx, "component_list", domain.AgentComponentListReq{})
	if grantCall == nil {
		return fmt.Errorf("%s: query caller agent %q mounts failed", prefix, callerAgentID)
	}
	defer grantCall.Close()
	raw, serr := grantCall.Final(grantCtx)
	if serr != nil {
		return fmt.Errorf("%s: query caller agent %q mounts: %w", prefix, callerAgentID, serr)
	}
	var mounts []domain.AgentComponentMount
	switch v := raw.(type) {
	case domain.AgentComponentListResp:
		mounts = v.Items
	case *domain.AgentComponentListResp:
		if v == nil {
			return fmt.Errorf("%s: caller agent %q returned a nil component list", prefix, callerAgentID)
		}
		mounts = v.Items
	default:
		return fmt.Errorf("%s: unexpected component_list response %T from caller agent %q", prefix, raw, callerAgentID)
	}
	wantByID := "agent-chat:" + target.ID
	wantByActorID := "agent-chat:" + target.ActorID
	for _, m := range mounts {
		if m.CardID == wantByID || m.CardID == wantByActorID {
			return nil
		}
	}
	return fmt.Errorf("%s: caller agent %q holds no agent-chat mount for %q (expected %q or %q)", prefix, callerAgentID, target.ID, wantByID, wantByActorID)
}

// requireActiveWorkflow verifies the caller agent is live and holds an active
// workflow map. PureContext-parameterized: only LookupID/ref.Invoke are used
// (both thread-safe), so stateful (spawn_assign/review paths) and stateless
// (agent_assign) callers share it.
func requireActiveWorkflow(ctx actor.PureContext, prefix, callerAgentID string) error {
	if callerAgentID == "" {
		return fmt.Errorf("%s: binding a task requires an active workflow", prefix)
	}
	cid, err := identity.ParseCanonicalID(callerAgentID)
	if err != nil {
		return fmt.Errorf("%s: invalid caller agent id: %w", prefix, err)
	}
	caller, ok := ctx.LookupID(id.From(cid))
	if !ok || caller == nil {
		return fmt.Errorf("%s: binding a task requires an active workflow", prefix)
	}
	// Bounded wait: an unresponsive caller agent must not park the workspace
	// ownerLoop forever (agent_status is PureContext on the callee, so a
	// healthy agent answers well within this bound).
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := caller.Invoke(statusCtx, "agent_status", nil)
	if call == nil {
		return fmt.Errorf("%s: binding a task requires an active workflow", prefix)
	}
	defer call.Close()
	result, err := call.Final(statusCtx)
	if err != nil {
		return fmt.Errorf("%s: verify active workflow: %w", prefix, err)
	}
	var status gen.AgentStatusResp
	switch value := result.(type) {
	case gen.AgentStatusResp:
		status = value
	case *gen.AgentStatusResp:
		if value == nil {
			return fmt.Errorf("%s: binding a task requires an active workflow", prefix)
		}
		status = *value
	default:
		return fmt.Errorf("%s: verify active workflow: unexpected status response %T", prefix, result)
	}
	if status.ActiveWorkflowMapCardID == "" {
		return fmt.Errorf("%s: binding a task requires an active workflow", prefix)
	}
	return nil
}

func (a *Actor) rollbackSpawn(ctx actor.Context, ag domain.AgentRef, idx int, projectID, cardID string, claimed bool) {
	if claimed && cardID != "" {
		a.setTaskCardStatus(ctx, projectID, cardID, "backlog")
	}
	// Remove the half-spawned agent synchronously (it never had children)
	// and tear it down via the unified async path.
	a.agentsMu.Lock()
	removed := false
	if idx >= 0 && idx < len(a.Agents) {
		a.Agents = append(a.Agents[:idx], a.Agents[idx+1:]...)
		if a.agentRuntime != nil && ag.ActorID != "" {
			delete(a.agentRuntime, ag.ActorID)
		}
		removed = true
	}
	a.agentsMu.Unlock()
	if removed {
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)
	}
	a.teardownSubtreeAsync(ctx, []domain.AgentRef{ag})
}

func findAgentIdx(agents []domain.AgentRef, ag domain.AgentRef) int {
	for i, x := range agents {
		if x.ActorID == ag.ActorID && x.ID == ag.ID {
			return i
		}
	}
	return -1
}

// spawnGoalStr extracts a string field from an optional spawn goal.
func spawnGoalStr(g *domain.AgentInternalAssignGoalReq, get func(*domain.AgentInternalAssignGoalReq) string) string {
	if g == nil {
		return ""
	}
	return get(g)
}

// spawnGoalI32 extracts an int32 field from an optional spawn goal.
func spawnGoalI32(g *domain.AgentInternalAssignGoalReq, get func(*domain.AgentInternalAssignGoalReq) int32) int32 {
	if g == nil {
		return 0
	}
	return get(g)
}

// claimTaskCard fuses the status CAS and body read into a single
// project-side call (project.wiki_claim_task_card), so the workspace
// ownerLoop waits once instead of up to three serial invokes. claimed=false
// means the CAS guard rejected the claim; err carries the project-side
// reason (dependency block, status mismatch, missing card, transport).
//
// PureContext-parameterized: LookupID/ref.Invoke only — shared by the
// stateful spawn_assign orchestrator and the stateless agent_assign.
func (a *Actor) claimTaskCard(ctx actor.PureContext, projectID, cardID string, expected []string) (claimed bool, previousStatus, raw string, inputs map[string]any, err error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return false, "", "", nil, err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return false, "", "", nil, fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.wiki_claim_task_card", gen.WikiClaimTaskCardReq{
		ID:               cardID,
		Status:           "doing",
		ExpectedStatuses: expected,
	})
	if call == nil {
		return false, "", "", nil, fmt.Errorf("task card claim invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return false, "", "", nil, err
	}
	resp, ok := result.(gen.WikiClaimTaskCardResp)
	if !ok {
		return false, "", "", nil, fmt.Errorf("unexpected task card claim response %T", result)
	}
	return true, resp.PreviousStatus, resp.Raw, resp.Inputs, nil
}

// stripCardFrontmatter returns the card body without its frontmatter block.
func stripCardFrontmatter(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "---\n") {
		if end := strings.Index(raw[4:], "\n---"); end >= 0 {
			raw = strings.TrimSpace(raw[end+8:])
		}
	}
	return raw
}

// injectTaskInputs appends a structured "Task Inputs" section to the goal
// condition when the task has resolved upstream outputs (from data bindings).
// The section is formatted as a fenced YAML block so the worker can parse it
// deterministically. When inputs is empty or nil, condition is returned
// unchanged.
func injectTaskInputs(condition string, inputs map[string]any) string {
	if len(inputs) == 0 {
		return condition
	}
	b, err := json.Marshal(inputs)
	if err != nil {
		return condition
	}
	return condition + "\n\n---\n\n## Task Inputs (resolved from upstream task outputs)\n\n```yaml\n" + string(b) + "\n```\n"
}

// isDependencyBlockError reports whether a CAS-claim failure was caused by
// unmet task-card dependencies (rather than contention or a status mismatch).
// It matches the stable prefix emitted by project.wiki_set_status.
func isDependencyBlockError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "dependencies not done")
}

// isClaimStatusMiss reports whether a claim failure is a plain CAS miss
// (current status not in the expected set) — the only failure mode where
// orphan reclaim makes sense. Anything else (dependency block, transport,
// missing card) must surface to the caller unchanged. It matches the stable
// phrasing emitted by project.wiki_claim_task_card.
func isClaimStatusMiss(err error) bool {
	return err != nil && strings.Contains(err.Error(), "expected status one of")
}

// casTaskCardStatus CAS-transitions a task card's status via project.wiki_set_status.
// It returns the underlying error so callers can distinguish a dependency block or
// contention from a plain miss; a CAS miss (expected-status mismatch) yields
// (false, err) like any other failure.
func (a *Actor) casTaskCardStatus(ctx actor.PureContext, projectID, cardID, status, expected string) (bool, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return false, err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return false, fmt.Errorf("project actor unavailable")
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(statusCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:             cardID,
		Status:         status,
		ExpectedStatus: expected,
	})
	if call == nil {
		return false, fmt.Errorf("wiki_set_status invoke returned nil")
	}
	if _, err := call.Final(statusCtx); err != nil {
		return false, err
	}
	return true, nil
}

// handleAgentReview approves or rejects an agent's completed work. Both
// developer/admin callers (via UI) and the map owner (via tool call) use this
// same callable — they are structurally identical.
//
// approve: task card → done, agent torn down.
// reject:  task card → doing, goal reset, agent resumed with feedback.
//
// Runs stateless (PureContext): the handler interleaves several cross-actor
// invokes (changeset clear, merge verification, card status) with no loop
// serialization across them. Every a.Agents access between those invokes goes
// through the locked snapshot helpers (agentSnapshot/agentAt); merge/rebase
// and card status live on the project actor's own loop, so the merge-failure
// rollback (clear changeset + card status left at doing) cannot interleave
// with another review of the same card on this actor.
func (a *Actor) handleAgentReview(ctx actor.PureContext, req domain.WorkspaceAgentReviewReq) (domain.WorkspaceAgentReviewResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_review", req, func() (domain.WorkspaceAgentReviewResp, error) {
		if req.AgentActorID == "" {
			return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("workspace.agent.review: AgentActorId is required")
		}
		if req.Decision != "approve" && req.Decision != "reject" {
			return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("workspace.agent.review: Decision must be %q or %q", "approve", "reject")
		}

	ag, ok := a.findAgentRef(req.AgentActorID)
	if !ok {
		// The agent is absent from both the in-memory registry and the
		// authoritative .ragents card — it was already torn down (a prior
		// approve, a terminate/sweep, a project stop, or a restart-wipe).
		// Approve is idempotent when verifiable: if the caller names the task
		// card and that card is already done, the prior approve's flip landed
		// and a retry (lost response, duplicate disposition, stale nudge)
		// must succeed instead of dead-ending here.
		cardStatus := ""
		if req.TaskCardID != "" {
			cardStatus = a.taskCardStatusForReview(ctx, req)
		}
		if req.Decision == "approve" && cardStatus == "done" {
			return domain.WorkspaceAgentReviewResp{
				Approved:   true,
				CardStatus: "done",
				ReviewNote: "idempotent re-approve: agent already disposed and its task card is already done",
			}, nil
		}
		cardHint := fmt.Sprintf("(task card %q was not provided)", req.TaskCardID)
		if req.TaskCardID != "" {
			cardHint = fmt.Sprintf("(task card %q is %q, not done)", req.TaskCardID, cardStatus)
		}
		return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
			"workspace.agent.review: agent %q not found in registry or authoritative card — it was already torn down while its review did not complete %s. Dispose manually: flip the card with project.wiki_set_status (done or cancelled) so the map can advance; re-spawn the worker if the work is still needed",
			req.AgentActorID, cardHint)
	}

		// Direct-parent authorization: enforced BEFORE any card or agent state
		// modification. A developer/admin caller (trusted via ctx.Identity) bypasses;
		// an agent caller must match the target's direct parent and be an active
		// workflow owner. A non-developer caller with empty CallerAgentId (direct
		// Public invocation) is rejected.
		if err := requireDirectParent(ctx, "workspace.agent.review", req.CallerAgentID, ag.ParentAgentID); err != nil {
			return domain.WorkspaceAgentReviewResp{}, err
		}

		// Resolve the bound task card: prefer an explicit override, otherwise fall
		// back to the agent's live goal binding. The binding lives in the agent's
		// goal (Goal.BoundTaskCardId), not on AgentRef, so without this fallback a
		// caller that omits TaskCardId would leave the task card stuck in
		// pending_review and the root map would never auto-complete.
		if req.TaskCardID == "" {
			req.TaskCardID = a.boundTaskCardFromAgent(ctx, ag)
		}

		switch req.Decision {
		case "approve":
			return a.reviewApprove(ctx, ag, req)
		case "reject":
			return a.reviewReject(ctx, ag, req)
		}
		return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("unreachable")
	})
}

func (a *Actor) reviewApprove(ctx actor.PureContext, ag domain.AgentRef, req domain.WorkspaceAgentReviewReq) (domain.WorkspaceAgentReviewResp, error) {
	// Outputs contract gate: before approving, validate the worker's produced
	// outputs against the bound task card's data.outputs contract. A card with
	// no contract passes trivially. When validation fails the approve is
	// converted to an auto-reject back to doing, with the validation errors
	// routed to the worker as resume feedback.
	if note, ok := a.validateReviewOutputs(ctx, ag, req); !ok {
		return a.autoRejectOutputs(ctx, ag, req, note)
	}

	// Workflow worktree merge (Phase 4 item 10): if the worker has a child
	// worktree, merge it into the parent (owner) worktree before any card or
	// agent state changes. On conflict nothing is mutated — the card stays
	// pending_review, the worker stays paused, and the conflict is surfaced
	// to the caller (the workflow owner), who resolves it and retries approve.
	if workerWtID := agentWorkflowWorktreeID(ag); workerWtID != "" && ag.ParentAgentID != "" {
		if parentWtID, _ := a.queryOwnerWorkflowWorktree(ctx, ag.ParentAgentID); parentWtID != "" {
			if policy.RequireDeveloper(ctx.Identity().Role) == nil {
				// Human/admin UI: keep the existing auto-merge path.
				mergeStatus, _, mergeErr := a.mergeChildWorktreeToParent(ctx, ag.ProjectID, workerWtID, parentWtID)
				if mergeErr != nil || mergeStatus == "conflict" {
					return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
						"workspace.agent.review: approve blocked: merging worker worktree %q into owner worktree %q conflicts: %v. The task card stays pending_review and the worker stays paused — resolve the conflict (e.g. merge the worker's branch manually in your worktree, or reject with feedback asking the worker to rebase) and retry approve",
						workerWtID, parentWtID, mergeErr)
				}
			} else {
				// Agent owner: only verify that the child's branch has already
				// been merged into the parent worktree. The owner must do the
				// actual merge itself; this path will never mutate worktrees.
				verifyResp, verifyErr := a.verifyChildMergedToParent(ctx, ag.ProjectID, workerWtID, parentWtID)
				if verifyErr != nil {
					return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
						"workspace.agent.review: approve blocked: verify failed for worker worktree %q into owner worktree %q: %v",
						workerWtID, parentWtID, verifyErr)
				}
				switch verifyResp.Status {
				case "merged", "gone":
					// Proceed with the remaining approve steps below.
				case "not_merged":
					return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
						"workspace.agent.review: approve blocked: worker branch %q is not yet merged into your worktree. Merge it first:\n  git merge %s\n(a true merge, not cherry-pick/squash), resolve conflicts, verify, then retry approve.\nWorker HEAD: %s, your HEAD: %s.",
						verifyResp.Branch, verifyResp.Branch, verifyResp.ChildHead, verifyResp.ParentHead)
				default:
					return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
						"workspace.agent.review: approve blocked: unexpected verify status %q for worker worktree %q into owner worktree %q",
						verifyResp.Status, workerWtID, parentWtID)
				}
			}
		}
	}

	// Persist the worker's produced outputs onto the task card's
	// data.task_outputs block before tearing down the agent. Downstream tasks
	// (via data bindings) resolve these outputs when they are claimed. This
	// must happen before the agent is destroyed — after teardown the outputs
	// only exist in the (about-to-be-deleted) agent's goal state.
	if req.TaskCardID != "" && ag.ProjectID != "" {
		a.persistTaskOutputs(ctx, ag, req.TaskCardID)
	}

	if req.TaskCardID != "" && ag.ProjectID != "" {
		// Checked CAS pending_review → done. The card flip is the semantic
		// core of approve and MUST land before the worker is torn down: the
		// old fire-and-forget flip silently failed on a project outage or
		// card-store error, cascadeDelete then destroyed the worker, and the
		// card stayed pending_review forever — no agent left to re-review,
		// the map wedged (the T6 stall shape). The CAS also loses cleanly
		// against the mirror of reject's race: a concurrent reject already
		// resumed the worker (card back to doing) — approving then would
		// tear down a running worker and stomp its card.
		if ok, casErr := a.casTaskCardStatus(ctx, ag.ProjectID, req.TaskCardID, "done", "pending_review"); !ok {
			return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
				"workspace.agent.review: approve aborted: task card %q could not transition pending_review → done: %v. Nothing has been mutated — the card is unchanged and the worker stays registered. If the card is already resolved (e.g. a prior approve's teardown aborted), dispose the worker with workspace.agent_terminate instead; otherwise fix the card-store condition and retry approve",
				req.TaskCardID, casErr)
		}
	}

	// Clear the frozen review changeset — the work is approved, the agent is
	// about to be torn down, and the cached snapshot should be released.
	// This also increments the freeze generation counter so any in-flight
	// freeze goroutine's result is rejected (the goroutine may try to read
	// the worktree path after the merge removes it — it fails gracefully
	// and the result is silently discarded by the generation mismatch).
	a.clearReviewChangeset(ctx, ag.ProjectID, ag.ActorID, req.TaskCardID)

	// Cascade delete: mark the subtree deleting, persist the intent, emit,
	// and kick off bounded-concurrency async teardown. This returns
	// immediately without waiting for ctx.Destroy.
	subtree := a.computeDeletionSubtree(ag.ActorID)
	if len(subtree) == 0 {
		subtree = []domain.AgentRef{ag}
	}
	a.cascadeDelete(ctx, subtree)

	return domain.WorkspaceAgentReviewResp{
		Approved:   true,
		CardStatus: "done",
	}, nil
}

// agentWorkflowWorktreeID extracts the worker's child worktree ID from the
// agent's Mode state. Returns empty string when the agent has no workflow
// worktree (non-workflow worker — backward compatible).
func agentWorkflowWorktreeID(ag domain.AgentRef) string {
	if ag.Mode == nil {
		return ""
	}
	return ag.Mode.ActiveWorkflowWorktreeID
}

func (a *Actor) reviewReject(ctx actor.PureContext, ag domain.AgentRef, req domain.WorkspaceAgentReviewReq) (domain.WorkspaceAgentReviewResp, error) {
	// CAS pending_review → doing: the reject must lose cleanly when the card
	// was concurrently disposed (e.g. an approve already set done — both the
	// developer UI and the map owner can review the same worker). Resuming the
	// worker after that would start a post-approve turn whose ready_for_review
	// completion writes the card back to pending_review after done.
	if req.TaskCardID != "" && ag.ProjectID != "" {
		if ok, casErr := a.casTaskCardStatus(ctx, ag.ProjectID, req.TaskCardID, "doing", "pending_review"); !ok {
			return domain.WorkspaceAgentReviewResp{}, fmt.Errorf(
				"workspace.agent.review: reject aborted: task card %q is no longer pending_review (concurrently disposed — e.g. an approve already landed); worker not resumed: %v",
				req.TaskCardID, casErr)
		}
	}

	// Clear the frozen review changeset so the next ready_for_review generates
	// a fresh snapshot reflecting the revised work.
	a.clearReviewChangeset(ctx, ag.ProjectID, ag.ActorID, req.TaskCardID)

	cid, err := identity.ParseCanonicalID(ag.ActorID)
	if err != nil {
		return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("workspace.agent.review: invalid agent actor id: %w", err)
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok {
		return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("workspace.agent.review: agent actor not available")
	}

	feedback := req.Feedback
	if feedback == "" {
		feedback = "Your work was rejected. Please revise based on the review."
	}

	// Workflow worktree rebase (Phase 4 item 11): if the worker has a child
	// worktree, rebase it onto the parent (owner) worktree's branch HEAD
	// before resuming. This ensures the worker's worktree incorporates the
	// parent's latest changes (e.g., from parallel workers' merged work).
	// On conflict, the worker must handle the rebase conflict state.
	if workerWtID := agentWorkflowWorktreeID(ag); workerWtID != "" && ag.ParentAgentID != "" {
		if parentWtID, _ := a.queryOwnerWorkflowWorktree(ctx, ag.ParentAgentID); parentWtID != "" {
			rebaseStatus, _, rebaseErr := a.rebaseChildWorktreeToParent(ctx, ag.ProjectID, workerWtID, parentWtID)
			if rebaseErr != nil {
				feedback = "Rebase conflict on the worktree. Resolve the rebase conflicts (git rebase --continue) before continuing.\n\nError: " + rebaseErr.Error() + "\n\n" + feedback
			} else if rebaseStatus == "rebased" {
				feedback = "Your worktree has been rebased to the latest owner branch HEAD.\n\n" + feedback
			}
		}
	}

	// Do not wait for the agent owner loop: the resumed turn can synchronously
	// query workspace and otherwise form a circular wait with this owner loop.
	call := agentRef.Invoke(ctx.Lifecycle(), "internal_resume_from_review", domain.AgentInternalResumeFromReviewReq{Feedback: feedback})
	if call == nil {
		return domain.WorkspaceAgentReviewResp{}, fmt.Errorf("workspace.agent.review: resume invoke returned nil")
	}
	_ = call.Close()

	return domain.WorkspaceAgentReviewResp{
		Approved:   false,
		CardStatus: "doing",
	}, nil
}

// validateReviewOutputs checks the worker's produced outputs against the bound
// task card's data.outputs contract by delegating to project.task_validate_outputs.
// Returns (note, true) when validation passes (or no contract / project
// unreachable); returns (note, false) when the contract is violated, with note
// carrying a human-readable summary of the violations.
//
// When the project actor cannot be reached, validation is skipped (fail-open)
// rather than blocking a legitimate approve: most cards declare no contract,
// and a transient project outage must not wedge the review loop. The frozen
// changeset and human review still apply as guardrails.
func (a *Actor) validateReviewOutputs(ctx actor.PureContext, ag domain.AgentRef, req domain.WorkspaceAgentReviewReq) (string, bool) {
	if req.TaskCardID == "" || ag.ProjectID == "" {
		return "", true
	}
	outputs := a.workerOutputs(ctx, ag)
	resp, err := a.invokeTaskValidateOutputs(ctx, ag.ProjectID, req.TaskCardID, outputs)
	if err != nil {
		ctx.Logger().Warn("workspace.agent.review: outputs validation unavailable, skipping", "error", err, "card", req.TaskCardID)
		return "", true
	}
	if resp.Valid {
		return "", true
	}
	return formatOutputErrors(resp.Errors), false
}

// autoRejectOutputs converts an approve into a reject because the worker's
// outputs failed contract validation. It reuses the reject path (card → doing,
// agent resumed with feedback) and annotates the response with the validation
// note so the caller knows the approve was overridden.
func (a *Actor) autoRejectOutputs(ctx actor.PureContext, ag domain.AgentRef, req domain.WorkspaceAgentReviewReq, note string) (domain.WorkspaceAgentReviewResp, error) {
	feedback := "Your work cannot be approved yet: the produced outputs do not satisfy the task card's data.outputs contract. Fix the outputs and declare ready_for_review again.\n\n" + note
	rejectReq := req
	rejectReq.Decision = "reject"
	rejectReq.Feedback = feedback
	resp, err := a.reviewReject(ctx, ag, rejectReq)
	if err != nil {
		return resp, err
	}
	resp.ReviewNote = "approve converted to auto-reject: outputs contract validation failed — " + note
	return resp, nil
}

// persistTaskOutputs stamps the worker's produced outputs onto the task card via
// project.wiki.set_task_outputs so downstream tasks can resolve them via data
// bindings. Fire-and-forget: a failure here does not block the approve — the
// card still reaches done. Missing outputs (no contract, no ready_for_review
// capture) are a no-op.
func (a *Actor) persistTaskOutputs(ctx actor.PureContext, ag domain.AgentRef, taskCardID string) {
	outputs := a.workerOutputs(ctx, ag)
	if len(outputs) == 0 {
		return
	}
	cid, err := identity.ParseCanonicalID(ag.ProjectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	outCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(outCtx, "project.wiki_set_task_outputs", gen.WikiSetTaskOutputsReq{
		CardID:  taskCardID,
		Outputs: outputs,
	})
	if call != nil {
		_ = call.Close()
	}
}

// stampTaskCardOwner writes the worker's actor id into the bound task card's
// data.ownerAgentId via project.wiki_set_map_owner, so card-side consumers
// (workflow fold classification, topology, card menu) can resolve the binding
// without waiting for the agent list. Non-fatal: a stamp failure is logged but
// does not block the spawn — the agent is already working and the binding
// lives in the workspace's agent state regardless. Cleared on review-approve
// /terminate by handleWikiUnbindAgent (cascade delete path).
//
// PureContext-parameterized: LookupID/ref.Invoke only — shared by the
// stateful spawn_assign orchestrator and the stateless agent_assign.
func (a *Actor) stampTaskCardOwner(ctx actor.PureContext, projectID, cardID, agentActorID string) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	stampCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(stampCtx, "project.wiki_set_map_owner", gen.WikiSetMapOwnerReq{
		MapID:        cardID,
		OwnerActorID: agentActorID,
	})
	if call == nil {
		ctx.Logger().Warn("workspace: stampTaskCardOwner invoke returned nil", "card", cardID, "agent", agentActorID)
		return
	}
	if _, err := call.Final(stampCtx); err != nil {
		ctx.Logger().Warn("workspace: stampTaskCardOwner failed", "card", cardID, "agent", agentActorID, "error", err)
	}
}

// workerOutputs reads the worker agent's captured ready_for_review outputs via
// agent_status (Goal.Outputs). Returns nil when the agent is unreachable or has
// no captured outputs.
func (a *Actor) workerOutputs(ctx actor.PureContext, ag domain.AgentRef) map[string]any {
	if ag.ActorID == "" {
		return nil
	}
	cid, err := identity.ParseCanonicalID(ag.ActorID)
	if err != nil {
		return nil
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return nil
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := agentRef.Invoke(statusCtx, "agent_status", nil)
	if call == nil {
		return nil
	}
	defer call.Close()
	v, err := call.Final(statusCtx)
	if err != nil || v == nil {
		return nil
	}
	var status gen.AgentStatusResp
	switch s := v.(type) {
	case gen.AgentStatusResp:
		status = s
	case *gen.AgentStatusResp:
		if s == nil {
			return nil
		}
		status = *s
	}
	if status.Goal != nil {
		return status.Goal.Outputs
	}
	return nil
}

// invokeTaskValidateOutputs calls project.task_validate_outputs to validate the
// worker's outputs against the card's declared contract.
func (a *Actor) invokeTaskValidateOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) (gen.ProjectTaskValidateOutputsResp, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.ProjectTaskValidateOutputsResp{}, err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return gen.ProjectTaskValidateOutputsResp{}, fmt.Errorf("project actor unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(callCtx, "project.task_validate_outputs", gen.ProjectTaskValidateOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	})
	if call == nil {
		return gen.ProjectTaskValidateOutputsResp{}, fmt.Errorf("task_validate_outputs invoke returned nil")
	}
	result, err := call.Final(callCtx)
	if err != nil {
		return gen.ProjectTaskValidateOutputsResp{}, err
	}
	resp, ok := result.(gen.ProjectTaskValidateOutputsResp)
	if !ok {
		return gen.ProjectTaskValidateOutputsResp{}, fmt.Errorf("unexpected task_validate_outputs response %T", result)
	}
	return resp, nil
}

// formatOutputErrors renders validation errors as a bulleted, human-readable
// list for inclusion in reject feedback.
func formatOutputErrors(errs []gen.CardValidationError) string {
	if len(errs) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, e := range errs {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(fmt.Sprintf("- [%s] %s: %s", e.Code, e.Field, e.Message))
	}
	return sb.String()
}

// handleAgentTerminate tears down a child agent (an agent spawned by another
// agent: workflow workers with a BoundTaskCardId, fork children, other
// spawned subagents). Used by the parent agent or the UI when a child agent
// fails or is unresponsive. Failed workers release their task card before
// teardown.
func (a *Actor) handleAgentTerminate(ctx actor.PureContext, req domain.WorkspaceAgentTerminateReq) (domain.WorkspaceAgentTerminateResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_terminate", req, func() (domain.WorkspaceAgentTerminateResp, error) {
		if req.AgentActorID == "" {
			return domain.WorkspaceAgentTerminateResp{}, fmt.Errorf("workspace.agent.terminate: AgentActorId is required")
		}

		ag, ok := a.findAgentRef(req.AgentActorID)
		if !ok {
			return domain.WorkspaceAgentTerminateResp{}, fmt.Errorf("workspace.agent.terminate: agent %q not found in registry or authoritative card", req.AgentActorID)
		}
		// Any child agent (spawned by another agent: workflow workers, fork
		// children, other spawned subagents) may be terminated through this
		// callable. Top-level agents (scheduler-managed, user-created) must
		// not be torn down here.
		if ag.AgentKind != domain.AgentKindWorker && ag.ParentAgentID == "" {
			return domain.WorkspaceAgentTerminateResp{}, fmt.Errorf("workspace.agent.terminate: only child agents may be terminated, %q is top-level (kind=%q, scope=%q)", req.AgentActorID, ag.AgentKind, ag.LifecycleScope)
		}

		if ag.AgentKind == domain.AgentKindWorker {
			// Direct-parent authorization: enforced BEFORE any state modification.
			// A developer/admin caller (trusted via ctx.Identity) bypasses; an agent
			// caller must match the target's direct parent and be an active workflow
			// owner. A non-developer caller with empty CallerAgentId is rejected.
			if err := requireDirectParent(ctx, "workspace.agent.terminate", req.CallerAgentID, ag.ParentAgentID); err != nil {
				return domain.WorkspaceAgentTerminateResp{}, err
			}

			// Recover the binding before teardown removes the worker actor, then release
			// only an in-progress or review-pending card for reuse.
			if cardID := a.boundTaskCardFromAgent(ctx, ag); cardID != "" && ag.ProjectID != "" {
				if ok, _ := a.casTaskCardStatus(ctx, ag.ProjectID, cardID, "backlog", "doing"); !ok {
					_, _ = a.casTaskCardStatus(ctx, ag.ProjectID, cardID, "backlog", "pending_review")
				}
			}
		} else {
			// Non-worker children (fork children, other spawned subagents) have
			// no bound task card and no workflow-owner parent check. They must
			// still be authorized: only the child itself (self-termination via
			// terminateSelf) or its direct parent agent may terminate it.
			// Trusted developer/admin callers (UI) bypass authorization.
			if policy.RequireDeveloper(ctx.Identity().Role) == nil {
				// allowed — proceed to cascade delete
			} else if req.CallerAgentID == "" {
				return domain.WorkspaceAgentTerminateResp{}, fmt.Errorf("workspace.agent.terminate: child agent requires caller identity (role=%q)", ctx.Identity().Role)
			} else if req.CallerAgentID != ag.ActorID && req.CallerAgentID != ag.ParentAgentID {
				return domain.WorkspaceAgentTerminateResp{}, fmt.Errorf("workspace.agent.terminate: only the child agent itself or its direct parent may terminate it (caller=%q, target=%q, parent=%q)", req.CallerAgentID, ag.ActorID, ag.ParentAgentID)
			}
		}

		// Cascade delete: mark the subtree deleting, persist the intent, emit,
		// and kick off bounded-concurrency async teardown. This returns
		// immediately without waiting for ctx.Destroy.
		subtree := a.computeDeletionSubtree(ag.ActorID)
		if len(subtree) == 0 {
			subtree = []domain.AgentRef{ag}
		}
		a.cascadeDelete(ctx, subtree)

		ctx.Logger().Info("workspace.agent.terminate: agent terminated", "agent", ag.ActorID, "reason", req.Reason)
		return domain.WorkspaceAgentTerminateResp{Deleted: true}, nil
	})
}

func (a *Actor) hasLiveWorkerForTaskCard(ctx actor.PureContext, projectID, cardID string) bool {
	for _, ag := range a.agentSnapshot() {
		if ag.AgentKind != domain.AgentKindWorker || ag.ProjectID != projectID || ag.DeletionStatus == "deleting" {
			continue
		}
		if a.boundTaskCardFromAgent(ctx, ag) == cardID {
			return true
		}
	}
	return false
}

// boundTaskCardFromAgent queries an agent's live status to recover the task
// card it is bound to. The binding lives in the agent's goal
// (Goal.BoundTaskCardId), not on AgentRef; review uses this to advance the
// card status when the caller omits an explicit TaskCardId.
func (a *Actor) boundTaskCardFromAgent(ctx actor.PureContext, ag domain.AgentRef) string {
	if ag.Mode != nil && ag.Mode.BoundTaskCardID != "" {
		return ag.Mode.BoundTaskCardID
	}
	if ag.LoadState != "loaded" || ag.ActorID == "" {
		return ""
	}
	cid, err := identity.ParseCanonicalID(ag.ActorID)
	if err != nil {
		return ""
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return ""
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := agentRef.Invoke(statusCtx, "agent_status", nil)
	if call == nil {
		return ""
	}
	v, err := call.Final(statusCtx)
	if err != nil || v == nil {
		return ""
	}
	switch s := v.(type) {
	case gen.AgentStatusResp:
		if s.Goal != nil {
			return s.Goal.BoundTaskCardID
		}
	case *gen.AgentStatusResp:
		if s != nil && s.Goal != nil {
			return s.Goal.BoundTaskCardID
		}
	}
	return ""
}

// PureContext-parameterized: fire-and-forget project invoke, shared by the
// stateful spawn_assign/review paths and the stateless agent_assign.
func (a *Actor) setTaskCardStatus(ctx actor.PureContext, projectID, cardID, status string) {
	if projectID == "" || cardID == "" {
		return
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(statusCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:     cardID,
		Status: status,
	})
	if call != nil {
		_ = call.Close()
	}
}

func (a *Actor) resolveWorkflowProjectID(projectID, callerAgentID string) (string, error) {
	if projectID != "" {
		if _, ok := a.findMountByActorID(projectID); ok {
			return projectID, nil
		}
		var match string
		a.mountMu.RLock()
		for _, m := range a.Mounts {
			if m.Name != projectID {
				continue
			}
			if match != "" {
				a.mountMu.RUnlock()
				return "", fmt.Errorf("project %q is ambiguous", projectID)
			}
			match = m.ActorID
		}
		a.mountMu.RUnlock()
		if match == "" {
			return "", fmt.Errorf("project %q not found", projectID)
		}
		return match, nil
	}
	if callerAgentID != "" {
		for _, ag := range a.agentSnapshot() {
			if (ag.ActorID == callerAgentID || ag.ID == callerAgentID) && ag.ProjectID != "" {
				if _, ok := a.findMountByActorID(ag.ProjectID); ok {
					return ag.ProjectID, nil
				}
			}
		}
	}
	a.mountMu.RLock()
	for _, m := range a.Mounts {
		if m.ActorID != "" && !m.System {
			a.mountMu.RUnlock()
			return m.ActorID, nil
		}
	}
	a.mountMu.RUnlock()
	return "", fmt.Errorf("no project available; specify ProjectId")
}

// clearReviewChangeset drops the frozen changeset for the given child agent via
// the project actor. Fire-and-forget: uses Invoke without Await so the review
// path never blocks on the project actor's loop.
//
// After reject: clearing ensures the next pending_review generates a fresh
// snapshot reflecting the child's revised work. The child will continue
// modifying code after receiving rejection feedback, so a stale snapshot would
// mislead the reviewer on the next review cycle.
//
// After approve: the child is being torn down; clearing frees memory
// proactively (the worktree teardown also clears via releaseWorktreeBinding).
func (a *Actor) clearReviewChangeset(ctx actor.PureContext, projectID, agentActorID, taskCardID string) {
	if projectID == "" || agentActorID == "" {
		return
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	// Fire-and-forget: do not block on the project actor's loop. If the
	// clear fails, the worktree teardown will clean up the changeset as a
	// safety net.
	clearCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	call := projectRef.Invoke(clearCtx, "project.review_changeset_clear",
		gen.ProjectReviewChangesetReq{
			AgentActorID: agentActorID,
			TaskCardID:   taskCardID,
		})
	if call != nil {
		_ = call.Close()
	}
	cancel()
}

// ── Workflow worktree integration helpers (Phase 4: items 9-14) ──

// queryOwnerWorkflowWorktree queries the owner agent's status to get the
// active workflow worktree ID and map card ID. Returns empty strings when
// the agent has no active workflow or is unreachable — callers treat this
// as a non-workflow spawn (backward compatible).
func (a *Actor) queryOwnerWorkflowWorktree(ctx actor.PureContext, ownerAgentID string) (worktreeID, mapCardID string) {
	if ownerAgentID == "" {
		return "", ""
	}
	cid, err := identity.ParseCanonicalID(ownerAgentID)
	if err != nil {
		return "", ""
	}
	caller, ok := ctx.LookupID(id.From(cid))
	if !ok || caller == nil {
		return "", ""
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := caller.Invoke(statusCtx, "agent_status", nil)
	if call == nil {
		return "", ""
	}
	defer call.Close()
	v, err := call.Final(statusCtx)
	if err != nil || v == nil {
		return "", ""
	}
	var status gen.AgentStatusResp
	switch s := v.(type) {
	case gen.AgentStatusResp:
		status = s
	case *gen.AgentStatusResp:
		if s == nil {
			return "", ""
		}
		status = *s
	default:
		return "", ""
	}
	return status.ActiveWorkflowWorktreeID, status.ActiveWorkflowMapCardID
}

// createWorkflowChildWorktree creates a child worktree derived from the
// owner's worktree via project.worktree_create. The branch name comes from
// branchName when the caller supplied one (git-sanitized as usual), else
// from the worker agent's display name (whitespace → '-', git-forbidden
// characters dropped) plus a short uuid suffix — display names are not
// unique and task card titles can contain CJK or special characters, so
// callers should pass an ASCII slug when the card title is not branch-safe.
// The child starts from the owner's current branch HEAD (git rev-parse,
// resolved by handleWorktreeCreate when BaseRef is empty and
// ParentWorktreeID is set). Returns the new worktree ID.
func (a *Actor) createWorkflowChildWorktree(ctx actor.PureContext, projectID, ownerWorktreeID, mapCardID, branchName, displayName string) (string, error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "", fmt.Errorf("createWorkflowChildWorktree: invalid project ID: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "", fmt.Errorf("createWorkflowChildWorktree: project %q not found", projectID)
	}
	base := branchName
	if base == "" {
		base = displayName
	}
	name := worktreeBranchName(base)
	createCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(createCtx, "project.worktree_create", gen.ProjectWorktreeCreateReq{
		Name:             name,
		BaseRef:          "", // empty — project resolves from parent HEAD via git rev-parse
		ParentWorktreeID: ownerWorktreeID,
		WorkflowMapID:    mapCardID,
	})
	if call == nil {
		return "", fmt.Errorf("createWorkflowChildWorktree: project.worktree_create invoke returned nil")
	}
	defer call.Close()
	result, err := call.Final(createCtx)
	if err != nil {
		return "", fmt.Errorf("createWorkflowChildWorktree: %w", err)
	}
	wt, ok := result.(gen.ProjectWorktree)
	if !ok {
		return "", fmt.Errorf("createWorkflowChildWorktree: unexpected response type %T", result)
	}
	return wt.ID, nil
}

// worktreeBranchName builds a git-safe branch name from an agent display
// name: whitespace becomes '-', git ref-forbidden characters are dropped,
// and a short uuid suffix guarantees uniqueness (display names repeat
// across spawns).
func worktreeBranchName(displayName string) string {
	var b strings.Builder
	for _, r := range displayName {
		switch {
		case unicode.IsSpace(r):
			b.WriteByte('-')
		case strings.ContainsRune("~^:?*[]\\", r):
			// git ref-forbidden, drop
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Trim(b.String(), "-. ")
	name = collapseHyphens(name)
	if name == "" {
		name = "wf"
	}
	return name + "-" + uuid.NewString()[:8]
}

func collapseHyphens(s string) string {
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}

// discardOrphanWorktree discards a worktree by ID (best-effort, logs on
// failure). Used during spawn failure cleanup to prevent orphan worktree
// leaks (leak #10 from adversarial analysis).
func (a *Actor) discardOrphanWorktree(ctx actor.PureContext, projectID, worktreeID string) {
	if projectID == "" || worktreeID == "" {
		return
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return
	}
	discardCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(discardCtx, "project.worktree_discard_by_id", gen.ProjectWorktreeDiscardByIDReq{
		WorktreeID: worktreeID,
		Force:      true,
	})
	if call != nil {
		_, _ = call.Final(discardCtx)
		_ = call.Close()
	}
}

// mergeChildWorktreeToParent merges a child worktree's branch into its
// parent worktree via project.worktree_merge_to_parent. Returns the merge
// status ("merged" or "conflict") and conflict files (if any).
//
// Due to the invoke framework's Once() semantics, when the handler returns
// both (resp, err) the value is discarded on error. So on error we return
// status="conflict" with the error for the caller to handle.
func (a *Actor) mergeChildWorktreeToParent(ctx actor.PureContext, projectID, childWorktreeID, parentWorktreeID string) (status string, conflictFiles []string, err error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "error", nil, fmt.Errorf("mergeChildWorktreeToParent: invalid project ID: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "error", nil, fmt.Errorf("mergeChildWorktreeToParent: project %q not found", projectID)
	}
	mergeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(mergeCtx, "project.worktree_merge_to_parent", gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  childWorktreeID,
		ParentWorktreeID: parentWorktreeID,
	})
	if call == nil {
		return "error", nil, fmt.Errorf("mergeChildWorktreeToParent: invoke returned nil")
	}
	defer call.Close()
	result, err := call.Final(mergeCtx)
	if err != nil {
		// The handler returns (resp{Status:"conflict"}, err) on merge
		// conflict, but the invoke framework's Once() discards the value
		// on error. The error message contains conflict details.
		return "conflict", nil, err
	}
	resp, ok := result.(gen.ProjectWorktreeMergeToParentResp)
	if !ok {
		return "error", nil, fmt.Errorf("mergeChildWorktreeToParent: unexpected response type %T", result)
	}
	return resp.Status, resp.ConflictFiles, nil
}

// verifyChildMergedToParent checks that a child worktree's branch has already
// been merged into its parent worktree via
// project.worktree_verify_merged_to_parent. It performs lineage verification
// only — it never mutates either worktree. Returns the verify response
// (Status "merged", "gone" or "not_merged", plus branch and HEAD info) and any
// invocation error.
func (a *Actor) verifyChildMergedToParent(ctx actor.PureContext, projectID, childWorktreeID, parentWorktreeID string) (resp gen.ProjectWorktreeVerifyMergedResp, err error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("verifyChildMergedToParent: invalid project ID: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("verifyChildMergedToParent: project %q not found", projectID)
	}
	verifyCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(verifyCtx, "project.worktree_verify_merged_to_parent", gen.ProjectWorktreeVerifyMergedReq{
		ChildWorktreeID:  childWorktreeID,
		ParentWorktreeID: parentWorktreeID,
	})
	if call == nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("verifyChildMergedToParent: invoke returned nil")
	}
	defer call.Close()
	result, err := call.Final(verifyCtx)
	if err != nil {
		return gen.ProjectWorktreeVerifyMergedResp{}, err
	}
	resp, ok = result.(gen.ProjectWorktreeVerifyMergedResp)
	if !ok {
		return gen.ProjectWorktreeVerifyMergedResp{}, fmt.Errorf("verifyChildMergedToParent: unexpected response type %T", result)
	}
	return resp, nil
}

// rebaseChildWorktreeToParent rebases a child worktree's branch onto its
// parent's branch HEAD via project.worktree_rebase_to_parent. Returns the
// rebase status ("rebased" or "conflict") and conflict files (if any).
func (a *Actor) rebaseChildWorktreeToParent(ctx actor.PureContext, projectID, childWorktreeID, parentWorktreeID string) (status string, conflictFiles []string, err error) {
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return "error", nil, fmt.Errorf("rebaseChildWorktreeToParent: invalid project ID: %w", err)
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return "error", nil, fmt.Errorf("rebaseChildWorktreeToParent: project %q not found", projectID)
	}
	rebaseCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := projectRef.Invoke(rebaseCtx, "project.worktree_rebase_to_parent", gen.ProjectWorktreeRebaseToParentReq{
		ChildWorktreeID:  childWorktreeID,
		ParentWorktreeID: parentWorktreeID,
	})
	if call == nil {
		return "error", nil, fmt.Errorf("rebaseChildWorktreeToParent: invoke returned nil")
	}
	defer call.Close()
	result, err := call.Final(rebaseCtx)
	if err != nil {
		return "conflict", nil, err
	}
	resp, ok := result.(gen.ProjectWorktreeRebaseToParentResp)
	if !ok {
		return "error", nil, fmt.Errorf("rebaseChildWorktreeToParent: unexpected response type %T", result)
	}
	return resp.Status, resp.ConflictFiles, nil
}

// requireLiveAgent verifies that callerAgentID resolves to a live actor via
// LookupID. Unlike requireActiveWorkflow it does NOT require the agent to have
// an active workflow (ActiveWorkflowMapCardId) — fork_explore/fork_general/
// fork_review must remain available to plain non-workflow agents. The defense
// is that CallerAgentId is unconditionally overwritten by the turn engine
// (injectCallerAgentID), so a non-empty value that resolves to a live agent
// is the authoritative caller identity, not client-supplied data.
//
// PureContext-parameterized: LookupID only — usable from stateless handlers.
func requireLiveAgent(ctx actor.PureContext, prefix, callerAgentID string) (ref.Ref, error) {
	if callerAgentID == "" {
		return nil, fmt.Errorf("%s: requires CallerAgentId injected by the turn engine", prefix)
	}
	cid, err := identity.ParseCanonicalID(callerAgentID)
	if err != nil {
		return nil, fmt.Errorf("%s: invalid CallerAgentId: %w", prefix, err)
	}
	r, ok := ctx.LookupID(id.From(cid))
	if !ok || r == nil {
		return nil, fmt.Errorf("%s: caller agent %q is not live", prefix, callerAgentID)
	}
	return r, nil
}

// invokeResolveChildSlot resolves a child agent's model slots by calling the
// caller (parent) agent's internal resolve_child_slot callable. Both fork
// children (agent_spawn_by_type) and workflow workers (agent_spawn_assign)
// inherit their model slots from the parent's runtime slots through this path.
// unit, when non-nil, overrides slot resolution on the parent.
//
// The returned Resp carries the resolved primary-purpose slot (Slot, priority
// logic untouched on the agent side) plus the parent's non-primary runtime
// slots (Fast/Execution/Review/Summary) so callers can copy them to the child
// spawn; empty ([auto]) slots are zero-valued and callers pass nil for them.
//
// PureContext-parameterized: ref.Invoke only — shared by the stateless
// agent_spawn_by_type and the stateful executors (worker_task, sub_map).
func invokeResolveChildSlot(ctx actor.PureContext, prefix, callerAgentID, agentKind string, unit *domain.ModelUnit) (agentactor.ResolveChildSlotResp, error) {
	parentRef, err := requireLiveAgent(ctx, prefix, callerAgentID)
	if err != nil {
		return agentactor.ResolveChildSlotResp{}, err
	}
	slotCtx, slotCancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer slotCancel()
	slotCall := parentRef.Invoke(slotCtx, "resolve_child_slot", agentactor.ResolveChildSlotReq{
		ChildKind: agentKind,
		Unit:      unit,
	})
	if slotCall == nil {
		return agentactor.ResolveChildSlotResp{}, fmt.Errorf("%s: resolve_child_slot not available on caller", prefix)
	}
	v, serr := slotCall.Final(slotCtx)
	slotCall.Close()
	if serr != nil {
		return agentactor.ResolveChildSlotResp{}, fmt.Errorf("%s: resolve_child_slot failed: %w", prefix, serr)
	}
	switch vs := v.(type) {
	case agentactor.ResolveChildSlotResp:
		return vs, nil
	case *agentactor.ResolveChildSlotResp:
		if vs != nil {
			return *vs, nil
		}
	}
	// Unexpected response shape: fall back to the zero resp ([auto]).
	return agentactor.ResolveChildSlotResp{}, nil
}

// invokeOwnerComponentMounts fetches the caller (owner) agent's mounted
// components over ref.Invoke, so spawn_assign workers can inherit bundles the
// owner actually mounted (e.g. plugin-dev). agent.component_list reads the
// agent's persisted ComponentMounts, so it is authoritative even between turns
// when the component snapshot cache is empty. Failures degrade to nil:
// inheritance is best-effort and must never block the spawn — an owner that
// cannot report its mounts simply passes nothing down.
// PureContext-parameterized: ref.Invoke only — shared by executors.
func invokeOwnerComponentMounts(ctx actor.PureContext, prefix, callerAgentID string) []domain.AgentComponentMount {
	parentRef, err := requireLiveAgent(ctx, prefix, callerAgentID)
	if err != nil {
		return nil
	}
	mountCtx, mountCancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer mountCancel()
	mountCall := parentRef.Invoke(mountCtx, "component_list", domain.AgentComponentListReq{})
	if mountCall == nil {
		return nil
	}
	defer mountCall.Close()
	v, serr := mountCall.Final(mountCtx)
	if serr != nil {
		ctx.Logger().Warn("workspace: owner component_list failed; no bundle inheritance",
			"owner", callerAgentID, "error", serr)
		return nil
	}
	switch vs := v.(type) {
	case domain.AgentComponentListResp:
		return vs.Items
	case *domain.AgentComponentListResp:
		if vs != nil {
			return vs.Items
		}
	}
	return nil
}

// spawnSlotPtr returns the slot in the pointer form expected by
// spawnAgentViaProject / ProjectSpawnAgentReq, or nil when the slot is empty
// ([auto], no candidates) so the spawned child keeps the default [auto].
func spawnSlotPtr(slot domain.ModelSlot) *domain.ModelSlot {
	if len(slot.Candidates) == 0 {
		return nil
	}
	s := slot
	return &s
}

// handleAgentSpawnByType is the sole fork-child spawn entry point. The workspace
// validates the request, authorizes the caller, resolves the parent's model slot,
// calls project.spawn_agent with ChildConfig, and registers the child in
// workspace.Agents with LifecycleScope="fork". This replaces the former
// fork_agent delegation that required a round-trip through the parent agent.
//
// Internal boundary:
//
//	Workspace (this handler) = sole spawner: validation, authorization, slot
//	resolution, project.spawn_agent call, child registration.
//	Parent agent (resolve_child_slot) = slot resolver (exposes runtime slot state
//	to workspace without needing the agent to own the spawn lifecycle).
//	Turn engine (injectCallerAgentID) = unforgeable identity injection.
//
// PureContext (stateless): all cross-actor hops are ref.Invokes
// (resolve_child_slot, project.spawn_agent, plan-card reads); registry
// mutation runs under agentsMu; child names come from the atomic
// childNameSeq; persistence/events use the atomic-snapshot emit path. No
// ownerLoop or lifecycle-lane occupancy.
func (a *Actor) handleAgentSpawnByType(ctx actor.PureContext, req domain.WorkspaceAgentSpawnByTypeReq) (domain.WorkspaceAgentSpawnByTypeResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_spawn_by_type", req, func() (domain.WorkspaceAgentSpawnByTypeResp, error) {
		const prefix = "workspace.agent_spawn_by_type"

		if req.AgentKind == "" {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: AgentKind is required", prefix)
		}
		if err := domain.ValidateAgentKind(req.AgentKind); err != nil {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: %w", prefix, err)
		}
		if strings.TrimSpace(req.Description) == "" {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: Description is required", prefix)
		}

		parentProjectID := ""
		for _, ag := range a.agentSnapshot() {
			if ag.ActorID == req.CallerAgentID || ag.ID == req.CallerAgentID {
				parentProjectID = ag.ProjectID
				break
			}
		}

		slotResp, err := invokeResolveChildSlot(ctx, prefix, req.CallerAgentID, req.AgentKind, req.Unit)
		if err != nil {
			return domain.WorkspaceAgentSpawnByTypeResp{}, err
		}

		spawnCtx, spawnCancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		defer spawnCancel()

		var projectActorID string
		var projectAppKind string
		a.mountMu.RLock()
		for _, m := range a.Mounts {
			if m.ActorID == parentProjectID {
				projectActorID = m.ActorID
				projectAppKind = m.AppKind
				break
			}
		}
		a.mountMu.RUnlock()
		if projectActorID == "" {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: parent project not found", prefix)
		}
		cid, perr := identity.ParseCanonicalID(projectActorID)
		if perr != nil {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: invalid project actor ID: %w", prefix, perr)
		}
		projectRef, pok := ctx.LookupID(id.From(cid))
		if !pok {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: project actor not available", prefix)
		}

		childPrompt := req.Prompt
		if req.AgentKind == domain.AgentKindReviewer {
			childPrompt = agentactor.BuildReviewPrompt(req.Prompt, loadPlanEvidence(spawnCtx, projectRef, req.PlanEvidence), "")
		}

		childName := fmt.Sprintf("%s-%d-%s", req.AgentKind, a.childNameSeq.Add(1), req.ToolUseID)
		primarySlot := slotResp.Slot
		parentTurnID := req.ParentTurnID

		childConfig := domain.ChildSpawnConfig{
			ParentActorID:   req.CallerAgentID,
			ParentTurnID:    parentTurnID,
			ParentStepID:    req.ParentStepID,
			ParentToolUseID: req.ToolUseID,
			Task:            req.Description,
			Prompt:          childPrompt,
			MaxIterations:   req.MaxIterations,
		}

		spawnReq := domain.ProjectSpawnAgentReq{
			SpawnName:   childName,
			AgentKind:   req.AgentKind,
			WorkspaceID: a.actorID,
			DisplayName: domain.AgentKindDisplayName(req.AgentKind),
			Primary:     &primarySlot,
			// Copy the parent's non-primary runtime slots so the fork child
			// inherits the same fast/execution/review/summary configuration;
			// empty ([auto]) slots stay nil so the child keeps the default.
			Fast:           spawnSlotPtr(slotResp.Fast),
			Execution:      spawnSlotPtr(slotResp.Execution),
			Review:         spawnSlotPtr(slotResp.Review),
			Summary:        spawnSlotPtr(slotResp.Summary),
			ParentAgentID:  req.CallerAgentID,
			ChildConfig:    &childConfig,
			PermissionMode: a.globalPermissionMode(),
		}

		if parentProjectID != "" {
			spawnReq.ProjectID = parentProjectID
		}
		// Sub-agents of an agent in a dev-app project inherit the
		// plugin-dev bundle so fork children can also drive the dev
		// callables (dev_guide / dev_gate are read-only-friendly).
		spawnReq.ExtraBundleIDs = extraBundlesForAppKind(projectAppKind, nil)

		spawnCall := projectRef.Invoke(spawnCtx, "project.spawn_agent", spawnReq)
		if spawnCall == nil {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: project.spawn_agent not available", prefix)
		}
		defer spawnCall.Close()
		spawnV, spawnErr := spawnCall.Final(spawnCtx)
		if spawnErr != nil {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: project.spawn_agent failed: %w", prefix, spawnErr)
		}

		var resp domain.ProjectSpawnAgentResp
		switch x := spawnV.(type) {
		case domain.ProjectSpawnAgentResp:
			resp = x
		case *domain.ProjectSpawnAgentResp:
			if x != nil {
				resp = *x
			}
		default:
			// Should not happen; ProjectSpawnAgentResp is a struct, not an interface.
		}

		if resp.ActorID == "" {
			return domain.WorkspaceAgentSpawnByTypeResp{}, fmt.Errorf("%s: project.spawn_agent returned empty ActorID", prefix)
		}

		displayName := domain.AgentKindDisplayName(req.AgentKind)
		childAgent := domain.AgentRef{
			ID:             resp.ActorID,
			ActorID:        resp.ActorID,
			DisplayName:    displayName,
			AgentKind:      req.AgentKind,
			ProjectID:      parentProjectID,
			Status:         "active",
			LoadState:      "loaded",
			Primary:        spawnSlotPtr(primarySlot),
			Fast:           spawnSlotPtr(slotResp.Fast),
			Execution:      spawnSlotPtr(slotResp.Execution),
			Review:         spawnSlotPtr(slotResp.Review),
			Summary:        spawnSlotPtr(slotResp.Summary),
			ParentAgentID:  req.CallerAgentID,
			LifecycleScope: "fork",
		}
		a.agentsMu.Lock()
		a.Agents = append(a.Agents, childAgent)
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)

		return domain.WorkspaceAgentSpawnByTypeResp{
			ChildActorID: resp.ActorID,
			DisplayName:  displayName,
		}, nil
	})
}

// loadPlanEvidence loads the raw bodies of the plan cards listed in ids via
// project.wiki_get_card. Cards that fail to load are kept as an explicit
// "[unavailable]" marker so the reviewer sees the gap instead of silently
// missing evidence. Returns nil when ids is empty.
func loadPlanEvidence(ctx context.Context, projectRef ref.Ref, ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, cardID := range ids {
		call := projectRef.Invoke(ctx, "project.wiki_get_card", domain.WikiGetCardReq{ID: cardID})
		if call == nil {
			out = append(out, fmt.Sprintf("[plan card %q unavailable: wiki_get_card not registered]", cardID))
			continue
		}
		v, err := call.Final(ctx)
		call.Close()
		if err != nil {
			out = append(out, fmt.Sprintf("[plan card %q unavailable: %v]", cardID, err))
			continue
		}
		switch c := v.(type) {
		case domain.WikiGetCardResp:
			out = append(out, c.Raw)
		case *domain.WikiGetCardResp:
			if c != nil {
				out = append(out, c.Raw)
			}
		default:
			out = append(out, fmt.Sprintf("[plan card %q unavailable: unexpected response]", cardID))
		}
	}
	return out
}
