package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/compaction"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/tokenest"
)

func enabledBundleIDs(snapshot domain.AgentComponentSnapshot) []string {
	ids := make([]string, 0, len(snapshot.Mounts))
	seen := make(map[string]struct{}, len(snapshot.Mounts))
	for _, mount := range snapshot.Mounts {
		if !mount.Enabled || mount.Kind != "bundle" {
			continue
		}
		if _, ok := seen[mount.CardID]; ok {
			continue
		}
		seen[mount.CardID] = struct{}{}
		ids = append(ids, mount.CardID)
	}
	return ids
}

// handleRun 是 agent.run callable 的处理函数，运行在 agent.exec 自定义 loop 上。
// 它构造 turnEngine 并调用 e.run() 驱动整个 LLM turn：
// Dispatch → Audit → Execute → Commit 循环，直到 turn 结束或取消。
// 执行完成后，把引擎的 delta 提交给 handleTurnComplete，写回 Session.Turns。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│       创建 turnEngine        │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    e.run(ctx) 主循环：       │
//	│    for loopState < Completed {│
//	│      Dispatch → Audit →      │
//	│      Execute → Commit        │
//	│      若未结束则继续下一轮    │
//	│    }                         │
//	└───────────┬──────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│         e.getDelta()         │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│      handleTurnComplete      │
//	└──────────────────────────────┘
// recomputeTurnToolSurface resolves the agent's full LLM-facing tool surface
// from the current component snapshot, topology callables, and kind config.
// Shared by the turn-start compile path and the mid-turn component-change
// refresh (turnEngine.applyPendingToolsChange) so both paths see identical
// resolution semantics: component tools first, web search, autonomous tools
// by kind, then dedup by LLM-facing name.
func (a *Actor) recomputeTurnToolSurface(ctx actor.Context, cfg domain.AgentKindConfig, model string) []domain.ToolSpec {
	callables := a.callablesMap()
	tools := a.resolveTools(ctx, cfg, callables)
	tools = nativeToolRegistry.AppendWebSearch(tools, model)
	tools = a.appendAutonomousTools(tools, cfg)
	return a.dedupToolsByName(ctx, tools)
}

func (a *Actor) handleRun(ctx actor.Context, req domain.TurnStartReq) error {
	ctx.Logger().Info("agent: inline turn execution starting", "model", derefModelUnit(req.Input.Unit).Model, "provider", derefModelUnit(req.Input.Unit).Provider)

	// TurnID is fixed before agent.run is queued. A cancelled or superseded
	// queued run must not borrow the mutable ActiveTurnRef of a later turn.
	turnID := req.TurnID
	if turnID == "" || a.getActiveTurnRef() != turnID {
		ctx.Logger().Info("agent: handleRun aborted, turn no longer active", "turn", turnID)
		return nil
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	cardScopeByID := make(map[string]string, len(snapshot.Mounts))
	for _, mount := range snapshot.Mounts {
		cardScopeByID[mount.CardID] = mount.Scope
	}
	callableScopes := make(map[string]string)
	for _, contribution := range snapshot.Tools {
		callableScopes[contribution.CallableID] = cardScopeByID[contribution.CardID]
	}

	e := &turnEngine{
		startReq: req,
		allowCallableScope: func(callableID string) bool {
			if isInterceptedInteractionCallable(callableID) {
				return true
			}
			return callableScopeAllowed(callableScopes, callableID)
		},

		projectID:             parentProjectID(ctx),
		workspaceID:           a.workspaceID,
		agentID:               a.actorID,
		turnID:                turnID,
		logger:                ctx.Logger(),
		resolveTargets:        a.resolveTargets,
		primarySlot:           a.primary,
		fastSlot:              a.fast,
		summarySlot:           a.summary,
		forkKindMap:           agentkit.ForkKindMapFromBundles(enabledBundleIDs(snapshot)),
		onChildTimeout:        a.cancelChild,
		onChildLiveness:       a.childLiveness,
		onChildTrack:          a.trackForkChild,
		onExploreResultLookup: a.lookupExploreResultByAgentID,
		onCostRate:            a.costRateFor,
		onStatsRecord:         a.submitStatsRecord,
		onGoalCondition:       func() string { return a.goalCondition(ctx) },
		assessValidator:       a.validateAssessDecision,
		// 回合终局强制 IO：agent 层解析 IO 卡契约 + 成功后落卡。
		onForcedIOContract: a.resolveForcedIOContract,
		onForcedIOComplete: a.completeForcedIO,
		nextIdx:            req.CompiledContext.NextIdx,
		summarySegments:    req.CompiledContext.SummarySegments,
		stepSeq:            initialStepSeqForTurn(a.steps, turnID),
		done:               make(chan struct{}),
		runExited:          make(chan struct{}),
		childDoneCh:        make(chan childResult, 16),
		resumeCh:           make(chan struct{}, 1),
		childProgressCh:    make(chan struct{}, 1),
		calibration:        a.cfg.TokenCalibration,
		meta:               a.agentMeta(),
		allocSeq:           a.allocSeq,
		openStepEvents:     make(map[string][]domain.StepEvent),
		onStepEvent:        a.applyStepEvent,
		onEventForward:     a.forwardToPlugins,
		onImageRecognized:  a.applyImageRecognition,
		onToolResult:       a.dispatchFirstToolCallHint,
		onAfterFlush:       a.takeSnapshotDebounced,
		onAfterFlushForce:  a.takeSnapshot,
		onCheckpoint:       func(ctx actor.Context) { a.saveMailbox(ctx) },
		onPause: func() {
			if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecyclePaused, turnID, nil, turnLifecycleOptions{
				role:        "assistant",
				turnOrder:   a.getActiveTurnOrder(),
				pauseReason: domain.PauseReasonUser,
			}); lcErr != nil {
				// The turnEngine pause callback has no return channel, so the
				// rejected transition is surfaced at Error level rather than
				// silently dropped. The adapter already preserved the record's
				// pre-pause state (no mutation committed on rejection).
				ctx.Logger().Error("agent: pause turn lifecycle rejected; record retains prior state",
					"turn", turnID, "error", lcErr)
			}
			a.notifyWorkspaceStatus(ctx)
		},
		onResume: func() {
			if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnID, nil, turnLifecycleOptions{
				role:      "assistant",
				turnOrder: a.getActiveTurnOrder(),
			}); lcErr != nil {
				ctx.Logger().Error("agent: resume turn lifecycle rejected; record retains prior state",
					"turn", turnID, "error", lcErr)
			}
			a.notifyWorkspaceStatus(ctx)
		},
		onUnitChanged: func(unit domain.ModelUnit) {
			a.applyUnitChange(ctx, turnID, unit)
		},
		onResolvedUnit: func(unit domain.ModelUnit) {
			// resolved_unit arrives on every dispatch; skip when the aggregator
			// re-selected the same model to avoid redundant events.
			if unit.Model == "" {
				return
			}
			// Record the observed routing: the executed unit is promoted to
			// the head of the primary route chain (soft pin chain), so the
			// next dispatch prefers it while the auto tail still guarantees
			// fallback. No-op when the unit already heads the chain.
			a.recordRouteUnit(ctx, unit)
			if unit.Model == a.status.Unit.Model && unit.Provider == a.status.Unit.Provider {
				return
			}
			a.applyUnitChange(ctx, turnID, unit)
		},
		onRouteUnitFailed: func(unit domain.ModelUnit) {
			a.demoteFailedRouteUnit(ctx, unit)
		},
		onUsageUpdated: func(u *domain.UsageData) {
			if u != nil {
				a.status.Usage = copyUsage(u)
			}
		},
		onContextBudgetUpdated: func(usage *domain.UsageData) {
			a.updateContextBudgetFromUsage(ctx, usage)
		},
		onContextBudgetEstimate: func(estimated int64) {
			a.updateContextBudgetFromEstimate(ctx, turnID, estimated)
		},
		resolveTokenBudget: func() int32 {
			if a.status.ContextBudget != nil && a.status.ContextBudget.TokenBudget > 0 {
				return a.status.ContextBudget.TokenBudget
			}
			return 0
		},
		onCalibrate: func(cc tokenest.CharCounts, estimated, actual int) {
			a.calibrateAndSave(ctx, cc, estimated, actual)
		},
		consumePendingSubmits: func() []domain.ChatMessage {
			return a.consumePendingSubmits(ctx)
		},
		consumePendingUnitChange: func() *domain.ModelUnit {
			return a.pendingChangeUnit.Swap(nil)
		},
		consumePendingToolsChange: func(model string) []domain.ToolSpec {
			if !a.pendingToolsRefresh.Swap(false) {
				return nil
			}
			cfg := a.fetchAgentKindConfig(ctx)
			return a.recomputeTurnToolSurface(ctx, cfg, model)
		},
		onReportDiagnostic: func(innerCtx actor.Context, req domain.OracleReportDiagnosticReq) {
			if req.AgentID == "" {
				req.AgentID = ctx.Self().ID().String()
			}
			a.reportDiagnostic(innerCtx, req)
		},
		saveSnapshot:     a.saveSnapshot,
		acceptingInjects: true,
		onUnfinishedTasks: func() []gen.TurnTask {
			var unfinished []gen.TurnTask
			for _, t := range a.RawSession.Tasks {
				if t.Status != "completed" && t.Status != "cancelled" {
					unfinished = append(unfinished, t)
				}
			}
			return unfinished
		},
	}
	e.initTurnContext(ctx.Lifecycle())
	e.pauseCtx, e.pauseCancel = context.WithCancel(e.turnContext(ctx.Lifecycle()))
	e.onGrantPermission = func(projectID, callableID string) {
		a.grantProjectApproval(projectID, callableID)
	}
	e.onAskUser = func() {
		a.pendingAskUser = true
		a.notifyWorkspaceStatus(ctx)
	}
	e.onPermissionRequested = func() {
		a.pendingApproval = true
		a.notifyWorkspaceStatus(ctx)
	}
	e.onInteractionRequested = a.setPendingInteraction
	e.onPlanSubmit = func(ctx actor.Context, input string) (string, error) {
		var req gen.PlanSubmitReq
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return "", err
		}
		return a.applyPlanSubmit(ctx, req)
	}
	e.onPlanSubmitted = func(ctx actor.Context, turnID, requestID string) {
		a.emitPlanApprovalEvent(ctx, turnID, requestID)
	}
	e.onPlanResolveApproval = a.resolvePlanApproval
	e.onPlanExpire = a.expirePlanApproval

	// goal_submit callbacks
	e.onGoalSubmit = func(ctx actor.Context, input string) (string, error) {
		var req gen.AgentGoalSubmitReq
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return "", err
		}
		return a.applyGoalSubmit(ctx, req)
	}
	e.onGoalSubmitted = func(ctx actor.Context, turnID, requestID string) {
		a.emitGoalSubmitEvent(ctx, turnID, requestID)
	}
	e.onGoalResolveSubmit = a.resolveGoalSubmit
	e.onGoalExpire = a.expireGoalSubmit
	e.onGoalBlockRefresh = a.buildGoalBlock

	// workflow_start callbacks (two-phase confirmation before activating map)
	e.onWorkflowStart = func(ctx actor.Context, input string) (string, error) {
		var req gen.AgentWorkflowStartReq
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return "", err
		}
		return a.applyWorkflowStart(ctx, req)
	}
	e.onWorkflowStartSubmitted = func(ctx actor.Context, turnID, requestID string) {
		a.emitWorkflowStartEvent(ctx, turnID, requestID)
	}
	e.onWorkflowStartResolveSubmit = a.resolveWorkflowStart
	e.onWorkflowStartExpire = a.expireWorkflowStart

	// workflow_plan_submit callback (shares workflow_start confirmation chain)
	e.onWorkflowPlanSubmit = func(ctx actor.Context, input string) (string, error) {
		var req gen.WorkflowPlanSubmitReq
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return "", err
		}
		return a.applyWorkflowPlanSubmit(ctx, req)
	}

	// goal_card_submit callbacks
	e.onGoalCardSubmit = func(ctx actor.Context, input string) (string, error) {
		var req gen.AgentGoalCardSubmitReq
		if err := json.Unmarshal([]byte(input), &req); err != nil {
			return "", err
		}
		return a.applyGoalCardSubmit(ctx, req)
	}
	e.onGoalCardSubmitted = func(ctx actor.Context, turnID, requestID string) {
		a.emitGoalCardSubmitEvent(ctx, turnID, requestID)
	}
	e.onGoalCardResolveSubmit = a.resolveGoalCardSubmit
	e.onGoalCardExpire = a.expireGoalCardSubmit

	e.hasProjectApproval = func(projectID, callableID string) bool {
		return a.hasProjectApproval(projectID, callableID)
	}
	e.livePermissionMode = func() string {
		return a.permissionMode
	}
	e.worktreeRoot = func() string {
		return a.worktreePath
	}
	e.onCompact = func(ctx actor.Context, turnID string, stepID string) error {
		if err := a.compactAsStep(ctx, turnID, "auto", stepID); err != nil {
			return err
		}
		e.summarySegments = append([]domain.SummarySegment(nil), a.RawSession.SummarySegments...)
		e.history = a.compileDispatchMessages()
		for i := range e.history {
			if e.history[i].ID == "" {
				e.history[i].ID = ctx.NewID().String()
			}
			if e.history[i].Idx == 0 {
				e.stamp(&e.history[i])
			}
		}
		for _, msg := range e.history {
			if msg.Idx >= e.nextIdx {
				e.nextIdx = msg.Idx + 1
			}
		}
		return nil
	}
	if a.child.Mode {
		e.suppressEventEmit = true
		// Report child progress to the parent for all forked children, not just
		// read-only explorers, so general-purpose agents (fork_general) also show
		// live progress and the final summary in the UI.
		e.onToolExecuted = a.onExploreToolExecuted
		e.onFlushProgress = func(ctx actor.Context) {
			a.touchChildActivity()
			a.maybeFlushChildProgress(ctx)
		}
		// During tool execution (phaseExecute) a silent, long-running tool such
		// as a build or `sleep` emits no stream chunks, so flushEvents /
		// onFlushProgress never fire and touchChildActivity stalls. This ticker
		// refreshes the atomic activity timestamp in the background so the
		// parent's ChildAgentIdleTimeout keeps seeing a live child.
		e.onToolHeartbeat = func() func() {
			if !a.child.Mode {
				return nil
			}
			ticker := time.NewTicker(domain.ChildHeartbeatInterval / 2)
			done := make(chan struct{})
			panicprobe.SafeGo(ctx, "child_tool_heartbeat", func() {
				for {
					select {
					case <-ticker.C:
						a.touchChildActivity()
					case <-done:
						return
					}
				}
			})
			return func() {
				close(done)
				ticker.Stop()
			}
		}
	}

	if a.child.Mode {
		a.touchChildActivity()
	}
	a.turnEngineStore(e)

	runErr := e.run(ctx)

	// 当引擎经 handleTurnCancel（用户停止）被取消时，运行在 default loop 上的
	// handleTurnCancel 是 cancelled turn 的唯一提交者（它等待 run() 退出后提交）。
	// 这里跳过 handleTurnComplete 及后续 status/pending 清理，确保 Session.Turns /
	// ActiveTurnRef 只有一个写者，消除跨 loop 数据竞争。
	// （生命周期关闭的取消不会置 e.cancelled，handleTurnComplete 仍会为该路径提交。）
	if e.isCancelled() {
		a.turnEngineStore(nil)
		ctx.Logger().Info("agent: inline turn execution finished (cancelled)")
		return runErr
	}

	// Commit the engine's delta into agent session state.
	// getDelta() computes State from loopState (completed/failed/cancelled);
	// do not override it.
	completeReq := e.getDelta()
	if completeErr := a.handleTurnComplete(ctx, completeReq); completeErr != nil {
		ctx.Logger().Error("agent: inline turn completion failed", "error", completeErr)
	}

	a.turnEngineStore(nil)
	// The engine pointer outlives handleTurnComplete by a full tail of
	// persist + status notify on agent.exec. A chat.submit landing in that
	// window queued its user turn as "next cycle" while every pickup scan
	// inside handleTurnComplete had already run; with the pointer now gone,
	// take the turn here or the user turn waits for the next external
	// submit. No-op for a workflow owner parked in waiting (active ref set)
	// and for paths that already scheduled their successor.
	a.maybeAutoStartTurn(ctx)
	a.pendingApproval = false
	a.planApprovalPending = false
	a.pendingAskUser = false
	// Drop any unit change that was staged but not consumed by the engine
	// so it does not leak into the next turn.
	a.pendingChangeUnit.Store(nil)
	a.notifyWorkspaceStatus(ctx)

	ctx.Logger().Info("agent: inline turn execution finished", "error", runErr,
		"steps", len(a.steps)-a.status.StartStepCount,
		"activeTurnRef", a.getActiveTurnRef() != "")
	return runErr
}

