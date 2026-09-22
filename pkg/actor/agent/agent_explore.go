package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"io"
	"math/rand/v2"
	"strings"
	"time"
	"unicode/utf8"
)

// handleChildStartExplore 提取子 agent 初始任务文本并启动唯一一次探索 turn。
// 这是子 agent 自举的第二步：onStartChild 注册完 callable 后通过
// ctx.After(0, "child_start_explore", nil) 触发。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│    取最后一条 step 的文本    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│          选择 model          │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│          startTurn           │
//	└──────────────────────────────┘

func (a *Actor) handleChildStartExplore(ctx actor.Context) (err error) {
	defer a.takeSnapshot()
	// If any step fails, report the error to the parent immediately so it
	// doesn't wait the full ChildAgentIdleTimeout (2 min) for nothing.
	defer func() {
		if err == nil {
			return
		}
		ctx.Logger().Error("agent: child start failed, reporting to parent", "error", err.Error())
		a.reportChildFailure(ctx, err.Error())
	}()
	if len(a.steps) == 0 {
		return fmt.Errorf("child agent: no messages to start exploration")
	}
	lastStep := a.steps[len(a.steps)-1]
	taskText := ""
	for _, cb := range lastStep.Content {
		if cb.Type == domain.ContentBlockText {
			taskText = cb.Text
		}
	}
	if taskText == "" {
		return fmt.Errorf("child agent: empty task text")
	}

	targets := a.resolveTargets(ctx, a.primary)
	if len(targets) == 0 {
		return fmt.Errorf("child agent: %s missing resolvable model slot", a.agentKind)
	}

	// Resolve the primary slot's first concrete unit so the child starts on the
	// same unit the parent selected (e.g. fast slot = unit). When the slot is
	// unit-locked (no aggregator fallback), this also pins the unit for dispatch
	// — the turn engine's resolveTargets/resolveCandidate path re-resolves with
	// live health, but UnitPinned is driven by isUnitLockedSlot(e.primarySlot).
	// For aggregator/auto slots the unit is a projection; the aggregator still
	// auto-picks at dispatch time.
	var reqUnit *domain.ModelUnit
	if u := a.slotFirstUnitResolved(ctx, a.primary); u.Model != "" {
		reqUnit = &u
	}
	_, err = a.startTurn(ctx, domain.TurnInput{
		Text: taskText,
		Unit: reqUnit,
	})
	if err != nil {
		return fmt.Errorf("child startTurn failed: %w", err)
	}
	return nil
}

// deliverExploreComplete delivers the explore_complete terminal message to the
// parent agent and then self-terminates via workspace.agent_terminate,
// regardless of delivery success.
//
// explore_complete is the authoritative signal that unblocks the parent's
// executeWaitChildren. It is delivered via the parent's owner queue, which is
// bounded and non-blocking: during a long-running fork the queue can be
// transiently saturated by high-frequency explore_progress messages, causing an
// instantaneous delivery failure. We therefore retry with backoff so a
// momentary full-queue does not strand the child.
//
// Self-termination is unconditional: a child whose terminal message never lands
// would otherwise linger as a zombie (turn already ended, no more heartbeats),
// and a still-alive zombie's agent_status keeps answering the parent's liveness
// poll with a stale activity timestamp — which can keep the parent's idle
// deadline from ever firing. Terminating the child via workspace.agent_terminate
// triggers cascadeDelete which both destroys the actor and removes it from
// workspace.Agents / UI projections, guaranteeing the parent's
// ChildAgentIdleTimeout backstop engages even when delivery ultimately fails.
func (a *Actor) deliverExploreComplete(ctx actor.Context, result domain.ForkResult) {
	defer a.terminateSelf(ctx)
	planner := ctx.Planner()
	if planner == nil || a.child.AgentRef == nil {
		return
	}
	req := exploreCompleteReq{
		ParentTurnID: a.child.TurnRef,
		ParentStepID: a.child.StepRef,
		ToolUseID:    a.child.ToolUseID,
		ChildAgentID: a.actorID,
		Result:       result,
	}
	const maxAttempts = 3
	backoff := [3]time.Duration{0, time.Second, 3 * time.Second}
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if backoff[attempt] > 0 {
			select {
			case <-time.After(backoff[attempt]):
			case <-ctx.Lifecycle().Done():
				return
			}
		}
		callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 8*time.Second)
		_, err := planner.Call(callCtx, a.child.AgentRef, "explore_complete", req).Await()
		cancel()
		if err == nil {
			return
		}
		ctx.Logger().Warn("agent: explore_complete delivery failed",
			"attempt", attempt+1, "max", maxAttempts, "error", err)
	}
	ctx.Logger().Error("agent: explore_complete delivery exhausted retries; terminating child so parent idle-timeout can fire",
		"toolUseID", a.child.ToolUseID)
}

// terminateSelf removes this fork child from the workspace via
// workspace.agent_terminate (which runs cascadeDelete: marks deleting,
// removes from workspace.Agents projection, destroys the actor). If the
// workspace is unavailable or the call fails, falls back to ctx.Destroy so
// the actor does not linger as a zombie.
//
// CallerAgentID is set to the child's own actor ID so the workspace
// authorization can verify self-termination (caller == target agent).
func (a *Actor) terminateSelf(ctx actor.Context) {
	wsRef, ok := ctx.LookupService("workspace")
	if ok && wsRef != nil && a.actorID != "" {
		termCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
		call := wsRef.Invoke(termCtx, "workspace.agent_terminate", domain.WorkspaceAgentTerminateReq{
			AgentActorID:  a.actorID,
			CallerAgentID: a.actorID,
		})
		if call != nil {
			_, err := call.Final(termCtx)
			call.Close()
			cancel()
			if err == nil {
				return
			}
			ctx.Logger().Warn("agent: workspace.agent_terminate failed, falling back to ctx.Destroy",
				"error", err)
		} else {
			cancel()
		}
	}
	// Fallback: direct self-destroy (legacy path).
	_ = ctx.Destroy(ctx.Self())
}

