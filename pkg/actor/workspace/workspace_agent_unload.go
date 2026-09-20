package workspace

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// handleAgentUnload implements workspace.agent_unload: it unloads a single
// agent's live actor while KEEPING its AgentRef in the registry — the "gray
// state" the frontend renders for agents that are not currently loaded.
//
// Contrast with cascadeDelete (workspace_cascade_delete.go): unload is NOT a
// deletion. It does NOT set DeletionStatus, does NOT delete persisted agent
// state, does NOT release the worktree binding, and does NOT cascade to the
// agent's subtree. It runs the same sequence the legacy teardown used but
// with the full registry bookkeeping the delete path gained:
//
//  1. turn_cancel on the live actor (best-effort, bounded timeout);
//  2. ctx.Destroy of the actor via destroyAgentActor (bounded; triggers the
//     agent's OnStop → saveMailbox so the session context survives);
//  3. flip the AgentRef to LoadState="unloaded" while PRESERVING ActorID —
//     the durable lazy-load identity that loadAgentByID re-spawns from
//     (workspace.go:3752) — and clearing the ephemeral in-memory runtime
//     cache; status normalization mirrors the restart path in OnStart so the
//     gray state has identical semantics;
//  4. saveAgentRegistry + emitAgentsChanged (which also emits
//     agent_list_state) so the frontend grays the sidebar item.
//
// Auth: external callers must be admin (owner); internal actor-to-actor
// calls (e.g. the project scheduler loop fire-and-forgeting
// workspace.agent_unload after an ephemeral agent finishes, per the R3
// research) arrive with a zero identity and are trusted without a role check.
//
// Deadlock: this handler runs stateless (PureContext) and never synchronously
// invokes a project callable — cancel/destroy only talk to the target agent
// actor via bounded framework calls — so the project → workspace → project
// back-edge that forced the workspace.agent_loaded fire-and-forget pattern
// cannot re-occur here.
func (a *Actor) handleAgentUnload(ctx actor.PureContext, req gen.AgentUnloadReq) (gen.AgentUnloadResp, error) {
	return panicprobe.Guard(ctx, "workspace.agent_unload", req, func() (gen.AgentUnloadResp, error) {
		if !ctx.Identity().IsZero() {
			if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
				return gen.AgentUnloadResp{}, err
			}
		}
		idx := -1
		for i, ag := range a.agentSnapshot() {
			if ag.ID == req.AgentID || (ag.ActorID != "" && ag.ActorID == req.AgentID) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return gen.AgentUnloadResp{}, fmt.Errorf("workspace.agent_unload: agent %q not found", req.AgentID)
		}
		ag := a.agentAt(idx)
		// Step 1: best-effort turn cancellation so a busy turn cannot wedge
		// Destroy. No-ops when the actor is already gone.
		a.cancelAgentTurn(ctx, ctx.Lifecycle(), ag.ActorID)
		// Step 2: destroy the actor (bounded; idempotent when absent). OnStop
		// persists the mailbox so the session survives the unload.
		if _, err := a.destroyAgentActor(ctx, ctx.Lifecycle(), ag.ActorID); err != nil {
			// The actor may still be alive — do not claim the agent is
			// unloaded while a live actor keeps running.
			return gen.AgentUnloadResp{}, fmt.Errorf("workspace.agent_unload: destroy agent %q: %w", ag.ID, err)
		}
		// Step 3: keep the AgentRef, flip to unloaded, clear runtime cache.
		a.unloadAgentRef(idx)
		// Step 4: persist the registry and emit so the frontend grays the
		// sidebar item (identical to the restart-OnStart unloaded semantics).
		if err := a.saveAgentRegistry(); err != nil {
			ctx.Logger().Error("workspace.agent_unload: save registry failed", "agent", ag.ID, "error", err)
		}
		a.emitAgentsChanged(ctx)
		return gen.AgentUnloadResp{Unloaded: true}, nil
	})
}

// unloadAgentRef marks the registry agent at idx as unloaded and clears its
// ephemeral runtime state, under agentsMu.
//
// ActorID is PRESERVED: it is the durable lazy-load identity that
// loadAgentByID re-spawns from (workspace.go:3752); clearing it would orphan
// the conversation history. ID, ProjectID, LifecycleScope and metadata are
// untouched. Status normalization mirrors the restart path in OnStart
// (workspace.go:1081-1105) so an unloaded agent renders with exactly the
// same semantics as one recovered from a process restart.
func (a *Actor) unloadAgentRef(idx int) {
	a.agentsMu.Lock()
	defer a.agentsMu.Unlock()
	if idx < 0 || idx >= len(a.Agents) {
		return
	}
	ref := &a.Agents[idx]
	// Clear only the ephemeral in-memory runtime cache: status pushes from the
	// now-destroyed actor are stale and must not resurface in the projection.
	if ref.ActorID != "" {
		delete(a.agentRuntime, ref.ActorID)
	}
	ref.LoadState = "unloaded"
	ref.Degraded = false
	ref.DegradedReason = ""
	ref.ActiveTurnRef = ""
	switch ref.Status {
	case "running", "waiting":
		// The live actor no longer exists; expose as paused until the agent
		// validates and reports its recovered turn state on next load.
		ref.Status = "paused"
	case "", "idle", "paused":
	default:
		ref.Status = "idle"
	}
}