// startTurn 负责把用户输入转化为一次 LLM turn 的执行。
// 它完成以下编排：
//  1. 绑定 aggregator；
//  2. 解析 agent kind 配置、指令、工具、权限策略；
//  3. 解析 hot context（git/project/profile）、goal、task board；
//  4. 计算 token budget 和 compaction 策略；
//  5. 组装 CompiledContext 并通过 fire-and-forget 调用 agent.run。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│       绑定 aggregator        │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│      resetTurnSnapshot       │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   解析指令、工具、权限策略   │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     组装 CompiledContext     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    self.Invoke agent.run     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   返回 TurnActorId 占位符    │
//	└──────────────────────────────┘
func (a *Actor) startTurn(ctx actor.Context, req domain.TurnInput) (domain.AgentChatSubmitResp, error) {
	return a.startTurnWithName(ctx, req, "")
}

// emitTurnLifecycle publishes a turn lifecycle event (started/paused/resumed/
// completed/failed/cancelled) on BOTH the "turn" and "step" topics. The step
// replica lets the frontend's step slot populate activeTurnStates before it
// processes the subsequent step events — notably the synthesized slash-skill
// step, which arrives Closed=true. Without the replica the step slot (which
// flushes independently of the turn slot) could project that closed step before
// turn.started arrives, infer allClosed and render a false "completed" turn
// tail. Handlers are idempotent under the resulting double-delivery. Status
// events (context_budget, unit_changed) are intentionally NOT replicated: they
// are high-frequency and do not affect completion.
func (a *Actor) emitTurnLifecycle(ctx actor.Context, ev domain.TurnEvent) error {
	return ctx.EmitEvent("turn", ev)
}

// resolveThinking applies the three-tier reasoning-effort precedence for a
// turn when the caller specified neither a budget nor an effort:
//
//  1. runtime registry override (keyed by model+provider)
//  2. selected unit's own ThinkLevel default
//
// Tier 2 only applies when tier 1 had no registry entry at all — an explicit
// registry choice (including "none") wins over the unit default. Callers should
// only invoke this when input.ReasoningEffort == "" && input.ThinkingBudget == 0.
func (a *Actor) resolveThinking(input domain.TurnInput) (budget int32, effort string) {
	u := input.Unit
	if u == nil {
		return 0, ""
	}
	if lvl, ok := a.loadThinkingRegistry()[thinkingUnitKey{Model: u.Model, Provider: u.Provider}]; ok {
		switch lvl.Mode {
		case "effort":
			return 0, lvl.Effort
		case "budget":
			return lvl.Budget, ""
		}
		return 0, "" // registry "none" or other: explicit choice, no unit fallback.
	}
	// No runtime override: use the unit's own default think level (free-text
	// effort value). "none"/empty mean "no default".
	if u.ThinkLevel != "" && u.ThinkLevel != "none" {
		return 0, u.ThinkLevel
	}
	return 0, ""
}

