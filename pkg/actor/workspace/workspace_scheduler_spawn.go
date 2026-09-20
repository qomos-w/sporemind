package workspace

import (
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// handleAgentSpawnScheduler implements workspace.agent_spawn_scheduler: it
// registers a fresh scheduler-run (ephemeral) agent in the project's registry
// without spawning the actor. The project execution engine then spawns the
// actor locally (project.spawn_agent — its own loop, avoiding the
// project→workspace→project round trip that deadlocked workspace.workflow_start)
// and notifies workspace.agent_loaded so the registry row picks up the actor
// identity and LoadState="loaded".
//
// LifecycleScope = "scheduler" marks the agent as scheduler-managed, mirroring
// the "workflow" scope spawnAgentViaProject stamps on workflow workers
// (workspace.go:4075). R3 concluded the "scheduler" value is safe — no current
// reader restricts on it, and separating scopes keeps the sidebar projection
// honest for agents that are unloaded after each ephemeral run.
//
// Auth: external callers must be admin; internal actor-to-actor calls
// (the project scheduler loop at timer fire time) arrive with a zero identity
// and are trusted — same carve-out as workspace.agent_unload.
//
// Deadlock: this handler never synchronously invokes a project callable, so it
// is safe to call synchronously from the project owner loop.
//
// PureContext (stateless): no cross-actor invokes at all; the mount pre-scan
// runs under mountMu.RLock, the unique-name allocation and registry append
// share one agentsMu critical section (check+append must be atomic now that
// concurrent handlers run off-loop), persistence goes through
// saveAgentRegistry (own RLock), and events use the atomic-snapshot emit
// path.
func (a *Actor) handleAgentSpawnScheduler(ctx actor.PureContext, req gen.WorkspaceAgentSpawnSchedulerReq) (gen.WorkspaceAgentSpawnSchedulerResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_spawn_scheduler", req, func() (gen.WorkspaceAgentSpawnSchedulerResp, error) {
		if !ctx.Identity().IsZero() {
			if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
				return gen.WorkspaceAgentSpawnSchedulerResp{}, err
			}
		}
		if req.ProjectID == "" {
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: ProjectId is required")
		}
		kind := req.AgentKind
		if kind == "" {
			kind = string(domain.AgentKindCoder)
		}
		if err := domain.ValidateAgentKind(kind); err != nil {
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: %w", err)
		}
		// The target project must be a real mounted project (mirrors the
		// create_agent guard; system meta projects cannot host agent kinds).
		// Pre-scan under mountMu.RLock so a PureContext conversion of this
		// handler cannot race owner-loop mount mutations.
		foundProject := false
		a.mountMu.RLock()
		for _, mounted := range a.Mounts {
			if mounted.ActorID == req.ProjectID {
				foundProject = true
				if mounted.System {
					a.mountMu.RUnlock()
					return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: agent kind %q is not available for the workspace project", kind)
				}
				break
			}
		}
		a.mountMu.RUnlock()
		if !foundProject {
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: project %q not found", req.ProjectID)
		}

		displayName := strings.TrimSpace(req.DisplayName)
		if displayName == "" {
			displayName = generateDisplayName(nil, a.agentSnapshot(), req.ProjectID)
		}
		if displayName == "" {
			displayName = domain.AgentKindDisplayName(kind)
		}
		if err := validateNameSegment("workspace.agent_spawn_scheduler", displayName); err != nil {
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: %w", err)
		}

		// Allocate the unique spawn name and append the registry row in one
		// agentsMu critical section: the stateless conversion lets concurrent
		// handler goroutines race a check-then-act split across
		// uniqueAgentSpawnName + append. The section is in-memory only — no
		// cross-actor invoke happens under the lock.
		spawnName := ""
		a.agentsMu.Lock()
		for attempt := 0; attempt < 16; attempt++ {
			candidate := displayName + "#" + randomHexSuffix()
			conflict := false
			for i := range a.Agents {
				if a.Agents[i].ProjectID == req.ProjectID && a.Agents[i].ID == candidate {
					conflict = true
					break
				}
			}
			if !conflict {
				spawnName = candidate
				break
			}
		}
		if spawnName == "" {
			a.agentsMu.Unlock()
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: could not allocate unique agent name for %q after retries", displayName)
		}

		// Model settings: slots the request carries ride along into the
		// registry row; nil slots fall back to the agent-kind config default,
		// mirroring handleWorkspaceCreateAgent's resolveAgentModelSlots so a
		// scheduler-spawned agent behaves like a user-created one when the
		// card omits a slot.
		primary, fast, execution, review, summary := req.Primary, req.Fast, req.Execution, req.Review, req.Summary
		if cfg, ok := a.findAgentKindConfig(kind); ok {
			if primary == nil {
				primary = cfg.Primary
			}
			if fast == nil {
				fast = cfg.Fast
			}
			if execution == nil {
				execution = cfg.Execution
			}
			if review == nil {
				review = cfg.Review
			}
			if summary == nil {
				summary = cfg.Summary
			}
		}

		ag := domain.AgentRef{
			ID:             spawnName,
			ProjectID:      req.ProjectID,
			DisplayName:    displayName,
			AgentKind:      kind,
			Status:         "idle",
			LoadState:      "unloaded",
			LifecycleScope: "scheduler",
			Primary:        cloneModelSlotPtr(primary),
			Fast:           cloneModelSlotPtr(fast),
			Execution:      cloneModelSlotPtr(execution),
			Review:         cloneModelSlotPtr(review),
			Summary:        cloneModelSlotPtr(summary),
		}
		a.Agents = append(a.Agents, ag)
		a.agentsMu.Unlock()
		if err := a.saveAgentRegistry(); err != nil {
			// Roll back the in-memory append: a spawn that could not be persisted
			// must not leave a phantom registry row behind (memory would diverge
			// from the authoritative card until the next reload). Surfacing the
			// error lets the caller abort the ephemeral run instead of silently
			// succeeding against a row that will vanish on reload.
			a.agentsMu.Lock()
			for i := range a.Agents {
				if a.Agents[i].ID == spawnName && a.Agents[i].ProjectID == req.ProjectID {
					a.Agents = append(a.Agents[:i], a.Agents[i+1:]...)
					break
				}
			}
			a.agentsMu.Unlock()
			return gen.WorkspaceAgentSpawnSchedulerResp{}, fmt.Errorf("workspace.agent_spawn_scheduler: save agent registry: %w", err)
		}
		a.emitAgentsChanged(ctx)
		ctx.Logger().Info("workspace: scheduler ephemeral agent registered", "agent", ag.ID, "project", req.ProjectID, "kind", kind)
		return gen.WorkspaceAgentSpawnSchedulerResp{
			AgentID:     ag.ID,
			ActorID:     ag.ActorID,
			DisplayName: ag.DisplayName,
			AgentKind:   ag.AgentKind,
		}, nil
	})
}