// reportChildFailure sends an explore_complete with an error summary to the
// parent agent so the parent's turn engine can resolve the fork step instead
// of waiting for ChildAgentIdleTimeout.
func (a *Actor) reportChildFailure(ctx actor.Context, errMsg string) {
	if !a.child.Mode || a.child.AgentRef == nil {
		return
	}
	a.deliverExploreComplete(ctx, domain.ForkResult{
		Summary: fmt.Sprintf("Child agent failed to start: %s", errMsg),
	})
}

// handleExploreProgress 接收子 agent 发来的进度更新。
// 它做三件事：
//   1. 把进度事件写入当前 turnEngine 的事件缓冲；
//   2. 立即 flush，因为父 turn 可能正阻塞在 executeWaitChildren；
//   3. 把最新进度 upsert 到 RawSession.ExploreResults，支持断线重连。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│       父 turn 仍活跃？       │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   构造 TurnExploreProgress   │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│   bufferTurnEvent + flush    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    upsert ExploreResults     │
//	└──────────────────────────────┘

// snapshotAfterChildProgress refreshes the atomic session snapshot at most once
// per second. Child progress arrives every ~200ms while a fork streams; taking
// a full snapshot per message (openStepEvents copy + chat message rebuild) kept
// the owner loop busy long enough to saturate the owner queue (cap 64), after
// which further explore_progress deliveries failed instantly and were dropped,
// freezing the progress display. ExploreResults freshness of ≤1s is acceptable.
func (a *Actor) snapshotAfterChildProgress() {
	if time.Since(a.exploreProgressSnapshotAt) < time.Second {
		return
	}
	a.exploreProgressSnapshotAt = time.Now()
	a.takeSnapshot()
}

func (a *Actor) handleExploreProgress(ctx actor.Context, progress domain.ForkChildProgress) error {
	defer a.snapshotAfterChildProgress()
	if a.getActiveTurnRef() == "" {
		return nil
	}
	eng := a.activeTurnEngine()
	if eng == nil {
		return nil
	}
	if progress.StepID == "" {
		return nil
	}
	if _, ok := eng.pendingChildSteps.Load(progress.StepID); !ok {
		ctx.Logger().Info("agent: ignoring stale child progress", "stepID", progress.StepID)
		return nil
	}
	// A valid progress proves the child is alive: refresh its activity timestamp
	// and wake executeWaitChildren so it recomputes the idle deadline immediately
	// instead of waiting up to ChildHeartbeatInterval (15s) for the next poll.
	eng.childProgressAt.Store(progress.StepID, time.Now().UnixNano())
	select {
	case eng.childProgressCh <- struct{}{}:
	default:
	}
	workMaps := make([]map[string]any, len(progress.ActiveWork))
	for i, w := range progress.ActiveWork {
		workMaps[i] = map[string]any{"Type": w.Type, "Target": w.Target}
	}
	// Emit explore progress as step.execution_progress.
	progressJSON, _ := json.Marshal(map[string]any{
		"phase":        progress.Phase,
		"searchCount":  progress.SearchCount,
		"readCount":    progress.ReadCount,
		"inputTokens":  progress.InputTokens,
		"outputTokens": progress.OutputTokens,
		"summaryText":  progress.SummaryText,
		"action":       progress.Action,
		"actionTarget": progress.ActionTarget,
		"activeWork":   workMaps,
	})
	ev := domain.StepEvent{
		Kind:     "step.execution_progress",
		StepID:   progress.StepID,
		TurnID:   a.getActiveTurnRef(),
		Progress: string(progressJSON),
	}
	eng.emitChildProgressEvent(ev)
	// Flush immediately — the parent's custom loop may be blocked in
	// stepWaitChild and won't flush until the child finishes.
	if err := eng.flushEvents(ctx); err != nil {
		ctx.Logger().Error("agent: flush explore progress failed", "error", err)
	}

	// Persist the latest progress to RawSession so it survives disconnect/reconnect.
	// Upsert by TurnId+StepId so multiple progress updates don't create duplicates.
	found := false
	for i := range a.RawSession.ExploreResults {
		if a.RawSession.ExploreResults[i].TurnID == a.getActiveTurnRef() && a.RawSession.ExploreResults[i].StepID == progress.StepID {
			a.RawSession.ExploreResults[i].Summary = progress.SummaryText
			a.RawSession.ExploreResults[i].SearchCount = progress.SearchCount
			a.RawSession.ExploreResults[i].ReadCount = progress.ReadCount
			found = true
			break
		}
	}
	if !found {
		a.RawSession.ExploreResults = append(a.RawSession.ExploreResults, domain.ExploreResult{
			TurnID:      a.getActiveTurnRef(),
			StepID:      progress.StepID,
			Summary:     progress.SummaryText,
			SearchCount: progress.SearchCount,
			ReadCount:   progress.ReadCount,
			Timestamp:   time.Now().UTC().Format(time.RFC3339),
		})
	}

	return nil
}

// exploreCompleteReq is the internal request for agent.explore_complete.
type exploreCompleteReq struct {
	ParentTurnID string `json:"ParentTurnID"`
	ParentStepID string `json:"ParentStepID"`
	ToolUseID    string `json:"ToolUseID"`
	// ChildAgentID is the child's own actor id; persisted into ExploreResult
	// so agent_wait can retrieve cross-turn results by agent id.
	ChildAgentID string            `json:"ChildAgentID,omitempty"`
	Result       domain.ForkResult `json:"Result"`
}

// handleExploreComplete 接收子 agent 的探索终态结果。
// 流程：
//   1. 从 activeChildren 中移除该子 agent；
//   2. 把结果 upsert 到 RawSession.ExploreResults（断线重连可恢复）；
//   3. 如果父 turn 仍在运行，通过 deliverChildResult 把结果交给 turnEngine，
//      唤醒 executeWaitChildren 并注入 tool message。
//
// 流程图：
//
//	┌──────────────────────────────┐
//	│        规范化 summary        │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    从 activeChildren 移除    │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    upsert ExploreResults     │
//	└──────────────────────────────┘
//	            ↓
//	┌──────────────────────────────┐
//	│    父 turn 仍活跃且匹配？    │
//	└──────────────────────────────┘

