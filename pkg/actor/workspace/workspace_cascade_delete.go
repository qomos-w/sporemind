package workspace

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// deletionConcurrency caps the number of concurrent teardown workers during
// cascade deletion. It prevents a wide subtree from exhausting goroutines
// or overwhelming the actor system with simultaneous Destroy calls.
const deletionConcurrency = 4

// deletionMaxAttempts caps the number of retries for a failed teardown node.
// After this, the node stays as a tombstone with its last error and must be
// resolved by a manual intervention or a restart that resets the attempt
// counter.
const deletionMaxAttempts = 5

// deletionSweepInterval is how often the deletion sweep runs. The sweep is a
// safety net: if ctx.After fails to schedule finalize or retry, the sweep
// will discover stuck tombstones and re-kick their teardown. It is NOT the
// primary retry mechanism — the primary mechanism is the bounded-backoff
// ctx.After retry scheduled in handleDeletionFinalize.
const deletionSweepInterval = 30 * time.Second

// deletionSweepRearmDelay is used when the sweep fails to re-schedule itself.
// A short retry is attempted so the sweep can recover within the current
// process without waiting for OnStart.
const deletionSweepRearmDelay = 5 * time.Second

// deletionInFlightStaleTimeout is the maximum time an in-flight teardown may
// run before the sweep considers it stale and clears the in-flight flag.
// This bounds the worst-case recovery time when ctx.After fails to schedule
// finalize (the goroutine completes but the in-flight flag is never cleared).
//
// Maximum teardown duration for a reasonable subtree (< 50 agents):
//
//	cancel: ceil(50/4) waves × 3s ≈ 38s
//	destroy: depth-grouped waves × 5s ≈ 30s (framework terminateTimeout)
//	release: ceil(50/4) waves × 2s ≈ 25s
//	total ≈ 90s
//
// 3 minutes provides generous margin. Nodes that have been in-flight longer
// than this are assumed to have completed or died, and the sweep re-kicks
// them.
const deletionInFlightStaleTimeout = 3 * time.Minute

// deletionFinalizeReq carries per-node teardown results back to the workspace
// "deletion" loop. The async goroutine schedules this via ctx.After so that state
// mutations (removing tombstones, recording errors, scheduling retries)
// happen serially.
type deletionFinalizeReq struct {
	Results []deletionNodeResult `json:"results"`
}

// deletionNodeResult is the outcome of teardown for a single node.
// Each result carries its own Generation so the finalize handler can
// verify staleness per-node. Agents in the same batch may have different
// generations if one had a prior failed teardown that was retried while
// another was a first-time claim.
type deletionNodeResult struct {
	AgentID       string `json:"agentId"`
	ActorID       string `json:"actorId"`
	Destroyed     bool   `json:"destroyed"`     // actor gone (was absent or Destroy succeeded)
	WorktreeFreed bool   `json:"worktreeFreed"` // worktree released or not applicable
	DestroyError  string `json:"destroyError,omitempty"`
	ReleaseError  string `json:"releaseError,omitempty"`
	Generation    uint64 `json:"generation"` // per-node generation, for staleness check
}

// deletionRetryReq is the internal payload for the bounded-backoff retry
// self-invoke. It carries the agent IDs whose teardown failed and need another
// attempt.
type deletionRetryReq struct {
	AgentIDs []string `json:"agentIds"`
}

// deletionResponse is the empty named response type for internal deletion
// callables. A named type is required by the actor framework so the schema ID
// is stable (anonymous structs are rejected at Register time).
type deletionResponse struct{}

// deletionSweepReq is the empty named request type for the deletion sweep
// callable. The framework rejects anonymous struct parameters.
type deletionSweepReq struct{}

// ---------------------------------------------------------------------------
// Synchronous phase: compute subtree, mark deleting, persist, emit
// ---------------------------------------------------------------------------

// computeDeletionSubtree returns the AgentRef slice for the entire logical
// subtree rooted at rootAgentID: the root itself plus all transitive
// descendants discovered via ParentAgentId. rootAgentID may be an agent ID
// (stable) or ActorID (runtime).
func (a *Actor) computeDeletionSubtree(rootAgentID string) []domain.AgentRef {
	agents := a.agentSnapshot()
	rootIdx := -1
	for i, ag := range agents {
		if ag.ActorID == rootAgentID || ag.ID == rootAgentID {
			rootIdx = i
			break
		}
	}
	if rootIdx < 0 {
		return nil
	}

	// Build child index: parent ActorID -> agent indices.
	childrenOf := make(map[string][]int)
	for i, ag := range agents {
		if ag.ParentAgentID != "" {
			childrenOf[ag.ParentAgentID] = append(childrenOf[ag.ParentAgentID], i)
		}
	}

	visited := make(map[int]bool)
	queue := []int{rootIdx}
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		if visited[idx] {
			continue
		}
		visited[idx] = true
		actorID := agents[idx].ActorID
		if actorID != "" {
			for _, childIdx := range childrenOf[actorID] {
				if !visited[childIdx] {
					queue = append(queue, childIdx)
				}
			}
		}
	}

	result := make([]domain.AgentRef, 0, len(visited))
	for idx := range visited {
		result = append(result, agents[idx])
	}
	return result
}

