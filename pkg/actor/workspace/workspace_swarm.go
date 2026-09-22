package workspace

import (
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// handleAgentSpawnSwarm spawns a persistent, card-free swarm sub-agent under
// the calling agent. Unlike workflow workers (agent_spawn_assign, bound to a
// task card) and fork children (single-turn NewChildActor), a swarm child is a
// full agent (NewActor): it self-assigns a spawn-time goal during OnStart,
// runs its own turn loop, and stays alive until the parent terminates it via
// workspace.agent_terminate. The swarm bundle is re-attached through
// ExtraBundleIDs so the child can spawn its own swarm children, bounded by
// MaxSwarmDepth / MaxSwarmChildrenPerAgent.
//
// PureContext (stateless), mirroring handleAgentSpawnByType: cross-actor hops
// are ref.Invokes (resolve_child_slot, project.spawn_agent); registry mutation
// runs under agentsMu; child names come from the atomic childNameSeq.
func (a *Actor) handleAgentSpawnSwarm(ctx actor.PureContext, req domain.WorkspaceAgentSpawnSwarmReq) (domain.WorkspaceAgentSpawnSwarmResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_spawn_swarm", req, func() (domain.WorkspaceAgentSpawnSwarmResp, error) {
		const prefix = "workspace.agent_spawn_swarm"

		if strings.TrimSpace(req.Description) == "" {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: Description is required", prefix)
		}
		kind := req.AgentKind
		if kind == "" {
			kind = "general"
		}
		if err := domain.ValidateAgentKind(kind); err != nil {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: %w", prefix, err)
		}
		// Read-only kinds never receive extra bundles (builtinCardsToSeed),
		// so a read-only swarm child would silently lose the recursion
		// capability this callable promises. Reject up front instead.
		if agentkit.IsReadOnlyAgentKind(kind) {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: read-only agent kind %q cannot run autonomous swarm work; use a fork_explore child instead", prefix, kind)
		}
		// The worker kind is reserved for workflow card-bound workers:
		// workspace.agent_terminate routes workers through the workflow
		// direct-parent checks, which would strand a card-free swarm child.
		if kind == domain.AgentKindWorker {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: agent kind %q is reserved for workflow workers; swarm children default to general", prefix, kind)
		}
		// CallerAgentId is injected by the turn engine (injectCallerAgentID)
		// and is the authoritative parent identity. A swarm spawn without a
		// live parent agent has no inbox to report back to.
		if req.CallerAgentID == "" {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: CallerAgentId is required (agent callers get it injected automatically)", prefix)
		}
		parent, ok := a.findAgentRef(req.CallerAgentID)
		if !ok {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: caller agent %q is not registered in this workspace", prefix, req.CallerAgentID)
		}
		if _, err := requireLiveAgent(ctx, prefix, req.CallerAgentID); err != nil {
			return domain.WorkspaceAgentSpawnSwarmResp{}, err
		}

		depth := a.swarmDepthOf(req.CallerAgentID)
		if depth+1 > domain.MaxSwarmDepth {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: swarm recursion cap reached: caller %q is at depth %d (max %d). Give the work to an existing child or do it yourself", prefix, req.CallerAgentID, depth, domain.MaxSwarmDepth)
		}
		if live := a.liveSwarmChildrenOf(req.CallerAgentID); live >= domain.MaxSwarmChildrenPerAgent {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: caller %q already has %d live swarm children (max %d); terminate finished children with workspace.agent_terminate before spawning more", prefix, req.CallerAgentID, live, domain.MaxSwarmChildrenPerAgent)
		}

		projectID := req.ProjectID
		if projectID == "" {
			projectID = parent.ProjectID
		}
		if projectID == "" {
			return domain.WorkspaceAgentSpawnSwarmResp{}, fmt.Errorf("%s: caller agent %q has no project; pass ProjectId explicitly", prefix, req.CallerAgentID)
		}

		slotResp, err := invokeResolveChildSlot(ctx, prefix, req.CallerAgentID, kind, req.Unit)
		if err != nil {
			return domain.WorkspaceAgentSpawnSwarmResp{}, err
		}

		condition := req.Prompt
		if strings.TrimSpace(condition) == "" {
			condition = req.Description
		}
		spawnGoal := &domain.AgentInternalAssignGoalReq{
			Condition:       condition,
			InterpretedGoal: req.Description,
			MaxTurns:        req.MaxTurns,
			PromptPrelude: fmt.Sprintf(
				"You are a swarm sub-agent spawned by agent %q (%s) to work autonomously. "+
					"When your goal is complete, send a concise final report to your parent with workspace.agent_send_message (ToAgentId: %q), then stop and wait — the parent reviews your work and terminates you. "+
					"You may spawn your own swarm sub-agents with workspace.agent_spawn_swarm when a subtask benefits from parallel work; you are at swarm depth %d of %d.",
				parent.DisplayName, req.CallerAgentID, req.CallerAgentID, depth+1, domain.MaxSwarmDepth),
		}

		// The child re-mounts the swarm bundle so it can recurse; plugin-dev
		// inheritance mirrors the spawn_assign worker path (dev callables are
		// read-only-friendly for children).
		extras := ownerInheritedPluginDevBundle(invokeOwnerComponentMounts(ctx, prefix, req.CallerAgentID))
		extras = append(extras, agentkit.SwarmBundleID)

		spawnName := fmt.Sprintf("swarm-%s-%d", kind, a.childNameSeq.Add(1))
		displayName := swarmDisplayName(req.Description)
		// PermissionMode stays workspace-controlled: honoring a caller-
		// supplied value would let the spawning LLM escalate the child past
		// the user's confirmation gate (the field is deprecated on every
		// sibling spawn callable for exactly this reason).
		permissionMode := a.globalPermissionMode()

		ag, err := a.spawnAgentViaProject(ctx, projectID, spawnName, kind, displayName,
			spawnSlotPtr(slotResp.Slot), spawnSlotPtr(slotResp.Fast), spawnSlotPtr(slotResp.Execution),
			spawnSlotPtr(slotResp.Review), spawnSlotPtr(slotResp.Summary),
			"", "", "", extras, spawnGoal, req.CallerAgentID, permissionMode)
		if err != nil {
			return domain.WorkspaceAgentSpawnSwarmResp{}, err
		}
		ag.LifecycleScope = domain.LifecycleScopeSwarm

		a.agentsMu.Lock()
		a.Agents = append(a.Agents, ag)
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)

		return domain.WorkspaceAgentSpawnSwarmResp{
			ChildActorID: ag.ActorID,
			DisplayName:  displayName,
			Depth:        int32(depth + 1),
		}, nil
	})
}