func (a *Actor) startTurnWithName(ctx actor.Context, req domain.TurnInput, turnName string) (domain.AgentChatSubmitResp, error) {
	if turnName == "" {
		turnName = fmt.Sprintf("turn-%d", a.nextTurnOrder())
	}

	// Detect a resumed turn: the turn already owns a Session.Turns entry created
	// by recoverOrphanTurnSteps at the restart that lost the live engine (state
	// paused/running). Continue it in place — reuse its order and emit a
	// non-terminal turn.resumed so the existing envelope flips back to running
	// instead of a fresh turn.started spawning a new envelope below it.
	resumeTurn := false
	var resumedOrder int64
	var resumedStartedAt string
	for i := range a.Session.Turns {
		t := &a.Session.Turns[i]
		if t.ID != turnName {
			continue
		}
		if t.State == "paused" || t.State == "running" {
			resumeTurn = true
			resumedOrder = t.TurnOrder
			resumedStartedAt = t.StartedAt
		}
		break
	}

	cfg := a.fetchAgentKindConfig(ctx)
	if cfg.Kind == "" && a.agentKind == domain.AgentKindCoder {
		for _, defaultCfg := range agentkit.BaseKindConfigs() {
			if defaultCfg.Kind == domain.AgentKindCoder {
				cfg = defaultCfg
				break
			}
		}
	}
	if changed, err := a.syncSkillMounts(ctx, cfg); err != nil {
		ctx.Logger().Warn("agent: failed to sync configured skills", "error", err)
	} else if changed {
		ctx.Logger().Info("agent: synchronized configured skills")
	}

	// Verify aggregator availability BEFORE setting ActiveTurnRef or emitting
	// turn.started. If the aggregator hasn't started yet (e.g. aimanager still
	// booting), failing here avoids leaving a zombie ActiveTurnRef that would
	// block all subsequent messages — the agent owner loop would treat every
	// new chat.submit as "turn already running" and the conversation would hang.
	a.refreshAggRefs(ctx)
	if a.aggRefCacheEmpty() {
		return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: no aggregator available (aimanager not ready)")
	}

	// Set the active turn ref and emit turn.started BEFORE emitting deferred
	// step events. The synthesized slash-skill step is already created with
	// TurnID = turnName, so after turn.started it just needs to be surfaced
	// via pendingSkillStepID; no migration from user turn id is needed.
	a.setActiveTurnRef(turnName)
	if resumeTurn && resumedOrder != 0 {
		a.setActiveTurnOrder(resumedOrder)
	} else {
		a.setActiveTurnOrder(a.allocTurnOrder())
	}

	if resumeTurn {
		if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnName, nil, turnLifecycleOptions{
			role:      "assistant",
			turnOrder: resumedOrder,
			startedAt: resumedStartedAt,
		}); lcErr != nil {
			// The lifecycle reducer rejected the resume (invariant violation).
			// Block the engine from running and roll back the ActiveTurnRef we
			// just set; otherwise a zombie ref would make every subsequent
			// chat.submit look like "turn already running" and hang the
			// conversation. The caller (handleChatSubmit) does the rest of the
			// failure cleanup (status.State=failed + turn.failed emit).
			a.setActiveTurnRef("")
			return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: resume lifecycle rejected for turn %q: %w", turnName, lcErr)
		}
	} else {
		if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleStarted, turnName, nil, turnLifecycleOptions{
			role:      "assistant",
			turnOrder: a.getActiveTurnOrder(),
			startedAt: time.Now().UTC().Format(time.RFC3339Nano),
			timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		}); lcErr != nil {
			a.setActiveTurnRef("")
			return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: start lifecycle rejected for turn %q: %w", turnName, lcErr)
		}
	}

	// Re-anchor legacy system-role skill-mount steps that were created with
	// TurnID = user turn id over to this assistant turn so the frontend renders
	// them inside the assistant envelope rather than as a standalone envelope
	// between user and assistant. Synthesized slash tool_call rounds now carry
	// TurnID = turnName from creation (see synthesizeSkillMount), so they are
	// emitted separately below and are not re-anchored here.
	//
	// The assistant record may already be appended to Session.Turns (the turn
	// lifecycle adapter creates it before this point), so scan backwards for the
	// most recent user turn rather than assuming it is the last entry.
	var prevTurnID string
	for i := len(a.Session.Turns) - 1; i >= 0; i-- {
		if a.Session.Turns[i].Role == "user" {
			prevTurnID = a.Session.Turns[i].ID
			break
		}
	}
	if prevTurnID != "" {
		for i := range a.steps {
			s := &a.steps[i]
			if s.TurnID != prevTurnID {
				continue
			}
			if s.Role != "system" {
				continue
			}
			s.TurnID = turnName
			a.touchStep(i)
			a.emitStepEventsForTurn(ctx, s.ID, turnName)
		}
	}

	// Emit the slash-skill step that was synthesized in handleChatSubmit. It
	// already has TurnID = turnName, so it just needs to be surfaced after
	// turn.started so it renders inside the assistant envelope.
	if a.pendingSkillStepID != "" {
		a.emitStepEventsForTurn(ctx, a.pendingSkillStepID, turnName)
		a.pendingSkillStepID = ""
	}

	// Emit a hidden turn_start step so the UI can immediately show the assistant turn tail.
	// This step is closed immediately and produces no visible frames; it only serves as
	// a placeholder to bridge the gap between the user message and the first LLM output.
	turnStartStepID := fmt.Sprintf("%s-start-%03d", turnName, len(a.steps)+1)
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:     "step.opened",
		StepID:   turnStartStepID,
		TurnID:   turnName,
		StepType: "turn_start",
		Role:     "assistant",
	})
	a.appendStep(domain.Step{
		ID:        turnStartStepID,
		Role:      "assistant",
		Type:      "turn_start",
		Content:   []domain.ContentBlock{},
		Closed:    true,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		TurnID:    turnName,
		Seq:       a.allocSeq(),
		Meta:      a.agentMeta(),
	})
	_ = a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: turnStartStepID,
		TurnID: turnName,
	})

	unit := derefModelUnit(req.Unit)
	a.resetTurnSnapshot(unit)
	// Drop builtin-seeded bundle mounts the user removed from the kind config
	// since the last turn, so settings saves take effect on the next turn
	// without an agent restart.
	if !a.child.Mode && a.reconcileBuiltinBundleMounts(ctx) {
		a.saveMailbox(ctx)
	}
	a.status.TurnID = turnName
	// A resumed turn keeps its original start time so the workspace status and
	// downstream events report the turn's true lifetime, not the resume moment.
	if resumeTurn && resumedStartedAt != "" {
		a.status.StartedAt = resumedStartedAt
	}
	a.notifyWorkspaceStatus(ctx)

	var tools []domain.ToolSpec
	var inst *domain.CompiledInstructions
	var permissionPolicy *domain.PermissionPolicy

	if a.child.Mode {
		ctx.Logger().Info("agent: resolving child tools", "kind", a.agentKind)
		tools = a.recomputeTurnToolSurface(ctx, cfg, unit.Model)
		ctx.Logger().Info("agent: compiling child context", "tools", len(tools))
		permissionPolicy = &domain.PermissionPolicy{DefaultBehavior: "allow"}
	} else {
		ctx.Logger().Info("agent: resolving tools")
		tools = a.recomputeTurnToolSurface(ctx, cfg, unit.Model)
		ctx.Logger().Info("agent: compiling context", "tools", len(tools))
		defaultBehavior := "confirm"
		switch a.permissionMode {
		case "yolo", "allow-all":
			defaultBehavior = "allow"
		case "auto", "autopilot":
			defaultBehavior = "bypass"
		}
		// Writing to the agent's own memory graph is side-effect-free from a
		// security standpoint: auto-allow memory_save when the memory mode is
		// mounted, otherwise confirm-mode turns pause for approval on every
		// save and the LLM learns to avoid proactively remembering.
		autoAllow := cfg.AutoAllowTools
		if a.memoryEnabled() {
			autoAllow = appendUnique(autoAllow, "memory_save")
		}
		permissionPolicy = &domain.PermissionPolicy{
			DefaultBehavior: defaultBehavior,
			AutoAllowTools:  autoAllow,
		}
	}

	// Tool-name dedup happens inside recomputeTurnToolSurface; see its
	// definition for why duplicate LLM-facing names must collapse.

	hotContext := a.resolveFullHotContext(ctx)
	policy := a.resolveCompactionPolicy(cfg)
	var summaryUnit *domain.ModelUnit
	if policy != nil {
		summaryUnit = policy.SummaryUnit
	}
	windowSize := a.resolveUnitMaxContextLength(ctx, unit)
	if windowSize <= 0 {
		windowSize = int32(tokenest.ModelContextWindow(unit.Model))
	}
	budget := compaction.ResolveTokenBudget(policy, windowSize)

	// Initialize context budget with zero estimated tokens; actual value
	// will be updated from LLM usage after the first dispatch.
	a.status.ContextBudget = &domain.TurnContextBudgetPayload{
		EstimatedTokens:   0,
		ContextWindowSize: windowSize,
		TokenBudget:       budget,
	}

	// The card compiler is the final context boundary: cards contribute metadata,
	// while the registry-resolved tools and conversation remain the execution truth.
	a.refreshWorktreeStatus(ctx)
	cardSnapshot := a.resolveComponentSnapshot(ctx)
	compiledMessages := a.compileDispatchMessages()
	synth := a.turnSynthCards(ctx, cfg, cardSnapshot)
	turnCardContext := compileTurnCardContextWithStoreAndFilters(a.projectCardStore(ctx), cardSnapshot, tools, compiledMessages, synth, toolFilterReasons(cardSnapshot, tools), a.activeContextCards())
	if turnCardContext.ToolSpec != nil {
		tools = turnCardContext.ToolSpec
	}
	if turnCardContext.Messages != nil {
		compiledMessages = turnCardContext.Messages
	}
	inst = compiledInstructions(turnCardContext)
	a.appendMemoryBase(inst)
	a.appendMemoryExperience(inst)
	a.touchProjectedMemory()

	compiledContext := domain.CompiledContext{
		Instructions: inst,
		HotContext:   hotContext,
		Messages:     compiledMessages,
		Capabilities: &domain.CompiledCapabilities{
			PrimitiveTools: tools,
		},
		PermissionPolicy:  permissionPolicy,
		NextIdx:           a.RawSession.NextIdx,
		CompactionPolicy:  policy,
		SummaryUnit:       summaryUnit,
		ContextWindowSize: windowSize,
		TokenBudget:       budget,
		SummarySegments:   a.RawSession.SummarySegments,
		ProjectRoot:       a.resolveProjectRoot(ctx),
	}
	if a.child.Mode && a.child.MaxIterations > 0 {
		compiledContext.MaxIterations = a.child.MaxIterations
	}
	startReq := domain.TurnStartReq{
		TurnID: turnName,
		Input: domain.TurnInput{
			Text:            req.Text,
			Unit:            req.Unit,
			Title:           req.Title,
			ThinkingBudget:  req.ThinkingBudget,
			ReasoningEffort: req.ReasoningEffort,
			Attachments:     req.Attachments,
			Images:          req.Images,
		},
		CompiledContext: compiledContext,
	}
	// When Unit is empty, resolve the primary slot's first concrete unit
	// so the turn starts on a concrete model when configured. This traverses
	// aggregator/auto candidates too (querying the aggregator for its first
	// available pooled unit), not just unit-kind candidates.
	if startReq.Input.Unit == nil {
		if u := a.slotFirstUnitResolved(ctx, a.primary); u.Model != "" {
			startReq.Input.Unit = &u
		}
	}
	// Fill thinking from registry when caller didn't specify.
	if startReq.Input.ThinkingBudget == 0 && startReq.Input.ReasoningEffort == "" {
		startReq.Input.ThinkingBudget, startReq.Input.ReasoningEffort =
			a.resolveThinking(startReq.Input)
	}

	ctx.Logger().Info("agent: starting inline turn", "name", turnName, "primaryCands", len(a.primary.Candidates),
		"tools", len(tools), "messages", len(startReq.CompiledContext.Messages),
		"window", startReq.CompiledContext.ContextWindowSize)

	// Fire-and-forget invoke of agent.run on the agent.exec custom loop.
	self := ctx.Self()
	if self == nil {
		return domain.AgentChatSubmitResp{}, fmt.Errorf("agent: self ref not available")
	}
	call := self.Invoke(ctx.Lifecycle(), "agent_run", startReq)
	if call != nil {
		_ = call.Close()
	}

	// ActiveTurnRef is set by handleRun on agent.exec; return a placeholder
	// that the frontend can poll against.
	return domain.AgentChatSubmitResp{TurnActorID: turnName}, nil
}

// handleTurnCancel 取消当前活跃 turn。
// 流程：
//   1. 取消所有在跑的子 agent；
//   2. 获取 turnEngine 的 delta 并调用 cancel；
//   3. 生成取消标记 step 并追加到 steps / turn；
//   4. 清理 ActiveTurnRef 和 tasks；
//   5. 子 agent 收到取消后自毁。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│   取消所有 activeChildren    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     turnEngine.cancel()      │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    追加 cancelMarker step    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     写入 cancelled turn      │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│         child.Mode ?         │
//	└──────────────────────────────┘

// resolvePrimaryUnit returns the primary slot's first concrete unit. For
// aggregator/auto-backed slots the aggregator is queried for its first
// available pooled unit. The unit is a projection for status/thinking
// lookups; dispatch re-resolves via resolveTargets at turn time.
func (a *Actor) resolvePrimaryUnit(ctx actor.Context) (unit domain.ModelUnit, found bool) {
	u := a.slotFirstUnitResolved(ctx, a.primary)
	if u.Model == "" {
		return domain.ModelUnit{}, false
	}
	return u, true
}

