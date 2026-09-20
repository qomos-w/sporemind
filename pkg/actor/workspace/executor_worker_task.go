package workspace

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// workerTaskExecutor implements the worker_task execKind: given a
// claimed task card (status → doing, body resolved, bindings merged),
// it resolves the worker's model slot, spawns a fresh 1:1 agent bound
// to the card, hands the card body to the worker as its goal
// condition, and registers the agent in workspace.Agents.
//
// This unifies the previous "spawn-assign" path. Every task card now
// declares its execution capability via data.exec.kind, and the
// orchestrator dispatches via the registry — there is no switch-on-
// card-type in the orchestrator. Existing cards that omit data.exec
// continue to take this path via DefaultExecKind.
type workerTaskExecutor struct {
	a *Actor // back-ref to the hosting workspace; needed for spawn/slot helpers
}

// newWorkerTaskExecutor wires the executor back to its hosting
// workspace. The registry owns lifecycle; this is a one-shot ctor.
func newWorkerTaskExecutor(a *Actor) *workerTaskExecutor {
	return &workerTaskExecutor{a: a}
}

// Kind returns "worker_task" — the registered ExecKind.
func (e *workerTaskExecutor) Kind() ExecKind {
	return ExecKindWorkerTask
}

// Preflight runs the worker_task-specific checks BEFORE the
// dispatcher's universal claim so failures leave the card untouched.
// It mirrors the preflight ordering the prior inline handler used:
// AgentKind validation → kind config lookup → display name → spawn
// name uniqueness → workflow authorization → slot resolution. All
// of these are worker_task-specific; future kinds replace this with
// their own preflight (e.g. crawl checks the browser config, sub_map
// validates the template id).
//
// On success the returned PreflightResult carries the resolved slot
// (so Execute does not re-resolve it) and the display name / spawn
// name (for the spawn call). On failure the returned error prevents
// the claim from happening.
func (e *workerTaskExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	a := e.a

	if req.AgentKind == "" {
		return PreflightResult{}, fmt.Errorf("workspace.executor.worker_task: AgentKind is required")
	}
	if err := domain.ValidateAgentKind(req.AgentKind); err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.worker_task: %w", err)
	}
	cfg, ok := a.findAgentKindConfig(req.AgentKind)
	if !ok {
		return PreflightResult{}, fmt.Errorf("workspace.executor.worker_task: unknown agent kind %q", req.AgentKind)
	}

	displayName := generateDisplayName(cfg.RandomName, a.agentSnapshot(), req.ProjectID)
	if displayName == "" {
		displayName = cfg.DisplayName
	}
	if err := validateNameSegment("workspace.executor.worker_task", displayName); err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.worker_task: %w", err)
	}
	spawnName, err := a.uniqueAgentSpawnName(req.ProjectID, displayName)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.worker_task: %w", err)
	}

	if err := requireActiveWorkflow(ctx, "workspace.executor.worker_task", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}

	// Resolve the worker's model slots from the parent (caller)
	// agent's runtime slots — same resolution fork children get
	// (execution → primary → fast, empty slot = [auto]). req.Unit
	// overrides slot resolution. The parent's non-primary slots are
	// carried through so the worker inherits them.
	resolvedSlots, err := invokeResolveChildSlot(ctx, "workspace.executor.worker_task", req.CallerAgentID, req.AgentKind, req.Unit)
	if err != nil {
		return PreflightResult{}, err
	}

	// Resolve the spawn-time bundles the worker inherits from the owner:
	// the plugin-dev bundle is inherited only when the owner actually
	// has it mounted (persisted component mounts), independent of the
	// project app-kind. Execute merges this into ExtraBundleIDs
	// idempotently, so plain-project owners without the bundle still get
	// nothing.
	inheritedBundleIDs := ownerInheritedPluginDevBundle(invokeOwnerComponentMounts(ctx, "workspace.executor.worker_task", req.CallerAgentID))

	return PreflightResult{
		KindConfig:         cfg,
		DisplayName:        displayName,
		SpawnName:          spawnName,
		ResolvedSlot:       resolvedSlots.Slot,
		FastSlot:           resolvedSlots.Fast,
		ExecutionSlot:      resolvedSlots.Execution,
		ReviewSlot:         resolvedSlots.Review,
		SummarySlot:        resolvedSlots.Summary,
		InheritedBundleIDs: inheritedBundleIDs,
		PermissionMode:     a.globalPermissionMode(),
		CompactionPolicy:   cfg.CompactionPolicy,
	}, nil
}