// cascadeDelete is the unified synchronous entry point for all deletion paths
// (agent terminate, review approve, spawn rollback, project unmount).
//
// It marks every agent in the subtree with DeletionStatus="deleting" (the
// persistent deletion intent), removes them from the interactive projection,
// persists, emits, and then kicks off bounded-concurrency async teardown.
//
// The caller returns immediately — no ctx.Destroy, network, or LLM wait
// happens inside this function.
func (a *Actor) cascadeDelete(ctx actor.PureContext, subtree []domain.AgentRef) {
	if len(subtree) == 0 {
		return
	}

	// Build a quick-lookup set of agent IDs / ActorIDs to mark.
	markSet := make(map[string]bool, len(subtree)*2)
	for _, ag := range subtree {
		markSet[ag.ID] = true
		if ag.ActorID != "" {
			markSet[ag.ActorID] = true
		}
	}

	// Phase 1: mark deleting (persistent intent) and clean runtime state.
	markedIDs := make([]string, 0, len(subtree))
	a.agentsMu.Lock()
	for i := range a.Agents {
		ag := &a.Agents[i]
		if !markSet[ag.ID] && !markSet[ag.ActorID] {
			continue
		}
		if ag.DeletionStatus == "deleting" {
			continue // already pending deletion — idempotent
		}
		ag.DeletionStatus = "deleting"
		if a.agentRuntime != nil && ag.ActorID != "" {
			delete(a.agentRuntime, ag.ActorID)
		}
		markedIDs = append(markedIDs, ag.ID)
	}
	a.agentsMu.Unlock()

	if len(markedIDs) > 0 {
		// Persist the mark before kicking off teardown. If the authoritative
		// card write fails, revert the in-memory marks so a.Agents matches
		// the card (which still carries these agents as non-deleting) and
		// abort the cascade: proceeding would tear down actors whose deletion
		// intent was never persisted, orphaning them on restart. The caller
		// may retry; a restart reloads the clean card.
		if err := a.saveAgentRegistry(); err != nil {
			ctx.Logger().Error("workspace: deletion mark registry save failed; reverting in-memory marks and aborting cascade",
				"marked", len(markedIDs), "error", err)
			a.agentsMu.Lock()
			for _, id := range markedIDs {
				for j := range a.Agents {
					if a.Agents[j].ID == id && a.Agents[j].DeletionStatus == "deleting" {
						a.Agents[j].DeletionStatus = ""
					}
				}
			}
			a.agentsMu.Unlock()
			a.emitAgentsChanged(ctx)
			return
		}
		a.emitAgentsChanged(ctx)
	}

	// Collect the snapshot of agents that are now marked deleting and belong
	// to this subtree. We re-scan because cascadeDelete may be called when
	// some agents were already marked by a prior cascade.
	var toTeardown []domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.DeletionStatus == "deleting" && markSet[ag.ID] {
			toTeardown = append(toTeardown, ag)
		}
	}

	if len(toTeardown) > 0 {
		a.teardownSubtreeAsync(ctx, toTeardown)
	}
}

// ---------------------------------------------------------------------------
// Async phase: leaf-first bounded-concurrency teardown with per-node results
// ---------------------------------------------------------------------------

// teardownTarget is the data captured for the goroutine. It must not reference
// mutable workspace state directly (a.Agents indices shift); only immutable
// string fields are safe. The generation field is captured at claim time and
// carried through to the finalize result so staleness can be checked per-node.
type teardownTarget struct {
	actorID    string
	agentID    string
	projectID  string
	generation uint64 // per-agent generation at claim time
}