// resolveUnitMaxContextLength returns the configured MaxContextLength for the
// given unit by querying the bound aggregator. The aggregator's
// AICallableUnitView carries the value sourced from provider config — this
// avoids relying on tokenest's prefix-based lookup, which returns wrong values
// for non-claude/gpt-4 models (kimi, deepseek, glm, qwen, etc.).
// Returns 0 when the aggregator or unit cannot be resolved — callers must
// fall back to tokenest in that case.
func (a *Actor) resolveUnitMaxContextLength(ctx actor.Context, unit domain.ModelUnit) int32 {
	if unit.Model == "" {
		return 0
	}
	aggRef, _, err := a.resolveTarget(ctx, a.primary)
	if err != nil {
		return 0
	}
	planner := ctx.Planner()
	if planner == nil {
		return 0
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(statusCtx, aggRef, "aiaggregator.status", nil).Await()
	if err != nil || result == nil {
		return 0
	}
	var status gen.AIAggregatorStatusResp
	if s, ok := result.(gen.AIAggregatorStatusResp); ok {
		status = s
	} else {
		body, _ := json.Marshal(result)
		_ = json.Unmarshal(body, &status)
	}
	for _, u := range status.Units {
		if u.Model == unit.Model && (unit.Provider == "" || u.ProviderName == unit.Provider) {
			if u.MaxContextLength <= 0 && a.warnCtxWindowOnce(unit, "zero") {
				ctx.Logger().Warn("agent: aggregator unit has MaxContextLength=0; falling back to tokenest",
					"model", unit.Model, "provider", unit.Provider)
			}
			return u.MaxContextLength
		}
	}
	if a.warnCtxWindowOnce(unit, "unresolved") {
		ctx.Logger().Warn("agent: context window unresolved (no matching aggregator unit); falling back to tokenest",
			"model", unit.Model, "provider", unit.Provider, "unitCount", len(status.Units))
	}
	return 0
}

// warnCtxWindowOnce reports whether the caller should log a context-window
// warning for (unit, cause): true exactly once per combination, so a
// permanently drifted unit does not re-warn on every turn and dispatch.
func (a *Actor) warnCtxWindowOnce(unit domain.ModelUnit, cause string) bool {
	key := cause + "|" + unit.Model + "|" + unit.Provider
	_, loaded := a.ctxWindowWarned.LoadOrStore(key, struct{}{})
	return !loaded
}

// currentContextWindow returns the resolved context window size for the
// active turn. Prefers the value cached on a.status.ContextBudget (set by
// startTurn after resolving from aggregator); falls back to tokenest's
// prefix-based lookup when no turn is active or the cache is empty.
func (a *Actor) currentContextWindow() int32 {
	if a.status.ContextBudget != nil && a.status.ContextBudget.ContextWindowSize > 0 {
		return a.status.ContextBudget.ContextWindowSize
	}
	return int32(tokenest.ModelContextWindow(a.status.Unit.Model))
}

func (a *Actor) handleTurnCancel(ctx actor.Context) error {
	defer a.takeSnapshot()
	if a.getActiveTurnRef() == "" {
		ctx.Logger().Info("agent: handleTurnCancel no active turn, returning")
		return nil
	}

	// Cancel all in-flight child agents so they stop burning tokens.
	a.activeChildrenMu.Lock()
	for toolUseID, childRef := range a.activeChildren {
		if childRef != nil {
			if call := childRef.Invoke(ctx.Lifecycle(), "turn_cancel", nil); call != nil {
				_ = call.Close()
			}
		}
		delete(a.activeChildren, toolUseID)
		// The dreamer self-destructs on cancel without sending explore_complete,
		// so finishMemorySleep (the only normal reset path) never fires. Reset
		// here to avoid blocking maybeAutoStartTurn forever.
		if toolUseID == "memory-sleep" {
			a.memorySleeping = false
			a.memorySleepTurn = ""
			a.closeDreamStep(ctx, "Memory dream cancelled.")
		}
	}
	a.activeChildrenMu.Unlock()

	turnRefStr := a.getActiveTurnRef()
	var commitReq domain.AgentTurnCompleteReq

	// Inline path only — turn actor has been retired.
	if eng := a.activeTurnEngine(); eng != nil {
		eng.cancel()
		// Terminal cancellation is committed only after the engine has exited.
		// Every turn-owned I/O derives from the engine context, so cancel() wakes
		// the active LLM/tool call and this wait does not leave agent.exec occupied
		// behind a frontend that already rendered turn.cancelled.
		eng.waitExit()
		commitReq = eng.getDelta()
	}

	// Commit cancel marker. Partial steps from the live turn are already in
	// a.steps; we snapshot them into the cancelled turn so they persist.
	const cancelMarker = "用户停止了生成。"
	var cancelledSteps []domain.Step
	if a.status.StartStepCount < len(a.steps) {
		cancelledSteps = make([]domain.Step, len(a.steps)-a.status.StartStepCount)
		copy(cancelledSteps, a.steps[a.status.StartStepCount:])
	}
	cancelStep := domain.Step{
		ID:        ctx.NewID().String(),
		Role:      "assistant",
		Type:      "text",
		Content:   []domain.ContentBlock{{Type: domain.ContentBlockText, Text: cancelMarker}},
		Closed:    true,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		TurnID:    turnRefStr,
		Seq:       a.allocSeq(),
	}
	cancelledSteps = append(cancelledSteps, cancelStep)
	a.appendStep(cancelStep)

	// Emit step events for the cancel marker so the frontend renders it
	// immediately, without waiting for a session-state sync.
	if err := a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:     "step.opened",
		StepID:   cancelStep.ID,
		TurnID:   turnRefStr,
		StepType: "text",
		Role:     "assistant",
		Block:    &domain.ContentBlock{Type: domain.ContentBlockText, Text: cancelMarker},
	}); err != nil {
		ctx.Logger().Warn("agent: emit cancel step.opened failed", "error", err)
	}
	if err := a.emitActorStepEvent(ctx, domain.StepEvent{
		Kind:   "step.closed",
		StepID: cancelStep.ID,
		TurnID: turnRefStr,
	}); err != nil {
		ctx.Logger().Warn("agent: emit cancel step.closed failed", "error", err)
	}

	if a.getActiveTurnRef() == turnRefStr {
		var turnSeq int64
		if len(cancelledSteps) > 0 {
			turnSeq = cancelledSteps[0].Seq
		} else {
			turnSeq = a.allocSeq()
		}
		// Snapshot the turn's tasks before clearing, so the cancelled record
		// preserves them as history.
		var cancelTasks []gen.TurnTask
		if len(a.RawSession.Tasks) > 0 {
			cancelTasks = make([]gen.TurnTask, len(a.RawSession.Tasks))
			copy(cancelTasks, a.RawSession.Tasks)
		}
		turnOrder := a.getActiveTurnOrder()
		prepare := func(rec *domain.Turn) {
			rec.Role = "assistant"
			if rec.Seq == 0 {
				rec.Seq = turnSeq
			}
			if rec.Timestamp == "" {
				rec.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
			}
			if len(cancelTasks) > 0 {
				rec.Tasks = cancelTasks
			}
		}

		// Clear completed tasks - snapshotted into the record via prepare.
		// Archived into TaskHistory so historical task ids stay resolvable.
		a.sweepCompletedTasks()
		a.setActiveTurnRef("")
		a.status.TurnID = ""
		a.clearPendingInteraction(ctx)

		// applyTurnLifecycle flips the record to cancelled (clearing
		// Error/PauseReason, setting Cancelled + CompletedAt, bumping
		// Revision), takes the snapshot, persists, and emits turn.cancelled.
		// The snapshot is taken before the emit so a frontend reconnecting on
		// the event sees the cancelled turn immediately. On a reducer rejection
		// the adapter preserves the record's pre-cancel state (explicit
		// rollback); we surface it at Error level and keep going so the task
		// cleanup and child self-destruct below still run.
		if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleCancelled, turnRefStr, prepare, turnLifecycleOptions{
			role:      "assistant",
			turnOrder: turnOrder,
			payload: map[string]any{
				// Carry the immutable ordering key on the terminal event too.
				// The frontend can receive cancellation without having received
				// turn.started, especially when stop is followed immediately by
				// the next submit.
				"turnOrder": turnOrder,
			},
		}); lcErr != nil {
			ctx.Logger().Error("agent: cancel turn lifecycle rejected; record retains pre-cancel state",
				"turn", turnRefStr, "error", lcErr)
		}
		a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)

		// Merge compaction results from the turn back into RawSession.
		if len(commitReq.SummarySegments) > 0 {
			a.RawSession.SummarySegments = mergeSummarySegments(a.RawSession.SummarySegments, commitReq.SummarySegments)
		}
		if len(commitReq.CompactionEvents) > 0 {
			a.RawSession.CompactionEvents = append(a.RawSession.CompactionEvents, commitReq.CompactionEvents...)
		}
		a.pendingApproval = false
		a.planApprovalPending = false
		a.pendingAskUser = false
		a.notifyWorkspaceStatus(ctx)
		// Crash before the next saveMailbox trigger would lose the cancelled turn.
		a.saveMailbox(ctx)
	}

	// Child agents self-terminate after cancellation via the unified
	// workspace.agent_terminate path (cascadeDelete removes them from
	// workspace.Agents / UI projections). This replaces the raw ctx.Destroy
	// that only destroyed the actor without cleaning up workspace state.
	if a.child.AgentRef != nil {
		a.terminateSelf(ctx)
	}

	return nil
}

// handleTurnPause requests the active turn engine to pause at the next safe
// point (after the current tool batch completes and all steps are closed).
func (a *Actor) handleTurnPause(ctx actor.Context) error {
	// A turn blocked on a user interaction (ask_user / permission / plan /
	// goal) is already waiting for input. Pausing it would abandon the
	// question, write a "paused" tool result, and lose the interaction — so
	// on resume the LLM would continue without the user's answer. Treat pause
	// as a no-op here (checked before the engine-nil guard so it holds even
	// on the crash-recovery path); the user answers or cancels instead. The
	// engine-side interaction handlers also drop their pauseDone branch for
	// race safety.
	if a.pendingInteraction != nil {
		ctx.Logger().Info("agent: pause ignored, turn waiting for interaction answer",
			"turn", a.getActiveTurnRef(), "type", a.pendingInteraction.Type)
		return nil
	}
	eng := a.activeTurnEngine()
	if eng == nil {
		return fmt.Errorf("agent: no active turn to pause")
	}
	ctx.Logger().Info("agent: pause requested", "turn", a.getActiveTurnRef())
	eng.requestPause()
	return nil
}

// handleTurnResume resumes from a paused state. Scenarios:
//  1. User pause (active engine alive and blocked on resumeCh): wake it up.
//  2. Workflow owner crash recovery: a waiting turn that OnStart recovery
//     converted to paused/recovery is restored to waiting, and all child agents
//     discovered via workspace topology are resumed.
//  3. Crash recovery (no active engine, paused turn in Session.Turns):
//     start a new turn with the paused turn's steps as history.
func (a *Actor) handleTurnResume(ctx actor.Context) error {
	defer a.takeSnapshot()

	// Workflow owner resume: dispatch the child cascade (lazy-load unloaded
	// children via workspace topology, then turn_resume each) for ANY resume
	// of a workflow-active owner, regardless of which scenario below applies.
	// The pause source (user / recovery, from waiting or mid-run) only shapes
	// the owner's own restore path below; children must be materialized and
	// resumed either way — turn_resume is a harmless no-op for completed
	// ("no paused turn") and running (resumeCh send dropped) children, so the
	// cascade is safe to fire unconditionally. Fire-and-forget: it runs on a
	// background goroutine (see resumeChildAgents) and must never block this
	// handler.
	if a.workflowActive() {
		a.resumeChildAgents(ctx)
	}

	// Scenario 1: active turn engine paused by the user.
	if eng := a.activeTurnEngine(); eng != nil {
		ctx.Logger().Info("agent: resuming user-paused turn", "turn", a.getActiveTurnRef())
		eng.resumeUserPause(ctx.Lifecycle())
		return nil
	}

	// Scenario 2: workflow owner paused from a waiting turn — either by
	// workflow_pause_all (PauseKind "user") or by OnStart restart recovery
	// (PauseKind "recovery"). Both preserve the CompletedAt carried over
	// from the waiting state, which is the discriminator against a mid-run
	// user pause (running turns have no CompletedAt). Resume order:
	// lazy-load + resume children via persistent topology FIRST, then
	// restore the owner to waiting, so children are already live when the
	// updater wakes the owner.
	if a.status.State == domain.TurnStatePaused &&
		(a.status.PauseKind == domain.PauseReasonRecovery || a.status.PauseKind == domain.PauseReasonUser) &&
		a.workflowActive() {
		turnID := a.getActiveTurnRef()
		if turnID != "" {
			var completedAt string
			for i := range a.Session.Turns {
				if a.Session.Turns[i].ID == turnID {
					completedAt = a.Session.Turns[i].CompletedAt
					break
				}
			}
			if completedAt != "" {
				ctx.Logger().Info("agent: resuming paused-from-waiting workflow turn",
					"turn", turnID, "pauseKind", a.status.PauseKind)
				a.setActiveTurnRef(turnID)
				a.status.TurnID = turnID
				// Children were already dispatched above (workflow owner
				// resume cascade); the owner's waiting restore happens here.
				if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleWaiting, turnID, nil, turnLifecycleOptions{
					turnOrder: a.getActiveTurnOrder(),
				}); lcErr != nil {
					ctx.Logger().Error("agent: resume waiting turn lifecycle rejected",
						"turn", turnID, "error", lcErr)
					return fmt.Errorf("agent: resume waiting turn lifecycle: %w", lcErr)
				}
				// applyTurnLifecycle does not clear PauseKind for non-paused states;
				// waiting must not retain the pause reason.
				a.status.PauseKind = ""
				a.notifyWorkspaceStatus(ctx)
				return nil
			}
		}
	}

	// A turn blocked on a pending interaction (workflow_start / goal_submit /
	// goal_card_submit / plan_approval / ask_user / ask_permission) must NOT
	// be resumed — the user must answer the interaction instead. Resuming
	// would bypass the confirmation and silently drop the pending workflow
	// or goal, re-running the turn without the user's decision.
	if a.pendingInteraction != nil && !a.pendingInteractionTurnIsTerminal() {
		return fmt.Errorf("agent: turn has a pending %s interaction awaiting your response", a.pendingInteraction.Type)
	}

	// Scenario 2: crash-recovery pause — no active engine. Continue the SAME
	// paused turn (reuse its ID) instead of spawning a new turn below it, so
	// resume picks up from the paused turn's accumulated tail inside the same
	// conversation envelope. The turn's committed steps are already in scope
	// for compileMessages via ActiveHead + a.status.TurnID.
	pausedTurnID := a.getActiveTurnRef()
	if pausedTurnID == "" {
		for i := len(a.Session.Turns) - 1; i >= 0; i-- {
			if a.Session.Turns[i].State == "paused" {
				pausedTurnID = a.Session.Turns[i].ID
				break
			}
		}
	}
	if pausedTurnID == "" {
		return fmt.Errorf("agent: no paused turn to resume")
	}

	ctx.Logger().Info("agent: resuming paused turn in place", "turn", pausedTurnID)
	turnName := pausedTurnID

	// Flip the paused turn entry back to running so the UI re-activates the
	// existing envelope. applyTurnLifecycle applies the canonical resumed
	// transition (state running, clears Error/PauseReason, bumps Revision),
	// persists, and emits turn.resumed. startTurnWithName re-emits this for
	// the same turn ID; the frontend handler is idempotent on state=running.
	// A rejection here is an invariant edge; surface it at Error level (the
	// adapter already preserved the record's prior state) and still schedule
	// the turn so the user is not left permanently stuck.
	a.setActiveTurnRef(turnName)
	a.status.TurnID = turnName
	if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnName, nil, turnLifecycleOptions{
		role:      "assistant",
		turnOrder: a.getActiveTurnOrder(),
	}); lcErr != nil {
		ctx.Logger().Error("agent: resume turn lifecycle rejected on crash-recovery; record retains prior state",
			"turn", turnName, "error", lcErr)
	}

	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)

	if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
		TurnInput: domain.TurnInput{Text: ""},
		TurnName:  turnName,
	}); err != nil {
		a.setActiveTurnRef("")
		a.status.TurnID = ""
		ctx.Logger().Error("agent: failed to schedule resume turn", "turn", turnName, "error", err)
		return fmt.Errorf("agent: schedule resume turn: %w", err)
	}

	a.notifyWorkspaceStatus(ctx)
	return nil
}