// Execute runs the worker_task-specific spawn + register after the
// dispatcher has successfully claimed the card. PreflightResult from
// the matching Preflight call MUST be supplied via req.Preflight;
// otherwise the spawn is rejected (defense in depth — the dispatcher
// is the only legitimate caller).
func (e *workerTaskExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.worker_task", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.worker_task: Preflight result is required (dispatcher contract)")
		}
		a := e.a
		pf := req.Preflight

		maxTurns := req.MaxTurns
		if maxTurns <= 0 {
			maxTurns = 0
		}
		prelude := ""
		if req.BoundTaskCardID != "" {
			prelude = fmt.Sprintf("You own task card %q until it is ready for independent review. Deliver the complete requested outcome: investigate as needed, make every required change, carry interfaces through their integrations, and run relevant verification. Do not submit ready_for_review until the task card's acceptance criteria are met and your changes are reviewable. Report any concrete blocker rather than reducing scope. When complete, declare ready_for_review for the coordinator to review your work.", req.BoundTaskCardID)
		}
		// Spawn-time goal threaded through ProjectSpawnAgentReq →
		// NewActor → OnStart so the agent self-assigns during
		// OnStart, avoiding the circular deadlock and the handler-
		// not-registered race.
		spawnGoal := &domain.AgentInternalAssignGoalReq{
			Condition:       req.Body,
			InterpretedGoal: req.GoalTitle,
			BoundTaskCardID: req.BoundTaskCardID,
			MaxTurns:        maxTurns,
			PromptPrelude:   prelude,
		}

		// Workflow worktree: if the owner agent (req.CallerAgentID)
		// has an active workflow worktree, create a child worktree
		// derived from the owner's worktree so the spawned worker
		// operates in an isolated git worktree. The branch name is
		// req.WorktreeBranch when the caller provided one (git-safe;
		// callers must pass ASCII slugs because task card titles may
		// contain CJK or special characters), else auto-generated from
		// the worker's display name + short uuid. The child starts
		// from the owner's current branch HEAD (git rev-parse,
		// resolved by handleWorktreeCreate when BaseRef is empty and
		// ParentWorktreeID is set).
		//
		// Coding-card gate: when the owner has no workflow worktree
		// (no-git workflow activation, owner runs on the project root
		// directly) and the bound card declares a coding category
		// (code|execute), spawning the worker here would let it
		// operate on the project main repo — the user's territory,
		// where writing tracked files is forbidden (see CLAUDE.md
		// "禁止 agent 自行在主仓 checkout 切分支"). Reject explicitly so
		// the owner either restarts the workflow with coding=true
		// (workflow_plan_submit.NonCoding=false) so the owner spawns
		// with a worktree, or downgrades the card to a read-only
		// category (research/explore/review). research/explore/review
		// (and cards that omit data.category) keep the legacy
		// "no-worktree silent skip" behavior because they are
		// read-only by design. The dispatcher rolls the card status
		// back to its previous value when Execute errors, so a
		// rejected coding spawn leaves the card re-claimable.
		var childWorktreeID string
		ownerWtID, mapCardID := a.queryOwnerWorkflowWorktree(ctx, req.CallerAgentID)
		switch {
		case ownerWtID != "":
			var wtErr error
			childWorktreeID, wtErr = a.createWorkflowChildWorktree(ctx, req.ProjectID, ownerWtID, mapCardID, req.WorktreeBranch, pf.DisplayName)
			if wtErr != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.worker_task: create child worktree: %w", wtErr)
			}
		case isCodingTaskCardCategory(req.CardRaw):
			return ExecResp{}, fmt.Errorf(
				"workspace.executor.worker_task: coding task card %q requires an owner workflow worktree, but the owner %q has none. "+
					"Coding workers must operate in an isolated worktree so they do not write to the project main repo (user territory). "+
					"Remediation: restart the workflow with coding=true (workflow_plan_submit.NonCoding=false) so the owner spawns with a worktree, "+
					"or downgrade this card's data.category to research/explore/review (read-only, no worktree needed).",
				req.BoundTaskCardID, req.CallerAgentID,
			)
		}

		// Spawn. primarySlot mirrors the prior handler: the resolved
		// slot is passed as Primary. The parent's non-primary slots are
		// copied so the worker inherits the same fast/execution/review/
		// summary configuration; empty ([auto]) slots stay nil so the
		// child's model selection is unambiguous.
		var primarySlot *domain.ModelSlot
		if len(pf.ResolvedSlot.Candidates) > 0 {
			rs := pf.ResolvedSlot
			primarySlot = &rs
		}
		ag, err := a.spawnAgentViaProject(ctx, req.ProjectID, pf.SpawnName, req.AgentKind, pf.DisplayName, primarySlot, spawnSlotPtr(pf.FastSlot), spawnSlotPtr(pf.ExecutionSlot), spawnSlotPtr(pf.ReviewSlot), spawnSlotPtr(pf.SummarySlot), "", childWorktreeID, "", pf.InheritedBundleIDs, spawnGoal, req.CallerAgentID, pf.PermissionMode)
		if err == nil {
			ag.Title = req.BoundTaskCardID
		}
		if err == nil && pf.CompactionPolicy != nil {
			cp := *pf.CompactionPolicy
			ag.CompactionPolicy = &cp
		}
		if err != nil {
			// Spawn failure cleanup: discard the orphan child worktree
			// so it doesn't leak (leak #10 from adversarial analysis).
			if childWorktreeID != "" {
				a.discardOrphanWorktree(ctx, req.ProjectID, childWorktreeID)
			}
			return ExecResp{}, fmt.Errorf("workspace.executor.worker_task: spawn failed: %w", err)
		}

		// Record (workspace-owned lifecycle).
		if req.BoundTaskCardID != "" || childWorktreeID != "" {
			ag.Mode = &gen.AgentModeState{}
			if req.BoundTaskCardID != "" {
				ag.Mode.BoundTaskCardID = req.BoundTaskCardID
			}
			// Stamp the child worktree ID so review approve/reject
			// can find the worker's worktree for merge/rebase.
			if childWorktreeID != "" {
				ag.Mode.ActiveWorkflowWorktreeID = childWorktreeID
			}
		}
		a.agentsMu.Lock()
		a.Agents = append(a.Agents, ag)
		a.agentsMu.Unlock()
		a.saveOrLog(ctx)
		a.emitAgentsChanged(ctx)

		return ExecResp{
			AgentActorID: ag.ActorID,
			DisplayName:  ag.DisplayName,
			Goal: gen.GoalSummary{
				Condition:       req.Body,
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        maxTurns,
			},
		}, nil
	})
}

// isCodingTaskCardCategory reports whether the bound task card declares a
// coding category (code or execute). The check intentionally reads
// data.category directly from the card raw instead of inspecting the
// resolved AgentKind: (a) category is the user-facing authoring
// primitive that drives the gate's intent ("coding workers must not
// run on the main repo"), (b) it is stable against future AgentKind
// routing changes — the upstream resolveSpawnAgentKind mapping is an
// implementation detail — and (c) it produces a clearer error message
// ("downgrade data.category") than any AgentKind-derived phrasing. Cards
// that omit data.category follow the legacy default (worker) but the
// gate does NOT trigger for them — the existing TestWorkerSpawn_NoGit
// OwnerSkipsChildWorktree pins this "no-category = legacy silent skip"
// behavior, and the protection is opt-in by declaring a coding
// category, matching the broader workflow-coding-gate decision that
// the gate fires on explicit coding declarations.
func isCodingTaskCardCategory(cardRaw string) bool {
	switch cardCategoryOf(cardRaw) {
	case taskCategoryCode, taskCategoryExecute:
		return true
	}
	return false
}