// teardownSubtreeAsync runs the async teardown in a goroutine. It:
//  1. Sends turn_cancel to all live agents (bounded concurrency, best-effort).
//  2. Destroys actors leaf-first (bounded concurrency), collecting per-node results.
//  3. Releases worktree bindings ONLY for destroy-succeeded nodes (bounded
//     concurrency), collecting per-node results.
//  4. Schedules finalization with per-node results so the finalize handler
//     can remove successful tombstones (including persist deletion), record
//     errors for failed nodes, and schedule bounded-backoff retries.
//
// Thread-safety: the goroutine accesses actor.Context methods that are
// documented as safe for concurrent use in gospore:
//   - LookupID: read-only, tree RWMutex-protected (pure_context.go:63)
//   - Destroy: implementation uses tree mutex + cellMu + bounded select
//     with terminateTimeout (app.go:886-910, tree/impl.go:197-225)
//   - After: internally synchronized, non-blocking (pure_context.go:66-68)
//   - Logger: read of immutable cell field
//   - ref.Ref.Invoke: thread-safe RPC mechanism
//
// The goroutine uses ctx.Lifecycle() as the parent context for all timeouts
// so it is cancelled when the workspace actor dies. It does NOT modify
// in-memory actor state (a.Agents, a.deletionErrors, etc.) — all state
// mutations happen in the finalize handler, serialized by the deletion*
// mutex contracts.
func (a *Actor) teardownSubtreeAsync(ctx actor.PureContext, agents []domain.AgentRef) {
	// Claim in-flight for agents not already being torn down. This prevents
	// the sweep or retry from starting a concurrent teardown for the same
	// agent. The whole claim (in-flight check, generation bump, target
	// capture) runs atomically under deletionMu because callers arrive from
	// concurrent goroutines (stateless cascadeDelete, retry and sweep, plus
	// any loop-lane caller). Only map field access happens under the lock —
	// no invokes, no persist I/O.
	a.deletionMu.Lock()
	if a.deletionInFlight == nil {
		a.deletionInFlight = make(map[string]bool)
	}
	if a.deletionGeneration == nil {
		a.deletionGeneration = make(map[string]uint64)
	}
	if a.deletionInFlightSince == nil {
		a.deletionInFlightSince = make(map[string]time.Time)
	}

	var toProcess []domain.AgentRef
	for _, ag := range agents {
		if a.deletionInFlight[ag.ID] {
			continue // already being torn down
		}
		toProcess = append(toProcess, ag)
	}
	if len(toProcess) == 0 {
		a.deletionMu.Unlock()
		return
	}

	// Increment generation and set in-flight for each agent. Each agent
	// gets its own generation value — they may differ if one agent had a
	// prior failed teardown (e.g., gen=1→2) while another is a first-time
	// claim (gen=0→1). The per-agent generation is stored in the target
	// and carried through to each finalize result.
	targets := make([]teardownTarget, 0, len(toProcess))
	for _, ag := range toProcess {
		a.deletionGeneration[ag.ID]++
		a.deletionInFlight[ag.ID] = true
		a.deletionInFlightSince[ag.ID] = time.Now()
		targets = append(targets, teardownTarget{
			actorID:    ag.ActorID,
			agentID:    ag.ID,
			projectID:  ag.ProjectID,
			generation: a.deletionGeneration[ag.ID],
		})
	}
	a.deletionMu.Unlock()

	// Compute depth for leaf-first ordering. Deeper = closer to leaf.
	// Outside deletionMu: computeDepths reads a.Agents via agentSnapshot
	// (agentsMu only), so no lock nesting.
	depthMap := a.computeDepths(toProcess)
	depths := make(map[string]int, len(targets))
	for _, t := range targets {
		depths[t.actorID] = depthMap[t.actorID]
	}

	// Use ctx.Lifecycle() as the parent for the goroutine so it is cancelled
	// when the workspace actor stops.
	lifecycleCtx := ctx.Lifecycle()

	go func(targets []teardownTarget, depths map[string]int) {
		// Phase 1: cancel all active turns (bounded concurrency, best-effort).
		// Cancel failure does NOT block teardown — we proceed to Destroy.
		runBounded(targets, deletionConcurrency, func(t teardownTarget) {
			a.cancelAgentTurn(ctx, lifecycleCtx, t.actorID)
		})

		// Per-node destroy results, keyed by agentID for deterministic merge.
		// Protected by resultMu because runBounded dispatches concurrent
		// goroutines within each wave.
		resultMu := sync.Mutex{}
		destroyResults := make(map[string]bool, len(targets))
		destroyErrors := make(map[string]string, len(targets))

		// Phase 2: destroy actors leaf-first. Group by depth, process from
		// deepest (leaves) to shallowest (root). Within each wave, bounded
		// concurrency.
		maxDepth := 0
		for _, d := range depths {
			if d > maxDepth {
				maxDepth = d
			}
		}
		for d := maxDepth; d >= 0; d-- {
			var wave []teardownTarget
			for _, t := range targets {
				if depths[t.actorID] == d {
					wave = append(wave, t)
				}
			}
			if len(wave) == 0 {
				continue
			}
			runBounded(wave, deletionConcurrency, func(t teardownTarget) {
				ok, err := a.destroyAgentActor(ctx, lifecycleCtx, t.actorID)
				resultMu.Lock()
				destroyResults[t.agentID] = ok
				if err != nil {
					destroyErrors[t.agentID] = err.Error()
				}
				resultMu.Unlock()
			})
		}

		// Phase 3: release worktree bindings ONLY for destroy-succeeded
		// nodes. If Destroy failed and the actor may still be alive, we must
		// NOT remove its isolated worktree environment.
		releaseResults := make(map[string]bool, len(targets))
		releaseErrors := make(map[string]string, len(targets))

		// Build the release-eligible set: only nodes where Destroy succeeded.
		var releaseTargets []teardownTarget
		for _, t := range targets {
			if destroyResults[t.agentID] {
				releaseTargets = append(releaseTargets, t)
			}
		}
		runBounded(releaseTargets, deletionConcurrency, func(t teardownTarget) {
			ok, err := a.releaseWorktree(ctx, lifecycleCtx, t.projectID, t.actorID)
			resultMu.Lock()
			releaseResults[t.agentID] = ok
			if err != nil {
				releaseErrors[t.agentID] = err.Error()
			}
			resultMu.Unlock()
			// Best-effort: clear the destroyed agent's id from any wiki cards it
			// was bound to (workflow map data.ownerAgentId). Failure only leaves
			// a stale card binding pointing at a dead agent; it must not affect
			// teardown results, so errors are logged inside the helper.
			a.unbindAgentCards(ctx, lifecycleCtx, t.projectID, t.actorID)
		})

		// Phase 4: build per-node results. Persist state deletion is deferred
		// to the finalize handler (stateless) to avoid modifying actor-owned
		// resources from a goroutine.
		results := make([]deletionNodeResult, 0, len(targets))
		for _, t := range targets {
			destroyed := destroyResults[t.agentID]
			worktreeFreed := releaseResults[t.agentID]
			r := deletionNodeResult{
				AgentID:       t.agentID,
				ActorID:       t.actorID,
				Destroyed:     destroyed,
				WorktreeFreed: worktreeFreed,
				DestroyError:  destroyErrors[t.agentID],
				ReleaseError:  releaseErrors[t.agentID],
				Generation:    t.generation,
			}
			// For destroy-failed nodes, WorktreeFreed defaults to false
			// (we didn't attempt release). This correctly prevents tombstone
			// removal and records only the DestroyError.
			results = append(results, r)
		}

		// Phase 5: schedule finalization with per-node results and the
		// teardown generation. The finalize handler will remove successful
		// tombstones (including persist deletion), record errors for failed
		// nodes, clear in-flight flags, and schedule bounded-backoff retries.
		if err := ctx.After(0, "workspace.internal_deletion_finalize", deletionFinalizeReq{
			Results: results,
		}); err != nil {
			// ctx.After failure is an extreme edge case (actor system
			// failure). The deletion intent is already persisted
			// (DeletionStatus="deleting"), the in-flight flag is set, and the
			// sweep will discover the stale in-flight flag after
			// deletionInFlightStaleTimeout and re-kick. We log at error
			// level with the affected agent IDs so the situation is
			// observable.
			ids := make([]string, 0, len(results))
			for _, r := range results {
				ids = append(ids, r.AgentID)
			}
			ctx.Logger().Error("workspace: CRITICAL — deletion finalize scheduling failed; "+
				"intent persisted, sweep will recover within stale timeout",
				"agentIds", ids, "error", err)
		}
	}(targets, depths)
}