// handleTurnComplete 把 turnEngine 运行结束后的结果持久化到 Session。
// 职责：
//   1. 快照 tasks（历史记录保留本 turn 期间的全部任务）；
//   2. 清理已完成的任务，避免泄漏到下一 turn；
//   3. 把本次 turn 产生的新 steps 合并成 assistant turn，追加到 Session.Turns；
//   4. 合并 summarySegments / compactionEvents / trace artifacts；
//   5. 子 agent 完成后向父 agent 发送 explore_complete 并自毁。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│        snapshot tasks        │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│     清理 completed tasks     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│  ActiveTurnRef == TurnId ?   │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│      组装 assistantTurn      │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   合并 segment/compaction    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│         child.Mode ?         │
//	└──────────────────────────────┘

func (a *Actor) handleTurnComplete(ctx actor.Context, req domain.AgentTurnCompleteReq) error {
	defer a.takeSnapshot()

	// 在清理前先快照任务列表，保证历史 turn 能看到本 turn 期间存在的所有任务。
	var taskSnapshot []gen.TurnTask
	if len(a.RawSession.Tasks) > 0 {
		taskSnapshot = make([]gen.TurnTask, len(a.RawSession.Tasks))
		copy(taskSnapshot, a.RawSession.Tasks)
	}

	// Clear completed and cancelled tasks so they don't leak into the next
	// turn, even when this turn is no longer active (race: a new message was
	// submitted before this completion notification arrived). Finished tasks
	// are archived into RawSession.TaskHistory so later update/cancel/delete
	// calls replaying a historical task id still resolve.
	a.sweepFinishedTasks()

	// Turn state management only when this is still the active turn.
	if a.getActiveTurnRef() != req.Turn.ID {
		return nil
	}

	// Persist the new UI Steps from the agent's live step array.
	// Compaction steps are UI-only artifacts reconstructed from
	// RawSession.CompactionEvents on reconnect; including them here would
	// duplicate those frames after rebuildSteps.
	var turnSteps []domain.Step
	startStepIdx := int32(a.status.StartStepCount)
	endStepIdx := startStepIdx
	for i := a.status.StartStepCount; i < len(a.steps); i++ {
		if isCompactionOnlyStep(a.steps[i]) {
			continue
		}
		turnSteps = append(turnSteps, a.steps[i])
		endStepIdx = int32(i)
	}
	if endStepIdx < startStepIdx {
		endStepIdx = startStepIdx
	}

	var assistantTurnSeq int64
	if len(turnSteps) > 0 {
		assistantTurnSeq = turnSteps[0].Seq
	} else {
		assistantTurnSeq = a.allocSeq()
	}
	_ = stepsToTurnEntries // preserved for future use

	// Stamp the non-lifecycle fields (usage, tasks, file changes, ...) onto the
	// turn record via the lifecycle adapter's prepare hook. The lifecycle state
	// (State/Revision/PauseReason/Error/CompletedAt) is established by the
	// canonical reducer inside applyTurnLifecycle, preserving the record's
	// original identity (StartedAt/TurnOrder/Seq) when it already exists.
	terminalKind := domain.TurnLifecycleCompleted
	switch req.Turn.State {
	case domain.TurnStateFailed:
		terminalKind = domain.TurnLifecycleFailed
	case domain.TurnStateCancelled:
		terminalKind = domain.TurnLifecycleCancelled
	default:
		if req.Turn.State != domain.TurnStateCompleted {
			ctx.Logger().Warn("agent: unknown terminal turn state, defaulting to completed", "state", req.Turn.State)
		}
		// A workflow map owner's finished turn parks in "waiting" instead of
		// completing so ActiveTurnRef/status stay pointed at it; chat_submit
		// finalizes waiting→completed when the updater wakes the owner with a
		// new message. Child-mode agents have no ActiveWorkflow and complete
		// normally.
		if a.workflowActive() {
			terminalKind = domain.TurnLifecycleWaiting
		}
	}
	waitingTurn := terminalKind == domain.TurnLifecycleWaiting
	prepare := func(rec *domain.Turn) {
		rec.Role = "assistant"
		if rec.Seq == 0 {
			rec.Seq = assistantTurnSeq
		}
		rec.Usage = mergeUsageData(rec.Usage, req.Turn.Usage)
		rec.RequestStats = append(rec.RequestStats, append([]domain.TurnRequestStat(nil), req.Turn.RequestStats...)...)
		// Mirror every freshly persisted TurnRequestStat into the session-level
		// request ledger ("llm" kind) so the sequence survives independent of
		// the Turn record.
		for _, st := range req.Turn.RequestStats {
			a.appendRequestRecord(domain.SessionRequestRecord{
				ID:          st.ID,
				Kind:        "llm",
				Seq:         a.allocSeq(),
				StartedAt:   st.StartedAt,
				CompletedAt: st.CompletedAt,
				TurnID:      req.Turn.ID,
				Usage:       st.Usage,
			})
		}
		if req.Turn.Timestamp != "" {
			rec.Timestamp = req.Turn.Timestamp
		}
		rec.Assessment = cloneTurnAssessment(req.Turn.Assessment)
		rec.ContextBudget = copyContextBudget(a.status.ContextBudget)
		rec.FileChanges = mergeTurnFileChanges(rec.FileChanges, req.Turn.FileChanges)
		if req.Turn.Unit != nil {
			rec.Unit = req.Turn.Unit
		}
		if len(taskSnapshot) > 0 {
			rec.Tasks = taskSnapshot
		}
	}
	// A waiting turn keeps ActiveTurnRef and status.TurnID pointed at itself:
	// the parked owner must look busy-blocked to maybeAutoStartTurn, and only
	// an explicit chat_submit finalizes the record.
	if !waitingTurn {
		a.setActiveTurnRef("")
		a.status.TurnID = ""
	}
	ctx.Logger().Info("agent: turn completed",
		"turnId", req.Turn.ID,
		"steps", len(turnSteps),
		"usage", req.Turn.Usage != nil)

	// Count goal turns before the terminal snapshot so the frontend sees the
	// updated budget together with turn.completed.
	if req.Turn.State == "completed" {
		a.recordGoalTurnCompletion()
	}

	// applyTurnLifecycle takes the snapshot, persists, and emits the terminal
	// lifecycle event (completed/failed/cancelled). The reducer enforces the
	// canonical field constraints (clearing PauseReason/Error on terminal,
	// setting CompletedAt) and bumps Revision. On a reducer rejection (an
	// invariant violation — the record is not in a state that can go terminal)
	// the adapter has already preserved the record's pre-terminal state (the
	// explicit rollback: no mutation committed). We surface it at Error level
	// rather than silently dropping it, and keep going so the turn's content
	// (steps/compaction/child summary) below is still persisted — returning
	// here would lose the turn's output for a state that already executed.
	if _, lcErr := a.applyTurnLifecycle(ctx, terminalKind, req.Turn.ID, prepare, turnLifecycleOptions{
		role:        "assistant",
		turnOrder:   a.getActiveTurnOrder(),
		startedAt:   req.Turn.StartedAt,
		completedAt: req.Turn.CompletedAt,
		errorMsg:    req.Turn.Error,
	}); lcErr != nil {
		ctx.Logger().Error("agent: terminal turn lifecycle rejected; record retains pre-terminal state",
			"turn", req.Turn.ID, "kind", terminalKind, "error", lcErr)
	}

	a.status.Usage = copyUsage(req.Turn.Usage)
	if !waitingTurn {
		a.status.TurnID = ""
	}
	oldActiveHead := a.Session.ActiveHead
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)

	if len(req.SummarySegments) > 0 {
		a.RawSession.SummarySegments = mergeSummarySegments(a.RawSession.SummarySegments, req.SummarySegments)
	}
	if len(req.CompactionEvents) > 0 {
		a.RawSession.CompactionEvents = append(a.RawSession.CompactionEvents, req.CompactionEvents...)
	}
	if req.NextIdx > 0 {
		a.RawSession.NextIdx = req.NextIdx
	}

	// Trace field removed from Turn; trace artifacts are stored directly in RawSession.
	// TODO(Phase 2): migrate trace to Step events or dedicated artifacts.
	// Harvest output text from steps (Turn.Output removed after flattening).
	stepOutput := a.summarizeChildFromSteps(turnSteps)

	if a.child.Mode && a.child.AgentRef != nil {
		// The standard turn text is already the exploration result. Explore may
		// optionally summarize it, but reviewer must never enter a second loop.
		summary := stepOutput
		summaryErr := error(nil)
		if stepOutput != "" && a.agentKind != domain.AgentKindDreamer {
			summary, summaryErr = a.runExploreSummary(ctx, stepOutput)
		}
		if summaryErr != nil {
			a.reportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
				Severity: "warning",
				Source:   "explore",
				Message:  summaryErr.Error(),
				TurnID:   a.getActiveTurnRef(),
				Unit:     modelUnitPtrToOracle(&a.status.Unit),
			})
		}
		if summaryErr != nil || summary == "" {
			// Fallback: use raw turn output or harvest from steps.
			summary = stepOutput
			if summary == "" {
				summary = a.summarizeChildFromSteps(a.steps)
			}
		}
		result := domain.ForkResult{
			Summary:                  summary,
			SearchCount:              a.child.SearchCnt,
			ReadCount:                a.child.ReadCnt,
			Iterations:               int32(len(turnSteps)),
			InputTokens:              a.child.InputTokens,
			OutputTokens:             a.child.OutputTokens,
			CacheCreationInputTokens: a.child.CacheCreationInputTokens,
			CacheReadInputTokens:     a.child.CacheReadInputTokens,
			FileChanges:              append([]domain.TurnFileChange(nil), req.Turn.FileChanges...),
		}
		// StepsJson and EntriesJson are omitted — they can be very large for
		// long explorations and are not used in the core result injection path.
		// The summary carries all information the parent LLM needs.
		a.deliverExploreComplete(ctx, result)
		return nil
	}

	a.saveMailbox(ctx)

	// Promote residual pending submits that arrived during the finalize window
	// (after the engine stopped accepting injects but before handleTurnComplete
	// ran). They are keyed by the assistant turn ID that just completed. Group
	// them by meta per the merge rule (T4), create one user turn per group, and
	// start a successor assistant turn for the first group. ActiveHead points at
	// the first promoted user turn so that when the successor completes, the
	// orphaned-user-turn loop below (oldActiveHead+1..) picks up the remaining
	// groups one at a time.
	if submits := a.pendingSubmits[req.Turn.ID]; len(submits) > 0 {
		delete(a.pendingSubmits, req.Turn.ID)
		inputs := pendingSubmitsToTurnInputs(submits)
		var firstUserTurn domain.Turn
		firstIdx := int32(-1)
		for _, input := range inputs {
			_, _, ut := a.createUserTurn(ctx, input)
			if firstUserTurn.ID == "" {
				firstUserTurn = ut
				firstIdx = int32(len(a.Session.Turns) - 1)
			}
		}
		a.Session.ActiveHead = firstIdx
		a.saveMailbox(ctx)
		if _, err := a.startTurn(ctx, turnInputFromUserTurn(firstUserTurn)); err != nil {
			ctx.Logger().Error("agent: start successor turn for residual pending submits failed",
				"error", err, "turn", req.Turn.ID)
		}
		a.notifyWorkspaceStatus(ctx)
		ctx.Logger().Info("agent: residual pending submits promoted to successor turn",
			"turn", req.Turn.ID, "groups", len(inputs))
		return nil
	}

	// Memory sleep (dreamer) is no longer triggered on turn completion.
	// It runs after compaction and via /dream command.

	// Detect user turns that were inserted between oldActiveHead (the user
	// turn that started this assistant turn) and the just-appended assistant
	// turn. These are created by chat.submit when the engine was in its
	// finalize phase (AcceptingInjects() == false). Without this check they
	// are orphaned: maybeAutoStartTurn only scans AFTER ActiveHead, but these
	// user turns sit BEFORE the assistant turn in Session.Turns, so they
	// would never be processed and the UI would hang indefinitely.
	for i := int(oldActiveHead) + 1; i < len(a.Session.Turns)-1; i++ {
		if a.Session.Turns[i].Role == "user" {
			a.Session.ActiveHead = int32(i)
			req := turnInputFromUserTurn(a.Session.Turns[i])
			_, _ = a.startTurn(ctx, req)
			a.notifyWorkspaceStatus(ctx)
			return nil
		}
	}

	// When the LLM marks the turn as a completion candidate, the goal is
	// considered complete: clear the goal state, unmount the goal card, and
	// emit a system step so the frontend can render a completion marker.
	if req.Turn.State == "completed" && req.Turn.Assessment != nil &&
		req.Turn.Assessment.Decision == "complete_candidate" &&
		a.RawSession.Goal != nil && a.RawSession.Goal.Confirmed {
		summary := strings.TrimSpace(req.Turn.Assessment.Reason)
		// A bound goal reaching complete_candidate has no parent reviewer
		// (validateAssessDecision routes parented bound goals to
		// ready_for_review). Mark the bound card done here so completion
		// cannot leave the card stuck in doing/pending_review after the
		// binding is cleared below.
		if boundID := a.RawSession.Goal.BoundTaskCardID; boundID != "" {
			if err := a.callWikiSetStatus(ctx, boundID, "done", "", nil); err != nil {
				ctx.Logger().Warn("agent: set bound card done on goal completion failed", "error", err, "cardID", boundID)
			}
		}
		a.clearGoal(ctx)
		a.emitGoalCompletedStep(ctx, req.Turn.ID, summary)
		a.notifyWorkspaceStatus(ctx)
		// A goal that completed inside an isolated worktree auto-merges the
		// worktree back into the main repo (best-effort; on conflict the goal
		// stays complete, the binding is kept, and a system step asks the user
		// to resolve manually). Workflow-owned worktrees are excluded — they
		// are merged by workflow_stop.
		a.autoMergeWorktreeAfterGoalCompletion(ctx, req.Turn.ID)
		// Drain any user message queued during the turn's finalize window;
		// otherwise it is orphaned (stuck pending) because this branch returns
		// before the shared maybeAutoStartTurn at the end of handleTurnComplete.
		a.maybeAutoStartTurn(ctx)
		return nil
	}

	// When the LLM declares ready_for_review, the agent pauses waiting for an
	// external reviewer (map owner or human). The goal is NOT cleared — the
	// agent remains bound to its task. Goal.Status is set so the updater can
	// detect the pending review state via agent.status polling.
	if req.Turn.State == "completed" && req.Turn.Assessment != nil &&
		req.Turn.Assessment.Decision == "ready_for_review" &&
		a.RawSession.Goal != nil && a.RawSession.Goal.Confirmed {

		// ── 干净门禁 ──
		// A ready_for_review declaration must leave the worktree clean: the
		// parent reviewer inspects the committed changeset, so uncommitted
		// changes would be invisible to it (and lost on teardown). Bounce the
		// declaration back into the goal loop instead of pausing for review —
		// the worker commits, then redeclares. No binding or an invoke failure
		// is fail-open: the gate never blocks a review on a missing worktree.
		if wtClean, dirtyFiles := a.checkWorktreeClean(ctx); !wtClean {
			preview := strings.Join(dirtyFiles, ", ")
			msg := fmt.Sprintf("ready_for_review rejected: your worktree has uncommitted changes (%d files: %s). Commit all changes (git add/git commit) before declaring ready_for_review again.", len(dirtyFiles), preview)
			a.enqueueUserMessage(ctx, msg)
			a.maybeAutoStartTurn(ctx)
			a.notifyWorkspaceStatus(ctx)
			return nil
		}

		a.RawSession.Goal.Status = "ready_for_review"
		// Capture the worker's typed JSON outputs onto the goal so the review
		// path can validate them against the bound task card's data.outputs
		// contract before approving. The assessment lives on the completing
		// turn; the goal is the persistent binding that survives until review.
		a.RawSession.Goal.Outputs = req.Turn.Assessment.Outputs
		// Set the bound task card to pending_review so the project and
		// updater can observe the review-pending state in the card store.
		// CAS on doing: turn completion runs on the engine goroutine, so a
		// review disposition can race this write. If the card already left
		// doing (e.g. an approve set done), this write must lose — a blind
		// write here rolls an approved card back to pending_review.
		if boundID := a.RawSession.Goal.BoundTaskCardID; boundID != "" {
			if err := a.callWikiSetStatus(ctx, boundID, "pending_review", "doing", req.Turn.Assessment.Evidence); err != nil {
				ctx.Logger().Warn("agent: set card pending_review failed", "error", err, "cardID", boundID)
			}
		}
		// Trigger async changeset freeze on the project actor so the
		// snapshot is ready before the parent reviewer inspects it. This
		// must happen at the pending_review transition, not lazily on
		// first read — otherwise an approve/teardown that arrives before
		// the first review_changeset call would permanently lose the
		// snapshot.
		a.triggerReviewChangesetFreeze(ctx)
		a.saveMailbox(ctx)
		a.takeSnapshot()
		a.notifyWorkspaceStatus(ctx)
		// Do NOT call maybeAutoStartTurn here. The agent has paused for
		// external review; queued messages must wait until the map owner
		// rejects (resume_from_review) or the agent is torn down.
		return nil
	}

	// Goal turns are autonomous cycles: after a confirmed turn completes,
	// create the next user turn until the explicit turn budget is exhausted.
	if req.Turn.State == "completed" && a.startNextGoalTurn(ctx) {
		a.notifyWorkspaceStatus(ctx)
		return nil
	}

	a.maybeAutoStartTurn(ctx)
	a.refreshPromptCaches(ctx)
	a.notifyWorkspaceStatus(ctx)
	return nil
}