func (a *Actor) handleExploreComplete(ctx actor.Context, req exploreCompleteReq) error {
	defer a.takeSnapshot()
	ctx.Logger().Info("agent: exploration complete",
		"summary_len", len(req.Result.Summary),
		"search_count", req.Result.SearchCount,
		"read_count", req.Result.ReadCount,
		"iterations", req.Result.Iterations)

	summary := req.Result.Summary
	if summary == "" {
		summary = "Exploration completed with no findings."
	}

	// Remove the child from active tracking once its result is delivered.
	a.activeChildrenMu.Lock()
	delete(a.activeChildren, req.ToolUseID)
	a.activeChildrenMu.Unlock()
	// Push updated fork child projection: the live child is removed.
	a.notifyWorkspaceStatus(ctx)
	if req.ToolUseID == "memory-sleep" {
		a.finishMemorySleep(ctx, summary)
		return nil
	}

	// Persist the explore result to RawSession so it survives disconnect/reconnect.
	// Upsert by TurnId+StepId — handleExploreProgress may have already created a stub entry.
	found := false
	for i := range a.RawSession.ExploreResults {
		if a.RawSession.ExploreResults[i].TurnID == req.ParentTurnID && a.RawSession.ExploreResults[i].StepID == req.ParentStepID {
			a.RawSession.ExploreResults[i].Summary = summary
			a.RawSession.ExploreResults[i].SearchCount = req.Result.SearchCount
			a.RawSession.ExploreResults[i].ReadCount = req.Result.ReadCount
			a.RawSession.ExploreResults[i].InputTokens = req.Result.InputTokens
			a.RawSession.ExploreResults[i].OutputTokens = req.Result.OutputTokens
			a.RawSession.ExploreResults[i].Timestamp = time.Now().UTC().Format(time.RFC3339)
			if req.ChildAgentID != "" {
				a.RawSession.ExploreResults[i].AgentID = req.ChildAgentID
			}
			found = true
			break
		}
	}
	if !found {
		a.RawSession.ExploreResults = append(a.RawSession.ExploreResults, domain.ExploreResult{
			TurnID:       req.ParentTurnID,
			StepID:       req.ParentStepID,
			AgentID:      req.ChildAgentID,
			Summary:      summary,
			SearchCount:  req.Result.SearchCount,
			ReadCount:    req.Result.ReadCount,
			InputTokens:  req.Result.InputTokens,
			OutputTokens: req.Result.OutputTokens,
			Timestamp:    time.Now().UTC().Format(time.RFC3339),
		})
	}

	// If the originating turn is still active, deliver the result to the
	// inline turnEngine so the turn loop wakes up and injects it into the
	// tool message history.
	if ref := a.getActiveTurnRef(); ref != "" && ref == req.ParentTurnID {
		a.deliverChildResult(ctx, req)
		return nil
	}

	// Parent turn was cancelled or has moved on — result is still persisted in
	// RawSession.ExploreResults for display on reconnect.
	ctx.Logger().Info("agent: parent turn no longer active, explore result persisted to RawSession")
	return nil
}

// childSpawnOpts captures the few parameters that actually differ between
// explore / fork_general / goal_review spawns. Everything else (hot context,
// parent context, NewChildActor wiring, activeChildren tracking, naming) is
// shared by spawnChild below.
type childSpawnOpts struct {
	Kind          string
	Description   string
	Prompt        string
	Slot          domain.ModelSlot
	MaxIterations int32
	ParentTurnID  string
	ParentStepID  string
	ToolUseID     string
	NamePrefix    string // "explore" | "general" | "review" | "dream"
	Isolated      bool
}