// computeDepths assigns a depth to each agent ActorID in the given slice.
// Depth 0 = root (no parent within the set). Used for leaf-first destroy
// ordering.
func (a *Actor) computeDepths(agents []domain.AgentRef) map[string]int {
	inSet := make(map[string]bool, len(agents))
	for _, ag := range agents {
		if ag.ActorID != "" {
			inSet[ag.ActorID] = true
		}
	}
	depths := make(map[string]int, len(agents))
	for _, ag := range agents {
		if ag.ActorID == "" {
			continue
		}
		// Walk up ParentAgentId chain until we leave the set.
		depth := 0
		parent := ag.ParentAgentID
		for parent != "" && inSet[parent] {
			depth++
			// Find the parent agent to get its ParentAgentId.
			found := false
			for _, p := range a.agentSnapshot() {
				if p.ActorID == parent {
					parent = p.ParentAgentID
					found = true
					break
				}
			}
			if !found {
				break
			}
		}
		depths[ag.ActorID] = depth
	}
	return depths
}

// cancelAgentTurn sends turn_cancel to a live agent actor (best-effort, with
// a short timeout). Cancel failure does not affect teardown success.
//
// The lifecycleCtx parameter is the workspace actor's Lifecycle() context,
// used as the parent for the cancel timeout so the goroutine is cancelled
// when the workspace dies.
func (a *Actor) cancelAgentTurn(ctx actor.PureContext, lifecycleCtx context.Context, actorID string) {
	if actorID == "" {
		return
	}
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return
	}
	cancelCtx, cancel := context.WithTimeout(lifecycleCtx, 3*time.Second)
	defer cancel()
	if call := agentRef.Invoke(cancelCtx, "turn_cancel", nil); call != nil {
		_ = call.Close()
	}
}

// destroyAgentActor destroys a single agent actor. Returns (destroyed, error):
//   - (true, nil)  — actor was already absent or Destroy succeeded.
//   - (false, err) — Destroy failed; the actor may still be alive.
//
// Boundedness: ctx.Destroy internally uses the framework's terminateTimeout
// (5s default, configurable via app config). The tree mutation is
// mutex-protected (tree/impl.go:201-216), the cell lookup is mutex-protected
// (app.go:891-894), and the termination wait uses a bounded select
// (app.go:902-905). Destroy never blocks indefinitely.
//
// Thread-safety: Destroy is safe to call from goroutines per the gospore
// implementation (see teardownSubtreeAsync doc comment for analysis).
func (a *Actor) destroyAgentActor(ctx actor.PureContext, lifecycleCtx context.Context, actorID string) (bool, error) {
	if actorID == "" {
		return true, nil // no actor to destroy
	}
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		// Invalid ID — treat as already gone (can't destroy what we can't find).
		return true, nil
	}
	agentRef, ok := ctx.LookupID(id.From(cid))
	if !ok || agentRef == nil {
		return true, nil // already gone — idempotent success
	}
	// Check lifecycle cancellation before blocking on Destroy.
	if lifecycleCtx.Err() != nil {
		return false, fmt.Errorf("destroy actor %s: workspace lifecycle cancelled", actorID)
	}
	if err := ctx.Destroy(agentRef); err != nil {
		return false, fmt.Errorf("destroy actor %s: %w", actorID, err)
	}
	return true, nil
}