// swarmDepthOf returns the swarm recursion depth of an agent: the number of
// ParentAgentID hops from the agent up to the registry root (a top-level agent
// is depth 0). The walk is bounded and cycle-safe.
func (a *Actor) swarmDepthOf(agentID string) int {
	byKey := make(map[string]domain.AgentRef, len(a.Agents))
	for _, ag := range a.agentSnapshot() {
		byKey[ag.ActorID] = ag
		byKey[ag.ID] = ag
	}
	depth := 0
	visited := map[string]bool{agentID: true}
	cur, ok := byKey[agentID]
	for ok && cur.ParentAgentID != "" && depth < domain.MaxSwarmDepth+8 {
		if visited[cur.ParentAgentID] {
			break
		}
		visited[cur.ParentAgentID] = true
		cur, ok = byKey[cur.ParentAgentID]
		depth++
	}
	return depth
}

// liveSwarmChildrenOf counts the caller's registered swarm children that are
// not being torn down. Terminated children leave the registry via cascade
// delete, so this is the authoritative "still occupies a slot" count.
func (a *Actor) liveSwarmChildrenOf(agentID string) int {
	n := 0
	for _, ag := range a.agentSnapshot() {
		if ag.LifecycleScope == domain.LifecycleScopeSwarm &&
			(ag.ParentAgentID == agentID) &&
			ag.DeletionStatus != "deleting" && ag.DeletionStatus != "deleted" {
			n++
		}
	}
	return n
}

// swarmDisplayName derives a compact, recognizable display name from the task
// description (bounded to 40 runes).
func swarmDisplayName(description string) string {
	d := strings.TrimSpace(description)
	r := []rune(d)
	if len(r) > 40 {
		r = r[:40]
	}
	return "Swarm: " + string(r)
}