func (a *Actor) childLiveness(ctx actor.Context, toolUseID string) (time.Time, error) {
	a.activeChildrenMu.Lock()
	childRef := a.activeChildren[toolUseID]
	a.activeChildrenMu.Unlock()
	if childRef == nil {
		return time.Time{}, fmt.Errorf("child %s is not active", toolUseID)
	}
	call := childRef.Invoke(ctx.Lifecycle(), "agent_status", nil)
	if call == nil {
		return time.Time{}, fmt.Errorf("child %s status call unavailable", toolUseID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	value, err := call.Final(callCtx)
	if err != nil {
		return time.Time{}, err
	}
	status, ok := value.(gen.AgentStatusResp)
	if !ok {
		if ptr, ok := value.(*gen.AgentStatusResp); ok && ptr != nil {
			status = *ptr
		} else {
			return time.Time{}, fmt.Errorf("child %s status response has type %T", toolUseID, value)
		}
	}
	if status.ChildLastActivitySec <= 0 {
		return time.Time{}, fmt.Errorf("child %s has no activity timestamp", toolUseID)
	}
	return time.Unix(int64(status.ChildLastActivitySec), 0), nil
}

// trackForkChild records a fork child spawned externally (by workspace.agent_spawn_by_type)
// in the parent agent's activeChildren map so cancellation, liveness checks, and
// explore_complete routing work. The workspace owns the spawn lifecycle; the parent
// only needs the child's ActorID to resolve its ref.
func (a *Actor) trackForkChild(ctx actor.Context, toolUseID, childActorID string) {
	if toolUseID == "" || childActorID == "" {
		return
	}
	childRef := resolveChildRef(ctx, childActorID)
	if childRef == nil {
		ctx.Logger().Warn("agent: fork child spawned but ref not resolvable",
			"tool_use_id", toolUseID, "child_id", childActorID)
	}
	a.activeChildrenMu.Lock()
	if a.activeChildren == nil {
		a.activeChildren = make(map[string]ref.Ref)
	}
	a.activeChildren[toolUseID] = childRef
	a.activeChildrenMu.Unlock()
	ctx.Logger().Info("agent: tracked fork child",
		"tool_use_id", toolUseID, "child_id", childActorID, "ref_resolved", childRef != nil)
	// Push updated fork child projection so the live child appears in sidebar/topology.
	a.notifyWorkspaceStatus(ctx)
}

func (a *Actor) cancelChild(ctx actor.Context, toolUseID string) {
	a.activeChildrenMu.Lock()
	childRef := a.activeChildren[toolUseID]
	delete(a.activeChildren, toolUseID)
	// Same rationale as handleTurnCancel: a dreamer killed by timeout
	// self-destructs without sending explore_complete, so finishMemorySleep
	// never fires. Reset here to avoid leaking memorySleeping=true forever.
	if toolUseID == "memory-sleep" {
		a.memorySleeping = false
		a.memorySleepTurn = ""
		a.closeDreamStep(ctx, "Memory dream cancelled.")
	}
	a.activeChildrenMu.Unlock()
	if childRef == nil {
		return
	}
	if call := childRef.Invoke(ctx.Lifecycle(), "turn_cancel", nil); call != nil {
		_ = call.Close()
	}
	// Push updated fork child projection: the cancelled child is removed
	// from the live projection.
	a.notifyWorkspaceStatus(ctx)
}

// spawnChild is the single shared child-agent spawn path. It resolves hot +
// parent context (the child cannot do this itself), then spawns a persistent
// workspace agent via project.spawn_agent (the unified spawn path) with
// ChildConfig so the child is created as a fork child (NewChildActor). It
// registers the child in activeChildren keyed by ToolUseID so the turn engine
// can route the result back. It returns the spawned child's actor ID.
func (a *Actor) spawnChild(ctx actor.Context, opts childSpawnOpts) (string, error) {
	var hotContext []domain.ContentBlock
	prompt := opts.Prompt
	if !opts.Isolated {
		hotContext = a.resolveHotContext(ctx)
		if ctxText := a.buildParentContext(); ctxText != "" {
			prompt += ctxText
		}
	}
	parentTurnID := opts.ParentTurnID
	if parentTurnID == "" {
		parentTurnID = a.getActiveTurnRef()
	}

	// Resolve the parent project ref so we can call project.spawn_agent.
	projectRef, ok := ctx.LookupService("project")
	if !ok || projectRef == nil {
		return "", fmt.Errorf("spawnChild: project service not available")
	}

	// Build the child config for the unified spawn path. The project will
	// use NewChildActor (child.Mode = true) to create a fork child that
	// auto-starts a single exploration turn.
	childName := fmt.Sprintf("%s-%d-%s", opts.NamePrefix, a.childNameSeq.Add(1), opts.ToolUseID)
	primarySlot := opts.Slot

	// Copy the parent's non-primary runtime slots so the child inherits the
	// same fast/execution/review/summary configuration. Empty ([auto]) slots
	// are dropped (slotPtr → nil) so the child keeps the default [auto].
	a.slotMu.RLock()
	fastSlot, executionSlot, reviewSlot, summarySlot := a.fast, a.execution, a.review, a.summary
	a.slotMu.RUnlock()

	spawnReq := domain.ProjectSpawnAgentReq{
		SpawnName:     childName,
		AgentKind:     opts.Kind,
		WorkspaceID:   a.workspaceID,
		DisplayName:   pickChildDisplayName(opts.Kind),
		Primary:       &primarySlot,
		Fast:          slotPtr(fastSlot),
		Execution:     slotPtr(executionSlot),
		Review:        slotPtr(reviewSlot),
		Summary:       slotPtr(summarySlot),
		ParentAgentID: a.actorID,
		ChildConfig: &domain.ChildSpawnConfig{
			ParentActorID:   a.actorID,
			ParentTurnID:    parentTurnID,
			ParentStepID:    opts.ParentStepID,
			ParentToolUseID: opts.ToolUseID,
			Task:            opts.Description,
			Prompt:          prompt,
			MaxIterations:   opts.MaxIterations,
			HotContext:      hotContext,
		},
	}

	spawnCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	call := projectRef.Invoke(spawnCtx, "project.spawn_agent", spawnReq)
	if call == nil {
		cancel()
		return "", fmt.Errorf("spawnChild: project.spawn_agent not available")
	}
	v, err := call.Final(spawnCtx)
	if err != nil {
		call.Close()
		cancel()
		return "", fmt.Errorf("spawnChild: project.spawn_agent failed: %w", err)
	}
	call.Close()
	cancel()

	var resp domain.ProjectSpawnAgentResp
	switch x := v.(type) {
	case domain.ProjectSpawnAgentResp:
		resp = x
	case *domain.ProjectSpawnAgentResp:
		if x != nil {
			resp = *x
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("spawnChild: marshal response: %w", err)
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", fmt.Errorf("spawnChild: unmarshal response: %w", err)
		}
	}
	if resp.ActorID == "" {
		return "", fmt.Errorf("spawnChild: project.spawn_agent returned empty ActorID")
	}

	// Resolve the child's ref.Ref from its ActorID for activeChildren tracking.
	childRef := resolveChildRef(ctx, resp.ActorID)
	if childRef == nil {
		// The child was spawned but we can't resolve its ref. This is
		// non-fatal: the child will still send explore_complete via the
		// workspace's planner routing, but the parent can't proactively
		// cancel or poll its liveness.
		ctx.Logger().Warn("agent: child spawned but ref not resolvable",
			"kind", opts.Kind, "child_id", resp.ActorID)
	}

	a.activeChildrenMu.Lock()
	if a.activeChildren == nil {
		a.activeChildren = make(map[string]ref.Ref)
	}
	a.activeChildren[opts.ToolUseID] = childRef
	a.activeChildrenMu.Unlock()
	ctx.Logger().Info("agent: child spawned via project.spawn_agent",
		"kind", opts.Kind, "child_id", resp.ActorID, "task", opts.Description,
		"slot_candidates", len(opts.Slot.Candidates),
	)
	// Push updated fork child projection to workspace so the new live
	// child appears in sidebar/topology immediately.
	a.notifyWorkspaceStatus(ctx)
	return resp.ActorID, nil
}

// resolveChildRef resolves a child agent's ref.Ref from its ActorID string.
func resolveChildRef(ctx actor.Context, actorID string) ref.Ref {
	cid, err := identity.ParseCanonicalID(actorID)
	if err != nil {
		return nil
	}
	r, ok := ctx.LookupID(id.From(cid))
	if !ok || r == nil {
		return nil
	}
	return r
}

// pickChildDisplayName returns a display name for the child based on kind.
func pickChildDisplayName(childKind string) string {
	if childKind == "explorer" {
		return pickExplorerName()
	}
	return pickGeneralName()
}

// resolveChildSlot picks the primary model slot for a child agent by kind. It
// uses resolveTargets to select the first slot that actually resolves, so
// aggregator/auto candidates are honored instead of being collapsed to a pinned
// unit (the bug fixed here: fast/execution/review slots configured as an
// aggregator or [auto] used to be silently dropped). An explicit request unit
// overrides slot selection entirely. The returned slot is the raw configured
// slot; the child turn engine re-resolves it on every dispatch, gaining the
// aggregator pool strategy and cross-unit fallback.
func (a *Actor) resolveChildSlot(ctx actor.PureContext, childKind string, reqUnit *domain.ModelUnit) (domain.ModelSlot, error) {
	if reqUnit != nil && reqUnit.Model != "" {
		return slotFromUnit(*reqUnit), nil
	}
	a.slotMu.RLock()
	slots := struct{ fast, primary, execution, review domain.ModelSlot }{
		a.fast, a.primary, a.execution, a.review,
	}
	a.slotMu.RUnlock()
	var priority []domain.ModelSlot
	switch childKind {
	case "explorer", domain.AgentKindDreamer:
		priority = []domain.ModelSlot{slots.fast, slots.primary}
	case "scout":
		priority = []domain.ModelSlot{slots.fast, slots.primary}
	case domain.AgentKindReviewer:
		priority = []domain.ModelSlot{slots.review, slots.fast, slots.primary}
	default:
		priority = []domain.ModelSlot{slots.execution, slots.primary, slots.fast}
	}
	for _, slot := range priority {
		if len(a.resolveTargets(ctx, slot)) > 0 {
			return slot, nil
		}
	}
	return domain.ModelSlot{}, fmt.Errorf("agent: no model target available for child kind %q", childKind)
}

// ResolveChildSlotReq is the internal request for agent.resolve_child_slot,
// called by workspace.agent_spawn_by_type to resolve the parent's runtime model
// slot for a fork child before calling project.spawn_agent directly.
type ResolveChildSlotReq struct {
	ChildKind string
	Unit      *domain.ModelUnit
}

// ResolveChildSlotResp returns the resolved model slot for the child spawn.
// Slot is the resolved primary-purpose slot (unchanged priority logic).
// Fast/Execution/Review/Summary mirror the parent agent's runtime non-primary
// slots so callers can copy them to the child spawn; an empty slot ([auto],
// slotIsEmpty) stays the zero value so callers treat absence as "keep [auto]".
type ResolveChildSlotResp struct {
	Slot      domain.ModelSlot
	Fast      domain.ModelSlot
	Execution domain.ModelSlot
	Review    domain.ModelSlot
	Summary   domain.ModelSlot
}

// slotOrZero returns the slot, or the zero ModelSlot when it is empty ([auto]).
func slotOrZero(slot domain.ModelSlot) domain.ModelSlot {
	if slotIsEmpty(slot) {
		return domain.ModelSlot{}
	}
	return slot
}

// handleResolveChildSlot resolves the model slot for a child agent spawn.
// It is called by the workspace's handleAgentSpawnByType to get the slot
// before calling project.spawn_agent. This replaces the former fork_agent
// delegation path where the agent performed slot resolution internally.
// Slot resolution (primary priority) is untouched; the parent's non-primary
// runtime slots are copied under the slotMu read lock so the child inherits
// the same fast/execution/review/summary configuration, with empty ([auto])
// slots left zero-valued for the caller to drop.
func (a *Actor) handleResolveChildSlot(ctx actor.PureContext, req ResolveChildSlotReq) (ResolveChildSlotResp, error) {
	childKind := req.ChildKind
	if childKind == "" {
		childKind = "general"
	}
	slot, err := a.resolveChildSlot(ctx, childKind, req.Unit)
	if err != nil {
		return ResolveChildSlotResp{}, err
	}
	a.slotMu.RLock()
	resp := ResolveChildSlotResp{
		Slot:      slot,
		Fast:      slotOrZero(a.fast),
		Execution: slotOrZero(a.execution),
		Review:    slotOrZero(a.review),
		Summary:   slotOrZero(a.summary),
	}
	a.slotMu.RUnlock()
	return resp, nil
}

// spawnDreamChild spawns a memory-dream child agent (isolated, single-turn).
// It is the dedicated local spawn path for memory consolidation, bypassing
// the workspace-level agent_spawn_by_type route. The child runs the dream
// prompt and delivers results via explore_complete → finishMemorySleep.
func (a *Actor) spawnDreamChild(ctx actor.Context, prompt string) (string, error) {
	slot, err := a.resolveChildSlot(ctx, domain.AgentKindDreamer, nil)
	if err != nil {
		return "", err
	}
	return a.spawnChild(ctx, childSpawnOpts{
		Kind:          domain.AgentKindDreamer,
		Description:   "Consolidate memory",
		Prompt:        prompt,
		Slot:          slot,
		MaxIterations: 1,
		ToolUseID:     "memory-sleep",
		NamePrefix:    "dream",
		Isolated:      true,
	})
}

func pickExplorerName() string {
	return explorerNamePool[rand.IntN(len(explorerNamePool))]
}

func pickGeneralName() string {
	return generalNamePool[rand.IntN(len(generalNamePool))]
}

func (a *Actor) onStartChild(ctx actor.Context) error {
	// Exec loop so startTurn → agent.run → handleRun can execute the turn inline.
	if err := ctx.RegisterLoop("agent_exec", actor.ModeStateful); err != nil {
		return fmt.Errorf("agent: register exec loop: %w", err)
	}
	if err := ctx.Register("agent_run", a.handleRun, actor.Internal(), actor.WithLoop("agent_exec")); err != nil {
		return fmt.Errorf("agent: register agent_run: %w", err)
	}
	if err := ctx.Register("turn_cancel", a.handleTurnCancel, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_cancel: %w", err)
	}
	if err := ctx.Register("turn_answer", a.handleTurnAnswer, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_answer: %w", err)
	}
	if err := ctx.Register("turn_complete", a.handleTurnComplete, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register turn_complete: %w", err)
	}
	if err := ctx.Register("turn_status", a.handleTurnStatus, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_status: %w", err)
	}
	if err := ctx.Register("turn_middleware", a.handleTurnMiddleware, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_middleware: %w", err)
	}
	if err := ctx.Register("turn_history", a.handleTurnHistory, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turn_history: %w", err)
	}
	if err := ctx.Register("agent_status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("agent: register agent_status: %w", err)
	}
	if err := ctx.Register("context_budget", a.handleContextBudget, actor.Public()); err != nil {
		return fmt.Errorf("agent: register context_budget: %w", err)
	}
	if err := ctx.Register("compaction_configure", a.handleCompactionConfigure, actor.Public()); err != nil {
		return fmt.Errorf("agent: register compaction_configure: %w", err)
	}
	if err := ctx.Register("inspect_pages", a.handleInspectPages, actor.Public()); err != nil {
		return fmt.Errorf("agent: register inspect_pages: %w", err)
	}
	if err := ctx.Register("prompt_artifact", a.handlePromptArtifact, actor.Public()); err != nil {
		return fmt.Errorf("agent: register prompt_artifact: %w", err)
	}
	if err := ctx.Register("compiled_prompt", a.handleCompiledPrompt, actor.Public()); err != nil {
		return fmt.Errorf("agent: register compiled_prompt: %w", err)
	}
	if err := ctx.Register("session_fork", a.handleSessionFork, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_fork: %w", err)
	}
	if err := ctx.Register("session_get", a.handleGetSession, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_get: %w", err)
	}
	if err := ctx.Register("session_summary", a.handleSessionSummaryPure, actor.Public()); err != nil {
		return fmt.Errorf("agent: register session_summary: %w", err)
	}
	if err := ctx.Register("turns_list", a.handleTurnsList, actor.Public()); err != nil {
		return fmt.Errorf("agent: register turns_list: %w", err)
	}
	if err := ctx.Register("thinking_get_registry", a.handleThinkingGetRegistry, actor.Public()); err != nil {
		return fmt.Errorf("agent: register thinking_get_registry: %w", err)
	}
	if err := ctx.Register("thinking_set_level", a.handleThinkingSetLevel, actor.Public()); err != nil {
		return fmt.Errorf("agent: register thinking_set_level: %w", err)
	}
	if err := ctx.Register("permission_mode_set", a.handleSetPermissionMode, actor.Public()); err != nil {
		return fmt.Errorf("agent: register permission_mode_set: %w", err)
	}
	if err := ctx.Register("child_start_explore", a.handleChildStartExplore, actor.Internal()); err != nil {
		return fmt.Errorf("agent: register child_start_explore: %w", err)
	}

	a.actorID = ctx.Self().ID().String()
	// Fork children inherit their primary slot from the parent at spawn time.
	// Arm the one-shot Primary report so the first status notification carries
	// the resolved slot — the workspace syncs it into the AgentRef so the
	// frontend composer shows the correct model instead of "Auto".
	if !slotIsEmpty(a.primary) {
		a.routeReportPending.Store(true)
	}
	a.notifyWorkspaceStatus(ctx)
	a.seedBuiltinComponentMounts(ctx)
	if err := ctx.Register("skill_use", a.handleSkillUse, actor.Public(),
		actor.WithDescription("Invoke a mounted skill by skillId and receive its workflow instructions. The skill must be mounted first; each skill can be used at most once per session."),
	); err != nil {
		return fmt.Errorf("agent: register child skill.use: %w", err)
	}

	// Reviewer children use the same automatic child turn as Explore. Their only
	// behavioral difference is the terminal callback handled by turn completion.
	return ctx.After(0, "child_start_explore", nil)
}

// deliverChildResult 把子 agent 的完成结果交给当前运行的 turnEngine。
// 由 turnEngine.deliverChildResult 写入 childDoneCh，唤醒 executeWaitChildren。

func (a *Actor) deliverChildResult(ctx actor.Context, req exploreCompleteReq) {
	// Inline path only — turn actor has been retired.
	eng := a.activeTurnEngine()
	if eng != nil && a.getActiveTurnRef() == req.ParentTurnID {
		eng.deliverChildResult(childResult{
			ToolUseID: req.ToolUseID,
			Result:    req.Result,
		})
		return
	}
	ctx.Logger().Warn("agent: no active inline turn for child result", "turnID", req.ParentTurnID)
}

// lookupExploreResultByAgentID resolves a child agent id against the persisted
// ExploreResults (newest first) so agent_wait can harvest async children that
// finished after their parent turn ended.
func (a *Actor) lookupExploreResultByAgentID(agentID string) (domain.ExploreResult, bool) {
	for i := len(a.RawSession.ExploreResults) - 1; i >= 0; i-- {
		if a.RawSession.ExploreResults[i].AgentID == agentID {
			return a.RawSession.ExploreResults[i], true
		}
	}
	return domain.ExploreResult{}, false
}

// startFollowUpTurn creates a new turn seeded with the child agent's
// findings so the LLM can process them.

func (a *Actor) startFollowUpTurn(ctx actor.Context, findings string) {
	// Use a minimal language-neutral prefix so the LLM can continue in the
	// user's established language rather than switching to English.
	turnInput := fmt.Sprintf("exploration findings:\n\n%s", findings)
	_, err := a.startTurn(ctx, domain.TurnInput{Text: turnInput})
	if err != nil {
		ctx.Logger().Error("agent: follow-up turn failed", "error", err)
	}
}

func (a *Actor) touchChildActivity() {
	if a.child.Mode {
		a.child.LastActivityNs.Store(time.Now().Unix())
	}
}

// onExploreToolExecuted is called by the turn engine after each tool execution
// in child mode. It updates counters, marks progress dirty, and triggers a flush.

func (a *Actor) onExploreToolExecuted(ctx actor.Context, callableID, input string) {
	a.touchChildActivity()
	wt := workItemType(callableID)
	target := workItemTarget(callableID, input)
	switch wt {
	case "search":
		a.child.SearchCnt++
	case "read":
		a.child.ReadCnt++
	}
	a.child.ActiveWork = []gen.WorkItem{{Type: wt, Target: target}}

	phase := "searching"
	if a.child.SearchCnt > 0 && a.child.ReadCnt > 0 {
		phase = "reading"
	} else if a.child.SearchCnt > 0 {
		phase = "searching"
	} else if a.child.ReadCnt > 0 {
		phase = "reading"
	}
	a.child.Phase = phase
	a.child.ProgressDirty = true
	a.maybeFlushChildProgress(ctx)
}

// maybeFlushChildProgress sends an explore_progress to the parent if enough time
// has passed since the last flush and there is dirty data to send. It runs
// asynchronously so the actor loop is never blocked.
//
// In addition to dirty-data flushes (200ms rate-limited), it sends a heartbeat
// every ChildHeartbeatInterval even when there is no dirty data. This keeps
// the parent's ChildAgentIdleTimeout alive during long LLM reasoning, where
// reasoning chunks flow through flushDeltas → flushEvents → onFlushProgress but
// ProgressDirty is never set.

func (a *Actor) maybeFlushChildProgress(ctx actor.Context) {
	if !a.child.Mode || a.child.AgentRef == nil {
		return
	}
	now := time.Now()
	sinceFlush := now.Sub(a.child.LastProgressFlush)

	// Heartbeat: if ChildHeartbeatInterval has elapsed since the last flush,
	// send a lightweight progress regardless of ProgressDirty. This covers
	// long LLM reasoning periods.
	heartbeat := sinceFlush >= domain.ChildHeartbeatInterval
	if !heartbeat && (sinceFlush < 200*time.Millisecond || !a.child.ProgressDirty) {
		return
	}
	a.child.LastProgressFlush = now
	a.child.ProgressDirty = false

	activeWork := make([]gen.WorkItem, len(a.child.ActiveWork))
	copy(activeWork, a.child.ActiveWork)

	progress := domain.ForkChildProgress{
		Phase:       a.child.Phase,
		SearchCount: a.child.SearchCnt,
		ReadCount:   a.child.ReadCnt,
		SummaryText: tailWindowSummary(a.child.SummaryText, childProgressSummaryTailBytes),
		ActiveWork:  activeWork,
		StepID:      a.child.StepRef,
	}

	planner := ctx.Planner()
	if planner == nil {
		return
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	panicprobe.SafeGo(ctx, "explore_progress", func() {
		defer cancel()
		if _, err := planner.Call(callCtx, a.child.AgentRef, "explore_progress", progress).Await(); err != nil {
			ctx.Logger().Warn("agent: explore_progress delivery failed", "error", err, "stepID", progress.StepID)
		}
	})
}

// childProgressSummaryTailBytes bounds the live summaryText carried by each
// explore_progress flush. The full text is never needed for the streaming
// preview (ExploreToolView shows a capped excerpt, GoalReviewView a live
// excerpt), and the authoritative full summary arrives once via
// explore_complete, which overwrites ExploreResults (handleExploreComplete).
// Sending the entire accumulated text every ~200ms made per-message size grow
// linearly and total progress traffic quadratic over the fork's lifetime.
const childProgressSummaryTailBytes = 4096

// tailWindowSummary returns the last max bytes of s, starting on a rune
// boundary, prefixed with an ellipsis marker when truncated.
func tailWindowSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return "…\n" + s[start:]
}

// workItemType maps a tool callable ID to a display type for progress UI.
func workItemType(callableID string) string {
	switch callableID {
	case "project.grep", "project.glob":
		return "search"
	case "project.read":
		return "read"
	case "project.write", "project.edit", "project.rm":
		return "write"
	default:
		return "tool"
	}
}

// workItemTarget extracts a human-readable target from a tool call for display.

func workItemTarget(callableID, input string) string {
	// Try to pull out a well-known key from the JSON input.
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(input), &m); err == nil {
		for _, key := range []string{"path", "Path", "pattern", "Pattern", "query", "Query", "url", "URL", "command", "Command", "cmd", "Cmd"} {
			if v, ok := m[key].(string); ok && v != "" {
				return v
			}
		}
	}
	// Never surface raw JSON params to the UI; fall back to the short callable name.
	if idx := strings.LastIndex(callableID, "."); idx >= 0 && idx < len(callableID)-1 {
		return callableID[idx+1:]
	}
	return callableID
}

func (a *Actor) childToolSpecs() []domain.ToolSpec {
	return []domain.ToolSpec{
		{
			Name:        "read",
			Description: "Read file contents. Supports offset/limit pagination and tail/filter.",
			InputSchema: "{\"type\":\"object\",\"properties\":{\"path\":{\"type\":\"string\",\"description\":\"Absolute or relative file path\"}},\"required\":[\"path\"]}",
			CallableID:  "project.read",
			ServiceName: "project",
			EffectKind:  string(domain.EffectNone),
		},
		{
			Name:        "glob",
			Description: "Find files by NAME (glob) or CONTENT (regex). Pass a glob like '**/*.go' to match filenames, or a regex like 'AgentRef|AgentListItem' to search file contents. Bare terms like 'TurnTail' match filenames recursively. Always returns a list of matching file paths.",
			InputSchema: "{\"type\":\"object\",\"properties\":{\"Pattern\":{\"type\":\"string\",\"description\":\"Glob for filename search (e.g. '**/*.go'), or regex for content search (e.g. 'AgentRef|AgentListItem').\"},\"Path\":{\"type\":\"string\",\"description\":\"Directory to search in. Defaults to project root.\"},\"Maxdepth\":{\"type\":\"number\",\"description\":\"Maximum directory depth. 0 = unlimited.\"}},\"required\":[\"Pattern\"]}",
			CallableID:  "project.glob",
			ServiceName: "project",
			EffectKind:  string(domain.EffectNone),
		},
		{
			Name:        "grep",
			Description: "Search file contents with a Go-compatible regular expression. Defaults to matching file paths; use output_mode=content for lines/context or count for per-file counts. An empty result means no matches, while project.grep: invalid pattern means the regex must be fixed. Use glob/path/depth to narrow the search; ignored directories and files are skipped unless no_ignore=true.",
			InputSchema: "{\"type\":\"object\",\"properties\":{\"Pattern\":{\"type\":\"string\",\"description\":\"Regular expression pattern to search for.\"},\"Path\":{\"type\":\"string\",\"description\":\"Directory or file to search in. Defaults to the project's primary root directory.\"},\"Output_mode\":{\"type\":\"string\",\"description\":\"Result format: files (default), content, count.\",\"enum\":[\"files\",\"content\",\"count\"]},\"Glob\":{\"type\":\"string\",\"description\":\"Comma-separated glob patterns to filter files.\"},\"Head_limit\":{\"type\":\"number\",\"description\":\"Max matches. Default 250.\"},\"Context\":{\"type\":\"number\",\"description\":\"Lines of context before and after each match.\"},\"Before_context\":{\"type\":\"number\",\"description\":\"Lines of context before each match.\"},\"After_context\":{\"type\":\"number\",\"description\":\"Lines of context after each match.\"},\"Ignore_case\":{\"type\":\"boolean\",\"description\":\"Case-insensitive matching.\"},\"Multiline\":{\"type\":\"boolean\",\"description\":\"Enable multiline regexp mode.\"},\"Depth\":{\"type\":\"number\",\"description\":\"Directory search depth. 0 (default) = recursive/unlimited; 1 = top directory only; 2 = two levels, etc.\"}},\"required\":[\"Pattern\"]}",
			CallableID:  "project.grep",
			ServiceName: "project",
			EffectKind:  string(domain.EffectNone),
		},
	}
}

// runExploreSummary executes a streaming LLM dispatch after the exploration
// turn completes, producing a clean summary that separates findings from
// intermediate reasoning. The summary text is streamed to the parent via the
// existing progress pipeline (Phase="summarizing").
func (a *Actor) runExploreSummary(ctx actor.Context, turnOutput string) (string, error) {
	// Collect the exploration findings as input for the summary prompt.
	findings := turnOutput
	if findings == "" {
		findings = a.summarizeChildFromSteps(a.steps)
	}
	if findings == "" {
		return "", nil
	}

	// Extract the original task from the first user step.
	task := ""
	for _, s := range a.steps {
		if s.Role == domain.ChatRoleUser {
			for _, cb := range s.Content {
				if cb.Type == domain.ContentBlockText && cb.Text != "" {
					task = cb.Text
					break
				}
			}
			break
		}
	}

	tmpl := agentkit.ExploreSummaryExplorer
	if a.agentKind != "explorer" {
		tmpl = agentkit.ExploreSummaryGeneral
	}
	if task == "" {
		tmpl = strings.ReplaceAll(tmpl, "\n\nOriginal task:\n{{TASK}}", "")
	}
	promptText := strings.ReplaceAll(tmpl, "{{TASK}}", task)
	promptText = strings.ReplaceAll(promptText, "{{FINDINGS}}", findings)

	var promptParts []string
	promptParts = append(promptParts, promptText)

	planner := ctx.Planner()
	aggRef, targetUnit, err := a.resolveTarget(ctx, a.primary)
	if err != nil || planner == nil {
		return "", fmt.Errorf("no planner or model target for summary dispatch: %w", err)
	}
	unit := targetUnit
	if unit.Model == "" {
		unit = a.status.Unit
	}

	req := domain.SendSessionMessageReq{
		SessionID: ctx.NewID().String(),
		AgentID:   a.actorID,
		SlotKind:  "summary",
		Unit:      &unit,
		Messages: []domain.ChatMessage{{
			ID:      ctx.NewID().String(),
			Role:    domain.ChatRoleUser,
			Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: strings.Join(promptParts, "")}},
		}},
	}

	node, err := planner.Plan(aggRef, "aiaggregator.dispatch", req)
	if err != nil {
		return "", fmt.Errorf("summary plan: %w", err)
	}

	startCtx, cancel := context.WithCancel(ctx.Lifecycle())
	defer cancel()

	if err := node.Start(startCtx); err != nil {
		_ = node.Stop()
		_ = ctx.Destroy(node.Ref())
		return "", fmt.Errorf("summary start: %w", err)
	}

	recvCh := plan.RecvChan(startCtx, node)

	a.child.Phase = "summarizing"
	a.child.ProgressDirty = true

	var summaryText strings.Builder