// releaseWorktree releases the worktree binding for an agent via its project
// actor. Returns (freed, error):
//   - (true, nil)  — worktree released, or no project/worktree binding exists.
//   - (false, err) — project exists but the release call failed.
//
// IMPORTANT: this must ONLY be called after the agent's actor has been
// confirmed destroyed (or was already absent). Releasing the worktree of a
// still-live actor would remove its isolated environment while it runs.
func (a *Actor) releaseWorktree(ctx actor.PureContext, lifecycleCtx context.Context, projectID, actorID string) (bool, error) {
	if projectID == "" || actorID == "" {
		return true, nil // no worktree to release
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return true, nil // can't find project — nothing to release
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return true, nil // project already destroyed — no worktree binding
	}
	releaseCtx, cancel := context.WithTimeout(lifecycleCtx, 2*time.Second)
	defer cancel()
	call := projectRef.Invoke(releaseCtx, "project.worktree_release_binding",
		gen.ProjectWorktreeReleaseBindingReq{AgentActorID: actorID, ForceDelete: true})
	if call == nil {
		return false, fmt.Errorf("worktree release invoke returned nil for agent %s", actorID)
	}
	result, err := call.Final(releaseCtx)
	_ = call.Close()
	if err != nil {
		return false, fmt.Errorf("worktree release for agent %s: %w", actorID, err)
	}
	// Some invoke paths return the error as the result value rather than the
	// error return. Check for that case too.
	if result != nil {
		if errVal, ok := result.(error); ok {
			return false, fmt.Errorf("worktree release for agent %s: %w", actorID, errVal)
		}
	}
	return true, nil
}