// recordGoalTurnCompletion advances the persisted goal budget for a successful
// top-level assistant turn.
func (a *Actor) recordGoalTurnCompletion() {
	goal := a.RawSession.Goal
	if goal == nil || !goal.Confirmed || goal.TurnCount >= goal.MaxTurns {
		return
	}
	goal.TurnCount++
}

// goalContinuePool holds the phrasing variants for the synthetic user turn
// that auto-continues a confirmed goal. A variant is picked at random so
// consecutive turns never see a byte-identical prompt: an LLM replaying the
// same prompt tends to replay the same actions and death-loop.
var goalContinuePool = []string{
	"We'll continue working toward the active goal.",
	"Let's continue toward the active goal. Re-check what is actually left before acting.",
	"We're going to continue toward the active goal; if the last turn made no progress, we'll take a different approach.",
	"We should continue toward the active goal and not repeat the previous turn's actions verbatim.",
	"We should carry on toward the active goal, working around whatever blocked progress last turn.",
	"We'll carry on toward the active goal and pick the next concrete step instead of restating the plan.",
	"Let's continue toward the active goal — we'll verify the last change actually worked before moving on.",
	"We're going to continue toward the active goal by re-reading the current state of the files instead of relying on memory.",
	"We should continue toward the active goal; if a step keeps failing, let's step back and reconsider the approach.",
	"We should take the smallest next action that makes real progress toward the active goal.",
	"We'll continue working toward the active goal, finishing unfinished work from earlier turns before starting anything new.",
	"Let's continue toward the active goal and describe what actually changed this turn, not what we intend to do.",
}

// goalContinueText builds the continuation prompt for the upcoming goal turn.
// idx selects the phrasing variant (callers pass a random index); the turn
// progress marker is appended so the prompt differs every turn even when the
// same variant is picked twice.
func goalContinueText(idx, turnCount, maxTurns int) string {
	variant := goalContinuePool[idx%len(goalContinuePool)]
	if maxTurns <= 0 {
		return variant
	}
	return fmt.Sprintf("%s (turn %d of %d)", variant, turnCount+1, maxTurns)
}

// enqueueUserMessage injects a synthetic user message into the session so the
// next goal turn starts with it. The message is appended as a user turn (the
// same shape a real chat_submit would create) and persisted; the next
// maybeAutoStartTurn picks it up and starts a turn for it. Meta "goal" marks
// it as goal-loop-driven rather than originating from a human user.
func (a *Actor) enqueueUserMessage(ctx actor.Context, text string) {
	_, _, _ = a.createUserTurn(ctx, domain.TurnInput{Text: text, Meta: "goal"})
	a.saveMailbox(ctx)
}

// startNextGoalTurn advances a confirmed goal by one assistant turn. It returns
// false when no autonomous continuation is allowed or the turn could not start.
func (a *Actor) startNextGoalTurn(ctx actor.Context) bool {
	goal := a.RawSession.Goal
	if goal == nil || !goal.Confirmed || goal.MaxTurns <= 0 || goal.TurnCount >= goal.MaxTurns {
		return false
	}
	// A workflow map owner is event-driven: the project updater wakes it via
	// chat_submit when frontier work appears or a worker needs review. Auto-
	// continuing here would busy-loop the owner, keeping its status
	// permanently "running" so the updater's owner-busy check suppresses every
	// notification and workers stall in ready_for_review indefinitely. Fall
	// through to maybeAutoStartTurn (event-driven) instead.
	if a.workflowActive() {
		return false
	}

	_, _, userTurn := a.createUserTurn(ctx, domain.TurnInput{
		Text: goalContinueText(rand.IntN(len(goalContinuePool)), int(goal.TurnCount), int(goal.MaxTurns)),
		Meta: "goal",
	})
	a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
	a.saveMailbox(ctx)
	if _, err := a.startTurn(ctx, turnInputFromUserTurn(userTurn)); err != nil {
		ctx.Logger().Error("agent: start next goal turn failed", "turn", userTurn.ID, "error", err)
		return false
	}
	ctx.Logger().Info("agent: starting next goal turn", "turnCount", goal.TurnCount, "maxTurns", goal.MaxTurns)
	return true
}