loop:
	for {
		select {
		case r, ok := <-recvCh:
			if !ok || errors.Is(r.Err, io.EOF) {
				break loop
			}
			if r.Err != nil {
				break loop
			}
			chunk, err := decodeChunk(r.Value)
			if err != nil {
				break loop
			}
			switch chunk.Kind {
			case domain.AggregatorChunkText:
				summaryText.WriteString(chunk.Text)
				a.child.SummaryText = summaryText.String()
				a.child.ProgressDirty = true
				a.maybeFlushChildProgress(ctx)
			case domain.AggregatorChunkUsage:
				if chunk.Usage != nil {
					a.child.InputTokens += int32(chunk.Usage.InputTokens)
					a.child.OutputTokens += int32(chunk.Usage.OutputTokens)
					if chunk.Usage.CacheCreationInputTokens > 0 {
						a.child.CacheCreationInputTokens += int32(chunk.Usage.CacheCreationInputTokens)
					}
					if chunk.Usage.CacheReadInputTokens > 0 {
						a.child.CacheReadInputTokens += int32(chunk.Usage.CacheReadInputTokens)
					}
				}
			}
		case <-ctx.Lifecycle().Done():
			break loop
		}
	}

	_ = node.Stop()
	_ = ctx.Destroy(node.Ref())

	return summaryText.String(), nil
}