// unbindAgentCards asks the agent's project actor to clear the agent's id
// from every wiki card it was bound to (workflow map data.ownerAgentId), so
// deleted agents leave no residual id references on cards. Best-effort:
// failures are logged and never affect teardown results — a stale binding
// only points at a dead agent and can be cleared by re-binding.
//
// IMPORTANT: like releaseWorktree, this must ONLY be called after the agent's
// actor has been confirmed destroyed (or was already absent); unbinding a
// still-live map owner would detach it from its workflow while it runs.
func (a *Actor) unbindAgentCards(ctx actor.PureContext, lifecycleCtx context.Context, projectID, actorID string) {
	if projectID == "" || actorID == "" {
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
	unbindCtx, cancel := context.WithTimeout(lifecycleCtx, 2*time.Second)
	defer cancel()
	call := projectRef.Invoke(unbindCtx, "project.wiki_unbind_agent",
		gen.ProjectWikiUnbindAgentReq{AgentActorID: actorID})
	if call == nil {
		ctx.Logger().Warn("workspace: wiki unbind invoke returned nil", "agent", actorID)
		return
	}
	result, err := call.Final(unbindCtx)
	_ = call.Close()
	if err != nil {
		ctx.Logger().Warn("workspace: wiki unbind failed for agent", "agent", actorID, "error", err)
		return
	}
	if errVal, ok := result.(error); ok {
		ctx.Logger().Warn("workspace: wiki unbind failed for agent", "agent", actorID, "error", errVal)
	}
}

// ---------------------------------------------------------------------------
// Convergence phase: remove successful tombstones, record errors, schedule retries
// ---------------------------------------------------------------------------

// handleDeletionFinalize is the internal self-invoke handler that processes
// per-node teardown results. For each result:
//   - If the generation is stale (doesn't match current): skip — a newer
//     teardown is in progress. Do NOT clear in-flight (current gen is active).
//   - If Destroyed && WorktreeFreed: the node is fully torn down. Delete its
//     persist state, remove its tombstone from a.Agents, clear any recorded
//     error.
//   - Otherwise: record the error and increment the attempt count. If under
//     the max-attempts cap, schedule a bounded-backoff retry. If at/over cap,
//     keep the tombstone with its last error for manual intervention or
//     restart recovery.
//
// In-flight flags are cleared for all non-stale results (both success and
// failure) since the goroutine has completed for this generation.
//
// Runs stateless (PureContext): the generation/in-flight dual-field checks
// and all deletion* map mutations happen in one atomic section under
// deletionMu; tombstone removal filters a.Agents in place under a single
// agentsMu section so it cannot lose a concurrent append; persist deletes,
// retry scheduling (ctx.After), and card saves happen outside any lock so
// no lock is held across I/O.
func (a *Actor) handleDeletionFinalize(ctx actor.PureContext, req deletionFinalizeReq) (deletionResponse, error) {
	if len(req.Results) == 0 {
		return deletionResponse{}, nil
	}

	successSet := make(map[string]bool, len(req.Results))
	successActorIDs := make(map[string]string) // agentID -> actorID for persist deletion
	var failedIDs []string
	failedErrors := make(map[string]string)
	var staleIDs []string
	// retryPlan captures failed nodes that are under the retry cap; the
	// ctx.After calls run after deletionMu is released.
	type retryPlan struct {
		agentID string
		attempt int
		delay   time.Duration
	}
	var retries []retryPlan
	// exhausted captures nodes at/over the cap, logged after unlock.
	var exhausted []retryPlan
	// releaseWarns captures successful nodes whose worktree release failed;
	// logged after unlock (tombstone is still removed — actor is gone).
	var releaseWarns []deletionNodeResult

	a.deletionMu.Lock()
	if a.deletionErrors == nil {
		a.deletionErrors = make(map[string]string)
	}
	if a.deletionAttempts == nil {
		a.deletionAttempts = make(map[string]int)
	}
	if a.deletionInFlight == nil {
		a.deletionInFlight = make(map[string]bool)
	}
	if a.deletionGeneration == nil {
		a.deletionGeneration = make(map[string]uint64)
	}

	for _, r := range req.Results {
		// Per-node staleness check: compare the result's generation against
		// the agent's current generation. If they differ, a newer teardown
		// has started for this agent — this result is stale. Don't process,
		// don't clear in-flight (the newer generation's goroutine is active).
		if currentGen, ok := a.deletionGeneration[r.AgentID]; ok && currentGen != r.Generation {
			staleIDs = append(staleIDs, r.AgentID)
			continue
		}

		// Clear in-flight for non-stale results (goroutine completed).
		delete(a.deletionInFlight, r.AgentID)
		delete(a.deletionInFlightSince, r.AgentID)

		if r.Destroyed {
			// Actor is gone (destroyed by us or already absent). The
			// worktree binding is stale regardless — treat it as success
			// so the tombstone is removed. A failed worktree release
			// only leaves an orphaned git worktree dir, which is harmless.
			successSet[r.AgentID] = true
			if r.ActorID != "" {
				successActorIDs[r.AgentID] = r.ActorID
			}
			delete(a.deletionErrors, r.AgentID)
			delete(a.deletionAttempts, r.AgentID)
			if r.ReleaseError != "" {
				releaseWarns = append(releaseWarns, r)
			}
		} else {
			failedIDs = append(failedIDs, r.AgentID)
			errMsg := r.DestroyError
			if errMsg == "" {
				errMsg = r.ReleaseError
			}
			if errMsg == "" {
				errMsg = "unknown teardown failure"
			}
			failedErrors[r.AgentID] = errMsg
		}
	}

	// Record errors and bump attempt counters for failed nodes (still under
	// the lock so the counter and error map move atomically).
	for _, agentID := range failedIDs {
		a.deletionErrors[agentID] = failedErrors[agentID]
		attempt := a.deletionAttempts[agentID] + 1
		a.deletionAttempts[agentID] = attempt

		if attempt >= deletionMaxAttempts {
			exhausted = append(exhausted, retryPlan{agentID: agentID, attempt: attempt})
			continue
		}
		retries = append(retries, retryPlan{agentID: agentID, attempt: attempt, delay: deletionBackoff(attempt)})
	}
	a.deletionMu.Unlock()

	if len(staleIDs) > 0 {
		ctx.Logger().Debug("workspace: deletion finalize skipped stale results",
			"staleCount", len(staleIDs))
	}
	for _, r := range releaseWarns {
		ctx.Logger().Warn("workspace: deletion worktree release failed (tombstone still removed — actor is gone)",
			"agentId", r.AgentID, "error", r.ReleaseError)
	}
	for _, r := range exhausted {
		ctx.Logger().Error("workspace: deletion teardown exhausted retries",
			"agentId", r.agentID, "attempts", r.attempt,
			"error", failedErrors[r.agentID],
			"action", "tombstone retained until restart or manual intervention")
	}

	// Delete persist state and remove successful tombstones. The filter+write
	// happens in one atomic section under agentsMu: without it, a concurrent
	// spawn appending to a.Agents between the snapshot and the write-back
	// would be silently dropped by the replacement slice.
	for _, actorID := range successActorIDs {
		a.deleteAgentState(actorID)
	}
	var removedTombstones []domain.AgentRef
	if len(successSet) > 0 {
		a.agentsMu.Lock()
		remaining := make([]domain.AgentRef, 0, len(a.Agents))
		for _, ag := range a.Agents {
			if ag.DeletionStatus == "deleting" && successSet[ag.ID] {
				removedTombstones = append(removedTombstones, ag)
				continue
			}
			remaining = append(remaining, ag)
		}
		if len(removedTombstones) > 0 {
			a.Agents = remaining
		}
		a.agentsMu.Unlock()
	}

	// Schedule bounded-backoff retries for failed nodes (outside deletionMu:
	// ctx.After is internally synchronized and non-blocking, but the lock
	// contract forbids holding it across anything but map field access).
	for _, rp := range retries {
		if err := ctx.After(rp.delay, "workspace.internal_deletion_retry", deletionRetryReq{
			AgentIDs: []string{rp.agentID},
		}); err != nil {
			// ctx.After failure: intent is persisted (DeletionStatus="deleting"
			// + deletionErrors). The sweep will discover and retry within
			// deletionSweepInterval. Log at error.
			ctx.Logger().Error("workspace: deletion retry scheduling failed; "+
				"sweep will recover",
				"agentId", rp.agentID, "attempt", rp.attempt, "error", err)
		} else {
			ctx.Logger().Info("workspace: deletion teardown retry scheduled",
				"agentId", rp.agentID, "attempt", rp.attempt, "delay", rp.delay)
		}
	}

	if len(successSet) > 0 || len(failedIDs) > 0 {
		// Persist the post-removal registry. If the authoritative .ragents card
		// write fails, revert the in-memory removal so a.Agents matches the
		// card (which still carries the tombstones). Without this revert the
		// cache desynced from the card on save failure: agent_review then
		// returned "not found" for an agent still in the card, and the
		// worktree was never released. The sweep re-discovers retained
		// tombstones and retries (it reads a.Agents in-memory).
		if err := a.saveAgentRegistry(); err != nil {
			ctx.Logger().Error("workspace: deletion finalize registry save failed; reverting in-memory tombstone removal",
				"error", err)
			if len(removedTombstones) > 0 {
				a.agentsMu.Lock()
				for _, r := range removedTombstones {
					present := false
					for _, ag := range a.Agents {
						if ag.ID == r.ID {
							present = true
							break
						}
					}
					if !present {
						a.Agents = append(a.Agents, r)
					}
				}
				a.agentsMu.Unlock()
			}
		}
		a.saveDeletionRetryOrLog(ctx)
		a.emitAgentsChanged(ctx)
	}

	if len(successSet) > 0 {
		ctx.Logger().Info("workspace: deletion finalized",
			"finalized", len(successSet), "failed", len(failedIDs), "stale", len(staleIDs))
	}
	return deletionResponse{}, nil
}

// handleDeletionRetry is the internal self-invoke handler for bounded-backoff
// retries. It looks up the failed agents by their stable IDs and re-kicks
// teardown for them. Agents that are currently in-flight are skipped to
// prevent concurrent teardown.
//
// Runs on a forked goroutine (PureContext); the in-flight check + skip
// decision runs atomically under deletionMu (snapshot of a.Agents taken
// beforehand so the agentsMu read does not nest inside deletionMu).
func (a *Actor) handleDeletionRetry(ctx actor.PureContext, req deletionRetryReq) (deletionResponse, error) {
	if len(req.AgentIDs) == 0 {
		return deletionResponse{}, nil
	}

	agents := a.agentSnapshot()
	a.deletionMu.Lock()
	if a.deletionInFlight == nil {
		a.deletionInFlight = make(map[string]bool)
	}
	if a.deletionInFlightSince == nil {
		a.deletionInFlightSince = make(map[string]time.Time)
	}

	var toRetry []domain.AgentRef
	for _, agentID := range req.AgentIDs {
		for _, ag := range agents {
			if ag.ID != agentID || ag.DeletionStatus != "deleting" {
				continue
			}
			// Skip if already in-flight (prevent concurrent teardown).
			if a.deletionInFlight[agentID] && !a.isInFlightStale(agentID) {
				continue
			}
			toRetry = append(toRetry, ag)
			break
		}
	}
	a.deletionMu.Unlock()
	if len(toRetry) == 0 {
		return deletionResponse{}, nil
	}
	ctx.Logger().Info("workspace: retrying deletion teardown",
		"count", len(toRetry))
	a.teardownSubtreeAsync(ctx, toRetry)
	return deletionResponse{}, nil
}

// ---------------------------------------------------------------------------
// Deletion sweep: safety net for ctx.After failures
// ---------------------------------------------------------------------------

// isInFlightStale returns true if the agent's in-flight flag has been set
// longer than deletionInFlightStaleTimeout, indicating the teardown goroutine
// has completed or died but the flag was never cleared (e.g., finalize
// scheduling failed).
//
// Caller must hold deletionMu (the read of deletionInFlightSince is only
// coherent alongside the caller's check-and-clear sequence).
func (a *Actor) isInFlightStale(agentID string) bool {
	since, ok := a.deletionInFlightSince[agentID]
	if !ok {
		return true // no timestamp — treat as stale
	}
	return time.Since(since) > deletionInFlightStaleTimeout
}

// startDeletionSweep schedules the first deletion sweep tick if it hasn't been
// scheduled yet. Called from OnStart (owner loop). The sweep self-reschedules
// via handleDeletionSweep (stateless), so this only needs to fire once.
// deletionSweepScheduled is guarded by deletionMu.
func (a *Actor) startDeletionSweep(ctx actor.Context) {
	a.deletionMu.Lock()
	if a.deletionSweepScheduled {
		a.deletionMu.Unlock()
		return
	}
	a.deletionSweepScheduled = true
	a.deletionMu.Unlock()
	if err := ctx.After(deletionSweepInterval, "workspace.internal_deletion_sweep", nil); err != nil {
		ctx.Logger().Error("workspace: failed to schedule deletion sweep", "error", err)
		a.deletionMu.Lock()
		a.deletionSweepScheduled = false
		a.deletionMu.Unlock()
	}
}

// scheduleSweepNext schedules the next sweep tick. If the normal interval
// scheduling fails, it retries with a shorter delay (deletionSweepRearmDelay)
// to recover within the current process. If the retry also fails, the flag is
// cleared so the next OnStart re-starts the sweep. deletionSweepScheduled is
// guarded by deletionMu.
func (a *Actor) scheduleSweepNext(ctx actor.PureContext) {
	if err := ctx.After(deletionSweepInterval, "workspace.internal_deletion_sweep", nil); err != nil {
		ctx.Logger().Warn("workspace: deletion sweep re-schedule failed, trying short re-arm",
			"error", err)
		// Try a short re-arm so we don't have to wait for OnStart.
		if err2 := ctx.After(deletionSweepRearmDelay, "workspace.internal_deletion_sweep", nil); err2 != nil {
			ctx.Logger().Error("workspace: deletion sweep short re-arm also failed; "+
				"flag cleared, will retry on next OnStart",
				"error", err2)
			a.deletionMu.Lock()
			a.deletionSweepScheduled = false
			a.deletionMu.Unlock()
		}
	}
}

// handleDeletionSweep is the periodic safety-net sweep. It runs on a forked
// goroutine (PureContext); its stuck-scan is guarded by deletionMu and the
// teardown re-kick runs through the internally-synchronized async path. It:
//   - Finds agents still marked "deleting" that have not exhausted their retry
//     budget and are not currently in-flight (or whose in-flight flag is
//     stale).
//   - Clears stale in-flight flags so they can be re-kicked.
//   - Re-kicks teardown for discovered stuck agents.
//   - Self-reschedules for the next interval.
//
// This ensures that even if ctx.After fails to schedule finalize or retry
// (an extreme actor-system edge case), the deletion will still make progress
// without requiring a process restart.
//
// The stuck-scan runs atomically under deletionMu (a.Agents snapshot taken
// beforehand so agentsMu does not nest inside deletionMu); the teardown
// re-kick and the self-reschedule run after the lock is released.
func (a *Actor) handleDeletionSweep(ctx actor.PureContext, _ deletionSweepReq) (deletionResponse, error) {
	// Re-schedule the next sweep regardless of what we find below.
	// scheduleSweepNext takes deletionMu internally; the deferred call runs
	// after every explicit Unlock below, so there is no nesting.
	defer a.scheduleSweepNext(ctx)

	agents := a.agentSnapshot()
	a.deletionMu.Lock()
	if a.deletionErrors == nil {
		a.deletionErrors = make(map[string]string)
	}
	if a.deletionAttempts == nil {
		a.deletionAttempts = make(map[string]int)
	}
	if a.deletionInFlight == nil {
		a.deletionInFlight = make(map[string]bool)
	}
	if a.deletionInFlightSince == nil {
		a.deletionInFlightSince = make(map[string]time.Time)
	}

	// Find deleting agents that haven't exhausted retries and aren't
	// actively in-flight.
	var stuck []domain.AgentRef
	var staleCleared []string
	for _, ag := range agents {
		if ag.DeletionStatus != "deleting" {
			continue
		}
		if a.deletionAttempts[ag.ID] >= deletionMaxAttempts {
			continue // exhausted — leave for manual intervention
		}
		// Skip agents currently in-flight unless the flag is stale.
		if a.deletionInFlight[ag.ID] {
			if a.isInFlightStale(ag.ID) {
				// Stale in-flight: the goroutine completed or died but
				// finalize never ran (ctx.After failure). Clear the flag.
				staleCleared = append(staleCleared, ag.ID)
				delete(a.deletionInFlight, ag.ID)
				delete(a.deletionInFlightSince, ag.ID)
				// Fall through to re-kick.
			} else {
				continue // still in progress
			}
		}
		stuck = append(stuck, ag)
	}
	a.deletionMu.Unlock()

	for _, id := range staleCleared {
		ctx.Logger().Warn("workspace: clearing stale in-flight flag", "agentId", id)
	}

	if len(stuck) == 0 {
		return deletionResponse{}, nil
	}

	ctx.Logger().Info("workspace: deletion sweep found stuck agents, re-kicking teardown",
		"count", len(stuck))
	a.teardownSubtreeAsync(ctx, stuck)

	return deletionResponse{}, nil
}

// ---------------------------------------------------------------------------
// Backoff
// ---------------------------------------------------------------------------

// deletionBackoff returns a bounded exponential backoff duration for the given
// attempt (1-based). The backoff is capped at 5 minutes.
func deletionBackoff(attempt int) time.Duration {
	base := 2 * time.Second
	maxBackoff := 5 * time.Minute
	shift := attempt - 1
	if shift > 7 {
		shift = 7 // cap exponent to avoid overflow
	}
	d := base * time.Duration(1<<uint(shift))
	if d > maxBackoff {
		d = maxBackoff
	}
	return d
}

// ---------------------------------------------------------------------------
// Restart recovery
// ---------------------------------------------------------------------------

// resumeDeletionIntents scans a.Agents for agents still marked "deleting"
// (left over from an interrupted cascade) and resumes their async teardown.
// Called from OnStart (owner loop) after persisted state is loaded. Map
// resets run under deletionMu: a teardown kicked from a concurrent
// status_update path could otherwise race the restart reset.
func (a *Actor) resumeDeletionIntents(ctx actor.Context) {
	var toResume []domain.AgentRef
	for _, ag := range a.agentSnapshot() {
		if ag.DeletionStatus != "deleting" {
			continue
		}
		toResume = append(toResume, ag)
	}
	a.deletionMu.Lock()
	// Clear all in-flight flags on restart — no goroutines survive restart.
	a.deletionInFlight = make(map[string]bool)
	a.deletionInFlightSince = make(map[string]time.Time)
	// Reset attempt counters on restart so backoff starts fresh.
	for _, ag := range toResume {
		delete(a.deletionAttempts, ag.ID)
	}
	a.deletionMu.Unlock()
	if len(toResume) == 0 {
		return
	}
	ctx.Logger().Info("workspace: resuming deletion intents", "count", len(toResume))
	a.teardownSubtreeAsync(ctx, toResume)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// runBounded runs fn over items with at most maxConcurrent goroutines.
// All goroutines are guaranteed to have completed before runBounded returns.
func runBounded[T any](items []T, maxConcurrent int, fn func(T)) {
	if len(items) == 0 {
		return
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	if len(items) <= maxConcurrent {
		var wg sync.WaitGroup
		for _, item := range items {
			wg.Add(1)
			go func(it T) {
				defer wg.Done()
				fn(it)
			}(item)
		}
		wg.Wait()
		return
	}
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it T) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(it)
		}(item)
	}
	wg.Wait()
}