// appendMessagesToTurn pushes incremental messages to the currently running
// turn so the next LLM dispatch sees them. Prefers the inline turnEngine
// when active.

func (a *Actor) appendMessagesToTurn(ctx actor.Context, messages []domain.ChatMessage) {
	if a.getActiveTurnRef() == "" {
		return
	}

	eng := a.activeTurnEngine()
	if eng == nil || !eng.AcceptingInjects() {
		return
	}

	// Strip any externally-assigned Idx before pushing.
	stripped := make([]domain.ChatMessage, len(messages))
	copy(stripped, messages)
	for i := range stripped {
		stripped[i].Idx = 0
	}

	eng.appendMessages(stripped)
}

// handleTurnAnswer routes a user answer (permission or ask_user) to the
// currently running turn. Prefers the inline turnEngine when active;
// when active.
// handleTurnAnswer 把用户答案（ask_user 或 permission 确认）路由给当前运行中的 turnEngine。
// 调用 turnEngine.receiveAnswer，匹配 requestId 后通过 resumeCh 唤醒对应的 wait 阶段。

func (a *Actor) handleTurnAnswer(ctx actor.Context, req domain.TurnAnswerReq) error {
	if a.getActiveTurnRef() == "" {
		// Fall through to the no-engine path: a restart may have cleared
		// ActiveTurnRef while a blocking interaction is still pending.
	} else {
		if eng := a.activeTurnEngine(); eng != nil {
			ctx.Logger().Info("agent: turn answer received", "requestID", req.RequestID, "answers", req.AnswersJSON)
			if !eng.receiveAnswer(req.RequestID, req.AnswersJSON) {
				ctx.Logger().Warn("agent: turn answer not accepted by engine", "requestID", req.RequestID, "engineResumeID", eng.resumeRequestID)
				return fmt.Errorf("agent: turn not waiting for answer (requestId=%q)", req.RequestID)
			}
			a.pendingApproval = false
			a.planApprovalPending = false
			a.pendingAskUser = false
			a.clearPendingInteraction(ctx)
			a.notifyWorkspaceStatus(ctx)
			return nil
		}
	}

	// No live engine: this is the restart path. The blocking turnEngine goroutine
	// is gone, but the pending interaction record survived in the mailbox. Apply
	// the user's answer without resuming the original LLM loop.
	pi := a.pendingInteraction
	if pi == nil {
		return fmt.Errorf("agent: no active turn to answer")
	}
	if pi.RequestID != "" && req.RequestID != "" && pi.RequestID != req.RequestID {
		return fmt.Errorf("agent: answer requestId %q does not match pending interaction %q", req.RequestID, pi.RequestID)
	}

	switch pi.Type {
	case "goal_submit":
		_, _, _ = a.resolveGoalSubmit(ctx, req.AnswersJSON)
		a.clearPendingInteraction(ctx)
		a.notifyWorkspaceStatus(ctx)
		ctx.Logger().Info("agent: resolved pending goal_submit after restart", "requestID", pi.RequestID)
	case "workflow_start":
		_, _, _ = a.resolveWorkflowStart(ctx, req.AnswersJSON)
		a.clearPendingInteraction(ctx)
		a.notifyWorkspaceStatus(ctx)
		ctx.Logger().Info("agent: resolved pending workflow_start after restart", "requestID", pi.RequestID)
	case "goal_card_submit":
		_, _, _, _, _ = a.resolveGoalCardSubmit(ctx, req.AnswersJSON)
		a.clearPendingInteraction(ctx)
		a.notifyWorkspaceStatus(ctx)
		ctx.Logger().Info("agent: resolved pending goal_card_submit after restart", "requestID", pi.RequestID)
	case "plan_approval":
		_, _, _, _ = a.resolvePlanApproval(ctx, req.AnswersJSON)
		a.clearPendingInteraction(ctx)
		ctx.Logger().Info("agent: resolved pending plan_approval after restart", "requestID", pi.RequestID)
	default:
		// ask_user / permission: the live engine is gone, but the pending
		// interaction record survived in the mailbox. Record the user's
		// answer into the interaction step, then CONTINUE the same paused
		// turn (reuse pi.TurnID) instead of spawning a fresh turn below it.
		// The LLM re-reads the resolved answer from the turn's accumulated
		// tail and carries on. This mirrors the crash-recovery resume path
		// in handleTurnResume so the conversation shows one continuous turn
		// envelope rather than a new one beneath the paused interaction.
		a.resolvePendingInteractionStep(ctx, pi, req.AnswersJSON)
		a.clearPendingInteraction(ctx)
		a.pendingApproval = false
		a.pendingAskUser = false

		turnName := pi.TurnID
		freshTurn := false
		if turnName == "" {
			// Defensive fallback: no recorded turn id (should not happen for
			// a recovered interaction). Continue is impossible, so start a
			// fresh turn so the answer is not lost.
			turnName = ctx.NewID().String()
			freshTurn = true
		}
		a.setActiveTurnRef(turnName)
		a.status.TurnID = turnName
		if !freshTurn {
			// Flip the paused interaction turn back to running via the canonical
			// resumed transition (clears Error/PauseReason, bumps Revision),
			// then announce live so the UI re-activates the existing envelope
			// without waiting for the deferred start_turn. Surface a rejection
			// at Error level (not silent); the adapter preserved the prior state.
			if _, lcErr := a.applyTurnLifecycle(ctx, domain.TurnLifecycleResumed, turnName, nil, turnLifecycleOptions{
				role:      "assistant",
				turnOrder: a.getActiveTurnOrder(),
			}); lcErr != nil {
				ctx.Logger().Error("agent: resume turn lifecycle rejected on interaction answer; record retains prior state",
					"turn", turnName, "error", lcErr)
			}
			a.Session.ActiveHead = int32(len(a.Session.Turns) - 1)
		}
		a.notifyWorkspaceStatus(ctx)
		if err := ctx.After(0, "internal_start_turn", startTurnInternalReq{
			TurnInput: domain.TurnInput{Text: answerAsUserMessage(pi.Type, req.AnswersJSON)},
			TurnName:  turnName,
		}); err != nil {
			a.setActiveTurnRef("")
			ctx.Logger().Error("agent: failed to schedule resume turn after interaction", "turn", turnName, "error", err)
			return fmt.Errorf("agent: schedule resume turn: %w", err)
		}
		ctx.Logger().Info("agent: scheduled resume of pending-interaction turn", "type", pi.Type, "turn", turnName, "fresh", freshTurn)
	}
	return nil
}

// resolvePendingInteractionStep marks the persisted interaction step resolved
// with the user's answer, so it stops rendering as a pending card after the
// restart-path answer.
func (a *Actor) resolvePendingInteractionStep(ctx actor.Context, pi *pendingInteractionState, answersJSON string) {
	for i := range a.steps {
		if a.steps[i].ID != pi.StepID {
			continue
		}
		a.steps[i].InteractionStatus = "resolved"
		a.steps[i].Closed = true
		if answersJSON != "" {
			a.steps[i].Content = []domain.ContentBlock{{Type: domain.ContentBlockText, Text: answersJSON}}
		}
		a.touchStep(i)
		break
	}
	ev := domain.StepEvent{
		Kind:            "step.interaction_resolved",
		StepID:          pi.StepID,
		TurnID:          pi.TurnID,
		InteractionType: pi.Type,
		RequestID:       pi.RequestID,
		Task:            map[string]any{"answer": answersJSON},
		Seq:             a.allocSeq(),
	}
	a.applyStepEvent(ev)
	_ = a.emitActorStepEvent(ctx, ev)
	a.takeSnapshot()
}

// answerAsUserMessage renders a blocking-interaction answer as a user turn
// message that the restart turn can consume. The LLM re-reads it as the user's
// response to the question it previously asked.
func answerAsUserMessage(interactionType, answersJSON string) string {
	if strings.TrimSpace(answersJSON) == "" {
		switch interactionType {
		case "permission":
			return "[permission denied]"
		default:
			return "[answered]"
		}
	}
	return answersJSON
}

// clearPendingInteraction drops the recorded blocking interaction and persists
// the change, so a resolved interaction does not resurface after a restart.
func (a *Actor) clearPendingInteraction(ctx actor.Context) {
	if a.pendingInteraction == nil {
		return
	}
	a.pendingInteraction = nil
	a.saveMailbox(ctx)
}

// evaluateGoal is retained as a conservative fallback for callers that still
// use it directly. A textual summary alone is never proof of goal completion.
// Goal completion must come from the independent reviewer verdict.
func (a *Actor) evaluateGoal(ctx actor.Context, turnSteps []domain.Step) (bool, string) {
	return false, "goal review required; textual output is not completion evidence"
}

// grantProjectApproval records a callable ID as approved for a specific project.
func (a *Actor) grantProjectApproval(projectID, callableID string) {
	if a.projectApprovals == nil {
		a.projectApprovals = make(map[string]struct{})
	}
	a.projectApprovals[projectID+"|"+callableID] = struct{}{}
}

// hasProjectApproval checks whether a callable ID has been approved for a specific project.
func (a *Actor) hasProjectApproval(projectID, callableID string) bool {
	if a.projectApprovals == nil {
		return false
	}
	_, ok := a.projectApprovals[projectID+"|"+callableID]
	return ok
}

// summarizeChildFromSteps extracts a fallback summary from the last tool
// results when a child agent produced no text output (common for explore
// agents that only issue tool calls).

func (a *Actor) summarizeChildFromSteps(steps []domain.Step) string {
	var parts []string
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.Role != "assistant" && step.Role != "tool" {
			continue
		}
		for _, cb := range step.Content {
			switch cb.Type {
			case domain.ContentBlockText:
				if cb.Text != "" {
					parts = append(parts, cb.Text)
				}
			case domain.ContentBlockToolResult:
				if cb.Text != "" {
					parts = append(parts, cb.Text)
				}
			}
		}
		// Stop after collecting the last contiguous block of assistant/tool steps.
		if step.Role == "user" && len(parts) > 0 {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	// Reverse to restore chronological order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return strings.Join(parts, "\n\n")
}

// maybeAutoStartTurn checks whether there are queued user messages after
// ActiveHead and, if the agent is idle, starts a new turn automatically.

func (a *Actor) maybeAutoStartTurn(ctx actor.Context) {
	if a.memorySleeping || a.getActiveTurnRef() != "" {
		return
	}
	// Guard: if the agent is paused awaiting external review, do not
	// auto-start a new turn from queued messages. The agent resumes only
	// via resume_from_review (map owner reject) or is torn down (approve).
	if a.RawSession.Goal != nil && a.RawSession.Goal.Status == "ready_for_review" {
		return
	}
	head := int(a.Session.ActiveHead)
	if head < 0 {
		return
	}
	// ActiveHead may already point to a newly queued user turn (e.g. one
	// created while the previous turn was finishing). Start a turn for it.
	if head == len(a.Session.Turns)-1 && a.Session.Turns[head].Role == "user" {
		req := turnInputFromUserTurn(a.Session.Turns[head])
		_, _ = a.startTurn(ctx, req)
		return
	}
	if head >= len(a.Session.Turns)-1 {
		return
	}
	// Find the first user turn after ActiveHead and start a turn for it.
	for i := head + 1; i < len(a.Session.Turns); i++ {
		if a.Session.Turns[i].Role == "user" {
			a.Session.ActiveHead = int32(i)
			req := turnInputFromUserTurn(a.Session.Turns[i])
			_, _ = a.startTurn(ctx, req)
			return
		}
	}
}

// turnInputFromUserTurn rebuilds a TurnInput from a persisted user turn so
// auto-started turns see the same text + images + attachments the user
// originally submitted.
func turnInputFromUserTurn(t domain.Turn) domain.TurnInput {
	return domain.TurnInput{
		Text:        t.UserInput,
		Attachments: t.UserAttachments,
		Images:      t.UserImages,
	}
}

// applyUnitChange records a model unit as the active executing model: it sets
// status.Unit, proactively re-resolves the context window so the budget bar
// tracks the new model, snapshots, emits a turn.unit_changed event, and
// notifies the frontend. Shared by the explicit mid-turn switch (onUnitChanged)
// and the aggregator-resolved-unit feedback (onResolvedUnit).
func (a *Actor) applyUnitChange(ctx actor.Context, turnID string, unit domain.ModelUnit) {
	a.status.Unit = unit
	if a.status.ContextBudget != nil {
		if freshWindow := a.resolveUnitMaxContextLength(ctx, unit); freshWindow > 0 {
			a.status.ContextBudget.ContextWindowSize = freshWindow
			cfg := a.fetchAgentKindConfig(ctx)
			policy := a.resolveCompactionPolicy(cfg)
			if budget := compaction.ResolveTokenBudget(policy, freshWindow); budget > 0 {
				a.status.ContextBudget.TokenBudget = budget
			}
			if a.status.ContextBudget.EstimatedTokens > 0 {
				_ = ctx.EmitEvent("turn", domain.TurnEvent{
					Kind:   domain.TurnContextBudget,
					TurnID: turnID,
					ContextBudget: &domain.TurnContextBudgetPayload{
						EstimatedTokens:   a.status.ContextBudget.EstimatedTokens,
						ContextWindowSize: a.status.ContextBudget.ContextWindowSize,
						TokenBudget:       a.status.ContextBudget.TokenBudget,
					},
				})
			}
		}
	}
	a.takeSnapshot()
	_ = ctx.EmitEvent("turn", domain.TurnEvent{
		Kind:    domain.TurnUnitChanged,
		TurnID:  turnID,
		Payload: map[string]any{"model": unit.Model, "provider": unit.Provider},
	})
	a.notifyWorkspaceStatus(ctx)
}

// resetTurnSnapshot resets the derived status snapshot for a new turn. Called at
// the start of every chat.submit so the new turn starts from a clean slate.

func (a *Actor) resetTurnSnapshot(unit domain.ModelUnit) {
	a.status.State = "running"
	a.status.Unit = unit
	a.status.TurnID = ""
	a.status.ActiveStepID = ""
	a.status.Usage = nil
	a.status.ContextBudget = nil
	a.status.StartStepCount = len(a.steps)
	a.status.StartedAt = ""
	a.status.StepByID = make(map[string]domain.TurnAction)
	a.status.StepOrder = nil

	// Invalidate cached kind config and instructions at the start of every turn
	// so changes to agent kind config (e.g. mounted bundles) are picked up
	// by existing agents without requiring a restart.
	a.kindConfig.Store(nil)
	a.resolvedInstructions.Store(nil)
	a.componentSnapshot.Store(nil)
}

// updateContextBudgetFromUsage updates the context budget bar with actual
// InputTokens from the LLM response and emits a turn event so the frontend
// progress bar reflects the real context size after each dispatch iteration.
// The aggregator piggybacks MaxContextLength in the usage event — this is the
// authoritative value from the unit that was actually used for dispatch,
// eliminating the need for a separate aggregator.status query that may fail
// or return stale/fallback values.
func (a *Actor) updateContextBudgetFromUsage(ctx actor.Context, usage *domain.UsageData) {
	if a.status.ContextBudget == nil || usage == nil {
		return
	}
	// Real context occupancy = all input tokens the provider actually received,
	// which it reports split into non-cache (InputTokens), cache reads and cache
	// creations. Summing the three gives the true window occupancy — not an
	// estimate. Using InputTokens alone would shrink as cache hit rate rises
	// and misreport the real budget consumption.
	occupied := realInputOccupancy(usage)
	a.status.ContextBudget.EstimatedTokens = int32(occupied)

	// Use piggybacked MaxContextLength from the dispatch usage event.
	if usage.MaxContextLength > 0 && usage.MaxContextLength != a.status.ContextBudget.ContextWindowSize {
		a.status.ContextBudget.ContextWindowSize = usage.MaxContextLength
		cfg := a.fetchAgentKindConfig(ctx)
		policy := a.resolveCompactionPolicy(cfg)
		if budget := compaction.ResolveTokenBudget(policy, usage.MaxContextLength); budget > 0 {
			a.status.ContextBudget.TokenBudget = budget
		}
	}

	_ = ctx.EmitEvent("turn", domain.TurnEvent{
		Kind:   domain.TurnContextBudget,
		TurnID: a.getActiveTurnRef(),
		ContextBudget: &domain.TurnContextBudgetPayload{
			EstimatedTokens:   int32(occupied),
			ContextWindowSize: a.status.ContextBudget.ContextWindowSize,
			TokenBudget:       a.status.ContextBudget.TokenBudget,
		},
	})
}

// updateContextBudgetFromEstimate drives the budget bar with a tiktoken local
// estimate when the provider does not report usage at all. It only updates the
// EstimatedTokens display value and emits the budget event so the context
// occupancy stays visible for usage-less providers. It never touches window
// size, token budget, calibration, cost, cache or requestStats, and it never
// overwrites a real value received from a provider that does report usage
// (those flow through updateContextBudgetFromUsage and take precedence because
// they are emitted on every dispatch that carries real usage).
func (a *Actor) updateContextBudgetFromEstimate(ctx actor.Context, turnID string, estimated int64) {
	if a.status.ContextBudget == nil || estimated <= 0 {
		return
	}
	a.status.ContextBudget.EstimatedTokens = int32(estimated)
	_ = ctx.EmitEvent("turn", domain.TurnEvent{
		Kind:   domain.TurnContextBudget,
		TurnID: turnID,
		ContextBudget: &domain.TurnContextBudgetPayload{
			EstimatedTokens:   int32(estimated),
			ContextWindowSize: a.status.ContextBudget.ContextWindowSize,
			TokenBudget:       a.status.ContextBudget.TokenBudget,
		},
	})
}

// realInputOccupancy returns the real context-window occupancy from a
// provider-reported usage: non-cache input + cache reads + cache creations.
// All three come straight from the LLM endpoint; no local estimation.
func realInputOccupancy(u *domain.UsageData) int64 {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// calibrateAndSave adjusts token estimation coefficients by comparing the
// estimated token count with the actual InputTokens from the LLM response,
// then persists the updated calibration to the mailbox.
func (a *Actor) calibrateAndSave(ctx actor.Context, cc tokenest.CharCounts, estimated, actual int) {
	if a.cfg.TokenCalibration == nil {
		a.cfg.TokenCalibration = tokenest.NewCalibration()
	}
	a.cfg.TokenCalibration.Update(cc, estimated, actual)
	ctx.Logger().Debug("agent: calibration updated",
		"latin", cc.Latin, "cjk", cc.CJK, "other", cc.Other,
		"estimated", estimated, "actual", actual,
		"count", a.cfg.TokenCalibration.Snapshot().Count)
	a.saveMailbox(ctx)
}

// reportDiagnostic sends a diagnostic report to the oracle actor.
// Best-effort, fire-and-forget: never blocks the turn loop.
func (a *Actor) reportDiagnostic(ctx actor.Context, req domain.OracleReportDiagnosticReq) {
	planner := ctx.Planner()
	if planner == nil {
		return
	}
	oracleRef, ok := ctx.LookupService("oracle")
	if !ok {
		return
	}
	payload, _ := json.Marshal(req)
	safeGo("oracle_diagnostic", func() {
		callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = planner.Call(callCtx, oracleRef, "oracle.report_diagnostic", payload).Await()
	})
}

// resumeChildAgents enumerates all direct child agents via workspace persistent
// topology (ParentAgentID=self), lazy-loads any that are unloaded via
// workspace.load_agent, then calls turn_resume on each. This is the
// restart-safe alternative to the in-memory activeChildren map which is lost
// on process restart. Used by handleTurnResume when restoring a workflow
// owner's waiting turn.
//
// The whole cascade runs in a background goroutine with Lifecycle-scoped
// timeouts. It MUST NOT run on the owner's actor loop: workspace.load_agent
// spawns the child via the project actor, whose OnStart calls back into
// workspace — a synchronous Final() from the owner would deadlock the owner's
// mailbox against the workspace/project loops (the loadAgentByID comment at
// workspace.go warns about exactly this). LookupID is a registry read, safe
// off-loop; Lifecycle() is actor-lifetime scoped so it outlives the handler.
func (a *Actor) resumeChildAgents(ctx actor.Context) {
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		ctx.Logger().Warn("agent: workspace unavailable, cannot resume child agents")
		return
	}
	pid := parentProjectID(ctx)
	if pid == "" {
		ctx.Logger().Warn("agent: no parent project, cannot resume child agents")
		return
	}
	logger := ctx.Logger()
	owner := a.actorID
	lookupID := ctx.LookupID
	lifecycle := ctx.Lifecycle()
	go func() {
		listCtx, cancel := context.WithTimeout(lifecycle, 10*time.Second)
		defer cancel()
		payload, _ := json.Marshal(domain.WorkspaceListAgentsReq{
			ProjectID:     pid,
			ParentAgentID: owner,
		})
		call := wsRef.Invoke(listCtx, "workspace.list_agents", payload)
		if call == nil {
			logger.Warn("agent: workspace.list_agents call failed")
			return
		}
		v, err := call.Final(listCtx)
		call.Close()
		if err != nil {
			logger.Warn("agent: failed to list child agents", "error", err)
			return
		}
		var resp gen.AgentRefListResp
		switch vv := v.(type) {
		case gen.AgentRefListResp:
			resp = vv
		default:
			body, _ := json.Marshal(vv)
			_ = json.Unmarshal(body, &resp)
		}
		for _, ag := range resp.Items {
			if ag.LoadState != "" && ag.LoadState != "loaded" {
				// Lazy-load: an unloaded child has no live actor to receive
				// turn_resume; load it first so the invoke actually lands.
				loadCtx, loadCancel := context.WithTimeout(lifecycle, 10*time.Second)
				loadCall := wsRef.Invoke(loadCtx, "workspace.load_agent", gen.WorkspaceLoadAgentReq{AgentID: ag.ID})
				if loadCall == nil {
					logger.Warn("agent: lazy-load child invoke failed, skipping turn_resume", "child", ag.ID)
					loadCancel()
					continue
				}
				_, loadErr := loadCall.Final(loadCtx)
				loadCall.Close()
				loadCancel()
				if loadErr != nil {
					logger.Warn("agent: lazy-load child failed, skipping turn_resume", "child", ag.ID, "error", loadErr)
					continue
				}
				logger.Info("agent: lazy-loaded child for turn_resume", "child", ag.ID)
			}
			cid, err := identity.ParseCanonicalID(ag.ActorID)
			if err != nil {
				logger.Warn("agent: cannot parse child actor id for turn_resume", "child", ag.ActorID)
				continue
			}
			childRef, ok := lookupID(id.From(cid))
			if !ok || childRef == nil {
				logger.Warn("agent: cannot resolve child ref for turn_resume", "child", ag.ActorID)
				continue
			}
			rCtx, rCancel := context.WithTimeout(lifecycle, 10*time.Second)
			if call := childRef.Invoke(rCtx, "turn_resume", nil); call != nil {
				_ = call.Close()
			}
			rCancel()
		}
	}()
}
